package config

import (
	"strings"
	"testing"
)

func TestModelsCatalogDefaults(t *testing.T) {
	var unset ModelsCatalogConfig
	if !unset.IsEnabled() || unset.EffectiveURL() != ModelsCatalogDefaultURL {
		t.Fatalf("an absent section: enabled %v, url %q; want on, %q", unset.IsEnabled(), unset.EffectiveURL(), ModelsCatalogDefaultURL)
	}
	off := false
	mirror := ModelsCatalogConfig{Enabled: &off, URL: " https://mirror.example/api.json "}
	mirror.Normalize()
	if mirror.IsEnabled() || mirror.EffectiveURL() != "https://mirror.example/api.json" {
		t.Fatalf("enable: false with a mirror: enabled %v, url %q", mirror.IsEnabled(), mirror.EffectiveURL())
	}
}

func TestModelsCatalogValidate(t *testing.T) {
	cases := []struct {
		name, url, proxy, wantErr string
	}{
		{name: "defaults"},
		{name: "a mirror", url: "http://10.0.0.5:8080/api.json"},
		{name: "proxy keywords", proxy: "NONE"},
		{name: "a socks proxy", proxy: "socks5h://127.0.0.1:1080"},
		{name: "a file url", url: "file:///etc/passwd", wantErr: "models_catalog.url"},
		{name: "no scheme", url: "models.dev/api.json", wantErr: "models_catalog.url"},
		{name: "no host", url: "https:///api.json", wantErr: "models_catalog.url"},
		{name: "a bare word for the proxy", proxy: "direct", wantErr: "models_catalog.proxy"},
		{name: "an ftp proxy", proxy: "ftp://proxy:21", wantErr: "models_catalog.proxy"},
	}
	for _, tc := range cases {
		m := ModelsCatalogConfig{URL: tc.url, Proxy: tc.proxy}
		m.Normalize()
		err := m.Validate()
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.wantErr)):
			t.Errorf("%s: err = %v, want one naming %s", tc.name, err, tc.wantErr)
		}
	}
}

func TestModelsCatalogFromYAML(t *testing.T) {
	cfg, err := parseValidateYAMLBytes(`
models_catalog:
  enable: false
  url: https://mirror.example/api.json
  proxy: None
`, Paths{Home: t.TempDir(), CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.ModelsCatalog
	if m.IsEnabled() || m.EffectiveURL() != "https://mirror.example/api.json" || m.Proxy != ProxyNone {
		t.Fatalf("models_catalog = %+v", m)
	}
	if _, err := parseValidateYAMLBytes("models_catalog:\n  url: ftp://mirror.example/api.json\n", Paths{Home: t.TempDir(), CWD: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "models_catalog.url") {
		t.Fatalf("an ftp url loaded: %v", err)
	}
}

// The settings document carries the section both ways, and an absent enable
// stays absent rather than turning into an explicit false.
func TestModelsCatalogJSONRoundTrip(t *testing.T) {
	off := false
	for _, in := range []ModelsCatalogConfig{
		{},
		{Enabled: &off, URL: "https://mirror.example/api.json", Proxy: "none"},
	} {
		cfg := &Config{ModelsCatalog: in}
		back := ConfigToJSONDTO(cfg)
		got := JSONDTOToConfig(back, Paths{}).ModelsCatalog
		if (got.Enabled == nil) != (in.Enabled == nil) || got.IsEnabled() != in.IsEnabled() || got.URL != in.URL || got.Proxy != in.Proxy {
			t.Errorf("round trip of %+v gave %+v", in, got)
		}
	}
}
