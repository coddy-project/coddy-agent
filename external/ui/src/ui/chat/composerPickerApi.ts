export type SlashRow = { name: string; description: string };

export type WorkspaceFileRow = { name: string; path_rel: string; kind: string };

/** Floating slash menu anchored to **`composer-field-wrap`** (viewport-relative). */
export type PickerFloatRect = {
  left: number;
  width: number;
  bottom: number;
  maxH: number;
};

export async function fetchSlashPage(
  sessionId: string,
  prefix: string,
  page: number,
): Promise<{ items: SlashRow[]; has_more: boolean; page: number }> {
  const sp = new URLSearchParams({
    page: String(page),
    page_size: "30",
  });
  if (prefix) {
    sp.set("prefix", prefix);
  }
  const headers: Record<string, string> = {};
  const sid = sessionId.trim();
  if (sid) {
    headers["X-Coddy-Session-ID"] = sid;
  }
  const res = await fetch(`/coddy/slash-commands?${sp.toString()}`, {
    headers,
  });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return (await res.json()) as {
    items: SlashRow[];
    has_more: boolean;
    page: number;
  };
}

export async function fetchAtPage(
  sessionId: string,
  prefix: string,
  page: number,
): Promise<{ items: WorkspaceFileRow[]; has_more: boolean }> {
  const sp = new URLSearchParams({
    page: String(page),
    page_size: "10",
    prefix,
    dirs: "true",
  });
  const headers: Record<string, string> = {};
  const sid = sessionId.trim();
  if (sid) {
    headers["X-Coddy-Session-ID"] = sid;
  }
  const res = await fetch(`/coddy/workspace/files?${sp.toString()}`, {
    headers,
  });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return (await res.json()) as {
    items: WorkspaceFileRow[];
    has_more: boolean;
  };
}

/** Built-in deterministic commands (/compact, /plugin); optional, errors return []. */
export async function fetchCommands(): Promise<SlashRow[] | null> {
  try {
    const res = await fetch("/coddy/commands");
    if (!res.ok) {
      return null;
    }
    const body = (await res.json()) as { items?: SlashRow[] };
    return body.items || [];
  } catch {
    // Built-in commands are optional; ignore fetch errors.
    return null;
  }
}
