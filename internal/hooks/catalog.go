package hooks

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// CatalogHandler is one handler as the catalog shows it.
type CatalogHandler struct {
	Event          string   `json:"event"`
	Matcher        string   `json:"matcher,omitempty"`
	Type           string   `json:"type"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Async          bool     `json:"async,omitempty"`
	FailClosed     bool     `json:"fail_closed,omitempty"`
	Unsupported    string   `json:"unsupported,omitempty"`
}

// CatalogEntry is one definition file as the catalog shows it, with the
// trust decision for the workspace it was loaded for.
type CatalogEntry struct {
	// File names the source: the workspace-relative path in slash form for
	// project scope, the absolute path otherwise (what receipts and the CLI
	// use).
	File   string     `json:"file"`
	Path   string     `json:"path"`
	Scope  Scope      `json:"scope"`
	Digest string     `json:"digest,omitempty"`
	Trust  TrustState `json:"trust"`
	// Trusted and NeedsApproval mirror Trust as booleans for clients that
	// switch on them.
	Trusted       bool             `json:"trusted"`
	NeedsApproval bool             `json:"needs_approval"`
	Error         string           `json:"error,omitempty"`
	Warnings      []string         `json:"warnings,omitempty"`
	Hooks         []CatalogHandler `json:"hooks"`
}

// BuildCatalog renders the loaded sources in load order.
func BuildCatalog(sources []*Source) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(sources))
	for _, src := range sources {
		if src == nil {
			continue
		}
		entry := CatalogEntry{
			File:          src.Display,
			Path:          src.Path,
			Scope:         src.Scope,
			Digest:        src.Digest,
			Trust:         src.Trust,
			Trusted:       src.Trust == TrustTrusted,
			NeedsApproval: src.Trust == TrustNeedsApproval,
			Hooks:         []CatalogHandler{},
		}
		if src.Err != nil {
			entry.Error = src.Err.Error()
		}
		if src.Definition != nil {
			entry.Warnings = append([]string(nil), src.Definition.Warnings...)
			for _, event := range Events {
				for _, g := range src.Definition.Events[event] {
					for _, h := range g.Handlers {
						entry.Hooks = append(entry.Hooks, CatalogHandler{
							Event:          event,
							Matcher:        g.Matcher,
							Type:           h.Type,
							Command:        h.Command,
							Args:           append([]string(nil), h.Args...),
							TimeoutSeconds: h.TimeoutSeconds,
							Async:          h.Async,
							FailClosed:     h.FailClosed,
							Unsupported:    h.Unsupported,
						})
					}
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// FindSource returns the source a file argument names: its catalog name
// (the workspace-relative path for project scope, the absolute path
// otherwise) or its absolute path, in either slash form.
func FindSource(sources []*Source, file string) *Source {
	file = strings.TrimSpace(file)
	if file == "" {
		return nil
	}
	slash := filepath.ToSlash(filepath.Clean(file))
	for _, src := range sources {
		if src == nil {
			continue
		}
		if src.Display == file || src.Path == file || filepath.ToSlash(filepath.Clean(src.Display)) == slash || filepath.ToSlash(src.Path) == slash {
			return src
		}
	}
	return nil
}

// WriteListing prints the catalog as a table for the CLI: one row per file
// with its scope, trust state and a summary of its hooks.
func WriteListing(w io.Writer, entries []CatalogEntry) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FILE\tSCOPE\tTRUST\tHOOKS")
	for _, e := range entries {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.File, e.Scope, e.Trust, summarizeHooks(e))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "(total %d)\n", len(entries))
}

func summarizeHooks(e CatalogEntry) string {
	if e.Error != "" {
		return "invalid: " + e.Error
	}
	if len(e.Hooks) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(e.Hooks))
	for _, h := range e.Hooks {
		matcher := h.Matcher
		if matcher == "" {
			matcher = "*"
		}
		part := fmt.Sprintf("%s(%s)", h.Event, matcher)
		if h.Unsupported != "" {
			part += " [unsupported]"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}
