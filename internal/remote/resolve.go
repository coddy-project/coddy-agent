package remote

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TokenEnvVar is the environment variable a client checks for the remote
// bearer token when --remote-token is not passed. Tokens are deliberately not
// stored in config.yaml (httpserver.remotes keeps name and URL only).
const TokenEnvVar = "CODDY_REMOTE_TOKEN"

// Resolve turns --remote / --remote-token values into connection options.
// remoteArg is either the name of a configured remote (httpserver.remotes)
// or a server address; a bare host[:port] defaults to http://. An empty
// remoteArg means local mode and resolves to nil options.
func Resolve(cfg *config.Config, remoteArg, tokenArg string) (*Options, error) {
	arg := strings.TrimSpace(remoteArg)
	if arg == "" {
		return nil, nil
	}
	target := ""
	if cfg != nil {
		for _, r := range cfg.HTTPServer.Remotes {
			if r.Name != "" && strings.EqualFold(r.Name, arg) {
				target = strings.TrimSpace(r.URL)
				if target == "" {
					return nil, fmt.Errorf("--remote: configured remote %q has no url", arg)
				}
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
	token := strings.TrimSpace(tokenArg)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(TokenEnvVar))
	}
	base := u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/")
	return &Options{
		BaseURL:  base,
		Token:    token,
		Insecure: u.Scheme == "http" && !isLoopbackHost(u.Hostname()),
	}, nil
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
