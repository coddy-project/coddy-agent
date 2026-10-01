package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// parseGoalCommand recognizes an operator's /goal command at the start of a prompt.
func parseGoalCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "/goal" {
		return "", true
	}
	if strings.HasPrefix(text, "/goal ") || strings.HasPrefix(text, "/goal\n") || strings.HasPrefix(text, "/goal\t") {
		return strings.TrimSpace(text[len("/goal"):]), true
	}
	return "", false
}

func (a *Agent) runGoalCommand(_ context.Context, arg, rawCommand string) (string, error) {
	store, ok := a.state.(interface {
		GetGoal() session.GoalState
		SetGoal(session.GoalState)
	})
	if !ok {
		return string(acp.StopReasonRefused), fmt.Errorf("session does not support goals")
	}
	a.addUserCommandMessage(rawCommand)
	var reply string
	switch arg {
	case "clear":
		store.SetGoal(session.GoalState{})
		reply = "Goal cleared."
	case "":
		goal := store.GetGoal()
		if goal.Text == "" {
			reply = "No goal is set. Use /goal <text> to set one."
		} else {
			reply = fmt.Sprintf("Goal (%s): %s", goal.Status, goal.Text)
			if goal.Remaining != "" {
				reply += "\nRemaining: " + goal.Remaining
			}
		}
	default:
		store.SetGoal(session.GoalState{Text: arg, Status: session.GoalActive})
		reply = "Goal set: " + arg
	}
	if a.server != nil {
		_ = a.server.SendSessionUpdate(a.state.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: reply},
		})
	}
	a.state.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   reply,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return string(acp.StopReasonEndTurn), nil
}
