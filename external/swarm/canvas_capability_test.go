//go:build swarm

package swarm

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// canvasNode answers like a node's canvas route: the page's own CSP and CORS,
// and records what it was asked.
type canvasNode struct {
	mu    sync.Mutex
	calls []mediaCapabilityCall
}

func (n *canvasNode) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		n.calls = append(n.calls, mediaCapabilityCall{method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization"), accessToken: r.URL.Query().Get("access_token")})
		n.mu.Unlock()
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(w, "export const n = 1;")
	}))
}

func (n *canvasNode) last(t *testing.T) mediaCapabilityCall {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.calls) == 0 {
		t.Fatal("the request never reached the node")
	}
	return n.calls[len(n.calls)-1]
}

const canvasToken = "eyJ2IjoxLCJrIjoiY2FudmFzIn0.c2lnbmF0dXJl"

func getWithOrigin(t *testing.T, target, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A frame's page loads its module scripts and fetches its files from an opaque
// origin, with no token: the relay carries the request on the node's
// capability, answers CORS for any origin itself, and passes the node's
// sandbox policy through.
func TestMountCarriesACanvasFrameToTheNode(t *testing.T) {
	node := &canvasNode{}
	ns := node.server()
	defer ns.Close()
	_, ts := mountTestRelay(t, ns.URL)
	defer ts.Close()

	res := getWithOrigin(t, ts.URL+"/swarm/nodes/nas02/coddy/canvas/"+canvasToken+"/js/app.js?v=2&access_token=relay-should-drop", "null")
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || string(body) != "export const n = 1;" {
		t.Fatalf("status %d %q", res.StatusCode, body)
	}
	if got := res.Header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "*" {
		t.Fatalf("CORS %v: a module script from an opaque origin needs exactly one *", got)
	}
	if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("the node's sandbox headers were lost: %v", res.Header)
	}
	got := node.last(t)
	if got.path != "/coddy/canvas/"+canvasToken+"/js/app.js" {
		t.Fatalf("node saw %q", got.path)
	}
	if got.authorization != "" {
		t.Fatalf("node saw authorization %q: the relay vouched for a request it could not check", got.authorization)
	}
	if got.accessToken != "" {
		t.Fatalf("a query token rode along: %q", got.accessToken)
	}
}

func TestMountCarriesACanvasFrameThroughAChain(t *testing.T) {
	node := &canvasNode{}
	ns := node.server()
	defer ns.Close()
	_, inner := mountTestRelay(t, ns.URL)
	defer inner.Close()
	_, outer := mountTestRelay(t, inner.URL)
	defer outer.Close()

	res := getWithOrigin(t, outer.URL+"/swarm/nodes/nas02/swarm/nodes/nas02/coddy/canvas/"+canvasToken+"/index.html", "null")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d through two relays: %s", res.StatusCode, body)
	}
	if got := node.last(t); got.authorization != "" || got.path != "/coddy/canvas/"+canvasToken+"/index.html" {
		t.Fatalf("node saw %+v", got)
	}
}

func TestMountAnswersACanvasPreflightForAnyOrigin(t *testing.T) {
	node := &canvasNode{}
	ns := node.server()
	defer ns.Close()
	_, ts := mountTestRelay(t, ns.URL)
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/swarm/nodes/nas02/coddy/canvas/"+canvasToken+"/data.json", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "x-app")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != "*" || res.Header.Get("Access-Control-Allow-Headers") != "x-app" {
		t.Fatalf("preflight %d %v", res.StatusCode, res.Header)
	}
}

// The exception is the canvas route only, read-only, without a credential:
// an opaque origin reaches nothing else of the swarm, whatever it carries.
func TestMountRefusesAnOpaqueOriginEverywhereElse(t *testing.T) {
	node := &canvasNode{}
	ns := node.server()
	defer ns.Close()
	_, ts := mountTestRelay(t, ns.URL)
	defer ts.Close()

	for _, path := range []string{"/swarm/nodes/nas02/coddy/sessions", "/swarm/nodes/nas02/v1/models", "/swarm/sessions"} {
		res := getWithOrigin(t, ts.URL+path, "null")
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s from an opaque origin answered %d", path, res.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("an opaque origin with the client token answered %d", res.StatusCode)
	}
	// The canvas route with a method other than GET or HEAD is not the exception.
	post, _ := http.NewRequest(http.MethodPost, ts.URL+"/swarm/nodes/nas02/coddy/canvas/"+canvasToken+"/index.html", strings.NewReader("x"))
	post.Header.Set("Origin", "null")
	res, err = http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("POST to a frame address from an opaque origin answered %d", res.StatusCode)
	}
}

// An open relay (no client token) in front of a node would hand a frame's
// request to its API with the relay's node credential: an opaque origin is
// refused there too, while the frame's own files still load.
func TestAnOpenRelayRefusesAnOpaqueOriginToo(t *testing.T) {
	node := &canvasNode{}
	ns := node.server()
	defer ns.Close()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.PairingTokens = []string{"pair-secret"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name: "nas02", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: ns.URL, InstanceUUID: "uuid-nas02", Token: "node-secret",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res := getWithOrigin(t, ts.URL+"/swarm/nodes/nas02/coddy/sessions", "null")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("an open relay forwarded an opaque origin: %d", res.StatusCode)
	}
	res = getWithOrigin(t, ts.URL+"/swarm/nodes/nas02/coddy/canvas/"+canvasToken+"/index.html", "null")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("a frame through an open relay answered %d", res.StatusCode)
	}
	if got := node.last(t); got.authorization != "" {
		t.Fatalf("an open relay vouched for a frame with %q", got.authorization)
	}
}
