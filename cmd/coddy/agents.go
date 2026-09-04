package main

// `coddy agents ...` — the operator's view of the subagent catalog and the
// out-of-band approval surface for project-scope definitions. A definition
// that arrived with the checkout directs what a child agent may do, so like a
// project-local MCP server it stays refused until the operator approves it
// here or through the HTTP route; there is no in-chat prompt, because every
// sender auto-allows under permission_mode: bypass. Domain logic lives in
// internal/subagents; this file only wires it to flags and stdout.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

func agentsUsage() string {
	return fmt.Sprintf("usage: %s agents list|trust <name>|untrust <name> [--cwd DIR]", os.Args[0])
}

func runAgents(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", agentsUsage())
	}
	sub := args[0]

	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cwdFlag := fs.String("cwd", "", "workspace the definitions are resolved for (default: process cwd)")
	fs.Usage = func() { _, _ = fmt.Fprintln(fs.Output(), agentsUsage()) }

	// The agent name is positional; everything after it is flags.
	rest := args[1:]
	name := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		name = rest[0]
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// Same resolution as `coddy mcp`: the flag, else the process cwd, made
	// absolute. Receipts are keyed by the canonical form of this path.
	cwd, err := mcpWorkspace(*cwdFlag)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		return agentsList(os.Stdout, cfg, cwd)
	case "trust":
		if name == "" {
			return fmt.Errorf("usage: %s agents trust <name> [--cwd DIR]", os.Args[0])
		}
		return agentsTrust(os.Stdout, cfg, cwd, name)
	case "untrust":
		if name == "" {
			return fmt.Errorf("usage: %s agents untrust <name> [--cwd DIR]", os.Args[0])
		}
		return agentsUntrust(os.Stdout, cfg, cwd, name)
	default:
		return fmt.Errorf("unknown agents subcommand %q (%s)", sub, agentsUsage())
	}
}

// agentsLoad resolves the definitions a session started in cwd would see:
// built-ins, the user's files, and the project's files unless the policy is
// deny, in which case the project directories are never read.
func agentsLoad(cfg *config.Config, cwd string) []*subagents.Definition {
	loader := subagents.NewLoader(cfg.Subagents.Dirs, cfg.Subagents.ResolvedProjectTrust())
	return loader.Load(cwd, cfg.Paths.Home)
}

func agentsList(w io.Writer, cfg *config.Config, cwd string) error {
	policy := cfg.Subagents.ResolvedProjectTrust()
	defs := agentsLoad(cfg, cwd)
	store := subagents.NewTrustStore(cfg.Paths.Home)
	entries := subagents.BuildCatalog(defs, policy, subagents.CanonicalWorkspace(cwd), store)

	_, _ = fmt.Fprintf(w, "Workspace: %s\nProject trust: %s\n\n", cwd, policy)
	subagents.WriteListing(w, entries)

	pending := 0
	for _, e := range entries {
		if e.NeedsApproval {
			pending++
		}
	}
	if pending > 0 {
		_, _ = fmt.Fprintf(w, "\n%d project definition(s) awaiting approval. Review the file, then run:\n  %s agents trust <name> [--cwd DIR]\n",
			pending, os.Args[0])
	}
	return nil
}

func agentsTrust(w io.Writer, cfg *config.Config, cwd, name string) error {
	policy := cfg.Subagents.ResolvedProjectTrust()
	defs := agentsLoad(cfg, cwd)
	def := subagents.FindByName(defs, name)
	if def == nil {
		return agentsNotFound(name, cwd, policy, defs)
	}
	// Only a file that arrived with the checkout needs a receipt; built-ins
	// and the operator's own files are trusted by construction.
	if def.Builtin {
		_, _ = fmt.Fprintf(w, "Subagent %q is built in and needs no approval.\n", def.Name)
		return nil
	}
	if def.Scope != subagents.ScopeProject {
		_, _ = fmt.Fprintf(w, "Subagent %q is a %s-scope definition (%s) and needs no approval.\n", def.Name, def.Scope, def.Path)
		return nil
	}

	store := subagents.NewTrustStore(cfg.Paths.Home)
	// Print what is being approved before recording it, so the terminal
	// carries the same detail the catalog exposes to the HTTP surface.
	_, _ = fmt.Fprintf(w, "Approving subagent %q for %s\n", def.Name, cwd)
	_, _ = fmt.Fprintf(w, "  file:            %s\n", def.Path)
	if def.Model != "" {
		_, _ = fmt.Fprintf(w, "  model:           %s\n", def.Model)
	}
	if def.Mode != "" {
		_, _ = fmt.Fprintf(w, "  mode:            %s\n", def.Mode)
	}
	if def.PermissionMode != "" {
		_, _ = fmt.Fprintf(w, "  permission_mode: %s\n", def.PermissionMode)
	}
	if len(def.Tools) > 0 {
		_, _ = fmt.Fprintf(w, "  tools:           %s\n", strings.Join(def.Tools, ", "))
	}
	if len(def.DisallowedTools) > 0 {
		_, _ = fmt.Fprintf(w, "  disallowed:      %s\n", strings.Join(def.DisallowedTools, ", "))
	}
	if err := store.Approve(subagents.CanonicalWorkspace(cwd), def); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  digest:          %s\n", def.Digest)
	_, _ = fmt.Fprintf(w, "  receipt:         %s\n", store.Path())
	switch policy {
	case config.SubagentsProjectTrustAllow:
		_, _ = fmt.Fprintln(w, "Approved. subagents.project_trust is allow, so this workspace already spawns it; the receipt applies once the policy returns to ask.")
	default:
		_, _ = fmt.Fprintln(w, "Approved. Spawns in this workspace may use it; editing the file changes the digest and asks again.")
	}
	return nil
}

func agentsUntrust(w io.Writer, cfg *config.Config, cwd, name string) error {
	store := subagents.NewTrustStore(cfg.Paths.Home)
	removed, err := store.Revoke(subagents.CanonicalWorkspace(cwd), name)
	if err != nil {
		return err
	}
	if !removed {
		_, _ = fmt.Fprintf(w, "No approval on file for %q in %s\n", name, cwd)
		return nil
	}
	_, _ = fmt.Fprintf(w, "Withdrew the approval of %q for %s (%s)\n", name, cwd, store.Path())
	return nil
}

// agentsNotFound names the definitions the operator could have meant. Hidden
// ones stay out of the list, as they do in the model-facing error, and a deny
// policy is called out because it is the one reason a project file is absent
// even though it exists on disk.
func agentsNotFound(name, cwd, policy string, defs []*subagents.Definition) error {
	msg := fmt.Sprintf("subagent %q not found for %s", name, cwd)
	if visible := subagents.VisibleNames(defs); len(visible) > 0 {
		msg += " (available: " + strings.Join(visible, ", ") + ")"
	}
	if policy == config.SubagentsProjectTrustDeny {
		msg += "; project definitions are not loaded under subagents.project_trust: deny"
	}
	return errors.New(msg)
}
