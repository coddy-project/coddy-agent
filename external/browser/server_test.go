//go:build browser

package browser

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
)

const testKey = "0123456789abcdef0123456789abcdef"

func listener(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	srv := httptest.NewServer(NewServer(hub, testKey, nil))
	t.Cleanup(srv.Close)
	return hub, srv
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInitializeRefusesAWrongKeyAndAnotherProtocolVersion(t *testing.T) {
	hub, srv := listener(t)
	var rpcErr *browsertest.RPCError
	if _, err := browsertest.Connect(srv.URL, browsertest.Options{Key: strings.Repeat("x", 32)}); !errors.As(err, &rpcErr) || rpcErr.Code != codeUnauthorized {
		t.Fatalf("a wrong key: %v", err)
	}
	if _, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, Version: 2}); !errors.As(err, &rpcErr) || rpcErr.Code != codeProtocolVersion {
		t.Fatalf("another version: %v", err)
	}
	if len(hub.List("")) != 0 {
		t.Fatal("a refused extension was registered")
	}
}

func TestOnlyAnExtensionOriginReachesTheSocket(t *testing.T) {
	_, srv := listener(t)
	for _, origin := range []string{"https://evil.example", "moz-extension://abc", "chrome-extension://abc/path"} {
		if _, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, Origin: origin}); err == nil {
			t.Fatalf("origin %q opened the socket", origin)
		}
	}
	// A plain request with no origin is not an upgrade the listener accepts.
	resp, err := http.Get(srv.URL + pathConnect)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no origin: %d", resp.StatusCode)
	}
}

func TestTheFirstMessageMustBeInitialize(t *testing.T) {
	_, srv := listener(t)
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+pathConnect, "", "chrome-extension://coddyfortestsonly")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Close() }()
	_ = ws.SetDeadline(time.Now().Add(3 * time.Second))
	if err := websocket.JSON.Send(ws, map[string]any{"jsonrpc": "2.0", "method": "browser/tabs", "params": map[string]any{"tabs": []any{}}}); err != nil {
		t.Fatal(err)
	}
	var answer message
	if err := websocket.JSON.Receive(ws, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Error == nil || answer.Error.Code != codeInvalidRequest {
		t.Fatalf("answer %+v", answer)
	}
	var next message
	if err := websocket.JSON.Receive(ws, &next); err == nil {
		t.Fatalf("the socket stayed open: %+v", next)
	}
}

func TestAnExtensionRequestGetsMethodNotFoundAndTheSocketStays(t *testing.T) {
	hub, srv := listener(t)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Close() }()
	if err := ext.Send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "session/list"}); err != nil {
		t.Fatal(err)
	}
	// An unknown notification is a newer extension's and is ignored.
	if err := ext.Notify("browser/somethingNew", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := ext.Share(browsertest.Tab{ID: 1, URL: "https://example.com/", Title: "Example"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { b := hub.List(""); return len(b) == 1 && len(b[0].Tabs) == 1 })
}

func TestAnInvalidTabListEndsTheConnection(t *testing.T) {
	hub, srv := listener(t)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Close() }()
	if err := ext.Share(browsertest.Tab{ID: 1, URL: "file:///etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ext.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("the connection survived an invalid tab list")
	}
	waitUntil(t, func() bool { return len(hub.List("")) == 0 })
}

func TestStopFromTheExtensionWithdrawsTheTabs(t *testing.T) {
	hub, srv := listener(t)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Close() }()
	_ = ext.Share(browsertest.Tab{ID: 1, URL: "https://example.com/"})
	waitUntil(t, func() bool { b := hub.List(""); return len(b) == 1 && len(b[0].Tabs) == 1 })
	_ = ext.Notify("browser/stop", map[string]any{})
	waitUntil(t, func() bool { return len(hub.List("")[0].Tabs) == 0 })
}

func TestAReconnectOfTheSameBrowserClosesTheOldSocket(t *testing.T) {
	hub, srv := listener(t)
	old, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-same-0001"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	fresh, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-same-0001"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	select {
	case <-old.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("the replaced socket stayed open")
	}
	_ = fresh.Share(browsertest.Tab{ID: 3, URL: "https://example.com/"})
	waitUntil(t, func() bool { b := hub.List(""); return len(b) == 1 && len(b[0].Tabs) == 1 })
}

func TestATransferTicketIsKeyedAndSingleUse(t *testing.T) {
	_, srv := listener(t)
	ticket, cleanup, err := StageFile([]byte("payload"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	get := func(key string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+pathFilesPrefix+ticket, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && string(body) != "payload" {
			t.Fatalf("body %q", body)
		}
		return resp.StatusCode
	}
	if code := get("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong key read the file: %d", code)
	}
	if code := get(testKey); code != http.StatusOK {
		t.Fatalf("first read: %d", code)
	}
	if code := get(testKey); code != http.StatusGone {
		t.Fatalf("second read: %d", code)
	}
	// An upload ticket takes one POST and nothing else.
	up, cleanupUp, _ := StageFile(nil, false)
	defer cleanupUp()
	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+pathFilesPrefix+up, bytes.NewReader([]byte("from the page")))
		req.Header.Set("Authorization", "Bearer "+testKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if _, err := FileResult(up); err == nil {
		t.Fatal("an empty download ticket answered")
	}
	if code := post(); code != http.StatusNoContent {
		t.Fatalf("post: %d", code)
	}
	if got, err := FileResult(up); err != nil || string(got) != "from the page" {
		t.Fatalf("result %q %v", got, err)
	}
	if code := post(); code != http.StatusGone {
		t.Fatalf("second post: %d", code)
	}
}

func TestShutdownClosesTheSockets(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	_ = probe.Close()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- NewServer(hub, testKey, nil).Serve(ctx, address, "", "") }()
	var ext *browsertest.Extension
	waitUntil(t, func() bool {
		ext, err = browsertest.Connect("http://"+address, browsertest.Options{Key: testKey})
		return err == nil
	})
	defer func() { _ = ext.Close() }()
	cancel()
	select {
	case <-ext.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("a socket outlived its listener")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := NewServer(hub, "", nil).Serve(context.Background(), address, "", ""); err == nil {
		t.Fatal("a listener started without a key")
	}
}
