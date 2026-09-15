import type { SessionRow } from "./types";

/**
 * How the History drawer divides the list it was handed. Grouping is a client
 * concern: the server answers a flat, ordered page and the drawer decides where
 * the headings fall, so switching the mode costs no request and never reorders
 * what the server sorted inside a group.
 */
export type SessionGroupMode = "none" | "time" | "workspace" | "tag";

export const SESSION_GROUP_MODES: readonly SessionGroupMode[] = [
  "none",
  "time",
  "workspace",
  "tag",
];

export function isSessionGroupMode(value: string): value is SessionGroupMode {
  return (SESSION_GROUP_MODES as readonly string[]).includes(value);
}

/**
 * One heading and the rows under it. `labelKey` names a bucket whose title is
 * translated (the age buckets, "no workspace"); `label` carries a value that is
 * the user's own text (a folder name, a tag) and is never translated.
 */
export type SessionGroup = {
  key: string;
  labelKey?: string;
  label?: string;
  /**
   * The workspace this heading stands for, when it stands for one. It is the
   * full path rather than the name shown, so a control on the heading can put
   * a new session in that folder.
   */
  workspacePath?: string;
  /**
   * A second line under the heading, set only when the name alone does not
   * identify it - two checkouts called "one" are two projects a reader cannot
   * tell apart without the path.
   */
  subLabel?: string;
  rows: SessionRow[];
};

/** Local midnight of the day a timestamp falls on. */
function startOfLocalDay(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/**
 * Local midnight `days` calendar days before the day `ms` falls on.
 *
 * Subtracting 24 hours is not the same thing: a local day is 23 or 25 hours
 * long across a daylight-saving change, so a fixed span lands at 23:00 or 01:00
 * and puts the hours on either side of it in the wrong bucket.
 */
function startOfLocalDayBefore(ms: number, days: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - days);
  return d.getTime();
}

type AgeBucket = { key: string; labelKey: string; from: number };

/**
 * The age buckets, newest first. The boundaries are local calendar days rather
 * than fixed spans, so "yesterday" means the day before the one on screen and
 * not "twenty-five hours ago".
 */
function ageBuckets(now: number): AgeBucket[] {
  return [
    {
      key: "today",
      labelKey: "sessions.group.today",
      from: startOfLocalDay(now),
    },
    {
      key: "yesterday",
      labelKey: "sessions.group.yesterday",
      from: startOfLocalDayBefore(now, 1),
    },
    {
      key: "week",
      labelKey: "sessions.group.week",
      from: startOfLocalDayBefore(now, 6),
    },
    {
      key: "month",
      labelKey: "sessions.group.month",
      from: startOfLocalDayBefore(now, 29),
    },
    {
      key: "older",
      labelKey: "sessions.group.older",
      from: Number.NEGATIVE_INFINITY,
    },
  ];
}

/** Trailing path segment of a workspace, the name a heading shows. */
function workspaceName(cwd: string): string {
  const trimmed = cwd.replace(/[\\/]+$/, "");
  if (!trimmed) {
    return "";
  }
  const cut = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return cut >= 0 ? trimmed.slice(cut + 1) || trimmed : trimmed;
}

function rowTags(row: SessionRow): string[] {
  return Array.isArray(row.tags) ? row.tags.filter((t) => !!t.trim()) : [];
}

/** Every tag present in a list, once each, alphabetically - the filter chips. */
export function sessionTagVocabulary(rows: readonly SessionRow[]): string[] {
  const seen = new Set<string>();
  for (const row of rows) {
    for (const tag of rowTags(row)) {
      seen.add(tag);
    }
  }
  return [...seen].sort((a, b) => a.localeCompare(b));
}

function groupByTime(rows: readonly SessionRow[], now: number): SessionGroup[] {
  const buckets = ageBuckets(now);
  const byKey = new Map<string, SessionGroup>();
  const undated: SessionGroup = {
    key: "undated",
    labelKey: "sessions.group.undated",
    rows: [],
  };
  for (const row of rows) {
    const ms = Date.parse(String(row.updatedAt ?? ""));
    if (!Number.isFinite(ms)) {
      undated.rows.push(row);
      continue;
    }
    const bucket =
      buckets.find((b) => ms >= b.from) ?? buckets[buckets.length - 1];
    let group = byKey.get(bucket.key);
    if (!group) {
      group = { key: bucket.key, labelKey: bucket.labelKey, rows: [] };
      byKey.set(bucket.key, group);
    }
    group.rows.push(row);
  }
  const out: SessionGroup[] = [];
  for (const bucket of buckets) {
    const group = byKey.get(bucket.key);
    if (group) {
      out.push(group);
    }
  }
  if (undated.rows.length > 0) {
    out.push(undated);
  }
  return out;
}

function groupByWorkspace(rows: readonly SessionRow[]): SessionGroup[] {
  // Keyed by the full path, not by the folder name: two checkouts called "one"
  // are two projects, and merging them under one heading would hide that.
  const byPath = new Map<string, SessionGroup>();
  const unknown: SessionGroup = {
    key: "no-workspace",
    labelKey: "sessions.group.noWorkspace",
    rows: [],
  };
  for (const row of rows) {
    const cwd = String(row.cwd ?? "").trim();
    if (!cwd) {
      unknown.rows.push(row);
      continue;
    }
    let group = byPath.get(cwd);
    if (!group) {
      group = {
        key: `cwd:${cwd}`,
        label: workspaceName(cwd) || cwd,
        workspacePath: cwd,
        rows: [],
      };
      byPath.set(cwd, group);
    }
    group.rows.push(row);
  }
  const out = [...byPath.values()].sort((a, b) =>
    String(a.label).localeCompare(String(b.label)),
  );
  // A folder name that more than one heading carries is not enough to tell them
  // apart, so those headings - and only those - spell out their path.
  const seen = new Map<string, number>();
  for (const group of out) {
    const label = String(group.label ?? "");
    seen.set(label, (seen.get(label) ?? 0) + 1);
  }
  for (const group of out) {
    if ((seen.get(String(group.label ?? "")) ?? 0) > 1 && group.workspacePath) {
      group.subLabel = group.workspacePath;
    }
  }
  if (unknown.rows.length > 0) {
    out.push(unknown);
  }
  return out;
}

function groupByTag(rows: readonly SessionRow[]): SessionGroup[] {
  // A session with three tags is listed under all three: that is what a tag
  // view is for, and hiding it under only the first would make the other two
  // lie about what they hold.
  const byTag = new Map<string, SessionGroup>();
  const untagged: SessionGroup = {
    key: "untagged",
    labelKey: "sessions.group.untagged",
    rows: [],
  };
  for (const row of rows) {
    const tags = rowTags(row);
    if (tags.length === 0) {
      untagged.rows.push(row);
      continue;
    }
    for (const tag of tags) {
      let group = byTag.get(tag);
      if (!group) {
        group = { key: `tag:${tag}`, label: tag, rows: [] };
        byTag.set(tag, group);
      }
      group.rows.push(row);
    }
  }
  const out = [...byTag.values()].sort((a, b) =>
    String(a.label).localeCompare(String(b.label)),
  );
  if (untagged.rows.length > 0) {
    out.push(untagged);
  }
  return out;
}

/**
 * Divides rows into the headings `mode` asks for. The order inside a group is
 * the order it was handed - the server decided that - and a bucket nothing
 * falls into is not rendered at all.
 *
 * Pinned conversations are lifted out first, into one group above everything
 * else. They are a single list the operator keeps by hand, not a stripe running
 * through every group: a pin repeated inside its folder *and* at the top would
 * be the same conversation twice, and dragging one of the two would raise the
 * question of what the other one means.
 */
export function groupSessions(
  rows: readonly SessionRow[],
  mode: SessionGroupMode,
  now: number = Date.now(),
): SessionGroup[] {
  const pinned = rows.filter((row) => row.pinned);
  const rest = rows.filter((row) => !row.pinned);
  const groups = groupTheRest(rest, mode, now);
  if (pinned.length === 0) {
    return groups;
  }
  return [
    { key: "pinned", labelKey: "sessions.group.pinned", rows: pinned },
    ...groups,
  ];
}

function groupTheRest(
  rows: readonly SessionRow[],
  mode: SessionGroupMode,
  now: number,
): SessionGroup[] {
  switch (mode) {
    case "time":
      return groupByTime(rows, now);
    case "workspace":
      return groupByWorkspace(rows);
    case "tag":
      return groupByTag(rows);
    default:
      return rows.length === 0 ? [] : [{ key: "all", rows: [...rows] }];
  }
}

export const CODDY_SESSIONS_GROUP_COOKIE = "coddy_sessions_group";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

/** The grouping the operator last chose, or null when they never chose one. */
export function readSessionGroupCookie(): SessionGroupMode | null {
  if (typeof document === "undefined") {
    return null;
  }
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (!s.startsWith(`${CODDY_SESSIONS_GROUP_COOKIE}=`)) {
      continue;
    }
    const v = decodeURIComponent(
      s.slice(CODDY_SESSIONS_GROUP_COOKIE.length + 1).trim(),
    );
    return isSessionGroupMode(v) ? v : null;
  }
  return null;
}

export function writeSessionGroupCookie(mode: SessionGroupMode): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${CODDY_SESSIONS_GROUP_COOKIE}=${encodeURIComponent(mode)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}
