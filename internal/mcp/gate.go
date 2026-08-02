package mcp

// The trust gate is the single decision point between a merged MCP server
// list and a running process or outbound connection. Connect and Probe below
// re-check the decision immediately before mcp.Connect spawns the child or
// opens the socket, so a caller cannot reach the transport by holding an
// entry it evaluated earlier.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TrustState is the gate verdict for one merged declaration.
type TrustState string

const (
	// TrustStateAllowed means the declaration may start: it is operator
	// authored (config.yaml or <home>/mcp.json), the policy is allow, or the
	// operator approved this exact project declaration for this workspace.
	TrustStateAllowed TrustState = "allowed"
	// TrustStateNeedsApproval means a project-local declaration is waiting
	// for a first-use approval bound to workspace and digest.
	TrustStateNeedsApproval TrustState = "needs_approval"
	// TrustStateDenied means project-local declarations are switched off
	// entirely by mcp.project_trust: deny.
	TrustStateDenied TrustState = "denied"
)

// BlockedError is returned by Connect and Probe when the gate refuses a
// declaration. It carries the verdict and the digest the approval would be
// bound to, so callers can report both without recomputing them.
type BlockedError struct {
	Server string
	State  TrustState
	Digest string
}

func (e *BlockedError) Error() string {
	if e.State == TrustStateDenied {
		return fmt.Sprintf("mcp %s: project-local MCP servers are disabled by mcp.project_trust: %s",
			e.Server, config.ProjectTrustDeny)
	}
	return fmt.Sprintf("mcp %s: project-local server is not approved for this workspace (%s)", e.Server, e.Digest)
}

// TrustGate applies the mcp.project_trust policy and the operator's recorded
// approvals to merged server declarations.
type TrustGate struct {
	policy string
	store  *TrustStore
}

// NewTrustGate builds the gate for one configuration. The approvals file is
// read on demand, so approvals granted while the process runs take effect on
// the next session without a restart.
func NewTrustGate(cfg *config.Config) *TrustGate {
	return &TrustGate{
		policy: cfg.MCP.ResolvedProjectTrust(),
		store:  NewTrustStore(cfg.Paths.Home),
	}
}

// Store exposes the backing approvals store (management surfaces list and
// revoke through it).
func (g *TrustGate) Store() *TrustStore { return g.store }

// Policy returns the effective mcp.project_trust value.
func (g *TrustGate) Policy() string { return g.policy }

// Evaluate returns the verdict for one merged declaration in a workspace.
// Only project-origin entries are gated: config.yaml and <home>/mcp.json are
// written by the operator, not by the checkout.
func (g *TrustGate) Evaluate(workspace string, srv ManagedServer) TrustState {
	if srv.Origin != OriginProject {
		return TrustStateAllowed
	}
	switch g.policy {
	case config.ProjectTrustAllow:
		return TrustStateAllowed
	case config.ProjectTrustDeny:
		return TrustStateDenied
	default:
		if g.store.Approved(workspace, srv.Config) {
			return TrustStateAllowed
		}
		return TrustStateNeedsApproval
	}
}

// Check returns nil when srv may start, and a *BlockedError otherwise.
func (g *TrustGate) Check(workspace string, srv ManagedServer) error {
	state := g.Evaluate(workspace, srv)
	if state == TrustStateAllowed {
		return nil
	}
	return &BlockedError{Server: srv.Config.Name, State: state, Digest: Fingerprint(srv.Config)}
}

// Connect checks the gate and then connects, in that order and with nothing
// in between: this is the only path session bootstrap uses for configured
// servers.
func (g *TrustGate) Connect(ctx context.Context, srv ManagedServer, workspace string, log *slog.Logger) (*Client, error) {
	if err := g.Check(workspace, srv); err != nil {
		return nil, err
	}
	return Connect(ctx, srv.Config, workspace, log)
}

// Probe checks the gate and then probes, so listing servers in the UI never
// starts a command the operator has not approved.
func (g *TrustGate) Probe(ctx context.Context, srv ManagedServer, workspace string, log *slog.Logger) ([]ToolInfo, error) {
	if err := g.Check(workspace, srv); err != nil {
		return nil, err
	}
	return Probe(ctx, srv.Config, workspace, log)
}

// Approve records the operator's decision for one declaration. source names
// the file the declaration came from and lands in the receipt.
func (g *TrustGate) Approve(workspace string, srv ManagedServer) error {
	if srv.Origin != OriginProject {
		return fmt.Errorf("mcp %s: only project-local servers are approved here (this one comes from %s)",
			srv.Config.Name, srv.Origin)
	}
	if g.policy == config.ProjectTrustDeny {
		return fmt.Errorf("mcp %s: project-local MCP servers are disabled by mcp.project_trust: %s",
			srv.Config.Name, config.ProjectTrustDeny)
	}
	return g.store.Approve(workspace, config.MCPJSONPath(workspace), srv.Config)
}

// Revoke withdraws the approval of one server name in a workspace.
func (g *TrustGate) Revoke(workspace, name string) (bool, error) {
	return g.store.Revoke(workspace, name)
}
