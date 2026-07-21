package skills

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
)

func TestParseSource(t *testing.T) {
	tests := []struct {
		in      string
		kind    string
		url     string
		ref     string
		wantErr bool
	}{
		{in: "EvilFreelancer/rpa-skills", kind: "git", url: "https://github.com/EvilFreelancer/rpa-skills"},
		{in: "owner/repo@v1.2", kind: "git", url: "https://github.com/owner/repo", ref: "v1.2"},
		{in: "https://github.com/owner/repo", kind: "git", url: "https://github.com/owner/repo"},
		{in: "https://github.com/owner/repo.git", kind: "git", url: "https://github.com/owner/repo.git"},
		{in: "git@github.com:owner/repo.git", kind: "git", url: "git@github.com:owner/repo.git"},
		{in: "https://example.com/skills/marketplace.json", kind: "api", url: "https://example.com/skills/marketplace.json"},
		{in: "https://api.example.com/v1/skills", kind: "api", url: "https://api.example.com/v1/skills"},
		{in: "not a source", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseSource(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSource(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSource(%q): %v", tt.in, err)
			continue
		}
		if got.kind != tt.kind || got.url != tt.url || got.ref != tt.ref {
			t.Errorf("parseSource(%q) = %+v, want kind=%s url=%s ref=%s", tt.in, got, tt.kind, tt.url, tt.ref)
		}
	}
}

func TestPluginSourceUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		json string
		want PluginSource
	}{
		{
			name: "github object",
			json: `{"source":"github","repo":"EvilFreelancer/rpa-init"}`,
			want: PluginSource{Kind: "github", Repo: "EvilFreelancer/rpa-init"},
		},
		{
			name: "url object with ref",
			json: `{"source":"url","url":"https://github.com/x/y","ref":"main"}`,
			want: PluginSource{Kind: "url", URL: "https://github.com/x/y", Ref: "main"},
		},
		{
			name: "string relative path",
			json: `"./plugins/docx-contracts"`,
			want: PluginSource{Kind: "path", Path: "./plugins/docx-contracts"},
		},
		{
			name: "string git url",
			json: `"https://github.com/x/y.git"`,
			want: PluginSource{Kind: "url", URL: "https://github.com/x/y.git"},
		},
		{
			name: "object missing source keyword infers github",
			json: `{"repo":"a/b"}`,
			want: PluginSource{Kind: "github", Repo: "a/b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ps PluginSource
			if err := ps.UnmarshalJSON([]byte(tt.json)); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if ps != tt.want {
				t.Errorf("got %+v, want %+v", ps, tt.want)
			}
		})
	}
}

// writeSkill creates dir/SKILL.md with the given frontmatter name.
func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test skill " + name + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocateSkillDirsLayouts(t *testing.T) {
	tests := []struct {
		name  string
		build func(root string)
		want  []string
	}{
		{
			name:  "root SKILL.md (avito/yookassa, no manifest)",
			build: func(root string) { writeSkill(t, root, "avito-api") },
			want:  []string{"avito-api"},
		},
		{
			name:  "skills/<name> (openclaw)",
			build: func(root string) { writeSkill(t, filepath.Join(root, "skills", "bitrix24"), "bitrix24") },
			want:  []string{"bitrix24"},
		},
		{
			name:  ".claude/skills/<name> (cc-1c)",
			build: func(root string) { writeSkill(t, filepath.Join(root, ".claude", "skills", "cf-edit"), "cf-edit") },
			want:  []string{"cf-edit"},
		},
		{
			name: "plugins/<p>/skills/<s> (polyakov)",
			build: func(root string) {
				writeSkill(t, filepath.Join(root, "plugins", "docx", "skills", "docx-contracts"), "docx-contracts")
			},
			want: []string{"docx-contracts"},
		},
		{
			name: "duplicate root + nested collapses to one (ru-text)",
			build: func(root string) {
				writeSkill(t, root, "ru-text")
				writeSkill(t, filepath.Join(root, "skills", "ru-text"), "ru-text")
			},
			want: []string{"ru-text"},
		},
		{
			name: "multiple skills in one monorepo",
			build: func(root string) {
				writeSkill(t, filepath.Join(root, "skills", "a"), "a")
				writeSkill(t, filepath.Join(root, "skills", "b"), "b")
			},
			want: []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.build(root)
			hits := locateSkillDirs(root)
			var got []string
			for _, h := range hits {
				got = append(got, h.name)
			}
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestLocateSkillDirsPrefersNestedForDuplicate(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "ru-text")
	nested := filepath.Join(root, "skills", "ru-text")
	writeSkill(t, nested, "ru-text")

	hits := locateSkillDirs(root)
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(hits))
	}
	if hits[0].dir != nested {
		t.Errorf("want nested dir %q, got %q", nested, hits[0].dir)
	}
}

func TestCopySkillDirExcludesGitCopiesResources(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, src, "demo")
	// sibling resources that must travel
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "scripts", "run.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .git that must NOT travel
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "demo")
	if err := copySkillDir(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "scripts", "run.py")); err != nil {
		t.Errorf("scripts/run.py not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should be excluded, stat err=%v", err)
	}
}

func TestInstallFromDirWritesManagedDirAndLock(t *testing.T) {
	// Simulate a monorepo clone with two skills.
	clone := t.TempDir()
	writeSkill(t, filepath.Join(clone, "skills", "one"), "one")
	writeSkill(t, filepath.Join(clone, "skills", "two"), "two")

	managed := t.TempDir()
	lock := map[string]RemoteEntry{}
	res := &SyncResult{}
	entry := RemoteEntry{Source: "owner/repo", Repo: "https://github.com/owner/repo"}

	if err := installFromDir(clone, entry, managed, lock, res); err != nil {
		t.Fatalf("installFromDir: %v", err)
	}
	for _, name := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(managed, name, "SKILL.md")); err != nil {
			t.Errorf("skill %q not materialized: %v", name, err)
		}
		if _, ok := lock[name]; !ok {
			t.Errorf("lock missing entry for %q", name)
		}
	}
	if len(res.Added) != 2 {
		t.Errorf("want 2 added, got %v", res.Added)
	}
}

// TestSyncFromLocalMarketplaceGit exercises the full pipeline against a real
// local git repo: clone → marketplace manifest with a relative ("path") plugin
// → locate nested SKILL.md → copy into ManagedDir → lockfile. Git-gated.
func TestSyncFromLocalMarketplaceGit(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	// Build a marketplace monorepo: manifest points at ./plugins/demo, whose
	// skill lives nested at plugins/demo/skills/demo/SKILL.md (polyakov layout).
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, "plugins", "demo", "skills", "demo"), "demo")
	if err := os.MkdirAll(filepath.Join(repo, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"m","plugins":[{"name":"demo","source":"./plugins/demo"}]}`
	if err := os.WriteFile(filepath.Join(repo, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("-c", "user.email=c@t", "-c", "user.name=c", "add", ".")
	git("-c", "user.email=c@t", "-c", "user.name=c", "commit", "-m", "init")

	home := t.TempDir()
	fileURL := "file://" + filepath.ToSlash(repo)
	cfg := &config.Config{
		Paths:  config.Paths{Home: home},
		Skills: config.Skills{Sources: []string{fileURL}},
	}

	res, err := Sync(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", res.Failed)
	}
	managed := cfg.Skills.ManagedDir(home)
	if _, err := os.Stat(filepath.Join(managed, "demo", "SKILL.md")); err != nil {
		t.Fatalf("skill not materialized: %v", err)
	}
	if _, ok := readRemoteLock(managed)["demo"]; !ok {
		t.Fatalf("lockfile missing demo entry")
	}
}

func TestRemoteLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	lock := map[string]RemoteEntry{
		"foo": {Source: "a/b", Repo: "https://github.com/a/b", Ref: "main"},
	}
	if err := writeRemoteLock(dir, lock); err != nil {
		t.Fatal(err)
	}
	got := readRemoteLock(dir)
	if got["foo"].Repo != "https://github.com/a/b" || got["foo"].Ref != "main" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestAddSourceAndRemoveRemote(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, ConfigPath: cfgPath}}

	added, err := AddSource(cfg, "owner/repo")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if !added {
		t.Fatal("expected source added")
	}
	// idempotent
	added2, err := AddSource(cfg, "owner/repo")
	if err != nil || added2 {
		t.Fatalf("expected no-op second add, added=%v err=%v", added2, err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "owner/repo") {
		t.Errorf("config not persisted with source: %s", data)
	}

	// RemoveRemote only removes installed (locked) skills.
	managed := cfg.Skills.ManagedDir(home)
	writeSkill(t, filepath.Join(managed, "demo"), "demo")
	if err := writeRemoteLock(managed, map[string]RemoteEntry{"demo": {Source: "owner/repo"}}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRemote(cfg, "demo"); err != nil {
		t.Fatalf("RemoveRemote: %v", err)
	}
	if _, err := os.Stat(filepath.Join(managed, "demo")); !os.IsNotExist(err) {
		t.Errorf("skill dir should be removed, err=%v", err)
	}
	if _, ok := readRemoteLock(managed)["demo"]; ok {
		t.Errorf("lock entry should be removed")
	}
	// removing a non-remote skill errors
	if err := RemoveRemote(cfg, "not-there"); err == nil {
		t.Errorf("expected error removing unknown remote skill")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.2.0", "1.10.0", -1}, // numeric, not lexical
		{"v1.2.3", "1.2.3", 0},  // leading v ignored
		{"1.0", "1.0.0", 0},     // missing fields treated as zero
		{"1.0.1", "1.0", 1},
		{"1.0.0-rc1", "1.0.0", 0}, // prerelease suffix ignored for core compare
		{"2.0.0", "1.9.9", 1},
		{"abc", "abd", -1},    // non-numeric lexical fallback
		{"1.0.0", "1.0.0a", 0}, // "1.0.0a" not numeric -> both stripped compare "1.0.0" vs "1.0.0a"? see note
	}
	for _, tc := range tests {
		got := compareVersions(tc.a, tc.b)
		// last case: "1.0.0" vs "1.0.0a": a is numeric, b is not -> lexical fallback compares "1.0.0" < "1.0.0a"
		if tc.a == "1.0.0" && tc.b == "1.0.0a" {
			if got >= 0 {
				t.Errorf("compareVersions(%q,%q)=%d, want <0", tc.a, tc.b, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("compareVersions(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestInstalledVersionPrecedence(t *testing.T) {
	remote := map[string]RemoteEntry{"synced": {Source: "owner/repo", Version: "1.2.3"}}
	sk := &Skill{Name: "synced", Version: "9.9.9"}
	if got := InstalledVersion(remote, "synced", sk); got != "1.2.3" {
		t.Errorf("lock version should win: got %q", got)
	}
	// Falls back to frontmatter when not in the lock.
	local := &Skill{Name: "local", Version: "0.4.0"}
	if got := InstalledVersion(remote, "local", local); got != "0.4.0" {
		t.Errorf("frontmatter fallback: got %q", got)
	}
	// Empty when neither known.
	if got := InstalledVersion(remote, "bare", &Skill{Name: "bare"}); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	// Lock entry with empty version falls through to frontmatter.
	remote2 := map[string]RemoteEntry{"x": {Source: "s"}}
	if got := InstalledVersion(remote2, "x", &Skill{Name: "x", Version: "3.0.0"}); got != "3.0.0" {
		t.Errorf("empty lock version should fall back: got %q", got)
	}
}

func TestMarketplaceVersionsOmitsEmpty(t *testing.T) {
	mf := &Marketplace{Plugins: []MarketplacePlugin{
		{Name: "a", Version: "1.0.0"},
		{Name: "b"}, // no version -> omitted (no false-positive updates)
		{Name: "c", Version: "2.1.0"},
	}}
	got := marketplaceVersions(mf)
	if got["a"] != "1.0.0" || got["c"] != "2.1.0" {
		t.Errorf("unexpected versions: %v", got)
	}
	if _, ok := got["b"]; ok {
		t.Errorf("version-less plugin should be omitted: %v", got)
	}
}

func TestListSourcesAndRemoveSource(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources:\n    - owner/one\n    - owner/two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, ConfigPath: cfgPath},
		Skills: config.Skills{Sources: []string{"owner/one", " ", "owner/two"}},
	}

	// ListSources trims blanks.
	got := ListSources(cfg)
	if len(got) != 2 || got[0] != "owner/one" || got[1] != "owner/two" {
		t.Fatalf("ListSources = %v", got)
	}

	// Removing an unknown source is a no-op (removed=false, no error).
	removed, err := RemoveSource(cfg, "owner/missing")
	if err != nil || removed {
		t.Fatalf("remove unknown: removed=%v err=%v", removed, err)
	}

	// Empty source is an error.
	if _, err := RemoveSource(cfg, "  "); err == nil {
		t.Fatal("expected error for empty source")
	}

	// Case-insensitive match, persisted to disk.
	removed, err = RemoveSource(cfg, "OWNER/ONE")
	if err != nil || !removed {
		t.Fatalf("remove existing: removed=%v err=%v", removed, err)
	}
	if got := ListSources(cfg); len(got) != 1 || got[0] != "owner/two" {
		t.Errorf("after remove ListSources = %v", got)
	}
	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "owner/one") {
		t.Errorf("removed source still on disk: %s", data)
	}
}

// gitCommitAllRepo initializes (if needed) and commits everything in repo.
func gitCommitAllRepo(t *testing.T, repo string, init bool, msg string) {
	t.Helper()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if init {
		git("init", "-b", "main")
	}
	git("-c", "user.email=c@t", "-c", "user.name=c", "add", "-A")
	git("-c", "user.email=c@t", "-c", "user.name=c", "commit", "-m", msg)
}

func writeMarketplaceManifest(t *testing.T, repo, skill, version string) {
	t.Helper()
	writeSkill(t, filepath.Join(repo, "skills", skill), skill)
	if err := os.MkdirAll(filepath.Join(repo, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"m","metadata":{"version":"` + version + `"},"plugins":[` +
		`{"name":"` + skill + `","source":"./skills/` + skill + `","version":"` + version + `"}]}`
	if err := os.WriteFile(filepath.Join(repo, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncRecordsVersionThenCheckAndUpdate(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	repo := t.TempDir()
	writeMarketplaceManifest(t, repo, "demo", "1.0.0")
	gitCommitAllRepo(t, repo, true, "v1")

	home := t.TempDir()
	fileURL := "file://" + filepath.ToSlash(repo)
	cfg := &config.Config{
		Paths:  config.Paths{Home: home},
		Skills: config.Skills{Sources: []string{fileURL}},
	}

	if _, err := Sync(context.Background(), cfg); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	managed := cfg.Skills.ManagedDir(home)
	if ent := readRemoteLock(managed)["demo"]; ent.Version != "1.0.0" {
		t.Fatalf("lock version = %q, want 1.0.0", ent.Version)
	}

	// No update available right after install.
	ups, err := CheckUpdates(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CheckUpdates: %v", err)
	}
	if len(ups) != 1 || ups[0].Name != "demo" || ups[0].UpdateAvailable {
		t.Fatalf("unexpected updates: %+v", ups)
	}

	// Publish a newer version upstream.
	writeMarketplaceManifest(t, repo, "demo", "2.0.0")
	gitCommitAllRepo(t, repo, false, "v2")

	ups, err = CheckUpdates(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CheckUpdates 2: %v", err)
	}
	if len(ups) != 1 || !ups[0].UpdateAvailable || ups[0].Latest != "2.0.0" {
		t.Fatalf("expected update to 2.0.0: %+v", ups)
	}

	// Applying the update installs it and clears the flag.
	if _, err := UpdateSkill(context.Background(), cfg, "demo"); err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	if ent := readRemoteLock(managed)["demo"]; ent.Version != "2.0.0" {
		t.Fatalf("post-update lock version = %q, want 2.0.0", ent.Version)
	}

	// Updating a non-remote skill errors.
	if _, err := UpdateSkill(context.Background(), cfg, "not-installed"); err == nil {
		t.Error("expected error updating unknown skill")
	}
}
