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

// ── /mode ────────────────────────────────────────────────────────────────────

func (b *Bot) handleModeCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(bot, msg.Chat.ID, msg.MessageID, "❌ Session error: "+err.Error())
		return
	}
	b.log.Debug("telegram: mode menu", "session", st.GetID(), "current", st.GetMode())
	kb := buildModeKeyboard(st.GetMode())
	m := tgbotapi.NewMessage(msg.Chat.ID, modeMenuText(st.GetMode()))
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = kb
	if _, err := bot.Send(m); err != nil {
		b.log.Warn("telegram: send mode menu", "err", err)
	}
}

func modeMenuText(current string) string {
	desc := map[string]string{
		string(session.ModeAgent): "executes tasks with full tool access",
		string(session.ModePlan):  "designs and plans without code execution",
		string(session.ModeAsk):   "answers questions with read-only research tools",
	}
	return fmt.Sprintf("*Session mode*\n\nCurrent: *%s* — %s\n\nSelect a new mode:", current, desc[current])
}

func buildModeKeyboard(current string) tgbotapi.InlineKeyboardMarkup {
	modes := []struct{ id, label string }{
		{string(session.ModeAgent), "Agent"},
		{string(session.ModePlan), "Plan"},
		{string(session.ModeAsk), "Ask"},
	}
	row := make([]tgbotapi.InlineKeyboardButton, 0, len(modes))
	for _, m := range modes {
		label := m.label
		if m.id == current {
			label = "✓ " + label
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, "mode:"+m.id))
	}
	return tgbotapi.NewInlineKeyboardMarkup(row)
}

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
	callbackActionMode  = "mode"
	callbackActionModel = "model"

	// telegramCallbackDataMax is Telegram's hard limit on callback_data.
	telegramCallbackDataMax = 64
	// modelDigestMarker prefixes the short form used for a model id that does
	// not fit that limit.
	modelDigestMarker = "#"
)

// modelCallbackValue returns the payload carried by a model button. An id that
// fits travels as itself; a longer one travels as a digest, because truncating
// it would send back a name nothing is configured under - the tap would be
// rejected and the keyboard would look broken with nothing to explain it.
func modelCallbackValue(model string) string {
	if len(callbackActionModel)+1+len(model) <= telegramCallbackDataMax {
		return model
	}
	sum := sha256.Sum256([]byte(model))
	return modelDigestMarker + hex.EncodeToString(sum[:8])
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
	if !strings.HasPrefix(payload, modelDigestMarker) {
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
	// Always acknowledge immediately to dismiss the loading spinner.
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
	if !split || payload == "" || (action != callbackActionMode && action != callbackActionModel) {
		b.log.Debug("telegram: callback ignored", "reason", "unrecognised payload",
			"data", cbq.Data, "user", userID, "chat", chatID)
		return
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	key := sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup)

	// The keyboard outlives the process that sent it: a chat keeps showing the
	// buttons long after a restart, and a tap then addresses a session that is
	// on disk but not loaded. Only a live session can be configured, so load it
	// here rather than handing the manager an id it will not recognise.
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.log.Warn("telegram: callback session", "err", err, "user", userID, "chat", chatID)
		_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ Session error: "+err.Error()))
		return
	}
	sessionID := st.GetID()

	value := payload
	if action == callbackActionModel {
		model, ok := resolveModelCallback(b.runner.Cfg().Models, payload)
		if !ok {
			b.log.Warn("telegram: callback model unknown", "payload", payload, "session", sessionID)
			_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ That model is no longer configured."))
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

	switch action {
	case callbackActionMode:
		b.applyMode(ctx, bot, cbq, sessionID, value)
	case callbackActionModel:
		b.applyModel(ctx, bot, cbq, sessionID, value)
	}
}

func (b *Bot) applyMode(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, sessionID, newMode string) {
	err := b.runner.HandleSessionSetMode(ctx, acp.SessionSetModeParams{
		SessionID: sessionID,
		ModeID:    newMode,
	})
	if err != nil {
		b.log.Warn("telegram: set mode", "err", err, "session", sessionID, "mode", newMode)
		_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ "+err.Error()))
		return
	}
	b.log.Info("telegram: mode applied", "session", sessionID, "mode", newMode)
	// Update the keyboard in-place so the user sees the new selection immediately.
	edit := tgbotapi.NewEditMessageTextAndMarkup(
		cbq.Message.Chat.ID,
		cbq.Message.MessageID,
		modeMenuText(newMode),
		buildModeKeyboard(newMode),
	)
	edit.ParseMode = tgbotapi.ModeMarkdown
	if _, err := bot.Request(edit); err != nil {
		b.log.Debug("telegram: edit mode message", "err", err)
	}
}

func (b *Bot) applyModel(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, sessionID, newModel string) {
	_, err := b.runner.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
		SessionID: sessionID,
		ConfigID:  "model",
		Value:     newModel,
	})
	if err != nil {
		b.log.Warn("telegram: set model", "err", err, "session", sessionID, "model", newModel)
		_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ "+err.Error()))
		return
	}
	b.log.Info("telegram: model applied", "session", sessionID, "model", newModel)
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
func (b *Bot) ensureSession(ctx context.Context, key string) (*session.State, error) {
	sessionID := b.store.Get(key)
	return b.runner.EnsureHTTPSession(ctx, sessionID, b.cwd)
}
