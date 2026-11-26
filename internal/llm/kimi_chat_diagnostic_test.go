package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKimiChatDiagnosticAndNoReplay(t *testing.T) {
	const token = "synthetic-auth-token"
	const device = "12345678-abcd-4321-9876-123456789abc"
	const secret = "synthetic-argument-secret"
	const historySecret = "synthetic-history-secret"
	const args = `{"content":"` + secret + `"`
	for _, tc := range []struct {
		name, trace, request, wantID, mode, region, wantMode, wantRegion string
		kind                                                             KimiSourceKind
		visible, doneOnly                                                bool
	}{
		{name: "before output", trace: "trace-42", request: "fallback-42", wantID: "trace-42", wantMode: "subscription", wantRegion: "global", kind: KimiSourceManaged},
		{name: "after output", request: "req-42", wantID: "req-42", mode: "subscription", region: "china", wantMode: "subscription", wantRegion: "china", kind: KimiSourceCLI, visible: true},
		{name: "reflected token and device", trace: token, request: "prefix-" + device, wantMode: "platform", wantRegion: "global", mode: "platform", visible: true},
		{name: "reflected argument and history", trace: secret, request: historySecret, wantMode: "subscription", wantRegion: "global", visible: true},
		{name: "unsafe trace falls back", trace: "Bearer " + token, request: "req-43", wantID: "req-43", wantMode: "subscription", wantRegion: "global"},
		{name: "absent metadata and DONE", wantMode: "subscription", wantRegion: "global", doneOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Kimi-Credential-Source", "managed") // not local metadata
				w.Header().Set("X-Trace-Id", tc.trace)
				w.Header().Set("X-Request-Id", tc.request)
				if tc.visible {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\",\"reasoning_content\":\"thought\"}}]}\n\n")
				}
				finish := `,"finish_reason":"stop"`
				if tc.doneOnly {
					finish = ""
				}
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":3,\"id\":\"call-from-upstream\",\"function\":{\"arguments\":%q}}]}%s}]}\n\ndata: [DONE]\n\n", args, finish)
			}))
			defer srv.Close()
			transport := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), catalogTicket: &kimiCredentialTicket{token: token, kind: tc.kind, deviceID: device}}
			p := wrapResilient(newKimiChatProvider(transport, ProviderInput{Model: "kimi-test", Mode: tc.mode, Region: tc.region}), ResilientOptions{RetryMax: 2, RetryBase: time.Millisecond})
			var visible bool
			resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: historySecret}}, nil, func(c StreamChunk) {
				visible = visible || chunkHasContent(c)
				if c.ToolCall != nil {
					t.Error("invalid call emitted as executable")
				}
			})
			var diag *StreamedToolCallError
			if !errors.As(err, &diag) {
				t.Fatalf("wanted typed diagnostic, got %v", err)
			}
			wantFinish := "stop"
			if tc.doneOnly {
				wantFinish = "[DONE]"
			}
			if requests.Load() != 1 || visible != tc.visible || isRetryableLLMError(err) || diag.Index != 3 || !diag.IDPresent || diag.NamePresent || diag.ArgumentBytes != len(args) || diag.FinishReason != wantFinish || diag.JSONErrorClass != "syntax" || diag.RequestID != tc.wantID || diag.Model != "kimi-test" || diag.Mode != tc.wantMode || diag.Region != tc.wantRegion || diag.CredentialSource != string(tc.kind) {
				t.Fatalf("unexpected diagnostic: %+v (requests=%d visible=%v)", diag, requests.Load(), visible)
			}
			if tc.visible && (resp == nil || resp.Content != "partial" || resp.Reasoning != "thought" || len(resp.ToolCalls) != 0) {
				t.Fatal("partial text and reasoning not preserved")
			}
			var log bytes.Buffer
			slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})).Debug("llm call finished", "error", err.Error())
			for _, diagnostic := range []string{err.Error(), fmt.Sprintf("%#v", diag), log.String()} {
				for _, private := range []string{token, device, secret, historySecret, "Bearer", "call-from-upstream", "undecodable SSE frame"} {
					if strings.Contains(diagnostic, private) {
						t.Fatalf("diagnostic leaked %q: %s", private, diagnostic)
					}
				}
			}
			for _, rendered := range []string{err.Error(), log.String()} {
				for _, want := range []string{"kimi-test", tc.wantMode, tc.wantRegion, tc.wantID, string(tc.kind), wantFinish} {
					if !strings.Contains(rendered, want) {
						t.Fatalf("missing %q in %s", want, rendered)
					}
				}
				if (tc.wantID == "" && strings.Contains(rendered, "request_id=")) || (tc.kind == "" && strings.Contains(rendered, "credential_source=")) {
					t.Fatalf("unknown metadata not omitted: %s", rendered)
				}
			}
		})
	}
}

func TestKimiChatNonJSONFrameStillUndecodable(t *testing.T) {
	p := kimiChatStreamFixture(t, "data: not JSON\n\n")
	_, err := p.Stream(context.Background(), nil, nil, nil)
	var frame *streamUndecodableError
	var call *StreamedToolCallError
	if !errors.As(err, &frame) || errors.As(err, &call) || !strings.Contains(err.Error(), "undecodable SSE frame: not JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}
