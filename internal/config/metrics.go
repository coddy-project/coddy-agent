package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Defaults for the Prometheus exporter.
const (
	// MetricsDefaultHost keeps the exporter on loopback until the operator
	// binds it elsewhere.
	MetricsDefaultHost = "127.0.0.1"
	// MetricsDefaultPort sits next to the HTTP API's 12345 and the swarm
	// relay's 12346.
	MetricsDefaultPort = 12347
)

// MetricsConfig is the YAML metrics section: the exporter coddy serve runs
// when the binary is built with the metrics tag.
type MetricsConfig struct {
	// Enable opens the exporter's listener.
	Enable bool `yaml:"enable"`
	// Host is the bind address. Empty falls back to 127.0.0.1.
	Host string `yaml:"host"`
	// Port is the listen port. Zero falls back to 12347.
	Port int `yaml:"port"`
	// Token, when set, is the bearer credential a scrape must present.
	Token string `yaml:"token"`
}

// ListenAddr resolves the address the exporter binds.
func (m *MetricsConfig) ListenAddr() string {
	host, port := MetricsDefaultHost, MetricsDefaultPort
	if m != nil {
		if h := strings.TrimSpace(m.Host); h != "" {
			host = h
		}
		if m.Port > 0 {
			port = m.Port
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// ExposedWithoutToken reports an exporter bound off loopback with no token:
// whoever can reach the port reads its figures.
func (m *MetricsConfig) ExposedWithoutToken() bool {
	if m == nil || m.Token != "" {
		return false
	}
	host, _, err := net.SplitHostPort(m.ListenAddr())
	return err != nil || !isLoopbackHostname(host)
}

// Validate refuses a port outside the range of a TCP port, as
// httpserver.port and swarm.port are refused: the fallback to 12347 is for
// zero, not for a value nobody can bind.
func (m *MetricsConfig) Validate() error {
	if m != nil && (m.Port < 0 || m.Port > 65535) {
		return fmt.Errorf("metrics.port %d is out of range", m.Port)
	}
	return nil
}

// Normalize trims the string fields that later comparisons depend on.
func (m *MetricsConfig) Normalize() {
	if m == nil {
		return
	}
	m.Host = strings.TrimSpace(m.Host)
	m.Token = strings.TrimSpace(m.Token)
}
