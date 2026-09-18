package llm

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPClientForProviderProxy(t *testing.T) {
	t.Parallel()
	// Every setting yields the provider's shared transport for it, so the
	// model list, the usage read and a sign-in take the route completions
	// take.
	for _, setting := range []string{"", "  \t  ", "inherit", "none", "http://127.0.0.1:3128", "socks5://127.0.0.1:1080"} {
		t.Run("shares_"+strings.TrimSpace(setting), func(t *testing.T) {
			t.Parallel()
			c, err := HTTPClientForProviderProxy(setting)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			want, err := providerTransport(setting)
			if err != nil {
				t.Fatalf("providerTransport: %v", err)
			}
			if c == nil || c.Transport != want {
				t.Fatalf("client transport %v, want the shared transport %v", c, want)
			}
		})
	}
	t.Run("bad_url", func(t *testing.T) {
		t.Parallel()
		_, err := HTTPClientForProviderProxy("http://%zz")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bad_scheme", func(t *testing.T) {
		t.Parallel()
		_, err := HTTPClientForProviderProxy("ftp://127.0.0.1:21")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unsupported scheme") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
	t.Run("unknown_keyword", func(t *testing.T) {
		t.Parallel()
		if _, err := HTTPClientForProviderProxy("direct"); err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestHTTPClientForOptionalProxy covers the URL-only helper callers other
// than a provider row use: no keywords, nil for an empty value.
func TestHTTPClientForOptionalProxy(t *testing.T) {
	t.Parallel()
	for _, empty := range []string{"", "  \t  "} {
		c, err := HTTPClientForOptionalProxy(empty)
		if err != nil || c != nil {
			t.Fatalf("HTTPClientForOptionalProxy(%q) = %v, %v, want nil, nil", empty, c, err)
		}
	}
	for _, bad := range []string{"http://%zz", "ftp://127.0.0.1:21", "none"} {
		if _, err := HTTPClientForOptionalProxy(bad); err == nil {
			t.Fatalf("HTTPClientForOptionalProxy(%q) accepted it", bad)
		}
	}
	if _, err := HTTPClientForOptionalProxy("http://user:s3cret@proxy:%zz"); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("a URL that does not parse: err = %v, want an error without the password", err)
	}
	for _, ok := range []string{"http://127.0.0.1:3128", "socks5://127.0.0.1:1080"} {
		c, err := HTTPClientForOptionalProxy(ok)
		if err != nil || c == nil || c.Transport == nil {
			t.Fatalf("HTTPClientForOptionalProxy(%q) = %v, %v, want a client", ok, c, err)
		}
	}
}

// httpTransportFor returns the *http.Transport behind the shared transport of
// a setting.
func httpTransportFor(t *testing.T, setting string) *http.Transport {
	t.Helper()
	rt, err := providerTransport(setting)
	if err != nil {
		t.Fatalf("providerTransport(%q): %v", setting, err)
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("providerTransport(%q) is %T, want *http.Transport", setting, rt)
	}
	return tr
}

func TestProviderTransportIsSharedPerSetting(t *testing.T) {
	t.Parallel()
	inherit := httpTransportFor(t, "")
	for _, alias := range []string{"inherit", "INHERIT", " inherit "} {
		if got := httpTransportFor(t, alias); got != inherit {
			t.Errorf("%q has a transport of its own, want the one the empty setting uses", alias)
		}
	}
	none := httpTransportFor(t, "none")
	if got := httpTransportFor(t, " None "); got != none {
		t.Error(`" None " has a transport of its own, want the one "none" uses`)
	}
	if none == inherit {
		t.Fatal(`"none" shares the transport of the environment's proxy`)
	}
	// Proxy nil is what connects directly: no environment variable is read.
	if none.Proxy != nil {
		t.Error(`the "none" transport consults a proxy function`)
	}
	if inherit.Proxy == nil {
		t.Error("the inherit transport does not consult the environment's proxy")
	}

	req, _ := http.NewRequest(http.MethodGet, "http://api.example.com/v1/models", nil)
	for _, setting := range []string{"http://127.0.0.1:3128", "HTTP://127.0.0.1:3128"} {
		proxied := httpTransportFor(t, setting)
		if proxied == inherit || proxied == none {
			t.Fatalf("%q shares a transport with a keyword", setting)
		}
		u, err := proxied.Proxy(req)
		if err != nil || u == nil || u.Scheme != "http" || u.Host != "127.0.0.1:3128" {
			t.Fatalf("%q routes through %v (%v), want http://127.0.0.1:3128", setting, u, err)
		}
	}
}

// standInEnvironmentProxy makes every provider that inherits its proxy use
// f until the test ends. The tests that call it must not run in parallel.
func standInEnvironmentProxy(t *testing.T, f func(*http.Request) (*url.URL, error)) {
	t.Helper()
	prev := environmentProxy.Swap(&f)
	t.Cleanup(func() { environmentProxy.Store(prev) })
}

// TestInheritTransportAsksTheEnvironmentPerRequest pins the seam the
// provider proxy feature stands its environment proxy in through: the
// inherit transport, built once for the process, asks for the proxy on
// each request rather than capturing it.
func TestInheritTransportAsksTheEnvironmentPerRequest(t *testing.T) {
	inherit := httpTransportFor(t, "")
	stand, _ := url.Parse("http://127.0.0.1:1")
	standInEnvironmentProxy(t, func(*http.Request) (*url.URL, error) { return stand, nil })

	req, _ := http.NewRequest(http.MethodGet, "http://api.example.com/v1/models", nil)
	u, err := inherit.Proxy(req)
	if err != nil || u == nil || u.String() != stand.String() {
		t.Fatalf("inherit transport routes through %v (%v), want %s", u, err, stand)
	}
}

// providerProxyChildEnv names the variables the parent hands the child of
// TestProviderProxyFollowsTheProcessEnvironment.
const (
	providerProxyChildEnv     = "CODDY_TEST_PROVIDER_PROXY_CHILD"
	providerProxyChildTarget  = "CODDY_TEST_PROVIDER_PROXY_TARGET"
	providerProxyChildSetting = "CODDY_TEST_PROVIDER_PROXY_SETTING"
)

// TestProviderProxyFollowsTheProcessEnvironment runs a model list in a child
// process whose environment names a proxy, so net/http reads the variables
// for real: the stand-in seam the feature suite uses cannot show that an
// unset key still follows HTTP_PROXY and NO_PROXY, nor that none ignores
// them. The target is 0.0.0.0, which reaches the local listener yet is not a
// loopback address, the one kind of host net/http never proxies.
func TestProviderProxyFollowsTheProcessEnvironment(t *testing.T) {
	if os.Getenv(providerProxyChildEnv) != "" {
		providerProxyChild()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses a connection to 0.0.0.0")
	}
	var direct, proxied atomic.Int32
	models := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		models(w)
	}))
	defer model.Close()
	envProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		proxied.Add(1)
		models(w)
	}))
	defer envProxy.Close()
	_, port, err := net.SplitHostPort(model.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Linux and macOS take a connection to 0.0.0.0 to the local host; a host
	// that does not would fail every case below for a reason of its own.
	if c, err := net.DialTimeout("tcp", "0.0.0.0:"+port, 2*time.Second); err != nil {
		t.Skipf("this host does not route 0.0.0.0 to a local listener: %v", err)
	} else {
		_ = c.Close()
	}
	target := "http://0.0.0.0:" + port + "/v1"

	for _, tc := range []struct {
		name, setting, noProxy string
		proxied, direct        int32
	}{
		{name: "unset follows HTTP_PROXY", proxied: 1},
		{name: "inherit follows HTTP_PROXY", setting: "inherit", proxied: 1},
		{name: "inherit honours NO_PROXY", setting: "inherit", noProxy: "0.0.0.0", direct: 1},
		{name: "none ignores HTTP_PROXY", setting: "none", direct: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct.Store(0)
			proxied.Store(0)
			cmd := exec.Command(os.Args[0], "-test.run=^TestProviderProxyFollowsTheProcessEnvironment$", "-test.count=1")
			cmd.Env = append(withoutProxyVariables(os.Environ()),
				providerProxyChildEnv+"=1",
				providerProxyChildTarget+"="+target,
				providerProxyChildSetting+"="+tc.setting,
				"HTTP_PROXY="+envProxy.URL,
				"HTTPS_PROXY="+envProxy.URL,
				"NO_PROXY="+tc.noProxy,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child: %v\n%s", err, out)
			}
			if got := proxied.Load(); got != tc.proxied {
				t.Errorf("the environment's proxy carried %d requests, want %d", got, tc.proxied)
			}
			if got := direct.Load(); got != tc.direct {
				t.Errorf("the model server was reached directly %d times, want %d", got, tc.direct)
			}
		})
	}
}

// withoutProxyVariables drops the proxy variables of the parent's
// environment, in either case, so the child sees only the ones the test sets.
func withoutProxyVariables(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "REQUEST_METHOD":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// providerProxyChild is the child side: one model list with the setting the
// parent chose, then exit with its outcome.
func providerProxyChild() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listed, err := ListModels(ctx, ProviderInput{
		Type:     "openai",
		APIKey:   "k",
		BaseURL:  os.Getenv(providerProxyChildTarget),
		ProxyURL: os.Getenv(providerProxyChildSetting),
	})
	if err == nil && (len(listed) != 1 || listed[0].ID != "m") {
		err = fmt.Errorf("model list = %+v, want the one model m", listed)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
