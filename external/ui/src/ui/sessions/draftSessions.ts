import type { SessionRow } from "./types";

const STORAGE_KEY = "coddy_draft_sessions_v1";
const DRAFT_ID_PREFIX = "draft_";

export type ClientDraftSession = {
  localId: string;
  draftText: string;
  updatedAt: string;
};

export function isClientDraftSessionId(id: string): boolean {
  return id.trim().startsWith(DRAFT_ID_PREFIX);
}

export function newClientDraftId(): string {
  const hex = crypto.randomUUID().replace(/-/g, "").slice(0, 16);
  return `${DRAFT_ID_PREFIX}${hex}`;
}

export function draftSessionTitle(draftText: string): string {
  const first = draftText.trim().split(/\r?\n/)[0]?.trim() || "";
  const preview = first.slice(0, 48);
  if (!preview) {
    return "Draft: New chat";
  }
  return `Draft: ${preview}`;
}

export function readClientDraftSessions(): ClientDraftSession[] {
  if (typeof localStorage === "undefined") {
    return [];
  }
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    const out: ClientDraftSession[] = [];
    for (const row of parsed) {
      if (!row || typeof row !== "object") continue;
      const r = row as Record<string, unknown>;
      const localId =
        typeof r.localId === "string" ? r.localId.trim() : "";
      const draftText =
        typeof r.draftText === "string" ? r.draftText : "";
      const updatedAt =
        typeof r.updatedAt === "string" ? r.updatedAt : "";
      if (!localId || !isClientDraftSessionId(localId)) continue;
      out.push({ localId, draftText, updatedAt: updatedAt || new Date().toISOString() });
    }
    return out.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  } catch {
    return [];
  }
}

export function writeClientDraftSessions(rows: ClientDraftSession[]): void {
  if (typeof localStorage === "undefined") {
    return;
  }
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(rows));
  } catch {
    // ignore quota
  }
}

export function upsertClientDraftSession(
  draft: ClientDraftSession,
): ClientDraftSession[] {
  const rows = readClientDraftSessions().filter(
    (r) => r.localId !== draft.localId,
  );
  rows.unshift(draft);
  writeClientDraftSessions(rows);
  return rows;
}

export function removeClientDraftSession(localId: string): ClientDraftSession[] {
  const rows = readClientDraftSessions().filter((r) => r.localId !== localId);
  writeClientDraftSessions(rows);
  return rows;
}

export function mergeSessionsWithDrafts(
  serverRows: SessionRow[],
  drafts: ClientDraftSession[],
): SessionRow[] {
  const serverIds = new Set(serverRows.map((s) => s.id));
  const draftRows: SessionRow[] = drafts
    .filter((d) => !serverIds.has(d.localId))
    .map((d) => ({
      id: d.localId,
      title: draftSessionTitle(d.draftText),
      updatedAt: d.updatedAt,
    }));
  return [...draftRows, ...serverRows];
}
