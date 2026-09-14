//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The chat's own agent is allowed without a question, and so is a subagent
// stamped with bypass; a subagent narrowed below it is never waved through on
// the parent's behalf. With nowhere to ask - a sender built outside a bot - it
// is refused.
func TestSenderRequestPermissionNeverWavesThroughANarrowedSubagent(t *testing.T) {
	s := &Sender{}
	params := func(mode string) acp.PermissionRequestParams {
		return acp.PermissionRequestParams{
			SessionID:               "sess_parent",
			ToolCall:                acp.PermissionToolCall{ToolCallID: "c1", Status: "pending"},
			EffectivePermissionMode: mode,
		}
	}
	if got, _ := s.RequestPermission(context.Background(), params("")); got.OptionID != "allow" {
		t.Fatalf("unstamped request = %#v, want allow", got)
	}
	if got, _ := s.RequestPermission(context.Background(), params("bypass")); got.OptionID != "allow" {
		t.Fatalf("stamped bypass = %#v, want allow", got)
	}
	for _, mode := range []string{"ask", "accept_edits"} {
		if got, _ := s.RequestPermission(context.Background(), params(mode)); got.OptionID != "reject" || got.Outcome != "cancelled" {
			t.Fatalf("stamped %s with nowhere to ask = %#v, want a denial", mode, got)
		}
	}
}

// newPermissionWorld is the feature harness without godog, for the edges.
func newPermissionWorld(t *testing.T) *subagentPermissionWorld {
	t.Helper()
	w := &subagentPermissionWorld{}
	w.reset()
	t.Cleanup(w.close)
	return w
}

// In a group where every member has a session of their own, another member's
// tap must not answer for the person whose subagent asked; a tap on a button
// nobody waits for any more changes nothing.
func TestAPermissionTapFromSomebodyElseIsIgnored(t *testing.T) {
	asks := newChatPermissions()
	p := &chatPrompt{
		sessionID: "sess_owner",
		options:   []acp.PermissionOption{{OptionID: "allow", Name: "Allow"}, {OptionID: "reject", Name: "Reject"}},
		answer:    make(chan *acp.PermissionResult, 1),
	}
	asks.pending["tok"] = p

	if _, ok := asks.resolve("tok", 0, "sess_other_member"); ok {
		t.Fatal("another member answered for the owner")
	}
	if _, ok := asks.resolve("tok", 0, ""); ok {
		t.Fatal("a user with no session answered")
	}
	if _, ok := asks.resolve("tok", 5, "sess_owner"); ok {
		t.Fatal("an option the request never offered was accepted")
	}
	if _, ok := asks.resolve("gone", 0, "sess_owner"); ok {
		t.Fatal("a token nobody waits for was accepted")
	}
	opt, ok := asks.resolve("tok", 1, "sess_owner")
	if !ok || opt.OptionID != "reject" {
		t.Fatalf("the owner's tap resolved %+v, %v", opt, ok)
	}
	if res := <-p.answer; res.OptionID != "reject" {
		t.Fatalf("the request received %+v", res)
	}
	if _, ok := asks.resolve("tok", 0, "sess_owner"); ok {
		t.Fatal("a second tap on an answered request was accepted")
	}
}

// A request withdrawn because its run ended - or another surface answered it
// first - stops waiting and says so in the chat, taking the buttons away.
func TestAWithdrawnRequestSaysItNoLongerWaits(t *testing.T) {
	w := newPermissionWorld(t)
	if err := w.chatWithSession(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *acp.PermissionResult, 1)
	go func() {
		res, _ := w.bot.asks.ask(ctx, w.api, slog.New(slog.DiscardHandler), permissionChatID, w.sessionID,
			permissionParams(w.sessionID, "writer", "echo checked"))
		done <- res
	}()
	if _, err := w.promptMessage(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case res := <-done:
		if res != nil {
			t.Fatalf("a withdrawn request returned %+v, want no answer", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request outlived its context")
	}
	if err := w.requestReads("No longer waiting"); err != nil {
		t.Fatal(err)
	}
}

// The bot shows only the prompts of its own conversations, and only while it
// is connected; anything else is for another surface of the process.
func TestTheBotDeclinesPromptsItCannotShow(t *testing.T) {
	w := newPermissionWorld(t)
	if err := w.chatWithSession(); err != nil {
		t.Fatal(err)
	}
	req := agent.DetachedPermissionRequest{
		ParentSessionID: "sess_from_a_browser",
		ChildSessionID:  "sess_child",
		AgentName:       "writer",
		Params:          permissionParams("sess_child", "writer", "echo checked"),
	}
	if _, err := w.bot.RequestDetachedPermission(context.Background(), req); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("a session of no chat answered %v, want ErrNoDetachedApprover", err)
	}

	req.ParentSessionID = w.sessionID
	w.bot.setAPI(nil)
	if _, err := w.bot.RequestDetachedPermission(context.Background(), req); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("a stopped bot answered %v, want ErrNoDetachedApprover", err)
	}

	req.Params.EffectivePermissionMode = config.PermModeBypass
	res, err := w.bot.RequestDetachedPermission(context.Background(), req)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("a child narrowed to bypass got %+v, %v; want allow without a question", res, err)
	}
}

// The request body reads as the relay titled it, so the subagent is named, and
// a command too long for one message is cut rather than refused by Telegram.
func TestPermissionTextNamesTheSubagent(t *testing.T) {
	params := permissionParams("sess_child", "writer", "echo checked")
	params.ToolCall.Content = []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: strings.Repeat("x", 5000)}}}
	text := permissionText(params)
	if !strings.HasPrefix(text, "🔐 [subagent writer] Run: echo checked") {
		t.Fatalf("text = %q", text[:60])
	}
	if len(text) > 4096 {
		t.Fatalf("text is %d bytes, over one Telegram message", len(text))
	}
}
