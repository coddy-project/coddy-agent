package fs

// Godog harness for features/atomic_file_writes.feature: drives the write, edit
// and apply_patch tools against real temp files with the write fault of
// internal/platform armed, and asserts that a failed call leaves the file as it
// was. The fault is process-wide, so the suite is not parallel.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type atomicWriteState struct {
	dir     string
	restore func()
	lastErr error
}

func (s *atomicWriteState) reset() error {
	s.cleanup()
	dir, err := os.MkdirTemp("", "coddy-bdd-atomic-*")
	if err != nil {
		return err
	}
	s.dir = dir
	s.lastErr = nil
	return nil
}

func (s *atomicWriteState) cleanup() {
	if s.restore != nil {
		s.restore()
		s.restore = nil
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
		s.dir = ""
	}
}

func jsonArgs(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func (s *atomicWriteState) path(name string) string { return filepath.Join(s.dir, name) }

func (s *atomicWriteState) env() *tooling.Env { return &tooling.Env{CWD: s.dir} }

func (s *atomicWriteState) aFileWithContent(name string, doc *godog.DocString) error {
	return os.WriteFile(s.path(name), []byte(doc.Content+"\n"), 0o644)
}

func (s *atomicWriteState) theDiskFailsAfter(n int) error {
	s.restore = platform.InjectWriteFault(n)
	return nil
}

func (s *atomicWriteState) iWrite(name string, doc *godog.DocString) error {
	args, err := jsonArgs(writeArgs{Path: name, Content: doc.Content + "\n"})
	if err != nil {
		return err
	}
	_, s.lastErr = executeWrite(context.Background(), args, s.env())
	return nil
}

func (s *atomicWriteState) iEdit(name, old, replacement string) error {
	args, err := jsonArgs(editArgs{Path: name, OldString: old, NewString: replacement})
	if err != nil {
		return err
	}
	_, s.lastErr = executeEdit(context.Background(), args, s.env())
	return nil
}

func (s *atomicWriteState) iPatch(name, old, replacement string) error {
	patch := fmt.Sprintf("--- a/%[1]s\n+++ b/%[1]s\n@@ -1,3 +1,3 @@\n first line\n-%[2]s\n+%[3]s\n third line\n", name, old, replacement)
	args, err := jsonArgs(map[string]string{"path": name, "patch": patch})
	if err != nil {
		return err
	}
	_, s.lastErr = executeApplyPatch(context.Background(), args, s.env())
	return nil
}

func (s *atomicWriteState) toolFailsLeavingFile() error {
	if s.lastErr == nil {
		return errors.New("the tool succeeded, want a failure")
	}
	if !errors.Is(s.lastErr, platform.ErrInjectedWriteFault) {
		return fmt.Errorf("error %q is not the write fault", s.lastErr)
	}
	if !strings.Contains(s.lastErr.Error(), "the file was left as it was") {
		return fmt.Errorf("error %q does not say the file was left as it was", s.lastErr)
	}
	return nil
}

func (s *atomicWriteState) toolSucceeds() error {
	if s.lastErr != nil {
		return fmt.Errorf("the tool failed: %w", s.lastErr)
	}
	return nil
}

func (s *atomicWriteState) stillHoldsOriginal(name string) error {
	return s.holds(name, "first line\nsecond line\nthird line\n")
}

func (s *atomicWriteState) holdsContent(name string, doc *godog.DocString) error {
	return s.holds(name, doc.Content+"\n")
}

func (s *atomicWriteState) holds(name, want string) error {
	got, err := os.ReadFile(s.path(name))
	if err != nil {
		return err
	}
	if string(got) != want {
		return fmt.Errorf("%s holds %q, want %q", name, got, want)
	}
	return nil
}

func (s *atomicWriteState) noTemporaryFile() error {
	leftover, err := temporaryFileIn(s.dir)
	if err == nil && leftover != "" {
		err = fmt.Errorf("temporary file left behind: %s", leftover)
	}
	return err
}

// temporaryFileIn names a temporary file of the write helper left in dir, ""
// when there is none: it ends with a tilde.
func temporaryFileIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "~") {
			return e.Name(), nil
		}
	}
	return "", nil
}

func initializeAtomicWriteScenario(sc *godog.ScenarioContext) {
	s := &atomicWriteState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a file "([^"]+)" with the content:$`, s.aFileWithContent)
	sc.Step(`^the disk fails after (\d+) bytes$`, s.theDiskFailsAfter)
	sc.Step(`^I write "([^"]+)" with the content:$`, s.iWrite)
	sc.Step(`^I edit "([^"]+)" replacing "([^"]+)" with "([^"]+)"$`, s.iEdit)
	sc.Step(`^I patch "([^"]+)" replacing "([^"]+)" with "([^"]+)"$`, s.iPatch)
	sc.Step(`^the tool fails and says the file was left as it was$`, s.toolFailsLeavingFile)
	sc.Step(`^the tool succeeds$`, s.toolSucceeds)
	sc.Step(`^"([^"]+)" still holds the original content$`, s.stillHoldsOriginal)
	sc.Step(`^"([^"]+)" holds the content:$`, s.holdsContent)
	sc.Step(`^no temporary file is left in the folder$`, s.noTemporaryFile)
}

func TestAtomicFileWritesFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "atomic-file-writes",
		ScenarioInitializer: initializeAtomicWriteScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/atomic_file_writes.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("atomic file writes feature suite failed")
	}
}
