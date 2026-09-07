package config

import (
	"flag"
	"fmt"
	"strings"
)

// Trust policy values for mcp.project_trust. They govern the project-local
// <cwd>/.coddy/mcp.json only: that file travels with the repository, so its
// command, arguments, and environment are chosen by whoever wrote the
// checkout rather than by the operator.
const (
	// ProjectTrustAsk keeps a project-local server cold until the operator
	// approves that exact declaration for that workspace.
	ProjectTrustAsk = "ask"
	// ProjectTrustAllow starts project-local servers without approval
	// (pre-1.0 behaviour; for workspaces the operator already trusts).
	ProjectTrustAllow = "allow"
	// ProjectTrustDeny never starts project-local servers and offers no
	// approval path.
	ProjectTrustDeny = "deny"
)

// ProjectTrustFlagName is the CLI flag (on `coddy acp` / `coddy http`) that
// overrides mcp.project_trust for one process, so a trusted checkout or a CI
// job can opt in without editing config.yaml.
const ProjectTrustFlagName = "mcp-project-trust"

// ProjectTrustFlagUsage is the shared flag help text.
const ProjectTrustFlagUsage = "trust policy for project-local .coddy/mcp.json: ask (approve each declaration), allow (start them automatically), deny (never load them); overrides mcp.project_trust"

// ApplyProjectTrustFlag overrides mcp.project_trust only when the
// -mcp-project-trust flag was explicitly provided on fs; otherwise the config
// value is left untouched. Shared by the acp and http entrypoints so both
// behave identically. An unknown value is a hard error: silently falling back
// to the default would hide the operator's intent behind a typo.
func ApplyProjectTrustFlag(fs *flag.FlagSet, cfg *Config, val *string) error {
	if fs == nil || cfg == nil || val == nil {
		return nil
	}
	var err error
	fs.Visit(func(f *flag.Flag) {
		if f.Name != ProjectTrustFlagName {
			return
		}
		next := MCP{ProjectTrust: *val}
		if verr := next.Validate(); verr != nil {
			err = fmt.Errorf("-%s: %w", ProjectTrustFlagName, verr)
			return
		}
		cfg.MCP = next
	})
	return err
}

// MCP holds MCP settings that are not tied to a single server entry
// (YAML key mcp; per-server definitions live under mcp_servers).
type MCP struct {
	// ProjectTrust is the trust policy for <cwd>/.coddy/mcp.json:
	// ask (default), allow, or deny.
	ProjectTrust string `yaml:"project_trust"`
}

// ResolvedProjectTrust returns ProjectTrust with a safe default of
// ProjectTrustAsk, so an empty or unknown value never widens the policy.
func (c MCP) ResolvedProjectTrust() string {
	switch v := strings.ToLower(strings.TrimSpace(c.ProjectTrust)); v {
	case ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny:
		return v
	default:
		return ProjectTrustAsk
	}
}

// Validate normalises ProjectTrust and rejects unknown values.
func (c *MCP) Validate() error {
	v := strings.ToLower(strings.TrimSpace(c.ProjectTrust))
	if v == "" {
		v = ProjectTrustAsk
	}
	switch v {
	case ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny:
		c.ProjectTrust = v
		return nil
	default:
		return fmt.Errorf("project_trust: unknown value %q (use %q, %q, or %q)",
			c.ProjectTrust, ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny)
	}
}
