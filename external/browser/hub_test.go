//go:build browser

package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func connectTest(t *testing.T, h *Hub, id string, actions ...string) *Connection {
	t.Helper()
	c, err := h.Connect(BrowserInfo{ID: id, Name: "Chrome"}, actions)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sent reads the next frame the hub queued for the socket.
func sent(t *testing.T, c *Connection) message {
	t.Helper()
	select {
	case raw := <-c.out:
		var m message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(time.Second):
		t.Fatal("nothing was sent to the browser")
	}
	return message{}
}

func TestATabInAnotherChatsGroupIsRefusedBeforeSending(t *testing.T) {
	h := NewHub()
	c := connectTest(t, h, "browser-one", "snapshot", "click")
	if err := h.UpdateTabs(c, []Tab{
		{ID: 7, URL: "https://example.com", Title: "Shared"},
		{ID: 8, URL: "https://example.org", Title: "Chat A's", SessionID: "chat-a"},
	}, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(context.Background(), Request{SessionID: "chat-b", BrowserID: "browser-one", Action: "click", TabID: 8}); err == nil || !strings.Contains(err.Error(), "another chat") {
		t.Fatalf("a chat reached another chat's tab: %v", err)
	}
	select {
	case raw := <-c.out:
		t.Fatalf("a refused call reached the browser: %s", raw)
	default:
	}
	views := h.List("chat-b")[0].Tabs
	if views[0].Busy || views[0].Yours || !views[1].Busy || views[1].Yours {
		t.Fatalf("chat b sees %+v", views)
	}
	if views := h.List("chat-a")[0].Tabs; !views[1].Yours || views[1].Busy {
		t.Fatalf("chat a sees %+v", views)
	}
	// A shared tab and the chat's own tab are both in reach.
	for _, tab := range []int{7, 8} {
		done := make(chan error, 1)
		go func() {
			_, err := h.Call(context.Background(), Request{SessionID: "chat-a", BrowserID: "browser-one", Action: "click", TabID: tab})
			done <- err
		}()
		id, _ := sent(t, c).stringID()
		h.Answer(c, id, json.RawMessage(`{"ok":true}`), nil)
		if err := <-done; err != nil {
			t.Fatalf("tab %d: %v", tab, err)
		}
	}
}

func TestTurnEndedReachesOnlyTheBrowsersHoldingTheChat(t *testing.T) {
	h := NewHub()
	holder := connectTest(t, h, "browser-one", "click")
	other := connectTest(t, h, "browser-two", "click")
	_ = h.UpdateTabs(holder, []Tab{{ID: 1, URL: "https://example.com", SessionID: "chat-a"}, {ID: 2, URL: "https://example.com/2", SessionID: "chat-a"}}, "g")
	_ = h.UpdateTabs(other, []Tab{{ID: 1, URL: "https://example.com"}}, "g")
	h.TurnEnded("chat-a")
	m := sent(t, holder)
	if m.Method != methodTurnEnded || !m.isNotification() || !strings.Contains(string(m.Params), `"chat-a"`) {
		t.Fatalf("holder heard %+v", m)
	}
	select {
	case raw := <-holder.out:
		t.Fatalf("the holder heard twice: %s", raw)
	case raw := <-other.out:
		t.Fatalf("a browser without the chat's tabs heard: %s", raw)
	default:
	}
}

func TestTheChatTitleTravelsWithTheCommand(t *testing.T) {
	h := NewHub()
	h.SetTitles(func(id string) string { return "Title of " + id })
	c := connectTest(t, h, "browser-one", "open_tab")
	go func() {
		_, _ = h.Call(context.Background(), Request{SessionID: "chat", BrowserID: "browser-one", Action: "open_tab", TabID: 99, Params: json.RawMessage(`{"url":"https://example.org/"}`)})
	}()
	var p commandParams
	_ = json.Unmarshal(sent(t, c).Params, &p)
	if p.Session.Title != "Title of chat" || p.TabID != 0 {
		t.Fatalf("command %+v", p)
	}
	h.Stop(c)
}

func TestACommandCarriesTheLatestGrantAndTheChat(t *testing.T) {
	h := NewHub()
	c := connectTest(t, h, "browser-one", "snapshot")
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com"}}, "first")
	// A tab update without a grant keeps the previous one.
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com/next"}}, "")
	go func() {
		_, _ = h.Call(context.Background(), Request{SessionID: "chat", SessionTitle: "Fix the docs", BrowserID: "browser-one", Action: "snapshot", TabID: 1, Params: json.RawMessage(`{"x":1}`)})
	}()
	m := sent(t, c)
	if m.Method != methodCommand || !m.isRequest() {
		t.Fatalf("sent %+v", m)
	}
	var p commandParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Grant != "first" || p.TabID != 1 || p.Action != "snapshot" || p.Session.ID != "chat" || p.Session.Title != "Fix the docs" || string(p.Params) != `{"x":1}` {
		t.Fatalf("command %+v", p)
	}
	h.Stop(c)
}

func TestUnsharedTabsAndUnannouncedActionsAreRefusedBeforeSending(t *testing.T) {
	h := NewHub()
	c := connectTest(t, h, "browser-one", "snapshot")
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com"}}, "g")
	for _, req := range []Request{
		{SessionID: "a", BrowserID: "browser-one", Action: "snapshot", TabID: 99},
		{SessionID: "a", BrowserID: "browser-one", Action: "click", TabID: 1},
		{SessionID: "a", BrowserID: "browser-gone", Action: "snapshot", TabID: 1},
	} {
		if _, err := h.Call(context.Background(), req); err == nil {
			t.Fatalf("%+v was accepted", req)
		}
	}
	select {
	case raw := <-c.out:
		t.Fatalf("a refused call reached the browser: %s", raw)
	default:
	}
}

func TestStopFailsWaitingCommandsAndWithdrawsEveryTab(t *testing.T) {
	h := NewHub()
	c := connectTest(t, h, "browser-one", "click")
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com"}}, "g")
	done := make(chan error, 1)
	go func() {
		_, err := h.Call(context.Background(), Request{SessionID: "a", BrowserID: "browser-one", Action: "click", TabID: 1})
		done <- err
	}()
	sent(t, c)
	h.Stop(c)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "Stop") {
		t.Fatalf("a waiting command survived Stop: %v", err)
	}
	if _, err := h.Call(context.Background(), Request{SessionID: "a", BrowserID: "browser-one", Action: "click", TabID: 1}); err == nil {
		t.Fatal("a tab stayed reachable after Stop")
	}
}

func TestAReconnectReplacesTheOldConnectionAndFailsItsCommands(t *testing.T) {
	h := NewHub()
	old := connectTest(t, h, "browser-one", "click")
	_ = h.UpdateTabs(old, []Tab{{ID: 1, URL: "https://example.com"}}, "g")
	done := make(chan error, 1)
	go func() {
		_, err := h.Call(context.Background(), Request{SessionID: "a", BrowserID: "browser-one", Action: "click", TabID: 1})
		done <- err
	}()
	cmd := sent(t, old)
	fresh := connectTest(t, h, "browser-one", "click")
	if err := <-done; err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("the replaced connection's command: %v", err)
	}
	// The old socket's late answer reaches nothing, and its disconnect does
	// not take the new connection with it.
	id, _ := cmd.stringID()
	h.Answer(old, id, json.RawMessage(`{}`), nil)
	h.Disconnect(old)
	if !h.Connected("browser-one") {
		t.Fatal("the old connection's disconnect dropped the new one")
	}
	if len(h.List("")[0].Tabs) != 0 {
		t.Fatal("the new connection inherited the old one's tabs")
	}
	h.Disconnect(fresh)
	if h.Connected("browser-one") {
		t.Fatal("a disconnected browser stayed listed")
	}
}

func TestACancelledCallTellsTheBrowser(t *testing.T) {
	h := NewHub()
	c := connectTest(t, h, "browser-one", "click")
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com"}}, "g")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.Call(ctx, Request{SessionID: "a", BrowserID: "browser-one", Action: "click", TabID: 1})
		done <- err
	}()
	cmd := sent(t, c)
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "inspect the page") {
		t.Fatalf("cancelled call: %v", err)
	}
	note := sent(t, c)
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(note.Params, &p)
	id, _ := cmd.stringID()
	if note.Method != methodCancel || !note.isNotification() || p.ID != id {
		t.Fatalf("cancel notice %+v", note)
	}
}

func TestAnErrorAnswerReachesTheChat(t *testing.T) {
	h := NewHub()
	c := connectTest(t, h, "browser-one", "click")
	_ = h.UpdateTabs(c, []Tab{{ID: 1, URL: "https://example.com"}}, "g")
	done := make(chan error, 1)
	go func() {
		_, err := h.Call(context.Background(), Request{SessionID: "a", BrowserID: "browser-one", Action: "click", TabID: 1})
		done <- err
	}()
	id, _ := sent(t, c).stringID()
	h.Answer(c, id, nil, &rpcError{Code: -32000, Message: "the user declined example.com"})
	if err := <-done; err == nil || err.Error() != "the user declined example.com" {
		t.Fatalf("error answer: %v", err)
	}
}

func TestRegistrationAndTabListsAreValidated(t *testing.T) {
	h := NewHub()
	for _, info := range []BrowserInfo{{ID: "short", Name: "x"}, {ID: "browser-one", Name: " "}, {ID: "browser/one", Name: "x"}} {
		if _, err := h.Connect(info, nil); err == nil {
			t.Fatalf("registered %+v", info)
		}
	}
	c := connectTest(t, h, "browser-one")
	for _, tabs := range [][]Tab{
		{{ID: 0, URL: "https://example.com"}},
		{{ID: 1, URL: "file:///etc/passwd"}},
		{{ID: 1, URL: "chrome://settings"}},
		{{ID: 1, URL: "https://" + strings.Repeat("a", 9000)}},
	} {
		if err := h.UpdateTabs(c, tabs, "g"); err == nil {
			t.Fatalf("accepted %+v", tabs)
		}
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
