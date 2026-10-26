package llm

// Events and frames cut inside their JSON (issue #384): on every streaming
// provider such a cut is a truncation - the delivered text and reasoning come
// back next to the error, no tool call of the unfinished answer is returned,
// the request is retried only while nothing reached the caller, and the
// decoder's error stays reachable - while a frame that is not JSON at all
// stays a final error that is never retried, whatever its text holds.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sseRaw writes one framed Codex event with its data verbatim, so a test can
// send JSON that stops short: the SDK decoder dispatches the frame and its
// decode fails at the end of the input, the error of issue #384.
func sseRaw(w io.Writer, eventType, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}

// TestStreamDecodeTruncation pins the rule that tells a cut from a malformed
// event: the typed syntax error first, then the spellings of the decoder's
// end-of-input diagnostic - the input ending, the space of the end-of-input
// probe inside a literal or a number, and the newline the SDK decoders append
// to every data line, inside a string or an escape sequence included - and,
// where the payload is at hand, the error sitting inside it: at its end for a
// spelling that names a byte. The cuts go through whatever toolchain runs the
// test; TestEndOfInputDiagnosticSpellings holds the spellings of each Go
// release. A diagnostic naming a byte the server sent, a type error over valid
// JSON, a message without the type and a transport error are never cuts. A
// malformed event whose whitespace falls inside a token is told apart by the
// position when the payload is known, and taken as a cut by the SDK paths,
// which have only the diagnostic: the documented heuristic.
func TestStreamDecodeTruncation(t *testing.T) {
	decode := func(in string) error {
		var v any
		return json.Unmarshal([]byte(in), &v)
	}
	cuts := []string{
		`{"a":"x`, `{"a":nu`, `{"a":tr`, `{"a":f`, `{"a":-`, `{"a":1.`, `{"a":1e`, `{"a":1e+`, `{"a":`,
		`{"a":"x\`, `{"a":"x\u`, `{"a":"\u12`, "{\"a\":\"\xd0",
		"{\"a\":\"x\n", "{\"a\":nu\n", "{\"a\":-\n", "{\"a\":1.\n", "{\"a\":1e\n", "{\"a\":1\n",
		"{\"a\":\"x\\\n", "{\"a\":\"\\u12\n", "{\"a\":\"\xd0\n",
	}
	for _, in := range cuts {
		err := decode(in)
		for _, payloadLen := range []int{0, len(in)} {
			trunc := streamDecodeTruncation(err, true, payloadLen)
			if trunc == nil {
				t.Errorf("%q (%v) with payload length %d: want a truncation", in, err, payloadLen)
				continue
			}
			if !trunc.emitted || !errors.Is(trunc, err) || !errors.As(trunc, new(*json.SyntaxError)) {
				t.Errorf("%q: emitted=%v cause=%v, want emitted with the decoder's error as cause", in, trunc.emitted, trunc.cause)
			}
			if !strings.HasPrefix(trunc.Error(), "stream truncated: an event arrived with incomplete JSON (") {
				t.Errorf("%q: message %q", in, trunc.Error())
			}
		}
	}
	if got, want := (&streamTruncatedError{cause: decode(`{"a":"x`)}).Error(),
		"stream truncated: an event arrived with incomplete JSON (unexpected end of JSON input)"; got != want {
		t.Errorf("message with a cause = %q, want %q", got, want)
	}
	if got, want := (&streamTruncatedError{}).Error(),
		"stream truncated: connection closed before a terminal marker ([DONE] or finish_reason)"; got != want {
		t.Errorf("message without a cause = %q, want the unchanged %q", got, want)
	}
	var typeErr struct{ A int }
	for name, err := range map[string]error{
		"a byte the server sent":              decode(`[DO`),
		"an HTML page":                        decode(`<html>`),
		"a stray comma":                       decode(`{"a":,}`),
		"a second top-level value":            decode(`{"a":1}}`),
		"a bad last byte":                     decode(`{"a":1x`),
		"valid JSON of another shape":         json.Unmarshal([]byte(`{"A":"x"}`), &typeErr),
		"the message without the type":        errors.New("unexpected end of JSON input"),
		"a transport error":                   io.ErrUnexpectedEOF,
		"an escape broken before the newline": decode("{\"a\":\"\\uzz\n"),
		"a backslash inside a unicode escape": decode("{\"a\":\"\\u12\\\n"),
		"nil":                                 nil,
	} {
		for _, payloadLen := range []int{0, 8} {
			if trunc := streamDecodeTruncation(err, false, payloadLen); trunc != nil {
				t.Errorf("%s (%v): classified as a truncation %q", name, err, trunc.Error())
			}
		}
	}
	// The SDK streams spell an in-band error event "received error while
	// streaming: <payload>" with no decoder error in the chain at all: the
	// payload that ends inside its JSON is a cut, the complete one is not,
	// and the spelling is found inside a wrapped message.
	if trunc := streamDecodeTruncation(errors.New(`received error while streaming: {"code":502,"message":"oops`), false, 0); trunc == nil {
		t.Error("an in-band error event cut inside its payload: want a truncation")
	}
	if trunc := streamDecodeTruncation(fmt.Errorf("codex stream: %w", errors.New(`received error while streaming: {"code":502`)), false, 0); trunc == nil {
		t.Error("the SDK spelling inside a wrapped message: want a truncation")
	}
	if trunc := streamDecodeTruncation(errors.New(`received error while streaming: {"code":502,"message":"oops"}`), false, 0); trunc != nil {
		t.Error("a complete in-band error event is not a truncation")
	}
	if trunc := streamDecodeTruncation(errors.New(`received error while streaming: overloaded`), false, 0); trunc != nil {
		t.Error("an in-band error spelled in plain text is not a truncation")
	}
	// Whitespace inside a token of a complete event: the diagnostic alone
	// cannot tell it from a cut, the position can.
	for name, in := range map[string]string{
		"a space inside a literal":                  `{"a":nu ll}`,
		"a space inside a number":                   `{"a":1. 5}`,
		"a JSON string split across two data lines": "{\"a\":\"hello\nworld\"}",
		"an escape sequence split across two lines": "{\"a\":\"x\\\nn\"}",
	} {
		err := decode(in)
		if trunc := streamDecodeTruncation(err, false, len(in)); trunc != nil {
			t.Errorf("%s (%v): the error is not at the end of the payload, yet classified as %q", name, err, trunc.Error())
		}
		if trunc := streamDecodeTruncation(err, false, 0); trunc == nil {
			t.Errorf("%s (%v): without the payload the diagnostic alone must take it for a cut", name, err)
		}
	}
}

// TestEndOfInputDiagnosticSpellings holds the diagnostics each Go release gives
// for a cut, whichever toolchain runs the suite. Up to Go 1.26 encoding/json
// reports the end of input at the end of the payload, and a cut inside a
// literal, a number or an escape sequence through its end-of-input probe, a
// space; from Go 1.27, with encoding/json/v2 underneath, a cut inside a
// number, an escape sequence or a multi-byte character is the plain end of
// input at the start of that token, and the newline the SDK decoders append
// reads "in string" or "invalid escape sequence" (issue #610). The messages
// and offsets are the ones each release gives for the input named.
func TestEndOfInputDiagnosticSpellings(t *testing.T) {
	cases := []struct {
		release, input string
		msg            string
		offset         int64
		payloadLen     int // 0 on the SDK paths, which have no payload
		want           bool
	}{
		{"1.26", `{"a":"x`, "unexpected end of JSON input", 7, 7, true},
		{"1.26", `{"a":nu`, "invalid character ' ' in literal null (expecting 'l')", 7, 7, true},
		{"1.26", `{"a":-`, "invalid character ' ' in numeric literal", 6, 6, true},
		{"1.26", `{"a":1.`, "invalid character ' ' after decimal point in numeric literal", 7, 7, true},
		{"1.26", `{"a":1e`, "invalid character ' ' in exponent of numeric literal", 7, 7, true},
		{"1.26", `{"a":"x\`, "invalid character ' ' in string escape code", 8, 8, true},
		{"1.26", `{"a":"\u12`, `invalid character ' ' in \u hexadecimal character escape`, 10, 10, true},
		{"1.26", "{\"a\":\"x\n", `invalid character '\n' in string literal`, 8, 0, true},
		{"1.26", "{\"a\":1e\n", `invalid character '\n' in exponent of numeric literal`, 8, 0, true},
		{"1.26", "{\"a\":\"x\\\n", `invalid character '\n' in string escape code`, 9, 0, true},
		{"1.26", `{"a":nu ll}`, "invalid character ' ' in literal null (expecting 'l')", 8, 11, false},
		{"1.26", "{\"a\":\"hello\nworld\"}", `invalid character '\n' in string literal`, 12, 19, false},
		{"1.26", `{"a":-x}`, "invalid character 'x' in numeric literal", 7, 0, false},
		{"1.26", `{"a":,}`, "invalid character ',' looking for beginning of value", 6, 0, false},
		{"1.26", "{\"a\":\"\\u12\\\n", `invalid character '\\' in \u hexadecimal character escape`, 11, 0, false},

		{"1.27", `{"a":"x`, "unexpected end of JSON input", 7, 7, true},
		{"1.27", `{"a":nu`, "unexpected end of JSON input", 7, 7, true},
		{"1.27", `{"a":-`, "unexpected end of JSON input", 5, 6, true},
		{"1.27", `{"a":1.`, "unexpected end of JSON input", 5, 7, true},
		{"1.27", `{"a":1e`, "unexpected end of JSON input", 5, 7, true},
		{"1.27", `{"a":"x\`, "unexpected end of JSON input", 7, 8, true},
		{"1.27", `{"a":"\u12`, "unexpected end of JSON input", 6, 10, true},
		{"1.27", "{\"a\":\"\xd0", "unexpected end of JSON input", 6, 7, true},
		{"1.27", "{\"a\":\"x\n", `invalid character '\n' in string`, 8, 0, true},
		{"1.27", "{\"a\":1e\n", `invalid character '\n' in numeric literal`, 8, 0, true},
		{"1.27", "{\"a\":\"x\\\n", `invalid escape sequence "\\\n" in string`, 9, 0, true},
		{"1.27", "{\"a\":\"\\u12\n", `invalid escape sequence "\\u12\n" in string`, 11, 0, true},
		{"1.27", "{\"a\":\"\\ud83d\\\n", `invalid escape sequence "\\\n" in string`, 14, 0, true},
		{"1.27", `{"a":nu ll}`, "invalid character ' ' in literal null (expecting 'l')", 8, 11, false},
		{"1.27", `{"a":1. 5}`, "invalid character ' ' in numeric literal", 8, 10, false},
		{"1.27", "{\"a\":\"hello\nworld\"}", `invalid character '\n' in string`, 12, 19, false},
		{"1.27", "{\"a\":\"x\\\nn\"}", `invalid escape sequence "\\\n" in string`, 9, 12, false},
		{"1.27", `{"a":"x\q"}`, "invalid escape sequence `\\q` in string", 9, 0, false},
		{"1.27", `{"a":1.x}`, "invalid character 'x' in numeric literal", 8, 0, false},
		// An escape broken before the SDK's newline names bytes the server sent.
		{"1.27", "{\"a\":\"\\uzz\n", `invalid escape sequence "\\uzz\n" in string`, 11, 0, false},
		{"1.27", "{\"a\":\"\\u12\\\n", `invalid escape sequence "\\u12\\\n" in string`, 12, 0, false},

		// An end of input past the payload is not this payload's.
		{"any", `{"a":`, "unexpected end of JSON input", 9, 5, false},
	}
	for _, tc := range cases {
		if got := endOfInputDiagnostic(tc.msg, tc.offset, tc.payloadLen); got != tc.want {
			t.Errorf("Go %s, %q: %q at offset %d of a payload of %d = %v, want %v",
				tc.release, tc.input, tc.msg, tc.offset, tc.payloadLen, got, tc.want)
		}
	}
}

// TestCodexStreamCutInsideJSONEvent pins issue #384 on the Codex path: a framed
// event whose JSON stops short is a truncation, not a raw decoder error. The
// delivered text and reasoning come back next to the error, no tool call of the
// unfinished answer is returned, the request is retried only while nothing
// reached the caller (Complete never emits, so it is retried), and the
// decoder's error stays reachable for logs. A malformed event that names a byte
// the server sent is not a cut and keeps the transport contract.
func TestCodexStreamCutInsideJSONEvent(t *testing.T) {
	text := func(w io.Writer, s string) { sse(w, "response.output_text.delta", map[string]any{"delta": s}) }
	reasoning := func(w io.Writer, s string) {
		sse(w, "response.reasoning_summary_text.delta", map[string]any{"delta": s})
	}
	named := func(w io.Writer) {
		sse(w, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{
			"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "run_command", "arguments": ""}})
	}
	finished := func(w io.Writer) {
		sse(w, "response.output_item.done", map[string]any{"item": map[string]any{
			"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "run_command", "arguments": `{"command":"ls"}`}})
	}
	const cutText = `{"type":"response.output_text.delta","del`
	type script func(w io.Writer)
	cases := []struct {
		name string
		// scripts: request n is served scripts[min(n, len-1)].
		scripts []script
		// complete calls Complete instead of Stream: nothing is emitted, so a
		// cut is retried.
		complete      bool
		wantErr       bool
		wantTruncated bool // else the decoder's error stays a plain transport-wrapped one
		wantRequests  int32
		wantResp      bool // a partial response next to the error
		wantContent   string
		wantReasoning string
		wantNamed     bool   // the callback saw the call announced before the cut
		wantToolChunk bool   // the callback saw a finished call (announce-only: never in the response)
		wantMsg       string // a fragment of the error text
	}{
		// The SDK decoder appends a newline to the data line, so the cut
		// inside the string is what the message names, verbatim up to the
		// wording of the Go release: "in string literal" up to 1.26, "in
		// string" from 1.27.
		{name: "cut before any event", scripts: []script{func(w io.Writer) { sseRaw(w, "response.output_text.delta", cutText) }},
			wantErr: true, wantTruncated: true, wantRequests: 2,
			wantMsg: `codex stream: stream truncated: an event arrived with incomplete JSON (invalid character '\n' in string`},
		{name: "cut after text and reasoning", scripts: []script{func(w io.Writer) {
			reasoning(w, "Thinking")
			text(w, "Hello")
			text(w, " fr")
			sseRaw(w, "response.output_text.delta", cutText)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantResp: true, wantContent: "Hello fr", wantReasoning: "Thinking"},
		{name: "reasoning only", scripts: []script{func(w io.Writer) {
			reasoning(w, "Thinking")
			sseRaw(w, "response.output_text.delta", cutText)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantResp: true, wantReasoning: "Thinking"},
		{name: "cut inside the arguments of a named call", scripts: []script{func(w io.Writer) {
			named(w)
			sse(w, "response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": `{"command":`})
			sseRaw(w, "response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","del`)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantNamed: true},
		{name: "cut inside the terminal event after a finished call", scripts: []script{func(w io.Writer) {
			text(w, "Let me look")
			named(w)
			finished(w)
			sseRaw(w, "response.completed", `{"type":"response.completed","response":{"status":"compl`)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantResp: true, wantContent: "Let me look", wantNamed: true, wantToolChunk: true},
		{name: "terminal event cut inside a number", scripts: []script{func(w io.Writer) {
			text(w, "Let me look")
			sseRaw(w, "response.completed", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1e`)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantResp: true, wantContent: "Let me look", wantMsg: "numeric literal)"},
		// A newline right after the backslash of an escape sequence: Go 1.27
		// names the sequence rather than the character (issue #610).
		{name: "cut inside an escape sequence", scripts: []script{func(w io.Writer) {
			text(w, "Let me look")
			sseRaw(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"line\`)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantResp: true, wantContent: "Let me look"},
		{name: "Complete retried after a cut before output", scripts: []script{
			func(w io.Writer) { text(w, "Hello"); sseRaw(w, "response.output_text.delta", cutText) },
			func(w io.Writer) { text(w, "Hello"); text(w, " world"); sseCompleted(w) },
		}, complete: true, wantRequests: 2, wantResp: true, wantContent: "Hello world"},
		{name: "Complete cut on every attempt", scripts: []script{
			func(w io.Writer) { text(w, "Hello"); sseRaw(w, "response.output_text.delta", cutText) },
		}, complete: true, wantErr: true, wantTruncated: true, wantRequests: 2, wantResp: true, wantContent: "Hello"},
		{name: "malformed event is not a cut", scripts: []script{func(w io.Writer) {
			text(w, "Hello")
			sseRaw(w, "response.output_text.delta", `{"type":"response.output_text.delta",,}`)
		}}, wantErr: true, wantRequests: 1, wantMsg: "invalid character ','"},
		// The SDK answers an event with a top-level "error" member without
		// decoding the frame ("received error while streaming: <payload>"): a
		// payload that ends inside its JSON is still a cut (issue #384).
		{name: "cut inside an in-band error event", scripts: []script{func(w io.Writer) {
			text(w, "Hello")
			sseRaw(w, "error", `{"error":{"code":502,"message":"oops`)
		}}, wantErr: true, wantTruncated: true, wantRequests: 1, wantResp: true, wantContent: "Hello",
			wantMsg: "received error while streaming:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := int(calls.Add(1)) - 1
				w.Header().Set("Content-Type", "text/event-stream")
				tc.scripts[min(n, len(tc.scripts)-1)](w)
			}))
			defer srv.Close()
			provider := applyResilientWrap(newCodexTestProvider(t, srv.URL), ProviderInput{
				RetryMax: 1, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
			})
			messages := []Message{{Role: RoleUser, Content: "hi"}}
			var named, toolChunks int
			var resp *Response
			var err error
			if tc.complete {
				resp, err = provider.Complete(context.Background(), messages, nil)
			} else {
				resp, err = provider.Stream(context.Background(), messages, nil, func(c StreamChunk) {
					if c.ToolCallNamed != nil {
						named++
					}
					if c.ToolCall != nil {
						toolChunks++
					}
				})
			}
			if got := calls.Load(); got != tc.wantRequests {
				t.Errorf("requests = %d, want %d", got, tc.wantRequests)
			}
			if (named > 0) != tc.wantNamed || (toolChunks > 0) != tc.wantToolChunk {
				t.Errorf("chunks: %d named, %d finished calls; want named=%v finished=%v", named, toolChunks, tc.wantNamed, tc.wantToolChunk)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("err = %v, want success", err)
				}
				if resp == nil || resp.Content != tc.wantContent || resp.StopReason != "end_turn" {
					t.Fatalf("resp = %+v, want %q with end_turn", resp, tc.wantContent)
				}
				return
			}
			if err == nil {
				t.Fatalf("succeeded with %+v, want an error", resp)
			}
			if IsStreamTruncated(err) != tc.wantTruncated {
				t.Errorf("IsStreamTruncated(%v) = %v, want %v", err, !tc.wantTruncated, tc.wantTruncated)
			}
			if IsTransientProviderError(err) != tc.wantTruncated {
				t.Errorf("IsTransientProviderError(%v) = %v, want %v", err, !tc.wantTruncated, tc.wantTruncated)
			}
			if !errors.As(err, new(*json.SyntaxError)) {
				t.Errorf("the decoder's error is not reachable through %v", err)
			}
			if got := httpStatusFromError(err); got != 0 {
				t.Errorf("httpStatusFromError = %d, want 0", got)
			}
			if got, want := isRetryableLLMError(err), tc.wantRequests == 2; got != want {
				t.Errorf("isRetryableLLMError = %v, want %v (%d requests)", got, want, tc.wantRequests)
			}
			if tc.wantTruncated && !strings.Contains(err.Error(), "stream truncated: an event arrived with incomplete JSON") {
				t.Errorf("error %q does not name the cut", err)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not carry %q", err, tc.wantMsg)
			}
			if !tc.wantResp {
				if resp != nil {
					t.Errorf("resp = %+v, want none", resp)
				}
				return
			}
			if resp == nil {
				t.Fatalf("no partial response next to %v", err)
			}
			if resp.Content != tc.wantContent || resp.Reasoning != tc.wantReasoning {
				t.Errorf("partial = %q / reasoning %q, want %q / %q", resp.Content, resp.Reasoning, tc.wantContent, tc.wantReasoning)
			}
			if len(resp.ToolCalls) != 0 || resp.StopReason != "" {
				t.Errorf("partial carries tool calls %+v and stop reason %q; an unfinished answer must run nothing", resp.ToolCalls, resp.StopReason)
			}
		})
	}
}

// openAIScriptStub serves request n the body scripts[min(n, len(scripts)-1)]
// and returns the provider behind the resilient wrapper (one retry,
// millisecond backoff), the request counter and the server's close func.
func openAIScriptStub(t *testing.T, scripts ...string) (Provider, *atomic.Int32, func()) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, scripts[min(n, len(scripts)-1)])
	}))
	p := applyResilientWrap(newOpenAIProvider("test-model", "", srv.URL, nil, 0, 0, ""), ProviderInput{
		RetryMax: 1, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
	})
	return p, calls, srv.Close
}

// Wire fragments of an OpenAI-compatible stream. A fragment ending in a blank
// line is a whole frame; one that does not is where the body ends.
const (
	oaiHel     = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n"
	oaiCut     = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo"
	oaiReason  = "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"Thinking\"}}]}\n\n"
	oaiTool    = "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"run_command\",\"arguments\":\"{\\\"command\\\":\"}}]}}]}\n\n"
	oaiToolCut = "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"l"
	oaiHTML    = "data: <html><title>502 Bad Gateway</title></html>\n\n"
	oaiNeedle  = "data: garbage unexpected EOF here\n\n"
	oaiNullCut = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":nu"
	// Cuts that Go 1.27 reports as the end of input at the start of the
	// unfinished token rather than at the end of the frame (issue #610).
	oaiNumberCut = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"logprobs\":{\"content\":[{\"token\":\"lo\",\"logprob\":-0."
	oaiEscapeCut = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\\"
	oaiRuneCut   = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\xd0"
	oaiDone      = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	// A complete frame whose JSON string is split across two data: lines:
	// the reader joins them with a newline, which is not a cut.
	oaiSplit = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\ndata: rem\"}}]}\n\n"
)

// TestOpenAIStreamFrameCutOrMalformed pins issue #384 on the OpenAI-compatible
// path, whose reader dispatches a last frame the server closed without a blank
// line: a frame cut inside its JSON is a truncation that keeps the delivered
// text and reasoning and returns no tool call, while a frame that is not JSON
// is final - never retried after output, whatever status or transport phrase
// its text holds, which is what used to replay the deltas the caller had
// already seen.
func TestOpenAIStreamFrameCutOrMalformed(t *testing.T) {
	cases := []struct {
		name           string
		body           string
		wantTruncated  bool
		wantResp       bool
		wantContent    string
		wantReasoning  string
		wantNamed      bool
		wantMsg        string
		wantTextChunks int
	}{
		{name: "cut tail", body: oaiHel + oaiCut, wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1,
			wantMsg: `stream truncated: an event arrived with incomplete JSON (undecodable SSE frame: {"choices":[{"index":0,"delta":{"content":"lo)`},
		{name: "reasoning only then cut", body: oaiReason + oaiCut, wantTruncated: true, wantResp: true, wantReasoning: "Thinking"},
		{name: "tool call in flight", body: oaiTool + oaiToolCut, wantTruncated: true, wantNamed: true},
		// The decoder's own text ("invalid character ' ' in literal null") is
		// behind Unwrap; the message carries the frame, which shows the cut.
		{name: "cut inside a null literal", body: oaiHel + oaiNullCut, wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1,
			wantMsg: `incomplete JSON (undecodable SSE frame: {"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":nu)`},
		{name: "cut inside a number", body: oaiHel + oaiNumberCut, wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1,
			wantMsg: `"logprob":-0.)`},
		{name: "cut inside an escape sequence", body: oaiHel + oaiEscapeCut, wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1},
		{name: "cut inside a multi-byte character", body: oaiHel + oaiRuneCut, wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1},
		{name: "HTML page after output", body: oaiHel + oaiHTML, wantTextChunks: 1,
			wantMsg: "openai stream: undecodable SSE frame: <html><title>502 Bad Gateway</title></html>"},
		{name: "HTML page before output", body: oaiHTML,
			wantMsg: "openai stream: undecodable SSE frame: <html><title>502 Bad Gateway</title></html>"},
		{name: "transport phrase inside a frame", body: oaiHel + oaiNeedle, wantTextChunks: 1,
			wantMsg: "undecodable SSE frame: garbage unexpected EOF here"},
		// gjson reports a complete "error" member even when the frame was cut
		// after it: only a payload that decodes whole is a real in-band error,
		// otherwise the frame's own classification applies (issue #384).
		{name: "cut inside an in-band error object", body: oaiHel + "data: {\"error\":{\"code\":502,\"message\":\"oops",
			wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1,
			wantMsg: `incomplete JSON (undecodable SSE frame: {"error":{"code":502,"message":"oops)`},
		{name: "cut after an in-band error object", body: oaiHel + "data: {\"error\":{\"code\":502,\"message\":\"oops\"},\"choices\":[",
			wantTruncated: true, wantResp: true, wantContent: "Hel", wantTextChunks: 1},
		// The decoder's diagnostic reads like a cut inside a string literal,
		// but the error is not at the end of the frame: malformed, final.
		{name: "JSON string split across two data lines", body: oaiHel + oaiSplit, wantTextChunks: 1,
			wantMsg: "openai stream: undecodable SSE frame: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\nrem\"}}]}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, calls, done := openAIScriptStub(t, tc.body)
			defer done()
			var named, toolChunks, textChunks int
			resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
				if c.ToolCallNamed != nil {
					named++
				}
				if c.ToolCall != nil {
					toolChunks++
				}
				if c.TextDelta != "" {
					textChunks++
				}
			})
			if err == nil {
				t.Fatalf("succeeded with %+v, want an error", resp)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1: the request must not be sent again", got)
			}
			if textChunks != tc.wantTextChunks {
				t.Errorf("text chunks = %d, want %d: delivered text must reach the caller once", textChunks, tc.wantTextChunks)
			}
			if (named > 0) != tc.wantNamed || toolChunks != 0 {
				t.Errorf("chunks: %d named, %d finished calls; want named=%v and no finished call", named, toolChunks, tc.wantNamed)
			}
			if IsStreamTruncated(err) != tc.wantTruncated || IsTransientProviderError(err) != tc.wantTruncated {
				t.Errorf("truncated=%v transient=%v for %v, want both %v", IsStreamTruncated(err), IsTransientProviderError(err), err, tc.wantTruncated)
			}
			if isRetryableLLMError(err) {
				t.Errorf("isRetryableLLMError(%v) = true; after output nothing is retried", err)
			}
			if got := httpStatusFromError(err); got != 0 {
				t.Errorf("httpStatusFromError(%v) = %d, want 0", err, got)
			}
			if !errors.As(err, new(*streamUndecodableError)) || !errors.As(err, new(*json.SyntaxError)) {
				t.Errorf("the frame and the decoder's error are not both reachable through %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not carry %q", err, tc.wantMsg)
			}
			if !tc.wantResp {
				if resp != nil {
					t.Errorf("resp = %+v, want none", resp)
				}
				return
			}
			if resp == nil {
				t.Fatalf("no partial response next to %v", err)
			}
			if resp.Content != tc.wantContent || resp.Reasoning != tc.wantReasoning {
				t.Errorf("partial = %q / reasoning %q, want %q / %q", resp.Content, resp.Reasoning, tc.wantContent, tc.wantReasoning)
			}
			if len(resp.ToolCalls) != 0 || resp.StopReason != "" {
				t.Errorf("partial carries tool calls %+v and stop reason %q", resp.ToolCalls, resp.StopReason)
			}
		})
	}
}

// TestOpenAIStreamCutFrameRetriedBeforeOutput: a frame cut before any delta
// reached the caller is worth the configured retry, and the second attempt's
// whole answer is what the caller gets.
func TestOpenAIStreamCutFrameRetriedBeforeOutput(t *testing.T) {
	p, calls, done := openAIScriptStub(t, "data: {\"choices\":[{\"index", oaiHel+oaiDone)
	defer done()
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream: %v (after %d requests)", err, calls.Load())
	}
	if resp.Content != "Hello" || resp.StopReason != "end_turn" || calls.Load() != 2 {
		t.Fatalf("content %q, stop %q after %d requests; want the retried whole answer after 2", resp.Content, resp.StopReason, calls.Load())
	}
}

// TestAnthropicStreamCutInsideJSONKeepsPartial mirrors the Codex contract on
// the anthropic path, which decodes its events with the same kind of SDK
// stream: an event whose JSON stops short is a truncation that keeps the
// delivered text, is not retried after it, and enters provider recovery.
func TestAnthropicStreamCutInsideJSONKeepsPartial(t *testing.T) {
	p, done := anthropicStreamStub(t, anthropicStreamPrefix+
		"event: content_block_delta\n"+
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"del\n\n")
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) || !IsTransientProviderError(err) {
		t.Fatalf("err = %v, want a stream truncation the loop recovers from", err)
	}
	if isRetryableLLMError(err) {
		t.Error("truncation after emitted deltas must not be retryable")
	}
	if !errors.As(err, new(*json.SyntaxError)) {
		t.Errorf("the decoder's error is not reachable through %v", err)
	}
	if resp == nil || resp.Content != "Paris" || len(resp.ToolCalls) != 0 || resp.StopReason != "" {
		t.Fatalf("resp = %+v, want partial content %q preserved with no tool call and no stop reason", resp, "Paris")
	}
}

// TestAnthropicStreamErrorEventCutInsideJSON: the anthropic SDK answers an
// event typed "error" with "received error while streaming: <data>" without
// decoding it, so a data line that ends inside its JSON carries no decoder
// error at all. The cut contract still applies: the delivered text is kept,
// the error names the truncation.
func TestAnthropicStreamErrorEventCutInsideJSON(t *testing.T) {
	p, done := anthropicStreamStub(t, anthropicStreamPrefix+
		"event: error\n"+
		"data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Over\n\n")
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) || !IsTransientProviderError(err) {
		t.Fatalf("err = %v, want a stream truncation the loop recovers from", err)
	}
	if isRetryableLLMError(err) {
		t.Error("a cut after emitted deltas must not be retried")
	}
	if resp == nil || resp.Content != "Paris" || len(resp.ToolCalls) != 0 || resp.StopReason != "" {
		t.Fatalf("resp = %+v, want partial content %q kept with no tool call and no stop reason", resp, "Paris")
	}
}

// TestAnthropicStreamInBandErrorRetry is the whole-payload twin of the cut
// event above (issue #537): the SDK hands over the complete event, so the
// overload is read from its decoded error object. Before any output it is
// retried like a 503 and the second attempt's answer is the caller's; after a
// delta it is not replayed, and the text stays next to an error the agent
// recovers from. An error event that is not an overload stays final.
func TestAnthropicStreamInBandErrorRetry(t *testing.T) {
	event := func(errType, message string) string {
		return "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"" + errType + "\",\"message\":\"" + message + "\"}}\n\n"
	}
	whole := anthropicStreamPrefix +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	// A stream that has delivered nothing yet: the opening events carry no text.
	const opening = "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n"

	cases := []struct {
		name          string
		scripts       []string
		wantRequests  int32
		wantErr       bool
		wantTransient bool
		wantContent   string
	}{
		{name: "overload before any output is retried and succeeds",
			scripts: []string{opening + event("overloaded_error", "Overloaded"), whole}, wantRequests: 2, wantContent: "Paris"},
		{name: "overload after a text delta is not replayed",
			scripts:      []string{anthropicStreamPrefix + event("overloaded_error", "Overloaded"), whole},
			wantRequests: 1, wantErr: true, wantTransient: true, wantContent: "Paris"},
		{name: "an error event that is not an overload is final",
			scripts:      []string{opening + event("invalid_request_error", "Overloaded"), whole},
			wantRequests: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := int(calls.Add(1)) - 1
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.scripts[min(n, len(tc.scripts)-1)])
			}))
			defer srv.Close()
			provider := applyResilientWrap(newAnthropicProvider("test-model", "k", srv.URL, nil, 0, 0, ""), ProviderInput{
				RetryMax: 2, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
			})

			var streamed strings.Builder
			resp, err := provider.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil,
				func(c StreamChunk) { streamed.WriteString(c.TextDelta) })
			if got := calls.Load(); got != tc.wantRequests {
				t.Errorf("requests = %d, want %d", got, tc.wantRequests)
			}
			if got := streamed.String(); got != tc.wantContent {
				t.Errorf("streamed text = %q, want %q: a retry must never show text twice", got, tc.wantContent)
			}
			if !tc.wantErr {
				if err != nil || resp == nil || resp.Content != "Paris" || resp.StopReason != "end_turn" {
					t.Fatalf("resp = %+v, err = %v, want the second attempt's whole answer", resp, err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), "anthropic stream: received error while streaming: ") {
				t.Fatalf("err = %v, want the in-band error the user saw before", err)
			}
			if got := IsTransientProviderError(err); got != tc.wantTransient {
				t.Errorf("IsTransientProviderError = %v, want %v", got, tc.wantTransient)
			}
		})
	}
}

// TestHTTPStatusFromErrorIgnoresStreamDecodeText: a truncation's cause and an
// undecodable frame carry server bytes, so their text is never scanned for a
// status the way an untyped message still is.
func TestHTTPStatusFromErrorIgnoresStreamDecodeText(t *testing.T) {
	var v any
	synCut := json.Unmarshal([]byte(`{"choices":[`), &v)
	frame := &streamUndecodableError{snippet: `{"choices":[{"delta":{"content":"HTTP 429 `, cause: synCut}
	for name, err := range map[string]error{
		"truncation with a status in its cause":  fmt.Errorf("codex stream: %w", &streamTruncatedError{cause: errors.New("code 502 ")}),
		"undecodable frame with a status":        fmt.Errorf("openai stream: %w", &streamUndecodableError{snippet: "<html><title>502 Bad Gateway</title></html>"}),
		"truncation over a frame naming a limit": fmt.Errorf("openai stream: %w", &streamTruncatedError{emitted: true, cause: frame}),
		"truncation without a cause":             fmt.Errorf("openai stream: %w", &streamTruncatedError{}),
	} {
		if got := httpStatusFromError(err); got != 0 {
			t.Errorf("%s: httpStatusFromError = %d, want 0", name, got)
		}
	}
	if got := httpStatusFromError(errors.New("openai stream: 502 Bad Gateway")); got != 502 {
		t.Errorf("an untyped message still yields its status: got %d, want 502", got)
	}
	// The truncation branch of the retry classification runs ahead of the
	// undecodable one: a frame cut before any delta is retried, the same
	// frame after output and the frame that is not JSON never are.
	cutBefore := fmt.Errorf("openai stream: %w", &streamTruncatedError{cause: frame})
	cutAfter := fmt.Errorf("openai stream: %w", &streamTruncatedError{emitted: true, cause: frame})
	if !isRetryableLLMError(cutBefore) || isRetryableLLMError(cutAfter) || isRetryableLLMError(fmt.Errorf("openai stream: %w", frame)) {
		t.Errorf("retry: before=%v after=%v frame=%v, want true/false/false",
			isRetryableLLMError(cutBefore), isRetryableLLMError(cutAfter), isRetryableLLMError(fmt.Errorf("openai stream: %w", frame)))
	}
	if !errors.As(cutAfter, new(*streamUndecodableError)) || !errors.As(cutAfter, new(*json.SyntaxError)) {
		t.Errorf("the frame and the decoder's error are not both reachable through %v", cutAfter)
	}
	if got, want := cutAfter.Error(), `openai stream: stream truncated: an event arrived with incomplete JSON (undecodable SSE frame: {"choices":[{"delta":{"content":"HTTP 429 )`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
