// Package acp implements the Agent Client Protocol server layer.
// It handles JSON-RPC 2.0 communication over stdio and dispatches
// protocol methods to the session manager.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
)

// Handler is called by the server to handle ACP methods.
// It must be implemented by the session manager layer.
type Handler interface {
	HandleInitialize(ctx context.Context, params InitializeParams) (*InitializeResult, error)
	HandleSessionNew(ctx context.Context, params SessionNewParams) (*SessionNewResult, error)
	HandleSessionLoad(ctx context.Context, params SessionLoadParams) error
	HandleSessionPrompt(ctx context.Context, params SessionPromptParams) (*SessionPromptResult, error)
	HandleSessionSetMode(ctx context.Context, params SessionSetModeParams) error
	HandleSessionCancel(params SessionCancelParams)
}

// Server is the ACP JSON-RPC server. Reads from stdin, writes to stdout.
type Server struct {
	handler Handler
	writer  *json.Encoder
	mu      sync.Mutex // protects writer

	// Pending permission requests waiting for client response.
	pendingPerms   map[interface{}]chan *PermissionResult
	pendingPermsMu sync.Mutex

	nextID atomic.Int64
	log    *slog.Logger
}

// NewServer creates a new ACP server.
func NewServer(handler Handler, log *slog.Logger) *Server {
	return &Server{
		handler:      handler,
		writer:       json.NewEncoder(os.Stdout),
		pendingPerms: make(map[interface{}]chan *PermissionResult),
		log:          log,
	}
}

// Run starts the server loop, reading messages from r until EOF or context cancellation.
func (s *Server) Run(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if err := s.processLine(ctx, line); err != nil {
			s.log.Error("failed to process line", "error", err)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return scanner.Err()
}

// processLine decodes a single JSON-RPC message and dispatches it.
func (s *Server) processLine(ctx context.Context, data []byte) error {
	// Peek at the raw JSON to determine if it's a request or a response
	// (responses have no "method" field, but have "result" or "error").
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return s.sendError(nil, ErrParseError, "parse error", nil)
	}

	_, hasMethod := raw["method"]
	_, hasResult := raw["result"]
	_, hasError := raw["error"]

	if hasResult || hasError {
		// This is a response to a request we sent (e.g. permission request).
		return s.handleResponse(raw)
	}

	if !hasMethod {
		return s.sendError(nil, ErrInvalidRequest, "missing method", nil)
	}

	// Parse as a request or notification.
	var idRaw json.RawMessage
	var hasID bool
	if rawID, ok := raw["id"]; ok {
		idRaw = rawID
		hasID = true
	}

	var method string
	if err := json.Unmarshal(raw["method"], &method); err != nil {
		return s.sendError(nil, ErrInvalidRequest, "invalid method", nil)
	}

	var paramsRaw json.RawMessage
	if p, ok := raw["params"]; ok {
		paramsRaw = p
	}

	if !hasID {
		// Notification - no response expected.
		s.handleNotification(ctx, method, paramsRaw)
		return nil
	}

	// Parse the ID (can be number or string).
	var id interface{}
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return s.sendError(nil, ErrInvalidRequest, "invalid id", nil)
	}

	// Handle method in a goroutine so we can handle cancellations.
	go func() {
		result, rpcErr := s.dispatch(ctx, method, paramsRaw)
		if rpcErr != nil {
			if err := s.sendError(id, rpcErr.Code, rpcErr.Message, rpcErr.Data); err != nil {
				s.log.Error("failed to send error response", "error", err)
			}
			return
		}
		if err := s.sendResult(id, result); err != nil {
			s.log.Error("failed to send result", "error", err)
		}
	}()

	return nil
}

// handleResponse processes a response to a request we sent (e.g. permission).
func (s *Server) handleResponse(raw map[string]json.RawMessage) error {
	var id interface{}
	if rawID, ok := raw["id"]; ok {
		if err := json.Unmarshal(rawID, &id); err != nil {
			return nil
		}
	}

	s.pendingPermsMu.Lock()
	ch, ok := s.pendingPerms[id]
	s.pendingPermsMu.Unlock()

	if !ok {
		s.log.Warn("received unexpected response", "id", id)
		return nil
	}

	if errRaw, ok := raw["error"]; ok {
		var rpcErr RPCError
		if err := json.Unmarshal(errRaw, &rpcErr); err == nil {
			if rpcErr.Message == "cancelled" {
				ch <- &PermissionResult{Outcome: "cancelled"}
				return nil
			}
		}
		ch <- &PermissionResult{Outcome: "cancelled"}
		return nil
	}

	if resultRaw, ok := raw["result"]; ok {
		var result PermissionResult
		if err := json.Unmarshal(resultRaw, &result); err != nil {
			ch <- &PermissionResult{Outcome: "cancelled"}
			return nil
		}
		ch <- &result
	}

	return nil
}

// handleNotification processes notifications (messages without an ID).
func (s *Server) handleNotification(ctx context.Context, method string, params json.RawMessage) {
	switch method {
	case "session/cancel":
		var p SessionCancelParams
		if err := json.Unmarshal(params, &p); err != nil {
			s.log.Error("invalid session/cancel params", "error", err)
			return
		}
		s.handler.HandleSessionCancel(p)
	default:
		s.log.Warn("unknown notification", "method", method)
	}
}

// dispatch routes a request to the appropriate handler.
func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, *RPCError) {
	switch method {
	case "initialize":
		var p InitializeParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: err.Error()}
		}
		result, err := s.handler.HandleInitialize(ctx, p)
		if err != nil {
			return nil, &RPCError{Code: ErrInternalError, Message: err.Error()}
		}
		return result, nil

	case "session/new":
		var p SessionNewParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: err.Error()}
		}
		result, err := s.handler.HandleSessionNew(ctx, p)
		if err != nil {
			return nil, &RPCError{Code: ErrInternalError, Message: err.Error()}
		}
		return result, nil

	case "session/load":
		var p SessionLoadParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: err.Error()}
		}
		if err := s.handler.HandleSessionLoad(ctx, p); err != nil {
			return nil, &RPCError{Code: ErrInternalError, Message: err.Error()}
		}
		return nil, nil

	case "session/prompt":
		var p SessionPromptParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: err.Error()}
		}
		result, err := s.handler.HandleSessionPrompt(ctx, p)
		if err != nil {
			return nil, &RPCError{Code: ErrInternalError, Message: err.Error()}
		}
		return result, nil

	case "session/set_mode":
		var p SessionSetModeParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: err.Error()}
		}
		if err := s.handler.HandleSessionSetMode(ctx, p); err != nil {
			return nil, &RPCError{Code: ErrInternalError, Message: err.Error()}
		}
		return nil, nil

	default:
		return nil, &RPCError{Code: ErrMethodNotFound, Message: fmt.Sprintf("method not found: %s", method)}
	}
}

// SendNotification sends a notification to the client (no response expected).
func (s *Server) SendNotification(method string, params interface{}) error {
	msg := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return s.writeJSON(msg)
}

// SendSessionUpdate sends a session/update notification.
func (s *Server) SendSessionUpdate(sessionID string, update interface{}) error {
	return s.SendNotification("session/update", map[string]interface{}{
		"sessionId": sessionID,
		"update":    update,
	})
}

// RequestPermission sends a session/request_permission request and waits for the response.
func (s *Server) RequestPermission(ctx context.Context, params PermissionRequestParams) (*PermissionResult, error) {
	id := s.nextID.Add(1)
	ch := make(chan *PermissionResult, 1)

	s.pendingPermsMu.Lock()
	s.pendingPerms[float64(id)] = ch
	s.pendingPermsMu.Unlock()

	defer func() {
		s.pendingPermsMu.Lock()
		delete(s.pendingPerms, float64(id))
		s.pendingPermsMu.Unlock()
	}()

	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/request_permission",
		"params":  params,
	}
	if err := s.writeJSON(msg); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return &PermissionResult{Outcome: "cancelled"}, nil
	}
}

// CallClientFS calls fs/read_text_file or fs/write_text_file on the client.
func (s *Server) CallClientFS(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan json.RawMessage, 1)

	// We reuse pendingPerms map with a wrapper - simpler than a second map.
	// Store a raw channel instead.
	rawCh := make(chan json.RawMessage, 1)

	s.pendingPermsMu.Lock()
	// Use a unique key combining id and method.
	key := fmt.Sprintf("fs_%d", id)
	// Store as PermissionResult channel - won't be called for FS.
	// Instead we use a parallel raw map.
	s.pendingPerms[key] = nil // placeholder
	s.pendingPermsMu.Unlock()

	// Store raw channel separately.
	rawPerms := s.getRawPermsMap()
	rawPerms.mu.Lock()
	rawPerms.m[key] = rawCh
	rawPerms.mu.Unlock()

	defer func() {
		s.pendingPermsMu.Lock()
		delete(s.pendingPerms, key)
		s.pendingPermsMu.Unlock()
		rawPerms.mu.Lock()
		delete(rawPerms.m, key)
		rawPerms.mu.Unlock()
	}()

	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      key,
		"method":  method,
		"params":  params,
	}
	if err := s.writeJSON(msg); err != nil {
		return nil, err
	}

	_ = ch // suppress unused warning
	select {
	case raw := <-rawCh:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// rawPermsMap holds raw response channels indexed by string key.
type rawPermsMap struct {
	mu sync.Mutex
	m  map[string]chan json.RawMessage
}

var globalRawPerms = &rawPermsMap{m: make(map[string]chan json.RawMessage)}

func (s *Server) getRawPermsMap() *rawPermsMap {
	return globalRawPerms
}

// sendResult writes a JSON-RPC success response.
func (s *Server) sendResult(id interface{}, result interface{}) error {
	msg := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	return s.writeJSON(msg)
}

// sendError writes a JSON-RPC error response.
func (s *Server) sendError(id interface{}, code int, message string, data interface{}) error {
	msg := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	return s.writeJSON(msg)
}

// writeJSON marshals v and writes it as a single line to stdout.
func (s *Server) writeJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writer.Encode(v)
}

// unmarshalParams decodes JSON params into target struct.
func unmarshalParams(data json.RawMessage, target interface{}) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}
