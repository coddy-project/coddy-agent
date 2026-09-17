//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// drainSubscriberAfter is drainSubscriber for a client that attaches with a transcript
// in hand: it asks for the frames the snapshot at sinceRev does not hold.
func drainSubscriberAfter(t *testing.T, r *composerStreamRelay, sinceRev uint64) string {
	t.Helper()
	sub := &signalOnWriteRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		wrote:            make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.serveSubscriberAfter(context.Background(), sub, relayResume{sinceRev: sinceRev, bySnapshot: true})
	}()
	<-sub.wrote
	r.Close()
	<-done
	return sub.Body.String()
}

// A tab reloaded in the middle of a turn loads the transcript first and attaches to the
// relay second. Replaying the whole turn on top of that transcript put every reasoning
// block and every answer of the finished steps on screen a second time, and stamped
// the finished tool rows with the moment of the replay, so they read 0ms. The frames a
// persisted message already holds are left out.
func TestRelayReplaysOnlyFramesTheSnapshotLacks(t *testing.T) {
	var rev uint64 = 3
	r := newComposerStreamRelay()
	r.rev = func() uint64 { return rev }
	if _, err := r.Write([]byte(frameOf("step-one"))); err != nil {
		t.Fatal(err)
	}
	rev = 4 // the first step's message is persisted
	if _, err := r.Write([]byte(frameOf("step-two"))); err != nil {
		t.Fatal(err)
	}

	body := drainSubscriberAfter(t, r, 4)
	if strings.Contains(body, "step-one") {
		t.Fatalf("replayed a step the transcript already holds: %s", body)
	}
	if !strings.Contains(body, "step-two") {
		t.Fatalf("left out the step still streaming: %s", body)
	}
	if strings.Contains(body, "event: desync") {
		t.Fatalf("nothing the snapshot lacks was trimmed: %s", body)
	}
}

func TestRelayReportsDesyncWhenFramesTheSnapshotLacksWereTrimmed(t *testing.T) {
	var rev uint64 = 1
	r := newComposerStreamRelay()
	r.rev = func() uint64 { return rev }
	r.maxBytes = 120
	for i := 1; i <= 30; i++ {
		if _, err := r.Write([]byte(frameOf(fmt.Sprintf("frame-%02d", i)))); err != nil {
			t.Fatal(err)
		}
	}
	if body := drainSubscriberAfter(t, r, 1); !strings.Contains(body, "event: desync") {
		t.Fatalf("a client missing trimmed unpersisted frames must be told: %s", body)
	}
}

func TestRelayTrimmedPersistedFramesAreNoGap(t *testing.T) {
	var rev uint64 = 1
	r := newComposerStreamRelay()
	r.rev = func() uint64 { return rev }
	r.maxBytes = 120
	for i := 1; i <= 30; i++ {
		if _, err := r.Write([]byte(frameOf(fmt.Sprintf("persisted-%02d", i)))); err != nil {
			t.Fatal(err)
		}
	}
	rev = 2
	if _, err := r.Write([]byte(frameOf("live"))); err != nil {
		t.Fatal(err)
	}
	body := drainSubscriberAfter(t, r, 2)
	if strings.Contains(body, "event: desync") {
		t.Fatalf("frames the snapshot holds were trimmed, which loses nothing: %s", body)
	}
	if !strings.Contains(body, "live") {
		t.Fatalf("missing the frame the snapshot lacks: %s", body)
	}
}

// A replayed frame is old news: its age lets the client date it when it happened
// rather than when it arrived, so a reasoning block that started a minute before the
// reload does not restart its clock, and a tool call that ran for seconds does not
// read 0ms because its start and its end were replayed in the same burst.
func TestRelayReplayedFramesCarryTheirAge(t *testing.T) {
	r := newComposerStreamRelay()
	if _, err := r.Write([]byte(frameOf("old"))); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.frames[0].at = time.Now().Add(-5 * time.Second)
	r.mu.Unlock()

	body := drainSubscriber(t, r, 0)
	if !strings.HasPrefix(body, "id: 1\nage: 5") || !strings.Contains(body, "\ndata: old\n\n") {
		t.Fatalf("replayed frame %q must carry its age in milliseconds", body)
	}
}
