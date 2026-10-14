//go:build swarm

package swarm

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestServeUsesConfiguredCertificateAndKey(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	cert := fixture.TLS.Certificates[0]
	client := fixture.Client()
	fixture.Close()
	der, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	for file, data := range map[string][]byte{
		certFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}),
		keyFile:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
	} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = "test-client-token"
	cfg.Swarm.TLS = config.SwarmTLSConfig{CertFile: certFile, KeyFile: keyFile}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ListenAddr: addr, Home: dir})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("relay did not stop")
		}
	})
	client.Timeout = time.Second
	t.Cleanup(client.CloseIdleConnections)
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := client.Get("https://" + addr + "/swarm/info")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK || res.TLS == nil {
				t.Fatalf("HTTPS relay: status %d, TLS %v", res.StatusCode, res.TLS)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("configured HTTPS listener did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _ := get(t, client, "https://"+addr, "/swarm/nodes", ""); code != http.StatusUnauthorized {
		t.Errorf("HTTPS without token: %d", code)
	}
	if code, _ := get(t, client, "https://"+addr, "/swarm/nodes", cfg.Swarm.AuthToken); code != http.StatusOK {
		t.Errorf("HTTPS with token: %d", code)
	}
}

func TestServeRejectsMissingTLSFiles(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.AuthToken = "test-client-token"
	cfg.Swarm.TLS = config.SwarmTLSConfig{CertFile: filepath.Join(t.TempDir(), "missing.pem"), KeyFile: "missing-key.pem"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := Serve(ctx, Options{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ListenAddr: "127.0.0.1:0", Home: t.TempDir()})
	if err == nil {
		t.Fatal("missing TLS files must not fall back to HTTP")
	}
}
