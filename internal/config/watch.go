package config

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// defaultWatchInterval is how often the config file is stat'ed. Two seconds is
// below what an operator notices between saving a file and seeing the model
// picker change, and far above what one stat per file costs.
const defaultWatchInterval = 2 * time.Second

// FileWatcher installs config.yaml into a running process when somebody else
// changes it.
//
// Every reload path inside the process already announces itself - the settings
// screen's PUT, the agent's config_commit tool, a skill install - but those are
// only the writers this process can see. `coddy providers login` runs in another
// terminal and adds a provider with its models; an operator edits the file in
// vim; a deployment drops a new one in. Without this, a daemon keeps serving the
// document it read at boot, and the model list in every open browser is the one
// from before the login.
//
// The watcher polls rather than subscribing to filesystem events. Config saves
// are atomic renames, which an inotify watch on the file itself stops seeing
// after the first one; watching the directory instead means a dependency and a
// second set of platform behaviours for a file that changes a few times a day.
type FileWatcher struct {
	// Paths locates the file and is what a reload is loaded through, so the
	// reloaded configuration keeps the same home and workspace as the running
	// one.
	Paths Paths
	// Interval is how often the file is stat'ed. Zero means defaultWatchInterval.
	Interval time.Duration
	// Live returns the configuration currently installed. It is what a freshly
	// read file is compared against, so a rewrite that changes no setting - the
	// file this process itself just saved, a comment, a reordered key - is not
	// announced as a change.
	Live func() *Config
	// Install receives a configuration that genuinely differs from the live one.
	Install func(*Config)
	// Adjust re-applies whatever the process decided at startup that the file
	// does not carry: the command-line overrides. Without it a file edit would
	// silently take away `--gateway` or `-H 0.0.0.0` from a running daemon. An
	// error means the reloaded document cannot be honoured, and the live one is
	// kept.
	Adjust func(*Config) error
	// Log records what was picked up and what could not be read.
	Log *slog.Logger

	// stamp is the file as it looked when it was last examined. It is the cheap
	// gate that keeps the common case to one stat.
	stamp       fileStamp
	stablePolls int
	stableCount int
}

// fileStamp is what a stat tells us about the watched file.
type fileStamp struct {
	modTime time.Time
	size    int64
	exists  bool
}

// Run polls until ctx ends. It never returns an error: a configuration that
// cannot be read is a reason to keep the one already running, not to take the
// daemon down with it.
func (w *FileWatcher) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	// Examine the current file before waiting for the first tick.
	w.Poll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.Poll()
		}
	}
}

// Poll examines the file once and reports whether a new configuration was
// installed. A changed file must retain the same stamp for the configured
// number of polls before it is read.
func (w *FileWatcher) Poll() bool {
	stamp := statConfig(w.Paths.ConfigPath)
	stablePolls := w.stablePolls
	if stablePolls <= 0 {
		stablePolls = 2
	}
	if stamp != w.stamp {
		w.stamp, w.stableCount = stamp, 1
		if stablePolls > 1 {
			return false
		}
	}
	if w.stableCount == 0 {
		return false
	}
	w.stableCount++
	if w.stableCount < stablePolls {
		return false
	}
	if !stamp.exists {
		w.stableCount = 0
		return false
	}

	// A watcher must not recover or migrate the file while an editor is writing it.
	data, err := os.ReadFile(w.Paths.ConfigPath)
	if err != nil {
		w.stableCount = stablePolls - 1
		w.logger().Warn("config file changed but could not be loaded, keeping the running configuration",
			"path", w.Paths.ConfigPath, "error", err)
		return false
	}
	next, err := ParseConfigYAML(data, w.Paths)
	if err != nil {
		w.logger().Warn("config file changed but could not be loaded, keeping the running configuration",
			"path", w.Paths.ConfigPath, "error", err)
		return false
	}
	if afterRead := statConfig(w.Paths.ConfigPath); afterRead != stamp {
		w.stamp, w.stableCount = afterRead, 1
		return false
	}
	if err := watchedLegacyKeys(data); err != nil {
		w.logger().Warn("config file changed but could not be loaded, keeping the running configuration", "path", w.Paths.ConfigPath, "error", err)
		return false
	}
	if w.Adjust != nil {
		if err := w.Adjust(next); err != nil {
			w.logger().Warn("config file changed but the process overrides no longer apply, keeping the running configuration",
				"path", w.Paths.ConfigPath, "error", err)
			return false
		}
	}
	installed := false
	if err := WithConfigFileLock(func() error {
		current, err := os.ReadFile(w.Paths.ConfigPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, data) {
			return fmt.Errorf("config file changed while being read")
		}
		if !w.differsFromLive(next) {
			return nil
		}
		if err := prepareOIDCPrimary(next, expandConfigBody(string(data), w.Paths)); err != nil {
			return err
		}
		if err := WriteBackup(w.Paths.ConfigPath, data); err != nil {
			w.logger().Warn("config backup could not be updated", "path", w.Paths.ConfigPath, "error", err)
		}
		w.logger().Info("configuration changed on disk, reloading", "path", w.Paths.ConfigPath)
		if w.Install != nil {
			w.Install(next)
		}
		installed = true
		return nil
	}); err != nil {
		w.stableCount = stablePolls - 1
		w.logger().Warn("config file could not be reloaded, keeping the running configuration", "path", w.Paths.ConfigPath, "error", err)
		return false
	}
	w.stableCount = 0
	return installed
}

// watchedLegacyKeys refuses keys that require a destructive migration on load.
func watchedLegacyKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode || len(doc.Content[0].Content) == 0 {
		return fmt.Errorf("configuration document is empty")
	}
	root := doc.Content[0]
	for _, path := range [][]string{{"mcp_servers"}, {"skills", "sources"}, {"tools", "permission_mode"}, {"scheduler", "dir"}} {
		node := root
		for _, part := range path {
			if node != nil && node.Kind == yaml.AliasNode {
				node = node.Alias
			}
			if node == nil || node.Kind != yaml.MappingNode {
				node = nil
				break
			}
			_, node = mappingEntryWithMerges(node, part, make(map[*yaml.Node]bool))
		}
		if node != nil {
			return fmt.Errorf("legacy key %s requires migration at startup", strings.Join(path, "."))
		}
	}
	return nil
}

func mappingEntryWithMerges(m *yaml.Node, key string, seen map[*yaml.Node]bool) (*yaml.Node, *yaml.Node) {
	if m != nil && m.Kind == yaml.AliasNode {
		m = m.Alias
	}
	if m == nil || m.Kind != yaml.MappingNode || seen[m] {
		return nil, nil
	}
	seen[m] = true
	if k, v := mappingEntry(m, key); k != nil {
		return k, v
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "<<" {
			continue
		}
		merge := m.Content[i+1]
		if merge.Kind == yaml.SequenceNode {
			for _, candidate := range merge.Content {
				if k, v := mappingEntryWithMerges(candidate, key, seen); k != nil {
					return k, v
				}
			}
		} else if k, v := mappingEntryWithMerges(merge, key, seen); k != nil {
			return k, v
		}
	}
	return nil, nil
}

// differsFromLive answers whether next says anything the running configuration
// does not.
//
// The comparison is on the rendered document rather than the struct, because
// that is the same view the settings screen writes and the loader reads: two
// configurations that render identically behave identically, and a file whose
// only edit was a comment or a key order renders the same as the one already
// installed. Reflection would instead report every unexported cache and every
// pointer identity as a change.
func (w *FileWatcher) differsFromLive(next *Config) bool {
	if w.Live == nil {
		return true
	}
	live := w.Live()
	if live == nil {
		return true
	}
	a, err := MarshalConfigYAML(live)
	if err != nil {
		return true
	}
	b, err := MarshalConfigYAML(next)
	if err != nil {
		return true
	}
	return !bytes.Equal(a, b)
}

func (w *FileWatcher) logger() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// statConfig reads the file's identity, reporting a missing file as an absent
// stamp rather than an error. A config that is gone for a moment is what an
// atomic rename looks like from here.
func statConfig(path string) fileStamp {
	if path == "" {
		return fileStamp{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size(), exists: true}
}

// StampWatcher calls Changed whenever a file's stamp moves: written,
// replaced, created or removed. It is the FileWatcher's gate without its
// loading, for the files a running process reads besides config.yaml -
// <home>/mcp.json, which the MCP servers come from. The first poll
// establishes the baseline and calls nothing. Changed is told only that the
// file moved; comparing what it says is the receiver's business, so a
// rewrite with the same content costs one read there.
type StampWatcher struct {
	// Path is the watched file.
	Path string
	// Interval is how often the file is stat'ed. Zero means defaultWatchInterval.
	Interval time.Duration
	// Changed runs on the watcher's goroutine after every move of the stamp.
	Changed func()

	stamp  fileStamp
	primed bool
}

// Run polls until ctx ends.
func (w *StampWatcher) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	w.Poll()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.Poll()
		}
	}
}

// Poll examines the file once and reports whether Changed was called.
func (w *StampWatcher) Poll() bool {
	stamp := statConfig(w.Path)
	if w.primed && stamp == w.stamp {
		return false
	}
	first := !w.primed
	w.primed, w.stamp = true, stamp
	if first || w.Changed == nil {
		return false
	}
	w.Changed()
	return true
}
