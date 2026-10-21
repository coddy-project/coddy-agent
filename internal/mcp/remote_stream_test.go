package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// streamEndServer is a streamable HTTP server that answers initialize and
// tools/list with JSON and every tools/call with an event stream that ends
// without the response: closed cleanly, or broken in the middle of an event.
func streamEndServer(t *testing.T, broken bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &req) != nil || req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "ends", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "shot", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			if !broken {
				_, _ = fmt.Fprint(w, ": the server goes away without an answer\n\n")
				return
			}
			_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(msg)
	}))
}

// An event stream that answers a call ends with the call: this transport
// does not resume a stream, so one that closed or broke before the response
// fails the call at once instead of leaving it to wait for the turn to be
// cancelled.
func TestACallWhoseEventStreamEndsWithoutTheResponseFails(t *testing.T) {
	for name, broken := range map[string]bool{"closed": false, "broken": true} {
		t.Run(name, func(t *testing.T) {
			srv := streamEndServer(t, broken)
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client, err := NewHTTPClient(ctx, "ends", srv.URL, nil, slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			started := time.Now()
			_, err = client.CallToolResult(ctx, "shot", `{}`)
			if err == nil || !strings.Contains(err.Error(), "response stream") {
				t.Fatalf("err = %v, want the call to fail on its response stream", err)
			}
			if took := time.Since(started); took > 5*time.Second {
				t.Fatalf("the call failed after %s, waiting on the stream", took)
			}
		})
	}
}
