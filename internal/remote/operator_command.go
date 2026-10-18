package remote

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// operatorCommandsMetadataKey declares on POST /v1/responses that this client
// shows operator commands and answers them (external/httpserver).
const operatorCommandsMetadataKey = "coddy_operator_commands"

// operatorAnswerTimeout bounds one answer's request: a proxy that holds the
// Run's answer must not hold the operator's Stop behind it.
var operatorAnswerTimeout = 10 * time.Second

// operatorDeclineRetry is the pause before a decline the server did not take
// is sent again.
var operatorDeclineRetry = 500 * time.Millisecond

// onOperatorCommand shows the server's operator command to the local operator
// and posts the answers back, on a goroutine of its own: the stream keeps
// being read, since the run's output arrives on it. The command runs on the
// server, where the session's files are; this side only says run, decline
// and stop. The local request ends when the server's tool call does.
func (t *turnStream) onOperatorCommand(data string) {
	var req acp.OperatorCommandRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil || req.RequestID == "" {
		return
	}
	// The request is of this console's session, whatever the payload says:
	// escape stops it only in the session it belongs to.
	req.SessionID = t.sessionID
	path := "/coddy/sessions/" + url.PathEscape(t.sessionID) + "/operator-command"
	post := func(ctx context.Context, action string) error {
		ctx, cancel := context.WithTimeout(ctx, operatorAnswerTimeout)
		defer cancel()
		return t.h.postJSON(ctx, path, map[string]string{"requestId": req.RequestID, "action": action}, nil)
	}
	answer := func(ctx context.Context, action string) bool {
		err := post(ctx, action)
		// A decline the server never got leaves the turn waiting with no
		// prompt left here to answer again: it is sent once more, twice at
		// most. A run or a stop is not - a second stop is the kill.
		for try := 0; err != nil && !isStaleAnswer(err) && action == acp.OperatorActionDecline && try < 2 && ctx.Err() == nil; try++ {
			select {
			case <-time.After(operatorDeclineRetry):
			case <-ctx.Done():
			}
			err = post(ctx, action)
		}
		if err != nil && !isStaleAnswer(err) {
			t.h.log.Warn("remote operator command answer failed", "session", t.sessionID, "requestId", req.RequestID, "action", action, "error", err)
		}
		return err == nil
	}
	local := acp.OperatorCommandsOf(t.sender)
	if local == nil {
		// Nobody here can answer: say so rather than leave the turn waiting.
		go answer(t.ctx, acp.OperatorActionDecline)
		return
	}
	ctx, cancel := context.WithCancel(t.ctx)
	t.asksMu.Lock()
	if t.ops == nil {
		t.ops = map[string]context.CancelFunc{}
	}
	if prev := t.ops[req.ToolCallID]; prev != nil {
		prev()
	}
	t.ops[req.ToolCallID] = cancel
	t.asksMu.Unlock()
	go func() {
		defer cancel()
		d, err := local.RequestOperatorCommand(ctx, req)
		if err != nil || d == nil {
			return
		}
		done := func() {
			if d.Done != nil {
				d.Done()
			}
		}
		took := answer(ctx, d.Action)
		if d.Action != acp.OperatorActionRun {
			if !took {
				// The server did not take the answer: the request may
				// still be waiting there, so the local one stays until the
				// server's call reaches its final status (or the turn ends).
				<-ctx.Done()
			}
			done()
			return
		}
		// A Run whose answer was lost may still have started the command on
		// the server, so the operator's Stop is forwarded either way: a stop
		// for a request the server never ran is refused there, nothing more.
		stop, kill := d.Stop, d.Kill
		for {
			select {
			case <-stop:
				stop = nil
				answer(ctx, acp.OperatorActionStop)
			case <-kill:
				kill = nil
				answer(ctx, acp.OperatorActionStop)
			case <-ctx.Done():
				done()
				return
			}
		}
	}()
}
