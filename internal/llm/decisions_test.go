package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// decisionStand serves the decisions endpoint for one canned status and body,
// recording every request's path, auth header and JSON body.
type decisionStand struct {
	srv      *httptest.Server
	calls    atomic.Int32
	lastAuth atomic.Value
	lastBody atomic.Value
}

func newDecisionStand(t *testing.T, key string, status int, body string, headers map[string]string) *decisionStand {
	t.Helper()
	d := &decisionStand{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.calls.Add(1)
		d.lastAuth.Store(r.Header.Get("Authorization"))
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		d.lastBody.Store(raw)
		if r.URL.Path != "/decisions" {
			http.NotFound(w, r)
			return
		}
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"unknown key"}`))
			return
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *decisionStand) bodyMap(t *testing.T) map[string]any {
	t.Helper()
	raw, ok := d.lastBody.Load().([]byte)
	if !ok {
		t.Fatal("no request body captured")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return m
}

// The answer shape the real endpoint returns (observed live on
// api.neuraldeep.ru, 2026-10-09): the model echoed, one answers entry per
// question with the chosen option, its probabilities, a confidence field the
// client does not read, and usage counters.
const decisionAnswerFixture = `{
  "model": "frida-decisions",
  "state_truncated": false,
  "answers": {
    "safety": {
      "type": "choice",
      "probabilities": {"safe": 0.02, "unsafe": 0.98},
      "confidence": 0.95,
      "choice": "unsafe"
    }
  },
  "usage": {"questions": 1, "options": 2, "state_tokens": 12, "state_truncated": false, "units_charged": 1}
}`

func TestFetchNeuralDeepDecisionSendsTheSafetyQuestion(t *testing.T) {
	d := newDecisionStand(t, "sk-test-key-0123456789abcdef", http.StatusOK, decisionAnswerFixture, nil)
	dec, err := FetchNeuralDeepDecision(context.Background(), d.srv.URL, "sk-test-key-0123456789abcdef",
		"clef-flash", "rm -rf /", "/home/user/project", d.srv.Client())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if d.calls.Load() != 1 || d.lastAuth.Load() != "Bearer sk-test-key-0123456789abcdef" {
		t.Fatalf("calls=%d auth=%v, want one bearer request", d.calls.Load(), d.lastAuth.Load())
	}
	body := d.bodyMap(t)
	if body["model"] != "clef-flash" {
		t.Fatalf("model = %v", body["model"])
	}
	state, _ := body["state"].(string)
	if !strings.Contains(state, "rm -rf /") || !strings.Contains(state, "/home/user/project") {
		t.Fatalf("state = %q, want the command and the cwd", state)
	}
	questions, _ := body["questions"].(map[string]any)
	question, _ := questions["safety"].(map[string]any)
	if question == nil {
		t.Fatalf("questions = %v, want a safety question keyed by id", body["questions"])
	}
	if question["type"] != "choice" {
		t.Fatalf("type = %v", question["type"])
	}
	if instr, _ := question["instructions"].(string); strings.TrimSpace(instr) == "" {
		t.Fatalf("instructions missing: %v", question)
	}
	criteria, _ := question["criteria"].(map[string]any)
	if _, ok := criteria[NeuralDeepDecisionSafe]; !ok {
		t.Fatalf("criteria = %v, want a %q option", criteria, NeuralDeepDecisionSafe)
	}
	if _, ok := criteria[NeuralDeepDecisionUnsafe]; !ok {
		t.Fatalf("criteria = %v, want an %q option", criteria, NeuralDeepDecisionUnsafe)
	}
	if dec.Choice != NeuralDeepDecisionUnsafe {
		t.Fatalf("choice = %q", dec.Choice)
	}
	if p := dec.Probability(NeuralDeepDecisionUnsafe); p < 0.97 || p > 0.99 {
		t.Fatalf("p(unsafe) = %v", p)
	}
}

func TestFetchNeuralDeepDecisionAnswerShapes(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantChoice string
		wantSafeP  float64
	}{
		{"choice with probabilities", decisionAnswerFixture, "unsafe", 0.02},
		{"bare option name", `{"answers":{"safety":"safe"}}`, "safe", 1},
		{"decision naming", `{"answers":{"safety":{"decision":"safe"}}}`, "safe", 1},
		{"label naming", `{"answers":{"safety":{"label":"unsafe"}}}`, "unsafe", 0},
		{"probabilities only", `{"answers":{"safety":{"probabilities":{"safe":0.9,"unsafe":0.1}}}}`, "safe", 0.9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDecisionStand(t, "", http.StatusOK, tc.body, nil)
			dec, err := FetchNeuralDeepDecision(context.Background(), d.srv.URL, "sk-x", "frida-decisions", "ls", "", d.srv.Client())
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if dec.Choice != tc.wantChoice {
				t.Fatalf("choice = %q, want %q", dec.Choice, tc.wantChoice)
			}
			if p := dec.Probability(NeuralDeepDecisionSafe); p != tc.wantSafeP {
				t.Fatalf("p(safe) = %v, want %v", p, tc.wantSafeP)
			}
		})
	}
}

func TestFetchNeuralDeepDecisionErrorKinds(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		headers    map[string]string
		wantKind   string
		wantRetry  time.Duration
		wantStatus int
	}{
		{"unauthorized", http.StatusUnauthorized, `{"detail":"unknown key"}`, nil, NeuralDeepDecisionUnauthorized, 0, 401},
		{"forbidden", http.StatusForbidden, `{"detail":"user blocked"}`, nil, NeuralDeepDecisionForbidden, 0, 403},
		{"rate limited with seconds", http.StatusTooManyRequests, `{"detail":"decisions quota exhausted"}`, map[string]string{"Retry-After": "30"}, NeuralDeepDecisionRateLimited, 30 * time.Second, 429},
		{"unavailable with seconds", http.StatusServiceUnavailable, `{"detail":"later"}`, map[string]string{"Retry-After": "5"}, NeuralDeepDecisionUnavailable, 5 * time.Second, 503},
		{"server error", http.StatusInternalServerError, `boom`, nil, NeuralDeepDecisionUnavailable, 0, 500},
		{"not json", http.StatusOK, `<html>proxy</html>`, nil, NeuralDeepDecisionInvalid, 0, 200},
		{"answer missing", http.StatusOK, `{"answers":{}}`, nil, NeuralDeepDecisionInvalid, 0, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDecisionStand(t, "", tc.status, tc.body, tc.headers)
			_, err := FetchNeuralDeepDecision(context.Background(), d.srv.URL, "sk-secret-value-abcdef0123456789", "frida-decisions", "ls", "", d.srv.Client())
			var de *NeuralDeepDecisionError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want *NeuralDeepDecisionError", err)
			}
			if de.Kind != tc.wantKind || de.Status != tc.wantStatus {
				t.Fatalf("kind/status = %s/%d, want %s/%d", de.Kind, de.Status, tc.wantKind, tc.wantStatus)
			}
			if de.RetryAfter != tc.wantRetry {
				t.Fatalf("retry-after = %s, want %s", de.RetryAfter, tc.wantRetry)
			}
			if strings.Contains(err.Error(), "sk-secret-value") {
				t.Fatalf("error text leaks the key: %q", err.Error())
			}
		})
	}
}

func TestFetchNeuralDeepDecisionNetworkFailureIsUnavailable(t *testing.T) {
	d := newDecisionStand(t, "", http.StatusOK, decisionAnswerFixture, nil)
	url := d.srv.URL
	d.srv.Close()
	_, err := FetchNeuralDeepDecision(context.Background(), url, "sk-x", "frida-decisions", "ls", "", &http.Client{})
	var de *NeuralDeepDecisionError
	if !errors.As(err, &de) || de.Kind != NeuralDeepDecisionUnavailable {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestNeuralDeepDecisionForProviderResolvesKeyAndBase(t *testing.T) {
	const key = "sk-stored-key-0123456789abcdef"
	// The stand accepts any bearer so both credential sources can be told
	// apart by the Authorization header they sent.
	d := newDecisionStand(t, "", http.StatusOK, decisionAnswerFixture, nil)
	t.Setenv(EnvNeuralDeepBaseURL, d.srv.URL)
	t.Setenv("NEURALDEEP_API_KEY", "")
	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")
	if err := SaveNeuralDeepAuth(authPath, key, "https://hub.example", NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	prov := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
	dec, err := NeuralDeepDecisionForProvider(context.Background(), prov, authPath, "frida-decisions", "rm -rf /", "")
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	if dec.Choice != NeuralDeepDecisionUnsafe || d.calls.Load() != 1 || d.lastAuth.Load() != "Bearer "+key {
		t.Fatalf("choice=%q calls=%d auth=%v", dec.Choice, d.calls.Load(), d.lastAuth.Load())
	}

	// An explicit api_key wins over the stored login, as it does for requests.
	explicit := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep", APIKey: "sk-explicit-key-0123456789abcd"}
	if _, err := NeuralDeepDecisionForProvider(context.Background(), explicit, authPath, "frida-decisions", "ls", ""); err != nil {
		t.Fatalf("explicit key: %v", err)
	}
	if d.lastAuth.Load() != "Bearer sk-explicit-key-0123456789abcd" {
		t.Fatalf("auth = %v, want the explicit key", d.lastAuth.Load())
	}
}

func TestNeuralDeepDecisionForProviderWithoutCredential(t *testing.T) {
	d := newDecisionStand(t, "", http.StatusOK, decisionAnswerFixture, nil)
	t.Setenv(EnvNeuralDeepBaseURL, d.srv.URL)
	t.Setenv("NEURALDEEP_API_KEY", "")
	prov := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
	_, err := NeuralDeepDecisionForProvider(context.Background(), prov, config.NeuralDeepAuthPath(t.TempDir(), "neuraldeep"), "frida-decisions", "ls", "")
	var de *NeuralDeepDecisionError
	if !errors.As(err, &de) || de.Kind != NeuralDeepDecisionUnauthorized {
		t.Fatalf("err = %v, want unauthorized without a request", err)
	}
	if d.calls.Load() != 0 {
		t.Fatalf("calls = %d, want no request without a credential", d.calls.Load())
	}
}
