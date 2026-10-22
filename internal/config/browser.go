package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// BrowserConfig controls the extension listener. The token never grants access
// to settings, workspace files or the main HTTP API.
type BrowserConfig struct {
	Enable   bool   `yaml:"enable" json:"enable"`
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	Token    string `yaml:"token" json:"token,omitempty"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

func (b *BrowserConfig) ListenAddr() string {
	host, port := strings.TrimSpace(b.Host), b.Port
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = 12348
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func (b *BrowserConfig) Validate() error {
	if b.Port < 0 || b.Port > 65535 {
		return fmt.Errorf("browser.port %d is out of range", b.Port)
	}
	if (b.CertFile == "") != (b.KeyFile == "") {
		return fmt.Errorf("browser.cert_file and browser.key_file must be set together")
	}
	if b.Enable && len(strings.TrimSpace(b.Token)) < 32 {
		return fmt.Errorf("browser.token must contain at least 32 characters when browser.enable is true")
	}
	return nil
}
