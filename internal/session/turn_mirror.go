package session

import "github.com/EvilFreelancer/coddy-agent/internal/acp"

// TurnMirror lets a surface that starts a turn of its own publish that turn
// where other clients can watch it.
//
// A messenger gateway owns the conversation it is running: it renders the
// answer into a chat and it is the only surface a permission prompt can be
// answered from. But the session is shared, so a browser sitting on the same
// session should see the turn happen rather than discover it afterwards. The
// mirror wraps the surface's own sender: session updates fan out to both, and
// everything that needs an answer stays with the surface that has a human.
//
// The HTTP server implements this over the same per-session relay a background
// wake turn publishes into. A process without it uses NopTurnMirror and behaves
// exactly as it did before anything was watching.
type TurnMirror interface {
	// MirrorTurn returns a sender that forwards to primary and to whatever the
	// mirror publishes into, plus the function that releases the mirror. done
	// is always safe to call and must be called exactly once, when the turn
	// ends.
	MirrorTurn(sessionID string, primary acp.UpdateSender) (mirrored acp.UpdateSender, done func())
}

// NopTurnMirror hands the primary sender back untouched. It is what a surface
// gets when nothing in the process is watching.
type NopTurnMirror struct{}

// MirrorTurn implements TurnMirror.
func (NopTurnMirror) MirrorTurn(_ string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	return primary, func() {}
}

// Mirror runs primary through m, tolerating a nil mirror so callers need no
// branch of their own.
func Mirror(m TurnMirror, sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	if m == nil {
		return primary, func() {}
	}
	return m.MirrorTurn(sessionID, primary)
}

// compile-time proof that a nop mirror satisfies the interface.
var _ TurnMirror = NopTurnMirror{}
