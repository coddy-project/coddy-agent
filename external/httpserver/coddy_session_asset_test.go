//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// pngBytes is a real, decodable PNG: the original-asset route decides what it
// serves by sniffing the bytes, so a test fixture has to be an actual image.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for x := 0; x < 4; x++ {
		for y := 0; y < 3; y++ {
			img.Set(x, y, color.RGBA{R: uint8(40 * x), G: uint8(60 * y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// assetTestServer is a server with one persisted session; the returned dir is
// that session's assets directory, ready for a fixture to be written into.
func assetTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	created, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	if st == nil {
		t.Fatal("session missing")
	}
	assets := session.AssetsPath(st.GetPersistedSessionDir())
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	return srv, created.SessionID, assets
}

func getAsset(t *testing.T, srv *Server, sessionID, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+sessionID+"/assets/"+name, nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	return rec
}

// The route hands back the picture as it was uploaded, not the bounded preview:
// that is what makes enlarging it worth anything.
func TestSessionAssetServesOriginalImageBytes(t *testing.T) {
	srv, sessionID, assets := assetTestServer(t)
	want := pngBytes(t)
	if err := os.WriteFile(filepath.Join(assets, "photo.png"), want, 0o444); err != nil {
		t.Fatal(err)
	}

	rec := getAsset(t, srv, sessionID, "photo.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("body is %d bytes, want the %d original bytes", rec.Body.Len(), len(want))
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

// Only images leave the bundle, and the name never decides it: a text file
// called photo.png is still not an image.
func TestSessionAssetRefusesBytesThatAreNotAnImage(t *testing.T) {
	srv, sessionID, assets := assetTestServer(t)
	if err := os.WriteFile(filepath.Join(assets, "notes.txt"), []byte("plain secrets"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "liar.png"), []byte("id_rsa PRIVATE KEY"), 0o444); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"notes.txt", "liar.png"} {
		rec := getAsset(t, srv, sessionID, name)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404 (body %s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A missing asset is a 404 and an asset name that is a path at all is refused
// before anything is opened, exactly as the thumbnail route refuses it.
func TestSessionAssetRejectsPathNamesAndMissingFiles(t *testing.T) {
	srv, sessionID, _ := assetTestServer(t)

	if rec := getAsset(t, srv, sessionID, "absent.png"); rec.Code != http.StatusNotFound {
		t.Fatalf("absent: status %d", rec.Code)
	}
	for _, name := range []string{"..", ".", "../assets/photo.png", `sub/photo.png`} {
		req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+sessionID+"/assets/x", nil)
		req.SetPathValue("id", sessionID)
		req.SetPathValue("name", name)
		rec := httptest.NewRecorder()
		srv.coddySessionAssetGet(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: status %d, want 400", name, rec.Code)
		}
	}
}

// A link planted in the bundle must not turn either asset route into a reader of
// whatever it points at. The agent can write into that directory, and the prompt
// tells it where the directory is.
func TestSessionAssetRefusesALinkOutOfTheBundle(t *testing.T) {
	srv, sessionID, assets := assetTestServer(t)
	sessionDir := filepath.Dir(assets)

	outside := filepath.Join(t.TempDir(), "private.png")
	if err := os.WriteFile(outside, pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(assets, "leak.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rec := getAsset(t, srv, sessionID, "leak.png"); rec.Code != http.StatusNotFound {
		t.Fatalf("a link out of the bundle was served: status %d", rec.Code)
	}

	if err := os.MkdirAll(session.AssetThumbnailsPath(sessionDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, session.AssetThumbnailPath(sessionDir, "leak.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+sessionID+"/assets/leak.png/thumbnail", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("name", "leak.png")
	rec := httptest.NewRecorder()
	srv.coddySessionAssetThumbnailGet(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a linked thumbnail was served: status %d", rec.Code)
	}
}

// A link does not have to be the file: the assets folder, or the thumbnails
// folder inside it, replaced by a link to a folder outside the bundle led both
// routes out of it, since a check of the last element alone passed a regular
// file there.
func TestSessionAssetRefusesALinkedFolderOutOfTheBundle(t *testing.T) {
	srv, sessionID, assets := assetTestServer(t)
	sessionDir := filepath.Dir(assets)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.png"), pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "thumbnails"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "thumbnails", "private.png.png"), pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(assets); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, assets); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rec := getAsset(t, srv, sessionID, "private.png"); rec.Code != http.StatusNotFound {
		t.Fatalf("a file behind a linked assets folder was served: status %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+sessionID+"/assets/private.png/thumbnail", nil)
	req.SetPathValue("id", sessionID)
	req.SetPathValue("name", "private.png")
	rec := httptest.NewRecorder()
	srv.coddySessionAssetThumbnailGet(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a thumbnail behind a linked assets folder was served: status %d", rec.Code)
	}
	if !strings.HasPrefix(session.AssetThumbnailPath(sessionDir, "private.png"), assets) {
		t.Fatal("the thumbnail is expected inside the assets folder")
	}
}

// The address is a bare name under this session's assets directory, so a part
// saved anywhere else must carry none: its base name would either answer 404 or
// name a different file that happens to share it.
func TestLlmMsgsToCoddyOpenAIForSessionAddressesOnlyRealAssets(t *testing.T) {
	assetsDir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "photo one.png")
	if err := os.WriteFile(elsewhere, pngBytes(t), 0o444); err != nil {
		t.Fatal(err)
	}
	out := llmMsgsToCoddyOpenAIForSession("sess_files", assetsDir, "", []llm.Message{
		{
			Role:       llm.RoleUser,
			Content:    "look",
			ImageParts: []llm.ImagePart{{Name: "photo one.png", FilePath: elsewhere}},
		},
	})
	files := out[0]["files"].([]map[string]interface{})
	if got, ok := files[0]["url"]; ok {
		t.Fatalf("a file outside the assets directory was addressed: %#v", got)
	}
}

// The transcript carries the full-size address next to the bounded preview, so
// a click in the bubble has something larger to open.
func TestLlmMsgsToCoddyOpenAIForSessionIncludesFullSizeAssetURL(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "photo one.png")
	if err := os.WriteFile(saved, pngBytes(t), 0o444); err != nil {
		t.Fatal(err)
	}
	writeThumbnail(t, dir, "photo one.png")
	out := llmMsgsToCoddyOpenAIForSession("sess_files", dir, "", []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "look",
			ImageParts: []llm.ImagePart{
				{Name: "photo one.png", FilePath: saved, ThumbnailPath: saved + ".png"},
				{Name: "gone.png", FilePath: filepath.Join(dir, "gone.png"), ThumbnailPath: filepath.Join(dir, "gone.png.png")},
			},
		},
	})
	files, ok := out[0]["files"].([]map[string]interface{})
	if !ok || len(files) != 2 {
		t.Fatalf("files: %#v", out[0]["files"])
	}
	if got := files[0]["preview_url"]; got != "/coddy/sessions/sess_files/assets/photo%20one.png/thumbnail" {
		t.Fatalf("preview_url = %#v", got)
	}
	if got := files[0]["url"]; got != "/coddy/sessions/sess_files/assets/photo%20one.png" {
		t.Fatalf("url = %#v", got)
	}
	if got, ok := files[1]["url"]; ok {
		t.Fatalf("an asset that is no longer on disk must carry no url, got %#v", got)
	}
}

// A picture a read showed the model stays on that call's result, and the
// transcript names it there the way it names a prompt attachment, so the web
// UI previews it on the read row after a reload. The bytes themselves never
// travel in the transcript.
func TestLlmMsgsToCoddyOpenAIForSessionNamesThePicturesOfAToolResult(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "shot-1a2b.png")
	if err := os.WriteFile(saved, pngBytes(t), 0o444); err != nil {
		t.Fatal(err)
	}
	writeThumbnail(t, dir, "shot-1a2b.png")
	out := llmMsgsToCoddyOpenAIForSession("sess_files", dir, "", []llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: "r1",
		Content:    "shot.png: PNG image, 4x3",
		ImageParts: []llm.ImagePart{{DataURL: "data:image/png;base64,abc", Name: "shot.png", FilePath: saved, ThumbnailPath: saved + ".png"}},
	}})
	files, ok := out[0]["files"].([]map[string]interface{})
	if !ok || len(files) != 1 {
		t.Fatalf("files: %#v", out[0]["files"])
	}
	if files[0]["name"] != "shot.png" || files[0]["mime_type"] != "image/png" {
		t.Errorf("file = %#v, want shot.png as image/png", files[0])
	}
	if got := files[0]["url"]; got != "/coddy/sessions/sess_files/assets/shot-1a2b.png" {
		t.Errorf("url = %#v", got)
	}
	if got := files[0]["preview_url"]; got != "/coddy/sessions/sess_files/assets/shot-1a2b.png/thumbnail" {
		t.Errorf("preview_url = %#v", got)
	}
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "base64,abc") {
		t.Errorf("the transcript carries the picture's bytes: %s", raw)
	}
}

// A session bundle that moved keeps the paths its parts recorded where it was
// before; the copies are in its assets directory now, under the same names,
// and the transcript addresses them there - the way the agent finds a tool
// picture's copy to send - so a WebP without a thumbnail still has a card.
func TestLlmMsgsToCoddyOpenAIForSessionAddressesTheCopiesOfAMovedBundle(t *testing.T) {
	assetsDir := filepath.Join(t.TempDir(), "sess_moved", "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const asset = "shot-0123456789abcdef.webp"
	if err := os.WriteFile(filepath.Join(assetsDir, asset), []byte("RIFF"), 0o444); err != nil {
		t.Fatal(err)
	}
	recorded := filepath.Join(t.TempDir(), "sess_moved", "assets", asset)
	out := llmMsgsToCoddyOpenAIForSession("sess_moved", assetsDir, "", []llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: "r1",
		Content:    "shot.webp: WebP image, 4x3",
		ImageParts: []llm.ImagePart{{Name: "shot.webp", MIMEType: "image/webp", FilePath: recorded}},
	}})
	files := out[0]["files"].([]map[string]interface{})
	if got := files[0]["url"]; got != "/coddy/sessions/sess_moved/assets/"+asset {
		t.Errorf("url = %#v, want the copy in the bundle's assets now", got)
	}
}

// The served spec is the contract clients generate against: the new route is
// in it, and its description states the narrower rule it now enforces.
func TestOpenAPIDescribesTheSessionAssetRoute(t *testing.T) {
	spec := openAPISpec()
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		t.Fatal("paths missing")
	}
	entry, ok := paths["/coddy/sessions/{id}/assets/{name}"].(map[string]interface{})
	if !ok {
		t.Fatalf("route missing from the spec")
	}
	get, ok := entry["get"].(map[string]interface{})
	if !ok {
		t.Fatalf("get missing: %#v", entry)
	}
	desc, _ := get["description"].(string)
	if desc == "" {
		t.Fatal("the route needs a description")
	}
	for _, want := range []string{"image", "sniff"} {
		if !bytes.Contains(bytes.ToLower([]byte(desc)), []byte(want)) {
			t.Fatalf("description does not mention %q: %s", want, desc)
		}
	}
}

// writeThumbnail puts the bounded preview of an asset where the server looks
// for it.
func writeThumbnail(t *testing.T, assetsDir, assetName string) {
	t.Helper()
	thumb := session.ThumbnailPathInAssets(assetsDir, assetName)
	if err := os.MkdirAll(filepath.Dir(thumb), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(thumb, pngBytes(t), 0o444); err != nil {
		t.Fatal(err)
	}
}
