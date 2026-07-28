package fs

// Godog harness for features/edit_line_endings.feature: drives the edit tool
// directly (executeEdit) against real temp files and asserts that the file's
// line-ending style survives edits whose arguments use a different style.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type editLineEndingsState struct {
	dir string

	// Pending edit built up across the "replace ... the text" and
	// "with the text" steps before executeEdit runs.
	pendFile       string
	pendOld        string
	pendArgEnding  string
	pendReplaceAll bool

	lastErr error
}

func (s *editLineEndingsState) reset() error {
	s.cleanup()
	dir, err := os.MkdirTemp("", "coddy-bdd-edit-*")
	if err != nil {
		return err
	}
	s.dir = dir
	s.pendFile = ""
	s.pendOld = ""
	s.pendArgEnding = ""
	s.pendReplaceAll = false
	s.lastErr = nil
	return nil
}

func (s *editLineEndingsState) cleanup() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
		s.dir = ""
	}
}

// lineEnding maps a spec keyword ("CRLF"/"LF"/"CR") to its byte sequence.
func lineEnding(style string) string {
	switch style {
	case "CRLF":
		return "\r\n"
	case "CR":
		return "\r"
	default:
		return "\n"
	}
}

// toStyle rewrites LF-separated text (as authored in the .feature docstring)
// into the requested line-ending style.
func toStyle(text, style string) string {
	if style == "LF" || style == "" {
		return text
	}
	return strings.ReplaceAll(text, "\n", lineEnding(style))
}

// lineEndingStyle is an independent oracle: it classifies the line endings of a
// byte slice without reusing the code under test. A file that mixes styles (the
// corruption the fix prevents) is reported as "MIXED".
func lineEndingStyle(b []byte) string {
	s := string(b)
	crlf := strings.Count(s, "\r\n")
	stripped := strings.ReplaceAll(s, "\r\n", "")
	loneCR := strings.Count(stripped, "\r")
	loneLF := strings.Count(stripped, "\n")
	switch {
	case crlf == 0 && loneCR == 0 && loneLF == 0:
		return "NONE"
	case crlf > 0 && loneCR == 0 && loneLF == 0:
		return "CRLF"
	case crlf == 0 && loneLF > 0 && loneCR == 0:
		return "LF"
	case crlf == 0 && loneCR > 0 && loneLF == 0:
		return "CR"
	default:
		return "MIXED"
	}
}

func (s *editLineEndingsState) path(name string) string {
	return filepath.Join(s.dir, name)
}

func (s *editLineEndingsState) readFile(name string) ([]byte, error) {
	return os.ReadFile(s.path(name))
}

func (s *editLineEndingsState) aFileWithLineEndings(name, style string, doc *godog.DocString) error {
	content := toStyle(doc.Content, style) + lineEnding(style)
	if err := os.WriteFile(s.path(name), []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

func (s *editLineEndingsState) replaceOnce(name, argStyle string, doc *godog.DocString) error {
	return s.stageReplace(name, argStyle, false, doc)
}

func (s *editLineEndingsState) replaceAll(name, argStyle string, doc *godog.DocString) error {
	return s.stageReplace(name, argStyle, true, doc)
}

func (s *editLineEndingsState) stageReplace(name, argStyle string, all bool, doc *godog.DocString) error {
	s.pendFile = name
	s.pendArgEnding = argStyle
	s.pendReplaceAll = all
	s.pendOld = toStyle(doc.Content, argStyle)
	return nil
}

func (s *editLineEndingsState) withText(doc *godog.DocString) error {
	if s.pendFile == "" {
		return fmt.Errorf("no pending replacement: a \"replace ... the text\" step must come first")
	}
	newText := toStyle(doc.Content, s.pendArgEnding)
	replaceAll := s.pendReplaceAll
	args, err := json.Marshal(editArgs{
		Path:       s.pendFile,
		OldString:  s.pendOld,
		NewString:  newText,
		ReplaceAll: &replaceAll,
	})
	if err != nil {
		return err
	}
	_, s.lastErr = executeEdit(context.Background(), string(args), &tooling.Env{CWD: s.dir})
	return nil
}

func (s *editLineEndingsState) editSucceeds() error {
	if s.lastErr != nil {
		return fmt.Errorf("edit failed: %w", s.lastErr)
	}
	return nil
}

func (s *editLineEndingsState) usesLineEndings(name, style string) error {
	b, err := s.readFile(name)
	if err != nil {
		return err
	}
	if got := lineEndingStyle(b); got != style {
		return fmt.Errorf("%s uses %s line endings, want %s (content %q)", name, got, style, b)
	}
	return nil
}

func (s *editLineEndingsState) contains(name, substr string) error {
	b, err := s.readFile(name)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), substr) {
		return fmt.Errorf("%s does not contain %q (content %q)", name, substr, b)
	}
	return nil
}

func (s *editLineEndingsState) doesNotContain(name, substr string) error {
	b, err := s.readFile(name)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), substr) {
		return fmt.Errorf("%s still contains %q (content %q)", name, substr, b)
	}
	return nil
}

func (s *editLineEndingsState) endsWithNewline(name string) error {
	b, err := s.readFile(name)
	if err != nil {
		return err
	}
	str := string(b)
	if !strings.HasSuffix(str, "\n") && !strings.HasSuffix(str, "\r") {
		return fmt.Errorf("%s does not end with a newline (content %q)", name, b)
	}
	return nil
}

func initializeEditLineEndingsScenario(sc *godog.ScenarioContext) {
	s := &editLineEndingsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a file "([^"]+)" with (CRLF|LF|CR) line endings and content:$`, s.aFileWithLineEndings)
	sc.Step(`^I replace in "([^"]+)" using (LF|CRLF|CR) arguments the text:$`, s.replaceOnce)
	sc.Step(`^I replace every occurrence in "([^"]+)" using (LF|CRLF|CR) arguments the text:$`, s.replaceAll)
	sc.Step(`^with the text:$`, s.withText)
	sc.Step(`^the edit succeeds$`, s.editSucceeds)
	sc.Step(`^"([^"]+)" uses (CRLF|LF|CR) line endings$`, s.usesLineEndings)
	sc.Step(`^"([^"]+)" contains "([^"]*)"$`, s.contains)
	sc.Step(`^"([^"]+)" does not contain "([^"]*)"$`, s.doesNotContain)
	sc.Step(`^"([^"]+)" ends with a newline$`, s.endsWithNewline)
}

func TestEditLineEndingsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "edit-line-endings",
		ScenarioInitializer: initializeEditLineEndingsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/edit_line_endings.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("edit line-endings feature suite failed")
	}
}
