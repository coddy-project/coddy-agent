//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The strict OpenAI view of a coddy turn.
//
// The SSE bridge speaks one stream for every surface: OpenAI-shaped text chunks
// plus the named event: frames the SPA, the remote console and the swarm relay
// read (tool_call, token_usage, coddy_meta, ...). POST /v1/chat/completions is
// the one surface whose reader is not ours - VS Code Copilot wired in through
// chatLanguageModels.json, the openai SDKs - and those parse chat.completion.chunk
// literally: a completion ends on the chunk that carries finish_reason, and every
// data: line is expected to be a chunk. The bridge never sent the former, and the
// named events were read as chunks with no choices at all (issue #183).
//
// openAIStreamFilter sits between the bridge and that client's socket and renders
// the same turn as the contract those clients implement. It is a view, not a mode:
// the bridge still writes the whole coddy stream, so a relay behind a teeSSEWriter -
// and every watcher on it - sees exactly what it saw before.
type openAIStreamFilter struct {
	http.ResponseWriter

	// Identity of the completion, learned from the first chunk that passes and
	// used for the chunks the filter writes on its own.
	chatID  string
	model   string
	created int64

	// includeUsage mirrors stream_options.include_usage on the request.
	includeUsage bool

	// pending holds bytes that do not yet form a whole frame.
	pending bytes.Buffer
	// opened is set once the assistant role chunk has been written.
	opened bool
	// done is set once [DONE] has been forwarded; nothing follows it.
	done bool
	// stopReason is the ACP stop reason read off coddy_meta, "" until then.
	stopReason string
	// usage is the last token_usage event seen, nil when the turn reported none.
	usage *acp.TokenUsageUpdate
}

// newOpenAIStreamFilter wraps the client's response writer. model is the id the
// request named; the completion id and timestamp are taken from the bridge's own
// chunks as they pass, these are the fallback for a turn that produced none.
func newOpenAIStreamFilter(w http.ResponseWriter, model string, includeUsage bool) *openAIStreamFilter {
	return &openAIStreamFilter{
		ResponseWriter: w,
		chatID:         newChatID(),
		model:          model,
		created:        time.Now().Unix(),
		includeUsage:   includeUsage,
	}
}

// Write consumes bridge bytes and emits the client's frames. The bridge writes one
// frame per call today, but the filter never relies on it: frames are cut on the
// blank line that ends them, whatever the write boundaries.
func (f *openAIStreamFilter) Write(p []byte) (int, error) {
	if f.done {
		return len(p), nil
	}
	f.pending.Write(p)
	for {
		raw := f.pending.Bytes()
		end := bytes.Index(raw, []byte("\n\n"))
		if end < 0 {
			return len(p), nil
		}
		frame := string(raw[:end])
		f.pending.Next(end + 2)
		if err := f.frame(frame); err != nil {
			return 0, err
		}
		if f.done {
			return len(p), nil
		}
	}
}

// Flush lets the bridge flush after every frame, the way it does on a bare
// ResponseWriter.
func (f *openAIStreamFilter) Flush() {
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

var _ http.Flusher = (*openAIStreamFilter)(nil)

// frame decides what one bridge frame becomes on the client's socket.
func (f *openAIStreamFilter) frame(frame string) error {
	frame = strings.TrimRight(frame, "\r\n")
	if frame == "" {
		return nil
	}
	if strings.HasPrefix(frame, ":") {
		// A keepalive comment; every SSE parser skips it, and it is what holds the
		// connection open through a long silent generation.
		return f.emit(frame)
	}
	event, data := splitSSEFrame(frame)
	if event != "" {
		f.named(event, data)
		return nil
	}
	if data == "[DONE]" {
		if err := f.finish(); err != nil {
			return err
		}
		f.done = true
		return f.emit("data: [DONE]")
	}
	if gjson.Get(data, "error").Exists() {
		// An OpenAI client surfaces this as an API error; it is the one non-chunk
		// data frame the contract allows.
		return f.emit("data: " + data)
	}
	if !gjson.Get(data, "choices").IsArray() {
		// Nothing an OpenAI client can do with a data frame that is not a chunk.
		return nil
	}
	return f.chunk(data)
}

// named records what the terminal chunks need out of coddy's own events and drops
// the frame: coddy_meta carries the ACP stop reason, token_usage the counters.
func (f *openAIStreamFilter) named(event, data string) {
	switch event {
	case "coddy_meta":
		f.stopReason = gjson.Get(data, "metadata.stop_reason").String()
	case "token_usage":
		var u acp.TokenUsageUpdate
		if err := json.Unmarshal([]byte(data), &u); err == nil {
			f.usage = &u
		}
	}
}

// chunk forwards one chat.completion.chunk, opening the message first and making
// sure every choice carries finish_reason (null while the message is streaming).
func (f *openAIStreamFilter) chunk(data string) error {
	if id := gjson.Get(data, "id").String(); id != "" {
		f.chatID = id
	}
	if model := gjson.Get(data, "model").String(); model != "" {
		f.model = model
	}
	if created := gjson.Get(data, "created").Int(); created > 0 {
		f.created = created
	}
	if err := f.open(); err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return f.emit("data: " + data)
	}
	choices, _ := payload["choices"].([]any)
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if _, has := choice["finish_reason"]; !has {
			choice["finish_reason"] = nil
		}
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return f.emit("data: " + string(line))
}

// open writes the chunk that starts the assistant message. OpenAI sends it first,
// before any content, and a client that never sees a choice at all has nothing to
// finish - so an empty turn still opens and closes one.
func (f *openAIStreamFilter) open() error {
	if f.opened {
		return nil
	}
	f.opened = true
	return f.emitChunk(map[string]any{"role": "assistant", "content": ""}, nil)
}

// finish writes what precedes [DONE]: the chunk that finishes the choice, then the
// usage chunk when the client asked for one.
func (f *openAIStreamFilter) finish() error {
	if err := f.open(); err != nil {
		return err
	}
	if err := f.emitChunk(map[string]any{}, openAIFinishReason(f.stopReason)); err != nil {
		return err
	}
	if !f.includeUsage {
		return nil
	}
	usage := map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	if f.usage != nil {
		usage["prompt_tokens"] = f.usage.InputTokens
		usage["completion_tokens"] = f.usage.OutputTokens
		usage["total_tokens"] = f.usage.TotalTokens
	}
	line, err := json.Marshal(map[string]any{
		"id":      f.chatID,
		"object":  "chat.completion.chunk",
		"created": f.created,
		"model":   f.model,
		"choices": []any{},
		"usage":   usage,
	})
	if err != nil {
		return err
	}
	return f.emit("data: " + string(line))
}

// emitChunk writes one chunk of the filter's own with a single choice.
func (f *openAIStreamFilter) emitChunk(delta map[string]any, finish any) error {
	line, err := json.Marshal(map[string]any{
		"id":      f.chatID,
		"object":  "chat.completion.chunk",
		"created": f.created,
		"model":   f.model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	})
	if err != nil {
		return err
	}
	return f.emit("data: " + string(line))
}

func (f *openAIStreamFilter) emit(frame string) error {
	_, err := f.ResponseWriter.Write([]byte(frame + "\n\n"))
	return err
}

// splitSSEFrame returns the event name (empty for the default event) and the
// joined data lines of one frame.
func splitSSEFrame(frame string) (event, data string) {
	var lines []string
	for _, line := range strings.Split(frame, "\n") {
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return event, strings.Join(lines, "\n")
}

// openAIFinishReason maps the ACP stop reason of a turn to the finish_reason an
// OpenAI client understands. A direct completion carries none and finished
// normally; a turn cut by its budget is what OpenAI calls length.
func openAIFinishReason(stop string) string {
	switch acp.StopReason(stop) {
	case acp.StopReasonMaxTokens, acp.StopReasonMaxTurns:
		return "length"
	default:
		return "stop"
	}
}
