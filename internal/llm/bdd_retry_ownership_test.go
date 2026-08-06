package llm

// Godog harness for features/llm_retry_ownership.feature: exercises the real
// OpenAI, Anthropic, and Codex providers against a request-counting HTTP server.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type retryOwnershipState struct {
	server    *httptest.Server
	provider  Provider
	mode      string
	requests  atomic.Int32
	callErr   error
	// codex wiring: a temp auth.json and the previous CODDY_CODEX_BASE_URL so
	// NewProvider's codex branch reaches the request-counting server.
	prevCodexBaseURL string
	setCodexBaseURL  bool
	authDir          string
}

func (s *retryOwnershipState) reset() {
	s.cleanup()
	s.provider = nil
	s.mode = ""
	s.requests.Store(0)
	s.callErr = nil
}

func (s *retryOwnershipState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	if s.setCodexBaseURL {
		_ = os.Setenv(EnvCodexBaseURL, s.prevCodexBaseURL)
		s.setCodexBaseURL = false
	}
	if s.authDir != "" {
		_ = os.RemoveAll(s.authDir)
		s.authDir = ""
	}
}

func (s *retryOwnershipState) aProviderWithOneConfiguredRetry(providerType, mode string) error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary failure","type":"server_error"}}`))
	}))

	in := ProviderInput{
		Type:          providerType,
		Model:         "test-model",
		APIKey:        "test-key",
		BaseURL:       s.server.URL,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	}
	if providerType == "codex" {
		authPath, err := s.writeCodexAuthForRetryOwnership()
		if err != nil {
			return fmt.Errorf("create %s provider: %w", providerType, err)
		}
		// NewProvider's codex branch ignores BaseURL and resolves the endpoint
		// from CODDY_CODEX_BASE_URL, so point it at the request-counting server.
		s.prevCodexBaseURL = os.Getenv(EnvCodexBaseURL)
		s.setCodexBaseURL = true
		if err := os.Setenv(EnvCodexBaseURL, s.server.URL); err != nil {
			return fmt.Errorf("create %s provider: set base URL: %w", providerType, err)
		}
		in.APIKey = ""
		in.AuthPath = authPath
	}

	provider, err := NewProvider(in)
	if err != nil {
		return fmt.Errorf("create %s provider: %w", providerType, err)
	}
	s.provider = provider
	s.mode = mode
	return nil
}

// writeCodexAuthForRetryOwnership stages a Codex auth.json with a far-future
// token so the auth source returns the static credential without an OAuth
// refresh. It mirrors the codex_auth_test.go makeJWT shape without depending
// on a *testing.T helper.
func (s *retryOwnershipState) writeCodexAuthForRetryOwnership() (string, error) {
	dir, err := os.MkdirTemp("", "coddy-retry-ownership-")
	if err != nil {
		return "", err
	}
	s.authDir = dir
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`))
	auth := codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens: codexTokens{
			AccessToken: header + "." + payload + ".sig",
			AccountID:   "acct-1",
		},
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *retryOwnershipState) theUpstreamRespondsWithARetryableServerError() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	messages := []Message{{Role: RoleUser, Content: "hello"}}
	switch s.mode {
	case "non-streaming":
		_, s.callErr = s.provider.Complete(ctx, messages, nil)
	case "streaming":
		_, s.callErr = s.provider.Stream(ctx, messages, nil, func(StreamChunk) {})
	default:
		return fmt.Errorf("unsupported request mode %q", s.mode)
	}
	return nil
}

func (s *retryOwnershipState) theProviderCallFails() error {
	if s.callErr == nil {
		return fmt.Errorf("provider call unexpectedly succeeded")
	}
	return nil
}

func (s *retryOwnershipState) exactlyUpstreamRequestsAreSent(want int) error {
	if got := int(s.requests.Load()); got != want {
		return fmt.Errorf("upstream requests = %d, want %d", got, want)
	}
	return nil
}

func initializeRetryOwnershipScenario(sc *godog.ScenarioContext) {
	s := &retryOwnershipState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a "([^"]+)" provider using "([^"]+)" requests with one configured retry$`, s.aProviderWithOneConfiguredRetry)
	sc.Step(`^the upstream responds with a retryable server error$`, s.theUpstreamRespondsWithARetryableServerError)
	sc.Step(`^the provider call fails$`, s.theProviderCallFails)
	sc.Step(`^exactly (\d+) upstream requests are sent$`, s.exactlyUpstreamRequestsAreSent)
}

func TestLLMRetryOwnershipFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-retry-ownership",
		ScenarioInitializer: initializeRetryOwnershipScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_retry_ownership.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM retry ownership feature suite failed")
	}
}
