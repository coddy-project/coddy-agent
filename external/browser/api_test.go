//go:build browser

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
)

// apiServer is a home server's mux as the HTTP server mounts it: the
// extension's endpoint and the API, over one hub.
func apiServer(t *testing.T, allow bool) (*Hub, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	mux := http.NewServeMux()
	mux.Handle("/browser/", NewServer(hub, keyOf(testKey), nil))
	(&API{Hub: hub, Allow: func() bool { return allow }}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return hub, srv
}

func TestRemoteOwnersKeepToTheirAlphabet(t *testing.T) {
	if owner, err := RemoteOwner(Caller{Node: "laptop.lan", SessionID: "sess_01"}); err != nil || owner != "laptop.lan/sess_01" {
		t.Fatalf("%q %v", owner, err)
	}
	for _, c := range []Caller{
		{Node: "", SessionID: "sess_01"},
		{Node: "a/b", SessionID: "sess_01"},
		{Node: "laptop", SessionID: ""},
		{Node: "laptop", SessionID: "../x"},
		{Node: strings.Repeat("n", 65), SessionID: "s"},
	} {
		if _, err := RemoteOwner(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}

func post(t *testing.T, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func TestTheAPIAnswersOnlyWhenServing(t *testing.T) {
	_, srv := apiServer(t, false)
	res, err := http.Get(srv.URL + "/coddy/browser/v1/browsers")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("browsers without serve_remote: %d", res.StatusCode)
	}
	if res, _ := post(t, srv.URL+"/coddy/browser/v1/call", map[string]any{}); res.StatusCode != http.StatusNotFound {
		t.Fatalf("call without serve_remote: %d", res.StatusCode)
	}
}

func TestACallNeedsACallerAndATransferAFileName(t *testing.T) {
	_, srv := apiServer(t, true)
	if res, out := post(t, srv.URL+"/coddy/browser/v1/call", map[string]any{"browser_id": "b", "action": "snapshot", "tab_id": 1}); res.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(string), "caller.node") {
		t.Fatalf("no caller: %d %v", res.StatusCode, out)
	}
	caller := map[string]string{"node": "laptop", "session_id": "sess_1"}
	if res, out := post(t, srv.URL+"/coddy/browser/v1/call", map[string]any{"browser_id": "b", "action": "upload_file", "tab_id": 1, "caller": caller}); res.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(string), "file.name") {
		t.Fatalf("upload without a file: %d %v", res.StatusCode, out)
	}
	if res, out := post(t, srv.URL+"/coddy/browser/v1/call", map[string]any{"browser_id": "missing-browser", "action": "snapshot", "tab_id": 1, "caller": caller}); res.StatusCode != http.StatusConflict || !strings.Contains(out["error"].(string), "not connected") {
		t.Fatalf("a browser that is not here: %d %v", res.StatusCode, out)
	}
}

// A remote chat owns its tabs as <node>/<session id>, the extension hears
// where it runs, and its tabs lose their working marks once it falls quiet.
func TestARemoteChatIsNamedByItsNodeAndEndsItsTurnByQuiet(t *testing.T) {
	saved := remoteTurnIdle
	remoteTurnIdle = 50 * time.Millisecond
	t.Cleanup(func() { remoteTurnIdle = saved })
	hub, srv := apiServer(t, true)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-api-0001"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ext.Close() })
	if err := ext.Share(browsertest.Tab{ID: 5, URL: "https://example.com/", Title: "Example", SessionID: "laptop/sess_1"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		s := hub.List("")
		return len(s) == 1 && len(s[0].Tabs) == 1
	})
	remote := &Remote{Label: "home", Base: srv.URL}
	snaps, err := remote.Browsers(context.Background(), Caller{Node: "laptop", SessionID: "sess_1"})
	if err != nil || len(snaps) != 1 || !snaps[0].Tabs[0].Yours {
		t.Fatalf("the remote chat does not see its tab as its own: %+v %v", snaps, err)
	}
	res, err := remote.Call(context.Background(), CallRequest{
		BrowserID: "browser-api-0001", Action: "snapshot", TabID: 5,
		Caller: Caller{Node: "laptop", SessionID: "sess_1", Title: "Plan a trip"},
	})
	if err != nil || !strings.Contains(string(res.Result), "ok") {
		t.Fatalf("%+v %v", res, err)
	}
	cmd, err := ext.Next(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Session.ID != "laptop/sess_1" || cmd.Session.Node != "laptop" || cmd.Session.Title != "Plan a trip" {
		t.Fatalf("the command named the chat %+v", cmd.Session)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case note := <-ext.Notes:
			if strings.HasPrefix(note, "browser/turnEnded") && strings.Contains(note, `"laptop/sess_1"`) {
				return
			}
		case <-deadline:
			t.Fatal("the quiet remote chat's turn did not end")
		}
	}
}

// A remote call that lasts longer than the quiet interval does not end its
// chat's turn while it runs.
func TestALongRemoteCallIsNotAQuietChat(t *testing.T) {
	saved := remoteTurnIdle
	remoteTurnIdle = 40 * time.Millisecond
	t.Cleanup(func() { remoteTurnIdle = saved })
	hub, srv := apiServer(t, true)
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: "browser-api-long1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ext.Close() })
	if err := ext.Share(browsertest.Tab{ID: 5, URL: "https://example.com/", Title: "Example", SessionID: "laptop/sess_2"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { s := hub.List(""); return len(s) == 1 && len(s[0].Tabs) == 1 })
	ext.Handle = func(browsertest.Command) browsertest.Answer {
		time.Sleep(200 * time.Millisecond)
		return browsertest.Answer{Result: map[string]bool{"ok": true}}
	}
	remote := &Remote{Label: "home", Base: srv.URL}
	if _, err := remote.Call(context.Background(), CallRequest{BrowserID: "browser-api-long1", Action: "snapshot", TabID: 5, Caller: Caller{Node: "laptop", SessionID: "sess_2"}}); err != nil {
		t.Fatal(err)
	}
	ended := false
	for drained := false; !drained; {
		select {
		case note := <-ext.Notes:
			if strings.HasPrefix(note, "browser/turnEnded") {
				ended = true
			}
		default:
			drained = true
		}
	}
	if ended {
		t.Fatal("the turn ended while its call ran")
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case note := <-ext.Notes:
			if strings.HasPrefix(note, "browser/turnEnded") {
				return
			}
		case <-deadline:
			t.Fatal("the turn did not end once the chat fell quiet")
		}
	}
}
