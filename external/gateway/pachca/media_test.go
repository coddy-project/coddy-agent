//go:build gateway || gateway.pachca

package pachca

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/pachcafake"
)

// lockedBuffer is a log sink the worker goroutines may write while a test
// reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// A message with nothing written - what Pachca delivers for a file sent on
// its own - is answered: the bot reads text only, and the person would
// otherwise hear nothing (issue #509). A mention with no words is dropped
// with a reason in the log.
func TestMessageWithoutTextIsAnsweredAndLogged(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	logs := &lockedBuffer{}
	e.bot.log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.startAt(t)

	e.dm(t, 1, "")
	e.tickUntilQuiet(t, 2)
	chat := e.fake.PersonalChat(1)
	waitFor(t, func() bool {
		for _, x := range e.fake.Messages(chat) {
			if x.UserID == e.fake.BotUserID() && strings.Contains(x.Content, "text") {
				return true
			}
		}
		return false
	}, "the answer to a message without text")
	if !strings.Contains(logs.String(), `msg="pachca: update refused" reason="no text"`) {
		t.Fatalf("the refusal was not logged:\n%s", logs.String())
	}

	e.dm(t, 1, "@coddy_bot")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool {
		return strings.Contains(logs.String(), `msg="pachca: update ignored" reason="empty message"`)
	}, "the logged drop")
	time.Sleep(50 * time.Millisecond)
	if e.runner.promptCount() != 0 {
		t.Fatal("a message without text ran a turn")
	}
	answers := 0
	for _, x := range e.fake.Messages(chat) {
		if x.UserID == e.fake.BotUserID() {
			answers++
		}
	}
	if answers != 1 {
		t.Fatalf("%d answers, want one to the message without text and none to the bare mention", answers)
	}
}
