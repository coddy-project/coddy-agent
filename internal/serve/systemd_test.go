package serve

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeService is a UserService over a temporary home whose systemctl records
// its calls and answers from answer.
type fakeService struct {
	*UserService
	root   string
	calls  []string
	answer func(args []string) ([]byte, error)
	out    *bytes.Buffer
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home", "user")
	f := &fakeService{root: root, out: &bytes.Buffer{}}
	f.UserService = &UserService{
		Home:           home,
		ConfigHome:     filepath.Join(home, ".config"),
		Exe:            filepath.Join(home, ".local", "bin", "coddy"),
		PackagedBinary: filepath.Join(root, "usr", "bin", "coddy"),
		PackagedUnit:   filepath.Join(root, "usr", "lib", "systemd", "user", UnitName),
		Mkdir:          "/bin/mkdir",
		LingerDir:      filepath.Join(root, "linger"),
		User:           "user",
		Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
			f.calls = append(f.calls, strings.Join(args, " "))
			if f.answer != nil {
				return f.answer(args)
			}
			return nil, nil
		},
		CheckConfig: func(io.Writer, string) error { return nil },
		Out:         f.out,
	}
	writeTestFile(t, f.Exe, "#!/bin/sh\n")
	return f
}

// changes lists the systemctl calls that change something, leaving out the
// question setup and uninstall ask the user manager first.
func (f *fakeService) changes() []string {
	var out []string
	for _, c := range f.calls {
		if c != "--user show-environment" {
			out = append(out, c)
		}
	}
	return out
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeService) installPackage(t *testing.T) {
	t.Helper()
	writeTestFile(t, f.PackagedBinary, "#!/bin/sh\n")
	writeTestFile(t, f.PackagedUnit, PackagedUnitFile())
	f.Exe = f.PackagedBinary
}

func TestPackagedUnitIsTheRenderedOne(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "packaging", "systemd", UnitName))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != PackagedUnitFile() {
		t.Fatalf("packaging/systemd/coddy.service differs from PackagedUnitFile(); replace the file with:\n%s", PackagedUnitFile())
	}
}

func TestUnitFileQuotesPathsForSystemd(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/coddy":              "ExecStart=/usr/bin/coddy serve",
		"/home/a b/bin/coddy":         `ExecStart="/home/a b/bin/coddy" serve`,
		"/home/u/100%/coddy":          "ExecStart=/home/u/100%%/coddy serve",
		"/home/u/$bin/coddy":          "ExecStart=/home/u/$$bin/coddy serve",
		`/home/u/say "hi"/coddy`:      `ExecStart="/home/u/say \"hi\"/coddy" serve`,
		"/opt/my tools/$v 100%/coddy": `ExecStart="/opt/my tools/$$v 100%%/coddy" serve`,
	}
	for exe, want := range cases {
		unit := UnitFile("", exe, "/bin/mkdir")
		if !strings.Contains(unit, "\n"+want+"\n") {
			t.Errorf("exe %q: unit has no line %q:\n%s", exe, want, unit)
		}
	}
}

func TestSetupStopsOnTheFirstSystemctlFailure(t *testing.T) {
	f := newFakeService(t)
	f.answer = func(args []string) ([]byte, error) {
		if args[1] == "enable" {
			return []byte("Failed to enable unit: Unit file coddy.service does not exist."), errors.New("exit status 1")
		}
		return nil, nil
	}
	err := f.Setup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Unit file coddy.service does not exist") {
		t.Fatalf("error = %v", err)
	}
	if want := []string{"--user show-environment", "--user daemon-reload", "--user enable coddy.service"}; strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("systemctl calls = %q, want %q", f.calls, want)
	}
}

func TestSetupReportsAServiceThatDidNotStayUp(t *testing.T) {
	f := newFakeService(t)
	f.answer = func(args []string) ([]byte, error) {
		if args[1] == "status" {
			return []byte("Active: activating (auto-restart) (Result: exit-code)"), errors.New("exit status 3")
		}
		return nil, nil
	}
	err := f.Setup(context.Background())
	if err == nil {
		t.Fatal("setup succeeded for a service that keeps restarting")
	}
	for _, want := range []string{"did not stay up", "activating (auto-restart)", "journalctl --user -u coddy.service"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error has no %q: %v", want, err)
		}
	}
	if strings.Contains(f.out.String(), "is enabled and running") {
		t.Fatalf("setup claimed success:\n%s", f.out.String())
	}
}

func TestSetupChecksTheConfigurationBeforeTouchingAnything(t *testing.T) {
	f := newFakeService(t)
	f.CheckConfig = func(w io.Writer, home string) error {
		if home != filepath.Join(f.Home, ".coddy") {
			t.Errorf("checked home %s", home)
		}
		return errors.New("config test failed")
	}
	if err := f.Setup(context.Background()); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("error = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("systemctl was called: %q", f.calls)
	}
	if _, err := os.Stat(f.userUnit()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a unit was written: %v", err)
	}
}

func TestSetupRefusesWhileTheDaemonRuns(t *testing.T) {
	f := newFakeService(t)
	f.DaemonRunning = func(string) (int, bool) { return 4242, true }
	err := f.Setup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "4242") || !strings.Contains(err.Error(), "coddy serve stop") {
		t.Fatalf("error = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("systemctl was called: %q", f.calls)
	}
}

func TestSetupLeavesAUnitTheUserWroteAlone(t *testing.T) {
	f := newFakeService(t)
	mine := "[Service]\nExecStart=/opt/coddy serve --port 9000\n"
	writeTestFile(t, f.userUnit(), mine)
	err := f.Setup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "was not written by coddy serve setup") {
		t.Fatalf("error = %v", err)
	}
	if body, _ := os.ReadFile(f.userUnit()); string(body) != mine {
		t.Fatalf("the user's unit was changed:\n%s", body)
	}
	if len(f.changes()) != 0 {
		t.Fatalf("systemctl was asked to change something: %q", f.changes())
	}
}

func TestSetupFollowsABinaryThatMoved(t *testing.T) {
	f := newFakeService(t)
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(f.Home, "bin", "coddy")
	writeTestFile(t, moved, "#!/bin/sh\n")
	f.Exe = moved
	f.calls = nil
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(f.userUnit())
	if !strings.Contains(string(body), "\nExecStart="+moved+" serve\n") {
		t.Fatalf("unit still runs the old binary:\n%s", body)
	}
	if !strings.Contains(strings.Join(f.calls, "|"), "--user restart coddy.service") {
		t.Fatalf("the running service was not restarted: %q", f.calls)
	}
}

func TestSetupHandsAScriptUnitOverToThePackage(t *testing.T) {
	f := newFakeService(t)
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.installPackage(t)
	f.out.Reset()
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.userUnit()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the unit setup wrote still shadows the packaged one: %v", err)
	}
	if !strings.Contains(f.out.String(), f.PackagedUnit) {
		t.Fatalf("setup does not name the packaged unit:\n%s", f.out.String())
	}
}

func TestSetupSuggestsLingerOnlyWhenItIsOff(t *testing.T) {
	f := newFakeService(t)
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "loginctl enable-linger user") {
		t.Fatalf("no linger hint:\n%s", f.out.String())
	}
	writeTestFile(t, filepath.Join(f.LingerDir, "user"), "")
	f.out.Reset()
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.out.String(), "enable-linger") {
		t.Fatalf("linger hint although lingering is on:\n%s", f.out.String())
	}
}

func TestSetupSaysTheServiceIgnoresTheShellsCoddyHome(t *testing.T) {
	f := newFakeService(t)
	f.EnvHome = filepath.Join(f.Home, "work-coddy")
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "CODDY_HOME is "+f.EnvHome) {
		t.Fatalf("no CODDY_HOME note:\n%s", f.out.String())
	}
}

func TestSetupExplainsAnUnreachableUserManager(t *testing.T) {
	f := newFakeService(t)
	f.answer = func([]string) ([]byte, error) {
		return []byte("Failed to connect to bus: No medium found"), errors.New("exit status 1")
	}
	err := f.Setup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not reachable from this shell") {
		t.Fatalf("error = %v", err)
	}
}

func TestUninstallWithNothingInstalled(t *testing.T) {
	f := newFakeService(t)
	if err := f.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.changes()) != 0 {
		t.Fatalf("systemctl was asked to change something: %q", f.changes())
	}
	if !strings.Contains(f.out.String(), "nothing to remove") {
		t.Fatalf("output:\n%s", f.out.String())
	}
}

func TestUninstallDisablesButKeepsAUnitTheUserWrote(t *testing.T) {
	f := newFakeService(t)
	mine := "[Service]\nExecStart=/opt/coddy serve\n"
	writeTestFile(t, f.userUnit(), mine)
	if err := f.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "|"), "--user disable --now coddy.service") {
		t.Fatalf("not disabled: %q", f.calls)
	}
	if body, _ := os.ReadFile(f.userUnit()); string(body) != mine {
		t.Fatalf("the user's unit was changed or removed")
	}
	if !strings.Contains(f.out.String(), "not written by coddy serve setup") {
		t.Fatalf("output:\n%s", f.out.String())
	}
}

func TestUninstallKeepsTheUnitWhenSystemctlFails(t *testing.T) {
	f := newFakeService(t)
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.answer = func([]string) ([]byte, error) {
		return []byte("Failed to connect to bus: No medium found"), errors.New("exit status 1")
	}
	if err := f.Uninstall(context.Background()); err == nil {
		t.Fatal("uninstall succeeded without reaching the user manager")
	}
	if _, err := os.Stat(f.userUnit()); err != nil {
		t.Fatalf("the unit of a service that may still run was removed: %v", err)
	}
}

func TestDropInCarriesTheAbsoluteEntriesOfTheShellPath(t *testing.T) {
	sep := string(filepath.ListSeparator)
	abs := func(p string) string { return filepath.Join(string(filepath.Separator), p) }
	path := strings.Join([]string{abs("usr/local/go/bin"), "", "relative/bin", ".", abs("home/u/.local/bin")}, sep)
	got := DropInFile(path)
	want := "Environment=PATH=" + abs("usr/local/go/bin") + sep + abs("home/u/.local/bin")
	if runtime.GOOS == "windows" {
		want = "Environment=\"PATH=" + strings.ReplaceAll(abs("usr/local/go/bin")+sep+abs("home/u/.local/bin"), `\`, `\\`) + "\""
	}
	if !strings.Contains(got, "\n"+want+"\n") {
		t.Fatalf("drop-in has no line %q:\n%s", want, got)
	}
	if !strings.HasPrefix(got, UnitHeaderWritten+"\n") {
		t.Fatalf("drop-in does not say setup wrote it:\n%s", got)
	}
}

func TestDropInQuotesAPathWithSpaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH")
	}
	got := DropInFile("/opt/my tools/bin:/usr/bin")
	if !strings.Contains(got, "\nEnvironment=\"PATH=/opt/my tools/bin:/usr/bin\"\n") {
		t.Fatalf("drop-in:\n%s", got)
	}
}

func TestSetupWritesUnitsWhereTheUserManagerLooks(t *testing.T) {
	f := newFakeService(t)
	elsewhere := filepath.Join(f.Home, "cfg")
	f.answer = func(args []string) ([]byte, error) {
		if args[1] == "show-environment" {
			return []byte("HOME=" + f.Home + "\nXDG_CONFIG_HOME=" + elsewhere + "\n"), nil
		}
		return nil, nil
	}
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(elsewhere, "systemd", "user", UnitName),
		filepath.Join(elsewhere, "systemd", "user", UnitName+".d", DropInName),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestUninstallRemovesTheDropInAndKeepsOnesTheUserWrote(t *testing.T) {
	f := newFakeService(t)
	f.installPackage(t)
	if err := f.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(filepath.Dir(f.dropIn()), "override.conf")
	writeTestFile(t, mine, "[Service]\nEnvironment=LANG=C.UTF-8\n")
	if err := f.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.dropIn()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the drop-in setup wrote is still there: %v", err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("the user's drop-in went with it: %v", err)
	}
}
