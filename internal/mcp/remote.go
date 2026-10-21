// Remote MCP transports: streamable HTTP (2025-03-26 spec) and the legacy
// HTTP+SSE transport (2024-11-05 spec), plus the shared SSE stream parser.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// NewHTTPClient connects over the streamable HTTP transport: JSON-RPC
// messages are POSTed to url and answered either as application/json bodies
// or as text/event-stream chunks. When the endpoint rejects the handshake
// (legacy servers answer POST with 4xx), the client falls back to the
// HTTP+SSE transport at the same URL, mirroring what Cursor and Claude Code
// do for url-only entries.
func NewHTTPClient(ctx context.Context, name, rawURL string, headers map[string]string, log *slog.Logger) (*Client, error) {
	tr := &streamableHTTPTransport{
		url:     rawURL,
		headers: headers,
		hc:      http.DefaultClient,
		msgs:    make(chan []byte, 16),
		closed:  make(chan struct{}),
		stopped: make(chan struct{}),
		lost:    make(chan struct{}),
	}
	tr.life, tr.endLife = context.WithCancel(context.Background())
	client, streamErr := newClientWithTransport(ctx, name, tr, log)
	if streamErr == nil {
		return client, nil
	}
	sseClient, sseErr := NewSSEClient(ctx, name, rawURL, headers, log)
	if sseErr == nil {
		log.Info("mcp streamable http failed; connected via legacy SSE", "server", name, "error", streamErr)
		return sseClient, nil
	}
	return nil, fmt.Errorf("streamable http: %v; sse fallback: %w", streamErr, sseErr)
}

// NewSSEClient connects over the legacy HTTP+SSE transport: a GET stream at
// url announces the POST endpoint in its first event and then carries every
// server->client JSON-RPC message.
func NewSSEClient(ctx context.Context, name, rawURL string, headers map[string]string, log *slog.Logger) (*Client, error) {
	tr, err := newSSETransport(ctx, name, rawURL, headers)
	if err != nil {
		return nil, err
	}
	return newClientWithTransport(ctx, name, tr, log)
}

// ---- streamable HTTP transport ----

type streamableHTTPTransport struct {
	url     string
	headers map[string]string
	hc      *http.Client
	msgs    chan []byte

	mu        sync.Mutex
	sessionID string
	// closing refuses new requests once Close began; inflight counts the
	// requests (and the event streams answering them) still running, which
	// Close waits for before it counts the transport as stopped. life ends
	// with Close and aborts them.
	closing  bool
	inflight sync.WaitGroup
	life     context.Context
	endLife  context.CancelFunc

	closed    chan struct{}
	closeOnce sync.Once
	// stopped is closed once Close has ended the session on the server;
	// lost once the server answered 404 for the session it handed out.
	stopped  chan struct{}
	lost     chan struct{}
	lostOnce sync.Once
}

// httpStatusError reports a non-2xx response to a JSON-RPC POST.
type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.status, strings.TrimSpace(e.body))
}

func (t *streamableHTTPTransport) Send(ctx context.Context, data []byte) error {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		return fmt.Errorf("mcp: streamable http transport closed")
	}
	t.inflight.Add(1)
	sentSession := t.sessionID
	t.mu.Unlock()
	// The request lives until the caller gives up or the transport closes,
	// whichever comes first; an event stream answering it keeps it open
	// (handedOff) until the stream ends.
	reqCtx, cancel := context.WithCancel(ctx)
	stopWatch := context.AfterFunc(t.life, cancel)
	finish := func() {
		stopWatch()
		cancel()
		t.inflight.Done()
	}
	handedOff := false
	defer func() {
		if !handedOff {
			finish()
		}
	}()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if sentSession != "" {
		req.Header.Set("Mcp-Session-Id", sentSession)
	}

	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound && sentSession != "" {
		// The server does not know the session any more (it restarted, or
		// expired it): the transport asks a client to start over with a new
		// initialize, which is a new connection here.
		t.lostOnce.Do(func() { close(t.lost) })
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		// Notification (or fire-and-forget request) accepted.
		_ = resp.Body.Close()
		return nil
	case resp.StatusCode >= 400:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return &httpStatusError{status: resp.StatusCode, body: string(body)}
	case strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"):
		// Responses stream in as SSE events; read them in the background
		// until the server closes this response stream, the call that
		// opened it gives up or the transport closes.
		handedOff = true
		id := requestID(data)
		go func() {
			defer finish()
			defer func() { _ = resp.Body.Close() }()
			err := readSSE(resp.Body, func(event, data string) {
				t.deliver([]byte(data))
			})
			if id != nil {
				// This transport does not resume a stream, so a request
				// whose stream ended unanswered never will be: it fails now
				// rather than waiting for its caller to give up. Once the
				// response came, the request no longer waits and this
				// answer goes nowhere.
				t.deliver(streamEndedAnswer(id, err))
			}
		}()
		return nil
	default:
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(body)) > 0 {
			t.deliver(body)
		}
		return nil
	}
}

// requestID is the id of the JSON-RPC request data, or nil for a
// notification, which no one waits an answer for.
func requestID(data []byte) json.RawMessage {
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(data, &msg) != nil || len(msg.ID) == 0 || string(msg.ID) == "null" {
		return nil
	}
	return msg.ID
}

// streamEndedAnswer is the JSON-RPC error a request whose response stream
// ended without its response is answered with: broke with err, or closed by
// the server when err is nil.
func streamEndedAnswer(id json.RawMessage, err error) []byte {
	why := "the server closed the response stream without answering"
	if err != nil {
		why = "the response stream broke: " + err.Error()
	}
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32603, "message": why},
	})
	return msg
}

func (t *streamableHTTPTransport) deliver(data []byte) {
	select {
	case t.msgs <- data:
	case <-t.closed:
	}
}

func (t *streamableHTTPTransport) Messages() <-chan []byte { return t.msgs }

// Stopped is closed once Close has told the server the session is over, or
// at Close when there was no session to end.
func (t *streamableHTTPTransport) Stopped() <-chan struct{} { return t.stopped }

// Lost is closed once the server answered 404 for the session it handed out.
func (t *streamableHTTPTransport) Lost() <-chan struct{} { return t.lost }

// streamableSessionEndTimeout bounds the DELETE that ends a streamable HTTP
// session on Close.
const streamableSessionEndTimeout = 5 * time.Second

// Close disconnects: requests still in flight, and the event streams
// answering them, are aborted, and a server that handed out a session id is
// then told the session is over with a DELETE, as the streamable HTTP
// transport asks a client that no longer needs it to do, so a remote server
// frees what it kept for the session. That runs in the background and the
// DELETE's answer is ignored (a server that does not let clients end
// sessions answers 405); Stopped says when it is all over, and a server that
// already lost the session is not asked. Closing twice is a no-op.
func (t *streamableHTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closing = true
		sessionID := t.sessionID
		t.mu.Unlock()
		close(t.closed)
		t.endLife()
		select {
		case <-t.lost:
			sessionID = ""
		default:
		}
		go func() {
			defer close(t.stopped)
			t.inflight.Wait()
			if sessionID != "" {
				t.endSession(sessionID)
			}
		}()
	})
	return nil
}

// endSession sends the DELETE that ends sessionID on the server.
func (t *streamableHTTPTransport) endSession(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), streamableSessionEndTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)
	if err != nil {
		return
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := t.hc.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}

// ---- legacy HTTP+SSE transport ----

type sseTransport struct {
	endpoint string
	headers  map[string]string
	hc       *http.Client
	msgs     chan []byte
	// stream is the event stream's lifetime, ended by Close (cancel); a POST
	// still in flight then is aborted with it.
	stream    context.Context
	cancel    context.CancelFunc
	closed    chan struct{}
	closeOnce sync.Once
}

func newSSETransport(ctx context.Context, name, rawURL string, headers map[string]string) (*sseTransport, error) {
	// The event stream must outlive the connect ctx: it carries every later
	// response, so it gets its own lifetime, cancelled by Close. The connect
	// phase (GET headers + endpoint event), however, must honor the caller's
	// deadline - AfterFunc aborts the stream if ctx dies while connecting, so
	// a server that accepts TCP but never answers cannot hang Connect/probe.
	streamCtx, cancel := context.WithCancel(context.Background())
	stopConnectGuard := context.AfterFunc(ctx, cancel)
	defer stopConnectGuard()

	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcp %s: sse connect: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("mcp %s: sse connect: http %d %s: %s",
			name, resp.StatusCode, resp.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
	}

	t := &sseTransport{
		headers: headers,
		hc:      http.DefaultClient,
		msgs:    make(chan []byte, 16),
		stream:  streamCtx,
		cancel:  cancel,
		closed:  make(chan struct{}),
	}

	endpointCh := make(chan string, 1)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		// This goroutine is the sole msgs writer: closing the channel when
		// the stream dies fails pending calls fast instead of letting them
		// hang until their ctx expires.
		defer close(t.msgs)
		_ = readSSE(resp.Body, func(event, data string) {
			if event == "endpoint" {
				select {
				case endpointCh <- data:
				default:
				}
				return
			}
			// Default (and explicit "message") events carry JSON-RPC payloads.
			select {
			case t.msgs <- []byte(data):
			case <-t.closed:
			}
		})
	}()

	select {
	case raw := <-endpointCh:
		endpoint, err := resolveSSEEndpoint(rawURL, raw)
		if err != nil {
			_ = t.Close()
			return nil, fmt.Errorf("mcp %s: sse endpoint: %w", name, err)
		}
		t.endpoint = endpoint
	case <-ctx.Done():
		_ = t.Close()
		return nil, fmt.Errorf("mcp %s: sse endpoint event not received: %w", name, ctx.Err())
	}
	return t, nil
}

// resolveSSEEndpoint resolves the endpoint event payload (usually a relative
// URI) against the SSE stream URL.
func resolveSSEEndpoint(base, endpoint string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	e, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	return b.ResolveReference(e).String(), nil
}

func (t *sseTransport) Send(ctx context.Context, data []byte) error {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(t.stream, cancel)()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpStatusError{status: resp.StatusCode, body: string(body)}
	}
	// Responses arrive over the event stream; any 2xx body is ignored.
	return nil
}

func (t *sseTransport) Messages() <-chan []byte { return t.msgs }

// Close ends the event stream. Closing twice is a no-op.
func (t *sseTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.cancel()
	})
	return nil
}

// ---- SSE parsing ----

// sseMaxLine bounds one line of an event stream. A server sends a JSON-RPC
// message as one data line, and a tool result may carry 20 MiB of pictures
// (maxToolImagesBytes), about 27 MiB once base64 encoded, with its text
// beside them.
const sseMaxLine = 32 << 20

// readSSE parses a text/event-stream, invoking emit once per event with the
// event name ("" for unnamed = "message" events) and the joined data payload.
func readSSE(r io.Reader, emit func(event, data string)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), sseMaxLine)
	event := ""
	var dataLines []string
	flush := func() {
		if len(dataLines) > 0 {
			emit(event, strings.Join(dataLines, "\n"))
		}
		event = ""
		dataLines = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return fmt.Errorf("a line of the event stream is longer than %d MiB", sseMaxLine>>20)
	}
	return scanner.Err()
}
