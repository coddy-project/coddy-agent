//go:build browser

package browser_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
)

// TestLiveExtensionChat writes in the side panel of the real Coddy for
// Browser extension, in a real Chromium, to a real listener and agent on a
// scripted model: the tab the person writes from reaches the browser tool
// with no tab_id, the panel answers the tool's permission and the site
// question, and the answer comes back to the panel. Opt in with
// CODDY_FOR_BROWSER_DIR naming a built checkout of
// github.com/coddy-project/coddy-for-browser, and Playwright reachable by Node.
func TestLiveExtensionChat(t *testing.T) {
	dir := os.Getenv("CODDY_FOR_BROWSER_DIR")
	if dir == "" {
		t.Skip("set CODDY_FOR_BROWSER_DIR to a built coddy-for-browser checkout")
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Panel fixture</title><h1>Panel fixture</h1><p>Deadline: 12 November.</p>`))
	}))
	defer page.Close()
	snapshot, _ := json.Marshal(map[string]any{"action": "snapshot"})
	listener, model := scriptedCoddy(t, []llmstub.Rule{
		{Match: "summarize this page", Tool: &llmstub.ToolCall{Name: "browser", Arguments: snapshot}, Answer: "The page names one deadline: 12 November."},
	})
	defer model.Close()
	defer listener.Close()

	drive := exec.Command("node", filepath.Join(dir, "tests", "e2e", "drive.mjs"))
	drive.Env = append(os.Environ(), "CODDY_URL="+listener.URL, "CODDY_KEY="+chatKey,
		"CODDY_PANEL_PAGE="+page.URL, "CODDY_PANEL_PROMPT=summarize this page")
	stdin, err := drive.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := drive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	drive.Stderr = os.Stderr
	if err := drive.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = drive.Wait()
	}()
	answers := make(chan string, 1)
	go func() {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			if text, ok := strings.CutPrefix(lines.Text(), "ANSWER: "); ok {
				answers <- text
			}
		}
		close(answers)
	}()
	select {
	case got, ok := <-answers:
		if !ok {
			t.Fatal("the panel never answered")
		}
		if !strings.Contains(got, "12 November") {
			t.Fatalf("the panel answered %q", got)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("no answer in the panel within 90 s")
	}
}
