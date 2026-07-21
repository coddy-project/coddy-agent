package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ManageSkillsTool exposes skill and marketplace management to the agent so a
// user can install, list, sync, and update skills conversationally — the
// chat-command parity for the CLI (`coddy skills ...`) and the Settings UI.
func ManageSkillsTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "manage_skills",
			Description: "Manage Coddy skills and their marketplace sources. Actions: " +
				"`list` (installed skills with versions), `list_sources` (configured marketplace sources), " +
				"`add_source` (register a marketplace source and install its skills — needs `source`), " +
				"`remove_source` (drop a marketplace source — needs `source`), " +
				"`sync` (re-fetch every configured source), " +
				"`check_updates` (report which installed skills have a newer version upstream), " +
				"`update` (install the newest version of one skill — needs `name`; without `name`, syncs all). " +
				"A `source` is a GitHub `owner/repo`, a git URL, or an http(s) URL to an agents-standard marketplace.json. " +
				"Use when the user asks to install, add, remove, list, sync, or update skills or skill marketplaces.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "The management action to perform.",
						"enum": []interface{}{
							"list", "list_sources", "add_source", "remove_source",
							"sync", "check_updates", "update",
						},
					},
					"source": map[string]interface{}{
						"type":        "string",
						"description": "Marketplace source for add_source/remove_source: owner/repo, a git URL, or a marketplace.json URL.",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Skill name for the update action.",
					},
				},
				"required": []interface{}{"action"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			return executeManageSkills(ctx, cfg, argsJSON, env)
		},
	}
}

type manageSkillsArgs struct {
	Action string `json:"action"`
	Source string `json:"source"`
	Name   string `json:"name"`
}

func executeManageSkills(ctx context.Context, cfg *config.Config, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[manageSkillsArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", fmt.Errorf("manage_skills: no configuration available")
	}
	cwd := "."
	if env != nil && strings.TrimSpace(env.CWD) != "" {
		cwd = env.CWD
	}

	switch strings.TrimSpace(args.Action) {
	case "list":
		return manageSkillsList(cfg, cwd), nil
	case "list_sources":
		return manageSkillsListSources(cfg), nil
	case "add_source":
		return manageSkillsAddSource(ctx, cfg, args.Source)
	case "remove_source":
		return manageSkillsRemoveSource(cfg, args.Source)
	case "sync":
		res, err := skills.Sync(ctx, cfg)
		if err != nil {
			return "", err
		}
		return "Synced skill sources.\n" + formatSyncResult(res), nil
	case "check_updates":
		return manageSkillsCheckUpdates(ctx, cfg)
	case "update":
		return manageSkillsUpdate(ctx, cfg, args.Name)
	default:
		return "", fmt.Errorf("manage_skills: unknown action %q", args.Action)
	}
}

func manageSkillsList(cfg *config.Config, cwd string) string {
	loader := skills.NewLoader(cfg.Skills.Dirs)
	loaded, err := loader.LoadAll(cwd, cfg.Paths.Home, cfg.Skills.ManagedDir(cfg.Paths.Home))
	if err != nil {
		return fmt.Sprintf("failed to load skills: %v", err)
	}
	remote := skills.RemoteSources(cfg)
	byName := make(map[string]*skills.Skill, len(loaded))
	for _, sk := range loaded {
		n := skills.CanonicalCommandName(sk)
		if _, ok := byName[n]; !ok {
			byName[n] = sk
		}
	}
	sums := skills.ListSkills(loaded)
	if len(sums) == 0 {
		return "No skills installed."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s):\n", len(sums))
	for _, sum := range sums {
		version := skills.InstalledVersion(remote, sum.Name, byName[sum.Name])
		if version == "" {
			version = "-"
		}
		origin := ""
		if ent, ok := remote[sum.Name]; ok {
			origin = " (from " + ent.Source + ")"
		}
		fmt.Fprintf(&b, "  - %s@%s%s: %s\n", sum.Name, version, origin, sum.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

func manageSkillsListSources(cfg *config.Config) string {
	srcs := skills.ListSources(cfg)
	if len(srcs) == 0 {
		return "No marketplace sources configured."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d marketplace source(s):\n", len(srcs))
	for _, s := range srcs {
		fmt.Fprintf(&b, "  - %s\n", s)
	}
	return strings.TrimRight(b.String(), "\n")
}

func manageSkillsAddSource(ctx context.Context, cfg *config.Config, source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("manage_skills: add_source requires a source")
	}
	added, err := skills.AddSource(cfg, source)
	if err != nil {
		return "", err
	}
	res, err := skills.Sync(ctx, cfg)
	if err != nil {
		return "", err
	}
	prefix := "Source already configured; re-synced."
	if added {
		prefix = fmt.Sprintf("Added marketplace source %q.", source)
	}
	return prefix + "\n" + formatSyncResult(res), nil
}

func manageSkillsRemoveSource(cfg *config.Config, source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("manage_skills: remove_source requires a source")
	}
	removed, err := skills.RemoveSource(cfg, source)
	if err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("Source %q was not configured.", source), nil
	}
	return fmt.Sprintf("Removed marketplace source %q. Installed skills remain until removed.", source), nil
}

func manageSkillsCheckUpdates(ctx context.Context, cfg *config.Config) (string, error) {
	statuses, err := skills.CheckUpdates(ctx, cfg)
	if err != nil {
		return "", err
	}
	if len(statuses) == 0 {
		return "No remote skills installed.", nil
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	var b strings.Builder
	updates := 0
	for _, st := range statuses {
		if st.UpdateAvailable {
			updates++
			fmt.Fprintf(&b, "  - %s: %s -> %s (update available)\n", st.Name, st.Version, st.Latest)
		} else {
			fmt.Fprintf(&b, "  - %s: %s (up to date)\n", st.Name, st.Version)
		}
	}
	if updates == 0 {
		return "All remote skills are up to date.", nil
	}
	return fmt.Sprintf("%d update(s) available:\n%s", updates, strings.TrimRight(b.String(), "\n")), nil
}

func manageSkillsUpdate(ctx context.Context, cfg *config.Config, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		res, err := skills.Sync(ctx, cfg)
		if err != nil {
			return "", err
		}
		return "Synced all skill sources.\n" + formatSyncResult(res), nil
	}
	res, err := skills.UpdateSkill(ctx, cfg, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated skill %q.\n%s", name, formatSyncResult(res)), nil
}

func formatSyncResult(res *skills.SyncResult) string {
	if res == nil {
		return ""
	}
	parts := fmt.Sprintf("%d added, %d updated, %d failed.", len(res.Added), len(res.Updated), len(res.Failed))
	var b strings.Builder
	b.WriteString(parts)
	for _, f := range res.Failed {
		fmt.Fprintf(&b, "\n  ! %s: %s", f.Source, f.Error)
	}
	return b.String()
}
