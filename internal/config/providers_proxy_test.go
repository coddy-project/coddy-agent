package config_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestProviderConfigValidateProxy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		proxy   string
		wantErr bool
	}{
		{name: "empty", proxy: "", wantErr: false},
		{name: "http", proxy: "http://127.0.0.1:8080", wantErr: false},
		{name: "https", proxy: "https://proxy.example:8443", wantErr: false},
		{name: "socks5", proxy: "socks5://127.0.0.1:1080", wantErr: false},
		{name: "socks5h", proxy: "socks5h://127.0.0.1:1080", wantErr: false},
		{name: "socks4", proxy: "socks4://127.0.0.1:1080", wantErr: true},
		{name: "ftp", proxy: "ftp://127.0.0.1:21", wantErr: true},
		{name: "no_scheme", proxy: "127.0.0.1:8080", wantErr: true},
		{name: "no_host", proxy: "http://", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := config.ProviderConfig{Name: "p", Type: "openai", Proxy: tt.proxy}
			p.Normalize()
			err := p.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}
