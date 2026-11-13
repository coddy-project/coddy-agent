//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type responsesStreamItem struct {
	kind, id, callID, name, text, status string
	done                                 bool
}

type responsesStream struct {
	mu        sync.Mutex
	writer    http.ResponseWriter
	cancel    context.CancelFunc
	sequence  int
	items     []*responsesStreamItem
	active    *responsesStreamItem
	calls     map[string]*responsesStreamItem
	lastWrite time.Time
	err       error
	writeErr  error
}

func newResponsesStream(w http.ResponseWriter, cancel context.CancelFunc) *responsesStream {
	return &responsesStream{writer: w, cancel: cancel, calls: make(map[string]*responsesStreamItem), lastWrite: time.Now()}
}

func (s *responsesStream) event(name string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventLocked(name, fields)
}

func (s *responsesStream) eventLocked(name string, fields map[string]any) {
	if s.writeErr != nil {
		return
	}
	fields["type"], fields["sequence_number"] = name, s.sequence
	raw, err := json.Marshal(fields)
	if err == nil {
		_, err = fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", name, raw)
	}
	if err != nil {
		s.err, s.writeErr = err, err
		s.cancel()
		return
	}
	s.sequence++
	s.lastWrite = time.Now()
	if f, ok := s.writer.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *responsesStream) keepalive(ctx context.Context) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(idleKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if s.err == nil && time.Since(s.lastWrite) >= idleKeepaliveInterval {
					if _, err := io.WriteString(s.writer, ": keepalive\n\n"); err != nil {
						s.err, s.writeErr = err, err
						s.cancel()
					} else {
						s.lastWrite = time.Now()
						if f, ok := s.writer.(http.Flusher); ok {
							f.Flush()
						}
					}
				}
				s.mu.Unlock()
			}
		}
	}()
	return func() { close(stop); <-done }
}

func (s *responsesStream) index(item *responsesStreamItem) int {
	for i, candidate := range s.items {
		if candidate == item {
			return i
		}
	}
	return -1
}

func (s *responsesStream) snapshot(item *responsesStreamItem, status string) map[string]any {
	switch item.kind {
	case "message":
		out := responsesMessageItem(item.id, item.text)
		out["status"] = status
		return out
	case "reasoning":
		return map[string]any{"id": item.id, "type": "reasoning", "summary": []map[string]any{{"type": "summary_text", "text": item.text}}}
	default:
		return map[string]any{"id": item.id, "type": "function_call", "call_id": item.callID, "name": item.name, "arguments": item.text, "status": status}
	}
}

func (s *responsesStream) add(kind, callID, name string) *responsesStreamItem {
	prefix := "msg_"
	switch kind {
	case "reasoning":
		prefix = "rs_"
	case "function_call":
		prefix = "fc_"
	}
	item := &responsesStreamItem{kind: kind, id: responsesItemID(prefix), callID: callID, name: name}
	s.items = append(s.items, item)
	snapshot := s.snapshot(item, "in_progress")
	switch kind {
	case "message":
		snapshot["content"] = []any{}
	case "reasoning":
		snapshot["summary"] = []any{}
	}
	s.eventLocked("response.output_item.added", map[string]any{"output_index": s.index(item), "item": snapshot})
	switch kind {
	case "message":
		s.eventLocked("response.content_part.added", map[string]any{"item_id": item.id, "output_index": s.index(item), "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	case "reasoning":
		s.eventLocked("response.reasoning_summary_part.added", map[string]any{"item_id": item.id, "output_index": s.index(item), "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}})
	}
	return item
}

func (s *responsesStream) text(kind, delta string) {
	if delta == "" {
		return
	}
	if s.active == nil || s.active.kind != kind || s.active.done {
		s.closeItem(s.active, "completed")
		s.active = s.add(kind, "", "")
	}
	item := s.active
	item.text += delta
	fields := map[string]any{"item_id": item.id, "output_index": s.index(item), "delta": delta}
	name := "response.output_text.delta"
	if kind == "reasoning" {
		name = "response.reasoning_summary_text.delta"
		fields["summary_index"] = 0
	} else {
		fields["content_index"] = 0
	}
	s.eventLocked(name, fields)
}

func (s *responsesStream) call(call llm.ToolCall) *responsesStreamItem {
	if item := s.calls[call.ID]; item != nil {
		return item
	}
	s.closeItem(s.active, "completed")
	s.active = nil
	item := s.add("function_call", call.ID, call.Name)
	s.calls[call.ID] = item
	return item
}

func (s *responsesStream) arguments(item *responsesStreamItem, delta string) {
	if delta == "" {
		return
	}
	if item.done {
		s.err = errors.New("provider changed streamed output")
		s.cancel()
		return
	}
	item.text += delta
	s.eventLocked("response.function_call_arguments.delta", map[string]any{"item_id": item.id, "output_index": s.index(item), "delta": delta})
}

// reconcile sends only the suffix a non-streaming backend or buffered tool
// assembly has not sent yet. Contradictory final data is not silently appended.
func (s *responsesStream) reconcile(item *responsesStreamItem, text string) {
	if item.text == text {
		return
	}
	if item.done || !strings.HasPrefix(text, item.text) {
		s.err = errors.New("provider changed streamed output")
		s.cancel()
		return
	}
	suffix := strings.TrimPrefix(text, item.text)
	if item.kind == "function_call" {
		s.arguments(item, suffix)
	} else {
		s.text(item.kind, suffix)
	}
}

func (s *responsesStream) chunk(chunk llm.StreamChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	s.text("reasoning", chunk.ReasoningDelta)
	if s.err != nil {
		return
	}
	s.text("message", chunk.TextDelta)
	if s.err != nil {
		return
	}
	if chunk.ToolCallNamed != nil {
		s.call(*chunk.ToolCallNamed)
	}
	if s.err != nil {
		return
	}
	if chunk.ToolCallDelta != nil {
		call := chunk.ToolCallDelta
		s.arguments(s.call(*call), call.InputJSON)
	}
	if s.err != nil {
		return
	}
	if chunk.ToolCall != nil {
		call := chunk.ToolCall
		item := s.call(*call)
		s.reconcile(item, call.InputJSON)
		if s.err == nil {
			s.closeItem(item, "completed")
		}
	}
}

func (s *responsesStream) closeItem(item *responsesStreamItem, status string) {
	if item == nil || item.done {
		return
	}
	item.done = true
	item.status = status
	fields := map[string]any{"item_id": item.id, "output_index": s.index(item)}
	switch item.kind {
	case "message":
		fields["content_index"], fields["text"] = 0, item.text
		s.eventLocked("response.output_text.done", fields)
		s.eventLocked("response.content_part.done", map[string]any{"item_id": item.id, "output_index": s.index(item), "content_index": 0, "part": map[string]any{"type": "output_text", "text": item.text, "annotations": []any{}}})
	case "reasoning":
		fields["summary_index"], fields["text"] = 0, item.text
		s.eventLocked("response.reasoning_summary_text.done", fields)
		s.eventLocked("response.reasoning_summary_part.done", map[string]any{"item_id": item.id, "output_index": s.index(item), "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": item.text}})
	case "function_call":
		fields["arguments"], fields["name"] = item.text, item.name
		s.eventLocked("response.function_call_arguments.done", fields)
	}
	s.eventLocked("response.output_item.done", map[string]any{"output_index": s.index(item), "item": s.snapshot(item, status)})
}

func (s *responsesStream) finish(response *llm.Response, callErr error) ([]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if response != nil && callErr == nil && s.err == nil {
		for _, part := range []struct{ kind, text string }{{"reasoning", response.Reasoning}, {"message", response.Content}} {
			if s.err != nil {
				break
			}
			if part.text == "" {
				continue
			}
			var sent strings.Builder
			for _, item := range s.items {
				if item.kind == part.kind {
					sent.WriteString(item.text)
				}
			}
			if !strings.HasPrefix(part.text, sent.String()) {
				s.err = errors.New("provider changed streamed output")
				s.cancel()
				break
			}
			s.text(part.kind, strings.TrimPrefix(part.text, sent.String()))
		}
		for _, call := range response.ToolCalls {
			if s.err != nil {
				break
			}
			item := s.call(call)
			s.reconcile(item, call.InputJSON)
		}
		if s.err == nil && len(s.items) == 0 {
			s.active = s.add("message", "", "")
		}
	}
	status, _ := responsesStatus(response)
	itemStatus := "completed"
	if status == "incomplete" || callErr != nil || s.err != nil {
		itemStatus = "incomplete"
	}
	output := make([]any, 0, len(s.items))
	for _, item := range s.items {
		s.closeItem(item, itemStatus)
		output = append(output, s.snapshot(item, item.status))
	}
	return output, s.err
}
