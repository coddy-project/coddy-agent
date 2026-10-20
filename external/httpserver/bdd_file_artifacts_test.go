//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/cucumber/godog"
)

type artifactFeatureState struct {
	t                            *testing.T
	tsURL, sessionID, artifactID string
	sourcePath                   string
	body                         string
	messages                     []byte
	status                       int
	opener                       *revealRecorder
	close                        func()
}

func (s *artifactFeatureState) reset() error {
	if s.close != nil {
		s.close()
	}
	s.tsURL = ""
	s.body = ""
	s.messages = nil
	s.status = 0
	return nil
}

func (s *artifactFeatureState) reveal() error {
	r, e := http.Post(s.tsURL+"/coddy/sessions/"+s.sessionID+"/artifacts/"+s.artifactID+"/reveal", "application/json", nil)
	if e != nil {
		return e
	}
	defer func() { _ = r.Body.Close() }()
	s.status = r.StatusCode
	return nil
}

func (s *artifactFeatureState) desktopAskedToRevealTheReport() error {
	if s.status != http.StatusNoContent {
		return fmt.Errorf("reveal status %d", s.status)
	}
	if got := s.opener.revealed(); len(got) != 1 || got[0] != s.sourcePath {
		return fmt.Errorf("the desktop opener was handed %v, want %q", got, s.sourcePath)
	}
	return nil
}
func (s *artifactFeatureState) shared(name string) error {
	if name != "report.txt" {
		return fmt.Errorf("unexpected artifact %q", name)
	}
	ts, _, id, _, a, opener := artifactServerRevealing(s.t)
	s.tsURL, s.sessionID, s.artifactID = ts.URL, id, a.ID
	s.sourcePath, s.opener = a.SourcePath, opener
	s.close = ts.Close
	return nil
}
func (s *artifactFeatureState) download() error {
	r, e := http.Get(s.tsURL + "/coddy/sessions/" + s.sessionID + "/artifacts/" + s.artifactID)
	if e != nil {
		return e
	}
	defer func() { _ = r.Body.Close() }()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", r.StatusCode)
	}
	s.body = string(b)
	return nil
}
func (s *artifactFeatureState) contains(w string) error {
	if s.body != w {
		return fmt.Errorf("artifact body %q, want %q", s.body, w)
	}
	return nil
}
func (s *artifactFeatureState) deleteSource() error {
	return os.Remove(s.sourcePath)
}

func (s *artifactFeatureState) readMessages() error {
	r, e := http.Get(s.tsURL + "/coddy/sessions/" + s.sessionID + "/messages")
	if e != nil {
		return e
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("messages status %d", r.StatusCode)
	}
	s.messages, e = io.ReadAll(r.Body)
	return e
}

// sourceIs finds the shared report on the tool row of the listing, where its
// card is drawn from after a reload.
func (s *artifactFeatureState) sourceIs(want string) error {
	var listing struct {
		Messages []struct {
			Role      string `json:"role"`
			Artifacts []struct {
				ID          string `json:"id"`
				SourceState string `json:"sourceState"`
			} `json:"artifacts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(s.messages, &listing); err != nil {
		return err
	}
	for _, m := range listing.Messages {
		for _, a := range m.Artifacts {
			if a.ID != s.artifactID {
				continue
			}
			if a.SourceState != want {
				return fmt.Errorf("source of the shared report is %q, want %q", a.SourceState, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the listing has no shared report: %s", s.messages)
}

func TestFileArtifactsFeature(t *testing.T) {
	s := &artifactFeatureState{t: t}
	suite := godog.TestSuite{Name: "file_artifacts", ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			if s.close != nil {
				s.close()
			}
			return ctx, nil
		})
		sc.Step(`^a deterministic session has shared "([^"]*)"$`, s.shared)
		sc.Step(`^the client downloads the shared artifact$`, s.download)
		sc.Step(`^the artifact download contains "([^"]*)"$`, s.contains)
		sc.Step(`^the client asks the server to reveal the shared artifact$`, s.reveal)
		sc.Step(`^the server asks the desktop to reveal the shared report$`, s.desktopAskedToRevealTheReport)
		sc.Step(`^the source of the shared report is deleted from the workspace$`, s.deleteSource)
		sc.Step(`^the client reads the session's messages$`, s.readMessages)
		sc.Step(`^the shared report's source is "([^"]*)"$`, s.sourceIs)
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/file_artifacts.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("file artifact feature failed")
	}
}
