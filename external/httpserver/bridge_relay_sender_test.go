//go:build http

package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestSenderSendErrorWritesOpenAIErrorFrame(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")

	if err := sender.SendError("boom \"quoted\""); err != nil {
		t.Fatal(err)
	}

	// Byte-for-byte the frame the handlers used to write inline, so no client that
	// parses the stream sees a different shape than before.
	want := "data: {\"error\":{\"message\":\"boom \\\"quoted\\\"\"}}\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
}

func TestSenderSendErrorIsSilentWithoutAWriter(t *testing.T) {
	sender := NewSender(&config.Config{}, nil, false, "agent-model")
	if err := sender.SendError("boom"); err != nil {
		t.Fatalf("SendError on a silent sender: %v", err)
	}
}

// A watcher must learn that the turn failed. Error frames written straight to the
// ResponseWriter bypass the tee, so the relay only ever saw the stream close.
func TestSenderSendErrorReachesRelaySubscribers(t *testing.T) {
	rec := httptest.NewRecorder()
	relay := newComposerStreamRelay()
	sender := NewSender(&config.Config{}, &teeSSEWriter{ResponseWriter: rec, relay: relay}, true, "agent-model")

	if err := sender.SendError("provider exploded"); err != nil {
		t.Fatal(err)
	}

	sub := &signalOnWriteRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		wrote:            make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = relay.serveSubscriber(context.Background(), sub)
	}()
	// Attaching replays the buffer, so wait for that write rather than for a duration:
	// closing the relay before the subscriber registers would end it with no body.
	<-sub.wrote
	relay.Close()
	<-done

	if got := sub.Body.String(); !strings.Contains(got, "provider exploded") {
		t.Fatalf("subscriber body %q missing the error frame", got)
	}
}

// signalOnWriteRecorder closes wrote on the first Write, so a test can wait for a
// subscriber to have received something instead of sleeping.
type signalOnWriteRecorder struct {
	*httptest.ResponseRecorder
	wrote chan struct{}
	once  sync.Once
}

func (r *signalOnWriteRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(b)
	r.once.Do(func() { close(r.wrote) })
	return n, err
}

func TestRelaySenderPublishesToTheRelayOnly(t *testing.T) {
	var relay bytes.Buffer
	sender := NewRelaySender(&config.Config{}, &relay, "agent-model")

	if err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hello"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sender.FinishStreamWithMetadata(map[string]string{"model": "agent"}); err != nil {
		t.Fatal(err)
	}

	got := relay.String()
	if !strings.Contains(got, `"content":"hello"`) {
		t.Fatalf("relay body %q missing the assistant delta", got)
	}
	if !strings.Contains(got, "coddy_meta") {
		t.Fatalf("relay body %q missing the metadata frame", got)
	}
	if !strings.Contains(got, "data: [DONE]\n\n") {
		t.Fatalf("relay body %q missing the terminating frame", got)
	}
}

// The direct YAML path chooses the provider call from the sender's emit flag. A
// non-stream request must keep landing on Complete: switching it to the provider's
// streaming API would change the wire call and the usage accounting behind the caller's
// back.
func TestDirectYAMLNonStreamCallsComplete(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	calls := &recordingProvider{reply: "direct answer"}
	srv.makeLLMFromYAML = func(*config.Config, string) (llm.Provider, error) { return calls, nil }

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := `{"model":"openai/gpt-4o","input":"hi","stream":false}`
	res, err := ts.Client().Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}

	if calls.streamed {
		t.Fatal("a stream:false request must not reach the provider's streaming API")
	}
	if !calls.completed {
		t.Fatal("a stream:false request should call Complete")
	}
}

type recordingProvider struct {
	reply     string
	completed bool
	streamed  bool
}

func (p *recordingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	p.completed = true
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

func (p *recordingProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.streamed = true
	onChunk(llm.StreamChunk{TextDelta: p.reply})
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

// Emitting frames and being able to answer a prompt are different capabilities. A turn
// started by a script that never reads a stream must keep auto-rejecting, or it would
// block forever waiting for an answer from a client that does not exist.
func TestRelaySenderIsNotInteractive(t *testing.T) {
	var relay bytes.Buffer
	sender := NewRelaySender(&config.Config{}, &relay, "agent-model")

	res, err := sender.RequestPermission(context.Background(), acp.PermissionRequestParams{
		SessionID: "sess-x",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "tc1", Title: "Run: rm -rf /"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Outcome != "cancelled" || res.OptionID != "reject" {
		t.Fatalf("permission result %+v, want the auto-reject a silent sender gives", res)
	}
	if strings.Contains(relay.String(), "event: permission") {
		t.Fatal("a permission frame nobody can answer must not be published")
	}

	if _, err := sender.RequestQuestion(context.Background(), acp.QuestionRequestParams{
		SessionID: "sess-x",
		RequestID: "rq1",
	}); err == nil {
		t.Fatal("RequestQuestion must still fail when no interactive client owns the turn")
	}
}

// syncBuffer is a writer safe to read while the keepalive goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestSenderIdleKeepaliveWritesCommentFrames covers the silence a model with
// stream: false produces: nothing reaches the client until the whole answer is
// generated, and an idle-timeout proxy would drop the stream in the meantime.
func TestSenderIdleKeepaliveWritesCommentFrames(t *testing.T) {
	out := &syncBuffer{}
	sender := NewSender(&config.Config{}, out, true, "agent-model")

	stop := sender.startIdleKeepalive(15*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.String(), ": keepalive\n\n") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	stop()

	if !strings.Contains(out.String(), ": keepalive\n\n") {
		t.Fatalf("idle stream produced no keepalive: %q", out.String())
	}
	// A comment frame carries no data, so no client sees it as content.
	if strings.Contains(out.String(), "data:") {
		t.Fatalf("keepalive frame must not carry data: %q", out.String())
	}
}

// TestSenderIdleKeepaliveStaysQuietWhileFramesFlow checks the keepalive only
// fills silence: a stream that is producing chunks must not gain extra frames.
func TestSenderIdleKeepaliveStaysQuietWhileFramesFlow(t *testing.T) {
	out := &syncBuffer{}
	sender := NewSender(&config.Config{}, out, true, "agent-model")

	stop := sender.startIdleKeepalive(200*time.Millisecond)
	for i := 0; i < 8; i++ {
		if err := sender.SendSessionUpdate("s", acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "tok "},
		}); err != nil {
			t.Fatalf("SendSessionUpdate: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()

	if strings.Contains(out.String(), ": keepalive") {
		t.Fatalf("a busy stream got a keepalive: %q", out.String())
	}
}

// TestSenderIdleKeepaliveIsSilentWithoutAWriter guards the non-streaming caller:
// a silent sender has nothing to keep alive.
func TestSenderIdleKeepaliveIsSilentWithoutAWriter(t *testing.T) {
	sender := NewSender(&config.Config{}, nil, false, "agent-model")
	stop := sender.startIdleKeepalive(time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	stop()
}
