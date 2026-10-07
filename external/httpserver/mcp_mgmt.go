//go:build http

package httpserver

// MCP management REST surface (/coddy/mcp*): merged server list (global
// <home>/mcp.json + project .coddy/mcp.json) with tool inventories
// probed over each server's transport, server/tool disable toggles persisted
// into the owning file, and CRUD for mcp.json entries in either scope.
// Mirrors the skills management surface in skills_mgmt.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
	Origin        string            `json:"origin"`    // home | project (owning file)
	Transport     string            `json:"transport"` // stdio | http
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	URL           string            `json:"url,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Reads         []string          `json:"reads,omitempty"`       // variables of the process its values read
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
	origin      string
	name        string
	workspace   string
	fingerprint string
	tools       []mcp.ToolInfo
	err         string
}

type mcpProbeKey struct {
	origin    string
	poolKey   string
	workspace string
}

func mcpFingerprint(srv config.MCPServerConfig) string {
	b, _ := json.Marshal(srv)
	return string(b)
}

// probeMCPServer returns the cached tool list for srv, probing on fingerprint
// change or when refresh is forced. The probe runs through the trust gate, so
// listing servers never starts a project command the operator has not
// approved, and through the session manager's server pool, so a server the
// process runs already answers from its connection instead of being started a
// second time.
func (s *Server) probeMCPServer(ctx context.Context, gate *mcp.TrustGate, srv mcp.ManagedServer, cwd string, refresh bool) ([]mcp.ToolInfo, string) {
	fp := mcpFingerprint(srv.Config)
	workspace := session.CanonicalWorkspacePath(cwd)
	key := mcpProbeKey{origin: srv.Origin, poolKey: mcp.PoolKey(srv, cwd), workspace: workspace}
	s.mcpProbeMu.Lock()
	if s.mcpProbeCache == nil {
		s.mcpProbeCache = make(map[mcpProbeKey]mcpProbeEntry)
	}
	entry, ok := s.mcpProbeCache[key]
	s.mcpProbeMu.Unlock()
	if ok && !refresh && entry.fingerprint == fp {
		return entry.tools, entry.err
	}

	probeCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	var tools []mcp.ToolInfo
	var err error
	if s.mgr != nil {
		tools, err = s.mgr.ProbeMCPServer(probeCtx, srv, cwd)
	} else {
		tools, err = gate.Probe(probeCtx, srv, cwd, s.log)
	}
	entry = mcpProbeEntry{origin: srv.Origin, name: srv.Config.Name, workspace: workspace, fingerprint: fp, tools: tools}
	if err != nil {
		// The list shows this error to every client of the API, so what the
		// declaration resolved to (a ${NAME} in the URL, a value the server
		// echoed back) leaves it the way the list shows the declaration.
		entry.err = mcp.RedactValues(srv.Config, cwd, err.Error())
	}
	s.mcpProbeMu.Lock()
	s.mcpProbeCache[key] = entry
	s.mcpProbeMu.Unlock()
	return entry.tools, entry.err
}

func (s *Server) invalidateMCPProbe(origin, name, cwd string) {
	workspace := session.CanonicalWorkspacePath(cwd)
	s.mcpProbeMu.Lock()
	for key, entry := range s.mcpProbeCache {
		if entry.origin != origin || entry.name != name {
			continue
		}
		if origin != mcp.OriginProject || entry.workspace == workspace {
			delete(s.mcpProbeCache, key)
		}
	}
	s.mcpProbeMu.Unlock()
}

// mcpWorkspace resolves the optional session header for every MCP route that
// reads or changes a workspace-dependent declaration. Project-trust policy is
// deliberately global and therefore does not call this helper.
func (s *Server) mcpWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	return s.resolveSessionCWD(w, r)
}

func findManagedMCPServer(cfg *config.Config, cwd, name string) (mcp.ManagedServer, error) {
	servers, err := mcp.ListManagedServers(cfg, cwd)
	if err != nil {
		return mcp.ManagedServer{}, err
	}
	for _, srv := range servers {
		if srv.Config.Name == name {
			return srv, nil
		}
	}
	return mcp.ManagedServer{}, fmt.Errorf("mcp server %q not found", name)
}

// coddyMCPGet lists merged MCP servers with tools and per-tool switches.
// ?refresh=1 re-probes every enabled stdio server.
func (s *Server) coddyMCPGet(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.mcpWorkspace(w, r)
	if !ok {
		return
	}
	servers, err := mcp.ListManagedServers(s.activeCfg(), cwd)
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
		trust := gate.Evaluate(cwd, srv)
		row := mcpServerRow{
			Name:          srv.Config.Name,
			Source:        srv.Scope,
			Origin:        srv.Origin,
			Transport:     transport,
			Command:       srv.Config.Command,
			Args:          srv.Config.Args,
			URL:           srv.Config.URL,
			SourcePath:    mcpSourcePath(s.activeCfg(), cwd, srv.Origin),
			Enabled:       !srv.Config.Disabled,
			Trusted:       trust == mcp.TrustStateAllowed,
			Gated:         srv.Origin == mcp.OriginProject,
			Fingerprint:   mcp.Fingerprint(srv.Config),
			Reads:         mcp.ReadsEnvironment(srv.Config),
			Tools:         []mcpToolRow{},
			DisabledTools: srv.Config.DisabledTools,
		}
		// Names only (issue #376): a value never leaves the server, whichever
		// file declared it. The placeholder in its place is what a save sends
		// back to keep the stored value (mcp.SaveServer), and reads names the
		// variables of the process the values take.
		if len(srv.Config.Env) > 0 {
			row.Env = make(map[string]string, len(srv.Config.Env))
			for _, e := range srv.Config.Env {
				row.Env[e.Name] = config.RedactedValue
			}
		}
		if len(srv.Config.Headers) > 0 {
			row.Headers = make(map[string]string, len(srv.Config.Headers))
			for _, h := range srv.Config.Headers {
				row.Headers[h.Name] = config.RedactedValue
			}
		}
		if !mcp.SupportedTransport(transport) {
			row.Status = "unsupported"
			row.Error = fmt.Sprintf("unsupported MCP transport: %s", transport)
			rows[i] = row
			continue
		}
		// A gated project entry is reported, never probed: probing it would
		// start exactly the command the approval is about.
		if trust != mcp.TrustStateAllowed {
			row.Status = string(trust)
			row.Error = gate.Check(cwd, srv).Error()
			rows[i] = row
			continue
		}
		if srv.Config.Disabled {
			row.Status = "disabled"
			rows[i] = row
			continue
		}
		rows[i] = row
		wg.Add(1)
		go func(i int, managed mcp.ManagedServer) {
			defer wg.Done()
			cfgSrv := managed.Config
			tools, probeErr := s.probeMCPServer(r.Context(), gate, managed, cwd, refresh)
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
		"workspace":     cwd,
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
		return ""
	}
}

// coddyMCPServerTrust approves the declaration of a project-local server the
// operator was shown, for this workspace, and connects it in live sessions.
// The optional body {"fingerprint": "..."} names that declaration by the
// fingerprint the list reported: when the checkout rewrote the entry since,
// the approval is refused with 409 and nothing is recorded. Without a body the
// current declaration is approved, for a client that shows it and approves it
// in one step.
func (s *Server) coddyMCPServerTrust(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.mcpWorkspace(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeCoddyMCPErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	srv, err := findManagedMCPServer(s.activeCfg(), cwd, name)
	if err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	gate := mcp.NewTrustGate(s.activeCfg())
	if err := gate.ApproveShown(cwd, srv, body.Fingerprint); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, mcp.ErrDeclarationChanged) {
			code = http.StatusConflict
		}
		writeCoddyMCPErr(w, code, err.Error())
		return
	}
	s.invalidateMCPProbe(srv.Origin, name, cwd)
	s.refreshLiveMCPServer(r, name, srv.Origin, cwd)
	slog.Info("mcp server approved for workspace", "name", name, "workspace", cwd, "digest", mcp.Fingerprint(srv.Config))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "fingerprint": mcp.Fingerprint(srv.Config)})
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

// coddyMCPServerUntrust withdraws an approval: live sessions close the server
// (a turn in flight keeps it until the turn ends) and new sessions do not
// start it again.
func (s *Server) coddyMCPServerUntrust(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.mcpWorkspace(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	srv, findErr := findManagedMCPServer(s.activeCfg(), cwd, name)
	removed, err := mcp.NewTrustGate(s.activeCfg()).Revoke(cwd, name)
	if err != nil {
		writeCoddyMCPErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if findErr == nil {
		s.invalidateMCPProbe(srv.Origin, name, cwd)
		s.refreshLiveMCPServer(r, name, srv.Origin, cwd)
	}
	slog.Info("mcp server approval revoked", "name", name, "workspace", cwd, "removed", removed)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "removed": removed})
}

// coddyMCPServerToggle enables or disables a whole server and connects or
// closes it in live sessions, leaving their other servers running. Project
// switches stay in the operator's home, outside the checkout.
func (s *Server) coddyMCPServerToggle(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cwd, ok := s.mcpWorkspace(w, r)
		if !ok {
			return
		}
		name := r.PathValue("name")
		srv, err := findManagedMCPServer(s.activeCfg(), cwd, name)
		if err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := mcp.SetServerDisabled(s.activeCfg(), cwd, name, disable); err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.reloadConfigFromDisk()
		s.invalidateMCPProbe(srv.Origin, name, cwd)
		s.refreshLiveMCPServer(r, name, srv.Origin, cwd)
		slog.Info("mcp server toggled", "name", name, "disabled", disable)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}
}

// coddyMCPToolToggle enables or disables a single tool of a server. Nothing
// reconnects: every turn rebuilds its tool filter from the switches.
func (s *Server) coddyMCPToolToggle(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cwd, ok := s.mcpWorkspace(w, r)
		if !ok {
			return
		}
		name := r.PathValue("name")
		tool := r.PathValue("tool")
		if strings.TrimSpace(tool) == "" {
			writeCoddyMCPErr(w, http.StatusBadRequest, "missing tool name")
			return
		}
		srv, err := findManagedMCPServer(s.activeCfg(), cwd, name)
		if err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := mcp.SetToolDisabled(s.activeCfg(), cwd, name, tool, disable); err != nil {
			writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.reloadConfigFromDisk()
		s.invalidateMCPProbe(srv.Origin, name, cwd)
		slog.Info("mcp tool toggled", "server", name, "tool", tool, "disabled", disable)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}
}

// coddyMCPServerPut creates or updates a server entry in the mcp.json file
// selected by ?scope=: "local" (default) writes <cwd>/.coddy/mcp.json,
// "global" writes <home>/mcp.json. An env or header value of "<redacted>",
// the placeholder the list shows, keeps the stored value; ?fingerprint= names
// the declaration the client was shown, and a save against one rewritten
// since is refused with 409 (mcp.SaveServer). Live sessions start it, or
// start it again from the new declaration.
func (s *Server) coddyMCPServerPut(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.mcpWorkspace(w, r)
	if !ok {
		return
	}
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
	shown := strings.TrimSpace(r.URL.Query().Get("fingerprint"))
	if err := mcp.SaveServer(s.activeCfg(), cwd, name, scope, entry, shown); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, mcp.ErrDeclarationChanged) {
			code = http.StatusConflict
		}
		writeCoddyMCPErr(w, code, err.Error())
		return
	}
	origin := mcp.OriginProject
	if scope == mcp.ScopeGlobal {
		origin = mcp.OriginHome
	}
	s.invalidateMCPProbe(origin, name, cwd)
	// Live sessions start the server, or start it again from the edited
	// declaration, and leave their other servers running.
	s.refreshLiveMCPServer(r, name, origin, cwd)
	slog.Info("mcp server saved", "name", name, "scope", scope)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// coddyMCPServerDelete removes a server from the mcp.json file that declares
// it and closes it in live sessions.
func (s *Server) coddyMCPServerDelete(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.mcpWorkspace(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	srv, err := findManagedMCPServer(s.activeCfg(), cwd, name)
	if err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := mcp.DeleteServer(s.activeCfg(), cwd, name); err != nil {
		writeCoddyMCPErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateMCPProbe(srv.Origin, name, cwd)
	// Live sessions close the server the declaration named.
	s.refreshLiveMCPServer(r, name, srv.Origin, cwd)
	slog.Info("mcp server deleted", "name", name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// refreshLiveMCPServer brings live sessions in line with one server whose
// switch or trust the request changed. The change is already on disk, so a
// client that goes away does not cut the refresh short: it keeps the request's
// values but not its cancellation, and the manager bounds its own time.
func (s *Server) refreshLiveMCPServer(r *http.Request, name, origin, cwd string) {
	s.mgr.RefreshMCPServerInWorkspace(context.WithoutCancel(r.Context()), name, origin, cwd)
}

func writeCoddyMCPErr(w http.ResponseWriter, code int, msg string) {
	body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"message": msg}})
	http.Error(w, string(body), code)
}
