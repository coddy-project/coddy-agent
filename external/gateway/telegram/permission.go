//go:build gateway || gateway.telegram

package telegram

// Permission prompts of subagents, asked in the chat.
//
// The bot allows what the chat's own agent asks without a question: the admin
// who configured the bot decided that. A subagent is different - its definition
// may have narrowed what it may do, and a background one keeps working after the
// reply was sent - so, like every other surface, the bot asks the person in the
// chat, names the subagent, and waits for a tap. A background subagent's prompt
// reaches the chat that owns its parent session even after the turn ended; the
// answer that arrives first from any surface wins, and this message is edited
// to say the request no longer waits.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// callbackActionPermission prefixes the callback data of a permission button:
// "perm:<token>:<option index>", well under Telegram's 64 bytes.
const callbackActionPermission = "perm"

// PromptSurfaces is where the bot offers to ask about a detached subagent of
// one of its chats; `coddy serve` passes its runtime.
type PromptSurfaces interface {
	AddDetachedPermissionApprover(agent.DetachedPermissionBroker) (withdraw func())
}

// chatPrompt is one request waiting for a tap.
type chatPrompt struct {
	// sessionID is the chat session whose person may answer.
	sessionID string
	options   []acp.PermissionOption
	answer    chan *acp.PermissionResult
}

// chatPermissions holds the requests the bot is waiting on, keyed by the token
// their buttons carry.
type chatPermissions struct {
	mu      sync.Mutex
	pending map[string]*chatPrompt
	stopped chan struct{}
	once    sync.Once
}

func newChatPermissions() *chatPermissions {
	return &chatPermissions{pending: make(map[string]*chatPrompt), stopped: make(chan struct{})}
}

// stop withdraws every request still waiting: a stopped bot receives no taps.
func (c *chatPermissions) stop() {
	c.once.Do(func() { close(c.stopped) })
}

func newPromptToken() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ask sends the request to chatID and blocks until someone who may answer for
// sessionID taps a button, or ctx ends, or the bot stops. A nil result means
// nobody answered here.
func (c *chatPermissions) ask(ctx context.Context, bot *tgbotapi.BotAPI, log *slog.Logger, chatID int64, sessionID string, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	token := newPromptToken()
	p := &chatPrompt{sessionID: sessionID, options: params.Options, answer: make(chan *acp.PermissionResult, 1)}
	c.mu.Lock()
	c.pending[token] = p
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, token)
		c.mu.Unlock()
	}()

	text := permissionText(params)
	buttons := make([]tgbotapi.InlineKeyboardButton, 0, len(params.Options))
	for i, opt := range params.Options {
		label := strings.TrimSpace(opt.Name)
		if label == "" {
			label = opt.OptionID
		}
		buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData(label,
			callbackActionPermission+":"+token+":"+strconv.Itoa(i)))
	}
	// No parse mode: a command line is full of characters Markdown would eat.
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons)
	sent, err := bot.Send(msg)
	if err != nil {
		return nil, err
	}
	log.Debug("telegram: permission asked", "chat", chatID, "session", sessionID, "toolCallId", params.ToolCall.ToolCallID)

	settle := func(note string) {
		// Editing the text without a keyboard also takes the buttons away.
		edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID, text+"\n\n"+note)
		if _, err := bot.Request(edit); err != nil {
			log.Debug("telegram: permission message edit", "err", err)
		}
	}
	select {
	case res := <-p.answer:
		if res.OptionID == "reject" || strings.HasPrefix(res.OptionID, "reject") {
			settle("🚫 Denied")
		} else {
			settle("✅ Allowed")
		}
		return res, nil
	case <-ctx.Done():
		settle("⌛ No longer waiting")
		return nil, nil
	case <-c.stopped:
		settle("⌛ No longer waiting")
		return nil, nil
	}
}

// resolve answers the request behind a tapped button. It refuses a token nobody
// waits for any more, an option the request did not offer, and a tap from
// somebody whose own session in this chat is not the one that asked - in a group
// with individual sessions another member must not answer for someone else.
func (c *chatPermissions) resolve(token string, index int, tapperSessionID string) (acp.PermissionOption, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pending[token]
	if p == nil || tapperSessionID == "" || tapperSessionID != p.sessionID || index < 0 || index >= len(p.options) {
		return acp.PermissionOption{}, false
	}
	opt := p.options[index]
	select {
	case p.answer <- &acp.PermissionResult{Outcome: "selected", OptionID: opt.OptionID}:
	default:
		// Tapped twice before the first tap was read: the first one counts.
		return acp.PermissionOption{}, false
	}
	delete(c.pending, token)
	return opt, true
}

// permissionText is the body of the request: the relay's title, which names
// the subagent, and the command or path it wants when the request carries one.
func permissionText(params acp.PermissionRequestParams) string {
	title := strings.TrimSpace(params.ToolCall.Title)
	if title == "" {
		title = "A subagent asks for permission"
	}
	text := "🔐 " + title
	for _, c := range params.ToolCall.Content {
		if body := strings.TrimSpace(c.Content.Text); body != "" {
			text += "\n\n" + truncate(body, 3000)
			break
		}
	}
	return text
}

// answerPermissionTap resolves a permission button. The tapped message is left
// to the waiting request to edit; a stale button just loses its keyboard.
func (b *Bot) answerPermissionTap(bot *tgbotapi.BotAPI, cbq *tgbotapi.CallbackQuery, payload string) {
	chatID := cbq.Message.Chat.ID
	userID := cbq.From.ID
	isGroup := cbq.Message.Chat.IsGroup() || cbq.Message.Chat.IsSuperGroup() || cbq.Message.Chat.IsChannel()
	isolation := access.EffectiveIsolation(chatID, b.cfg)
	if isGroup && isolation == config.IsolationAdmin && !b.cfg.IsAdmin(userID) {
		b.log.Debug("telegram: callback ignored", "reason", "admin-only chat", "user", userID, "chat", chatID)
		return
	}
	token, rawIndex, _ := strings.Cut(payload, ":")
	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		index = -1
	}
	sessionID := b.store.Peek(sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup))
	opt, ok := b.asks.resolve(token, index, sessionID)
	if !ok {
		b.log.Debug("telegram: callback ignored", "reason", "permission request not waiting for this user",
			"user", userID, "chat", chatID)
		empty := tgbotapi.NewEditMessageReplyMarkup(chatID, cbq.Message.MessageID,
			tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})
		if _, err := bot.Request(empty); err != nil {
			b.log.Debug("telegram: stale permission keyboard", "err", err)
		}
		return
	}
	b.log.Info("telegram: permission answered", "session", sessionID, "option", opt.OptionID, "user", userID, "chat", chatID)
}

// setAPI records the connected client, or with nil that the bot stopped.
func (b *Bot) setAPI(api *tgbotapi.BotAPI) {
	b.apiMu.Lock()
	b.api = api
	b.apiMu.Unlock()
}

func (b *Bot) connectedAPI() *tgbotapi.BotAPI {
	b.apiMu.Lock()
	defer b.apiMu.Unlock()
	return b.api
}

// SetPromptSurfaces names where the bot offers to ask about detached subagents
// of its chats. Nil (the default) means such a prompt is never shown in a chat.
func (b *Bot) SetPromptSurfaces(p PromptSurfaces) {
	b.promptSurfaces = p
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker: a
// background subagent whose parent session is a conversation of this bot is
// asked about in that chat. Any other session is not the bot's to show.
func (b *Bot) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	if strings.TrimSpace(req.Params.EffectivePermissionMode) == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	api := b.connectedAPI()
	if api == nil {
		return nil, agent.ErrNoDetachedApprover
	}
	key, ok := b.store.KeyFor(req.ParentSessionID)
	if !ok {
		return nil, agent.ErrNoDetachedApprover
	}
	chatID, ok := sessionstore.ChatID(key)
	if !ok {
		return nil, agent.ErrNoDetachedApprover
	}
	res, err := b.asks.ask(ctx, api, b.log, chatID, req.ParentSessionID, req.Params)
	if err != nil {
		b.log.Warn("telegram: detached permission not delivered", "err", err, "chat", chatID, "session", req.ParentSessionID)
		return nil, agent.ErrNoDetachedApprover
	}
	return res, nil
}

var _ agent.DetachedPermissionBroker = (*Bot)(nil)
