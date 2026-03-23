package acp

import "context"

// UpdateSender is the interface that both the ACP server (for editor integration)
// and the TUI dispatcher implement. The react agent uses this interface to send
// streaming updates and request user permissions.
type UpdateSender interface {
	// SendSessionUpdate sends a session/update notification.
	SendSessionUpdate(sessionID string, update interface{}) error

	// RequestPermission sends a permission request and waits for the user's response.
	RequestPermission(ctx context.Context, params PermissionRequestParams) (*PermissionResult, error)
}
