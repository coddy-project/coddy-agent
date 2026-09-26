//go:build gateway || gateway.telegram

// Package telegram implements the Telegram bot adapter for the Coddy gateway.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/proxyutil"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
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
	HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
	// HandleSessionList lists the sessions the server keeps, the most
	// recently updated first; /resume offers them to the chat.
	HandleSessionList(ctx context.Context, params acp.SessionListParams) (*acp.SessionListResult, error)
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

	// apiBase is the Bot API origin Start connects to; empty means the
	// CODDY_TELEGRAM_API_BASE environment variable, and failing that
	// api.telegram.org. Tests set it to point the bot at a local stand-in.
	apiBase string

	mu      sync.Mutex
	workers map[string]chan workerJob // session key → sequential job queue

	draftSeq atomic.Int64 // monotonic source of non-zero rich-message draft IDs

	// inFlight counts the turns being generated right now, so a stop can wait
	// for them instead of cutting them off mid-sentence.
	inFlight sync.WaitGroup

	// mirror publishes a chat turn where other surfaces can watch it. In a
	// process that also serves the HTTP API this is what puts a Telegram
	// conversation on a browser's screen as it happens; on its own the bot
	// runs against a mirror that hands the sender straight back.
	mirror session.TurnMirror

	// asks holds the subagent permission requests waiting for a tap
	// (permission.go); promptSurfaces is where the bot offers to ask about
	// detached subagents, and api the connected client they are asked through.
	asks           *chatPermissions
	promptSurfaces PromptSurfaces
	apiMu          sync.Mutex
	api            *tgbotapi.BotAPI

	// wakeSurfaces is where the bot offers to run the woken turns of its
	// chats' sessions (wake.go).
	wakeSurfaces agent.WakeSurfaces
}

// New creates a Bot. cwd is the default working directory for agent sessions.
// storePath is an optional path for persisting session IDs across restarts; pass "" for in-memory only.
func New(cfg *config.TelegramGatewayConfig, runner SessionRunner, cwd string, log *slog.Logger, storePath string, mirror session.TurnMirror) *Bot {
	store := sessionstore.NewPersisted(storePath)
	if mirror == nil {
		mirror = session.NopTurnMirror{}
	}
	b := &Bot{
		cfg:     cfg,
		runner:  runner,
		cwd:     cwd,
		log:     log,
		store:   store,
		workers: make(map[string]chan workerJob),
		mirror:  mirror,
		asks:    newChatPermissions(),
	}
	return b
}

// Name satisfies gateway.Adapter.
func (b *Bot) Name() string { return "telegram" }

// Start connects to Telegram and begins polling. Blocks until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) error {
	httpClient, err := proxyutil.BuildHTTPClient(b.cfg.Proxy)
	if err != nil {
		// The error names the proxy itself ("proxy: unknown value; ...").
		return fmt.Errorf("telegram: %w", err)
	}
	token := b.cfg.EffectiveToken()
	if token == "" {
		return fmt.Errorf("telegram: no bot token; set gateways.telegram.token or the %s environment variable", config.TelegramBotTokenEnvVar)
	}
	base := b.apiBase
	if base == "" {
		base = os.Getenv(config.TelegramAPIBaseEnv)
	}
	endpoint := telegramAPIEndpoint(base)
	if endpoint != tgbotapi.APIEndpoint {
		b.log.Info("telegram: api base override", "base", strings.TrimSuffix(endpoint, apiEndpointSuffix))
	}
	bot, err := tgbotapi.NewBotAPIWithClient(token, endpoint, httpClient)
	if err != nil {
		return fmt.Errorf("telegram: connect: %w", err)
	}
	b.botName = bot.Self.UserName
	b.log.Info("telegram bot connected", "username", b.botName)

	// From here on a background subagent of one of these chats can be asked
	// about in the chat. A stopped bot receives no taps, so on the way out it
	// withdraws the offer and every request still waiting.
	b.setAPI(bot)
	defer b.setAPI(nil)
	defer b.asks.stop()
	if b.promptSurfaces != nil {
		withdraw := b.promptSurfaces.AddDetachedPermissionApprover(b)
		defer withdraw()
	}
	// A turn a finished background task starts in one of these chats'
	// sessions runs in that chat, for as long as the bot is connected.
	if b.wakeSurfaces != nil {
		withdraw := b.wakeSurfaces.AddWakeSurface(b, agent.WakeOwner)
		defer withdraw()
	}

	if _, err := bot.Request(tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Greeting and quick intro"},
		tgbotapi.BotCommand{Command: "help", Description: "Show available commands"},
		tgbotapi.BotCommand{Command: "model", Description: "Switch LLM model"},
		tgbotapi.BotCommand{Command: "mcp", Description: "List and toggle MCP servers"},
		tgbotapi.BotCommand{Command: "agent", Description: "Agent mode: every tool (add --once for one message)"},
		tgbotapi.BotCommand{Command: "plan", Description: "Plan mode: read-only, plans the work"},
		tgbotapi.BotCommand{Command: "ask", Description: "Ask mode: read-only answers"},
		tgbotapi.BotCommand{Command: "context", Description: "Show context window usage"},
		tgbotapi.BotCommand{Command: "resume", Description: "Continue another session (pick from the list or name it)"},
		tgbotapi.BotCommand{Command: "clear", Description: "Start a new session (forget context)"},
	)); err != nil {
		b.log.Warn("telegram: set commands", "err", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	// Telegram remembers the last allowed_updates a bot asked for, and the
	// library sends none, which means "keep the previous setting". A bot that
	// another framework once ran with messages only would then never see a
	// keyboard tap: say what this adapter handles, every time.
	u.AllowedUpdates = subscribedUpdates
	updates := bot.GetUpdatesChan(u)

	// Turns run under a context of their own so that stopping the bot stops
	// intake first and generation second. A settings change that rotates the
	// token restarts this adapter, and an answer half-written into a chat is
	// the one thing the operator would notice.
	turnCtx, cancelTurns := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTurns()

	for {
		select {
		case <-ctx.Done():
			bot.StopReceivingUpdates()
			b.drain()
			return nil
		case upd, ok := <-updates:
			if !ok {
				return fmt.Errorf("telegram: updates channel closed")
			}
			if upd.Message != nil {
				b.dispatch(turnCtx, bot, upd.Message)
			}
			if upd.CallbackQuery != nil {
				go b.handleCallback(turnCtx, bot, upd.CallbackQuery)
			}
		}
	}
}

// apiEndpointSuffix is the path template the Bot API library formats the
// token and the method into.
const apiEndpointSuffix = "/bot%s/%s"

// subscribedUpdates is what the poll asks Telegram for: the two update kinds
// Start dispatches. Anything else is dropped server-side, and a subscription
// left behind by a previous bot process is replaced rather than inherited.
var subscribedUpdates = []string{"message", "callback_query"}

// telegramAPIEndpoint turns a Bot API origin into the library's endpoint
// template. Empty means api.telegram.org; a trailing slash or surrounding
// whitespace on the origin is tolerated, the way the --dry-run probe reads it.
func telegramAPIEndpoint(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return tgbotapi.APIEndpoint
	}
	return base + apiEndpointSuffix
}

// drainTimeout bounds the wait for turns still being generated when the bot is
// asked to stop. It sits under the supervisor's own restart deadline, so a
// wedged turn delays the replacement bot rather than blocking it forever.
const drainTimeout = 20 * time.Second

// drain waits for the turns already in flight to finish.
func (b *Bot) drain() {
	done := make(chan struct{})
	go func() {
		b.inFlight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		b.log.Warn("telegram: turns still running at stop", "waited", drainTimeout)
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

	b.log.Debug("telegram: update",
		"kind", "message",
		"user", userID,
		"chat", chatID,
		"is_group", isGroup,
		"command", strings.ToLower(msg.Command()),
		"text_len", len(msg.Text),
	)

	level := access.EffectiveAccess(chatID, b.cfg)
	if !access.CanAccess(userID, level, b.cfg) {
		b.log.Debug("telegram: update ignored", "reason", "access denied", "user", userID, "chat", chatID)
		return
	}

	isolation := access.EffectiveIsolation(chatID, b.cfg)
	if isGroup && isolation == config.IsolationAdmin && !b.cfg.IsAdmin(userID) {
		b.log.Debug("telegram: update ignored", "reason", "admin-only chat", "user", userID, "chat", chatID)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if isGroup && !b.shouldRespond(msg, text) {
		b.log.Debug("telegram: update ignored", "reason", "not addressed to the bot", "user", userID, "chat", chatID)
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
		b.log.Debug("telegram: update rejected", "reason", "worker queue full", "key", key, "cap", workerQueueCap)
		b.reply(bot, chatID, msg.MessageID, "⏳ Still processing your previous message, please wait.")
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
			b.inFlight.Add(1)
			b.processMessage(ctx, job.bot, job.msg, job.key)
			b.inFlight.Done()
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
	// Peek, not Get: a log line must not mint a session mapping for a chat that
	// only ever typed /help. The session id is empty until something creates
	// one, and the handlers below name it once they have.
	if cmd := strings.ToLower(msg.Command()); msg.IsCommand() && cmd != "" {
		b.log.Debug("telegram: command",
			"command", cmd,
			"session", b.store.Peek(key),
			"user", userID,
			"chat", chatID,
		)
	}
	if isCommand(msg, "clear") {
		oldID := b.store.Get(key)
		newID := b.store.Reset(key)
		b.runner.ForgetLiveSession(oldID)
		b.reply(bot, chatID, msg.MessageID, "🔄 New session started.")
		b.log.Info("telegram: session cleared", "old", oldID, "new", newID, "user", userID)
		return
	}
	if isCommand(msg, "start") {
		b.reply(bot, chatID, msg.MessageID,
			"👋 Hi! I'm Coddy — an AI coding assistant.\n\nJust send me your question or task. Use /help to see available commands.")
		return
	}
	if isCommand(msg, "help") {
		b.reply(bot, chatID, msg.MessageID,
			"*Available commands:*\n\n"+
				"/start — greeting and quick intro\n"+
				"/model — switch LLM model (/model <id> sets it directly)\n"+
				"/mcp — list and toggle approved MCP servers\n"+
				"/agent, /plan, /ask — switch the session mode\n"+
				"/think, /nothink, /reasoning <level> — thinking and reasoning level\n"+
				"Add --once or --count=N to change a setting for the next messages only, and write the message after it.\n"+
				"/context — show context window usage\n"+
				"/resume [id or title] — continue another session\n"+
				"/clear — start a new session (forgets previous context)\n"+
				"/help — show this message\n\n"+
				"In group chats mention me (@"+b.botName+") or reply to my message to talk to me.")
		return
	}
	if isCommand(msg, "model") && strings.TrimSpace(msg.CommandArguments()) == "" {
		b.handleModelCommand(ctx, bot, msg, key)
		return
	}
	if isCommand(msg, "mcp") {
		b.handleMCPCommand(ctx, bot, msg)
		return
	}
	if isCommand(msg, "context") {
		b.handleContextCommand(ctx, bot, msg, key)
		return
	}
	if isCommand(msg, "resume") {
		b.handleResumeCommand(ctx, bot, msg, key)
		return
	}

	// --- Skip other commands and empty messages ---
	// A settings command (/model x, /think, /plan --once ...) goes to the
	// session like a message: the manager takes it off the start of the text
	// and answers with a notice, or applies it to the turn the rest starts.
	// /permissions is not one of them here: the bot approves its chat agent
	// itself, and the mode would only change what other surfaces ask.
	if text == "" || (msg.IsCommand() && !isSettingsCommand(msg)) {
		return
	}

	// Strip @mention prefix if present.
	text = stripMention(text, b.botName)
	if strings.TrimSpace(text) == "" {
		return
	}

	// A typed "/model <id>" is a session-scoped pick on this surface even
	// though the manager applies it inside the turn: the gateway remembers it
	// like a keyboard tap (turn-scoped forms live in line.Turns and never
	// reach this).
	if line, err := session.ParseSettingsCommands(text); err == nil && line.Session.Model != nil {
		if id := strings.TrimSpace(*line.Session.Model); b.runner.Cfg().FindModelEntry(id) != nil {
			b.store.SetLastModel(id)
		}
	}

	// --- Get or create session ---
	sessionID := b.store.Get(key)

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	st, err := b.runner.EnsureHTTPSession(ctx2, sessionID, b.cwd)
	if err != nil {
		b.log.Warn("telegram: ensure session", "err", err)
		b.reply(bot, chatID, msg.MessageID, "❌ Failed to start session: "+err.Error())
		return
	}
	b.applyInitialModel(ctx2, st)

	// Show "typing…" in the chat header while the agent prepares its first response.
	if _, err := bot.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)); err != nil {
		b.log.Debug("telegram: typing action", "err", err)
	}

	isGroup := msg.Chat.IsGroup() || msg.Chat.IsSuperGroup() || msg.Chat.IsChannel()
	rich := b.cfg.RichMessages

	// The session gets what the person typed and nothing else. What this
	// messenger needs is told to the model for the turn, as a block of the
	// system prompt (prompt.go), and applied to the answer on its way out
	// (Sender.Flush, markdown.go). Neither reaches the transcript, so the
	// conversation reads the same whether the turn came from a chat, a browser
	// or a terminal.
	b.log.Debug("telegram: prompt turn",
		"session", st.GetID(),
		"user", userID,
		"chat", chatID,
		"rich", rich,
		"prompt_len", len(text),
	)

	// Rich Messages: stream an ephemeral draft preview in private chats (drafts are
	// private-only); group chats receive the final sendRichMessage without streaming.
	sender := b.chatSender(bot, chatID, msg.MessageID, richConfig{
		enabled:    rich,
		allowDraft: rich && !isGroup,
		draftID:    b.draftSeq.Add(1),
	})

	// Anything else in this process that can show a session follows along.
	// The chat stays in charge: permission prompts and questions never leave
	// it, because it is the only surface with somebody reading.
	mirrored, releaseMirror := session.Mirror(b.mirror, st.GetID(), sender)
	defer releaseMirror()

	// A chat has no status bar: no provider usage refresh at the end.
	result, err := b.runner.HandleSessionPromptWithSender(ctx2, acp.SessionPromptParams{
		SessionID: st.GetID(),
		Prompt:    []acp.ContentBlock{{Type: "text", Text: text}},
	}, mirrored, &session.PromptRunOpts{
		SkipUsagePublish:    true,
		SurfaceSystemPrompt: surfaceSystemPrompt(rich),
	})
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
		b.reply(bot, chatID, msg.MessageID, "❌ Agent error: "+err.Error())
	} else {
		b.log.Debug("telegram: agent turn done",
			"session", st.GetID(),
			"stop_reason", stopReason,
		)
		if result != nil && result.StopNotice != "" {
			// A chat has no transcript log: why the turn stopped short is
			// said as a message of its own (issue #255).
			b.reply(bot, chatID, msg.MessageID, result.StopNotice)
		}
	}
}

// chatSender builds the sender of one turn in chatID, able to ask the chat about
// a subagent's permission request.
func (b *Bot) chatSender(bot *tgbotapi.BotAPI, chatID int64, replyTo int, rich richConfig) *Sender {
	s := newSender(bot, chatID, replyTo, b.log, rich)
	s.asks = b.asks
	return s
}

// shouldRespond checks whether the bot should process a group message.
// It responds to: built-in commands, direct @-mentions, and replies to the bot.
func (b *Bot) shouldRespond(msg *tgbotapi.Message, text string) bool {
	if msg.IsCommand() {
		switch strings.ToLower(msg.Command()) {
		case "clear", "start", "help", "model", "mcp", "context", "resume":
			return true
		}
		if isSettingsCommand(msg) {
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

// isSettingsCommand reports whether msg starts with a settings command the
// bot hands to the session (session.LookupSettingsCommand), /permissions
// aside.
func isSettingsCommand(msg *tgbotapi.Message) bool {
	if !msg.IsCommand() {
		return false
	}
	cmd, ok := session.LookupSettingsCommand(msg.Command())
	return ok && cmd.Setting != session.SettingPermissionMode
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

// reply sends one plain message back into the chat. It is a method so the
// failure lands in the adapter's own logger: routed to the configured sink and
// tagged with the component, rather than in whatever slog.Default happens to be.
func (b *Bot) reply(bot *tgbotapi.BotAPI, chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if replyTo != 0 {
		msg.ReplyToMessageID = replyTo
	}
	if _, err := bot.Send(msg); err != nil {
		b.log.Warn("telegram: send reply failed", "err", err, "chat", chatID)
	}
}
