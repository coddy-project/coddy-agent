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

// NoticeSender is implemented by a sender whose client said it renders the
// standard notice update (clientCapabilities.session.notices). A surface that
// does not implement it, or answers false, receives the notice as the notice
// field of the Coddy update it already reads - one line either way, never both.
type NoticeSender interface {
	SupportsNotices() bool
}
