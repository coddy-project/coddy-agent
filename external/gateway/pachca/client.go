//go:build gateway || gateway.pachca

package pachca

// The Pachca REST client: the handful of methods the bot calls, one place for
// the bearer token, the {"data": ...} envelopes, the error bodies and the
// handling of 429.
//
// A 429 is retried only when Pachca asks for a short pause. A frequency limit
// is refused before the method runs (a text/plain body), so a repeated POST
// cannot create a second message. The daily limit of messages per chat
// answers with a JSON "rate_limit" code and a long Retry-After, and every
// attempt during that pause doubles it, so that one is never retried: the
// error goes up and the chat is told. Nothing else is retried for a POST.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxRetryAfter is the longest pause the client waits out on a 429 before
// trying again. Pachca's own guidance: a pause over a minute is a limit to
// report, not to sit through.
const maxRetryAfter = 60 * time.Second

// Client talks to one Pachca workspace with one bot token.
type Client struct {
	base  string
	token string
	http  *http.Client
	// sleep waits out a Retry-After; tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewClient returns a client for base (the API root, without a trailing slash).
func NewClient(base, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{
		base:  strings.TrimRight(strings.TrimSpace(base), "/"),
		token: token,
		http:  hc,
		sleep: sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// APIError is a refusal from Pachca.
type APIError struct {
	Status int
	// Codes are the errors[].code values of an ApiError body, or the error
	// field of an OAuth one.
	Codes []string
	// Message is the human text Pachca sent, when it sent one.
	Message string
	// RetryAfter is the pause a 429 asked for.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" && len(e.Codes) > 0 {
		msg = strings.Join(e.Codes, ", ")
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("pachca: HTTP %d: %s", e.Status, msg)
}

// HasCode reports whether the refusal carries code.
func (e *APIError) HasCode(code string) bool {
	for _, c := range e.Codes {
		if c == code {
			return true
		}
	}
	return false
}

// IsStatus reports whether err is a Pachca refusal with HTTP status code.
func IsStatus(err error, code int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == code
}

// IsTooLong reports whether Pachca refused a text as too long.
func IsTooLong(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.HasCode("too_long")
}

// IsRateLimited reports whether err is a 429 the client did not wait out.
func IsRateLimited(err error) bool {
	return IsStatus(err, http.StatusTooManyRequests)
}

// IsTransient reports whether err may well not happen again: the network, a
// server error or a rate limit, as opposed to a refusal that will stand (a
// missing scope, a message that does not exist).
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return true
	}
	return ae.Status == http.StatusTooManyRequests || ae.Status >= 500
}

// TokenInfo is GET /oauth/token/info.
type TokenInfo struct {
	UserID int64    `json:"user_id"`
	Scopes []string `json:"scopes"`
}

// User is the part of a Pachca user card the bot reads.
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Nickname  string `json:"nickname"`
	Bot       bool   `json:"bot"`
}

// Chat is the part of a Pachca chat the bot reads.
type Chat struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Personal bool   `json:"personal"`
	Channel  bool   `json:"channel"`
}

// Button is one button under a message: Data comes back in a button_click
// event, URL just opens a link.
type Button struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Message is the part of a Pachca message the bot reads.
type Message struct {
	ID              int64  `json:"id"`
	EntityType      string `json:"entity_type"`
	EntityID        int64  `json:"entity_id"`
	ChatID          int64  `json:"chat_id"`
	Content         string `json:"content"`
	UserID          int64  `json:"user_id"`
	ParentMessageID *int64 `json:"parent_message_id"`
}

// Target names where a message goes: a chat (a conversation, a channel, a
// thread's own chat) or a person, whose direct chat Pachca finds or opens.
type Target struct {
	EntityType string // "discussion" or "user"
	EntityID   int64
}

// ChatTarget addresses a chat by its id.
func ChatTarget(chatID int64) Target { return Target{EntityType: "discussion", EntityID: chatID} }

// UserTarget addresses a person's direct chat with the bot.
func UserTarget(userID int64) Target { return Target{EntityType: "user", EntityID: userID} }

// OutgoingMessage is what the bot posts.
type OutgoingMessage struct {
	Target   Target
	Content  string
	Buttons  [][]Button
	ParentID int64
}

// Event is one entry of the bot's events history.
type Event struct {
	ID        string          `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

// EventsPage is one page of the events history, newest first.
type EventsPage struct {
	Events  []Event
	Next    string
	HasNext bool
}

// TokenInfo reads what the token is and may do.
func (c *Client) TokenInfo(ctx context.Context) (*TokenInfo, error) {
	var out TokenInfo
	if err := c.do(ctx, http.MethodGet, "/oauth/token/info", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Profile reads the bot's own user card.
func (c *Client) Profile(ctx context.Context) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodGet, "/profile", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// User reads one person's card.
func (c *Client) User(ctx context.Context, id int64) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodGet, "/users/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DisplayName is how a message from u is introduced in a quote.
func (u *User) DisplayName() string {
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name == "" && u.Nickname != "" {
		name = "@" + strings.TrimPrefix(u.Nickname, "@")
	}
	return name
}

// Chat reads one chat.
func (c *Client) Chat(ctx context.Context, id int64) (*Chat, error) {
	var out Chat
	if err := c.do(ctx, http.MethodGet, "/chats/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Message reads one message.
func (c *Client) Message(ctx context.Context, id int64) (*Message, error) {
	var out Message
	if err := c.do(ctx, http.MethodGet, "/messages/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendMessage posts a message and returns it as Pachca stored it. Mentions
// never invite anybody into a thread: the bot names people, it does not
// decide who joins.
func (c *Client) SendMessage(ctx context.Context, m OutgoingMessage) (*Message, error) {
	body := map[string]any{
		"entity_type":          m.Target.EntityType,
		"entity_id":            m.Target.EntityID,
		"content":              m.Content,
		"skip_invite_mentions": true,
	}
	if len(m.Buttons) > 0 {
		body["buttons"] = m.Buttons
	}
	if m.ParentID != 0 {
		body["parent_message_id"] = m.ParentID
	}
	var out Message
	if err := c.do(ctx, http.MethodPost, "/messages", map[string]any{"message": body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EditMessage replaces a message's text. Buttons nil leaves them as they are,
// an empty slice removes them.
func (c *Client) EditMessage(ctx context.Context, id int64, content string, buttons [][]Button) error {
	body := map[string]any{"content": content}
	if buttons != nil {
		body["buttons"] = buttons
	}
	return c.do(ctx, http.MethodPut, "/messages/"+strconv.FormatInt(id, 10), map[string]any{"message": body}, nil)
}

// ListEvents reads one page of the events history. An empty cursor reads the
// newest page.
func (c *Client) ListEvents(ctx context.Context, cursor string, limit int) (*EventsPage, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var env struct {
		Data []Event `json:"data"`
		Meta struct {
			Paginate struct {
				NextPage string `json:"next_page"`
				HasNext  bool   `json:"has_next"`
			} `json:"paginate"`
		} `json:"meta"`
	}
	if err := c.doRaw(ctx, http.MethodGet, "/webhooks/events?"+q.Encode(), nil, &env); err != nil {
		return nil, err
	}
	return &EventsPage{Events: env.Data, Next: env.Meta.Paginate.NextPage, HasNext: env.Meta.Paginate.HasNext}, nil
}

// DeleteEvent removes a handled event from the history.
func (c *Client) DeleteEvent(ctx context.Context, id string) error {
	return c.doRaw(ctx, http.MethodDelete, "/webhooks/events/"+url.PathEscape(id), nil, nil)
}

// do calls a method whose answer is wrapped in {"data": ...}.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if out == nil {
		return c.doRaw(ctx, method, path, in, nil)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.doRaw(ctx, method, path, in, &env); err != nil {
		return err
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("pachca: %s %s: the answer has no data", method, path)
	}
	return json.Unmarshal(env.Data, out)
}

// doRaw sends one request, waiting out a short 429 and trying again.
func (c *Client) doRaw(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		err := c.once(ctx, method, path, payload, out)
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests || attempt >= 3 {
			return err
		}
		if ae.HasCode("rate_limit") || ae.RetryAfter > maxRetryAfter {
			return err
		}
		wait := ae.RetryAfter
		if wait <= 0 {
			wait = time.Second
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return err
		}
	}
}

func (c *Client) once(ctx context.Context, method, path string, payload []byte, out any) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("pachca: %s %s: %w", method, strings.SplitN(path, "?", 2)[0], err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("pachca: %s %s: decode: %w", method, strings.SplitN(path, "?", 2)[0], err)
	}
	return nil
}

// parseAPIError reads either error body Pachca sends - {"errors":[...]} or the
// OAuth {"error","error_description"} - or the plain text of a 429.
func parseAPIError(resp *http.Response, raw []byte) error {
	ae := &APIError{Status: resp.StatusCode}
	if s := strings.TrimSpace(resp.Header.Get("Retry-After")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			ae.RetryAfter = time.Duration(n) * time.Second
		}
	}
	var api struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(raw, &api) == nil {
		for _, e := range api.Errors {
			if e.Code != "" {
				ae.Codes = append(ae.Codes, e.Code)
			}
			if ae.Message == "" {
				ae.Message = e.Message
			}
		}
		if api.Error != "" {
			ae.Codes = append(ae.Codes, api.Error)
			ae.Message = api.ErrorDescription
		}
	} else if text := strings.TrimSpace(string(raw)); text != "" && len(text) < 300 {
		ae.Message = text
	}
	return ae
}
