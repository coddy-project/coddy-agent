package configapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// One live configuration keeps one revision; a revision this process never issued -
// from before a restart, or made up - names nothing, and neither does one older than
// the configurations kept, so the save falls back to the live configuration.
func TestRevisions(t *testing.T) {
	served := NewRevisions()
	first := &config.Config{}
	rev := served.Revision(first)
	if again := served.Revision(first); again != rev {
		t.Fatalf("one configuration got two revisions: %q and %q", rev, again)
	}
	if got := served.Lookup(rev); got != first {
		t.Fatalf("lookup(%q) = %p, want the configuration served under it", rev, got)
	}
	if got := NewRevisions().Lookup(rev); got != nil {
		t.Fatalf("another process resolved %q", rev)
	}
	for _, unknown := range []string{"", "made-up", rev + "0"} {
		if got := served.Lookup(unknown); got != nil {
			t.Fatalf("lookup(%q) resolved to %p", unknown, got)
		}
	}
	for i := 0; i < RevisionsKept; i++ {
		served.Revision(&config.Config{})
	}
	if got := served.Lookup(rev); got != nil {
		t.Fatalf("a revision older than the %d kept still resolves", RevisionsKept)
	}
}

// setPath writes one dot path of a JSON document: keys, array indices and
// `+` for an append, inventing objects but never walking through a scalar.
func TestSetPath(t *testing.T) {
	doc := map[string]any{
		"providers": []any{map[string]any{"name": "openai"}},
		"agent":     map[string]any{"model": "openai/gpt-5"},
	}
	if _, err := setPath(doc, []string{"providers", "+"}, map[string]any{"name": "neuraldeep"}); err != nil {
		t.Fatalf("append: %s", err)
	}
	providers := doc["providers"].([]any)
	if len(providers) != 2 || providers[1].(map[string]any)["name"] != "neuraldeep" {
		t.Fatalf("append landed wrong: %#v", providers)
	}
	if _, err := setPath(doc, []string{"models", "+"}, map[string]any{"model": "neuraldeep/qwen3.6-35b-a3b"}); err != nil {
		t.Fatalf("append to a missing array: %s", err)
	}
	if models, ok := doc["models"].([]any); !ok || len(models) != 1 {
		t.Fatalf("missing array was not created: %#v", doc["models"])
	}
	if _, err := setPath(doc, []string{"agent", "model"}, "neuraldeep/qwen3.6-35b-a3b"); err != nil {
		t.Fatalf("overwrite: %s", err)
	}
	if doc["agent"].(map[string]any)["model"] != "neuraldeep/qwen3.6-35b-a3b" {
		t.Fatalf("overwrite landed wrong: %#v", doc["agent"])
	}
	if _, err := setPath(doc, []string{"ui", "locale"}, "ru"); err != nil {
		t.Fatalf("nested create: %s", err)
	}
	if doc["ui"].(map[string]any)["locale"] != "ru" {
		t.Fatalf("nested create landed wrong: %#v", doc["ui"])
	}
	if _, err := setPath(doc, []string{"providers", "0", "name"}, "renamed"); err != nil {
		t.Fatalf("index write: %s", err)
	}
	if providers := doc["providers"].([]any); providers[0].(map[string]any)["name"] != "renamed" {
		t.Fatalf("index write landed wrong: %#v", providers)
	}
	if _, err := setPath(doc, []string{"agent", "model", "x"}, 1); err == nil {
		t.Fatal("a scalar was walked through")
	}
	if _, err := setPath(doc, []string{"providers", "+", "name"}, 1); err == nil {
		t.Fatal("+ addressed a non-final segment")
	}
	if _, err := setPath(doc, []string{"agent", "+"}, 1); err == nil {
		t.Fatal("+ appended to an object")
	}
	if _, err := setPath(doc, []string{"providers", "nope"}, 1); err == nil {
		t.Fatal("a non-index addressed an array")
	}
}

// ApplyPath writes one dot path through the save transaction a settings save
// runs under: the file keeps the operator's comments, the change lands, and
// the new configuration is installed.
func TestApplyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	source := "# the operator's own comment stays\nproviders: []\n"
	if err := os.WriteFile(cfgPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{ConfigPath: cfgPath, Home: dir}
	base, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	installed := 0
	backend := &Backend{
		Live: func() *config.Config { return base },
		Install: func(c *config.Config) error {
			installed++
			base = c
			return nil
		},
		Schema: func() ([]byte, error) { return []byte("{}"), nil },
	}
	if err := backend.ApplyPath("providers.+", json.RawMessage(`{"name":"neuraldeep","type":"neuraldeep","api_key":"sk-test"}`)); err != nil {
		t.Fatalf("append provider: %s", err)
	}
	if err := backend.ApplyPath("models.+", json.RawMessage(`{"model":"neuraldeep/qwen3.6-35b-a3b"}`)); err != nil {
		t.Fatalf("append model: %s", err)
	}
	if installed != 2 {
		t.Fatalf("installed %d configurations, want one per write", installed)
	}
	if len(base.Providers) != 1 || base.Providers[0].Name != "neuraldeep" || base.Providers[0].APIKey != "sk-test" {
		t.Fatalf("provider landed wrong: %#v", base.Providers)
	}
	if len(base.Models) != 1 || base.Models[0].Model != "neuraldeep/qwen3.6-35b-a3b" {
		t.Fatalf("model landed wrong: %#v", base.Models)
	}
	written, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(written, []byte("# the operator's own comment stays")) {
		t.Fatalf("a save dropped the operator's comment:\n%s", written)
	}
	// A path that walks through a scalar is a parse refusal the client sees.
	if err := backend.ApplyPath("providers.0.name.x", json.RawMessage(`1`)); !ConfigBadRequest(err) {
		t.Fatalf("scalar walk: %v", err)
	}
}
