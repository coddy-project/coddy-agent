//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/canvas"
)

// canvasRequest goes through the whole handler chain: CORS, the auth gate,
// the mux.
func canvasRequest(e *changesEnv, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, r)
	return w
}

type addedFrame struct {
	Reason string `json:"reason"`
	Frame  struct {
		ID    string `json:"id"`
		Path  string `json:"path"`
		Root  string `json:"root"`
		URL   string `json:"url"`
		Stamp string `json:"stamp"`
		Gen   int    `json:"gen"`
	} `json:"frame"`
}

func addCanvasFrame(t *testing.T, e *changesEnv, path string, headers map[string]string) addedFrame {
	t.Helper()
	w := canvasRequest(e, "POST", "/coddy/sessions/"+e.id+"/canvas/frames", `{"path":"`+path+`"}`, headers)
	if w.Code != 200 {
		t.Fatalf("add frame: %d %s", w.Code, w.Body)
	}
	var out addedFrame
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sitePage(t *testing.T, cwd string) {
	t.Helper()
	writeInWorkspace(t, cwd, "site/index.html", `<!doctype html><html><head><title>Shop</title><script type="module" src="app.js"></script></head><body><button id="add">Add</button></body></html>`)
	writeInWorkspace(t, cwd, "site/app.js", "document.getElementById('add').onclick = () => {};\n")
	writeInWorkspace(t, cwd, "site/.env", "SECRET=1")
	writeInWorkspace(t, cwd, "secret.txt", "outside the root")
}

func TestCanvasFrameRoundTrip(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	if added.Reason != canvas.ReasonAdded || added.Frame.Root != "site" || added.Frame.Gen != 1 || added.Frame.Stamp == "" {
		t.Fatalf("added %+v", added)
	}
	if !strings.HasPrefix(added.Frame.URL, "/coddy/canvas/") || !strings.HasSuffix(added.Frame.URL, "/index.html") {
		t.Fatalf("frame url %q", added.Frame.URL)
	}

	board := canvasRequest(e, "GET", "/coddy/sessions/"+e.id+"/canvas", "", nil)
	if board.Code != 200 || !strings.Contains(board.Body.String(), added.Frame.ID) {
		t.Fatalf("board %d %s", board.Code, board.Body)
	}

	page := canvasRequest(e, "GET", added.Frame.URL, "", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `<script src=".coddy/bridge.js"></script>`) {
		t.Fatalf("page %d %s", page.Code, page.Body)
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox allow-scripts") || !strings.Contains(csp, "https://cdn.jsdelivr.net") {
		t.Fatalf("page CSP %q", csp)
	}
	base := strings.TrimSuffix(added.Frame.URL, "index.html")
	module := canvasRequest(e, "GET", base+"app.js", "", map[string]string{"Origin": "null"})
	if module.Code != 200 || module.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("module script from an opaque origin: %d %v", module.Code, module.Header())
	}
	bridge := canvasRequest(e, "GET", base+".coddy/bridge.js", "", map[string]string{"Origin": "null"})
	if bridge.Code != 200 || !strings.Contains(bridge.Body.String(), canvas.BridgeProtocol) {
		t.Fatalf("bridge %d", bridge.Code)
	}
	for _, rel := range []string{".env", "../secret.txt"} {
		if w := canvasRequest(e, "GET", base+rel, "", nil); w.Code == 200 {
			t.Fatalf("%s served: %s", rel, w.Body)
		}
	}

	del := canvasRequest(e, "DELETE", "/coddy/sessions/"+e.id+"/canvas/frames/"+added.Frame.ID, "", nil)
	if del.Code != 200 {
		t.Fatalf("remove %d %s", del.Code, del.Body)
	}
	if w := canvasRequest(e, "GET", added.Frame.URL, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a removed frame's address answered %d", w.Code)
	}
}

func TestCanvasRotateRevokesTheOldAddress(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	w := canvasRequest(e, "POST", "/coddy/sessions/"+e.id+"/canvas/frames/"+added.Frame.ID+"/rotate", "", nil)
	if w.Code != 200 {
		t.Fatalf("rotate %d %s", w.Code, w.Body)
	}
	var rotated addedFrame
	_ = json.Unmarshal(w.Body.Bytes(), &rotated)
	if rotated.Frame.Gen != 2 || rotated.Frame.URL == added.Frame.URL {
		t.Fatalf("rotated %+v", rotated)
	}
	if w := canvasRequest(e, "GET", added.Frame.URL, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("the old address answered %d after a rotate", w.Code)
	}
	if w := canvasRequest(e, "GET", rotated.Frame.URL, "", nil); w.Code != 200 {
		t.Fatalf("the new address answered %d", w.Code)
	}
}

func TestCanvasAddressRefusals(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	cases := map[string]string{
		"garbage":     "/coddy/canvas/not-a-token/index.html",
		"other key":   "/coddy/canvas/" + canvas.SignCapability([]byte("other"), canvas.Claim{Session: e.id, Frame: added.Frame.ID, Workspace: workspaceIdentity(e.cwd), Root: "site", Gen: 1}, time.Now()) + "/index.html",
		"other root":  "/coddy/canvas/" + canvas.SignCapability(e.srv.canvasKey(), canvas.Claim{Session: e.id, Frame: added.Frame.ID, Workspace: workspaceIdentity(e.cwd), Root: "", Gen: 1}, time.Now()) + "/secret.txt",
		"other gen":   "/coddy/canvas/" + canvas.SignCapability(e.srv.canvasKey(), canvas.Claim{Session: e.id, Frame: added.Frame.ID, Workspace: workspaceIdentity(e.cwd), Root: "site", Gen: 7}, time.Now()) + "/index.html",
		"expired":     "/coddy/canvas/" + canvas.SignCapability(e.srv.canvasKey(), canvas.Claim{Session: e.id, Frame: added.Frame.ID, Workspace: workspaceIdentity(e.cwd), Root: "site", Gen: 1}, time.Now().Add(-13*time.Hour)) + "/index.html",
		"other frame": "/coddy/canvas/" + canvas.SignCapability(e.srv.canvasKey(), canvas.Claim{Session: e.id, Frame: "fr_other", Workspace: workspaceIdentity(e.cwd), Root: "site", Gen: 1}, time.Now()) + "/index.html",
	}
	for name, target := range cases {
		if w := canvasRequest(e, "GET", target, "", nil); w.Code != http.StatusUnauthorized && w.Code != http.StatusNotFound {
			t.Errorf("%s answered %d: %s", name, w.Code, w.Body)
		}
	}
	if w := canvasRequest(e, "POST", added.Frame.URL, "", nil); w.Code == 200 {
		t.Fatal("POST to a frame's address succeeded")
	}
}

func TestCanvasAddressNeedsNoAPICredential(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	e.srv.SetExtraAuthTokens([]string{"test-secret"})
	bearer := map[string]string{"Authorization": "Bearer test-secret"}
	if w := canvasRequest(e, "GET", "/coddy/sessions/"+e.id+"/canvas", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("the board answered %d without a token", w.Code)
	}
	added := addCanvasFrame(t, e, "site/index.html", bearer)
	if w := canvasRequest(e, "GET", added.Frame.URL, "", nil); w.Code != 200 {
		t.Fatalf("a frame's address answered %d without an API token", w.Code)
	}
}

func TestOpaqueOriginsNeverReachTheAPI(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	null := map[string]string{"Origin": "null"}
	for _, target := range []string{"/coddy/sessions", "/v1/models", "/coddy/sessions/" + e.id + "/canvas"} {
		if w := canvasRequest(e, "GET", target, "", null); w.Code != http.StatusForbidden {
			t.Errorf("GET %s from an opaque origin answered %d", target, w.Code)
		}
	}
	if w := canvasRequest(e, "POST", "/v1/responses", `{"input":"hi"}`, null); w.Code != http.StatusForbidden {
		t.Errorf("POST /v1/responses from an opaque origin answered %d", w.Code)
	}
	// Whatever credential it carries: a relay forwards its own node token.
	e.srv.SetExtraAuthTokens([]string{"test-secret"})
	if w := canvasRequest(e, "GET", "/coddy/sessions", "", map[string]string{"Origin": "null", "Authorization": "Bearer test-secret"}); w.Code != http.StatusForbidden {
		t.Errorf("an opaque origin with a valid bearer answered %d", w.Code)
	}
	if w := canvasRequest(e, "GET", "/coddy/sessions", "", map[string]string{"Authorization": "Bearer test-secret"}); w.Code != 200 {
		t.Errorf("a plain bearer client answered %d", w.Code)
	}
	// A new token signs new addresses: read the board again for the frame's.
	board := canvasRequest(e, "GET", "/coddy/sessions/"+e.id+"/canvas", "", map[string]string{"Authorization": "Bearer test-secret"})
	var b struct {
		Frames []struct {
			URL string `json:"url"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(board.Body.Bytes(), &b); err != nil || len(b.Frames) != 1 {
		t.Fatalf("board %d %s", board.Code, board.Body)
	}
	if b.Frames[0].URL == added.Frame.URL {
		t.Fatal("a new token kept the old address valid")
	}
	if w := canvasRequest(e, "GET", b.Frames[0].URL, "", null); w.Code != 200 {
		t.Errorf("a frame's own address from its opaque origin answered %d", w.Code)
	}
}

func TestCanvasPreflightAnswersAnyOrigin(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	w := canvasRequest(e, "OPTIONS", added.Frame.URL, "", map[string]string{"Origin": "null", "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "x-app"})
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Access-Control-Allow-Headers") != "x-app" || !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "GET") {
		t.Fatalf("preflight %d %v", w.Code, w.Header())
	}
}

func TestCanvasReportReachesTheStore(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	body := `{"stamp":"` + added.Frame.Stamp + `","loaded":true,"title":"Shop","console":[{"level":"error","message":"boom","source":"app.js","line":3}]}`
	if w := canvasRequest(e, "POST", "/coddy/sessions/"+e.id+"/canvas/frames/"+added.Frame.ID+"/report", body, nil); w.Code != http.StatusNoContent {
		t.Fatalf("report %d %s", w.Code, w.Body)
	}
	defer canvas.DefaultReports().Forget(e.id, added.Frame.ID)
	rep, ok := canvas.DefaultReports().Get(e.id, added.Frame.ID)
	if !ok || rep.Stamp != added.Frame.Stamp || len(rep.Console) != 1 || rep.Console[0].Line != 3 {
		t.Fatalf("stored report %+v", rep)
	}
	board := canvasRequest(e, "GET", "/coddy/sessions/"+e.id+"/canvas", "", nil)
	if !strings.Contains(board.Body.String(), `"errors":1`) {
		t.Fatalf("the board does not count the error: %s", board.Body)
	}
	if w := canvasRequest(e, "POST", "/coddy/sessions/"+e.id+"/canvas/frames/fr_nope/report", body, nil); w.Code != http.StatusNotFound {
		t.Fatalf("a report for an unknown frame answered %d", w.Code)
	}
}

func TestCanvasChangesAreAnnouncedOnTheEventsStream(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	frames, stop := e.srv.events.subscribe()
	defer stop()
	added := addCanvasFrame(t, e, "site/index.html", nil)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f := <-frames:
			s := string(f)
			if strings.HasPrefix(s, "event: canvas_changed\n") && strings.Contains(s, added.Frame.ID) && strings.Contains(s, `"reason":"added"`) && strings.Contains(s, e.id) {
				return
			}
		case <-deadline:
			t.Fatal("no canvas_changed frame")
		}
	}
}

func TestCanvasOffAnswersNotFound(t *testing.T) {
	e := newChangesEnv(t)
	sitePage(t, e.cwd)
	added := addCanvasFrame(t, e, "site/index.html", nil)
	off := false
	e.srv.activeCfg().Tools.Canvas.Enabled = &off
	if w := canvasRequest(e, "GET", "/coddy/sessions/"+e.id+"/canvas", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("the board answered %d with the canvas off", w.Code)
	}
	if w := canvasRequest(e, "GET", added.Frame.URL, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a frame answered %d with the canvas off", w.Code)
	}
}

func TestCanvasOpenAPIListsTheRoutes(t *testing.T) {
	paths := openAPISpec()["paths"].(map[string]interface{})
	for _, p := range []string{
		"/coddy/sessions/{id}/canvas",
		"/coddy/sessions/{id}/canvas/frames",
		"/coddy/sessions/{id}/canvas/frames/{frame}",
		"/coddy/sessions/{id}/canvas/frames/{frame}/rotate",
		"/coddy/sessions/{id}/canvas/frames/{frame}/report",
		"/coddy/canvas/{capability}/{path}",
	} {
		if _, ok := paths[p]; !ok {
			t.Errorf("OpenAPI lacks %s", p)
		}
	}
}
