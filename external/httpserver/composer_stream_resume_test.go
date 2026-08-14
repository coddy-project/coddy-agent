//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func frameOf(text string) string { return "data: " + text + "\n\n" }

// drainSubscriber attaches, waits for the first write, closes the relay and returns
// everything the subscriber received.
func drainSubscriber(t *testing.T, r *composerStreamRelay, lastEventID uint64) string {
	t.Helper()
	sub := &signalOnWriteRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		wrote:            make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.serveSubscriberFrom(context.Background(), sub, lastEventID)
	}()
	<-sub.wrote
	r.Close()
	<-done
	return sub.Body.String()
}

// A late subscriber replays the turn so far. Trimming that history by bytes cut the
// oldest frame in half, so the client's parser saw a corrupt event; trimming by whole
// frames cannot.
func TestRelayTrimsWholeFramesOnly(t *testing.T) {
	r := newComposerStreamRelay()
	r.maxBytes = 200
	for i := 0; i < 40; i++ {
		if _, err := r.Write([]byte(frameOf(fmt.Sprintf("chunk-%02d-%s", i, strings.Repeat("x", 20))))); err != nil {
			t.Fatal(err)
		}
	}

	body := drainSubscriber(t, r, 0)
	for _, block := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		if block == "" {
			continue
		}
		if !strings.HasPrefix(block, "id: ") {
			t.Fatalf("replayed block %q does not start a frame", block)
		}
		if !strings.Contains(block, "data: chunk-") {
			t.Fatalf("replayed block %q was cut mid-frame", block)
		}
	}
}

// Resuming is what turns a dropped connection into a gap the client can close by itself
// instead of a reload.
func TestRelayResumesAfterLastEventID(t *testing.T) {
	r := newComposerStreamRelay()
	for i := 1; i <= 4; i++ {
		if _, err := r.Write([]byte(frameOf(fmt.Sprintf("frame-%d", i)))); err != nil {
			t.Fatal(err)
		}
	}

	body := drainSubscriber(t, r, 2)
	if strings.Contains(body, "frame-1") || strings.Contains(body, "frame-2") {
		t.Fatalf("resume replayed frames the client already had: %s", body)
	}
	if !strings.Contains(body, "frame-3") || !strings.Contains(body, "frame-4") {
		t.Fatalf("resume skipped frames the client is missing: %s", body)
	}
	if !strings.Contains(body, "id: 3") {
		t.Fatalf("resumed frames must carry their sequence: %s", body)
	}
}

// When the frames a client asks to resume from are already gone, saying so is the only
// honest answer: it can reload the transcript instead of rendering a hole.
func TestRelayReportsDesyncWhenHistoryIsGone(t *testing.T) {
	r := newComposerStreamRelay()
	r.maxBytes = 120
	for i := 1; i <= 30; i++ {
		if _, err := r.Write([]byte(frameOf(fmt.Sprintf("frame-%02d", i)))); err != nil {
			t.Fatal(err)
		}
	}

	body := drainSubscriber(t, r, 1)
	if !strings.Contains(body, "event: desync") {
		t.Fatalf("a client resuming from trimmed history must be told: %s", body)
	}
}

func TestRelaySubscriberFramesCarrySequenceIDs(t *testing.T) {
	r := newComposerStreamRelay()
	if _, err := r.Write([]byte(frameOf("only"))); err != nil {
		t.Fatal(err)
	}
	body := drainSubscriber(t, r, 0)
	if !strings.HasPrefix(body, "id: 1\ndata: only\n\n") {
		t.Fatalf("subscriber frame %q must be prefixed with its sequence", body)
	}
}

// The bytes the original POST streams to its own client must not change: existing API
// clients parse that stream and never asked for ids.
func TestRelayTeeLeavesThePrimaryStreamUntouched(t *testing.T) {
	rec := httptest.NewRecorder()
	relay := newComposerStreamRelay()
	tee := &teeSSEWriter{ResponseWriter: rec, relay: relay}
	if _, err := tee.Write([]byte(frameOf("hello"))); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != frameOf("hello") {
		t.Fatalf("primary stream %q, want the frame unchanged", got)
	}
}

// A writer may hand over a partial frame; buffering until the terminator keeps the
// numbering aligned with real SSE frames.
func TestRelayAssemblesFramesSplitAcrossWrites(t *testing.T) {
	r := newComposerStreamRelay()
	if _, err := r.Write([]byte("data: split")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("-frame\n\n")); err != nil {
		t.Fatal(err)
	}
	body := drainSubscriber(t, r, 0)
	if !strings.Contains(body, "id: 1\ndata: split-frame\n\n") {
		t.Fatalf("frame split across writes was not reassembled: %q", body)
	}
}
