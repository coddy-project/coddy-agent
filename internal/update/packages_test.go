package update

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestPackageAssetFileName(t *testing.T) {
	for _, tc := range []struct {
		format string
		goarch string
		want   string
	}{
		{"deb", "amd64", "coddy_1.2.3_linux_amd64.deb"},
		{"deb", "arm64", "coddy_1.2.3_linux_arm64.deb"},
		{"rpm", "amd64", "coddy_1.2.3_linux_amd64.rpm"},
		{"rpm", "arm64", "coddy_1.2.3_linux_arm64.rpm"},
	} {
		got, err := PackageAssetFileName("1.2.3", tc.format, tc.goarch)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.format, tc.goarch, err)
		}
		if got != tc.want {
			t.Fatalf("%s/%s = %q, want %q", tc.format, tc.goarch, got, tc.want)
		}
	}

	if _, err := PackageAssetFileName("1.2.3", "apk", "amd64"); err == nil {
		t.Fatal("unsupported format accepted")
	}
	if _, err := PackageAssetFileName("1.2.3", "deb", "riscv64"); err == nil {
		t.Fatal("unsupported architecture accepted")
	}
}

// lookPathFor answers as a host where only the named tools are installed.
func lookPathFor(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func TestPackageFrontEndPrefersTheResolvingTool(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  packageFormat
		present []string
		want    string
	}{
		{"apt over dpkg", formatDeb, []string{"dpkg", "apt-get"}, "apt-get"},
		{"apt when apt-get is absent", formatDeb, []string{"dpkg", "apt"}, "apt"},
		{"dpkg alone", formatDeb, []string{"dpkg"}, "dpkg"},
		{"deb fallback with nothing installed", formatDeb, nil, "dpkg"},
		{"dnf over rpm", formatRPM, []string{"rpm", "dnf"}, "dnf"},
		{"zypper when dnf is absent", formatRPM, []string{"rpm", "zypper", "yum"}, "zypper"},
		{"rpm fallback with nothing installed", formatRPM, nil, "rpm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := packageFrontEnd(packageEnv{LookPath: lookPathFor(tc.present...)}, tc.format)
			if got != tc.want {
				t.Fatalf("front end = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPackageInstallCommandIsNonInteractive(t *testing.T) {
	for _, tc := range []struct {
		manager string
		want    string
	}{
		{"apt-get", "apt-get install -y --allow-downgrades /tmp/coddy.deb"},
		{"apt", "apt install -y --allow-downgrades /tmp/coddy.deb"},
		{"dpkg", "dpkg --install /tmp/coddy.deb"},
		{"dnf", "dnf install -y /tmp/coddy.deb"},
		{"yum", "yum install -y /tmp/coddy.deb"},
		{"zypper", "zypper --non-interactive install --allow-unsigned-rpm /tmp/coddy.deb"},
		{"rpm", "rpm --upgrade --oldpackage /tmp/coddy.deb"},
	} {
		got := strings.Join(packageInstallCommand(tc.manager, "/tmp/coddy.deb"), " ")
		if got != tc.want {
			t.Fatalf("%s: command = %q, want %q", tc.manager, got, tc.want)
		}
	}
}

// A low-level tool cannot upgrade from a repository, so the hint has to send
// the user back through Coddy, which downloads the file the tool needs.
func TestPackageUpgradeHintFallsBackToCoddyForFileOnlyTools(t *testing.T) {
	for _, tc := range []struct {
		manager string
		want    string
	}{
		{"apt-get", "sudo apt-get install --only-upgrade coddy"},
		{"dnf", "sudo dnf upgrade coddy"},
		{"zypper", "sudo zypper update coddy"},
		{"dpkg", "sudo coddy update"},
		{"rpm", "sudo coddy update"},
	} {
		got := packageUpgradeHint(systemPackage{Name: "coddy", Manager: tc.manager})
		if got != tc.want {
			t.Fatalf("%s: hint = %q, want %q", tc.manager, got, tc.want)
		}
	}
}

func TestDetectSystemPackage(t *testing.T) {
	const owned = "/usr/bin/coddy"

	debEnv := func(out string, err error) packageEnv {
		return packageEnv{
			GOOS:     "linux",
			LookPath: lookPathFor("dpkg-query", "apt-get"),
			Query:    func(context.Context, string, ...string) (string, error) { return out, err },
		}
	}

	t.Run("dpkg ownership", func(t *testing.T) {
		pkg, ok := detectSystemPackage(context.Background(), debEnv("coddy: /usr/bin/coddy\n", nil), owned)
		if !ok {
			t.Fatal("owned file reported as unmanaged")
		}
		if pkg.Format != formatDeb || pkg.Name != "coddy" || pkg.Manager != "apt-get" {
			t.Fatalf("package = %+v", pkg)
		}
	})

	t.Run("several packages declare the file", func(t *testing.T) {
		pkg, ok := detectSystemPackage(context.Background(), debEnv("coddy, coddy-extras: /usr/bin/coddy\n", nil), owned)
		if !ok || pkg.Name != "coddy" {
			t.Fatalf("package = %+v, ok = %v; want the first listed package", pkg, ok)
		}
	})

	t.Run("dpkg reports no owner", func(t *testing.T) {
		if _, ok := detectSystemPackage(context.Background(), debEnv("", errors.New("exit 1")), owned); ok {
			t.Fatal("unowned file reported as package-managed")
		}
	})

	t.Run("rpm ownership", func(t *testing.T) {
		env := packageEnv{
			GOOS:     "linux",
			LookPath: lookPathFor("rpm", "dnf"),
			Query:    func(context.Context, string, ...string) (string, error) { return "coddy", nil },
		}
		pkg, ok := detectSystemPackage(context.Background(), env, owned)
		if !ok || pkg.Format != formatRPM || pkg.Manager != "dnf" {
			t.Fatalf("package = %+v, ok = %v", pkg, ok)
		}
	})

	// Some rpm builds answer a miss on stdout with a zero exit ("file ... is
	// not owned by any package"), which must not be read as a package name.
	t.Run("rpm reports a miss on stdout", func(t *testing.T) {
		env := packageEnv{
			GOOS:     "linux",
			LookPath: lookPathFor("rpm"),
			Query: func(context.Context, string, ...string) (string, error) {
				return "file /usr/bin/coddy is not owned by any package", nil
			},
		}
		if _, ok := detectSystemPackage(context.Background(), env, owned); ok {
			t.Fatal("rpm miss text read as a package name")
		}
	})

	t.Run("no package tools installed", func(t *testing.T) {
		env := packageEnv{GOOS: "linux", LookPath: lookPathFor()}
		if _, ok := detectSystemPackage(context.Background(), env, owned); ok {
			t.Fatal("host without dpkg or rpm reported a package")
		}
	})

	// Homebrew is read off the resolved path, so it needs no package tool at
	// all and works the same on macOS and on Linux.
	t.Run("homebrew cask and cellar", func(t *testing.T) {
		for _, path := range []string{
			"/opt/homebrew/Caskroom/coddy/1.0.11/coddy",
			"/usr/local/Cellar/coddy/1.0.11/bin/coddy",
			"/home/dev/.linuxbrew/Caskroom/coddy/1.0.11/coddy",
		} {
			env := packageEnv{GOOS: "darwin", LookPath: lookPathFor()}
			pkg, ok := detectSystemPackage(context.Background(), env, path)
			if !ok || pkg.Format != formatBrew || pkg.Name != "coddy" || pkg.Manager != "brew" {
				t.Fatalf("%s: package = %+v, ok = %v", path, pkg, ok)
			}
			if got := packageUpgradeHint(pkg); got != "brew upgrade --cask coddy" {
				t.Fatalf("%s: hint = %q", path, got)
			}
		}
	})

	t.Run("a path that only mentions a cellar", func(t *testing.T) {
		env := packageEnv{GOOS: "darwin", LookPath: lookPathFor()}
		if _, ok := detectSystemPackage(context.Background(), env, "/home/dev/Cellar-notes/coddy"); ok {
			t.Fatal("an unrelated path was read as a Homebrew install")
		}
	})

	t.Run("not linux", func(t *testing.T) {
		env := packageEnv{
			GOOS:     "darwin",
			LookPath: lookPathFor("dpkg-query"),
			Query:    func(context.Context, string, ...string) (string, error) { return "coddy: x", nil },
		}
		if _, ok := detectSystemPackage(context.Background(), env, owned); ok {
			t.Fatal("package database queried outside linux")
		}
	})

	t.Run("no install path", func(t *testing.T) {
		env := packageEnv{
			GOOS:     "linux",
			LookPath: lookPathFor("dpkg-query"),
			Query: func(context.Context, string, ...string) (string, error) {
				t.Fatal("package database queried without a path")
				return "", nil
			},
		}
		if _, ok := detectSystemPackage(context.Background(), env, "  "); ok {
			t.Fatal("empty path reported as package-managed")
		}
	})
}

// A release older than the packaging pipeline publishes archives only. Root has
// to learn that from the update rather than from a bare "no asset" line.
func TestInstallSystemPackageWithoutAPackageAsset(t *testing.T) {
	rel := &ghRelease{TagName: "0.9.70", Assets: []releaseAsset{
		{Name: "coddy_0.9.70_linux_amd64.tar.gz", BrowserDownloadURL: "http://example.invalid/a"},
	}}
	env := packageEnv{
		GOOS:    "linux",
		Geteuid: func() int { return 0 },
		Install: func(context.Context, io.Writer, string, ...string) error {
			t.Fatal("package manager invoked without a package to install")
			return nil
		},
	}
	pkg := systemPackage{Format: formatDeb, Name: "coddy", Manager: "apt-get"}
	opts := Options{GOARCH: "amd64", Yes: true}

	err := installSystemPackage(context.Background(), opts, env, pkg, rel, "0.9.70", io.Discard, nil)
	if err == nil {
		t.Fatal("missing package asset accepted")
	}
	if !strings.Contains(err.Error(), "coddy_0.9.70_linux_amd64.deb") || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("error does not explain the missing package: %v", err)
	}
}
