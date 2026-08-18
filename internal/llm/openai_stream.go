package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/tidwall/gjson"
)

// OpenAI-compatible servers deviate from the OpenAI SSE shape in ways the
// openai-go decoder does not tolerate: llama.cpp builds through 2025 report
// mid-stream failures with a non-standard "error:" SSE field, and servers or
// proxies may interleave keep-alive comments and empty data frames. The SDK
// decoder turns every such frame into an empty JSON document and aborts the
// whole stream with "unexpected end of JSON input", hiding the real server
// message (issue #104). This file owns the lenient SSE path used instead:
// the request still goes through the SDK client (auth, base URL, params
// encoding, HTTP error mapping), but frames are decoded here.

// sseFrame is one server-sent event accumulated from the wire.
type sseFrame struct {
	// data is the newline-joined payload of "data:" lines.
	data []byte
	// errData is the payload of the non-standard "error:" field
	// (llama.cpp <= 2025 mid-stream errors).
	errData []byte
}

// sseMaxScanSize mirrors the openai-go decoder buffer cap (32 MiB).
const sseMaxScanSize = bufio.MaxScanTokenSize << 9

// sseScanner reads SSE frames leniently: comments are dropped, the llama.cpp
// "error:" field is preserved, and frames with no payload are skipped instead
// of being dispatched as empty JSON documents.
type sseScanner struct {
	scn   *bufio.Scanner
	cur   sseFrame
	err   error
	first bool
}

func newSSEScanner(r io.Reader) *sseScanner {
	scn := bufio.NewScanner(r)
	scn.Buffer(nil, sseMaxScanSize)
	return &sseScanner{scn: scn, first: true}
}

// Next advances to the next non-empty frame. It returns false at end of
// stream or on transport error (see Err).
func (s *sseScanner) Next() bool {
	if s.err != nil {
		return false
	}
	var data, errData bytes.Buffer
	flush := func() bool {
		if data.Len() == 0 && errData.Len() == 0 {
			// Nothing accumulated: comment-only frame or a run of blank
			// lines. Skip instead of dispatching an empty document.
			return false
		}
		s.cur = sseFrame{data: data.Bytes(), errData: errData.Bytes()}
		return true
	}
	for s.scn.Scan() {
		line := s.scn.Bytes()
		if s.first {
			// The EventSource spec strips one leading U+FEFF BOM.
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
			s.first = false
		}
		if len(line) == 0 {
			if flush() {
				return true
			}
			data.Reset()
			errData.Reset()
			continue
		}
		name, value, _ := bytes.Cut(line, []byte(":"))
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch string(name) {
		case "": // comment line (": keep-alive")
		case "data":
			data.Write(value)
			data.WriteByte('\n')
		case "error":
			errData.Write(value)
			errData.WriteByte('\n')
		}
	}
	s.err = s.scn.Err()
	// A final frame not terminated by a blank line is still dispatched, like
	// browsers do, so a server that closes right after the last payload is
	// not silently truncated.
	if s.err == nil && flush() {
		return true
	}
	return false
}

func (s *sseScanner) Frame() sseFrame { return s.cur }

func (s *sseScanner) Err() error { return s.err }

// streamErrorSnippetLimit bounds how much of an undecodable frame is carried
// into the returned error for diagnostics.
const streamErrorSnippetLimit = 2048

func streamErrorSnippet(payload []byte) string {
	s := strings.TrimSpace(string(payload))
	if len(s) <= streamErrorSnippetLimit {
		return s
	}
	cut := streamErrorSnippetLimit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// streamServerError is a failure the server reported inside an SSE stream.
// It keeps the HTTP-style status code structurally so retry classification
// (httpStatusFromError) treats streamed errors like their pre-stream
// equivalents: 5xx retryable, 4xx not. emitted records whether chunks were
// already delivered to the caller before the failure: retrying such a call
// would stream the same deltas twice, so classification refuses the code.
type streamServerError struct {
	code    int
	msg     string
	emitted bool
}

func (e *streamServerError) Error() string {
	if e.code > 0 {
		return fmt.Sprintf("server error %d: %s", e.code, e.msg)
	}
	return "server error: " + e.msg
}

// newStreamServerError parses a server-reported streaming failure. obj is
// the error object ({"code":400,"message":"...","type":"..."} for llama.cpp)
// or any other payload the server put in the error frame.
func newStreamServerError(obj []byte, emitted bool) error {
	msg := gjson.GetBytes(obj, "message").String()
	if msg == "" {
		msg = streamErrorSnippet(obj)
	}
	return &streamServerError{code: int(gjson.GetBytes(obj, "code").Int()), msg: msg, emitted: emitted}
}

// Stream implements Provider.Stream over the lenient SSE path.
func (p *openAIProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	params := p.buildParams(messages, tools, true)

	var raw *http.Response
	err := p.client.Post(ctx, "chat/completions", params, &raw, option.WithJSONSet("stream", true))
	if err != nil {
		return nil, fmt.Errorf("openai stream: %w", err)
	}
	defer func() { _ = raw.Body.Close() }()

	var fullContent string
	var toolCalls []ToolCall
	var stopReason string
	var inputTokens, outputTokens int

	// Accumulate tool call deltas by index.
	type tcBuilder struct {
		id   string
		name string
		args string
	}
	builders := make(map[int]*tcBuilder)

	finalizeOpenAIToolBuilders := func() []ToolCall {
		var out []ToolCall
		for i := 0; i < len(builders); i++ {
			b, ok := builders[i]
			if !ok {
				continue
			}
			out = append(out, ToolCall{ID: b.id, Name: b.name, InputJSON: b.args})
		}
		return out
	}

	// emitted flips once any chunk reached the caller; a retry after that
	// would deliver the same deltas twice, so streamed server errors carry
	// it to disable retry classification.
	var emitted bool
	emit := func(c StreamChunk) {
		emitted = true
		onChunk(c)
	}

	scanner := newSSEScanner(raw.Body)
	var streamErr error
	var done bool

	for scanner.Next() {
		frame := scanner.Frame()

		if payload := bytes.TrimSpace(frame.errData); len(payload) > 0 {
			// llama.cpp <= 2025 mid-stream error frame ("error: {...}").
			streamErr = newStreamServerError(payload, emitted)
			break
		}

		payload := bytes.TrimSpace(frame.data)
		if len(payload) == 0 {
			continue
		}
		if bytes.HasPrefix(payload, []byte("[DONE]")) {
			done = true
			continue
		}
		if done {
			continue
		}
		if e := gjson.GetBytes(payload, "error"); e.Exists() && e.Type != gjson.Null {
			// Standard-shaped in-band error object (llama.cpp b9038+, gateways).
			streamErr = newStreamServerError([]byte(e.Raw), emitted)
			break
		}

		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			// A non-empty data frame that is not JSON means the stream is
			// corrupt; failing silently here would truncate the response.
			// Carry the payload so the operator sees what the server sent.
			streamErr = fmt.Errorf("undecodable SSE frame: %s", streamErrorSnippet(payload))
			break
		}

		if len(chunk.Choices) == 0 {
			if chunk.Usage.TotalTokens > 0 {
				inputTokens = int(chunk.Usage.PromptTokens)
				outputTokens = int(chunk.Usage.CompletionTokens)
			}
			continue
		}
		choice := chunk.Choices[0]

		if choice.FinishReason != "" {
			stopReason = mapOpenAIStopReason(string(choice.FinishReason))
		}

		delta := choice.Delta

		if delta.Content != "" {
			fullContent += delta.Content
			emit(StreamChunk{TextDelta: delta.Content})
		}

		if raw := delta.RawJSON(); raw != "" {
			r := gjson.Get(raw, "reasoning_content").String()
			if r == "" {
				r = gjson.Get(raw, "thinking").String()
			}
			if r != "" {
				emit(StreamChunk{ReasoningDelta: r})
			}
		}

		for _, tc := range delta.ToolCalls {
			idx := int(tc.Index)
			if _, ok := builders[idx]; !ok {
				builders[idx] = &tcBuilder{}
			}
			b := builders[idx]
			if tc.ID != "" {
				b.id = tc.ID
			}
			if tc.Function.Name != "" {
				b.name = tc.Function.Name
			}
			b.args += tc.Function.Arguments
		}

		if chunk.Usage.TotalTokens > 0 {
			inputTokens = int(chunk.Usage.PromptTokens)
			outputTokens = int(chunk.Usage.CompletionTokens)
		}
	}

	if streamErr == nil {
		streamErr = scanner.Err()
	}

	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			toolCalls = finalizeOpenAIToolBuilders()
			if strings.TrimSpace(fullContent) != "" || len(toolCalls) > 0 {
				sr := stopReason
				if sr == "" {
					if len(toolCalls) > 0 {
						sr = "tool_use"
					} else {
						sr = "end_turn"
					}
				}
				return &Response{
					Content:      fullContent,
					ToolCalls:    toolCalls,
					StopReason:   sr,
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				}, fmt.Errorf("openai stream: %w", streamErr)
			}
		}
		return nil, fmt.Errorf("openai stream: %w", streamErr)
	}

	toolCalls = finalizeOpenAIToolBuilders()
	for i := range toolCalls {
		tc := toolCalls[i]
		emit(StreamChunk{ToolCall: &tc})
	}

	if stopReason == "" {
		if len(toolCalls) > 0 {
			stopReason = "tool_use"
		} else {
			stopReason = "end_turn"
		}
	}

	return &Response{
		Content:      fullContent,
		ToolCalls:    toolCalls,
		StopReason:   stopReason,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}
