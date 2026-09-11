package main

// Godog harness for features/config_dry_run.feature (every build) and, under
// -tags http, features/config_dry_run_serve.feature: runs --dry-run on the
// console, coddy acp and coddy serve against configs written into a
// temporary home, with stand-in HTTP servers playing the model server and the
// Telegram Bot API, and reads the report they print.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/dryrun"
)

type dryRunState struct {
	home     string
	cfgPath  string
	written  string
	out      bytes.Buffer
	runErr   error
	prevOut  io.Writer
	servers  []*httptest.Server
	holder   net.Listener
	restore  []func()
	prevHome string
	hadHome  bool
}

func (s *dryRunState) reset() error {
	s.close()
	home, err := os.MkdirTemp("", "coddy-dry-run-*")
	if err != nil {
		return err
	}
	s.home = home
	s.cfgPath = filepath.Join(home, "config.yaml")
	s.written = ""
	s.out.Reset()
	s.runErr = nil
	s.prevOut = configTestOutput
	configTestOutput = &s.out
	s.prevHome, s.hadHome = os.LookupEnv("CODDY_HOME")
	return os.Unsetenv("CODDY_HOME")
}

func (s *dryRunState) close() {
	if s.prevOut != nil {
		configTestOutput = s.prevOut
		s.prevOut = nil
	}
	for _, srv := range s.servers {
		srv.Close()
	}
	s.servers = nil
	if s.holder != nil {
		_ = s.holder.Close()
		s.holder = nil
	}
	for i := len(s.restore) - 1; i >= 0; i-- {
		s.restore[i]()
	}
	s.restore = nil
	if s.hadHome {
		_ = os.Setenv("CODDY_HOME", s.prevHome)
		s.hadHome = false
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

func (s *dryRunState) setenv(key, value string) {
	prev, had := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	s.restore = append(s.restore, func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func (s *dryRunState) serve(h http.HandlerFunc) *httptest.Server {
	srv := httptest.NewServer(h)
	s.servers = append(s.servers, srv)
	return srv
}

// modelServer plays an OpenAI-compatible endpoint whose /models lists ids.
func (s *dryRunState) modelServer(ids ...string) *httptest.Server {
	return s.serve(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		var rows []string
		for _, id := range ids {
			rows = append(rows, fmt.Sprintf(`{"id":%q}`, id))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(rows, ","))
	})
}

func (s *dryRunState) write(body string) error {
	s.written = body
	return os.WriteFile(s.cfgPath, []byte(body), 0o644)
}

const dryRunModeline = "# yaml-language-server: $schema=https://coddy.dev/config.schema.json\n"

func (s *dryRunState) providerListing(name, model string) error {
	srv := s.modelServer(model)
	return s.write(dryRunModeline + fmt.Sprintf("providers:\n  - name: %s\n    type: openai\n    api_base: %s/v1\n", name, srv.URL))
}

func (s *dryRunState) usesModel(model string) error {
	return s.write(s.written + fmt.Sprintf("models:\n  - model: %s\nagent:\n  model: %s\n", model, model))
}

func (s *dryRunState) providerRejecting(name string) error {
	srv := s.serve(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	})
	return s.write(dryRunModeline + fmt.Sprintf("providers:\n  - name: %s\n    type: openai\n    api_base: %s/v1\n    api_key: nope\n", name, srv.URL))
}

func (s *dryRunState) mcpCommand(server, command string) error {
	return s.write(dryRunModeline + fmt.Sprintf("mcp_servers:\n  - name: %s\n    command: %s\n", server, command))
}

func (s *dryRunState) telegramAccepting(username string) error {
	srv := s.serve(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":%q}}`, username)
	})
	s.setenv(dryrun.TelegramAPIBaseEnv, srv.URL)
	return s.write(dryRunModeline + "gateways:\n  telegram:\n    enable: true\n    token: \"123456:dry-run\"\n")
}

func (s *dryRunState) promptsDirMissing() error {
	return s.write(dryRunModeline + "prompts:\n  dir: " + filepath.Join(s.home, "no-such-prompts") + "\n")
}

func (s *dryRunState) misspelledHTTPServerKey(line int) error {
	body := "httpserver:\n  host: 127.0.0.1\n  enabled: true\n"
	if got := strings.Count(strings.TrimSuffix(body, "\n"), "\n") + 1; got != line {
		return fmt.Errorf("the fixture puts enabled on line %d, the scenario says %d", got, line)
	}
	return s.write(body)
}

func (s *dryRunState) freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func (s *dryRunState) httpOnFreePort() error {
	port, err := s.freePort()
	if err != nil {
		return err
	}
	srv := s.modelServer("qwen")
	return s.write(dryRunModeline + fmt.Sprintf("providers:\n  - name: local\n    type: openai\n    api_base: %s/v1\nmodels:\n  - model: local/qwen\nagent:\n  model: local/qwen\nhttpserver:\n  host: 127.0.0.1\n  port: %d\n", srv.URL, port))
}

func (s *dryRunState) httpOnHeldPort() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.holder = l
	port := l.Addr().(*net.TCPAddr).Port
	return s.write(dryRunModeline + fmt.Sprintf("httpserver:\n  host: 127.0.0.1\n  port: %d\n", port))
}

func (s *dryRunState) run(fn func([]string) error, flags ...string) error {
	s.out.Reset()
	s.runErr = fn(append(flags, "--home", s.home, "--config", s.cfgPath))
	return nil
}

func (s *dryRunState) runCLIDry() error        { return s.run(runCLI, "--dry-run") }
func (s *dryRunState) runCLIDryVerbose() error { return s.run(runCLI, "--dry-run", "--test-config") }
func (s *dryRunState) runACPDry() error        { return s.run(runACP, "--dry-run") }
func (s *dryRunState) runACPDryVerbose() error { return s.run(runACP, "--dry-run", "--test-config") }
func (s *dryRunState) runServeDry() error      { return s.run(runServe, "--dry-run") }
func (s *dryRunState) runServeDryVerbose() error {
	return s.run(runServe, "--dry-run", "--test-config")
}

func (s *dryRunState) reportSaysValid() error {
	if !strings.Contains(s.out.String(), s.cfgPath+": valid") {
		return fmt.Errorf("the report does not call %s valid:\n%s", s.cfgPath, s.out.String())
	}
	return nil
}

func (s *dryRunState) singleStatusLine(prefix string) error {
	lines := strings.Split(strings.TrimRight(s.out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], prefix) {
		return fmt.Errorf("want one status line starting with %q, got:\n%s", prefix, s.out.String())
	}
	return nil
}

func (s *dryRunState) commandSucceeds() error {
	if s.runErr != nil {
		return fmt.Errorf("the command failed: %v\n%s", s.runErr, s.out.String())
	}
	return nil
}

func (s *dryRunState) commandFails() error {
	if s.runErr == nil {
		return fmt.Errorf("the command succeeded:\n%s", s.out.String())
	}
	return nil
}

// checkLine finds the report line for path with the given status.
func (s *dryRunState) checkLine(status, path string) (string, error) {
	for _, l := range strings.Split(s.out.String(), "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 2 && fields[0] == status && strings.HasPrefix(l[len(fields[0]):], strings.Repeat(" ", 1)) && strings.HasPrefix(strings.TrimSpace(l[len(fields[0]):]), path+":") {
			return l, nil
		}
	}
	return "", fmt.Errorf("no %s line for %s in:\n%s", status, path, s.out.String())
}

func (s *dryRunState) marksOK(path string) error {
	_, err := s.checkLine("ok", path)
	return err
}

func (s *dryRunState) marksOKMentioning(path, text string) error {
	l, err := s.checkLine("ok", path)
	if err != nil {
		return err
	}
	if !strings.Contains(l, text) {
		return fmt.Errorf("the ok line for %s does not mention %q: %s", path, text, l)
	}
	return nil
}

func (s *dryRunState) marksErrorMentioning(path, text string) error {
	l, err := s.checkLine("error", path)
	if err != nil {
		return err
	}
	if !strings.Contains(l, text) {
		return fmt.Errorf("the error line for %s does not mention %q: %s", path, text, l)
	}
	return nil
}

func (s *dryRunState) pointsAtLine(line int) error {
	want := s.cfgPath + ":" + strconv.Itoa(line) + ":"
	if !strings.Contains(s.out.String(), want) {
		return fmt.Errorf("no reference to %s in:\n%s", want, s.out.String())
	}
	return nil
}

func (s *dryRunState) pointsAtLineOf(text string) error {
	for i, l := range strings.Split(s.written, "\n") {
		if strings.Contains(l, text) {
			return s.pointsAtLine(i + 1)
		}
	}
	return fmt.Errorf("the fixture has no line containing %q", text)
}

func initializeDryRunScenario(sc *godog.ScenarioContext) {
	s := &dryRunState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a config\.yaml whose provider "([^"]*)" points at a model server listing "([^"]*)"$`, s.providerListing)
	sc.Step(`^the config uses model "([^"]*)"$`, s.usesModel)
	sc.Step(`^a config\.yaml whose provider "([^"]*)" points at a model server that rejects every request$`, s.providerRejecting)
	sc.Step(`^a config\.yaml with an MCP server "([^"]*)" whose command is "([^"]*)"$`, s.mcpCommand)
	sc.Step(`^a config\.yaml enabling the Telegram gateway with a token the Bot API accepts as "([^"]*)"$`, s.telegramAccepting)
	sc.Step(`^a config\.yaml whose prompts\.dir points at a folder that does not exist$`, s.promptsDirMissing)
	sc.Step(`^a config\.yaml whose httpserver section says "enabled: true" on line (\d+)$`, s.misspelledHTTPServerKey)
	sc.Step(`^a config\.yaml with the HTTP API on a free port and a provider that answers$`, s.httpOnFreePort)
	sc.Step(`^a config\.yaml with the HTTP API on a port another process holds$`, s.httpOnHeldPort)
	sc.Step(`^I run coddy with --dry-run$`, s.runCLIDry)
	sc.Step(`^I run coddy with --dry-run --test-config$`, s.runCLIDryVerbose)
	sc.Step(`^I run coddy acp with --dry-run$`, s.runACPDry)
	sc.Step(`^I run coddy acp with --dry-run --test-config$`, s.runACPDryVerbose)
	sc.Step(`^I run coddy serve with --dry-run$`, s.runServeDry)
	sc.Step(`^I run coddy serve with --dry-run --test-config$`, s.runServeDryVerbose)
	sc.Step(`^the report says the config is valid$`, s.reportSaysValid)
	sc.Step(`^the report is a single status line starting with "([^"]*)"$`, s.singleStatusLine)
	sc.Step(`^the command succeeds$`, s.commandSucceeds)
	sc.Step(`^the command fails$`, s.commandFails)
	sc.Step(`^the report marks ([^ ]+) as ok$`, s.marksOK)
	sc.Step(`^the report marks ([^ ]+) as ok mentioning "([^"]*)"$`, s.marksOKMentioning)
	sc.Step(`^the report marks ([^ ]+) as an error mentioning "([^"]*)"$`, s.marksErrorMentioning)
	sc.Step(`^the report points at line (\d+) of the config file$`, s.pointsAtLine)
	sc.Step(`^the report points at the line of "([^"]*)"$`, s.pointsAtLineOf)
}

func TestConfigDryRunFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "config-dry-run",
		ScenarioInitializer: initializeDryRunScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/config_dry_run.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("config dry run feature failed")
	}
}
