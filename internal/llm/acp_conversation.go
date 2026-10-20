package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// acpConversation binds one Coddy session to one ACP session of an agent
// process. Coddy sends the whole history on every call; the ACP session keeps
// its own, so a call sends the agent only what the session does not hold yet.
type acpConversation struct {
	// slot is the conversation's lock, a semaphore so a call can wait for it
	// under its own context: the prompts of one ACP session take turns, and
	// no call ever forks a second session for a conversation that is busy.
	slot chan struct{}
	// The fields below are guarded by holding slot.
	// sessionID is the bound ACP session; empty when there is none.
	sessionID string
	// record says what the ACP session holds, in order.
	record []acpRecordEntry
	// uncertain marks a last prompt whose outcome is unknown: it was
	// cancelled, failed or lost its process after the agent reported
	// something, so the agent may have acted on it.
	uncertain bool
	// lastUsed is when a call last let go of the binding; a binding unused
	// for the agent's idle period is released (acpPoolEntry.sweep).
	lastUsed time.Time
}

func newACPConversation() *acpConversation {
	return &acpConversation{slot: make(chan struct{}, 1)}
}

// lock waits for the conversation, bounded by ctx.
func (c *acpConversation) lock(ctx context.Context) error {
	select {
	case c.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *acpConversation) unlock() { <-c.slot }

// reset forgets the bound session.
func (c *acpConversation) reset() {
	c.sessionID, c.record, c.uncertain = "", nil, false
}

// acpRecordEntry is one message the ACP session holds: one Coddy sent
// (digest), or the agent's own answer to a prompt that completed with text
// (ownReply).
type acpRecordEntry struct {
	digest string
	// ownReply matches the assistant message Coddy stored for the agent's
	// answer: zero or one assistant message without tool calls. Its text is
	// not compared, since Coddy stores the answer as it keeps it (trimmed,
	// folded with a continuation), not as it streamed.
	ownReply bool
}

// sweep releases the bindings no call held for the agent's idle period:
// their ACP sessions are closed (when the agent can) and forgotten, so a
// shared agent that never idles out does not keep a session for every Coddy
// session that ever used it. A busy binding is left alone.
func (e *acpPoolEntry) sweep() {
	if e.proc == nil {
		return
	}
	e.convMu.Lock()
	defer e.convMu.Unlock()
	for key, c := range e.conversations {
		select {
		case c.slot <- struct{}{}:
		default:
			continue
		}
		if !c.lastUsed.IsZero() && time.Since(c.lastUsed) >= e.spec.idle {
			if c.sessionID != "" {
				e.proc.closeSession(c.sessionID)
			}
			delete(e.conversations, key)
		}
		<-c.slot
	}
}

// conversation returns the binding of key on this process, creating it.
func (e *acpPoolEntry) conversation(key string) *acpConversation {
	e.convMu.Lock()
	defer e.convMu.Unlock()
	c := e.conversations[key]
	if c == nil {
		c = newACPConversation()
		e.conversations[key] = c
	}
	return c
}

// acpMessageDigest identifies a message for the record: its role, text,
// pictures, tool calls and the call a tool result answers.
//
// Two parts are left out because the loop rewrites them in what it sends,
// while the message stays the same message: the text of a tool result (the
// eviction of old results replaces it with a stub), known by the call it
// answers, and the pictures (only the newest ones are sent).
func acpMessageDigest(m Message) string {
	h := sha256.New()
	write := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }
	write(string(m.Role))
	write(m.ToolCallID)
	if m.Role != RoleTool || m.ToolCallID == "" {
		write(m.Content)
	}
	for _, tc := range m.ToolCalls {
		write(tc.ID)
		write(tc.Name)
		write(tc.InputJSON)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// acpHistory splits what the loop sends into the conversation the agent
// sees and the ephemeral messages: the system message is left out (the agent
// has its own instructions and tools, and Coddy's describe tools the agent
// cannot call); the ephemeral ones (the turn context, a recovery nudge) are
// sent with the prompt but never recorded, since no later history holds
// them.
func acpHistory(messages []Message) (history, ephemeral []Message) {
	for _, m := range messages {
		switch {
		case m.Role == RoleSystem:
		case m.Ephemeral:
			ephemeral = append(ephemeral, m)
		default:
			history = append(history, m)
		}
	}
	return history, ephemeral
}

// matchRecord walks the record against the history from the front. It
// returns how many history messages the ACP session already holds, and false
// when the history went another way (a rewind, an edit, a compaction).
func matchRecord(record []acpRecordEntry, history []Message) (int, bool) {
	i := 0
	for _, r := range record {
		if r.ownReply {
			if i < len(history) && history[i].Role == RoleAssistant && len(history[i].ToolCalls) == 0 {
				i++
			}
			continue
		}
		if i >= len(history) || acpMessageDigest(history[i]) != r.digest {
			return 0, false
		}
		i++
	}
	return i, true
}

// endsWithOwnReply reports whether the last message of a history the record
// covers whole is the one the record's last answer slot took: an assistant
// message without tool calls after the prompt the slot answers.
func endsWithOwnReply(record []acpRecordEntry, history []Message) bool {
	if len(record) == 0 || !record[len(record)-1].ownReply || len(history) == 0 {
		return false
	}
	last := history[len(history)-1]
	return last.Role == RoleAssistant && len(last.ToolCalls) == 0
}

// acpPromptPlan is what one call sends.
type acpPromptPlan struct {
	// send are the history messages the prompt carries.
	send []Message
	// fresh: the prompt opens a new ACP session and carries the whole
	// history.
	fresh bool
}

// planPrompt decides what a call on the bound session sends. ok is false
// when the session cannot be continued and a new one must start; err is set
// when the call repeats a prompt whose outcome is unknown, which is never
// sent twice.
func (c *acpConversation) planPrompt(history []Message) (plan acpPromptPlan, ok bool, err error) {
	if c.sessionID == "" || len(c.record) == 0 {
		return acpPromptPlan{}, false, nil
	}
	held, matched := matchRecord(c.record, history)
	if !matched {
		return acpPromptPlan{}, false, nil
	}
	if held < len(history) {
		return acpPromptPlan{send: history[held:]}, true, nil
	}
	// The session holds the whole history.
	if c.uncertain && endsWithOwnReply(c.record, history) {
		// It ends with what the agent wrote before its prompt was stopped:
		// the loop calls again after cutting a degenerating answer, with a
		// nudge of its own. The stopped prompt is not sent again; what this
		// call adds (its ephemeral messages) is.
		return acpPromptPlan{}, true, nil
	}
	// The call repeats the last prompt.
	if c.uncertain {
		return acpPromptPlan{}, false, &ACPError{Kind: ACPErrorStalled, Message: "the agent's last prompt in this conversation ended without a clear outcome and may have run; it is not sent again. Send a new message to go on"}
	}
	// After a completed prompt it is a regenerate: a new session answers it.
	return acpPromptPlan{}, false, nil
}

// commit records what a prompt carried and, when armed (the prompt
// completed with the agent's own text), the slot of its answer.
func (c *acpConversation) commit(plan acpPromptPlan, armed bool) {
	if plan.fresh {
		c.record = nil
	}
	for _, m := range plan.send {
		c.record = append(c.record, acpRecordEntry{digest: acpMessageDigest(m)})
	}
	if armed {
		c.record = append(c.record, acpRecordEntry{ownReply: true})
	}
	c.uncertain = !armed
}

// commitStopped records a prompt the caller stopped after the agent had
// reported something. Its outcome stays uncertain, so the same history is
// never sent again, but what the agent wrote before the stop is in its
// session as much as in Coddy's transcript: the slot of its answer is armed,
// and the partial answer Coddy kept is not sent back to the agent as another
// model's words. No other model answers a stopped turn, which is what keeps
// that slot from swallowing a fallback's answer (the case of a prompt that
// failed, which commit leaves unarmed).
func (c *acpConversation) commitStopped(plan acpPromptPlan) {
	c.commit(plan, false)
	c.record = append(c.record, acpRecordEntry{ownReply: true})
}

// acpPromptBlocks renders what a prompt carries. A single user message goes
// as itself; several (the history of a new session, or what happened while
// other models ran the session) go as a plain transcript followed by the last
// user message. Pictures go as image blocks where they stand when the agent
// takes them, as a line naming each otherwise.
func acpPromptBlocks(plan acpPromptPlan, ephemeral []Message, images bool) []acpContentBlock {
	send := plan.send
	var blocks []acpContentBlock
	var last *Message
	if n := len(send); n > 0 && send[n-1].Role == RoleUser {
		last = &send[n-1]
		send = send[:n-1]
	}
	if len(send) > 0 {
		var b strings.Builder
		if plan.fresh {
			b.WriteString("The conversation so far, which you are continuing. Answer the last user message.\n")
		} else {
			b.WriteString("Messages added to this conversation since your last answer (another model may have written some of them).\n")
		}
		flush := func() {
			if t := strings.TrimRight(b.String(), "\n"); strings.TrimSpace(t) != "" {
				blocks = append(blocks, acpContentBlock{Type: "text", Text: t})
			}
			b.Reset()
		}
		for _, m := range send {
			b.WriteString("\n")
			writeTranscriptMessage(&b, m, images)
			if images && m.Role == RoleUser && hasDataImages(m) {
				flush()
				// Only the pictures the transcript did not name.
				for _, blk := range acpImageBlocks(m, true) {
					if blk.Type == "image" {
						blocks = append(blocks, blk)
					}
				}
			}
		}
		flush()
	}
	if last != nil {
		text := last.Content
		if len(send) > 0 {
			text = "## User (the message to answer)\n" + text
		}
		if strings.TrimSpace(text) != "" {
			// A text block carries text: a message of pictures alone sends
			// its pictures only.
			blocks = append(blocks, acpContentBlock{Type: "text", Text: text})
		}
		blocks = append(blocks, acpImageBlocks(*last, images)...)
	}
	for _, m := range ephemeral {
		if strings.TrimSpace(m.Content) != "" {
			blocks = append(blocks, acpContentBlock{Type: "text", Text: m.Content})
		}
	}
	if len(blocks) == 0 {
		// Nothing to say (an empty history): the agent still needs a prompt.
		blocks = append(blocks, acpContentBlock{Type: "text", Text: "Continue."})
	}
	return blocks
}

// writeTranscriptMessage writes one message of a transcript block. A
// picture the agent receives as an image block is not named again.
func writeTranscriptMessage(b *strings.Builder, m Message, images bool) {
	switch m.Role {
	case RoleUser:
		b.WriteString("## User\n")
		b.WriteString(m.Content)
		for _, img := range m.ImageParts {
			if images {
				if _, _, ok := splitDataURL(img.DataURL); ok {
					continue
				}
			}
			fmt.Fprintf(b, "\n[picture: %s]", imageLabel(img))
		}
	case RoleAssistant:
		if strings.TrimSpace(m.Content) != "" {
			b.WriteString("## Assistant\n")
			b.WriteString(m.Content)
		}
		for _, tc := range m.ToolCalls {
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("\n")
			}
			fmt.Fprintf(b, "## Assistant called the tool %s\n%s", tc.Name, tc.InputJSON)
		}
	case RoleTool:
		b.WriteString("## Tool result\n")
		b.WriteString(m.Content)
	default:
		fmt.Fprintf(b, "## %s\n%s", m.Role, m.Content)
	}
	b.WriteString("\n")
}

func hasDataImages(m Message) bool {
	for _, img := range m.ImageParts {
		if _, _, ok := splitDataURL(img.DataURL); ok {
			return true
		}
	}
	return false
}

func imageLabel(img ImagePart) string {
	switch {
	case img.Name != "":
		return img.Name
	case img.FilePath != "":
		return img.FilePath
	default:
		return "attached"
	}
}

// acpImageBlocks returns the pictures of m as image blocks when the agent
// takes them, else a text line naming each.
func acpImageBlocks(m Message, images bool) []acpContentBlock {
	var out []acpContentBlock
	for _, img := range m.ImageParts {
		mime, data, ok := splitDataURL(img.DataURL)
		if images && ok {
			out = append(out, acpContentBlock{Type: "image", MIMEType: mime, Data: data})
			continue
		}
		out = append(out, acpContentBlock{Type: "text", Text: "[picture: " + imageLabel(img) + "]"})
	}
	return out
}

// splitDataURL reads a base64 data URL.
func splitDataURL(u string) (mime, data string, ok bool) {
	rest, found := strings.CutPrefix(u, "data:")
	if !found {
		return "", "", false
	}
	head, body, found := strings.Cut(rest, ",")
	if !found {
		return "", "", false
	}
	mime, enc, _ := strings.Cut(head, ";")
	if enc != "base64" || mime == "" {
		return "", "", false
	}
	return mime, body, true
}
