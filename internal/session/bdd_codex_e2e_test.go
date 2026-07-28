package session_test

// Godog harness for the @acp scenario of features/codex_auth.feature: a real
// ReAct turn through the ACP session flow (Manager.HandleSessionNew +
// HandleSessionPrompt) whose LLM is the REAL codex provider built by
// llm.NewProvider. No Coddy-managed credential exists, so the provider must
// fall back to the Codex CLI login in CODEX_HOME; a stand-in Codex backend
// (CODDY_CODEX_BASE_URL) records the credential and the tools it received.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	acpCodexFileToken   = "CODEX-ACP-FILE-OK"
	acpCodexAccessToken = "acp-codex-cli-access-token"
	acpCodexAccountID   = "acct-acp-cli"
	acpCodexReasoningID = "rs_acp_1"
	acpCodexEncrypted   = "gAAAAAB-acp-encrypted-reasoning"
)

// acpCodexBackend stands in for the Codex Responses backend: first turn calls
// coddy's own "read" tool, second turn answers with the tool result.
type acpCodexBackend struct {
	readPath string

	mu           sync.Mutex
	authHeaders  []string
	accountIDs   []string
	originators  []string
	instructions []string
	offeredTools [][]string
	inputs       [][]codexInputItem
	includes     [][]string
}

// codexInputItem is the part of a replayed Responses input item the spec cares about.
type codexInputItem struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	Output           string `json:"output"`
	EncryptedContent string `json:"encrypted_content"`
}

func (b *acpCodexBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/responses") {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Instructions string           `json:"instructions"`
		Input        []codexInputItem `json:"input"`
		Include      []string         `json:"include"`
		Tools        []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &req)

	toolResult := ""
	for _, item := range req.Input {
		if item.Type == "function_call_output" {
			toolResult = item.Output
		}
	}
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}

	b.mu.Lock()
	b.authHeaders = append(b.authHeaders, r.Header.Get("Authorization"))
	b.accountIDs = append(b.accountIDs, r.Header.Get("chatgpt-account-id"))
	b.originators = append(b.originators, r.Header.Get("originator"))
	b.instructions = append(b.instructions, req.Instructions)
	b.offeredTools = append(b.offeredTools, names)
	b.inputs = append(b.inputs, req.Input)
	b.includes = append(b.includes, req.Include)
	b.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	if toolResult == "" {
		acpCodexSSE(w, "response.reasoning_summary_text.delta", map[string]any{"delta": "**Reading the file**"})
		acpCodexSSE(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type":              "reasoning",
				"id":                acpCodexReasoningID,
				"summary":           []map[string]any{{"type": "summary_text", "text": "**Reading the file**"}},
				"content":           []any{},
				"encrypted_content": acpCodexEncrypted,
			},
		})
		acpCodexSSE(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   "call_acp_codex_1",
				"name":      "read",
				"arguments": fmt.Sprintf(`{"path":%q}`, b.readPath),
			},
		})
	} else {
		acpCodexSSE(w, "response.output_text.delta", map[string]any{
			"delta": "The file says: " + strings.TrimSpace(toolResult),
		})
	}
	acpCodexSSE(w, "response.completed", map[string]any{
		"response": map[string]any{"usage": map[string]any{"input_tokens": 5, "output_tokens": 2}},
	})
}

func acpCodexSSE(w io.Writer, eventType string, data map[string]any) {
	data["type"] = eventType
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
}

type acpCodexE2EState struct {
	root      string
	home      string
	cwd       string
	backend   *acpCodexBackend
	backendTS *httptest.Server
	mgr       *session.Manager
	state     *session.State
	prevBase  string
	prevCodex string
}

func (s *acpCodexE2EState) reset() error {
	s.close()
	s.state = nil
	return nil
}

func (s *acpCodexE2EState) close() {
	if s.state != nil {
		s.state.CloseAll()
		s.state = nil
	}
	if s.backendTS != nil {
		s.backendTS.Close()
		s.backendTS = nil
	}
	acpRestoreEnv(llm.EnvCodexBaseURL, s.prevBase)
	acpRestoreEnv("CODEX_HOME", s.prevCodex)
	s.prevBase, s.prevCodex = "", ""
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.mgr = nil
}

func acpRestoreEnv(name, previous string) {
	if previous == "" {
		_ = os.Unsetenv(name)
		return
	}
	_ = os.Setenv(name, previous)
}

// startManager mirrors cmd/coddy's ACP wiring: the real agent runner with the
// real provider factory, so the codex credential lookup is the production one.
func (s *acpCodexE2EState) startManager() error {
	root, err := os.MkdirTemp("", "coddy-bdd-acp-codex-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "workspace")
	codexHome := filepath.Join(root, "codex-cli")
	for _, dir := range []string{s.home, s.cwd, codexHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	readPath := filepath.Join(s.cwd, "codex-acp.txt")
	if err := os.WriteFile(readPath, []byte(acpCodexFileToken+"\n"), 0o644); err != nil {
		return err
	}

	// Only the Codex CLI login exists; CODDY_HOME/providers/codex/codex-auth.json
	// is deliberately absent so the fallback path is what gets exercised.
	cliAuth := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      acpCodexJWT(map[string]any{"chatgpt_account_id": acpCodexAccountID}),
			"access_token":  acpCodexAccessToken,
			"refresh_token": "acp-refresh",
			"account_id":    acpCodexAccountID,
		},
	}
	data, _ := json.MarshalIndent(cliAuth, "", "  ")
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), data, 0o600); err != nil {
		return err
	}

	s.backend = &acpCodexBackend{readPath: readPath}
	s.backendTS = httptest.NewServer(s.backend)
	s.prevBase = os.Getenv(llm.EnvCodexBaseURL)
	s.prevCodex = os.Getenv("CODEX_HOME")
	if err := os.Setenv(llm.EnvCodexBaseURL, s.backendTS.URL); err != nil {
		return err
	}
	if err := os.Setenv("CODEX_HOME", codexHome); err != nil {
		return err
	}

	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
		Models:    []config.ModelEntry{{Model: "codex/gpt-5.5", ReasoningDefault: "medium"}},
		Agent:     config.Agent{Model: "codex/gpt-5.5"},
	}
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		s.state = st
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, log, s.cwd, nil)
	return nil
}

// acpCodexJWT builds an unsigned JWT-shaped id token for the account claim.
func acpCodexJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// ---- steps ----

func (s *acpCodexE2EState) runPrompt() error {
	ctx := context.Background()
	newRes, err := s.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	res, err := s.mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: newRes.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "read the workspace file"}},
	})
	if err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}
	if res.StopReason != acp.StopReasonEndTurn {
		return fmt.Errorf("stop reason = %q, want end_turn", res.StopReason)
	}
	return nil
}

func (s *acpCodexE2EState) backendReceivedCLIToken() error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	if len(s.backend.authHeaders) == 0 {
		return fmt.Errorf("the Codex backend received no request")
	}
	for i, got := range s.backend.authHeaders {
		if got != "Bearer "+acpCodexAccessToken {
			return fmt.Errorf("request %d Authorization = %q, want the Codex CLI access token", i+1, got)
		}
		if s.backend.accountIDs[i] != acpCodexAccountID {
			return fmt.Errorf("request %d chatgpt-account-id = %q, want %q", i+1, s.backend.accountIDs[i], acpCodexAccountID)
		}
		if s.backend.originators[i] != "codex_cli_rs" {
			return fmt.Errorf("request %d originator = %q, want codex_cli_rs", i+1, s.backend.originators[i])
		}
	}
	return nil
}

func (s *acpCodexE2EState) requestCarriedCoddyToolsAndPrompt() error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	if len(s.backend.instructions) == 0 {
		return fmt.Errorf("the Codex backend received no request")
	}
	if !strings.Contains(strings.ToLower(s.backend.instructions[0]), "coddy") {
		return fmt.Errorf("instructions are not coddy's system prompt: %q", s.backend.instructions[0])
	}
	for _, want := range []string{"read", "glob", "run_command"} {
		found := false
		for _, name := range s.backend.offeredTools[0] {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("coddy tool %q was not offered to the Codex backend; offered: %v", want, s.backend.offeredTools[0])
		}
	}
	return nil
}

// secondRequestReplayedReasoning proves the Codex chain of thought survives a
// tool call: the encrypted reasoning item of turn one comes back verbatim in the
// input of turn two, ahead of the tool output it produced.
func (s *acpCodexE2EState) secondRequestReplayedReasoning() error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	if len(s.backend.inputs) < 2 {
		return fmt.Errorf("the Codex backend saw %d requests, want at least 2", len(s.backend.inputs))
	}
	if len(s.backend.includes[0]) == 0 || s.backend.includes[0][0] != "reasoning.encrypted_content" {
		return fmt.Errorf("first request include = %v, want reasoning.encrypted_content", s.backend.includes[0])
	}
	reasoningIdx, outputIdx := -1, -1
	for i, item := range s.backend.inputs[1] {
		switch item.Type {
		case "reasoning":
			if item.EncryptedContent != acpCodexEncrypted || item.ID != acpCodexReasoningID {
				return fmt.Errorf("replayed reasoning item = %+v, want the verbatim item of turn one", item)
			}
			reasoningIdx = i
		case "function_call_output":
			outputIdx = i
		}
	}
	if reasoningIdx < 0 {
		return fmt.Errorf("second request carries no reasoning item: %+v", s.backend.inputs[1])
	}
	if outputIdx >= 0 && reasoningIdx > outputIdx {
		return fmt.Errorf("reasoning item must precede the tool output (%d > %d)", reasoningIdx, outputIdx)
	}
	return nil
}

func (s *acpCodexE2EState) finalAnswerContainsToolResult() error {
	if s.state == nil {
		return fmt.Errorf("no session state captured")
	}
	msgs := s.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			if !strings.Contains(msgs[i].Content, acpCodexFileToken) {
				return fmt.Errorf("final assistant message %q lacks the coddy tool result", msgs[i].Content)
			}
			return nil
		}
	}
	return fmt.Errorf("no assistant message in the transcript")
}

func initializeACPCodexE2EScenario(sc *godog.ScenarioContext) {
	s := &acpCodexE2EState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an ACP session manager with a codex provider and a Codex CLI login only$`, s.startManager)
	sc.Step(`^I run an agent prompt through the ACP session flow$`, s.runPrompt)
	sc.Step(`^the Codex backend received the Codex CLI access token$`, s.backendReceivedCLIToken)
	sc.Step(`^the Codex request carried coddy's own tools and system prompt$`, s.requestCarriedCoddyToolsAndPrompt)
	sc.Step(`^the second Codex request replayed the encrypted reasoning of the first$`, s.secondRequestReplayedReasoning)
	sc.Step(`^the final assistant message contains the coddy tool result$`, s.finalAnswerContainsToolResult)
}

func TestCodexAuthACPE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "codex_auth_acp",
		ScenarioInitializer: initializeACPCodexE2EScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/codex_auth.feature"},
			Tags:     "@acp",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("codex_auth @acp feature failed")
	}
}
