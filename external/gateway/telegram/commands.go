//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ── /model ───────────────────────────────────────────────────────────────────

func (b *Bot) handleModelCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	cfg := b.runner.Cfg()
	if len(cfg.Models) == 0 {
		b.reply(bot, msg.Chat.ID, msg.MessageID, "⚠️ No models configured.")
		return
	}
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(bot, msg.Chat.ID, msg.MessageID, "❌ Session error: "+err.Error())
		return
	}
	current := st.EffectiveModelID(cfg)
	kb := buildModelKeyboard(cfg.Models, current)
	b.log.Debug("telegram: model menu",
		"session", st.GetID(),
		"current", current,
		"models", len(cfg.Models),
	)
	m := tgbotapi.NewMessage(msg.Chat.ID, modelMenuText(current))
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = kb
	if _, err := bot.Send(m); err != nil {
		b.log.Warn("telegram: send model menu", "err", err)
	}
}

func modelMenuText(current string) string {
	return fmt.Sprintf("*LLM model*\n\nCurrent: `%s`\n\nSelect a new model:", current)
}

func buildModelKeyboard(models []config.ModelEntry, current string) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(models))
	for _, m := range models {
		label := m.Model
		if m.Model == current {
			label = "✓ " + label
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, callbackActionModel+":"+modelCallbackValue(m.Model)),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// ── Callback payloads ────────────────────────────────────────────────────────

const (
	callbackActionModel = "model"

	// telegramCallbackDataMax is Telegram's hard limit on callback_data.
	telegramCallbackDataMax = 64
	// callbackDigestMarker prefixes the short form used for a value that does
	// not fit that limit. A session id cannot carry it (ValidateFolderSessionID
	// admits letters, digits, underscore and hyphen), so the two forms never
	// collide there.
	callbackDigestMarker = "#"
)

// callbackValue returns the payload a button carries for value, next to a
// prefix of prefixLen bytes ("model:", "resume:s:"). A value that fits travels
// as itself; a longer one travels as a digest, because truncating it would send
// back a name nothing is stored under - the tap would be rejected and the
// keyboard would look broken with nothing to explain it.
func callbackValue(prefixLen int, value string) string {
	if prefixLen+len(value) <= telegramCallbackDataMax {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return callbackDigestMarker + hex.EncodeToString(sum[:8])
}

// modelCallbackValue returns the payload carried by a model button.
func modelCallbackValue(model string) string {
	return callbackValue(len(callbackActionModel)+1, model)
}

// resolveModelCallback maps a payload back to a configured model id. A model
// dropped from the configuration since the keyboard was sent resolves to
// nothing, which is a better answer than applying a name that is gone.
func resolveModelCallback(models []config.ModelEntry, payload string) (string, bool) {
	for i := range models {
		if models[i].Model == payload {
			return payload, true
		}
	}
	if !strings.HasPrefix(payload, callbackDigestMarker) {
		return "", false
	}
	for i := range models {
		if modelCallbackValue(models[i].Model) == payload {
			return models[i].Model, true
		}
	}
	return "", false
}

// ── /context ─────────────────────────────────────────────────────────────────

func (b *Bot) handleContextCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(bot, msg.Chat.ID, msg.MessageID, "❌ Session error: "+err.Error())
		return
	}
	bd := st.GetLastContextBreakdown()
	b.log.Debug("telegram: context command", "session", st.GetID(), "has_breakdown", bd != nil)
	if bd == nil {
		b.reply(bot, msg.Chat.ID, msg.MessageID,
			"📊 *Context usage*\n\nNo data yet — send a message first.")
		return
	}
	text := formatContextBreakdown(bd, st.GetID())
	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ReplyToMessageID = msg.MessageID
	m.ParseMode = tgbotapi.ModeMarkdown
	if _, err := bot.Send(m); err != nil {
		b.log.Warn("telegram: send context", "err", err)
	}
}

func formatContextBreakdown(bd *session.ContextBreakdown, sessionID string) string {
	fmtN := func(n int) string { return fmt.Sprintf("%d", n) }
	rows := []struct {
		label string
		val   int
	}{
		{"Conversation", bd.Conversation},
		{"System prompt", bd.SystemPrompt},
		{"Tool definitions", bd.ToolDefinitions},
		{"Rules", bd.Rules},
		{"Skills", bd.Skills},
		{"MCP", bd.MCP},
		{"Subagents", bd.Subagents},
	}
	var sb strings.Builder
	sb.WriteString("📊 *Context usage*\n")
	sb.WriteString("`" + sessionID + "`\n\n")
	for _, r := range rows {
		if r.val == 0 {
			continue
		}
		fmt.Fprintf(&sb, "%-20s `%s`\n", r.label+":", fmtN(r.val))
	}
	sb.WriteString("\n*Total ≈ " + fmtN(bd.EstimatedTotal) + " tokens*")
	sb.WriteString("\n_Estimate: runes ÷ 4_")
	return sb.String()
}

// ── Callback query handler ────────────────────────────────────────────────────

func (b *Bot) handleCallback(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery) {
	// Always acknowledge immediately to dismiss the loading spinner. This is
	// the query's only answer: Telegram takes one per query, so whatever goes
	// wrong later is said in the chat (replyToTap, showMCPFailure).
	_, _ = bot.Request(tgbotapi.NewCallback(cbq.ID, ""))

	b.log.Debug("telegram: update", "kind", "callback", "id", cbq.ID, "data", cbq.Data)

	if cbq.Data == "" || cbq.Message == nil || cbq.From == nil {
		b.log.Debug("telegram: callback ignored", "reason", "incomplete update", "id", cbq.ID)
		return
	}

	chatID := cbq.Message.Chat.ID
	userID := cbq.From.ID
	isGroup := cbq.Message.Chat.IsGroup() || cbq.Message.Chat.IsSuperGroup() || cbq.Message.Chat.IsChannel()

	level := access.EffectiveAccess(chatID, b.cfg)
	if !access.CanAccess(userID, level, b.cfg) {
		b.log.Debug("telegram: callback ignored", "reason", "access denied", "user", userID, "chat", chatID)
		return
	}

	action, payload, split := strings.Cut(cbq.Data, ":")
	if split && action == callbackActionPermission {
		// A permission button answers a request that is already waiting; it
		// configures nothing, so no session is loaded for it.
		b.answerPermissionTap(bot, cbq, payload)
		return
	}
	if !split || payload == "" || !knownCallbackAction(action) {
		b.log.Debug("telegram: callback ignored", "reason", "unrecognised payload",
			"data", cbq.Data, "user", userID, "chat", chatID)
		return
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	key := sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup)

	// A resume tap names the session the chat moves to, so the chat's current
	// session is not loaded first: for a chat that never spoke, that would
	// mint a session only to leave it a moment later.
	if action == callbackActionResume {
		b.handleResumeCallback(ctx, bot, cbq, key, payload)
		return
	}
	if action == callbackActionMCP {
		b.handleMCPCallback(ctx, bot, cbq, payload)
		return
	}

	// The keyboard outlives the process that sent it: a chat keeps showing the
	// buttons long after a restart, and a tap then addresses a session that is
	// on disk but not loaded. Only a live session can be configured, so load it
	// here rather than handing the manager an id it will not recognise.
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.log.Warn("telegram: callback session", "err", err, "user", userID, "chat", chatID)
		b.replyToTap(bot, cbq, "❌ Session error: "+err.Error())
		return
	}
	sessionID := st.GetID()

	value := payload
	if action == callbackActionModel {
		model, ok := resolveModelCallback(b.runner.Cfg().Models, payload)
		if !ok {
			b.log.Warn("telegram: callback model unknown", "payload", payload, "session", sessionID)
			b.replyToTap(bot, cbq, "❌ That model is no longer configured.")
			return
		}
		value = model
	}

	b.log.Debug("telegram: callback",
		"action", action,
		"value", value,
		"session", sessionID,
		"user", userID,
		"chat", chatID,
	)

	if action == callbackActionModel {
		b.applyModel(ctx, bot, cbq, sessionID, value)
	}
}

// replyToTap tells the chat why a tap did nothing, as a reply to the message
// whose button was pressed. It cannot be an alert on the callback query:
// handleCallback answered the query as the tap arrived, and Telegram refuses a
// second answer, so the person would see nothing at all.
func (b *Bot) replyToTap(bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, text string) {
	b.reply(bot, cbq.Message.Chat.ID, cbq.Message.MessageID, text)
}

// knownCallbackAction reports whether action names a keyboard this bot sends.
func knownCallbackAction(action string) bool {
	switch action {
	case callbackActionModel, callbackActionResume, callbackActionMCP:
		return true
	}
	return false
}

func (b *Bot) applyModel(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, sessionID, newModel string) {
	_, err := b.runner.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
		SessionID: sessionID,
		ConfigID:  "model",
		Value:     newModel,
	})
	if err != nil {
		b.log.Warn("telegram: set model", "err", err, "session", sessionID, "model", newModel)
		b.replyToTap(bot, cbq, "❌ "+err.Error())
		return
	}
	b.log.Info("telegram: model applied", "session", sessionID, "model", newModel)
	// The gateway is a surface of its own: the model the operator picked here
	// is what the next fresh chat session starts on.
	b.store.SetLastModel(newModel)
	cfg := b.runner.Cfg()
	edit := tgbotapi.NewEditMessageTextAndMarkup(
		cbq.Message.Chat.ID,
		cbq.Message.MessageID,
		modelMenuText(newModel),
		buildModelKeyboard(cfg.Models, newModel),
	)
	edit.ParseMode = tgbotapi.ModeMarkdown
	if _, err := bot.Request(edit); err != nil {
		b.log.Debug("telegram: edit model message", "err", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// ensureSession gets or creates the session for this key.
//
// A conversation this gateway starts is stamped with where it came from, so a
// listing can tell a chat somebody is holding in Telegram from one opened on
// this host. Only a chat the store has never seen is stamped: an id it already
// knows belongs to a session that was stamped when it began, and an id this
// gateway did not mint is somebody else's conversation to name.
func (b *Bot) ensureSession(ctx context.Context, key string) (*session.State, error) {
	fresh := b.store.Peek(key) == ""
	sessionID := b.store.Get(key)
	st, err := b.runner.EnsureHTTPSession(ctx, sessionID, b.cwd)
	if err != nil {
		return nil, err
	}
	if fresh {
		st.SetOrigin(session.GatewayOrigin("telegram"))
	}
	b.applyInitialModel(ctx, st)
	return st, nil
}

// applyInitialModel stamps the gateway's own model on a session that is just
// beginning - no transcript and no saved pick: the model an operator last
// chose on this surface, or the alphabetically first configured one on the
// gateway's very first use. A session that already chose - including a pick
// on an empty transcript - keeps its model, and so does a resumed one.
func (b *Bot) applyInitialModel(ctx context.Context, st *session.State) {
	if st == nil || st.GetSelectedModelID() != "" || len(st.GetMessages()) != 0 {
		return
	}
	cfg := b.runner.Cfg()
	initial := cfg.FirstModelID()
	if last := b.store.LastModel(); cfg.FindModelEntry(last) != nil {
		initial = last
	}
	if initial == "" || initial == st.EffectiveModelID(cfg) {
		return
	}
	if _, err := b.runner.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
		SessionID: st.GetID(), ConfigID: "model", Value: initial,
	}); err != nil {
		b.log.Warn("telegram: initial model", "err", err, "session", st.GetID(), "model", initial)
	}
}
