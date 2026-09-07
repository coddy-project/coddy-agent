//go:build http

package httpserver

// MCP management REST surface (/coddy/mcp*): merged server list (config.yaml
// + global <home>/mcp.json + project .coddy/mcp.json) with tool inventories
// probed over each server's transport, server/tool disable toggles persisted
// into the owning file, and CRUD for mcp.json entries in either scope.
// Mirrors the skills management surface in skills_mgmt.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// mcpProbeTimeout bounds one server probe (spawn + initialize + tools/list).
const mcpProbeTimeout = 8 * time.Second

func (s *Server) registerMCPManagementRoutes() {
	s.mux.HandleFunc("GET /coddy/mcp", s.coddyMCPGet)
	s.mux.HandleFunc("POST /coddy/mcp/{name}/enable", s.coddyMCPServerToggle(false))
	s.mux.HandleFunc("POST /coddy/mcp/{name}/disable", s.coddyMCPServerToggle(true))
	s.mux.HandleFunc("POST /coddy/mcp/{name}/trust", s.coddyMCPServerTrust)
	s.mux.HandleFunc("POST /coddy/mcp/{name}/untrust", s.coddyMCPServerUntrust)
	s.mux.HandleFunc("POST /coddy/mcp/project-trust", s.coddyMCPProjectTrust)
	s.mux.HandleFunc("POST /coddy/mcp/{name}/tools/{tool}/enable", s.coddyMCPToolToggle(false))
	s.mux.HandleFunc("POST /coddy/mcp/{name}/tools/{tool}/disable", s.coddyMCPToolToggle(true))
	s.mux.HandleFunc("PUT /coddy/mcp/{name}", s.coddyMCPServerPut)
	s.mux.HandleFunc("DELETE /coddy/mcp/{name}", s.coddyMCPServerDelete)
}

// mcpToolRow is one tool of a server in the list response.
type mcpToolRow struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// mcpServerRow is one merged server in the list response.
type mcpServerRow struct {
	Name          string            `json:"name"`
	Source        string            `json:"source"`    // global | local (scope)
	Origin        string            `json:"origin"`    // config | home | project (owning file)
	Readonly      bool              `json:"readonly"`  // config.yaml entries: no edit/delete here
	Transport     string            `json:"transport"` // stdio | http
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	URL           string            `json:"url,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	SourcePath    string            `json:"source_path,omitempty"` // file that defines the entry
	Enabled       bool              `json:"enabled"`
	Status        string            `json:"status"` // connected | error | disabled | unsupported | needs_approval | denied
	Error         string            `json:"error,omitempty"`
	Trusted       bool              `json:"trusted"`               // false only for a gated project entry
	Gated         bool              `json:"gated"`                 // the workspace trust gate applies to this entry
	Fingerprint   string            `json:"fingerprint,omitempty"` // digest an approval binds to
	Tools         []mcpToolRow      `json:"tools"`
	DisabledTools []string          `json:"disabled_tools,omitempty"`
}

// mcpProbeEntry caches one server's probed tool list keyed by its config
// fingerprint, so repeated GETs do not respawn subprocesses.
type mcpProbeEntry struct {
	fingerprint string
	tools       []mcp.ToolInfo
	err         string
}

func mcpFingerprint(srv config.MCPServerConfig) string {
	b, _ := json.Marshal(srv)
	return string(b)
}

// probeMCPServer returns the cached tool list for srv, probing on fingerprint
// change or when refresh is forced. The probe runs through the trust gate, so
// listing servers never starts a project command the operator has not
// approved.
func (s *Server) probeMCPServer(ctx context.Context, gate *mcp.TrustGate, srv mcp.ManagedServer, refresh bool) ([]mcp.ToolInfo, string) {
	fp := mcpFingerprint(srv.Config)
	s.mcpProbeMu.Lock()
	if s.mcpProbeCache == nil {
		s.mcpProbeCache = make(map[string]mcpProbeEntry)
	}
	entry, ok := s.mcpProbeCache[srv.Config.Name]
	s.mcpProbeMu.Unlock()
	if ok && !refresh && entry.fingerprint == fp {
		return entry.tools, entry.err
	}

	probeCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	tools, err := gate.Probe(probeCtx, srv, s.defaultCWD, s.log)
	entry = mcpProbeEntry{fingerprint: fp, tools: tools}
	if err != nil {
		entry.err = err.Error()
	}
	s.mcpProbeMu.Lock()
	s.mcpProbeCache[srv.Config.Name] = entry
	s.mcpProbeMu.Unlock()
	return entry.tools, entry.err
}

func (s *Server) invalidateMCPProbe(name string) {
	s.mcpProbeMu.Lock()
	delete(s.mcpProbeCache, name)
	s.mcpProbeMu.Unlock()
}

// coddyMCPGet lists merged MCP servers with tools and per-tool switches.
// ?refresh=1 re-probes every enabled stdio server.
func (s *Server) coddyMCPGet(w http.ResponseWriter, r *http.Request) {
	servers, err := mcp.ListManagedServers(s.activeCfg(), s.defaultCWD)
	if err != nil {
		writeCoddyMCPErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	gate := mcp.NewTrustGate(s.activeCfg())

	rows := make([]mcpServerRow, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		transport := mcp.EffectiveTransport(srv.Config)
		trust := gate.Evaluate(s.defaultCWD, srv)
		row := mcpServerRow{
			Name:          srv.Config.Name,
			Source:        srv.Scope,
			Origin:        srv.Origin,
			Readonly:      srv.Origin == mcp.OriginConfig,
			Transport:     transport,
			Command:       srv.Config.Command,
			Args:          srv.Config.Args,
			URL:           srv.Config.URL,
			SourcePath:    mcpSourcePath(s.activeCfg(), s.defaultCWD, srv.Origin),
			Enabled:       !srv.Config.Disabled,
			Trusted:       trust == mcp.TrustStateAllowed,
			Gated:         srv.Origin == mcp.OriginProject,
			Fingerprint:   mcp.Fingerprint(srv.Config),
			Tools:         []mcpToolRow{},
			DisabledTools: srv.Config.DisabledTools,
		}
		if len(srv.Config.Env) > 0 {
			row.Env = make(map[string]string, len(srv.Config.Env))
			for _, e := range srv.Config.Env {
				row.Env[e.Name] = e.Value
			}
		}
		if len(srv.Config.Headers) > 0 {
			row.Headers = make(map[string]string, len(srv.Config.Headers))
			for _, h := range srv.Config.Headers {
				row.Headers[h.Name] = h.Value
			}
		}
		if !mcp.SupportedTransport(transport) {
			row.Status = "unsupported"
			row.Error = fmt.Sprintf("unsupported MCP transport: %s", transport)
			rows[i] = row
			continue
		}
		if srv.Config.Disabled {
			row.Status = "disabled"
			rows[i] = row
			continue
		}
		// A gated project entry is reported, never probed: probing it would
		// start exactly the command the approval is about.
		if trust != mcp.TrustStateAllowed {
			row.Status = string(trust)
			row.Error = gate.Check(s.defaultCWD, srv).Error()
			rows[i] = row
			continue
		}
		rows[i] = row
		wg.Add(1)
		go func(i int, managed mcp.ManagedServer) {
			defer wg.Done()
			cfgSrv := managed.Config
			tools, probeErr := s.probeMCPServer(r.Context(), gate, managed, refresh)
			disabled := make(map[string]bool, len(cfgSrv.DisabledTools))
			for _, t := range cfgSrv.DisabledTools {
				disabled[t] = true
			}
			toolRows := make([]mcpToolRow, 0, len(tools))
			for _, t := range tools {
				toolRows = append(toolRows, mcpToolRow{Name: t.Name, Description: t.Description, Enabled: !disabled[t.Name]})
			}
			sort.Slice(toolRows, func(a, b int) bool { return toolRows[a].Name < toolRows[b].Name })
			rows[i].Tools = toolRows
			if probeErr != "" {
				rows[i].Status = "error"
				rows[i].Error = probeErr
			} else {
				rows[i].Status = "connected"
			}
		}(i, srv)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":        "coddy.mcp_list",
		"workspace":     s.defaultCWD,
		"project_trust": gate.Policy(),
		"items":         rows,
	})
}

// mcpSourcePath names the file a merged entry comes from, so an approval
// dialog can show where the declaration was read.
func mcpSourcePath(cfg *config.Config, cwd, origin string) string {
	switch origin {
	case mcp.OriginProject:
		return config.MCPJSONPath(cwd)
	case mcp.OriginHome:
		return config.GlobalMCPJSONPath(cfg.Paths.Home)
	default:
		return cfg.Paths.ConfigPath
	}
}

// coddyMCPServerTrust approves the current declaration of a project-local
// server for this workspace, so sessions may start it.
func (s *Server) coddyMCPServerTrust(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	servers, err := mcp.ListManagedServers(s.activeCfg(), s.defaultCWD)
	if err != nil {
		writeCoddyMCPErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	gate := mcp.NewTrustGate(s.activeCfg())
	for _, srv := range servers {
		if srv.Config.Name != name {
			continue
		}
		if err := gate.Approve(s.defaultCWD, srv); err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.invalidateMCPProbe(name)
		slog.Info("mcp server approved for workspace",
			"name", name, "workspace", s.defaultCWD, "digest", mcp.Fingerprint(srv.Config))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":          true,
			"fingerprint": mcp.Fingerprint(srv.Config),
		})
		return
	}
	writeCoddyMCPErr(w, http.StatusBadRequest, fmt.Sprintf("mcp server %q not found", name))
}

// coddyMCPProjectTrust persists the mcp.project_trust policy, so the MCP tab
// owns the whole policy rather than sending operators to another settings tab.
func (s *Server) coddyMCPProjectTrust(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Policy string `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := mcp.SetProjectTrust(s.activeCfg(), body.Policy); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reloadConfigFromDisk()
	slog.Info("mcp project trust policy set", "policy", body.Policy)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":            true,
		"project_trust": s.activeCfg().MCP.ResolvedProjectTrust(),
	})
}

// coddyMCPServerUntrust withdraws an approval; running sessions keep their
// already connected clients, new sessions do not start the server again.
func (s *Server) coddyMCPServerUntrust(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	removed, err := mcp.NewTrustGate(s.activeCfg()).Revoke(s.defaultCWD, name)
	if err != nil {
		writeCoddyMCPErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidateMCPProbe(name)
	slog.Info("mcp server approval revoked", "name", name, "workspace", s.defaultCWD, "removed", removed)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "removed": removed})
}

// coddyMCPServerToggle enables or disables a whole server, persisting into
// the file that defines it (config.yaml or .coddy/mcp.json).
func (s *Server) coddyMCPServerToggle(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := mcp.SetServerDisabled(s.activeCfg(), s.defaultCWD, name, disable); err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.reloadConfigFromDisk()
		slog.Info("mcp server toggled", "name", name, "disabled", disable)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}
}

// coddyMCPToolToggle enables or disables a single tool of a server.
func (s *Server) coddyMCPToolToggle(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tool := r.PathValue("tool")
		if strings.TrimSpace(tool) == "" {
			writeCoddyMCPErr(w, http.StatusBadRequest, "missing tool name")
			return
		}
		if err := mcp.SetToolDisabled(s.activeCfg(), s.defaultCWD, name, tool, disable); err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.reloadConfigFromDisk()
		slog.Info("mcp tool toggled", "server", name, "tool", tool, "disabled", disable)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}
}

// coddyMCPServerPut creates or updates a server entry in the mcp.json file
// selected by ?scope=: "local" (default) writes <cwd>/.coddy/mcp.json,
// "global" writes <home>/mcp.json.
func (s *Server) coddyMCPServerPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := mcp.ValidateServerName(name); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = mcp.ScopeLocal
	}
	var entry config.MCPJSONServer
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(entry.Command) == "" && strings.TrimSpace(entry.URL) == "" {
		writeCoddyMCPErr(w, http.StatusBadRequest, "either command or url is required")
		return
	}
	if err := mcp.UpsertServer(s.activeCfg(), s.defaultCWD, name, scope, entry); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateMCPProbe(name)
	slog.Info("mcp server saved", "name", name, "scope", scope)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// coddyMCPServerDelete removes an mcp.json-defined server from its owning
// file. Config.yaml-defined servers are refused (edit Settings instead).
func (s *Server) coddyMCPServerDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := mcp.DeleteServer(s.activeCfg(), s.defaultCWD, name); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateMCPProbe(name)
	slog.Info("mcp server deleted", "name", name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func writeCoddyMCPErr(w http.ResponseWriter, code int, msg string) {
	body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": msg}})
	http.Error(w, string(body), code)
}
