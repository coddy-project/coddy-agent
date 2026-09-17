import { useCallback, useEffect, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { SchemaForm, type JsonSchema } from "./SchemaForm";
import {
  scopeBadgeKey,
  subagentDeclaredFacts,
  type SubagentCatalog,
} from "./subagentCatalog";
import { fetchSubagentCatalog } from "./subagentsApi";

/**
 * SubagentsSection is the Settings -> Subagents tab. Hybrid, like the Skills
 * tab: the generated form edits the `subagents` config section, and below it
 * the catalog (/coddy/subagents) lists every definition the viewed session's
 * workspace can spawn. The list only reads. A project file awaiting approval
 * says so and names the command that approves it, which runs on the machine
 * that runs coddy - the web UI records no approvals.
 */
export function SubagentsSection(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  /**
   * Workspace of the viewed session. spawn_agent resolves definitions against
   * the session's own cwd, so the tab lists that workspace rather than the
   * server's. Undefined with nothing to ask about: the server then answers for
   * its own default workspace.
   */
  workspacePath?: string | undefined;
}) {
  const { t } = useT();
  const [catalog, setCatalog] = useState<SubagentCatalog | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const workspacePath = props.workspacePath;

  const load = useCallback(async () => {
    setLoading(true);
    const res = await fetchSubagentCatalog(workspacePath);
    if (res.ok) {
      setCatalog(res.data);
      setError(null);
    } else {
      setError(t("subagents.error.load"));
    }
    setLoading(false);
    // `t` is stable for a locale; the workspace is what makes this a
    // different question.
  }, [workspacePath, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const items = catalog?.items ?? [];

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
        {catalog?.workspace ? (
          <p
            className="settings-field-desc subagents-workspace"
            data-testid="subagents-workspace"
          >
            {t("subagents.catalog.workspace")} <code>{catalog.workspace}</code>
          </p>
        ) : null}
        {error ? <p className="settings-error">{error}</p> : null}

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
                          title={t("subagents.badge.needsApprovalTitle", {
                            name: entry.name,
                          })}
                          data-testid={`subagent-pending-${entry.name}`}
                        >
                          {t("subagents.badge.needsApproval")}
                        </span>
                      ) : null}
                    </div>
                    {/*
                      Plain text on purpose: the description comes out of a
                      file that may have arrived with the checkout, so it is
                      never markdown and never a link.
                    */}
                    <div className="skills-list-item-desc">
                      {entry.description}
                    </div>
                    {entry.path ? (
                      <code
                        className="subagents-file"
                        data-testid={`subagent-file-${entry.name}`}
                      >
                        {entry.path}
                      </code>
                    ) : null}
                  </div>
                </div>
                <details
                  className="subagents-declared"
                  data-testid={`subagent-declared-${entry.name}`}
                >
                  <summary>{t("subagents.catalog.declared")}</summary>
                  <dl className="subagents-facts">
                    {subagentDeclaredFacts(entry).map((fact) => (
                      <div key={fact.label}>
                        <dt>{fact.label}</dt>
                        <dd>
                          <code>{fact.value}</code>
                        </dd>
                      </div>
                    ))}
                  </dl>
                </details>
              </li>
            ))}
          </ul>
        )}
      </fieldset>
    </div>
  );
}
