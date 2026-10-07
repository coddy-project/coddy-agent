//go:build gateway || gateway.pachca

package pachca

// Permission prompts of subagents, asked in the chat with buttons.
//
// The bot allows what the chat's own agent asks; a subagent whose definition
// narrowed what it may do is asked about in the chat, and only the person
// whose session asked may answer. A background subagent's prompt reaches the
// chat that owns its parent session after the turn ended too; the first answer
// from any surface wins, and the message is edited to say the request no
// longer waits.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type chatPrompt struct {
	sessionID string
	options   []acp.PermissionOption
	answer    chan *acp.PermissionResult
}

// chatPermissions holds the requests waiting for a click, keyed by the token
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

// stop withdraws every request still waiting: a stopped bot reads no clicks.
func (c *chatPermissions) stop() {
	c.once.Do(func() { close(c.stopped) })
}

func newPromptToken() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ask posts the request into target and blocks until the person of sessionID
// clicks a button, or ctx ends, or the bot stops. A nil result means nobody
// answered here.
func (c *chatPermissions) ask(ctx context.Context, b *Bot, cl *Client, target Target, sessionID string, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
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
	var row []Button
	var rows [][]Button
	for i, opt := range params.Options {
		label := strings.TrimSpace(opt.Name)
		if label == "" {
			label = opt.OptionID
		}
		row = append(row, Button{Text: label, Data: actionPermission + ":" + token + ":" + strconv.Itoa(i)})
		if len(row) == maxButtonsPerRow {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	m, err := cl.SendMessage(ctx, OutgoingMessage{Target: target, Content: text, Buttons: rows})
	if err != nil {
		return nil, err
	}
	b.posted.add(m.ID)
	b.log.Debug("pachca: permission asked", "target", target.EntityID, "session", sessionID, "toolCallId", params.ToolCall.ToolCallID)

	settle := func(note string) {
		// The edit takes the buttons away with an empty list.
		if err := cl.EditMessage(context.WithoutCancel(ctx), m.ID, text+"\n\n"+note, [][]Button{}); err != nil {
			b.log.Debug("pachca: permission message edit", "err", err)
		}
	}
	select {
	case res := <-p.answer:
		if strings.HasPrefix(res.OptionID, "reject") {
			settle("Denied.")
		} else {
			settle("Allowed.")
		}
		return res, nil
	case <-ctx.Done():
		settle("No longer waiting.")
		return nil, nil
	case <-c.stopped:
		settle("No longer waiting.")
		return nil, nil
	}
}

// resolve answers the request behind a clicked button. It refuses a token
// nobody waits for, an option the request did not offer, and a click from
// somebody whose session in this chat is not the one that asked.
func (c *chatPermissions) resolve(token string, index int, clickerSessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pending[token]
	if p == nil || clickerSessionID == "" || clickerSessionID != p.sessionID || index < 0 || index >= len(p.options) {
		return false
	}
	select {
	case p.answer <- &acp.PermissionResult{Outcome: "selected", OptionID: p.options[index].OptionID}:
	default:
		return false
	}
	delete(c.pending, token)
	return true
}

func permissionText(params acp.PermissionRequestParams) string {
	title := strings.TrimSpace(params.ToolCall.Title)
	if title == "" {
		title = "A subagent asks for permission"
	}
	text := "🔐 " + title
	for _, c := range params.ToolCall.Content {
		if body := strings.TrimSpace(c.Content.Text); body != "" {
			if r := []rune(body); len(r) > 3000 {
				body = string(r[:3000]) + "…"
			}
			text += "\n\n```text\n" + body + "\n```"
			break
		}
	}
	return text
}

// answerPermissionClick resolves a permission button clicked by the person
// whose session key is key.
func (b *Bot) answerPermissionClick(_ context.Context, _ *Client, p buttonPayload, value, key string) {
	// Approving what an agent asks is the bot's admins': in a shared group
	// session anybody shares the asking session.
	if !b.cfg.IsAdmin(p.UserID) {
		b.log.Debug("pachca: permission click refused", "reason", "approvals are admin-only", "user", p.UserID, "chat", p.ChatID)
		return
	}
	token, idx, ok := strings.Cut(value, ":")
	if !ok {
		return
	}
	i, err := strconv.Atoi(idx)
	if err != nil {
		return
	}
	if !b.permissions().resolve(token, i, b.store.Peek(key)) {
		b.log.Debug("pachca: permission click refused", "user", p.UserID, "chat", p.ChatID)
		return
	}
	b.log.Debug("pachca: permission answered", "user", p.UserID, "chat", p.ChatID)
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker: a
// background subagent whose parent session is a conversation of this bot is
// asked about in that chat.
func (b *Bot) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	if strings.TrimSpace(req.Params.EffectivePermissionMode) == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	c := b.connectedClient()
	if c == nil {
		return nil, agent.ErrNoDetachedApprover
	}
	key, ok := b.store.KeyFor(req.ParentSessionID)
	if !ok {
		return nil, agent.ErrNoDetachedApprover
	}
	target, ok := targetForKey(key)
	if !ok {
		return nil, agent.ErrNoDetachedApprover
	}
	// The conversation of somebody who is not the bot's admin approves
	// nothing, now as during its turns.
	if !access.KeyIsAdmin(key, b.cfg) {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	res, err := b.permissions().ask(ctx, b, c, target, req.ParentSessionID, req.Params)
	if err != nil {
		b.log.Warn("pachca: detached permission not delivered", "err", err, "session", req.ParentSessionID)
		return nil, agent.ErrNoDetachedApprover
	}
	return res, nil
}

var _ agent.DetachedPermissionBroker = (*Bot)(nil)
