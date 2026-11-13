package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// kimiBodyStand answers one chat completion and records the request body.
func kimiBodyStand(t *testing.T, body *map[string]any) *KimiInferenceTransport {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	return &KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}
}

// The subscription endpoint takes only temperature 1, so a value a models[]
// row merely configures never travels: Coddy's own catalog import writes one,
// and every such session answered HTTP 400 before.
func TestKimiChatLeavesAConfiguredTemperatureOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ProviderInput
	}{
		{"configured", ProviderInput{Model: "kimi", Temperature: 0.2}},
		{"configured with reasoning", ProviderInput{Model: "kimi", Temperature: 0.2, ReasoningEffort: "low"}},
		{"configured zero", ProviderInput{Model: "kimi", Temperature: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			if _, err := newKimiChatProvider(kimiBodyStand(t, &body), tc.in).Complete(context.Background(), nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["temperature"]; ok {
				t.Fatalf("request carried temperature %v", body["temperature"])
			}
		})
	}
}

// A temperature the caller asked for on the request still travels, as it does
// on the Anthropic and Responses protocols of the same provider: the endpoint
// answers for itself. Platform is the Moonshot API, which takes the
// parameter, so a configured one reaches it as well.
func TestKimiChatSendsATemperatureThatIsMeantToTravel(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ProviderInput
		want float64
	}{
		{"requested zero", ProviderInput{Model: "kimi", Temperature: 0, TemperatureSet: true}, 0},
		{"requested one", ProviderInput{Model: "kimi", Temperature: 1, TemperatureSet: true}, 1},
		{"requested on a subscription", ProviderInput{Model: "kimi", Temperature: 0.4, TemperatureSet: true}, 0.4},
		{"configured on Platform", ProviderInput{Model: "kimi", Mode: "platform", Temperature: 0.3}, 0.3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			if _, err := newKimiChatProvider(kimiBodyStand(t, &body), tc.in).Complete(context.Background(), nil, nil); err != nil {
				t.Fatal(err)
			}
			if body["temperature"] != tc.want {
				t.Fatalf("temperature = %v, want %v", body["temperature"], tc.want)
			}
		})
	}
}

// A refused request carries the endpoint's own explanation: "HTTP 400" alone
// says nothing about what the request got wrong.
func TestKimiStatusErrorsCarryTheProviderExplanation(t *testing.T) {
	const refusal = `{"error":{"message":"invalid temperature: only 1 is allowed for this model","type":"invalid_request_error"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(refusal))
	}))
	defer server.Close()
	source := func(context.Context) (string, error) { return "token", nil }
	for name, provider := range map[string]Provider{
		"chat":      newKimiChatProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: source}, ProviderInput{Model: "kimi"}),
		"anthropic": newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: source, anthropic: true}, ProviderInput{Model: "kimi"}),
		"responses": newKimiResponsesProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: source}, ProviderInput{Model: "kimi"}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), "only 1 is allowed for this model") {
				t.Fatalf("error %q does not carry the explanation", err)
			}
			if UpstreamStatus(err) != http.StatusBadRequest {
				t.Fatalf("status = %d", UpstreamStatus(err))
			}
		})
	}
}

// A refusal with a long body is bounded, so one error cannot carry a whole
// response into the transcript.
func TestKimiStatusErrorBoundsTheBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(strings.Repeat("x", 64<<10)))
	}))
	defer server.Close()
	tr := &KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	_, err := newKimiChatProvider(tr, ProviderInput{Model: "kimi"}).Complete(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if len(err.Error()) > kimiStatusBodyLimit+256 {
		t.Fatalf("error of %d bytes is not bounded", len(err.Error()))
	}
}
