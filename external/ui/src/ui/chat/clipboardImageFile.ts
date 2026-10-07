const IMAGE_MIME_BY_EXTENSION: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
};

/**
 * Clipboard providers occasionally omit a pasted screenshot's MIME type. The
 * queue API correctly rejects a non-image data URI, so infer the image type
 * from its deterministic pasted filename before generating a preview or data
 * URL. Files with an explicit MIME type and non-images stay untouched.
 */
export function normalizeClipboardImageFile(file: File): File {
  if (file.type) return file;
  const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
  const type = IMAGE_MIME_BY_EXTENSION[extension];
  if (!type) return file;
  return new File([file], file.name, {
    type,
    lastModified: file.lastModified,
  });
}
