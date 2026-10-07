package gitws

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// globalExcludes reads the ignore patterns git applies besides a
// repository's own .gitignore files and info/exclude: the file
// core.excludesFile names in the user's and the system's configuration, or,
// when none names one, git's default $XDG_CONFIG_HOME/git/ignore.
//
// go-git's status reads only the repository's own files, so without these a
// file the user's global ignore covers - an IDE's settings, say - would be
// listed as a new file and deleted by a discard of everything, which the git
// binary would never do.
func globalExcludes() []gitignore.Pattern {
	var files []string
	for _, cfg := range gitConfigFiles() {
		if f := excludesFileOf(cfg); f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		files = append(files, filepath.Join(xdgConfigHome(), "git", "ignore"))
	}
	var patterns []gitignore.Pattern
	for _, f := range files {
		patterns = append(patterns, readPatterns(f)...)
	}
	return patterns
}

// gitConfigFiles lists the configuration files git reads core.excludesFile
// from, lowest priority first: the system's, then the user's two.
func gitConfigFiles() []string {
	files := []string{"/etc/gitconfig", filepath.Join(xdgConfigHome(), "git", "config")}
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".gitconfig"))
	}
	return files
}

func xdgConfigHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

// excludesFileOf returns the core.excludesFile a git configuration file
// names, with a leading ~ expanded, or "".
func excludesFileOf(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	raw := config.New()
	if err := config.NewDecoder(bytes.NewReader(data)).Decode(raw); err != nil {
		return ""
	}
	value := strings.TrimSpace(raw.Section("core").Options.Get("excludesfile"))
	if value == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(value, "~"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, rest)
		}
	}
	return filepath.FromSlash(value)
}

// readPatterns parses one ignore file, every pattern anchored at the
// repository's root as git applies a global one.
func readPatterns(path string) []gitignore.Pattern {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, nil))
	}
	return patterns
}
