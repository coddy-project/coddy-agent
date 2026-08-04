package main

// `coddy mcp ...` — the out-of-band approval surface for project-local MCP
// servers. ACP clients have no place to render a workspace-trust prompt
// during session/new, and the HTTP UI is not running for them, so the
// operator decides here, in their own terminal, before a session starts.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

func mcpUsage() string {
	return fmt.Sprintf("usage: %s mcp list|trust <name>|untrust <name> [--cwd DIR]", os.Args[0])
}

func runMCP(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s", mcpUsage())
	}
	sub := args[0]

	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cwdFlag := fs.String("cwd", "", "workspace the servers are merged for (default: process cwd)")
	fs.Usage = func() { _, _ = fmt.Fprintln(fs.Output(), mcpUsage()) }

	// The server name is positional; everything after it is flags.
	rest := args[1:]
	name := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		name = rest[0]
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cwd, err := mcpWorkspace(*cwdFlag)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		return mcpList(cfg, cwd)
	case "trust":
		if name == "" {
			return fmt.Errorf("usage: %s mcp trust <name> [--cwd DIR]", os.Args[0])
		}
		return mcpTrust(cfg, cwd, name)
	case "untrust":
		if name == "" {
			return fmt.Errorf("usage: %s mcp untrust <name> [--cwd DIR]", os.Args[0])
		}
		return mcpUntrust(cfg, cwd, name)
	default:
		return fmt.Errorf("unknown mcp subcommand %q (%s)", sub, mcpUsage())
	}
}

func mcpWorkspace(flagValue string) (string, error) {
	dir := strings.TrimSpace(flagValue)
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	return abs, nil
}

func mcpList(cfg *config.Config, cwd string) error {
	servers, err := mcp.ListManagedServers(cfg, cwd)
	if err != nil {
		return err
	}
	gate := mcp.NewTrustGate(cfg)
	fmt.Printf("Workspace: %s\nProject trust: %s\n\n", cwd, gate.Policy())
	if len(servers) == 0 {
		fmt.Println("No MCP servers configured.")
		return nil
	}
	pending := 0
	for _, srv := range servers {
		state := gate.Evaluate(cwd, srv)
		status := "ready"
		switch {
		case srv.Config.Disabled:
			status = "disabled"
		case state == mcp.TrustStateNeedsApproval:
			status = "NEEDS APPROVAL"
			pending++
		case state == mcp.TrustStateDenied:
			status = "denied by policy"
		}
		fmt.Printf("%-24s %-8s %-16s %s\n", srv.Config.Name, srv.Scope, status, mcpTarget(srv.Config))
	}
	if pending > 0 {
		fmt.Printf("\n%d project server(s) awaiting approval. Review the command above, then run:\n  %s mcp trust <name>\n",
			pending, os.Args[0])
	}
	return nil
}

// mcpTarget renders what a server would run or contact, in one line.
func mcpTarget(srv config.MCPServerConfig) string {
	if strings.TrimSpace(srv.Command) != "" {
		return strings.Join(append([]string{srv.Command}, srv.Args...), " ")
	}
	return srv.URL
}

func mcpTrust(cfg *config.Config, cwd, name string) error {
	srv, err := mcpFind(cfg, cwd, name)
	if err != nil {
		return err
	}
	gate := mcp.NewTrustGate(cfg)
	// Print what is being approved before recording it, so the terminal
	// carries the same detail the HTTP surface shows in its dialog.
	fmt.Printf("Approving MCP server %q for %s\n", srv.Config.Name, cwd)
	fmt.Printf("  source:    %s\n", config.MCPJSONPath(cwd))
	transport := mcp.EffectiveTransport(srv.Config)
	fmt.Printf("  transport: %s\n", transport)
	if target := mcpTarget(srv.Config); target != "" {
		// stdio starts a process; the remote transports open a connection.
		label := "runs:     "
		if transport != "stdio" {
			label = "contacts: "
		}
		fmt.Printf("  %s %s\n", label, target)
	}
	// Names only, never values: they routinely hold secrets, and the decision
	// rests on which variables travel to the child, not on what is in them.
	if len(srv.Config.Env) > 0 {
		names := make([]string, 0, len(srv.Config.Env))
		for _, e := range srv.Config.Env {
			names = append(names, e.Name)
		}
		fmt.Printf("  env:       %s\n", strings.Join(names, ", "))
	}
	if len(srv.Config.Headers) > 0 {
		names := make([]string, 0, len(srv.Config.Headers))
		for _, h := range srv.Config.Headers {
			names = append(names, h.Name)
		}
		fmt.Printf("  headers:   %s\n", strings.Join(names, ", "))
	}
	if err := gate.Approve(cwd, *srv); err != nil {
		return err
	}
	fmt.Printf("  digest:    %s\n", mcp.Fingerprint(srv.Config))
	fmt.Println("Approved. New sessions in this workspace will start it; editing the entry asks again.")
	return nil
}

func mcpUntrust(cfg *config.Config, cwd, name string) error {
	removed, err := mcp.NewTrustGate(cfg).Revoke(cwd, name)
	if err != nil {
		return err
	}
	if !removed {
		fmt.Printf("No approval on file for %q in %s\n", name, cwd)
		return nil
	}
	fmt.Printf("Withdrew the approval of %q for %s\n", name, cwd)
	return nil
}

func mcpFind(cfg *config.Config, cwd, name string) (*mcp.ManagedServer, error) {
	servers, err := mcp.ListManagedServers(cfg, cwd)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		if servers[i].Config.Name == name {
			return &servers[i], nil
		}
	}
	return nil, fmt.Errorf("mcp server %q not found for %s", name, cwd)
}
