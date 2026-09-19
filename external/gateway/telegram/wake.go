//go:build gateway || gateway.telegram

package telegram

// Woken turns in a chat.
//
// A chat conversation is an ordinary session, so its agent can start a
// background task with notify_on_finish and end its turn. When the task ends
// the process wakes the agent, and the turn belongs to the chat bound to the
// session: it runs here, through the chat's own sender, exactly like a message
// the person typed - mirrored to a browser watching the session, allowed what
// the chat's agent is allowed - so the answer lands where the work was asked
// for. The first thing the chat receives is the note that says what woke the
// agent (Sender.SendSessionUpdate).

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// wakeTurnTimeout bounds a woken turn the way a chat message's turn is bounded.
const wakeTurnTimeout = 10 * time.Minute

// SetWakeSurfaces names where the bot offers to run the woken turns of its
// chats' sessions; `coddy serve` passes its runtime. Nil (the default) means a
// woken turn never reaches a chat.
func (b *Bot) SetWakeSurfaces(w agent.WakeSurfaces) {
	b.wakeSurfaces = w
}

// RunBackgroundWake implements agent.WakeSurface. A session one of this bot's
// chats is bound to is the chat's, and its woken turn runs there; any other
// session is not the bot's to run, and neither is anything while the bot is not
// connected.
func (b *Bot) RunBackgroundWake(ctx context.Context, wake agent.Wake) (bool, error) {
	api := b.connectedAPI()
	if api == nil {
		return false, nil
	}
	key, ok := b.store.KeyFor(strings.TrimSpace(wake.SessionID))
	if !ok {
		return false, nil
	}
	chatID, ok := sessionstore.ChatID(key)
	if !ok {
		return false, nil
	}
	b.inFlight.Add(1)
	defer b.inFlight.Done()
	return true, b.runWokenTurn(ctx, api, chatID, isGroupKey(key), wake)
}

// runWokenTurn is processMessage for a turn nobody typed: the same session,
// sender, mirror and surface prompt, with the wake as the prompt.
func (b *Bot) runWokenTurn(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, isGroup bool, wake agent.Wake) error {
	ctx, cancel := context.WithTimeout(ctx, wakeTurnTimeout)
	defer cancel()

	st, err := b.runner.EnsureHTTPSession(ctx, wake.SessionID, b.cwd)
	if err != nil {
		b.log.Warn("telegram: woken turn: ensure session", "err", err, "session", wake.SessionID)
		return err
	}
	rich := b.cfg.RichMessages
	sender := b.chatSender(bot, chatID, 0, richConfig{
		enabled:    rich,
		allowDraft: rich && !isGroup,
		draftID:    b.draftSeq.Add(1),
	})
	mirrored, releaseMirror := session.Mirror(b.mirror, st.GetID(), sender)
	defer releaseMirror()

	b.log.Debug("telegram: woken turn", "session", st.GetID(), "chat", chatID, "tasks", len(wake.Tasks))
	opts := wake.RunOpts()
	opts.SurfaceSystemPrompt = surfaceSystemPrompt(rich)
	result, err := b.runner.HandleSessionPromptWithSender(ctx, wake.PromptParams(), mirrored, opts)
	if errors.Is(err, session.ErrSessionTurnBusy) {
		// The person's own turn is still running: the waker asks again once
		// it has ended, and nothing was said in the chat meanwhile.
		return err
	}
	sender.Flush()
	if err != nil {
		b.log.Warn("telegram: woken turn failed", "err", err, "session", st.GetID())
		b.reply(bot, chatID, 0, "❌ Agent error: "+err.Error())
		return err
	}
	stopReason := ""
	if result != nil {
		stopReason = string(result.StopReason)
	}
	b.log.Debug("telegram: woken turn done", "session", st.GetID(), "stop_reason", stopReason)
	return nil
}

// isGroupKey reports whether a session key addresses a group chat: a private
// conversation is keyed by the user alone (sessionstore.SessionKey).
func isGroupKey(key string) bool {
	parts := strings.Split(key, ":")
	return len(parts) > 1 && parts[1] == "chat"
}

var _ agent.WakeSurface = (*Bot)(nil)
