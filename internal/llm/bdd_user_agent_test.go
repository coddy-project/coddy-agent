package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
	"github.com/cucumber/godog"
)

func TestProviderUserAgentFeature(t *testing.T) {
	var identity, protocol string
	var seen []string
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^an "([^"]*)" provider with user agent "([^"]*)"$`, func(kind, value string) { protocol = kind; identity = value; seen = nil })
			sc.Step(`^completions and model listing are requested$`, func() error {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen = append(seen, r.Header.Get("User-Agent"))
					var body struct {
						Stream bool `json:"stream"`
					}
					if r.Method == http.MethodPost {
						_ = json.NewDecoder(r.Body).Decode(&body)
					}
					if protocol == "anthropic" && body.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, anthropicStreamPrefix+
							"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
							"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"+
							"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						return
					}
					if body.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(r.URL.Path, "/models") {
						_, _ = fmt.Fprint(w, `{"data":[{"id":"test"}]}`)
					} else if protocol == "anthropic" {
						_, _ = fmt.Fprint(w, `{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
					} else {
						_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}
				}))
				defer srv.Close()
				in := ProviderInput{Type: protocol, Model: "test", APIKey: "test", BaseURL: srv.URL, UserAgent: identity}
				p, err := NewProvider(in)
				if err != nil {
					return err
				}
				if _, err = p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil); err != nil {
					return err
				}
				if _, err = p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil, func(StreamChunk) {}); err != nil {
					return err
				}
				in.DisableStream = true
				p, err = NewProvider(in)
				if err != nil {
					return err
				}
				if _, err = p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil); err != nil {
					return err
				}
				_, err = ListModels(context.Background(), in)
				return err
			})
			sc.Step(`^all requests identify the client as "([^"]*)"$`, func(prefix string) error {
				if len(seen) != 4 {
					return fmt.Errorf("got %d requests", len(seen))
				}
				for _, value := range seen {
					if prefix == "Coddy/" && value != prefix+version.Get() {
						return fmt.Errorf("wrong application version in %q", value)
					}
					if !strings.HasPrefix(value, prefix) {
						return fmt.Errorf("User-Agent = %q, want prefix %q", value, prefix)
					}
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/provider_user_agent.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("provider identity feature failed")
	}
}
