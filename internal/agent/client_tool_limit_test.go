package agent

// What a turn says when the provider refused the tools it offered. The counts
// and the ways out are the whole point: the bare provider error that used to
// surface named neither how many tools went out nor anything to switch off.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/openai/openai-go"
)

func TestClientToolLimitMessage(t *testing.T) {
	cases := []struct {
		name    string
		offered int
		detail  llm.ClientToolLimit
		presets []string
		want    []string
		absent  []string
	}{
		{
			name:    "both counts and a preset to pick",
			offered: 177,
			detail:  llm.ClientToolLimit{Offered: 177, Limit: 128},
			presets: []string{"coding", "review"},
			want: []string{
				"too many tools offered",
				"Coddy offered 177 tools",
				"it takes at most 128",
				"/toolset (configured here: coding, review)",
				"turn off an MCP server",
				"httpserver.max_client_tools",
			},
			// One count said twice reads as two different numbers.
			absent: []string{"the provider counted 177"},
		},
		{
			name:    "no preset configured yet",
			offered: 177,
			detail:  llm.ClientToolLimit{Limit: 128},
			want: []string{
				"Coddy offered 177 tools",
				"it takes at most 128",
				"add a toolsets[] preset",
			},
			absent: []string{"configured here"},
		},
		{
			// A provider that counts the tools differently (it drops one it
			// cannot read): both figures are shown, because they disagree.
			name:    "the provider counted something else",
			offered: 177,
			detail:  llm.ClientToolLimit{Offered: 176, Limit: 128},
			want:    []string{"Coddy offered 177 tools", "the provider counted 176", "it takes at most 128"},
		},
		{
			// A refusal that gives no numbers still names what went out.
			name:    "the refusal gave no numbers",
			offered: 177,
			detail:  llm.ClientToolLimit{},
			want:    []string{"Coddy offered 177 tools"},
			absent:  []string{"it takes at most 0", "the provider counted 0"},
		},
		{
			name:    "one tool's schema",
			offered: 177,
			detail:  llm.ClientToolLimit{Tool: "Playwright_browser_navigate", Limit: 1024},
			presets: []string{"coding"},
			want: []string{
				"tool schema too large",
				`"Playwright_browser_navigate"`,
				"at most 1024 bytes",
				"stop offering that one tool",
				"httpserver.max_client_tool_schema_bytes",
			},
			// The count is not the problem, so its advice is not given.
			absent: []string{"too many tools offered", "Coddy offered 177"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := clientToolLimitMessage(tc.offered, tc.detail, tc.presets)
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q lacks %q", msg, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(msg, absent) {
					t.Errorf("message %q carries %q", msg, absent)
				}
			}
		})
	}
}

// TestRunExplainsARefusedToolListAndKeepsTheProvidersAnswer drives a whole
// turn into the refusal: the loop has to recognise it and end the turn with
// the explanation rather than with the bare "LLM error:" the provider's words
// used to arrive as, the provider's error has to stay in the chain so a
// surface still reads its status, and the request must not be sent again.
func TestRunExplainsARefusedToolListAndKeepsTheProvidersAnswer(t *testing.T) {
	var apiErr openai.Error
	body := `{"message":"at most 128 tools may be offered, 177 were offered: offer fewer ` +
		`(a Coddy client narrows its tools with a toolsets preset) or raise httpserver.max_client_tools on this server",` +
		`"type":"invalid_request_error","code":"too_many_tools","param":"tools"}`
	if err := apiErr.UnmarshalJSON([]byte(body)); err != nil {
		t.Fatal(err)
	}
	apiErr.StatusCode = http.StatusBadRequest
	apiErr.Request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/chat/completions", nil)
	apiErr.Response = &http.Response{StatusCode: http.StatusBadRequest}
	cause := fmt.Errorf("provider %q: openai stream: %w", "local", &apiErr)

	provider := &failingStreamProvider{err: cause}
	ag := overflowRunAgent(t, provider, 49152)

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go"}})
	if err == nil {
		t.Fatal("expected the refusal to end the turn with an error")
	}
	if stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop = %q, want refused", stop)
	}
	for _, want := range []string{
		"too many tools offered", "it takes at most 128", "/toolset", "httpserver.max_client_tools",
		"at most 128 tools may be offered", // the provider's own words stay
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.HasPrefix(err.Error(), "LLM error:") {
		t.Errorf("the bare provider error is back: %v", err)
	}
	if got := llm.UpstreamStatus(err); got != http.StatusBadRequest {
		t.Errorf("UpstreamStatus = %d, want the 400 the provider answered", got)
	}
	if !llm.IsClientToolLimit(err) {
		t.Error("a surface cannot tell the turn ended on a refused tool list")
	}
	if provider.calls != 1 {
		t.Fatalf("provider asked %d times, want once: the same tools are not offered again", provider.calls)
	}
}
