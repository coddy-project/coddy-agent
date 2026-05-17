package plans_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/plans"
)

func TestValidateSlug(t *testing.T) {
	cases := []struct {
		slug string
		ok   bool
	}{
		{"auth-refactor", true},
		{"a", true},
		{"", false},
		{"Auth", false},
		{"-bad", false},
		{"bad-", false},
	}
	for _, tc := range cases {
		err := plans.ValidateSlug(tc.slug)
		if tc.ok && err != nil {
			t.Errorf("slug %q: want ok, got %v", tc.slug, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("slug %q: want error", tc.slug)
		}
	}
}

func TestParseTodosAsPlainStrings(t *testing.T) {
	raw := `---
name: QA plan
overview: Test scenarios
todos:
  - Опросить требования
  - Сформировать сценарии
  - Добавить отчёт
---
## Body
`
	doc, err := plans.Parse("qa-plan", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Todos) != 3 {
		t.Fatalf("todos len: %d %+v", len(doc.Todos), doc.Todos)
	}
	if doc.Todos[0].Content != "Опросить требования" {
		t.Fatalf("todo[0]: %q", doc.Todos[0].Content)
	}
}

func TestParseTodosWithTitleField(t *testing.T) {
	raw := `---
name: Demo
todos:
  - title: Set up UI component
    status: pending
---
## Steps
`
	doc, err := plans.Parse("demo", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Todos) != 1 || doc.Todos[0].Content != "Set up UI component" {
		t.Fatalf("todos: %+v", doc.Todos)
	}
	if doc.Todos[0].Status != "pending" {
		t.Fatalf("status: %q", doc.Todos[0].Status)
	}
}

func TestWriteRejectsInvalidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	slug := "bad-plan"
	invalid := `---
name: Bad
todos: not-a-list
---
body
`
	_, err := plans.Write(dir, slug, invalid)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, plans.DirName, slug+".plan.md")); statErr == nil {
		t.Fatal("invalid plan file should not be written")
	}
}

func TestParseFormatRoundTrip(t *testing.T) {
	raw := `---
name: Auth refactor
overview: JWT migration
todos:
  - content: Add middleware
    status: pending
---
## Steps

1. Do thing
`
	doc, err := plans.Parse("auth-refactor", raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "Auth refactor" {
		t.Fatalf("name: %q", doc.Name)
	}
	if doc.Overview != "JWT migration" {
		t.Fatalf("overview: %q", doc.Overview)
	}
	if len(doc.Todos) != 1 || doc.Todos[0].Content != "Add middleware" {
		t.Fatalf("todos: %+v", doc.Todos)
	}
	if !strings.Contains(doc.Body, "## Steps") {
		t.Fatalf("body: %q", doc.Body)
	}
	entries := plans.EntriesFromTodos(doc.Todos)
	if len(entries) != 1 || entries[0].Status != "pending" {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestCRUD(t *testing.T) {
	dir := t.TempDir()
	slug := "my-plan"
	created, err := plans.Create(dir, slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created.Content, "my-plan") && created.Name == "" {
		t.Fatalf("unexpected default: %+v", created)
	}
	_, err = plans.Create(dir, slug, "")
	if err != plans.ErrExists {
		t.Fatalf("create again: %v", err)
	}
	updated, err := plans.Write(dir, slug, plans.DefaultContent(slug, "Renamed"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" {
		t.Fatalf("name: %q", updated.Name)
	}
	items, err := plans.List(dir)
	if err != nil || len(items) != 1 || items[0].Slug != slug {
		t.Fatalf("list: %v %v", items, err)
	}
	if err := plans.Delete(dir, slug); err != nil {
		t.Fatal(err)
	}
	_, err = plans.Read(dir, slug)
	if err != plans.ErrNotFound {
		t.Fatalf("read after delete: %v", err)
	}
}

func TestReadByMention(t *testing.T) {
	dir := t.TempDir()
	slug := "foo"
	if _, err := plans.Create(dir, slug, plans.DefaultContent(slug, "Foo")); err != nil {
		t.Fatal(err)
	}
	doc, err := plans.ReadByMention(dir, "plans/foo.plan.md")
	if err != nil || doc.Slug != slug {
		t.Fatalf("mention: %v %+v", err, doc)
	}
}

func TestEnsureDirCreatesPlansPath(t *testing.T) {
	dir := t.TempDir()
	if err := plans.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, plans.DirName))
	if err != nil || !st.IsDir() {
		t.Fatalf("stat plans: %v", err)
	}
}
