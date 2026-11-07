package providerlogin

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiCatalogPreservesAndRejectsReplacedIdentity(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	data := "providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: " + id + "\n  - name: other\n    type: openai\n    api_key: synthetic-key\nmodels:\n  - model: other/existing\nagent:\n  model: other/existing\n"
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	p := *cfg.FindProvider("kimi")
	image := true
	models := []llm.KimiModel{{ID: "k2", Protocol: "anthropic", ContextWindow: 131072, ImageInput: &image, ReasoningLevels: []string{"low", "high"}, DefaultReasoning: "high"}}
	added, err := applyKimiCatalog(context.Background(), cfg.Paths, p, models)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 {
		t.Fatalf("added: %v", added)
	}
	got, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	m := got.FindModelEntry("kimi/k2")
	if m == nil || m.KimiMetadata == nil || m.KimiMetadata.Model != "kimi/k2" || m.KimiMetadata.Region != "global" || m.KimiMetadata.Mode != "subscription" || m.KimiMetadata.Protocol != "anthropic" || m.MaxContextTokens != 131072 || !m.Multimodal || m.ReasoningLevels == nil || m.ReasoningDefault != "high" {
		t.Fatalf("bad model: %+v", m)
	}
	if got.Agent.Model != "other/existing" || got.FindProvider("other") == nil {
		t.Fatal("existing config changed")
	}
	added, err = applyKimiCatalog(context.Background(), cfg.Paths, p, models)
	if err != nil || len(added) != 0 {
		t.Fatalf("second apply %v %v", added, err)
	}
	other, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(strings.Replace(string(raw), id, other, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := applyKimiCatalog(context.Background(), cfg.Paths, p, models); err == nil {
		t.Fatal("replaced provider accepted")
	}
}

func TestKimiCatalogRepairsUnavailableDefault(t *testing.T) {
	t.Setenv("OTHER_API_KEY", "")
	cases := []struct{ name, other, entries, current, want string }{
		{"provider without credentials", "  - name: other\n    type: openai\n", "  - model: other/old\n", "other/old", "kimi/first"},
		{"missing model on ready provider", "  - name: other\n    type: openai\n    api_key: synthetic\n", "  - model: other/existing\n", "other/missing", "kimi/first"},
		{"missing model with empty list", "  - name: other\n    type: openai\n    api_key: synthetic\n", "", "other/missing", "kimi/first"},
		{"ready provider model", "  - name: other\n    type: openai\n    api_key: synthetic\n", "  - model: other/existing\n", "other/existing", "other/existing"},
		{"current Kimi model listed", "", "  - model: kimi/second\n", "kimi/second", "kimi/second"},
		{"current Kimi model not listed", "", "  - model: kimi/gone\n", "kimi/gone", "kimi/first"},
		{"current Kimi model stale metadata", "", "  - model: kimi/second\n    kimi_metadata:\n      model: kimi/second\n      mode: subscription\n      region: china\n      protocol: kimi\n", "kimi/second", "kimi/first"},
		{"first listed incompatible metadata", "", "  - model: kimi/first\n    kimi_metadata:\n      model: kimi/first\n      mode: subscription\n      region: china\n      protocol: kimi\n", "kimi/first", "kimi/second"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cfg := freshConfig(home)
			id, err := config.NewKimiIdentity()
			if err != nil {
				t.Fatal(err)
			}
			modelBlock := "models:\n" + tc.entries
			if tc.entries == "" {
				modelBlock = "models: []\n"
			}
			data := "providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: " + id + "\n" + tc.other + modelBlock + "agent:\n  model: " + tc.current + "\n"
			if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: id}
			models := []llm.KimiModel{{ID: "unsupported", Protocol: "unknown"}, {ID: "first", Protocol: "kimi"}, {ID: "second", Protocol: "anthropic"}}
			if tc.name == "current Kimi model stale metadata" {
				models[2].Protocol = "kimi"
			}
			if _, err := applyKimiCatalog(context.Background(), cfg.Paths, p, models); err != nil {
				t.Fatal(err)
			}
			saved, err := config.LoadWithPaths(cfg.Paths)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Agent.Model != tc.want {
				t.Fatalf("default = %q, want %q", saved.Agent.Model, tc.want)
			}
		})
	}
}

func TestKimiCatalogSnapshotDoesNotRestoreBackup(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	original := "providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: " + id + "\nmodels:\n  - model: kimi/existing\nagent:\n  model: kimi/missing\n"
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BackupPath(cfg.Paths.ConfigPath), []byte("agent:\n  model: backup\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, snapshot, err := catalogSnapshot(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Model != "kimi/missing" || string(snapshot) != original {
		t.Fatal("snapshot does not represent the current file")
	}
	after, err := os.ReadFile(cfg.Paths.ConfigPath)
	if err != nil || string(after) != original {
		t.Fatalf("config changed during read: %v", err)
	}
}

func TestKimiLoginOfflineCatalog(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := freshConfig(home)
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte("providers:\n  - name: kimi\n    type: kimi\n    region: china\n    proxy: http://proxy.example:3128\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	oldStart, oldList := kimiStartForLogin, kimiListForLogin
	t.Cleanup(func() { kimiStartForLogin, kimiListForLogin = oldStart, oldList })
	kimiStartForLogin = func(ctx context.Context, home string, p config.ProviderConfig) (*KimiLogin, error) {
		if p.Region != "china" || p.Proxy != "http://proxy.example:3128" {
			t.Fatalf("device options: %+v", p)
		}
		store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, p.KimiIdentity))
		attempt, err := store.BeginLogin(ctx, "china")
		if err != nil {
			return nil, err
		}
		return &KimiLogin{Device: &llm.KimiDeviceLogin{URL: "https://www.kimi.com/code/authorize_device", UserCode: "TEST"}, store: store, region: "china", attempt: attempt, waitToken: func(context.Context) (llm.KimiToken, error) {
			return llm.KimiToken{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
		}}, nil
	}
	kimiListForLogin = func(ctx context.Context, mode, region, token, proxy string) ([]llm.KimiModel, error) {
		if mode != "subscription" || region != "china" || token != "synthetic" || proxy != "http://proxy.example:3128" {
			t.Fatalf("catalog options: %s %s %s %s", mode, region, token, proxy)
		}
		return []llm.KimiModel{{ID: "k2", Protocol: "kimi", ContextWindow: 131072}}, nil
	}
	result, err := Run(context.Background(), cfg, cfg.FindProvider("kimi"), Hooks{})
	if err != nil || result.ConfigErr != nil {
		t.Fatalf("login: %v config: %v", err, result.ConfigErr)
	}
	saved, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	p := saved.FindProvider("kimi")
	m := saved.FindModelEntry("kimi/k2")
	if p == nil || !config.ValidKimiIdentity(p.KimiIdentity) || m == nil || m.KimiMetadata == nil || m.KimiMetadata.Region != "china" || saved.Agent.Model != "kimi/k2" {
		t.Fatalf("missing login result: %+v %+v %q", p, m, saved.Agent.Model)
	}
}

func TestKimiCatalogSkipsUnsupportedProtocol(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	data := "providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: " + id + "\n"
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: id}
	_, err = applyKimiCatalog(context.Background(), cfg.Paths, p, []llm.KimiModel{{ID: "unknown", Protocol: "unrecognized"}, {ID: "valid", Protocol: "kimi"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.FindModelEntry("kimi/unknown") != nil || saved.Agent.Model != "kimi/valid" {
		t.Fatalf("unsupported default or model: %q", saved.Agent.Model)
	}
}

func TestKimiCatalogWithoutUsableModelsFails(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte("providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: "+id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: id}
	if _, err := applyKimiCatalog(context.Background(), cfg.Paths, p, []llm.KimiModel{{ID: "bad", Protocol: "unknown"}}); err == nil {
		t.Fatal("no usable models must be reported")
	}
}

func TestKimiCatalogRejectsChangedInputMode(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte("providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: "+id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", Mode: "platform", KimiIdentity: id}
	if _, err := applyKimiCatalog(context.Background(), cfg.Paths, p, []llm.KimiModel{{ID: "k2", Protocol: "kimi"}}); err == nil {
		t.Fatal("platform login context accepted for subscription catalog")
	}
}

func TestKimiCatalogSkipsStaleExistingDefault(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	data := "providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: " + id + "\nmodels:\n  - model: kimi/old\n    kimi_metadata:\n      model: kimi/old\n      protocol: kimi\n      mode: subscription\n      region: china\n"
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: id}
	_, err = applyKimiCatalog(context.Background(), cfg.Paths, p, []llm.KimiModel{{ID: "old", Protocol: "kimi"}, {ID: "fresh", Protocol: "kimi"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Agent.Model != "kimi/fresh" {
		t.Fatalf("default = %q", saved.Agent.Model)
	}
	if saved.FindModelEntry("kimi/old").KimiMetadata.Region != "china" {
		t.Fatal("existing entry overwritten")
	}
}

func TestKimiCatalogIdentityChangesDuringFetch(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	data := "providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: " + id + "\n"
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	p := *cfg.FindProvider("kimi")
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
	attempt, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLogin(context.Background(), "global", attempt, llm.KimiCredential{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	other, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	oldList := kimiListForLogin
	t.Cleanup(func() { kimiListForLogin = oldList })
	kimiListForLogin = func(context.Context, string, string, string, string) ([]llm.KimiModel, error) {
		if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(strings.Replace(data, id, other, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		return []llm.KimiModel{{ID: "k2", Protocol: "kimi"}}, nil
	}
	if _, err := ApplyKimiLoginCatalog(context.Background(), cfg, p); err == nil {
		t.Fatal("provider replacement accepted")
	}
	saved, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.FindModelEntry("kimi/k2") != nil {
		t.Fatal("catalog attached to replacement")
	}
	cred, state, err := store.Load("global")
	if err != nil || state != llm.KimiCredentialValid || cred.AccessToken != "synthetic" {
		t.Fatal("original credential was not retained")
	}
}

func TestKimiCatalogLogoutDuringFetch(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte("providers:\n  - name: kimi\n    type: kimi\n    kimi_identity: "+id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	p := *cfg.FindProvider("kimi")
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
	attempt, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLogin(context.Background(), "global", attempt, llm.KimiCredential{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	old := kimiListForLogin
	t.Cleanup(func() { kimiListForLogin = old })
	kimiListForLogin = func(context.Context, string, string, string, string) ([]llm.KimiModel, error) {
		if err := store.Remove("global"); err != nil {
			t.Fatal(err)
		}
		return []llm.KimiModel{{ID: "k2", Protocol: "kimi"}}, nil
	}
	if _, err := ApplyKimiLoginCatalog(context.Background(), cfg, p); err == nil {
		t.Fatal("logout during catalog accepted")
	}
	saved, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.FindModelEntry("kimi/k2") != nil {
		t.Fatal("model added after logout")
	}
}
