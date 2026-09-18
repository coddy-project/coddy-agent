package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (m *Manager) replayConversation(sessionID string, msgs []llm.Message, sessionDir string) error {
	if m.server == nil {
		return nil
	}

	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]
		switch msg.Role {
		case llm.RoleUser:
			content := strings.TrimSpace(msg.Content)
			if content != "" {
				_ = m.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: "user_message_chunk",
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: content},
				})
			}

		case llm.RoleAssistant:
			if txt := strings.TrimSpace(msg.Content); txt != "" {
				_ = m.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: "agent_message_chunk",
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: txt},
				})
			}
			for _, tc := range msg.ToolCalls {
				_ = m.server.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: acp.UpdateTypeToolCall,
					ToolCallID:    tc.ID,
					Title:         tc.Name,
					Kind:          replayToolKind(tc.Name),
					Status:        "pending",
				})
			}
			for k := range msg.ToolCalls {
				tc := msg.ToolCalls[k]
				if i+1 >= len(msgs) || msgs[i+1].Role != llm.RoleTool {
					break
				}
				tm := msgs[i+1]
				i++
				display, pmeta := PreviewToolResultForSessionUpdate(tc.Name, tm.Content)
				var content []acp.ToolCallResultItem
				if display != "" {
					content = []acp.ToolCallResultItem{
						{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: display}},
					}
				}
				_ = m.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
					SessionUpdate: acp.UpdateTypeToolCallUpdate,
					ToolCallID:    tm.ToolCallID,
					Status:        "completed",
					Content:       content,
					Meta:          pmeta,
				})
			}

		case llm.RoleTool:
			toolName := ""
			if sd := strings.TrimSpace(sessionDir); sd != "" {
				if meta, err := ReadToolCallMeta(sd, msg.ToolCallID); err == nil && meta != nil {
					toolName = meta.Name
				}
			}
			display, pmeta := PreviewToolResultForSessionUpdate(toolName, msg.Content)
			var content []acp.ToolCallResultItem
			if display != "" {
				content = []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: display}},
				}
			}
			_ = m.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    msg.ToolCallID,
				Status:        "completed",
				Content:       content,
				Meta:          pmeta,
			})

		default:
			continue
		}
	}

	return nil
}

func replayToolKind(name string) string {
	switch name {
	case "read", "glob", "grep":
		return "read"
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm", "mv":
		return "write"
	case "run_command":
		return "run_command"
	default:
		return "other"
	}
}
