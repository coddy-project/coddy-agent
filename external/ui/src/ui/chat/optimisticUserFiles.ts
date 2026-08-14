/**
 * Metadata for the optimistic user_message item built at send time
 * (see `files` on the `user_message` TranscriptItem variant).
 */
export type OptimisticUserFile = {
  name: string;
  mimeType: string;
  sizeBytes: number;
  /** Local blob URL for `image/*` files — drives the bubble thumbnail until reload. */
  previewUrl?: string;
};

/**
 * Files → optimistic bubble metadata. Image files get a `previewUrl` object URL
 * so the sent bubble renders a real thumbnail; non-image files get metadata only.
 *
 * The URL survives optimistic transcript merges, then is revoked once the
 * backend publishes the durable session thumbnail URL for the same message.
 */
export function optimisticUserFiles(files: File[]): OptimisticUserFile[] {
  return files.map((f) => {
    const base: OptimisticUserFile = {
      name: f.name,
      mimeType: f.type || "application/octet-stream",
      sizeBytes: f.size,
    };
    if (
      f.type.startsWith("image/") &&
      typeof URL !== "undefined" &&
      typeof URL.createObjectURL === "function"
    ) {
      return { ...base, previewUrl: URL.createObjectURL(f) };
    }
    return base;
  });
}
