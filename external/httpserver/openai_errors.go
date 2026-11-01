//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// openAIErrorWriter normalizes errors from the mux, authentication and handlers
// without buffering successful responses or interfering with SSE flushing.
type openAIErrorWriter struct {
	http.ResponseWriter
	server  *Server
	status  int
	written bool
}

func (w *openAIErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *openAIErrorWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status < 400 {
		w.ResponseWriter.WriteHeader(status)
		w.written = true
	}
}
func (w *openAIErrorWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status < 400 || w.written {
		return w.ResponseWriter.Write(p)
	}
	n := len(p)
	var body struct {
		Error map[string]any `json:"error"`
	}
	_ = json.Unmarshal(p, &body)
	e := body.Error
	if e == nil {
		e = map[string]any{"message": strings.TrimSpace(string(p))}
	}
	if _, ok := e["message"]; !ok {
		e["message"] = http.StatusText(w.status)
	}
	if w.status >= 500 {
		w.server.log.Error("OpenAI HTTP error", "status", w.status, "detail", string(p))
		if e["code"] == "stream_truncated" {
			e["message"] = "Upstream stream truncated"
		} else if e["type"] != "upstream_error" {
			// An upstream_error body comes from replyForError, whose message
			// is already a fixed safe phrase - flattening it would lose the
			// only thing the client can act on. Every other 5xx keeps its
			// detail in the log and answers the generic message.
			e["message"] = "Internal server error"
		}
	}
	if _, ok := e["type"]; !ok {
		kind := "invalid_request_error"
		if w.status == http.StatusUnauthorized {
			kind = "authentication_error"
		} else if w.status == http.StatusTooManyRequests {
			kind = "rate_limit_error"
		} else if w.status >= 500 {
			kind = "server_error"
		}
		e["type"] = kind
	}
	if _, ok := e["code"]; !ok {
		e["code"] = nil
	}
	if _, ok := e["param"]; !ok {
		e["param"] = nil
	}
	raw, err := json.Marshal(map[string]any{"error": e})
	if err != nil {
		return 0, err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(w.status)
	w.written = true
	_, err = fmt.Fprintln(w.ResponseWriter, string(raw))
	return n, err
}
func (w *openAIErrorWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= 400 && !w.written {
		_, _ = w.Write([]byte(http.StatusText(w.status)))
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) openAIErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		ow := &openAIErrorWriter{ResponseWriter: w, server: s}
		next.ServeHTTP(ow, r)
		if ow.status >= 400 && !ow.written {
			_, _ = ow.Write([]byte(http.StatusText(ow.status)))
		}
	})
}

func openAIMethodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allow)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeOpenAIValidationError(w http.ResponseWriter, message, param string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"message": message, "type": "invalid_request_error", "code": nil, "param": param,
	}})
}

// attachmentErrorReason maps a prompt hydration failure to a client-safe
// reason: it names the kind of problem without repeating the path, the size or
// any server-side detail (the full error stays in the log). An empty reason
// means the failure is Coddy's own and stays an opaque 500.
func attachmentErrorReason(err error) string {
	switch {
	case errors.Is(err, session.ErrPathTraversal):
		return "the path escapes the workspace"
	case errors.Is(err, session.ErrFolderAttach):
		return "the path is a directory"
	case errors.Is(err, session.ErrNotDecodableText):
		return "the file is not readable text"
	case errors.Is(err, session.ErrLineRange):
		return "the line range is invalid"
	case errors.Is(err, os.ErrNotExist):
		return "file not found"
	case errors.Is(err, os.ErrPermission):
		return "the file cannot be read"
	case strings.Contains(err.Error(), "path is empty"), strings.Contains(err.Error(), "empty path"):
		return "the path is empty"
	case strings.Contains(err.Error(), "file too large"):
		return "the file exceeds the size limit"
	case strings.Contains(err.Error(), "not valid UTF-8"):
		return "the literal is not valid UTF-8"
	case strings.Contains(err.Error(), "invalid attachment kind"):
		return "the kind is invalid"
	case strings.Contains(err.Error(), "invalid attachment source range"):
		return "the source range is invalid"
	case strings.Contains(err.Error(), "needs its body in source.literal"):
		return "the literal body is missing"
	case strings.Contains(err.Error(), "invalid attachment"):
		return "the attachment is invalid"
	default:
		return ""
	}
}

// attachmentErrorJSON is the SSE error frame of a failed attachment read: a
// client-caused failure is invalid_request_error naming the attachments
// field, anything else is a server_error with no param.
func attachmentErrorJSON(message string, clientErr bool) string {
	e := map[string]any{"message": message, "code": nil, "param": nil}
	if clientErr {
		e["type"] = "invalid_request_error"
		e["param"] = "attachments"
	} else {
		e["type"] = "server_error"
	}
	raw, _ := json.Marshal(map[string]any{"error": e})
	return string(raw)
}
