package browser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtensionBrowser runs the packaged extension in a real browser. Opt in
// with CODDY_BROWSER_E2E=1 and make the playwright package available to Node.
func TestExtensionBrowser(t *testing.T) {
	if os.Getenv("CODDY_BROWSER_E2E") != "1" {
		t.Skip("set CODDY_BROWSER_E2E=1 for real browser verification")
	}
	hub := NewHub()
	bridge := NewServer(hub, testKey)
	permission := make(chan struct{}, 1)
	bridge.Chat = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/coddy/sessions" {
			_, _ = w.Write([]byte(`{"sessions":[]}`))
			return
		}
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		if r.URL.Path == "/v1/responses" && r.Method == "POST" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: permission\ndata: {\"sessionId\":\"" + r.Header.Get("X-Coddy-Session-ID") + "\",\"toolCall\":{\"toolCallId\":\"test-tool\",\"title\":\"Browser action\"},\"options\":[{\"optionId\":\"allow\",\"name\":\"Allow tool once\"}]}\n\n"))
			w.(http.Flusher).Flush()
			select {
			case <-permission:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Permission accepted\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/permission") && r.Method == "POST" {
			permission <- struct{}{}
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	})
	mux := http.NewServeMux()
	mux.Handle("/browser/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("CODDY_DEBUG_FIREFOX") != "" {
			t.Logf("browser request: %s origin=%s", r.URL.Path, r.Header.Get("Origin"))
		}
		bridge.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/example", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Browser fixture</title><h1>Browser fixture</h1><button onclick="this.textContent='Clicked'">Click me</button><label>Name<input aria-label="Name"></label><div contenteditable="true" role="textbox" aria-label="Editor">draft</div><input type="file" aria-label="Upload"><a href="/download">Download</a>`))
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("download fixture")) })
	mux.HandleFunc("/test/action", func(w http.ResponseWriter, r *http.Request) {
		var cmd Command
		if json.NewDecoder(r.Body).Decode(&cmd) != nil {
			w.WriteHeader(400)
			return
		}
		if cmd.Action == "upload_file" {
			var p map[string]any
			_ = json.Unmarshal(cmd.Params, &p)
			id, cleanup, err := StageFile("example.txt", []byte("upload fixture"), false)
			if err != nil {
				t.Error(err)
				return
			}
			defer cleanup()
			p["transfer_id"], p["filename"] = id, "example.txt"
			cmd.Params, _ = json.Marshal(p)
		}
		var cleanup func()
		var ticket string
		if cmd.Action == "download_file" {
			var p map[string]any
			_ = json.Unmarshal(cmd.Params, &p)
			ticket, cleanup, _ = StageFile("download.txt", nil, true)
			defer cleanup()
			p["transfer_id"], p["filename"] = ticket, "download.txt"
			cmd.Params, _ = json.Marshal(p)
		}
		browsers := hub.List()
		if len(browsers) == 0 {
			http.Error(w, "browser not connected", http.StatusConflict)
			return
		}
		result, err := hub.Call(r.Context(), "test-chat", browsers[0].ID, cmd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if ticket != "" {
			data, e := FileResult(ticket)
			if e != nil || string(data) != "download fixture" {
				http.Error(w, "bad downloaded data", 500)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "external/browser-extension/tests/browser.cjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CODDY_TEST_URL="+server.URL, "CODDY_TEST_KEY="+testKey, "CODDY_TEST_PROFILE="+t.TempDir())
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
