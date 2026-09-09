package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrPackageManaged reports that the executable about to be replaced belongs to
// a system package. Overwriting it would leave dpkg or rpm describing a build
// that is no longer on disk, and the next `apt upgrade` would silently undo the
// update, so the package flow refuses to touch the file and says what to run
// instead.
var ErrPackageManaged = errors.New("coddy is installed by a system package manager")

// packageFormat is the distribution package format an installation came from.
type packageFormat string

const (
	formatDeb  packageFormat = "deb"
	formatRPM  packageFormat = "rpm"
	formatBrew packageFormat = "brew"
)

// systemPackage describes an installed coddy that a package database owns.
type systemPackage struct {
	// Format is the package format that carried the file.
	Format packageFormat
	// Name is the package name as its manager knows it, usually "coddy".
	Name string
	// Manager is the front-end used to install and upgrade the package.
	Manager string
	// Cask marks a Homebrew cask, as opposed to a formula. Both are brew
	// installs, but only a cask is upgraded with the --cask flag.
	Cask bool
}

// packageEnv is the host as the package flow sees it. Tests replace every field
// so the whole flow runs without a package manager, without root, and without
// touching anything outside the test's temp directory.
type packageEnv struct {
	GOOS     string
	Geteuid  func() int
	LookPath func(name string) (string, error)
	// Query runs a package database lookup and returns its stdout. A non-zero
	// exit means the file is not owned, which is an answer, not a failure.
	Query func(ctx context.Context, name string, args ...string) (string, error)
	// Install hands a package file to the package manager, streaming its
	// output to the caller's writer.
	Install func(ctx context.Context, out io.Writer, name string, args ...string) error
}

// defaultPackageEnv talks to the real host.
func defaultPackageEnv() packageEnv {
	return packageEnv{
		GOOS:     runtime.GOOS,
		Geteuid:  os.Geteuid,
		LookPath: exec.LookPath,
		Query: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).Output()
			return string(out), err
		},
		Install: func(ctx context.Context, out io.Writer, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdout, cmd.Stderr = out, out
			return cmd.Run()
		},
	}
}

// PackageAssetFileName returns the release package CI publishes for a Linux
// distribution format, named like the archives beside it so one release listing
// reads the same way for every asset.
func PackageAssetFileName(version, format, goarch string) (string, error) {
	switch packageFormat(format) {
	case formatDeb, formatRPM:
	default:
		return "", fmt.Errorf("unsupported package format %q", format)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported package platform linux/%s", goarch)
	}
	return fmt.Sprintf("coddy_%s_linux_%s.%s", version, goarch, format), nil
}

// detectSystemPackage reports whether path is a file a package manager put on
// disk, and how to talk to that manager. Ownership is asked of the package
// database rather than guessed from the path, so a binary copied out of
// /usr/bin keeps updating itself and a distribution rebuild is recognised the
// same as our own package.
func detectSystemPackage(ctx context.Context, env packageEnv, path string) (systemPackage, bool) {
	if strings.TrimSpace(path) == "" {
		return systemPackage{}, false
	}
	if name, cask, ok := homebrewOwner(path); ok {
		return systemPackage{Format: formatBrew, Name: name, Manager: "brew", Cask: cask}, true
	}
	if env.GOOS != "linux" {
		return systemPackage{}, false
	}
	if name, ok := debPackageOwner(ctx, env, path); ok {
		return systemPackage{Format: formatDeb, Name: name, Manager: packageFrontEnd(env, formatDeb)}, true
	}
	if name, ok := rpmPackageOwner(ctx, env, path); ok {
		return systemPackage{Format: formatRPM, Name: name, Manager: packageFrontEnd(env, formatRPM)}, true
	}
	return systemPackage{}, false
}

// homebrewOwner recognises a binary Homebrew installed. The executable on PATH
// is a symlink into the Caskroom (casks) or the Cellar (formulae), and the
// caller resolves symlinks before asking, so the prefix is visible in the path
// itself - no `brew` process, and no answer that depends on brew being on PATH
// at all. The directory below Caskroom or Cellar is the package name, and which
// of the two markers matched is what tells a cask from a formula.
func homebrewOwner(path string) (name string, cask bool, ok bool) {
	// Homebrew is a Unix-only tool, but the path handed in comes from the host
	// this process runs on, so normalise the separator before matching rather
	// than assuming one.
	path = filepath.ToSlash(path)
	for _, marker := range []struct {
		segment string
		cask    bool
	}{
		{"/Caskroom/", true},
		{"/Cellar/", false},
	} {
		_, rest, found := strings.Cut(path, marker.segment)
		if !found {
			continue
		}
		name, _, _ = strings.Cut(rest, "/")
		if name = strings.TrimSpace(name); name != "" {
			return name, marker.cask, true
		}
	}
	return "", false, false
}

// debPackageOwner asks dpkg which package shipped path. dpkg-query prints
// "coddy: /usr/bin/coddy", and lists several packages when more than one
// declares the file.
func debPackageOwner(ctx context.Context, env packageEnv, path string) (string, bool) {
	if _, err := env.LookPath("dpkg-query"); err != nil {
		return "", false
	}
	out, err := env.Query(ctx, "dpkg-query", "-S", path)
	if err != nil {
		return "", false
	}
	name, _, ok := strings.Cut(strings.TrimSpace(out), ":")
	if !ok {
		return "", false
	}
	name, _, _ = strings.Cut(name, ",")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	return name, true
}

// rpmPackageOwner asks rpm which package shipped path, formatting the answer
// down to the bare package name.
func rpmPackageOwner(ctx context.Context, env packageEnv, path string) (string, bool) {
	if _, err := env.LookPath("rpm"); err != nil {
		return "", false
	}
	out, err := env.Query(ctx, "rpm", "-qf", "--queryformat", "%{NAME}", path)
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(out)
	// rpm answers a miss on stdout with a zero exit on some builds.
	if name == "" || strings.Contains(name, " ") {
		return "", false
	}
	return name, true
}

// packageFrontEnd picks the highest-level tool present for a format. The
// low-level tool is the fallback rather than the first choice: apt and dnf
// resolve dependencies, dpkg and rpm only report them.
func packageFrontEnd(env packageEnv, format packageFormat) string {
	candidates := []string{"dnf", "zypper", "yum", "rpm"}
	fallback := "rpm"
	if format == formatDeb {
		candidates = []string{"apt-get", "apt", "dpkg"}
		fallback = "dpkg"
	}
	for _, name := range candidates {
		if _, err := env.LookPath(name); err == nil {
			return name
		}
	}
	return fallback
}

// packageInstallCommand builds the non-interactive upgrade for one downloaded
// package file.
func packageInstallCommand(manager, file string) []string {
	switch manager {
	case "apt-get", "apt":
		return []string{manager, "install", "-y", "--allow-downgrades", file}
	case "dpkg":
		return []string{"dpkg", "--install", file}
	case "dnf", "yum":
		return []string{manager, "install", "-y", file}
	case "zypper":
		return []string{"zypper", "--non-interactive", "install", "--allow-unsigned-rpm", file}
	default:
		return []string{"rpm", "--upgrade", "--oldpackage", file}
	}
}

// packageUpgradeHint is the command a user without root privileges is told to
// run. Repository front-ends can upgrade from a configured repository; dpkg and
// rpm can only install a file, so those point back at `sudo coddy update`,
// which downloads that file.
//
// Homebrew needs the artefact kind, because --cask is not a hint brew ignores:
// `brew upgrade --cask coddy` on a formula install fails, there being no cask by
// that name to upgrade. The hint therefore follows the marker the path carried.
func packageUpgradeHint(pkg systemPackage) string {
	if pkg.Format == formatBrew {
		if pkg.Cask {
			return fmt.Sprintf("brew upgrade --cask %s", pkg.Name)
		}
		return fmt.Sprintf("brew upgrade %s", pkg.Name)
	}
	switch pkg.Manager {
	case "apt-get", "apt":
		return fmt.Sprintf("sudo %s install --only-upgrade %s", pkg.Manager, pkg.Name)
	case "dnf", "yum":
		return fmt.Sprintf("sudo %s upgrade %s", pkg.Manager, pkg.Name)
	case "zypper":
		return fmt.Sprintf("sudo zypper update %s", pkg.Name)
	default:
		return "sudo coddy update"
	}
}

// packageManagedError explains what owns the installation and how to move it
// forward. The guidance travels inside the error so it is printed once, on the
// stream the caller reports failures on.
func packageManagedError(pkg systemPackage, current, latest, dest string) error {
	// Homebrew refuses to run under sudo, so there is no privileged route to
	// offer there - the upgrade command is the whole answer.
	trailer := "\n\nOr let Coddy fetch and install the release package for you:\n\n    sudo coddy update"
	if pkg.Format == formatBrew {
		trailer = ""
	}
	return fmt.Errorf("%w\n\n"+
		"coddy %s at %s belongs to the %q package, and %s is available.\n"+
		"Replacing that file would leave the package database describing a build that is gone,\n"+
		"so nothing was changed.\n\n"+
		"Upgrade it with your package manager:\n\n"+
		"    %s%s",
		ErrPackageManaged, current, dest, pkg.Name, latest, packageUpgradeHint(pkg), trailer)
}

// installSystemPackage downloads the release package for this host and hands it
// to the package manager. The caller has already established that the running
// executable is package-managed and that this process may write to the package
// database.
func installSystemPackage(ctx context.Context, opts Options, env packageEnv, pkg systemPackage, rel *ghRelease, latest string, out io.Writer, client *http.Client) error {
	assetName, err := PackageAssetFileName(latest, string(pkg.Format), opts.GOARCH)
	if err != nil {
		return err
	}
	asset, err := pickAsset(rel, assetName)
	if err != nil {
		return fmt.Errorf("%w; upgrade the %q package with your package manager instead", err, pkg.Name)
	}

	if !opts.Yes {
		_, _ = fmt.Fprintf(out, "Update the %q package %s -> %s with %s? [y/N] ", pkg.Name, opts.CurrentVersion, latest, pkg.Manager)
		ok, err := readYesNo(os.Stdin)
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(out, "cancelled")
			return nil
		}
	}

	_, _ = fmt.Fprintf(out, "Downloading %s ...\n", asset.Name)
	data, err := downloadURL(ctx, client, asset.BrowserDownloadURL, newDownloadProgress(out, asset.Name))
	if err != nil {
		return err
	}
	if err := verifyAssetChecksum(ctx, client, rel, asset.Name, data, out); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "coddy-package-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, asset.Name)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return err
	}

	argv := packageInstallCommand(pkg.Manager, file)
	_, _ = fmt.Fprintf(out, "Installing with %s ...\n", pkg.Manager)
	if err := env.Install(ctx, out, argv[0], argv[1:]...); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	_, _ = fmt.Fprintf(out, "Installed %s (%s package %q via %s)\n", latest, pkg.Format, pkg.Name, pkg.Manager)
	return nil
}
