// Package plans manages session-scoped design plan files (plans/<slug>.plan.md).
package plans

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

const (
	DirName          = "plans"
	FileSuffix       = ".plan.md"
	MetaPlanSlug     = "coddy.dev/planSlug"
	MetaPlanKind     = "coddy.dev/planKind"
	MetaRunPlanSlug  = "coddy.dev/runPlanSlug"
	PlanKindDesign   = "design"
	defaultPlanName  = "Plan"
	maxSlugLen       = 64
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// ErrInvalidSlug is returned when a plan slug fails validation.
var ErrInvalidSlug = errors.New("invalid plan slug")

// ErrNotFound is returned when a plan file does not exist.
var ErrNotFound = errors.New("plan not found")

// ErrExists is returned when creating a plan that already exists.
var ErrExists = errors.New("plan already exists")

// TodoItem is one step in the design plan frontmatter.
type TodoItem struct {
	Content  string `yaml:"content"`
	Status   string `yaml:"status,omitempty"`
	Priority string `yaml:"priority,omitempty"`
}

// Frontmatter is YAML metadata at the top of a plan file.
type Frontmatter struct {
	Name     string   `yaml:"name,omitempty"`
	Overview string   `yaml:"overview,omitempty"`
	Todos    todoList `yaml:"todos,omitempty"`
}

// Document is a parsed design plan file.
type Document struct {
	Slug     string
	Name     string
	Overview string
	Todos    []TodoItem
	Body     string
	Content  string // full file bytes as written
	UpdatedAt time.Time
}

// PlansDir returns the plans directory under a session bundle.
func PlansDir(sessionDir string) string {
	return filepath.Join(sessionDir, DirName)
}

// FilePath returns the absolute path for a plan slug.
func FilePath(sessionDir, slug string) (string, error) {
	if err := ValidateSlug(slug); err != nil {
		return "", err
	}
	return filepath.Join(PlansDir(sessionDir), slug+FileSuffix), nil
}

// ValidateSlug checks slug syntax (lowercase alnum and hyphens).
func ValidateSlug(slug string) error {
	s := strings.TrimSpace(slug)
	if s == "" {
		return fmt.Errorf("%w: empty", ErrInvalidSlug)
	}
	if len(s) > maxSlugLen {
		return fmt.Errorf("%w: too long", ErrInvalidSlug)
	}
	if !slugPattern.MatchString(s) {
		return fmt.Errorf("%w: %q", ErrInvalidSlug, slug)
	}
	return nil
}

// EntriesFromTodos converts frontmatter todos to ACP plan entries for session/update.
func EntriesFromTodos(todos []TodoItem) []acp.PlanEntry {
	if len(todos) == 0 {
		return nil
	}
	out := make([]acp.PlanEntry, 0, len(todos))
	for _, t := range todos {
		content := strings.TrimSpace(t.Content)
		if content == "" {
			continue
		}
		status := strings.TrimSpace(t.Status)
		if status == "" {
			status = "pending"
		}
		e := acp.PlanEntry{
			Content: content,
			Status:  status,
		}
		if p := strings.TrimSpace(t.Priority); p != "" {
			e.Priority = p
		}
		out = append(out, e)
	}
	return out
}

// DesignPlanMeta returns _meta for a design plan PlanUpdate.
func DesignPlanMeta(slug string) map[string]interface{} {
	return map[string]interface{}{
		MetaPlanSlug: slug,
		MetaPlanKind: PlanKindDesign,
	}
}

// AssistantPlanHeading formats the visible plan heading for chat clients.
func AssistantPlanHeading(name, slug string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		n = defaultPlanName
	}
	return fmt.Sprintf("# %s (plan: %s)", n, strings.TrimSpace(slug))
}
