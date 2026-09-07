//go:build http

package httpserver

// Godog harness for the @openai scenario of features/codex_auth.feature: a
// ChatGPT device sign-in over REST, followed by a real ReAct turn over
// POST /v1/responses whose LLM is the REAL codex provider (llm.NewProvider,
// no injected fake) pointed at a stand-in Codex Responses backend through
// CODDY_CODEX_BASE_URL. The stand-in records what the provider actually sent,
// so the credential, the headers, and coddy's own tools are all observable.

import (
	"bytes"
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
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// codexE2EFileToken is the payload of the workspace file the scripted Codex
// backend asks coddy to read with its own "read" tool.
const codexE2EFileToken = "CODEX-E2E-FILE-OK"

// codexE2ETestJWT builds an unsigned JWT-shaped token; the provider only reads
// the payload (exp for refresh decisions, chatgpt_account_id for the header).
func codexE2ETestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// fakeCodexBackend stands in for https://chatgpt.com/backend-api/codex. It
// serves the model catalog and a two-step Responses conversation: the first
// turn calls coddy's "read" tool, the second answers with the tool result.
type fakeCodexBackend struct {
	readPath string

	mu           sync.Mutex
	modelsAuth   string
	authHeaders  []string
	accountIDs   []string
	originators  []string
	instructions []string
	offeredTools [][]string
	turns        int
}

func (b *fakeCodexBackend) snapshot() ([]string, []string, []string, []string, [][]string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.authHeaders...),
		append([]string(nil), b.accountIDs...),
		append([]string(nil), b.originators...),
		append([]string(nil), b.instructions...),
		append([][]string(nil), b.offeredTools...),
		b.modelsAuth
}

func (b *fakeCodexBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		b.mu.Lock()
		b.modelsAuth = r.Header.Get("Authorization")
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5"},{"slug":"gpt-5.4","display_name":"GPT-5.4"}]}`)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/responses") {
		http.NotFound(w, r)
		return
	}

	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Instructions string `json:"instructions"`
		Input        []struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		} `json:"input"`
		Tools []struct {
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
	b.turns++
	b.authHeaders = append(b.authHeaders, r.Header.Get("Authorization"))
	b.accountIDs = append(b.accountIDs, r.Header.Get("chatgpt-account-id"))
	b.originators = append(b.originators, r.Header.Get("originator"))
	b.instructions = append(b.instructions, req.Instructions)
	b.offeredTools = append(b.offeredTools, names)
	b.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	if toolResult == "" {
		codexSSE(w, "response.output_item.done", map[string]any{
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   "call_codex_e2e_1",
				"name":      "read",
				"arguments": fmt.Sprintf(`{"path":%q}`, b.readPath),
			},
		})
	} else {
		codexSSE(w, "response.output_text.delta", map[string]any{
			"delta": "The file says: " + strings.TrimSpace(toolResult),
		})
	}
	codexSSE(w, "response.completed", map[string]any{
		"response": map[string]any{"usage": map[string]any{"input_tokens": 7, "output_tokens": 3}},
	})
}

func codexSSE(w io.Writer, eventType string, data map[string]any) {
	data["type"] = eventType
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
}

// fakeCodexIssuer stands in for https://auth.openai.com during the device flow.
func newFakeCodexIssuer(idToken, accessToken string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = io.WriteString(w, `{"device_auth_id":"device-e2e","user_code":"E2E-CODE","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			_, _ = io.WriteString(w, `{"authorization_code":"code-e2e","code_challenge":"challenge-e2e","code_verifier":"verifier-e2e"}`)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": idToken, "access_token": accessToken, "refresh_token": "refresh-e2e",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

type codexE2EState struct {
	root        string
	home        string
	cwd         string
	accessToken string
	accountID   string
	backend     *fakeCodexBackend
	backendTS   *httptest.Server
	issuerTS    *httptest.Server
	srv         *Server
	ts          *httptest.Server
	prevHome    string
	prevBase    string
	sid         string
}

func (s *codexE2EState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-codex-e2e-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sid = ""
	return nil
}

func (s *codexE2EState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.backendTS != nil {
		s.backendTS.Close()
		s.backendTS = nil
	}
	if s.issuerTS != nil {
		s.issuerTS.Close()
		s.issuerTS = nil
	}
	restoreEnv("CODDY_HOME", s.prevHome)
	restoreEnv("CODDY_CODEX_BASE_URL", s.prevBase)
	s.prevHome, s.prevBase = "", ""
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// restoreEnv puts an environment variable back to its pre-scenario value.
func restoreEnv(name, previous string) {
	if previous == "" {
		_ = os.Unsetenv(name)
		return
	}
	_ = os.Setenv(name, previous)
}

// startServer boots the gateway with the REAL agent runner and the REAL
// provider factory, so the codex provider is built from configuration exactly
// as in production; only the Codex endpoint is redirected.
func (s *codexE2EState) startServer() error {
	s.home = filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	readPath := filepath.Join(s.cwd, "codex-e2e.txt")
	if err := os.WriteFile(readPath, []byte(codexE2EFileToken+"\n"), 0o644); err != nil {
		return err
	}

	s.accountID = "acct-e2e"
	s.accessToken = codexE2ETestJWT(map[string]any{"exp": 4_102_444_800})
	idToken := codexE2ETestJWT(map[string]any{"chatgpt_account_id": s.accountID})
	s.backend = &fakeCodexBackend{readPath: readPath}
	s.backendTS = httptest.NewServer(s.backend)
	s.issuerTS = newFakeCodexIssuer(idToken, s.accessToken)

	s.prevHome = os.Getenv("CODDY_HOME")
	s.prevBase = os.Getenv("CODDY_CODEX_BASE_URL")
	if err := os.Setenv("CODDY_HOME", s.home); err != nil {
		return err
	}
	if err := os.Setenv("CODDY_CODEX_BASE_URL", s.backendTS.URL); err != nil {
		return err
	}

	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
		Models:    []config.ModelEntry{{Model: "codex/gpt-5.5"}},
		Agent:     config.Agent{Model: "codex/gpt-5.5"},
	}
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, s.cwd, store)
	s.srv = New(cfg, mgr, log, s.cwd)
	s.srv.codexAuthIssuer = s.issuerTS.URL
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

// ---- steps ----

func (s *codexE2EState) signInThroughDeviceFlow() error {
	res, err := http.Post(s.ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("device start status %d: %s", res.StatusCode, body)
	}
	var start struct {
		LoginID  string `json:"login_id"`
		UserCode string `json:"user_code"`
	}
	if err := json.NewDecoder(res.Body).Decode(&start); err != nil {
		return err
	}
	if start.LoginID == "" || start.UserCode != "E2E-CODE" {
		return fmt.Errorf("unexpected device start response: %+v", start)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		statusRes, err := http.Get(s.ts.URL + "/coddy/providers/codex/codex-auth/device/" + start.LoginID)
		if err != nil {
			return err
		}
		var status struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		decodeErr := json.NewDecoder(statusRes.Body).Decode(&status)
		_ = statusRes.Body.Close()
		if decodeErr != nil {
			return decodeErr
		}
		if status.Status == "completed" {
			return nil
		}
		if status.Status == "failed" {
			return fmt.Errorf("device login failed: %s", status.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("device login did not complete, last status %q", status.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *codexE2EState) reportsConnected(source string) error {
	res, err := http.Get(s.ts.URL + "/coddy/providers/codex/codex-auth")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var status struct {
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		return err
	}
	if !status.Connected || status.Source != source {
		return fmt.Errorf("codex-auth status = %+v, want connected with source %q", status, source)
	}
	// The credential must live under CODDY_HOME, never in the settings document.
	if _, err := os.Stat(config.CodexAuthPath(s.home, "codex")); err != nil {
		return fmt.Errorf("managed credential missing: %w", err)
	}
	return nil
}

func (s *codexE2EState) modelListUsesToken() error {
	res, err := http.Get(s.ts.URL + "/coddy/providers/codex/models")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if !body.OK || len(body.Models) != 2 || body.Models[0].ID != "gpt-5.4" {
		return fmt.Errorf("provider models = %+v", body)
	}
	_, _, _, _, _, modelsAuth := s.backend.snapshot()
	if modelsAuth != "Bearer "+s.accessToken {
		return fmt.Errorf("model list Authorization = %q, want the signed-in access token", modelsAuth)
	}
	return nil
}

func (s *codexE2EState) sendAgentPrompt() error {
	s.sid = "sess_codex_e2e_1"
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses",
		bytes.NewReader([]byte(`{"model":"agent","input":"read the workspace file","stream":false}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, body)
	}
	return nil
}

func (s *codexE2EState) backendReceivedSignedInToken() error {
	auths, accounts, originators, _, _, _ := s.backend.snapshot()
	return codexAssertCredential(auths, accounts, originators, s.accessToken, s.accountID)
}

func (s *codexE2EState) requestCarriedCoddyToolsAndPrompt() error {
	_, _, _, instructions, offered, _ := s.backend.snapshot()
	return codexAssertCoddyTurn(instructions, offered)
}

func (s *codexE2EState) finalAnswerContainsToolResult() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("messages status %d: %s", res.StatusCode, raw)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	for i := len(body.Messages) - 1; i >= 0; i-- {
		m := body.Messages[i]
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			if !strings.Contains(m.Content, codexE2EFileToken) {
				return fmt.Errorf("final assistant message %q lacks the coddy tool result", m.Content)
			}
			return nil
		}
	}
	return fmt.Errorf("no assistant message in transcript: %s", raw)
}

// codexAssertCredential checks that every Codex request carried the expected
// OAuth credential and the Codex-specific headers.
func codexAssertCredential(auths, accounts, originators []string, token, accountID string) error {
	if len(auths) == 0 {
		return fmt.Errorf("the Codex backend received no request")
	}
	for i, got := range auths {
		if got != "Bearer "+token {
			return fmt.Errorf("request %d Authorization = %q, want the codex access token", i+1, got)
		}
		if accounts[i] != accountID {
			return fmt.Errorf("request %d chatgpt-account-id = %q, want %q", i+1, accounts[i], accountID)
		}
		if originators[i] != "codex_cli_rs" {
			return fmt.Errorf("request %d originator = %q, want codex_cli_rs", i+1, originators[i])
		}
	}
	return nil
}

// codexAssertCoddyTurn checks the agent stayed coddy's: coddy's system prompt
// and coddy's own tool catalog reach the Codex backend.
func codexAssertCoddyTurn(instructions []string, offered [][]string) error {
	if len(instructions) == 0 {
		return fmt.Errorf("the Codex backend received no request")
	}
	if !strings.Contains(strings.ToLower(instructions[0]), "coddy") {
		return fmt.Errorf("instructions are not coddy's system prompt: %q", instructions[0])
	}
	for _, want := range []string{"read", "glob", "run_command"} {
		found := false
		for _, name := range offered[0] {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("coddy tool %q was not offered to the Codex backend; offered: %v", want, offered[0])
		}
	}
	return nil
}

func initializeCodexE2EScenario(sc *godog.ScenarioContext) {
	s := &codexE2EState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy HTTP server with a codex provider and a stand-in Codex backend$`, s.startServer)
	sc.Step(`^I sign in with ChatGPT through the device flow over REST$`, s.signInThroughDeviceFlow)
	sc.Step(`^the codex provider reports connected with source "([^"]*)"$`, s.reportsConnected)
	sc.Step(`^the provider model list is fetched with the signed-in token$`, s.modelListUsesToken)
	sc.Step(`^I send an agent prompt over POST /v1/responses$`, s.sendAgentPrompt)
	sc.Step(`^the Codex backend received the signed-in access token$`, s.backendReceivedSignedInToken)
	sc.Step(`^the Codex request carried coddy's own tools and system prompt$`, s.requestCarriedCoddyToolsAndPrompt)
	sc.Step(`^the final assistant message contains the coddy tool result$`, s.finalAnswerContainsToolResult)
}

func TestCodexAuthOpenAIE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "codex_auth_openai",
		ScenarioInitializer: initializeCodexE2EScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/codex_auth.feature"},
			Tags:     "@openai",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("codex_auth @openai feature failed")
	}
}
