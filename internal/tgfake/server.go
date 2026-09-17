// Package tgfake is a stand-in for the Telegram Bot API: an HTTP server that
// answers the methods the Telegram gateway calls, keeps the chats it is sent,
// and hands out the updates an operator or a test injects. It speaks the wire
// format only - urlencoded forms in, {"ok":true,"result":...} out - so any Bot
// API client can be pointed at it; the gateway does so through
// CODDY_TELEGRAM_API_BASE. cmd/tgfake serves it on a port with a chat page,
// and the gateway's own tests run it in-process on httptest.
//
// The fake is deliberately literal about the parts a bot can get wrong: update
// ids grow across a Reset, getUpdates honours offset and long-polls, a
// command needs its bot_command entity, an edit that changes nothing is
// refused the way api.telegram.org refuses it, and a fault can be scheduled
// for any method.
package tgfake

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configures a Server.
type Options struct {
	// Token, when set, is the only token accepted in /bot<token>/ paths; empty
	// accepts any.
	Token string
	// BotUsername is what getMe reports; the gateway matches @mentions on it.
	BotUsername string
	// BotID is the user id getMe reports.
	BotID int64
	// MaxPollWait caps how long getUpdates holds a request open, whatever
	// timeout the client asked for. Tests keep it short so a server closes
	// promptly; the command defaults to Telegram's 30 s.
	MaxPollWait time.Duration
	// AllowedUpdates is the subscription the bot starts with, as Telegram
	// remembers the last allowed_updates a bot asked for: nil delivers every
	// kind. A previous bot process may have left "message" alone behind, and
	// a poll that names no allowed_updates inherits that.
	AllowedUpdates []string
	// Logf, when set, receives one line per Bot API call.
	Logf func(format string, args ...any)
}

const (
	defaultBotUsername = "coddy_fake_bot"
	defaultBotID       = int64(7000000001)
	defaultMaxPollWait = 30 * time.Second
	defaultPollLimit   = 100
)

// Call is one Bot API request the fake answered, kept in the outbox.
type Call struct {
	Seq      int               `json:"seq"`
	At       time.Time         `json:"at"`
	Method   string            `json:"method"`
	Params   map[string]string `json:"params"`
	Status   int               `json:"status"`
	Response json.RawMessage   `json:"response"`
}

// Server is the fake Bot API. Every method is safe for concurrent use.
type Server struct {
	opts Options

	mu         sync.Mutex
	nextUpdate int
	pending    []Update
	allowed    []string // update kinds delivered; nil = every kind
	wake       chan struct{}
	closing    chan struct{}
	closeOnce  sync.Once
	chats      map[int64]*chatState
	calls      []Call
	faults     map[string]*Fault
	commands   []BotCommand
	nextCbq    int
	cbqChat    map[string]int64 // callback query id → chat the tap came from
	now        func() time.Time
}

// New returns a Server with nothing in it.
func New(opts Options) *Server {
	if opts.BotUsername == "" {
		opts.BotUsername = defaultBotUsername
	}
	if opts.BotID == 0 {
		opts.BotID = defaultBotID
	}
	if opts.MaxPollWait <= 0 {
		opts.MaxPollWait = defaultMaxPollWait
	}
	return &Server{
		opts:       opts,
		nextUpdate: 1,
		allowed:    append([]string(nil), opts.AllowedUpdates...),
		wake:       make(chan struct{}),
		closing:    make(chan struct{}),
		chats:      map[int64]*chatState{},
		faults:     map[string]*Fault{},
		cbqChat:    map[string]int64{},
		now:        time.Now,
	}
}

// Handler serves the Bot API under /bot<token>/<method>, the simulation API
// under /sim/ and the chat page at /.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerSim(mux)
	mux.HandleFunc("/{$}", s.servePage)
	mux.HandleFunc("/", s.serveBotAPI)
	return mux
}

// Close releases every getUpdates request held open, so an http server in
// front of the fake can shut down without waiting for a poll to time out.
// Call it before httptest.Server.Close.
func (s *Server) Close() {
	s.closeOnce.Do(func() { close(s.closing) })
}

// Reset forgets the chats, the outbox, the faults and the commands. Update
// ids keep growing: a bot that is polling remembers the last id it confirmed
// and would drop anything numbered below it. The allowed_updates subscription
// stays as well, on purpose: Telegram keeps it with the token, not with the
// chats, so a reset between two runs of a bot leaves it what the previous run
// asked for - SetAllowedUpdates puts it back to anything else.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	s.chats = map[int64]*chatState{}
	s.calls = nil
	s.faults = map[string]*Fault{}
	s.commands = nil
	s.nextCbq = 0
	s.cbqChat = map[string]int64{}
	s.wakeLocked()
}

// BotUsername is the name getMe reports.
func (s *Server) BotUsername() string { return s.opts.BotUsername }

// SetAllowedUpdates sets the subscription as a previous bot process would
// have left it; nil means every kind. A poll naming allowed_updates replaces
// it, a poll naming none keeps it.
func (s *Server) SetAllowedUpdates(kinds []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowed = append([]string(nil), kinds...)
	if len(s.allowed) == 0 {
		s.allowed = nil
	}
}

// AllowedUpdates is the subscription in force; nil means every kind.
func (s *Server) AllowedUpdates() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.allowed...)
}

// Calls returns the outbox, oldest first; method filters by Bot API method
// name (case-insensitive) and "" returns everything.
func (s *Server) Calls(method string) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, 0, len(s.calls))
	for _, c := range s.calls {
		if method == "" || strings.EqualFold(c.Method, method) {
			out = append(out, c)
		}
	}
	return out
}

// WaitCall blocks until the outbox holds at least n calls of method, or
// timeout passes; it reports which.
func (s *Server) WaitCall(method string, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if len(s.Calls(method)) >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Commands returns what the bot last registered with setMyCommands.
func (s *Server) Commands() []BotCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]BotCommand(nil), s.commands...)
}

// record appends a call to the outbox. Caller holds s.mu.
func (s *Server) recordLocked(method string, params url.Values, status int, body []byte) {
	flat := make(map[string]string, len(params))
	for k := range params {
		flat[k] = params.Get(k)
	}
	s.calls = append(s.calls, Call{
		Seq:      len(s.calls) + 1,
		At:       s.now(),
		Method:   method,
		Params:   flat,
		Status:   status,
		Response: json.RawMessage(append([]byte(nil), body...)),
	})
	if s.opts.Logf != nil {
		s.opts.Logf("%s %d %s", method, status, summarizeParams(flat))
	}
}

// summarizeParams renders the parameters of a call on one line, keys sorted,
// long values cut.
func summarizeParams(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(" ")
		}
		v := params[k]
		if len(v) > 80 {
			v = v[:80] + "…"
		}
		sb.WriteString(k + "=" + strings.ReplaceAll(v, "\n", "\\n"))
	}
	return sb.String()
}
