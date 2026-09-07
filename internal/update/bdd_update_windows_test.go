//go:build windows

package update

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

const (
	helperFeatureStaged    = "the staged build of Coddy"
	helperFeatureInstalled = "the installed build of Coddy"
)

type helperFeatureState struct {
	dir       string
	staged    string
	target    string
	orphan    string
	bystander string
	handoff   bool
	restart   bool
	started   []string
	out       bytes.Buffer

	restoreProbe func(string) bool
	restoreStart func(string, ...string) error
}

func (s *helperFeatureState) reset() {
	if s.restoreProbe != nil {
		restartProbe = s.restoreProbe
	}
	if s.restoreStart != nil {
		startProcess = s.restoreStart
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	for _, path := range []string{s.orphan, s.bystander} {
		if path != "" {
			_ = os.Remove(path)
		}
	}
	*s = helperFeatureState{}
}

// stubRestart replaces the two calls that reach outside the process, so the
// scenarios can watch how the helper hands the restart over without starting a
// real executable.
func (s *helperFeatureState) stubRestart() {
	s.restoreProbe, s.restoreStart = restartProbe, startProcess
	s.handoff = true
	s.restart = true
	restartProbe = func(string) bool { return s.handoff }
	startProcess = func(target string, args ...string) error {
		s.started = append(s.started, strings.Join(append([]string{target}, args...), " "))
		return nil
	}
}

func (s *helperFeatureState) stagedUpdateNextToTheExecutable() error {
	dir, err := os.MkdirTemp("", "coddy-helper-feature-*")
	if err != nil {
		return err
	}
	s.dir = dir
	s.target = filepath.Join(dir, "coddy.exe")
	s.staged = filepath.Join(dir, ".coddy-update-staged")
	if err := os.WriteFile(s.target, []byte(helperFeatureInstalled), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.staged, []byte(helperFeatureStaged), 0o755); err != nil {
		return err
	}
	s.stubRestart()
	return nil
}

func (s *helperFeatureState) stagedReleasePredatesTheHandoff() error {
	s.handoff = false
	return nil
}

func (s *helperFeatureState) updateSkipsTheRestart() error {
	s.restart = false
	return nil
}

func (s *helperFeatureState) helperInstallsTheUpdate() error {
	req := windowsUpdateRequest{
		ParentPID:  os.Getpid(),
		Restart:    s.restart,
		StagedPath: s.staged,
		TargetPath: s.target,
	}
	return installWindowsUpdate(req, filepath.Join(os.TempDir(), helperPrefix+"feature.exe"), &s.out)
}

func (s *helperFeatureState) installedExecutableIsTheStagedOne() error {
	got, err := os.ReadFile(s.target)
	if err != nil {
		return err
	}
	if string(got) != helperFeatureStaged {
		return fmt.Errorf("installed executable = %q, want %q", got, helperFeatureStaged)
	}
	return nil
}

func (s *helperFeatureState) noLeftoversRemain() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "coddy.exe" {
			return fmt.Errorf("install directory still holds %q", entry.Name())
		}
	}
	return nil
}

func (s *helperFeatureState) restartCarriesTheHelperPath() error {
	if len(s.started) != 1 {
		return fmt.Errorf("started %d processes, want 1: %v", len(s.started), s.started)
	}
	if !strings.Contains(s.started[0], restartAfterUpdateCommand) || !strings.Contains(s.started[0], helperPrefix) {
		return fmt.Errorf("restart command = %q", s.started[0])
	}
	return nil
}

func (s *helperFeatureState) cleanupHappensWithoutARestart() error {
	if len(s.started) != 1 {
		return fmt.Errorf("started %d processes, want 1: %v", len(s.started), s.started)
	}
	if !strings.Contains(s.started[0], helperPrefix) || !strings.Contains(s.started[0], "--no-restart") {
		return fmt.Errorf("cleanup command = %q, want a helper deletion that does not start Coddy", s.started[0])
	}
	return nil
}

func (s *helperFeatureState) restartOmitsTheHandoff() error {
	if len(s.started) != 1 {
		return fmt.Errorf("started %d processes, want 1: %v", len(s.started), s.started)
	}
	if s.started[0] != s.target {
		return fmt.Errorf("restart command = %q, want a plain %q", s.started[0], s.target)
	}
	return nil
}

func (s *helperFeatureState) orphanedHelperInTemp() error {
	helper, err := os.CreateTemp("", helperPrefix+"*.exe")
	if err != nil {
		return err
	}
	s.orphan = helper.Name()
	if err := helper.Close(); err != nil {
		return err
	}
	bystander, err := os.CreateTemp("", "coddy-not-a-helper-*.exe")
	if err != nil {
		return err
	}
	s.bystander = bystander.Name()
	return bystander.Close()
}

func (s *helperFeatureState) coddySweepsOldHelpers() error {
	sweepOrphanedHelpers()
	return nil
}

func (s *helperFeatureState) orphanedHelperIsGone() error {
	if _, err := os.Stat(s.orphan); !os.IsNotExist(err) {
		return fmt.Errorf("helper %s survived the sweep (stat err %v)", s.orphan, err)
	}
	return nil
}

func (s *helperFeatureState) bystanderSurvives() error {
	if _, err := os.Stat(s.bystander); err != nil {
		return fmt.Errorf("the sweep removed an unrelated file: %w", err)
	}
	return nil
}

func TestWindowsUpdateHelperFeature(t *testing.T) {
	s := &helperFeatureState{}
	t.Cleanup(s.reset)

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^a staged Coddy update next to the installed executable$`, s.stagedUpdateNextToTheExecutable)
			sc.Step(`^the staged release does not understand the restart handoff$`, s.stagedReleasePredatesTheHandoff)
			sc.Step(`^the update is installed without starting Coddy again$`, s.updateSkipsTheRestart)
			sc.Step(`^the update helper installs it$`, s.helperInstallsTheUpdate)
			sc.Step(`^the installed executable is the staged one$`, s.installedExecutableIsTheStagedOne)
			sc.Step(`^the helper leaves no staging or backup files behind$`, s.noLeftoversRemain)
			sc.Step(`^the helper hands the restart the path of the helper to delete$`, s.restartCarriesTheHelperPath)
			sc.Step(`^the helper starts Coddy without the handoff$`, s.restartOmitsTheHandoff)
			sc.Step(`^the helper hands the cleanup over without starting Coddy$`, s.cleanupHappensWithoutARestart)
			sc.Step(`^an update helper left in the system temporary directory$`, s.orphanedHelperInTemp)
			sc.Step(`^Coddy sweeps helpers from earlier updates$`, s.coddySweepsOldHelpers)
			sc.Step(`^the helper left by the earlier update is gone$`, s.orphanedHelperIsGone)
			sc.Step(`^unrelated files in the temporary directory are untouched$`, s.bystanderSurvives)
		},
		Options: &godog.Options{
			Format: "progress",
			Paths:  []string{"../../features/update_windows.feature"},
		},
	}
	if suite.Run() != 0 {
		t.Fatal("windows update helper feature failed")
	}
}
