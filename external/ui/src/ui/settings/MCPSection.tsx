import { useCallback, useEffect, useState } from "react";
import { IconTrash } from "./SchemaForm";
import { Switch } from "./Switch";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";
import {
  MCP_SERVER_TEMPLATE,
  PROJECT_TRUST_OPTIONS,
  declarationFacts,
  originLabel,
  parseServerEntryJson,
  serverRowToEntryJson,
  showsTrustControl,
  validateMCPServerName,
  type MCPScope,
  type MCPServerRow,
  type ProjectTrust,
} from "./mcpServerJson";

type MCPList = {
  items: MCPServerRow[];
  /** Effective mcp.project_trust policy for the server's workspace. */
  projectTrust: ProjectTrust;
  /** Workspace the rows were merged for; approvals are recorded against it. */
  workspace: string;
};

async function fetchServers(refresh = false): Promise<MCPList> {
  const res = await fetch(`/coddy/mcp${refresh ? "?refresh=1" : ""}`);
  if (!res.ok) return { items: [], projectTrust: "ask", workspace: "" };
  const data = (await res.json()) as {
    items?: MCPServerRow[];
    project_trust?: ProjectTrust;
    workspace?: string;
  };
  return {
    items: data.items ?? [],
    projectTrust: data.project_trust ?? "ask",
    workspace: data.workspace ?? "",
  };
}

async function apiSend(
  path: string,
  method: "POST" | "PUT" | "DELETE",
  body?: unknown,
): Promise<{ ok: boolean; error?: string }> {
  const init: RequestInit = { method };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    try {
      const j = (await res.json()) as { error?: { message?: string } };
      return { ok: false, error: j.error?.message || `HTTP ${res.status}` };
    } catch {
      return { ok: false, error: `HTTP ${res.status}` };
    }
  }
  return { ok: true };
}

// Plug glyph shared with the Skills list style.
function IconServer() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <rect x="2" y="3" width="20" height="7" rx="2" />
      <rect x="2" y="14" width="20" height="7" rx="2" />
      <line x1="6" y1="6.5" x2="6.01" y2="6.5" />
      <line x1="6" y1="17.5" x2="6.01" y2="17.5" />
    </svg>
  );
}

function IconSync() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M21 2v6h-6" />
      <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
      <path d="M3 22v-6h6" />
      <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
    </svg>
  );
}

function IconPencil() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
    </svg>
  );
}

// Shield glyph for the workspace trust control on project-local rows.
function IconShield() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M12 3l7 3v6c0 4.4-3 8.2-7 9-4-.8-7-4.6-7-9V6Z" />
      <path d="M9 12l2 2 4-4" />
    </svg>
  );
}

function IconChevron(props: { open: boolean }) {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      style={{ transform: props.open ? "rotate(90deg)" : undefined }}
    >
      <polyline points="9 18 15 12 9 6" />
    </svg>
  );
}

function statusTitle(row: MCPServerRow): string {
  switch (row.status) {
    case "connected":
      return row.tools.length === 1
        ? translate("mcp.status.connectedOne", { count: row.tools.length })
        : translate("mcp.status.connectedMany", { count: row.tools.length });
    case "error":
      return row.error || translate("mcp.status.probeFailed");
    case "disabled":
      return translate("mcp.status.disabled");
    case "needs_approval":
      return translate("mcp.status.needsApproval");
    case "denied":
      return translate("mcp.status.denied");
    default:
      return row.error || translate("mcp.status.unsupported");
  }
}

/** What a server would run or contact, as one line. */
function targetLine(row: MCPServerRow): string {
  if (row.command) return [row.command, ...(row.args ?? [])].join(" ");
  return row.url ?? "";
}

/** Editor state: null = closed; name empty = adding a new server. */
type EditorState = {
  name: string;
  text: string;
  isNew: boolean;
  scope: MCPScope;
};

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
  const [servers, setServers] = useState<MCPServerRow[]>([]);
  const [projectTrust, setProjectTrust] = useState<ProjectTrust>("ask");
  const [workspace, setWorkspace] = useState("");
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [editorError, setEditorError] = useState<string | null>(null);
  const [editorBusy, setEditorBusy] = useState(false);

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
              const editable = !row.readonly;
              return (
                <li
                  key={row.name}
                  className={`mcp-list-item${row.enabled ? "" : " is-disabled"}`}
                >
                  <div className="mcp-list-item-head">
                    <button
                      type="button"
                      className="mcp-expand-btn"
                      onClick={() =>
                        setExpanded((p) => ({ ...p, [row.name]: !isOpen }))
                      }
                      title={
                        isOpen
                          ? t("mcp.expand.collapse")
                          : t("mcp.expand.expand")
                      }
                      aria-label={t("mcp.expand.aria", {
                        action: isOpen
                          ? t("mcp.expand.collapse")
                          : t("mcp.expand.expand"),
                        name: row.name,
                      })}
                      aria-expanded={isOpen}
                      data-testid={`mcp-expand-${row.name}`}
                    >
                      <IconChevron open={isOpen} />
                    </button>
                    <span
                      className={`mcp-status-dot is-${row.status}`}
                      title={statusTitle(row)}
                      data-testid={`mcp-status-${row.name}`}
                    />
                    <IconServer />
                    <div className="mcp-list-item-text">
                      <div className="skills-list-item-name">
                        {row.name}
                        <span
                          className="skills-list-item-badge"
                          title={t("mcp.badge.definedIn", {
                            origin: originLabel(row.origin),
                          })}
                        >
                          {row.source}
                        </span>
                        {row.transport !== "stdio" ? (
                          <span className="skills-list-item-badge mcp-badge-transport">
                            {row.transport}
                          </span>
                        ) : null}
                      </div>
                      <div className="skills-list-item-desc mcp-command">
                        {row.status === "error" || row.status === "unsupported"
                          ? row.error
                          : targetLine(row)}
                      </div>
                    </div>
                    {showsTrustControl(row, projectTrust) ? (
                      <button
                        type="button"
                        className={`settings-btn settings-btn-icon${row.trusted ? "" : " settings-btn-approve"}`}
                        disabled={!!busy[row.name]}
                        onClick={() => onToggleTrust(row)}
                        title={
                          row.trusted
                            ? t("mcp.trust.approvedTitle", {
                                fingerprint: row.fingerprint ?? "",
                              })
                            : t("mcp.trust.approveTitle", {
                                target: targetLine(row),
                              })
                        }
                        aria-label={t(
                          row.trusted
                            ? "mcp.trust.withdrawAria"
                            : "mcp.trust.approveAria",
                          { name: row.name },
                        )}
                        data-testid={`mcp-trust-${row.name}`}
                      >
                        <IconShield />
                      </button>
                    ) : null}
                    <Switch
                      checked={row.enabled}
                      disabled={!!busy[row.name]}
                      onChange={() => onToggleServer(row)}
                      title={
                        row.enabled
                          ? t("mcp.switch.enabledTitle")
                          : t("mcp.switch.disabledTitle")
                      }
                      ariaLabel={t(
                        row.enabled
                          ? "mcp.switch.disableAria"
                          : "mcp.switch.enableAria",
                        { name: row.name },
                      )}
                      dataTestId={`mcp-toggle-${row.name}`}
                    />
                    <button
                      type="button"
                      className="settings-btn settings-btn-icon"
                      disabled={!editable}
                      onClick={() => openEdit(row)}
                      title={
                        editable
                          ? t("mcp.edit.title", {
                              origin: originLabel(row.origin),
                            })
                          : t("mcp.edit.readonlyTitle")
                      }
                      aria-label={t("mcp.edit.aria", { name: row.name })}
                      data-testid={`mcp-edit-${row.name}`}
                    >
                      <IconPencil />
                    </button>
                    <button
                      type="button"
                      className="settings-btn settings-btn-icon settings-btn-danger"
                      disabled={!editable || !!busy[row.name]}
                      onClick={() => onDelete(row)}
                      title={
                        editable
                          ? t("mcp.delete.title", {
                              origin: originLabel(row.origin),
                            })
                          : t("mcp.delete.readonlyTitle")
                      }
                      aria-label={t("mcp.delete.aria", { name: row.name })}
                      data-testid={`mcp-delete-${row.name}`}
                    >
                      <IconTrash />
                    </button>
                  </div>

                  {row.status === "needs_approval" ||
                  row.status === "denied" ? (
                    <div
                      className="mcp-trust-note"
                      data-testid={`mcp-trust-note-${row.name}`}
                    >
                      {row.status === "denied" ? (
                        <p>{t("mcp.note.denied")}</p>
                      ) : (
                        <>
                          <p>
                            {t("mcp.note.declaredBy", {
                              path: row.source_path ?? "./.coddy/mcp.json",
                            })}
                          </p>
                          <dl className="mcp-trust-facts">
                            {declarationFacts(row).map((fact) => (
                              <div key={fact.label}>
                                <dt>{fact.label}</dt>
                                <dd>
                                  <code>{fact.value}</code>
                                </dd>
                              </div>
                            ))}
                            <div>
                              <dt>{t("mcp.fact.in")}</dt>
                              <dd>
                                <code>
                                  {workspace || t("mcp.note.workspaceFallback")}
                                </code>
                              </dd>
                            </div>
                          </dl>
                          <p>{t("mcp.note.namesOnly")}</p>
                        </>
                      )}
                    </div>
                  ) : null}

                  {editor && !editor.isNew && editor.name === row.name ? (
                    <MCPEditorCard
                      editor={editor}
                      error={editorError}
                      busy={editorBusy}
                      onChange={setEditor}
                      onSave={onEditorSave}
                      onCancel={() => setEditor(null)}
                    />
                  ) : null}

                  {isOpen ? (
                    row.tools.length === 0 ? (
                      <p className="settings-muted mcp-tools-empty">
                        {row.status === "connected"
                          ? t("mcp.tools.emptyConnected")
                          : t("mcp.tools.notReachable")}
                      </p>
                    ) : (
                      <ul
                        className="mcp-tools"
                        data-testid={`mcp-tools-${row.name}`}
                      >
                        {row.tools.map((tool) => (
                          <li
                            key={tool.name}
                            className={`mcp-tool-row${tool.enabled ? "" : " is-disabled"}`}
                          >
                            <div className="mcp-tool-text">
                              <div className="mcp-tool-name">{tool.name}</div>
                              {tool.description ? (
                                <div className="skills-list-item-desc">
                                  {tool.description}
                                </div>
                              ) : null}
                            </div>
                            <Switch
                              checked={tool.enabled}
                              disabled={
                                !!busy[`${row.name}__${tool.name}`] ||
                                !row.enabled
                              }
                              onChange={() =>
                                onToggleTool(row, tool.name, tool.enabled)
                              }
                              title={
                                !row.enabled
                                  ? t("mcp.toolSwitch.serverDisabled")
                                  : tool.enabled
                                    ? t("mcp.switch.enabledTitle")
                                    : t("mcp.switch.disabledTitle")
                              }
                              ariaLabel={t(
                                tool.enabled
                                  ? "mcp.toolSwitch.disableAria"
                                  : "mcp.toolSwitch.enableAria",
                                { tool: tool.name, server: row.name },
                              )}
                              dataTestId={`mcp-tool-toggle-${row.name}-${tool.name}`}
                            />
                          </li>
                        ))}
                      </ul>
                    )
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </fieldset>
    </div>
  );
}

/** Cursor-style JSON editor card for one .coddy/mcp.json entry. */
function MCPEditorCard(props: {
  editor: EditorState;
  error: string | null;
  busy: boolean;
  onChange: (next: EditorState) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const { editor, error, busy, onChange, onSave, onCancel } = props;
  const { t } = useT();
  return (
    <div className="mcp-editor" data-testid="mcp-editor">
      {editor.isNew ? (
        <input
          className="settings-input"
          type="text"
          placeholder={t("mcp.editor.namePlaceholder")}
          value={editor.name}
          onChange={(e) => onChange({ ...editor, name: e.target.value })}
          aria-label={t("mcp.editor.nameAria")}
          data-testid="mcp-editor-name"
        />
      ) : null}
      {editor.isNew ? (
        <div
          className="mcp-editor-scope"
          role="radiogroup"
          aria-label={t("mcp.editor.scopeAria")}
        >
          <label className="mcp-editor-scope-option">
            <input
              type="radio"
              name="mcp-editor-scope"
              checked={editor.scope === "local"}
              onChange={() => onChange({ ...editor, scope: "local" })}
              data-testid="mcp-editor-scope-local"
            />
            <span>{t("mcp.editor.scopeLocal")}</span>
          </label>
          <label className="mcp-editor-scope-option">
            <input
              type="radio"
              name="mcp-editor-scope"
              checked={editor.scope === "global"}
              onChange={() => onChange({ ...editor, scope: "global" })}
              data-testid="mcp-editor-scope-global"
            />
            <span>{t("mcp.editor.scopeGlobal")}</span>
          </label>
        </div>
      ) : null}
      <textarea
        className="settings-input mcp-editor-json"
        rows={8}
        spellCheck={false}
        value={editor.text}
        onChange={(e) => onChange({ ...editor, text: e.target.value })}
        aria-label={t("mcp.editor.jsonAria")}
        data-testid="mcp-editor-json"
      />
      <p className="settings-field-desc">
        {t("mcp.editor.formatDescription", {
          path:
            editor.scope === "global"
              ? "~/.coddy/mcp.json"
              : "./.coddy/mcp.json",
        })}
      </p>
      {error ? <p className="settings-error">{error}</p> : null}
      <div className="mcp-editor-actions">
        <button
          type="button"
          className="settings-btn settings-btn-primary"
          disabled={busy}
          onClick={onSave}
          data-testid="mcp-editor-save"
        >
          {t("mcp.editor.save")}
        </button>
        <button
          type="button"
          className="settings-btn"
          disabled={busy}
          onClick={onCancel}
        >
          {t("mcp.editor.cancel")}
        </button>
      </div>
    </div>
  );
}
