package skills_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestCanonicalCommandName_rootAndSubdir(t *testing.T) {
	a := &skills.Skill{FilePath: filepath.Join("d", "alpha.md")}
	if g := skills.CanonicalCommandName(a); g != "alpha" {
		t.Fatalf("root md: got %q", g)
	}
	b := &skills.Skill{FilePath: filepath.Join("d", "my-skill", "SKILL.md")}
	if g := skills.CanonicalCommandName(b); g != "my-skill" {
		t.Fatalf("subdir SKILL: got %q", g)
	}
}

func TestListSkills_dedupAndSort(t *testing.T) {
	sk := []*skills.Skill{
		{Name: "x", FilePath: filepath.Join("/a", "b-skill.md"), Content: "# B\n\nsecond wins should not"},
		{Name: "SKILL", FilePath: filepath.Join("/first", "dup", "SKILL.md"), Content: "Body A", Description: "dA"},
		{Name: "SKILL", FilePath: filepath.Join("/second", "dup", "SKILL.md"), Content: "Body B"},
	}
	got := skills.ListSkills(sk)
	if len(got) != 2 {
		t.Fatalf("len=%d %#v", len(got), got)
	}
	if got[0].Name != "b-skill" || got[1].Name != "dup" {
		t.Fatalf("order: %#v", got)
	}
	if got[1].Description != "dA" {
		t.Fatalf("first dup should win: %#v", got[1])
	}
}

func TestParseInvokedCommandNames_fencesAndQuotes(t *testing.T) {
	text := "/one\n```\n/two\n```\n> /three\n/FOUR_bar\n"
	got := skills.ParseInvokedCommandNames(text)
	if len(got) != 2 || got[0] != "one" || got[1] != "FOUR_bar" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseInvokedCommandNames_whitespaceSlashMidLine(t *testing.T) {
	if g := skills.ParseInvokedCommandNames("see /foo"); len(g) != 1 || g[0] != "foo" {
		t.Fatalf("got %#v", g)
	}
}

func TestParseInvokedCommandNames_coddySkillPickLink(t *testing.T) {
	g := skills.ParseInvokedCommandNames("[/demo](coddy-skill:demo) extra.")
	if len(g) != 1 || g[0] != "demo" {
		t.Fatalf("got %#v", g)
	}
}

func TestParseInvokedCommandNames_pickLinkDedupSlash(t *testing.T) {
	g := skills.ParseInvokedCommandNames("[/demo](coddy-skill:demo) /demo trailing")
	if len(g) != 1 || g[0] != "demo" {
		t.Fatalf("got %#v want single demo", g)
	}
}

func TestParseInvokedCommandNames_rejectsUnequalCoddyHref(t *testing.T) {
	if g := skills.ParseInvokedCommandNames("[/demo](coddy-skill:other)"); len(g) != 0 {
		t.Fatalf("got %#v", g)
	}
}

func TestFilterSummariesByPrefix(t *testing.T) {
	sums := []skills.SkillSummary{{Name: "alpha"}, {Name: "Beta"}, {Name: "alphabet"}}
	got := skills.FilterSummariesByPrefix(sums, "AL")
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "alphabet" {
		t.Fatalf("got %#v", got)
	}
}

func TestPaginateSkillSummaries(t *testing.T) {
	sums := []skills.SkillSummary{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	p1, total, more := skills.PaginateSkillSummaries(sums, 1, 2)
	if total != 3 || !more || len(p1) != 2 || p1[0].Name != "a" || p1[1].Name != "b" {
		t.Fatalf("page1: %#v total=%d more=%v", p1, total, more)
	}
	p2, total2, more2 := skills.PaginateSkillSummaries(sums, 2, 2)
	if total2 != 3 || more2 || len(p2) != 1 || p2[0].Name != "c" {
		t.Fatalf("page2: %#v", p2)
	}
}

func TestBuildSlashCatalogMarkdown(t *testing.T) {
	md := skills.BuildSlashCatalogMarkdown([]skills.SkillSummary{{Name: "x", Description: "d"}})
	if !strings.Contains(md, "`/x`") || !strings.Contains(md, "d") {
		t.Fatalf("got %q", md)
	}
}

func TestBuildInvokedSkillsSection(t *testing.T) {
	loaded := []*skills.Skill{{
		Name: "SKILL", FilePath: filepath.Join("p", "foo", "SKILL.md"), Content: "full text",
	}}
	sec := skills.BuildInvokedSkillsSection(loaded, []string{"foo", "missing"})
	if !strings.Contains(sec, "### /foo") || !strings.Contains(sec, "full text") {
		t.Fatalf("got %q", sec)
	}
}
