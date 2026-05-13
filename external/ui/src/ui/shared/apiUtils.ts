export async function readErrorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as {
      error?: { message?: string };
    };
    const m = j?.error?.message;
    if (typeof m === "string" && m.trim()) {
      return m.trim();
    }
  } catch {
    /* ignore */
  }
  return `HTTP ${res.status}`;
}

export type ApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; status: number; message: string };

export async function parseJson<T>(res: Response): Promise<ApiResult<T>> {
  if (!res.ok) {
    return {
      ok: false,
      status: res.status,
      message: await readErrorMessage(res),
    };
  }
  const data = (await res.json()) as T;
  return { ok: true, data };
}
