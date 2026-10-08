package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// usageRemoteStand serves the routes a usage pull touches: the model list,
// a one-chunk turn stream, and the usage route whose answers the test
// scripts in order.
type usageRemoteStand struct {
	srv     *httptest.Server
	calls   atomic.Int32
	refresh atomic.Int32
	answers chan string
	// queries holds the raw query of every usage read, in arrival order.
	queryMu sync.Mutex
	queries []string
}

// seen returns the raw queries of the usage reads so far.
func (s *usageRemoteStand) seen() []string {
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	return append([]string(nil), s.queries...)
}

func newUsageRemoteStand(t *testing.T) *usageRemoteStand {
	t.Helper()
	s := &usageRemoteStand{answers: make(chan string, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"agent","owned_by":"coddy"},
			{"id":"neuraldeep/qwen","default":true,"owned_by":"neuraldeep"},
			{"id":"stub/model","owned_by":"stub"}]}`))
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
			"data: [DONE]\n\n"))
	})
	mux.HandleFunc("GET /coddy/providers/{name}/usage", func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.queryMu.Lock()
		s.queries = append(s.queries, r.URL.Path+"?"+r.URL.RawQuery)
		s.queryMu.Unlock()
		if r.URL.Query().Get("refresh") == "1" {
			s.refresh.Add(1)
		}
		select {
		case body := <-s.answers:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func usageAnswer(used int, pending bool, in int) string {
	body, _ := json.Marshal(map[string]interface{}{
		"ok": true,
		"usage": map[string]interface{}{
			"sessionUpdate": "provider_usage", "provider": "neuraldeep", "providerType": "neuraldeep", "plan": "pro",
			"windows":        []map[string]interface{}{{"id": "session", "label": "3h", "used": used, "limit": 15000, "usedPercent": float64(used) / 150, "resetInSec": 700}},
			"refreshPending": pending, "refreshInSec": in,
		},
	})
	return string(body)
}

func usageUpdates(sender *collectSender) []acp.ProviderUsageUpdate {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	var out []acp.ProviderUsageUpdate
	for _, u := range sender.updates {
		if pu, ok := u.(acp.ProviderUsageUpdate); ok {
			out = append(out, pu)
		}
	}
	return out
}

func TestRemoteUsagePullsAtReadyAndAfterATurnWithoutATimerOfItsOwn(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- usageAnswer(407, false, 0) // ready
	stand.answers <- usageAnswer(407, true, 9)  // after the turn: deferred by the server
	stand.answers <- usageAnswer(1200, false, 0)
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	res, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h.HandleSessionReady(res.SessionID)
	h.WaitUsage(2 * time.Second)
	if got := usageUpdates(sender); len(got) != 1 || *got[0].Windows[0].Used != 407 {
		t.Fatalf("ready pull = %+v", got)
	}
	if _, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}, sender, nil); err != nil {
		t.Fatal(err)
	}
	h.WaitUsage(2 * time.Second)
	got := usageUpdates(sender)
	if len(got) != 2 || !got[1].RefreshPending || got[1].RefreshInSec != 9 || stand.refresh.Load() != 1 {
		t.Fatalf("post-turn pull = %+v (refresh reads %d)", got, stand.refresh.Load())
	}
	// The console's own timer reads the cache when refreshInSec says so;
	// this client arms nothing, so no third pull happens by itself.
	time.Sleep(150 * time.Millisecond)
	if stand.calls.Load() != 2 || len(usageUpdates(sender)) != 2 {
		t.Fatalf("calls = %d updates = %d, want no follow-up from the client", stand.calls.Load(), len(usageUpdates(sender)))
	}
	// A cache read from the console lands the fresh snapshot.
	u, err := h.ProviderUsageForSession(context.Background(), res.SessionID, "neuraldeep", false)
	if err != nil || *u.Windows[0].Used != 1200 || u.RefreshPending {
		t.Fatalf("console read = %+v err=%v", u, err)
	}
}

func TestRemoteUsageCachesUnsupportedUntilARefresh(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- `{"ok":false,"unsupported":true,"provider":"stub","providerType":"openai"}`
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	u, err := h.ProviderUsage(context.Background(), "stub", false)
	if err != nil || u == nil || !u.Unsupported || u.ProviderType != "openai" || stand.calls.Load() != 1 {
		t.Fatalf("first: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	if u, err = h.ProviderUsage(context.Background(), "stub", false); err != nil || !u.Unsupported || u.ProviderType != "openai" || stand.calls.Load() != 1 {
		t.Fatalf("cached: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	stand.answers <- usageAnswer(5, false, 0)
	if u, err = h.ProviderUsage(context.Background(), "stub", true); err != nil || u.Unsupported || stand.calls.Load() != 2 {
		t.Fatalf("refresh must ask again: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	stand.answers <- usageAnswer(6, false, 0)
	if u, err = h.ProviderUsage(context.Background(), "stub", false); err != nil || u.Unsupported || stand.calls.Load() != 3 {
		t.Fatalf("a supported answer clears the mark: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	// The mark expires on its own.
	stand.answers <- `{"ok":false,"unsupported":true,"provider":"stub","providerType":"openai"}`
	if _, err = h.ProviderUsage(context.Background(), "stub", true); err != nil {
		t.Fatal(err)
	}
	h.usageMu.Lock()
	h.usageUnsupported["stub"] = usageUnsupportedMark{until: time.Now().Add(-time.Second), providerType: "openai"}
	h.usageMu.Unlock()
	stand.answers <- usageAnswer(7, false, 0)
	if u, err = h.ProviderUsage(context.Background(), "stub", false); err != nil || u.Unsupported || stand.calls.Load() != 5 {
		t.Fatalf("an expired mark asks again: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
}

func TestRemotePullSkipsAForgottenSession(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- usageAnswer(407, false, 0)
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	res, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h.ForgetLiveSession(res.SessionID)
	h.pullProviderUsage(context.Background(), res.SessionID, false)
	if stand.calls.Load() != 0 || len(usageUpdates(sender)) != 0 {
		t.Fatalf("a forgotten session pulled: calls=%d updates=%d", stand.calls.Load(), len(usageUpdates(sender)))
	}
	h.mu.Lock()
	_, resurrected := h.sessions[res.SessionID]
	h.mu.Unlock()
	if resurrected {
		t.Fatalf("the pull recreated the forgotten session")
	}
}

func TestRemoteCloseDropsAPullAlreadyInFlight(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"neuraldeep/qwen","default":true,"owned_by":"neuraldeep"}]}`))
	})
	mux.HandleFunc("GET /coddy/providers/{name}/usage", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(usageAnswer(407, false, 0)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	res, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h.HandleSessionReady(res.SessionID)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the pull never reached the server")
	}
	h.Close()
	close(gate)
	h.WaitUsage(2 * time.Second)
	if got := usageUpdates(sender); len(got) != 0 {
		t.Fatalf("a pull that finished after Close delivered %+v", got)
	}
	// Nothing starts after Close either.
	h.pullProviderUsageAsync(res.SessionID, true)
	h.WaitUsage(time.Second)
	if got := usageUpdates(sender); len(got) != 0 {
		t.Fatalf("a pull after Close delivered %+v", got)
	}
}

// A row whose usage limits panel is switched off on the server answers
// unsupported with the disabled flag; the mark keeps the flag so the
// console's /usage can name the switch without another round trip.
func TestRemoteUsageKeepsTheDisabledFlagOfASwitchedOffPanel(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- `{"ok":false,"unsupported":true,"disabled":true,"provider":"neuraldeep","providerType":"neuraldeep"}`
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	u, err := h.ProviderUsage(context.Background(), "neuraldeep", false)
	if err != nil || u == nil || !u.Unsupported || !u.Disabled || u.ProviderType != "neuraldeep" || stand.calls.Load() != 1 {
		t.Fatalf("first: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	if u, err = h.ProviderUsage(context.Background(), "neuraldeep", false); err != nil || !u.Unsupported || !u.Disabled || stand.calls.Load() != 1 {
		t.Fatalf("cached: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	// The pull after a turn forwards the answer too, so a remote console
	// takes a stale line down when the server switched the panel off.
	stand.answers <- `{"ok":false,"unsupported":true,"disabled":true,"provider":"neuraldeep","providerType":"neuraldeep"}`
	sender := &collectSender{}
	h.SetServer(sender)
	res, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}, sender, nil); err != nil {
		t.Fatal(err)
	}
	h.WaitUsage(2 * time.Second)
	if got := usageUpdates(sender); len(got) != 1 || !got[0].Unsupported || !got[0].Disabled || got[0].Provider != "neuraldeep" {
		t.Fatalf("post-turn pull must forward the disabled answer: %+v", got)
	}
}

// A model of a remote Coddy's own provider row is read per alias: the console
// hands the selector, and the read asks the server for the alias.
func TestRemoteUsageReadsTheAliasOfASelector(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- `{"ok":true,"usage":{"sessionUpdate":"provider_usage","provider":"lab","providerType":"coddy","model":"terra","windows":[{"id":"session","label":"3h","usedPercent":62}]}}`
	stand.answers <- `{"ok":true,"usage":{"sessionUpdate":"provider_usage","provider":"lab","providerType":"coddy","model":"terra","windows":[{"id":"session","label":"3h","usedPercent":63}]}}`
	stand.answers <- usageAnswer(5, false, 0)
	stand.answers <- usageAnswer(6, false, 0)
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	u, err := h.ProviderUsage(context.Background(), "lab/terra", false)
	if err != nil || u == nil || u.Model != "terra" || u.Provider != "lab" || u.Windows[0].UsedPercent != 62 {
		t.Fatalf("selector read: err=%v u=%+v", err, u)
	}
	if _, err := h.ProviderUsageForSession(context.Background(), "s1", "lab/terra", true); err != nil {
		t.Fatal(err)
	}
	// A bare row name asks for no alias, and so does a row that is not a coddy
	// one only by the answer: the server ignores the parameter for it.
	if _, err := h.ProviderUsage(context.Background(), "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ProviderUsage(context.Background(), "neuraldeep", true); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/coddy/providers/lab/usage?model=terra",
		"/coddy/providers/lab/usage?model=terra&refresh=1",
		"/coddy/providers/neuraldeep/usage?",
		"/coddy/providers/neuraldeep/usage?refresh=1",
	}
	if got := stand.seen(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reads = %q, want %q", got, want)
	}
}

// An alias with characters a query cannot carry is escaped.
func TestRemoteUsageEscapesTheAliasInTheQuery(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- usageAnswer(5, false, 0)
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ProviderUsage(context.Background(), "lab/org/qwen 3&x=1", false); err != nil {
		t.Fatal(err)
	}
	if got := stand.seen(); len(got) != 1 || got[0] != "/coddy/providers/lab/usage?model=org%2Fqwen+3%26x%3D1" {
		t.Fatalf("reads = %q", got)
	}
}

// The server's own memory of an alias that has no usage is the only one: the
// console records nothing for a coddy row, so another alias of the same row is
// asked, and the alias itself is asked again at the next read.
func TestRemoteUsageRecordsNoUnsupportedMarkForACoddyRow(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- `{"ok":false,"unsupported":true,"provider":"lab","providerType":"coddy","model":"terra"}`
	stand.answers <- `{"ok":true,"usage":{"sessionUpdate":"provider_usage","provider":"lab","providerType":"coddy","model":"luna","windows":[{"id":"session","label":"3h","usedPercent":9}]}}`
	stand.answers <- `{"ok":false,"unsupported":true,"provider":"lab","providerType":"coddy"}`
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	u, err := h.ProviderUsage(context.Background(), "lab/terra", false)
	if err != nil || u == nil || !u.Unsupported || u.ProviderType != "coddy" || u.Model != "terra" || u.Provider != "lab" {
		t.Fatalf("first: err=%v u=%+v", err, u)
	}
	h.usageMu.Lock()
	marks := len(h.usageUnsupported)
	h.usageMu.Unlock()
	if marks != 0 {
		t.Fatalf("the console recorded %d unsupported marks for a coddy row", marks)
	}
	u, err = h.ProviderUsage(context.Background(), "lab/luna", false)
	if err != nil || u == nil || u.Unsupported || u.Model != "luna" {
		t.Fatalf("another alias of the row: err=%v u=%+v", err, u)
	}
	// An unsupported answer that names no alias still carries the one asked for.
	u, err = h.ProviderUsage(context.Background(), "lab/sol", false)
	if err != nil || u == nil || !u.Unsupported || u.Model != "sol" {
		t.Fatalf("answer without a model: err=%v u=%+v", err, u)
	}
	if stand.calls.Load() != 3 {
		t.Fatalf("reads = %d, want one per call", stand.calls.Load())
	}
}

// A usage answer of a coddy row that predates the alias on the wire is the
// alias asked for.
func TestRemoteUsageNamesTheAliasWhenTheServerLeftItOut(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- `{"ok":true,"usage":{"sessionUpdate":"provider_usage","provider":"lab","providerType":"coddy","windows":[{"id":"session","label":"3h","usedPercent":62}]}}`
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	u, err := h.ProviderUsage(context.Background(), "lab/terra", false)
	if err != nil || u == nil || u.Model != "terra" {
		t.Fatalf("err=%v u=%+v", err, u)
	}
}

// For every other type the mark stays per provider row, whatever alias part the
// selector carries.
func TestRemoteUsageKeepsItsUnsupportedMarkPerRowForSelectors(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- `{"ok":false,"unsupported":true,"provider":"stub","providerType":"openai"}`
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if u, err := h.ProviderUsage(context.Background(), "stub/model-a", false); err != nil || !u.Unsupported || u.Model != "" {
		t.Fatalf("first: err=%v u=%+v", err, u)
	}
	if u, err := h.ProviderUsage(context.Background(), "stub/model-b", false); err != nil || !u.Unsupported || u.ProviderType != "openai" || stand.calls.Load() != 1 {
		t.Fatalf("a second model of a row with no source asked again: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
}

func TestRemoteUsageRefusesAnEmptySubject(t *testing.T) {
	stand := newUsageRemoteStand(t)
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "  ", "/terra"} {
		if _, err := h.ProviderUsage(context.Background(), name, false); err == nil {
			t.Fatalf("ProviderUsage(%q) did not fail", name)
		}
	}
	if stand.calls.Load() != 0 {
		t.Fatalf("a read with no provider reached the server (%d)", stand.calls.Load())
	}
}

// The pull a remote console makes at session ready and after a turn reads the
// subject of the session's model: the alias goes with the row.
func TestRemotePullReadsTheSubjectOfTheSessionsModel(t *testing.T) {
	stand := newUsageRemoteStand(t)
	stand.answers <- usageAnswer(407, false, 0)
	h, err := NewHandler(Options{BaseURL: stand.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	res, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h.HandleSessionReady(res.SessionID)
	h.WaitUsage(2 * time.Second)
	got := stand.seen()
	if len(got) != 1 || got[0] != "/coddy/providers/neuraldeep/usage?model=qwen" {
		t.Fatalf("the ready pull read %q, want the row and the model of the default model", got)
	}
}
