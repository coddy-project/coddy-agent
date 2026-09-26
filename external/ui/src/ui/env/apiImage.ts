import { useEffect, useState, useSyncExternalStore } from "react";

import { remoteApiRequest, snapshotEnv, subscribeEnv } from "./remoteEnv";

/**
 * apiImageRequest says how the bytes of an image the server named have to be
 * fetched: null when an <img> can load the URL itself - the local origin (its
 * session cookie goes along), a blob: or data: URL, an address elsewhere - and
 * the environment's request otherwise. Through a relay, or any remote
 * environment, the page's own origin does not serve the node's API, and an
 * <img> would ask it without the environment's token.
 */
export function apiImageRequest(url: string): { url: string; init: RequestInit } | null {
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
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const request = url ? apiImageRequest(url) : null;
  const target = request?.url ?? "";
  const [loaded, setLoaded] = useState<{ target: string; src: string } | null>(null);

  useEffect(() => {
    if (!url || !target) return;
    const req = apiImageRequest(url);
    if (!req) return;
    const abort = new AbortController();
    let objectUrl: string | null = null;
    void (async () => {
      try {
        // The mapped request is absolute and passes the fetch shim untouched;
        // a relative base URL would be mapped by the shim a second time, so
        // that one goes as the plain path and is mapped there once.
        const res = req.url.startsWith("/")
          ? await fetch(url, { signal: abort.signal })
          : await fetch(req.url, { ...req.init, signal: abort.signal });
        if (!res.ok) return;
        const blob = await res.blob();
        if (abort.signal.aborted) return;
        objectUrl = URL.createObjectURL(blob);
        setLoaded({ target: req.url, src: objectUrl });
      } catch {
        // Aborted, or unreadable: the image stays without a src.
      }
    })();
    return () => {
      abort.abort();
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [url, target, env]);

  if (!url) return undefined;
  if (!request) return url;
  return loaded?.target === target ? loaded.src : undefined;
}
