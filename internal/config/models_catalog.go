package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ModelsCatalogDefaultURL is the catalog of model metadata the settings
// screen suggests a model's context window, output limit and image support
// from (models.dev).
const ModelsCatalogDefaultURL = "https://models.dev/api.json"

// ModelsCatalogConfig is the YAML models_catalog section: where the settings
// screen reads the metadata it suggests for models[] rows. The catalog only
// suggests values the operator writes into models[]; a session never reads it.
type ModelsCatalogConfig struct {
	// Enabled lets the server download the catalog. A nil pointer means the
	// default, true; false makes no request to it at all.
	Enabled *bool `yaml:"enable"`
	// URL is the address of the catalog: models.dev's api.json or a mirror of
	// it in the same shape. Empty falls back to ModelsCatalogDefaultURL.
	URL string `yaml:"url"`
	// Proxy is how the server reaches the catalog, read like providers[].proxy
	// (ParseProxySetting): empty or "inherit" follows the environment's
	// proxy, "none" connects directly, a URL goes through that proxy.
	Proxy string `yaml:"proxy"`
}

// IsEnabled reports whether the catalog may be downloaded. Unset means true.
func (m *ModelsCatalogConfig) IsEnabled() bool {
	return m == nil || m.Enabled == nil || *m.Enabled
}

// EffectiveURL is the address the catalog is read from.
func (m *ModelsCatalogConfig) EffectiveURL() string {
	if m != nil {
		if u := strings.TrimSpace(m.URL); u != "" {
			return u
		}
	}
	return ModelsCatalogDefaultURL
}

// Normalize trims the string fields and spells the proxy keywords in lower case.
func (m *ModelsCatalogConfig) Normalize() {
	if m == nil {
		return
	}
	m.URL = strings.TrimSpace(m.URL)
	m.Proxy = normalizeProxySetting(m.Proxy)
}

// Validate refuses a url that is not an http or https address with a host
// and a proxy setting ParseProxySetting refuses.
func (m *ModelsCatalogConfig) Validate() error {
	if m == nil {
		return nil
	}
	if m.URL != "" {
		u, err := url.Parse(m.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("models_catalog.url: must be an http or https address with a host")
		}
	}
	if err := validateProxySetting(m.Proxy); err != nil {
		return fmt.Errorf("models_catalog.%w", err)
	}
	return nil
}
