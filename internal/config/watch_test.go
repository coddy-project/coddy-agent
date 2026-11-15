package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const watchBaseYAML = `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096

agent:
  model: "openai/gpt-4o"
`

// watchFixture writes a config file and returns a watcher over it whose live
// configuration is whatever was last installed.
func watchFixture(t *testing.T, body string) (*FileWatcher, *string, func() *Config) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	live := cfg
	installed := ""
	w := &FileWatcher{
		Paths:       cfg.Paths,
		Live:        func() *Config { return live },
		stablePolls: 1,
		Install: func(next *Config) {
			live = next
			installed = next.Agent.Model
		},
	}
	return w, &installed, func() *Config { return live }
}

// rewrite replaces the watched file and moves its modification time forward, so
// the stat gate cannot hide a change made inside one filesystem timestamp tick.
func rewriteWatched(t *testing.T, w *FileWatcher, body string) {
	t.Helper()
	if err := os.WriteFile(w.Paths.ConfigPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(w.Paths.ConfigPath, future, future); err != nil {
		t.Fatalf("touch config: %v", err)
	}
}

// TestFileWatcherInstallsAChangedFileOnce proves the watcher reports a real edit
// and then goes quiet: a poll that installs on every tick would restart every
// subsystem the supervisor watches, forever.
func TestFileWatcherInstallsAChangedFileOnce(t *testing.T) {
	w, installed, _ := watchFixture(t, watchBaseYAML)
	if w.Poll() {
		t.Fatal("the first poll installed a configuration nobody changed")
	}
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	if !w.Poll() {
		t.Fatal("a changed file was not installed")
	}
	if *installed != "openai/gpt-4o-mini" {
		t.Fatalf("installed agent.model = %q, want openai/gpt-4o-mini", *installed)
	}
	if w.Poll() {
		t.Fatal("the same file was installed twice")
	}
}

// TestFileWatcherIgnoresAnEditThatChangesNoSetting keeps a comment, a reordered
// key or a trailing newline from restarting a gateway. The file the settings
// screen itself writes goes through this path too, so a save must not come back
// as a second reload.
func TestFileWatcherIgnoresAnEditThatChangesNoSetting(t *testing.T) {
	w, _, _ := watchFixture(t, watchBaseYAML)
	w.Poll()
	rewriteWatched(t, w, "# an operator's note\n"+watchBaseYAML+"\n")
	if w.Poll() {
		t.Fatal("a comment-only edit was installed as a configuration change")
	}
}

// TestFileWatcherKeepsTheLiveConfigWhenTheFileIsBroken is the whole point of
// reloading defensively: an operator halfway through an edit, or an editor that
// truncates before it writes, must not blank the model list of a running daemon.
func TestFileWatcherKeepsTheLiveConfigWhenTheFileIsBroken(t *testing.T) {
	w, _, live := watchFixture(t, watchBaseYAML)
	w.Poll()
	before := live()
	rewriteWatched(t, w, "providers: [ this is not yaml\n")
	if w.Poll() {
		t.Fatal("an unparsable file was installed")
	}
	if live() != before {
		t.Fatal("the live configuration was replaced by an unparsable file")
	}
	data, err := os.ReadFile(w.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "providers: [ this is not yaml\n" {
		t.Fatalf("watcher overwrote the invalid edit: %q", data)
	}
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	if !w.Poll() || live().Agent.Model != "openai/gpt-4o-mini" {
		t.Fatal("the corrected edit was not installed")
	}
	data, err = os.ReadFile(BackupPath(w.Paths.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "openai/gpt-4o-mini") {
		t.Fatalf("backup did not follow the corrected edit: %q", data)
	}
}

func TestFileWatcherDoesNotInstallEmptyManualEdit(t *testing.T) {
	w, _, live := watchFixture(t, watchBaseYAML)
	w.Poll()
	before := live()
	rewriteWatched(t, w, "# editor has truncated the file\n")
	if w.Poll() {
		t.Fatal("watcher installed an empty configuration")
	}
	if live() != before {
		t.Fatal("empty manual edit replaced the running configuration")
	}
}

func TestFileWatcherRetriesTransientAdjustmentFailure(t *testing.T) {
	w, installed, _ := watchFixture(t, watchBaseYAML)
	w.Poll()
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	attempts := 0
	w.Adjust = func(*Config) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}
	if w.Poll() {
		t.Fatal("adjustment failure installed the configuration")
	}
	if !w.Poll() || *installed != "openai/gpt-4o-mini" {
		t.Fatal("watcher did not retry the unchanged configuration")
	}
}

func TestFileWatcherRejectsLegacyKeysWithoutRewriting(t *testing.T) {
	for _, legacy := range []string{
		"mcp_servers: []\n", "skills:\n  sources: []\n",
		"tools:\n  permission_mode: ask\n", "scheduler:\n  dir: old\n",
	} {
		t.Run(strings.Fields(legacy)[0], func(t *testing.T) {
			w, _, live := watchFixture(t, watchBaseYAML)
			w.Poll()
			before := live()
			edited := strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`) + legacy
			rewriteWatched(t, w, edited)
			if w.Poll() || live() != before {
				t.Fatal("watcher installed an unmigrated configuration")
			}
			data, err := os.ReadFile(w.Paths.ConfigPath)
			if err != nil || string(data) != edited {
				t.Fatalf("watcher changed the edit: %q, %v", data, err)
			}
		})
	}
}

func TestFileWatcherRejectsLegacyKeysThroughYAMLMerge(t *testing.T) {
	for _, body := range []string{
		"defaults: &defaults\n  permission_mode: ask\ntools:\n  <<: *defaults\n",
		"defaults: &defaults\n  dir: old\nscheduler:\n  <<: *defaults\n",
		"defaults: &defaults\n  permission_mode: ask\ntools: *defaults\n",
	} {
		if err := watchedLegacyKeys([]byte(body)); err == nil {
			t.Fatalf("legacy key in YAML merge was accepted:\n%s", body)
		}
	}
}

func TestFileWatcherWaitsForAStableFileStamp(t *testing.T) {
	w, installed, _ := watchFixture(t, watchBaseYAML)
	w.stablePolls = 2
	w.Poll()
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	if w.Poll() {
		t.Fatal("watcher installed a file on its first observation")
	}
	if *installed != "" {
		t.Fatal("watcher applied a candidate before confirming stability")
	}
	if !w.Poll() || *installed != "openai/gpt-4o-mini" {
		t.Fatal("watcher did not install a stable file")
	}
}

func TestFileWatcherDoesNotInstallAStaleReadAfterConcurrentSave(t *testing.T) {
	w, _, _ := watchFixture(t, watchBaseYAML)
	liveConfig, err := Load(w.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Live = func() *Config { return liveConfig }
	w.Install = func(next *Config) { liveConfig = next }
	w.Poll()
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	entered, release := make(chan struct{}), make(chan struct{})
	w.Adjust = func(*Config) error {
		close(entered)
		<-release
		return nil
	}
	done := make(chan bool, 1)
	go func() { done <- w.Poll() }()
	<-entered
	newBody := strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4.1"`)
	if err := WithConfigFileLock(func() error {
		if err := os.WriteFile(w.Paths.ConfigPath, []byte(newBody), 0o600); err != nil {
			return err
		}
		cfg, err := ParseConfigYAML([]byte(newBody), w.Paths)
		if err != nil {
			return err
		}
		liveConfig = cfg
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if <-done {
		t.Fatal("watcher installed a stale config after a concurrent save")
	}
	if liveConfig.Agent.Model != "openai/gpt-4.1" {
		t.Fatalf("live model = %q, want concurrent save", liveConfig.Agent.Model)
	}
}

func TestFileWatcherRejectedAdjustmentPreservesBackup(t *testing.T) {
	w, _, _ := watchFixture(t, watchBaseYAML)
	w.Poll()
	before, err := os.ReadFile(BackupPath(w.Paths.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	w.Adjust = func(*Config) error { return errors.New("rejected") }
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	if w.Poll() {
		t.Fatal("rejected configuration installed")
	}
	after, err := os.ReadFile(BackupPath(w.Paths.ConfigPath))
	if err != nil || string(after) != string(before) {
		t.Fatalf("backup changed after rejected adjustment: %q, %v", after, err)
	}
}

// TestFileWatcherKeepsTheLiveConfigWhenTheFileIsGone covers the window an atomic
// write opens on some platforms, and an operator who moved the file away.
func TestFileWatcherKeepsTheLiveConfigWhenTheFileIsGone(t *testing.T) {
	w, _, live := watchFixture(t, watchBaseYAML)
	w.Poll()
	before := live()
	if err := os.Remove(w.Paths.ConfigPath); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	if w.Poll() {
		t.Fatal("a missing file was installed")
	}
	if live() != before {
		t.Fatal("the live configuration was dropped with the file")
	}
}

// TestFileWatcherReappliesProcessOverrides proves a file edit cannot take away
// what the operator typed on the command line. `coddy serve --gateway` means the
// gateway runs whatever the file says, and a reload that dropped the flag would
// stop the bot the moment somebody saved an unrelated setting.
func TestFileWatcherReappliesProcessOverrides(t *testing.T) {
	w, _, live := watchFixture(t, watchBaseYAML)
	w.Adjust = func(c *Config) error {
		c.Gateways.Telegram.Enabled = true
		return nil
	}
	w.Poll()
	rewriteWatched(t, w, strings.ReplaceAll(watchBaseYAML, `"openai/gpt-4o"`, `"openai/gpt-4o-mini"`))
	if !w.Poll() {
		t.Fatal("a changed file was not installed")
	}
	if !live().Gateways.Telegram.Enabled {
		t.Fatal("the reload dropped an override the process was started with")
	}
}

// TestFileWatcherRunStopsWithItsContext keeps the daemon's shutdown honest: the
// watcher is one more goroutine to join, not one more thing to leak.
func TestFileWatcherRunStopsWithItsContext(t *testing.T) {
	w, _, _ := watchFixture(t, watchBaseYAML)
	w.Interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}

// The watcher of <home>/mcp.json reports every move of the file after the
// first look - written, removed, created again - and stays quiet between.
func TestStampWatcherReportsEveryMoveAfterTheBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	calls := 0
	w := &StampWatcher{Path: path, Changed: func() { calls++ }}
	if w.Poll() || calls != 0 {
		t.Fatal("the first poll reported a file nobody changed")
	}
	touch := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(time.Duration(calls+1) * time.Second)
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatal(err)
		}
	}
	touch(`{"mcpServers":{}}`)
	if !w.Poll() || calls != 1 {
		t.Fatalf("a created file was not reported (calls %d)", calls)
	}
	if w.Poll() || calls != 1 {
		t.Fatal("an unchanged file was reported again")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !w.Poll() || calls != 2 {
		t.Fatalf("a removed file was not reported (calls %d)", calls)
	}
}
