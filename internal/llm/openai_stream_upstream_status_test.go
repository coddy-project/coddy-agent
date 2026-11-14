package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIStreamUpstreamStatusEnvelope(t *testing.T) {
	for _, status := range []int{400, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := fmt.Fprintf(w, "data: {\"error\":{\"type\":\"upstream_error\",\"message\":\"Upstream provider request failed\",\"upstream_status\":%d}}\n\n", status); err != nil {
					t.Errorf("write upstream error fixture: %v", err)
				}
			}))
			defer srv.Close()
			p := newOpenAIProvider("m", "key", srv.URL, nil, 0, 0, "", "")
			_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
			if err == nil || UpstreamStatus(err) != status {
				t.Fatalf("status = %d, error %v; want %d", UpstreamStatus(err), err, status)
			}
		})
	}
}

func TestOpenAIStreamUpstreamEnvelopeRetryAndTrust(t *testing.T) {
	for _, tc := range []struct {
		payload string
		status  int
		retry   bool
	}{
		{`{"type":"upstream_error","upstream_status":429,"retry_after":1}`, 429, true},
		{`{"type":"other","upstream_status":429,"retry_after":1}`, 0, false},
		{`{"type":"upstream_error","upstream_status":200,"retry_after":1}`, 0, false},
	} {
		err := newStreamServerError([]byte(tc.payload), false)
		if got := UpstreamStatus(err); got != tc.status {
			t.Errorf("%s status=%d", tc.payload, got)
		}
		_, ok := UpstreamRetryAfter(err)
		if ok != tc.retry {
			t.Errorf("%s retry=%v", tc.payload, ok)
		}
	}
}
