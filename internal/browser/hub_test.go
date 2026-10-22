package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOwnershipAndDisconnect(t *testing.T) {
	h := NewHub()
	c, err := h.Connect("browser-one", "Firefox", []string{"snapshot", "click"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.UpdateTabs(c, []Tab{{ID: 7, URL: "https://example.com", Title: "Example"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, e := h.Call(ctx, "chat-a", "browser-one", Command{Action: "click", TabID: 7})
		finished <- e
	}()
	cmd := <-c.Commands
	if _, err := h.Call(ctx, "chat-b", "browser-one", Command{Action: "click", TabID: 7}); err == nil || !strings.Contains(err.Error(), "another chat") {
		t.Fatalf("ownership: %v", err)
	}
	h.Reply(c, Reply{ID: cmd.ID, Result: json.RawMessage(`{"ok":true}`)})
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(ctx, "chat-a", "browser-one", Command{Action: "click", TabID: 99}); err == nil {
		t.Fatal("unselected tab accepted")
	}
	h.Disconnect(c)
	if len(h.List()) != 0 {
		t.Fatal("disconnected browser retained")
	}
}

func TestStopRevokesPendingAndRequiresReselection(t *testing.T) {
	h := NewHub()
	c, _ := h.Connect("browser-one", "Chrome", []string{"click"})
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com"}})
	done := make(chan error, 1)
	go func() {
		_, err := h.Call(context.Background(), "a", c.ID, Command{Action: "click", TabID: 1})
		done <- err
	}()
	<-c.Commands
	h.Stop(c)
	if err := <-done; err == nil {
		t.Fatal("Stop did not fail pending command")
	}
	if _, err := h.Call(context.Background(), "a", c.ID, Command{Action: "click", TabID: 1}); err == nil {
		t.Fatal("stopped tab accepted")
	}
}

func TestDuplicateConnectionRefused(t *testing.T) {
	h := NewHub()
	_, _ = h.Connect("browser-one", "one", nil)
	if _, err := h.Connect("browser-one", "two", nil); err == nil {
		t.Fatal("live browser replaced")
	}
}

func TestActionTimeoutBoundsDownloadsWider(t *testing.T) {
	if got := actionTimeout("click"); got != 2*time.Minute {
		t.Fatalf("an interaction is bounded by %s", got)
	}
	if got := actionTimeout("download_file"); got != 10*time.Minute {
		t.Fatalf("a download is bounded by %s", got)
	}
}
