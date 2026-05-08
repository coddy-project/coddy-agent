export type TitleSuggestDeps = {
  userText: string;
  /** Resolves once the persisted session ID for PATCH is known (usually after `/v1/responses` headers). */
  sessionIdPromise: Promise<string>;
  /** Best current session id for UI (provisional id, then header id once known). */
  getPreviewSessionId?: () => string;
  /** Fires as soon as describe returns a non-empty `short`, before PATCH. */
  onShortReady?: (sessionId: string, title: string) => void;
  fetchImpl?: typeof fetch;
  onApplied?: (sessionId: string, title: string) => void;
};

async function delay(ms: number): Promise<void> {
  await new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

/** Fire-and-forget: POST `/coddy/describe` without blocking, then PATCH title when describe and session ID are ready. */
export function startSuggestSessionTitle(deps: TitleSuggestDeps): void {
  const fetchFn = deps.fetchImpl ?? fetch;
  const trimmed = deps.userText.trim();
  if (!trimmed) {
    return;
  }

  void (async () => {
    let describeRes: Response;
    try {
      describeRes = await fetchFn('/coddy/describe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: trimmed }),
      });
    } catch {
      return;
    }
    if (!describeRes.ok) {
      return;
    }
    let short = '';
    try {
      const data = (await describeRes.json()) as { short?: string };
      short = (data.short || '').trim();
    } catch {
      return;
    }
    if (!short) {
      return;
    }

    const previewId = (deps.getPreviewSessionId?.() ?? '').trim();
    if (previewId && deps.onShortReady) {
      deps.onShortReady(previewId, short);
    }

    let sid: string;
    try {
      sid = (await deps.sessionIdPromise).trim();
    } catch {
      return;
    }
    if (!sid) {
      return;
    }

    for (let attempt = 0; attempt < 40; attempt++) {
      let patchRes: Response;
      try {
        patchRes = await fetchFn(`/coddy/sessions/${encodeURIComponent(sid)}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ title: short }),
        });
      } catch {
        await delay(100);
        continue;
      }
      if (patchRes.ok) {
        deps.onApplied?.(sid, short);
        return;
      }
      if (patchRes.status !== 404) {
        return;
      }
      await delay(100);
    }
  })();
}
