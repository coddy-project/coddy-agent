package preview

// Godog harness for features/preview_publish.feature: drives preview_publish
// and preview_unpublish against a real artifact root and a real published
// origin, so every scenario asserts what a visitor behind the edge would get.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const (
	bddPublishSession = "bdd-publish"
	bddOtherSession   = "bdd-publish-other"
)

type publishState struct {
	root   string
	srv    *publishedServer
	done   chan struct{}
	cancel context.CancelFunc

	dir       string
	env       *tooling.Env
	otherDir  string
	otherEnv  *tooling.Env
	publicURL string

	result    string
	address   string
	previous  string
	other     string
	expiresIn time.Duration
}

func (s *publishState) reset() error {
	root, err := os.MkdirTemp("", "coddy-publish-root-")
	if err != nil {
		return err
	}
	s.root = root
	if s.dir, err = os.MkdirTemp("", "coddy-publish-src-"); err != nil {
		return err
	}
	if s.otherDir, err = os.MkdirTemp("", "coddy-publish-other-"); err != nil {
		return err
	}
	s.result, s.address, s.previous, s.other, s.publicURL = "", "", "", "", ""
	s.expiresIn = 0
	return nil
}

func (s *publishState) cleanup() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
		s.cancel = nil
	}
	for _, dir := range []string{s.root, s.dir, s.otherDir} {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	}
}

// originIsRunning starts the published-preview origin on a free loopback port
// and points both sessions' settings at it. A deployment pins the port in
// config.yaml; a test that did the same could not run twice at once.
func (s *publishState) originIsRunning() error {
	srv, err := listenPublished(PublishOptions{Root: s.root, ListenAddr: "127.0.0.1:0"})
	if err != nil {
		return err
	}
	s.srv = srv
	s.publicURL = "http://" + srv.addr() + "/"
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		_ = srv.serve(ctx)
	}()

	settings := func() *tooling.PreviewPublishSettings {
		return &tooling.PreviewPublishSettings{
			Enabled: true, Root: s.root, PublicURL: s.publicURL,
			TTLSeconds: 3600, MaxBytes: 1 << 20,
		}
	}
	s.env = &tooling.Env{CWD: s.dir, SessionID: bddPublishSession, PreviewPublish: settings()}
	s.otherEnv = &tooling.Env{CWD: s.otherDir, SessionID: bddOtherSession, PreviewPublish: settings()}
	return nil
}

func (s *publishState) projectPage(name, text string) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte("<!doctype html><p>"+text+"</p>"), 0o644)
}

func (s *publishState) projectHoldsDotFile(name, body string) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte(body), 0o600)
}

func (s *publishState) rewritePage(name, text string) error { return s.projectPage(name, text) }

// publishWith runs the tool and returns its result and the address it reported.
func publishWith(env *tooling.Env, args map[string]interface{}) (result, address string, err error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", "", err
	}
	out, err := PreviewPublishTool().Execute(context.Background(), string(raw), env)
	if err != nil {
		return "", "", fmt.Errorf("preview_publish returned %v", err)
	}
	address = urlInResult.FindString(out)
	if address == "" {
		return "", "", fmt.Errorf("no address in the tool result %q", out)
	}
	return out, address, nil
}

// publishOwn publishes this session's directory and remembers what came back,
// keeping the address of the publish before it for the republish scenario.
func (s *publishState) publishOwn(args map[string]interface{}) error {
	out, address, err := publishWith(s.env, args)
	if err != nil {
		return err
	}
	s.previous, s.address, s.result = s.address, address, out
	return nil
}

func (s *publishState) publish() error { return s.publishOwn(map[string]interface{}{}) }

func (s *publishState) publishAs(name string) error {
	return s.publishOwn(map[string]interface{}{"name": name})
}

func (s *publishState) publishForSeconds(seconds int) error {
	if err := s.publishOwn(map[string]interface{}{"ttl_seconds": seconds}); err != nil {
		return err
	}
	s.expiresIn = time.Duration(seconds) * time.Second
	return nil
}

func (s *publishState) otherSessionPublishes(name string) error {
	if err := os.WriteFile(filepath.Join(s.otherDir, "index.html"),
		[]byte("<!doctype html><p>another session</p>"), 0o644); err != nil {
		return err
	}
	_, address, err := publishWith(s.otherEnv, map[string]interface{}{"name": name})
	if err != nil {
		return err
	}
	s.other = address
	return nil
}

func (s *publishState) unpublish(name string) error {
	_, err := PreviewUnpublishTool().Execute(context.Background(),
		fmt.Sprintf(`{"name":%q}`, name), s.env)
	return err
}

func (s *publishState) reportsAddressUnderPublicURL() error {
	if !strings.HasPrefix(s.address, s.publicURL) {
		return fmt.Errorf("address %q is not under the public URL %q", s.address, s.publicURL)
	}
	if !strings.HasSuffix(s.address, "/") {
		return fmt.Errorf("address %q does not end in a slash, so a relative asset of the page would leave the artifact", s.address)
	}
	if strings.TrimPrefix(s.address, s.publicURL) == "" {
		return fmt.Errorf("address %q names no artifact", s.address)
	}
	return nil
}

func (s *publishState) openingReturns(text string) error {
	body, err := get(s.address)
	if err != nil {
		return err
	}
	if !strings.Contains(body, text) {
		return fmt.Errorf("page %q does not contain %q", body, text)
	}
	return nil
}

func (s *publishState) invitesTheUser() error {
	lower := strings.ToLower(s.result)
	for _, want := range []string{"invite", "browser", s.address} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			return fmt.Errorf("tool result %q does not mention %q", s.result, want)
		}
	}
	return nil
}

func (s *publishState) sameAddress() error {
	if s.previous == "" || s.address != s.previous {
		return fmt.Errorf("the second publish reported %q, the first reported %q", s.address, s.previous)
	}
	return nil
}

func (s *publishState) addressesDiffer() error {
	if s.other == "" || s.other == s.address {
		return fmt.Errorf("both sessions were given %q", s.address)
	}
	return nil
}

func (s *publishState) eachAddressReturnsItsOwnPage() error {
	if err := s.openingReturns("published build"); err != nil {
		return err
	}
	body, err := get(s.other)
	if err != nil {
		return err
	}
	if !strings.Contains(body, "another session") {
		return fmt.Errorf("the other session's address returned %q", body)
	}
	return nil
}

// artifactDir is where the address s.address lives on the disk.
func (s *publishState) artifactDir() (string, error) {
	u, err := url.Parse(s.address)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(s.publicURL)
	if err != nil {
		return "", err
	}
	id := strings.Trim(strings.TrimPrefix(u.Path, base.Path), "/")
	if id == "" {
		return "", fmt.Errorf("address %q names no artifact", s.address)
	}
	return filepath.Join(s.root, id), nil
}

func (s *publishState) artifactHoldsNo(name string) error {
	dir, err := s.artifactDir()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
		return fmt.Errorf("%s was copied into the artifact", name)
	}
	return nil
}

func (s *publishState) askingForAnswers404(name string) error {
	return status404(s.address + name)
}

func (s *publishState) addressAnswers404() error { return status404(s.address) }

func (s *publishState) rootAnswers404() error {
	return status404("http://" + s.srv.addr() + "/")
}

func (s *publishState) lifetimeElapses() error {
	if s.expiresIn <= 0 {
		return fmt.Errorf("the artifact has no lifetime to wait out")
	}
	time.Sleep(s.expiresIn + 200*time.Millisecond)
	return nil
}

func status404(address string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(address) //nolint:noctx // a loopback request of a test, bounded by the client's own timeout
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("GET %s answered %s, want 404", address, resp.Status)
	}
	return nil
}

func initializePreviewPublishScenario(sc *godog.ScenarioContext) {
	s := &publishState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a published-preview origin over an artifact root$`, s.originIsRunning)
	sc.Step(`^a project directory with an "([^"]*)" that says "([^"]*)"$`, s.projectPage)
	sc.Step(`^the project directory holds a "([^"]*)" with "([^"]*)"$`, s.projectHoldsDotFile)
	sc.Step(`^I publish the project directory$`, s.publish)
	sc.Step(`^I publish the project directory as "([^"]*)"$`, s.publishAs)
	sc.Step(`^I publish the project directory for (\d+) second$`, s.publishForSeconds)
	sc.Step(`^another session publishes its own directory as "([^"]*)"$`, s.otherSessionPublishes)
	sc.Step(`^I unpublish "([^"]*)"$`, s.unpublish)
	sc.Step(`^"([^"]*)" is rewritten to say "([^"]*)"$`, s.rewritePage)
	sc.Step(`^the tool reports an address under the configured public URL$`, s.reportsAddressUnderPublicURL)
	sc.Step(`^opening that address returns "([^"]*)"$`, s.openingReturns)
	sc.Step(`^the tool result invites the user to open that address in a browser$`, s.invitesTheUser)
	sc.Step(`^the tool reports the same address as before$`, s.sameAddress)
	sc.Step(`^the two addresses differ$`, s.addressesDiffer)
	sc.Step(`^each address returns the page of the session that published it$`, s.eachAddressReturnsItsOwnPage)
	sc.Step(`^the artifact holds no "([^"]*)"$`, s.artifactHoldsNo)
	sc.Step(`^asking that address for "([^"]*)" answers 404$`, s.askingForAnswers404)
	sc.Step(`^that address answers 404$`, s.addressAnswers404)
	sc.Step(`^that artifact's lifetime elapses$`, s.lifetimeElapses)
	sc.Step(`^asking the origin for its root answers 404$`, s.rootAnswers404)
}

func TestPreviewPublishFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "preview-publish",
		ScenarioInitializer: initializePreviewPublishScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/preview_publish.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("preview publish feature suite failed")
	}
}
