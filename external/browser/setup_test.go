//go:build browser

package browser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The settings screen reads the endpoint while a person connects a browser:
// whether a key is set, and which browsers are on the socket right now. It is
// answered whether or not browser.serve_remote is on - the reader is
// configuring this very endpoint - and it carries no key and no tab.
func TestSetupReadAnswersTheSettingsScreen(t *testing.T) {
	hub := NewHub()
	api := &API{Hub: hub, Allow: func() bool { return false }, KeySet: func() bool { return true }}
	mux := http.NewServeMux()
	api.Register(mux)

	ask := func() SetupResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/coddy/browser/setup", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with serve_remote off", rec.Code)
		}
		var got SetupResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := ask()
	if !got.KeySet || len(got.Connected) != 0 {
		t.Fatalf("setup = %+v, want a key and no browser", got)
	}
	conn, err := hub.Connect(BrowserInfo{ID: "browser-one", Name: "Chrome on devbox", Version: "142.0"}, []string{"open_tab"})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Disconnect(conn)
	got = ask()
	want := SetupBrowser{ID: "browser-one", Name: "Chrome on devbox", Version: "142.0"}
	if len(got.Connected) != 1 || got.Connected[0] != want {
		t.Fatalf("setup = %+v, want the connected browser as it named itself", got)
	}
	// Nothing of the browser but what it called itself: no tab, no action,
	// no key.
	raw, _ := json.Marshal(got)
	for _, leak := range []string{"tabs", "actions", "token", "key\""} {
		if string(raw) != "" && containsWord(string(raw), leak) {
			t.Fatalf("the setup read carries %q: %s", leak, raw)
		}
	}
}

func containsWord(s, word string) bool {
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] == word {
			return true
		}
	}
	return false
}
