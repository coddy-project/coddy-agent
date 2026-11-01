//go:build browser

package browser

import (
	"encoding/json"
	"errors"
)

// The wire is JSON-RPC 2.0, one message per WebSocket text frame, in both
// directions: the extension's initialize and its notifications go up, Coddy's
// browser/command requests come down and their responses go back up.
const jsonrpcVersion = "2.0"

// Method names of the protocol (docs/reference/browser-protocol.md).
const (
	methodInitialize = "initialize"
	methodTabs       = "browser/tabs"
	methodStop       = "browser/stop"
	methodCommand    = "browser/command"
	methodTurnEnded  = "browser/turnEnded"
	methodCancel     = "$/cancelRequest"
	methodPing       = "$/ping"
	methodPanelOpen  = "panel/open"
)

// Error codes Coddy answers with. The -32xxx range below -32000 is JSON-RPC's
// own; -32000, -32001 and -32002 are this protocol's.
const (
	codeFailed          = -32000
	codeInvalidRequest  = -32600
	codeMethodNotFound  = -32601
	codeInvalidParams   = -32602
	codeUnauthorized    = -32001
	codeProtocolVersion = -32002
)

// message is any frame of the wire. Which fields are present says what it is:
// a method with an id is a request, a method without one a notification, an
// id with a result or an error a response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

func (m message) isRequest() bool      { return m.Method != "" && len(m.ID) > 0 }
func (m message) isNotification() bool { return m.Method != "" && len(m.ID) == 0 }
func (m message) isResponse() bool {
	return m.Method == "" && len(m.ID) > 0 && (m.Result != nil || m.Error != nil)
}

// stringID reads the id of a response to one of Coddy's requests, which are
// always strings. A number answers nothing Coddy sent.
func (m message) stringID() (string, error) {
	var id string
	if err := json.Unmarshal(m.ID, &id); err != nil || id == "" {
		return "", errors.New("response id is not one of ours")
	}
	return id, nil
}

func request(id, method string, params any) ([]byte, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	idRaw, _ := json.Marshal(id)
	return json.Marshal(message{JSONRPC: jsonrpcVersion, ID: idRaw, Method: method, Params: raw})
}

func notification(method string, params any) ([]byte, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(message{JSONRPC: jsonrpcVersion, Method: method, Params: raw})
}

func result(id json.RawMessage, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(message{JSONRPC: jsonrpcVersion, ID: id, Result: raw})
}

func failure(id json.RawMessage, code int, text string, data any) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	raw, _ := json.Marshal(message{JSONRPC: jsonrpcVersion, ID: id, Error: &rpcError{Code: code, Message: text, Data: data}})
	return raw
}
