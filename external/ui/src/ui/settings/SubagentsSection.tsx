import { useCallback, useEffect, useId, useState } from "react";
import { Chevron } from "../components/Chevron";
import { useT } from "../i18n/I18nProvider";
import { SchemaForm, type JsonSchema } from "./SchemaForm";
import {
  scopeBadgeKey,
  subagentDeclaredFacts,
  type SubagentCatalog,
} from "./subagentCatalog";
import { fetchSubagentCatalog } from "./subagentsApi";
import { LegendWithHint } from "./FieldHint";

/**
 * SubagentsSection is the Settings -> Subagents tab. Hybrid, like the Skills
 * tab: the generated form edits the `subagents` config section, and below it
 * the catalog (/coddy/subagents) lists every definition the viewed session's
 * workspace can spawn. The list only reads. A project file awaiting approval
 * says so and names the command that approves it, which runs on the machine
 * that runs coddy - the web UI records no approvals.
 *
 * A row is the definition's name behind the app's chevron, its badges, its
 * description and its file; the chevron and the name together fold open what
 * the definition declares (model, tools, timeout, ...). Rows carry no rule
 * between them, like every other list of the drawer.
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
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const factsId = useId();
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
        groups={[
          {
            id: "subagents",
            legend: t("subagents.settings.legend"),
            description: t("subagents.settings.description"),
          },
        ]}
      />

      <fieldset
        className="settings-fieldset subagents-catalog-box"
        data-testid="subagents-catalog"
      >
        <LegendWithHint
          label={t("subagents.catalog.legend")}
          description={t("subagents.catalog.description")}
        />
        {error ? <p className="settings-error">{error}</p> : null}

        {items.length === 0 ? (
          <p className="settings-muted" data-testid="subagents-empty">
            {loading
              ? t("subagents.catalog.loading")
              : t("subagents.catalog.empty")}
          </p>
        ) : (
          <ul className="subagents-list" data-testid="subagents-list">
            {items.map((entry, index) => {
              const isOpen = !!open[entry.name];
              const facts = `${factsId}-${index}`;
              const action = isOpen
                ? t("subagents.catalog.hideDeclared")
                : t("subagents.catalog.showDeclared");
              return (
                <li
                  key={entry.name}
                  className={`subagents-item${isOpen ? " is-open" : ""}`}
                  data-testid={`subagent-row-${entry.name}`}
                >
                  <div className="subagents-item-head">
                    <button
                      type="button"
                      className="subagents-toggle"
                      aria-expanded={isOpen}
                      aria-controls={facts}
                      title={action}
                      data-testid={`subagent-toggle-${entry.name}`}
                      onClick={() =>
                        setOpen((p) => ({ ...p, [entry.name]: !isOpen }))
                      }
                    >
                      <Chevron open={isOpen} />
                      <span className="subagents-item-name">{entry.name}</span>
                    </button>
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
                  <div className="subagents-item-body">
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
                    <dl
                      id={facts}
                      className="subagents-facts"
                      data-testid={`subagent-declared-${entry.name}`}
                      hidden={!isOpen}
                    >
                      {subagentDeclaredFacts(entry).map((fact) => (
                        <div key={fact.label}>
                          <dt>{fact.label}</dt>
                          <dd>
                            <code>{fact.value}</code>
                          </dd>
                        </div>
                      ))}
                    </dl>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </fieldset>
    </div>
  );
}
