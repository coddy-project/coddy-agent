import { type WorkspaceScope, withWorkspaceQuery } from "./chat/workspaceScope";

export type TitleSuggestDeps = {
  userText: string;
  /**
   * The workspace of the chat being named. The server reads the slash
   * commands the text invokes from it, so a first message such as `/rpa-init`
   * is named by what that command does rather than by its token.
   */
  scope?: WorkspaceScope;
  /** Resolves once the persisted session ID for PATCH is known (usually after `/v1/responses` headers). */
  sessionIdPromise: Promise<string>;
  /** Best current session id for UI (provisional id, then header id once known). */
  getPreviewSessionId?: () => string;
  /**
   * Fires as soon as describe returns a non-empty `short`, before PATCH, with
   * the tags the same answer proposed (empty when it proposed none).
   */
  onShortReady?: (sessionId: string, title: string, tags: string[]) => void;
  /**
   * Fires once, when the placeholder the header and the History row show
   * while a chat is being named should go: right after `onShortReady` when
   * describe named the chat, alone when it named nothing or failed, and after
   * `placeholderTimeoutMs` when it has not answered yet. A name that arrives
   * after that bound is still applied through `onShortReady` and the PATCH.
   */
  onDescribeSettled?: () => void;
  /** How long the placeholder waits for describe before it gives way. */
  placeholderTimeoutMs?: number;
  fetchImpl?: typeof fetch;
  onApplied?: (sessionId: string, title: string) => void;
};

/**
 * The describe call is one short model request; past this, the placeholder
 * stops promising a name, though a late one is still taken.
 */
export const NAMING_PLACEHOLDER_TIMEOUT_MS = 30_000;

async function delay(ms: number): Promise<void> {
  await new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

/**
 * Asks the server to name the text. Resolves to null when nothing usable came
 * back: an empty `short` (a text of settings commands names nothing), a
 * refusal or a network failure.
 */
async function describe(
  fetchFn: typeof fetch,
  text: string,
  deps: TitleSuggestDeps,
): Promise<{ short: string; tags: string[] } | null> {
  try {
    const res = await fetchFn(
      deps.scope
        ? withWorkspaceQuery("/coddy/describe", deps.scope)
        : "/coddy/describe",
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(deps.scope?.headers ?? {}),
        },
        body: JSON.stringify({ text }),
      },
    );
    if (!res.ok) {
      return null;
    }
    const data = (await res.json()) as { short?: string; tags?: string[] };
    const short = (data.short || "").trim();
    if (!short) {
      return null;
    }
    const tags = Array.isArray(data.tags)
      ? data.tags.map((t) => String(t).trim()).filter(Boolean)
      : [];
    return { short, tags };
  } catch {
    return null;
  }
}

/** Fire-and-forget: POST `/coddy/describe` without blocking, then PATCH title when describe and session ID are ready. */
export function startSuggestSessionTitle(deps: TitleSuggestDeps): void {
  const fetchFn = deps.fetchImpl ?? fetch;
  const trimmed = deps.userText.trim();
  if (!trimmed) {
    // Nothing to name the chat by (attachments alone): no placeholder may
    // wait for a describe call that is never made.
    deps.onDescribeSettled?.();
    return;
  }

  let settled = false;
  const settle = () => {
    if (settled) return;
    settled = true;
    window.clearTimeout(placeholderTimer);
    deps.onDescribeSettled?.();
  };
  const placeholderTimer = window.setTimeout(
    settle,
    deps.placeholderTimeoutMs ?? NAMING_PLACEHOLDER_TIMEOUT_MS,
  );

  void (async () => {
    const described = await describe(fetchFn, trimmed, deps);
    if (described) {
      const previewId = (deps.getPreviewSessionId?.() ?? "").trim();
      if (previewId && deps.onShortReady) {
        deps.onShortReady(previewId, described.short, described.tags);
      }
    }
    settle();
    if (!described) {
      return;
    }
    const { short, tags } = described;

    let sid: string;
    try {
      sid = (await deps.sessionIdPromise).trim();
    } catch {
      return;
    }
    if (!sid) {
      return;
    }

    // Naming and filing the chat travel together: the tags came from the same
    // answer as the title, and an empty set stays out of the body rather than
    // going as [], which would clear tags the operator set by hand.
    //
    // titleIfUnpinned is what makes this a suggestion: the first turn may have
    // named the session itself - the operator in the header, or the model
    // through session_describe - and this answer, in flight since before that,
    // must not land on top of it.
    const patchBody = JSON.stringify(
      tags.length > 0
        ? { title: short, titleIfUnpinned: true, tags }
        : { title: short, titleIfUnpinned: true },
    );

    for (let attempt = 0; attempt < 40; attempt++) {
      let patchRes: Response;
      try {
        patchRes = await fetchFn(`/coddy/sessions/${encodeURIComponent(sid)}`, {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: patchBody,
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
