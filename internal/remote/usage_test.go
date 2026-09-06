package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func newUsageRemoteStand(t *testing.T) *usageRemoteStand {
	t.Helper()
	s := &usageRemoteStand{answers: make(chan string, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","default_agent_model":"neuraldeep/qwen","data":[
			{"id":"agent","owned_by":"coddy"},
			{"id":"neuraldeep/qwen","owned_by":"neuraldeep"},
			{"id":"stub/model","owned_by":"stub"}]}`))
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
			"data: [DONE]\n\n"))
	})
	mux.HandleFunc("GET /coddy/providers/{name}/usage", func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
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

func TestRemoteUsagePullsAtReadyAfterATurnAndFollowsUpOnce(t *testing.T) {
	prev := usageFollowUpGrace
	usageFollowUpGrace = 50 * time.Millisecond
	defer func() { usageFollowUpGrace = prev }()

	stand := newUsageRemoteStand(t)
	stand.answers <- usageAnswer(407, false, 0)  // ready
	stand.answers <- usageAnswer(407, true, 0)   // after the turn: deferred
	stand.answers <- usageAnswer(1200, false, 0) // the follow-up
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
	if got := usageUpdates(sender); len(got) != 2 || !got[1].RefreshPending || stand.refresh.Load() != 1 {
		t.Fatalf("post-turn pull = %+v (refresh reads %d)", got, stand.refresh.Load())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := usageUpdates(sender); len(got) == 3 {
			if *got[2].Windows[0].Used != 1200 || got[2].RefreshPending {
				t.Fatalf("follow-up = %+v", got[2])
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := usageUpdates(sender); len(got) != 3 {
		t.Fatalf("follow-up never arrived: %d updates", len(got))
	}
	time.Sleep(3 * usageFollowUpGrace)
	if stand.calls.Load() != 3 {
		t.Fatalf("calls = %d, want exactly one follow-up", stand.calls.Load())
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
	if err != nil || u == nil || !u.Unsupported || stand.calls.Load() != 1 {
		t.Fatalf("first: err=%v u=%+v calls=%d", err, u, stand.calls.Load())
	}
	if u, err = h.ProviderUsage(context.Background(), "stub", false); err != nil || !u.Unsupported || stand.calls.Load() != 1 {
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
}

func TestRemoteCloseStopsThePendingFollowUp(t *testing.T) {
	prev := usageFollowUpGrace
	usageFollowUpGrace = 50 * time.Millisecond
	defer func() { usageFollowUpGrace = prev }()

	stand := newUsageRemoteStand(t)
	stand.answers <- usageAnswer(407, true, 0) // ready: deferred, arms the follow-up
	stand.answers <- usageAnswer(900, false, 0)
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
	h.Close()
	time.Sleep(4 * usageFollowUpGrace)
	if stand.calls.Load() != 1 || len(usageUpdates(sender)) != 1 {
		t.Fatalf("a closed console must not pull again: calls=%d updates=%d", stand.calls.Load(), len(usageUpdates(sender)))
	}
	// A closed handler pulls nothing and arms nothing.
	h.pullProviderUsage(context.Background(), res.SessionID, false)
	h.pullProviderUsageAsync(res.SessionID, true)
	h.WaitUsage(time.Second)
	time.Sleep(4 * usageFollowUpGrace)
	if stand.calls.Load() != 1 {
		t.Fatalf("calls after close = %d, want none", stand.calls.Load())
	}
}
