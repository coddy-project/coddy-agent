import { Children, memo, type ReactNode, type Ref } from "react";

import {
  backgroundView,
  docsContentsMarkdown,
  fieldRows,
  httpRequestView,
  looksLikeMarkdown,
  memoryEntries,
  memoryHits,
  modelSwitchView,
  parseDocsHits,
  parseDocsPage,
  parseHttpExchange,
  parseJsonDocument,
  parsePlanList,
  parseToolArgs,
  planFileView,
  previewServerView,
  sessionFilingView,
  type FieldValue,
  type HeaderView,
  type HttpRequestView,
  type TaskLine,
  type ToolArgs,
} from "../chat/structuredToolDisplay";
import { useT } from "../i18n/I18nProvider";
import { Markdown } from "../markdown/Markdown";
import { docsHrefFromCoddyLink } from "../scheduler/hashRoute";
import { taskStatusLabel } from "../tasks/taskStatus";
import type { BackgroundTaskStatus } from "../tasks/types";
import { formatUtcForLocalDisplay } from "./formatMessageTime";
import { parseMcpToolName } from "./toolDisplayName";

/**
 * Built-in tools whose call reads better as a card than as its JSON arguments over
 * the raw text the tool wrote for the model. The text is parsed by
 * `chat/structuredToolDisplay.ts`; anything it does not recognise is shown as it
 * was, inside the card, so a change of format loses no information.
 */
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
  "coddy_docs_read",
  "plan_list",
  "plan_read",
  "plan_write",
  "session_describe",
  "config_get",
  "config_set",
  "config_changes",
  "config_commit",
  "config_revert",
  "config_rollback",
  "keep_result",
  "compact_context",
  "coddy_memory_save",
  "coddy_memory_read",
  "coddy_memory_list",
  "coddy_memory_search",
  "coddy_memory_delete",
  "coddy_memory_mkdir",
]);

/**
 * Whether a call renders as a structured card: a known built-in tool or a tool an
 * MCP server serves, with arguments that parse, and a call that did not fail. A
 * failed or cancelled call keeps the raw panels, where the error reads as written.
 */
export function supportsStructuredToolCard(
  name: string,
  argsText: string | undefined,
  status: string,
): boolean {
  const lower = name.toLowerCase();
  return (
    (structuredNames.has(lower) || parseMcpToolName(name) !== null) &&
    parseToolArgs(argsText) !== null &&
    status !== "failed" &&
    status !== "cancelled"
  );
}

type Row = [string, ReactNode];

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function hasContent(children: ReactNode): boolean {
  return Children.toArray(children).length > 0;
}

function fields(candidates: Array<Row | null | false>): ReactNode {
  const rows = candidates.filter((row): row is Row => !!row);
  return rows.length > 0 ? (
    <dl className="scheduler-tool-fields">
      {rows.map(([label, value], i) => (
        <div className="scheduler-tool-field" key={`${label}-${i}`}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  ) : null;
}

/** Text the tool returned as it was: a log, a response body. Monospace, like the raw panel. */
function output(text: string): ReactNode {
  return text ? <pre className="structured-tool-output">{text}</pre> : null;
}

function Muted(props: { children: ReactNode }) {
  return <div className="scheduler-tool-muted">{props.children}</div>;
}

function Mono(props: { children: ReactNode }) {
  return <code className="structured-tool-mono">{props.children}</code>;
}

function Chips(props: { items: string[] }) {
  return (
    <span className="structured-tool-chips">
      {props.items.map((item, i) => (
        <span className="structured-tool-chip" key={`${item}-${i}`}>
          {item}
        </span>
      ))}
    </span>
  );
}

function documentNode(markdown: string): ReactNode {
  return markdown.trim() ? (
    <div className="structured-tool-document">
      <Markdown text={markdown} />
    </div>
  ) : null;
}

function FieldValueView(props: { value: FieldValue }) {
  const { value } = props;
  switch (value.kind) {
    case "text":
      return <span className="structured-tool-text">{value.text}</span>;
    case "literal":
      return <Mono>{value.text}</Mono>;
    case "list":
      return <Chips items={value.items} />;
    case "json":
      return <pre className="structured-tool-output">{value.text}</pre>;
  }
}

function objectFields(value: Record<string, unknown>): ReactNode {
  return fields(
    fieldRows(value).map(
      (row): Row => [row.key, <FieldValueView value={row.value} />],
    ),
  );
}

/** The answer under the request, set apart from the arguments above it by a rule. */
function answerSection(node: ReactNode): ReactNode {
  return node ? <div className="structured-tool-answer">{node}</div> : null;
}

/**
 * What a tool answered when there is no dedicated reading of it: an object as
 * fields, other JSON pretty-printed, Markdown as a document, anything else as the
 * text it is.
 */
function resultNode(text: string): ReactNode {
  if (!text.trim()) return null;
  const json = parseJsonDocument(text);
  if (json !== undefined) {
    if (!Array.isArray(json) && Object.keys(json as object).length <= 30) {
      return objectFields(json as Record<string, unknown>);
    }
    return output(JSON.stringify(json, null, 2));
  }
  if (looksLikeMarkdown(text)) return documentNode(text);
  return output(text);
}

type BodyProps = {
  bodyRef?: Ref<HTMLDivElement> | undefined;
  bodyClassName?: string | undefined;
};

function Card(
  props: BodyProps & {
    heading: ReactNode;
    children?: ReactNode;
  },
) {
  const { t } = useT();
  return (
    <div
      className="permission-preview structured-tool-card"
      data-testid="structured-tool-card"
      role="group"
      aria-label={t("messages.toolResultAriaLabel")}
    >
      <div className="permission-preview-bar">
        <span className="permission-preview-location">{props.heading}</span>
      </div>
      {hasContent(props.children) ? (
        <div
          ref={props.bodyRef}
          className={["scheduler-tool-body", props.bodyClassName]
            .filter(Boolean)
            .join(" ")}
        >
          {props.children}
        </div>
      ) : null}
    </div>
  );
}

function headerText(header: HeaderView, removed: string): string {
  if (header.masked) return `${header.name}: •••`;
  if (header.removed) return `${header.name}: (${removed})`;
  return `${header.name}: ${header.value}`;
}

function requestBodyText(
  view: HttpRequestView,
  t: (key: string) => string,
  tp: (key: string, count: number) => string,
): string {
  const body = view.body;
  if (!body) return "";
  const kind = {
    json: "JSON",
    text: t("structuredTool.text"),
    base64: "base64",
    file: t("structuredTool.file"),
    form: "x-www-form-urlencoded",
    multipart: "multipart/form-data",
  }[body.kind];
  return [
    kind,
    body.detail,
    body.bytes === null ? "" : tp("structuredTool.bytes", body.bytes),
  ]
    .filter(Boolean)
    .join(" · ");
}

function HttpCard(props: BodyProps & { args: ToolArgs; result: string }) {
  const { t, tp } = useT();
  const view = httpRequestView(props.args);
  const removed = t("structuredTool.headerRemoved");
  const exchange = parseHttpExchange(props.result);
  return (
    <Card
      heading={`${view.method} ${view.url}`}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {fields([
        view.rationale ? [t("structuredTool.rationale"), view.rationale] : null,
        view.headers.length > 0
          ? [
              t("structuredTool.requestHeaders"),
              view.headers.map((h) => headerText(h, removed)).join(" · "),
            ]
          : null,
        view.body
          ? [t("structuredTool.requestBody"), requestBodyText(view, t, tp)]
          : null,
        view.proxy
          ? [
              t("structuredTool.proxy"),
              view.proxy === "direct" ? (
                t("structuredTool.proxyDirect")
              ) : (
                <Mono>{view.proxy}</Mono>
              ),
            ]
          : null,
        view.insecureTls
          ? [
              t("structuredTool.tls"),
              <span className="structured-tool-warning">
                {t("structuredTool.tlsNotVerified")}
              </span>,
            ]
          : null,
        view.followRedirects
          ? [
              t("structuredTool.redirects"),
              t("structuredTool.redirectsFollowed"),
            ]
          : null,
        view.timeoutSeconds !== null
          ? [t("structuredTool.timeout"), `${view.timeoutSeconds}s`]
          : null,
        view.outputFile
          ? [t("structuredTool.outputFile"), <Mono>{view.outputFile}</Mono>]
          : null,
      ])}
      {exchange
        ? answerSection(
            <>
              {fields([
                [t("structuredTool.status"), exchange.status],
                exchange.headers.length > 0
                  ? [
                      t("structuredTool.responseHeaders"),
                      exchange.headers
                        .map((h) => headerText(h, removed))
                        .join(" · "),
                    ]
                  : null,
              ])}
              {output(exchange.body)}
              {exchange.notes.map((note, i) => (
                <Muted key={i}>{note}</Muted>
              ))}
            </>,
          )
        : answerSection(output(props.result))}
    </Card>
  );
}

function StatusLabel(props: { status: string }) {
  return (
    <span className="structured-tool-status" data-status={props.status}>
      {taskStatusLabel(props.status as BackgroundTaskStatus)}
    </span>
  );
}

function TaskRow(props: { task: TaskLine; showId: boolean }) {
  const { task } = props;
  const url = task.url && /^https?:\/\//.test(task.url) ? task.url : "";
  return (
    <div className="scheduler-tool-row">
      {props.showId ? (
        <span className="scheduler-tool-row-id">{task.id}</span>
      ) : null}
      <StatusLabel status={task.status} />
      <span className="scheduler-tool-row-text">{task.label}</span>
      {url ? (
        <a href={url} target="_blank" rel="noopener noreferrer">
          {url}
        </a>
      ) : null}
      {task.detail ? (
        <span className="scheduler-tool-muted">{task.detail}</span>
      ) : null}
      {task.note ? (
        <span className="scheduler-tool-muted">{task.note}</span>
      ) : null}
    </div>
  );
}

function BackgroundCard(
  props: BodyProps & { name: string; args: ToolArgs; result: string },
) {
  const { t } = useT();
  const taskId = str(props.args.task_id);
  const view = backgroundView(props.name, props.result);
  const timeout =
    typeof props.args.timeout_seconds === "number" &&
    props.args.timeout_seconds > 0
      ? props.args.timeout_seconds
      : null;
  let body: ReactNode = null;
  switch (view.kind) {
    case "tasks":
      body =
        view.tasks.length === 0 ? (
          <Muted>{t("structuredTool.noTasks")}</Muted>
        ) : (
          <ul className="scheduler-tool-rows">
            {view.tasks.map((task) => (
              <li key={task.id}>
                <TaskRow task={task} showId />
              </li>
            ))}
          </ul>
        );
      break;
    case "task":
      body = (
        <>
          <TaskRow task={view.task} showId={view.task.id !== taskId} />
          {view.waitedSeconds !== null ? (
            <Muted>
              {t("structuredTool.stillRunning", {
                seconds: view.waitedSeconds,
              })}
            </Muted>
          ) : null}
          {view.earlierDropped ? (
            <Muted>{t("structuredTool.earlierDropped")}</Muted>
          ) : null}
          {props.name === "background_output" && !view.output ? (
            <Muted>{t("structuredTool.noOutput")}</Muted>
          ) : (
            output(view.output)
          )}
        </>
      );
      break;
    case "reaped":
      body =
        view.tasks.length === 0 ? (
          <Muted>{t("structuredTool.noLeftovers")}</Muted>
        ) : (
          <ul className="scheduler-tool-rows">
            {view.tasks.map((task) => (
              <li className="scheduler-tool-row" key={task.id}>
                <span className="scheduler-tool-row-id">{task.id}</span>
                <span className="scheduler-tool-row-text">{task.label}</span>
                <span className="scheduler-tool-muted">pid {task.pid}</span>
              </li>
            ))}
          </ul>
        );
      break;
    case "raw":
      body = output(view.text);
      break;
    case "pending":
      break;
  }
  return (
    <Card
      heading={taskId || t("structuredTool.backgroundTasks")}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {timeout !== null
        ? fields([[t("structuredTool.timeout"), `${timeout}s`]])
        : null}
      {body}
    </Card>
  );
}

function PreviewServerCard(
  props: BodyProps & { args: ToolArgs; result: string },
) {
  const { t } = useT();
  const view = previewServerView(props.result);
  return (
    <Card
      heading={
        str(props.args.path) || view?.directory || t("structuredTool.workspace")
      }
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {view ? (
        <>
          <a
            className="structured-tool-link"
            href={view.url}
            target="_blank"
            rel="noopener noreferrer"
          >
            {view.url}
          </a>
          {fields([
            view.taskId
              ? [t("structuredTool.task"), <Mono>{view.taskId}</Mono>]
              : null,
            [
              t("structuredTool.stops"),
              view.stopsAfterSeconds !== null
                ? t("structuredTool.stopsAfter", {
                    seconds: view.stopsAfterSeconds,
                  })
                : t("structuredTool.stopsManually"),
            ],
          ])}
          {view.reused ? (
            <Muted>{t("structuredTool.alreadyRunning")}</Muted>
          ) : null}
        </>
      ) : (
        output(props.result)
      )}
    </Card>
  );
}

function DocsLink(props: { reference: string; children: ReactNode }) {
  const href = docsHrefFromCoddyLink(`coddy:${props.reference}`);
  return href ? (
    <a className="md-docs-link" href={href}>
      {props.children}
    </a>
  ) : (
    <>{props.children}</>
  );
}

function DocsSearchCard(props: BodyProps & { args: ToolArgs; result: string }) {
  const { t } = useT();
  const hits = parseDocsHits(props.result);
  return (
    <Card
      heading={str(props.args.query)}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {hits === null ? (
        output(props.result)
      ) : hits.length === 0 ? (
        <Muted>{t("structuredTool.noDocsHits")}</Muted>
      ) : (
        <ul className="scheduler-tool-rows">
          {hits.map((hit) => (
            <li className="structured-tool-hit" key={hit.ref}>
              <span className="scheduler-tool-row">
                <DocsLink reference={hit.ref}>{hit.title}</DocsLink>
                <span className="scheduler-tool-muted">{hit.ref}</span>
              </span>
              {hit.snippet ? (
                <span className="scheduler-tool-row-text">{hit.snippet}</span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function DocsReadCard(props: BodyProps & { args: ToolArgs; result: string }) {
  const { t } = useT();
  const page = parseDocsPage(props.result);
  const contents = page ? null : docsContentsMarkdown(props.result);
  const requested = str(props.args.page);
  if (page) {
    return (
      <Card
        heading={page.title}
        bodyRef={props.bodyRef}
        bodyClassName={props.bodyClassName}
      >
        <div className="scheduler-tool-row">
          <DocsLink reference={page.ref}>{page.ref}</DocsLink>
          <span className="scheduler-tool-muted">
            {t("structuredTool.docsLines", {
              from: page.from,
              to: page.to,
              total: page.total,
            })}
          </span>
        </div>
        {documentNode(page.body)}
        {page.continuesAt !== null ? (
          <Muted>
            {t("structuredTool.docsContinues", { line: page.continuesAt })}
          </Muted>
        ) : null}
      </Card>
    );
  }
  return (
    <Card
      heading={requested || t("structuredTool.docsContents")}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {contents !== null ? documentNode(contents) : output(props.result)}
    </Card>
  );
}

function PlanCard(
  props: BodyProps & { name: string; args: ToolArgs; result: string },
) {
  const { t } = useT();
  const slug = str(props.args.slug);
  const body = { bodyRef: props.bodyRef, bodyClassName: props.bodyClassName };
  if (props.name === "plan_list") {
    const plans = props.result ? parsePlanList(props.result) : null;
    return (
      <Card heading={t("structuredTool.plans")} {...body}>
        {plans === null ? (
          output(props.result)
        ) : plans.length === 0 ? (
          <Muted>{t("structuredTool.noPlans")}</Muted>
        ) : (
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
                {plan.updatedAt ? (
                  <span className="scheduler-tool-muted">
                    {formatUtcForLocalDisplay(plan.updatedAt)}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </Card>
    );
  }
  if (props.name === "plan_write") {
    const plan = planFileView(str(props.args.content));
    return (
      <Card heading={plan.name || slug} {...body}>
        {fields([
          plan.name && slug
            ? [t("structuredTool.slug"), <Mono>{slug}</Mono>]
            : null,
          plan.overview ? [t("structuredTool.overview"), plan.overview] : null,
        ])}
        {props.result ? <Muted>{props.result}</Muted> : null}
      </Card>
    );
  }
  const plan = planFileView(props.result);
  // A preview cut inside a long frontmatter has no closing delimiter yet: that is
  // YAML, not Markdown, until More brings the rest.
  if (
    /^\s*---/.test(props.result) &&
    !plan.name &&
    !plan.overview &&
    plan.body.startsWith("---")
  ) {
    return (
      <Card heading={slug} {...body}>
        {output(props.result)}
      </Card>
    );
  }
  return (
    <Card heading={slug} {...body}>
      {fields([
        plan.name ? [t("structuredTool.name"), plan.name] : null,
        plan.overview ? [t("structuredTool.overview"), plan.overview] : null,
      ])}
      {documentNode(plan.body)}
    </Card>
  );
}

function SessionFilingCard(
  props: BodyProps & { args: ToolArgs; result: string },
) {
  const { t } = useT();
  const filing = sessionFilingView(props.result);
  const heading = t("structuredTool.sessionFiling");
  const body = { bodyRef: props.bodyRef, bodyClassName: props.bodyClassName };
  if (filing) {
    return (
      <Card heading={heading} {...body}>
        {fields([
          [t("structuredTool.title"), filing.title],
          filing.tags.length > 0
            ? [t("structuredTool.tags"), <Chips items={filing.tags} />]
            : null,
        ])}
        <Muted>
          {filing.changed.length > 0
            ? t("structuredTool.changed", { fields: filing.changed.join(", ") })
            : t("structuredTool.unchanged")}
        </Muted>
      </Card>
    );
  }
  return (
    <Card heading={heading} {...body}>
      {objectFields(props.args)}
      {resultNode(props.result)}
    </Card>
  );
}

function ConfigCard(props: BodyProps & { args: ToolArgs; result: string }) {
  const { t } = useT();
  // The result repeats what the call staged with secrets redacted, so once it is in
  // it replaces the arguments rather than sitting under them.
  return (
    <Card
      heading={str(props.args.path) || t("structuredTool.config")}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {props.result.trim()
        ? resultNode(props.result)
        : objectFields(props.args)}
    </Card>
  );
}

function MemoryCard(
  props: BodyProps & { name: string; args: ToolArgs; result: string },
) {
  const { t, tp } = useT();
  const body = { bodyRef: props.bodyRef, bodyClassName: props.bodyClassName };
  const path = str(props.args.path);
  switch (props.name) {
    case "coddy_memory_read":
      return (
        <Card heading={path} {...body}>
          {documentNode(props.result)}
        </Card>
      );
    case "coddy_memory_save": {
      const scope = str(props.args.scope);
      const relative = str(props.args.relative_path);
      return (
        <Card
          heading={relative ? `${scope}:${relative}` : str(props.args.title)}
          {...body}
        >
          {fields([
            relative && str(props.args.title)
              ? [t("structuredTool.title"), str(props.args.title)]
              : null,
            scope ? [t("structuredTool.memoryScope"), scope] : null,
          ])}
          {documentNode(str(props.args.body))}
          {props.result ? <Muted>{props.result}</Muted> : null}
        </Card>
      );
    }
    case "coddy_memory_search": {
      const hits = props.result ? memoryHits(props.result) : null;
      return (
        <Card heading={str(props.args.query)} {...body}>
          {hits === null ? (
            output(props.result)
          ) : hits.length === 0 ? (
            <Muted>{t("structuredTool.noMemoryHits")}</Muted>
          ) : (
            <ul className="scheduler-tool-rows">
              {hits.map((hit) => (
                <li className="structured-tool-hit" key={hit.path}>
                  <span className="scheduler-tool-row">
                    <Mono>{hit.path}</Mono>
                    <span className="scheduler-tool-muted">
                      {t("structuredTool.score", { score: hit.score })}
                    </span>
                  </span>
                  {hit.snippet ? (
                    <span className="scheduler-tool-row-text">
                      {hit.snippet}
                    </span>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </Card>
      );
    }
    case "coddy_memory_list": {
      const entries = props.result ? memoryEntries(props.result) : null;
      return (
        <Card heading={path} {...body}>
          {entries === null ? (
            output(props.result)
          ) : entries.length === 0 ? (
            <Muted>{t("structuredTool.emptyDirectory")}</Muted>
          ) : (
            <ul className="scheduler-tool-rows">
              {entries.map((entry) => (
                <li className="scheduler-tool-row" key={entry.name}>
                  <Mono>{entry.name}</Mono>
                  <span className="scheduler-tool-muted">
                    {entry.kind === "dir"
                      ? t("structuredTool.kindDir")
                      : t("structuredTool.kindFile")}
                  </span>
                  {entry.size !== null ? (
                    <span className="scheduler-tool-muted">
                      {tp("structuredTool.bytes", entry.size)}
                    </span>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </Card>
      );
    }
    default:
      return (
        <Card heading={path} {...body}>
          {props.result ? <Muted>{props.result}</Muted> : null}
        </Card>
      );
  }
}

/** A small call described by its arguments, answered with a sentence. */
function ArgumentsCard(
  props: BodyProps & { heading: string; args: ToolArgs; result: string },
) {
  return (
    <Card
      heading={props.heading}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {objectFields(props.args)}
      {props.result ? <Muted>{props.result}</Muted> : null}
    </Card>
  );
}

function McpCard(
  props: BodyProps & {
    server: string;
    tool: string;
    args: ToolArgs;
    result: string;
  },
) {
  return (
    <Card
      heading={
        <>
          <span className="structured-tool-mcp-server">{props.server}</span>
          <span className="structured-tool-mcp-tool">{props.tool}</span>
        </>
      }
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {objectFields(props.args)}
      {answerSection(resultNode(props.result))}
    </Card>
  );
}

function ModelSwitchCard(
  props: BodyProps & { args: ToolArgs; result: string },
) {
  const { t } = useT();
  const view = modelSwitchView(props.args, props.result);
  return (
    <Card
      heading={t("structuredTool.modelChange")}
      bodyRef={props.bodyRef}
      bodyClassName={props.bodyClassName}
    >
      {fields([
        view.model
          ? [t("structuredTool.model"), <Mono>{view.model}</Mono>]
          : null,
        view.reasoning ? [t("structuredTool.reasoning"), view.reasoning] : null,
        [
          t("structuredTool.scope"),
          view.scope === "session"
            ? t("structuredTool.session")
            : t("structuredTool.turn"),
        ],
      ])}
      {!view.applied && props.result ? <Muted>{props.result}</Muted> : null}
    </Card>
  );
}

export const StructuredToolCard = memo(function StructuredToolCard(
  props: BodyProps & {
    name: string;
    argsText?: string | undefined;
    resultText: string;
    status: string;
  },
) {
  const { t } = useT();
  const args = parseToolArgs(props.argsText);
  if (!args || props.status === "failed" || props.status === "cancelled")
    return null;
  const name = props.name.toLowerCase();
  const result = props.resultText;
  const body = { bodyRef: props.bodyRef, bodyClassName: props.bodyClassName };
  if (name === "switch_model")
    return <ModelSwitchCard args={args} result={result} {...body} />;
  if (name === "http_request")
    return <HttpCard args={args} result={result} {...body} />;
  if (name === "preview_server")
    return <PreviewServerCard args={args} result={result} {...body} />;
  if (name.startsWith("background_"))
    return <BackgroundCard name={name} args={args} result={result} {...body} />;
  if (name === "coddy_docs_search")
    return <DocsSearchCard args={args} result={result} {...body} />;
  if (name === "coddy_docs_read")
    return <DocsReadCard args={args} result={result} {...body} />;
  if (name.startsWith("plan_"))
    return <PlanCard name={name} args={args} result={result} {...body} />;
  if (name === "session_describe")
    return <SessionFilingCard args={args} result={result} {...body} />;
  if (name.startsWith("config_"))
    return <ConfigCard args={args} result={result} {...body} />;
  if (name.startsWith("coddy_memory_"))
    return <MemoryCard name={name} args={args} result={result} {...body} />;
  if (name === "keep_result") {
    const { path, ...rest } = args;
    return (
      <ArgumentsCard
        heading={str(path)}
        args={rest}
        result={result}
        {...body}
      />
    );
  }
  if (name === "compact_context")
    return (
      <ArgumentsCard
        heading={t("structuredTool.compaction")}
        args={args}
        result={result}
        {...body}
      />
    );
  const mcp = parseMcpToolName(props.name);
  if (mcp)
    return (
      <McpCard
        server={mcp.server}
        tool={mcp.tool}
        args={args}
        result={result}
        {...body}
      />
    );
  return null;
});
