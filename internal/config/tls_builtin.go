package config

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

// TLSConfig is the top-level `tls` block: what the built-in certificate authority (coddy tls, internal/pki) puts into the server
// certificate beyond what it finds by itself. TLS is the transport's business; nothing here is an identity or a credential.
type TLSConfig struct {
	// Names are extra DNS names and IP addresses for the server certificate, for a host reached by a name Coddy cannot see (a proxy's,
	// a DNS alias, a public address behind NAT). The host name, `localhost`, the loopback addresses, the addresses of the interfaces
	// and the bind and advertise_url hosts of this file are always there.
	Names []string `yaml:"names"`
}

// Normalize trims the names and drops blanks.
func (t *TLSConfig) Normalize() {
	var out []string
	for _, n := range t.Names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	t.Names = out
}

// ListenerFiles are the files a listener serves with, after the built-in mode filled what its block left empty.
type ListenerFiles struct {
	Cert, Key, ClientCA string
}

// builtinPaths are the built-in files of this configuration's home, or the zero value when the configuration has no home to look in.
func (c *Config) builtinPaths() (pki.Paths, bool) {
	if c == nil || strings.TrimSpace(c.Paths.Home) == "" {
		return pki.Paths{}, false
	}
	return pki.PathsIn(pki.Dir(c.Paths.Home)), true
}

func (c *Config) listenerFiles(t SwarmTLSConfig) ListenerFiles {
	f := ListenerFiles{Cert: strings.TrimSpace(t.CertFile), Key: strings.TrimSpace(t.KeyFile), ClientCA: strings.TrimSpace(t.ClientCAFile)}
	if b, ok := c.builtinPaths(); ok && t.Auto {
		if f.Cert == "" && f.Key == "" {
			f.Cert, f.Key = b.ServerCert, b.ServerKey
		}
		if f.ClientCA == "" && t.RequireClientCert {
			f.ClientCA = b.Bundle
		}
	}
	return f
}

// HTTPListenerFiles are the files httpserver.tls serves with.
func (c *Config) HTTPListenerFiles() ListenerFiles { return c.listenerFiles(c.HTTPServer.TLS) }

// SwarmListenerFiles are the files swarm.tls serves with.
func (c *Config) SwarmListenerFiles() ListenerFiles { return c.listenerFiles(c.Swarm.TLS) }

func (c *Config) clientFiles(auto bool, ca, cert, key string) netx.ClientTLS {
	f := netx.ClientTLS{CAFile: strings.TrimSpace(ca), CertFile: strings.TrimSpace(cert), KeyFile: strings.TrimSpace(key)}
	if b, ok := c.builtinPaths(); ok && auto {
		if f.CAFile == "" {
			f.CAFile = b.Bundle
		}
		if f.CertFile == "" && f.KeyFile == "" {
			f.CertFile, f.KeyFile = b.ClientCert, b.ClientKey
		}
	}
	return f
}

// NodeDialFiles are the files the relay uses towards a node that registered itself (swarm.node_tls).
func (c *Config) NodeDialFiles() netx.ClientTLS {
	n := c.Swarm.NodeTLS
	return c.clientFiles(n.Auto, n.CAFile, n.CertFile, n.KeyFile)
}

// DialFiles are the files one leg of the swarm (a join, an upstream) uses, after the built-in mode filled what its block left empty.
func (c *Config) DialFiles(d SwarmDialConfig) netx.ClientTLS {
	return c.clientFiles(d.Auto, d.CAFile, d.CertFile, d.KeyFile)
}

// ProviderClientTLS is the identity of the connections of a provider of type coddy.
func (c *Config) ProviderClientTLS(p *ProviderConfig) netx.ClientTLS {
	return c.clientFiles(p.TLSAuto, p.CAFile, p.ClientCertFile, p.ClientKeyFile)
}

// BuiltinTLSWanted reports whether any block of this configuration asks for the built-in certificates.
func (c *Config) BuiltinTLSWanted() bool {
	if c == nil {
		return false
	}
	if c.HTTPServer.TLS.Auto || c.Swarm.TLS.Auto || c.Swarm.NodeTLS.Auto {
		return true
	}
	for _, j := range c.Swarm.Join {
		if j.Dial.Auto {
			return true
		}
	}
	for _, u := range c.Swarm.Upstreams {
		if u.Dial.Auto {
			return true
		}
	}
	for i := range c.Providers {
		if c.Providers[i].TLSAuto {
			return true
		}
	}
	return false
}

// TLSNames are the extra names this configuration gives the server certificate: the hosts of its own addresses and `tls.names`.
func (c *Config) TLSNames() []string {
	if c == nil {
		return nil
	}
	return append(c.TLSHosts(), c.TLS.Names...)
}

// TLSWant is what this configuration asks the built-in authority for.
func (c *Config) TLSWant(extra ...string) pki.Want {
	host := pki.Hostname()
	return pki.Want{Host: host, Names: pki.DefaultNames(host, nil, c.TLSNames(), extra)}
}

// EnsureBuiltinTLS makes or renews the built-in certificates when a block of the configuration asks for them, which is what a start of
// `coddy serve` and a reload do before the listeners and the dials read their files. It does nothing otherwise, and nothing when the
// files are right. The result lists what was done.
func EnsureBuiltinTLS(c *Config, now func() time.Time) (pki.Result, error) {
	if !c.BuiltinTLSWanted() {
		return pki.Result{}, nil
	}
	if _, ok := c.builtinPaths(); !ok {
		return pki.Result{}, nil
	}
	return pki.Ensure(pki.Dir(c.Paths.Home), c.TLSWant(), now)
}
