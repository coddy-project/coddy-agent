/**
 * What the transcript shows for a tool call whose answer has a known shape.
 *
 * The Go tools format their results as text for the model: the model switch
 * sentence, the `HTTP/2 200` block of an http request, the `bg_1 [running] ...`
 * lines of the background task pool, the preview server announcement, the
 * documentation reader's pages and hits, plan listings, the session filing and
 * the memory search answers. The row used to print that text as it was. This
 * module parses those formats into small view objects so the transcript can
 * render a structured card instead, and returns `null` (or the documented
 * fallback) for anything it does not recognise so the row keeps the raw text.
 */

import { indentJson, type JsonNode, parseJsonSource } from "./jsonSource";
import { splitPlanFileContent } from "./planContent";

export type ToolArgs = Record<string, unknown>;

/** CRLF normalisation shared by every text parser below. */
function norm(text: string): string {
  return text.replace(/\r\n/g, "\n");
}

/**
 * The text without the "..." line the transcript appends to a cut preview, so a
 * list whose rows all read still reads as rows until More brings the rest.
 */
function withoutPreviewMarker(text: string): string {
  return text.replace(/\n\.\.\.\s*$/, "");
}

/** Trimmed string for strings, "" for anything else. */
function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

/** The value as a string for scalars, null for objects, arrays and undefined. */
function scalar(v: unknown): string | null {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return null;
}

/**
 * Arguments of a call: {} for empty/undefined text, the object for a JSON
 * object, null for anything else (malformed JSON, arrays, null, scalars).
 */
export function parseToolArgs(text?: string): ToolArgs | null {
  const raw = norm(text || "").trim();
  if (!raw) return {};
  try {
    const value = JSON.parse(raw) as unknown;
    return value !== null && typeof value === "object" && !Array.isArray(value)
      ? (value as ToolArgs)
      : null;
  } catch {
    return null;
  }
}

export type ModelSwitchView = {
  model: string;
  reasoning: string;
  scope: "turn" | "session";
  /** True when read from the tool's result, false when only the request is known. */
  applied: boolean;
};

const MODEL_SWITCHED =
  /^Switched for the rest of (this turn|the session): model (.+?), reasoning (.+)\. It applies from your next request\.$/;

/** What a model switch did, from the answer when it ran, else from the request. */
export function modelSwitchView(
  args: ToolArgs,
  result: string,
): ModelSwitchView {
  const m = MODEL_SWITCHED.exec(norm(result).trim());
  if (m) {
    return {
      model: m[2] ?? "",
      reasoning: m[3] === "none offered" ? "" : (m[3] ?? ""),
      scope: m[1] === "this turn" ? "turn" : "session",
      applied: true,
    };
  }
  return {
    model: str(args.model),
    reasoning: str(args.reasoning),
    scope:
      String(args.scope).trim().toLowerCase() === "session"
        ? "session"
        : "turn",
    applied: false,
  };
}

export type HeaderView = {
  name: string;
  value: string;
  masked: boolean;
  removed: boolean;
};
export type HttpBodyKind =
  | "json"
  | "text"
  | "base64"
  | "file"
  | "form"
  | "multipart";
export type HttpRequestView = {
  method: string;
  url: string;
  headers: HeaderView[];
  body: {
    kind: HttpBodyKind;
    bytes: number | null;
    /** The body file, or the form fields as they are sent (credentials hidden). */
    detail: string;
    /** The payload itself, for a JSON (indented) or a raw text body. */
    content: string;
  } | null;
  outputFile: string;
  proxy: string;
  insecureTls: boolean;
  followRedirects: boolean;
  timeoutSeconds: number | null;
  rationale: string;
};

const SENSITIVE_HEADER =
  /(authorization|cookie|token|secret|password|api[-_]?key)/i;
// Greedy up to the first "/", "?" or "#": a raw "@" inside a password belongs to
// the userinfo, which ends at the last "@" of the authority, as Go's url.Parse reads it.
const URL_CREDENTIALS = /^([a-z][a-z0-9+.-]*:\/\/)([^/?#]*)@(.*)$/i;

/** An address with the password (or the lone token) of its userinfo hidden. */
function hideCredentials(address: string): string {
  const m = URL_CREDENTIALS.exec(address);
  if (!m) return address;
  const userinfo = m[2] ?? "";
  const colon = userinfo.indexOf(":");
  const hidden = colon >= 0 ? `${userinfo.slice(0, colon)}:•••` : "•••";
  return `${m[1]}${hidden}@${m[3]}`;
}

/** A form field as the request sends it, its value hidden when its name says secret. */
function formField(name: string, value: string): string {
  return SENSITIVE_HEADER.test(name) ? `${name}=•••` : `${name}=${value}`;
}

/**
 * The body an http request will send; the first matching kind wins. A JSON body
 * is shown from the text of the arguments when they are at hand, so a number
 * past 2^53 is not rounded; JSON.parse keeps the last of a repeated key, and so
 * does the lookup.
 */
function httpBody(args: ToolArgs, argsNode?: JsonNode): HttpRequestView["body"] {
  if (args.json !== undefined) {
    const written =
      argsNode?.kind === "object"
        ? argsNode.entries.filter((e) => e.key === "json").pop()?.value.source
        : undefined;
    return {
      kind: "json",
      bytes: null,
      detail: "",
      content:
        written !== undefined
          ? indentJson(written)
          : (JSON.stringify(args.json, null, 2) ?? ""),
    };
  }
  if (Array.isArray(args.form_data)) {
    const detail = args.form_data
      .map((part) => {
        const p =
          part !== null && typeof part === "object" ? (part as ToolArgs) : {};
        const name = str(p.name);
        const file = str(p.file);
        if (file) return `${name}=@${file}`;
        const value = scalar(p.value);
        return value === null ? name : formField(name, value);
      })
      .join(", ");
    return { kind: "multipart", bytes: null, detail, content: "" };
  }
  if (
    args.form !== null &&
    typeof args.form === "object" &&
    !Array.isArray(args.form)
  ) {
    const form = args.form as ToolArgs;
    const fields: string[] = [];
    for (const name of Object.keys(form).sort()) {
      const v = form[name];
      for (const el of Array.isArray(v) ? v : [v]) {
        const value = scalar(el);
        if (value !== null) fields.push(formField(name, value));
      }
    }
    return {
      kind: "form",
      bytes: null,
      detail: fields.join(", "),
      content: "",
    };
  }
  const bodyFile = str(args.body_file);
  if (bodyFile) {
    return { kind: "file", bytes: null, detail: bodyFile, content: "" };
  }
  const base64 = str(args.body_base64);
  if (base64) {
    const clean = base64.replace(/\s+/g, "");
    const padding = (clean.match(/=+$/) || [""])[0].length;
    return {
      kind: "base64",
      bytes: Math.floor((clean.length * 3) / 4) - padding,
      detail: "",
      content: "",
    };
  }
  if (typeof args.body === "string") {
    return {
      kind: "text",
      bytes: new TextEncoder().encode(args.body).length,
      detail: "",
      content: args.body,
    };
  }
  return null;
}

/** The url, credentials hidden, with the query object appended the way the tool builds it. */
function httpUrl(args: ToolArgs): string {
  let url = hideCredentials(str(args.url));
  const query = args.query;
  if (query === null || typeof query !== "object" || Array.isArray(query))
    return url;
  const params = new URLSearchParams();
  for (const name of Object.keys(query).sort()) {
    const v = (query as ToolArgs)[name];
    for (const el of Array.isArray(v) ? v : [v]) {
      const s = scalar(el);
      if (s !== null) params.append(name, s);
    }
  }
  const encoded = params.toString();
  if (!encoded) return url;
  // The query goes before a fragment, and only a "?" before the fragment starts one.
  const hash = url.indexOf("#");
  const base = hash < 0 ? url : url.slice(0, hash);
  const fragment = hash < 0 ? "" : url.slice(hash);
  return base + (base.includes("?") ? "&" : "?") + encoded + fragment;
}

/** The request headers, sorted, with sensitive values masked and empty ones removed. */
function httpHeaders(args: ToolArgs): HeaderView[] {
  const headers = args.headers;
  if (headers === null || typeof headers !== "object" || Array.isArray(headers))
    return [];
  const out: HeaderView[] = [];
  for (const name of Object.keys(headers).sort()) {
    const s = scalar((headers as ToolArgs)[name]);
    if (s === null) continue;
    const value = s.trim();
    const removed = value === "";
    const masked = !removed && SENSITIVE_HEADER.test(name.trim());
    out.push({
      name: name.trim(),
      value: masked || removed ? "" : value,
      masked,
      removed,
    });
  }
  return out;
}

/** The proxy as configured, `direct` in lower case, credentials hidden. */
function httpProxy(args: ToolArgs): string {
  const proxy = str(args.proxy);
  if (!proxy) return "";
  if (proxy.toLowerCase() === "direct") return "direct";
  return hideCredentials(proxy);
}

/** Everything about an http request that changes where it goes and what it sends. */
export function httpRequestView(
  args: ToolArgs,
  argsNode?: JsonNode,
): HttpRequestView {
  const body = httpBody(args, argsNode);
  const timeout = args.timeout_seconds;
  return {
    method: str(args.method).toUpperCase() || (body ? "POST" : "GET"),
    url: httpUrl(args),
    headers: httpHeaders(args),
    body,
    outputFile: str(args.output_file),
    proxy: httpProxy(args),
    insecureTls: args.verify_tls === false,
    followRedirects: args.follow_redirects === true,
    timeoutSeconds: typeof timeout === "number" && timeout > 0 ? timeout : null,
    rationale: str(args.permission_rationale),
  };
}

export type HttpExchange = {
  status: string;
  headers: HeaderView[];
  body: string;
  notes: string[];
};

const HTTP_STATUS = /^HTTP\/\S+\s+(\d{3}(?:\s.*)?)$/;
const HTTP_NOTE_PREFIXES = [
  "followed redirects: ",
  "redirect to ",
  "body: saved ",
  "binary body: ",
  "body truncated after ",
];

/** An http answer split into status line, headers, body and the tool's trailing notes. */
export function parseHttpExchange(text: string): HttpExchange | null {
  const lines = norm(text).split("\n");
  const m = HTTP_STATUS.exec(lines[0] ?? "");
  if (!m) return null;
  const headers: HeaderView[] = [];
  let i = 1;
  let truncated = false;
  for (; i < lines.length; i++) {
    const line = lines[i] ?? "";
    if (line === "...") {
      truncated = true;
      i++;
      break;
    }
    if (line === "") {
      i++;
      break;
    }
    const colon = line.indexOf(":");
    if (colon <= 0) continue;
    const name = line.slice(0, colon).trim();
    const value = line.slice(colon + 1).trim();
    const masked = SENSITIVE_HEADER.test(name);
    headers.push({ name, value: masked ? "" : value, masked, removed: false });
  }
  let body = truncated ? "..." : lines.slice(i).join("\n");
  const bodyLines = body.split("\n");
  const notes: string[] = [];
  let end = bodyLines.length;
  for (let j = bodyLines.length - 1; j >= 0; j--) {
    const line = bodyLines[j] ?? "";
    const note = /^\[(.*)\]$/.exec(line);
    if (
      line !== "" &&
      (!note || !HTTP_NOTE_PREFIXES.some((p) => (note[1] ?? "").startsWith(p)))
    ) {
      break;
    }
    if (note && line !== "") notes.unshift(note[1] ?? "");
    end = j;
  }
  body = bodyLines
    .slice(0, end)
    .join("\n")
    .replace(/^\n+|\n+$/g, "");
  return { status: (m[1] ?? "").trim(), headers, body, notes };
}

export type TaskLine = {
  id: string;
  status: string;
  label: string;
  url: string;
  detail: string;
  note: string;
};

const TASK_LINE = /^(\S+) \[([a-z_]+)\] (.*)$/;
const TASK_AT_URL = /^(.*) at (https?:\/\/\S+)$/;

/** One `bg_1 [running] label (elapsed ...) note` line of the background task pool. */
export function parseTaskLine(line: string): TaskLine | null {
  const m = TASK_LINE.exec(norm(line).trim());
  if (!m) return null;
  const rest = m[3] ?? "";
  let head = rest;
  let detail = "";
  let note = "";
  const idx = rest.lastIndexOf(" (elapsed ");
  if (idx >= 0) {
    head = rest.slice(0, idx);
    const close = rest.indexOf(")", idx);
    detail = rest.slice(idx + 2, close < 0 ? rest.length : close);
    note = close < 0 ? "" : rest.slice(close + 1).trim();
  }
  const at = TASK_AT_URL.exec(head);
  return {
    id: m[1] ?? "",
    status: m[2] ?? "",
    label: at ? (at[1] ?? "") : head,
    url: at ? (at[2] ?? "") : "",
    detail,
    note,
  };
}

export type BackgroundView =
  | { kind: "pending" }
  | { kind: "tasks"; tasks: TaskLine[] }
  | {
      kind: "task";
      task: TaskLine;
      output: string;
      earlierDropped: boolean;
      waitedSeconds: number | null;
    }
  | { kind: "reaped"; tasks: Array<{ id: string; pid: number; label: string }> }
  | { kind: "raw"; text: string };

const REAPED_LINE = /^- (\S+) \(pid (\d+)\) (.*)$/;
const REAPED_HEAD = /^Killed \d+ leftover background process group/;
const STILL_RUNNING = /^Still running after (\d+)s\./;

/** The answer of a background_* tool as a task list, one task with output, or raw text. */
export function backgroundView(name: string, result: string): BackgroundView {
  const text = norm(result);
  if (!text.trim()) return { kind: "pending" };
  if (name === "background_list") {
    if (text.trim() === "No background tasks in this session.")
      return { kind: "tasks", tasks: [] };
    const tasks: TaskLine[] = [];
    for (const line of withoutPreviewMarker(text).split("\n")) {
      if (!line.trim()) continue;
      const task = parseTaskLine(line);
      if (!task) return { kind: "raw", text };
      tasks.push(task);
    }
    return { kind: "tasks", tasks };
  }
  if (name === "background_reap") {
    if (
      text.trim() === "No leftover background processes from an earlier run."
    ) {
      return { kind: "reaped", tasks: [] };
    }
    const lines = text.split("\n");
    if (REAPED_HEAD.test(lines[0] ?? "")) {
      const tasks: Array<{ id: string; pid: number; label: string }> = [];
      let ok = true;
      for (const line of lines.slice(1)) {
        if (!line.trim()) continue;
        const m = REAPED_LINE.exec(line);
        if (!m) {
          ok = false;
          break;
        }
        tasks.push({ id: m[1] ?? "", pid: Number(m[2]), label: m[3] ?? "" });
      }
      if (ok) return { kind: "reaped", tasks };
    }
    return { kind: "raw", text };
  }
  const lines = text.split("\n");
  const task = parseTaskLine(lines[0] ?? "");
  if (!task) return { kind: "raw", text };
  const second = lines[1] ?? "";
  const wait = STILL_RUNNING.exec(second);
  const empty = lines.indexOf("");
  let output = "";
  if (empty >= 0) {
    output = lines
      .slice(empty + 1)
      .join("\n")
      .replace(/^\n+|\n+$/g, "");
    if (output === "(no output yet)") output = "";
  }
  return {
    kind: "task",
    task,
    output,
    earlierDropped: second.startsWith("(earlier output dropped"),
    waitedSeconds: wait ? Number(wait[1]) : null,
  };
}

export type PreviewServerView = {
  url: string;
  taskId: string;
  directory: string;
  reused: boolean;
  stopsAfterSeconds: number | null;
};

const PREVIEW_STARTED = /^Preview server started: (\S+)$/;
const PREVIEW_REUSED =
  /^The preview server for this directory is already running: (\S+)$/;
const PREVIEW_SERVING = /^Serving (.+) as background task (\S+?)\.$/;
const PREVIEW_STOPS = /It stops by itself (\d+)s after it started/;

/** The preview server announcement: address, task, directory and lifetime. */
export function previewServerView(result: string): PreviewServerView | null {
  const text = norm(result);
  const lines = text.split("\n");
  const first = lines[0] ?? "";
  const started = PREVIEW_STARTED.exec(first);
  const running = PREVIEW_REUSED.exec(first);
  const address = started
    ? (started[1] ?? "")
    : running
      ? (running[1] ?? "")
      : "";
  if (!address) return null;
  try {
    const u = new URL(address);
    if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  } catch {
    return null;
  }
  let directory = "";
  let taskId = "";
  for (const line of lines) {
    const m = PREVIEW_SERVING.exec(line);
    if (m) {
      directory = m[1] ?? "";
      taskId = m[2] ?? "";
      break;
    }
  }
  const stops = PREVIEW_STOPS.exec(text);
  return {
    url: address,
    taskId,
    directory,
    reused: !started,
    stopsAfterSeconds: stops ? Number(stops[1]) : null,
  };
}

export type DocsHit = { ref: string; title: string; snippet: string };

const DOCS_HIT = /^\d+\. (\S+)  \((.*)\)$/;

/** The hits of a documentation search: reference, title and the leading snippet line. */
export function parseDocsHits(result: string): DocsHit[] | null {
  const text = norm(result);
  if (!text.trim()) return null;
  if (text.trimStart().startsWith("No section of the ")) return [];
  const lines = text.split("\n");
  const hits: DocsHit[] = [];
  for (let i = 0; i < lines.length; i++) {
    const m = DOCS_HIT.exec(lines[i] ?? "");
    if (!m) continue;
    const next = lines[i + 1] ?? "";
    hits.push({
      ref: m[1] ?? "",
      title: m[2] ?? "",
      snippet: next.startsWith("   ") ? next.trim() : "",
    });
  }
  return hits.length ? hits : null;
}

export type DocsPageView = {
  title: string;
  ref: string;
  from: number;
  to: number;
  total: number;
  body: string;
  continuesAt: number | null;
};

const DOCS_PAGE_TITLE = /^\[Coddy .+ documentation\] (.+)$/;
const DOCS_PAGE_REF =
  /^reference: (\S+), lines (\d+)-(\d+) of (\d+); public address: \S+$/;
const DOCS_CONTINUES = /(^|\n)\[The (?:page|section) continues at line (\d+)/;

/** A documentation page read: title, reference, line range and the section text. */
export function parseDocsPage(result: string): DocsPageView | null {
  const lines = norm(result).split("\n");
  const title = DOCS_PAGE_TITLE.exec(lines[0] ?? "");
  const ref = DOCS_PAGE_REF.exec(lines[1] ?? "");
  if (!title || !ref) return null;
  let body = lines.slice(3).join("\n");
  let continuesAt: number | null = null;
  const cont = DOCS_CONTINUES.exec(body);
  if (cont) {
    continuesAt = Number(cont[2]);
    body = body.slice(0, cont.index);
  }
  return {
    title: title[1] ?? "",
    ref: ref[1] ?? "",
    from: Number(ref[2]),
    to: Number(ref[3]),
    total: Number(ref[4]),
    body: body.replace(/^\n+|\n+$/g, ""),
    continuesAt,
  };
}

const DOCS_CONTENTS = /^\[Coddy .+ documentation\] Contents: /;
const DOCS_CONTENT_ITEM = /^- (\S+) - (.+?): (.*)$/;

/** The documentation contents listing as Markdown links into the reader. */
export function docsContentsMarkdown(result: string): string | null {
  const lines = norm(result).split("\n");
  if (!DOCS_CONTENTS.test(lines[0] ?? "")) return null;
  let i = 1;
  while (i < lines.length && lines[i] === "") i++;
  const out: string[] = [];
  for (; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const m = DOCS_CONTENT_ITEM.exec(line);
    if (m) {
      const title = (m[2] ?? "").replace(/([\\\[\]])/g, "\\$1");
      out.push(`- [${title}](coddy:${m[1] ?? ""}): ${m[3] ?? ""}`);
    } else {
      out.push(line);
    }
  }
  return out.join("\n").replace(/\n+$/, "");
}

export type PlanListEntry = {
  slug: string;
  name: string;
  overview: string;
  updatedAt: string;
};

/** The design plan list the tool answers with, as JSON, or its "no plans" sentence. */
export function parsePlanList(result: string): PlanListEntry[] | null {
  const text = norm(result).trim();
  if (text === "No design plans in this session.") return [];
  try {
    const value = JSON.parse(text) as unknown;
    if (!Array.isArray(value)) return null;
    const out: PlanListEntry[] = [];
    for (const el of value) {
      if (el === null || typeof el !== "object" || Array.isArray(el))
        return null;
      const o = el as Record<string, unknown>;
      if (typeof o.slug !== "string") return null;
      out.push({
        slug: o.slug,
        name: typeof o.name === "string" ? o.name : "",
        overview: typeof o.overview === "string" ? o.overview : "",
        updatedAt: typeof o.updatedAt === "string" ? o.updatedAt : "",
      });
    }
    return out;
  } catch {
    return null;
  }
}

export type PlanFileView = { name: string; overview: string; body: string };

/** A frontmatter field, trimmed, with one pair of matching quotes removed. */
function frontmatterField(frontmatter: string, field: string): string {
  const m = new RegExp(`^${field}:\\s*(.*)$`, "m").exec(frontmatter);
  if (!m) return "";
  const v = (m[1] ?? "").trim();
  const first = v.charAt(0);
  if (
    v.length >= 2 &&
    (first === '"' || first === "'") &&
    v.charAt(v.length - 1) === first
  ) {
    return v.slice(1, -1);
  }
  return v;
}

/** A plan file's name and overview from its frontmatter, the markdown body as the rest. */
export function planFileView(content: string): PlanFileView {
  const { frontmatter, body } = splitPlanFileContent(content);
  return {
    name: frontmatterField(frontmatter, "name"),
    overview: frontmatterField(frontmatter, "overview"),
    body,
  };
}

export type SessionFilingView = {
  title: string;
  tags: string[];
  changed: string[];
};

/** The JSON filing a session_describe call returns. */
export function sessionFilingView(result: string): SessionFilingView | null {
  const text = norm(result).trim();
  if (!text) return null;
  try {
    const value = JSON.parse(text) as unknown;
    if (value === null || typeof value !== "object" || Array.isArray(value))
      return null;
    const o = value as Record<string, unknown>;
    if (o.object !== "session.filing") return null;
    const strings = (v: unknown): string[] =>
      Array.isArray(v)
        ? v.filter((x): x is string => typeof x === "string")
        : [];
    return {
      title: typeof o.title === "string" ? o.title : "",
      tags: strings(o.tags),
      changed: strings(o.changed),
    };
  } catch {
    return null;
  }
}

export type MemoryHit = {
  scope: string;
  score: number;
  path: string;
  snippet: string;
};

const MEMORY_HIT = /^### Hit \d+ \((\S+) score=(\d+) path=(.+)\)$/;

/** The hits of a memory search: scope, score, path and the snippet under each header. */
export function memoryHits(result: string): MemoryHit[] | null {
  const text = norm(result);
  if (!text.trim()) return null;
  if (text.trim() === "No matching memory files.") return [];
  const hits: MemoryHit[] = [];
  let current: MemoryHit | null = null;
  let snippet: string[] = [];
  const flush = () => {
    if (!current) return;
    current.snippet = snippet.join("\n").replace(/^\n+|\n+$/g, "");
    hits.push(current);
  };
  for (const line of text.split("\n")) {
    const m = MEMORY_HIT.exec(line);
    // A header the pattern does not read would fold its hit into the one above.
    if (!m && line.startsWith("### Hit ")) return null;
    if (m) {
      flush();
      current = {
        scope: m[1] ?? "",
        score: Number(m[2]),
        path: m[3] ?? "",
        snippet: "",
      };
      snippet = [];
    } else if (current) {
      snippet.push(line);
    }
  }
  flush();
  return hits.length ? hits : null;
}

export type MemoryEntry = { name: string; kind: string; size: number | null };

const MEMORY_ENTRY =
  /^- (.+) \((file|dir)\)(?: size=(\d+))?(?: modified=\S+)?$/;

/** A memory directory listing: one `- name (kind) size=N modified=...` line per entry. */
export function memoryEntries(result: string): MemoryEntry[] | null {
  const text = norm(result);
  if (!text.trim()) return null;
  if (text.trim() === "(empty directory)") return [];
  const out: MemoryEntry[] = [];
  for (const line of withoutPreviewMarker(text).split("\n")) {
    if (!line.trim()) continue;
    const m = MEMORY_ENTRY.exec(line);
    if (!m) return null;
    out.push({
      name: m[1] ?? "",
      kind: m[2] ?? "",
      size: m[3] !== undefined ? Number(m[3]) : null,
    });
  }
  return out;
}

/**
 * A JSON document: an object or an array, else undefined. Read by
 * `parseJsonSource`, so every value keeps the text the server wrote.
 */
export function parseJsonDocument(
  text: string,
): Extract<JsonNode, { kind: "object" | "array" }> | undefined {
  const node = parseJsonSource(norm(text));
  return node && (node.kind === "object" || node.kind === "array") ? node : undefined;
}

export type FieldValue =
  | { kind: "text"; text: string }
  | { kind: "literal"; text: string }
  | { kind: "list"; items: string[] }
  | { kind: "json"; text: string };
export type FieldRow = { key: string; value: FieldValue };

/** A scalar as a row shows it: the string a string encodes, any other literal as written. */
function scalarText(node: JsonNode): string | null {
  if (node.kind === "string") return node.value;
  if (node.kind === "literal") return node.source;
  return null;
}

/**
 * An object's entries as display rows: text, literals, short scalar lists, nested
 * JSON. Numbers, booleans and nested JSON are the server's own text - a snowflake
 * id past 2^53 is not rounded - and a repeated key gives a row per value.
 */
export function fieldRows(
  obj: Extract<JsonNode, { kind: "object" }>,
): FieldRow[] {
  const rows: FieldRow[] = [];
  for (const { key, value: v } of obj.entries) {
    if (v.kind === "string") {
      rows.push({ key, value: { kind: "text", text: v.value } });
    } else if (v.kind === "literal") {
      rows.push({ key, value: { kind: "literal", text: v.source } });
    } else if (v.kind === "array" && v.items.length === 0) {
      rows.push({ key, value: { kind: "literal", text: "[]" } });
    } else if (
      v.kind === "array" &&
      v.items.length <= 20 &&
      v.items.every((el) => scalarText(el) !== null && el.source !== "null")
    ) {
      rows.push({
        key,
        value: { kind: "list", items: v.items.map((el) => scalarText(el) ?? "") },
      });
    } else {
      rows.push({ key, value: { kind: "json", text: indentJson(v.source) } });
    }
  }
  return rows;
}

const MARKDOWN_HEADING = /^#{1,6}\s+\S/;
const MARKDOWN_FENCE = /^\s*```/;
const MARKDOWN_BULLET = /^\s*[-*+]\s+\S/;

/** Whether a text carries Markdown's own structure: a heading, a fence, or a list. */
export function looksLikeMarkdown(text: string): boolean {
  let bullets = 0;
  for (const line of norm(text).split("\n")) {
    if (MARKDOWN_HEADING.test(line) || MARKDOWN_FENCE.test(line)) return true;
    if (MARKDOWN_BULLET.test(line)) bullets++;
  }
  return bullets >= 2;
}
