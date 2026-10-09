package session

// The first event of a turn (issue #357): a surface hears that the turn was
// taken while the turn still brings in its MCP servers and waits for its
// model's context window, so a browser that sent the first message of a chat
// is not left looking at nothing for the seconds those take.

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// progressRecorder is a turn sender that keeps the turn_progress updates it
// was handed, in order.
type progressRecorder struct {
	mcpTestSender
	mu      sync.Mutex
	updates []acp.TurnProgressUpdate
}

func (r *progressRecorder) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.TurnProgressUpdate); ok {
		r.mu.Lock()
		r.updates = append(r.updates, u)
		r.mu.Unlock()
	}
	return nil
}

func (r *progressRecorder) snapshot() []acp.TurnProgressUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]acp.TurnProgressUpdate(nil), r.updates...)
}

// TestTurnAnnouncesItselfBeforeItsServersAnswer: while the session's MCP
// server is still in its handshake, the turn has already sent a turn_progress
// in the preparing phase, from the clock it was admitted at.
func TestTurnAnnouncesItselfBeforeItsServersAnswer(t *testing.T) {
	entered := make(chan struct{}, 1)
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		entered <- struct{}{}
		return string(acp.StopReasonEndTurn), nil
	}
	f := newBackgroundFixture(t, runner, nil)
	rec := &progressRecorder{}
	turn := make(chan error, 1)
	go func() {
		_, err := f.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
			SessionID: f.st.GetID(), Prompt: []acp.ContentBlock{{Type: "text", Text: "hi"}},
		}, rec, nil)
		turn <- err
	}()

	if !waitUntil(t, 5*time.Second, func() bool { return len(rec.snapshot()) > 0 }) {
		t.Fatal("no turn_progress while the turn waited for its MCP server")
	}
	select {
	case <-entered:
		t.Fatal("the turn ran before its server answered")
	default:
	}
	first := rec.snapshot()[0]
	if first.Phase != acp.TurnPhasePreparing {
		t.Fatalf("first turn_progress phase = %q, want %q", first.Phase, acp.TurnPhasePreparing)
	}
	started, ok := f.mgr.TurnStartedAt(f.st.GetID())
	if !ok || first.StartedAt != started.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("first turn_progress startedAt = %q, want the admission %v", first.StartedAt, started)
	}

	f.releaseServer()
	if err := <-turn; err != nil {
		t.Fatal(err)
	}
}

// TestNewSessionsConnectInTheBackgroundAndStoredOnesWait: the mode coddy serve
// runs in. A new session returns before its servers answer and dials them in
// the background; a stored session loaded to be read starts nothing until its
// first turn, so browsing History does not leave a server per chat looked at.
func TestNewSessionsConnectInTheBackgroundAndStoredOnesWait(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	writeHomeMCP(t, home, reloadTestMCPServer("alpha"))
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) { return "", nil }
	mgr := NewManager(cfg, mcpTurnSender{}, runner, slog.Default(), cwd, &FileStore{Root: filepath.Join(home, "sessions")})
	mgr.SetNewSessionsBackgroundMCP(true)
	ctx := context.Background()

	created, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	fresh := mgr.SessionByID(created.SessionID)
	if _, recorded := fresh.MCPConnectSnapshot(); !recorded {
		t.Fatal("a new session did not connect its servers in the background")
	}
	if !waitUntil(t, 10*time.Second, func() bool { s, _ := fresh.MCPConnectSnapshot(); return s.Done }) {
		t.Fatal("the background dial of the new session never settled")
	}
	mgr.ForgetLiveSession(created.SessionID)

	stored, err := mgr.EnsureHTTPSession(ctx, created.SessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.ForgetLiveSession(created.SessionID) })
	if !stored.configuredMCPDeferred() {
		t.Fatal("loading a stored session did not leave its servers for its first turn")
	}
	if _, recorded := stored.MCPConnectSnapshot(); recorded {
		t.Fatal("loading a stored session started a background dial")
	}
	if clients := stored.GetMCPClients(); len(clients) != 0 {
		t.Fatalf("loading a stored session started %d MCP servers", len(clients))
	}
}
