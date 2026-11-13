package llm

// Edge cases of the client-tool-limit classifier. The happy path - a server
// takes as many tools as it is configured for and says so on its model
// listing - is in features/openai_client_tool_limit.feature. The answers below
// are served by a stub HTTP server and read back through the real OpenAI
// client, so the classifier sees exactly the error that client builds.

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// The refusals a Coddy server writes, before and after the bounds became
// configurable, and the one OpenAI's own validator writes.
const (
	coddyTooManyToolsBody = `{"error":{"message":"at most 128 tools may be offered, 177 were offered: ` +
		`offer fewer (a Coddy client narrows its tools with a toolsets preset) or raise httpserver.max_client_tools on this server",` +
		`"type":"invalid_request_error","code":"too_many_tools","param":"tools"}}`
	coddyOldTooManyToolsBody = `{"error":{"message":"at most 128 tools may be offered"}}`
	coddySchemaTooLargeBody  = `{"error":{"message":"tool \"Playwright_browser_navigate\": parameters exceed 1024 bytes (2034 given): ` +
		`raise httpserver.max_client_tool_schema_bytes on this server",` +
		`"type":"invalid_request_error","code":"tool_parameters_too_large","param":"tools"}}`
	openAITooManyToolsBody = `{"error":{"message":"Invalid 'tools': array too long. Expected an array with maximum length 128, ` +
		`but got an array with length 177 instead.","type":"invalid_request_error","param":"tools","code":"array_above_max_length"}}`
)

func TestIsClientToolLimitOnAnswersReadByTheOpenAIClient(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "coddy names the code and the counts", status: 400, body: coddyTooManyToolsBody, want: true},
		{name: "coddy before the counts, by its words alone", status: 400, body: coddyOldTooManyToolsBody, want: true},
		{name: "coddy refuses one tool's schema", status: 400, body: coddySchemaTooLargeBody, want: true},
		{name: "openai's own validator", status: 400, body: openAITooManyToolsBody, want: true},
		{
			name: "a 422 that says it in words", status: 422,
			body: `{"error":{"message":"too many tools in this request"}}`, want: true,
		},
		{
			// The same words in a limit answer: the wrapper's to retry.
			name: "a rate limit that mentions tools", status: 429,
			body: `{"error":{"message":"too many tools may be offered per minute"}}`, want: false,
		},
		{
			// array_above_max_length says only that some array was too long.
			name: "an array bound on another field", status: 400,
			body: `{"error":{"message":"Invalid 'messages': array too long.","type":"invalid_request_error",` +
				`"param":"messages","code":"array_above_max_length"}}`, want: false,
		},
		{
			name: "a request that does not fit the window", status: 400,
			body: `{"error":{"message":"This model's maximum context length is 4096 tokens.",` +
				`"code":"context_length_exceeded"}}`, want: false,
		},
		{
			name: "an unrelated refusal", status: 400,
			body: `{"error":{"message":"unsupported value: 'temperature'","param":"temperature"}}`, want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for path, err := range refusals(t, tc.status, tc.body, openAIClient) {
				if err == nil {
					t.Fatalf("%s: the stub answered %d and the call succeeded", path, tc.status)
				}
				if got := IsClientToolLimit(err); got != tc.want {
					t.Errorf("%s: IsClientToolLimit = %v, want %v for %v", path, got, tc.want, err)
				}
			}
		})
	}
}

func TestClientToolLimitDetailOf(t *testing.T) {
	cases := []struct {
		name string
		body string
		want ClientToolLimit
	}{
		{name: "coddy names both counts", body: coddyTooManyToolsBody, want: ClientToolLimit{Offered: 177, Limit: 128}},
		{name: "coddy before the counts", body: coddyOldTooManyToolsBody, want: ClientToolLimit{Limit: 128}},
		{name: "openai names both", body: openAITooManyToolsBody, want: ClientToolLimit{Offered: 177, Limit: 128}},
		{
			// The tool is read off the answer as it came, so a name with
			// capitals keeps them: it is what the person has to switch off.
			name: "coddy names the tool and the bound", body: coddySchemaTooLargeBody,
			want: ClientToolLimit{Tool: "Playwright_browser_navigate", Limit: 1024},
		},
		{
			name: "a refusal that gives no numbers",
			body: `{"error":{"message":"too many tools","code":"too_many_tools"}}`,
			want: ClientToolLimit{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for path, err := range refusals(t, 400, tc.body, openAIClient) {
				if got := ClientToolLimitDetailOf(err); got != tc.want {
					t.Errorf("%s: detail = %+v, want %+v", path, got, tc.want)
				}
			}
		})
	}
	if got := ClientToolLimitDetailOf(errors.New("plain")); got != (ClientToolLimit{}) {
		t.Errorf("detail of an unrelated error = %+v, want the zero value", got)
	}
}

// SchemaRefusal is what tells a refused tool list from one refused tool, so
// the turn can say which of the two things to do.
func TestClientToolLimitSchemaRefusal(t *testing.T) {
	for path, err := range refusals(t, 400, coddySchemaTooLargeBody, openAIClient) {
		if !ClientToolLimitDetailOf(err).SchemaRefusal() {
			t.Errorf("%s: a refused schema does not report itself as one", path)
		}
	}
	for path, err := range refusals(t, 400, coddyTooManyToolsBody, openAIClient) {
		if ClientToolLimitDetailOf(err).SchemaRefusal() {
			t.Errorf("%s: a refused tool list reports itself as a refused schema", path)
		}
	}
}

// A refusal of the tools is not a failure of the provider's lane: sending the
// same tools again asks the same thing and is refused again.
func TestAClientToolLimitIsNotTransient(t *testing.T) {
	for path, err := range refusals(t, 400, coddyTooManyToolsBody, openAIClient) {
		if IsTransientProviderError(err) {
			t.Errorf("%s: a refused tool list would be retried", path)
		}
	}
}

func TestIsClientToolLimitIgnoresCancellation(t *testing.T) {
	if IsClientToolLimit(nil) {
		t.Error("a nil error is not a refusal")
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		wrapped := fmt.Errorf("at most 128 tools may be offered: %w", err)
		if IsClientToolLimit(wrapped) {
			t.Errorf("a cancellation wrapped in those words is not a refusal: %v", wrapped)
		}
	}
}
