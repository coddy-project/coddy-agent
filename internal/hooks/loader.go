package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// Scope says where a definition file comes from.
type Scope string

const (
	// ScopeUser is a file outside the workspace: the operator's own.
	ScopeUser Scope = "user"
	// ScopeProject is a file at or under the workspace: it arrived with the
	// checkout and follows hooks.project_trust.
	ScopeProject Scope = "project"
)

// TrustState is what the catalog and the runner report for a source.
type TrustState string

const (
	// TrustTrusted means the file's hooks run: user scope, project scope
	// under allow, or project scope with a matching receipt.
	TrustTrusted TrustState = "trusted"
	// TrustNeedsApproval means a project-scope file under ask has no receipt
	// for this workspace and this content; none of its hooks runs.
	TrustNeedsApproval TrustState = "needs_approval"
)

// Source is one definition file as the loader found it.
type Source struct {
	// Path is the absolute file path.
	Path string
	// Display names the file in the catalog and in receipts: the
	// workspace-relative path in slash form for project scope, the absolute
	// path otherwise.
	Display string
	Scope   Scope
	// Digest is sha256 over the file bytes; a receipt is bound to it.
	Digest string
	// Definition is the parsed content; nil when Err is set.
	Definition *Definition
	// Err is the parse or read error of an invalid file.
	Err   error
	Trust TrustState
}

// Runnable reports whether the source's hooks may run at all.
func (s *Source) Runnable() bool {
	return s != nil && s.Err == nil && s.Definition != nil && s.Trust == TrustTrusted
}

// Loader resolves hooks.files for one session.
type Loader struct {
	Files        []string
	ProjectTrust string
	Log          *slog.Logger
	// Approved reports whether a receipt exists for a project-scope file
	// with this digest in the canonical workspace. Nil means no receipts, so
	// under ask every project-scope file needs approval.
	Approved func(workspace, display, digest string) bool
}

// NewLoader builds a loader for the configured files and policy.
func NewLoader(files []string, projectTrust string) *Loader {
	return &Loader{Files: append([]string(nil), files...), ProjectTrust: strings.ToLower(strings.TrimSpace(projectTrust))}
}

// CanonicalWorkspace normalises a cwd the way trust receipts are keyed:
// absolute, symlinks resolved, cleaned. It is the MCP trust store's function,
// so every kind of project-local approval agrees on what a workspace is.
func CanonicalWorkspace(cwd string) string {
	return mcp.CanonicalWorkspace(cwd)
}

// Load reads every configured file for a session, in hooks.files order. A
// missing file is skipped; a project-scope file is not read at all under
// deny; an unreadable or unparsable file is kept as an invalid source so
// the catalog can show it.
func (l *Loader) Load(cwd, home string) []*Source {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	workspace := CanonicalWorkspace(cwd)
	lexicalWorkspace := lexicalPath(cwd)
	policy := l.ProjectTrust
	if policy == "" {
		policy = config.ProjectTrustAsk
	}

	var out []*Source
	seen := map[string]bool{}
	for _, raw := range l.Files {
		path := expandFile(raw, cwd, home)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		scope := scopeFor(path, workspace, lexicalWorkspace)
		if scope == ScopeProject && policy == config.ProjectTrustDeny {
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304 -- the path comes from hooks.files, an operator setting
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			out = append(out, &Source{Path: path, Display: displayFor(path, scope, cwd), Scope: scope, Err: fmt.Errorf("read: %w", err)})
			log.Warn("hooks file is unreadable", "path", path, "error", err)
			continue
		}
		sum := sha256.Sum256(data)
		src := &Source{
			Path:    path,
			Display: displayFor(path, scope, cwd),
			Scope:   scope,
			Digest:  "sha256:" + hex.EncodeToString(sum[:]),
		}
		def, perr := Parse(data)
		if perr != nil {
			src.Err = perr
			log.Warn("hooks file is invalid", "path", path, "error", perr)
		} else {
			src.Definition = def
			for _, w := range def.Warnings {
				log.Warn("hooks file warning", "path", path, "warning", w)
			}
		}
		src.Trust = l.trustFor(src, workspace, policy)
		out = append(out, src)
	}
	return out
}

func (l *Loader) trustFor(src *Source, workspace, policy string) TrustState {
	if src.Scope != ScopeProject || policy == config.ProjectTrustAllow {
		return TrustTrusted
	}
	if l.Approved != nil && src.Err == nil && l.Approved(workspace, src.Display, src.Digest) {
		return TrustTrusted
	}
	return TrustNeedsApproval
}

// displayFor names a source: relative to the workspace in slash form for
// project scope, absolute otherwise.
func displayFor(path string, scope Scope, cwd string) string {
	if scope == ScopeProject {
		if rel, err := filepath.Rel(lexicalPath(cwd), path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// scopeFor decides the scope on both the canonical and the lexical form of
// the file path, the way the subagents loader does, so a symlinked workspace
// still owns its .coddy/hooks.json and a checkout that commits .coddy as a
// symlink elsewhere cannot escape the project policy.
func scopeFor(path, workspace, lexicalWorkspace string) Scope {
	if workspace == "" && lexicalWorkspace == "" {
		return ScopeUser
	}
	if under(CanonicalWorkspace(path), workspace) || under(lexicalPath(path), lexicalWorkspace) {
		return ScopeProject
	}
	return ScopeUser
}

// under reports whether path equals root or sits below it.
func under(path, root string) bool {
	if root == "" || path == "" {
		return false
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// lexicalPath is the absolute, cleaned form of a path without resolving
// symlinks.
func lexicalPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// expandFile resolves ${CODDY_HOME}, ${CWD} and a leading ~, and anchors a
// relative entry at the session cwd.
func expandFile(path, cwd, home string) string {
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
