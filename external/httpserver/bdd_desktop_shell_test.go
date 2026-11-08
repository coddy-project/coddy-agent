//go:build http && ui && desktop

package httpserver

// Godog harness for features/desktop_shell.feature: where the tab shell is
// served and who may read it. The point of the first scenario is the gate -
// the shell sits under the "/" catch-all with the SPA, because a page served
// as a route of its own would be protected (isProtectedPattern), and the
// window would open on a 401 with no way to present the password.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

type desktopShellState struct {
	t    *testing.T
	srv  *Server
	page *httptest.ResponseRecorder
}

func (s *desktopShellState) reset(t *testing.T) {
	s.t = t
	s.srv = nil
	s.page = nil
}

func (s *desktopShellState) uiAsksForSignIn() error {
	s.srv = newLoginServer(s.t, configuredLogin(s.t))
	return nil
}

func (s *desktopShellState) opensTheShell() error {
	rec := httptest.NewRecorder()
	s.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, shell.Prefix, nil))
	s.page = rec
	return nil
}

func (s *desktopShellState) answersWithTheTabStrip() error {
	if s.page.Code != http.StatusOK {
		return fmt.Errorf("the shell answered %d, not 200: a browser with no cookie cannot sign in through this", s.page.Code)
	}
	body := s.page.Body.String()
	if !strings.Contains(body, `id="coddy-desktop-tabs"`) {
		return fmt.Errorf("the page carries no tab strip:\n%s", head(body))
	}
	return nil
}

func (s *desktopShellState) tellsALauncherADesktopIsHere() error {
	if got := s.page.Header().Get(shell.MarkerHeader); got == "" {
		return fmt.Errorf("the shell set no %s header, so a second launch cannot tell a Coddy desktop from any other server on the port", shell.MarkerHeader)
	}
	return nil
}

func (s *desktopShellState) framesTheWebUI() error {
	body := s.page.Body.String()
	if !strings.Contains(body, `id="coddy-desktop-frames"`) {
		return fmt.Errorf("the page has nowhere to put a chat:\n%s", head(body))
	}
	return nil
}

func (s *desktopShellState) sessionListStillGated() error {
	rec := httptest.NewRecorder()
	s.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/coddy/sessions", nil))
	if rec.Code != http.StatusUnauthorized {
		return fmt.Errorf("GET /coddy/sessions answered %d without a credential; the shell being public must not open the data behind it", rec.Code)
	}
	return nil
}

// servesForAWindow is what coddy-desktop does to the server it starts: it asks
// for nothing. The configuration is untouched - the same document still signs
// in a `coddy serve` reachable from elsewhere - so the step keeps the account
// to check afterwards.
func (s *desktopShellState) servesForAWindow() error {
	s.srv.SetNoAuth(true)
	return nil
}

func (s *desktopShellState) sessionListAnswersToo() error {
	rec := httptest.NewRecorder()
	s.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/coddy/sessions", nil))
	if rec.Code != http.StatusOK {
		return fmt.Errorf("GET /coddy/sessions answered %d behind a window that asks for nothing", rec.Code)
	}
	return nil
}

// The window asking for nothing must not be a configuration that was edited:
// the settings screen inside it draws the live document, so a value this
// process had quietly cleared is the value a save from that screen would write
// to the operator's file.
func (s *desktopShellState) accountStillConfigured() error {
	login := s.srv.activeCfg().HTTPServer.Login
	if !login.HasAccount() {
		return fmt.Errorf("the sign-in account is gone from the live configuration, so saving the settings from this window would take it off disk")
	}
	return nil
}

func head(s string) string {
	if len(s) > 600 {
		return s[:600] + "..."
	}
	return s
}

func TestDesktopShellFeature(t *testing.T) {
	st := &desktopShellState{}
	suite := godog.TestSuite{
		Name: "desktop-shell",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a Coddy whose web UI asks for a sign-in$`, func() error {
				st.reset(t)
				return st.uiAsksForSignIn()
			})
			sc.Step(`^a browser with no credential opens the tab shell$`, st.opensTheShell)
			sc.Step(`^the shell answers with its tab strip$`, st.answersWithTheTabStrip)
			sc.Step(`^it tells a launcher that a Coddy desktop is serving here$`, st.tellsALauncherADesktopIsHere)
			sc.Step(`^the shell frames the web UI of this server$`, st.framesTheWebUI)
			sc.Step(`^the session list it draws its titles from is still behind the gate$`, st.sessionListStillGated)
			sc.Step(`^the desktop serves it for a window of its own$`, st.servesForAWindow)
			sc.Step(`^the session list behind it answers too$`, st.sessionListAnswersToo)
			sc.Step(`^the sign-in account in the configuration is still there$`, st.accountStillConfigured)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/desktop_shell.feature"},
			TestingT: t,
			Output:   os.Stdout,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("desktop shell feature failed")
	}
}
