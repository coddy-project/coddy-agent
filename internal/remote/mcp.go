package remote

import (
	"context"
	"net/url"

	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// MCPServers uses the server's management routes, so /mcp in --remote mode
// controls the same workspace as the browser.
func (h *Handler) MCPServers(ctx context.Context, _ string) ([]mcp.ServerStatus, error) {
	var response struct {
		Items []struct {
			mcp.ServerStatus
			Gated      bool              `json:"gated"`
			Command    string            `json:"command"`
			Args       []string          `json:"args"`
			URL        string            `json:"url"`
			Transport  string            `json:"transport"`
			Env        map[string]string `json:"env"`
			Headers    map[string]string `json:"headers"`
			SourcePath string            `json:"source_path"`
		} `json:"items"`
		Workspace    string `json:"workspace"`
		ProjectTrust string `json:"project_trust"`
	}
	if err := h.getJSON(ctx, "/coddy/mcp", &response); err != nil {
		return nil, err
	}
	rows := make([]mcp.ServerStatus, 0, len(response.Items))
	for _, item := range response.Items {
		row := item.ServerStatus
		// The server's policy decides whether a per-server trust decision
		// exists, the same rule the web's shield follows.
		row.Approvable = item.Gated && response.ProjectTrust == "ask"
		envKeys := make([]string, 0, len(item.Env))
		for key := range item.Env {
			envKeys = append(envKeys, key)
		}
		headerKeys := make([]string, 0, len(item.Headers))
		for key := range item.Headers {
			headerKeys = append(headerKeys, key)
		}
		row.Declaration = mcp.DeclarationSummary(item.Transport, item.Command, item.Args, item.URL, envKeys, headerKeys, response.Workspace, item.SourcePath)
		rows = append(rows, row)
	}
	return rows, nil
}

// SetMCPEnabled flips a server's switch, or one tool's, on the server.
func (h *Handler) SetMCPEnabled(ctx context.Context, _, name, tool string, enabled bool) error {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	path := "/coddy/mcp/" + url.PathEscape(name)
	if tool != "" {
		path += "/tools/" + url.PathEscape(tool)
	}
	return h.postJSON(ctx, path+"/"+verb, nil, nil)
}

// SetMCPTrust approves or withdraws a project server on the server. An
// approval names the declaration the operator was shown by its fingerprint,
// so the server refuses it (409) when the checkout rewrote the entry since.
func (h *Handler) SetMCPTrust(ctx context.Context, _, name, fingerprint string, trusted bool) error {
	path := "/coddy/mcp/" + url.PathEscape(name)
	if !trusted {
		return h.postJSON(ctx, path+"/untrust", nil, nil)
	}
	return h.postJSON(ctx, path+"/trust", map[string]string{"fingerprint": fingerprint}, nil)
}
