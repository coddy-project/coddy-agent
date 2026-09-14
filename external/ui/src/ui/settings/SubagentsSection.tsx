import { useCallback, useEffect, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { IconShield } from "./MCPSection";
import { SchemaForm, type JsonSchema } from "./SchemaForm";
import {
  pendingApprovalCount,
  scopeBadgeKey,
  shortDigest,
  showsSubagentTrustControl,
  subagentApprovalFacts,
  type SubagentCatalog,
  type SubagentCatalogEntry,
} from "./subagentCatalog";
import {
  fetchSubagentCatalog,
  trustSubagent,
  untrustSubagent,
} from "./subagentsApi";

/**
 * SubagentsSection is the Settings -> Subagents tab. Hybrid, like the Skills
 * tab: the generated form edits the `subagents` config section (including
 * `project_trust`, which saves with the rest of the document), and the catalog
 * below is API-driven (/coddy/subagents): approving a definition writes a
 * receipt at once, because a receipt is not configuration but a statement about
 * one file's current content in one workspace.
 */
export function SubagentsSection(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  /**
   * Workspace of the viewed session. Receipts are keyed by workspace and
   * spawn_agent decides against the session's own cwd, so the tab asks about
   * that workspace rather than the server's. Undefined with nothing to ask
   * about: the server then answers for its own default workspace.
   */
  workspacePath?: string | undefined;
}) {
  const { t, tp } = useT();
  const [catalog, setCatalog] = useState<SubagentCatalog | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const workspacePath = props.workspacePath;

  // firstLoad guards the "Loading…" placeholder so a refresh after an approval
  // never collapses the list height (same pattern as the MCP and Skills tabs).
  const load = useCallback(
    async (firstLoad: boolean) => {
      if (firstLoad) {
        setLoading(true);
      }
      const res = await fetchSubagentCatalog(workspacePath);
      if (res.ok) {
        setCatalog(res.data);
        setError(null);
      } else {
        setError(t("subagents.error.load"));
      }
      if (firstLoad) {
        setLoading(false);
      }
    },
    // `t` is stable for a locale; the workspace is what makes this a
    // different question.
    [workspacePath, t],
  );

  useEffect(() => {
    void load(true);
  }, [load]);

  const onToggleTrust = (entry: SubagentCatalogEntry) => {
    setBusy((p) => ({ ...p, [entry.name]: true }));
    setError(null);
    void (async () => {
      const res = entry.trusted
        ? await untrustSubagent(entry.name, workspacePath)
        : await trustSubagent(entry.name, workspacePath);
      if (res.ok) {
        await load(false);
      } else {
        setError(
          t("subagents.error.trust", { name: entry.name, error: res.error }),
        );
      }
      setBusy((p) => ({ ...p, [entry.name]: false }));
    })();
  };

  const items = catalog?.items ?? [];
  const policy = catalog?.policy ?? "ask";
  const pending = pendingApprovalCount(items);

  return (
    <div className="settings-subagents-section">
      <SchemaForm
        schema={props.schema}
        value={props.value}
        onChange={props.onChange}
        i18nDomain="subagents"
      />

      <fieldset
        className="settings-fieldset subagents-catalog-box"
        data-testid="subagents-catalog"
      >
        <legend>{t("subagents.catalog.legend")}</legend>
        <p className="settings-field-desc">
          {t("subagents.catalog.description")}
        </p>
        <p className="settings-field-desc">
          {t("subagents.catalog.policyAppliesAfterSave")}
        </p>
        {catalog?.workspace ? (
          <p
            className="settings-field-desc subagents-workspace"
            data-testid="subagents-workspace"
          >
            {t("subagents.catalog.workspace")} <code>{catalog.workspace}</code>
          </p>
        ) : null}
        {error ? <p className="settings-error">{error}</p> : null}
        {pending > 0 ? (
          <p
            className="settings-field-desc subagents-pending-hint"
            data-testid="subagents-pending-hint"
          >
            {tp("subagents.catalog.pendingHint", pending)}
          </p>
        ) : null}

        {items.length === 0 ? (
          <p className="settings-muted" data-testid="subagents-empty">
            {loading
              ? t("subagents.catalog.loading")
              : t("subagents.catalog.empty")}
          </p>
        ) : (
          <ul className="mcp-list subagents-list" data-testid="subagents-list">
            {items.map((entry) => (
              <li
                key={entry.name}
                className="mcp-list-item"
                data-testid={`subagent-row-${entry.name}`}
              >
                <div className="mcp-list-item-head">
                  <div className="mcp-list-item-text">
                    <div className="skills-list-item-name">
                      {entry.name}
                      <span className="skills-list-item-badge">
                        {t(scopeBadgeKey(entry.scope))}
                      </span>
                      {entry.hidden ? (
                        <span className="skills-list-item-badge">
                          {t("subagents.badge.hidden")}
                        </span>
                      ) : null}
                      {entry.needs_approval ? (
                        <span
                          className="skills-list-item-badge subagents-badge-pending"
                          data-testid={`subagent-pending-${entry.name}`}
                        >
                          {t("subagents.badge.needsApproval")}
                        </span>
                      ) : null}
                    </div>
                    {/*
                      Plain text on purpose: the description comes out of a
                      file nobody may have approved yet, so it is never
                      markdown and never a link - and until the receipt exists
                      it is not shown at all, exactly as the model's prompt
                      withholds it.
                    */}
                    <div className="skills-list-item-desc">
                      {entry.needs_approval
                        ? t("subagents.catalog.descriptionWithheld")
                        : entry.description}
                    </div>
                  </div>
                  {showsSubagentTrustControl(entry, policy) ? (
                    <button
                      type="button"
                      className={`settings-btn settings-btn-icon${entry.trusted ? "" : " settings-btn-approve"}`}
                      disabled={!!busy[entry.name]}
                      onClick={() => onToggleTrust(entry)}
                      title={
                        entry.trusted
                          ? t("subagents.trust.approvedTitle", {
                              digest: shortDigest(entry.digest),
                            })
                          : t("subagents.trust.approveTitle", {
                              name: entry.name,
                            })
                      }
                      aria-label={t(
                        entry.trusted
                          ? "subagents.trust.withdrawAria"
                          : "subagents.trust.approveAria",
                        { name: entry.name },
                      )}
                      data-testid={`subagent-trust-${entry.name}`}
                    >
                      <IconShield />
                    </button>
                  ) : null}
                </div>

                {entry.needs_approval ? (
                  <div
                    className="mcp-trust-note"
                    data-testid={`subagent-trust-note-${entry.name}`}
                  >
                    <p>{t("subagents.note.declaredBy")}</p>
                    <dl className="mcp-trust-facts">
                      {subagentApprovalFacts(entry).map((fact) => (
                        <div key={fact.label}>
                          <dt>{fact.label}</dt>
                          <dd>
                            <code>{fact.value}</code>
                          </dd>
                        </div>
                      ))}
                      {entry.digest ? (
                        <div>
                          <dt>{t("subagents.fact.digest")}</dt>
                          <dd>
                            <code title={entry.digest}>
                              {shortDigest(entry.digest)}
                            </code>
                          </dd>
                        </div>
                      ) : null}
                    </dl>
                    <p>{t("subagents.note.upperBound")}</p>
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </fieldset>
    </div>
  );
}
