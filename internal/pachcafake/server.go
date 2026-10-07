// Package pachcafake is a stand-in for the part of the Pachca REST API a bot
// adapter calls: an HTTP server that keeps chats, users, threads and
// messages in memory, answers the bot's requests in the wire format of the
// official OpenAPI specification - JSON bodies, {"data": ...} envelopes,
// ApiError and OAuthError bodies, the documented status codes - and keeps
// the bot's event log that GET /webhooks/events reads. The adapter's tests
// run it on httptest and point the adapter's base URL at it; the API paths
// are served at the root (/messages, not /api/shared/v1/messages).
//
// The fake is deliberately literal about the parts a bot can get wrong: the
// event log is read newest first through an opaque cursor that keeps going
// older while new events arrive, read events come back until they are
// deleted, the bot's own messages are logged like anybody else's unless the
// bot is set to ignore them, an edit with no buttons keeps the old ones, a
// button carries exactly one of url or data, and a fault can be scheduled
// for any route.
package pachcafake

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Options configures a Server.
type Options struct {
	// Token, when set, is the only bearer token accepted; empty accepts any
	// non-empty one.
	Token string
	// BotUserID is the bot's user id, what /profile and the token report.
	BotUserID int64
	// BotNickname is the bot's nickname.
	BotNickname string
	// BotName is the bot user's first_name.
	BotName string
	// Scopes the token carries; nil means DefaultScopes.
	Scopes []string
	// HistoryDisabled is the bot setting "Сохранять историю событий" turned
	// off: nothing is logged and GET /webhooks/events returns an empty list.
	HistoryDisabled bool
	// IgnoreSelfMessages keeps the bot's own messages out of the event log.
	// Pachca logs them by default, so false (the zero value) logs them.
	IgnoreSelfMessages bool
	// MaxContentRunes, when positive, refuses a POST or PUT whose content is
	// longer with 422 too_long.
	MaxContentRunes int
	// Logf, when set, receives one line per request.
	Logf func(format string, args ...any)
}

const (
	defaultBotUserID   = int64(7000001)
	defaultBotNickname = "coddy_bot"
	defaultBotName     = "Coddy"

	firstChatID    = int64(50000)
	firstThreadID  = int64(900000)
	firstMessageID = int64(1000000)

	maxBodyBytes = 4 << 20
)

// DefaultScopes is what the token carries when Options.Scopes is nil: every
// scope the adapter needs.
func DefaultScopes() []string {
	return []string{
		"messages:create",
		"messages:update",
		"messages:read",
		"chats:read",
		"profile:read",
		"users:read",
		"webhooks:events:read",
		"webhooks:events:delete",
	}
}

// Call is one request the fake answered, kept in the outbox.
type Call struct {
	Seq int
	At  time.Time
	// Route is the method plus the collection path, ids stripped:
	// "POST /messages", "PUT /messages", "DELETE /webhooks/events".
	Route string
	// Path is the request path as sent, ids included.
	Path  string
	Query string
	Body  json.RawMessage
	// Status is the HTTP status the fake answered with.
	Status int
	// SkipInviteMentions and LinkPreview are the flags of a POST /messages
	// body, false when absent.
	SkipInviteMentions bool
	LinkPreview        bool
}

// user is a person (or the bot) as the fake remembers them.
type user struct {
	id        int64
	nickname  string
	firstName string
	bot       bool
	createdAt time.Time
}

// chat is a group chat, a personal chat or a thread's chat.
type chat struct {
	id        int64
	name      string
	personal  bool
	members   []int64
	ownerID   int64
	createdAt time.Time
	lastAt    time.Time
	thread    *thread // set on the chat of a thread
	messages  []*Message
}

// thread is a thread opened under a message, with a chat of its own.
type thread struct {
	id           int64
	chatID       int64
	parentID     int64
	parentChatID int64
}

// Server is the fake Pachca API. Every method is safe for concurrent use.
type Server struct {
	opts      Options
	createdAt time.Time

	mu         sync.Mutex
	now        func() time.Time
	users      map[int64]*user
	chats      map[int64]*chat
	personal   map[int64]int64 // user id -> personal chat id
	threads    map[int64]*thread
	messages   map[int64]*Message
	events     []WebhookEvent // oldest first
	calls      []Call
	faults     []*Fault
	nextChat   int64
	nextThread int64
	nextMsg    int64
	nextEvent  int64
}

// New returns a Server that knows the bot and nothing else.
func New(opts Options) *Server {
	if opts.BotUserID == 0 {
		opts.BotUserID = defaultBotUserID
	}
	if opts.BotNickname == "" {
		opts.BotNickname = defaultBotNickname
	}
	if opts.BotName == "" {
		opts.BotName = defaultBotName
	}
	if opts.Scopes == nil {
		opts.Scopes = DefaultScopes()
	}
	opts.Scopes = slices.Clone(opts.Scopes)
	s := &Server{
		opts:       opts,
		now:        time.Now,
		users:      map[int64]*user{},
		chats:      map[int64]*chat{},
		personal:   map[int64]int64{},
		threads:    map[int64]*thread{},
		messages:   map[int64]*Message{},
		nextChat:   firstChatID,
		nextThread: firstThreadID,
		nextMsg:    firstMessageID,
	}
	s.createdAt = s.now()
	s.users[opts.BotUserID] = &user{
		id:        opts.BotUserID,
		nickname:  opts.BotNickname,
		firstName: opts.BotName,
		bot:       true,
		createdAt: s.createdAt,
	}
	return s
}

// SetNow replaces the clock behind every created_at, changed_at and
// webhook_timestamp; tests use it to make several events share a
// millisecond. nil restores time.Now.
func (s *Server) SetNow(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// BotUserID is the bot's user id.
func (s *Server) BotUserID() int64 { return s.opts.BotUserID }

// Handler serves the API at the root of whatever server it is mounted on.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

// Reset forgets the outbox and the faults. Chats, messages and the event log
// stay: they are the world the bot lives in, not what it did.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
	s.faults = nil
}

// Calls returns the outbox, oldest first; route filters by Call.Route
// (case-insensitive) and "" returns everything.
func (s *Server) Calls(route string) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, 0, len(s.calls))
	for _, c := range s.calls {
		if route == "" || strings.EqualFold(c.Route, route) {
			c.Body = append(json.RawMessage(nil), c.Body...)
			out = append(out, c)
		}
	}
	return out
}

// Messages returns a chat's history, oldest first.
func (s *Server) Messages(chatID int64) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[chatID]
	if c == nil {
		return nil
	}
	out := make([]Message, 0, len(c.messages))
	for _, m := range c.messages {
		out = append(out, m.clone())
	}
	return out
}

// Message returns one message by id.
func (s *Server) Message(id int64) (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.messages[id]
	if m == nil {
		return Message{}, false
	}
	return m.clone(), true
}

// reply is a response computed under the lock and written after it.
type reply struct {
	status      int
	contentType string
	body        []byte
	retryAfter  int
}

func jsonReply(status int, v any) reply {
	body, err := json.Marshal(v)
	if err != nil {
		body = []byte(`{"errors":[{"key":"server","value":null,"message":"marshal failed","code":"unhandled","payload":null}]}`)
		status = http.StatusInternalServerError
	}
	return reply{status: status, contentType: "application/json; charset=utf-8", body: body}
}

func dataReply(status int, v any) reply {
	return jsonReply(status, map[string]any{"data": v})
}

// apiErrorReply is an ApiError with one item.
func apiErrorReply(status int, key, value, code, message string) reply {
	item := apiErrorItem{Key: key, Message: message, Code: code}
	if value != "" {
		item.Value = &value
	}
	return jsonReply(status, apiError{Errors: []apiErrorItem{item}})
}

func oauthReply(status int, code, description string) reply {
	return jsonReply(status, oauthError{Error: code, ErrorDescription: description})
}

// serve authenticates a request, lets a scheduled fault answer it, checks
// the scope of its route and runs the route, recording it in the outbox.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		body = nil
	}
	rt := matchRoute(r.Method, r.URL.Path)

	s.mu.Lock()
	out, call := s.dispatchLocked(r, rt, body)
	call.Seq = len(s.calls) + 1
	call.At = s.now()
	call.Route = rt.key
	call.Path = r.URL.Path
	call.Query = r.URL.RawQuery
	if len(body) > 0 {
		call.Body = json.RawMessage(append([]byte(nil), body...))
	}
	call.Status = out.status
	s.calls = append(s.calls, call)
	logf := s.opts.Logf
	s.mu.Unlock()

	if logf != nil {
		logf("%s %s %d %s", r.Method, r.URL.RequestURI(), out.status, summarize(body))
	}
	if out.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(out.retryAfter))
	}
	if out.status == http.StatusNoContent {
		w.WriteHeader(out.status)
		return
	}
	w.Header().Set("Content-Type", out.contentType)
	w.WriteHeader(out.status)
	_, _ = w.Write(out.body)
}

// dispatchLocked computes the answer. Caller holds s.mu.
func (s *Server) dispatchLocked(r *http.Request, rt route, body []byte) (reply, Call) {
	var call Call
	if out, ok := s.authenticate(r); !ok {
		return out, call
	}
	if f := s.takeFaultLocked(rt.key, body); f != nil {
		return f.reply(), call
	}
	if rt.handler == nil {
		return apiErrorReply(http.StatusNotFound, "path", r.URL.Path, "not_found", "No route matches "+r.Method+" "+r.URL.Path), call
	}
	if rt.scope != "" && !slices.Contains(s.opts.Scopes, rt.scope) {
		return oauthReply(http.StatusForbidden, "insufficient_scope", "The token does not carry the scope "+rt.scope), call
	}
	out := rt.handler(s, r, rt.id, body, &call)
	return out, call
}

// authenticate checks the bearer token.
func (s *Server) authenticate(r *http.Request) (reply, bool) {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || token == "" {
		return oauthReply(http.StatusUnauthorized, "invalid_token", "The access token is missing"), false
	}
	if s.opts.Token != "" && token != s.opts.Token {
		return oauthReply(http.StatusUnauthorized, "invalid_token", "The access token is invalid"), false
	}
	return reply{}, true
}

// handlerFunc runs one route under s.mu; id is the path id, "" for a
// collection route.
type handlerFunc func(s *Server, r *http.Request, id string, body []byte, call *Call) reply

// route is a request matched against the routes the fake serves.
type route struct {
	key     string
	id      string
	scope   string
	handler handlerFunc
}

// matchRoute maps a method and path onto a route. An unknown one keeps its
// method and path as its key and has no handler.
func matchRoute(method, path string) route {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	rt := route{key: method + " " + path}
	switch {
	case method == http.MethodGet && slices.Equal(parts, []string{"oauth", "token", "info"}):
		rt = route{key: "GET /oauth/token/info", handler: (*Server).handleTokenInfo}
	case method == http.MethodGet && slices.Equal(parts, []string{"profile"}):
		rt = route{key: "GET /profile", scope: "profile:read", handler: (*Server).handleProfile}
	case method == http.MethodGet && len(parts) == 2 && parts[0] == "users":
		rt = route{key: "GET /users", id: parts[1], scope: "users:read", handler: (*Server).handleGetUser}
	case method == http.MethodGet && len(parts) == 2 && parts[0] == "chats":
		rt = route{key: "GET /chats", id: parts[1], scope: "chats:read", handler: (*Server).handleGetChat}
	case method == http.MethodPost && slices.Equal(parts, []string{"messages"}):
		rt = route{key: "POST /messages", scope: "messages:create", handler: (*Server).handleCreateMessage}
	case method == http.MethodGet && len(parts) == 2 && parts[0] == "messages":
		rt = route{key: "GET /messages", id: parts[1], scope: "messages:read", handler: (*Server).handleGetMessage}
	case method == http.MethodPut && len(parts) == 2 && parts[0] == "messages":
		rt = route{key: "PUT /messages", id: parts[1], scope: "messages:update", handler: (*Server).handleUpdateMessage}
	case method == http.MethodGet && slices.Equal(parts, []string{"webhooks", "events"}):
		rt = route{key: "GET /webhooks/events", scope: "webhooks:events:read", handler: (*Server).handleListEvents}
	case method == http.MethodDelete && len(parts) == 3 && parts[0] == "webhooks" && parts[1] == "events":
		rt = route{key: "DELETE /webhooks/events", id: parts[2], scope: "webhooks:events:delete", handler: (*Server).handleDeleteEvent}
	}
	return rt
}

func (s *Server) handleTokenInfo(r *http.Request, _ string, _ []byte, _ *Call) reply {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	used := formatTime(s.now())
	return dataReply(http.StatusOK, tokenInfo{
		ID:         1,
		Token:      maskToken(token),
		UserID:     s.opts.BotUserID,
		Scopes:     slices.Clone(s.opts.Scopes),
		CreatedAt:  formatTime(s.createdAt),
		LastUsedAt: &used,
	})
}

func (s *Server) handleProfile(_ *http.Request, _ string, _ []byte, _ *Call) reply {
	return dataReply(http.StatusOK, s.users[s.opts.BotUserID].wire())
}

func (s *Server) handleGetUser(_ *http.Request, id string, _ []byte, _ *Call) reply {
	n, err := strconv.ParseInt(id, 10, 64)
	u := s.users[n]
	if err != nil || u == nil {
		return apiErrorReply(http.StatusNotFound, "id", id, "not_found", "User not found")
	}
	return dataReply(http.StatusOK, u.wire())
}

func (s *Server) handleGetChat(_ *http.Request, id string, _ []byte, _ *Call) reply {
	n, err := strconv.ParseInt(id, 10, 64)
	c := s.chats[n]
	if err != nil || c == nil {
		return apiErrorReply(http.StatusNotFound, "id", id, "not_found", "Chat not found")
	}
	return dataReply(http.StatusOK, c.wire())
}

// maskToken shows the head and the tail of a token, the way Pachca does.
func maskToken(token string) string {
	if len(token) <= 12 {
		return "..."
	}
	return token[:8] + "..." + token[len(token)-4:]
}

// wire renders a user as the User schema.
func (u *user) wire() userJSON {
	first := u.firstName
	last := ""
	return userJSON{
		ID:               u.id,
		FirstName:        &first,
		LastName:         &last,
		Nickname:         u.nickname,
		Role:             "user",
		InviteStatus:     "confirmed",
		ListTags:         []string{},
		CustomProperties: []json.RawMessage{},
		Bot:              u.bot,
		CreatedAt:        formatTime(u.createdAt),
	}
}

// wire renders a chat as the Chat schema.
func (c *chat) wire() chatJSON {
	return chatJSON{
		ID:            c.id,
		Name:          c.name,
		CreatedAt:     formatTime(c.createdAt),
		OwnerID:       c.ownerID,
		MemberIDs:     slices.Clone(c.members),
		GroupTagIDs:   []int64{},
		Personal:      c.personal,
		LastMessageAt: formatTime(c.lastAt),
		MeetRoomURL:   "https://meet.pachca.com/chat-" + strconv.FormatInt(c.id, 10),
	}
}

// summarize renders a request body on one line, cut when long.
func summarize(body []byte) string {
	text := strings.ReplaceAll(string(body), "\n", "\\n")
	if utf8.RuneCountInString(text) > 160 {
		text = string([]rune(text)[:160]) + "…"
	}
	return text
}
