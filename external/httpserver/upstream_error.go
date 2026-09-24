//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// errorReply is the HTTP answer to a turn or a completion that failed: the
// status, the OpenAI-shaped error object, and the Retry-After seconds of a
// limit (0 for none).
type errorReply struct {
	status     int
	body       map[string]any
	retryAfter int
}

// replyForError maps a failed turn or completion to its HTTP answer. A
// failure the provider answered with a status keeps it, so a client can tell
// a deterministic rejection from an outage (issue #322): a 4xx goes through
// as it is, a 5xx becomes 502 Bad Gateway, a 504 or a timeout 504 Gateway
// Timeout, and a 429 or a 503 carries Retry-After when the provider named a
// pause; the error names type upstream_error and upstream_status. 500 is
// left for failures of Coddy's own, and 409 for a session that is busy or
// read-only.
func replyForError(err error) errorReply {
	e := map[string]any{"message": err.Error()}
	reply := errorReply{status: http.StatusInternalServerError, body: map[string]any{"error": e}}
	if errors.Is(err, session.ErrSessionTurnBusy) || isSubagentReadOnly(err) {
		reply.status = http.StatusConflict
		return reply
	}
	upstream := llm.UpstreamStatus(err)
	switch {
	case upstream >= 400 && upstream < 500:
		reply.status = upstream
	case upstream == http.StatusGatewayTimeout:
		reply.status = http.StatusGatewayTimeout
	case upstream >= 500:
		reply.status = http.StatusBadGateway
	case errors.Is(err, context.DeadlineExceeded):
		reply.status = http.StatusGatewayTimeout
		e["type"] = "upstream_error"
		return reply
	default:
		return reply
	}
	e["type"] = "upstream_error"
	e["upstream_status"] = upstream
	// The pause a limit or a lane that is down for a while asked for.
	if upstream == http.StatusTooManyRequests || upstream == http.StatusServiceUnavailable {
		if d, ok := llm.UpstreamRetryAfter(err); ok && d > 0 {
			reply.retryAfter = int(math.Ceil(d.Seconds()))
		}
	}
	return reply
}

// writeErrorReply answers a blocking request that failed.
func writeErrorReply(w http.ResponseWriter, err error) {
	reply := replyForError(err)
	raw, _ := json.Marshal(reply.body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if reply.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(reply.retryAfter))
	}
	w.WriteHeader(reply.status)
	_, _ = w.Write(append(raw, '\n'))
}
