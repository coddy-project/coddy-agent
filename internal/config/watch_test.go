package config

import (
	"context"
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
		Paths: cfg.Paths,
		Live:  func() *Config { return live },
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
