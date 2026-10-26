//go:build browser

package browsertool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// connected starts a listener on the process hub the tool calls through and
// connects a fake extension with one shared tab.
func connected(t *testing.T, id string) *browsertest.Extension {
	t.Helper()
	srv := httptest.NewServer(browser.NewServer(browser.Default(), testKey, nil))
	serverURL = srv.URL
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ext.Close()
		srv.Close()
		_ = waitFor(func() bool { return !browser.Default().Connected(id) })
	})
	if err := ext.Share(browsertest.Tab{ID: 1, URL: "https://example.com/", Title: "Example"}); err != nil {
		t.Fatal(err)
	}
	if err := waitFor(func() bool {
		for _, b := range browser.Default().List("") {
			if b.ID == id && len(b.Tabs) == 1 {
				return true
			}
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	return ext
}

// serverURL is the listener of the latest connected call, which a fake
// extension posts downloaded bytes to.
var serverURL string

func postTransfer(ticket string, data []byte) error {
	req, err := http.NewRequest(http.MethodPost, serverURL+"/browser/v1/files/"+ticket, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("transfer answered %d", resp.StatusCode)
	}
	return nil
}

func run(t *testing.T, env *tooling.Env, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return execute(ctx, string(raw), env)
}

func chat(t *testing.T) *tooling.Env {
	return &tooling.Env{SessionID: "sess_tool_test", SessionDir: t.TempDir(), CWD: t.TempDir()}
}

func TestTheOnlyConnectedBrowserIsUsedWithoutSelecting(t *testing.T) {
	connected(t, "browser-only-0001")
	out, err := run(t, chat(t), map[string]any{"action": "snapshot", "tab_id": 1})
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestSeveralBrowsersNeedASelectionThatIsRemembered(t *testing.T) {
	connected(t, "browser-many-0001")
	second := connected(t, "browser-many-0002")
	env := chat(t)
	if _, err := run(t, env, map[string]any{"action": "snapshot", "tab_id": 1}); err == nil || !strings.Contains(err.Error(), "several") {
		t.Fatalf("no selection: %v", err)
	}
	if _, err := run(t, env, map[string]any{"action": "select", "browser_id": "browser-many-0002"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, env, map[string]any{"action": "snapshot", "tab_id": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Next(time.Second); err != nil {
		t.Fatalf("the selected browser got nothing: %v", err)
	}
	out, err := run(t, env, map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, `"selected":"browser-many-0002"`) {
		t.Fatalf("list: %s %v", out, err)
	}
	if _, err := run(t, env, map[string]any{"action": "select", "browser_id": "browser-gone-0001"}); err == nil {
		t.Fatal("selected a browser that is not connected")
	}
}

func TestASelectedBrowserThatWentOfflineIsAnError(t *testing.T) {
	connected(t, "browser-stays-0001")
	env := chat(t)
	raw, _ := json.Marshal("browser-left-0001")
	if err := os.WriteFile(filepath.Join(env.SessionDir, "browser-selection.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, env, map[string]any{"action": "snapshot", "tab_id": 1}); err == nil || !strings.Contains(err.Error(), "browser-left-0001") {
		t.Fatalf("an offline selection moved to another browser: %v", err)
	}
}

func TestListSaysWhenNoBrowserIsConnected(t *testing.T) {
	out, err := run(t, chat(t), map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, "No browser is connected") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestFilePathsStayInTheWorkspace(t *testing.T) {
	connected(t, "browser-files-0001")
	env := chat(t)
	for _, path := range []string{"", "../outside", "/absolute", filepath.Join("..", "x")} {
		if _, err := run(t, env, map[string]any{"action": "upload_file", "tab_id": 1, "path": path}); err == nil {
			t.Fatalf("upload accepted %q", path)
		}
	}
	if err := os.WriteFile(filepath.Join(env.CWD, "exists.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, env, map[string]any{"action": "download_file", "tab_id": 1, "path": "exists.txt", "params": map[string]any{"url": "https://example.com/f"}}); err == nil {
		t.Fatal("a download overwrote a file")
	}
}

func TestADownloadSavesWhatTheExtensionPosted(t *testing.T) {
	ext := connected(t, "browser-download-0001")
	env := chat(t)
	ext.Handle = func(c browsertest.Command) browsertest.Answer {
		var p struct {
			TransferID string `json:"transfer_id"`
		}
		_ = json.Unmarshal(c.Params, &p)
		// The real extension posts the bytes it fetched to the ticket route;
		// the test plays that part through the same server-side entry.
		if err := postTransfer(p.TransferID, []byte("downloaded bytes")); err != nil {
			return browsertest.Answer{Error: err.Error()}
		}
		return browsertest.Answer{Result: map[string]int{"bytes": 16}}
	}
	out, err := run(t, env, map[string]any{"action": "download_file", "tab_id": 1, "path": "sub/file.bin", "params": map[string]any{"url": "https://example.com/f"}})
	if err == nil {
		t.Fatalf("a download into a missing folder succeeded: %s", out)
	}
	if err := os.Mkdir(filepath.Join(env.CWD, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, env, map[string]any{"action": "download_file", "tab_id": 1, "path": "sub/file.bin", "params": map[string]any{"url": "https://example.com/f"}})
	if err != nil || !strings.Contains(out, "16 bytes") {
		t.Fatalf("%s %v", out, err)
	}
	got, err := os.ReadFile(filepath.Join(env.CWD, "sub", "file.bin"))
	if err != nil || string(got) != "downloaded bytes" {
		t.Fatalf("saved %q %v", got, err)
	}
}

func TestScreenshotsAreCheckedAndAttached(t *testing.T) {
	ext := connected(t, "browser-shot-0001")
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	_ = png.Encode(&buf, img)
	ext.Handle = func(browsertest.Command) browsertest.Answer {
		return browsertest.Answer{Result: map[string]string{"dataUrl": "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())}}
	}
	env := chat(t)
	if _, err := run(t, env, map[string]any{"action": "screenshot", "tab_id": 1}); err == nil || !strings.Contains(err.Error(), "images") {
		t.Fatalf("a model without images got a screenshot: %v", err)
	}
	var attached []byte
	env.AttachImage = func(name, mime string, data []byte) error {
		if name != "browser.png" || mime != "image/png" {
			t.Fatalf("attached %s %s", name, mime)
		}
		attached = data
		return nil
	}
	if _, err := run(t, env, map[string]any{"action": "screenshot", "tab_id": 1}); err != nil || !bytes.Equal(attached, buf.Bytes()) {
		t.Fatalf("screenshot: %v", err)
	}
	ext.Handle = func(browsertest.Command) browsertest.Answer {
		return browsertest.Answer{Result: map[string]string{"dataUrl": "data:image/jpeg;base64,AAAA"}}
	}
	if _, err := run(t, env, map[string]any{"action": "screenshot", "tab_id": 1}); err == nil {
		t.Fatal("a JPEG passed as a PNG")
	}
}

func TestUnknownActionsAndMissingSessionsAreRefused(t *testing.T) {
	if _, err := execute(context.Background(), `{"action":"snapshot"}`, &tooling.Env{}); err == nil {
		t.Fatal("ran without a session")
	}
	if _, err := run(t, chat(t), map[string]any{"action": "javascript"}); err == nil {
		t.Fatal("ran an unknown action")
	}
}

func TestACallWithoutATabInAPanelChatActsOnThePanelsTab(t *testing.T) {
	ext := connected(t, "browser-panel-0001")
	env := chat(t)
	if err := os.WriteFile(filepath.Join(env.SessionDir, browser.PanelTabFile), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, env, map[string]any{"action": "snapshot"}); err != nil {
		t.Fatal(err)
	}
	cmd, err := ext.Next(time.Second)
	if err != nil || cmd.TabID != 1 {
		t.Fatalf("the call went to tab %d: %v", cmd.TabID, err)
	}
	// A chat outside the panel names its tab, or the call reaches none.
	if _, err := run(t, chat(t), map[string]any{"action": "snapshot"}); err == nil || !strings.Contains(err.Error(), "not shared") {
		t.Fatalf("a call with no tab: %v", err)
	}
}
