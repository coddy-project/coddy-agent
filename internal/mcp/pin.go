package mcp

// Version pinning of MCP servers run through npx: the detection of an
// `npx -y <package>` that names no exact version (FindUnpinnedNPX), the
// registry lookup of the package's current release (Resolver), the rewrite
// of the server's arguments when it is registered (PinNPXArgs), and the one
// wording every surface uses to say what was pinned and why. Without a
// version npx asks the registry for the latest release on every start, so
// each session waits on the network and the console hangs without one.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// NPXSpec is the package an `npx -y <package>` stdio server runs, found in
// the server's arguments.
type NPXSpec struct {
	// ArgIndex is the position of the package spec in the arguments.
	ArgIndex int
	// Name is the package name, scope included (@scope/name).
	Name string
	// Version is the version or tag after the name; empty when the spec
	// carries none.
	Version string
}

// npxValueFlags are the npx flags that take the next argument as their value,
// so the scan for the package spec must step over it. A flag outside this
// list and the boolean ones ends the scan: the spec after it may not be a
// package at all.
var npxValueFlags = map[string]bool{
	"-p": true, "--package": true,
	"-c": true, "--call": true,
	"--registry": true, "--cache": true, "--prefix": true,
	"--loglevel": true, "--userconfig": true,
}

// npxBoolFlags are the npx flags that stand alone.
var npxBoolFlags = map[string]bool{
	"-y": true, "--yes": true, "-n": true, "--no": true,
	"-q": true, "--quiet": true, "--no-install": true, "--ignore-existing": true,
	"--prefer-offline": true, "--prefer-online": true, "--offline": true,
}

// isNPX reports whether command is npx: the base name without its extension,
// case aside, so `npx`, `npx.cmd` and a full path all count.
func isNPX(command string) bool {
	base := filepath.Base(strings.TrimSpace(command))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return strings.EqualFold(base, "npx")
}

// FindUnpinnedNPX finds the package an `npx -y <package>` server runs when
// that package carries no exact version. It reports nothing for a command
// that is not npx, for a run without -y / --yes (npx would prompt, so it is
// not a server anybody starts unattended), for a spec that already names a
// version, a range or a tag other than latest, and for anything that is not
// a registry package: a path, a URL, a git or file spec, a tarball, or a
// spec that still holds a ${VAR} placeholder.
func FindUnpinnedNPX(command string, args []string) (NPXSpec, bool) {
	if !isNPX(command) {
		return NPXSpec{}, false
	}
	yes := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-y" || arg == "--yes":
			yes = true
			continue
		case arg == "--":
			continue
		case npxBoolFlags[arg]:
			continue
		case npxValueFlags[arg]:
			i++
			continue
		case strings.HasPrefix(arg, "-"):
			// A flag this scan does not know: whatever follows may be its
			// value, so no package is claimed.
			return NPXSpec{}, false
		}
		if !yes {
			return NPXSpec{}, false
		}
		spec, ok := parseRegistrySpec(arg)
		if !ok || spec.Version != "" && spec.Version != "latest" {
			return NPXSpec{}, false
		}
		spec.ArgIndex = i
		return spec, true
	}
	return NPXSpec{}, false
}

// parseRegistrySpec splits a registry package spec into name and version.
// Only a plain or scoped package name qualifies.
func parseRegistrySpec(arg string) (NPXSpec, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.Contains(arg, "${") {
		return NPXSpec{}, false
	}
	lower := strings.ToLower(arg)
	for _, prefix := range []string{"npm:", "file:", "git+", "git:", "github:", "http:", "https:", "./", "../", "/", "~"} {
		if strings.HasPrefix(lower, prefix) {
			return NPXSpec{}, false
		}
	}
	if strings.Contains(arg, "/") && !strings.HasPrefix(arg, "@") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") {
		return NPXSpec{}, false
	}
	name, version := arg, ""
	// The version follows the last "@" that is not the scope's leading one.
	if at := strings.LastIndex(arg, "@"); at > 0 {
		name, version = arg[:at], arg[at+1:]
	}
	if name == "" || strings.HasPrefix(name, "@") && !strings.Contains(name, "/") {
		return NPXSpec{}, false
	}
	if strings.ContainsAny(name, " \t\"'\\") {
		return NPXSpec{}, false
	}
	return NPXSpec{Name: name, Version: version}, true
}

// PinnedSpec is the spec that runs exactly version of the package.
func (s NPXSpec) PinnedSpec(version string) string {
	return s.Name + "@" + version
}

// unpinnedReason is the one explanation of why an unpinned npx package is a
// problem, shared by every surface that reports one.
const unpinnedReason = "an unpinned `npx -y <package>` asks the npm registry for the latest release on every start, " +
	"even when the package is cached, so Coddy waits on the network before the console's first frame and hangs without one"

// UnpinnedHint returns what the operator can do about a stdio server that
// runs an unpinned npx package, or "" when the server runs something else.
func UnpinnedHint(srv config.MCPServerConfig) string {
	spec, ok := FindUnpinnedNPX(srv.Command, srv.Args)
	if !ok {
		return ""
	}
	return "npx -y " + spec.Name + " has no version: " + unpinnedReason +
		". Pin it in args as " + spec.Name + "@<version>."
}

// DefaultNPMRegistry is where package versions are read from unless npm's own
// registry variable names another one.
const DefaultNPMRegistry = "https://registry.npmjs.org"

// PinResult says what registering an `npx -y <package>` server did about its
// version. It travels to the operator on every surface that registers a
// server: the management API and the web UI, the staged config tools.
type PinResult struct {
	Server  string `json:"server"`
	Package string `json:"package"`
	Version string `json:"version,omitempty"`
	// Pinned is true when the saved arguments now name an exact version.
	Pinned bool `json:"pinned"`
	// Message explains what happened and why it matters, in the operator's
	// words.
	Message string `json:"message"`
}

// Resolver reads the current version of a package from an npm registry.
type Resolver struct {
	// Registry is the registry's base URL.
	Registry string
	// Client makes the request; the default transport honours the proxy
	// environment the way the rest of Coddy does.
	Client *http.Client
	// Timeout bounds one lookup. Registration is a deliberate action with
	// the network at hand, so the bound is short: a registry that does not
	// answer leaves the server unpinned with a warning rather than holding
	// the save.
	Timeout time.Duration
}

// DefaultResolver reads the registry from npm's own configuration variable
// (npm_config_registry, or NPM_CONFIG_REGISTRY), then falls back to the
// public registry. Auth and per-scope registries of a .npmrc are not read;
// a package they alone can resolve is saved unpinned with the warning.
func DefaultResolver() *Resolver {
	registry := strings.TrimSpace(os.Getenv("npm_config_registry"))
	if registry == "" {
		registry = strings.TrimSpace(os.Getenv("NPM_CONFIG_REGISTRY"))
	}
	if registry == "" {
		registry = DefaultNPMRegistry
	}
	return &Resolver{Registry: registry, Client: &http.Client{}, Timeout: 10 * time.Second}
}

// exactVersion accepts a semver release, prerelease or build included, and
// nothing looser: a range or a tag would send npx back to the registry.
var exactVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Latest returns the version the registry publishes as latest for pkg.
func (r *Resolver) Latest(ctx context.Context, pkg string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no registry resolver")
	}
	registry := strings.TrimRight(strings.TrimSpace(r.Registry), "/")
	if registry == "" {
		registry = DefaultNPMRegistry
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The scope's slash is escaped: the registry reads @scope%2fname as one
	// package path segment.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registry+"/"+url.PathEscape(pkg)+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	client := r.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %d for %s", registry, resp.StatusCode, pkg)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("read the registry's answer for %s: %w", pkg, err)
	}
	if !exactVersion.MatchString(body.Version) {
		return "", fmt.Errorf("%s reported %q for %s, not an exact version", registry, body.Version, pkg)
	}
	return body.Version, nil
}

// PinNPXArgs pins the package an `npx -y <package>` server runs to the
// version the registry publishes now. It returns the rewritten arguments
// and the result to show the operator, nil and nil when the server runs
// something else or already names a version, and nil arguments with a
// result when the version could not be read: the server is then saved as it
// was, and the result says why that matters. A nil resolver pins nothing.
func PinNPXArgs(ctx context.Context, r *Resolver, server, command string, args []string) ([]string, *PinResult) {
	if r == nil {
		return nil, nil
	}
	spec, ok := FindUnpinnedNPX(command, args)
	if !ok {
		return nil, nil
	}
	result := &PinResult{Server: server, Package: spec.Name}
	version, err := r.Latest(ctx, spec.Name)
	if err != nil {
		result.Message = UnresolvedMessage(spec.Name, err)
		return nil, result
	}
	pinned := append([]string(nil), args...)
	pinned[spec.ArgIndex] = spec.PinnedSpec(version)
	result.Version, result.Pinned = version, true
	result.Message = PinnedMessage(spec.Name, version)
	return pinned, result
}

// PinnedMessage is what the operator reads after a registration pinned a
// package: what was written, why, and how to move to a newer release.
func PinnedMessage(pkg, version string) string {
	return "Pinned " + pkg + " to " + version + " (npx -y " + pkg + "@" + version + "). " +
		strings.ToUpper(unpinnedReason[:1]) + unpinnedReason[1:] +
		". To update, change the version in args, or remove it and save again to pin the current release."
}

// UnresolvedMessage is what the operator reads when the version could not
// be read: the server is saved unpinned, and this says what to do about it.
func UnresolvedMessage(pkg string, err error) string {
	return "Could not read the current version of " + pkg + " (" + err.Error() + "); saved unpinned. " +
		strings.ToUpper(unpinnedReason[:1]) + unpinnedReason[1:] +
		". Pin it by hand in args as " + pkg + "@<version>."
}
