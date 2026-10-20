import { remoteApiRequest } from "../env/remoteEnv";

/** A downloadable file a tool deliberately shared with the reader. */
export type ToolArtifact = {
  id: string;
  name: string;
  sha256: string;
  size: number;
  /** Missing when the server can no longer provide the artifact bytes. */
  url?: string;
  /** Optional richer metadata supplied by newer servers. */
  previewUrl?: string;
  sourcePath?: string;
  relativePath?: string;
  /**
   * Where the file it was copied from is now, which decides whether the card
   * opens it in the Files window: still at relativePath in the chat's
   * workspace, deleted from there, or not a file of this workspace. Absent
   * from an older server.
   */
  sourceState?: ArtifactSourceState;
};

export type ArtifactSourceState = "present" | "deleted" | "unavailable";

const SOURCE_STATES: readonly string[] = ["present", "deleted", "unavailable"];

const SHA256 = /^[a-f0-9]{64}$/i;

/**
 * Normalizes artifact metadata supplied by both tool SSE frames and persisted
 * history. A missing URL is retained: it is useful evidence that the tool
 * shared a file, and the card can explain that its download has expired.
 */
export function parseToolArtifacts(raw: unknown): ToolArtifact[] {
  if (!Array.isArray(raw)) return [];
  const artifacts: ToolArtifact[] = [];
  for (const value of raw) {
    if (!value || typeof value !== "object" || Array.isArray(value)) continue;
    const row = value as Record<string, unknown>;
    const id = typeof row.id === "string" ? row.id.trim() : "";
    const name = typeof row.name === "string" ? row.name.trim() : "";
    const sha256 = typeof row.sha256 === "string" ? row.sha256.trim() : "";
    const size = row.size;
    if (
      !id ||
      !name ||
      !SHA256.test(sha256) ||
      typeof size !== "number" ||
      !Number.isFinite(size) ||
      size < 0
    ) {
      continue;
    }
    const url = typeof row.url === "string" ? row.url.trim() : "";
    const text = (value: unknown) =>
      typeof value === "string" && value.trim() ? value.trim() : undefined;
    const previewUrl = text(row.preview_url ?? row.previewUrl);
    const sourcePath = text(row.source_path ?? row.sourcePath);
    const relativePath = text(row.relative_path ?? row.relativePath);
    const sourceState = text(row.source_state ?? row.sourceState);
    artifacts.push({
      id,
      name,
      sha256,
      size,
      ...(url ? { url } : {}),
      ...(previewUrl ? { previewUrl } : {}),
      ...(sourcePath ? { sourcePath } : {}),
      ...(relativePath ? { relativePath } : {}),
      ...(sourceState && SOURCE_STATES.includes(sourceState)
        ? { sourceState: sourceState as ArtifactSourceState }
        : {}),
    });
  }
  return artifacts;
}

function triggerDownload(url: string, name: string): void {
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.style.display = "none";
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
}

/**
 * Downloads directly on the local origin. A selected remote needs an explicit
 * fetch so its bearer token stays in a header rather than leaking into a URL.
 */
export async function downloadToolArtifact(
  artifact: ToolArtifact,
): Promise<void> {
  if (!artifact.url) throw new Error("artifact URL is unavailable");
  const request = remoteApiRequest(artifact.url);
  if (!request) {
    triggerDownload(artifact.url, artifact.name);
    return;
  }
  const response = await fetch(request.url, request.init);
  if (!response.ok)
    throw new Error(`artifact download failed (${response.status})`);
  const objectUrl = URL.createObjectURL(await response.blob());
  try {
    triggerDownload(objectUrl, artifact.name);
  } finally {
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
  }
}
