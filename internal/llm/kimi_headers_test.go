package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func TestKimiIdentityHeaders(t *testing.T) {
	const device = "12345678-1234-4123-8123-123456789abc"
	for _, id := range []string{"", device} {
		t.Run(id, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.test/path", nil)
			req.Header.Set("Authorization", "Bearer synthetic")
			req.Header.Set("Proxy-Authorization", "synthetic-proxy")
			if err := setKimiIdentityHeaders(req, id); err != nil {
				t.Fatal(err)
			}
			got := req.Header.Get("X-Msh-Device-Id")
			if !kimiDeviceIDPattern.MatchString(got) || (id != "" && got != id) {
				t.Fatalf("device ID %q", got)
			}
			if req.UserAgent() != "coddy/"+version.Get() || req.Header.Get("X-Msh-Platform") != "kimi_code_cli" || req.Header.Get("X-Msh-Version") != version.Get() {
				t.Fatalf("identity headers: %v", req.Header)
			}
			if req.Header.Get("Authorization") != "Bearer synthetic" || req.Header.Get("Proxy-Authorization") != "synthetic-proxy" || req.Host != "example.test" || req.URL.Host != "example.test" {
				t.Fatal("request security fields changed")
			}
			if err := setKimiIdentityHeaders(req, id); err != nil || req.Header.Get("X-Msh-Device-Id") != got {
				t.Fatal("device identity changed")
			}
		})
	}
}

func TestKimiIdentityHeadersRejectInvalidDevice(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	for _, id := range []string{"invalid", "12345678-1234-4123-8123-123456789abc\r\nAuthorization: bad"} {
		if err := setKimiIdentityHeaders(req, id); err == nil {
			t.Fatalf("accepted %q", id)
		}
		if len(req.Header) != 0 {
			t.Fatalf("headers modified on error: %v", req.Header)
		}
	}
}
