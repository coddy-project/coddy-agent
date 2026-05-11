/**
 * Hash routes: `#/s/<sessionId>`, `#/history`, `#/scheduler`, `#/scheduler/jobs/<job_id>`.
 * Optional `?history=1` on scheduler (and session) URLs keeps the History drawer open on wide screens.
 */

export type ParsedAppHash =
  | { branch: "none"; historyOpen: boolean }
  | { branch: "session"; sessionId: string; historyOpen: boolean }
  | { branch: "history" }
  | { branch: "scheduler"; jobId: string | null; historyOpen: boolean };

function splitHashFragment(): { path: string; search: string } {
  const raw = window.location.hash.replace(/^#\/?/, "").trim();
  const q = raw.indexOf("?");
  if (q < 0) {
    return { path: raw, search: "" };
  }
  return { path: raw.slice(0, q), search: raw.slice(q + 1) };
}

export function normalizeHashPath(): string {
  return splitHashFragment().path;
}

function historyOpenFromSearch(search: string): boolean {
  const params = new URLSearchParams(search);
  return params.get("history") === "1";
}

export function parseAppHash(): ParsedAppHash {
  const { path: h, search } = splitHashFragment();
  const historyOpen = historyOpenFromSearch(search);
  if (!h) {
    return { branch: "none", historyOpen };
  }
  if (h === "history") {
    return { branch: "history" };
  }
  const schedJob = /^scheduler\/jobs\/(.+)$/.exec(h);
  if (schedJob && schedJob[1]) {
    return {
      branch: "scheduler",
      jobId: decodeURIComponent(schedJob[1]),
      historyOpen,
    };
  }
  if (h === "scheduler") {
    return { branch: "scheduler", jobId: null, historyOpen };
  }
  const sess = /^s\/([^/]+)$/.exec(h);
  if (sess && sess[1]) {
    return {
      branch: "session",
      sessionId: decodeURIComponent(sess[1]),
      historyOpen,
    };
  }
  return { branch: "none", historyOpen };
}

export function setSessionHashInLocation(
  id: string,
  opts?: { historySidebar?: boolean },
): void {
  if (!id) {
    if (window.location.hash) {
      history.replaceState(
        null,
        "",
        `${window.location.pathname}${window.location.search}`,
      );
    }
    return;
  }
  const base = `#/s/${encodeURIComponent(id)}`;
  const next = withHistoryQuery(base, !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
  }
}

function withHistoryQuery(base: string, historySidebar: boolean): string {
  if (!historySidebar) {
    return base;
  }
  return `${base}?history=1`;
}

export function setSchedulerListHash(opts?: {
  historySidebar?: boolean;
}): void {
  const next = withHistoryQuery("#/scheduler", !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
  }
}

export function setHistoryHash(): void {
  const next = "#/history";
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
  }
}

export function setSchedulerJobHash(
  jobId: string,
  opts?: { historySidebar?: boolean },
): void {
  const base = `#/scheduler/jobs/${encodeURIComponent(jobId)}`;
  const next = withHistoryQuery(base, !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
  }
}

/** Remove `history=1` from the current hash for scheduler or session routes. */
export function stripHistorySidebarFromHash(): void {
  const p = parseAppHash();
  if (p.branch === "scheduler" && p.historyOpen) {
    if (p.jobId) {
      setSchedulerJobHash(p.jobId);
    } else {
      setSchedulerListHash();
    }
    return;
  }
  if (p.branch === "session" && p.historyOpen) {
    setSessionHashInLocation(p.sessionId);
  }
}
