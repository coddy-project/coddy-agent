//go:build gateway || gateway.telegram

package telegram

import (
	"encoding/json"
	"net/url"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// The web UI as this bot's Mini App (gateways.telegram.mini_app): the bot's
// menu button opens it, and /app answers with a button that opens the chat's
// own conversation there. A conversation in a chat is an ordinary session, so
// the web UI already shows it, live. The SPA adapts to Telegram by itself and
// needs nothing from here; the bot only says where the web UI is. The library
// has no Mini App types, so both calls go through MakeRequest with JSON of
// our own, the way richclient.go sends Rich Messages.

const (
	// miniAppMenuText is the label of the menu button beside the message field.
	miniAppMenuText = "Coddy"
	// miniAppButtonText is the label of the button /app answers with.
	miniAppButtonText = "Open in Coddy"
)

// webAppInfo is the Bot API WebAppInfo: the address a button opens as a Mini App.
type webAppInfo struct {
	URL string `json:"url"`
}

// menuButton is the Bot API MenuButton: commands, web_app or default.
type menuButton struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	WebApp *webAppInfo `json:"web_app,omitempty"`
}

// linkButton is an inline keyboard button that opens an address: as a Mini App
// (WebApp, private chats only) or in the browser (URL).
type linkButton struct {
	Text   string      `json:"text"`
	URL    string      `json:"url,omitempty"`
	WebApp *webAppInfo `json:"web_app,omitempty"`
}

type linkKeyboard struct {
	InlineKeyboard [][]linkButton `json:"inline_keyboard"`
}

// miniAppLink is the address that opens sessionID in the web UI published at
// base: the session goes into the query, because the fragment is where
// Telegram puts its launch parameters, and the SPA moves it to its own route.
// No session opens the start screen.
func miniAppLink(base, sessionID string) string {
	if sessionID == "" {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("session", sessionID)
	u.RawQuery = q.Encode()
	return u.String()
}

// SetWebUIGate gives the bot what decides whether it may hand out the web
// UI's address: the menu button on every start, /app every time. The menu
// button is shown to everybody who opens the bot, whatever default_access
// says, so an open web UI is not advertised unless the operator said so.
func (b *Bot) SetWebUIGate(gate func() (allowed bool, note string)) {
	b.webUIGate = gate
}

// webUIAllowed asks the gate; without one the address may be handed out.
func (b *Bot) webUIAllowed() (bool, string) {
	if b.webUIGate == nil {
		return true, ""
	}
	return b.webUIGate()
}

// syncMenuButton points the bot's menu button at the web UI when the
// configuration asks for it and the web UI may be advertised, on every start.
// The first time, it remembers the button it replaces - one set in @BotFather,
// or the commands - in the gateway's store, per bot. When the button is no
// longer wanted (url emptied, menu_button off, the web UI open), the button
// goes back to what it replaced, but only if it still opens the address this
// bot set: a button changed in @BotFather in the meantime is left alone. A bot
// that never set one never touches the menu button.
func (b *Bot) syncMenuButton(bot *tgbotapi.BotAPI) {
	app := b.cfg.MiniApp
	botID := bot.Self.ID
	set, before := b.store.MenuButton(botID)
	want := app.MenuButtonEnabled()
	withheld := false
	if want {
		allowed, note := b.webUIAllowed()
		if !allowed {
			b.log.Warn("telegram: menu button not pointed at the web UI", "url", app.URL, "why", note)
			want, withheld = false, true
		} else if note != "" {
			b.log.Info("telegram: web UI elsewhere", "url", app.URL, "note", note)
		}
	}
	if want {
		if set == "" {
			// The button this one replaces, to put back later. One that
			// cannot be read is not replaced, since it could not come back:
			// the next start tries again.
			current, err := getMenuButton(bot)
			if err != nil {
				b.log.Warn("telegram: read the menu button before replacing it", "err", err)
				return
			}
			if worthPuttingBack(current, app.URL) {
				if raw, err := json.Marshal(current); err == nil {
					before = string(raw)
				}
			}
		}
		if err := setMenuButton(bot, menuButton{Type: "web_app", Text: miniAppMenuText, WebApp: &webAppInfo{URL: app.URL}}); err != nil {
			b.log.Warn("telegram: set the menu button", "err", err, "url", app.URL)
			return
		}
		b.store.SetMenuButton(botID, app.URL, before)
		b.log.Info("telegram: menu button opens the web UI", "url", app.URL)
		return
	}
	if set == "" {
		return
	}
	current, err := getMenuButton(bot)
	if err != nil {
		b.log.Warn("telegram: read the menu button", "err", err)
		return
	}
	if current.Type == "web_app" && current.WebApp != nil && current.WebApp.URL == set {
		restore := menuButton{Type: "default"}
		if before != "" {
			var prev menuButton
			// The operator's own button goes back, unless it opens the
			// address the bot stops advertising for asking no sign-in.
			if json.Unmarshal([]byte(before), &prev) == nil && prev.Type != "" &&
				(!withheld || prev.WebApp == nil || prev.WebApp.URL != set) {
				restore = prev
			}
		}
		if err := setMenuButton(bot, restore); err != nil {
			b.log.Warn("telegram: put the menu button back", "err", err)
			return
		}
		b.log.Info("telegram: menu button put back", "was", set, "now", restore.Type)
	} else {
		b.log.Info("telegram: menu button was changed elsewhere, left alone", "set_by_coddy", set)
	}
	b.store.SetMenuButton(botID, "", "")
}

// worthPuttingBack reports whether a menu button the bot is about to replace
// should come back later: a Mini App the operator set, not the commands every
// bot shows, and not the bot's own button, which opens this address under the
// bot's label (a store lost on the way). An operator's button labelled
// exactly like the bot's on the same address cannot be told from it.
func worthPuttingBack(current menuButton, url string) bool {
	if current.Type != "web_app" || current.WebApp == nil {
		return false
	}
	return current.WebApp.URL != url || current.Text != miniAppMenuText
}

// getMenuButton reads the bot's default menu button.
func getMenuButton(bot *tgbotapi.BotAPI) (menuButton, error) {
	var current menuButton
	resp, err := bot.MakeRequest("getChatMenuButton", tgbotapi.Params{})
	if err != nil {
		return current, err
	}
	err = json.Unmarshal(resp.Result, &current)
	return current, err
}

// setMenuButton sets the bot's default menu button.
func setMenuButton(bot *tgbotapi.BotAPI, button menuButton) error {
	params := tgbotapi.Params{}
	_ = params.AddInterface("menu_button", button)
	_, err := bot.MakeRequest("setChatMenuButton", params)
	return err
}

// handleAppCommand answers /app with one button that opens the chat's own
// conversation in the web UI, or its start screen when the chat has none yet.
// Telegram allows web_app buttons only in a private chat with the bot, so a
// group gets an ordinary link to the same address, which opens in the
// browser. The session is only looked up, never created here.
func (b *Bot) handleAppCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	chatID := msg.Chat.ID
	// The web UI is the whole agent; only the bot's admins open it from here,
	// and the server signs in only them by Telegram's launch data.
	if msg.From == nil || !b.cfg.IsAdmin(msg.From.ID) {
		b.reply(bot, chatID, msg.MessageID, "Only the bot's admins can open Coddy's web UI.")
		return
	}
	base := b.cfg.MiniApp.URL
	if base == "" {
		b.reply(bot, chatID, msg.MessageID,
			"The web UI is not this bot's Mini App yet: set gateways.telegram.mini_app.url to the public https address of the web UI.")
		return
	}
	if allowed, note := b.webUIAllowed(); !allowed {
		b.log.Warn("telegram: web UI button withheld", "chat", chatID, "why", note)
		b.reply(bot, chatID, msg.MessageID, "This bot does not hand out the web UI: "+note+".")
		return
	}
	sessionID := b.store.Peek(key)
	link := miniAppLink(base, sessionID)
	button := linkButton{Text: miniAppButtonText}
	if msg.Chat.IsPrivate() {
		button.WebApp = &webAppInfo{URL: link}
	} else {
		button.URL = link
	}
	text := "This conversation in the web UI:"
	if sessionID == "" {
		text = "The web UI (this chat has no conversation yet):"
	}
	params := tgbotapi.Params{}
	params["chat_id"] = strconv.FormatInt(chatID, 10)
	params["text"] = text
	_ = params.AddInterface("reply_markup", linkKeyboard{InlineKeyboard: [][]linkButton{{button}}})
	if msg.MessageID != 0 {
		_ = params.AddInterface("reply_parameters", replyParameters{MessageID: msg.MessageID})
	}
	if _, err := bot.MakeRequest("sendMessage", params); err != nil {
		b.log.Warn("telegram: send the web UI button", "err", err, "chat", chatID)
		return
	}
	b.log.Debug("telegram: web UI button", "chat", chatID, "session", sessionID, "web_app", button.WebApp != nil)
}
