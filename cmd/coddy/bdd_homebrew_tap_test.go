package main

// Godog harness for features/homebrew_tap.feature: the cask a release renders
// for its macOS archives, and the commit that publishes it to the tap, through
// the scripts the release job runs (homebrew_tap_test.go). Installing the cask
// with a real Homebrew is the "Homebrew tap" workflow on macOS.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type homebrewTapState struct {
	env      []string
	dir      string
	archives map[string]string // cask arch -> checksum
	cask     string            // path of the rendered cask
	facts    caskFacts
	tap      string
}

func (s *homebrewTapState) theMacOSArchivesOfRelease(t *testing.T) func(string) error {
	return func(version string) error {
		s.archives = writeDarwinArchives(t, s.dir, version)
		return nil
	}
}

func (s *homebrewTapState) theCaskIsRenderedFor(t *testing.T) func(string) error {
	return func(version string) error {
		path, err := renderCask(t, s.env, s.dir, version)
		if err != nil {
			return err
		}
		s.cask = path
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s.facts, err = parseCask(string(b))
		return err
	}
}

func (s *homebrewTapState) theCaskIsAt(token, version string) error {
	if s.facts.token != token {
		return fmt.Errorf("the cask is named %q, want %q", s.facts.token, token)
	}
	if s.facts.version != version {
		return fmt.Errorf("the cask is at version %q, want %q", s.facts.version, version)
	}
	return nil
}

func (s *homebrewTapState) onArchItDownloads(caskArch, archive string) error {
	want := "https://github.com/coddy-project/coddy-agent/releases/download/" + s.facts.version + "/" + archive
	if got := s.facts.downloadFor(caskArch); got != want {
		return fmt.Errorf("on %s the cask downloads %s, want %s", caskArch, got, want)
	}
	if got, want := s.facts.sha256[caskArch], s.archives[caskArch]; got != want {
		return fmt.Errorf("on %s the cask pins sha256 %s, the archive hashes to %s", caskArch, got, want)
	}
	return nil
}

func (s *homebrewTapState) itInstallsTheBinaryManPageAndCompletions() error {
	want := map[string]caskArtifact{
		"binary":          {stanza: "binary", source: "coddy"},
		"manpage":         {stanza: "manpage", source: "coddy.1"},
		"bash_completion": {stanza: "bash_completion", source: "coddy.bash", target: "coddy"},
		"zsh_completion":  {stanza: "zsh_completion", source: "coddy.zsh", target: "_coddy"},
	}
	got := map[string]caskArtifact{}
	for _, a := range s.facts.artifacts() {
		got[a.stanza] = a
	}
	for stanza, w := range want {
		if got[stanza] != w {
			return fmt.Errorf("the cask's %s stanza is %+v, want %+v", stanza, got[stanza], w)
		}
	}
	return nil
}

// quarantineStepRe is the post-install step that takes the quarantine attribute
// Homebrew sets on a cask download off the binary it linked.
var quarantineStepRe = regexp.MustCompile(`(?s)postflight_steps do\s+run "/usr/bin/xattr", args: \["-d", "com\.apple\.quarantine", "\{\{staged_path\}\}/coddy"\]`)

func (s *homebrewTapState) itLiftsTheQuarantine() error {
	if !quarantineStepRe.MatchString(s.facts.text) {
		return fmt.Errorf("the cask has no postflight step removing com.apple.quarantine from {{staged_path}}/coddy:\n%s", s.facts.text)
	}
	return nil
}

func (s *homebrewTapState) itUsesNoDeprecatedStanza() error {
	var found []string
	for _, d := range deprecatedCaskStanzas {
		if loc := d.re.FindString(s.facts.text); loc != "" {
			found = append(found, fmt.Sprintf("%q: %s", strings.TrimSpace(loc), d.note))
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("the cask uses what current Homebrew deprecates:\n%s", strings.Join(found, "\n"))
	}
	return nil
}

func (s *homebrewTapState) aTapWhoseCaskIsAt(t *testing.T) func(string) error {
	return func(version string) error {
		s.tap = newTap(t, s.env, version)
		return nil
	}
}

func (s *homebrewTapState) releasePublishesItsCask(t *testing.T) func(string) error {
	return func(version string) error {
		out, err := publishCask(t, s.env, s.tap, version, s.cask)
		if err != nil {
			return fmt.Errorf("publish-homebrew-tap.sh: %v\n%s", err, out)
		}
		return nil
	}
}

func (s *homebrewTapState) theCaskInTheTapIsTheRenderedOne(t *testing.T) func(string) error {
	return func(version string) error {
		got := tapGit(t, s.env, s.tap, "show", "main:Casks/coddy.rb")
		want, err := os.ReadFile(s.cask)
		if err != nil {
			return err
		}
		if got != string(want) {
			return fmt.Errorf("the tap's Casks/coddy.rb is not the cask rendered for %s:\n%s", version, got)
		}
		return nil
	}
}

func (s *homebrewTapState) theNewestCommitNames(t *testing.T) func(string) error {
	return func(version string) error {
		subject := strings.TrimSpace(tapGit(t, s.env, s.tap, "log", "-1", "--format=%s", "main"))
		if !strings.Contains(subject, version) {
			return fmt.Errorf("the tap's newest commit is %q, which does not name %s", subject, version)
		}
		return nil
	}
}

func TestHomebrewTapFeature(t *testing.T) {
	skipWithoutBash(t)
	suite := godog.TestSuite{
		Name: "homebrew-tap",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &homebrewTapState{}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = homebrewTapState{env: isolatedGitEnv(t), dir: filepath.Join(t.TempDir(), "dist")}
				return ctx, os.MkdirAll(s.dir, 0o755)
			})
			sc.Step(`^the macOS archives of release "([^"]*)"$`, s.theMacOSArchivesOfRelease(t))
			sc.Step(`^the Homebrew cask is rendered for release "([^"]*)"$`, s.theCaskIsRenderedFor(t))
			sc.Step(`^the cask "([^"]*)" is at version "([^"]*)"$`, s.theCaskIsAt)
			sc.Step(`^on "([^"]*)" it downloads "([^"]*)" with that archive's checksum$`, s.onArchItDownloads)
			sc.Step(`^it installs the binary, the man page and the bash and zsh completions$`, s.itInstallsTheBinaryManPageAndCompletions)
			sc.Step(`^it lifts the quarantine from the installed binary$`, s.itLiftsTheQuarantine)
			sc.Step(`^it uses no stanza current Homebrew deprecates$`, s.itUsesNoDeprecatedStanza)
			sc.Step(`^a Homebrew tap whose cask is at release "([^"]*)"$`, s.aTapWhoseCaskIsAt(t))
			sc.Step(`^release "([^"]*)" publishes its cask to the tap$`, s.releasePublishesItsCask(t))
			sc.Step(`^the cask in the tap is the one rendered for release "([^"]*)"$`, s.theCaskInTheTapIsTheRenderedOne(t))
			sc.Step(`^the newest commit of the tap names release "([^"]*)"$`, s.theNewestCommitNames(t))
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/homebrew_tap.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("homebrew tap feature failed")
	}
}
