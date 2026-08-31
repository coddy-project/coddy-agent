import { useCallback, useEffect, useState } from "react";
import { translate } from "../i18n/i18n";
import { fetchServers } from "./mcpApi";
import { apiSend } from "./settingsApi";
import type { MCPServerRow, ProjectTrust } from "./mcpServerJson";

/**
 * Server list state and actions for the Settings -> MCP servers tab. All
 * actions talk to /coddy/mcp* directly; nothing here touches the settings
 * document.
 */
export function useMCPServers(): {
  servers: MCPServerRow[];
  projectTrust: ProjectTrust;
  workspace: string;
  loading: boolean;
  refreshing: boolean;
  error: string | null;
  setError: (err: string | null) => void;
  busy: Record<string, boolean>;
  loadServers: (firstLoad?: boolean, refresh?: boolean) => Promise<void>;
  withBusy: (key: string, fn: () => Promise<void>) => void;
  onToggleServer: (row: MCPServerRow) => void;
  onToggleTool: (row: MCPServerRow, tool: string, enabled: boolean) => void;
  onProjectTrustChange: (next: ProjectTrust) => void;
  onToggleTrust: (row: MCPServerRow) => void;
  onDelete: (row: MCPServerRow) => void;
} {
  const [servers, setServers] = useState<MCPServerRow[]>([]);
  const [projectTrust, setProjectTrust] = useState<ProjectTrust>("ask");
  const [workspace, setWorkspace] = useState("");
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});

  // firstLoad guards the "Loading…" placeholder so refreshes never collapse
  // the list height (same pattern as the Skills tab).
  const loadServers = useCallback(
    async (firstLoad = false, refresh = false) => {
      if (firstLoad) setLoading(true);
      if (refresh) setRefreshing(true);
      const list = await fetchServers(refresh);
      setServers(list.items);
      setProjectTrust(list.projectTrust);
      setWorkspace(list.workspace);
      if (firstLoad) setLoading(false);
      if (refresh) setRefreshing(false);
    },
    [],
  );

  useEffect(() => {
    void loadServers(true);
  }, [loadServers]);

  const withBusy = (key: string, fn: () => Promise<void>) => {
    setBusy((p) => ({ ...p, [key]: true }));
    setError(null);
    void (async () => {
      await fn();
      setBusy((p) => ({ ...p, [key]: false }));
    })();
  };

  const onToggleServer = (row: MCPServerRow) => {
    withBusy(row.name, async () => {
      const action = row.enabled ? "disable" : "enable";
      const res = await apiSend(
        `/coddy/mcp/${encodeURIComponent(row.name)}/${action}`,
        "POST",
      );
      if (!res.ok) {
        setError(
          res.error ||
            translate("mcp.error.toggleServer", { action, name: row.name }),
        );
      } else await loadServers();
    });
  };

  const onToggleTool = (row: MCPServerRow, tool: string, enabled: boolean) => {
    withBusy(`${row.name}__${tool}`, async () => {
      const action = enabled ? "disable" : "enable";
      const res = await apiSend(
        `/coddy/mcp/${encodeURIComponent(row.name)}/tools/${encodeURIComponent(tool)}/${action}`,
        "POST",
      );
      if (!res.ok) {
        setError(
          res.error || translate("mcp.error.toggleTool", { action, tool }),
        );
      } else await loadServers();
    });
  };

  // The policy lives here, not in a settings section of its own: it governs
  // exactly the servers listed below. It persists into config.yaml through the
  // MCP API, so it never joins the settings document Save all flow.
  const onProjectTrustChange = (next: ProjectTrust) => {
    withBusy("project-trust", async () => {
      const res = await apiSend("/coddy/mcp/project-trust", "POST", {
        policy: next,
      });
      if (!res.ok) {
        setError(res.error || translate("mcp.error.changeTrustPolicy"));
      } else await loadServers();
    });
  };

  // Approving binds to the declaration shown in this row; withdrawing takes
  // effect for the next session, not for clients already connected.
  const onToggleTrust = (row: MCPServerRow) => {
    withBusy(row.name, async () => {
      const action = row.trusted ? "untrust" : "trust";
      const res = await apiSend(
        `/coddy/mcp/${encodeURIComponent(row.name)}/${action}`,
        "POST",
      );
      if (!res.ok) {
        setError(
          res.error ||
            translate("mcp.error.toggleTrust", { action, name: row.name }),
        );
      } else await loadServers();
    });
  };

  const onDelete = (row: MCPServerRow) => {
    withBusy(row.name, async () => {
      const res = await apiSend(
        `/coddy/mcp/${encodeURIComponent(row.name)}`,
        "DELETE",
      );
      if (!res.ok) {
        setError(
          res.error || translate("mcp.error.delete", { name: row.name }),
        );
      } else await loadServers();
    });
  };

  return {
    servers,
    projectTrust,
    workspace,
    loading,
    refreshing,
    error,
    setError,
    busy,
    loadServers,
    withBusy,
    onToggleServer,
    onToggleTool,
    onProjectTrustChange,
    onToggleTrust,
    onDelete,
  };
}
