import { useEffect, useMemo, useRef, useState } from 'react';
import { ChatScreen } from './chat/ChatScreen';
import { parseSSEBlocks } from './chat/sse';
import type { TokenUsage, TranscriptItem } from './chat/types';
import { NavRail } from './nav/NavRail';
import { SessionsSidebar } from './sessions/SessionsSidebar';
import type { SessionRow } from './sessions/types';
import { startSuggestSessionTitle } from './sessionTitleSuggest';

const HDR = 'X-Coddy-Session-ID';

type ToolCallUpdate = {
  toolCallId: string;
  title?: string;
  kind?: string;
  status?: string;
};

type ToolCallStatusUpdate = {
  toolCallId: string;
  status?: string;
  content?: Array<{ type: string; content: { type: string; text?: string } }>;
};

type ToolCallListRow = {
  toolCallId: string;
  name?: string;
  kind?: string;
  status?: string;
  startedAt?: string;
  finishedAt?: string;
  argsPreview?: string;
  resultPreview?: string;
};

type ModelInfo = { id: string; maxContextTokens?: number | undefined };

type SessionStats = {
  tokenUsageTotal?: { inputTokens: number; outputTokens: number; totalTokens: number };
};

function randomSessionId(): string {
  const hex = [...crypto.getRandomValues(new Uint8Array(18))]
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
  return `sess_${hex}`;
}

function getSessionFromHash(): string {
  const h = window.location.hash.replace(/^#\/?/, '');
  const m = /^s\/([^/]+)$/.exec(h);
  const id = m && m[1] ? m[1] : '';
  return id ? decodeURIComponent(id) : '';
}

function setSessionHash(id: string): void {
  if (!id) {
    if (window.location.hash) {
      history.replaceState(null, '', `${window.location.pathname}${window.location.search}`);
    }
    return;
  }
  const next = `#/s/${encodeURIComponent(id)}`;
  if (window.location.hash !== next) {
    history.replaceState(null, '', `${window.location.pathname}${window.location.search}${next}`);
  }
}

async function fetchJSON<T>(path: string, init?: RequestInit): Promise<{ ok: boolean; status: number; data?: T }> {
  const res = await fetch(path, init);
  const status = res.status;
  if (!res.ok) {
    return { ok: false, status };
  }
  const data = (await res.json()) as T;
  return { ok: true, status, data };
}

function newId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(16).slice(2)}`;
}

function parseRFC3339ms(s: string | undefined): number | null {
  const t = (s || '').trim();
  if (!t) return null;
  const ms = Date.parse(t);
  return Number.isFinite(ms) ? ms : null;
}

export function App() {
  const [sessionId, setSessionId] = useState('');
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [draft, setDraft] = useState('');
  const [tokenUsage, setTokenUsage] = useState<TokenUsage | null>(null);
  const tokenBaselineRef = useRef<{ input: number; output: number; total: number }>({ input: 0, output: 0, total: 0 });
  const inFlightRef = useRef(false);
  const [modelInfos, setModelInfos] = useState<ModelInfo[]>([]);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [modes, setModes] = useState<string[]>(['agent', 'plan']);
  const [mode, setMode] = useState<string>('agent');
  const [describePreview, setDescribePreview] = useState<{ sessionId: string; title: string } | null>(null);
  const currentTitle = useMemo(() => {
    if (!sessionId) {
      return 'New chat';
    }
    if (describePreview?.sessionId === sessionId) {
      const hint = describePreview.title.trim();
      if (hint) {
        return hint;
      }
    }
    const row = sessions.find((s) => s.id === sessionId);
    const t = (row?.title || '').trim();
    return t || 'New chat';
  }, [sessionId, sessions, describePreview]);

  async function saveSessionTitle(id: string, title: string) {
    const t = title.trim();
    if (!t) {
      return;
    }
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: t }),
    });
    setSessions((prev) => prev.map((s) => (s.id === id ? { ...s, title: t } : s)));
  }


  const headers = useMemo(() => (sessionId ? { [HDR]: sessionId } : {}), [sessionId]);

  useEffect(() => {
    let id = getSessionFromHash();
    setSessionId(id || '');
  }, []);

  useEffect(() => {
    void (async () => {
      const res = await fetchJSON<{ data?: Array<{ id?: string; max_context_tokens?: number }> }>('/v1/models');
      if (!res.ok || !res.data?.data) {
        return;
      }
      const rows = res.data.data
        .map((d) => ({ id: (d.id || '').trim(), ...(d.max_context_tokens !== undefined ? { maxContextTokens: d.max_context_tokens } : {}) }))
        .filter((d) => d.id);
      setModelInfos(rows);
      const ids = rows.map((d) => d.id);
      if (ids.length > 0) {
        setModes(ids);
        if (!ids.includes(mode)) {
          setMode(ids[0] || 'agent');
        }
      }
    })();
  }, []);

  useEffect(() => {
    const onHash = () => {
      const id = getSessionFromHash();
      setSessionId(id || '');
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, [sessionId]);

  useEffect(() => {
    setDescribePreview((p) => (p && p.sessionId !== sessionId ? null : p));
  }, [sessionId]);

  async function loadSessions(reset: boolean): Promise<SessionRow[] | null> {
    const ps = new URLSearchParams();
    ps.set('limit', '30');
    if (!reset && sessionsCursor) {
      ps.set('cursor', sessionsCursor);
    }
    const res = await fetchJSON<{ sessions: SessionRow[]; nextCursor?: string | null }>(`/coddy/sessions?${ps.toString()}`, {
      headers,
    });
    if (!res.ok || !res.data) {
      setSessionsError(`Backend is unavailable (${res.status})`);
      return null;
    }
    setSessionsError(null);
    const next = res.data.sessions || [];
    setSessions((prev) => {
      if (reset) {
        return next;
      }
      const seen = new Set(prev.map((s) => s.id));
      return [...prev, ...next.filter((s) => !seen.has(s.id))];
    });
    setSessionsCursor(res.data.nextCursor ?? null);
    return next;
  }

  async function loadMessages(idOverride?: string): Promise<boolean> {
    const sid = (idOverride ?? sessionId).trim();
    if (!sid) {
      setItems([]);
      return false;
    }
    const res = await fetchJSON<{ messages: Array<any> }>(
      `/coddy/sessions/${encodeURIComponent(sid)}/messages`,
      { headers: sid === sessionId ? headers : { [HDR]: sid } },
    );
    if (!res.ok || !res.data) {
      setItems([]);
      return false;
    }
    const next: TranscriptItem[] = [];
    const toolIdx = new Map<string, number>();
    for (const m of res.data.messages || []) {
      const role = (m.role || '').trim();
      if (role === 'user') {
        next.push({ id: newId('u'), type: 'user_message', content: m.content || '' });
        continue;
      }
      if (role === 'assistant') {
        const reasoning = (m.reasoning || '').trim();
        if (reasoning) {
          next.push({ id: newId('r'), type: 'thinking', status: 'completed', content: reasoning });
        }
        const content = m.content || '';
        if (content) {
          next.push({ id: newId('a'), type: 'assistant_message', content });
        }
        const tcs = Array.isArray(m.tool_calls) ? m.tool_calls : [];
        for (const tc of tcs) {
          const id = tc?.id || '';
          const fn = tc?.function || {};
          const name = (fn?.name || '').trim();
          const args = fn?.arguments || '';
          if (!id) continue;
          if (toolIdx.has(id)) continue;
          const it: Extract<TranscriptItem, { type: 'tool_call' }> = {
            id: newId('t'),
            type: 'tool_call',
            toolCallId: id,
            status: 'pending',
            detailsLoaded: false,
          };
          if (name) it.title = name;
          if (args) it.argsText = args;
          toolIdx.set(id, next.length);
          next.push(it);
        }
        continue;
      }
      if (role === 'tool') {
        const id = (m.tool_call_id || '').trim();
        if (!id) continue;
        const idx = toolIdx.get(id);
        if (idx === undefined) {
          const it: Extract<TranscriptItem, { type: 'tool_call' }> = {
            id: newId('t'),
            type: 'tool_call',
            toolCallId: id,
            status: 'completed',
            resultText: m.content || '',
            detailsLoaded: false,
          };
          toolIdx.set(id, next.length);
          next.push(it);
          continue;
        }
        const cur = next[idx] as Extract<TranscriptItem, { type: 'tool_call' }>;
        next[idx] = { ...cur, status: 'completed', resultText: m.content || '' };
      }
    }

    // Enrich tool calls with persisted previews when available.
    const tcRes = await fetchJSON<{ toolCalls: ToolCallListRow[] }>(`/coddy/sessions/${encodeURIComponent(sid)}/tool-calls`, {
      headers: sid === sessionId ? headers : { [HDR]: sid },
    });
    if (tcRes.ok && tcRes.data?.toolCalls) {
      for (const row of tcRes.data.toolCalls) {
        const id = (row.toolCallId || '').trim();
        if (!id) continue;
        const idx = toolIdx.get(id);
        if (idx === undefined) continue;
        const cur = next[idx] as Extract<TranscriptItem, { type: 'tool_call' }>;
        const title = (row.name || cur.title || '').trim() || undefined;
        const kind = (row.kind || cur.kind || '').trim() || undefined;
        const status = (row.status as any) || cur.status;
        const merged: Extract<TranscriptItem, { type: 'tool_call' }> = {
          ...cur,
          status,
          detailsLoaded: false,
        };
        if (title) merged.title = title;
        if (kind) merged.kind = kind;
        if (row.argsPreview) merged.argsText = row.argsPreview;
        if (row.resultPreview) merged.resultText = row.resultPreview;
        const st = parseRFC3339ms(row.startedAt);
        const fin = parseRFC3339ms(row.finishedAt);
        if (st != null && fin != null && fin >= st) {
          merged.durationMs = fin - st;
        }
        next[idx] = merged;
      }
    }
    setItems(next);
    return next.some((it) => it.type === 'assistant_message');
  }

  async function pickSession(id: string, opts?: { closeMenu?: boolean }) {
    setSessionHash(id);
    setSessionId(id);
    if (opts?.closeMenu !== false) {
      setSessionsOpen(false);
    }
  }

  function goHome() {
    setSessionHash('');
    setSessionId('');
    setItems([]);
    setDraft('');
    setTokenUsage(null);
    setSessionsOpen(false);
    setDescribePreview(null);
  }

  async function renameSession(id: string) {
    const current = sessions.find((s) => s.id === id)?.title ?? '';
    const next = window.prompt('New title', current);
    if (next == null) {
      return;
    }
    const title = next.trim();
    if (!title) {
      return;
    }
    await saveSessionTitle(id, title);
  }

  async function deleteSession(id: string) {
    const ok = window.confirm('Delete chat');
    if (!ok) {
      return;
    }
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, { method: 'DELETE', headers });
    if (id === sessionId) {
      pickSession(randomSessionId(), { closeMenu: false });
      return;
    }
    await loadSessions(true);
  }

  useEffect(() => {
    if (!sessionId) {
      setItems([]);
      void loadSessions(true);
      return;
    }
    if (inFlightRef.current) {
      return;
    }
    setTokenUsage(null);
    tokenBaselineRef.current = { input: 0, output: 0, total: 0 };
    void (async () => {
      const list = await loadSessions(true);
      const exists = !!list?.some((s) => s.id === sessionId);
      if (exists) {
        const statsRes = await fetchJSON<{ stats?: SessionStats | null }>(`/coddy/sessions/${encodeURIComponent(sessionId)}/stats`, { headers });
        if (statsRes.ok && statsRes.data?.stats?.tokenUsageTotal) {
          const t = statsRes.data.stats.tokenUsageTotal;
          tokenBaselineRef.current = { input: t.inputTokens || 0, output: t.outputTokens || 0, total: t.totalTokens || 0 };
          setTokenUsage({
            inputTokens: tokenBaselineRef.current.input,
            outputTokens: tokenBaselineRef.current.output,
            totalTokens: tokenBaselineRef.current.total,
          });
        }
        await loadMessages();
      } else {
        setItems([]);
      }
    })();
  }, [sessionId]);

  function upsertToolCall(update: Partial<Extract<TranscriptItem, { type: 'tool_call' }>> & { toolCallId: string }) {
    setItems((prev) => {
      const idx = prev.findIndex((x) => x.type === 'tool_call' && x.toolCallId === update.toolCallId);
      if (idx < 0) {
        const itBase: Extract<TranscriptItem, { type: 'tool_call' }> = {
          id: newId('t'),
          type: 'tool_call',
          toolCallId: update.toolCallId,
          status: (update.status as any) || 'pending',
        };
        const it: Extract<TranscriptItem, { type: 'tool_call' }> = { ...itBase };
        if (update.title !== undefined) it.title = update.title;
        if (update.kind !== undefined) it.kind = update.kind;
        if (update.argsText !== undefined) it.argsText = update.argsText;
        if (update.resultText !== undefined) it.resultText = update.resultText;
        if (update.startedAtMs !== undefined) it.startedAtMs = update.startedAtMs;
        if (update.finishedAtMs !== undefined) it.finishedAtMs = update.finishedAtMs;
        if (update.durationMs !== undefined) it.durationMs = update.durationMs;
        return [...prev, it];
      }
      const next = [...prev];
      const cur = next[idx] as Extract<TranscriptItem, { type: 'tool_call' }>;
      const nextStarted = update.startedAtMs !== undefined ? update.startedAtMs : cur.startedAtMs;
      const nextFinished = update.finishedAtMs !== undefined ? update.finishedAtMs : cur.finishedAtMs;
      const nextDuration =
        update.durationMs !== undefined
          ? update.durationMs
          : nextStarted && nextFinished
            ? Math.max(0, nextFinished - nextStarted)
            : cur.durationMs;
      const merged: Extract<TranscriptItem, { type: 'tool_call' }> = {
        ...cur,
        status: (update.status as any) || cur.status,
      };
      if (nextStarted !== undefined) merged.startedAtMs = nextStarted;
      if (nextFinished !== undefined) merged.finishedAtMs = nextFinished;
      if (nextDuration !== undefined) merged.durationMs = nextDuration;
      if (update.title !== undefined) merged.title = update.title;
      if (update.kind !== undefined) merged.kind = update.kind;
      if (update.argsText !== undefined) merged.argsText = update.argsText;
      if (update.resultText !== undefined) merged.resultText = update.resultText;
      next[idx] = merged;
      return next;
    });
  }

  async function streamResponses(text: string) {
    inFlightRef.current = true;
    const isNewChatFirstSend = !sessionId.trim();
    let releaseSessionId: ((id: string) => void) | undefined;
    const sessionIdWhenKnown = isNewChatFirstSend
      ? new Promise<string>((resolve) => {
          releaseSessionId = resolve;
        })
      : null;

    let sidEffective = '';
    try {
      let sid = sessionId;
      if (!sid) {
        sid = randomSessionId();
        setSessionHash(sid);
        setSessionId(sid);
      }
      sidEffective = sid;
      let latestPreviewSid = sid;

      if (isNewChatFirstSend && sessionIdWhenKnown) {
        startSuggestSessionTitle({
          userText: text,
          sessionIdPromise: sessionIdWhenKnown,
          getPreviewSessionId: () => latestPreviewSid,
          onShortReady: (cid, ttl) => {
            setDescribePreview({ sessionId: cid, title: ttl });
            setSessions((prev) => {
              const i = prev.findIndex((s) => s.id === cid);
              if (i >= 0) {
                return prev.map((s) => (s.id === cid ? { ...s, title: ttl } : s));
              }
              return [{ id: cid, title: ttl }, ...prev];
            });
          },
          onApplied: (id, appliedTitle) => {
            setSessions((prev) => prev.map((s) => (s.id === id ? { ...s, title: appliedTitle } : s)));
            setDescribePreview((p) => (p?.sessionId === id ? null : p));
          },
        });
      }

      const hdrs = sid ? { [HDR]: sid } : {};
      const userItem: TranscriptItem = { id: newId('u'), type: 'user_message', content: text };
      const assistantId = newId('a');
      setItems((prev) => [...prev, userItem]);
      setTokenUsage(null);

      const res = await fetch('/v1/responses', {
        method: 'POST',
        headers: { ...hdrs, 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: mode || 'agent', input: text, stream: true }),
      });

      const sidHdr = res.headers.get(HDR);
      if (sidHdr && sidHdr !== sid) {
        sidEffective = sidHdr;
        setSessionHash(sidHdr);
        setSessionId(sidHdr);
        setDescribePreview((p) => (p?.sessionId === sid ? { ...p, sessionId: sidHdr } : p));
        setSessions((prev) =>
          prev.map((s) => (s.id === sid ? { ...s, id: sidHdr } : s)),
        );
      }
      latestPreviewSid = sidEffective;
      releaseSessionId?.(sidEffective);

      if (!res.ok || !res.body) {
        setItems((prev) =>
          prev.map((it) =>
            it.type === 'assistant_message' && it.id === assistantId
              ? { ...it, content: `Request failed (${res.status})`, streaming: false }
              : it,
          ),
        );
        return;
      }

      const reader = res.body.getReader();
      const dec = new TextDecoder();
      const carry = { buf: '' };

      const toolQueue: Array<Partial<Extract<TranscriptItem, { type: 'tool_call' }>> & { toolCallId: string }> = [];
      let raf = 0;
    const flushToolQueue = () => {
      raf = 0;
      if (toolQueue.length === 0) return;
      const pending = toolQueue.splice(0, toolQueue.length);
      setItems((prev) => {
        let next = prev;
        for (const upd of pending) {
          const idx = next.findIndex((x) => x.type === 'tool_call' && x.toolCallId === upd.toolCallId);
          if (idx < 0) {
            const itBase: Extract<TranscriptItem, { type: 'tool_call' }> = {
              id: newId('t'),
              type: 'tool_call',
              toolCallId: upd.toolCallId,
              status: (upd.status as any) || 'pending',
            };
            const it: Extract<TranscriptItem, { type: 'tool_call' }> = { ...itBase };
            if (upd.title !== undefined) it.title = upd.title;
            if (upd.kind !== undefined) it.kind = upd.kind;
            if (upd.argsText !== undefined) it.argsText = upd.argsText;
            if (upd.resultText !== undefined) it.resultText = upd.resultText;
            if (upd.detailsLoaded !== undefined) it.detailsLoaded = upd.detailsLoaded;
            if (upd.startedAtMs !== undefined) it.startedAtMs = upd.startedAtMs;
            if (upd.finishedAtMs !== undefined) it.finishedAtMs = upd.finishedAtMs;
            if (upd.durationMs !== undefined) it.durationMs = upd.durationMs;
            const aIdx = next.findIndex((x) => x.type === 'assistant_message' && x.id === assistantId);
            if (aIdx >= 0) {
              const arr = next === prev ? [...next] : next;
              arr.splice(aIdx, 0, it);
              next = arr;
            } else {
              next = [...next, it];
            }
            continue;
          }
          const arr = next === prev ? [...next] : next;
          const cur = arr[idx] as Extract<TranscriptItem, { type: 'tool_call' }>;
          const nextStarted = upd.startedAtMs !== undefined ? upd.startedAtMs : cur.startedAtMs;
          const nextFinished = upd.finishedAtMs !== undefined ? upd.finishedAtMs : cur.finishedAtMs;
          const nextDuration =
            upd.durationMs !== undefined
              ? upd.durationMs
              : nextStarted && nextFinished
                ? Math.max(0, nextFinished - nextStarted)
                : cur.durationMs;
          const merged: Extract<TranscriptItem, { type: 'tool_call' }> = {
            ...cur,
            status: (upd.status as any) || cur.status,
          };
          if (nextStarted !== undefined) merged.startedAtMs = nextStarted;
          if (nextFinished !== undefined) merged.finishedAtMs = nextFinished;
          if (nextDuration !== undefined) merged.durationMs = nextDuration;
          if (upd.title !== undefined) merged.title = upd.title;
          if (upd.kind !== undefined) merged.kind = upd.kind;
          if (upd.argsText !== undefined) merged.argsText = upd.argsText;
          if (upd.resultText !== undefined) merged.resultText = upd.resultText;
          if (upd.detailsLoaded !== undefined) merged.detailsLoaded = upd.detailsLoaded;
          arr[idx] = merged;
          next = arr;
        }
        return next;
      });
    };
    const scheduleToolFlush = () => {
      if (raf) return;
      raf = window.requestAnimationFrame(flushToolQueue);
    };

      const ensureAssistant = (patch?: Partial<Extract<TranscriptItem, { type: 'assistant_message' }>>) => {
        setItems((prev) => {
          const idx = prev.findIndex((x) => x.type === 'assistant_message' && x.id === assistantId);
          if (idx < 0) {
            const base: Extract<TranscriptItem, { type: 'assistant_message' }> = {
              id: assistantId,
              type: 'assistant_message',
              content: '',
              streaming: true,
            };
            return [...prev, { ...base, ...(patch || {}) }];
          }
          if (!patch) return prev;
          const next = [...prev];
          const cur = next[idx] as Extract<TranscriptItem, { type: 'assistant_message' }>;
          next[idx] = { ...cur, ...patch };
          return next;
        });
      };

      let activeThinkingId: string | null = null;
      let activeThinkingStarted = 0;
      const startThinkingIfNeeded = () => {
        if (activeThinkingId) return activeThinkingId;
        activeThinkingId = newId('r');
        activeThinkingStarted = Date.now();
        const id = activeThinkingId;
        const t0 = activeThinkingStarted;
        setItems((prev) => [...prev, { id, type: 'thinking', status: 'in_progress', content: '', startedAtMs: t0 }]);
        return id;
      };
      const appendThinking = (delta: string) => {
        const id = startThinkingIfNeeded();
        setItems((prev) => prev.map((it) => (it.type === 'thinking' && it.id === id ? { ...it, content: it.content + delta } : it)));
      };
      const finishThinking = () => {
        if (!activeThinkingId) return;
        const id = activeThinkingId;
        const dur = Math.max(0, Date.now() - activeThinkingStarted);
        setItems((prev) =>
          prev.map((it) => (it.type === 'thinking' && it.id === id ? { ...it, status: 'completed', durationMs: dur } : it)),
        );
        activeThinkingId = null;
      };

      const syncAssistantFromServer = async () => {
        try {
          const res = await fetchJSON<{ messages: Array<any> }>(`/coddy/sessions/${encodeURIComponent(sidEffective)}/messages`, { headers: { [HDR]: sidEffective } });
          if (!res.ok || !res.data?.messages) return false;
          let last = '';
          for (const m of res.data.messages) {
            if ((m.role || '').trim() !== 'assistant') continue;
            const c = (m.content || '').trim();
            if (c) last = c;
          }
          if (!last) return false;
          ensureAssistant();
          setItems((prev) => prev.map((it) => (it.type === 'assistant_message' && it.id === assistantId ? { ...it, content: last } : it)));
          return true;
        } catch {
          return false;
        }
      };

      let sawDone = false;
      while (true) {
        const step = await reader.read();
        if (step.done) {
          break;
        }
        const events = parseSSEBlocks(dec.decode(step.value, { stream: true }), carry);
        for (const ev of events) {
          if (ev.data === '[DONE]') {
            sawDone = true;
            break;
          }

          if (!ev.event) {
            try {
              const delta = JSON.parse(ev.data) as any;
              const contentDelta = delta.choices?.[0]?.delta?.content;
              const c = typeof contentDelta === 'string' ? contentDelta : '';
              const r = delta.choices?.[0]?.delta?.reasoning_content || '';
              if (r) {
                appendThinking(r);
              }
              if (c) {
                if (/\S/.test(c)) {
                  finishThinking();
                }
                ensureAssistant();
                setItems((prev) =>
                  prev.map((it) =>
                    it.type === 'assistant_message' && it.id === assistantId ? { ...it, content: it.content + c } : it,
                  ),
                );
              }
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === 'token_usage') {
            try {
              const u = JSON.parse(ev.data) as TokenUsage;
              const merged: TokenUsage = {
                inputTokens: tokenBaselineRef.current.input + (u.inputTokens || 0),
                outputTokens: tokenBaselineRef.current.output + (u.outputTokens || 0),
                totalTokens: tokenBaselineRef.current.total + (u.totalTokens || 0),
              };
              setTokenUsage(merged);
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === 'tool_call') {
            try {
              finishThinking();
              const t = JSON.parse(ev.data) as ToolCallUpdate;
              const now = Date.now();
              const patch: Partial<Extract<TranscriptItem, { type: 'tool_call' }>> & { toolCallId: string } = {
                toolCallId: t.toolCallId,
                status: (t.status as any) || 'pending',
                startedAtMs: now,
              };
              if (t.title !== undefined) patch.title = t.title;
              if (t.kind !== undefined) patch.kind = t.kind;
              toolQueue.push(patch);
              scheduleToolFlush();
            } catch {
              // ignore
            }
            continue;
          }

          if (ev.event === 'tool_call_update') {
            try {
              const u = JSON.parse(ev.data) as ToolCallStatusUpdate;
              const status = (u.status as any) || 'in_progress';
              const text0 = u.content?.[0]?.content?.text || '';
              const now = Date.now();
              if (status === 'in_progress' && text0) {
                toolQueue.push({ toolCallId: u.toolCallId, status, argsText: text0, startedAtMs: now });
                scheduleToolFlush();
              } else if ((status === 'completed' || status === 'failed' || status === 'cancelled') && text0) {
                toolQueue.push({ toolCallId: u.toolCallId, status, resultText: text0, finishedAtMs: now });
                scheduleToolFlush();
              } else {
                if (status === 'completed' || status === 'failed' || status === 'cancelled') {
                  toolQueue.push({ toolCallId: u.toolCallId, status, finishedAtMs: now });
                } else {
                  toolQueue.push({ toolCallId: u.toolCallId, status, startedAtMs: now });
                }
                scheduleToolFlush();
              }
            } catch {
              // ignore
            }
            continue;
          }
        }
        if (sawDone) {
          break;
        }
      }
      if (sawDone) {
        try {
          await reader.cancel();
        } catch {
          // ignore
        }
      }

      if (carry.buf.trim()) {
        const tailEvents = parseSSEBlocks('\n\n', carry);
        for (const ev of tailEvents) {
          if (ev.data === '[DONE]') continue;
          if (!ev.event) {
            try {
              const delta = JSON.parse(ev.data) as any;
              const contentDelta = delta.choices?.[0]?.delta?.content;
              const c = typeof contentDelta === 'string' ? contentDelta : '';
              const r = delta.choices?.[0]?.delta?.reasoning_content || '';
              if (r) {
                appendThinking(r);
              }
              if (c) {
                if (/\S/.test(c)) {
                  finishThinking();
                }
                ensureAssistant();
                setItems((prev) =>
                  prev.map((it) =>
                    it.type === 'assistant_message' && it.id === assistantId ? { ...it, content: it.content + c } : it,
                  ),
                );
              }
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === 'tool_call') {
            try {
              finishThinking();
              const t = JSON.parse(ev.data) as ToolCallUpdate;
              const now = Date.now();
              const patch: Partial<Extract<TranscriptItem, { type: 'tool_call' }>> & { toolCallId: string } = {
                toolCallId: t.toolCallId,
                status: (t.status as any) || 'pending',
                startedAtMs: now,
              };
              if (t.title !== undefined) patch.title = t.title;
              if (t.kind !== undefined) patch.kind = t.kind;
              toolQueue.push(patch);
              scheduleToolFlush();
            } catch {
              // ignore
            }
            continue;
          }
          if (ev.event === 'tool_call_update') {
            try {
              const u = JSON.parse(ev.data) as ToolCallStatusUpdate;
              const status = (u.status as any) || 'in_progress';
              const text0 = u.content?.[0]?.content?.text || '';
              const now = Date.now();
              if (status === 'in_progress' && text0) {
                toolQueue.push({ toolCallId: u.toolCallId, status, argsText: text0, startedAtMs: now });
                scheduleToolFlush();
              } else if ((status === 'completed' || status === 'failed' || status === 'cancelled') && text0) {
                toolQueue.push({ toolCallId: u.toolCallId, status, resultText: text0, finishedAtMs: now });
                scheduleToolFlush();
              } else {
                if (status === 'completed' || status === 'failed' || status === 'cancelled') {
                  toolQueue.push({ toolCallId: u.toolCallId, status, finishedAtMs: now });
                } else {
                  toolQueue.push({ toolCallId: u.toolCallId, status, startedAtMs: now });
                }
                scheduleToolFlush();
              }
            } catch {
              // ignore
            }
            continue;
          }
        }
      }

      flushToolQueue();

      finishThinking();
      ensureAssistant({ streaming: false });

      void loadSessions(true);
      let ok = await syncAssistantFromServer();
      for (let i = 0; i < 10 && !ok; i++) {
        await new Promise((r) => setTimeout(r, 500));
        ok = await syncAssistantFromServer();
      }
      await loadMessages(sidEffective);
    } finally {
      releaseSessionId?.(sidEffective);
      inFlightRef.current = false;
    }
  }

  const maxContextTokens = useMemo(() => {
    const row = modelInfos.find((m) => m.id === mode);
    return row?.maxContextTokens || 128000;
  }, [modelInfos, mode]);

  const contextPct = useMemo(() => {
    if (!tokenUsage || !maxContextTokens) return 0;
    return Math.min(100, Math.max(0, (tokenUsage.totalTokens / maxContextTokens) * 100));
  }, [tokenUsage, maxContextTokens]);

  return (
    <div className="shell">
      <NavRail
        onNewChat={goHome}
        menuOpen={sessionsOpen}
        onToggleMenu={() => setSessionsOpen((v) => !v)}
      />

      <div
        className={`backdrop ${sessionsOpen ? 'is-open' : ''}`}
        onClick={() => setSessionsOpen(false)}
        aria-hidden={!sessionsOpen}
      />

      <SessionsSidebar
        sessionId={sessionId}
        sessions={sessions}
        error={sessionsError}
        variant="drawer"
        open={sessionsOpen}
        onClose={() => setSessionsOpen(false)}
        onPick={pickSession}
        onRename={(id: string) => void renameSession(id)}
        onTitleSave={(id: string, title: string) => void saveSessionTitle(id, title)}
        onDelete={(id: string) => void deleteSession(id)}
        onLoadMore={() => void loadSessions(false)}
      />
      <ChatScreen
        title={currentTitle}
        sessionId={sessionId}
        onTitleSave={(t: string) => void saveSessionTitle(sessionId, t)}
        items={items}
        draft={draft}
        tokenUsage={tokenUsage}
        contextPct={contextPct}
        maxContextTokens={maxContextTokens}
        modelLabel={mode}
        mode={mode}
        modes={modes}
        onModeChange={setMode}
        onDraftChange={setDraft}
        onSend={(text: string) => {
          setDraft('');
          void streamResponses(text);
        }}
        onLoadToolCallDetails={(toolCallId: string) => {
          void (async () => {
            if (!sessionId) return;
            const det = await fetchJSON<{ args?: string; result?: string; meta?: { status?: string; kind?: string; name?: string } }>(
              `/coddy/sessions/${encodeURIComponent(sessionId)}/tool-calls/${encodeURIComponent(toolCallId)}`,
              { headers },
            );
            if (!det.ok || !det.data) return;
            const meta = det.data.meta || {};
            const patch: any = { toolCallId, detailsLoaded: true };
            if (meta.name) patch.title = meta.name;
            if (meta.kind) patch.kind = meta.kind;
            if (meta.status) patch.status = meta.status;
            if (det.data.args) patch.argsText = det.data.args;
            if (det.data.result) patch.resultText = det.data.result;
            upsertToolCall(patch);
          })();
        }}
      />
    </div>
  );
}
