package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

const (
	featureReleaseTag = "0.9.70"
	featurePayload    = "the release build of Coddy"
)

type updateFeatureState struct {
	archive   []byte
	assetName string
	goarch    string
	goos      string
	dest      string
	dir       string
	dropFirst bool
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
		s.archive, err = tarGzArchive(BinaryName(goos), []byte(featurePayload))
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

func (s *updateFeatureState) options() Options {
	return Options{
		APIBase:        s.server.URL,
		Repo:           DefaultRepo,
		CurrentVersion: "0.9.67",
		GOOS:           s.goos,
		GOARCH:         s.goarch,
		InstallPath:    s.dest,
		Yes:            true,
		Stdout:         &s.out,
	}
}

func (s *updateFeatureState) coddyInstallsTheUpdate() error {
	return Run(context.Background(), s.options())
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
			sc.Step(`^Coddy installs the update$`, s.coddyInstallsTheUpdate)
			sc.Step(`^Coddy prepares the Windows update$`, s.coddyPreparesTheWindowsUpdate)
			sc.Step(`^Coddy prepares the Windows update with --no-restart$`, s.coddyPreparesTheWindowsUpdateWithoutRestart)
			sc.Step(`^the installed executable is the one from the release$`, s.installedExecutableIsFromTheRelease)
			sc.Step(`^Coddy reports the release it installed$`, s.reportsTheInstalledRelease)
			sc.Step(`^Coddy reports that it verified the archive against the published checksums$`, s.reportsAVerifiedArchive)
			sc.Step(`^Coddy reports that it resumed the download$`, s.reportsAResumedDownload)
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
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(body); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
