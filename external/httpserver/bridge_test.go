//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestForwardTextChunk_ReasoningEmittedAsReasoningContent(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: "silent plan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"reasoning_content":"silent plan"`) {
		t.Fatalf("expected reasoning_content in SSE body, got: %s", raw)
	}
	if strings.Contains(raw, `"content":"silent plan"`) {
		t.Fatalf("reasoning must not map to delta.content, got: %s", raw)
	}
	var payload map[string]interface{}
	idx := strings.Index(raw, "{")
	if idx < 0 {
		t.Fatal("no json in response")
	}
	jsonLine := raw[idx:]
	if nl := strings.IndexByte(jsonLine, '\n'); nl >= 0 {
		jsonLine = jsonLine[:nl]
	}
	if err := json.Unmarshal([]byte(jsonLine), &payload); err != nil {
		t.Fatal(err)
	}
	choices, _ := payload["choices"].([]interface{})
	ch0 := choices[0].(map[string]interface{})
	delta := ch0["delta"].(map[string]interface{})
	if delta["reasoning_content"] != "silent plan" {
		t.Fatalf("delta: %#v", delta)
	}
	if _, has := delta["content"]; has {
		t.Fatalf("reasoning chunk should omit content field, delta=%#v", delta)
	}
}

func TestRequestQuestionSSECompletesWhenPosted(t *testing.T) {
	rec := &syncBuffer{} // polled while the question goroutine writes
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	ctx := context.Background()
	p := acp.QuestionRequestParams{
		SessionID: "s1",
		RequestID: "r1",
		Questions: []acp.QuestionPrompt{{Question: "x", Options: []acp.QuestionOption{{Label: "y"}}}},
	}
	done := make(chan error, 1)
	var got *acp.QuestionResult
	go func() {
		r, err := sender.RequestQuestion(ctx, p)
		if err != nil {
			done <- err
			return
		}
		got = r
		done <- nil
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), "event: question") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if ok := CompleteQuestionAnswer("s1", "r1", &acp.QuestionResult{Answers: [][]string{{"y"}}}); !ok {
		t.Fatal("CompleteQuestionAnswer failed")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Answers) != 1 || len(got.Answers[0]) != 1 || got.Answers[0][0] != "y" {
		t.Fatalf("unexpected result %#v", got)
	}
}

func TestRequestPermissionSSECompletesWhenPosted(t *testing.T) {
	// The permission goroutine writes the SSE frame while the test polls for
	// it, so the output goes through the synchronised buffer.
	rec := &syncBuffer{}
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	ctx := context.Background()
	p := acp.PermissionRequestParams{
		SessionID: "s1",
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_perm_1",
			Title:      "Run: run_command",
			Kind:       "run_command",
			Status:     "pending",
			Content: []acp.ToolCallResultItem{
				{Type: "content", Content: acp.ContentBlock{Type: "text", Text: "Execute: echo hi"}},
			},
		},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	}
	done := make(chan error, 1)
	var got *acp.PermissionResult
	go func() {
		r, err := sender.RequestPermission(ctx, p)
		if err != nil {
			done <- err
			return
		}
		got = r
		done <- nil
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), "event: permission") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if ok := CompletePermissionAnswer("s1", "call_perm_1", &acp.PermissionResult{
		Outcome:  "allow",
		OptionID: "allow",
	}); !ok {
		t.Fatal("CompletePermissionAnswer failed")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Outcome != "allow" || got.OptionID != "allow" {
		t.Fatalf("unexpected result %#v", got)
	}
}

func TestRequestPermissionDeniesWhenNotStreaming(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, false, "agent-model")
	got, err := sender.RequestPermission(context.Background(), acp.PermissionRequestParams{
		SessionID: "s1",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "c1", Status: "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "cancelled" || got.OptionID != "reject" {
		t.Fatalf("expected deny, got %#v", got)
	}
}

func TestForwardTextChunk_TextUsesContentDelta(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"content":"hello"`) {
		t.Fatalf("expected content in SSE body, got: %s", raw)
	}
	if strings.Contains(raw, "reasoning_content") {
		t.Fatalf("text chunk must not set reasoning_content, got: %s", raw)
	}
}

// A subagent's forwarded request carries the child's own effective mode; the
// bridge decides its bypass short-circuit from that stamp, not from the
// global setting, and denies a narrowed child when nobody can answer.
func TestRequestPermissionHonoursTheStampedEffectiveMode(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.PermissionMode = config.PermModeBypass
	params := func(mode string) acp.PermissionRequestParams {
		return acp.PermissionRequestParams{
			SessionID:               "s1",
			ToolCall:                acp.PermissionToolCall{ToolCallID: "c1", Title: "[subagent writer] Run: run_command", Status: "pending"},
			EffectivePermissionMode: mode,
		}
	}
	nonInteractive := NewSender(cfg, httptest.NewRecorder(), false, "agent-model")
	if got, _ := nonInteractive.RequestPermission(context.Background(), params("")); got.OptionID != "allow" {
		t.Fatalf("unstamped request under global bypass = %#v, want allow", got)
	}
	if got, _ := nonInteractive.RequestPermission(context.Background(), params(config.PermModeBypass)); got.OptionID != "allow" {
		t.Fatalf("stamped bypass = %#v, want allow", got)
	}
	if got, _ := nonInteractive.RequestPermission(context.Background(), params(config.PermModeAsk)); got.OptionID != "reject" || got.Outcome != "cancelled" {
		t.Fatalf("stamped ask with nobody to answer = %#v, want a denial", got)
	}

	// Interactive: the stamped ask goes out as a permission event and waits
	// for the answer instead of being auto-allowed.
	out := &syncBuffer{}
	interactive := NewSender(cfg, out, true, "agent-model")
	done := make(chan *acp.PermissionResult, 1)
	go func() {
		r, _ := interactive.RequestPermission(context.Background(), params(config.PermModeAsk))
		done <- r
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "event: permission") {
		time.Sleep(2 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "event: permission") {
		t.Fatal("a stamped ask under global bypass must be forwarded as a permission event")
	}
	if !CompletePermissionAnswer("s1", "c1", &acp.PermissionResult{Outcome: "selected", OptionID: "reject"}) {
		t.Fatal("CompletePermissionAnswer failed")
	}
	if got := <-done; got == nil || got.OptionID != "reject" {
		t.Fatalf("interactive answer = %#v, want the operator's reject", got)
	}
}

// A relayed subagent prompt (stamped with the child's mode) is answered live
// or not at all: it never becomes the parent's pending permission record, so
// it cannot be resumed later and cannot evict the parent's own gate.
func TestRequestPermissionDoesNotPersistARelayedPrompt(t *testing.T) {
	dir := t.TempDir()
	out := &syncBuffer{}
	sender := NewSender(&config.Config{}, out, true, "agent-model")
	sender.SetSessionDir(dir)
	relayed := acp.PermissionRequestParams{
		SessionID:               "s1",
		ToolCall:                acp.PermissionToolCall{ToolCallID: "child-1", Title: "[subagent explore] Run: run_command", Status: "pending"},
		EffectivePermissionMode: config.PermModeAsk,
	}
	done := make(chan *acp.PermissionResult, 1)
	go func() {
		r, _ := sender.RequestPermission(context.Background(), relayed)
		done <- r
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "event: permission") {
		time.Sleep(2 * time.Millisecond)
	}
	if session.PendingPermissionHeld(dir) {
		t.Fatal("a relayed prompt must not write pending_permission.json")
	}
	if !CompletePermissionAnswer("s1", "child-1", &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}) {
		t.Fatal("the in-memory wait must still accept the answer")
	}
	if got := <-done; got == nil || got.OptionID != "allow" {
		t.Fatalf("answer = %#v", got)
	}

	// The parent's own prompt still leaves its record while it waits.
	own := acp.PermissionRequestParams{
		SessionID: "s1",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "own-1", Title: "Run: run_command", Status: "pending"},
	}
	go func() {
		r, _ := sender.RequestPermission(context.Background(), own)
		done <- r
	}()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !session.PendingPermissionHeld(dir) {
		time.Sleep(2 * time.Millisecond)
	}
	if !session.PendingPermissionHeld(dir) {
		t.Fatal("the parent's own prompt must be recorded")
	}
	if !CompletePermissionAnswer("s1", "own-1", &acp.PermissionResult{Outcome: "selected", OptionID: "reject"}) {
		t.Fatal("CompletePermissionAnswer failed")
	}
	<-done
}
