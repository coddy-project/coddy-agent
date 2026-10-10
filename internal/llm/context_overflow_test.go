package llm

// Edge cases of the context-overflow classifier. The happy path - a turn whose
// request outgrew the window ends with an explanation - is in
// features/context_compaction.feature. The answers below are served by a stub
// HTTP server and read back through the real OpenAI and Anthropic clients, so
// the classifier sees exactly the errors those clients build, including what
// they keep of the body.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go"
)

type overflowAnswer struct {
	name   string
	status int
	body   string
	want   bool
}

// refusals sends one request through Complete and one through Stream of the
// provider newProvider builds against a server answering status and body, and
// returns both errors.
func refusals(t *testing.T, status int, body string, newProvider func(url string) Provider) map[string]error {
	t.Helper()
	return refusalsAs(t, status, "application/json", body, newProvider)
}

// refusalsAs is refusals for a server that labels its answer contentType.
func refusalsAs(t *testing.T, status int, contentType, body string, newProvider func(url string) Provider) map[string]error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	p := newProvider(srv.URL)
	msgs := []Message{{Role: RoleUser, Content: "hello"}}
	out := map[string]error{}
	_, out["complete"] = p.Complete(context.Background(), msgs, nil)
	_, out["stream"] = p.Stream(context.Background(), msgs, nil, func(StreamChunk) {})
	return out
}

func runOverflowAnswers(t *testing.T, cases []overflowAnswer, newProvider func(url string) Provider) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for path, err := range refusals(t, tc.status, tc.body, newProvider) {
				if err == nil {
					t.Fatalf("%s: the stub answered %d and the call succeeded", path, tc.status)
				}
				if got := IsContextOverflow(err); got != tc.want {
					t.Errorf("%s: IsContextOverflow = %v, want %v for %v", path, got, tc.want, err)
				}
			}
		})
	}
}

func openAIClient(url string) Provider { return newOpenAIProvider("m", "key", url, nil, 100, 0, "") }

func anthropicClient(url string) Provider {
	return newAnthropicProvider("m", "key", url, nil, 100, 0, "")
}

func TestIsContextOverflowOnAnswersReadByTheOpenAIClient(t *testing.T) {
	runOverflowAnswers(t, []overflowAnswer{
		// A typed code settles it, whatever the message says.
		{"openai names the code", 400, `{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 10000 tokens. Please reduce the length of the messages.","type":"invalid_request_error","param":"messages","code":"context_length_exceeded"}}`, true},
		{"openai input over the window", 400, `{"error":{"message":"Your input exceeds the context window of this model. Please adjust your input and try again.","type":"invalid_request_error","param":"input","code":"context_length_exceeded"}}`, true},
		{"the code alone, in a status that is not on the list", 500, `{"error":{"message":"refused","code":"context_length_exceeded"}}`, true},
		{"openrouter", 400, `{"error":{"message":"This endpoint's maximum context length is 8192 tokens. However, you requested about 12000 tokens (10000 of text input, 2000 in the output). Please reduce the length of either one, or use the \"middle-out\" transform to compress your prompt automatically.","code":400}}`, true},
		{"vllm", 400, `{"error":{"message":"This model's maximum context length is 32768 tokens. However, your request has 40000 input tokens. Please reduce the length of the input messages.","type":"BadRequestError","param":null,"code":400}}`, true},
		{"deepseek", 400, `{"error":{"message":"This model's maximum context length is 65536 tokens. However, you requested 70000 tokens (70000 in the messages, 0 in the completion). Please reduce the length of the messages or completion.","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`, true},
		{"litellm proxy", 400, `{"error":{"message":"litellm.ContextWindowExceededError: litellm.BadRequestError: Hosted_vllmException - Input length 9000 exceeds model limit","type":null,"param":null,"code":"400"}}`, true},
		// The report behind this classifier: a local backend that answers 404.
		{"local backend answering 404", 404, `{"error":"Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"}`, true},
		{"llama.cpp exceed_context_size_error", 400, `{"error":{"code":400,"message":"the request exceeds the available context size, try increasing it","type":"exceed_context_size_error","n_prompt_tokens":5000,"n_ctx":4096}}`, true},
		{"llama.cpp older wording", 400, `{"error":{"code":400,"message":"the request exceeds the available context size. try increasing the context size or enable context shift","type":"invalid_request_error"}}`, true},
		{"gateway counting input tokens", 400, `{"error":"Request too long: 121692 input tokens over the limit of 46112"}`, true},
		{"xai", 400, `{"code":"Client specified an invalid argument","error":"This model's maximum prompt length is 131072 but the request contains 150000 tokens."}`, true},
		{"gemini", 400, `{"error":{"code":400,"message":"The input token count (1043256) exceeds the maximum number of tokens allowed (1048576).","status":"INVALID_ARGUMENT"}}`, true},
		{"bedrock behind a gateway", 400, `{"error":{"message":"Input is too long for requested model.","type":"invalid_request_error"}}`, true},
		{"moonshot", 400, `{"error":{"message":"Invalid request: Your request exceeded model token limit: 8192","type":"invalid_request_error"}}`, true},
		{"lm studio", 400, `{"error":"Trying to keep the first 6269 tokens when context the overflows. However, the model is loaded with context length of only 4096 tokens, which is not enough. Try to load the model with a larger context length, or provide a shorter input"}`, true},
		{"a gateway answering 413", 413, `{"error":{"message":"The prompt is too long for this model","type":"request_too_large"}}`, true},
		{"a gateway answering 422", 422, `{"error":{"message":"Input length 9000 exceeds the context window of 8192 tokens"}}`, true},
		// Bodies without an "error" key: the client keeps none of it in the
		// error it builds, so the classifier reads the response itself.
		{"vllm, flat body", 400, `{"object":"error","message":"This model's maximum context length is 4096 tokens. However, you requested 4500 tokens (3000 in the messages, 1500 in the completion). Please reduce the length of the messages or completion.","type":"BadRequestError","param":null,"code":400}`, true},
		{"mistral, flat body", 400, `{"object":"error","message":"Prompt contains 10000 tokens and 0 draft tokens, too large for model with 8192 maximum context length","type":"invalid_request_error","param":null,"code":null}`, true},
		{"cohere, flat body", 400, `{"message":"too many tokens: total number of tokens in the prompt exceeds the limit of 4081"}`, true},
		{"plain text body", 400, `Request rejected: this model's maximum context length is 8192 tokens.`, true},
		{"a code in a status that is retried", 500, `{"error":{"message":"refused","code":"context_length_exceeded"}}`, true},

		// Ordinary refusals stay refusals.
		{"invalid tool schema", 400, `{"error":{"message":"Invalid schema for function 'read': In context=('properties', 'path'), schema must have a 'type' key.","type":"invalid_request_error","param":"tools[0].function.parameters","code":"invalid_function_parameters"}}`, false},
		{"completion budget over the model's cap", 400, `{"error":{"message":"max_tokens is too large: 128000. This model supports at most 16384 completion tokens, whereas you provided 128000.","type":"invalid_request_error","param":"max_tokens","code":null}}`, false},
		{"tool message without its call", 400, `{"error":{"message":"Invalid parameter: messages with role 'tool' must be a response to a preceeding message with 'tool_calls'.","type":"invalid_request_error","param":"messages.[3].role","code":null}}`, false},
		{"one string over its cap", 400, `{"error":{"message":"Invalid 'messages[3].content': string too long. Expected a string with maximum length 1048576, but got a string with length 2097152 instead.","type":"invalid_request_error","param":"messages[3].content","code":"string_above_max_length"}}`, false},
		{"too many tools", 400, `{"error":{"message":"Invalid 'tools': array too long. Expected an array with maximum length 128, but got an array with length 150 instead.","type":"invalid_request_error","param":"tools","code":"array_above_max_length"}}`, false},
		{"wrong key", 401, `{"error":{"message":"Incorrect API key provided: sk-abc***def.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`, false},
		{"unknown model", 404, `{"error":{"message":"The model ` + "`gpt-9`" + ` does not exist or you do not have access to it.","type":"invalid_request_error","param":null,"code":"model_not_found"}}`, false},
		{"empty 400", 400, ``, false},
		{"flat body that is no overflow", 400, `{"object":"error","message":"Invalid schema for function 'read'","type":"BadRequestError","param":null,"code":400}`, false},
		{"flat limit answer that says too many tokens", 429, `{"message":"too many tokens per minute"}`, false},
		{"plain text of an outage", 502, `upstream connect error: maximum context length`, false},
		{"proxy body limit", 413, `<html><head><title>413 Request Entity Too Large</title></head><body>nginx</body></html>`, false},
		{"groq tokens per minute", 413, `{"error":{"message":"Request too large for model ` + "`llama3-70b-8192`" + ` in organization ` + "`org_x`" + ` service tier ` + "`on_demand`" + ` on tokens per minute (TPM): Limit 6000, Requested 9000, please reduce your message size and try again.","type":"tokens","code":"rate_limit_exceeded"}}`, false},
		{"tokens per minute", 429, `{"error":{"message":"Rate limit reached for gpt-4o in organization org-x on tokens per min (TPM): Limit 30000, Used 29000, Requested 5000. Please try again in 2s.","type":"tokens","code":"rate_limit_exceeded"}}`, false},
		{"a limit answer that says too many tokens", 429, `{"error":{"message":"Too many tokens in the last minute, slow down","type":"rate_limit"}}`, false},
		{"a server error that names the window", 500, `{"error":{"message":"failed to load the model: the maximum context length is not available","type":"server_error"}}`, false},
	}, openAIClient)
}

func TestIsContextOverflowOnAnswersReadByTheAnthropicClient(t *testing.T) {
	runOverflowAnswers(t, []overflowAnswer{
		{"prompt is too long", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`, true},
		{"prompt is too long as plain text", 400, `prompt is too long: 210000 tokens > 200000 maximum`, true},
		{"input and max_tokens over the limit", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"input length and ` + "`max_tokens`" + ` exceed context limit: 188240 + 21333 > 200000, decrease input length or ` + "`max_tokens`" + ` and try again"}}`, true},

		{"output cap", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 100000 > 64000, which is the maximum allowed number of output tokens for claude-sonnet-4-20250514"}}`, false},
		{"tool_use without tool_result", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.3: ` + "`tool_use`" + ` ids were found without ` + "`tool_result`" + ` blocks immediately after: toolu_01A09q90qw90lq917835lq9."}}`, false},
		{"image over its size", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.0.content.1.image.source.base64: image exceeds 5 MB maximum: 6291456 bytes > 5242880 bytes"}}`, false},
		{"body over the byte cap", 413, `{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`, false},
		{"bad key", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, false},
		{"rate limit", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the rate limit for your organization of 40,000 input tokens per minute."}}`, false},
		{"overloaded", 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, false},
	}, anthropicClient)
}

func TestIsContextOverflowOnErrorsWithoutAClientBehindThem(t *testing.T) {
	llamaFrame := newStreamServerError([]byte(`{"code":400,"message":"the request exceeds the available context size. try increasing the context size or enable context shift","type":"invalid_request_error"}`), false)
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"llama.cpp error frame inside the stream", fmt.Errorf("openai stream: %w", llamaFrame), true},
		{"the same frame as a server's 500", fmt.Errorf("openai stream: %w", &streamServerError{code: 500, msg: "the request exceeds the available context size"}), false},
		{"codex response.failed", fmt.Errorf("codex stream: %s", "Your input exceeds the context window of this model. Please adjust your input and try again."), true},
		{"codex failure after text", fmt.Errorf("codex stream: %w", &streamServerError{msg: "Your input exceeds the context window of this model.", emitted: true}), true},
		{"devin carrying a claude answer", fmt.Errorf("devin chat: %w", devinErrorFromBody(400, []byte(`{"code":"invalid_argument","message":"prompt is too long: 250000 tokens > 200000 maximum"}`))), true},
		{"devin quota", fmt.Errorf("devin chat: %w", devinErrorFromBody(429, []byte(`{"code":"resource_exhausted","message":"too many tokens"}`))), false},
		{"mistral body relayed as text", errors.New("400 Bad Request: Prompt contains 10000 tokens and 0 draft tokens, too large for model with 8192 maximum context length"), true},
		{"a summarizer's refusal in the test providers", errors.New("400 Bad Request: this model's maximum context length is exceeded, reduce the length of the messages"), true},
		{"the same text behind a provider label", fmt.Errorf("provider %q (http://127.0.0.1:8080/v1): %w", "local", errors.New("openai stream: 404 Not Found: Context limit is 49152 tokens; prompt=51402")), true},

		// "too many tokens" is a refusal's wording only where a status says it is
		// one: without a status it also words a rate limit.
		{"a codex rate limit worded as too many tokens", fmt.Errorf("codex stream: %s", "Rate limit reached: too many tokens per min"), false},
		{"a relayed text of a limit", errors.New("upstream said: too many tokens in the last minute"), false},
		{"a tool's failure that mentions a window", errors.New("run_command: grep: the context window is not configured"), false},
		{"a limit", fmt.Errorf("openai stream: %w", &streamServerError{code: 429, msg: "too many tokens"}), false},
		{"a quota", &QuotaResetError{Cause: errors.New("too many tokens")}, false},
		{"a connection that died", errors.New("read tcp 10.0.0.2:51234->10.0.0.1:443: connection reset by peer"), false},
		{"a cut stream", fmt.Errorf("openai stream: %w", &streamTruncatedError{}), false},
		{"cancelled over the overflow text", fmt.Errorf("maximum context length: %w", context.Canceled), false},
		{"deadline over the overflow text", fmt.Errorf("maximum context length: %w", context.DeadlineExceeded), false},
		{"nil", nil, false},
	} {
		if got := IsContextOverflow(tc.err); got != tc.want {
			t.Errorf("%s: IsContextOverflow = %v, want %v for %v", tc.name, got, tc.want, tc.err)
		}
	}
}

// The whole chain a configured provider is built as - the resilient wrapper
// under the labelling one - classifies the answer and asks once, however many
// retries are allowed: a request that does not fit does not fit twice.
func TestContextOverflowThroughTheProviderChainIsAskedOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"}`))
	}))
	defer srv.Close()
	p, err := NewProvider(ProviderInput{
		Type: "openai", Name: "local", Model: "qwen", BaseURL: srv.URL,
		RetryMax: 3, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil, func(StreamChunk) {})
	if err == nil {
		t.Fatal("expected the refusal")
	}
	if !IsContextOverflow(err) {
		t.Fatalf("not classified through the provider chain: %v", err)
	}
	if got := UpstreamStatus(err); got != 404 {
		t.Fatalf("UpstreamStatus = %d, want the 404 the backend answered", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the backend was asked %d times, want once", got)
	}
}

// httpStatusFromError reads a status out of the words of an untyped error, and
// "4500 tokens" holds "500 ". The refusal is still not retried.
func TestContextOverflowTextThatLooksLikeAServerErrorIsNotRetried(t *testing.T) {
	err := errors.New("400 Bad Request: This model's maximum context length is 4096 tokens. However, your messages resulted in 4500 tokens.")
	if !IsContextOverflow(err) {
		t.Fatalf("not classified: %v", err)
	}
	if isRetryableLLMError(err) {
		t.Fatal("a request the provider refused for its size would be sent again")
	}
	if IsTransientProviderError(err) {
		t.Fatal("a request the provider refused for its size would be run again after a pause")
	}
}

func TestOverflowDetailOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want OverflowDetail
	}{
		{"openai counting the messages", errors.New("400: This model's maximum context length is 8192 tokens. However, your messages resulted in 10000 tokens. Please reduce the length of the messages."), OverflowDetail{Prompt: 10000, Limit: 8192}},
		{"vllm counting the request", errors.New("400: This model's maximum context length is 4096 tokens. However, you requested 4500 tokens (3000 in the messages, 1500 in the completion)."), OverflowDetail{Prompt: 4500, Limit: 4096}},
		{"openrouter estimating the request", errors.New("400: This endpoint's maximum context length is 8192 tokens. However, you requested about 12000 tokens (10000 of text input, 2000 in the output)."), OverflowDetail{Prompt: 12000, Limit: 8192}},
		{"vllm counting input tokens", errors.New("400: This model's maximum context length is 32768 tokens. However, your request has 40000 input tokens."), OverflowDetail{Prompt: 40000, Limit: 32768}},
		{"the local backend of the report", errors.New("404: Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"), OverflowDetail{Prompt: 51402, Limit: 49152}},
		{"anthropic prompt", errors.New("400: prompt is too long: 210000 tokens > 200000 maximum"), OverflowDetail{Prompt: 210000, Limit: 200000}},
		{"anthropic prompt and max_tokens", errors.New("400: input length and `max_tokens` exceed context limit: 188240 + 21333 > 200000, decrease input length"), OverflowDetail{Prompt: 209573, Limit: 200000}},
		{"gateway", errors.New("400: Request too long: 121692 input tokens over the limit of 46112"), OverflowDetail{Prompt: 121692, Limit: 46112}},
		{"llama.cpp", errors.New(`400: {"code":400,"message":"the request exceeds the available context size, try increasing it","type":"exceed_context_size_error","n_prompt_tokens":5000,"n_ctx":4096}`), OverflowDetail{Prompt: 5000, Limit: 4096}},
		{"a limit and no prompt", errors.New("400: This model's maximum context length is 8192 tokens. Reduce the input."), OverflowDetail{Limit: 8192}},
		{"no numbers", errors.New("400: the request exceeds the available context size"), OverflowDetail{}},
		{"not an overflow, though numbers match", errors.New("400: invalid value, prompt=51402, limit=49152"), OverflowDetail{}},
		{"nil", nil, OverflowDetail{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := OverflowDetailOf(tc.err); got != tc.want {
				t.Fatalf("OverflowDetailOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The detail is read from the body the client kept, not from a message that
// also carries the request's address.
func TestOverflowDetailOfAnswerReadByTheClient(t *testing.T) {
	errs := refusals(t, 400, `{"error":{"message":"prompt is too long: 210000 tokens > 200000 maximum"}}`, anthropicClient)
	for path, err := range errs {
		if got := OverflowDetailOf(err); got != (OverflowDetail{Prompt: 210000, Limit: 200000}) {
			t.Errorf("%s: OverflowDetailOf = %+v", path, got)
		}
	}
}

// Codex answers {"detail": ...}; codexRequestError reads the body for the
// message and leaves the typed error empty, so the detail is what the
// classifier has.
func TestIsContextOverflowOnACodexDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"detail over the window", 400, `{"detail":"Your input exceeds the context window of this model. Please adjust your input and try again."}`, true},
		{"detail of something else", 400, `{"detail":"The 'gpt-5.1-codex' model is not supported when using Codex with a ChatGPT account."}`, false},
		{"a limit", 429, `{"detail":"Rate limit reached: too many tokens per min"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p := newCodexTestProvider(t, srv.URL)
			_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil, func(StreamChunk) {})
			if err == nil {
				t.Fatal("expected the refusal")
			}
			if got := IsContextOverflow(err); got != tc.want {
				t.Fatalf("IsContextOverflow = %v, want %v for %v", got, tc.want, err)
			}
		})
	}
}

// Reading a response for the classifier leaves it as it was found: the same
// answer the next time, and the same bytes for whoever dumps the response.
func TestClassifyingAFlatBodyLeavesTheResponseReadable(t *testing.T) {
	body := `{"object":"error","message":"This model's maximum context length is 4096 tokens. However, you requested 4500 tokens"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err := openAIClient(srv.URL).Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil)
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("not a client error: %v", err)
	}
	for i := 0; i < 3; i++ {
		if !IsContextOverflow(err) {
			t.Fatalf("classification %d: not an overflow", i+1)
		}
	}
	got, readErr := io.ReadAll(apiErr.Response.Body)
	if readErr != nil || string(got) != body {
		t.Fatalf("the response body after classifying = %q, %v; want the %d bytes the server sent", got, readErr, len(body))
	}
}

// An error body is read to a bound; one longer than it is classified on its
// head and still reaches the next reader whole.
func TestClassifyingALongBodyReadsABoundedPrefixAndKeepsTheRest(t *testing.T) {
	head := "maximum context length is 4096 tokens. "
	long := head + strings.Repeat("x", 3*keptBodyLimit)
	tail := "maximum context length is 1 tokens"
	apiErr := &openai.Error{
		StatusCode: 400,
		Request:    httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/chat/completions", nil),
		Response:   &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(long + tail))},
	}
	err := fmt.Errorf("openai complete: %w", apiErr)
	if !IsContextOverflow(err) {
		t.Fatal("the head of the body names the window")
	}
	got, _ := io.ReadAll(apiErr.Response.Body)
	if string(got) != long+tail {
		t.Fatalf("the body after classifying is %d bytes, want the %d it had", len(got), len(long+tail))
	}

	tailOnly := &openai.Error{
		StatusCode: 400,
		Request:    httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/chat/completions", nil),
		Response:   &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 3*keptBodyLimit) + tail))},
	}
	if IsContextOverflow(fmt.Errorf("openai complete: %w", tailOnly)) {
		t.Fatal("a phrase past the bound was read")
	}
}

// A refusal that carries the overflow code is not a failure of the provider's
// lane, whatever its status: running the same step again asks the same thing.
func TestAnOverflowCodeInAServerErrorIsNeitherRetriedNorRunAgain(t *testing.T) {
	for path, err := range refusals(t, 500, `{"error":{"message":"refused","code":"context_length_exceeded"}}`, openAIClient) {
		if !IsContextOverflow(err) {
			t.Fatalf("%s: not classified: %v", path, err)
		}
		if isRetryableLLMError(err) {
			t.Errorf("%s: the wrapper would send the request again", path)
		}
		if IsTransientProviderError(err) {
			t.Errorf("%s: the loop would run the step again after a pause", path)
		}
	}
}

func TestOverflowDetailOfIgnoresAbsurdFigures(t *testing.T) {
	err := errors.New("400: prompt is too long: 4611686018427387904 tokens > 1 maximum")
	if got := OverflowDetailOf(err); got != (OverflowDetail{Limit: 1}) {
		t.Fatalf("OverflowDetailOf = %+v, want only the limit of 1", got)
	}
	huge := errors.New("400: input length and `max_tokens` exceed context limit: 4611686018427387904 + 4611686018427387904 > 200000")
	if got := OverflowDetailOf(huge); got.Prompt != 0 {
		t.Fatalf("OverflowDetailOf = %+v, want no prompt size from figures no window has", got)
	}
}

// The refusal an OpenAI-compatible MLX swap proxy gave in a real session: a
// 404 whose message says the prompt was 30 tokens past the window. Servers of
// this kind word the error object, flatten it or send it as text, and the
// client keeps a different part of the answer for each; all of them are
// recognised on both paths and give the same two figures.
func TestIsContextOverflowOnAnMLXSwapProxyRefusal(t *testing.T) {
	const msg = "Context limit is 49152 tokens; prompt=49182 leaves 0 output tokens, below the minimum 16"
	want := OverflowDetail{Prompt: 49182, Limit: 49152}
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"error object", "application/json", `{"error":{"message":"` + msg + `","type":"invalid_request_error","code":404}}`},
		{"error object, no code", "application/json", `{"error":{"message":"` + msg + `"}}`},
		{"error as a string", "application/json", `{"error":"` + msg + `"}`},
		{"flat message", "application/json", `{"message":"` + msg + `"}`},
		{"flat detail", "application/json", `{"detail":"` + msg + `"}`},
		{"plain text", "text/plain; charset=utf-8", msg},
		{"plain text with a newline", "text/plain", msg + "\n"},
		{"plain text labelled json", "application/json", msg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for path, err := range refusalsAs(t, 404, tc.contentType, tc.body, openAIClient) {
				if err == nil {
					t.Fatalf("%s: the stub answered 404 and the call succeeded", path)
				}
				if !IsContextOverflow(err) {
					t.Errorf("%s: not classified as an overflow: %v", path, err)
				}
				if got := OverflowDetailOf(err); got != want {
					t.Errorf("%s: OverflowDetailOf = %+v, want %+v", path, got, want)
				}
				if isRetryableLLMError(err) || IsTransientProviderError(err) {
					t.Errorf("%s: the refusal would be sent again", path)
				}
			}
		})
	}

	// A 404 of the same proxy that is about something else stays an ordinary
	// failure, and so does its answer for an unknown model.
	for _, body := range []string{
		`{"error":{"message":"Model not found: qwen-9b-4bit"}}`,
		`{"detail":"Not Found"}`,
		`404 page not found`,
	} {
		for path, err := range refusalsAs(t, 404, "application/json", body, openAIClient) {
			if IsContextOverflow(err) {
				t.Errorf("%s: %q is no overflow", path, body)
			}
		}
	}
}

func TestOverflowDetailOfFlatBodyReadByTheClient(t *testing.T) {
	body := `{"object":"error","message":"This model's maximum context length is 4096 tokens. However, you requested 4500 tokens (3000 in the messages, 1500 in the completion).","type":"BadRequestError","param":null,"code":400}`
	for path, err := range refusals(t, 400, body, openAIClient) {
		if got := OverflowDetailOf(err); got != (OverflowDetail{Prompt: 4500, Limit: 4096}) {
			t.Errorf("%s: OverflowDetailOf = %+v", path, got)
		}
	}
}

// The same error is classified from several goroutines when a surface and the
// loop both look at it; the read-and-put-back of its body is one step.
func TestClassifyingOneErrorFromManyGoroutines(t *testing.T) {
	body := `{"object":"error","message":"This model's maximum context length is 4096 tokens. However, you requested 4500 tokens"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err := openAIClient(srv.URL).Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !IsContextOverflow(err) {
				t.Error("one classification lost the body to another")
			}
		}()
	}
	wg.Wait()
}

// An error built without the request its message reads is no overflow, and
// asking is no panic.
func TestClassifyingAClientErrorWithoutARequestDoesNotPanic(t *testing.T) {
	if IsContextOverflow(fmt.Errorf("openai complete: %w", &openai.Error{StatusCode: 400})) {
		t.Fatal("an empty 400 is no overflow")
	}
}
