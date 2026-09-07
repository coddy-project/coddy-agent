//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// editInterval is how often the live message is updated while tokens stream in.
	editInterval = 1500 * time.Millisecond
	// typingInterval is how often the "typing…" chat action is refreshed (Telegram shows it for 5s).
	typingInterval = 4 * time.Second
)

// Sender implements acp.UpdateSender and streams agent output back to a Telegram chat.
//
// Two separate accumulators:
//   - responseBuf: LLM text tokens only → used in Flush() for the final persistent message.
//   - currentTool: name of the tool currently executing → shown in the live streaming message
//     as "⚙️ toolname…" but NOT included in the final message, keeping it clean.
//
// Streaming: first token → new message sent, ID saved. Subsequent tokens → editMessageText,
// throttled to editInterval. Flush() replaces the live message with the final formatted text.
type Sender struct {
	bot     *tgbotapi.BotAPI
	chatID  int64
	replyTo int // original user message ID; zeroed after first send

	log *slog.Logger

	rich richConfig // Bot API 10.1 Rich Messages mode (off → legacy formatting)

	mu          sync.Mutex
	responseBuf strings.Builder      // LLM text only — sent in Flush()
	currentTool string               // tool currently running — shown in stream, not in Flush()
	toolOrder   []string             // tool-call keys in execution order
	toolByID    map[string]*toolCall // captured tool calls (name, args, result) keyed by call id
	liveID      int                  // message ID being progressively edited; 0 = none sent yet
	lastEdit    time.Time
	lastTyping  time.Time
}

// richConfig controls the Rich Messages behaviour of a Sender.
type richConfig struct {
	enabled    bool  // send the final turn via sendRichMessage instead of legacy formatting
	allowDraft bool  // stream a sendRichMessageDraft preview (private chats only)
	draftID    int64 // non-zero draft identifier for this turn; reused so updates animate
}

func newSender(bot *tgbotapi.BotAPI, chatID int64, replyTo int, log *slog.Logger, rich richConfig) *Sender {
	return &Sender{bot: bot, chatID: chatID, replyTo: replyTo, log: log, rich: rich,
		toolByID: make(map[string]*toolCall)}
}

// ensureTool returns the toolCall for key, creating and ordering it on first sight.
// Caller holds s.mu.
func (s *Sender) ensureTool(key string) *toolCall {
	if tc, ok := s.toolByID[key]; ok {
		return tc
	}
	tc := &toolCall{}
	s.toolByID[key] = tc
	s.toolOrder = append(s.toolOrder, key)
	return tc
}

// collectTools returns the captured tool calls in execution order. Caller holds s.mu.
func (s *Sender) collectTools() []toolCall {
	out := make([]toolCall, 0, len(s.toolOrder))
	for _, k := range s.toolOrder {
		if tc := s.toolByID[k]; tc != nil {
			out = append(out, *tc)
		}
	}
	return out
}

// toolResultText extracts the first text content block from a tool status update.
func toolResultText(items []acp.ToolCallResultItem) string {
	for _, it := range items {
		if it.Content.Type == acp.ContentTypeText {
			return it.Content.Text
		}
	}
	return ""
}

// SendSessionUpdate handles streaming events from the agent.
func (s *Sender) SendSessionUpdate(_ string, update interface{}) error {
	switch u := update.(type) {

	case acp.MessageChunkUpdate:
		if u.Content.Type != acp.ContentTypeText {
			return nil
		}
		s.mu.Lock()
		s.responseBuf.WriteString(u.Content.Text)
		s.currentTool = "" // LLM is writing → last tool has finished
		text := s.responseBuf.String()
		now := time.Now()
		wantEdit := now.Sub(s.lastEdit) >= editInterval
		if wantEdit {
			s.lastEdit = now
		}
		s.mu.Unlock()

		if wantEdit {
			s.stream(text, "")
		}

	case acp.ToolCallUpdate:
		title := u.Title
		if title == "" {
			title = u.Kind
		}
		if title == "" {
			return nil
		}
		s.mu.Lock()
		s.currentTool = title
		tc := s.ensureTool(toolKey(u.ToolCallID, title))
		tc.name = title
		llmText := s.responseBuf.String()
		now := time.Now()
		wantEdit := now.Sub(s.lastEdit) >= editInterval
		wantTyping := now.Sub(s.lastTyping) >= typingInterval
		if wantEdit {
			s.lastEdit = now
		}
		if wantTyping {
			s.lastTyping = now
		}
		s.mu.Unlock()

		// In draft mode the <tg-thinking> block conveys activity, so skip the
		// redundant typing action; legacy and group-rich paths still use it.
		if wantTyping && !s.rich.allowDraft {
			s.sendTyping()
		}
		if wantEdit {
			s.stream(llmText, title)
		}

	case acp.ToolCallStatusUpdate:
		// Capture tool args (in_progress) and output (completed/failed) for the
		// per-tool <details> blocks rendered in the final rich message.
		text := toolResultText(u.Content)
		s.mu.Lock()
		if tc := s.toolForStatus(u.ToolCallID); tc != nil {
			switch u.Status {
			case "in_progress":
				tc.args = text
			case "failed", "cancelled":
				tc.result = text
				tc.failed = true
			default: // "completed" and any terminal status
				if text != "" {
					tc.result = text
				}
			}
		}
		s.mu.Unlock()
	}
	return nil
}

// toolKey keys a started tool call by its id, falling back to the title when the
// provider does not supply an id.
func toolKey(id, title string) string {
	if k := strings.TrimSpace(id); k != "" {
		return k
	}
	return "title:" + title
}

// toolForStatus resolves a status update to its tool: by id when present, otherwise
// the most recently started tool (status updates arrive right after their start, in
// sequence). Returns nil when there is no tool to attach to. Caller holds s.mu.
func (s *Sender) toolForStatus(id string) *toolCall {
	if k := strings.TrimSpace(id); k != "" {
		return s.ensureTool(k)
	}
	if len(s.toolOrder) == 0 {
		return nil
	}
	return s.toolByID[s.toolOrder[len(s.toolOrder)-1]]
}

// stream dispatches a progressive update to the right transport: a rich draft
// (private chats, Rich Messages on), or the legacy live editMessageText path.
// Rich group chats get no progressive update (drafts are private-only); their
// content is delivered by the final sendRichMessage in Flush().
func (s *Sender) stream(llmText, toolName string) {
	switch {
	case s.rich.enabled && s.rich.allowDraft:
		s.streamDraft(llmText, toolName)
	case s.rich.enabled:
		// Rich group chat: no ephemeral draft available; wait for Flush().
	default:
		s.streamUpdate(llmText, toolName)
	}
}

// streamDraft sends an ephemeral sendRichMessageDraft preview for this turn.
func (s *Sender) streamDraft(llmText, toolName string) {
	md := buildRichDraftMarkdown(llmText, toolName)
	if md == "" {
		return
	}
	if err := sendRichMessageDraft(s.bot, s.chatID, s.rich.draftID, md); err != nil {
		s.log.Debug("telegram: rich draft", "err", err)
	}
}

// RequestPermission auto-approves in gateway context (no interactive UI).
// A subagent's request carries the child's own effective mode: a child
// narrowed below bypass cannot be prompted here, so it is denied rather than
// waved through on the parent's behalf.
func (s *Sender) RequestPermission(_ context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if stamped := strings.TrimSpace(params.EffectivePermissionMode); stamped != "" && stamped != "bypass" {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

// RequestQuestion sends the question text to Telegram and returns an empty answer.
func (s *Sender) RequestQuestion(_ context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if len(params.Questions) > 0 {
		_ = s.sendFormatted(params.Questions[0].Question, 0)
	}
	return &acp.QuestionResult{}, nil
}

// Flush sends the final accumulated LLM text as a persistent message.
// If a live streaming message exists it is edited in-place; otherwise a new message is sent.
// Tool indicators are NOT included — only the clean LLM response.
func (s *Sender) Flush() {
	s.mu.Lock()
	text := s.responseBuf.String()
	s.responseBuf.Reset()
	s.currentTool = ""
	tools := s.collectTools()
	liveID := s.liveID
	s.liveID = 0
	replyTo := s.replyTo
	s.mu.Unlock()

	if s.rich.enabled {
		if s.flushRich(text, tools, replyTo) {
			return
		}
		// Rich send failed — fall through to the legacy formatted send so the
		// user still receives the reply.
	}
	s.flushLegacy(text, liveID, replyTo)
}

// flushRich sends the final persistent message via sendRichMessage. It returns
// true on success and false when the caller should fall back to legacy formatting.
//
// If the combined message (answer + per-tool blocks) is rejected, it retries with the
// answer alone before giving up — the tool-output blocks are the riskiest part of the
// payload, so this keeps the assistant's reply visible even when they break the parse.
func (s *Sender) flushRich(text string, tools []toolCall, replyTo int) bool {
	md := buildRichMarkdown(text, tools)
	if md == "" {
		return true // nothing to send; treat as handled
	}
	if _, err := sendRichMessage(s.bot, s.chatID, md, replyTo); err == nil {
		return true
	} else {
		s.log.Warn("telegram: rich send failed", "err", err)
	}

	answer := strings.TrimSpace(text)
	if answer != "" && answer != md {
		if _, err := sendRichMessage(s.bot, s.chatID, answer, replyTo); err == nil {
			s.log.Warn("telegram: sent answer without tool blocks after rich failure")
			return true
		} else {
			s.log.Warn("telegram: answer-only rich retry failed", "err", err)
		}
	}
	return false
}

// flushLegacy sends the final message using Telegram legacy Markdown, editing the
// live streaming message in place when one exists.
func (s *Sender) flushLegacy(text string, liveID, replyTo int) {
	if strings.TrimSpace(text) == "" {
		// Nothing from the LLM — remove the streaming placeholder if one was sent.
		if liveID != 0 {
			del := tgbotapi.NewDeleteMessage(s.chatID, liveID)
			if _, err := s.bot.Request(del); err != nil {
				s.log.Debug("telegram: delete empty stream message", "err", err)
			}
		}
		return
	}

	converted := mdToTelegram(text)
	chunks := splitMessage(converted, 4096)

	for i, chunk := range chunks {
		if i == 0 && liveID != 0 {
			edit := tgbotapi.NewEditMessageText(s.chatID, liveID, chunk)
			edit.ParseMode = tgbotapi.ModeMarkdown
			if _, err := s.bot.Request(edit); err != nil {
				s.log.Debug("telegram: flush edit failed, retrying plain", "err", err)
				plain := tgbotapi.NewEditMessageText(s.chatID, liveID, stripMarkdown(chunk))
				if _, err2 := s.bot.Request(plain); err2 != nil {
					s.log.Warn("telegram: flush edit plain", "err", err2)
				}
			}
		} else {
			_ = s.sendFormatted(chunk, replyTo)
			replyTo = 0
		}
	}
}

// streamUpdate sends or edits the live message.
// llmText is accumulated LLM output; toolName (non-empty) is the tool currently running.
func (s *Sender) streamUpdate(llmText, toolName string) {
	s.mu.Lock()
	liveID := s.liveID
	replyTo := s.replyTo
	s.mu.Unlock()

	display := buildStreamPreview(llmText, toolName)
	if display == "" {
		return
	}

	if liveID == 0 {
		msg := tgbotapi.NewMessage(s.chatID, display)
		if replyTo != 0 {
			msg.ReplyToMessageID = replyTo
		}
		sent, err := s.bot.Send(msg)
		if err != nil {
			s.log.Debug("telegram: stream initial send", "err", err)
			return
		}
		s.mu.Lock()
		s.liveID = sent.MessageID
		s.replyTo = 0
		s.mu.Unlock()
		return
	}

	edit := tgbotapi.NewEditMessageText(s.chatID, liveID, display)
	if _, err := s.bot.Request(edit); err != nil {
		s.log.Debug("telegram: stream edit", "err", err)
	}
}

// buildStreamPreview builds the text shown in the live streaming message.
// While a tool is running, it appends "⚙️ toolName…" below the accumulated LLM text.
// While the LLM is writing, it appends "…" to signal the response is not finished.
func buildStreamPreview(llmText, toolName string) string {
	if toolName != "" {
		indicator := "⚙️ " + toolName + "…"
		if llmText == "" {
			return indicator
		}
		return truncate(llmText, 3800) + "\n\n" + indicator
	}
	if llmText == "" {
		return ""
	}
	return truncate(llmText, 4000) + "…"
}

// sendTyping refreshes the "typing…" indicator in the chat header.
func (s *Sender) sendTyping() {
	if _, err := s.bot.Request(tgbotapi.NewChatAction(s.chatID, tgbotapi.ChatTyping)); err != nil {
		s.log.Debug("telegram: typing action", "err", err)
	}
}

// sendFormatted sends text with Telegram Markdown, falling back to plain text on parse errors.
func (s *Sender) sendFormatted(text string, replyTo int) error {
	for _, chunk := range splitMessage(text, 4096) {
		msg := tgbotapi.NewMessage(s.chatID, chunk)
		msg.ParseMode = tgbotapi.ModeMarkdown
		if replyTo != 0 {
			msg.ReplyToMessageID = replyTo
			replyTo = 0
		}
		if _, err := s.bot.Send(msg); err != nil {
			s.log.Debug("telegram: send formatted failed, retrying plain", "err", err)
			msg.ParseMode = ""
			msg.Text = stripMarkdown(chunk)
			if _, err2 := s.bot.Send(msg); err2 != nil {
				s.log.Warn("telegram: send plain", "err", err2)
				return err2
			}
		}
	}
	return nil
}

// stripMarkdown removes Telegram markdown characters so text is safe without ParseMode.
func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "`", "'")
	return s
}

// truncate returns the first n bytes of s on a UTF-8 boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

func splitMessage(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var parts []string
	for len(text) > limit {
		cut := limit
		for i := limit - 1; i > limit/2; i-- {
			if text[i] == '\n' {
				cut = i + 1
				break
			}
		}
		parts = append(parts, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}
