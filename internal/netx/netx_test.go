package netx

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDialFuncRejectsAnUnsupportedProxyScheme(t *testing.T) {
	if _, err := (Options{Proxy: "ftp://proxy.example"}).DialFunc(); err == nil {
		t.Fatal("an ftp proxy should not be accepted")
	}
	if _, err := (Options{Proxy: "://broken"}).DialFunc(); err == nil {
		t.Fatal("an unparsable proxy url should not be accepted")
	}
}

func TestDialFuncWithoutAProxyDialsDirectly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, aerr := ln.Accept()
		if aerr == nil {
			_, _ = c.Write([]byte("hello"))
			_ = c.Close()
		}
	}()

	dial, err := (Options{}).DialFunc()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dial(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("read %q", buf)
	}
}

// A relay in another network is usually reached through a proxy, and the
// tunnel needs a raw stream out of it rather than an http.Client.
func TestDialFuncTunnelsThroughAnHTTPProxy(t *testing.T) {
	// The origin the caller actually wants.
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = origin.Close() }()
	go func() {
		c, aerr := origin.Accept()
		if aerr == nil {
			_, _ = c.Write([]byte("origin-speaking"))
			_ = c.Close()
		}
	}()

	var connects atomic.Int32
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxyLn.Close() }()
	go func() {
		for {
			client, aerr := proxyLn.Accept()
			if aerr != nil {
				return
			}
			go func(client net.Conn) {
				br := bufio.NewReader(client)
				req, rerr := http.ReadRequest(br)
				if rerr != nil {
					_ = client.Close()
					return
				}
				if req.Method != http.MethodConnect {
					_, _ = client.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
					_ = client.Close()
					return
				}
				connects.Add(1)
				upstream, derr := net.Dial("tcp", req.Host)
				if derr != nil {
					_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					_ = client.Close()
					return
				}
				_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				go func() { _, _ = io.Copy(upstream, client) }()
				_, _ = io.Copy(client, upstream)
				_ = client.Close()
				_ = upstream.Close()
			}(client)
		}
	}()

	dial, err := (Options{Proxy: "http://" + proxyLn.Addr().String()}).DialFunc()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dial(context.Background(), "tcp", origin.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, len("origin-speaking"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "origin-speaking" {
		t.Fatalf("read %q through the proxy", buf)
	}
	if connects.Load() != 1 {
		t.Fatalf("proxy saw %d CONNECT requests, want 1", connects.Load())
	}
}

func TestDialFuncReportsAProxyRefusal(t *testing.T) {
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxyLn.Close() }()
	go func() {
		for {
			c, aerr := proxyLn.Accept()
			if aerr != nil {
				return
			}
			br := bufio.NewReader(c)
			_, _ = http.ReadRequest(br)
			_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
			_ = c.Close()
		}
	}()
	dial, err := (Options{Proxy: "http://" + proxyLn.Addr().String()}).DialFunc()
	if err != nil {
		t.Fatal(err)
	}
	_, err = dial(context.Background(), "tcp", "198.51.100.7:9")
	if err == nil {
		t.Fatal("a refusing proxy should surface as an error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("the error should name the proxy's answer, got %v", err)
	}
}

func TestTLSConfigLoadsAPrivateAuthority(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "secured")
	}))
	defer ts.Close()

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, pemOf(t, ts), 0o600); err != nil {
		t.Fatal(err)
	}

	// Without the authority the handshake must fail: that is the whole point.
	client, err := (Options{}).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(ts.URL); err == nil {
		t.Fatal("an unknown authority should not verify")
	}

	trusting, err := (Options{CAFile: caPath}).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	res, err := trusting.Get(ts.URL)
	if err != nil {
		t.Fatalf("with the authority the handshake should succeed: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "secured" {
		t.Fatalf("body %q", body)
	}
}

func TestTLSConfigRejectsAMissingAuthorityFile(t *testing.T) {
	if _, err := (Options{CAFile: filepath.Join(t.TempDir(), "absent.pem")}).TLSConfig(""); err == nil {
		t.Fatal("a missing ca_file should be an error, not a silent fallback")
	}
	bad := filepath.Join(t.TempDir(), "not-a-cert.pem")
	if err := os.WriteFile(bad, []byte("this is not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Options{CAFile: bad}).TLSConfig(""); err == nil {
		t.Fatal("a ca_file without a certificate should be an error")
	}
}

func TestTLSConfigMinimumVersion(t *testing.T) {
	cfg, err := (Options{}).TLSConfig("relay.example")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want at least TLS 1.2", cfg.MinVersion)
	}
	if cfg.ServerName != "relay.example" {
		t.Fatalf("ServerName = %q", cfg.ServerName)
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("verification must be on unless the caller opts out")
	}
}

func TestHTTPClientHasNoWholeRequestDeadline(t *testing.T) {
	// A swarm response can stream for as long as a turn lasts, so a client
	// timeout would cut exactly the traffic this package exists to carry.
	client, err := (Options{}).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 0 {
		t.Fatalf("client.Timeout = %v, want it unset", client.Timeout)
	}
}

func TestDialTimeoutDefaults(t *testing.T) {
	if got := (Options{}).dialTimeout(); got != 15*time.Second {
		t.Fatalf("default dial timeout = %v", got)
	}
	if got := (Options{DialTimeout: time.Second}).dialTimeout(); got != time.Second {
		t.Fatalf("explicit dial timeout = %v", got)
	}
}

func pemOf(t *testing.T, ts *httptest.Server) []byte {
	t.Helper()
	cert := ts.Certificate()
	if cert == nil {
		t.Fatal("test server has no certificate")
	}
	return pemEncode(cert.Raw)
}

func pemEncode(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
