//go:build gateway || gateway.telegram

// Package telegram implements the Telegram bot adapter for the Coddy gateway.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/proxyutil"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	adapterName    = "tg"
	workerQueueCap = 32 // max queued messages per session
)

// SessionRunner abstracts the session management and agent execution needed by the bot.
type SessionRunner interface {
	EnsureHTTPSession(ctx context.Context, sessionID string, defaultCWD string) (*session.State, error)
	HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
	ForgetLiveSession(sessionID string)
	HandleSessionSetMode(ctx context.Context, params acp.SessionSetModeParams) error
	HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
	Cfg() *config.Config
}

type workerJob struct {
	bot *tgbotapi.BotAPI
	msg *tgbotapi.Message
	key string // pre-computed session key
}

// Bot is the Telegram gateway adapter.
type Bot struct {
	cfg     *config.TelegramGatewayConfig
	runner  SessionRunner
	cwd     string
	log     *slog.Logger
	store   *sessionstore.Store
	botName string // @username of the bot (set after connect)

	mu      sync.Mutex
	workers map[string]chan workerJob // session key → sequential job queue

	seenSessions sync.Map     // tracks sessions that already received the formatting hint
	draftSeq     atomic.Int64 // monotonic source of non-zero rich-message draft IDs
}

// New creates a Bot. cwd is the default working directory for agent sessions.
// storePath is an optional path for persisting session IDs across restarts; pass "" for in-memory only.
func New(cfg *config.TelegramGatewayConfig, runner SessionRunner, cwd string, log *slog.Logger, storePath string) *Bot {
	store := sessionstore.NewPersisted(storePath)
	b := &Bot{
		cfg:     cfg,
		runner:  runner,
		cwd:     cwd,
		log:     log,
		store:   store,
		workers: make(map[string]chan workerJob),
	}
	// Pre-populate seenSessions so a restart doesn't re-inject the formatting hint into existing sessions.
	for _, id := range store.KnownIDs() {
		b.seenSessions.Store(id, struct{}{})
	}
	return b
}

// Name satisfies gateway.Adapter.
func (b *Bot) Name() string { return "telegram" }

// Start connects to Telegram and begins polling. Blocks until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) error {
	httpClient, err := proxyutil.BuildHTTPClient(b.cfg.Proxy)
	if err != nil {
		return fmt.Errorf("telegram: proxy: %w", err)
	}
	token := b.cfg.EffectiveToken()
	if token == "" {
		return fmt.Errorf("telegram: no bot token; set gateways.telegram.token or the %s environment variable", config.TelegramBotTokenEnvVar)
	}
	bot, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return fmt.Errorf("telegram: connect: %w", err)
	}
	b.botName = bot.Self.UserName
	b.log.Info("telegram bot connected", "username", b.botName)

	if _, err := bot.Request(tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Greeting and quick intro"},
		tgbotapi.BotCommand{Command: "help", Description: "Show available commands"},
		tgbotapi.BotCommand{Command: "mode", Description: "Switch session mode (agent / plan / ask)"},
		tgbotapi.BotCommand{Command: "model", Description: "Switch LLM model"},
		tgbotapi.BotCommand{Command: "context", Description: "Show context window usage"},
		tgbotapi.BotCommand{Command: "clear", Description: "Start a new session (forget context)"},
	)); err != nil {
		b.log.Warn("telegram: set commands", "err", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			bot.StopReceivingUpdates()
			return nil
		case upd, ok := <-updates:
			if !ok {
				return fmt.Errorf("telegram: updates channel closed")
			}
			if upd.Message != nil {
				b.dispatch(ctx, bot, upd.Message)
			}
			if upd.CallbackQuery != nil {
				go b.handleCallback(ctx, bot, upd.CallbackQuery)
			}
		}
	}
}

// dispatch runs fast pre-checks in the polling goroutine, then routes the message
// to the per-session worker that processes turns sequentially for that session key.
func (b *Bot) dispatch(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.From == nil {
		return
	}

	userID := msg.From.ID
	chatID := msg.Chat.ID
	isGroup := msg.Chat.IsGroup() || msg.Chat.IsSuperGroup() || msg.Chat.IsChannel()

	level := access.EffectiveAccess(chatID, b.cfg)
	if !access.CanAccess(userID, level, b.cfg) {
		b.log.Debug("telegram: access denied", "user", userID, "chat", chatID)
		return
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	if isGroup && isolation == config.IsolationAdmin && !b.cfg.IsAdmin(userID) {
		return
	}

	text := strings.TrimSpace(msg.Text)
	if isGroup && !b.shouldRespond(msg, text) {
		return
	}

	key := sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup)

	b.mu.Lock()
	ch, ok := b.workers[key]
	if !ok {
		ch = make(chan workerJob, workerQueueCap)
		b.workers[key] = ch
		go b.sessionWorker(ctx, ch)
	}
	b.mu.Unlock()

	select {
	case ch <- workerJob{bot: bot, msg: msg, key: key}:
	default:
		reply(bot, chatID, msg.MessageID, "⏳ Still processing your previous message, please wait.")
	}
}

// sessionWorker processes jobs for one session key sequentially.
// It exits when ctx is cancelled.
func (b *Bot) sessionWorker(ctx context.Context, ch chan workerJob) {
	for {
		select {
		case job, ok := <-ch:
			if !ok {
				return
			}
			b.processMessage(ctx, job.bot, job.msg, job.key)
		case <-ctx.Done():
			return
		}
	}
}

func (b *Bot) processMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	// --- Built-in commands ---
	if isCommand(msg, "clear") {
		oldID := b.store.Get(key)
		newID := b.store.Reset(key)
		b.runner.ForgetLiveSession(oldID)
		reply(bot, chatID, msg.MessageID, "🔄 New session started.")
		b.log.Info("telegram: session cleared", "old", oldID, "new", newID, "user", userID)
		return
	}
	if isCommand(msg, "start") {
		reply(bot, chatID, msg.MessageID,
			"👋 Hi! I'm Coddy — an AI coding assistant.\n\nJust send me your question or task. Use /help to see available commands.")
		return
	}
	if isCommand(msg, "help") {
		reply(bot, chatID, msg.MessageID,
			"*Available commands:*\n\n"+
				"/start — greeting and quick intro\n"+
				"/mode — switch session mode (agent / plan / ask)\n"+
				"/model — switch LLM model\n"+
				"/context — show context window usage\n"+
				"/clear — start a new session (forgets previous context)\n"+
				"/help — show this message\n\n"+
				"In group chats mention me (@"+b.botName+") or reply to my message to talk to me.")
		return
	}
	if isCommand(msg, "mode") {
		b.handleModeCommand(ctx, bot, msg, key)
		return
	}
	if isCommand(msg, "model") {
		b.handleModelCommand(ctx, bot, msg, key)
		return
	}
	if isCommand(msg, "context") {
		b.handleContextCommand(ctx, bot, msg, key)
		return
	}

	// --- Skip other commands and empty messages ---
	if text == "" || msg.IsCommand() {
		return
	}

	// Strip @mention prefix if present.
	text = stripMention(text, b.botName)
	if strings.TrimSpace(text) == "" {
		return
	}

	// --- Get or create session ---
	sessionID := b.store.Get(key)

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	st, err := b.runner.EnsureHTTPSession(ctx2, sessionID, b.cwd)
	if err != nil {
		b.log.Warn("telegram: ensure session", "err", err)
		reply(bot, chatID, msg.MessageID, "❌ Failed to start session: "+err.Error())
		return
	}

	// Show "typing…" in the chat header while the agent prepares its first response.
	if _, err := bot.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)); err != nil {
		b.log.Debug("telegram: typing action", "err", err)
	}

	isGroup := msg.Chat.IsGroup() || msg.Chat.IsSuperGroup() || msg.Chat.IsChannel()
	rich := b.cfg.RichMessages

	// Legacy mode needs a one-time hint on the first message of a new session so the
	// agent restricts itself to the Telegram-compatible Markdown subset. Rich mode sends
	// the agent's natural Markdown verbatim, so no hint is prepended — keeping the first
	// turn identical to later ones (a hint-prefixed first message was suppressing replies).
	promptText := text
	firstTurn := false
	if !rich {
		if _, alreadySeen := b.seenSessions.LoadOrStore(st.GetID(), struct{}{}); !alreadySeen {
			firstTurn = true
			promptText = telegramFormattingHint + promptText
		}
	}

	b.log.Debug("telegram: prompt turn",
		"session", st.GetID(),
		"user", userID,
		"chat", chatID,
		"first_turn", firstTurn,
		"rich", rich,
		"prompt_len", len(promptText),
	)

	// Rich Messages: stream an ephemeral draft preview in private chats (drafts are
	// private-only); group chats receive the final sendRichMessage without streaming.
	sender := newSender(bot, chatID, msg.MessageID, b.log, richConfig{
		enabled:    rich,
		allowDraft: rich && !isGroup,
		draftID:    b.draftSeq.Add(1),
	})

	result, err := b.runner.HandleSessionPromptWithSender(ctx2, acp.SessionPromptParams{
		SessionID: st.GetID(),
		Prompt:    []acp.ContentBlock{{Type: "text", Text: promptText}},
	}, sender, nil)
	sender.Flush()

	stopReason := ""
	if result != nil {
		stopReason = string(result.StopReason)
	}
	if err != nil {
		b.log.Warn("telegram: agent error",
			"err", err,
			"session", st.GetID(),
			"stop_reason", stopReason,
		)
		reply(bot, chatID, msg.MessageID, "❌ Agent error: "+err.Error())
	} else {
		b.log.Debug("telegram: agent turn done",
			"session", st.GetID(),
			"stop_reason", stopReason,
		)
	}
}

// shouldRespond checks whether the bot should process a group message.
// It responds to: built-in commands, direct @-mentions, and replies to the bot.
func (b *Bot) shouldRespond(msg *tgbotapi.Message, text string) bool {
	if msg.IsCommand() {
		switch strings.ToLower(msg.Command()) {
		case "clear", "start", "help", "mode", "model", "context":
			return true
		}
	}
	if strings.Contains(text, "@"+b.botName) {
		return true
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.UserName == b.botName {
		return true
	}
	return false
}

func isCommand(msg *tgbotapi.Message, cmd string) bool {
	return msg.IsCommand() && strings.EqualFold(msg.Command(), cmd)
}

func stripMention(text, botName string) string {
	if botName == "" {
		return text
	}
	mention := "@" + botName
	s := strings.TrimPrefix(text, mention)
	s = strings.ReplaceAll(s, mention, "")
	return strings.TrimSpace(s)
}

func reply(bot *tgbotapi.BotAPI, chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if replyTo != 0 {
		msg.ReplyToMessageID = replyTo
	}
	if _, err := bot.Send(msg); err != nil {
		slog.Warn("telegram: send reply failed", "err", err)
	}
}
