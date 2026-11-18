import type { ChangedFile } from "./types";

export type RichDiffKind = "image" | "markdown";

export function richDiffKind(
  file: Pick<ChangedFile, "path" | "image">,
): RichDiffKind | null {
  if (file.image) {
    return "image";
  }
  return /\.(md|markdown)$/i.test(file.path) ? "markdown" : null;
}

export function sessionChangeImageUrl(
  sessionId: string,
  path: string,
  side: "before" | "after",
): string {
  return `/coddy/sessions/${encodeURIComponent(sessionId)}/changes/file?path=${encodeURIComponent(path)}&side=${side}&raw=1`;
}

export function formatDiffBytes(size: number, locale: string): string {
  const units = locale.startsWith("ru")
    ? ["Б", "КБ", "МБ", "ГБ"]
    : ["B", "KB", "MB", "GB"];
  let value = Math.max(0, size);
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit++;
  }
  if (value >= 999.5 && unit < units.length - 1) {
    value /= 1000;
    unit++;
  }
  return `${new Intl.NumberFormat(locale, { maximumSignificantDigits: 3 }).format(value)} ${units[unit]}`;
}

export function formatExactDiffBytes(size: number, locale: string): string {
  const unit = locale.startsWith("ru") ? "Б" : "B";
  return `${new Intl.NumberFormat(locale).format(Math.max(0, size))} ${unit}`;
}
