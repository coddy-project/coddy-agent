package remote

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// TokenEnvVar is the environment variable a client checks for the remote
// bearer token when --remote-token is not passed and the configured remote the
// target belongs to carries no token of its own.
const TokenEnvVar = "CODDY_REMOTE_TOKEN" //nolint:gosec // G101: the name of an environment variable, not a credential

// Resolve turns --remote / --remote-token values into connection options.
// remoteArg is either the name of a configured remote (httpserver.remotes)
// or a server address; a bare host[:port] defaults to http://. An empty
// remoteArg means local mode and resolves to nil options.
//
// The token is the first of: tokenArg (--remote-token); the token of the
// configured remote the target belongs to - the entry named, the entry whose
// address it is, or a relay entry the target is a node mount of; and
// CODDY_REMOTE_TOKEN. The entry's token wins over the variable because it is
// bound to a destination and the variable is not.
func Resolve(cfg *config.Config, remoteArg, tokenArg string) (*Options, error) {
	arg := strings.TrimSpace(remoteArg)
	if arg == "" {
		return nil, nil
	}
	target := ""
	entryToken := ""
	if cfg != nil {
		for _, r := range cfg.HTTPServer.Remotes {
			if r.Name != "" && strings.EqualFold(r.Name, arg) {
				target = strings.TrimSpace(r.URL)
				if target == "" {
					return nil, fmt.Errorf("--remote: configured remote %q has no url", arg)
				}
				entryToken = strings.TrimSpace(r.Token)
				break
			}
		}
	}
	if target == "" {
		target = arg
		if !strings.Contains(target, "://") {
			target = "http://" + target
		}
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("--remote: invalid server address %q (want a configured remote name, host:port, or http(s) URL)", remoteArg)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("--remote: server address must be a bare origin without query, fragment, or credentials")
	}
	base := u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/")
	token := strings.TrimSpace(tokenArg)
	if token == "" {
		token = entryToken
	}
	if token == "" && cfg != nil {
		token = configuredTokenFor(cfg.HTTPServer.Remotes, u)
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv(TokenEnvVar))
	}
	return &Options{
		BaseURL:  base,
		Token:    token,
		Insecure: u.Scheme == "http" && !isLoopbackHost(u.Hostname()),
	}, nil
}

// configuredTokenFor is the token of the configured remote the target belongs
// to: the entry at the same address, or a relay entry the target is a node
// mount of (<relay>/swarm/nodes/...), since a mount takes the relay's client
// token. The longest matching entry wins; "" when none carries a token.
func configuredTokenFor(remotes []config.HTTPRemote, target *url.URL) string {
	want := strings.TrimRight(target.Path, "/")
	best, bestLen := "", -1
	for _, r := range remotes {
		token := strings.TrimSpace(r.Token)
		raw := strings.TrimSpace(r.URL)
		if token == "" || raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		e, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(e.Scheme, target.Scheme) || !strings.EqualFold(e.Host, target.Host) {
			continue
		}
		path := strings.TrimRight(e.Path, "/")
		if want != path && !strings.HasPrefix(want, path+swarm.MountPath) {
			continue
		}
		if len(path) > bestLen {
			best, bestLen = token, len(path)
		}
	}
	return best
}

// isLoopbackHost reports whether the host stays on this machine, where plain
// http cannot leak the bearer token onto a network.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
