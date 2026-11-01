//go:build browser

package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// backend is one place browsers are: the hub of this process, or another
// Coddy server reached through its HTTP API.
type backend struct {
	// label is "" for this machine: this process, or the server of this
	// machine for a console or an editor. Its browsers keep their bare ids;
	// every other one is named <browser id>@<label>.
	label  string
	hub    *browser.Hub
	remote *browser.Remote
}

// backends are the places a chat of this process looks for browsers: this
// process when it takes the extension's sockets, else the server of this
// machine when it serves its browsers, then browser.remotes.
func backends(cfg *config.Config) []backend {
	var out []backend
	hub := browser.Default()
	switch {
	case hub.Serving() || cfg == nil:
		out = append(out, backend{hub: hub})
	case cfg.Browser.Serves() && cfg.Browser.ServeRemote && cfg.HTTPServer.IsEnabled():
		out = append(out, backend{remote: localServer(cfg)})
	}
	if cfg == nil {
		return out
	}
	seen := map[string]bool{"": true}
	for _, entry := range cfg.Browser.Remotes {
		opts, err := remote.Resolve(cfg, entry, "")
		if err != nil || opts == nil {
			continue
		}
		label := remoteLabel(entry, opts.BaseURL)
		for base, n := label, 2; seen[label]; n++ {
			label = fmt.Sprintf("%s-%d", base, n)
		}
		seen[label] = true
		out = append(out, backend{label: label, remote: &browser.Remote{Label: label, Base: opts.BaseURL, Token: opts.Token}})
	}
	return out
}

// localServer is the HTTP API of the coddy serve this configuration starts,
// as a console or an editor on the same machine reaches it.
func localServer(cfg *config.Config) *browser.Remote {
	host := cfg.HTTPServer.DefaultListenHost()
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	token := ""
	if tokens := cfg.HTTPServer.EffectiveAuthTokens(); len(tokens) > 0 {
		token = tokens[0]
	}
	return &browser.Remote{Base: "http://" + net.JoinHostPort(host, cfg.HTTPServer.DefaultListenPortString()), Token: token}
}

// remoteLabel names a remote the way the operator wrote it: the name of an
// httpserver.remotes entry as it is, a relay's node mount by the node, any
// other address by its host.
func remoteLabel(entry, base string) string {
	if !strings.Contains(entry, "/") && !strings.Contains(entry, ":") {
		return entry
	}
	if i := strings.LastIndex(base, swarm.MountPath); i >= 0 {
		return strings.Trim(base[i+len(swarm.MountPath):], "/")
	}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return host
}

// splitName parses a browser name: the browser's id and the label of the
// server it is connected to. Ids hold no "@".
func splitName(name string) (id, label string) {
	id, label, _ = strings.Cut(name, "@")
	return id, label
}

func (b backend) name(id string) string {
	if b.label == "" {
		return id
	}
	return id + "@" + b.label
}

// caller is how this chat names itself to another server.
func caller(cfg *config.Config, env *tooling.Env) browser.Caller {
	return browser.Caller{Node: nodeName(cfg), SessionID: env.SessionID, Title: sessionTitle(env)}
}

// nodeName is this process's name in the swarm, else the machine's.
func nodeName(cfg *config.Config) string {
	if cfg != nil {
		for _, j := range cfg.Swarm.Join {
			if n := strings.TrimSpace(j.Name); n != "" {
				return label(n)
			}
		}
	}
	host, _ := os.Hostname()
	return label(host)
}

// label keeps a name to the letters a remote owner accepts.
func label(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "coddy"
	}
	return b.String()
}

// sessionTitle is the chat's title as its bundle records it, which names the
// chat's tab group in a browser of another server.
func sessionTitle(env *tooling.Env) string {
	if env.SessionDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(env.SessionDir, "session.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Title string `json:"title"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return ""
	}
	return meta.Title
}

// found is a browser of some backend, as one chat sees it.
type found struct {
	backend  backend
	snapshot browser.Snapshot
}

// discover lists the browsers of every backend; the backends that did not
// answer are reported, not fatal.
func discover(ctx context.Context, bs []backend, cfg *config.Config, env *tooling.Env) ([]found, []string) {
	var out []found
	var problems []string
	for _, b := range bs {
		var snaps []browser.Snapshot
		if b.hub != nil {
			snaps = b.hub.List(env.SessionID)
		} else {
			var err error
			if snaps, err = b.remote.Browsers(ctx, caller(cfg, env)); err != nil {
				problems = append(problems, err.Error())
				continue
			}
		}
		for _, s := range snaps {
			out = append(out, found{backend: b, snapshot: s})
		}
	}
	return out, problems
}

// backendOf finds the backend a browser name points at.
func backendOf(bs []backend, name string) (backend, string, error) {
	id, lbl := splitName(name)
	for _, b := range bs {
		if b.label == lbl {
			return b, id, nil
		}
	}
	if lbl != "" {
		return backend{}, "", fmt.Errorf("no server %q in browser.remotes; call list", lbl)
	}
	return backend{}, "", errors.New("this process reaches no browser of its own machine: the server needs browser.token and browser.serve_remote; call list")
}
