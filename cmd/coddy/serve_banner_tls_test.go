package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func TestServeBannerUsesRelayTLSScheme(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tls    config.SwarmTLSConfig
		scheme string
	}{
		{name: "plain", scheme: "http"},
		{name: "certificate and key", tls: config.SwarmTLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"}, scheme: "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}()
			previous := os.Stderr
			os.Stderr = file
			defer func() { os.Stderr = previous }()
			cfg := &config.Config{}
			cfg.Swarm.TLS = tc.tls
			printServeBanner(cfg, []serve.Subsystem{{Kind: serve.KindHTTP}, {Kind: serve.KindSwarm}}, "localhost:12345", "localhost:12346", false, false)
			data, err := os.ReadFile(file.Name())
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "swarm       "+tc.scheme+"://localhost:12346") {
				t.Errorf("relay scheme in banner: %s", text)
			}
			if !strings.Contains(text, "httpserver  http://localhost:12345") {
				t.Errorf("relay TLS must not change the agent listener: %s", text)
			}
		})
	}
}
