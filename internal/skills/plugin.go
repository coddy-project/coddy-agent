package skills

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// SourceStatus describes whether a configured marketplace source resolves to a
// valid agents-standard marketplace, reported by `plugin marketplace list`.
type SourceStatus struct {
	Source   string `json:"source"`
	Kind     string `json:"kind"`              // "git" | "api"
	Valid    bool   `json:"valid"`             // usable as a skill source
	Standard string `json:"standard"`          // "marketplace" | "no-manifest" | "unreachable" | "invalid"
	Name     string `json:"name,omitempty"`    // marketplace name (agents standard)
	Version  string `json:"version,omitempty"` // marketplace metadata version
	Plugins  int    `json:"plugins"`           // plugin/skill count when known
	Error    string `json:"error,omitempty"`
}

// MarketplaceStatus probes every source in effect for cwd (network / git
// access) and reports whether each is a valid agents-standard marketplace.
// Failures are captured per source rather than aborting the whole listing.
func MarketplaceStatus(ctx context.Context, cfg *config.Config, cwd string) []SourceStatus {
	srcs := ListSources(cfg, cwd)
	out := make([]SourceStatus, 0, len(srcs))
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	for _, src := range srcs {
		out = append(out, probeSource(ctx, managedDir, src))
	}
	return out
}

func probeSource(ctx context.Context, managedDir, src string) SourceStatus {
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
		tmp, cleanup, err := remoteStagingDir(managedDir, "coddy-mpstat-")
		if err != nil {
			st.Standard = "unreachable"
			st.Error = err.Error()
			return st
		}
		defer cleanup()
		clone := filepath.Join(tmp, "repo")
		if err := safeClone(spec.url, spec.ref, clone); err != nil {
			st.Standard = "unreachable"
			st.Error = err.Error()
			return st
		}
		mf, _, err := loadMarketplace(clone)
		if err != nil {
			st.Standard = "invalid"
			st.Error = err.Error()
			return st
		}
		if mf != nil {
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

// PluginOptions shapes what a surface of the `plugin` command may do.
type PluginOptions struct {
	// AllowTrust admits `plugin marketplace trust|untrust`, the approval of a
	// project entry. Only the terminal (`coddy plugin`) sets it: a chat names
	// the command instead, the way a chat cannot grant MCP workspace trust.
	AllowTrust bool
}

// RunPluginCommand dispatches a `plugin ...` invocation of the chat `/plugin`
// command. args are the tokens after `/plugin`, cwd the session workspace,
// whose project marketplaces count as the trust gate allows. It returns
// human-readable output; a non-nil error signals a usage or execution failure
// the caller surfaces to the user.
func RunPluginCommand(ctx context.Context, cfg *config.Config, cwd string, args []string) (string, error) {
	return RunPluginCommandWith(ctx, cfg, cwd, args, PluginOptions{})
}

// RunPluginCommandWith is RunPluginCommand for a surface with its own
// options; the CLI (`coddy plugin ...`) runs it with AllowTrust. The usage
// screen - no arguments, `help`, or a -h / -help - reads neither cfg nor cwd,
// so a caller asking for help (`coddy help plugin`) may pass nil for both.
func RunPluginCommandWith(ctx context.Context, cfg *config.Config, cwd string, args []string, opts PluginOptions) (string, error) {
	args = trimTokens(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return pluginUsage(), nil
	}
	switch args[0] {
	case "marketplace", "mp":
		return runPluginMarketplace(ctx, cfg, cwd, args[1:], opts)
	case "install", "add":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin install <plugin>@<marketplace> | <owner/repo | git-url | marketplace-url>")
		}
		if plugin, market, ok := ParsePluginRef(args[1]); ok {
			return pluginInstallFromMarketplace(ctx, cfg, cwd, plugin, market)
		}
		return pluginInstall(ctx, cfg, cwd, args[1])
	case "update", "upgrade":
		// No argument: every installed skill a newer version is published
		// for. With one: that skill or plugin.
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		return pluginUpdate(ctx, cfg, cwd, name)
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

func runPluginMarketplace(ctx context.Context, cfg *config.Config, cwd string, args []string, opts PluginOptions) (string, error) {
	args = trimTokens(args)
	if len(args) == 0 {
		return "", fmt.Errorf("usage: plugin marketplace list|add|remove|update|trust|untrust")
	}
	switch args[0] {
	case "list", "ls":
		if len(args) >= 2 {
			return pluginMarketplaceShow(cfg, cwd, args[1])
		}
		return pluginMarketplaceList(ctx, cfg, cwd), nil
	case "add":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin marketplace add <owner/repo | git-url | marketplace-url>")
		}
		return pluginMarketplaceAdd(ctx, cfg, cwd, args[1])
	case "remove", "rm":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin marketplace remove <marketplace | owner/repo | git-url | marketplace-url>")
		}
		return pluginMarketplaceRemove(cfg, cwd, args[1])
	case "update":
		// No argument: every marketplace and source in effect. With one: only
		// it. Either way the lists are read and nothing is installed.
		key := ""
		if len(args) >= 2 {
			key = args[1]
		}
		return pluginMarketplaceUpdate(ctx, cfg, cwd, key)
	case "trust", "untrust":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: plugin marketplace %s <marketplace | source>", args[0])
		}
		if !opts.AllowTrust {
			return "", fmt.Errorf("a project marketplace is approved in a terminal, with `coddy plugin marketplace trust %s` in the workspace, or with the shield in Settings -> Skills", args[1])
		}
		if args[0] == "untrust" {
			return pluginMarketplaceUntrust(cfg, cwd, args[1])
		}
		return pluginMarketplaceTrust(cfg, cwd, args[1])
	default:
		return "", fmt.Errorf("unknown marketplace subcommand %q", args[0])
	}
}

// pluginMarketplaceAdd adds a marketplace to the operator's marketplaces.json:
// it reads the list of plugins the marketplace publishes and installs none of
// them. Adding it again refreshes that list.
func pluginMarketplaceAdd(ctx context.Context, cfg *config.Config, cwd, source string) (string, error) {
	m, refreshed, err := AddMarketplace(ctx, cfg, cwd, source, ScopeGlobal)
	if err != nil {
		return "", err
	}
	how := fmt.Sprintf("Install one with `plugin install <plugin>@%s`; `plugin marketplace list %s` names them.", m.Name, m.Name)
	if isWholeSource(cfg, cwd, m.Source) {
		how += fmt.Sprintf(" It is declared as a source as well: `plugin install %s` installs every plugin it lists.", m.Source)
	}
	if refreshed {
		return fmt.Sprintf("Marketplace %q is already added; its list is refreshed: %d plugin(s). %s", m.Name, len(m.Plugins), how), nil
	}
	return fmt.Sprintf("Added marketplace %q from %s: %d plugin(s). %s", m.Name, m.Source, len(m.Plugins), how), nil
}

// pluginInstallFromMarketplace installs one plugin of a marketplace in effect.
// Nothing installed is an error, so a script sees it in the exit status.
func pluginInstallFromMarketplace(ctx context.Context, cfg *config.Config, cwd, plugin, market string) (string, error) {
	res, err := InstallFromMarketplace(ctx, cfg, cwd, plugin, market)
	if err != nil {
		return "", err
	}
	if len(res.Added)+len(res.Updated) == 0 && len(res.Failed) > 0 {
		return "", fmt.Errorf("install %s@%s: %s", plugin, market, res.Failed[0].Error)
	}
	return fmt.Sprintf("Installed %s@%s. %s", plugin, market, formatSyncLine(res)), nil
}

// pluginMarketplaceUpdate reads the list of one marketplace or source, or of
// every one in effect for an empty key, and names the installed skills a
// newer version is published for (RefreshIndex). It installs nothing.
func pluginMarketplaceUpdate(ctx context.Context, cfg *config.Config, cwd, key string) (string, error) {
	res, err := RefreshIndex(ctx, cfg, cwd, key)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	switch {
	case res.Marketplace != nil:
		fmt.Fprintf(&b, "Read marketplace %q: %d plugin(s) listed.", res.Marketplace.Name, len(res.Marketplace.Plugins))
	case key != "" && len(res.Read) > 0:
		fmt.Fprintf(&b, "Read %s.", res.Read[0])
	case key != "":
		fmt.Fprintf(&b, "Could not read %s.", key)
	default:
		fmt.Fprintf(&b, "Read %d marketplace(s) and source(s).", len(res.Read))
	}
	switch n := len(res.Updates); {
	case n == 0 && len(res.Failed) > 0:
		b.WriteString(" No newer version was found in what could be read.")
	case n > 0:
		noun := "updates"
		if n == 1 {
			noun = "update"
		}
		fmt.Fprintf(&b, "\n%d %s available:", n, noun)
		for _, u := range res.Updates {
			from := u.Version
			if from == "" {
				from = "(no version)"
			}
			fmt.Fprintf(&b, "\n  - %s %s -> %s", u.Name, from, u.Latest)
		}
		b.WriteString("\nInstall them with `plugin update`, or one with `plugin update <name>`.")
	case key != "":
		b.WriteString(" Every skill installed from it is up to date.")
	default:
		b.WriteString(" Every installed skill is up to date.")
	}
	writeProblems(&b, res.Failed, res.Held)
	return b.String(), nil
}

// pluginUpdate installs the newer versions of installed skills: every one a
// newer version is published for, or the skill or plugin name names
// (UpdateSkills). Nothing updated and something failed is an error, so a
// script sees it in the exit status.
func pluginUpdate(ctx context.Context, cfg *config.Config, cwd, name string) (string, error) {
	res, err := UpdateSkills(ctx, cfg, cwd, name)
	if err != nil {
		return "", err
	}
	changed := len(res.Added) + len(res.Updated)
	switch {
	case changed == 0 && len(res.Failed) > 0 && name != "":
		return "", fmt.Errorf("update %s: %s", name, res.Failed[0].Error)
	case changed == 0 && len(res.Failed) > 0:
		return "", fmt.Errorf("update failed: %s%s", res.Failed[0].Error, moreFailures(res.Failed))
	case changed == 0 && len(res.Failed) == 0 && name != "":
		return fmt.Sprintf("%s is up to date.", name), nil
	case changed == 0 && len(res.Failed) == 0:
		return "Every installed skill is up to date.", nil
	case name != "":
		return fmt.Sprintf("Updated %s. %s", name, formatSyncLine(res)), nil
	default:
		return fmt.Sprintf("Updated %d skill(s). %s", changed, formatSyncLine(res)), nil
	}
}

// pluginMarketplaceRemove takes the marketplaces and sources key names out of
// the operator's file and the project's (RemoveDeclared).
func pluginMarketplaceRemove(cfg *config.Config, cwd, key string) (string, error) {
	r, err := RemoveDeclared(cfg, cwd, key)
	switch {
	case err != nil:
		return "", err
	case len(r.Marketplaces) > 0 && len(r.Sources) > 0:
		return fmt.Sprintf("Removed marketplace %q and its source %s. Installed skills remain until removed.", r.Marketplaces[0].Name, r.Sources[0]), nil
	case len(r.Marketplaces) > 0:
		return fmt.Sprintf("Removed marketplace %q. Installed skills remain until removed.", r.Marketplaces[0].Name), nil
	case len(r.Sources) > 0:
		return fmt.Sprintf("Removed marketplace %q. Installed skills remain until removed.", key), nil
	default:
		return fmt.Sprintf("Marketplace %q was not configured.", key), nil
	}
}

// pluginMarketplaceTrust approves a project entry for the workspace, after
// printing what it is and where it is declared.
func pluginMarketplaceTrust(cfg *config.Config, cwd, key string) (string, error) {
	d, err := ApproveShown(cfg, cwd, key, "")
	if err != nil {
		return "", err
	}
	what := "source, installed whole"
	how := fmt.Sprintf("`plugin install %s` installs every plugin it publishes into %s", d.Source, cfg.Skills.ManagedDir(cfg.Paths.Home))
	if d.Kind == KindMarketplace {
		what = fmt.Sprintf("marketplace %q, plugins installed one by one", d.Name)
		how = fmt.Sprintf("`plugin install <plugin>@%s` installs one of its plugins into %s", d.Name, cfg.Skills.ManagedDir(cfg.Paths.Home))
	}
	return fmt.Sprintf("Approved %s for %s:\n  declared by: %s\n  reads from:  %s\n  digest:      %s\n%s; rewriting the entry asks again.",
		what, cwd, d.Path, d.Source, d.Fingerprint, how), nil
}

// pluginMarketplaceUntrust withdraws the approval of a project entry.
func pluginMarketplaceUntrust(cfg *config.Config, cwd, key string) (string, error) {
	removed, err := Revoke(cfg, cwd, key)
	if err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("%q had no approval for %s.", key, cwd), nil
	}
	return fmt.Sprintf("Withdrew the approval of %q for %s; skills it installed stay until removed.", key, cwd), nil
}

// pluginMarketplaceShow lists the plugins of a marketplace in effect as it
// read them last.
func pluginMarketplaceShow(cfg *config.Config, cwd, key string) (string, error) {
	ms, err := AddedMarketplaces(cfg, cwd)
	if err != nil {
		return "", err
	}
	i := indexAdded(ms, key, false)
	if i < 0 {
		i = indexAdded(ms, key, true)
	}
	if i < 0 {
		if d, ok := FindDeclaration(cfg, cwd, key); ok && d.State != StateReady {
			return "", heldError(d)
		}
		return "", fmt.Errorf("marketplace %q is not added; `plugin marketplace list` shows the added ones", key)
	}
	m := ms[i]
	installed := map[string]bool{}
	for _, p := range installedPlugins(RemoteSources(cfg), m.Source) {
		installed[strings.ToLower(p)] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Marketplace %q (%s), %d plugin(s) as read %s:\n", m.Name, m.Source, len(m.Plugins), m.UpdatedAt)
	for _, p := range m.Plugins {
		line := "  - " + p.Name
		if p.Version != "" {
			line += "@" + p.Version
		}
		if installed[strings.ToLower(p.Name)] {
			line += "  [installed]"
		}
		if p.Description != "" {
			line += "  " + firstLine(p.Description)
		}
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "Install one with `plugin install <plugin>@%s`.", m.Name)
	return b.String(), nil
}

// pluginInstall installs every plugin a source publishes (InstallSource),
// declaring it in the operator's file when no file of cwd declares it in
// effect. Only this source is read: installing one neither reads the others
// nor fails on them.
func pluginInstall(ctx context.Context, cfg *config.Config, cwd, source string) (string, error) {
	res, _, err := InstallSource(ctx, cfg, cwd, source)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Installed %q. %s", source, formatSyncLine(res)), nil
}

func pluginMarketplaceList(ctx context.Context, cfg *config.Config, cwd string) string {
	decls, errs := Declarations(cfg, cwd)
	statuses := MarketplaceStatus(ctx, cfg, cwd)
	added, addedErr := AddedMarketplaces(cfg, cwd)
	pending := held(decls)
	if len(statuses) == 0 && len(added) == 0 && len(pending) == 0 && addedErr == nil && len(errs) == 0 {
		return "No marketplaces configured. Add one with `plugin marketplace add <owner/repo | url>`."
	}
	var b strings.Builder
	for _, err := range errs {
		fmt.Fprintf(&b, "A marketplaces.json could not be read: %s\n", firstLine(err.Error()))
	}
	if addedErr != nil {
		fmt.Fprintf(&b, "Added marketplaces could not be read: %s\n", firstLine(addedErr.Error()))
	}
	if len(added) > 0 {
		fmt.Fprintf(&b, "%d added marketplace(s), install a plugin with `plugin install <plugin>@<marketplace>`:\n", len(added))
		for _, m := range added {
			fmt.Fprintf(&b, "  - %s  [%s; %s; %s]\n", m.Name, statusDetail(probeSource(ctx, cfg.Skills.ManagedDir(cfg.Paths.Home), m.Source)), m.Source, originLabel(m.Origin))
		}
	}
	if len(statuses) > 0 {
		fmt.Fprintf(&b, "%d source(s), installed whole with `plugin install <source>`:\n", len(statuses))
		for _, st := range statuses {
			origin := OriginHome
			for _, d := range decls {
				if d.Kind == KindSource && sameSource(d.Source, st.Source) {
					origin = d.Origin
					break
				}
			}
			fmt.Fprintf(&b, "  - %s  [%s; %s]\n", st.Source, statusDetail(st), originLabel(origin))
		}
	}
	if len(pending) > 0 {
		fmt.Fprintf(&b, "%d project entr(ies) of %s not in effect:\n", len(pending), config.ProjectMarketplacesPath(cwd))
		for _, d := range pending {
			state := "awaiting approval: `coddy plugin marketplace trust " + d.Key() + "`"
			if d.State == StateDenied {
				state = "switched off by skills.project_trust: deny"
			}
			fmt.Fprintf(&b, "  - %s  [%s; %s]\n", d.Key(), d.Source, state)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// originLabel says where an entry is declared, for `marketplace list`.
func originLabel(origin string) string {
	switch origin {
	case OriginSystem:
		return "built into Coddy"
	case OriginProject:
		return "from the project"
	default:
		return "yours"
	}
}

// statusDetail is the bracketed state of a marketplace in `marketplace list`.
func statusDetail(st SourceStatus) string {
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
	return detail
}

func pluginInstalledList(cfg *config.Config, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	loader := NewLoader(cfg.Skills.SearchDirs())
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
		"  plugin marketplace add <owner/repo|url>     add a marketplace and read its plugin list (installs nothing)",
		"  plugin marketplace list [<marketplace>]     list marketplaces and sources, or the plugins of one marketplace",
		"  plugin marketplace update [<marketplace>]   read the plugin lists again and report newer versions (installs nothing)",
		"  plugin marketplace remove <marketplace>     remove a marketplace or a source",
		"  plugin marketplace trust <marketplace>      approve a project marketplace or source for this workspace (terminal only)",
		"  plugin marketplace untrust <marketplace>    withdraw that approval",
		"  plugin install <plugin>@<marketplace>       install one plugin of an added marketplace",
		"  plugin install <owner/repo|url>             install every skill a source publishes",
		"  plugin update [<name>]                      install the newer versions of installed skills, or of one",
		"  plugin remove <name>                        remove an installed skill",
		"  plugin enable <name>                        enable a skill",
		"  plugin disable <name>                       disable a skill",
		"  plugin list                                 list installed skills with versions",
	}, "\n")
}

func formatSyncLine(res *SyncResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d added, %d updated, %d failed.", len(res.Added), len(res.Updated), len(res.Failed))
	writeProblems(&b, res.Failed, res.Held)
	return b.String()
}

// writeProblems lists what failed and the project entries the trust gate held
// back, one line each.
func writeProblems(b *strings.Builder, failed []SyncFailure, heldBack []Declaration) {
	for _, f := range failed {
		fmt.Fprintf(b, "\n  ! %s: %s", f.Source, firstLine(f.Error))
	}
	for _, d := range heldBack {
		if d.State == StateDenied {
			fmt.Fprintf(b, "\n  ? %s: declared by %s, switched off by skills.project_trust: deny", d.Key(), d.Path)
			continue
		}
		fmt.Fprintf(b, "\n  ? %s: declared by %s, not approved for this workspace (`coddy plugin marketplace trust %s`)", d.Key(), d.Path, d.Key())
	}
}

// moreFailures names how many failures follow the first one.
func moreFailures(failed []SyncFailure) string {
	if len(failed) < 2 {
		return ""
	}
	return fmt.Sprintf(" (and %d more)", len(failed)-1)
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
