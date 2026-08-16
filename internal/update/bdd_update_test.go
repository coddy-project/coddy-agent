package update

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type updateFeatureState struct {
	archive   []byte
	server    *httptest.Server
	dir       string
	out       bytes.Buffer
	scheduled *windowsUpdateRequest
}

func (s *updateFeatureState) reset() {
	if s.server != nil {
		s.server.Close()
	}
	*s = updateFeatureState{}
}

func (s *updateFeatureState) newerWindowsReleaseIsAvailable() error {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("coddy.exe")
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("new Windows Coddy")); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	s.archive = archive.Bytes()
	s.dir, err = os.MkdirTemp("", "coddy-update-feature-*")
	if err != nil {
		return err
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/coddy-project/coddy-agent/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":"0.9.70","assets":[{"name":"coddy_0.9.70_windows_amd64.zip","browser_download_url":"http://%s/asset.zip"}]}`, r.Host)
		case "/asset.zip":
			_, _ = w.Write(s.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	return nil
}

func (s *updateFeatureState) coddyPreparesTheWindowsUpdate() error {
	dest := filepath.Join(s.dir, "coddy.exe")
	if err := os.WriteFile(dest, []byte("old Windows Coddy"), 0o755); err != nil {
		return err
	}
	return Run(context.Background(), Options{
		APIBase:        s.server.URL,
		Repo:           DefaultRepo,
		CurrentVersion: "0.9.67",
		GOOS:           "windows",
		GOARCH:         "amd64",
		InstallPath:    dest,
		Yes:            true,
		Stdout:         &s.out,
		windowsInstaller: func(req windowsUpdateRequest) error {
			s.scheduled = &req
			return nil
		},
	})
}

func (s *updateFeatureState) updateIsReady() error {
	if !strings.Contains(s.out.String(), "Update downloaded") {
		return fmt.Errorf("output does not report a ready update: %q", s.out.String())
	}
	return nil
}

func (s *updateFeatureState) helperWillRestartCoddy() error {
	if s.scheduled == nil {
		return fmt.Errorf("no helper was scheduled")
	}
	defer func() { _ = os.Remove(s.scheduled.StagedPath) }()
	if !s.scheduled.Restart {
		return fmt.Errorf("helper restart = false")
	}
	if s.scheduled.TargetPath != filepath.Join(s.dir, "coddy.exe") {
		return fmt.Errorf("helper target = %q", s.scheduled.TargetPath)
	}
	got, err := os.ReadFile(s.scheduled.StagedPath)
	if err != nil {
		return err
	}
	if string(got) != "new Windows Coddy" {
		return fmt.Errorf("staged binary = %q", got)
	}
	return nil
}

func TestUpdateFeature(t *testing.T) {
	s := &updateFeatureState{}
	t.Cleanup(func() {
		dir := s.dir
		s.reset()
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	})

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(context.Context, *godog.Scenario) (context.Context, error) {
				dir := s.dir
				s.reset()
				if dir != "" {
					_ = os.RemoveAll(dir)
				}
				return context.Background(), nil
			})
			sc.Step(`^a newer Windows Coddy release is available$`, s.newerWindowsReleaseIsAvailable)
			sc.Step(`^Coddy prepares the Windows update$`, s.coddyPreparesTheWindowsUpdate)
			sc.Step(`^it reports that the update is ready$`, s.updateIsReady)
			sc.Step(`^it schedules a helper that will restart Coddy$`, s.helperWillRestartCoddy)
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
