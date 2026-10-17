package llm

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Content block types of an Anthropic answer that the replay of its thinking
// keeps track of.
const (
	anthropicBlockThinking = "thinking"
	anthropicBlockRedacted = "redacted_thinking"
	anthropicBlockText     = "text"
	anthropicBlockToolUse  = "tool_use"
)

// anthropicReplayBlock is one content block of an answer as the replay of its
// thinking needs it: a thinking block with its exact text and signature, a
// redacted block with its data, a text block with its text, a tool_use block
// by the id of its call (the call itself stays in Message.ToolCalls).
type anthropicReplayBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
}

// replayableThinking reports a thinking block the API takes back: a signed
// thinking block, whose text may be empty (display "omitted"), or a redacted
// block. A thinking block cut before its signature arrived cannot be replayed.
func (b anthropicReplayBlock) replayableThinking() bool {
	switch b.Type {
	case anthropicBlockThinking:
		return b.Signature != ""
	case anthropicBlockRedacted:
		return b.Data != ""
	}
	return false
}

// anthropicThinkingCarrier travels in Message.ReasoningSignature when a bare
// signature cannot carry an answer's thinking: several thinking blocks (an
// adaptive model writes a signed progress note before each tool call), a
// block with empty text, a redacted block, or thinking that does not open the
// answer. It keeps the blocks in the order the model wrote them, since each
// thinking block validates only with its own signature and the exact content
// in front of it.
type anthropicThinkingCarrier struct {
	Anthropic bool                   `json:"anthropic"`
	Blocks    []anthropicReplayBlock `json:"blocks"`
}

// anthropicThinkingSignature packs an answer's blocks for
// Message.ReasoningSignature: "" when it has no thinking to replay, the bare
// signature when the answer's only thinking block opens it, signed and with
// text (the shape every answer had before adaptive thinking, stored as
// before), and the carrier otherwise. A block cut before its signature counts
// too: its text is in Response.Reasoning, which a bare signature replays.
func anthropicThinkingSignature(blocks []anthropicReplayBlock) string {
	replayable, thinking := 0, 0
	for _, b := range blocks {
		if b.Type == anthropicBlockThinking || b.Type == anthropicBlockRedacted {
			thinking++
		}
		if b.replayableThinking() {
			replayable++
		}
	}
	if replayable == 0 {
		return ""
	}
	if thinking == 1 && blocks[0].Type == anthropicBlockThinking && blocks[0].Signature != "" && blocks[0].Thinking != "" {
		return blocks[0].Signature
	}
	kept := make([]anthropicReplayBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == anthropicBlockThinking || b.Type == anthropicBlockRedacted {
			if !b.replayableThinking() {
				continue
			}
		}
		kept = append(kept, b)
	}
	raw, err := json.Marshal(anthropicThinkingCarrier{Anthropic: true, Blocks: kept})
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeAnthropicThinking reads Message.ReasoningSignature back: the carrier
// when it holds one, or bare when it is a signature of its own. Another
// provider's carrier (Codex, Devin) is neither: it cannot be replayed here.
func decodeAnthropicThinking(signature string) (carrier *anthropicThinkingCarrier, bare bool) {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return nil, false
	}
	if !strings.HasPrefix(signature, "{") {
		return nil, true
	}
	var c anthropicThinkingCarrier
	if err := json.Unmarshal([]byte(signature), &c); err != nil || !c.Anthropic {
		return nil, false
	}
	return &c, false
}

// anthropicAssistantBlocks rebuilds an assistant message for a request. With
// thinking on, its signed thinking is replayed unchanged: in the order the
// model wrote it when the stored message still matches the carrier, or in
// front of the text and the tool calls otherwise, which is where a single
// block always stood.
func anthropicAssistantBlocks(m Message, thinking bool) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	if thinking {
		carrier, bare := decodeAnthropicThinking(m.ReasoningSignature)
		switch {
		case carrier != nil:
			if ordered, ok := carrier.inOrder(m); ok {
				return ordered
			}
			for _, b := range carrier.Blocks {
				if block, ok := anthropicThinkingParam(b); ok {
					blocks = append(blocks, block)
				}
			}
		case bare && m.Reasoning != "":
			// The text must be the exact unmodified reasoning so the signature
			// validates.
			blocks = append(blocks, anthropic.NewThinkingBlock(m.ReasoningSignature, m.Reasoning))
		}
	}
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, anthropicToolUseParam(tc))
	}
	return blocks
}

// inOrder replays the carrier's blocks in their order, the text and the tool
// calls taken from the message. It gives up (false) when the message no longer
// holds what the carrier recorded: other text, or a call the carrier never saw.
// A recorded call the message dropped (one cut off mid-input) is left out.
func (c *anthropicThinkingCarrier) inOrder(m Message) ([]anthropic.ContentBlockParamUnion, bool) {
	calls := make(map[string]ToolCall, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		calls[tc.ID] = tc
	}
	used := make(map[string]bool, len(m.ToolCalls))
	var text strings.Builder
	var blocks []anthropic.ContentBlockParamUnion
	for _, b := range c.Blocks {
		switch b.Type {
		case anthropicBlockText:
			text.WriteString(b.Text)
			if strings.TrimSpace(b.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			}
		case anthropicBlockToolUse:
			tc, ok := calls[b.ID]
			if !ok || used[b.ID] {
				continue
			}
			used[b.ID] = true
			blocks = append(blocks, anthropicToolUseParam(tc))
		default:
			if block, ok := anthropicThinkingParam(b); ok {
				blocks = append(blocks, block)
			}
		}
	}
	if strings.TrimSpace(text.String()) != strings.TrimSpace(m.Content) || len(used) != len(m.ToolCalls) {
		return nil, false
	}
	return blocks, true
}

// anthropicThinkingParam turns a recorded thinking block into its request form.
func anthropicThinkingParam(b anthropicReplayBlock) (anthropic.ContentBlockParamUnion, bool) {
	switch {
	case !b.replayableThinking():
		return anthropic.ContentBlockParamUnion{}, false
	case b.Type == anthropicBlockRedacted:
		return anthropic.NewRedactedThinkingBlock(b.Data), true
	default:
		return anthropic.NewThinkingBlock(b.Signature, b.Thinking), true
	}
}

// anthropicToolUseParam turns a tool call into its tool_use block.
func anthropicToolUseParam(tc ToolCall) anthropic.ContentBlockParamUnion {
	var inputMap map[string]interface{}
	_ = json.Unmarshal([]byte(tc.InputJSON), &inputMap)
	return anthropic.NewToolUseBlock(tc.ID, inputMap, tc.Name)
}

// anthropicBlockLog records the blocks of a streamed answer by their index,
// which is the block's position in the answer.
type anthropicBlockLog map[int64]*anthropicReplayBlock

// ordered returns the recorded blocks in the order the model wrote them.
func (l anthropicBlockLog) ordered() []anthropicReplayBlock {
	indexes := make([]int64, 0, len(l))
	for i := range l {
		indexes = append(indexes, i)
	}
	sort.Slice(indexes, func(a, b int) bool { return indexes[a] < indexes[b] })
	out := make([]anthropicReplayBlock, 0, len(indexes))
	for _, i := range indexes {
		out = append(out, *l[i])
	}
	return out
}
