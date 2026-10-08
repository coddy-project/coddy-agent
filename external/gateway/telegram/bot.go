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
	"unicode/utf16"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/proxyutil"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/replyquote"
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

	// webUIGate decides whether the bot may hand out the web UI's address
	// (miniapp.go); nil allows it.
	webUIGate func() (allowed bool, note string)
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

	if _, err := bot.Request(tgbotapi.NewSetMyCommands(botCommands(b.cfg)...)); err != nil {
		b.log.Warn("telegram: set commands", "err", err)
	}
	b.syncMenuButton(bot)

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

// botCommands is the command list setMyCommands registers: /app only while
// the web UI is the bot's Mini App.
func botCommands(cfg *config.TelegramGatewayConfig) []tgbotapi.BotCommand {
	cmds := []tgbotapi.BotCommand{
		{Command: "start", Description: "Greeting and quick intro"},
		{Command: "help", Description: "Show available commands"},
		{Command: "model", Description: "Switch LLM model"},
		{Command: "mcp", Description: "List and toggle MCP servers"},
		{Command: "agent", Description: "Agent mode: every tool (add --once for one message)"},
		{Command: "plan", Description: "Plan mode: read-only, plans the work"},
		{Command: "ask", Description: "Ask mode: read-only answers"},
		{Command: "context", Description: "Show context window usage"},
		{Command: "goal", Description: "Work on a goal until it is checked done"},
		{Command: "resume", Description: "Continue another session (pick from the list or name it)"},
	}
	if cfg.MiniApp.URL != "" {
		cmds = append(cmds, tgbotapi.BotCommand{Command: "app", Description: "Open this conversation in the web UI"})
	}
	return append(cmds, tgbotapi.BotCommand{Command: "clear", Description: "Start a new session (forget context)"})
}

// helpText is the answer to /help; /app is listed only while the web UI is
// the bot's Mini App.
func helpText(cfg *config.TelegramGatewayConfig, botName string) string {
	app := ""
	if cfg.MiniApp.URL != "" {
		app = "/app — open this conversation in the web UI\n"
	}
	return "*Available commands:*\n\n" +
		"/start — greeting and quick intro\n" +
		"/model — switch LLM model (/model <id> sets it directly)\n" +
		"/mcp — list and toggle approved MCP servers\n" +
		"/agent, /plan, /ask — switch the session mode\n" +
		"/think, /nothink, /reasoning <level> — thinking and reasoning level\n" +
		"Add --once or --count=N to change a setting for the next messages only, and write the message after it.\n" +
		"/context — show context window usage\n" +
		"/goal <objective> — work on a goal until a second model confirms it; /goal shows it, /goal pause, /goal resume, /goal clear\n" +
		"/resume [id or title] — continue another session\n" +
		app +
		"/clear — start a new session (forgets previous context)\n" +
		"/help — show this message\n\n" +
		"Reply to a message to ask about it.\n" +
		"In group chats mention me (@" + botName + ") or reply to my message to talk to me; commands there need the mention too (/clear@" + botName + ")."
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

	// A queued message counts as in flight from here, so a stop waits for it
	// as well as for the turn running ahead of it: Telegram already
	// confirmed its update. Only this goroutine dispatches, and it is the one
	// that waits at a stop, so the count never grows during that wait.
	b.inFlight.Add(1)
	select {
	case ch <- workerJob{bot: bot, msg: msg, key: key}:
	default:
		b.inFlight.Done()
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
			b.processMessage(ctx, job.bot, job.msg, job.key)
			b.inFlight.Done()
		case <-ctx.Done():
			// The stop waited as long as it could: what is still queued is
			// let go, and no longer counted.
			for {
				select {
				case <-ch:
					b.inFlight.Done()
				default:
					return
				}
			}
		}
	}
}

func (b *Bot) processMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, key string) {
	msg = commandAfterMention(msg, b.botName)
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
	// A group shares the bot with many people: what changes the session's
	// settings, or replaces the conversation, is the admins' to do. /resume
	// reaches every session the server keeps, so it is theirs in any chat.
	// The command may follow the bot's mention ("@bot /think"), which Telegram
	// does not mark as a command: the words after the mention count too.
	leading := leadingCommand(stripMention(text, b.botName), b.botName)
	textChangesSettings := changesSettings(msg) || commandChangesSettings(leading)
	if ((isGroupChat(msg.Chat) && textChangesSettings) || isCommand(msg, "resume") || leading == "resume") && !b.cfg.IsAdmin(userID) {
		b.log.Debug("telegram: update refused", "reason", "admin-only command", "user", userID, "chat", chatID)
		b.reply(bot, chatID, msg.MessageID, adminOnlyNote)
		return
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
		b.reply(bot, chatID, msg.MessageID, helpText(b.cfg, b.botName))
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
	if isCommand(msg, "app") {
		b.handleAppCommand(bot, msg, key)
		return
	}

	// --- Skip other commands and empty messages ---
	// A settings command (/model x, /think, /plan --once ...) goes to the
	// session like a message: the manager takes it off the start of the text
	// and answers with a notice, or applies it to the turn the rest starts.
	// /permissions is not one of them here: the bot approves its chat agent
	// itself, and the mode would only change what other surfaces ask.
	if text == "" || (msg.IsCommand() && !isSettingsCommand(msg) && !isCommand(msg, "goal")) {
		return
	}

	// Strip @mention prefix if present.
	text = strings.TrimSpace(stripMention(text, b.botName))
	// A reply asks about the message it answers: the session receives that
	// message quoted in front of what the person wrote, and a mention alone
	// under a reply asks about the quoted message. A settings command and
	// /goal are not quoted - the manager reads them off the start of the text.
	author, quoted := replyContext(msg.ReplyToMessage)
	if text == "" && quoted == "" {
		return
	}
	if !isSettingsCommand(msg) && !isCommand(msg, "goal") {
		text = replyquote.Prompt(author, quoted, text)
	}

	// A typed "/model <id>" is a session-scoped pick on this surface even
	// though the manager applies it inside the turn: the gateway remembers it
	// like a keyboard tap (turn-scoped forms live in line.Turns and never
	// reach this) - when an admin typed it, since the remembered model is
	// what every fresh chat of the bot starts on.
	if line, err := session.ParseSettingsCommands(text); err == nil && line.Session.Model != nil && b.cfg.IsAdmin(userID) {
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
	sender.pictures = st
	// Somebody who is not the bot's admin gets the conversation and nothing
	// that changes the agent: the turn refuses the tools that would, and the
	// chat approves nothing it is asked about.
	var restriction *session.TurnRestriction
	if !b.cfg.IsAdmin(userID) {
		restriction = access.NonAdminTurn()
		sender.refuseApprovals = true
	}

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
		Restriction:         restriction,
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

// replyContext is the author and the text of the message msg replies to,
// empty when it replies to none or to one without text (the service message
// that opens a forum topic, a sticker).
func replyContext(msg *tgbotapi.Message) (author, text string) {
	if msg == nil {
		return "", ""
	}
	text = msg.Text
	if text == "" {
		text = msg.Caption
	}
	if msg.From != nil {
		author = strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
		if author == "" && msg.From.UserName != "" {
			author = "@" + msg.From.UserName
		}
	}
	return author, text
}

// shouldRespond checks whether the bot should process a group message. A
// group is where many people talk, so only a mention of the bot or a reply to
// one of its messages is for it; a command needs the mention too, which
// Telegram writes as /command@botname.
func (b *Bot) shouldRespond(msg *tgbotapi.Message, text string) bool {
	if strings.Contains(text, "@"+b.botName) {
		return true
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.UserName == b.botName {
		return true
	}
	return false
}

// adminOnlyNote answers a group member who is not an admin and tried to
// change the settings.
const adminOnlyNote = "Only the bot's admins can change settings in this chat."

// isGroupChat reports whether chat is shared by several people.
func isGroupChat(chat *tgbotapi.Chat) bool {
	return chat != nil && (chat.IsGroup() || chat.IsSuperGroup() || chat.IsChannel())
}

// changesSettings reports whether msg changes the session's settings or
// replaces the conversation: a settings command, /model, /clear, /resume.
func changesSettings(msg *tgbotapi.Message) bool {
	if !msg.IsCommand() {
		return false
	}
	switch strings.ToLower(msg.Command()) {
	case "model", "clear", "resume":
		return true
	}
	return isSettingsCommand(msg)
}

// commandAfterMention turns "@bot /command args" into the command Telegram
// would have marked had the mention not come first, so every check and
// handler reads both forms the same: the filter of unknown commands and of
// /permissions, the admin-only commands, the quote a reply would add.
func commandAfterMention(msg *tgbotapi.Message, botName string) *tgbotapi.Message {
	if msg == nil || msg.IsCommand() || botName == "" {
		return msg
	}
	text := strings.TrimSpace(msg.Text)
	stripped := stripMention(text, botName)
	if stripped == text || leadingCommand(stripped, botName) == "" {
		return msg
	}
	word := strings.Fields(stripped)[0]
	out := *msg
	out.Text = stripped
	out.Entities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(utf16.Encode([]rune(word)))}}
	return &out
}

// leadingCommand is the lower-case command word text starts with ("/think",
// "/model@botname x"), without the slash and the bot's name, or "".
func leadingCommand(text, botName string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	word := strings.Fields(text[1:])
	if len(word) == 0 {
		return ""
	}
	cmd := strings.ToLower(word[0])
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		if !strings.EqualFold(cmd[at+1:], botName) {
			return ""
		}
		cmd = cmd[:at]
	}
	return cmd
}

// commandChangesSettings is changesSettings for a command word.
func commandChangesSettings(cmd string) bool {
	switch cmd {
	case "":
		return false
	case "model", "clear", "resume":
		return true
	}
	sc, ok := session.LookupSettingsCommand(cmd)
	return ok && sc.Setting != session.SettingPermissionMode
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
