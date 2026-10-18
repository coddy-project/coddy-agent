package configapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const savedYAML = "logger:\n  level: info\n"

// saveFixture is a backend over a config file in a fresh home, its live
// configuration loaded from that file.
func saveFixture(t *testing.T, install func(*config.Config) error, check func(*config.Config) error) (*httptest.Server, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(savedYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := config.LoadWithPaths(config.Paths{Home: home, ConfigPath: cfgPath, CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	b := &Backend{
		Live:    func() *config.Config { return live },
		Install: install,
		Check:   check,
		Schema:  config.UISchemaJSON,
	}
	mux := http.NewServeMux()
	b.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfgPath
}

func send(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A configuration Install refuses after it was written leaves the file and its
// backup as the save found them: the load that read the new file back has
// refreshed the backup with it, so the backup cannot be what is put back.
func TestASaveInstallRefusesPutsTheFileAndTheBackupBack(t *testing.T) {
	ts, cfgPath := saveFixture(t, func(*config.Config) error { return errors.New("refused by install") }, nil)
	code, body := send(t, http.MethodPut, ts.URL+"/coddy/config", `{"logger":{"level":"debug"}}`)
	if code == http.StatusOK || !strings.Contains(body, "refused by install") {
		t.Fatalf("status %d: %s", code, body)
	}
	if got := readFile(t, cfgPath); got != savedYAML {
		t.Fatalf("the file holds the refused configuration:\n%s", got)
	}
	if got := readFile(t, config.BackupPath(cfgPath)); got != savedYAML {
		t.Fatalf("the backup holds the refused configuration:\n%s", got)
	}
}

// Check sees the candidate as the file will load, its references expanded,
// and a refusal is a 400 that writes nothing, for a save and a validation
// alike.
func TestCheckRefusesBeforeTheWriteWithReferencesExpanded(t *testing.T) {
	t.Setenv("CODDY_CONFIGAPI_TOKEN", "expanded-token")
	var seen []string
	check := func(c *config.Config) error {
		tokens := c.HTTPServer.SharedModels.Tokens
		if len(tokens) == 0 {
			return nil
		}
		seen = append(seen, tokens[0])
		if tokens[0] == "expanded-token" {
			return errors.New("that token is refused")
		}
		return nil
	}
	installed := false
	ts, cfgPath := saveFixture(t, func(*config.Config) error { installed = true; return nil }, check)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/coddy/config/validate"},
		{http.MethodPut, "/coddy/config"},
	} {
		code, body := send(t, route.method, ts.URL+route.path, `{"httpserver":{"shared_models":{"tokens":["${CODDY_CONFIGAPI_TOKEN}"]}}}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "that token is refused") {
			t.Fatalf("%s %s: status %d: %s", route.method, route.path, code, body)
		}
	}
	if len(seen) != 2 || seen[0] != "expanded-token" || seen[1] != "expanded-token" {
		t.Fatalf("check saw %q, want the expanded value twice", seen)
	}
	if installed {
		t.Fatal("a refused candidate was installed")
	}
	if got := readFile(t, cfgPath); got != savedYAML {
		t.Fatalf("the file changed:\n%s", got)
	}

	code, body := send(t, http.MethodPut, ts.URL+"/coddy/config", `{"httpserver":{"shared_models":{"tokens":["another-token"]}}}`)
	if code != http.StatusOK || !installed {
		t.Fatalf("a candidate check accepts was not saved: %d %s", code, body)
	}
}
