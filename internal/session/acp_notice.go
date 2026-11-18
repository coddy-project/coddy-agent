package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// sendsNotices reports whether the sender's client said it renders the standard
// notice update. A surface that does not implement acp.NoticeSender - the HTTP
// relay, a remote console - keeps reading the notice field of the Coddy update
// it already draws.
func sendsNotices(sender acp.UpdateSender) bool {
	n, ok := sender.(acp.NoticeSender)
	return ok && n.SupportsNotices()
}

// sendNotice puts one live line in front of the person, and reports whether it
// went out as the standard notice.
//
// ACP gives a client that advertises session.notices a line that is not part of
// the transcript: a settings change the agent made, a goal that paused. Coddy's
// own updates carry the same line in a notice field, so a client that renders
// both would show it twice. The notice therefore goes one way only - as the
// standard update for a client that asked for it, and as the field of the Coddy
// update for everyone else - and the caller empties that field when this returns
// true.
func sendNotice(sender acp.UpdateSender, sessionID, severity, title, description string) bool {
	if sender == nil || title == "" || !sendsNotices(sender) {
		return false
	}
	_ = sender.SendSessionUpdate(sessionID, acp.NoticeUpdate{
		SessionUpdate: acp.UpdateTypeNotice,
		Severity:      severity,
		Title:         title,
		Description:   description,
	})
	return true
}
