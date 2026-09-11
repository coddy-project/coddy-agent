package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

const (
	featureReleaseTag = "0.9.70"
	featurePayload    = "the release build of Coddy"
)

// featureExtras are the files the release archive carries beside the binary,
// keyed by their name inside the archive, with the body the release ships.
var featureExtras = map[string]string{
	"coddy.1":    "the release man page",
	"coddy.bash": "the release bash completion",
	"coddy.zsh":  "the release zsh completion",
}

// featureInstalledVersion is the release that is running when the scenarios
// start; every release after it is news the report is expected to carry.
const featureInstalledVersion = "0.9.67"

// featureReleaseHistory is the release list GitHub answers with, newest first,
// each with the one change its generated notes describe. The report is
// expected to quote the change and its pull request number and to drop the
// boilerplate around them; the releases up to the running one are not news.
var featureReleaseHistory = []struct {
	tag    string
	change string
	pr     int
}{
	{"0.9.70", "feat(update): print what changed after an update", 195},
	{"0.9.69", "fix(update): refresh the man page and the shell completions beside the binary", 193},
	{"0.9.68", "feat(config): --dry-run probes what config.yaml points at", 194},
	{"0.9.67", "fix(cli): the release that is running", 191},
	{"0.9.66", "feat(serve): a release older than the running one", 174},
}

const featureReleaseDate = "2026-09-11"

// featureReleaseList renders the history the way GET /repos/{repo}/releases
// does: the generated "What's Changed" body, the author and pull request
// trailer on every line, and the Full Changelog footer.
func featureReleaseList() string {
	type release struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
	}
	list := make([]release, 0, len(featureReleaseHistory))
	for _, n := range featureReleaseHistory {
		body := fmt.Sprintf("## What's Changed\n* %s by @EvilFreelancer in https://github.com/%s/pull/%d\n\n\n**Full Changelog**: https://github.com/%s/compare/prev...%s",
			n.change, DefaultRepo, n.pr, DefaultRepo, n.tag)
		list = append(list, release{
			TagName:     n.tag,
			Name:        n.tag,
			HTMLURL:     fmt.Sprintf("https://github.com/%s/releases/tag/%s", DefaultRepo, n.tag),
			PublishedAt: featureReleaseDate + "T16:53:15Z",
			Body:        body,
		})
	}
	b, err := json.Marshal(list)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// checkReleasesListed checks a report against the history: every release
// after the running one is named with its date, its change and its pull
// request number, the releases up to the running one are not, and none of
// the boilerplate GitHub wraps around the notes made it through.
func checkReleasesListed(out string) error {
	if !strings.Contains(out, "Changes since "+featureInstalledVersion+":") {
		return fmt.Errorf("output does not open the report: %q", out)
	}
	for _, n := range featureReleaseHistory {
		news := CompareSemver(n.tag, featureInstalledVersion) > 0
		heading := n.tag + " (" + featureReleaseDate + ")"
		if strings.Contains(out, heading) != news {
			return fmt.Errorf("release %s listed = %v, want %v: %q", n.tag, !news, news, out)
		}
		line := fmt.Sprintf("- %s (#%d)", n.change, n.pr)
		if strings.Contains(out, line) != news {
			return fmt.Errorf("change of %s listed = %v, want %v: %q", n.tag, !news, news, out)
		}
	}
	for _, noise := range []string{"What's Changed", "Full Changelog", "by @EvilFreelancer", "/pull/"} {
		if strings.Contains(out, noise) {
			return fmt.Errorf("output carries the GitHub boilerplate %q: %q", noise, out)
		}
	}
	return nil
}

// checkChangelogLinked checks that the report ends on the GitHub comparison
// of the whole range, the one link that covers every release at once.
func checkChangelogLinked(out string) error {
	want := fmt.Sprintf("Full changelog: https://github.com/%s/compare/%s...%s", DefaultRepo, featureInstalledVersion, featureReleaseTag)
	if !strings.Contains(out, want) {
		return fmt.Errorf("output does not link %q: %q", want, out)
	}
	return nil
}

// checkNoNotes checks that neither the report nor its link was printed.
func checkNoNotes(out string) error {
	for _, marker := range []string{"Changes since", "Release notes", "/compare/"} {
		if strings.Contains(out, marker) {
			return fmt.Errorf("output carries release notes %q: %q", marker, out)
		}
	}
	return nil
}

type updateFeatureState struct {
	archive   []byte
	assetName string
	goarch    string
	goos      string
	dest      string
	dir       string
	share     string
	dropFirst bool
	notes     bool
	noNotes   bool
	served    int
	out       bytes.Buffer
	scheduled *windowsUpdateRequest
	server    *httptest.Server
}

func (s *updateFeatureState) reset() {
	if s.server != nil {
		s.server.Close()
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	*s = updateFeatureState{}
}

// releaseIsAvailable stands up a GitHub-shaped release: the platform archive
// plus the SHA256SUMS list CI publishes beside it.
func (s *updateFeatureState) releaseIsAvailable(goos, goarch string) error {
	assetName, err := AssetFileName(featureReleaseTag, goos, goarch)
	if err != nil {
		return err
	}
	s.goos, s.goarch, s.assetName = goos, goarch, assetName
	if goos == "windows" {
		s.archive, err = zipArchive("coddy.exe", []byte(featurePayload))
	} else {
		// The Linux and macOS archives carry the man page and the completions
		// beside the binary, the way CI publishes them.
		members := map[string][]byte{BinaryName(goos): []byte(featurePayload)}
		for name, body := range featureExtras {
			members[name] = []byte(body)
		}
		s.archive, err = tarGzArchiveOf(members)
	}
	if err != nil {
		return err
	}

	s.dir, err = os.MkdirTemp("", "coddy-update-feature-*")
	if err != nil {
		return err
	}
	s.dest = filepath.Join(s.dir, BinaryName(goos))
	if err := os.WriteFile(s.dest, []byte("the installed build of Coddy"), 0o755); err != nil {
		return err
	}

	sum := sha256.Sum256(s.archive)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + DefaultRepo + "/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"http://%s/asset"},{"name":%q,"browser_download_url":"http://%s/sums"}]}`,
				featureReleaseTag, assetName, r.Host, checksumAssetName, r.Host)
		case "/repos/" + DefaultRepo + "/releases":
			if !s.notes {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(featureReleaseList()))
		case "/asset":
			s.serveAsset(w, r)
		case "/sums":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	return nil
}

// serveAsset answers the archive request, honouring a Range header the way the
// release CDN does, and optionally cutting the first response short so the
// resume path has something real to recover from.
func (s *updateFeatureState) serveAsset(w http.ResponseWriter, r *http.Request) {
	s.served++
	if s.dropFirst && s.served == 1 {
		w.Header().Set("Content-Length", strconv.Itoa(len(s.archive)))
		_, _ = w.Write(s.archive[:len(s.archive)/2])
		return
	}
	offset := 0
	if spec := strings.TrimPrefix(r.Header.Get("Range"), "bytes="); spec != r.Header.Get("Range") {
		start, _, _ := strings.Cut(spec, "-")
		parsed, err := strconv.Atoi(start)
		if err != nil || parsed < 0 || parsed > len(s.archive) {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		offset = parsed
	}
	if offset > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(s.archive)-1, len(s.archive)))
		w.Header().Set("Content-Length", strconv.Itoa(len(s.archive)-offset))
		w.WriteHeader(http.StatusPartialContent)
	}
	_, _ = w.Write(s.archive[offset:])
}

func (s *updateFeatureState) newerReleaseIsAvailable() error {
	// Deliberately not the host platform: this scenario is about the ordinary
	// replace-in-place install, which Windows routes through the helper.
	return s.releaseIsAvailable("linux", "amd64")
}

func (s *updateFeatureState) newerWindowsReleaseIsAvailable() error {
	return s.releaseIsAvailable("windows", "amd64")
}

func (s *updateFeatureState) serverDropsTheFirstConnection() error {
	s.dropFirst = true
	return nil
}

func (s *updateFeatureState) releasesCarryTheirNotes() error {
	s.notes = true
	return nil
}

// extrasSitBesideTheExecutable lays the installation out the way the install
// script does: the binary in a bin directory, the man page and the completions
// in the share directory of the same prefix, all from the release before this
// one.
func (s *updateFeatureState) extrasSitBesideTheExecutable() error {
	bin := filepath.Join(s.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	installed, err := os.ReadFile(s.dest)
	if err != nil {
		return err
	}
	if err := os.Remove(s.dest); err != nil {
		return err
	}
	s.dest = filepath.Join(bin, BinaryName(s.goos))
	if err := os.WriteFile(s.dest, installed, 0o755); err != nil {
		return err
	}
	s.share = filepath.Join(s.dir, "share")
	for _, e := range extraFiles {
		path := filepath.Join(s.share, filepath.FromSlash(e.Rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("the installed "+e.Label), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *updateFeatureState) extrasAreFromTheRelease() error {
	for _, e := range extraFiles {
		got, err := os.ReadFile(filepath.Join(s.share, filepath.FromSlash(e.Rel)))
		if err != nil {
			return err
		}
		if string(got) != featureExtras[e.Member] {
			return fmt.Errorf("%s = %q, want %q", e.Label, got, featureExtras[e.Member])
		}
	}
	return nil
}

func (s *updateFeatureState) reportsRefreshedExtras() error {
	return s.reports("Refreshed the man page, the bash completion and the zsh completion under " + s.share)
}

func (s *updateFeatureState) options() Options {
	return Options{
		APIBase:        s.server.URL,
		Repo:           DefaultRepo,
		CurrentVersion: featureInstalledVersion,
		GOOS:           s.goos,
		GOARCH:         s.goarch,
		InstallPath:    s.dest,
		Yes:            true,
		NoNotes:        s.noNotes,
		Stdout:         &s.out,
	}
}

func (s *updateFeatureState) coddyInstallsTheUpdate() error {
	return Run(context.Background(), s.options())
}

func (s *updateFeatureState) coddyInstallsTheUpdateWithNoNotes() error {
	s.noNotes = true
	return s.coddyInstallsTheUpdate()
}

func (s *updateFeatureState) listsTheReleasesSinceTheInstalledVersion() error {
	return checkReleasesListed(s.out.String())
}

func (s *updateFeatureState) linksTheFullChangelog() error {
	return checkChangelogLinked(s.out.String())
}

func (s *updateFeatureState) printsNoReleaseNotes() error {
	return checkNoNotes(s.out.String())
}

func (s *updateFeatureState) coddyPreparesTheWindowsUpdate() error {
	return s.prepareWindowsUpdate(false)
}

func (s *updateFeatureState) coddyPreparesTheWindowsUpdateWithoutRestart() error {
	return s.prepareWindowsUpdate(true)
}

func (s *updateFeatureState) prepareWindowsUpdate(noRestart bool) error {
	opts := s.options()
	opts.NoRestart = noRestart
	opts.windowsInstaller = func(req windowsUpdateRequest) error {
		s.scheduled = &req
		return nil
	}
	return Run(context.Background(), opts)
}

func (s *updateFeatureState) installedExecutableIsFromTheRelease() error {
	got, err := os.ReadFile(s.dest)
	if err != nil {
		return err
	}
	if string(got) != featurePayload {
		return fmt.Errorf("installed executable = %q, want %q", got, featurePayload)
	}
	return nil
}

func (s *updateFeatureState) reports(want string) error {
	if !strings.Contains(s.out.String(), want) {
		return fmt.Errorf("output does not contain %q: %q", want, s.out.String())
	}
	return nil
}

func (s *updateFeatureState) reportsTheInstalledRelease() error {
	return s.reports("Installed " + featureReleaseTag)
}

func (s *updateFeatureState) reportsAVerifiedArchive() error {
	return s.reports(fmt.Sprintf("Verified %s against %s", s.assetName, checksumAssetName))
}

func (s *updateFeatureState) reportsAResumedDownload() error {
	return s.reports("resuming, attempt 2 of 3")
}

func (s *updateFeatureState) updateIsReady() error {
	return s.reports("Update downloaded")
}

func (s *updateFeatureState) helperWillLeaveCoddyStopped() error {
	if err := s.helperWasScheduled(); err != nil {
		return err
	}
	if s.scheduled.Restart {
		return fmt.Errorf("helper restart = true, want the update installed without one")
	}
	return nil
}

func (s *updateFeatureState) helperWillRestartCoddy() error {
	if err := s.helperWasScheduled(); err != nil {
		return err
	}
	if !s.scheduled.Restart {
		return fmt.Errorf("helper restart = false")
	}
	return nil
}

func (s *updateFeatureState) helperWasScheduled() error {
	if s.scheduled == nil {
		return fmt.Errorf("no helper was scheduled")
	}
	defer func() { _ = os.Remove(s.scheduled.StagedPath) }()
	if s.scheduled.TargetPath != s.dest {
		return fmt.Errorf("helper target = %q, want %q", s.scheduled.TargetPath, s.dest)
	}
	got, err := os.ReadFile(s.scheduled.StagedPath)
	if err != nil {
		return err
	}
	if string(got) != featurePayload {
		return fmt.Errorf("staged binary = %q, want %q", got, featurePayload)
	}
	return nil
}

func TestUpdateFeature(t *testing.T) {
	s := &updateFeatureState{}
	t.Cleanup(s.reset)

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^a newer Coddy release is available$`, s.newerReleaseIsAvailable)
			sc.Step(`^a newer Windows Coddy release is available$`, s.newerWindowsReleaseIsAvailable)
			sc.Step(`^the download server drops the first connection halfway$`, s.serverDropsTheFirstConnection)
			sc.Step(`^the releases since the installed version carry their notes$`, s.releasesCarryTheirNotes)
			sc.Step(`^the man page and the shell completions of the installed release sit beside the executable$`, s.extrasSitBesideTheExecutable)
			sc.Step(`^Coddy installs the update$`, s.coddyInstallsTheUpdate)
			sc.Step(`^Coddy installs the update with --no-notes$`, s.coddyInstallsTheUpdateWithNoNotes)
			sc.Step(`^Coddy lists every release since the installed version with its notes$`, s.listsTheReleasesSinceTheInstalledVersion)
			sc.Step(`^Coddy links the full changelog between the two versions on GitHub$`, s.linksTheFullChangelog)
			sc.Step(`^Coddy prints no release notes$`, s.printsNoReleaseNotes)
			sc.Step(`^Coddy prepares the Windows update$`, s.coddyPreparesTheWindowsUpdate)
			sc.Step(`^Coddy prepares the Windows update with --no-restart$`, s.coddyPreparesTheWindowsUpdateWithoutRestart)
			sc.Step(`^the installed executable is the one from the release$`, s.installedExecutableIsFromTheRelease)
			sc.Step(`^Coddy reports the release it installed$`, s.reportsTheInstalledRelease)
			sc.Step(`^Coddy reports that it verified the archive against the published checksums$`, s.reportsAVerifiedArchive)
			sc.Step(`^Coddy reports that it resumed the download$`, s.reportsAResumedDownload)
			sc.Step(`^the man page and the shell completions are the ones from the release$`, s.extrasAreFromTheRelease)
			sc.Step(`^Coddy reports that it refreshed the man page and the shell completions$`, s.reportsRefreshedExtras)
			sc.Step(`^it reports that the update is ready$`, s.updateIsReady)
			sc.Step(`^it schedules a helper that will restart Coddy$`, s.helperWillRestartCoddy)
			sc.Step(`^it schedules a helper that will leave Coddy stopped$`, s.helperWillLeaveCoddyStopped)
		},
		Options: &godog.Options{
			Format: "progress",
			Paths:  []string{"../../features/update.feature"},
		},
	}
	if suite.Run() != 0 {
		t.Fatal("update feature failed")
	}
}

func zipArchive(name string, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tarGzArchive(name string, body []byte) ([]byte, error) {
	return tarGzArchiveOf(map[string][]byte{name: body})
}

// tarGzArchiveOf builds a release-shaped archive holding every member given,
// in a stable order so two calls with the same input hash the same.
func tarGzArchiveOf(members map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		body := members[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
