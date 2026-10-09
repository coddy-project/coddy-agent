package config

import "strings"

// TLSHosts are the addresses this process's own listeners are reached by, as the configuration states them: the bind hosts of the HTTP
// server and of the relay, and the host of every advertise_url a join gives to its parent. The built-in certificate authority puts them
// into the server certificate (internal/pki), so a client that dials any of them verifies it. A wildcard bind address is no name and
// the authority skips it.
func (c *Config) TLSHosts() []string {
	if c == nil {
		return nil
	}
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	add(c.HTTPServer.Host)
	add(c.Swarm.Host)
	for _, j := range c.Swarm.Join {
		add(j.AdvertiseURL)
	}
	return out
}
