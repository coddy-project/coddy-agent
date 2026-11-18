//go:build http

package httpserver

// Steps of features/webui_login_totp.feature: the one-time code a sign-in asks
// for after the password. They extend the web sign-in harness
// (bdd_webui_login_test.go), and the code is computed the way an authenticator
// app computes it, from the secret and the clock.

import (
	"net/http"
	"os"
	"path/filepath"
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

func (s *loginFeatureState) backupCodesAvailable() error {
	state, codes, err := webauth.GenerateBackupCodes(2)
	if err != nil {
		return err
	}
	next := *s.srv.activeCfg()
	next.HTTPServer.Login.BackupCodes = state
	next.Paths.ConfigPath = filepath.Join(s.root, "config.yaml")
	next.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
	data, err := config.MarshalConfigYAMLForFile(&next, next.Paths.ConfigPath)
	if err != nil {
		return err
	}
	if err = os.WriteFile(next.Paths.ConfigPath, data, 0600); err != nil {
		return err
	}
	if err = s.mgr.InstallConfig(&next); err != nil {
		return err
	}
	s.backupCode = codes[0]
	return nil
}
func (s *loginFeatureState) verifyBackupCode() error {
	challenge, _ := s.body["challenge"].(string)
	return s.browse(http.MethodPost, "/coddy/auth/login/verify", map[string]string{"challenge": challenge, "code": s.backupCode})
}

func (s *loginFeatureState) operatorTurnsCodesOn() error {
	s.replaceConfig(func(c *config.Config) { c.HTTPServer.Login.TOTPSecret = loginTestTOTPSecret })
	return nil
}

func (s *loginFeatureState) pendingChallenge() error {
	if s.status != http.StatusOK || s.body["second_factor"] != "totp" || s.body["challenge"] == nil {
		return errStatus("missing challenge", s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) verifyCurrentCode() error {
	challenge, _ := s.body["challenge"].(string)
	code, err := webauth.TOTPCode(loginTestTOTPSecret, time.Now())
	if err != nil {
		return err
	}
	return s.browse(http.MethodPost, "/coddy/auth/login/verify", map[string]string{"challenge": challenge, "code": code})
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
	sc.Step(`^backup codes are available$`, s.backupCodesAvailable)
	sc.Step(`^the browser verifies a backup code$`, s.verifyBackupCode)
	sc.Step(`^the operator turns one-time codes on$`, s.operatorTurnsCodesOn)
	sc.Step(`^the answer contains a pending challenge without a session$`, s.pendingChallenge)
	sc.Step(`^the browser verifies the current code$`, s.verifyCurrentCode)
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
