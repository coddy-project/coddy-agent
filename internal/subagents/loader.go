package subagents

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// Loader discovers definitions from configured directories.
type Loader struct {
	// Dirs are searched in order; later directories override earlier ones by
	// name. ${CODDY_HOME}, ${CWD} and ~ expand per Load call.
	Dirs []string
	// ProjectTrust is the subagents.project_trust policy; "deny" keeps the
	// loader from reading project-scope directories at all.
	ProjectTrust string
	// Log receives skipped-file warnings; nil uses slog.Default.
	Log *slog.Logger

	// visit is observed by tests to prove which directories were read.
	visit func(dir string)
}

// NewLoader returns a loader over dirs under the given project trust policy.
func NewLoader(dirs []string, projectTrust string) *Loader {
	return &Loader{Dirs: append([]string(nil), dirs...), ProjectTrust: strings.ToLower(strings.TrimSpace(projectTrust))}
}

// CanonicalWorkspace normalises a cwd the way trust receipts are keyed:
// absolute, symlinks resolved, cleaned. It is the MCP trust store's function,
// so both kinds of project-local approval agree on what a workspace is.
func CanonicalWorkspace(cwd string) string {
	return mcp.CanonicalWorkspace(cwd)
}

// Load returns every definition visible for a session: built-ins first, then
// each directory in order, later ones replacing earlier ones by name. The
// result is sorted by name.
func (l *Loader) Load(cwd, home string) []*Definition {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	workspace := CanonicalWorkspace(cwd)

	byName := map[string]*Definition{}
	for _, d := range Bundled() {
		byName[d.Name] = d
	}

	for _, raw := range l.Dirs {
		dir := expandDir(raw, cwd, home)
		if dir == "" {
			continue
		}
		scope := scopeFor(dir, workspace)
		if scope == ScopeProject && l.ProjectTrust == "deny" {
			continue
		}
		if l.visit != nil {
			l.visit(dir)
		}
		for _, d := range loadDir(dir, scope, log) {
			byName[d.Name] = d
		}
	}

	out := make([]*Definition, 0, len(byName))
	for _, d := range byName {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scopeFor decides whether a directory belongs to the workspace. Both paths are
// canonical, so a cwd reached through a symlink still owns its .coddy/agents.
func scopeFor(dir, workspace string) Scope {
	canon := CanonicalWorkspace(dir)
	if workspace == "" {
		return ScopeUser
	}
	if canon == workspace || strings.HasPrefix(canon, workspace+string(filepath.Separator)) {
		return ScopeProject
	}
	return ScopeUser
}

// expandDir resolves ${CODDY_HOME}, ${CWD} and a leading ~.
func expandDir(path, cwd, home string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if home != "" {
		path = strings.ReplaceAll(path, "${CODDY_HOME}", home)
	}
	path = strings.ReplaceAll(path, "${CWD}", cwd)
	if strings.HasPrefix(path, "~/") || path == "~" {
		if userHome, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(userHome, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

// loadDir reads every definition under dir, recursively, in lexical order.
// Inside one directory the first file claiming a name wins; later duplicates,
// unreadable, oversized and invalid files are skipped with a warning, and the
// directory contributes at most MaxDefinitionsPerDir definitions.
func loadDir(dir string, scope Scope, log *slog.Logger) []*Definition {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)

	seen := map[string]string{}
	out := make([]*Definition, 0, len(files))
	for _, path := range files {
		if len(out) >= MaxDefinitionsPerDir {
			log.Warn("subagents: definition cap reached, skipping the rest", "dir", dir, "cap", MaxDefinitionsPerDir)
			break
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.Size() > MaxFileBytes {
			log.Warn("subagents: definition file too large, skipped", "path", path, "bytes", fi.Size())
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from a configured definition directory
		if err != nil {
			continue
		}
		def, err := Parse(path, data)
		if err != nil {
			log.Warn("subagents: definition skipped", "error", err)
			continue
		}
		if prev, dup := seen[def.Name]; dup {
			log.Warn("subagents: duplicate definition name in one directory, first file wins", "name", def.Name, "kept", prev, "skipped", path)
			continue
		}
		seen[def.Name] = path
		def.Scope = scope
		out = append(out, def)
	}
	return out
}

// FindByName returns the definition with that name, or nil.
func FindByName(defs []*Definition, name string) *Definition {
	name = strings.TrimSpace(name)
	for _, d := range defs {
		if d != nil && d.Name == name {
			return d
		}
	}
	return nil
}

// VisibleNames lists the names a model may be told about: hidden definitions
// stay out of error messages as well as the catalog.
func VisibleNames(defs []*Definition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		if d != nil && !d.Hidden {
			out = append(out, d.Name)
		}
	}
	sort.Strings(out)
	return out
}
