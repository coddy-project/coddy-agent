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

func (b *Bot) mcpMenu(ctx context.Context) (string, tgbotapi.InlineKeyboardMarkup, error) {
	rows, err := mcp.ListStatus(ctx, b.runner.Cfg(), b.cwd, b.log)
	if err != nil {
		return "", tgbotapi.InlineKeyboardMarkup{}, err
	}
	if len(rows) == 0 {
		return "No MCP servers configured.", tgbotapi.NewInlineKeyboardMarkup(), nil
	}
	lines := []string{"MCP servers:"}
	buttons := make([][]tgbotapi.InlineKeyboardButton, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("%s · %s · %d tools", row.Name, row.Status, len(row.Tools)))
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
	return strings.Join(lines, "\n"), tgbotapi.NewInlineKeyboardMarkup(buttons...), nil
}

func (b *Bot) handleMCPCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	text, keyboard, err := b.mcpMenu(ctx)
	if err != nil {
		b.reply(bot, msg.Chat.ID, msg.MessageID, "❌ MCP: "+err.Error())
		return
	}
	answer := tgbotapi.NewMessage(msg.Chat.ID, text)
	answer.ReplyToMessageID = msg.MessageID
	answer.ReplyMarkup = keyboard
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
		_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ MCP: "+err.Error()))
		return
	}
	for _, row := range rows {
		prefix := callbackActionMCP + ":" + value + ":"
		if callbackValue(len(prefix), row.Config.Name) != nameToken {
			continue
		}
		if mcp.NewTrustGate(b.runner.Cfg()).Evaluate(b.cwd, row) != mcp.TrustStateAllowed {
			_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "This server needs approval outside Telegram."))
			return
		}
		if err := mcp.SetStatus(b.runner.Cfg(), b.cwd, row.Config.Name, "", value == "1"); err != nil {
			_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "❌ MCP: "+err.Error()))
			return
		}
		b.log.Info("telegram: mcp server toggled", "server", row.Config.Name,
			"enabled", value == "1", "user", cbq.From.ID, "chat", cbq.Message.Chat.ID)
		if refresher, ok := b.runner.(interface{ RefreshMCPServers(context.Context) }); ok {
			refresher.RefreshMCPServers(ctx)
		}
		text, keyboard, err := b.mcpMenu(ctx)
		if err == nil {
			edit := tgbotapi.NewEditMessageTextAndMarkup(cbq.Message.Chat.ID, cbq.Message.MessageID, text, keyboard)
			if _, err := bot.Request(edit); err != nil {
				b.log.Warn("telegram: edit MCP menu", "err", err)
			}
		}
		return
	}
	_, _ = bot.Request(tgbotapi.NewCallbackWithAlert(cbq.ID, "MCP server no longer exists."))
}
