package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

const installedPayload = "the installed build of Coddy"

// packageFeatureState stands up a release that publishes distribution packages
// beside the archives, and a host whose package database owns the executable.
type packageFeatureState struct {
	assetName string
	body      []byte
	dest      string
	dir       string
	euid      int
	format    packageFormat
	installed []string
	manager   string
	out       bytes.Buffer
	runErr    error
	server    *httptest.Server
	served    int
}

func (s *packageFeatureState) reset() {
	if s.server != nil {
		s.server.Close()
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	*s = packageFeatureState{}
}

func (s *packageFeatureState) installedFromDeb() error {
	return s.installedFromPackage(formatDeb, "apt-get")
}

func (s *packageFeatureState) installedFromRPM() error {
	return s.installedFromPackage(formatRPM, "dnf")
}

func (s *packageFeatureState) installedByHomebrew() error {
	return s.installedFromPackage(formatBrew, "brew")
}

func (s *packageFeatureState) installedFromPackage(format packageFormat, manager string) error {
	s.format, s.manager = format, manager
	assetFormat := format
	if assetFormat == formatBrew {
		// brew never downloads a package here; the release still has to look
		// like a real one.
		assetFormat = formatDeb
	}
	assetName, err := PackageAssetFileName(featureReleaseTag, string(assetFormat), "amd64")
	if err != nil {
		return err
	}
	s.assetName = assetName
	s.body = []byte("the release " + string(format) + " of Coddy")

	s.dir, err = os.MkdirTemp("", "coddy-package-feature-*")
	if err != nil {
		return err
	}
	// The path a package manager would own. For dpkg and rpm what matters is
	// that the database claims it, not where it sits; Homebrew is recognised
	// from the path itself, so that one has to look like a Caskroom.
	s.dest = filepath.Join(s.dir, "coddy")
	if format == formatBrew {
		s.dest = filepath.Join(s.dir, "Caskroom", "coddy", featureReleaseTag, "coddy")
		if err := os.MkdirAll(filepath.Dir(s.dest), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(s.dest, []byte(installedPayload), 0o755); err != nil {
		return err
	}

	sum := sha256.Sum256(s.body)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + DefaultRepo + "/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"http://%s/asset"},{"name":%q,"browser_download_url":"http://%s/sums"}]}`,
				featureReleaseTag, assetName, r.Host, checksumAssetName, r.Host)
		case "/asset":
			s.served++
			_, _ = w.Write(s.body)
		case "/sums":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	return nil
}

func (s *packageFeatureState) runsAsRoot() error {
	s.euid = 0
	return nil
}

func (s *packageFeatureState) runsWithoutRoot() error {
	s.euid = 1000
	return nil
}

// env answers as a host where the package database owns s.dest and the chosen
// front-end is the only package tool installed.
func (s *packageFeatureState) env() packageEnv {
	owner := "dpkg-query"
	if s.format == formatRPM {
		owner = "rpm"
	}
	return packageEnv{
		GOOS:    "linux",
		Geteuid: func() int { return s.euid },
		LookPath: func(name string) (string, error) {
			if name == owner || name == s.manager {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Query: func(_ context.Context, name string, args ...string) (string, error) {
			path := args[len(args)-1]
			if path != s.dest {
				return "", errors.New("not owned")
			}
			if name == "dpkg-query" {
				return "coddy: " + path + "\n", nil
			}
			return "coddy", nil
		},
		Install: func(_ context.Context, out io.Writer, name string, args ...string) error {
			s.installed = append([]string{name}, args...)
			_, _ = fmt.Fprintf(out, "%s: installed\n", name)
			return nil
		},
	}
}

func (s *packageFeatureState) run() error {
	env := s.env()
	s.runErr = Run(context.Background(), Options{
		APIBase:        s.server.URL,
		Repo:           DefaultRepo,
		CurrentVersion: "0.9.67",
		GOOS:           "linux",
		GOARCH:         "amd64",
		InstallPath:    s.dest,
		Yes:            true,
		Stdout:         &s.out,
		packageEnv:     &env,
	})
	return nil
}

func (s *packageFeatureState) installsTheUpdate() error {
	_ = s.run()
	return s.runErr
}

func (s *packageFeatureState) triesToInstallTheUpdate() error {
	return s.run()
}

func (s *packageFeatureState) reportsPackageManagerOwnership() error {
	if !errors.Is(s.runErr, ErrPackageManaged) {
		return fmt.Errorf("error = %v, want one wrapping ErrPackageManaged", s.runErr)
	}
	if !strings.Contains(s.runErr.Error(), s.dest) {
		return fmt.Errorf("error does not name the installed file: %v", s.runErr)
	}
	return nil
}

func (s *packageFeatureState) namesTheUpgradeCommand() error {
	want := packageUpgradeHint(systemPackage{Format: s.format, Name: "coddy", Manager: s.manager})
	if !strings.Contains(s.runErr.Error(), want) {
		return fmt.Errorf("error does not name %q: %v", want, s.runErr)
	}
	return nil
}

func (s *packageFeatureState) executableIsUntouched() error {
	got, err := os.ReadFile(s.dest)
	if err != nil {
		return err
	}
	if string(got) != installedPayload {
		return fmt.Errorf("installed executable = %q, want it left at %q", got, installedPayload)
	}
	return nil
}

func (s *packageFeatureState) downloadsTheReleasePackage() error {
	if s.served != 1 {
		return fmt.Errorf("package asset served %d times, want 1", s.served)
	}
	return nil
}

func (s *packageFeatureState) handsThePackageToTheManager() error {
	if len(s.installed) == 0 {
		return fmt.Errorf("no package manager was invoked")
	}
	if s.installed[0] != s.manager {
		return fmt.Errorf("invoked %q, want %q", s.installed[0], s.manager)
	}
	file := s.installed[len(s.installed)-1]
	if filepath.Base(file) != s.assetName {
		return fmt.Errorf("installed %q, want the downloaded %q", file, s.assetName)
	}
	return nil
}

func (s *packageFeatureState) reportsTheInstalledRelease() error {
	if !strings.Contains(s.out.String(), "Installed "+featureReleaseTag) {
		return fmt.Errorf("output does not report the release: %q", s.out.String())
	}
	return nil
}

func TestUpdatePackagesFeature(t *testing.T) {
	s := &packageFeatureState{}
	t.Cleanup(s.reset)

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^Coddy was installed from a deb package$`, s.installedFromDeb)
			sc.Step(`^Coddy was installed from an rpm package$`, s.installedFromRPM)
			sc.Step(`^Coddy was installed by Homebrew$`, s.installedByHomebrew)
			sc.Step(`^Coddy runs as root$`, s.runsAsRoot)
			sc.Step(`^Coddy runs without root privileges$`, s.runsWithoutRoot)
			sc.Step(`^Coddy installs the update$`, s.installsTheUpdate)
			sc.Step(`^Coddy tries to install the update$`, s.triesToInstallTheUpdate)
			sc.Step(`^Coddy reports that a package manager owns the installation$`, s.reportsPackageManagerOwnership)
			sc.Step(`^Coddy names the command that upgrades the package$`, s.namesTheUpgradeCommand)
			sc.Step(`^the installed executable is left untouched$`, s.executableIsUntouched)
			sc.Step(`^Coddy downloads the release package for this platform$`, s.downloadsTheReleasePackage)
			sc.Step(`^Coddy hands the package to the system package manager$`, s.handsThePackageToTheManager)
			sc.Step(`^Coddy reports the release it installed$`, s.reportsTheInstalledRelease)
		},
		Options: &godog.Options{
			Format: "progress",
			Paths:  []string{"../../features/update_packages.feature"},
		},
	}
	if suite.Run() != 0 {
		t.Fatal("update packages feature failed")
	}
}
