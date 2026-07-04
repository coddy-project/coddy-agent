/**
 * Hash routes: `#/s/<sessionId>`, `#/history`, `#/scheduler`, `#/scheduler/new`,
 * `#/scheduler/jobs/<job_id>`.
 * Optional `?history=1` on scheduler (and session) URLs keeps the History drawer open on wide screens.
 */

export type ParsedAppHash =
  | { branch: "none"; historyOpen: boolean }
  | { branch: "session"; sessionId: string; historyOpen: boolean }
  | { branch: "draft"; draftId: string; historyOpen: boolean }
  | { branch: "history" }
  | {
      branch: "scheduler";
      jobId: string | null;
      createOpen: boolean;
      historyOpen: boolean;
    }
  | { branch: "settings"; historyOpen: boolean; section: string | null };

export type SchedulerEditorRoute =
  | { mode: "create" }
  | { mode: "edit"; jobId: string }
  | null;

/** Maps a parsed scheduler hash to editor state (list-only hash yields null). */
export function schedulerEditorFromParsedHash(
  p: ParsedAppHash,
): SchedulerEditorRoute {
  if (p.branch !== "scheduler") {
    return null;
  }
  if (p.createOpen) {
    return { mode: "create" };
  }
  const jid = (p.jobId || "").trim();
  if (jid) {
    return { mode: "edit", jobId: jid };
  }
  return null;
}

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

/** `history.replaceState` does not fire `hashchange`; App syncs route from hash in that listener. */
function notifyHashAfterReplaceState() {
  queueMicrotask(() => {
    const ev =
      typeof HashChangeEvent !== "undefined"
        ? new HashChangeEvent("hashchange")
        : new Event("hashchange");
    window.dispatchEvent(ev);
  });
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
  if (h === "settings") {
    return { branch: "settings", historyOpen, section: null };
  }
  const settingsSec = /^settings\/(.+)$/.exec(h);
  if (settingsSec && settingsSec[1]) {
    return {
      branch: "settings",
      historyOpen,
      section: decodeURIComponent(settingsSec[1]),
    };
  }
  const schedJob = /^scheduler\/jobs\/(.+)$/.exec(h);
  if (schedJob && schedJob[1]) {
    return {
      branch: "scheduler",
      jobId: decodeURIComponent(schedJob[1]),
      createOpen: false,
      historyOpen,
    };
  }
  if (h === "scheduler/new") {
    return {
      branch: "scheduler",
      jobId: null,
      createOpen: true,
      historyOpen,
    };
  }
  if (h === "scheduler") {
    return {
      branch: "scheduler",
      jobId: null,
      createOpen: false,
      historyOpen,
    };
  }
  const sess = /^s\/([^/]+)$/.exec(h);
  if (sess && sess[1]) {
    return {
      branch: "session",
      sessionId: decodeURIComponent(sess[1]),
      historyOpen,
    };
  }
  const draft = /^draft\/([^/]+)$/.exec(h);
  if (draft && draft[1]) {
    return {
      branch: "draft",
      draftId: decodeURIComponent(draft[1]),
      historyOpen,
    };
  }
  return { branch: "none", historyOpen };
}

export function setDraftHashInLocation(
  draftId: string,
  opts?: { historySidebar?: boolean },
): void {
  const id = draftId.trim();
  if (!id) {
    setSessionHashInLocation("", opts);
    return;
  }
  const base = `#/draft/${encodeURIComponent(id)}`;
  const next = withHistoryQuery(base, !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
    notifyHashAfterReplaceState();
  }
}

export function appNavHrefDraft(draftId: string): string {
  const id = draftId.trim();
  if (!id) {
    return "#/";
  }
  return `#/draft/${encodeURIComponent(id)}`;
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
      notifyHashAfterReplaceState();
    }
    return;
  }
  const base = `#/s/${encodeURIComponent(id)}`;
  const next = opts?.historySidebar ? `${base}?history=1` : base;
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
    notifyHashAfterReplaceState();
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
    notifyHashAfterReplaceState();
  }
}

export function setSchedulerCreateHash(opts?: {
  historySidebar?: boolean;
}): void {
  const next = withHistoryQuery("#/scheduler/new", !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
    notifyHashAfterReplaceState();
  }
}

export function setSettingsHash(opts?: {
  historySidebar?: boolean;
}): void {
  const next = withHistoryQuery("#/settings", !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
    notifyHashAfterReplaceState();
  }
}

/** `#/settings/<section>` deep link for a specific Settings tab. Empty section
 * falls back to the bare `#/settings` route. */
export function setSettingsSectionHash(
  section: string,
  opts?: { historySidebar?: boolean },
): void {
  const id = section.trim();
  if (!id) {
    setSettingsHash(opts);
    return;
  }
  const base = `#/settings/${encodeURIComponent(id)}`;
  const next = withHistoryQuery(base, !!opts?.historySidebar);
  if (window.location.hash !== next) {
    history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}${next}`,
    );
    notifyHashAfterReplaceState();
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
    notifyHashAfterReplaceState();
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
    notifyHashAfterReplaceState();
  }
}

/** Remove `history=1` from the current hash for scheduler or session routes. */
export function stripHistorySidebarFromHash(): void {
  const p = parseAppHash();
  if (p.branch === "scheduler" && p.historyOpen) {
    if (p.createOpen) {
      setSchedulerCreateHash();
    } else if (p.jobId) {
      setSchedulerJobHash(p.jobId);
    } else {
      setSchedulerListHash();
    }
    return;
  }
  if (p.branch === "session" && p.historyOpen) {
    setSessionHashInLocation(p.sessionId);
    return;
  }
  if (p.branch === "settings" && p.historyOpen) {
    if (p.section) {
      setSettingsSectionHash(p.section);
    } else {
      setSettingsHash();
    }
  }
}

/** Hash for home / new chat (no session). Middle-click opens a parallel tab. */
export function appNavHrefHome(): string {
  return "#/";
}

export function appNavHrefHistory(): string {
  return "#/history";
}

export function appNavHrefSettings(): string {
  return "#/settings";
}

export function appNavHrefSettingsSection(section: string): string {
  const id = (section || "").trim();
  return id ? `#/settings/${encodeURIComponent(id)}` : "#/settings";
}

export function appNavHrefScheduler(): string {
  return "#/scheduler";
}

export function appNavHrefSchedulerNew(): string {
  return "#/scheduler/new";
}

/** Hash to open a chat session (middle-click or Ctrl/Cmd-click in a new tab). */
export function appNavHrefSession(sessionId: string): string {
  const id = (sessionId || "").trim();
  if (!id) {
    return appNavHrefHome();
  }
  return `#/s/${encodeURIComponent(id)}`;
}

/** Hash to open a scheduler job editor (middle-click opens a new tab). */
export function appNavHrefSchedulerJob(jobId: string): string {
  const id = (jobId || "").trim();
  if (!id) {
    return appNavHrefScheduler();
  }
  return `#/scheduler/jobs/${encodeURIComponent(id)}`;
}
