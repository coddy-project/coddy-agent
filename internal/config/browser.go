package config

import (
	"fmt"
	"strings"
)

// BrowserConfig turns on Coddy for Browser. With a token, the extension's
// endpoint on the HTTP server accepts browsers (their socket and file
// transfers) and the browser tool of this process drives them; with
// serve_remote, other Coddy processes drive them too, through
// /coddy/browser/v1; with remotes, this process's sessions also reach the
// browsers of other Coddy servers. The token is the extension key, independent
// of the HTTP API's credentials; the side panel's web UI is signed in with a
// one-time ticket the endpoint issues for that key, so whoever holds it has
// the web UI of this server too.
type BrowserConfig struct {
	Enable      bool     `yaml:"enable" json:"enable"`
	Token       string   `yaml:"token" json:"token,omitempty"`
	ServeRemote bool     `yaml:"serve_remote" json:"serve_remote,omitempty"`
	Remotes     []string `yaml:"remotes" json:"remotes,omitempty"`
}

// Validate checks the block alone; that the HTTP server the endpoint needs is
// on is checked with the whole configuration (validateSubconfigs).
func (b *BrowserConfig) Validate() error {
	if !b.Enable {
		return nil
	}
	for i := range b.Remotes {
		b.Remotes[i] = strings.TrimSpace(b.Remotes[i])
		if b.Remotes[i] == "" {
			return fmt.Errorf("browser.remotes[%d] is empty: name a remote of httpserver.remotes or a server address", i)
		}
	}
	token := strings.TrimSpace(b.Token)
	switch {
	case token == "" && len(b.Remotes) == 0:
		return fmt.Errorf("browser.enable needs browser.token, the extension key of this server, or browser.remotes, the servers whose browsers this one uses")
	case token != "" && len(token) < 32:
		return fmt.Errorf("browser.token must contain at least 32 characters")
	case token == "" && b.ServeRemote:
		return fmt.Errorf("browser.serve_remote needs browser.token: only a server the extension connects to has browsers to serve")
	}
	return nil
}

// Serves reports whether this configuration accepts the extension: the
// module on, with a key.
func (b *BrowserConfig) Serves() bool {
	return b.Enable && strings.TrimSpace(b.Token) != ""
}
