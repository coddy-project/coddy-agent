//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// TestCoddySkillsInstallThatInstallsNothingIsAnError: POST /coddy/skills/install
// of a plugin the installer refuses (here one whose folder holds no SKILL.md)
// answers 400 with the reason, not 200 with ok:true, so the Settings screen does
// not report an install that did not happen.
func TestCoddySkillsInstallThatInstallsNothingIsAnError(t *testing.T) {
	offlineSystemSources(t)
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"shop","plugins":[{"name":"empty","source":"./plugins/empty"}]}`
	if err := os.WriteFile(filepath.Join(repo, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "plugins", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "plugins", "empty", "README.md"), []byte("no skill here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@t.io", "-c", "user.name=t", "commit", "-q", "-m", "publish"}} {
		if err := bddGit(repo, args...); err != nil {
			t.Fatal(err)
		}
	}

	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  auto_discovery: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"source": "file://" + filepath.ToSlash(repo), "plugin": "empty"})
	res, err := http.Post(ts.URL+"/coddy/skills/install", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("install of a plugin with no skill: status %d %s, want 400", res.StatusCode, out)
	}
	if !strings.Contains(string(out), "no SKILL.md") {
		t.Errorf("body %s does not say why", out)
	}
}

// TestCoddySkillsSyncOfAnAddedMarketplaceInstallsNothingNew: POST
// /coddy/skills/sync?source= naming a marketplace added with
// `plugin marketplace add` reads its list and installs nothing, and the update
// of the plugin installed from it leaves the rest of the marketplace alone.
func TestCoddySkillsSyncOfAnAddedMarketplaceInstallsNothingNew(t *testing.T) {
	offlineSystemSources(t)
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"shop","plugins":[{"name":"one","source":"./skills/one"},{"name":"two","source":"./skills/two"}]}`
	if err := os.WriteFile(filepath.Join(repo, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		dir := filepath.Join(repo, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: d\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@t.io", "-c", "user.name=t", "commit", "-q", "-m", "publish"}} {
		if err := bddGit(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	market := "file://" + filepath.ToSlash(repo)

	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  auto_discovery: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, words := range [][]string{{"marketplace", "add", market}, {"install", "one@shop"}} {
		if _, err := skills.RunPluginCommand(context.Background(), cfg, home, words); err != nil {
			t.Fatalf("plugin %v: %v", words, err)
		}
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/coddy/skills/sync?source="+market, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sync status %d %s", res.StatusCode, out)
	}
	var check struct {
		Read    []string `json:"read"`
		Updates []any    `json:"updates"`
	}
	if err := json.Unmarshal(out, &check); err != nil {
		t.Fatal(err)
	}
	if strings.Join(check.Read, ",") != "shop" || check.Updates == nil || len(check.Updates) != 0 {
		t.Fatalf("sync of the added marketplace = %s, want shop read and no update", out)
	}
	managed := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if _, err := os.Stat(filepath.Join(managed, "two")); !os.IsNotExist(err) {
		t.Fatalf("the read installed two: %v", err)
	}

	res, err = http.Post(ts.URL+"/coddy/skills/one/update", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	out, _ = ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update status %d %s", res.StatusCode, out)
	}
	var body struct {
		Added   []string `json:"added"`
		Updated []string `json:"updated"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Added) != 0 || strings.Join(body.Updated, ",") != "one" {
		t.Fatalf("update of one = %s, want one updated and nothing added", out)
	}
	if _, err := os.Stat(filepath.Join(managed, "two")); !os.IsNotExist(err) {
		t.Fatalf("the update installed two: %v", err)
	}
}

// TestCoddySkillsAddingADeclaredSourceInstallsNothing: the add field of
// Settings sends sync:true, but only a source the call declares is installed.
// A source declared already, or the built-in catalogue, is not installed
// again: an add is not the explicit install that brings back the skills the
// operator removed.
func TestCoddySkillsAddingADeclaredSourceInstallsNothing(t *testing.T) {
	offlineSystemSources(t)
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	repo := t.TempDir()
	dir := filepath.Join(repo, "skills", "one")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: one\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@t.io", "-c", "user.name=t", "commit", "-q", "-m", "publish"}} {
		if err := bddGit(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	source := "file://" + filepath.ToSlash(repo)

	home := t.TempDir()
	t.Setenv("CODDY_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  auto_discovery: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteMarketplacesFile(config.GlobalMarketplacesPath(home), config.MarketplacesFile{Sources: []string{source}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	skills.SystemSources = []config.DeclaredMarketplace{{Name: config.SystemSkillsMarketplace, Source: config.SystemSkillsSource}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, src := range []string{source, config.SystemSkillsSource} {
		body := strings.NewReader(`{"source":` + strconv.Quote(src) + `,"sync":true}`)
		res, err := http.Post(ts.URL+"/coddy/skills/sources", "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := ioReadAllClose(res.Body)
		if res.StatusCode != http.StatusOK || !strings.Contains(string(out), `"added":false`) || strings.Contains(string(out), `"sync"`) {
			t.Fatalf("adding %s again = %d %s, want added:false and no install", src, res.StatusCode, out)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Skills.ManagedDir(home), "one")); !os.IsNotExist(err) {
		t.Fatalf("adding a declared source installed it: %v", err)
	}
}
