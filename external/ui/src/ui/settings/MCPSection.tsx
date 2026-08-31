import { useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";
import {
  MCP_SERVER_TEMPLATE,
  PROJECT_TRUST_OPTIONS,
  parseServerEntryJson,
  serverRowToEntryJson,
  validateMCPServerName,
  type MCPServerRow,
  type ProjectTrust,
} from "./mcpServerJson";
import { apiSend } from "./settingsApi";
import { IconSync } from "./settingsIcons";
import { MCPEditorCard, type MCPEditorState } from "./MCPEditorCard";
import { MCPServerListItem } from "./MCPServerListItem";
import { useMCPServers } from "./useMCPServers";

/**
 * MCPSection is the Settings -> MCP servers tab: the merged server list from
 * config.yaml, the global ~/.coddy/mcp.json, and the local ./.coddy/mcp.json
 * in the Cursor style — status dot, scope badge, server switch, expandable
 * per-tool switches, and a JSON editor for mcp.json entries of either scope.
 * All actions talk to /coddy/mcp* directly; nothing here touches the
 * settings document.
 */
export function MCPSection() {
  const { t } = useT();
  const {
    servers,
    projectTrust,
    workspace,
    loading,
    refreshing,
    error,
    busy,
    loadServers,
    onToggleServer,
    onToggleTool,
    onProjectTrustChange,
    onToggleTrust,
    onDelete,
  } = useMCPServers();
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [editor, setEditor] = useState<MCPEditorState | null>(null);
  const [editorError, setEditorError] = useState<string | null>(null);
  const [editorBusy, setEditorBusy] = useState(false);

  const openAdd = () => {
    setEditorError(null);
    setEditor({
      name: "",
      text: MCP_SERVER_TEMPLATE,
      isNew: true,
      scope: "local",
    });
  };

  const openEdit = (row: MCPServerRow) => {
    setEditorError(null);
    // Editing writes back to the file that owns the row: origin home lives in
    // the global ~/.coddy/mcp.json, project in the local ./.coddy/mcp.json.
    setEditor({
      name: row.name,
      text: serverRowToEntryJson(row),
      isNew: false,
      scope: row.origin === "home" ? "global" : "local",
    });
  };

  const onEditorSave = () => {
    if (!editor) return;
    const nameErr = validateMCPServerName(editor.name);
    if (nameErr) {
      setEditorError(nameErr);
      return;
    }
    const { entry, error: parseErr } = parseServerEntryJson(editor.text);
    if (parseErr || !entry) {
      setEditorError(parseErr || translate("mcp.error.invalidEntry"));
      return;
    }
    setEditorBusy(true);
    setEditorError(null);
    void (async () => {
      const res = await apiSend(
        `/coddy/mcp/${encodeURIComponent(editor.name.trim())}?scope=${editor.scope}`,
        "PUT",
        entry,
      );
      if (!res.ok) {
        setEditorError(res.error || translate("mcp.error.saveServer"));
      } else {
        setEditor(null);
        await loadServers();
      }
      setEditorBusy(false);
    })();
  };

  return (
    <div className="settings-mcp-section">
      <fieldset className="settings-fieldset mcp-discovery-box">
        <legend>{t("mcp.discovery.legend")}</legend>
        <p className="settings-field-desc">{t("mcp.discovery.description")}</p>
        <label className="settings-label" htmlFor="mcp-project-trust">
          {t("mcp.discovery.projectServersLabel")}
        </label>
        <select
          id="mcp-project-trust"
          className="settings-input"
          value={projectTrust}
          disabled={!!busy["project-trust"]}
          onChange={(e) => onProjectTrustChange(e.target.value as ProjectTrust)}
          data-testid="mcp-project-trust"
        >
          {PROJECT_TRUST_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {t(opt.labelKey)}
            </option>
          ))}
        </select>
      </fieldset>

      <fieldset className="settings-fieldset mcp-servers-box">
        <legend>{t("mcp.servers.legend")}</legend>
        <p className="settings-field-desc">{t("mcp.servers.description")}</p>

        <div className="mcp-toolbar">
          <button
            type="button"
            className="settings-btn"
            onClick={openAdd}
            data-testid="mcp-add-server"
          >
            {t("mcp.addServer")}
          </button>
          <button
            type="button"
            className="settings-btn settings-btn-icon"
            disabled={refreshing}
            onClick={() => void loadServers(false, true)}
            title={t("mcp.refresh.title")}
            aria-label={t("mcp.refresh.aria")}
            data-testid="mcp-refresh"
          >
            <IconSync />
          </button>
        </div>

        {error ? <p className="settings-error">{error}</p> : null}

        {editor && editor.isNew ? (
          <MCPEditorCard
            editor={editor}
            error={editorError}
            busy={editorBusy}
            onChange={setEditor}
            onSave={onEditorSave}
            onCancel={() => setEditor(null)}
          />
        ) : null}

        {servers.length === 0 ? (
          loading ? (
            <p className="settings-muted">{t("mcp.loading")}</p>
          ) : (
            <p className="settings-muted">{t("mcp.empty")}</p>
          )
        ) : (
          <ul className="mcp-list" data-testid="mcp-list">
            {servers.map((row) => {
              const isOpen = !!expanded[row.name];
              return (
                <MCPServerListItem
                  key={row.name}
                  row={row}
                  open={isOpen}
                  busy={!!busy[row.name]}
                  toolBusy={(tool) => !!busy[`${row.name}__${tool}`]}
                  projectTrust={projectTrust}
                  workspace={workspace}
                  editorNode={
                    editor && !editor.isNew && editor.name === row.name ? (
                      <MCPEditorCard
                        editor={editor}
                        error={editorError}
                        busy={editorBusy}
                        onChange={setEditor}
                        onSave={onEditorSave}
                        onCancel={() => setEditor(null)}
                      />
                    ) : null
                  }
                  onToggleExpand={() =>
                    setExpanded((p) => ({ ...p, [row.name]: !isOpen }))
                  }
                  onToggleServer={() => onToggleServer(row)}
                  onToggleTrust={() => onToggleTrust(row)}
                  onToggleTool={(tool, enabled) =>
                    onToggleTool(row, tool, enabled)
                  }
                  onEdit={() => openEdit(row)}
                  onDelete={() => onDelete(row)}
                />
              );
            })}
          </ul>
        )}
      </fieldset>
    </div>
  );
}
