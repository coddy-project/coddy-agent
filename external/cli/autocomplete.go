//go:build cli

package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// maxWalkEntries caps the @-mention filesystem scan (mirrors the HTTP
// workspace collector limit).
const maxWalkEntries = 50000

// skippedWalkDirs are never descended into during @ completion.
var skippedWalkDirs = map[string]bool{".git": true, "node_modules": true, ".coddy": true}

// completionProvider serves slash-command and @path suggestions to the editor.
type completionProvider struct {
	cwd string
	// commands returns the current slash catalog (server + client merged).
	commands func() []tui.AutocompleteItem

	filesOnce  bool
	filesCache []string
}

func newCompletionProvider(cwd string, commands func() []tui.AutocompleteItem) *completionProvider {
	return &completionProvider{cwd: cwd, commands: commands}
}

// Suggestions implements tui.AutocompleteProvider.
func (p *completionProvider) Suggestions(lines []string, cursorLine, cursorCol int, force bool) []tui.AutocompleteItem {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return nil
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	before := line[:cursorCol]

	// Slash commands: only on the first line, message starting with "/".
	// An exact value match sorts first so enter picks it over longer
	// prefix-sharing commands (pi behavior: /mode must not resolve to /model).
	if cursorLine == 0 && strings.HasPrefix(line, "/") && !strings.Contains(before, " ") {
		prefix := strings.ToLower(strings.TrimPrefix(before, "/"))
		var exact, rest []tui.AutocompleteItem
		for _, item := range p.commands() {
			lowered := strings.ToLower(item.Value)
			if lowered == prefix {
				exact = append(exact, item)
			} else if strings.HasPrefix(lowered, prefix) {
				rest = append(rest, item)
			}
		}
		return append(exact, rest...)
	}

	// @path mentions: token starting with "@", or tab-forced file completion.
	tokenStart := strings.LastIndexByte(before, ' ') + 1
	token := before[tokenStart:]
	if strings.HasPrefix(token, "@") {
		return p.fileSuggestions(strings.TrimPrefix(token, "@"), "@")
	}
	if force {
		return p.fileSuggestions(token, "")
	}
	return nil
}

func (p *completionProvider) fileSuggestions(prefix, trigger string) []tui.AutocompleteItem {
	files := p.workspaceFiles()
	lowered := strings.ToLower(prefix)
	var out []tui.AutocompleteItem
	for _, f := range files {
		if lowered == "" || strings.Contains(strings.ToLower(f), lowered) {
			out = append(out, tui.AutocompleteItem{Value: trigger + f, Label: f})
			if len(out) >= 50 {
				break
			}
		}
	}
	return out
}

// workspaceFiles walks the cwd once, capped and with vendor dirs skipped.
func (p *completionProvider) workspaceFiles() []string {
	if p.filesOnce {
		return p.filesCache
	}
	p.filesOnce = true
	var files []string
	count := 0
	_ = filepath.WalkDir(p.cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count >= maxWalkEntries {
			return filepath.SkipAll
		}
		count++
		name := d.Name()
		if d.IsDir() {
			if skippedWalkDirs[name] || (strings.HasPrefix(name, ".") && path != p.cwd) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(p.cwd, path)
		if relErr != nil {
			return nil
		}
		clean := tui.SanitizeText(filepath.ToSlash(rel))
		if clean != "" {
			files = append(files, clean)
		}
		return nil
	})
	sort.Strings(files)
	p.filesCache = files
	return files
}

// Apply implements tui.AutocompleteProvider: replaces the active token.
func (p *completionProvider) Apply(lines []string, cursorLine, cursorCol int, item tui.AutocompleteItem) ([]string, int, int) {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return lines, cursorLine, cursorCol
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	before := line[:cursorCol]
	after := line[cursorCol:]

	if cursorLine == 0 && strings.HasPrefix(line, "/") && !strings.Contains(before, " ") {
		newBefore := "/" + item.Value + " "
		lines[0] = newBefore + after
		return lines, 0, len(newBefore)
	}

	tokenStart := strings.LastIndexByte(before, ' ') + 1
	newBefore := before[:tokenStart] + item.Value + " "
	lines[cursorLine] = newBefore + after
	return lines, cursorLine, len(newBefore)
}
