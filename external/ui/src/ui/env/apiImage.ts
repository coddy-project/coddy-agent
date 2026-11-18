import { useEffect, useState, useSyncExternalStore } from "react";

import { remoteApiRequest, snapshotEnv, subscribeEnv } from "./remoteEnv";
import { acquireObjectUrl } from "../files/objectUrl";

/**
 * apiImageRequest says how the bytes of an image the server named have to be
 * fetched: null when an <img> can load the URL itself - the local origin (its
 * session cookie goes along), a blob: or data: URL, an address elsewhere - and
 * the environment's request otherwise. Through a relay, or any remote
 * environment, the page's own origin does not serve the node's API, and an
 * <img> would ask it without the environment's token.
 */
export function apiImageRequest(
  url: string,
): { url: string; init: RequestInit } | null {
  return url.startsWith("/") ? remoteApiRequest(url) : null;
}

/**
 * useApiImageSrc turns an image URL the server gave (a user message's
 * `preview_url` or `url`) into one an <img> can show. Where the environment has
 * to be asked, the image is fetched through it and shown from an object URL,
 * released when the image is no longer shown or the environment changes;
 * until it arrives, or when it cannot be read, there is no src at all, never a
 * broken one. The token never goes into a URL.
 */
export function useApiImageSrc(url: string | undefined): string | undefined {
  return useApiImageState(url).src;
}

export function useApiImageState(url: string | undefined): {
  src: string | undefined;
  loading: boolean;
  failed: boolean;
} {
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const request = url ? apiImageRequest(url) : null;
  const target = request?.url ?? "";
  const [result, setResult] = useState<{
    target: string;
    src?: string;
    failed: boolean;
  } | null>(null);

  useEffect(() => {
    if (!url || !target) return;
    const req = apiImageRequest(url);
    if (!req) return;
    const lease = acquireObjectUrl(url);
    let cancelled = false;
    void (async () => {
      try {
        // The mapped request is absolute and passes the fetch shim untouched;
        // a relative base URL would be mapped by the shim a second time, so
        // that one goes as the plain path and is mapped there once.
        const objectUrl = await lease.promise;
        if (cancelled) return;
        setResult({ target: req.url, src: objectUrl, failed: false });
      } catch {
        if (!cancelled) {
          setResult({ target: req.url, failed: true });
        }
      }
    })();
    return () => {
      cancelled = true;
      lease.release();
    };
  }, [url, target, env]);

  if (!url) return { src: undefined, loading: false, failed: false };
  if (!request) return { src: url, loading: false, failed: false };
  if (result?.target !== target) {
    return { src: undefined, loading: true, failed: false };
  }
  return {
    src: result.src,
    loading: !result.failed && !result.src,
    failed: result.failed,
  };
}
