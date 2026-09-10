package config

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"time"
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
	stamp fileStamp
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
// installed.
//
// It is exported because a single deterministic pass is what a test can assert
// on, and because the first call establishes the baseline: the file as it is
// right now is what the process is already running, so it installs nothing.
func (w *FileWatcher) Poll() bool {
	stamp := statConfig(w.Paths.ConfigPath)
	if stamp == w.stamp {
		return false
	}
	// The stamp is recorded whatever comes of the read. A file that cannot be
	// parsed is not worth re-reading and re-reporting every couple of seconds;
	// the next edit moves the stamp again and gets a fresh attempt.
	w.stamp = stamp
	if !stamp.exists {
		return false
	}

	next, err := LoadWithPaths(w.Paths)
	if err != nil {
		w.logger().Warn("config file changed but could not be loaded, keeping the running configuration",
			"path", w.Paths.ConfigPath, "error", err)
		return false
	}
	if w.Adjust != nil {
		if err := w.Adjust(next); err != nil {
			w.logger().Warn("config file changed but the process overrides no longer apply, keeping the running configuration",
				"path", w.Paths.ConfigPath, "error", err)
			return false
		}
	}
	if !w.differsFromLive(next) {
		return false
	}
	w.logger().Info("configuration changed on disk, reloading", "path", w.Paths.ConfigPath)
	if w.Install != nil {
		w.Install(next)
	}
	return true
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
