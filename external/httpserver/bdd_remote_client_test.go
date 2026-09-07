//go:build http

package httpserver

// Godog harness for features/remote_client.feature: the internal/remote client
// drives a real, bearer-protected coddy HTTP server end to end. A scripted
// runner on the server side keeps scenarios deterministic and LLM-free.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// recordingClientSender captures what the remote client forwards to its
// surface and answers permission prompts with a scripted option.
type recordingClientSender struct {
	mu          sync.Mutex
	updates     []interface{}
	permAnswer  string
	permissions []acp.PermissionRequestParams
}

func (r *recordingClientSender) SendSessionUpdate(_ string, update interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, update)
	return nil
}

func (r *recordingClientSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	r.mu.Lock()
	r.permissions = append(r.permissions, p)
	answer := r.permAnswer
	r.mu.Unlock()
	if answer == "" {
		answer = "reject"
	}
	outcome := "allow"
	if answer == "reject" {
		outcome = "cancelled"
	}
	return &acp.PermissionResult{Outcome: outcome, OptionID: answer}, nil
}

func (r *recordingClientSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (r *recordingClientSender) snapshot() []interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]interface{}, len(r.updates))
	copy(out, r.updates)
	return out
}

func (r *recordingClientSender) streamedText() string {
	var b strings.Builder
	for _, u := range r.snapshot() {
		if chunk, ok := u.(acp.MessageChunkUpdate); ok {
			if chunk.SessionUpdate == acp.UpdateTypeAgentMessageChunk && chunk.Content.Type == acp.ContentTypeText {
				b.WriteString(chunk.Content.Text)
			}
		}
	}
	return b.String()
}

// remoteClientState wires one scripted server plus one client under test.
type remoteClientState struct {
	root     string
	sessRoot string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	token    string

	// subagentPermission makes the runner behave like a parent whose child
	// asks for permission through the relay.
	subagentPermission bool
	// runner script for the next turns
	replyText      string
	askPermission  bool
	recordToolCall bool
	stopReason     string

	client    *remote.Handler
	sender    *recordingClientSender
	sessionID string
	firstID   string
	lastStop  string
	promptErr error

	replaySender *recordingClientSender
	loadResult   *acp.SessionLoadResult
	listResult   *acp.SessionListResult
}

func (s *remoteClientState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-remote-client-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.replyText = ""
	s.askPermission = false
	s.subagentPermission = false
	s.recordToolCall = false
	s.stopReason = ""
	s.client = nil
	s.sender = nil
	s.sessionID = ""
	s.firstID = ""
	s.lastStop = ""
	s.promptErr = nil
	s.replaySender = nil
	s.loadResult = nil
	s.listResult = nil
	return nil
}

func (s *remoteClientState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *remoteClientState) startServer(token string) error {
	s.token = token
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		text := strings.TrimSpace(promptBlocksText(prompt))
		if text != "" {
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		}
		if s.subagentPermission {
			// What the parent's turn emits while a child runs: the spawn_agent
			// call, then the child's own prompt forwarded by the permission
			// relay under the parent's session id, stamped with the child's
			// effective mode (the stamp never travels; the bridge decides).
			_ = snd.SendSessionUpdate(st.ID, acp.ToolCallUpdate{
				SessionUpdate: acp.UpdateTypeToolCall,
				ToolCallID:    "call_spawn",
				Title:         "spawn_agent",
				Kind:          "other",
				Status:        "pending",
			})
			_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    "call_spawn",
				Status:        "in_progress",
				Content: []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: `{"agent":"explore","prompt":"read the marker"}`}},
				},
			})
			res, err := snd.RequestPermission(ctx, acp.PermissionRequestParams{
				SessionID: st.ID,
				ToolCall: acp.PermissionToolCall{
					ToolCallID: "child-perm-1",
					Title:      "[subagent explore] Run: run_command",
					Kind:       "run_command",
					Status:     "pending",
				},
				Options: []acp.PermissionOption{
					{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
					{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
				},
				EffectivePermissionMode: config.PermModeAsk,
			})
			if err != nil {
				return string(acp.StopReasonCancelled), err
			}
			if res == nil || res.OptionID != "allow" {
				return string(acp.StopReasonCancelled), nil
			}
		}
		if s.askPermission {
			res, err := snd.RequestPermission(ctx, acp.PermissionRequestParams{
				SessionID: st.ID,
				ToolCall: acp.PermissionToolCall{
					ToolCallID: "perm-1",
					Title:      "Run: run_command",
					Kind:       "run_command",
					Status:     "pending",
				},
				Options: []acp.PermissionOption{
					{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
					{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
				},
			})
			if err != nil {
				return string(acp.StopReasonCancelled), err
			}
			if res == nil || res.Outcome != "allow" {
				return string(acp.StopReasonCancelled), nil
			}
		}
		if s.recordToolCall {
			_ = snd.SendSessionUpdate(st.ID, acp.ToolCallUpdate{
				SessionUpdate: acp.UpdateTypeToolCall,
				ToolCallID:    "tool-1",
				Title:         "read_file",
				Kind:          "read",
				Status:        "pending",
			})
			_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    "tool-1",
				Status:        "completed",
				Content: []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "file body"}},
				},
			})
			st.AddMessage(llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: "tool-1", Name: "read_file", InputJSON: `{"path":"a.txt"}`},
				},
			})
			st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "tool-1", Content: "file body"})
		}
		if s.replyText != "" {
			_ = snd.SendSessionUpdate(st.ID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: s.replyText},
			})
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: s.replyText})
		}
		if s.stopReason != "" {
			return s.stopReason, nil
		}
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: s.root},
		Models: []config.ModelEntry{
			{Model: "remote/alpha", MaxTokens: 100, Temperature: 0.2},
			{Model: "remote/beta", MaxTokens: 100, Temperature: 0.2},
		},
		Agent:      config.Agent{Model: "remote/alpha"},
		HTTPServer: config.HTTPServerConfig{AuthToken: token},
	}
	store := &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *remoteClientState) connectClient(token string) error {
	s.sender = &recordingClientSender{}
	h, err := remote.NewHandler(remote.Options{
		BaseURL: s.ts.URL,
		Token:   token,
		Log:     slog.Default(),
	})
	if err != nil {
		return err
	}
	h.SetServer(s.sender)
	s.client = h
	return nil
}

func (s *remoteClientState) agentReplies(text string) error {
	s.replyText = text
	return nil
}

func (s *remoteClientState) agentAsksPermission(text string) error {
	s.askPermission = true
	s.replyText = text
	return nil
}

func (s *remoteClientState) agentRelaysSubagentPermission(text string) error {
	s.subagentPermission = true
	s.replyText = text
	return nil
}

// serverPermissionMode switches the running server's global permission mode
// the way a config commit would, so the bridge decides from the live value.
func (s *remoteClientState) serverPermissionMode(mode string) error {
	if s.mgr == nil || s.srv == nil {
		return fmt.Errorf("server not started")
	}
	cfg := s.mgr.Cfg()
	cfg.Tools.PermissionMode = mode
	s.srv.ReplaceConfig(cfg)
	return nil
}

func (s *remoteClientState) clientReceivedPermissionTitled(title string) error {
	if s.sender == nil {
		return fmt.Errorf("client not connected")
	}
	s.sender.mu.Lock()
	defer s.sender.mu.Unlock()
	for _, p := range s.sender.permissions {
		if p.ToolCall.Title == title {
			return nil
		}
	}
	return fmt.Errorf("no permission request titled %q reached the client (got %d requests)", title, len(s.sender.permissions))
}

func (s *remoteClientState) clientSawToolCall(name string) error {
	if s.sender == nil {
		return fmt.Errorf("client not connected")
	}
	for _, u := range s.sender.snapshot() {
		if tc, ok := u.(acp.ToolCallUpdate); ok && tc.Title == name {
			return nil
		}
	}
	return fmt.Errorf("no tool call named %q reached the client", name)
}

func (s *remoteClientState) agentRepliesWithToolCall(text string) error {
	s.replyText = text
	s.recordToolCall = true
	return nil
}

func (s *remoteClientState) agentRepliesAndStopsAtTurnLimit(text string) error {
	s.replyText = text
	s.stopReason = string(acp.StopReasonMaxTurns)
	return nil
}

func (s *remoteClientState) remoteSessionSwitchedToPlan() error {
	return s.mgr.HandleSessionSetMode(context.Background(), acp.SessionSetModeParams{
		SessionID: s.sessionID, ModeID: "plan",
	})
}

func (s *remoteClientState) loadedSessionModeIs(mode string) error {
	if s.loadResult == nil || s.loadResult.Modes == nil {
		return fmt.Errorf("no load result captured")
	}
	if s.loadResult.Modes.CurrentModeID != mode {
		return fmt.Errorf("loaded mode %q, want %q", s.loadResult.Modes.CurrentModeID, mode)
	}
	return nil
}

func (s *remoteClientState) clientAnswersPermissions(option string) error {
	if s.sender == nil {
		return fmt.Errorf("client not connected")
	}
	s.sender.permAnswer = option
	return nil
}

func (s *remoteClientState) clientStartsSession() error {
	if s.client == nil {
		return fmt.Errorf("client not connected")
	}
	res, err := s.client.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	if s.sessionID != "" && s.firstID == "" {
		s.firstID = s.sessionID
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *remoteClientState) clientSendsPrompt(text string) error {
	if s.client == nil || s.sessionID == "" {
		return fmt.Errorf("no client session")
	}
	// Session snapshots order by updatedAt; keep sibling turns apart.
	time.Sleep(20 * time.Millisecond)
	res, err := s.client.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: s.sessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: text}},
	}, s.sender, nil)
	s.promptErr = err
	if res != nil {
		s.lastStop = string(res.StopReason)
	}
	return nil
}

func (s *remoteClientState) clientReceivedText(text string) error {
	if s.promptErr != nil {
		return fmt.Errorf("prompt failed: %v", s.promptErr)
	}
	got := s.sender.streamedText()
	if !strings.Contains(got, text) {
		return fmt.Errorf("streamed text %q does not contain %q", got, text)
	}
	return nil
}

func (s *remoteClientState) turnEndedWith(stop string) error {
	if s.lastStop != stop {
		return fmt.Errorf("stop reason %q, want %q", s.lastStop, stop)
	}
	return nil
}

func (s *remoteClientState) sessionPersistedRemotely() error {
	rows, err := s.mgr.FileStore().ListSnapshots("", false)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.SessionID == s.sessionID {
			return nil
		}
	}
	return fmt.Errorf("session %s not persisted on the server", s.sessionID)
}

func (s *remoteClientState) modelOptionsFromRemoteCatalog() error {
	res, err := s.client.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: s.sessionID, ConfigID: "model", Value: "remote/beta",
	})
	if err != nil {
		return err
	}
	var modelOpt *acp.ConfigOption
	for i := range res.ConfigOptions {
		if res.ConfigOptions[i].ID == "model" {
			modelOpt = &res.ConfigOptions[i]
		}
	}
	if modelOpt == nil {
		return fmt.Errorf("no model option in %+v", res.ConfigOptions)
	}
	var values []string
	for _, v := range modelOpt.Options {
		values = append(values, v.Value)
	}
	joined := strings.Join(values, ",")
	if !strings.Contains(joined, "remote/alpha") || !strings.Contains(joined, "remote/beta") {
		return fmt.Errorf("model options %q do not list the remote catalog", joined)
	}
	if modelOpt.CurrentValue != "remote/beta" {
		return fmt.Errorf("current model %q, want the selected remote/beta", modelOpt.CurrentValue)
	}
	return nil
}

func (s *remoteClientState) freshClientLoads() error {
	s.replaySender = &recordingClientSender{}
	h, err := remote.NewHandler(remote.Options{
		BaseURL: s.ts.URL,
		Token:   s.token,
		Log:     slog.Default(),
	})
	if err != nil {
		return err
	}
	h.SetServer(s.replaySender)
	res, err := h.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: s.sessionID, CWD: s.root})
	s.loadResult = res
	return err
}

func (s *remoteClientState) replayContains(role, text string) error {
	if s.replaySender == nil {
		return fmt.Errorf("no replay happened")
	}
	for _, u := range s.replaySender.snapshot() {
		if chunk, ok := u.(acp.MessageChunkUpdate); ok {
			if chunk.SessionUpdate == role && strings.Contains(chunk.Content.Text, text) {
				return nil
			}
		}
	}
	return fmt.Errorf("replay has no %s containing %q", role, text)
}

func (s *remoteClientState) replayUserText(text string) error {
	return s.replayContains(acp.UpdateTypeUserMessageChunk, text)
}

func (s *remoteClientState) replayAgentText(text string) error {
	return s.replayContains(acp.UpdateTypeAgentMessageChunk, text)
}

func (s *remoteClientState) replayHasCompletedToolCall() error {
	sawStart := false
	for _, u := range s.replaySender.snapshot() {
		switch v := u.(type) {
		case acp.ToolCallUpdate:
			sawStart = true
		case acp.ToolCallStatusUpdate:
			if sawStart && v.Status == "completed" {
				return nil
			}
		}
	}
	return fmt.Errorf("replay has no completed tool call")
}

func (s *remoteClientState) clientListsSessions() error {
	res, err := s.client.HandleSessionList(context.Background(), acp.SessionListParams{})
	if err != nil {
		return err
	}
	s.listResult = res
	return nil
}

func (s *remoteClientState) firstListedIsNewest() error {
	if s.listResult == nil || len(s.listResult.Sessions) < 2 {
		return fmt.Errorf("expected at least two sessions, got %+v", s.listResult)
	}
	if got := s.listResult.Sessions[0].SessionID; got != s.sessionID {
		return fmt.Errorf("first listed session %q, want the newest %q", got, s.sessionID)
	}
	return nil
}

func initializeRemoteClientScenario(sc *godog.ScenarioContext) {
	s := &remoteClientState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a remote coddy server protected by the token "([^"]*)"$`, s.startServer)
	sc.Step(`^a remote client connected with the token "([^"]*)"$`, s.connectClient)
	sc.Step(`^the remote agent replies with "([^"]*)"$`, s.agentReplies)
	sc.Step(`^the remote agent asks permission before replying "([^"]*)"$`, s.agentAsksPermission)
	sc.Step(`^the remote agent relays a subagent permission prompt before replying "([^"]*)"$`, s.agentRelaysSubagentPermission)
	sc.Step(`^the remote server runs with permission mode "([^"]*)"$`, s.serverPermissionMode)
	sc.Step(`^the client received a permission request titled "([^"]*)"$`, s.clientReceivedPermissionTitled)
	sc.Step(`^the client saw a tool call named "([^"]*)"$`, s.clientSawToolCall)
	sc.Step(`^the remote agent replies with "([^"]*)" and records a tool call$`, s.agentRepliesWithToolCall)
	sc.Step(`^the remote agent replies with "([^"]*)" and stops at the turn limit$`, s.agentRepliesAndStopsAtTurnLimit)
	sc.Step(`^the remote session is switched to plan mode$`, s.remoteSessionSwitchedToPlan)
	sc.Step(`^the loaded session mode is "([^"]*)"$`, s.loadedSessionModeIs)
	sc.Step(`^the client answers permissions with "([^"]*)"$`, s.clientAnswersPermissions)
	sc.Step(`^the client starts a session$`, s.clientStartsSession)
	sc.Step(`^the client sends the prompt "([^"]*)"$`, s.clientSendsPrompt)
	sc.Step(`^the client receives the streamed text "([^"]*)"$`, s.clientReceivedText)
	sc.Step(`^the turn ends with stop reason "([^"]*)"$`, s.turnEndedWith)
	sc.Step(`^the session is persisted on the remote server$`, s.sessionPersistedRemotely)
	sc.Step(`^the session model options come from the remote server catalog$`, s.modelOptionsFromRemoteCatalog)
	sc.Step(`^a fresh client loads that session$`, s.freshClientLoads)
	sc.Step(`^the replay contains the user text "([^"]*)"$`, s.replayUserText)
	sc.Step(`^the replay contains the agent text "([^"]*)"$`, s.replayAgentText)
	sc.Step(`^the replay contains a completed tool call$`, s.replayHasCompletedToolCall)
	sc.Step(`^the client lists remote sessions$`, s.clientListsSessions)
	sc.Step(`^the first listed session is the newest one$`, s.firstListedIsNewest)
}

func TestRemoteClientFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "remote-client",
		ScenarioInitializer: initializeRemoteClientScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/remote_client.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("remote client feature suite failed")
	}
}
