//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

// The strict OpenAI view on POST /v1/chat/completions (openai_stream.go). The
// happy path is features/openai_stream_compat.feature; these are the boundaries a
// third-party parser meets: split writes, an empty turn, an error frame, a turn
// cut by its budget, and what the relay behind the tee keeps seeing.

// openAIFilterFrames runs raw bridge bytes through the filter and returns the
// frames the client received.
func openAIFilterFrames(t *testing.T, includeUsage bool, writes ...string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	f := newOpenAIStreamFilter(rec, "local/m", includeUsage)
	for _, w := range writes {
		if _, err := f.Write([]byte(w)); err != nil {
			t.Fatal(err)
		}
	}
	var frames []string
	for _, fr := range strings.Split(rec.Body.String(), "\n\n") {
		if fr != "" {
			frames = append(frames, fr)
		}
	}
	return frames
}

func TestOpenAIStreamFilter_FramesSurviveArbitraryWriteBoundaries(t *testing.T) {
	chunk := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":7,"model":"local/m","choices":[{"index":0,"delta":{"content":"Hi"}}]}` + "\n\n"
	whole := chunk + "event: token_usage\ndata: {\"sessionUpdate\":\"token_usage\",\"inputTokens\":3,\"outputTokens\":2,\"totalTokens\":5}\n\n" + "data: [DONE]\n\n"
	// Feed it a byte at a time: no write boundary may leak a half frame or split a
	// named event from its data line.
	var writes []string
	for i := range whole {
		writes = append(writes, whole[i:i+1])
	}
	frames := openAIFilterFrames(t, false, writes...)
	want := []string{
		`data: {"choices":[{"delta":{"content":"","role":"assistant"},"finish_reason":null,"index":0}],"created":7,"id":"chatcmpl-1","model":"local/m","object":"chat.completion.chunk"}`,
		`data: {"choices":[{"delta":{"content":"Hi"},"finish_reason":null,"index":0}],"created":7,"id":"chatcmpl-1","model":"local/m","object":"chat.completion.chunk"}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"created":7,"id":"chatcmpl-1","model":"local/m","object":"chat.completion.chunk"}`,
		"data: [DONE]",
	}
	if len(frames) != len(want) {
		t.Fatalf("got %d frames, want %d:\n%s", len(frames), len(want), strings.Join(frames, "\n"))
	}
	for i := range want {
		if frames[i] != want[i] {
			t.Fatalf("frame %d:\n got %s\nwant %s", i, frames[i], want[i])
		}
	}
}

func TestOpenAIStreamFilter_EmptyTurnStillFinishesOneChoice(t *testing.T) {
	// A turn that produced no text must not leave the client with "no choices":
	// the message is opened and finished all the same.
	frames := openAIFilterFrames(t, false, "event: coddy_meta\ndata: {\"metadata\":{\"model\":\"local/m\"}}\n\n", "data: [DONE]\n\n")
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want role, finish and [DONE]:\n%s", len(frames), strings.Join(frames, "\n"))
	}
	if !strings.Contains(frames[0], `"role":"assistant"`) {
		t.Fatalf("first frame must open the message: %s", frames[0])
	}
	if !strings.Contains(frames[1], `"finish_reason":"stop"`) {
		t.Fatalf("second frame must finish the choice: %s", frames[1])
	}
	if frames[2] != "data: [DONE]" {
		t.Fatalf("last frame = %s", frames[2])
	}
}

func TestOpenAIStreamFilter_BudgetStopReasonBecomesLength(t *testing.T) {
	for stop, want := range map[string]string{
		"end_turn":   "stop",
		"cancelled":  "stop",
		"max_turns":  "length",
		"max_tokens": "length",
		"":           "stop",
	} {
		meta := "event: coddy_meta\ndata: {\"metadata\":{\"model\":\"local/m\",\"stop_reason\":\"" + stop + "\"}}\n\n"
		frames := openAIFilterFrames(t, false, meta, "data: [DONE]\n\n")
		finish := frames[len(frames)-2]
		if !strings.Contains(finish, `"finish_reason":"`+want+`"`) {
			t.Fatalf("stop_reason %q: finish frame = %s, want %s", stop, finish, want)
		}
	}
}

func TestOpenAIStreamFilter_UsageChunkFollowsTheFinishedChoice(t *testing.T) {
	frames := openAIFilterFrames(t, true,
		"event: token_usage\ndata: {\"sessionUpdate\":\"token_usage\",\"inputTokens\":30,\"outputTokens\":12,\"totalTokens\":42}\n\n",
		"data: [DONE]\n\n")
	if len(frames) != 4 {
		t.Fatalf("got %d frames, want role, finish, usage and [DONE]:\n%s", len(frames), strings.Join(frames, "\n"))
	}
	usage := frames[2]
	if !strings.Contains(usage, `"choices":[]`) || !strings.Contains(usage, `"usage":{"completion_tokens":12,"prompt_tokens":30,"total_tokens":42}`) {
		t.Fatalf("usage frame = %s", usage)
	}
	// Without the request flag the same turn ends without a usage frame.
	frames = openAIFilterFrames(t, false,
		"event: token_usage\ndata: {\"sessionUpdate\":\"token_usage\",\"inputTokens\":30,\"outputTokens\":12,\"totalTokens\":42}\n\n",
		"data: [DONE]\n\n")
	for _, fr := range frames {
		if strings.Contains(fr, `"usage"`) {
			t.Fatalf("usage frame sent without include_usage: %s", fr)
		}
	}
}

func TestOpenAIStreamFilter_ErrorFrameAndKeepaliveReachTheClient(t *testing.T) {
	frames := openAIFilterFrames(t, false, ": keepalive\n\n", "data: {\"error\":{\"message\":\"boom\"}}\n\n")
	want := []string{": keepalive", `data: {"error":{"message":"boom"}}`}
	if strings.Join(frames, "|") != strings.Join(want, "|") {
		t.Fatalf("frames = %q, want %q", frames, want)
	}
}

func TestOpenAIStreamFilter_NothingFollowsDone(t *testing.T) {
	frames := openAIFilterFrames(t, false, "data: [DONE]\n\n",
		`data: {"choices":[{"index":0,"delta":{"content":"late"}}]}`+"\n\n")
	if frames[len(frames)-1] != "data: [DONE]" {
		t.Fatalf("frames after [DONE]: %s", strings.Join(frames, "\n"))
	}
}

func TestOpenAIStreamFilter_RelayBehindTheTeeKeepsTheCoddyStream(t *testing.T) {
	// The filter is the client's view only: a relay teed off the same sender must
	// still receive every named event, as the SPA watching the turn expects.
	client := httptest.NewRecorder()
	relay := newComposerStreamRelay()
	tee := &teeSSEWriter{ResponseWriter: newOpenAIStreamFilter(client, "agent", false), relay: relay}
	sender := NewSender(&config.Config{}, tee, true, "agent")
	if err := sender.SendSessionUpdate("s", acp.TokenUsageUpdate{SessionUpdate: acp.UpdateTypeTokenUsage, InputTokens: 1, OutputTokens: 1, TotalTokens: 2}); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendSessionUpdate("s", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "Hi"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sender.FinishStreamWithMetadata(map[string]string{"model": "local/m", "stop_reason": "end_turn"}); err != nil {
		t.Fatal(err)
	}
	relay.mu.Lock()
	var relayed strings.Builder
	for _, fr := range relay.frames {
		relayed.Write(fr.data)
	}
	relay.mu.Unlock()
	if got := relayed.String(); !strings.Contains(got, "event: token_usage") || !strings.Contains(got, "event: coddy_meta") {
		t.Fatalf("relay lost coddy's named events:\n%s", got)
	}
	if got := client.Body.String(); strings.Contains(got, "event:") || !strings.Contains(got, `"finish_reason":"stop"`) {
		t.Fatalf("client did not get the strict view:\n%s", got)
	}
}

func TestOpenAIStreamFilter_AForwardedFinishIsTheOnlyFinish(t *testing.T) {
	// The bridge never finishes a choice itself; should a chunk ever arrive
	// already finished, the completion ends there and [DONE] adds no second finish.
	frames := openAIFilterFrames(t, false,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":7,"model":"local/m","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":"stop"}]}`+"\n\n",
		"data: [DONE]\n\n")
	finished := 0
	for _, fr := range frames {
		if strings.Contains(fr, `"finish_reason":"stop"`) {
			finished++
		}
	}
	if finished != 1 {
		t.Fatalf("%d finishing frames, want one:\n%s", finished, strings.Join(frames, "\n"))
	}
}

func TestOpenAIStreamFilter_AFrameWithoutAChoiceIsNotAChunk(t *testing.T) {
	// Empty choices is the shape of a provider's usage frame; the client's usage
	// frame is the filter's own, so such a frame neither opens the message nor
	// reaches the client.
	frames := openAIFilterFrames(t, false,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":7,"model":"local/m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`+"\n\n",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":7,"model":"local/m"}`+"\n\n",
		"data: [DONE]\n\n")
	for _, fr := range frames {
		if strings.Contains(fr, `"usage"`) || strings.Contains(fr, `"choices":[]`) {
			t.Fatalf("a frame without a choice reached the client: %s", fr)
		}
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want role, finish and [DONE]:\n%s", len(frames), strings.Join(frames, "\n"))
	}
}

func TestOpenAIStreamFilter_AnErrorEndsTheTurnWithoutAFinishedChoice(t *testing.T) {
	// The agent path writes an error frame and then [DONE]; a finished choice
	// after the error would read as a successful answer to an OpenAI client.
	frames := openAIFilterFrames(t, true,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":7,"model":"local/m","choices":[{"index":0,"delta":{"content":"Hi"}}]}`+"\n\n",
		`data: {"error":{"message":"boom"}}`+"\n\n",
		"data: [DONE]\n\n")
	want := []string{
		`data: {"choices":[{"delta":{"content":"","role":"assistant"},"finish_reason":null,"index":0}],"created":7,"id":"chatcmpl-1","model":"local/m","object":"chat.completion.chunk"}`,
		`data: {"choices":[{"delta":{"content":"Hi"},"finish_reason":null,"index":0}],"created":7,"id":"chatcmpl-1","model":"local/m","object":"chat.completion.chunk"}`,
		`data: {"error":{"message":"boom"}}`,
		"data: [DONE]",
	}
	if strings.Join(frames, "|") != strings.Join(want, "|") {
		t.Fatalf("frames:\n%s\nwant:\n%s", strings.Join(frames, "\n"), strings.Join(want, "\n"))
	}
}

// failingResponseWriter fails every write, the way a client that hung up does.
type failingResponseWriter struct {
	http.ResponseWriter
}

func (failingResponseWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOpenAIStreamFilter_AFailedWriteReportsTheBytesItTookAndClosesTheStream(t *testing.T) {
	f := newOpenAIStreamFilter(failingResponseWriter{httptest.NewRecorder()}, "local/m", false)
	frame := []byte(`data: {"choices":[{"index":0,"delta":{"content":"Hi"}}]}` + "\n\n")
	n, err := f.Write(frame)
	if err == nil {
		t.Fatal("a failed emission must surface as an error")
	}
	if n != len(frame) {
		t.Fatalf("Write reported %d bytes, want the %d it consumed", n, len(frame))
	}
	// The stream is closed: a later frame is swallowed rather than retried.
	if n, err := f.Write([]byte("data: [DONE]\n\n")); err != nil || n != len("data: [DONE]\n\n") {
		t.Fatalf("after a failure Write = (%d, %v), want the bytes accepted silently", n, err)
	}
}
