//go:build gateway || gateway.pachca

package pachca

// Commands with buttons and the clicks that answer them.
//
// A button with data comes back as a button_click event naming the message,
// the person and the data. Data is capped at 255 bytes: a model id that does
// not fit next to its prefix travels as a digest and is resolved against the
// configured models, never truncated.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	buttonDataMax    = 255
	actionModel      = "model"
	actionPermission = "perm"
	digestPrefix     = "#"
	maxButtonsPerRow = 8
	maxButtons       = 100
)

// buttonValue is value itself when "<action>:<value>" fits in a button's
// data, else a digest of it.
func buttonValue(action, value string) string {
	if len(action)+1+len(value) <= buttonDataMax {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return digestPrefix + hex.EncodeToString(sum[:12])
}

// resolveModelValue maps a button value back to a configured model id.
func resolveModelValue(models []config.ModelEntry, value string) (string, bool) {
	for _, m := range models {
		if m.Model == value || (strings.HasPrefix(value, digestPrefix) && buttonValue(actionModel, m.Model) == value) {
			return m.Model, true
		}
	}
	return "", false
}

func modelMenuText(current string) string {
	if current == "" {
		return "**Model**\n\nChoose the model for this conversation:"
	}
	return "**Model**: `" + current + "`\n\nChoose the model for this conversation:"
}

// modelButtons lays the configured models out one per row, the current one
// marked.
func modelButtons(models []config.ModelEntry, current string) [][]Button {
	var rows [][]Button
	for i, m := range models {
		if i >= maxButtons {
			break
		}
		label := m.Model
		if m.Model == current {
			label = "• " + label
		}
		if len(label) > buttonDataMax {
			label = label[:buttonDataMax]
		}
		rows = append(rows, []Button{{Text: label, Data: actionModel + ":" + buttonValue(actionModel, m.Model)}})
	}
	return rows
}

func (b *Bot) handleModelCommand(ctx context.Context, c *Client, target Target, parent int64, key string) {
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(ctx, c, target, parent, "Session error: "+err.Error())
		return
	}
	cfg := b.runner.Cfg()
	if len(cfg.Models) == 0 {
		b.reply(ctx, c, target, parent, "No models are configured.")
		return
	}
	current := st.EffectiveModelID(cfg)
	b.log.Debug("pachca: model menu", "session", st.GetID(), "current", current)
	m, err := c.SendMessage(ctx, OutgoingMessage{
		Target: target, Content: modelMenuText(current), Buttons: modelButtons(cfg.Models, current), ParentID: parent,
	})
	if err != nil {
		b.log.Warn("pachca: send model menu", "err", err)
		return
	}
	b.posted.add(m.ID)
}

func (b *Bot) handleContextCommand(ctx context.Context, c *Client, target Target, parent int64, key string) {
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.reply(ctx, c, target, parent, "Session error: "+err.Error())
		return
	}
	bd := st.GetLastContextBreakdown()
	if bd == nil {
		b.reply(ctx, c, target, parent, "**Context usage**\n\nNo data yet: send a message first.")
		return
	}
	b.reply(ctx, c, target, parent, formatContextBreakdown(bd, st.GetID()))
}

func formatContextBreakdown(bd *session.ContextBreakdown, sessionID string) string {
	rows := []struct {
		label string
		val   int
	}{
		{"Conversation", bd.Conversation},
		{"System prompt", bd.SystemPrompt},
		{"Tool definitions", bd.ToolDefinitions},
		{"Rules", bd.Rules},
		{"Skills", bd.Skills},
		{"MCP", bd.MCP},
		{"Subagents", bd.Subagents},
	}
	var sb strings.Builder
	sb.WriteString("**Context usage**\n`" + sessionID + "`\n\n```text\n")
	for _, r := range rows {
		if r.val == 0 {
			continue
		}
		fmt.Fprintf(&sb, "%-18s %d\n", r.label+":", r.val)
	}
	sb.WriteString("```\n**Total about " + fmt.Sprint(bd.EstimatedTotal) + " tokens** (estimate: runes / 4)")
	return sb.String()
}

// clickKey is the session key of the person who clicked, in the chat the
// button is in. An error is a lookup to try again.
func (b *Bot) clickKey(ctx context.Context, c *Client, p buttonPayload) (string, bool, error) {
	isGroup, err := b.isGroup(ctx, c, p.ChatID, "")
	if err != nil {
		return "", false, err
	}
	if !b.allowed(p.ChatID, p.UserID, isGroup) {
		return "", false, nil
	}
	isolation := access.EffectiveIsolation(p.ChatID, b.cfg)
	return sessionstore.SessionKey(adapterName, p.ChatID, p.UserID, isolation, isGroup), true, nil
}

// handleButton answers a click. It runs off the session workers, so a click
// that answers a permission request reaches the turn waiting for it. It
// reports false when the click must be read again.
func (b *Bot) handleButton(ctx context.Context, c *Client, p buttonPayload) bool {
	action, value, _ := strings.Cut(p.Data, ":")
	b.log.Debug("pachca: update", "kind", "button", "user", p.UserID, "chat", p.ChatID, "action", action)
	if action != actionModel && action != actionPermission {
		b.log.Debug("pachca: update ignored", "reason", "unknown button", "data_len", len(p.Data))
		return true
	}
	key, ok, err := b.clickKey(ctx, c, p)
	if err != nil {
		return false
	}
	if !ok {
		return true
	}
	switch action {
	case actionPermission:
		b.answerPermissionClick(ctx, c, p, value, key)
	case actionModel:
		// A chat that cannot be read counts as a group, as everywhere else.
		if isGroup, err := b.isGroup(ctx, c, p.ChatID, ""); (err != nil || isGroup) && !b.cfg.IsAdmin(p.UserID) {
			b.reply(ctx, c, ChatTarget(p.ChatID), p.MessageID, adminOnlyNote)
			return true
		}
		if !b.beginTurn() {
			return false
		}
		go func() {
			defer b.inFlight.Done()
			b.applyModelClick(ctx, c, p, value, key)
		}()
	}
	return true
}

func (b *Bot) applyModelClick(ctx context.Context, c *Client, p buttonPayload, value, key string) {
	cfg := b.runner.Cfg()
	model, ok := resolveModelValue(cfg.Models, value)
	if !ok {
		b.log.Debug("pachca: button ignored", "reason", "unknown model")
		return
	}
	// The menu outlives the process that sent it: after a restart the
	// session is on disk, and the manager only configures live ones.
	st, err := b.ensureSession(ctx, key)
	if err != nil {
		b.log.Warn("pachca: model click: ensure session", "err", err)
		return
	}
	if _, err := b.runner.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
		SessionID: st.GetID(), ConfigID: "model", Value: model,
	}); err != nil {
		b.log.Warn("pachca: set model", "err", err, "session", st.GetID(), "model", model)
		return
	}
	b.log.Info("pachca: model applied", "session", st.GetID(), "model", model)
	// The model a fresh conversation starts on is an admin's pick; anybody
	// else's stays in their own session.
	if b.cfg.IsAdmin(p.UserID) {
		b.store.SetLastModel(model)
	}
	if err := c.EditMessage(ctx, p.MessageID, modelMenuText(model), modelButtons(cfg.Models, model)); err != nil {
		b.log.Debug("pachca: edit model menu", "err", err)
	}
}
