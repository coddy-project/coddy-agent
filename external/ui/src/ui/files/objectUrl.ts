import { getEnv } from "../env/remoteEnv";
import { resourceRequest } from "./api";

export const IMAGE_BYTE_CAP = 20 * 1024 * 1024;

/** The raster types an <img> of workspace bytes may be given as they are. */
export const RASTER_IMAGE_TYPE = /^image\/(png|jpeg|gif|webp|avif)(?:;|$)/i;

/** The only type the browser's PDF viewer is handed. */
export const PDF_TYPE = /^application\/pdf(?:;|$)/i;

export interface ObjectUrlOptions {
  /** Bytes past this end the read with an error (default `IMAGE_BYTE_CAP`). */
  cap?: number;
  /** The type the node must answer with; another one is refused. */
  accept?: RegExp;
  /**
   * The type the blob is given whatever the node answered: the node serves an
   * SVG as a download, and only an <img> ever receives these bytes.
   */
  as?: string;
}

interface Entry {
  refs: number;
  abort: AbortController;
  promise: Promise<string>;
  url?: string;
}
const entries = new Map<string, Entry>();
/** The bytes behind each object URL this module made, while it lives. */
const blobs = new Map<string, Blob>();

/** The bytes an object URL of this module stands for, if it still lives. */
export function objectUrlBlob(url: string): Blob | undefined {
  return blobs.get(url);
}

/** Bounded, typed bytes through the selected environment, shared by both file
 * previews and session assets. Mounted consumers own references; none is evicted. */
export function acquireObjectUrl(
  path: string,
  options: ObjectUrlOptions = {},
): { promise: Promise<string>; release: () => void } {
  const cap = options.cap ?? IMAGE_BYTE_CAP;
  const env = getEnv();
  const key = JSON.stringify([
    env,
    path,
    cap,
    options.accept?.source ?? "",
    options.as ?? "",
  ]);
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
        throw new Error("File exceeds preview limit");
      }
      const served = res.headers?.get("Content-Type") || "";
      if (options.accept && !options.accept.test(served)) {
        await res.body?.cancel();
        throw new Error("File is no longer the type it is previewed as");
      }
      const type = options.as ?? served;
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
              throw new Error("File exceeds preview limit");
            }
            chunks.push(new Uint8Array(next.value));
          }
        } finally {
          reader.releaseLock();
        }
        blob = new Blob(chunks, { type });
      } else {
        const read = await res.blob();
        if (read.size > cap) throw new Error("File exceeds preview limit");
        blob = new Blob([read], { type });
      }
      if (abort.signal.aborted) throw new DOMException("Aborted", "AbortError");
      created.url = URL.createObjectURL(blob);
      blobs.set(created.url, blob);
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
        if (lease.url) {
          URL.revokeObjectURL(lease.url);
          blobs.delete(lease.url);
        }
        if (entries.get(key) === lease) entries.delete(key);
      }
    },
  };
}
