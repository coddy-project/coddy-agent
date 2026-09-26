//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const callbackActionMCP = "mcp"

// mcpMenu renders the /mcp menu: one line per server and a button for each
// server the workspace trusts. The keyboard is nil when there is no button to
// offer, and the caller then sends no reply_markup at all: a keyboard built
// from no rows reaches Telegram as {"inline_keyboard":null}, which it refuses,
// and the chat would get nothing.
func (b *Bot) mcpMenu(ctx context.Context) (string, *tgbotapi.InlineKeyboardMarkup, error) {
	rows, err := mcp.ListStatus(ctx, b.runner.Cfg(), b.cwd, b.log)
	if err != nil {
		return "", nil, err
	}
	if len(rows) == 0 {
		return "No MCP servers configured.", nil, nil
	}
	lines := []string{"MCP servers:"}
	buttons := make([][]tgbotapi.InlineKeyboardButton, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, mcpMenuLine(row))
		if !row.Trusted {
			continue
		} // Chat cannot grant workspace trust.
		value := "1"
		label := "Enable " + row.Name
		if row.Enabled {
			value, label = "0", "Disable "+row.Name
		}
		prefix := callbackActionMCP + ":" + value + ":"
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, prefix+callbackValue(len(prefix), row.Name))))
	}
	if len(buttons) == 0 {
		return strings.Join(lines, "\n"), nil, nil
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)
	return strings.Join(lines, "\n"), &keyboard, nil
}

// mcpMenuLine is one server of the menu: name, status, tool count. A trust
// verdict (needs_approval, denied) takes the status slot and would hide that
// the server is also switched off, which approving it would not change, so a
// server that is off says so whenever its status does not.
func mcpMenuLine(row mcp.ServerStatus) string {
	status := row.Status
	if !row.Enabled && row.Status != "disabled" {
		status += " · off"
	}
	return fmt.Sprintf("%s · %s · %d tools", row.Name, status, len(row.Tools))
}

func (b *Bot) handleMCPCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	text, keyboard, err := b.mcpMenu(ctx)
	if err != nil {
		b.reply(bot, msg.Chat.ID, msg.MessageID, "❌ MCP: "+err.Error())
		return
	}
	answer := tgbotapi.NewMessage(msg.Chat.ID, text)
	answer.ReplyToMessageID = msg.MessageID
	if keyboard != nil {
		answer.ReplyMarkup = *keyboard
	}
	if _, err := bot.Send(answer); err != nil {
		b.log.Warn("telegram: send MCP menu", "err", err)
	}
}

func (b *Bot) handleMCPCallback(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, payload string) {
	value, nameToken, ok := strings.Cut(payload, ":")
	if !ok || (value != "0" && value != "1") {
		return
	}
	rows, err := mcp.ListManagedServers(b.runner.Cfg(), b.cwd)
	if err != nil {
		b.showMCPFailure(ctx, bot, cbq, "❌ MCP: "+err.Error())
		return
	}
	for _, row := range rows {
		prefix := callbackActionMCP + ":" + value + ":"
		if callbackValue(len(prefix), row.Config.Name) != nameToken {
			continue
		}
		if mcp.NewTrustGate(b.runner.Cfg()).Evaluate(b.cwd, row) != mcp.TrustStateAllowed {
			b.showMCPFailure(ctx, bot, cbq, "❌ This server needs approval outside Telegram.")
			return
		}
		if err := mcp.SetStatus(b.runner.Cfg(), b.cwd, row.Config.Name, "", value == "1"); err != nil {
			b.showMCPFailure(ctx, bot, cbq, "❌ MCP: "+err.Error())
			return
		}
		b.log.Info("telegram: mcp server toggled", "server", row.Config.Name,
			"enabled", value == "1", "user", cbq.From.ID, "chat", cbq.Message.Chat.ID)
		// Live sessions hold clients of their own; the manager reconciles the
		// one server that changed and bounds the time that takes itself.
		if refresher, ok := b.runner.(interface {
			RefreshMCPServer(ctx context.Context, name string)
		}); ok {
			refresher.RefreshMCPServer(ctx, row.Config.Name)
		}
		text, keyboard, err := b.mcpMenu(ctx)
		if err != nil {
			// The switch landed but the menu cannot show it. The old buttons
			// would now undo it, so they go and the failure takes their place.
			b.log.Warn("telegram: mcp menu after toggle", "err", err, "server", row.Config.Name, "chat", cbq.Message.Chat.ID)
			b.editMCPMenu(bot, cbq, "❌ MCP: "+err.Error(), nil)
			return
		}
		b.editMCPMenu(bot, cbq, text, keyboard)
		return
	}
	b.showMCPFailure(ctx, bot, cbq, "❌ MCP server no longer exists.")
}

// showMCPFailure tells the chat why a tap on the menu did nothing. The query
// cannot carry it: handleCallback answered it as the tap arrived, and Telegram
// takes one answer per query, so an alert sent now would be refused unseen.
// The failure becomes the first line of the menu message instead, above the
// menu drawn afresh so that its buttons match the servers as they are now; a
// menu that cannot be drawn leaves the failure alone, without the old buttons.
func (b *Bot) showMCPFailure(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, failure string) {
	b.log.Warn("telegram: mcp callback failed", "reason", failure, "user", cbq.From.ID, "chat", cbq.Message.Chat.ID)
	text := failure
	var keyboard *tgbotapi.InlineKeyboardMarkup
	if menu, kb, err := b.mcpMenu(ctx); err == nil {
		text, keyboard = failure+"\n\n"+menu, kb
	}
	b.editMCPMenu(bot, cbq, text, keyboard)
}

// editMCPMenu rewrites the menu message a tap came from. Without buttons the
// edit carries no markup at all, which also takes the old keyboard away: an
// empty keyboard would reach Telegram as null and be refused, edit and all.
func (b *Bot) editMCPMenu(bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(cbq.Message.Chat.ID, cbq.Message.MessageID, text)
	if keyboard != nil {
		edit = tgbotapi.NewEditMessageTextAndMarkup(cbq.Message.Chat.ID, cbq.Message.MessageID, text, *keyboard)
	}
	if _, err := bot.Request(edit); err != nil {
		b.log.Warn("telegram: edit MCP menu", "err", err)
	}
}
