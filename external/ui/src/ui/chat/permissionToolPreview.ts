import {
  flattenDiffLines,
  parseDiffPatch,
  type ParsedDiffLine,
} from "../messages/parseDiff";
import { permissionPromptDetail } from "./permissionPromptDisplay";
import type { CoddyPermissionPayload } from "./permissionTypes";
import { permissionBodyText } from "./permissionTypes";

export type PermissionToolCallContext = {
  title?: string | undefined;
  kind?: string | undefined;
  argsText?: string | undefined;
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
    "tool"
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

function questionForTool(
  toolName: string,
  args: Record<string, unknown>,
): string {
  switch (toolName.toLowerCase()) {
    case "run_command":
    case "ssh_run_command":
      return "Run this command?";
    case "write":
      return "Write this file?";
    case "edit":
      return "Edit this file?";
    case "apply_patch":
      return "Apply this patch?";
    case "mkdir":
      return "Create this directory?";
    case "touch":
      return "Create or update this file?";
    case "mv":
      return "Move this path?";
    case "rm":
      return boolArg(args, "recursive", false)
        ? "Remove this directory tree?"
        : "Remove this path?";
    case "rmdir":
      return "Remove this empty directory?";
    default:
      return "Allow this action?";
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
    "tool";
  const normalized = toolName.toLowerCase();
  const args = parseArgsText(context.argsText || "") || {};
  const title = questionForTool(normalized, args);

  if (normalized === "run_command" || normalized === "ssh_run_command") {
    const command = stringArg(args, "command") || fallback;
    const timeout = numberArg(args, "timeout_seconds", 30);
    return {
      toolName,
      title,
      header: normalized === "ssh_run_command" ? "SSH shell" : "Shell",
      meta: ["timeout " + timeout + "s"],
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
    if (boolArg(args, "replaceAll", false)) meta.push("replace all");
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

  if (normalized === "write") {
    const path = stringArg(args, "path");
    const content = stringArg(args, "content");
    return {
      toolName,
      title,
      header: path,
      meta: [content.length + " chars"],
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
      header: "Move",
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
          ? "create parents"
          : "direct parent only",
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
          ? "create parents"
          : "existing parents only",
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
      meta: recursive ? ["recursive"] : [],
      copyText: path,
      kind: "path",
    };
  }
  if (normalized === "rmdir") {
    return {
      toolName,
      title,
      header: path,
      meta: ["empty directory only"],
      copyText: path,
      kind: "path",
    };
  }

  if (normalized === "read" || normalized === "list_dir") {
    const meta: string[] = [];
    const offset = numberArg(args, "offset", 0);
    const limit = numberArg(args, "limit", 0);
    if (offset > 0) meta.push("from line " + offset);
    if (limit > 0) meta.push(limit + " lines");
    if (boolArg(args, "recursive", false)) meta.push("recursive");
    if (boolArg(args, "show_hidden", false)) meta.push("hidden files");
    return {
      toolName,
      title,
      header: stringArg(args, "path") || "Workspace",
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
    if (boolArg(args, "case_sensitive", false)) meta.push("case sensitive");
    const maxResults = numberArg(args, "max_results", 0);
    if (maxResults > 0) meta.push("max " + maxResults);
    return {
      toolName,
      title,
      header: stringArg(args, "path") || "Workspace",
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
      header: stringArg(args, "path") || "Workspace",
      meta: depth > 0 ? ["depth " + depth] : [],
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
