//go:build browser

package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
)

const testKey = "0123456789abcdef0123456789abcdef"

func keyOf(k string) func() string { return func() string { return k } }

func listener(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	srv := httptest.NewServer(NewServer(hub, keyOf(testKey), nil))
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
	for _, origin := range []string{"https://evil.example", "chrome-extension://abc/path", "moz-extension://abc/path", "ftp-extension://abc"} {
		if _, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, Origin: origin}); err == nil {
			t.Fatalf("origin %q opened the socket", origin)
		}
	}
	// Firefox's extension pages have origins of their own scheme.
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-firefox-0001", Origin: "moz-extension://0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"})
	if err != nil {
		t.Fatalf("a Firefox extension was refused: %v", err)
	}
	_ = ext.Close()
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
	ticket, cleanup, err := StageFile([]byte("payload"), true, 2*time.Minute)
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
	up, cleanupUp, _ := StageFile(nil, false, 2*time.Minute)
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

func TestClosingTheSocketsAndAnEmptyKey(t *testing.T) {
	hub := NewHub()
	key := testKey
	srv := NewServer(hub, func() string { return key }, nil)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ext, err := browsertest.Connect(ts.URL, browsertest.Options{Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Close() }()
	// What the HTTP server does when it goes away, or when the key changes.
	srv.CloseSockets()
	select {
	case <-ext.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("a socket outlived CloseSockets")
	}
	// browser.enable off: the endpoint is not there.
	key = ""
	resp, err := http.Get(ts.URL + pathConnect)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the endpoint answered %d with no key", resp.StatusCode)
	}
}

func TestOpeningThePanelGivesATicketOfThisBrowser(t *testing.T) {
	hub := NewHub()
	srv := NewServer(hub, keyOf(testKey), nil)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ext, err := browsertest.Connect(ts.URL, browsertest.Options{Key: testKey, ID: "browser-panel-0001"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ext.Close() }()
	raw, err := ext.Request("panel/open", map[string]any{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	grant, ok := RedeemPanelTicket(got.Ticket, time.Now())
	if !ok || grant.BrowserID != "browser-panel-0001" || grant.KeyDigest != KeyDigest(testKey) {
		t.Fatalf("ticket %+v %v", grant, ok)
	}
	if _, err := ext.Request("chat/send", map[string]any{}, 5*time.Second); err == nil {
		t.Fatal("an unknown method was answered")
	}
}

// A transfer ticket lives as long as the action it belongs to: a download is
// posted only after the person answered and the bytes arrived.
func TestATransferTicketLastsAsLongAsItsAction(t *testing.T) {
	hub, srv := listener(t)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-ticket-life"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ext.Close() })
	if err := ext.Share(browsertest.Tab{ID: 1, URL: "https://example.com/", Title: "Example"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { s := hub.List(""); return len(s) == 1 && len(s[0].Tabs) == 1 })
	var left time.Duration
	ext.Handle = func(c browsertest.Command) browsertest.Answer {
		var p struct {
			TransferID string `json:"transfer_id"`
		}
		_ = json.Unmarshal(c.Params, &p)
		if v, ok := transfers.Load(p.TransferID); ok {
			left = time.Until(v.(*transfer).expires)
		}
		return browsertest.Answer{Error: "stop here"}
	}
	_, _, _ = hub.Transfer(t.Context(), Request{SessionID: "sess_1", BrowserID: "browser-ticket-life", Action: "download_file", TabID: 1}, "f.bin", nil)
	if left < actionTimeout("download_file") {
		t.Fatalf("the ticket lives %v, the download may take %v", left, actionTimeout("download_file"))
	}
}

// A panel ticket is bound to the key the extension proved, whatever the
// configuration holds when the panel asks.
func TestAPanelTicketIsBoundToTheKeyTheExtensionProved(t *testing.T) {
	key := testKey
	hub := NewHub()
	srv := httptest.NewServer(NewServer(hub, func() string { return key }, nil))
	t.Cleanup(srv.Close)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-panel-key1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ext.Close() })
	key = strings.Repeat("b", 32)
	raw, err := ext.Request("panel/open", map[string]any{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(raw, &got)
	grant, ok := RedeemPanelTicket(got.Ticket, time.Now())
	if !ok || grant.KeyDigest != KeyDigest(testKey) {
		t.Fatalf("the ticket is bound to %q, the proved key is %q", grant.KeyDigest, KeyDigest(testKey))
	}
}

// Sockets that never prove the key do not keep the extension out.
func TestSocketsThatNeverAuthenticateDoNotKeepTheExtensionOut(t *testing.T) {
	_, srv := listener(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/browser/v1/connect"
	for i := 0; i < 80; i++ {
		cfg, err := websocket.NewConfig(wsURL, "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
		if err != nil {
			t.Fatal(err)
		}
		ws, err := websocket.DialConfig(cfg)
		if err != nil {
			t.Fatalf("socket %d: %v", i, err)
		}
		t.Cleanup(func() { _ = ws.Close() })
	}
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-not-locked"})
	if err != nil {
		t.Fatalf("the extension was kept out: %v", err)
	}
	_ = ext.Close()
}
