//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ── /resume ──────────────────────────────────────────────────────────────────
//
// A chat is bound to one session, and /clear replaces it with a fresh one: the
// session it left stays on disk, out of the chat's reach. /resume is the way
// back. Alone it offers the sessions the server keeps as a keyboard of titles,
// newest first; with a query it resumes the one session the words name - by
// id, by a prefix of it or by a fragment of the title - and offers the
// keyboard when several do. The choice rewrites the same mapping /clear
// writes, so it survives a restart of the gateway.
//
// The session left behind stays loaded. /resume is a switch, not an ending:
// the chat may come straight back to it. /clear is the command that says a
// conversation is over, and dropping it from memory belongs there.

const (
	callbackActionResume = "resume"

	// resumePickMarker and resumePageMarker tell a tap on a session from a
	// tap on the navigation row inside the callback payload.
	resumePickMarker = "s"
	resumePageMarker = "p"

	// resumePageSize is how many sessions one keyboard page carries: about
	// what a phone screen shows without scrolling.
	resumePageSize = 8

	// resumeLabelMaxRunes caps a button label. Telegram cuts a longer one
	// itself, at a width that depends on the device; cutting here ends the
	// label with an ellipsis instead of mid-word and keeps the keyboard small.
	resumeLabelMaxRunes = 48
)

// resumePickPrefix precedes the session in the payload of a session button.
const resumePickPrefix = callbackActionResume + ":" + resumePickMarker + ":"

func (b *Bot) handleResumeCommand(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	chatID := msg.Chat.ID
	userID := msg.From.ID
	rows, err := b.listSessions(ctx)
	if err != nil {
		b.log.Warn("telegram: resume list", "err", err, "user", userID, "chat", chatID)
		b.reply(bot, chatID, msg.MessageID, "❌ Cannot list sessions: "+err.Error())
		return
	}
	current := b.store.Peek(key)
	query := strings.TrimSpace(msg.CommandArguments())
	if query == "" {
		b.log.Debug("telegram: resume menu", "session", current, "sessions", len(rows), "page", 0)
		if len(rows) == 0 {
			b.reply(bot, chatID, msg.MessageID, "🗂 No sessions to resume yet.")
			return
		}
		b.sendResumeMenu(bot, chatID, msg.MessageID, buildResumeMenu(rows, current, 0, "", time.Now()))
		return
	}
	matches := matchSessions(rows, query)
	b.log.Debug("telegram: resume query", "query", query, "matches", len(matches), "session", current)
	switch len(matches) {
	case 0:
		b.reply(bot, chatID, msg.MessageID, "🔍 No session matches \""+query+"\". Send /resume to pick from the list.")
	case 1:
		text, err := b.resumeSession(ctx, key, matches[0], userID, chatID)
		if err != nil {
			b.reply(bot, chatID, msg.MessageID, "❌ Cannot resume that session: "+err.Error())
			return
		}
		b.reply(bot, chatID, msg.MessageID, text)
	default:
		b.sendResumeMenu(bot, chatID, msg.MessageID, buildResumeMenu(matches, current, 0, query, time.Now()))
	}
}

// handleResumeCallback answers a tap on the resume keyboard: a page turn
// redraws the menu in place, a session pick binds the chat and replaces the
// menu with the confirmation, so the keyboard does not outlive the choice.
func (b *Bot) handleResumeCallback(ctx context.Context, bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, key, payload string) {
	chatID := cbq.Message.Chat.ID
	userID := cbq.From.ID
	kind, value, split := strings.Cut(payload, ":")
	if !split || value == "" {
		b.log.Debug("telegram: callback ignored", "reason", "unrecognised payload", "data", cbq.Data, "user", userID, "chat", chatID)
		return
	}
	rows, err := b.listSessions(ctx)
	if err != nil {
		b.log.Warn("telegram: resume list", "err", err, "user", userID, "chat", chatID)
		b.replyToTap(bot, cbq, "❌ Cannot list sessions: "+err.Error())
		return
	}
	current := b.store.Peek(key)
	switch kind {
	case resumePageMarker:
		page, err := strconv.Atoi(value)
		if err != nil {
			b.log.Debug("telegram: callback ignored", "reason", "unrecognised payload", "data", cbq.Data, "user", userID, "chat", chatID)
			return
		}
		b.log.Debug("telegram: resume menu", "session", current, "sessions", len(rows), "page", page)
		if len(rows) == 0 {
			edit := tgbotapi.NewEditMessageText(chatID, cbq.Message.MessageID, "🗂 No sessions to resume yet.")
			if _, err := bot.Request(edit); err != nil {
				b.log.Debug("telegram: edit resume menu", "err", err)
			}
			return
		}
		menu := buildResumeMenu(rows, current, page, "", time.Now())
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, cbq.Message.MessageID, menu.text, menu.keyboard)
		if _, err := bot.Request(edit); err != nil {
			b.log.Debug("telegram: edit resume menu", "err", err)
		}
	case resumePickMarker:
		row, ok := resolveResumeCallback(rows, value)
		if !ok {
			b.log.Warn("telegram: callback session unknown", "payload", value, "user", userID, "chat", chatID)
			b.replyToTap(bot, cbq, "❌ That session no longer exists.")
			return
		}
		b.log.Debug("telegram: callback",
			"action", callbackActionResume,
			"value", row.SessionID,
			"session", current,
			"user", userID,
			"chat", chatID,
		)
		text, err := b.resumeSession(ctx, key, row, userID, chatID)
		if err != nil {
			b.replyToTap(bot, cbq, "❌ Cannot resume that session: "+err.Error())
			return
		}
		// The confirmation replaces the menu, keyboard included.
		edit := tgbotapi.NewEditMessageText(chatID, cbq.Message.MessageID, text)
		if _, err := bot.Request(edit); err != nil {
			b.log.Debug("telegram: edit resume menu", "err", err)
		}
	default:
		b.log.Debug("telegram: callback ignored", "reason", "unrecognised payload", "data", cbq.Data, "user", userID, "chat", chatID)
	}
}

// resumeSession binds the chat behind key to row and returns the text that
// tells the chat so. The session is loaded first: a bundle that cannot be read
// is reported here, not on the next message, and the mapping is left as it was.
func (b *Bot) resumeSession(ctx context.Context, key string, row acp.SessionListInfo, userID, chatID int64) (string, error) {
	id := row.SessionID
	old := b.store.Peek(key)
	if old == id {
		return "▶️ This chat is already on " + resumeTitle(row) + "\n" + id, nil
	}
	if _, err := b.runner.EnsureHTTPSession(ctx, id, b.cwd); err != nil {
		b.log.Warn("telegram: resume session", "err", err, "session", id, "user", userID, "chat", chatID)
		return "", err
	}
	b.store.Bind(key, id)
	b.log.Info("telegram: session resumed", "old", old, "new", id, "user", userID, "chat", chatID)
	return "▶️ Resumed: " + resumeTitle(row) + "\n" + id + "\n\nSend a message to continue it.", nil
}

// listSessions asks the server for the sessions it keeps, newest first. No
// folder narrows the list: a chat has no working directory of its own, so it
// sees what a console connected to this server would see.
func (b *Bot) listSessions(ctx context.Context) ([]acp.SessionListInfo, error) {
	res, err := b.runner.HandleSessionList(ctx, acp.SessionListParams{})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.Sessions, nil
}

func (b *Bot) sendResumeMenu(bot *tgbotapi.BotAPI, chatID int64, replyTo int, menu resumeMenu) {
	m := tgbotapi.NewMessage(chatID, menu.text)
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = menu.keyboard
	if _, err := bot.Send(m); err != nil {
		b.log.Warn("telegram: send resume menu", "err", err, "chat", chatID)
	}
}

// matchSessions returns the sessions query names: the one whose id it is, or
// otherwise every one whose id starts with it or whose title contains it,
// case-insensitively. A query is what a person types from memory, so it is
// read loosely; the exact id is checked first, byte for byte, because an id
// spelled in full can be nothing else - ids are folder names, and on Linux
// two of them may differ only in case, so folding here could pick the wrong
// one in silence. An id typed in another case falls through to the prefix
// match, which offers every spelling.
func matchSessions(rows []acp.SessionListInfo, query string) []acp.SessionListInfo {
	exact := strings.TrimSpace(query)
	q := strings.ToLower(exact)
	if q == "" {
		return nil
	}
	for _, row := range rows {
		if row.SessionID == exact {
			return []acp.SessionListInfo{row}
		}
	}
	var out []acp.SessionListInfo
	for _, row := range rows {
		if strings.HasPrefix(strings.ToLower(row.SessionID), q) || strings.Contains(strings.ToLower(rowTitle(row)), q) {
			out = append(out, row)
		}
	}
	return out
}

// resumeMenu is one page of the keyboard with the text above it.
type resumeMenu struct {
	text     string
	keyboard tgbotapi.InlineKeyboardMarkup
}

// buildResumeMenu lays out one page of the keyboard over rows: a button per
// session, the chat's current one marked, and a navigation row when there is
// more than one page. With a query the rows are the matches and there is no
// navigation - the query would not fit the callback payload, so a second page
// could not be asked for - and the text says how many matched instead; a
// narrower query is the way to the rest.
func buildResumeMenu(rows []acp.SessionListInfo, current string, page int, query string, now time.Time) resumeMenu {
	pages := (len(rows) + resumePageSize - 1) / resumePageSize
	if pages < 1 || query != "" {
		pages = 1
	}
	page = max(0, min(page, pages-1))
	start := page * resumePageSize
	end := min(start+resumePageSize, len(rows))

	kb := make([][]tgbotapi.InlineKeyboardButton, 0, resumePageSize+1)
	for _, row := range rows[start:end] {
		kb = append(kb, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(
			resumeButtonLabel(row, row.SessionID == current, now),
			resumePickPrefix+callbackValue(len(resumePickPrefix), row.SessionID),
		)))
	}
	var nav []tgbotapi.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀ Prev", resumePageCallback(page-1)))
	}
	if page < pages-1 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("Next ▶", resumePageCallback(page+1)))
	}
	if len(nav) > 0 {
		kb = append(kb, nav)
	}

	var sb strings.Builder
	sb.WriteString("🗂 Resume a session\n")
	if current != "" {
		sb.WriteString("Current: " + current + "\n")
	} else {
		sb.WriteString("Current: none yet\n")
	}
	switch {
	case query != "" && end < len(rows):
		fmt.Fprintf(&sb, "%s match \"%s\", showing the first %d", sessionsWord(len(rows)), query, end)
	case query != "":
		fmt.Fprintf(&sb, "%s match \"%s\"", sessionsWord(len(rows)), query)
	default:
		fmt.Fprintf(&sb, "Page %d of %d · %s", page+1, pages, sessionsWord(len(rows)))
	}
	sb.WriteString("\n\nPick one below, or send /resume <id or title>.")
	return resumeMenu{text: sb.String(), keyboard: tgbotapi.NewInlineKeyboardMarkup(kb...)}
}

func resumePageCallback(page int) string {
	return callbackActionResume + ":" + resumePageMarker + ":" + strconv.Itoa(page)
}

// resolveResumeCallback maps the payload of a session button back to a row of
// the current listing. A session deleted since the keyboard was sent resolves
// to nothing, which is a better answer than binding the chat to a bundle that
// is gone.
func resolveResumeCallback(rows []acp.SessionListInfo, payload string) (acp.SessionListInfo, bool) {
	for _, row := range rows {
		if row.SessionID == payload {
			return row, true
		}
	}
	if !strings.HasPrefix(payload, callbackDigestMarker) {
		return acp.SessionListInfo{}, false
	}
	for _, row := range rows {
		if callbackValue(len(resumePickPrefix), row.SessionID) == payload {
			return row, true
		}
	}
	return acp.SessionListInfo{}, false
}

// resumeButtonLabel is what a session button shows: the title, or the id of a
// session that has none yet, then how long ago it was last touched - two chats
// titled the same are told apart by it - and a mark on the chat's own session.
func resumeButtonLabel(row acp.SessionListInfo, current bool, now time.Time) string {
	label := rowTitle(row)
	if label == "" {
		label = row.SessionID
	}
	label = truncateRunes(label, resumeLabelMaxRunes)
	if age := relativeAge(row.UpdatedAt, now); age != "" {
		label += " · " + age
	}
	if current {
		label = "✓ " + label
	}
	return label
}

// resumeTitle names a session in prose: its title, or a placeholder when it
// has none yet, next to the id the caller prints on its own line.
func resumeTitle(row acp.SessionListInfo) string {
	if t := rowTitle(row); t != "" {
		return t
	}
	return "an untitled session"
}

func rowTitle(row acp.SessionListInfo) string {
	if row.Title == nil {
		return ""
	}
	return strings.TrimSpace(*row.Title)
}

func sessionsWord(n int) string {
	if n == 1 {
		return "1 session"
	}
	return strconv.Itoa(n) + " sessions"
}

// truncateRunes cuts s to at most n runes, ending it with an ellipsis when it
// was longer. Counting runes, not bytes, keeps a Cyrillic title from being cut
// inside a character.
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n < 1 {
		return "…"
	}
	return string(rs[:n-1]) + "…"
}

// relativeAge reads a session's last-update stamp as an age: minutes and hours
// for today, days for the past month, the date beyond that. An empty or
// unreadable stamp answers "", and the label goes out without one.
func relativeAge(stamp *string, now time.Time) string {
	if stamp == nil {
		return ""
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*stamp))
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}
