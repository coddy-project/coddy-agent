package main

// `coddy serve` and the credential rules of shared models: a shared-model token
// is a class of its own and never the same string as a main or a swarm token,
// and shared models are never served without some credential. The start-up
// check, the config reload and `-t` / `--dry-run` all see the tokens of
// --auth-token, --swarm-auth-token and --swarm-pairing-token and of the
// environment (docs/plans/remote-model-provider.md, 4.5).

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// noCredentialEnvironment keeps the machine's own credentials out of a check.
func noCredentialEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CODDY_HTTP_TOKEN", "CODDY_HTTP_USER", "CODDY_HTTP_PASSWORD", "CODDY_SWARM_TOKEN", "CODDY_SWARM_PAIRING_TOKEN", "CODDY_HOME"} {
		t.Setenv(name, "")
	}
}

func TestServeExtraTokensGathersFlagsAndEnvironment(t *testing.T) {
	noCredentialEnvironment(t)
	t.Setenv("CODDY_HTTP_TOKEN", "env-http")
	t.Setenv("CODDY_SWARM_TOKEN", "env-swarm")
	t.Setenv("CODDY_SWARM_PAIRING_TOKEN", "env-pairing")
	t.Setenv("CODDY_HTTP_USER", "me")
	t.Setenv("CODDY_HTTP_PASSWORD", "pw")

	got := serveExtraTokens("  flag-http ", "flag-swarm", "flag-pairing")

	want := func(name string, have []string, wants ...string) {
		t.Helper()
		for _, w := range wants {
			found := false
			for _, h := range have {
				found = found || h == w
			}
			if !found {
				t.Errorf("%s = %v, want it to hold %q", name, have, w)
			}
		}
	}
	want("HTTP", got.HTTP, "flag-http", "env-http")
	want("Swarm", got.Swarm, "flag-swarm", "flag-pairing", "env-swarm", "env-pairing")
	if !got.Login {
		t.Error("a sign-in account of the environment is not counted")
	}

	noCredentialEnvironment(t)
	if none := serveExtraTokens("", "", ""); len(none.HTTP) != 0 || len(none.Swarm) != 0 || none.Login {
		t.Errorf("nothing given, got %+v", none)
	}
}

func sharedModelConfig(extra string) *config.Config {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "openai/gpt-5.5", SharedAs: "smart"}},
	}
	switch extra {
	case "shared token":
		cfg.HTTPServer.SharedModels.Tokens = []string{"tok-shared"}
	case "main token":
		cfg.HTTPServer.AuthToken = "main"
	case "both same":
		cfg.HTTPServer.AuthToken = "same"
		cfg.HTTPServer.SharedModels.Tokens = []string{"same"}
	case "insecure":
		cfg.HTTPServer.AllowInsecure = true
	}
	return cfg
}

func TestCheckSharedModelsRefusesSharedModelsWithoutAnyCredential(t *testing.T) {
	noCredentialEnvironment(t)
	err := checkSharedModels(sharedModelConfig(""), config.ExtraTokens{})
	if err == nil {
		t.Fatal("shared models with no credential were accepted")
	}
	for _, want := range []string{"models[openai/gpt-5.5].shared_as", "authentication", "httpserver.shared_models.tokens"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}

	for name, tc := range map[string]struct {
		cfg   *config.Config
		extra config.ExtraTokens
	}{
		"a shared-model token":      {sharedModelConfig("shared token"), config.ExtraTokens{}},
		"a main token":              {sharedModelConfig("main token"), config.ExtraTokens{}},
		"allow_insecure":            {sharedModelConfig("insecure"), config.ExtraTokens{}},
		"--auth-token":              {sharedModelConfig(""), config.ExtraTokens{HTTP: []string{"from-flag"}}},
		"a sign-in out of band":     {sharedModelConfig(""), config.ExtraTokens{Login: true}},
		"nothing shared, no tokens": {&config.Config{}, config.ExtraTokens{}},
	} {
		if err := checkSharedModels(tc.cfg, tc.extra); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

func TestCheckSharedModelsRefusesATokenOfTwoClasses(t *testing.T) {
	noCredentialEnvironment(t)

	err := checkSharedModels(sharedModelConfig("both same"), config.ExtraTokens{})
	if err == nil || !strings.Contains(err.Error(), "httpserver.shared_models.tokens[0]") || !strings.Contains(err.Error(), "httpserver.auth_token") {
		t.Fatalf("the same token in two classes: %v", err)
	}
	if strings.Contains(strings.ReplaceAll(err.Error(), "the same token", ""), "same") {
		t.Errorf("the error carries the token: %v", err)
	}

	// The tokens the loader cannot see: the flags and the environment.
	cfg := sharedModelConfig("shared token")
	for name, extra := range map[string]config.ExtraTokens{
		"--auth-token":                        {HTTP: []string{"tok-shared"}},
		"--swarm-auth-token":                  {Swarm: []string{"tok-shared"}},
		"--swarm-pairing-token":               {Swarm: []string{"tok-shared"}},
		"CODDY_HTTP_TOKEN and its neighbours": {HTTP: []string{"tok-shared"}, Swarm: []string{"x"}},
	} {
		err := checkSharedModels(cfg, extra)
		if err == nil {
			t.Errorf("%s: a shared-model token equal to it was accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "tok-shared") {
			t.Errorf("%s: the error carries the token: %v", name, err)
		}
	}
	if err := checkSharedModels(cfg, config.ExtraTokens{HTTP: []string{"other"}, Swarm: []string{"another"}}); err != nil {
		t.Errorf("distinct tokens refused: %v", err)
	}
}

const sharedNoAuthYAML = `# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: openai
    type: openai
    api_base: http://127.0.0.1:1/v1
    api_key: sk-test
models:
  - model: openai/gpt-5.5
    shared_as: smart
agent:
  model: openai/gpt-5.5
`

func writeServeConfig(t *testing.T, body string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

// captureReport redirects the -t report and returns what it printed.
func captureReport(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	prev := configTestOutput
	configTestOutput = &out
	t.Cleanup(func() { configTestOutput = prev })
	return &out
}

// `coddy -t` on a config with shared_as and no authentication reports the error
// at the line of shared_as and exits non-zero; the flag a server would be started
// with closes the gate, so the same file passes with it.
func TestServeTestConfigReportsSharedModelsWithoutAuthentication(t *testing.T) {
	noCredentialEnvironment(t)
	home, path := writeServeConfig(t, sharedNoAuthYAML)
	out := captureReport(t)

	err := runServe([]string{"--test-config", "--home", home, "--config", path})
	if err == nil {
		t.Fatalf("a config that shares a model with no authentication was accepted:\n%s", out)
	}
	if want := path + ":9:"; !strings.Contains(out.String(), want) {
		t.Errorf("the report has no finding at %s:\n%s", want, out)
	}
	if !strings.Contains(out.String(), "authentication") {
		t.Errorf("the report does not say what is missing:\n%s", out)
	}

	out.Reset()
	if err := runServe([]string{"--test-config", "--home", home, "--config", path, "--auth-token", "from-flag"}); err != nil {
		t.Fatalf("--auth-token closes the gate but -t still failed: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), path+": valid") {
		t.Errorf("the report does not call the file valid:\n%s", out)
	}
}

func TestServeTestConfigSeesAFlagToken(t *testing.T) {
	noCredentialEnvironment(t)
	body := sharedNoAuthYAML + "httpserver:\n  shared_models:\n    tokens:\n      - tok-shared\n"
	home, path := writeServeConfig(t, body)
	out := captureReport(t)

	if err := runServe([]string{"--test-config", "--home", home, "--config", path}); err != nil {
		t.Fatalf("a valid file failed: %v\n%s", err, out)
	}
	for _, flags := range [][]string{
		{"--auth-token", "tok-shared"},
		{"--swarm-auth-token", "tok-shared"},
		{"--swarm-pairing-token", "tok-shared"},
	} {
		out.Reset()
		args := append([]string{"--test-config", "--home", home, "--config", path}, flags...)
		if err := runServe(args); err == nil {
			t.Errorf("%v: a shared-model token that is also a flag token was accepted:\n%s", flags, out)
			continue
		}
		if !strings.Contains(out.String(), "shared_models.tokens") {
			t.Errorf("%v: the report does not name the key of the token:\n%s", flags, out)
		}
		if strings.Contains(out.String(), "tok-shared") {
			t.Errorf("%v: the report carries the token:\n%s", flags, out)
		}
	}

	// The environment counts the same way.
	t.Setenv("CODDY_HTTP_TOKEN", "tok-shared")
	out.Reset()
	if err := runServe([]string{"--test-config", "--home", home, "--config", path}); err == nil {
		t.Errorf("a shared-model token equal to CODDY_HTTP_TOKEN was accepted:\n%s", out)
	}
}

// --dry-run runs the same static check first, with the same flags.
func TestServeDryRunSeesTheTokensOfItsFlags(t *testing.T) {
	noCredentialEnvironment(t)
	home, path := writeServeConfig(t, sharedNoAuthYAML)
	out := captureReport(t)

	err := runServe([]string{"--dry-run", "--home", home, "--config", path})
	if err == nil {
		t.Fatalf("a dry run accepted shared models with no authentication:\n%s", out)
	}
	if !strings.Contains(out.String(), "authentication") {
		t.Errorf("the dry run does not say what is missing:\n%s", out)
	}

	out.Reset()
	err = runServe([]string{"--dry-run", "--home", home, "--config", path, "--auth-token", "from-flag"})
	if err != nil && strings.Contains(out.String(), "authentication") {
		t.Fatalf("--auth-token closes the gate but the dry run still reports it:\n%s", out)
	}
}

// Nothing else changes: the check of a config that shares nothing is as before,
// and the console takes the same entry without flags.
func TestRunConfigTestKeepsItsShape(t *testing.T) {
	noCredentialEnvironment(t)
	home, path := writeServeConfig(t, strings.ReplaceAll(sharedNoAuthYAML, "    shared_as: smart\n", ""))
	out := captureReport(t)

	if err := runConfigTest(config.CLIPaths{Home: home, Config: path}); err != nil {
		t.Fatalf("a valid file failed: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), path+": valid") {
		t.Errorf("report = %q", out)
	}
}

// --- credentials kept in <home>/.env -----------------------------------------

// credentialEnvNames are the variables the credential rules read.
var credentialEnvNames = []string{"CODDY_HTTP_TOKEN", "CODDY_HTTP_USER", "CODDY_HTTP_PASSWORD", "CODDY_SWARM_TOKEN", "CODDY_SWARM_PAIRING_TOKEN"}

// unsetCredentialEnvironment leaves the credential variables out of the
// environment altogether - not set to an empty value, which keeps <home>/.env
// from filling them in - and puts them back after the test.
func unsetCredentialEnvironment(t *testing.T) {
	t.Helper()
	noCredentialEnvironment(t)
	for _, name := range credentialEnvNames {
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func writeDotEnv(t *testing.T, home string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// serveUntilNothingToRun runs `coddy serve` as far as the start-up checks
// go and no further: with the HTTP API switched off by flag nothing is enabled,
// so a configuration that passes every check ends in the "nothing enabled"
// refusal, before a listener opens.
func serveUntilNothingToRun(t *testing.T, home, path string, flags ...string) error {
	t.Helper()
	args := append([]string{"--home", home, "--config", path, "--http=false"}, flags...)
	return runServe(args)
}

func isNothingEnabled(err error) bool {
	var none *serve.ErrNothingEnabled
	return errors.As(err, &none)
}

const sharedTokenYAML = sharedNoAuthYAML + "httpserver:\n  shared_models:\n    tokens:\n      - tok-shared\n"

// The documented home of CODDY_HTTP_TOKEN and of the web sign-in account is
// <home>/.env, which the config loader reads into the environment. A start must
// see them the way `-t` does: shared models behind such a credential are
// served, not refused for having none.
func TestServeStartSeesTheCredentialsKeptInDotEnv(t *testing.T) {
	for name, env := range map[string][]string{
		"CODDY_HTTP_TOKEN":               {"CODDY_HTTP_TOKEN=env-main-token"},
		"CODDY_HTTP_USER and PASSWORD":   {"CODDY_HTTP_USER=me", "CODDY_HTTP_PASSWORD=pw"},
		"an exported, quoted token line": {`export CODDY_HTTP_TOKEN="env-main-token"`},
	} {
		t.Run(name, func(t *testing.T) {
			unsetCredentialEnvironment(t)
			home, path := writeServeConfig(t, sharedNoAuthYAML)
			writeDotEnv(t, home, env...)

			// -t and a start agree.
			out := captureReport(t)
			if err := runServe([]string{"--test-config", "--home", home, "--config", path}); err != nil {
				t.Fatalf("-t refused the file: %v\n%s", err, out)
			}

			unsetCredentialEnvironment(t) // the check above read .env into the environment
			err := serveUntilNothingToRun(t, home, path)
			if !isNothingEnabled(err) {
				t.Fatalf("the start did not get past the shared-model credential check: %v", err)
			}
		})
	}

	t.Run("without the file the start is refused, and says why", func(t *testing.T) {
		unsetCredentialEnvironment(t)
		home, path := writeServeConfig(t, sharedNoAuthYAML)
		err := serveUntilNothingToRun(t, home, path)
		if err == nil || isNothingEnabled(err) || !strings.Contains(err.Error(), "authentication") {
			t.Fatalf("err = %v, want the refusal for shared models with no credential", err)
		}
	})
}

// A shared-model token is of its own class: equal to a token of .env it is
// refused at start, exactly as it is refused when the same token is exported.
func TestServeStartRefusesASharedModelTokenThatIsAlsoATokenOfDotEnv(t *testing.T) {
	for _, name := range []string{"CODDY_HTTP_TOKEN", "CODDY_SWARM_TOKEN", "CODDY_SWARM_PAIRING_TOKEN"} {
		t.Run(name, func(t *testing.T) {
			unsetCredentialEnvironment(t)
			home, path := writeServeConfig(t, sharedTokenYAML)
			writeDotEnv(t, home, name+"=tok-shared")

			err := serveUntilNothingToRun(t, home, path)
			if err == nil || isNothingEnabled(err) {
				t.Fatalf("a shared-model token that is also %s of .env was accepted: %v", name, err)
			}
			if !strings.Contains(err.Error(), "httpserver.shared_models.tokens[0]") {
				t.Errorf("the refusal does not name the key: %v", err)
			}
			if strings.Contains(err.Error(), "tok-shared") {
				t.Errorf("the refusal carries the token: %v", err)
			}
		})
	}

	t.Run("distinct tokens are served", func(t *testing.T) {
		unsetCredentialEnvironment(t)
		home, path := writeServeConfig(t, sharedTokenYAML)
		writeDotEnv(t, home, "CODDY_HTTP_TOKEN=env-main-token", "CODDY_SWARM_TOKEN=env-swarm-token")
		if err := serveUntilNothingToRun(t, home, path); !isNothingEnabled(err) {
			t.Fatalf("err = %v", err)
		}
	})
}

// The gatherer reads the environment when it is asked: built before the load
// and asked after it, it sees what <home>/.env put there.
func TestServeExtraNowReadsTheEnvironmentWhenAsked(t *testing.T) {
	unsetCredentialEnvironment(t)
	home, path := writeServeConfig(t, sharedNoAuthYAML)
	writeDotEnv(t, home,
		"CODDY_HTTP_TOKEN=env-main-token",
		"CODDY_SWARM_TOKEN=env-swarm-token",
		"CODDY_SWARM_PAIRING_TOKEN=env-pairing-token",
		"CODDY_HTTP_USER=me", "CODDY_HTTP_PASSWORD=pw")

	extraNow := serveExtraNow("flag-http", "flag-swarm", "flag-pairing")
	before := extraNow()
	if len(before.HTTP) != 1 || before.HTTP[0] != "flag-http" || len(before.Swarm) != 2 || before.Login {
		t.Fatalf("before the load: %+v, want the flags only", before)
	}

	if _, err := config.LoadFromCLI(config.CLIPaths{Home: home, Config: path}); err != nil {
		t.Fatal(err)
	}
	after := extraNow()
	has := func(have []string, want string) bool {
		for _, h := range have {
			if h == want {
				return true
			}
		}
		return false
	}
	if !has(after.HTTP, "flag-http") || !has(after.HTTP, "env-main-token") {
		t.Errorf("HTTP = %v, want the flag and the .env token", after.HTTP)
	}
	for _, want := range []string{"flag-swarm", "flag-pairing", "env-swarm-token", "env-pairing-token"} {
		if !has(after.Swarm, want) {
			t.Errorf("Swarm = %v, want it to hold %q", after.Swarm, want)
		}
	}
	if !after.Login {
		t.Error("the sign-in account of .env is not counted")
	}
}

// The adjuster is what a start, a reload and the relay's own settings page
// run a configuration through: it asks for the credentials each time, so one
// kept in .env counts at every one of them.
func TestServeAdjusterSeesDotEnvAtStartAndOnEveryReload(t *testing.T) {
	unsetCredentialEnvironment(t)
	home, path := writeServeConfig(t, sharedNoAuthYAML)
	writeDotEnv(t, home, "CODDY_HTTP_TOKEN=tok-shared")
	cli := config.CLIPaths{Home: home, Config: path}

	// The command line is parsed, the adjuster built, and only then is the
	// configuration loaded - the order runServe has.
	adjust := serveAdjuster(func(*config.Config) error { return nil }, serveExtraNow("", "", ""))
	load := func() *config.Config {
		t.Helper()
		cfg, err := config.LoadFromCLI(cli)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	rewrite := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// No credential in the file: the token of .env closes the gate.
	if err := adjust(load()); err != nil {
		t.Fatalf("start: the token of .env was not seen: %v", err)
	}
	// A reload that brings a shared-model token of its own is accepted, and one
	// that reuses the main token of .env is refused and names the key only.
	rewrite(strings.Replace(sharedTokenYAML, "tok-shared", "tok-other", 1))
	if err := adjust(load()); err != nil {
		t.Fatalf("reload with a distinct token: %v", err)
	}
	rewrite(sharedTokenYAML)
	err := adjust(load())
	if err == nil || !strings.Contains(err.Error(), "httpserver.shared_models.tokens[0]") {
		t.Fatalf("reload with the main token of .env as a shared-model token: %v", err)
	}
	if strings.Contains(err.Error(), "tok-shared") {
		t.Errorf("the refusal carries the token: %v", err)
	}

	// The overrides still run first and their failure is the answer.
	boom := errors.New("override failed")
	failing := serveAdjuster(func(*config.Config) error { return boom }, serveExtraNow("", "", ""))
	if err := failing(load()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// `coddy serve install` checks the home's config.yaml with config.RunCheck,
// which reads <home>/.env before it looks at the credentials: a token kept
// there counts, as it does at a start, so the unit is not refused (or
// installed) on a different reading than the service will have.
func TestServeInstallCheckSeesTheCredentialsKeptInDotEnv(t *testing.T) {
	unsetCredentialEnvironment(t)
	home, path := writeServeConfig(t, sharedNoAuthYAML)
	cli := config.CLIPaths{Home: home, Config: path}

	var out bytes.Buffer
	if err := config.RunCheck(&out, cli); err == nil {
		t.Fatalf("shared models with no credential passed the install check:\n%s", out.String())
	}

	unsetCredentialEnvironment(t)
	writeDotEnv(t, home, "CODDY_HTTP_TOKEN=env-main-token")
	out.Reset()
	if err := config.RunCheck(&out, cli); err != nil {
		t.Fatalf("the token kept in .env was not seen by the install check: %v\n%s", err, out.String())
	}
}
