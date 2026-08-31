import {
  flattenDiffLines,
  parseDiffPatch,
  type ParsedDiffLine,
} from "../messages/parseDiff";
import {
  buildTodoToolPreview,
  type TodoPlanEntry,
} from "./todoToolPreview";
import { permissionPromptDetail } from "./permissionPromptDisplay";
import type { CoddyPermissionPayload } from "./permissionTypes";
import { permissionBodyText } from "./permissionTypes";
import { t, tp } from "../i18n/i18n";

export type PermissionToolCallContext = {
  title?: string | undefined;
  kind?: string | undefined;
  argsText?: string | undefined;
  /** Final todo state captured when this tool call completed. */
  todoPlan?: TodoPlanEntry[] | undefined;
};

type PermissionPreviewBase = {
  toolName: string;
  title: string;
  header: string;
  meta: string[];
  copyText: string;
};

export type PermissionToolPreview =
  | (PermissionPreviewBase & { kind: "code"; text: string })
  | (PermissionPreviewBase & { kind: "path" })
  | (PermissionPreviewBase & {
      kind: "move";
      sourcePath: string;
      destinationPath: string;
    })
  | (PermissionPreviewBase & {
      kind: "diff";
      lines: ParsedDiffLine[];
      hunkHeaders: Array<{ at: number; text: string }>;
    })
  | (PermissionPreviewBase & {
      kind: "todo";
      variant: "item" | "plan";
      entries: TodoPlanEntry[];
    });

function normalizedToolName(value: string | undefined): string {
  return (value || "").replace(/^run:\s*/i, "").trim();
}

/** Concrete Coddy tool id, preferring the matching transcript call over its generic ACP kind. */
export function permissionPromptToolName(
  payload: CoddyPermissionPayload,
  context?: PermissionToolCallContext | undefined,
): string {
  return (
    normalizedToolName(context?.title) ||
    normalizedToolName(payload.toolCall.title) ||
    normalizedToolName(context?.kind) ||
    normalizedToolName(payload.toolCall.kind) ||
    t("messages.toolDefaultName")
  );
}

function parseArgsText(text: string): Record<string, unknown> | null {
  const raw = text.trim();
  if (!raw) return null;
  const match = /^Arguments:\s*(\{[\s\S]*\})\s*$/i.exec(raw);
  const candidate = match?.[1] || raw;
  if (!candidate.startsWith("{")) return null;
  try {
    const parsed = JSON.parse(candidate) as unknown;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

function stringArg(args: Record<string, unknown>, ...names: string[]): string {
  for (const name of names) {
    const value = args[name];
    if (typeof value === "string") return value;
  }
  return "";
}

function boolArg(
  args: Record<string, unknown>,
  name: string,
  fallback: boolean,
): boolean {
  return typeof args[name] === "boolean" ? args[name] : fallback;
}

function numberArg(
  args: Record<string, unknown>,
  name: string,
  fallback: number,
): number {
  const value = args[name];
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

/**
 * The one argument that identifies what a call acts on: the path it reads, the command it
 * runs, the pattern it searches for. Returns "" when the call takes no meaningful target
 * or its arguments have not streamed in yet. Used by the live status line (liveStatus.ts)
 * to render "Reading src/app.ts" rather than a bare verb.
 */
export function toolCallTargetText(context: PermissionToolCallContext): string {
  const toolName = (
    normalizedToolName(context.title) ||
    normalizedToolName(context.kind) ||
    ""
  ).toLowerCase();
  const args = parseArgsText(context.argsText || "");
  if (!args) {
    return "";
  }
  switch (toolName) {
    case "run_command":
    case "ssh_run_command":
      return stringArg(args, "command");
    case "grep":
    case "glob":
      return stringArg(args, "pattern");
    case "websearch":
      return stringArg(args, "query");
    case "mv":
      return stringArg(args, "src");
    case "question":
      return "";
    default:
      // read / write / edit / apply_patch / mkdir / touch / rm / rmdir / print_tree /
      // plan_* take a path; webfetch takes a url.
      return stringArg(args, "path", "filePath", "file_path", "url", "name");
  }
}

function questionForTool(
  toolName: string,
  args: Record<string, unknown>,
): string {
  switch (toolName.toLowerCase()) {
    case "run_command":
    case "ssh_run_command":
      return t("permission.question.runCommand");
    case "write":
    case "write_file":
      return t("permission.question.writeFile");
    case "edit":
      return t("permission.question.editFile");
    case "apply_patch":
      return t("permission.question.applyPatch");
    case "mkdir":
      return t("permission.question.createDirectory");
    case "touch":
      return t("permission.question.createOrUpdateFile");
    case "mv":
      return t("permission.question.movePath");
    case "rm":
      return boolArg(args, "recursive", false)
        ? t("permission.question.removeDirectoryTree")
        : t("permission.question.removePath");
    case "rmdir":
      return t("permission.question.removeEmptyDirectory");
    default:
      return t("permission.question.allowAction");
  }
}

function editDiffLines(oldString: string, newString: string): ParsedDiffLine[] {
  const oldLines = oldString === "" ? [] : oldString.split(/\r?\n/);
  const newLines = newString === "" ? [] : newString.split(/\r?\n/);
  let prefix = 0;
  while (
    prefix < oldLines.length &&
    prefix < newLines.length &&
    oldLines[prefix] === newLines[prefix]
  ) {
    prefix++;
  }
  let suffix = 0;
  while (
    suffix < oldLines.length - prefix &&
    suffix < newLines.length - prefix &&
    oldLines[oldLines.length - 1 - suffix] ===
      newLines[newLines.length - 1 - suffix]
  ) {
    suffix++;
  }

  const contextBefore = Math.min(prefix, 2);
  const contextAfter = Math.min(suffix, 2);
  const rows: ParsedDiffLine[] = [];
  for (let i = prefix - contextBefore; i < prefix; i++) {
    rows.push({
      kind: "ctx",
      oldNo: i + 1,
      newNo: i + 1,
      content: oldLines[i] || "",
    });
  }
  for (let i = prefix; i < oldLines.length - suffix; i++) {
    rows.push({
      kind: "del",
      oldNo: i + 1,
      newNo: null,
      content: oldLines[i] || "",
    });
  }
  for (let i = prefix; i < newLines.length - suffix; i++) {
    rows.push({
      kind: "add",
      oldNo: null,
      newNo: i + 1,
      content: newLines[i] || "",
    });
  }
  for (let offset = contextAfter; offset > 0; offset--) {
    const oldIndex = oldLines.length - offset;
    const newIndex = newLines.length - offset;
    rows.push({
      kind: "ctx",
      oldNo: oldIndex + 1,
      newNo: newIndex + 1,
      content: oldLines[oldIndex] || "",
    });
  }
  return rows;
}

function diffMeta(lines: ParsedDiffLine[]): string[] {
  const additions = lines.filter((line) => line.kind === "add").length;
  const deletions = lines.filter((line) => line.kind === "del").length;
  return ["+" + additions, "−" + deletions];
}

/** Tool-specific, render-ready preview shared by permission gates and transcript foldouts. */
export function buildToolCallPreview(
  context: PermissionToolCallContext,
  fallback = "",
): PermissionToolPreview {
  const toolName =
    normalizedToolName(context.title) ||
    normalizedToolName(context.kind) ||
    t("messages.toolDefaultName");
  const normalized = toolName.toLowerCase();
  const args = parseArgsText(context.argsText || "") || {};
  const title = questionForTool(normalized, args);
  const todoPreview = buildTodoToolPreview({
    toolName,
    argsText: context.argsText,
    planSnapshot: context.todoPlan,
  });
  if (todoPreview) {
    return {
      toolName,
      title,
      header: todoPreview.header,
      meta: todoPreview.meta,
      copyText: "",
      kind: "todo",
      variant: todoPreview.variant,
      entries: todoPreview.entries,
    };
  }

  if (normalized === "run_command" || normalized === "ssh_run_command") {
    const command = stringArg(args, "command") || fallback;
    const timeout = numberArg(args, "timeout_seconds", 30);
    return {
      toolName,
      title,
      header:
        normalized === "ssh_run_command"
          ? t("permission.header.sshShell")
          : t("permission.header.shell"),
      meta: [t("permission.meta.timeout", { seconds: timeout })],
      copyText: command,
      kind: "code",
      text: command,
    };
  }

  if (normalized === "apply_patch") {
    const path = stringArg(args, "path", "filePath");
    const patch = stringArg(args, "patch", "diff");
    const parsed = parseDiffPatch(patch, path);
    const lines = flattenDiffLines(parsed);
    let at = 0;
    const hunkHeaders = parsed.hunks.map((hunk) => {
      const row = { at, text: hunk.header };
      at += hunk.lines.length;
      return row;
    });
    return {
      toolName,
      title,
      header: parsed.filePath || path,
      meta: diffMeta(lines),
      copyText: patch,
      kind: "diff",
      lines,
      hunkHeaders,
    };
  }

  if (normalized === "edit") {
    const path = stringArg(args, "path");
    const oldString = stringArg(args, "oldString");
    const newString = stringArg(args, "newString");
    const lines = editDiffLines(oldString, newString);
    const meta = diffMeta(lines);
    if (boolArg(args, "replaceAll", false)) {
      meta.push(t("permission.meta.replaceAll"));
    }
    return {
      toolName,
      title,
      header: path,
      meta,
      copyText: newString,
      kind: "diff",
      lines,
      hunkHeaders: [],
    };
  }

  if (normalized === "write" || normalized === "write_file") {
    const path = stringArg(args, "path", "filePath");
    const content = stringArg(args, "content");
    return {
      toolName,
      title,
      header: path,
      meta: [tp("permission.meta.chars", content.length)],
      copyText: content,
      kind: "code",
      text: content,
    };
  }

  if (normalized === "mv") {
    const sourcePath = stringArg(args, "src");
    const destinationPath = stringArg(args, "dst");
    return {
      toolName,
      title,
      header: t("permission.header.move"),
      meta: [],
      copyText: (sourcePath + "\n" + destinationPath).trim(),
      kind: "move",
      sourcePath,
      destinationPath,
    };
  }

  const path = stringArg(args, "path");
  if (normalized === "mkdir") {
    return {
      toolName,
      title,
      header: path,
      meta: [
        boolArg(args, "parents", true)
          ? t("permission.meta.createParents")
          : t("permission.meta.directParentOnly"),
      ],
      copyText: path,
      kind: "path",
    };
  }
  if (normalized === "touch") {
    return {
      toolName,
      title,
      header: path,
      meta: [
        boolArg(args, "create_parents", true)
          ? t("permission.meta.createParents")
          : t("permission.meta.existingParentsOnly"),
      ],
      copyText: path,
      kind: "path",
    };
  }
  if (normalized === "rm") {
    const recursive = boolArg(args, "recursive", false);
    return {
      toolName,
      title,
      header: path,
      meta: recursive ? [t("permission.meta.recursive")] : [],
      copyText: path,
      kind: "path",
    };
  }
  if (normalized === "rmdir") {
    return {
      toolName,
      title,
      header: path,
      meta: [t("permission.meta.emptyDirectoryOnly")],
      copyText: path,
      kind: "path",
    };
  }

  if (normalized === "read" || normalized === "list_dir") {
    const meta: string[] = [];
    const offset = numberArg(args, "offset", 0);
    const limit = numberArg(args, "limit", 0);
    if (offset > 0) meta.push(t("permission.meta.fromLine", { line: offset }));
    if (limit > 0) meta.push(tp("permission.meta.lines", limit));
    if (boolArg(args, "recursive", false)) {
      meta.push(t("permission.meta.recursive"));
    }
    if (boolArg(args, "show_hidden", false)) {
      meta.push(t("permission.meta.hiddenFiles"));
    }
    return {
      toolName,
      title,
      header: stringArg(args, "path") || t("permission.header.workspace"),
      meta,
      copyText: stringArg(args, "path"),
      kind: "path",
    };
  }

  if (normalized === "grep" || normalized === "glob") {
    const pattern = stringArg(args, "pattern");
    const meta: string[] = [];
    const glob = stringArg(args, "glob");
    if (glob) meta.push(glob);
    if (boolArg(args, "case_sensitive", false)) {
      meta.push(t("permission.meta.caseSensitive"));
    }
    const maxResults = numberArg(args, "max_results", 0);
    if (maxResults > 0) {
      meta.push(t("permission.meta.maxResults", { count: maxResults }));
    }
    return {
      toolName,
      title,
      header: stringArg(args, "path") || t("permission.header.workspace"),
      meta,
      copyText: pattern,
      kind: "code",
      text: pattern,
    };
  }

  if (normalized === "print_tree") {
    const depth = numberArg(args, "depth", 0);
    return {
      toolName,
      title,
      header: stringArg(args, "path") || t("permission.header.workspace"),
      meta: depth > 0 ? [t("permission.meta.depth", { depth })] : [],
      copyText: stringArg(args, "path"),
      kind: "path",
    };
  }

  const text =
    fallback ||
    (Object.keys(args).length > 0 ? JSON.stringify(args, null, 2) : "");
  return {
    toolName,
    title,
    header: "",
    meta: [],
    copyText: text,
    kind: "code",
    text,
  };
}

/** Permission wrapper that can fall back to the ACP content when transcript args are absent. */
export function buildPermissionToolPreview(
  payload: CoddyPermissionPayload,
  context?: PermissionToolCallContext | undefined,
): PermissionToolPreview {
  const toolName = permissionPromptToolName(payload, context);
  const argsText = context?.argsText || permissionBodyText(payload);
  return buildToolCallPreview(
    { title: toolName, kind: context?.kind, argsText },
    permissionPromptDetail(payload),
  );
}
