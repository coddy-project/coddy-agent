//go:build http

package httpserver

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// readEventFrames reads SSE frames until one containing want has been read whole - up to
// its blank-line terminator, so the caller sees the frame's data line too.
func readEventFrames(t *testing.T, body *bufio.Reader, want string) string {
	t.Helper()
	var seen strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := body.ReadString('\n')
		if err != nil {
			t.Fatalf("read events (seen %q): %v", seen.String(), err)
		}
		seen.WriteString(line)
		if strings.Contains(seen.String(), want) && strings.HasSuffix(seen.String(), "\n\n") {
			return seen.String()
		}
	}
	t.Fatalf("timed out waiting for %q, seen %q", want, seen.String())
	return ""
}

func subscribeEvents(t *testing.T, ts *httptest.Server, query string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/coddy/events"+query, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("events status %d", res.StatusCode)
	}
	return bufio.NewReader(res.Body), func() {
		cancel()
		res.Body.Close()
	}
}

// An idle client must be told a turn began in a session it is not driving; that is the
// whole reason the route exists.
func TestCoddyEventsStreamAnnouncesTurnLifecycle(t *testing.T) {
	turn := &watchedTurn{started: make(chan struct{}), release: make(chan struct{})}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = srv.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
		})
	}()
	defer wg.Wait()

	<-turn.started
	started := readEventFrames(t, body, "turn_started")
	if !strings.Contains(started, sn.SessionID) {
		t.Fatalf("turn_started did not name the session: %s", started)
	}

	close(turn.release)
	readEventFrames(t, body, "turn_ended")
}

// A client that connects while a turn is already running would otherwise stay blind
// until that turn ended.
func TestCoddyEventsStreamSnapshotsRunningTurns(t *testing.T) {
	turn := &watchedTurn{started: make(chan struct{}), release: make(chan struct{})}
	_, srv, _ := testHTTPServerPersistWithRunner(t, turn.runner())
	sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = srv.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
		})
	}()
	defer wg.Wait()
	defer close(turn.release)
	<-turn.started

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()

	snapshot := readEventFrames(t, body, "event: ready")
	if !strings.Contains(snapshot, "turn_started") || !strings.Contains(snapshot, sn.SessionID) {
		t.Fatalf("connect-time snapshot missed the running turn: %s", snapshot)
	}
}

// EventSource cannot set an Authorization header, so this route accepts the token the
// same way the composer stream does.
func TestCoddyEventsStreamAcceptsAccessTokenQuery(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.extraAuthTokens = []string{"secret-token"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := ts.Client().Get(ts.URL + "/coddy/events")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d without a token, want 401", res.StatusCode)
	}

	body, closeEvents := subscribeEvents(t, ts, "?access_token=secret-token")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")
}

func TestTurnEventFrameShape(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	frame := string(turnEventFrame(session.TurnEvent{
		SessionID: "sess_x",
		Phase:     session.TurnPhaseEnded,
		At:        at,
	}))
	if !strings.HasPrefix(frame, "event: turn_ended\ndata: ") || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("frame %q is not a well-formed SSE frame", frame)
	}
	for _, want := range []string{`"object":"coddy.turn_event"`, `"sessionId":"sess_x"`, `"phase":"ended"`, "2026-08-10T12:00:00Z"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame %q missing %s", frame, want)
		}
	}
}

func TestConfigReloadedFrameShape(t *testing.T) {
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	frame := string(configReloadedFrame(at))
	if !strings.HasPrefix(frame, "event: config_reloaded\ndata: ") || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("frame %q is not a well-formed SSE frame", frame)
	}
	for _, want := range []string{`"object":"coddy.config_reloaded"`, "2026-09-09T12:00:00Z"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame %q missing %s", frame, want)
		}
	}
	// The frame carries no copy of what changed on purpose: a client re-reads
	// GET /v1/models and the slash-command list, which stay the single source.
	if strings.Contains(frame, `"models"`) {
		t.Fatalf("frame %q duplicates the model list", frame)
	}
}

// ReplaceConfig is the choke point every reload path goes through, so it is also where
// the announcement belongs - and it must announce a swap that actually happened, once.
// The hub is read directly here because publish is synchronous: by the time
// ReplaceConfig returns, the frame is already in the subscriber's buffer, so an absent
// frame is an absent announcement rather than a race.
func TestReplaceConfigAnnouncesEverySwapAndNothingElse(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	frames, unsubscribe := srv.events.subscribe()
	defer unsubscribe()

	// A nil configuration is not a reload; announcing it would send every client to
	// re-read a configuration that never moved.
	before := srv.activeCfg()
	srv.ReplaceConfig(nil)
	if srv.activeCfg() != before {
		t.Fatal("a nil configuration replaced the live one")
	}
	select {
	case f := <-frames:
		t.Fatalf("a nil configuration was announced: %s", f)
	default:
	}

	next := *before
	next.Agent.MaxTurns = 41
	srv.ReplaceConfig(&next)
	if srv.activeCfg().Agent.MaxTurns != 41 {
		t.Fatalf("live config was not swapped: max_turns %d", srv.activeCfg().Agent.MaxTurns)
	}
	select {
	case f := <-frames:
		if !strings.HasPrefix(string(f), "event: config_reloaded\n") {
			t.Fatalf("first frame after the swap = %s", f)
		}
	default:
		t.Fatal("the swap was not announced")
	}
	select {
	case f := <-frames:
		t.Fatalf("one swap produced a second frame: %s", f)
	default:
	}
}

// The announcement must follow the swap: a client re-reads the model list the moment it
// sees the event, and reading the outgoing configuration is the bug this fixes.
func TestConfigReloadedAnnouncedAfterTheSwapIsVisible(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	next := *srv.activeCfg()
	next.Models = append(append([]config.ModelEntry{}, next.Models...), config.ModelEntry{Model: "rpa/qwen3.6-35b-a3b"})
	srv.ReplaceConfig(&next)

	readEventFrames(t, body, "event: config_reloaded")
	res, err := ts.Client().Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := ioReadAllClose(res.Body)
	if !strings.Contains(string(list), "rpa/qwen3.6-35b-a3b") {
		t.Fatalf("model list read after the event is stale: %s", string(list))
	}
}

// The slash-command list is cached per workspace, and its cache key stats only the
// configured skill directories. An edit inside a skill - or a reload that moves
// skills.managed_dir, which is not among them at all - is invisible to that key, so the
// cache would keep serving the old list and a client re-reading on the reload event
// would be handed exactly the staleness the event exists to end. ReplaceConfig drops
// the cache before it announces.
func TestConfigReloadServesFreshSlashCommands(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	skillsDir := filepath.Join(root, "skills")
	alpha := filepath.Join(skillsDir, "alpha")
	writeSkill := func(description string) {
		t.Helper()
		if err := os.MkdirAll(alpha, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: alpha\ndescription: " + description + "\n---\n"
		if err := os.WriteFile(filepath.Join(alpha, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill("the old description")

	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: root},
		Skills: config.Skills{Dirs: []string{skillsDir}},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	slashList := func() string {
		t.Helper()
		res, err := ts.Client().Get(ts.URL + "/coddy/slash-commands?page=1&page_size=200")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ioReadAllClose(res.Body)
		return string(b)
	}

	// Prime the cache, then edit the skill in place: the configured directory keeps its
	// size and mtime, so the cache key does not move.
	if got := slashList(); !strings.Contains(got, "the old description") {
		t.Fatalf("first read did not find the skill on disk: %s", got)
	}
	writeSkill("the new description")

	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	next := *cfg
	srv.ReplaceConfig(&next)
	readEventFrames(t, body, "event: config_reloaded")

	if got := slashList(); !strings.Contains(got, "the new description") {
		t.Fatalf("slash list read on the reload event is stale: %s", got)
	}
}
