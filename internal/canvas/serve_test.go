package canvas

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, ws, rel, body string) {
	t.Helper()
	p := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serve(t *testing.T, f Frame, method, rel string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/coddy/canvas/tok/"+rel, nil)
	rec := httptest.NewRecorder()
	ServeFrame(rec, req, f, rel, ServeOptions{AllowedSources: []string{"https://cdn.jsdelivr.net"}})
	return rec
}

func siteFrame(t *testing.T) (Frame, string) {
	t.Helper()
	ws := t.TempDir()
	writeFile(t, ws, "site/index.html", "<!DOCTYPE html>\n<html><head><title>Shop</title></head><body><h1>Hi</h1></body></html>")
	writeFile(t, ws, "site/app.js", "export const n = 1;\n")
	writeFile(t, ws, "site/css/main.css", "body{margin:0}")
	writeFile(t, ws, "site/pages/about.html", "<html><body>about</body></html>")
	writeFile(t, ws, "site/.env", "SECRET=1")
	writeFile(t, ws, "site/.git/config", "[core]")
	writeFile(t, ws, "secret.txt", "outside the root")
	return Frame{ID: "fr_1", Path: "site/index.html", Root: "site", Workspace: ws, Width: 1280, Height: 800}, ws
}

func TestServeFrameAnswersThePageWithTheBridgeAndTheSandbox(t *testing.T) {
	f, _ := siteFrame(t)
	rec := serve(t, f, http.MethodGet, "index.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if got := h.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content type %q", got)
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "default-src 'self'", "https://cdn.jsdelivr.net", "form-action 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "allow-top-navigation") || strings.Contains(csp, "allow-popups") {
		t.Fatalf("CSP %q grants too much", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers %v", h)
	}
	if h.Get("Access-Control-Allow-Origin") != "*" || h.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("CORS headers %v", h)
	}
	if h.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control %q", h.Get("Cache-Control"))
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "<!DOCTYPE html>") {
		t.Fatalf("the doctype is no longer first: %q", body[:40])
	}
	headAt := strings.Index(body, "<head>")
	bridgeAt := strings.Index(body, `<script src=".coddy/bridge.js"></script>`)
	titleAt := strings.Index(body, "<title>")
	if bridgeAt < 0 || bridgeAt < headAt || bridgeAt > titleAt {
		t.Fatalf("bridge not first in the head: %q", body)
	}
	if !strings.Contains(body, `<meta name="referrer" content="no-referrer">`) {
		t.Fatalf("referrer meta missing: %q", body)
	}
}

func TestServeFrameAnswersAssetsAsTheyAre(t *testing.T) {
	f, _ := siteFrame(t)
	js := serve(t, f, http.MethodGet, "app.js")
	if js.Code != 200 || js.Body.String() != "export const n = 1;\n" {
		t.Fatalf("app.js %d %q", js.Code, js.Body)
	}
	if ct := js.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("app.js type %q", ct)
	}
	if js.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("a module script would be refused without CORS")
	}
	css := serve(t, f, http.MethodGet, "css/main.css")
	if css.Code != 200 || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("css %d %q", css.Code, css.Header().Get("Content-Type"))
	}
	head := serve(t, f, http.MethodHead, "app.js")
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatalf("HEAD %d %q", head.Code, head.Body)
	}
}

func TestServeFrameInjectsTheBridgeIntoNestedPagesWithARelativeAddress(t *testing.T) {
	f, _ := siteFrame(t)
	rec := serve(t, f, http.MethodGet, "pages/about.html")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<script src="../.coddy/bridge.js"></script>`) {
		t.Fatalf("nested page bridge: %q", rec.Body)
	}
}

func TestServeFrameServesTheBridgeItself(t *testing.T) {
	f, _ := siteFrame(t)
	rec := serve(t, f, http.MethodGet, BridgePath)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("bridge: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), BridgeProtocol) {
		t.Fatalf("the bridge does not speak %q", BridgeProtocol)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("the bridge would be refused from an opaque origin")
	}
}

func TestServeFrameOpensTheFolderIndex(t *testing.T) {
	f, ws := siteFrame(t)
	rec := serve(t, f, http.MethodGet, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Shop</title>") {
		t.Fatalf("root index %d %q", rec.Code, rec.Body)
	}
	if got := serve(t, f, http.MethodGet, "css/"); got.Code != http.StatusNotFound {
		t.Fatalf("a folder without an index answered %d (listing?)", got.Code)
	}
	writeFile(t, ws, "site/blog/index.html", "<p>blog</p>")
	if got := serve(t, f, http.MethodGet, "blog/"); got.Code != 200 || !strings.Contains(got.Body.String(), "blog") {
		t.Fatalf("folder index %d %q", got.Code, got.Body)
	}
}

func TestServeFrameRefuses(t *testing.T) {
	f, _ := siteFrame(t)
	for _, rel := range []string{".env", ".git/config", "../secret.txt", "css/../../secret.txt", "missing.js", ".coddy/other.js", "%2e%2e/secret.txt"} {
		if rec := serve(t, f, http.MethodGet, rel); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d: %q", rel, rec.Code, rec.Body)
		}
	}
	rec := serve(t, f, http.MethodPost, "index.html")
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "GET") {
		t.Fatalf("POST answered %d", rec.Code)
	}
}

func TestServeFrameRefusesALinkToADotFileOrOutOfTheRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	f, ws := siteFrame(t)
	if err := os.Symlink(filepath.Join(ws, "site", ".env"), filepath.Join(ws, "site", "env.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "secret.txt"), filepath.Join(ws, "site", "secret.txt")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"env.txt", "secret.txt"} {
		if rec := serve(t, f, http.MethodGet, rel); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d: %q", rel, rec.Code, rec.Body)
		}
	}
}

func TestServeFrameLeavesAHugePageAlone(t *testing.T) {
	f, ws := siteFrame(t)
	big := "<html><head></head><body>" + strings.Repeat("x", MaxInjectBytes) + "</body></html>"
	writeFile(t, ws, "site/big.html", big)
	rec := serve(t, f, http.MethodGet, "big.html")
	if rec.Code != 200 || rec.Body.Len() != len(big) {
		t.Fatalf("big page %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestInjectBridgePlacement(t *testing.T) {
	const tag = `<meta name="referrer" content="no-referrer"><script src="x.js"></script>`
	cases := []struct{ in, want string }{
		{"<!doctype html><html><head><title>t</title></head></html>", "<!doctype html><html><head>" + tag + "<title>t</title></head></html>"},
		{"<HTML><HEAD lang=en><TITLE>t</TITLE>", "<HTML><HEAD lang=en>" + tag + "<TITLE>t</TITLE>"},
		{"<!DOCTYPE html>\n<html lang=\"en\"><body>b</body></html>", "<!DOCTYPE html>\n<html lang=\"en\">" + tag + "<body>b</body></html>"},
		{"<!doctype html><p>bare</p>", "<!doctype html>" + tag + "<p>bare</p>"},
		{"<p>fragment</p>", tag + "<p>fragment</p>"},
		{"\xef\xbb\xbf<!doctype html><head></head>", "\xef\xbb\xbf<!doctype html><head>" + tag + "</head>"},
		{"<!doctype html><header>not a head</header>", "<!doctype html>" + tag + "<header>not a head</header>"},
	}
	for _, tc := range cases {
		if got := string(InjectBridge([]byte(tc.in), "x.js")); got != tc.want {
			t.Errorf("InjectBridge(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestBridgeSrcClimbsToTheRoot(t *testing.T) {
	for rel, want := range map[string]string{
		"index.html":       ".coddy/bridge.js",
		"":                 ".coddy/bridge.js",
		"pages/about.html": "../.coddy/bridge.js",
		"a/b/c.html":       "../../.coddy/bridge.js",
		"blog/":            "../.coddy/bridge.js",
	} {
		if got := BridgeSrc(rel); got != want {
			t.Errorf("BridgeSrc(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestValidateSource(t *testing.T) {
	for _, ok := range []string{"https://cdn.jsdelivr.net", "https://*.example.com", "https://fonts.gstatic.com:443", "http://localhost:5173", "https://cdn.jsdelivr.net/npm/"} {
		if err := ValidateSource(ok); err != nil {
			t.Errorf("ValidateSource(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "https:", "http:", "'unsafe-inline'", "*", "https://a.com; script-src *", "https://a.com https://b.com", "data:", "'self'", "javascript:", "https://a.com/'x"} {
		if err := ValidateSource(bad); err == nil {
			t.Errorf("ValidateSource(%q) accepted", bad)
		}
	}
}

func TestContentSecurityPolicyWithNoSourcesKeepsTheFrameToItsFiles(t *testing.T) {
	csp := ContentSecurityPolicy(nil)
	if strings.Contains(csp, "https:") || !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("CSP without sources %q", csp)
	}
}

func TestServeFrameServesNoLinksAtAll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	f, ws := siteFrame(t)
	// A link to an ordinary file of the root, and a linked folder.
	if err := os.Symlink(filepath.Join(ws, "site", "app.js"), filepath.Join(ws, "site", "alias.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "site", "css"), filepath.Join(ws, "site", "styles")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"alias.js", "styles/main.css"} {
		if rec := serve(t, f, http.MethodGet, rel); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d", rel, rec.Code)
		}
	}
}
