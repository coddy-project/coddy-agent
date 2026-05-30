//go:build http

package httpserver

import (
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type permissionWaitKey struct {
	sessionID  string
	toolCallID string
}

var (
	permissionWaitsMu sync.Mutex
	permissionWaits   = make(map[permissionWaitKey]chan *acp.PermissionResult)
)

func registerPermissionWait(sessionID, toolCallID, sessionDir string) <-chan *acp.PermissionResult {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	k := permissionWaitKey{sessionID: sessionID, toolCallID: toolCallID}
	ch := make(chan *acp.PermissionResult, 1)
	permissionWaits[k] = ch
	_ = sessionDir
	return ch
}

func unregisterPermissionWait(sessionID, toolCallID, sessionDir string) {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	delete(permissionWaits, permissionWaitKey{sessionID: sessionID, toolCallID: toolCallID})
	_ = sessionDir
}

// CompletePermissionAnswer resolves a pending HTTP/streaming permission prompt.
func CompletePermissionAnswer(sessionID, toolCallID string, res *acp.PermissionResult) bool {
	permissionWaitsMu.Lock()
	defer permissionWaitsMu.Unlock()
	k := permissionWaitKey{sessionID: sessionID, toolCallID: toolCallID}
	ch, ok := permissionWaits[k]
	if !ok {
		return false
	}
	delete(permissionWaits, k)
	ch <- res
	return true
}
