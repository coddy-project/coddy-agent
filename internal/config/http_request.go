package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ToolHTTPRequest is the YAML tools.http_request section: the policy of the
// http_request tool.
type ToolHTTPRequest struct {
	// Allowlist names the destinations a request may reach without a
	// permission prompt: a host ("api.github.com"), a subdomain wildcard
	// ("*.example.com"), either with an optional port, an origin
	// ("http://localhost:8080") or an address prefix
	// ("https://api.example.com/v1/"). "*" allows every destination. An entry
	// covers everything a request to that destination carries - its files, an
	// unchecked certificate - but not the file it would write, which follows
	// the write policy, nor a proxy, which is a destination of its own.
	Allowlist []string `yaml:"allowlist"`
}

// validate trims the entries in place and refuses one that cannot match.
func (h *ToolHTTPRequest) validate() error {
	for i := range h.Allowlist {
		h.Allowlist[i] = strings.TrimSpace(h.Allowlist[i])
		if _, err := parseHTTPAllowRule(h.Allowlist[i]); err != nil {
			return fmt.Errorf("tools.http_request.allowlist[%d]: %w", i, err)
		}
	}
	return nil
}

// HTTPAllowlistAllows reports whether any entry of a tools.http_request
// allowlist covers u. An entry that does not parse covers nothing.
func HTTPAllowlistAllows(entries []string, u *url.URL) bool {
	if u == nil {
		return false
	}
	for _, entry := range entries {
		rule, err := parseHTTPAllowRule(strings.TrimSpace(entry))
		if err == nil && rule.matches(u) {
			return true
		}
	}
	return false
}

// httpAllowRule is one parsed allowlist entry.
type httpAllowRule struct {
	any bool
	// scheme is empty when the entry names none and so covers both.
	scheme string
	// host is lower-cased; with wildcard it is the domain the subdomains sit under.
	host     string
	wildcard bool
	// port is empty when any port matches.
	port string
	// path is empty when any path matches.
	path string
}

func parseHTTPAllowRule(entry string) (httpAllowRule, error) {
	if entry == "" {
		return httpAllowRule{}, fmt.Errorf("empty entry")
	}
	if entry == "*" {
		return httpAllowRule{any: true}, nil
	}
	var rule httpAllowRule
	var host string
	if strings.Contains(entry, "://") {
		u, err := url.Parse(entry)
		if err != nil {
			return rule, err
		}
		rule.scheme = strings.ToLower(u.Scheme)
		if rule.scheme != "http" && rule.scheme != "https" {
			return rule, fmt.Errorf("%q: the scheme must be http or https", entry)
		}
		if u.User != nil {
			return rule, fmt.Errorf("%q: credentials do not belong in an allowlist entry", entry)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return rule, fmt.Errorf("%q: a query or a fragment cannot be matched", entry)
		}
		host = u.Hostname()
		rule.port = u.Port()
		if rule.port == "" {
			rule.port = map[string]string{"http": "80", "https": "443"}[rule.scheme]
		}
		if p := u.EscapedPath(); p != "" && p != "/" {
			rule.path = p
		}
	} else {
		if strings.Contains(entry, "/") {
			return rule, fmt.Errorf("%q: an entry with a path needs a scheme, like https://%s", entry, entry)
		}
		host = entry
		if h, p, err := net.SplitHostPort(entry); err == nil {
			host, rule.port = h, p
		} else if strings.HasPrefix(entry, "[") && strings.HasSuffix(entry, "]") {
			host = strings.Trim(entry, "[]")
		}
	}
	if rule.port != "" {
		if n, err := strconv.Atoi(rule.port); err != nil || n < 1 || n > 65535 {
			return rule, fmt.Errorf("%q: port %q is not a port number", entry, rule.port)
		}
	}
	host = strings.ToLower(host)
	if strings.HasPrefix(host, "*.") {
		rule.wildcard = true
		host = strings.TrimPrefix(host, "*.")
	}
	if host == "" || strings.Contains(host, "*") {
		return rule, fmt.Errorf("%q: a wildcard may only stand for the leftmost labels, as in *.example.com", entry)
	}
	rule.host = host
	return rule, nil
}

func (r httpAllowRule) matches(u *url.URL) bool {
	if r.any {
		return true
	}
	scheme := strings.ToLower(u.Scheme)
	if r.scheme != "" && r.scheme != scheme {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if r.wildcard {
		if !strings.HasSuffix(host, "."+r.host) {
			return false
		}
	} else if host != r.host {
		return false
	}
	if r.port != "" {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[scheme]
		}
		if port != r.port {
			return false
		}
	}
	if r.path != "" {
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		if strings.HasSuffix(r.path, "/") {
			return strings.HasPrefix(path, r.path)
		}
		return path == r.path || strings.HasPrefix(path, r.path+"/")
	}
	return true
}
