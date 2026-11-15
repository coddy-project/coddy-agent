//go:build http

package httpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestDescribeFactoryUsesKimiManagedIdentity(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi credential store unavailable")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	id := "0123456789abcdef0123456789abcdef"
	dir := config.KimiIdentityDir(home, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kimi-global-auth.json"), []byte(`{"access_token":"","generation":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", KimiIdentity: id}}, Models: []config.ModelEntry{{Model: "kimi/example"}}}
	cfg.Agent.Model = "kimi/example"
	for name, factory := range map[string]func() (llm.Provider, error){
		"selected": func() (llm.Provider, error) { return defaultMakeLLMFromYAML(cfg, "kimi/example", llm.RequestOptions{}) },
		"default":  func() (llm.Provider, error) { return defaultProviderFromAgentModel(cfg) },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := factory()
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Complete(context.Background(), nil, nil)
			if !errors.Is(err, llm.ErrKimiCredentialUnauthorized) {
				t.Fatalf("managed identity not used: %v", err)
			}
		})
	}
}
