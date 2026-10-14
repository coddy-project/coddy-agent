package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedTLSMigration(t *testing.T) {
	cases := []struct{ body, path string }{
		{"tls: null\n", "tls"}, {"httpserver: {tls: {}}\n", "httpserver.tls"},
		{"swarm: {node_tls: null}\n", "swarm.node_tls"},
		{"swarm: {tls: {auto: false}}\n", "swarm.tls.auto"},
		{"swarm: {tls: {client_ca_file: ''}}\n", "swarm.tls.client_ca_file"},
		{"swarm: {tls: {require_client_cert: false}}\n", "swarm.tls.require_client_cert"},
		{"providers: [{name: remote, type: coddy, tls_auto: false}]\n", "providers[0].tls_auto"},
		{"providers: [{ca_file: ''}]\n", "providers[0].ca_file"},
		{"providers: [{client_cert_file: null}]\n", "providers[0].client_cert_file"},
		{"providers: [{client_key_file: ''}]\n", "providers[0].client_key_file"},
		{"swarm: {join: [{dial: {auto: false}}]}\n", "swarm.join[0].dial.auto"},
		{"swarm: {join: [{dial: {cert_file: ''}}]}\n", "swarm.join[0].dial.cert_file"},
		{"swarm: {join: [{dial: {key_file: null}}]}\n", "swarm.join[0].dial.key_file"},
		{"swarm: {upstreams: [{dial: {insecure_skip_verify: false}}]}\n", "swarm.upstreams[0].dial.insecure_skip_verify"},
		{"defaults: &old {tls: null}\nhttpserver: *old\n", "httpserver.tls"},
		{"defaults: &old {ca_file: ''}\nswarm: {join: [{dial: {<<: *old, proxy: none}}]}\n", "swarm.join[0].dial.ca_file"},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			dir := t.TempDir()
			p := Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
			if err := os.WriteFile(p.ConfigPath, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := WriteBackup(p.ConfigPath, []byte("{}\n")); err != nil {
				t.Fatal(err)
			}
			for _, load := range []func() error{
				func() error { _, err := LoadWithPaths(p); return err },
				func() error { _, _, err := LoadReadOnly(CLIPaths{Home: dir, Config: p.ConfigPath}); return err },
				func() error { _, err := parseValidateYAMLBytes(tc.body, p); return err },
			} {
				err := load()
				if err == nil || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), "reverse proxy") {
					t.Errorf("expected located migration for %s, got %v", tc.path, err)
				}
			}
			found := false
			for _, f := range checkConfigBytes([]byte(tc.body), p) {
				if f.Severity == SeverityError && f.Path == tc.path && f.Line > 0 && strings.Contains(f.Fix, "reverse proxy") {
					found = true
				}
			}
			if !found {
				t.Error("check missing located migration finding")
			}
			raw, _ := os.ReadFile(p.ConfigPath)
			if string(raw) != tc.body {
				t.Fatal("migration rewrote active file")
			}
		})
	}
}

func TestRemovedTLSExplicitCommit(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	if err := os.WriteFile(p.ConfigPath, []byte("httpserver: {tls: null}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitUCICommands(p, []UCICommand{{Op: UCIOpSet, Path: "agent.max_turns", Value: "3"}}); err == nil || !strings.Contains(err.Error(), "reverse proxy") {
		t.Fatalf("commit bypassed migration: %v", err)
	}
	if _, err := CommitUCICommands(p, []UCICommand{{Op: UCIOpDelete, Path: "httpserver.tls"}}); err != nil {
		t.Fatalf("explicit migration refused: %v", err)
	}
	if _, err := LoadWithPaths(p); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(PrevConfigPath(p.ConfigPath)); err != nil || string(raw) != "httpserver: {tls: null}\n" {
		t.Fatalf("previous document was not preserved: %q %v", raw, err)
	}
	if _, err := RollbackConfigFromSnapshot(p); err == nil || !strings.Contains(err.Error(), "reverse proxy") {
		t.Fatalf("rollback bypassed explicit migration: %v", err)
	}
}

func TestRemovedTLSJSON(t *testing.T) {
	for _, body := range []string{
		`{"tls":null}`,
		`{"httpserver":{"auth_token":"\ud83d\ude00","tls":null}}`,
		`{"providers":[{"name":"\ud800","TLS_AUTO":false}]}`,
		`{"providers":[{"name":"\ud83d\ude00","tls_auto":false}],"providers":[]}`,
		`{"HTTPServer":{"tls":{}}}`,
		`{"Swarm":{"TLS":{"AUTO":false}}}`,
		`{"swarm":{"tls":{"client_ca_file":""}}}`,
		`{"swarm":{"tls":{"require_client_cert":false}}}`,
		`{"providers":[{"TLS_AUTO":false}]}`,
		`{"providers":[{"\u0074\u006c\u0073_\u0061\u0075\u0074\u006f":false}]}`,
		`{"swarm":{"join":[{"dial":{"ca_file":""}}]}}`,
	} {
		_, err := ParseConfigJSONPreservingSecrets([]byte(body), Paths{}, nil)
		if err == nil || !strings.Contains(err.Error(), "reverse proxy") {
			t.Errorf("migration missing for %s: %v", body, err)
		}
	}
}

func TestRemovedTLSYAMLKeysRemainCaseSensitive(t *testing.T) {
	if err := ValidateRemovedTLSKeys([]byte("HTTPServer: {tls: null}\n")); err != nil {
		t.Fatalf("YAML key case changed migration behavior: %v", err)
	}
}

func TestCheckReportsTLSMigrationAlongsideSchemaErrors(t *testing.T) {
	findings := checkConfigBytes([]byte("httpserver: {tls: null}\nbogus: true\n"), Paths{})
	var migration, schema bool
	for _, f := range findings {
		if f.Path == "httpserver.tls" && strings.Contains(f.Fix, "reverse proxy") {
			migration = true
		}
		if f.Path == "bogus" && strings.Contains(f.Message, "unknown key") {
			schema = true
		}
	}
	if !migration || !schema {
		t.Fatalf("findings = %#v, want TLS migration and schema errors", findings)
	}
}

func TestRemovedTLSRollbackAndReload(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	if err := os.WriteFile(p.ConfigPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	live, err := LoadWithPaths(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PrevConfigPath(p.ConfigPath), []byte("tls: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RollbackConfigFromSnapshot(p); err == nil || !strings.Contains(err.Error(), "reverse proxy") {
		t.Fatalf("rollback: %v", err)
	}
	if err := os.WriteFile(p.ConfigPath, []byte("httpserver: {tls: null}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	installed := false
	w := FileWatcher{Paths: p, Live: func() *Config { return live }, Install: func(*Config) { installed = true }}
	if w.Poll() || installed {
		t.Fatal("invalid reload replaced active config")
	}
	if err := os.WriteFile(BackupPath(p.ConfigPath), []byte("tls: null\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tryRecoverFromBackup(p); err == nil || !strings.Contains(err.Error(), "reverse proxy") {
		t.Fatalf("obsolete backup accepted: %v", err)
	}
}
