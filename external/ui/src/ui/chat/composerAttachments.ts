/** File extension for an image MIME type ("image/svg+xml" -> "svg"). */
export function imageExtFromMime(mime: string): string {
  const wellKnown: Record<string, string> = {
    png: "png",
    jpeg: "jpg",
    gif: "gif",
    webp: "webp",
    "svg+xml": "svg",
    bmp: "bmp",
  };
  const sub = (mime.split("/")[1] || "").toLowerCase();
  return wellKnown[sub] || sub.replace(/[^a-z0-9]/g, "") || "png";
}

/** Image files carried by a clipboard **`DataTransfer`** (`kind === "file"` + `image/*` MIME). */
export function clipboardImageFiles(
  data: DataTransfer | null | undefined,
): File[] {
  if (!data || !data.items) return [];
  const out: File[] = [];
  for (const item of Array.from(data.items)) {
    if (item.kind !== "file" || !item.type.startsWith("image/")) continue;
    const f = item.getAsFile();
    if (f) out.push(f);
  }
  return out;
}

/**
 * Browsers name every clipboard image "image.png"; give pasted files
 * deterministic per-composer names so chips and session history stay unambiguous.
 */
export function renamePastedImages(
  files: File[],
  seqRef: { current: number },
): File[] {
  return files.map((f) => {
    seqRef.current += 1;
    const name = `pasted-${seqRef.current}.${imageExtFromMime(f.type)}`;
    return new File([f], name, {
      type: f.type || "image/png",
      lastModified: f.lastModified,
    });
  });
}
