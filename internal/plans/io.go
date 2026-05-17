package plans

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListItem is metadata for one plan file.
type ListItem struct {
	Slug      string `json:"slug"`
	Name      string `json:"name,omitempty"`
	Overview  string `json:"overview,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// EnsureDir creates the plans directory under sessionDir.
func EnsureDir(sessionDir string) error {
	return os.MkdirAll(PlansDir(sessionDir), 0o755)
}

// Read loads and parses a plan file.
func Read(sessionDir, slug string) (*Document, error) {
	path, err := FilePath(sessionDir, slug)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	doc, err := Parse(slug, string(b))
	if err != nil {
		return nil, err
	}
	if fi, statErr := os.Stat(path); statErr == nil {
		doc.UpdatedAt = fi.ModTime().UTC()
	}
	return doc, nil
}

// Write persists content for slug, creating the plans dir if needed.
func Write(sessionDir, slug, content string) (*Document, error) {
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	if err := EnsureDir(sessionDir); err != nil {
		return nil, err
	}
	path, err := FilePath(sessionDir, slug)
	if err != nil {
		return nil, err
	}
	doc, err := Parse(slug, content)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	if fi, statErr := os.Stat(path); statErr == nil {
		doc.UpdatedAt = fi.ModTime().UTC()
	}
	return doc, nil
}

// Create writes a new plan file; returns ErrExists if the file is already present.
func Create(sessionDir, slug, content string) (*Document, error) {
	path, err := FilePath(sessionDir, slug)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return nil, ErrExists
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if strings.TrimSpace(content) == "" {
		content = DefaultContent(slug, slug)
	}
	return Write(sessionDir, slug, content)
}

// Delete removes a plan file.
func Delete(sessionDir, slug string) error {
	path, err := FilePath(sessionDir, slug)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// List returns plan metadata sorted by slug.
func List(sessionDir string) ([]ListItem, error) {
	dir := PlansDir(sessionDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ListItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileSuffix) {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), FileSuffix)
		if err := ValidateSlug(slug); err != nil {
			continue
		}
		doc, readErr := Read(sessionDir, slug)
		if readErr != nil {
			continue
		}
		item := ListItem{
			Slug:     slug,
			Name:     doc.Name,
			Overview: doc.Overview,
		}
		if !doc.UpdatedAt.IsZero() {
			item.UpdatedAt = doc.UpdatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// ReadByMention loads a plan when the user references @plans/<slug>.plan.md.
func ReadByMention(sessionDir, relPath string) (*Document, error) {
	rel := filepath.ToSlash(strings.TrimSpace(relPath))
	rel = strings.TrimPrefix(rel, "./")
	if !strings.HasPrefix(rel, DirName+"/") {
		return nil, fmt.Errorf("not a plan path: %s", relPath)
	}
	base := filepath.Base(rel)
	if !strings.HasSuffix(base, FileSuffix) {
		return nil, fmt.Errorf("not a plan file: %s", relPath)
	}
	slug := strings.TrimSuffix(base, FileSuffix)
	return Read(sessionDir, slug)
}

// IsPlanMention reports whether relPath refers to a session plan file.
func IsPlanMention(relPath string) bool {
	rel := filepath.ToSlash(strings.TrimSpace(relPath))
	return strings.HasPrefix(rel, DirName+"/") && strings.HasSuffix(rel, FileSuffix)
}

// RunContextText builds text injected into the agent system prompt when executing a design plan.
func RunContextText(doc *Document) string {
	if doc == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Design plan to implement\n\n")
	if o := strings.TrimSpace(doc.Overview); o != "" {
		b.WriteString(o)
		b.WriteString("\n\n")
	}
	if body := strings.TrimSpace(doc.Body); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// WrapReadError maps filesystem errors for HTTP handlers.
func WrapReadError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return err
	}
	return err
}
