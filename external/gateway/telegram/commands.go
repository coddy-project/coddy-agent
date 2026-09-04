//go:build gateway || gateway.telegram

package telegram

import (
	"context"
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
		reply(bot, msg.Chat.ID, msg.MessageID, "❌ Session error: "+err.Error())
		return
	}
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
		reply(bot, msg.Chat.ID, msg.MessageID, "⚠️ No models configured.")
		return
	}
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		reply(bot, msg.Chat.ID, msg.MessageID, "❌ Session error: "+err.Error())
		return
	}
	current := st.EffectiveModelID(cfg)
	kb := buildModelKeyboard(cfg.Models, current)
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
		// Callback data must fit in 64 bytes; prefix "model:" = 6 chars.
		data := "model:" + m.Model
		if len(data) > 64 {
			data = data[:64]
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// ── /context ─────────────────────────────────────────────────────────────────

func (b *Bot) handleContextCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		reply(bot, msg.Chat.ID, msg.MessageID, "❌ Session error: "+err.Error())
		return
	}
	bd := st.GetLastContextBreakdown()
	if bd == nil {
		reply(bot, msg.Chat.ID, msg.MessageID,
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
		sb.WriteString(fmt.Sprintf("%-20s `%s`\n", r.label+":", fmtN(r.val)))
	}
	sb.WriteString("\n*Total ≈ " + fmtN(bd.EstimatedTotal) + " tokens*")
	sb.WriteString("\n_Estimate: runes ÷ 4_")
	return sb.String()
}

// ── Callback query handler ────────────────────────────────────────────────────

func (b *Bot) handleCallback(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery) {
	// Always acknowledge immediately to dismiss the loading spinner.
	_, _ = bot.Request(tgbotapi.NewCallback(cbq.ID, ""))

	if cbq.Data == "" || cbq.Message == nil || cbq.From == nil {
		return
	}

	chatID := cbq.Message.Chat.ID
	userID := cbq.From.ID
	isGroup := cbq.Message.Chat.IsGroup() || cbq.Message.Chat.IsSuperGroup() || cbq.Message.Chat.IsChannel()

	level := access.EffectiveAccess(chatID, b.cfg)
	if !access.CanAccess(userID, level, b.cfg) {
		return
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	key := sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup)
	sessionID := b.store.Get(key)

	parts := strings.SplitN(cbq.Data, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return
	}

	switch parts[0] {
	case "mode":
		b.applyMode(ctx, bot, cbq, sessionID, parts[1])
	case "model":
		b.applyModel(ctx, bot, cbq, sessionID, parts[1])
	}
}

func (b *Bot) applyMode(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, sessionID, newMode string) {
	err := b.runner.HandleSessionSetMode(ctx, acp.SessionSetModeParams{
		SessionID: sessionID,
		ModeID:    newMode,
	})
	if err != nil {
		b.log.Warn("telegram: set mode", "err", err)
		_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ "+err.Error()))
		return
	}
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
		b.log.Warn("telegram: set model", "err", err)
		_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ "+err.Error()))
		return
	}
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
