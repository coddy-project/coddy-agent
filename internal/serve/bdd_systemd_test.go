package serve

// Godog harness for features/serve_systemd_service.feature: `coddy serve setup`
// and `coddy serve uninstall` over a temporary home, with the package's files
// laid out under a temporary root and a systemctl stand-in that records what it
// was asked and remembers what that did.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// shellPath is the PATH of the shell a scenario runs setup from.
var shellPath = strings.Join([]string{
	filepath.FromSlash("/home/user/go/bin"),
	filepath.FromSlash("/home/user/.local/bin"),
	filepath.FromSlash("/usr/bin"),
}, string(filepath.ListSeparator))

type systemdFeatureState struct {
	root string
	home string
	svc  *UserService
	out  bytes.Buffer
	err  error

	calls   [][]string
	enabled bool
	active  bool

	// unit is the text of the unit a scenario started coddy serve from, and env
	// the role variable it set, restored after the scenario.
	unit        string
	prevRole    string
	hadPrevRole bool
}

func (s *systemdFeatureState) reset() error {
	s.cleanup()
	root, err := os.MkdirTemp("", "coddy-systemd-")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home", "user")
	if err := os.MkdirAll(s.home, 0o755); err != nil {
		return err
	}
	s.out.Reset()
	s.err = nil
	s.calls = nil
	s.enabled, s.active = false, false
	s.unit = ""
	s.svc = &UserService{
		Home:           s.home,
		ConfigHome:     filepath.Join(s.home, ".config"),
		PackagedBinary: filepath.Join(root, "usr", "bin", "coddy"),
		PackagedUnit:   filepath.Join(root, "usr", "lib", "systemd", "user", UnitName),
		Mkdir:          "/bin/mkdir",
		LingerDir:      filepath.Join(root, "var", "lib", "systemd", "linger"),
		User:           "user",
		ShellPath:      shellPath,
		Systemctl:      s.systemctl,
		CheckConfig: func(w io.Writer, home string) error {
			return config.RunCheck(w, config.CLIPaths{Home: home, Config: filepath.Join(home, "config.yaml")})
		},
		Out: &s.out,
	}
	return nil
}

func (s *systemdFeatureState) cleanup() {
	if s.hadPrevRole {
		_ = os.Setenv(EnvRole, s.prevRole)
	} else {
		_ = os.Unsetenv(EnvRole)
	}
	s.hadPrevRole = false
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// systemctl answers the way a user manager does for the verbs setup and
// uninstall use, and keeps the state those verbs change.
func (s *systemdFeatureState) systemctl(_ context.Context, args ...string) ([]byte, error) {
	s.calls = append(s.calls, args)
	verb := ""
	if len(args) > 1 {
		verb = args[1]
	}
	switch verb {
	case "enable":
		s.enabled = true
	case "disable":
		s.enabled = false
		if contains(args, "--now") {
			s.active = false
		}
	case "restart":
		s.active = true
	case "status":
		if !s.active {
			return []byte("○ coddy.service - Coddy agent server\n     Active: inactive (dead)\n"), errors.New("exit status 3")
		}
		return []byte("● coddy.service - Coddy agent server\n     Active: active (running)\n"), nil
	}
	return nil, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// path turns the "~/..." of a step into a path under the scenario's home.
func (s *systemdFeatureState) path(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(s.home, filepath.FromSlash(rest))
	}
	return p
}

func (s *systemdFeatureState) scriptInstall(exe string) error {
	path := s.path(exe)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		return err
	}
	s.svc.Exe = path
	return nil
}

func (s *systemdFeatureState) packageInstall() error {
	for path, body := range map[string]string{
		s.svc.PackagedBinary: "#!/bin/sh\n",
		s.svc.PackagedUnit:   PackagedUnitFile(),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			return err
		}
	}
	s.svc.Exe = s.svc.PackagedBinary
	return nil
}

func (s *systemdFeatureState) validConfig() error {
	dir := filepath.Join(s.home, ".coddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: local
    type: openai
    api_base: http://127.0.0.1:11434/v1
models:
  - model: local/qwen
    max_tokens: 4096
agent:
  model: local/qwen
`), 0o644)
}

func (s *systemdFeatureState) runSetup() error {
	s.err = s.svc.Setup(context.Background())
	return nil
}

func (s *systemdFeatureState) ranSetup() error {
	if err := s.svc.Setup(context.Background()); err != nil {
		return fmt.Errorf("setup failed: %w\n%s", err, s.out.String())
	}
	s.calls = nil
	s.out.Reset()
	return nil
}

func (s *systemdFeatureState) runUninstall() error {
	s.err = s.svc.Uninstall(context.Background())
	return nil
}

func (s *systemdFeatureState) readUnit(path string) (string, error) {
	body, err := os.ReadFile(s.path(path))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (s *systemdFeatureState) unitRuns(path, command string) error {
	if s.err != nil {
		return fmt.Errorf("setup failed: %w\n%s", s.err, s.out.String())
	}
	unit, err := s.readUnit(path)
	if err != nil {
		return err
	}
	exe, args, _ := strings.Cut(command, " ")
	want := "ExecStart=" + s.path(exe) + " " + args
	if !strings.Contains(unit, "\n"+want+"\n") {
		return fmt.Errorf("unit %s has no line %q:\n%s", path, want, unit)
	}
	s.unit = unit
	return nil
}

func (s *systemdFeatureState) unitWorksIn(dir string) error {
	rest, ok := strings.CutPrefix(dir, "~/")
	if !ok {
		return fmt.Errorf("step names %q, not a folder under the home", dir)
	}
	want := "WorkingDirectory=-%h/" + rest
	if !strings.Contains(s.unit, "\n"+want+"\n") {
		return fmt.Errorf("unit has no line %q:\n%s", want, s.unit)
	}
	return nil
}

func (s *systemdFeatureState) folderExists(dir string) error {
	info, err := os.Stat(s.path(dir))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	return nil
}

// calledInOrder checks that every wanted call was made, in this order, with
// anything else allowed in between.
func (s *systemdFeatureState) calledInOrder(want ...[]string) error {
	i := 0
	for _, call := range s.calls {
		if i < len(want) && strings.Join(call, " ") == strings.Join(want[i], " ") {
			i++
		}
	}
	if i < len(want) {
		return fmt.Errorf("systemctl was never asked %q in order; calls: %q", want[i], s.calls)
	}
	return nil
}

func (s *systemdFeatureState) servicePath() error {
	if s.err != nil {
		return fmt.Errorf("setup failed: %w\n%s", s.err, s.out.String())
	}
	body, err := os.ReadFile(s.svc.dropIn())
	if err != nil {
		return err
	}
	if !strings.Contains(string(body), "\nEnvironment="+systemdQuote("PATH="+shellPath)+"\n") {
		return fmt.Errorf("the drop-in does not hand over %q:\n%s", shellPath, body)
	}
	return nil
}

func (s *systemdFeatureState) pathGone() error {
	if _, err := os.Stat(s.svc.dropIn()); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is still there (err %v)", s.svc.dropIn(), err)
	}
	return nil
}

func (s *systemdFeatureState) reloadedEnabledRestarted() error {
	if s.err != nil {
		return fmt.Errorf("setup failed: %w\n%s", s.err, s.out.String())
	}
	return s.calledInOrder(
		[]string{"--user", "daemon-reload"},
		[]string{"--user", "enable", UnitName},
		[]string{"--user", "restart", UnitName},
	)
}

func (s *systemdFeatureState) reportsRunning() error {
	if s.err != nil {
		return fmt.Errorf("setup failed: %w\n%s", s.err, s.out.String())
	}
	out := s.out.String()
	for _, want := range []string{"coddy.service is enabled and running", "journalctl --user -u coddy.service"} {
		if !strings.Contains(out, want) {
			return fmt.Errorf("setup output has no %q:\n%s", want, out)
		}
	}
	return nil
}

func (s *systemdFeatureState) noUnitUnder(dir string) error {
	path := filepath.Join(s.path(dir), UnitName)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s exists (err %v)", path, err)
	}
	return nil
}

func (s *systemdFeatureState) disabledAndStopped() error {
	if s.err != nil {
		return fmt.Errorf("uninstall failed: %w\n%s", s.err, s.out.String())
	}
	if err := s.calledInOrder([]string{"--user", "disable", "--now", UnitName}); err != nil {
		return err
	}
	if s.enabled || s.active {
		return fmt.Errorf("coddy.service is still enabled=%v active=%v", s.enabled, s.active)
	}
	return nil
}

func (s *systemdFeatureState) unitGone(path string) error {
	if _, err := os.Stat(s.path(path)); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is still there (err %v)", path, err)
	}
	return nil
}

func (s *systemdFeatureState) userFilesKept(dir string) error {
	if _, err := os.Stat(filepath.Join(s.home, ".coddy", "config.yaml")); err != nil {
		return err
	}
	return s.folderExists(dir)
}

func (s *systemdFeatureState) packagedUnitKept() error {
	body, err := os.ReadFile(s.svc.PackagedUnit)
	if err != nil {
		return err
	}
	if string(body) != PackagedUnitFile() {
		return errors.New("the packaged unit was changed")
	}
	return nil
}

// startedByUnit applies the Environment= lines of the unit setup writes to
// this process, which is what systemd does before it runs ExecStart.
func (s *systemdFeatureState) startedByUnit() error {
	s.unit = UnitFile(UnitHeaderWritten, "/home/user/.local/bin/coddy", "/bin/mkdir")
	s.prevRole, s.hadPrevRole = os.LookupEnv(EnvRole)
	_ = os.Unsetenv(EnvRole)
	found := false
	for _, line := range strings.Split(s.unit, "\n") {
		assignment, ok := strings.CutPrefix(line, "Environment=")
		if !ok {
			continue
		}
		name, value, _ := strings.Cut(assignment, "=")
		if err := os.Setenv(name, value); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return errors.New("the unit sets no environment")
	}
	return nil
}

func (s *systemdFeatureState) mayEndForRestart() error {
	if !Supervised() {
		return fmt.Errorf("coddy serve started by the unit does not count as supervised (%s=%q)", EnvRole, Role())
	}
	return nil
}

func (s *systemdFeatureState) unitRestartsOnExitStatus() error {
	want := "RestartForceExitStatus=" + strconv.Itoa(ExitRestart)
	if !strings.Contains(s.unit, "\n"+want+"\n") {
		return fmt.Errorf("unit has no line %q:\n%s", want, s.unit)
	}
	return nil
}

func initializeSystemdScenario(sc *godog.ScenarioContext) {
	s := &systemdFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return ctx, err
	})

	sc.Step(`^a Linux account whose coddy runs from "([^"]*)"$`, s.scriptInstall)
	sc.Step(`^a Linux account whose coddy is the packaged binary with its unit$`, s.packageInstall)
	sc.Step(`^the account has a valid configuration$`, s.validConfig)
	sc.Step(`^the user runs coddy serve setup$`, s.runSetup)
	sc.Step(`^the user ran coddy serve setup$`, s.ranSetup)
	sc.Step(`^the user runs coddy serve uninstall$`, s.runUninstall)
	sc.Step(`^the unit "([^"]*)" runs "([^"]*)"$`, s.unitRuns)
	sc.Step(`^the unit works in "([^"]*)"$`, s.unitWorksIn)
	sc.Step(`^the folder "([^"]*)" exists$`, s.folderExists)
	sc.Step(`^the service gets the PATH of the shell setup ran from$`, s.servicePath)
	sc.Step(`^the PATH setup handed to the service is gone$`, s.pathGone)
	sc.Step(`^systemd reloaded its units, enabled coddy\.service and restarted it$`, s.reloadedEnabledRestarted)
	sc.Step(`^setup reports the service running and how to read its log$`, s.reportsRunning)
	sc.Step(`^no unit is written under "([^"]*)"$`, s.noUnitUnder)
	sc.Step(`^systemd disabled and stopped coddy\.service$`, s.disabledAndStopped)
	sc.Step(`^the unit "([^"]*)" is gone$`, s.unitGone)
	sc.Step(`^the configuration and the folder "([^"]*)" are still there$`, s.userFilesKept)
	sc.Step(`^the packaged unit is still installed$`, s.packagedUnitKept)
	sc.Step(`^coddy serve was started by the unit setup writes$`, s.startedByUnit)
	sc.Step(`^it may end itself for a restart$`, s.mayEndForRestart)
	sc.Step(`^the unit starts it again on the status it exits with$`, s.unitRestartsOnExitStatus)
}

func TestServeSystemdServiceFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "serve-systemd-service",
		ScenarioInitializer: initializeSystemdScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/serve_systemd_service.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("serve systemd service feature suite failed")
	}
}
