/**
 * Paths an answer names, read out of its Markdown: a path written as inline
 * code (`internal/foo.go:12`), the target of a link (`[foo](internal/foo.go)`)
 * and a path in prose (see internal/foo.go). This only says what could be a
 * path; whether it is a file of the chat's workspace is the server's answer
 * (files/workspaceEntries.ts), and only a file it confirms becomes a link.
 */

/** A path and the line it names, as the Files window opens it. */
export interface FileReference {
  path: string;
  line: number | undefined;
}

export type ProsePart =
  | { type: "text"; value: string }
  | { type: "path"; value: string; path: string; line: number | undefined };

/** `:12`, `:12:7`, `:12-20`, `#L12`, `#L12-L20` at the end of a path. */
const LINE_SUFFIX = /(?::(\d+)(?::\d+)?(?:-\d+)?|#L(\d+)(?:-L?\d+)?)$/;

/** Names a project has with no extension, which a reader still calls files. */
const BARE_FILE_NAMES =
  /^(?:Makefile|GNUmakefile|Dockerfile|Containerfile|Jenkinsfile|Vagrantfile|Procfile|Gemfile|Rakefile|Brewfile|Justfile|Taskfile|README|LICENSE|LICENCE|CHANGELOG|CONTRIBUTING|NOTICE|AUTHORS|CODEOWNERS)$/i;

/** A last segment with an extension that starts with a letter. */
const WITH_EXTENSION = /^[^.].*\.[\p{L}][\p{L}\p{N}_+-]{0,15}$/u;

/** A dot file: .gitignore, .env.example. */
const DOT_FILE = /^\.[\p{L}\p{N}_][\p{L}\p{N}_.+-]*$/u;

/** A scheme in front of an address: https:, mailto:, coddy:. A drive letter is not one. */
const SCHEME = /^[a-z][a-z0-9+.-]*:/i;
const DRIVE = /^[a-z]:[\\/]/i;

/** What a segment of a path may hold; anything else is code, a glob or a template. */
const SEGMENT = /^[\p{L}\p{N}_.+@=-]+$/u;

function splitLine(text: string): FileReference {
  const match = LINE_SUFFIX.exec(text);
  if (!match) return { path: text, line: undefined };
  const line = Number(match[1] ?? match[2]);
  return {
    path: text.slice(0, match.index),
    line: Number.isFinite(line) && line > 0 ? line : undefined,
  };
}

/** The segments of a path that reads as a file: nothing but path characters, a file name last. */
function readsAsFile(path: string, needsFolder: boolean): boolean {
  if (!path || path.length > 4096) return false;
  if (path.startsWith("~") || path.includes("://")) return false;
  if (SCHEME.test(path) && !DRIVE.test(path)) return false;
  const body = DRIVE.test(path) ? path.slice(3) : path;
  const segments = body.split(/[\\/]/);
  // A leading slash (an absolute path) or "./" leaves an empty or dot segment
  // first; nothing else may be empty.
  const first = segments[0] === "" || segments[0] === "." ? 1 : 0;
  const rest = segments.slice(first);
  if (rest.length === 0) return false;
  if (needsFolder && segments.length < 2) return false;
  for (const segment of rest) {
    if (segment === "..") continue;
    if (!SEGMENT.test(segment)) return false;
  }
  const name = rest[rest.length - 1]!;
  if (name === "." || name === "..") return false;
  if (needsFolder) return WITH_EXTENSION.test(name);
  return (
    WITH_EXTENSION.test(name) ||
    DOT_FILE.test(name) ||
    BARE_FILE_NAMES.test(name) ||
    rest.length > 1
  );
}

/**
 * A path written as inline code, with the line it names: a file name alone
 * (`README.md`, `Makefile`) counts, since the workspace check decides.
 */
export function parseFileReference(text: string): FileReference | null {
  const value = text.trim();
  if (!value || /\s/.test(value)) return null;
  const ref = splitLine(value);
  return readsAsFile(ref.path, false) ? ref : null;
}

/**
 * The file a link points at: a relative or an absolute target, or a
 * `file://` address. An address with any other scheme, a protocol-relative
 * one and an anchor on the page are not files.
 */
export function localLinkReference(href: string): FileReference | null {
  let value = href.trim();
  if (!value || value.startsWith("#") || value.startsWith("//")) return null;
  if (/^file:\/\//i.test(value)) value = value.replace(/^file:\/\/[^/]*/i, "");
  else if (SCHEME.test(value) && !DRIVE.test(value)) return null;
  try {
    value = decodeURIComponent(value);
  } catch {
    return null;
  }
  const ref = splitLine(value);
  if (!ref.path || /[\n\r]/.test(ref.path)) return null;
  // A link target may hold spaces once decoded; it was written as a target.
  return readsAsFile(ref.path.replace(/ /g, "_"), false) ? ref : null;
}

/** Punctuation that closes a sentence or a bracket rather than a path. */
const LEADING = /^[([{"'«“‘<@]+/;
const TRAILING = /[)\]}"'»”’>.,;:!?]+$/;

/**
 * Cuts the paths out of a run of prose. A path in prose needs a folder and an
 * extension (internal/foo.go, ./run.sh): a word with a slash (and/or, TCP/IP)
 * or a name alone (README.md) is left as text, since prose is full of them.
 */
export function splitProsePaths(text: string): ProsePart[] {
  const parts: ProsePart[] = [];
  let pending = "";
  const flushText = (value: string) => {
    pending += value;
  };
  const pushPath = (value: string, ref: FileReference) => {
    if (pending) parts.push({ type: "text", value: pending });
    pending = "";
    parts.push({ type: "path", value, path: ref.path, line: ref.line });
  };
  for (const token of text.split(/(\s+)/)) {
    if (!token || /^\s+$/.test(token) || !token.includes("/")) {
      flushText(token);
      continue;
    }
    const lead = LEADING.exec(token)?.[0] ?? "";
    let core = token.slice(lead.length);
    // What closes the sentence or a bracket goes back to the text, a colon
    // after a line suffix included: "foo.go:12:".
    const trail = TRAILING.exec(core)?.[0] ?? "";
    core = core.slice(0, core.length - trail.length);
    const ref = core ? splitLine(core) : null;
    // A backslash in prose is an escape or a Windows path the sentence quotes
    // loosely; inline code still takes a Windows path.
    if (!ref || !readsAsFile(ref.path, true) || ref.path.includes("\\")) {
      flushText(token);
      continue;
    }
    flushText(lead);
    pushPath(core, ref);
    flushText(trail);
  }
  if (pending) parts.push({ type: "text", value: pending });
  return parts.length ? parts : [{ type: "text", value: text }];
}
