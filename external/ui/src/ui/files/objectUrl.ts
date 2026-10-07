import { getEnv } from "../env/remoteEnv";
import { resourceRequest } from "./api";

export const IMAGE_BYTE_CAP = 20 * 1024 * 1024;
interface Entry {
  refs: number;
  abort: AbortController;
  promise: Promise<string>;
  url?: string;
}
const entries = new Map<string, Entry>();

/** Bounded, typed bytes through the selected environment, shared by both file
 * previews and session assets. Mounted consumers own references; none is evicted. */
export function acquireObjectUrl(
  path: string,
  cap = IMAGE_BYTE_CAP,
  safeImage = false,
): { promise: Promise<string>; release: () => void } {
  const env = getEnv();
  const key = JSON.stringify([env, path, cap, safeImage]);
  let entry = entries.get(key);
  if (!entry) {
    const abort = new AbortController();
    const created: Entry = { refs: 0, abort, promise: Promise.resolve("") };
    created.promise = (async () => {
      const res = await resourceRequest(path, { signal: abort.signal });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const length = Number(res.headers?.get("Content-Length") || 0);
      if (length > cap) {
        await res.body?.cancel();
        throw new Error("Image exceeds preview limit");
      }
      const type = res.headers?.get("Content-Type") || "";
      if (safeImage && !/^image\/(png|jpeg|gif|webp|avif)(?:;|$)/i.test(type)) {
        await res.body?.cancel();
        throw new Error("File is no longer a supported image");
      }
      let blob: Blob;
      if (res.body) {
        const reader = res.body.getReader();
        const chunks: BlobPart[] = [];
        let size = 0;
        try {
          for (;;) {
            const next = await reader.read();
            if (next.done) break;
            size += next.value.byteLength;
            if (size > cap) {
              await reader.cancel();
              throw new Error("Image exceeds preview limit");
            }
            chunks.push(new Uint8Array(next.value));
          }
        } finally {
          reader.releaseLock();
        }
        blob = new Blob(chunks, { type });
      } else {
        blob = await res.blob();
        if (blob.size > cap) throw new Error("Image exceeds preview limit");
      }
      if (abort.signal.aborted) throw new DOMException("Aborted", "AbortError");
      created.url = URL.createObjectURL(blob);
      return created.url;
    })();
    entry = created;
    entries.set(key, created);
  }
  entry.refs++;
  const lease = entry;
  let released = false;
  return {
    promise: lease.promise,
    release: () => {
      if (released) return;
      released = true;
      lease.refs--;
      if (!lease.refs) {
        lease.abort.abort();
        if (lease.url) URL.revokeObjectURL(lease.url);
        if (entries.get(key) === lease) entries.delete(key);
      }
    },
  };
}
