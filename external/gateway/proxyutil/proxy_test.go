//go:build gateway || gateway.telegram

package proxyutil

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/proxytest"
)

// TestBuildHTTPClientRoutes pins the route each gateways.telegram.proxy value
// gives the bot: an empty value and inherit keep the default client, which
// follows HTTPS_PROXY, HTTP_PROXY and NO_PROXY; none connects directly; a URL
// goes through that proxy.
func TestBuildHTTPClientRoutes(t *testing.T) {
	t.Parallel()
	for _, setting := range []string{"", "inherit", " Inherit "} {
		c, err := BuildHTTPClient(setting)
		if err != nil {
			t.Fatalf("BuildHTTPClient(%q): %v", setting, err)
		}
		if c != http.DefaultClient {
			t.Errorf("BuildHTTPClient(%q) is not the default client, which follows the environment's proxy", setting)
		}
	}

	for _, setting := range []string{"none", "NONE"} {
		c, err := BuildHTTPClient(setting)
		if err != nil {
			t.Fatalf("BuildHTTPClient(%q): %v", setting, err)
		}
		tr, ok := c.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("BuildHTTPClient(%q) transport is %T, want *http.Transport", setting, c.Transport)
		}
		if tr.Proxy != nil {
			t.Errorf("BuildHTTPClient(%q) consults a proxy function; none connects directly", setting)
		}
	}

	c, err := BuildHTTPClient("http://127.0.0.1:3128")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.telegram.org/bot1:x/getMe", nil)
	u, err := c.Transport.(*http.Transport).Proxy(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:3128" {
		t.Errorf("an http proxy URL routes through %v (%v), want 127.0.0.1:3128", u, err)
	}

	if _, err := BuildHTTPClient("direct"); err == nil || !strings.Contains(err.Error(), `use "none"`) {
		t.Errorf(`BuildHTTPClient("direct") err = %v, want the accepted words named`, err)
	}
}

// The variables the parent hands the child of
// TestBuildHTTPClientFollowsTheProcessEnvironment.
const (
	proxyChildEnv     = "CODDY_TEST_PROXYUTIL_CHILD"
	proxyChildTarget  = "CODDY_TEST_PROXYUTIL_TARGET"
	proxyChildSetting = "CODDY_TEST_PROXYUTIL_SETTING"
)

// TestBuildHTTPClientFollowsTheProcessEnvironment asks getMe through the
// bot's client in a child process whose environment names a proxy, so
// net/http reads the variables for real (it reads them once per process,
// and never for a loopback address, so no in-process test can stage them).
// The target is 0.0.0.0, which reaches the local listener and is not a
// loopback address to net/http.
func TestBuildHTTPClientFollowsTheProcessEnvironment(t *testing.T) {
	if os.Getenv(proxyChildEnv) != "" {
		buildHTTPClientChild()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses a connection to 0.0.0.0")
	}
	var direct, proxied atomic.Int32
	answer := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"username":"proxy_bot"}}`)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		answer(w)
	}))
	defer api.Close()
	envProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		proxied.Add(1)
		answer(w)
	}))
	defer envProxy.Close()
	_, port, err := net.SplitHostPort(api.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", "0.0.0.0:"+port, 2*time.Second); err != nil {
		t.Skipf("this host does not route 0.0.0.0 to a local listener: %v", err)
	} else {
		_ = c.Close()
	}
	target := "http://0.0.0.0:" + port + "/bot1:x/getMe"

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
			cmd := exec.Command(os.Args[0], "-test.run=^TestBuildHTTPClientFollowsTheProcessEnvironment$", "-test.count=1")
			cmd.Env = append(proxytest.WithoutProxyVariables(os.Environ()),
				proxyChildEnv+"=1",
				proxyChildTarget+"="+target,
				proxyChildSetting+"="+tc.setting,
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
				t.Errorf("the Bot API was reached directly %d times, want %d", got, tc.direct)
			}
		})
	}
}

// buildHTTPClientChild is the child side: one getMe through the client the
// setting builds, then exit with its outcome.
func buildHTTPClientChild() {
	err := func() error {
		c, err := BuildHTTPClient(os.Getenv(proxyChildSetting))
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, os.Getenv(proxyChildTarget), nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	}()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
