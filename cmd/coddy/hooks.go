package main

// `coddy hooks ...` — the operator's view of the hook definition files a
// session would load and the out-of-band approval surface for files found
// inside a workspace. A hooks file that arrived with the checkout names
// commands coddy would run with the operator's permissions before every tool
// call, so like a project-local MCP server or subagent definition it stays
// held until the operator approves it here or through the HTTP route; there
// is no in-chat prompt, because every sender auto-allows under
// permission_mode: bypass. Domain logic lives in internal/hooks; this file
// only wires it to flags and stdout.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

func hooksUsage() string {
	return fmt.Sprintf("usage: %s hooks list|trust <file>|untrust <file> [--cwd DIR]", os.Args[0])
}

func runHooks(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", hooksUsage())
	}
	sub := args[0]

	fs := flag.NewFlagSet("hooks", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cwdFlag := fs.String("cwd", "", "workspace the files are resolved for (default: process cwd)")
	fs.Usage = func() { _, _ = fmt.Fprintln(fs.Output(), hooksUsage()) }

	// The file is positional; everything after it is flags.
	rest := args[1:]
	file := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		file = rest[0]
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
		return hooksList(os.Stdout, cfg, cwd)
	case "trust":
		if file == "" {
			return fmt.Errorf("usage: %s hooks trust <file> [--cwd DIR]", os.Args[0])
		}
		return hooksTrust(os.Stdout, cfg, cwd, file)
	case "untrust":
		if file == "" {
			return fmt.Errorf("usage: %s hooks untrust <file> [--cwd DIR]", os.Args[0])
		}
		return hooksUntrust(os.Stdout, cfg, cwd, file)
	default:
		return fmt.Errorf("unknown hooks subcommand %q (%s)", sub, hooksUsage())
	}
}

// hooksLoad resolves the files a session started in cwd would see, with the
// receipts consulted, so the listing and the agent agree.
func hooksLoad(cfg *config.Config, cwd string) []*hooks.Source {
	loader := hooks.NewLoader(cfg.Hooks.Files, cfg.Hooks.ResolvedProjectTrust()).
		WithStore(hooks.NewTrustStore(cfg.Paths.Home))
	return loader.Load(cwd, cfg.Paths.Home)
}

func hooksList(w io.Writer, cfg *config.Config, cwd string) error {
	policy := cfg.Hooks.ResolvedProjectTrust()
	entries := hooks.BuildCatalog(hooksLoad(cfg, cwd))

	_, _ = fmt.Fprintf(w, "Workspace: %s\nProject trust: %s\n\n", cwd, policy)
	hooks.WriteListing(w, entries)

	pending := 0
	for _, e := range entries {
		if e.NeedsApproval && e.Error == "" {
			pending++
		}
	}
	if pending > 0 {
		_, _ = fmt.Fprintf(w, "\n%d project file(s) awaiting approval. Review the file, then run:\n  %s hooks trust <file> [--cwd DIR]\n",
			pending, os.Args[0])
	}
	return nil
}

func hooksTrust(w io.Writer, cfg *config.Config, cwd, file string) error {
	policy := cfg.Hooks.ResolvedProjectTrust()
	sources := hooksLoad(cfg, cwd)
	src := hooks.FindSource(sources, file)
	if src == nil {
		return hooksNotFound(file, cwd, policy, sources)
	}
	// Only a file that arrived with the checkout needs a receipt; the
	// operator's own files are trusted by construction.
	if src.Scope != hooks.ScopeProject {
		_, _ = fmt.Fprintf(w, "Hooks file %s is %s scope and needs no approval.\n", src.Display, src.Scope)
		return nil
	}
	if src.Err != nil {
		return fmt.Errorf("hooks file %s cannot be approved: %v", src.Display, src.Err)
	}

	store := hooks.NewTrustStore(cfg.Paths.Home)
	// Print what is being approved before recording it, so the terminal
	// carries the same detail the catalog exposes to the HTTP surface.
	_, _ = fmt.Fprintf(w, "Approving hooks file %s for %s\n", src.Display, cwd)
	_, _ = fmt.Fprintf(w, "  path:    %s\n", src.Path)
	for _, entry := range hooks.BuildCatalog([]*hooks.Source{src}) {
		for _, h := range entry.Hooks {
			matcher := h.Matcher
			if matcher == "" {
				matcher = "*"
			}
			line := fmt.Sprintf("  hook:    %s(%s): %s", h.Event, matcher, h.Command)
			if len(h.Args) > 0 {
				line += " " + strings.Join(h.Args, " ")
			}
			if h.Unsupported != "" {
				line += " [unsupported: " + h.Unsupported + "]"
			}
			_, _ = fmt.Fprintln(w, line)
		}
	}
	if err := store.Approve(hooks.CanonicalWorkspace(cwd), src); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  digest:  %s\n", src.Digest)
	_, _ = fmt.Fprintf(w, "  receipt: %s\n", store.Path())
	switch policy {
	case config.ProjectTrustAllow:
		_, _ = fmt.Fprintln(w, "Approved. hooks.project_trust is allow, so this workspace already runs it; the receipt applies once the policy returns to ask.")
	default:
		_, _ = fmt.Fprintln(w, "Approved. Sessions in this workspace run its hooks from their next turn; editing the file changes the digest and asks again.")
	}
	return nil
}

func hooksUntrust(w io.Writer, cfg *config.Config, cwd, file string) error {
	store := hooks.NewTrustStore(cfg.Paths.Home)
	// A file that is still on disk is named the way the catalog names it; a
	// receipt whose file is gone can still be withdrawn by its stored name.
	key := hookFileKey(cwd, file)
	if src := hooks.FindSource(hooksLoad(cfg, cwd), file); src != nil {
		key = src.Display
	}
	removed, err := store.Revoke(hooks.CanonicalWorkspace(cwd), key)
	if err != nil {
		return err
	}
	if !removed {
		_, _ = fmt.Fprintf(w, "No approval on file for %s in %s\n", key, cwd)
		return nil
	}
	_, _ = fmt.Fprintf(w, "Withdrew the approval of %s for %s (%s)\n", key, cwd, store.Path())
	return nil
}

// hookFileKey normalises a file argument to the name receipts use: the
// workspace-relative path in slash form for a file inside the workspace.
func hookFileKey(cwd, file string) string {
	file = strings.TrimSpace(file)
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(cwd, file); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		return filepath.Clean(file)
	}
	return filepath.ToSlash(filepath.Clean(file))
}

// hooksNotFound names the files the operator could have meant, and calls
// out a deny policy because it is the one reason a project file is absent
// even though it exists on disk.
func hooksNotFound(file, cwd, policy string, sources []*hooks.Source) error {
	msg := fmt.Sprintf("hooks file %q not found for %s", file, cwd)
	names := make([]string, 0, len(sources))
	for _, src := range sources {
		names = append(names, src.Display)
	}
	if len(names) > 0 {
		msg += " (known: " + strings.Join(names, ", ") + ")"
	}
	if policy == config.ProjectTrustDeny {
		msg += "; project files are not loaded under hooks.project_trust: deny"
	}
	return errors.New(msg)
}
