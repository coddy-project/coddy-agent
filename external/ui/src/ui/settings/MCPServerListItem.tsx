import type { ReactNode } from "react";
import { IconTrash } from "./SchemaForm";
import { Switch } from "./Switch";
import { useT } from "../i18n/I18nProvider";
import {
  declarationFacts,
  originLabel,
  showsTrustControl,
  type MCPServerRow,
  type ProjectTrust,
} from "./mcpServerJson";
import { statusTitle, targetLine } from "./mcpRowText";
import { IconChevron, IconPencil, IconServer, IconShield } from "./settingsIcons";

/** One server row of the MCP list: head with controls, trust note, tools sublist. */
export function MCPServerListItem(props: {
  row: MCPServerRow;
  open: boolean;
  busy: boolean;
  toolBusy: (tool: string) => boolean;
  projectTrust: ProjectTrust;
  workspace: string;
  /** Inline MCPEditorCard slot; rendered by the parent when this row is being edited. */
  editorNode: ReactNode | null;
  onToggleExpand: () => void;
  onToggleServer: () => void;
  onToggleTrust: () => void;
  onToggleTool: (tool: string, enabled: boolean) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const {
    row,
    open: isOpen,
    busy,
    toolBusy,
    projectTrust,
    workspace,
    editorNode,
    onToggleExpand,
    onToggleServer,
    onToggleTrust,
    onToggleTool,
    onEdit,
    onDelete,
  } = props;
  const { t } = useT();
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
          onClick={onToggleExpand}
          title={isOpen ? t("mcp.expand.collapse") : t("mcp.expand.expand")}
          aria-label={t("mcp.expand.aria", {
            action: isOpen ? t("mcp.expand.collapse") : t("mcp.expand.expand"),
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
            disabled={busy}
            onClick={onToggleTrust}
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
              row.trusted ? "mcp.trust.withdrawAria" : "mcp.trust.approveAria",
              { name: row.name },
            )}
            data-testid={`mcp-trust-${row.name}`}
          >
            <IconShield />
          </button>
        ) : null}
        <Switch
          checked={row.enabled}
          disabled={busy}
          onChange={onToggleServer}
          title={
            row.enabled
              ? t("mcp.switch.enabledTitle")
              : t("mcp.switch.disabledTitle")
          }
          ariaLabel={t(
            row.enabled ? "mcp.switch.disableAria" : "mcp.switch.enableAria",
            { name: row.name },
          )}
          dataTestId={`mcp-toggle-${row.name}`}
        />
        <button
          type="button"
          className="settings-btn settings-btn-icon"
          disabled={!editable}
          onClick={onEdit}
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
          disabled={!editable || busy}
          onClick={onDelete}
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

      {row.status === "needs_approval" || row.status === "denied" ? (
        <div className="mcp-trust-note" data-testid={`mcp-trust-note-${row.name}`}>
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
                    <code>{workspace || t("mcp.note.workspaceFallback")}</code>
                  </dd>
                </div>
              </dl>
              <p>{t("mcp.note.namesOnly")}</p>
            </>
          )}
        </div>
      ) : null}

      {editorNode}

      {isOpen ? (
        row.tools.length === 0 ? (
          <p className="settings-muted mcp-tools-empty">
            {row.status === "connected"
              ? t("mcp.tools.emptyConnected")
              : t("mcp.tools.notReachable")}
          </p>
        ) : (
          <ul className="mcp-tools" data-testid={`mcp-tools-${row.name}`}>
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
                  disabled={toolBusy(tool.name) || !row.enabled}
                  onChange={() => onToggleTool(tool.name, tool.enabled)}
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
}
