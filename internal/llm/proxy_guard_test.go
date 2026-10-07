package llm

// The transport guard: no provider code path may reach the network around the
// proxy machinery of providers[].proxy. These shapes are the only ways Go
// code can skip the row's route for it:
//
//   - http.DefaultClient, which follows the environment and nothing else;
//   - a bare &http.Client{}, which silently does the same as DefaultClient;
//   - the package-level http.Get / http.Post / http.Head helpers, which ride
//     DefaultClient too.
//
// A call that hands a provider client to a helper must instead take the row's
// proxy setting and build the client through HTTPClientForProviderProxy, or
// hold one built by providerHTTPClient for the SDK providers. The scan keeps
// that contract against silent reintroductions: it runs over every
// non-test source of this package and the spots elsewhere that used to hand
// clients to it (the sign-in HTTP handlers and the CLI login commands).
// Adding another "it is just a client argument" escape hatch fails this test
// with file and line.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var providerProxyGuardPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"http.DefaultClient", regexp.MustCompile(`\bhttp\.DefaultClient\b`)},
	{"empty http.Client literal", regexp.MustCompile(`&http\.Client\{\}`)},
	{"http.Get", regexp.MustCompile(`\bhttp\.Get\(`)},
	{"http.Post", regexp.MustCompile(`\bhttp\.Post\(`)},
	{"http.Head", regexp.MustCompile(`\bhttp\.Head\(`)},
}

// providerProxyGuardScopes are the directories and file selectors the scan
// covers. internal/llm is where provider I/O lives; the sign-in handlers in
// external/httpserver and the login commands in cmd/coddy are the places that
// used to decide which client the provider sees.
var providerProxyGuardScopes = []struct {
	dir     string
	include func(name string) bool
}{
	{".", func(name string) bool {
		return strings.HasSuffix(name, ".go") && name != "transport.go" && name != "proxy_http_client.go"
	}},
	{"../../external/httpserver", func(name string) bool {
		return strings.HasSuffix(name, "_auth_http.go") || name == "providers_models_http.go"
	}},
	{"../../cmd/coddy", func(name string) bool {
		return name == "codex.go" || name == "devin.go" || name == "providers.go"
	}},
}

func TestProviderProxyGuard(t *testing.T) {
	var hits []string
	for _, scope := range providerProxyGuardScopes {
		entries, err := os.ReadDir(scope.dir)
		if err != nil {
			t.Fatalf("read %s: %v", scope.dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasSuffix(name, "_test.go") || !scope.include(name) {
				continue
			}
			path := filepath.Join(scope.dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for lineNo, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				for _, p := range providerProxyGuardPatterns {
					if p.re.MatchString(line) {
						hits = append(hits, path+":"+itoa(lineNo+1)+": "+p.name)
					}
				}
			}
		}
	}
	if len(hits) > 0 {
		t.Fatalf("provider requests must go through the row's proxy setting; forbidden client shapes found:\n%s",
			strings.Join(hits, "\n"))
	}
}

// itoa is a tiny int formatter that avoids importing strconv for one use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
