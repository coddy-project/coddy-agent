package llm

import (
	"net/http"
	"testing"
)

// TestIsEventStream pins which answers the stall guard watches: one whose
// Content-Type names an event stream, and a success that names no
// Content-Type when its request asked for an event stream - the Codex backend
// sends its stream with no Content-Type at all. A Content-Type that names
// anything else wins over the request, an error status without one is a
// blocking error body, and an answer with neither is a blocking one.
func TestIsEventStream(t *testing.T) {
	cases := []struct {
		name        string
		accept      string
		status      int
		contentType string
		want        bool
	}{
		{"event stream", "application/json", http.StatusOK, "text/event-stream", true},
		{"event stream with a charset", "", http.StatusOK, "Text/Event-Stream; charset=utf-8", true},
		{"no content type, an event stream asked for", "text/event-stream", http.StatusOK, "", true},
		{"no content type, an event stream among others", "application/json, text/event-stream", http.StatusOK, "", true},
		{"no content type, json asked for", "application/json", http.StatusOK, "", false},
		{"no content type, nothing asked for", "", http.StatusOK, "", false},
		{"json answer to a stream request", "text/event-stream", http.StatusOK, "application/json", false},
		{"error status without a content type", "text/event-stream", http.StatusTooManyRequests, "", false},
		{"server error without a content type", "text/event-stream", http.StatusBadGateway, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "http://backend.test/responses", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			resp := &http.Response{StatusCode: tc.status, Header: http.Header{}}
			if tc.contentType != "" {
				resp.Header.Set("Content-Type", tc.contentType)
			}
			if got := isEventStream(req, resp); got != tc.want {
				t.Errorf("isEventStream(Accept %q, status %d, Content-Type %q) = %v, want %v", tc.accept, tc.status, tc.contentType, got, tc.want)
			}
		})
	}
}
