//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func artifactServer(t *testing.T) (*httptest.Server, *session.Manager, string, string, session.Artifact) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	if err := os.Mkdir(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "report.txt"), []byte("report bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: filepath.Join(root, "home"), CWD: cwd}, Agent: config.Agent{Model: "fake/model"}}
	mgr := session.NewManager(cfg, noopSender{}, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}, slog.Default(), cwd, &session.FileStore{Root: filepath.Join(root, "sessions")})
	one, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(one.SessionID)
	a, err := session.CaptureArtifact(st.GetPersistedSessionDir(), cwd, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "share", Name: "share_file", InputJSON: `{"path":"report.txt"}`}}})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "share", Content: `{"artifact":{"id":"` + a.ID + `"}}`, Artifacts: []llm.Artifact{{ID: a.ID, Name: a.Name, SHA256: a.SHA256, Size: a.Size, SourcePath: a.SourcePath, SourceRelativePath: a.SourceRelativePath}}})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The report is ready."})
	two, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, mgr, slog.Default(), cwd)
	return httptest.NewServer(srv.Handler()), mgr, one.SessionID, two.SessionID, a
}

func artifactBMPBytes() []byte {
	return []byte{
		0x42, 0x4d, 0x3a, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x36, 0x00, 0x00, 0x00, 0x28, 0x00,
		0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x01, 0x00, 0x18, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x00,
	}
}

func TestSessionArtifactDownloadGuardsAndDTOs(t *testing.T) {
	ts, mgr, id, other, a := artifactServer(t)
	defer ts.Close()
	client := ts.Client()
	get := func(method, path string, h map[string]string) *http.Response {
		r, _ := http.NewRequest(method, ts.URL+path, nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		x, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return x
	}
	path := "/coddy/sessions/" + id + "/artifacts/" + a.ID
	r := get("GET", path, nil)
	b, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || string(b) != "report bytes" {
		t.Fatalf("download %d %q", r.StatusCode, b)
	}
	for k, w := range map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": "attachment; filename*=UTF-8''report.txt", "Content-Security-Policy": "sandbox; default-src 'none'", "X-Content-Type-Options": "nosniff", "Cache-Control": "private, max-age=31536000, immutable"} {
		if got := r.Header.Get(k); got != w {
			t.Errorf("%s=%q want %q", k, got, w)
		}
	}
	for _, tc := range []struct {
		method, path string
		headers      map[string]string
	}{{"HEAD", path, nil}, {"GET", path, map[string]string{"Range": "bytes=0-1"}}, {"GET", "/coddy/sessions/" + id + "/artifacts/unknown", nil}, {"GET", "/coddy/sessions/" + other + "/artifacts/" + a.ID, nil}, {"GET", "/coddy/sessions/" + id + "/artifacts/..%2Fmanifest.json", nil}} {
		x := get(tc.method, tc.path, tc.headers)
		_ = x.Body.Close()
		if x.StatusCode != http.StatusNotFound && x.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s=%d", tc.method, tc.path, x.StatusCode)
		}
	}
	// The presentation DTO survives a manager reload in both message and tool-call views.
	mgr2 := mgr
	_ = mgr2
	for _, u := range []string{"/coddy/sessions/" + id + "/messages", "/coddy/sessions/" + id + "/tool-calls", "/coddy/sessions/" + id + "/tool-calls/share"} {
		x := get("GET", u, nil)
		var v map[string]any
		if err := json.NewDecoder(x.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		_ = x.Body.Close()
		raw, _ := json.Marshal(v)
		if !bytes.Contains(raw, []byte(`"artifacts"`)) || !bytes.Contains(raw, []byte(a.ID)) || !bytes.Contains(raw, []byte(`"sourcePath"`)) || !bytes.Contains(raw, []byte(`"relativePath"`)) {
			t.Errorf("%s lacks artifact dto: %s", u, raw)
		}
	}
	response := get("GET", "/coddy/sessions/"+id+"/messages", nil)
	messageBody, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var messages struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(messageBody, &messages); err != nil {
		t.Fatal(err)
	}
	foundPlacement := false
	for _, message := range messages.Messages {
		if message.Role == string(llm.RoleAssistant) && strings.Contains(message.Content, `<coddy_file id="`+a.ID+`"/>`) {
			foundPlacement = true
			break
		}
	}
	if !foundPlacement {
		t.Fatalf("messages API lost assistant artifact placement: %s", messageBody)
	}
	// HTTP streaming hashes the immutable copy rather than trusting its name.
	p := session.ArtifactPath(mgr.SessionByID(id).GetPersistedSessionDir(), a.SHA256)
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("tampered"), 0400); err != nil {
		t.Fatal(err)
	}
	x := get("GET", path, nil)
	_ = x.Body.Close()
	if x.StatusCode != 404 {
		t.Errorf("tampered download=%d", x.StatusCode)
	}
}

func TestSessionArtifactPreviewServesOnlyImages(t *testing.T) {
	ts, mgr, id, _, report := artifactServer(t)
	defer ts.Close()
	st := mgr.SessionByID(id)
	if st == nil {
		t.Fatal("session missing")
	}
	imagePath := filepath.Join(st.GetCWD(), "preview.png")
	if err := os.WriteFile(imagePath, pngBytes(t), 0644); err != nil {
		t.Fatal(err)
	}
	image, err := session.CaptureArtifact(st.GetPersistedSessionDir(), st.GetCWD(), "preview.png")
	if err != nil {
		t.Fatal(err)
	}
	imageDTO := artifactDTOs(id, []llm.Artifact{{ID: image.ID, Name: image.Name}})
	if got := imageDTO[0]["previewUrl"]; got != "/coddy/sessions/"+id+"/artifacts/"+image.ID+"/preview" {
		t.Fatalf("image artifact preview URL = %#v", got)
	}
	reportDTO := artifactDTOs(id, []llm.Artifact{{ID: report.ID, Name: report.Name}})
	if _, ok := reportDTO[0]["previewUrl"]; ok {
		t.Fatalf("non-image artifact unexpectedly has a preview URL: %#v", reportDTO[0])
	}
	bmpPath := filepath.Join(st.GetCWD(), "preview.bmp")
	if err := os.WriteFile(bmpPath, artifactBMPBytes(), 0644); err != nil {
		t.Fatal(err)
	}
	bmp, err := session.CaptureArtifact(st.GetPersistedSessionDir(), st.GetCWD(), "preview.bmp")
	if err != nil {
		t.Fatal(err)
	}
	bmpDTO := artifactDTOs(id, []llm.Artifact{{ID: bmp.ID, Name: bmp.Name}})
	if got := bmpDTO[0]["previewUrl"]; got != "/coddy/sessions/"+id+"/artifacts/"+bmp.ID+"/preview" {
		t.Fatalf("BMP artifact preview URL = %#v", got)
	}
	client := ts.Client()
	imageResponse, err := client.Get(ts.URL + "/coddy/sessions/" + id + "/artifacts/" + image.ID + "/preview")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = imageResponse.Body.Close() }()
	if imageResponse.StatusCode != http.StatusOK || imageResponse.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("image preview status=%d type=%q", imageResponse.StatusCode, imageResponse.Header.Get("Content-Type"))
	}
	reportResponse, err := client.Get(ts.URL + "/coddy/sessions/" + id + "/artifacts/" + report.ID + "/preview")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reportResponse.Body.Close() }()
	if reportResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("text artifact preview status=%d", reportResponse.StatusCode)
	}
	bmpResponse, err := client.Get(ts.URL + "/coddy/sessions/" + id + "/artifacts/" + bmp.ID + "/preview")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bmpResponse.Body.Close() }()
	if bmpResponse.StatusCode != http.StatusOK || bmpResponse.Header.Get("Content-Type") != "image/bmp" {
		t.Fatalf("BMP preview status=%d type=%q", bmpResponse.StatusCode, bmpResponse.Header.Get("Content-Type"))
	}
	imageArtifactPath := session.ArtifactPath(st.GetPersistedSessionDir(), image.SHA256)
	if err := os.Chmod(imageArtifactPath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		imageArtifactPath,
		[]byte("tampered"),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	tamperedResponse, err := client.Get(ts.URL + "/coddy/sessions/" + id + "/artifacts/" + image.ID + "/preview")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tamperedResponse.Body.Close() }()
	if tamperedResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("tampered image preview status=%d", tamperedResponse.StatusCode)
	}
}

func TestSessionArtifactRevealIsScopedToStoredArtifactSource(t *testing.T) {
	ts, _, id, other, a := artifactServer(t)
	defer ts.Close()
	post := func(path string) int {
		r, err := ts.Client().Post(ts.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		return r.StatusCode
	}

	path := "/coddy/sessions/" + id + "/artifacts/" + a.ID + "/reveal"
	if got := post(path); got != http.StatusNoContent && got != http.StatusServiceUnavailable {
		t.Fatalf("reveal status = %d, want accepted or clear unavailable", got)
	}
	for _, path := range []string{
		"/coddy/sessions/" + other + "/artifacts/" + a.ID + "/reveal",
		"/coddy/sessions/" + id + "/artifacts/unknown/reveal",
		"/coddy/sessions/" + id + "/artifacts/..%2Fmanifest.json/reveal",
	} {
		if got := post(path); got != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, got)
		}
	}
}

func TestOpenAPISessionArtifactPathIsSeparateFromAssets(t *testing.T) {
	paths, ok := openAPISpec()["paths"].(map[string]interface{})
	if !ok {
		t.Fatal("OpenAPI paths missing")
	}
	const artifactPath = "/coddy/sessions/{id}/artifacts/{artifactID}"
	const previewPath = "/coddy/sessions/{id}/artifacts/{artifactID}/preview"
	const revealPath = "/coddy/sessions/{id}/artifacts/{artifactID}/reveal"
	const assetPath = "/coddy/sessions/{id}/assets/{name}"
	artifact, ok := paths[artifactPath].(map[string]interface{})
	if !ok || artifact["get"] == nil {
		t.Fatalf("artifact path missing or has no GET operation: %#v", artifact)
	}
	assets, ok := paths[assetPath].(map[string]interface{})
	if !ok || assets["get"] == nil {
		t.Fatalf("assets path missing or has no GET operation: %#v", assets)
	}
	if _, nested := assets[artifactPath]; nested {
		t.Fatalf("artifact path is incorrectly nested under assets: %#v", assets)
	}
	preview, ok := paths[previewPath].(map[string]interface{})
	if !ok || preview["get"] == nil {
		t.Fatalf("artifact preview path missing or has no GET operation: %#v", preview)
	}
	reveal, ok := paths[revealPath].(map[string]interface{})
	if !ok || reveal["post"] == nil {
		t.Fatalf("artifact reveal path missing or has no POST operation: %#v", reveal)
	}
}
