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
			Command    string            `json:"command"`
			Args       []string          `json:"args"`
			URL        string            `json:"url"`
			Transport  string            `json:"transport"`
			Env        map[string]string `json:"env"`
			Headers    map[string]string `json:"headers"`
			SourcePath string            `json:"source_path"`
		} `json:"items"`
		Workspace string `json:"workspace"`
	}
	if err := h.getJSON(ctx, "/coddy/mcp", &response); err != nil {
		return nil, err
	}
	rows := make([]mcp.ServerStatus, 0, len(response.Items))
	for _, item := range response.Items {
		row := item.ServerStatus
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

func (h *Handler) SetMCPTrust(ctx context.Context, _, name string, trusted bool) error {
	verb := "untrust"
	if trusted {
		verb = "trust"
	}
	return h.postJSON(ctx, "/coddy/mcp/"+url.PathEscape(name)+"/"+verb, nil, nil)
}
