package config_test

// The lineage guard. The manager's model-listing cache reaches a configuration
// through the cell on config.Paths (listing.go), so a configuration is bound to
// the cache exactly when it was loaded from the Paths of a configuration that
// is. Every production caller of LoadWithPaths must therefore take its Paths
// from a live configuration (c.Paths, a.cfg.Paths, the watcher's Paths), or be
// one of the callers below that start a lineage of their own or never install
// what they load. A new caller that builds its Paths some other way would hand
// the process a configuration no source can answer for: the five capability
// reads would say "not known" for it, silently, until the manager's own attach
// in storeConfig (the backstop) or never.
//
// The scan reads the production files of the repository (see productionGoFiles)
// and fails with file and line.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// loadWithPathsCall finds a call of LoadWithPaths, qualified or not, and
// captures the text of its single argument.
var loadWithPathsCall = regexp.MustCompile(`\bLoadWithPaths\(([^()]*)\)`)

// loadWithPathsAllowed names the callers whose argument is not a selector on a
// live configuration, keyed by file and argument text, with the reason.
var loadWithPathsAllowed = map[string]string{
	"cmd/coddy/serve_password.go|paths": "start-up helper: the paths come from config.Resolve and the config it " +
		"loads is read for one value and dropped",
	"internal/configapi/configapi.go|paths": "the settings save: paths := c.Paths of the live configuration, read a few lines above, " +
		"inside the same config-file transaction",
	"internal/config/uci.go|paths": "the staged commit of the tools (toolConfigPaths) loads to validate; what is installed is " +
		"loaded again by the manager through the live configuration's Paths",
}

type loadWithPathsSite struct {
	file, arg string
	line      int
}

func loadWithPathsSites(t *testing.T) []loadWithPathsSite {
	t.Helper()
	var sites []loadWithPathsSite
	productionGoFiles(t, func(rel, text string) {
		for i, line := range strings.Split(text, "\n") {
			code := goCodeOf(line)
			if strings.Contains(code, "func LoadWithPaths(") {
				continue
			}
			for _, m := range loadWithPathsCall.FindAllStringSubmatch(code, -1) {
				sites = append(sites, loadWithPathsSite{file: rel, arg: strings.TrimSpace(m[1]), line: i + 1})
			}
		}
	})
	return sites
}

func TestLoadWithPathsCallersTakeTheirPathsFromALiveConfig(t *testing.T) {
	sites := loadWithPathsSites(t)
	if len(sites) < 6 {
		t.Fatalf("the scan found only %d callers of LoadWithPaths: the pattern or the repository root is wrong", len(sites))
	}
	used := make(map[string]bool)
	var bad []string
	for _, s := range sites {
		if strings.HasSuffix(s.arg, ".Paths") {
			continue // c.Paths, a.cfg.Paths, current.Paths, w.Paths: a live configuration's (or the watcher's)
		}
		key := s.file + "|" + s.arg
		if _, ok := loadWithPathsAllowed[key]; ok {
			used[key] = true
			continue
		}
		bad = append(bad, s.file+":"+strconv.Itoa(s.line)+": LoadWithPaths("+s.arg+")")
	}
	if len(bad) > 0 {
		t.Fatalf("LoadWithPaths must be given the Paths of a live *Config (so the loaded configuration shares its listing cell) "+
			"or be listed in loadWithPathsAllowed with a reason:\n%s", strings.Join(bad, "\n"))
	}
	for key := range loadWithPathsAllowed {
		if !used[key] {
			t.Errorf("the allowlist entry %q matches no caller any more: remove it", key)
		}
	}
}

func TestLoadWithPathsScannerReadsArguments(t *testing.T) {
	cases := []struct{ line, want string }{
		{`	next, err := config.LoadWithPaths(a.cfg.Paths)`, "a.cfg.Paths"},
		{`	reloaded, err := LoadWithPaths(paths)`, "paths"},
		{`	cfg, err := config.LoadWithPaths( c.Paths )`, "c.Paths"},
	}
	for _, c := range cases {
		m := loadWithPathsCall.FindStringSubmatch(goCodeOf(c.line))
		if m == nil || strings.TrimSpace(m[1]) != c.want {
			t.Errorf("%q: argument = %v, want %q", c.line, m, c.want)
		}
	}
	if m := loadWithPathsCall.FindStringSubmatch(goCodeOf(`	// config.LoadWithPaths(x)`)); m != nil {
		t.Error("a call inside a comment was found")
	}
}
