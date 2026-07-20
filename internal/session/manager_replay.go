package session

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (m *Manager) replayConversation(sessionID string, msgs []llm.Message, sessionDir string) error {
	if m.server == nil {
		return nil
	}

	byTurn := map[int]*MemoryTurnTraceJSON{}
	if sd := strings.TrimSpace(sessionDir); sd != "" {
		if env, err := ReadMemoryTrace(sd); err == nil && env != nil {
			for i := range env.Turns {
				row := env.Turns[i]
				cp := row
				byTurn[row.UserTurnIndex] = &cp
			}
		}
	}

	userTurn := 0
	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]
		switch msg.Role {
		case llm.RoleUser:
			userTurn++
			content := strings.TrimSpace(msg.Content)
			if content != "" {
				_ = m.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: "user_message_chunk",
					Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: content},
				})
			}
			m.replayMemoryTrace(sessionID, byTurn[userTurn])

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

func (m *Manager) replayMemoryTrace(sessionID string, row *MemoryTurnTraceJSON) {
	if m.server == nil || row == nil {
		return
	}
	rowID := strings.TrimSpace(row.MemoryRowID)
	if rowID == "" {
		rowID = fmt.Sprintf("mem-%d", row.UserTurnIndex)
	}

	unified := row.MemoryDurationMs > 0 || strings.TrimSpace(row.MemoryContextText) != ""
	if unified {
		_ = m.server.SendSessionUpdate(sessionID, acp.MemoryPhaseUpdate{
			SessionUpdate: acp.UpdateTypeMemoryPhase,
			MemoryRowID:   rowID,
			Phase:         "memory",
			Status:        "started",
			UserTurnIndex: row.UserTurnIndex,
		})
		if t := strings.TrimSpace(row.MemoryContextText); t != "" {
			_ = m.server.SendSessionUpdate(sessionID, acp.MemoryMessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeMemoryMessageChunk,
				MemoryRowID:   rowID,
				Phase:         "memory",
				Kind:          "text",
				Delta:         t,
			})
		}
		ph := acp.MemoryPhaseUpdate{
			SessionUpdate: acp.UpdateTypeMemoryPhase,
			MemoryRowID:   rowID,
			Phase:         "memory",
			Status:        "completed",
			UserTurnIndex: row.UserTurnIndex,
			DurationMs:    row.MemoryDurationMs,
		}
		if len(row.RecallReadPaths) > 0 {
			ph.RecallReadPaths = row.RecallReadPaths
		}
		if row.PersistSaved {
			ph.PersistSaved = true
			ph.PersistRelativePath = row.PersistRelativePath
			ph.PersistTitle = row.PersistTitle
			if strings.TrimSpace(row.PersistSavedBody) != "" {
				ph.PersistSavedBody = row.PersistSavedBody
			}
		}
		_ = m.server.SendSessionUpdate(sessionID, ph)
		return
	}

	hasRecall := row.RecallDurationMs > 0 || strings.TrimSpace(row.RecallText) != "" || strings.TrimSpace(row.RecallReasoningText) != "" || len(row.RecallReadPaths) > 0
	hasPersist := row.PersistDurationMs > 0 || strings.TrimSpace(row.PersistFinalText) != "" || row.PersistSaved

	if hasRecall {
		_ = m.server.SendSessionUpdate(sessionID, acp.MemoryPhaseUpdate{
			SessionUpdate: acp.UpdateTypeMemoryPhase,
			MemoryRowID:   rowID,
			Phase:         "recall",
			Status:        "started",
			UserTurnIndex: row.UserTurnIndex,
		})
		if r := strings.TrimSpace(row.RecallReasoningText); r != "" {
			_ = m.server.SendSessionUpdate(sessionID, acp.MemoryMessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeMemoryMessageChunk,
				MemoryRowID:   rowID,
				Phase:         "recall",
				Kind:          "reasoning",
				Delta:         r,
			})
		}
		if r := strings.TrimSpace(row.RecallText); r != "" {
			_ = m.server.SendSessionUpdate(sessionID, acp.MemoryMessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeMemoryMessageChunk,
				MemoryRowID:   rowID,
				Phase:         "recall",
				Kind:          "text",
				Delta:         r,
			})
		}
		rc := acp.MemoryPhaseUpdate{
			SessionUpdate: acp.UpdateTypeMemoryPhase,
			MemoryRowID:   rowID,
			Phase:         "recall",
			Status:        "completed",
			UserTurnIndex: row.UserTurnIndex,
			DurationMs:    row.RecallDurationMs,
		}
		if len(row.RecallReadPaths) > 0 {
			rc.RecallReadPaths = row.RecallReadPaths
		}
		_ = m.server.SendSessionUpdate(sessionID, rc)
	}

	if hasPersist {
		_ = m.server.SendSessionUpdate(sessionID, acp.MemoryPhaseUpdate{
			SessionUpdate: acp.UpdateTypeMemoryPhase,
			MemoryRowID:   rowID,
			Phase:         "persist",
			Status:        "started",
			UserTurnIndex: row.UserTurnIndex,
		})
		if r := strings.TrimSpace(row.PersistFinalText); r != "" {
			_ = m.server.SendSessionUpdate(sessionID, acp.MemoryMessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeMemoryMessageChunk,
				MemoryRowID:   rowID,
				Phase:         "persist",
				Kind:          "text",
				Delta:         r,
			})
		}
		ph := acp.MemoryPhaseUpdate{
			SessionUpdate:       acp.UpdateTypeMemoryPhase,
			MemoryRowID:         rowID,
			Phase:               "persist",
			Status:              "completed",
			UserTurnIndex:       row.UserTurnIndex,
			DurationMs:          row.PersistDurationMs,
			PersistSaved:        row.PersistSaved,
			PersistRelativePath: row.PersistRelativePath,
			PersistTitle:        row.PersistTitle,
		}
		if row.PersistSaved && strings.TrimSpace(row.PersistSavedBody) != "" {
			ph.PersistSavedBody = row.PersistSavedBody
		}
		_ = m.server.SendSessionUpdate(sessionID, ph)
	}
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
