package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (m *Manager) replayConversation(sessionID string, msgs []llm.Message) error {
	if m.server == nil {
		return nil
	}

	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]
		switch msg.Role {
		case llm.RoleUser:
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				continue
			}
			_ = m.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
				SessionUpdate: "user_message_chunk",
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: content},
			})

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
			for range msg.ToolCalls {
				if i+1 < len(msgs) && msgs[i+1].Role == llm.RoleTool {
					tm := msgs[i+1]
					i++
					display := truncateForReplay(tm.Content)
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
					})
				}
			}

		case llm.RoleTool:
			display := truncateForReplay(msg.Content)
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
			})

		default:
			continue
		}
	}

	return nil
}

func truncateForReplay(s string) string {
	display := strings.TrimRight(s, "\n\r")
	if len(display) > 2000 {
		display = display[:2000] + "\n... (truncated)"
	}
	return display
}

func replayToolKind(name string) string {
	switch {
	case name == "read_file" || name == "list_dir":
		return "read"
	case name == "write_file" || name == "write_text_file" || name == "apply_diff" ||
		name == "mkdir" || name == "rmdir" || name == "touch" || name == "rm" || name == "mv":
		return "write"
	case name == "run_command":
		return "run_command"
	default:
		return "other"
	}
}
