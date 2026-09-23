import { memo } from "react";

import { splitPlanFileContent } from "../chat/planContent";
import { useT } from "../i18n/I18nProvider";
import { Markdown } from "../markdown/Markdown";

type Args = Record<string, unknown>;

function parseArgs(text?: string): Args | null {
  try {
    const value: unknown = JSON.parse(text || "{}");
    return value && typeof value === "object" && !Array.isArray(value)
      ? (value as Args)
      : null;
  } catch {
    return null;
  }
}

const structuredNames = new Set([
  "switch_model",
  "http_request",
  "background_list",
  "background_output",
  "background_wait",
  "background_stop",
  "background_reap",
  "preview_server",
  "coddy_docs_search",
  "plan_list",
  "plan_read",
  "plan_write",
]);

export function supportsStructuredToolCard(
  name: string,
  argsText: string | undefined,
  status: string,
): boolean {
  return (
    structuredNames.has(name.toLowerCase()) &&
    parseArgs(argsText) !== null &&
    status !== "failed" &&
    status !== "cancelled"
  );
}

function string(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function validHttpUrl(value: string): string {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:"
      ? url.href
      : "";
  } catch {
    return "";
  }
}

function safeHeader(name: string, value: string): string {
  return /(authorization|cookie|token|secret|api[-_]?key)/i.test(name)
    ? `${name}: •••`
    : `${name}: ${value}`;
}

function Fields(props: { rows: Array<[string, string]> }) {
  return props.rows.length > 0 ? (
    <dl className="scheduler-tool-fields">
      {props.rows.map(([label, value]) => (
        <div className="scheduler-tool-field" key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  ) : null;
}

function Card(props: { heading: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div
      className="permission-preview structured-tool-card"
      data-testid="structured-tool-card"
    >
      <div className="permission-preview-bar">
        <span className="permission-preview-location">{props.heading}</span>
      </div>
      {props.children ? (
        <div className="scheduler-tool-body">{props.children}</div>
      ) : null}
    </div>
  );
}

function HTTPCard(props: { args: Args; result: string }) {
  const { t, tp } = useT();
  const hasRequestBody = [
    "json",
    "form_data",
    "form",
    "body_file",
    "body_base64",
    "body",
  ].some((key) => props.args[key] !== undefined);
  const method =
    string(props.args.method).toUpperCase() ||
    (hasRequestBody ? "POST" : "GET");
  const url = string(props.args.url);
  const requestHeaders =
    props.args.headers &&
    typeof props.args.headers === "object" &&
    !Array.isArray(props.args.headers)
      ? Object.entries(props.args.headers as Args).map(([name, value]) =>
          safeHeader(name, string(value)),
        )
      : [];
  const bodyKind =
    props.args.json !== undefined
      ? "JSON"
      : props.args.form_data !== undefined
        ? "multipart"
        : props.args.form !== undefined
          ? "form"
          : props.args.body_file !== undefined
            ? t("structuredTool.file")
            : props.args.body_base64 !== undefined
              ? "base64"
              : props.args.body !== undefined
                ? t("structuredTool.text")
                : "";
  const bodyText =
    props.args.json !== undefined
      ? JSON.stringify(props.args.json)
      : typeof props.args.body === "string"
        ? props.args.body
        : "";
  const bodyBytes = bodyText ? new TextEncoder().encode(bodyText).length : null;
  const bodyDescription =
    bodyBytes === null
      ? bodyKind
      : `${bodyKind} · ${tp("structuredTool.bytes", bodyBytes)}`;
  const lines = props.result.replace(/\r\n/g, "\n").split("\n");
  const status = /^HTTP\/\S+\s+(\d{3}\s+.*)$/.exec(lines[0] || "")?.[1] || "";
  let separator = lines.indexOf("");
  if (separator < 0) separator = lines.length;
  const responseHeaders = status
    ? lines
        .slice(1, separator)
        .filter(Boolean)
        .map((line) => {
          const at = line.indexOf(":");
          return at > 0
            ? safeHeader(line.slice(0, at), line.slice(at + 1).trim())
            : line;
        })
    : [];
  const body = status
    ? lines
        .slice(separator + 1)
        .join("\n")
        .trim()
    : props.result;
  return (
    <Card heading={`${method} ${url}`}>
      <Fields
        rows={[
          ...(requestHeaders.length
            ? [
                [
                  t("structuredTool.requestHeaders"),
                  requestHeaders.join(" · "),
                ] as [string, string],
              ]
            : []),
          ...(bodyKind
            ? [
                [t("structuredTool.requestBody"), bodyDescription] as [
                  string,
                  string,
                ],
              ]
            : []),
          ...(string(props.args.output_file)
            ? [
                [
                  t("structuredTool.outputFile"),
                  string(props.args.output_file),
                ] as [string, string],
              ]
            : []),
          ...(status
            ? [[t("structuredTool.status"), status] as [string, string]]
            : []),
          ...(responseHeaders.length
            ? [
                [
                  t("structuredTool.responseHeaders"),
                  responseHeaders.join(" · "),
                ] as [string, string],
              ]
            : []),
        ]}
      />
      {body ? <pre className="tool-result-pre">{body}</pre> : null}
    </Card>
  );
}

function BackgroundCard(props: { name: string; args: Args; result: string }) {
  const { t } = useT();
  const taskId = string(props.args.task_id);
  if (props.name === "preview_server") {
    const address =
      /^Preview server started: (\S+)|^The preview server for this directory is already running: (\S+)/m.exec(
        props.result,
      );
    const url = validHttpUrl(address?.[1] || address?.[2] || "");
    const taskId = /as background task ([\w-]+)/.exec(props.result)?.[1] || "";
    return (
      <Card heading={string(props.args.path) || t("structuredTool.workspace")}>
        {url ? (
          <a href={url} target="_blank" rel="noreferrer">
            {url}
          </a>
        ) : null}
        {taskId ? <Fields rows={[[t("structuredTool.task"), taskId]]} /> : null}
        {props.result && !url ? (
          <pre className="tool-result-pre">{props.result}</pre>
        ) : null}
      </Card>
    );
  }
  const lines = props.result.split(/\r?\n/);
  const taskLine = /^(\S+) \[([^\]]+)\] (.+)$/.exec(lines[0] || "");
  const listLines =
    props.name === "background_list" || props.name === "background_reap"
      ? lines.filter(Boolean)
      : [];
  const output = taskLine ? lines.slice(1).join("\n").trim() : props.result;
  return (
    <Card heading={taskId || t("structuredTool.backgroundTasks")}>
      {typeof props.args.timeout_seconds === "number" ? (
        <Fields
          rows={[
            [t("structuredTool.timeout"), `${props.args.timeout_seconds}s`],
          ]}
        />
      ) : null}
      {taskLine && !listLines.length ? (
        <div className="scheduler-tool-row">
          {taskLine[1] !== taskId ? (
            <span className="scheduler-tool-row-id">{taskLine[1]}</span>
          ) : null}
          <span>{taskLine[2]}</span>
          <span className="scheduler-tool-muted">{taskLine[3]}</span>
        </div>
      ) : null}
      {listLines.length ? (
        <ul className="scheduler-tool-rows">
          {listLines.map((line, i) => {
            const match = /^(\S+) \[([^\]]+)\] (.+)$/.exec(line);
            return (
              <li className="scheduler-tool-row" key={i}>
                {match ? (
                  <>
                    <span className="scheduler-tool-row-id">{match[1]}</span>
                    <span>{match[2]}</span>
                    <span className="scheduler-tool-muted">{match[3]}</span>
                  </>
                ) : (
                  line
                )}
              </li>
            );
          })}
        </ul>
      ) : null}
      {output && !listLines.length ? (
        <pre className="tool-result-pre">{output}</pre>
      ) : null}
    </Card>
  );
}

function DocsSearchCard(props: { args: Args; result: string }) {
  const { t } = useT();
  const hits = [
    ...props.result.matchAll(
      /^\d+\. (\S+)  \(([^\n]+)\)\n(?:   ([^\n]+)\n?)?/gm,
    ),
  ];
  return (
    <Card heading={string(props.args.query)}>
      {hits.length ? (
        <ul className="scheduler-tool-rows">
          {hits.map((hit) => (
            <li className="scheduler-tool-row" key={hit[1]}>
              <Markdown text={`[${hit[2]}](coddy:${hit[1]})`} />
              <span className="scheduler-tool-muted">{hit[1]}</span>
              {hit[3] ? (
                <span className="scheduler-tool-row-text">{hit[3]}</span>
              ) : null}
            </li>
          ))}
        </ul>
      ) : props.result ? (
        <span className="scheduler-tool-muted">{props.result}</span>
      ) : (
        <span className="scheduler-tool-muted">{t("toolAction.running")}</span>
      )}
    </Card>
  );
}

function PlanCard(props: { name: string; args: Args; result: string }) {
  const { t } = useT();
  if (props.name === "plan_list") {
    let plans: Array<{
      slug: string;
      name?: string;
      overview?: string;
    }> | null = null;
    try {
      const parsed: unknown = JSON.parse(props.result);
      if (
        Array.isArray(parsed) &&
        parsed.every((entry) => entry && typeof entry.slug === "string")
      )
        plans = parsed;
    } catch {
      /* A text result (including the empty state) remains readable. */
    }
    return (
      <Card heading={t("structuredTool.plans")}>
        {plans ? (
          <ul className="scheduler-tool-rows">
            {plans.map((plan) => (
              <li className="scheduler-tool-row" key={plan.slug}>
                <span className="scheduler-tool-row-text">
                  {plan.name || plan.slug}
                </span>
                {plan.name ? (
                  <span className="scheduler-tool-muted">{plan.slug}</span>
                ) : null}
                {plan.overview ? (
                  <span className="scheduler-tool-row-text">
                    {plan.overview}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        ) : (
          <span className="scheduler-tool-muted">
            {props.result || t("toolAction.running")}
          </span>
        )}
      </Card>
    );
  }
  const slug = string(props.args.slug);
  if (props.name === "plan_write") {
    const { frontmatter } = splitPlanFileContent(string(props.args.content));
    const name = /^name:\s*(.+)$/m.exec(frontmatter)?.[1] || "";
    return (
      <Card heading={name || slug}>
        {name ? <Fields rows={[[t("structuredTool.slug"), slug]]} /> : null}
        {props.result ? (
          <span className="scheduler-tool-muted">{props.result}</span>
        ) : null}
      </Card>
    );
  }
  const { body } = splitPlanFileContent(props.result);
  return (
    <Card heading={slug}>
      {body ? (
        <Markdown text={body} />
      ) : (
        <span className="scheduler-tool-muted">{t("toolAction.running")}</span>
      )}
    </Card>
  );
}

export const StructuredToolCard = memo(function StructuredToolCard(props: {
  name: string;
  argsText?: string | undefined;
  resultText: string;
  status: string;
}) {
  const { t } = useT();
  const args = parseArgs(props.argsText);
  if (!args || props.status === "failed" || props.status === "cancelled")
    return null;
  const name = props.name.toLowerCase();
  if (name === "switch_model") {
    const rows: Array<[string, string]> = [];
    if (string(args.model))
      rows.push([t("structuredTool.model"), string(args.model)]);
    if (string(args.reasoning))
      rows.push([t("structuredTool.reasoning"), string(args.reasoning)]);
    rows.push([
      t("structuredTool.scope"),
      string(args.scope) === "session"
        ? t("structuredTool.session")
        : t("structuredTool.turn"),
    ]);
    return (
      <Card heading={t("structuredTool.modelChange")}>
        <Fields rows={rows} />
        {props.resultText ? (
          <span className="scheduler-tool-muted">{props.resultText}</span>
        ) : null}
      </Card>
    );
  }
  if (name === "http_request")
    return <HTTPCard args={args} result={props.resultText} />;
  if (
    [
      "background_list",
      "background_output",
      "background_wait",
      "background_stop",
      "background_reap",
      "preview_server",
    ].includes(name)
  )
    return <BackgroundCard name={name} args={args} result={props.resultText} />;
  if (name === "coddy_docs_search")
    return <DocsSearchCard args={args} result={props.resultText} />;
  if (["plan_list", "plan_read", "plan_write"].includes(name))
    return <PlanCard name={name} args={args} result={props.resultText} />;
  return null;
});
