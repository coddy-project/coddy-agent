package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestGoalJudgeKimiIdentityInput(t *testing.T) {
	home := t.TempDir()
	cli := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", cli)
	file := filepath.Join(cli, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(fmt.Sprintf(`{"access_token":"other-cli","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", Mode: "subscription", Region: "china", KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Proxy: "none"}
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{p}, Models: []config.ModelEntry{{Model: "kimi/model"}}, Supervisor: config.Supervisor{Model: "kimi/model"}}
	rm, err := cfg.ResolveLLM("kimi/model")
	if err != nil {
		t.Fatal(err)
	}
	in := goalJudgeProviderInput(cfg, &State{}, rm)
	if in.KimiIdentity != p.KimiIdentity || in.AuthPath != config.ProviderConfigAuthPath(home, p) || in.Region != "china" || in.ProxyURL != "none" {
		t.Fatalf("incorrect Kimi routing: %+v", in)
	}
}
