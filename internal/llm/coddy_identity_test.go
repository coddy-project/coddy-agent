package llm

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Re-exec before the first HTTPS request: system roots are process-wide.
func runSystemTrustRow(t *testing.T, baseURL, proxy, caFile, wantError string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCoddySystemTrustChild$", "-test.v")
	cmd.Env = append(os.Environ(), "CODDY_TRUST_CHILD=1", "CODDY_TRUST_URL="+baseURL,
		"CODDY_TRUST_PROXY="+proxy, "CODDY_TRUST_ERROR="+wantError,
		"SSL_CERT_FILE="+caFile, "SSL_CERT_DIR="+t.TempDir())
	// Windows/macOS do not read SSL_CERT_FILE. Use Go's system-root fallback
	// there; Linux exercises the actual system-store environment path.
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "GODEBUG=x509usefallbackroots=1", "CODDY_TRUST_FALLBACK="+caFile)
	}
	platform.AdaptCommand(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("system-trust child: %v\n%s", err, out)
	}
}

func TestCoddySystemTrustChild(t *testing.T) {
	if os.Getenv("CODDY_TRUST_CHILD") != "1" {
		return
	}
	if file := os.Getenv("CODDY_TRUST_FALLBACK"); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			t.Fatal("invalid fallback roots")
		}
		x509.SetFallbackRoots(pool)
	}
	in := ProviderInput{Name: "remote", Type: "coddy", Model: "coder", APIKey: "tok",
		BaseURL: os.Getenv("CODDY_TRUST_URL"), ProxyURL: os.Getenv("CODDY_TRUST_PROXY"), RetryDisabled: true}
	p, err := NewProvider(in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, streamErr := p.Stream(ctx, userMsg("Hi"), nil, nil)
	_, listErr := ListModels(ctx, in)
	u, usageErr := CoddyUsageForProvider(ctx, in, "coder")
	want := os.Getenv("CODDY_TRUST_ERROR")
	for path, err := range map[string]error{"completion": streamErr, "listing": listErr, "usage": usageErr} {
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", path, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", path, want, err)
		}
	}
	if want == "" && (u == nil || !u.Supported) {
		t.Fatalf("usage: %+v", u)
	}
}

func TestCoddyRowSystemHTTPS(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+" "+r.TLS.NegotiatedProtocol)
		mu.Unlock()
		switch r.URL.Path {
		case CoddyCompletionsPath:
			fw := startStream(w)
			fw.text("hi")
			fw.final(&Response{Content: "hi", StopReason: "end_turn", InputTokens: 1, OutputTokens: 1})
		case CoddyModelsPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"protocol":1,"data":[{"id":"coder","revision":"r1","max_context_tokens":1000}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"supported":true,"account_wide":true,"windows":[{"id":"session","label":"3h","used_percent":10}]}`)
		}
	}))
	defer srv.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, url, roots, want string }{
		{"trusted", srv.URL, caFile, ""},
		{"unknown authority", srv.URL, filepath.Join(t.TempDir(), "absent.pem"), "unknown authority"},
	} {
		t.Run(tc.name, func(t *testing.T) { runSystemTrustRow(t, tc.url, "none", tc.roots, tc.want) })
	}
	// Dial the same listener through a CONNECT proxy under another name, so
	// the wrong-hostname case tests certificate verification, not DNS failure.
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "not CONNECT", http.StatusBadRequest)
			return
		}
		connects.Add(1)
		up, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		down, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		if _, err := down.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			_ = up.Close()
			_ = down.Close()
			return
		}
		go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
		go func() { _, _ = io.Copy(down, up); _ = down.Close() }()
	}))
	defer proxy.Close()
	t.Run("trusted through proxy", func(t *testing.T) { runSystemTrustRow(t, srv.URL, proxy.URL, caFile, "") })
	t.Run("wrong hostname", func(t *testing.T) {
		_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		runSystemTrustRow(t, "https://wrong.invalid:"+port, proxy.URL, caFile, "wrong.invalid")
	})
	if connects.Load() < 2 {
		t.Fatal("HTTPS did not follow the configured proxy")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 6 {
		t.Fatalf("want 6 trusted requests and no untrusted requests, got %v", seen)
	}
	for _, s := range seen {
		if !strings.HasSuffix(s, " http/1.1") {
			t.Errorf("not HTTP/1.1: %q", s)
		}
	}
}
