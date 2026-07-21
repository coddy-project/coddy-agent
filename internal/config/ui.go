package config

// UIConfig controls the embedded single-page web UI (only meaningful in binaries built with
// -tags http,ui). It lets an operator run an API-only server (e.g. a remote box) without serving
// the SPA, while the OpenAI-compatible and /coddy/* API keep working (and keep requiring the
// bearer token when httpserver.auth_token is set).
type UIConfig struct {
	// Enabled toggles serving the embedded SPA at GET /. A nil pointer means the default (true),
	// so existing configs and builds are unchanged; set `ui.enabled: false` to disable it.
	Enabled *bool `yaml:"enabled"`
}

// IsEnabled reports whether the embedded SPA should be served. It defaults to true when unset.
func (u *UIConfig) IsEnabled() bool {
	return u.Enabled == nil || *u.Enabled
}
