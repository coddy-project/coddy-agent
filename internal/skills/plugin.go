package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// SourceStatus describes whether a configured marketplace source resolves to a
// valid agents-standard marketplace, reported by `plugin marketplace list`.
type SourceStatus struct {
	Source   string `json:"source"`
	Kind     string `json:"kind"`               // "git" | "api"
	Valid    bool   `json:"valid"`              // usable as a skill source
	Standard string `json:"standard"`           // "marketplace" | "no-manifest" | "unreachable" | "invalid"
	Name     string `json:"name,omitempty"`     // marketplace name (agents standard)
	Version  string `json:"version,omitempty"`  // marketplace metadata version
	Plugins  int    `json:"plugins"`            // plugin/skill count when known
	Error    string `json:"error,omitempty"`
}

// MarketplaceStatus probes every configured source (network / git access) and
// reports whether each is a valid agents-standard marketplace. Failures are
// captured per source rather than aborting the whole listing.
func MarketplaceStatus(ctx context.Context, cfg *config.Config) []SourceStatus {
	srcs := ListSources(cfg)
	out := make([]SourceStatus, 0, len(srcs))
	for _, src := range srcs {
		out = append(out, probeSource(ctx, src))
	}
	return out
}

func probeSource(ctx context.Context, src string) SourceStatus {
	st := SourceStatus{Source: src}
	spec, err := parseSource(src)
	if err != nil {
		st.Standard = "invalid"
		st.Error = err.Error()
		return st
	}
	st.Kind = spec.kind

	switch spec.kind {
	case "api":
		mf, err := fetchManifestHTTP(ctx, spec.url)
		if err != nil {
			st.Standard = "unreachable"
			st.Error = err.Error()
			return st
		}
		st.Valid = true
		st.Standard = "marketplace"
		st.Name = mf.Name
		st.Version = mf.Metadata.Version
		st.Plugins = len(mf.Plugins)
		return st

	case "git":
		tmp, err := os.MkdirTemp("", "coddy-mpstat-")
		if err != nil {
			st.Standard = "unreachable"
			st.Error = err.Error()
			return st
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		clone := filepath.Join(tmp, "repo")
		if err := safeClone(spec.url, spec.ref, clone); err != nil {
			st.Standard = "unreachable"
			st.Error = err.Error()
			return st
		}
		if mfPath := findMarketplaceFile(clone); mfPath != "" {
			mf, err := parseMarketplace(mfPath)
			if err != nil {
				st.Standard = "invalid"
				st.Error = err.Error()
				return st
			}
			st.Valid = true
			st.Standard = "marketplace"
			st.Name = mf.Name
			st.Version = mf.Metadata.Version
			st.Plugins = len(mf.Plugins)
			return st
		}
		hits := locateSkillDirs(clone)
		st.Standard = "no-manifest"
		st.Plugins = len(hits)
		st.Valid = len(hits) > 0
		return st

	default:
		st.Standard = "unknown"
		return st
	}
}

// RunPluginCommand dispatches a `plugin ...` invocation shared by the CLI
// (`coddy plugin ...`) and the chat `/plugin` command. args are the tokens
// after `plugin` / `/plugin`. It returns human-readable output; a non-nil error
// signals a usage or execution failure the caller surfaces to the user.
func RunPluginCommand(ctx context.Context, cfg *config.Config, cwd string, args []string) (string, error) {
	args = trimTokens(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return pluginUsage(), nil
	}
	switch args[0] {
	case "marketplace", "mp":
		return runPluginMarketplace(ctx, cfg, args[1:])
	case "install", "add":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin install <owner/repo | git-url | marketplace-url>")
		}
		return pluginInstall(ctx, cfg, args[1])
	case "remove", "uninstall":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin remove <name>")
		}
		if err := DeleteSkill(cfg, cwd, args[1]); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed skill %q.", args[1]), nil
	case "enable":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin enable <name>")
		}
		if err := Enable(cfg, args[1]); err != nil {
			return "", err
		}
		return fmt.Sprintf("Enabled skill %q.", args[1]), nil
	case "disable":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin disable <name>")
		}
		if err := Disable(cfg, args[1]); err != nil {
			return "", err
		}
		return fmt.Sprintf("Disabled skill %q.", args[1]), nil
	case "list":
		// Convenience: `plugin list` = installed skills with versions.
		return pluginInstalledList(cfg, cwd), nil
	default:
		return "", fmt.Errorf("unknown plugin subcommand %q (try `plugin help`)", args[0])
	}
}

func runPluginMarketplace(ctx context.Context, cfg *config.Config, args []string) (string, error) {
	args = trimTokens(args)
	if len(args) == 0 {
		return "", fmt.Errorf("usage: plugin marketplace list|add|remove|sync")
	}
	switch args[0] {
	case "list", "ls":
		return pluginMarketplaceList(ctx, cfg), nil
	case "add":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin marketplace add <owner/repo | git-url | marketplace-url>")
		}
		return pluginMarketplaceAdd(ctx, cfg, args[1])
	case "remove", "rm":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin marketplace remove <owner/repo | git-url | marketplace-url>")
		}
		removed, err := RemoveSource(cfg, args[1])
		if err != nil {
			return "", err
		}
		if !removed {
			return fmt.Sprintf("Marketplace %q was not configured.", args[1]), nil
		}
		return fmt.Sprintf("Removed marketplace %q. Installed skills remain until removed.", args[1]), nil
	case "sync", "update":
		// No address: sync every configured marketplace. With an address: only it.
		if len(args) >= 2 {
			res, err := SyncSource(ctx, cfg, args[1])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Synced marketplace %q. %s", args[1], formatSyncLine(res)), nil
		}
		res, err := Sync(ctx, cfg)
		if err != nil {
			return "", err
		}
		return "Synced all marketplaces. " + formatSyncLine(res), nil
	default:
		return "", fmt.Errorf("unknown marketplace subcommand %q", args[0])
	}
}

// pluginMarketplaceAdd registers a marketplace source and fetches it so its
// skills install immediately.
func pluginMarketplaceAdd(ctx context.Context, cfg *config.Config, source string) (string, error) {
	added, err := AddSource(cfg, source)
	if err != nil {
		return "", err
	}
	res, err := Sync(ctx, cfg)
	if err != nil {
		return "", err
	}
	prefix := fmt.Sprintf("Marketplace %q already configured; re-synced.", source)
	if added {
		prefix = fmt.Sprintf("Added marketplace %q.", source)
	}
	return prefix + " " + formatSyncLine(res), nil
}

// pluginInstall adds the source when new and (re-)syncs it, so install also
// updates an already-installed source in one step.
func pluginInstall(ctx context.Context, cfg *config.Config, source string) (string, error) {
	added, err := AddSource(cfg, source)
	if err != nil {
		return "", err
	}
	res, err := Sync(ctx, cfg)
	if err != nil {
		return "", err
	}
	verb := "Updated"
	if added {
		verb = "Installed"
	}
	return fmt.Sprintf("%s %q. %s", verb, source, formatSyncLine(res)), nil
}

func pluginMarketplaceList(ctx context.Context, cfg *config.Config) string {
	statuses := MarketplaceStatus(ctx, cfg)
	if len(statuses) == 0 {
		return "No marketplaces configured. Add one with `plugin marketplace add <owner/repo | url>`."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d marketplace(s):\n", len(statuses))
	for _, st := range statuses {
		detail := st.Standard
		switch st.Standard {
		case "marketplace":
			name := st.Name
			if name == "" {
				name = "(unnamed)"
			}
			ver := ""
			if st.Version != "" {
				ver = " v" + st.Version
			}
			detail = fmt.Sprintf("valid marketplace — %s%s, %d plugin(s)", name, ver, st.Plugins)
		case "no-manifest":
			detail = fmt.Sprintf("no marketplace.json — %d skill(s) discovered directly", st.Plugins)
		case "unreachable":
			detail = "unreachable"
			if st.Error != "" {
				detail += " (" + firstLine(st.Error) + ")"
			}
		case "invalid":
			detail = "invalid source"
			if st.Error != "" {
				detail += " (" + firstLine(st.Error) + ")"
			}
		}
		fmt.Fprintf(&b, "  - %s  [%s]\n", st.Source, detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

func pluginInstalledList(cfg *config.Config, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	loader := NewLoader(cfg.Skills.Dirs)
	loaded, err := loader.LoadAll(cwd, cfg.Paths.Home, cfg.Skills.ManagedDir(cfg.Paths.Home))
	if err != nil {
		return fmt.Sprintf("failed to load skills: %v", err)
	}
	remote := RemoteSources(cfg)
	byName := make(map[string]*Skill, len(loaded))
	for _, sk := range loaded {
		n := CanonicalCommandName(sk)
		if _, ok := byName[n]; !ok {
			byName[n] = sk
		}
	}
	sums := ListSkills(loaded)
	if len(sums) == 0 {
		return "No skills installed."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s):\n", len(sums))
	for _, sum := range sums {
		version := InstalledVersion(remote, sum.Name, byName[sum.Name])
		if version == "" {
			version = "-"
		}
		origin := ""
		if ent, ok := remote[sum.Name]; ok {
			origin = " (from " + ent.Source + ")"
		}
		fmt.Fprintf(&b, "  - %s@%s%s\n", sum.Name, version, origin)
	}
	return strings.TrimRight(b.String(), "\n")
}

func pluginUsage() string {
	return strings.Join([]string{
		"plugin commands:",
		"  plugin marketplace list                 list configured marketplaces and their status",
		"  plugin marketplace add <owner/repo|url> add a marketplace and fetch its skills",
		"  plugin marketplace remove <source>      remove a marketplace",
		"  plugin marketplace sync                 refresh all marketplaces",
		"  plugin install <owner/repo|url>         install (and update) a marketplace's skills",
		"  plugin remove <name>                    remove an installed skill",
		"  plugin enable <name>                    enable a skill",
		"  plugin disable <name>                   disable a skill",
		"  plugin list                             list installed skills with versions",
	}, "\n")
}

func formatSyncLine(res *SyncResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d added, %d updated, %d failed.", len(res.Added), len(res.Updated), len(res.Failed))
	for _, f := range res.Failed {
		fmt.Fprintf(&b, "\n  ! %s: %s", f.Source, firstLine(f.Error))
	}
	return b.String()
}

func trimTokens(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
