/**
 * The documentation built into the binary (`GET /coddy/docs`,
 * `/coddy/docs/page`, `/coddy/docs/search`). The environment shim routes these
 * paths to the selected environment, so the reader shows the documentation of
 * the binary it talks to.
 */

export type DocsPageRef = { slug: string; title: string; summary?: string };

export type DocsGroup = {
  id: string;
  title: string;
  summary: string;
  pages: DocsPageRef[];
};

export type DocsContents = { version: string; groups: DocsGroup[] };

export type DocsHeading = { level: number; text: string; anchor: string };

export type DocsPage = {
  version: string;
  slug: string;
  title: string;
  summary: string;
  group: { id: string; title: string };
  anchor: string;
  markdown: string;
  headings: DocsHeading[];
  prev: DocsPageRef | null;
  next: DocsPageRef | null;
  url: string;
};

export type DocsFragment = { text: string; hit?: boolean };

export type DocsHit = {
  slug: string;
  title: string;
  group: string;
  anchor?: string;
  heading?: string;
  /** Empty for a section whose text is all in its subsections; older servers sent null. */
  snippet: DocsFragment[] | null;
};

export type DocsResult<T> =
  | { ok: true; data: T }
  | { ok: false; status: number; message: string };

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<DocsResult<T>> {
  const init: RequestInit = { headers: { Accept: "application/json" } };
  if (signal) {
    init.signal = signal;
  }
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch (e) {
    if ((e as { name?: string })?.name === "AbortError") {
      throw e;
    }
    return { ok: false, status: 0, message: String(e) };
  }
  if (!res.ok) {
    let message = `${res.status}`;
    try {
      const body = (await res.json()) as { error?: { message?: string } };
      message = body.error?.message || message;
    } catch {
      /* not JSON */
    }
    return { ok: false, status: res.status, message };
  }
  return { ok: true, data: (await res.json()) as T };
}

export function fetchDocsContents(signal?: AbortSignal) {
  return getJSON<DocsContents>("/coddy/docs", signal);
}

export function fetchDocsPage(ref: string, signal?: AbortSignal) {
  return getJSON<DocsPage>(`/coddy/docs/page?ref=${encodeURIComponent(ref)}`, signal);
}

export async function searchDocs(
  q: string,
  signal?: AbortSignal,
  limit = 20,
): Promise<DocsResult<DocsHit[]>> {
  const res = await getJSON<{ hits: DocsHit[] }>(
    `/coddy/docs/search?q=${encodeURIComponent(q)}&limit=${limit}`,
    signal,
  );
  return res.ok ? { ok: true, data: res.data.hits || [] } : res;
}
