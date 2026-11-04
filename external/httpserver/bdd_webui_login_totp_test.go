//go:build http

package httpserver

// Steps of features/webui_login_totp.feature: the one-time code a sign-in asks
// for after the password. They extend the web sign-in harness
// (bdd_webui_login_test.go), and the code is computed the way an authenticator
// app computes it, from the secret and the clock.

import (
	"net/http"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// loginTestTOTPSecret is the secret the scenarios' authenticator app holds.
const loginTestTOTPSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

func (s *loginFeatureState) serverWithAccountAndCodes(user, password string) error {
	if err := s.start(user, password); err != nil {
		return err
	}
	s.replaceConfig(func(c *config.Config) { c.HTTPServer.Login.TOTPSecret = loginTestTOTPSecret })
	return nil
}

func (s *loginFeatureState) operatorTurnsCodesOn() error {
	s.replaceConfig(func(c *config.Config) { c.HTTPServer.Login.TOTPSecret = loginTestTOTPSecret })
	return nil
}

func (s *loginFeatureState) signInWithCurrentCode(user, password string) error {
	code, err := webauth.TOTPCode(loginTestTOTPSecret, time.Now())
	if err != nil {
		return err
	}
	return s.browse(http.MethodPost, "/coddy/auth/login",
		map[string]string{"user": user, "password": password, "code": code})
}

func (s *loginFeatureState) answerSaysCodeRequired() error {
	if got, _ := s.body["second_factor"].(string); got != secondFactorTOTP {
		return errStatus("second_factor = "+got+", want "+secondFactorTOTP, s.status, s.rawBody)
	}
	return nil
}

// registerLoginTOTPSteps adds the steps of the one-time code feature to the
// sign-in harness.
func registerLoginTOTPSteps(sc *godog.ScenarioContext, s *loginFeatureState) {
	sc.Step(`^a coddy HTTP server with the account "([^"]+)", password "([^"]+)" and one-time codes$`, s.serverWithAccountAndCodes)
	sc.Step(`^the operator turns one-time codes on$`, s.operatorTurnsCodesOn)
	sc.Step(`^the browser signs in as "([^"]+)" with password "([^"]+)" and the current code$`, s.signInWithCurrentCode)
	sc.Step(`^the answer says a one-time code is required$`, s.answerSaysCodeRequired)
}

func TestWebUILoginTOTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "webui-login-totp",
		ScenarioInitializer: initializeLoginScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/webui_login_totp.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI one-time code feature suite failed")
	}
}
