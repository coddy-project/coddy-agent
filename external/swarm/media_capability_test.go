//go:build swarm

package swarm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// mediaCapabilityNode records what the node behind a mount was asked.
type mediaCapabilityNode struct {
	mu    sync.Mutex
	calls []mediaCapabilityCall
}

type mediaCapabilityCall struct {
	method        string
	path          string
	authorization string
	accessToken   string
}

func (n *mediaCapabilityNode) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		n.calls = append(n.calls, mediaCapabilityCall{
			method:        r.Method,
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			accessToken:   r.URL.Query().Get("access_token"),
		})
		n.mu.Unlock()
		w.Header().Set("Content-Range", "bytes 0-4/11")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "hello")
	}))
}

func (n *mediaCapabilityNode) last(t *testing.T) mediaCapabilityCall {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.calls) == 0 {
		t.Fatal("the request never reached the node")
	}
	return n.calls[len(n.calls)-1]
}

func (n *mediaCapabilityNode) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

// The web UI plays a workspace video or audio file through a native <video> /
// <audio> element, which cannot send a header: the address carries a short-lived
// capability the node minted and signed (access_token). The relay cannot check
// that signature, so it hands the request to the node as it came - capability in
// the query, no credential of the relay's - and the node decides.
const mediaCapability = "eyJ2IjoxfQ.c2lnbmF0dXJl"

func TestMountCarriesAWorkspaceMediaCapabilityToTheNode(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			node := &mediaCapabilityNode{}
			ns := node.server()
			defer ns.Close()
			_, ts := mountTestRelay(t, ns.URL)
			defer ts.Close()

			req, err := http.NewRequest(method, ts.URL+"/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=clip.webm&download=0&access_token="+url.QueryEscape(mediaCapability), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", "bytes=0-4")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusPartialContent {
				body, _ := io.ReadAll(res.Body)
				t.Fatalf("status %d, want the node's 206; body %s", res.StatusCode, body)
			}
			got := node.last(t)
			if got.path != "/coddy/sessions/sess_1/workspace/raw" {
				t.Fatalf("node saw path %q", got.path)
			}
			if got.accessToken != mediaCapability {
				t.Fatalf("node saw access_token %q, want the capability it has to check", got.accessToken)
			}
			if got.authorization != "" {
				t.Fatalf("node saw authorization %q: the relay vouched for a request it could not check", got.authorization)
			}
		})
	}
}

// Through a chain the outer relay hands the capability to the inner relay, which
// has to recognise the same request after the first hop.
func TestMountCarriesAWorkspaceMediaCapabilityThroughAChain(t *testing.T) {
	node := &mediaCapabilityNode{}
	ns := node.server()
	defer ns.Close()
	_, inner := mountTestRelay(t, ns.URL)
	defer inner.Close()
	_, outer := mountTestRelay(t, inner.URL)
	defer outer.Close()

	res, err := http.Get(outer.URL + "/swarm/nodes/nas02/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=clip.webm&access_token=" + url.QueryEscape(mediaCapability))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d through two relays, want 206; body %s", res.StatusCode, body)
	}
	got := node.last(t)
	if got.accessToken != mediaCapability || got.authorization != "" {
		t.Fatalf("node saw access_token %q and authorization %q", got.accessToken, got.authorization)
	}
}

// The exception is exactly one route, read-only, and only for a request that
// brings nothing else: every other query token stays refused, and a client that
// did authenticate to the relay is carried the ordinary way.
func TestMountKeepsTheClientGateAroundTheMediaException(t *testing.T) {
	node := &mediaCapabilityNode{}
	ns := node.server()
	defer ns.Close()
	_, ts := mountTestRelay(t, ns.URL)
	defer ts.Close()

	refused := []struct {
		name   string
		method string
		path   string
	}{
		{"another session route", http.MethodGet, "/swarm/nodes/nas02/coddy/sessions/sess_1/messages?access_token=" + mediaCapability},
		{"the workspace text route", http.MethodGet, "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/text?path_rel=a.txt&access_token=" + mediaCapability},
		{"minting a capability", http.MethodPost, "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/media-token?access_token=" + mediaCapability},
		{"a write to the raw route", http.MethodPost, "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?access_token=" + mediaCapability},
		{"a path below the raw route", http.MethodGet, "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw/more?access_token=" + mediaCapability},
		{"the raw route without a capability", http.MethodGet, "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt"},
		{"an empty capability", http.MethodGet, "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token="},
		{"the relay's own route", http.MethodGet, "/swarm/sessions?access_token=" + mediaCapability},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			before := node.count()
			req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(""))
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", res.StatusCode)
			}
			if node.count() != before {
				t.Fatal("an unauthenticated request reached the node")
			}
		})
	}

	// Let in on a capability alone, a request has shown the relay nothing: every
	// refusal of the relay's own is the gate's 401, so the exception lists no node,
	// no path rule and no node's state to someone without a client token.
	for _, tc := range []struct{ name, path string }{
		{"an unknown node", "/swarm/nodes/ghost/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token=" + mediaCapability},
		{"an invalid node name", "/swarm/nodes/Bad%20Name/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token=" + mediaCapability},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := http.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), "ghost") {
				t.Fatalf("status %d, body %s: want the gate's plain 401", res.StatusCode, body)
			}
		})
	}

	// A second access_token rides along with a first one the relay would take for
	// a capability: the request is not the exception's, whatever the first says.
	t.Run("two access tokens", func(t *testing.T) {
		before := node.count()
		res, err := http.Get(ts.URL + "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token=" + mediaCapability + "&access_token=client-secret")
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", res.StatusCode)
		}
		if node.count() != before {
			t.Fatal("a request with two access tokens reached the node")
		}
	})

	// The relay's own client token in the query is never forwarded: it would land
	// in the node's access log as if it were a capability.
	t.Run("the relay's client token as a capability", func(t *testing.T) {
		before := node.count()
		res, err := http.Get(ts.URL + "/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token=client-secret")
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", res.StatusCode)
		}
		if node.count() != before {
			t.Fatal("the relay's client token was carried to the node")
		}
	})

	// A client with the relay's bearer is carried as before: its credential is
	// replaced by the node's, and no query token crosses the hop.
	t.Run("an authenticated client", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token="+mediaCapability, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer client-secret")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusPartialContent {
			t.Fatalf("status %d, want 206", res.StatusCode)
		}
		got := node.last(t)
		if got.authorization != "Bearer node-secret" || got.accessToken != "" {
			t.Fatalf("node saw authorization %q and access_token %q", got.authorization, got.accessToken)
		}
	})
}

// A browser on another origin that reads a node through the relay - a node's
// web UI with the relay added as a remote - asks for a workspace file the way
// the Files window does: a HEAD with If-None-Match to revalidate, a Range for
// media, and the ETag read back. The relay owns the CORS answer (a node's own
// headers are dropped on the way), so it has to allow those request headers
// and expose those response headers itself.
func TestRelayCORSCarriesTheFilesWindowHeaders(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `W/"v1"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
	}))
	defer node.Close()
	srv, ts := mountTestRelay(t, node.URL)
	defer ts.Close()
	srv.cfg.Swarm.CORS.Enabled = true
	srv.cfg.Swarm.CORS.AllowedOrigins = []string{"http://ui.example"}

	pre, err := http.NewRequest(http.MethodOptions, ts.URL+"/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	pre.Header.Set("Origin", "http://ui.example")
	pre.Header.Set("Access-Control-Request-Method", "HEAD")
	pre.Header.Set("Access-Control-Request-Headers", "authorization,if-none-match,range")
	res, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	allowed := strings.ToLower(res.Header.Get("Access-Control-Allow-Headers"))
	for _, h := range []string{"authorization", "if-none-match", "range"} {
		if !strings.Contains(allowed, h) {
			t.Errorf("preflight allows headers %q, missing %s", allowed, h)
		}
	}
	if methods := res.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "HEAD") {
		t.Errorf("preflight allows methods %q, missing HEAD", methods)
	}

	req, err := http.NewRequest(http.MethodHead, ts.URL+"/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://ui.example")
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	exposed := strings.ToLower(res.Header.Get("Access-Control-Expose-Headers"))
	for _, h := range []string{"etag", "content-range", "accept-ranges", "content-disposition"} {
		if !strings.Contains(exposed, h) {
			t.Errorf("response exposes %q, missing %s", exposed, h)
		}
	}
	if res.Header.Get("ETag") != `W/"v1"` {
		t.Errorf("ETag %q did not cross the relay", res.Header.Get("ETag"))
	}
}

// A relay that asks no client token hands a capability request on the same
// way: mounted under a relay that has a gate, it must not vouch for what the
// outer relay let in on a capability, or a forged one would read any file.
func TestAnOpenRelayInAChainLeavesTheCapabilityToTheNode(t *testing.T) {
	node := &mediaCapabilityNode{}
	ns := node.server()
	defer ns.Close()
	inner, innerTS := mountTestRelay(t, ns.URL)
	defer innerTS.Close()
	inner.cfg.Swarm.AuthToken = ""
	_, outer := mountTestRelay(t, innerTS.URL)
	defer outer.Close()

	res, err := http.Get(outer.URL + "/swarm/nodes/nas02/swarm/nodes/nas02/coddy/sessions/sess_1/workspace/raw?path_rel=a.txt&access_token=" + url.QueryEscape(mediaCapability))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	got := node.last(t)
	if got.authorization != "" || got.accessToken != mediaCapability {
		t.Fatalf("node saw authorization %q and access_token %q: the open relay vouched for a capability request", got.authorization, got.accessToken)
	}
}
