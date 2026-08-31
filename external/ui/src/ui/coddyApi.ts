export const HDR = "X-Coddy-Session-ID";

export async function markCoddySessionActivityRead(id: string): Promise<void> {
  const t = id.trim();
  if (!t) {
    return;
  }
  try {
    await fetch(`/coddy/sessions/${encodeURIComponent(t)}`, {
      method: "PATCH",
      headers: {
        "Content-Type": "application/json",
        [HDR]: t,
      },
      body: JSON.stringify({ markActivityRead: true }),
    });
  } catch {
    // ignore
  }
}

export function randomSessionId(): string {
  const hex = [...crypto.getRandomValues(new Uint8Array(18))]
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  return `sess_${hex}`;
}

export async function fetchJSON<T>(
  path: string,
  init?: RequestInit,
): Promise<{ ok: boolean; status: number; data?: T }> {
  const res = await fetch(path, init);
  const status = res.status;
  if (!res.ok) {
    return { ok: false, status };
  }
  const data = (await res.json()) as T;
  return { ok: true, status, data };
}

export function newId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(16).slice(2)}`;
}
