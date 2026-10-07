//go:build gateway || gateway.pachca

package pachca

// From an event of the history to a turn: what the event is, whether it is
// addressed to the bot, who may talk to it, which session it belongs to.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/replyquote"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// messagePayload is a message event (MessageWebhookPayload).
type messagePayload struct {
	Type            string `json:"type"`
	ID              int64  `json:"id"`
	Event           string `json:"event"`
	EntityType      string `json:"entity_type"`
	EntityID        int64  `json:"entity_id"`
	Content         string `json:"content"`
	UserID          int64  `json:"user_id"`
	ChatID          int64  `json:"chat_id"`
	ParentMessageID *int64 `json:"parent_message_id"`
	Thread          *struct {
		MessageID     *int64 `json:"message_id"`
		MessageChatID *int64 `json:"message_chat_id"`
	} `json:"thread"`
}

// buttonPayload is a button click event (ButtonWebhookPayload).
type buttonPayload struct {
	Type      string `json:"type"`
	Event     string `json:"event"`
	MessageID int64  `json:"message_id"`
	Data      string `json:"data"`
	UserID    int64  `json:"user_id"`
	ChatID    int64  `json:"chat_id"`
}

// inbound is a message the bot will answer.
type inbound struct {
	msg     messagePayload
	text    string // the content with the bot's mention taken out
	command string // lower-case command word without the slash, "" when none
	args    string
	isGroup bool
}

// handleEvent routes one event. It reports false only when the bot is
// stopping and the event must stay in the history; everything else - handled,
// ignored or refused - is done with.
func (b *Bot) handleEvent(ctx, turnCtx context.Context, c *Client, e Event) bool {
	if b.seen.has(e.ID) {
		return true
	}
	var kind struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(e.Payload, &kind)
	switch {
	case e.EventType == "button_click" || kind.Type == "button":
		var p buttonPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			b.log.Debug("pachca: event ignored", "reason", "bad button payload", "event", e.ID)
			return true
		}
		return b.handleButton(turnCtx, c, p)
	case kind.Type == "message":
		var p messagePayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			b.log.Debug("pachca: event ignored", "reason", "bad message payload", "event", e.ID)
			return true
		}
		return b.handleMessage(ctx, turnCtx, c, p)
	default:
		b.log.Debug("pachca: event ignored", "reason", "not handled", "event_type", e.EventType)
		return true
	}
}

func (b *Bot) handleMessage(ctx, turnCtx context.Context, c *Client, p messagePayload) bool {
	if p.Event != "new" {
		b.log.Debug("pachca: event ignored", "reason", "message "+p.Event, "message", p.ID)
		return true
	}
	if p.UserID == b.selfID {
		b.posted.add(p.ID)
		return true
	}
	seenKey := "m:" + strconv.FormatInt(p.ID, 10)
	if b.seen.has(seenKey) {
		b.log.Debug("pachca: event ignored", "reason", "message delivered twice", "message", p.ID)
		return true
	}
	isGroup, err := b.isGroup(ctx, c, p.ChatID, p.EntityType)
	if err != nil {
		// Read again on the next pass rather than drop a message that may
		// well be for the bot.
		return false
	}
	b.log.Debug("pachca: update",
		"kind", "message",
		"user", p.UserID,
		"chat", p.ChatID,
		"is_group", isGroup,
		"text_len", len(p.Content),
	)
	if !b.allowed(p.ChatID, p.UserID, isGroup) {
		return true
	}

	in := inbound{msg: p, isGroup: isGroup}
	in.text = strings.TrimSpace(stripMention(p.Content, b.nickname, b.selfID))
	in.command, in.args = parseCommand(in.text)
	if in.command != "" && !knownCommand(in.command) {
		b.log.Debug("pachca: update ignored", "reason", "unknown command", "command", in.command)
		return true
	}
	addressed := true
	if isGroup {
		if addressed, err = b.addressed(ctx, c, p); err != nil {
			return false
		}
	}
	if !addressed {
		b.log.Debug("pachca: update ignored", "reason", "not addressed to the bot", "user", p.UserID, "chat", p.ChatID)
		return true
	}
	if in.text == "" && p.ParentMessageID == nil {
		return true
	}

	isolation := access.EffectiveIsolation(p.ChatID, b.cfg)
	key := sessionstore.SessionKey(adapterName, p.ChatID, p.UserID, isolation, isGroup)
	accepted, queued := b.enqueue(turnCtx, in, key)
	if !accepted {
		return false
	}
	b.seen.add(seenKey)
	if !queued {
		b.log.Debug("pachca: update rejected", "reason", "worker queue full", "key", key, "cap", workerQueueCap)
		b.reply(ctx, c, b.answerTarget(in), b.replyParent(in), "Too many messages are waiting for an answer here; this one was not taken. Send it again once I have answered.")
	}
	return true
}

// allowed applies the access level and the admin-only isolation of the chat.
func (b *Bot) allowed(chatID, userID int64, isGroup bool) bool {
	level := access.EffectiveAccess(chatID, b.cfg)
	if !access.CanAccess(userID, level, b.cfg) {
		b.log.Debug("pachca: update ignored", "reason", "access denied", "user", userID, "chat", chatID)
		return false
	}
	if isGroup && access.EffectiveIsolation(chatID, b.cfg) == config.IsolationAdmin && !b.cfg.IsAdmin(userID) {
		b.log.Debug("pachca: update ignored", "reason", "admin-only chat", "user", userID, "chat", chatID)
		return false
	}
	return true
}

// addressed reports whether a group message is for the bot. A group is
// where many people talk, so only a mention of the bot or a reply to one of
// its messages is: a command without either, or a message in a thread under
// the bot's message, is left to the people in it.
func (b *Bot) addressed(ctx context.Context, c *Client, p messagePayload) (bool, error) {
	if mentions(p.Content, b.nickname, b.selfID) {
		return true, nil
	}
	if p.ParentMessageID == nil {
		return false, nil
	}
	return b.wroteMessage(ctx, c, *p.ParentMessageID)
}

// wroteMessage reports whether the bot wrote message id: from memory, or by
// reading the message once. A read that failed for a passing reason is an
// error, so the event is read again.
func (b *Bot) wroteMessage(ctx context.Context, c *Client, id int64) (bool, error) {
	if id == 0 {
		return false, nil
	}
	if b.posted.has(id) {
		return true, nil
	}
	if author, ok := b.parents.get(id); ok {
		return author == b.selfID, nil
	}
	m, err := c.Message(ctx, id)
	if err != nil {
		b.log.Debug("pachca: read parent message", "err", err, "message", id)
		if IsTransient(err) {
			return false, err
		}
		return false, nil
	}
	b.parents.put(id, m.UserID)
	return m.UserID == b.selfID, nil
}

// mentions reports whether text names the bot, as @nickname or <@id>.
func mentions(text, nickname string, selfID int64) bool {
	if selfID != 0 && strings.Contains(text, "<@"+strconv.FormatInt(selfID, 10)+">") {
		return true
	}
	if nickname == "" {
		return false
	}
	lower := strings.ToLower(text)
	needle := "@" + strings.ToLower(nickname)
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			return false
		}
		end := i + j + len(needle)
		if end == len(lower) || !isNickRune(lower[end]) {
			return true
		}
		i = end
	}
}

func isNickRune(c byte) bool {
	return c == '_' || c == '.' || c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// stripMention takes the bot's mentions out of text, each with one space
// next to it. The lines and the indentation of what the person wrote (code,
// a log, a list) stay as they are.
func stripMention(text, nickname string, selfID int64) string {
	var needles []string
	if selfID != 0 {
		needles = append(needles, "<@"+strconv.FormatInt(selfID, 10)+">")
	}
	if nickname != "" {
		needles = append(needles, "@"+nickname)
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		n := matchMention(text, i, needles)
		if n == 0 {
			out.WriteByte(text[i])
			i++
			continue
		}
		i += n
		switch {
		case i < len(text) && text[i] == ' ':
			i++
		case out.Len() > 0 && strings.HasSuffix(out.String(), " "):
			s := out.String()
			out.Reset()
			out.WriteString(s[:len(s)-1])
		}
	}
	return strings.TrimSpace(out.String())
}

// matchMention is the length of the mention among needles that starts at
// text[i], or 0. A nickname followed by more nickname characters is somebody
// else's.
func matchMention(text string, i int, needles []string) int {
	for _, n := range needles {
		if i+len(n) > len(text) || !strings.EqualFold(text[i:i+len(n)], n) {
			continue
		}
		if strings.HasPrefix(n, "@") {
			if end := i + len(n); end < len(text) && isNickRune(toLowerASCII(text[end])) {
				continue
			}
		}
		return len(n)
	}
	return 0
}

func toLowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// parseCommand splits "/word rest" into its command word and the rest.
func parseCommand(text string) (string, string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	word, rest, _ := strings.Cut(text[1:], " ")
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" || strings.ContainsAny(word, "/\n\t") {
		return "", ""
	}
	return word, strings.TrimSpace(rest)
}

// knownCommand reports whether cmd is a built-in command or a settings
// command the bot hands to the session (/permissions aside: the bot approves
// its chat agent itself, and the mode would only change what other surfaces
// ask).
func knownCommand(cmd string) bool {
	switch cmd {
	case "start", "help", "clear", "model", "context":
		return true
	}
	return isSettingsCommand(cmd)
}

// adminOnlyNote answers a group member who is not an admin and tried to
// change the settings.
const adminOnlyNote = "Only the bot's admins can change settings in this chat."

// changesSettings reports whether cmd changes the session's settings or
// replaces the conversation.
func changesSettings(cmd string) bool {
	return cmd == "model" || cmd == "clear" || isSettingsCommand(cmd)
}

func isSettingsCommand(cmd string) bool {
	sc, ok := session.LookupSettingsCommand(cmd)
	return ok && sc.Setting != session.SettingPermissionMode
}

// answerTarget is where the answer to in goes.
func (b *Bot) answerTarget(in inbound) Target {
	if in.isGroup {
		return ChatTarget(in.msg.ChatID)
	}
	return UserTarget(in.msg.UserID)
}

// replyParent is the message an answer replies to: the person's own message
// in a group, so the answer is found next to the question; none in a direct
// chat, where the answer follows it anyway.
func (b *Bot) replyParent(in inbound) int64 {
	if in.isGroup {
		return in.msg.ID
	}
	return 0
}

// turnTimeout bounds one turn of a chat message.
const turnTimeout = 10 * time.Minute

// processMessage runs in the worker of the message's session key.
func (b *Bot) processMessage(ctx context.Context, in inbound, key string) {
	c := b.connectedClient()
	if c == nil {
		return
	}
	target := b.answerTarget(in)
	parent := b.replyParent(in)
	userID := in.msg.UserID

	if in.command != "" {
		b.log.Debug("pachca: command", "command", in.command, "session", b.store.Peek(key), "user", userID, "chat", in.msg.ChatID)
	}
	// A group shares the bot with many people: what changes the session's
	// settings, or replaces the conversation, is the admins' to do.
	if in.isGroup && changesSettings(in.command) && !b.cfg.IsAdmin(userID) {
		b.log.Debug("pachca: update refused", "reason", "settings are admin-only in a group", "user", userID, "chat", in.msg.ChatID)
		b.reply(ctx, c, target, parent, adminOnlyNote)
		return
	}
	switch in.command {
	case "clear":
		oldID := b.store.Get(key)
		newID := b.store.Reset(key)
		b.runner.ForgetLiveSession(oldID)
		b.reply(ctx, c, target, parent, "New session started.")
		b.log.Info("pachca: session cleared", "old", oldID, "new", newID, "user", userID)
		return
	case "start":
		b.reply(ctx, c, target, parent, "Hi! I'm Coddy, an AI coding assistant. Send me a question or a task; /help lists the commands.")
		return
	case "help":
		b.reply(ctx, c, target, parent, b.helpText())
		return
	case "model":
		if in.args == "" {
			b.handleModelCommand(ctx, c, target, parent, key)
			return
		}
	case "context":
		b.handleContextCommand(ctx, c, target, parent, key)
		return
	}

	text := in.text
	if text == "" && in.msg.ParentMessageID == nil {
		return
	}
	// A typed "/model <id>" is a session-scoped pick on this surface even
	// though the manager applies it inside the turn: the gateway remembers it
	// like a button click - when an admin typed it, since the remembered model
	// is what every fresh conversation of the bot starts on.
	if line, err := session.ParseSettingsCommands(text); err == nil && line.Session.Model != nil && b.cfg.IsAdmin(userID) {
		if id := strings.TrimSpace(*line.Session.Model); b.runner.Cfg().FindModelEntry(id) != nil {
			b.store.SetLastModel(id)
		}
	}

	tctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	// A reply asks about the message it answers: the session receives that
	// message quoted in front of what the person wrote. Commands are not
	// quoted - the manager reads them off the start of the text.
	if in.msg.ParentMessageID != nil && !isSettingsCommand(in.command) {
		text = b.quoteParent(tctx, c, *in.msg.ParentMessageID, text)
	}
	st, err := b.ensureSession(tctx, key)
	if err != nil {
		b.log.Warn("pachca: ensure session", "err", err)
		b.reply(ctx, c, target, parent, "Failed to start a session: "+err.Error())
		return
	}
	b.log.Debug("pachca: prompt turn", "session", st.GetID(), "user", userID, "chat", in.msg.ChatID, "prompt_len", len(text))

	sender := b.newSender(tctx, c, target, parent)
	// Somebody who is not the bot's admin gets the conversation and nothing
	// that changes the agent: the turn refuses the tools that would, and the
	// chat approves nothing it is asked about.
	var restriction *session.TurnRestriction
	if !b.cfg.IsAdmin(userID) {
		restriction = access.NonAdminTurn()
		sender.refuseApprovals = true
	}
	// Anything else in this process that can show a session follows along;
	// permission prompts and questions stay with the chat.
	mirrored, releaseMirror := session.Mirror(b.mirror, st.GetID(), sender)
	defer releaseMirror()

	result, err := b.runner.HandleSessionPromptWithSender(tctx, acp.SessionPromptParams{
		SessionID: st.GetID(),
		Prompt:    []acp.ContentBlock{{Type: "text", Text: text}},
	}, mirrored, &session.PromptRunOpts{
		SkipUsagePublish:    true,
		SurfaceSystemPrompt: surfaceSystemPrompt(),
		Restriction:         restriction,
	})
	sender.Flush()

	stopReason := ""
	if result != nil {
		stopReason = string(result.StopReason)
	}
	if err != nil {
		b.log.Warn("pachca: agent error", "err", err, "session", st.GetID(), "stop_reason", stopReason)
		b.reply(ctx, c, target, parent, "Agent error: "+err.Error())
		return
	}
	b.log.Debug("pachca: agent turn done", "session", st.GetID(), "stop_reason", stopReason)
	if result != nil && result.StopNotice != "" {
		b.reply(ctx, c, target, parent, result.StopNotice)
	}
}

// quoteParent puts the replied-to message in front of text. A message the
// bot cannot read is left out rather than holding the turn back.
func (b *Bot) quoteParent(ctx context.Context, c *Client, parentID int64, text string) string {
	m, err := c.Message(ctx, parentID)
	if err != nil {
		b.log.Debug("pachca: read replied-to message", "err", err, "message", parentID)
		return text
	}
	b.parents.put(m.ID, m.UserID)
	return replyquote.Prompt(b.authorName(ctx, c, m.UserID), m.Content, text)
}

// authorName names the author of a quoted message: the bot itself, or the
// person's card when the token may read it (users:read), else nobody.
func (b *Bot) authorName(ctx context.Context, c *Client, userID int64) string {
	if userID == b.selfID {
		return b.displayName
	}
	u, err := c.User(ctx, userID)
	if err != nil {
		b.log.Debug("pachca: read author", "err", err, "user", userID)
		return ""
	}
	return u.DisplayName()
}

func (b *Bot) helpText() string {
	var sb strings.Builder
	sb.WriteString("**Commands**\n\n")
	sb.WriteString("/model - switch the LLM model (/model <id> sets it directly)\n")
	sb.WriteString("/agent, /plan, /ask - switch the session mode\n")
	sb.WriteString("/think, /nothink, /reasoning <level> - thinking and reasoning level\n")
	sb.WriteString("Add --once or --count=N to change a setting for the next messages only, and write the message after it.\n")
	sb.WriteString("/context - context window usage\n")
	sb.WriteString("/clear - start a new session\n")
	sb.WriteString("/help - this message\n\n")
	sb.WriteString("Reply to a message to ask about it.\n")
	if b.nickname != "" {
		sb.WriteString("In a group chat mention me (@" + b.nickname + ") or reply to my message; commands there need the mention too.")
	} else {
		sb.WriteString("In a group chat mention me or reply to my message; commands there need the mention too.")
	}
	return sb.String()
}

// ensureSession gets or creates the session for key. A conversation this
// gateway starts is stamped with where it came from.
func (b *Bot) ensureSession(ctx context.Context, key string) (*session.State, error) {
	fresh := b.store.Peek(key) == ""
	st, err := b.runner.EnsureHTTPSession(ctx, b.store.Get(key), b.cwd)
	if err != nil {
		return nil, err
	}
	if fresh {
		st.SetOrigin(session.GatewayOrigin(adapterName))
	}
	b.applyInitialModel(ctx, st)
	return st, nil
}

// applyInitialModel stamps the gateway's own model on a session that is just
// beginning: the model last picked on this surface, or the alphabetically
// first configured one. A session that already chose keeps its model.
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
		b.log.Warn("pachca: initial model", "err", err, "session", st.GetID(), "model", initial)
	}
}
