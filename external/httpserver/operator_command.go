//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// operatorCommandsMetadataKey is the metadata key of POST /v1/responses with
// which the web UI and the remote console declare that they show operator
// commands and answer them.
const operatorCommandsMetadataKey = "coddy_operator_commands"

// operatorCommandsFromHTTP reads the declaration: "1", "true" or true.
func operatorCommandsFromHTTP(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	v, ok := m[operatorCommandsMetadataKey]
	if !ok {
		return false
	}
	var b bool
	if json.Unmarshal(v, &b) == nil {
		return b
	}
	var str string
	if json.Unmarshal(v, &str) == nil {
		str = strings.ToLower(strings.TrimSpace(str))
		return str == "1" || str == "true"
	}
	return false
}

type operatorCommandKey struct {
	sessionID string
	requestID string
}

type operatorCommandWait struct {
	ticket  *acp.OperatorCommandTicket
	request acp.OperatorCommandRequest
}

var (
	operatorCommandsMu   sync.Mutex
	operatorCommandWaits = make(map[operatorCommandKey]*operatorCommandWait)
)

// OperatorCommandPending reports whether an operator command of the session
// waits for the operator or runs: what History marks as needing attention.
func OperatorCommandPending(sessionID string) bool {
	operatorCommandsMu.Lock()
	defer operatorCommandsMu.Unlock()
	for k := range operatorCommandWaits {
		if k.sessionID == sessionID {
			return true
		}
	}
	return false
}

func lookupOperatorCommand(sessionID, requestID string) *operatorCommandWait {
	operatorCommandsMu.Lock()
	defer operatorCommandsMu.Unlock()
	return operatorCommandWaits[operatorCommandKey{sessionID: sessionID, requestID: requestID}]
}

// EnableOperatorCommands records whether the client that reads this stream
// answers operator commands.
func (s *Sender) EnableOperatorCommands(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatorCommands = on
}

// The capability defaults to deny: a signature that drifts must fail the
// build, not hide the tool from the web UI.
var (
	_ acp.OperatorCommandSender = (*Sender)(nil)
	_ acp.SenderUnwrapper       = (*mirroredSender)(nil)
)

// OperatorCommandsAvailable implements acp.OperatorCommandSender: a client
// reads this stream and declared it answers operator commands.
func (s *Sender) OperatorCommandsAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interactive && s.w != nil && s.operatorCommands && !s.detached
}

// RequestOperatorCommand emits the request as an operator_command event and
// waits for POST /coddy/sessions/{id}/operator-command. A run keeps the
// request registered, so a stop reaches it, until the tool reports the end.
func (s *Sender) RequestOperatorCommand(ctx context.Context, req acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	if !s.OperatorCommandsAvailable() {
		return nil, fmt.Errorf("operator commands need the web UI or a console on this stream")
	}
	sid := strings.TrimSpace(req.SessionID)
	rid := strings.TrimSpace(req.RequestID)
	if sid == "" || rid == "" {
		return nil, fmt.Errorf("sessionId and requestId are required")
	}
	key := operatorCommandKey{sessionID: sid, requestID: rid}
	tk := acp.NewOperatorCommandTicket()
	operatorCommandsMu.Lock()
	operatorCommandWaits[key] = &operatorCommandWait{ticket: tk, request: req}
	operatorCommandsMu.Unlock()
	s.notifyQuestionPending(sid, true)
	go func() {
		<-tk.Finished()
		operatorCommandsMu.Lock()
		delete(operatorCommandWaits, key)
		operatorCommandsMu.Unlock()
		s.notifyQuestionPending(sid, false)
	}()
	if err := s.writeNamedEventJSON("operator_command", req); err != nil {
		if _, merr := json.Marshal(req); merr != nil {
			// End the ticket of a request no surface was ever shown.
			_ = tk.Answer(acp.OperatorActionDecline, "")
			return nil, err
		}
		// A dead client socket does not end the request: the frame reached
		// the composer relay, and the answer arrives through the route.
	}
	return tk.Wait(ctx)
}

// coddySessionOperatorCommandPost takes the operator's answer to an operator
// command: run, decline or stop for a request id. The body never carries the
// command - an unknown field is refused - because the tool runs the command
// it holds, which is the one the operator was shown.
func (s *Server) coddySessionOperatorCommandPost(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := session.ValidateFolderSessionID(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusBadRequest)
		return
	}
	if hdr := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")); hdr != "" && hdr != id {
		http.Error(w, `{"error":{"message":"X-Coddy-Session-ID does not match path id"}}`, http.StatusBadRequest)
		return
	}
	var body struct {
		RequestID string `json:"requestId"`
		Action    string `json:"action"`
		Note      string `json:"note"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, "invalid body: "+err.Error()), http.StatusBadRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, `{"error":{"message":"invalid body: one JSON object expected"}}`, http.StatusBadRequest)
		return
	}
	rid := strings.TrimSpace(body.RequestID)
	if rid == "" {
		http.Error(w, `{"error":{"message":"requestId is required"}}`, http.StatusBadRequest)
		return
	}
	switch body.Action {
	case acp.OperatorActionRun, acp.OperatorActionDecline, acp.OperatorActionStop:
	default:
		http.Error(w, `{"error":{"message":"action must be run, decline or stop"}}`, http.StatusBadRequest)
		return
	}
	wait := lookupOperatorCommand(id, rid)
	if wait == nil {
		http.Error(w, `{"error":{"message":"no operator command with this requestId"}}`, http.StatusNotFound)
		return
	}
	switch err := wait.ticket.Answer(body.Action, strings.TrimSpace(body.Note)); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, acp.ErrOperatorCommandEnded):
		http.Error(w, `{"error":{"message":"the operator command has ended"}}`, http.StatusNotFound)
	default:
		http.Error(w, fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()), http.StatusConflict)
	}
}
