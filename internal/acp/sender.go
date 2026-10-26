package acp

import "context"

// UpdateSender is the interface implemented by the ACP server adapter that bridges
// the ReAct agent to protocol notifications and permission dialogs.
type UpdateSender interface {
	// SendSessionUpdate sends a session/update notification.
	SendSessionUpdate(sessionID string, update interface{}) error

	// RequestPermission sends a permission request and waits for the user's response.
	RequestPermission(ctx context.Context, params PermissionRequestParams) (*PermissionResult, error)

	// RequestQuestion asks the question tool's questions and waits for
	// structured answers: through elicitation/create when the client renders
	// form elicitation, else through session/request_question.
	RequestQuestion(ctx context.Context, params QuestionRequestParams) (*QuestionResult, error)
}
