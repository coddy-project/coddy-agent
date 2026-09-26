//go:build http

package httpserver

// Godog harness for features/config_save_round_trip.feature: the settings screen's
// read-modify-write cycle (GET /coddy/config, PUT /coddy/config) over a config.yaml
// that spells its values the way operators write them, compared byte for byte with
// what lands on disk.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// spelledConfigYAML is a config kept by hand: paths under ${CODDY_HOME}, ~ and ${CWD},
// a key and a directory taken from the environment, quoted values, a flow list, a
// comment, and list entries that name only the fields their author cared about.
const spelledConfigYAML = `# yaml-language-server: $schema=https://coddy.dev/config.schema.json
# Workstation config, kept by hand.
providers:
  - name: local
    type: openai
    api_base: "http://127.0.0.1:8080/v1"
    api_key: ${CODDY_BDD_LOCAL_KEY}
  - name: spare
    type: openai
    api_key: "sk-spare" # rotated monthly
models:
  - model: local/qwen
    max_tokens: 4096
    temperature: 0.2
  - model: spare/tiny
    max_tokens: 1024
agent:
  model: local/qwen
  max_turns: 40
memory:
  dir: ${CODDY_HOME}/memory
skills:
  dirs:
    - ~/.agents/skills
    - ${CODDY_HOME}/skills
    - ${CWD}/.coddy/skills
    - ${CODDY_BDD_TEAM}/skills
logger:
  outputs: [stderr, file]
  file: ${CODDY_HOME}/coddy.log
`

type configRoundTripWorld struct {
	ts      *httptest.Server
	cfgPath string
	initial string
	saved   string
	// second is the document another browser read and holds on to.
	second map[string]interface{}
}

// startSpelledGateway serves spelledConfigYAML the way coddy serve would: the home is
// the --home directory (CODDY_HOME is not in the environment, so nothing but the
// loader knows what ${CODDY_HOME} stands for), and the relay's listen address is
// filled in on the live config as applyProcessOverrides in cmd/coddy/serve.go does.
func (w *configRoundTripWorld) startSpelledGateway(t *testing.T) error {
	t.Setenv(config.EnvCODDYHome, "")
	t.Setenv("CODDY_BDD_LOCAL_KEY", "sk-local")
	t.Setenv("CODDY_BDD_TEAM", "/opt/team")
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	paths := config.Paths{
		Home:       home,
		CWD:        filepath.Join(dir, "work"),
		ConfigPath: filepath.Join(home, "config.yaml"),
	}
	w.cfgPath = paths.ConfigPath
	w.initial = spelledConfigYAML
	if err := os.WriteFile(w.cfgPath, []byte(spelledConfigYAML), 0o644); err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		return err
	}
	cfg.Swarm.Host = cfg.Swarm.EffectiveHost()
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	w.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(w.ts.Close)
	return nil
}

// read fetches the config document the settings screen edits.
func (w *configRoundTripWorld) read() (map[string]interface{}, error) {
	if w.ts == nil {
		return nil, fmt.Errorf("gateway not started")
	}
	res, err := http.Get(w.ts.URL + "/coddy/config")
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /coddy/config: status %d, want 200", res.StatusCode)
	}
	var doc map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// save reads the config document, lets edit change it the way the form would, and
// writes the whole document back.
func (w *configRoundTripWorld) save(edit func(doc map[string]interface{}) error) error {
	doc, err := w.read()
	if err != nil {
		return err
	}
	if err := edit(doc); err != nil {
		return err
	}
	return w.put(doc)
}

// put writes doc as the settings screen does and reads the file that lands.
func (w *configRoundTripWorld) put(doc map[string]interface{}) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, w.ts.URL+"/coddy/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = put.Body.Close() }()
	if put.StatusCode != http.StatusOK {
		raw, _ := readAllString(put)
		return fmt.Errorf("PUT /coddy/config: status %d, want 200 (%s)", put.StatusCode, raw)
	}
	raw, err := os.ReadFile(w.cfgPath)
	if err != nil {
		return err
	}
	w.saved = string(raw)
	return nil
}

func (w *configRoundTripWorld) saveUnchanged() error {
	return w.save(func(map[string]interface{}) error { return nil })
}

func (w *configRoundTripWorld) saveWithField(field string, value int) error {
	return w.save(func(doc map[string]interface{}) error {
		section, key, ok := strings.Cut(field, ".")
		if !ok {
			return fmt.Errorf("field %q must be section.key", field)
		}
		obj, _ := doc[section].(map[string]interface{})
		if obj == nil {
			return fmt.Errorf("GET /coddy/config carries no %q section", section)
		}
		obj[key] = value
		return nil
	})
}

func (w *configRoundTripWorld) secondReads() error {
	doc, err := w.read()
	w.second = doc
	return err
}

// secondSaves sends back the document the second browser read long ago, with one
// value of its own changed.
func (w *configRoundTripWorld) secondSaves(field, value string) error {
	if w.second == nil {
		return fmt.Errorf("the second browser has read nothing")
	}
	section, key, ok := strings.Cut(field, ".")
	if !ok {
		return fmt.Errorf("field %q must be section.key", field)
	}
	obj, _ := w.second[section].(map[string]interface{})
	if obj == nil {
		return fmt.Errorf("the document carries no %q section", section)
	}
	obj[key] = value
	return w.put(w.second)
}

func (w *configRoundTripWorld) wantBothSaves() error {
	return w.wantSaved(strings.NewReplacer(
		"  max_turns: 40\n", "  max_turns: 42\n",
		"agent:\n  model: local/qwen\n", "agent:\n  model: spare/tiny\n",
	).Replace(w.initial))
}

func (w *configRoundTripWorld) wantUnchanged() error {
	return w.wantSaved(w.initial)
}

func (w *configRoundTripWorld) wantOneLineChanged(before, after string) error {
	if strings.Count(w.initial, before) != 1 {
		return fmt.Errorf("the fixture must carry %q exactly once", before)
	}
	return w.wantSaved(strings.Replace(w.initial, before, after, 1))
}

func (w *configRoundTripWorld) wantSaved(want string) error {
	if w.saved == want {
		return nil
	}
	return fmt.Errorf("config.yaml after the save:\n%s\nwant:\n%s", w.saved, want)
}

func TestConfigSaveRoundTripFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "config_save_round_trip",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &configRoundTripWorld{}
			sc.Step(`^a coddy server whose config\.yaml spells its paths with \$\{CODDY_HOME\}, \$\{CWD\}, ~ and \$\{VAR\}$`, func() error {
				return w.startSpelledGateway(t)
			})
			sc.Step(`^the settings screen saves the config without changing anything$`, w.saveUnchanged)
			sc.Step(`^the settings screen saves the config with "([^"]*)" set to (\d+)$`, w.saveWithField)
			sc.Step(`^a second browser has read the config$`, w.secondReads)
			sc.Step(`^the second browser saves what it read with "([^"]*)" set to "([^"]*)"$`, w.secondSaves)
			sc.Step(`^config\.yaml carries both saves and is otherwise what it was$`, w.wantBothSaves)
			sc.Step(`^config\.yaml is byte for byte what it was$`, w.wantUnchanged)
			sc.Step(`^config\.yaml is what it was with "([^"]*)" written as "([^"]*)"$`, w.wantOneLineChanged)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/config_save_round_trip.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("config_save_round_trip feature failed")
	}
}

// One live configuration keeps one revision; a revision this process never issued -
// from before a restart, or made up - names nothing, and neither does one older than
// the configurations kept, so the save falls back to the live configuration.
func TestServedConfigsRevisions(t *testing.T) {
	served := newServedConfigs()
	first := &config.Config{}
	rev := served.revision(first)
	if again := served.revision(first); again != rev {
		t.Fatalf("one configuration got two revisions: %q and %q", rev, again)
	}
	if got := served.lookup(rev); got != first {
		t.Fatalf("lookup(%q) = %p, want the configuration served under it", rev, got)
	}
	if got := newServedConfigs().lookup(rev); got != nil {
		t.Fatalf("another process resolved %q", rev)
	}
	for _, unknown := range []string{"", "made-up", rev + "0"} {
		if got := served.lookup(unknown); got != nil {
			t.Fatalf("lookup(%q) resolved to %p", unknown, got)
		}
	}
	for i := 0; i < servedConfigsKept; i++ {
		served.revision(&config.Config{})
	}
	if got := served.lookup(rev); got != nil {
		t.Fatalf("a revision older than the %d kept still resolves", servedConfigsKept)
	}
}
