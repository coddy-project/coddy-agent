package agent

// Godog harness for features/at_line_range_mention.feature: drives both prompt
// hydration entry points over a real temp workspace - BuildHydratedComposerPrompt
// for an explicit composer attachment, HydratePromptContentBlocks for a ranged
// @path typed into the prompt text - then renders the blocks the way a turn does
// (contentBlocksToText) to assert the XML the model actually receives.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type atLineRangeState struct {
	dir    string
	blocks []acp.ContentBlock
}

func (s *atLineRangeState) reset() error {
	s.cleanup()
	dir, err := os.MkdirTemp("", "coddy-bdd-at-range-*")
	if err != nil {
		return err
	}
	// The temp root may be a symlink (/var -> /private/var on macOS); the
	// hydration helpers resolve paths, so compare against the resolved root.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	s.dir = dir
	s.blocks = nil
	return nil
}

func (s *atLineRangeState) cleanup() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
		s.dir = ""
	}
}

func (s *atLineRangeState) aWorkspaceFile(name string, doc *godog.DocString) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte(doc.Content), 0o644)
}

func (s *atLineRangeState) attachRangeToPrompt(name string, start, end int, input string) error {
	blocks, err := session.BuildHydratedComposerPrompt(s.dir, input, []session.PromptFileAttachment{{
		Path:   name,
		Source: &session.PromptFileAttachmentSourceField{StartLine: start, EndLine: end},
	}})
	if err != nil {
		return fmt.Errorf("attach %q lines %d-%d: %w", name, start, end, err)
	}
	s.blocks = blocks
	return nil
}

func (s *atLineRangeState) hydratePromptText(input string) error {
	blocks, err := session.HydratePromptContentBlocks(s.dir, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: input}})
	if err != nil {
		return fmt.Errorf("hydrate %q: %w", input, err)
	}
	s.blocks = blocks
	return nil
}

func (s *atLineRangeState) resourceFor(uri string) (*acp.Resource, error) {
	for _, b := range s.blocks {
		if b.Type != "resource" || b.Resource == nil {
			continue
		}
		if filepath.ToSlash(b.Resource.URI) == uri {
			return b.Resource, nil
		}
	}
	return nil, fmt.Errorf("no resource block for %q in %+v", uri, s.blocks)
}

func (s *atLineRangeState) hasResource(uri string) error {
	_, err := s.resourceFor(uri)
	return err
}

func (s *atLineRangeState) resourceText(doc *godog.DocString) error {
	res, err := s.soleResource()
	if err != nil {
		return err
	}
	if res.Text != doc.Content {
		return fmt.Errorf("resource %q text is %q, want %q", res.URI, res.Text, doc.Content)
	}
	return nil
}

func (s *atLineRangeState) soleResource() (*acp.Resource, error) {
	var found []*acp.Resource
	for _, b := range s.blocks {
		if b.Type == "resource" && b.Resource != nil {
			found = append(found, b.Resource)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("expected exactly one resource block, got %d", len(found))
	}
	return found[0], nil
}

// attachmentOpenTagRe finds the opening tag of the rendered attachment so the
// assertions read the same XML the model is handed.
var attachmentOpenTagRe = regexp.MustCompile(`<coddy_attachment [^>]*>`)

func (s *atLineRangeState) openTagFor(path string) (string, error) {
	rendered := contentBlocksToText(s.blocks)
	for _, tag := range attachmentOpenTagRe.FindAllString(rendered, -1) {
		if strings.Contains(tag, fmt.Sprintf(`path=%q`, path)) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no attachment tag for %q in:\n%s", path, rendered)
}

func (s *atLineRangeState) attachmentWithLines(path, lines string) error {
	tag, err := s.openTagFor(path)
	if err != nil {
		return err
	}
	want := fmt.Sprintf(`lines=%q`, lines)
	if !strings.Contains(tag, want) {
		return fmt.Errorf("attachment tag %q lacks %s", tag, want)
	}
	return nil
}

func (s *atLineRangeState) attachmentWithoutLines(path string) error {
	tag, err := s.openTagFor(path)
	if err != nil {
		return err
	}
	if strings.Contains(tag, "lines=") {
		return fmt.Errorf("attachment tag %q should carry no line range", tag)
	}
	return nil
}

func initializeAtLineRangeScenario(sc *godog.ScenarioContext) {
	s := &atLineRangeState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a workspace file "([^"]+)" with content:$`, s.aWorkspaceFile)
	sc.Step(`^I attach "([^"]+)" lines (\d+) to (\d+) to the prompt "([^"]*)"$`, s.attachRangeToPrompt)
	sc.Step(`^I hydrate the prompt text "([^"]*)"$`, s.hydratePromptText)
	sc.Step(`^the prompt has a resource for "([^"]+)"$`, s.hasResource)
	sc.Step(`^the resource text is:$`, s.resourceText)
	sc.Step(`^the model sees an attachment for "([^"]+)" with lines "([^"]+)"$`, s.attachmentWithLines)
	sc.Step(`^the model sees an attachment for "([^"]+)" without a line range$`, s.attachmentWithoutLines)
}

func TestAtLineRangeMentionFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "at-line-range-mention",
		ScenarioInitializer: initializeAtLineRangeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/at_line_range_mention.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("at line range mention feature suite failed")
	}
}
