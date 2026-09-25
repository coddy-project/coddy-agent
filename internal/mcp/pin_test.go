package mcp

// Tests of the npx version pinning (pin.go): the detection table, the
// registry lookup against an httptest stand-in, the argument rewrite, the
// unresolved report and the wording of the messages.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestFindUnpinnedNPX(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
		want    string // the package, "" when nothing should be pinned
		index   int
	}{
		{"scoped without version", "npx", []string{"-y", "@modelcontextprotocol/server-github"}, "@modelcontextprotocol/server-github", 1},
		{"plain without version", "npx", []string{"-y", "mcp-server-docker"}, "mcp-server-docker", 1},
		{"latest tag counts as unpinned", "npx", []string{"-y", "mcp-server-docker@latest"}, "mcp-server-docker", 1},
		{"--yes spelling", "npx", []string{"--yes", "@playwright/mcp"}, "@playwright/mcp", 1},
		{"package after a value flag", "npx", []string{"-y", "--registry", "https://r.example", "@playwright/mcp"}, "@playwright/mcp", 3},
		{"npx.cmd on Windows", "npx.cmd", []string{"-y", "@playwright/mcp"}, "@playwright/mcp", 1},
		{"full path to npx", "/usr/local/bin/npx", []string{"-y", "@playwright/mcp"}, "@playwright/mcp", 1},
		{"case of the command", "NPX", []string{"-y", "@playwright/mcp"}, "@playwright/mcp", 1},
		{"pinned scoped", "npx", []string{"-y", "@modelcontextprotocol/server-github@2026.9.1"}, "", 0},
		{"pinned plain", "npx", []string{"-y", "mcp-server-docker@1.2.3"}, "", 0},
		{"a range is left alone", "npx", []string{"-y", "mcp-server-docker@^1.2.0"}, "", 0},
		{"another tag is left alone", "npx", []string{"-y", "mcp-server-docker@next"}, "", 0},
		{"without -y npx would prompt", "npx", []string{"mcp-server-docker"}, "", 0},
		{"not npx", "node", []string{"-y", "mcp-server-docker"}, "", 0},
		{"unknown flag ends the scan", "npx", []string{"-y", "--weird", "mcp-server-docker"}, "", 0},
		{"--package names the package apart from the command", "npx", []string{"-y", "--package", "some-package", "some-command"}, "", 0},
		{"-p spelling", "npx", []string{"-y", "-p", "some-package", "some-command"}, "", 0},
		{"--package=value spelling", "npx", []string{"-y", "--package=some-package", "some-command"}, "", 0},
		{"several packages", "npx", []string{"-y", "-p", "one", "-p", "two", "some-command"}, "", 0},
		{"--call runs a script, not a package", "npx", []string{"-y", "--call", "some-command", "pkg"}, "", 0},
		{"a value flag with = keeps the positional", "npx", []string{"-y", "--registry=https://r.example", "@playwright/mcp"}, "@playwright/mcp", 2},
		{"a path is not a package", "npx", []string{"-y", "./local-server"}, "", 0},
		{"a git spec is not a package", "npx", []string{"-y", "github:owner/repo"}, "", 0},
		{"a url is not a package", "npx", []string{"-y", "https://example.com/server.tgz"}, "", 0},
		{"a tarball is not a package", "npx", []string{"-y", "server.tgz"}, "", 0},
		{"a placeholder is left alone", "npx", []string{"-y", "${MCP_PACKAGE}"}, "", 0},
		{"no arguments", "npx", nil, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := FindUnpinnedNPX(tc.command, tc.args)
			if tc.want == "" {
				if ok {
					t.Fatalf("found %+v, want nothing", spec)
				}
				return
			}
			if !ok || spec.Name != tc.want || spec.ArgIndex != tc.index {
				t.Fatalf("spec = %+v (ok %v), want %s at %d", spec, ok, tc.want, tc.index)
			}
		})
	}
}

// registryStub answers GET /<package>/latest from a table.
func registryStub(t *testing.T, versions map[string]string, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var seen string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.EscapedPath()
		if body != "" {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/latest")
		v, ok := versions[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"` + name + `","version":"` + v + `"}`))
	}))
	t.Cleanup(ts.Close)
	return ts, &seen
}

func TestResolverLatestEscapesTheScope(t *testing.T) {
	ts, seen := registryStub(t, map[string]string{"@upstash/context7-mcp": "1.0.14"}, 0, "")
	r := &Resolver{Registry: ts.URL + "/", Timeout: 2 * time.Second}
	got, err := r.Latest(context.Background(), "@upstash/context7-mcp")
	if err != nil || got != "1.0.14" {
		t.Fatalf("Latest = %q, %v; want 1.0.14", got, err)
	}
	if !strings.EqualFold(*seen, "/@upstash%2Fcontext7-mcp/latest") {
		t.Fatalf("registry path = %q, want the scope's slash escaped", *seen)
	}
}

func TestResolverLatestRefusesWhatIsNotAVersion(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"not found", http.StatusNotFound, `{"error":"Not found"}`},
		{"bad json", http.StatusOK, `{"version":`},
		{"a tag instead of a version", http.StatusOK, `{"version":"latest"}`},
		{"a range instead of a version", http.StatusOK, `{"version":"^1.0.0"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, _ := registryStub(t, nil, tc.status, tc.body)
			r := &Resolver{Registry: ts.URL, Timeout: 2 * time.Second}
			if got, err := r.Latest(context.Background(), "pkg"); err == nil {
				t.Fatalf("Latest = %q, want an error", got)
			}
		})
	}
}

func TestResolverLatestGivesUpOnASilentRegistry(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)
	r := &Resolver{Registry: ts.URL, Timeout: 200 * time.Millisecond}
	started := time.Now()
	if _, err := r.Latest(context.Background(), "pkg"); err == nil {
		t.Fatal("Latest returned a version from a registry that never answered")
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("Latest waited %s on a silent registry", took)
	}
}

func TestPinNPXArgsRewritesOnlyTheSpec(t *testing.T) {
	ts, _ := registryStub(t, map[string]string{"@modelcontextprotocol/server-github": "2026.9.1"}, 0, "")
	r := &Resolver{Registry: ts.URL, Timeout: 2 * time.Second}
	args, res := PinNPXArgs(context.Background(), r, "github", "npx", []string{"-y", "@modelcontextprotocol/server-github", "--stdio"})
	if res == nil || !res.Pinned || res.Version != "2026.9.1" || res.Package != "@modelcontextprotocol/server-github" || res.Server != "github" {
		t.Fatalf("result = %+v, want pinned 2026.9.1", res)
	}
	if want := []string{"-y", "@modelcontextprotocol/server-github@2026.9.1", "--stdio"}; strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for _, phrase := range []string{"Pinned @modelcontextprotocol/server-github to 2026.9.1", "asks the npm registry for the latest release on every start", "first frame", "To update"} {
		if !strings.Contains(res.Message, phrase) {
			t.Fatalf("message lacks %q:\n%s", phrase, res.Message)
		}
	}
}

func TestPinNPXArgsReportsAnUnresolvedPackage(t *testing.T) {
	ts, _ := registryStub(t, nil, 0, "")
	r := &Resolver{Registry: ts.URL, Timeout: 2 * time.Second}
	args, res := PinNPXArgs(context.Background(), r, "docker", "npx", []string{"-y", "mcp-server-docker"})
	if args != nil {
		t.Fatalf("args = %v, want nil when the version could not be read", args)
	}
	if res == nil || res.Pinned || res.Package != "mcp-server-docker" {
		t.Fatalf("result = %+v, want an unpinned report", res)
	}
	for _, phrase := range []string{"Could not read the current version of mcp-server-docker", "saved unpinned", "mcp-server-docker@<version>"} {
		if !strings.Contains(res.Message, phrase) {
			t.Fatalf("message lacks %q:\n%s", phrase, res.Message)
		}
	}
}

func TestPinNPXArgsUsesTheCommandRegistry(t *testing.T) {
	for _, name := range []string{"split", "equals", "last wins", "server argument"} {
		t.Run(name, func(t *testing.T) {
			fallback, fallbackSeen := registryStub(t, map[string]string{"pkg": "1.0.0"}, 0, "")
			custom, customSeen := registryStub(t, map[string]string{"pkg": "2.0.0"}, 0, "")
			resolver := &Resolver{Registry: fallback.URL, Client: custom.Client(), Timeout: time.Second}
			before := *resolver
			args := []string{"-y", "--registry", custom.URL, "pkg", "--stdio"}
			wantVersion := "2.0.0"
			switch name {
			case "equals":
				args = []string{"-y", "--registry=" + custom.URL, "pkg", "--stdio"}
			case "last wins":
				args = []string{"-y", "--registry", fallback.URL, "--registry=" + custom.URL, "pkg"}
			case "server argument":
				args = []string{"-y", "pkg", "--registry", custom.URL}
				wantVersion = "1.0.0"
			}
			original := strings.Join(args, " ")
			pinned, result := PinNPXArgs(context.Background(), resolver, "example", "npx", args)
			if result == nil || !result.Pinned || result.Version != wantVersion {
				t.Fatalf("result = %+v, want version %s", result, wantVersion)
			}
			want := strings.Replace(original, "pkg", "pkg@"+wantVersion, 1)
			if got := strings.Join(pinned, " "); got != want {
				t.Fatalf("args = %q, want %q", got, want)
			}
			if *resolver != before || strings.Join(args, " ") != original {
				t.Fatal("pinning modified the shared resolver or input arguments")
			}
			if wantVersion == "2.0.0" && (*customSeen == "" || *fallbackSeen != "") {
				t.Fatalf("registry requests: custom %q, fallback %q", *customSeen, *fallbackSeen)
			}
			if wantVersion == "1.0.0" && (*fallbackSeen == "" || *customSeen != "") {
				t.Fatalf("server argument changed the registry: custom %q, fallback %q", *customSeen, *fallbackSeen)
			}
		})
	}
}

func TestPinNPXArgsDoesNotFallBackFromAnExplicitRegistry(t *testing.T) {
	for _, name := range []string{"missing package", "empty", "placeholder"} {
		t.Run(name, func(t *testing.T) {
			fallback, seen := registryStub(t, map[string]string{"pkg": "1.0.0"}, 0, "")
			custom, _ := registryStub(t, nil, 0, "")
			registry := custom.URL
			switch name {
			case "empty":
				registry = ""
			case "placeholder":
				registry = "${NPM_REGISTRY}"
			}
			args, result := PinNPXArgs(context.Background(), &Resolver{Registry: fallback.URL}, "example", "npx", []string{"-y", "--registry=" + registry, "pkg"})
			if args != nil || result == nil || result.Pinned || !strings.Contains(result.Message, "saved unpinned") {
				t.Fatalf("args = %v, result = %+v; want an unresolved pin", args, result)
			}
			if *seen != "" {
				t.Fatalf("queried the fallback registry at %q", *seen)
			}
		})
	}
}

func TestPinNPXArgsLeavesOtherServersAlone(t *testing.T) {
	ts, seen := registryStub(t, map[string]string{"pkg": "1.0.0"}, 0, "")
	r := &Resolver{Registry: ts.URL, Timeout: 2 * time.Second}
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"node", []string{"server.js"}},
		{"npx", []string{"-y", "pkg@1.0.0"}},
		{"npx", []string{"pkg"}},
	} {
		if args, res := PinNPXArgs(context.Background(), r, "s", tc.command, tc.args); args != nil || res != nil {
			t.Fatalf("%s %v: got %v %+v, want nothing", tc.command, tc.args, args, res)
		}
	}
	if *seen != "" {
		t.Fatalf("the registry was asked (%s) for a server that needs no pin", *seen)
	}
	if args, res := PinNPXArgs(context.Background(), nil, "s", "npx", []string{"-y", "pkg"}); args != nil || res != nil {
		t.Fatalf("a nil resolver pinned: %v %+v", args, res)
	}
}

func TestUnpinnedHintNamesThePackage(t *testing.T) {
	hint := UnpinnedHint(config.MCPServerConfig{Command: "npx", Args: []string{"-y", "@playwright/mcp"}})
	for _, phrase := range []string{"npx -y @playwright/mcp has no version", "@playwright/mcp@<version>"} {
		if !strings.Contains(hint, phrase) {
			t.Fatalf("hint lacks %q: %s", phrase, hint)
		}
	}
	if hint := UnpinnedHint(config.MCPServerConfig{Command: "npx", Args: []string{"-y", "@playwright/mcp@0.1.0"}}); hint != "" {
		t.Fatalf("a pinned server got a hint: %s", hint)
	}
}

func TestDefaultResolverReadsNPMsRegistryVariable(t *testing.T) {
	t.Setenv("NPM_CONFIG_REGISTRY", "")
	t.Setenv("npm_config_registry", "https://registry.example.test/")
	if got := DefaultResolver().Registry; got != "https://registry.example.test/" {
		t.Fatalf("registry = %q, want npm_config_registry", got)
	}
	t.Setenv("npm_config_registry", "")
	if got := DefaultResolver().Registry; got != DefaultNPMRegistry {
		t.Fatalf("registry = %q, want the public registry", got)
	}
}
