package serve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// UnitName is the systemd user unit `coddy serve` runs under.
const UnitName = "coddy.service"

// Where the .deb and .rpm put the binary and the user unit. The unit is
// installed and not enabled: a package must not start a server for every
// account on the machine, so each user turns it on with `coddy serve setup`.
const (
	PackagedBinary = "/usr/bin/coddy"
	PackagedUnit   = "/usr/lib/systemd/user/" + UnitName
)

// ServiceWorkspace is the folder under the user's home the service works in.
// The working directory of `coddy serve` is the workspace of every session
// opened without one, and the agent home next door holds the configuration and
// the provider keys, so the service gets a folder of its own.
const ServiceWorkspace = "Coddy"

// The first comment line of a unit tells who put it there. A first line that
// starts with setupMarker is how setup and uninstall recognise a file setup
// wrote, and so the only kind they overwrite or delete; a unit the user wrote
// by hand is left alone. The rest of the line is prose and may change between
// releases; the marker may not, or files an older setup wrote stop being ours.
const setupMarker = "# Written by `coddy serve setup`"

const (
	UnitHeaderWritten  = setupMarker + "; `coddy serve uninstall` removes it."
	UnitHeaderPackaged = "# Installed by the coddy package and not enabled. Run `coddy serve setup`\n# as the user the service is for (without sudo) to enable and start it."
)

// DropInName is the drop-in setup writes next to either unit, under
// coddy.service.d: the environment the service needs from the user's shell.
const DropInName = "coddy-setup.conf"

// DropInFile renders the drop-in that hands the service the PATH of the shell
// setup ran from. A user manager starts services with a bare system PATH, and
// an agent that cannot find the go, node or python a terminal finds is not
// much of a coding agent.
func DropInFile(path string) string {
	dirs := absolutePath(path)
	var b strings.Builder
	b.WriteString(UnitHeaderWritten + "\n")
	b.WriteString("# The PATH of the shell `coddy serve setup` ran from. Run setup again to\n")
	b.WriteString("# refresh it, or add a drop-in of your own with `systemctl --user edit coddy.service`.\n")
	b.WriteString("[Service]\n")
	if len(dirs) > 0 {
		b.WriteString("Environment=" + systemdQuote("PATH="+strings.Join(dirs, string(filepath.ListSeparator))) + "\n")
	}
	return b.String()
}

// absolutePath is the entries of a PATH that mean the same thing in a service,
// which has no current directory of the shell's: the absolute ones.
func absolutePath(path string) []string {
	var dirs []string
	for _, dir := range filepath.SplitList(path) {
		if dir != "" && filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// serviceSettle is how long setup lets a freshly started service run before it
// asks whether the service stayed up: a configuration that fails on startup
// shows as "activating (auto-restart)" by then rather than as "active".
const serviceSettle = 2 * time.Second

// UnitFile renders the user unit that runs exe. mkdir is the absolute path of
// mkdir(1), which creates the workspace on a first start.
func UnitFile(header, exe, mkdir string) string {
	workspace := "%h/" + ServiceWorkspace
	var b strings.Builder
	if header != "" {
		b.WriteString(header + "\n")
	}
	b.WriteString(`[Unit]
Description=Coddy agent server (coddy serve)
Documentation=https://coddy.dev/docs/operate/serve man:coddy(1)

[Service]
Type=simple
# Tells coddy serve that systemd starts it again, so a configuration change
# that moves a listen address ends the process with status 75 instead of
# waiting for a manual restart.
Environment=` + EnvRole + `=` + RoleService + `
# The workspace of every session opened without one. The "-" lets ExecStartPre
# run while the folder is still missing; ExecStart then starts inside it.
WorkingDirectory=-` + workspace + `
ExecStartPre=` + systemdPath(mkdir) + ` -p ` + workspace + `
ExecStart=` + systemdPath(exe) + ` serve
Restart=on-failure
# Status 75 is coddy serve asking for a fresh process: a clean exit that is
# restarted all the same, not a failure.
SuccessExitStatus=` + strconv.Itoa(ExitRestart) + `
RestartForceExitStatus=` + strconv.Itoa(ExitRestart) + `
RestartSec=5s
# systemd waits this long for coddy serve to finish after SIGTERM, then kills it.
TimeoutStopSec=40s

[Install]
WantedBy=default.target
`)
	return b.String()
}

// PackagedUnitFile is packaging/systemd/coddy.service, byte for byte; a test
// holds the file to it.
func PackagedUnitFile() string {
	return UnitFile(UnitHeaderPackaged, PackagedBinary, "/bin/mkdir")
}

// systemdPath quotes a path for a unit's command line: "%" and "$" would be
// expanded by systemd, and a space would split the path into two words.
func systemdPath(p string) string {
	return systemdQuote(strings.ReplaceAll(p, "$", "$$"))
}

// systemdQuote escapes the specifiers of a unit setting and quotes it when a
// space, a quote or a backslash would otherwise change how systemd splits it.
func systemdQuote(v string) string {
	v = strings.ReplaceAll(v, "%", "%%")
	if !strings.ContainsAny(v, " \t\"'\\") {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// UserService is the systemd user unit of one account: `coddy serve setup`
// and `coddy serve uninstall`. Every outside dependency is a field, so tests
// run it over a temporary home and a stand-in systemctl.
type UserService struct {
	// Home is the user's home directory, which systemd names %h.
	Home string
	// ConfigHome is $XDG_CONFIG_HOME: the user manager reads the units a user
	// installed from its systemd/user folder.
	ConfigHome string
	// Exe is the coddy binary the service runs.
	Exe string
	// PackagedBinary and PackagedUnit are where the packages install the
	// binary and its unit.
	PackagedBinary string
	PackagedUnit   string
	// Mkdir is the absolute path of mkdir(1) for a unit setup writes.
	Mkdir string
	// LingerDir is where logind records the accounts whose user manager runs
	// without a login session, and User the name it records this one under.
	LingerDir string
	User      string
	// EnvHome is $CODDY_HOME in the shell setup runs from. The service does not
	// see it: the user manager has an environment of its own.
	EnvHome string
	// ShellPath is $PATH in the shell setup runs from, handed to the service.
	ShellPath string
	// Systemctl runs systemctl with args and returns what it printed.
	Systemctl func(ctx context.Context, args ...string) ([]byte, error)
	// CheckConfig validates the config.yaml of an agent home, printing the
	// report to w, and fails on an error.
	CheckConfig func(w io.Writer, home string) error
	// DaemonRunning reports a `coddy serve --daemon` already running for an
	// agent home. Nil means none.
	DaemonRunning func(home string) (pid int, running bool)
	// Out is where the report goes.
	Out io.Writer
	// Settle is how long setup waits after starting the service before it
	// checks the service is still up. Zero checks at once.
	Settle time.Duration
}

// NewUserService describes the systemd user unit of the account running this
// process, for the coddy binary running it.
func NewUserService(out io.Writer) (*UserService, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("the coddy systemd user service needs Linux with systemd; use `coddy serve --daemon` here")
	}
	if os.Geteuid() == 0 {
		return nil, errors.New("run this as the user the service is for, without sudo: a systemd user service belongs to one account")
	}
	systemctl, ok := systemctlPath()
	if !ok {
		return nil, errors.New("systemctl was not found: this system does not run systemd; use `coddy serve --daemon` instead")
	}
	// %h in a unit is the home of the account record, which is also the
	// $HOME the service starts with; a shell can export another one.
	home, name := "", ""
	if u, err := user.Current(); err == nil {
		home, name = u.HomeDir, u.Username
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find the home directory: %w", err)
		}
		home = h
	}
	exe, err := invokedExecutable()
	if err != nil {
		return nil, err
	}
	return &UserService{
		Home: home,
		// Setup asks the user manager whether it reads another one.
		ConfigHome:     filepath.Join(home, ".config"),
		Exe:            exe,
		PackagedBinary: PackagedBinary,
		PackagedUnit:   PackagedUnit,
		Mkdir:          mkdirPath(),
		LingerDir:      "/var/lib/systemd/linger",
		User:           name,
		EnvHome:        strings.TrimSpace(os.Getenv(config.EnvCODDYHome)),
		ShellPath:      os.Getenv("PATH"),
		Systemctl: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, systemctl, args...).CombinedOutput()
		},
		CheckConfig: func(w io.Writer, home string) error {
			return config.RunCheck(w, config.CLIPaths{Home: home, Config: filepath.Join(home, "config.yaml")})
		},
		DaemonRunning: func(home string) (int, bool) {
			rec, err := ReadRecord(home)
			if err != nil || !rec.Running() {
				return 0, false
			}
			return rec.PID, true
		},
		Out:    out,
		Settle: serviceSettle,
	}, nil
}

// invokedExecutable is the path this coddy was started by, symlinks kept: a
// unit that names ~/.local/bin/coddy or a Homebrew bin link keeps working after
// an update moves the file behind it, where the resolved path would not.
func invokedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the coddy binary: %w", err)
	}
	running, err := os.Stat(exe)
	if err != nil {
		return exe, nil
	}
	path := os.Args[0]
	if path != "" && !strings.ContainsRune(path, os.PathSeparator) {
		path, _ = exec.LookPath(path)
	}
	if path == "" {
		return exe, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return exe, nil
	}
	// Only a path that leads to the very binary running is kept; anything else
	// (a PATH that changed, an argv[0] that was made up) falls back to the file.
	if invoked, err := os.Stat(abs); err == nil && os.SameFile(invoked, running) {
		return abs, nil
	}
	return exe, nil
}

// systemctlPath finds systemctl on PATH, or where every systemd distribution
// puts it when PATH is empty or trimmed.
func systemctlPath() (string, bool) {
	if p, err := exec.LookPath("systemctl"); err == nil {
		return p, true
	}
	for _, p := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// mkdirPath is mkdir(1) for a unit: /bin/mkdir wherever it exists, which covers
// merged and split /usr alike, and whatever PATH has otherwise.
func mkdirPath() string {
	if _, err := os.Stat("/bin/mkdir"); err == nil {
		return "/bin/mkdir"
	}
	if p, err := exec.LookPath("mkdir"); err == nil {
		return p
	}
	return "/bin/mkdir"
}

func (s *UserService) agentHome() string { return filepath.Join(s.Home, ".coddy") }

func (s *UserService) workspace() string { return filepath.Join(s.Home, ServiceWorkspace) }

// userUnit is where a unit setup writes goes. The user manager prefers it to
// the packaged one of the same name.
func (s *UserService) userUnit() string {
	return filepath.Join(s.ConfigHome, "systemd", "user", UnitName)
}

// packaged reports whether this coddy is the packaged binary and the package's
// unit is there to run it. The binaries are compared as files, so /bin/coddy on
// a merged /usr counts as /usr/bin/coddy.
func (s *UserService) packaged() bool {
	if !fileExists(s.PackagedUnit) {
		return false
	}
	a, err := os.Stat(s.Exe)
	if err != nil {
		return false
	}
	b, err := os.Stat(s.PackagedBinary)
	if err != nil {
		return false
	}
	return os.SameFile(a, b)
}

// dropIn is the drop-in setup writes, which applies to whichever unit the
// user manager loads under the name.
func (s *UserService) dropIn() string {
	return filepath.Join(s.ConfigHome, "systemd", "user", UnitName+".d", DropInName)
}

// managerConfigHome asks the user manager where it reads a user's units from:
// its own $XDG_CONFIG_HOME, which need not be the one of this shell. Asking
// first also finds an unreachable manager before anything is written.
func (s *UserService) managerConfigHome(ctx context.Context) error {
	env, err := s.systemctl(ctx, "show-environment")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(env), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "XDG_CONFIG_HOME="); ok && filepath.IsAbs(v) {
			s.ConfigHome = v
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// writtenBySetup reports whether the unit at path is one setup wrote.
func writtenBySetup(path string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(body), "\n")
	return strings.HasPrefix(strings.TrimSpace(first), setupMarker)
}

func (s *UserService) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	out, err := s.Systemctl(ctx, append([]string{"--user"}, args...)...)
	if err == nil {
		return out, nil
	}
	err = fmt.Errorf("systemctl --user %s: %w", strings.Join(args, " "), err)
	if msg := strings.TrimSpace(string(out)); msg != "" {
		err = fmt.Errorf("%w\n%s", err, msg)
	}
	if strings.Contains(string(out), "Failed to connect to") {
		// su and sudo -u keep the caller's session, so the user manager of
		// the target account is not on the bus this shell can see.
		err = fmt.Errorf("%w\nthe systemd user manager of this account is not reachable from this shell: log in as the user directly (ssh, a desktop session, machinectl shell) rather than through su or sudo -u", err)
	}
	return out, err
}

func (s *UserService) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.Out, format, args...)
}

// Setup is `coddy serve setup`: it checks the configuration, puts a unit in
// place for this binary, enables the service and restarts it, so a second run
// after an update or a move picks up the new binary, and reports whether the
// service stayed up.
func (s *UserService) Setup(ctx context.Context) error {
	home := s.agentHome()
	if s.EnvHome != "" && filepath.Clean(s.EnvHome) != home {
		s.printf("note: CODDY_HOME is %s in this shell, but the service does not inherit it and reads %s\n", s.EnvHome, home)
	}
	if err := s.CheckConfig(s.Out, home); err != nil {
		return fmt.Errorf("check %s before enabling the service: %w", filepath.Join(home, "config.yaml"), err)
	}
	if s.DaemonRunning != nil {
		if pid, ok := s.DaemonRunning(home); ok {
			return fmt.Errorf("coddy serve --daemon is already running for %s (pid %d) and would hold the same port; stop it with `coddy serve stop`, then run setup again", home, pid)
		}
	}

	if err := s.managerConfigHome(ctx); err != nil {
		return err
	}
	unit, source, err := s.placeUnit()
	if err != nil {
		return err
	}
	if err := s.placeDropIn(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.workspace(), 0o755); err != nil {
		return fmt.Errorf("create the service workspace: %w", err)
	}

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", UnitName},
		// restart, not start: a service that is already running picks up
		// the binary and the unit as they are now.
		{"restart", UnitName},
	} {
		if _, err := s.systemctl(ctx, args...); err != nil {
			return err
		}
	}
	if s.Settle > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Settle):
		}
	}
	status, err := s.systemctl(ctx, "status", "--no-pager", "--lines=0", UnitName)
	if err != nil {
		return fmt.Errorf("%s is enabled but did not stay up: %w\nread its log with: journalctl --user -u %s -e", UnitName, err, UnitName)
	}
	_, _ = s.Out.Write(status)

	s.printf("\n%s is enabled and running\n", UnitName)
	s.printf("  unit       %s (%s)\n", unit, source)
	s.printf("  binary     %s\n", s.Exe)
	s.printf("  config     %s\n", filepath.Join(home, "config.yaml"))
	s.printf("  workspace  %s\n", s.workspace())
	s.printf("  PATH       from this shell, in %s\n", s.dropIn())
	s.printf("  log        journalctl --user -u %s -f\n", UnitName)
	s.printf("  remove     coddy serve uninstall\n")
	if s.User != "" && s.LingerDir != "" && !fileExists(filepath.Join(s.LingerDir, s.User)) {
		s.printf("\nThe service stops when your last session ends. To keep it running after logout\nand start it at boot, an administrator can run: sudo loginctl enable-linger %s\n", s.User)
	}
	return nil
}

// placeUnit makes sure the user manager finds a unit that runs this binary. A
// packaged binary uses the package's unit, and a unit an earlier setup wrote
// for a script install is removed so it no longer shadows that one. Any other
// binary gets a unit written for it.
func (s *UserService) placeUnit() (path, source string, err error) {
	userUnit := s.userUnit()
	userUnitExists := fileExists(userUnit)
	if userUnitExists && !writtenBySetup(userUnit) {
		return "", "", fmt.Errorf("%s was not written by coddy serve setup and would override it; move it aside, or keep it and enable it yourself with `systemctl --user enable --now %s`", userUnit, UnitName)
	}
	if s.packaged() {
		if userUnitExists {
			if err := os.Remove(userUnit); err != nil {
				return "", "", fmt.Errorf("remove the unit an earlier setup wrote: %w", err)
			}
			s.printf("removed %s: the packaged unit runs this binary\n", userUnit)
		}
		return s.PackagedUnit, "installed by the package", nil
	}
	if err := writeFileAtomic(userUnit, []byte(UnitFile(UnitHeaderWritten, s.Exe, s.Mkdir))); err != nil {
		return "", "", fmt.Errorf("write %s: %w", userUnit, err)
	}
	s.printf("wrote %s\n", userUnit)
	return userUnit, "written by coddy serve setup", nil
}

// placeDropIn writes the drop-in with this shell's PATH. One the user wrote
// under the same name is left alone.
func (s *UserService) placeDropIn() error {
	path := s.dropIn()
	if fileExists(path) && !writtenBySetup(path) {
		s.printf("kept %s: it was not written by coddy serve setup\n", path)
		return nil
	}
	if len(absolutePath(s.ShellPath)) == 0 && fileExists(path) {
		// A shell started with an empty environment would otherwise take
		// away the PATH an earlier setup handed over.
		s.printf("kept %s: this shell has no PATH to hand over\n", path)
		return nil
	}
	if err := writeFileAtomic(path, []byte(DropInFile(s.ShellPath))); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeFileAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, body) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Uninstall is `coddy serve uninstall`: it stops and disables the service and
// deletes the unit setup wrote. The packaged unit belongs to the package and a
// unit the user wrote belongs to the user, so both stay on disk, disabled. The
// configuration, the sessions and the workspace are never touched.
func (s *UserService) Uninstall(ctx context.Context) error {
	if err := s.managerConfigHome(ctx); err != nil {
		return err
	}
	userUnit := s.userUnit()
	userUnitExists := fileExists(userUnit)
	ours := userUnitExists && writtenBySetup(userUnit)
	dropIn := s.dropIn()
	ourDropIn := fileExists(dropIn) && writtenBySetup(dropIn)
	packagedUnit := fileExists(s.PackagedUnit)
	if !userUnitExists && !packagedUnit {
		if ourDropIn {
			if err := removeDropIn(dropIn); err != nil {
				return err
			}
			if _, err := s.systemctl(ctx, "daemon-reload"); err != nil {
				return err
			}
			s.printf("removed %s; no %s is installed for this user\n", dropIn, UnitName)
			return nil
		}
		s.printf("no %s is installed for this user; nothing to remove\n", UnitName)
		return nil
	}

	if _, err := s.systemctl(ctx, "disable", "--now", UnitName); err != nil {
		return err
	}
	if ours {
		if err := os.Remove(userUnit); err != nil {
			return fmt.Errorf("remove %s: %w", userUnit, err)
		}
	}
	if ourDropIn {
		if err := removeDropIn(dropIn); err != nil {
			return err
		}
	}
	if _, err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	// A unit that failed before it was stopped stays listed as failed until
	// this, which would read as a service that is still around.
	_, _ = s.Systemctl(ctx, "--user", "reset-failed", UnitName)

	s.printf("%s is stopped and disabled\n", UnitName)
	switch {
	case ours:
		s.printf("  removed    %s\n", userUnit)
	case userUnitExists:
		s.printf("  kept       %s (not written by coddy serve setup)\n", userUnit)
	}
	if ourDropIn {
		s.printf("  removed    %s\n", dropIn)
	}
	if packagedUnit {
		s.printf("  kept       %s (belongs to the package; `coddy serve setup` enables it again)\n", s.PackagedUnit)
	}
	s.printf("  untouched  %s (configuration and sessions), %s (workspace)\n", s.agentHome(), s.workspace())
	return nil
}

// removeDropIn deletes the drop-in setup wrote and its folder once nothing
// else is in it.
func removeDropIn(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	_ = os.Remove(filepath.Dir(path)) // fails, and is meant to, while other drop-ins remain
	return nil
}

// UserServiceActive reports whether the coddy systemd user service is running
// for this account. It is false wherever there is no systemd to ask.
func UserServiceActive(ctx context.Context) bool {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		return false
	}
	systemctl, ok := systemctlPath()
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, systemctl, "--user", "--quiet", "is-active", UnitName).Run() == nil
}
