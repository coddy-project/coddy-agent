import { useEffect, useMemo, useState } from 'react';
import { ChatScreen } from './chat/ChatScreen';
import { parseSSEBlocks } from './chat/sse';
import type { TokenUsage, TranscriptItem } from './chat/types';
import { NavRail } from './nav/NavRail';
import { SessionsSidebar } from './sessions/SessionsSidebar';
import type { SessionRow } from './sessions/types';

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

export function App() {
  const [sessionId, setSessionId] = useState('');
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [sessionsCursor, setSessionsCursor] = useState<string | null>(null);
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [draft, setDraft] = useState('');
  const [tokenUsage, setTokenUsage] = useState<TokenUsage | null>(null);

  const headers = useMemo(() => ({ [HDR]: sessionId }), [sessionId]);

  useEffect(() => {
    let id = getSessionFromHash();
    if (!id) {
      id = randomSessionId();
      setSessionHash(id);
    }
    setSessionId(id);
  }, []);

  useEffect(() => {
    const onHash = () => {
      const id = getSessionFromHash();
      if (id && id !== sessionId) {
        setSessionId(id);
      }
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, [sessionId]);

  async function loadSessions(reset: boolean) {
    const ps = new URLSearchParams();
    ps.set('limit', '30');
    if (!reset && sessionsCursor) {
      ps.set('cursor', sessionsCursor);
    }
    const res = await fetchJSON<{ sessions: SessionRow[]; nextCursor?: string | null }>(`/coddy/sessions?${ps.toString()}`, {
      headers,
    });
    if (!res.ok || !res.data) {
      return;
    }
    const next = res.data.sessions || [];
    setSessions((prev) => {
      if (reset) {
        return next;
      }
      const seen = new Set(prev.map((s) => s.id));
      return [...prev, ...next.filter((s) => !seen.has(s.id))];
    });
    setSessionsCursor(res.data.nextCursor ?? null);
  }

  async function loadMessages() {
    const res = await fetchJSON<{ messages: Array<{ role: string; content?: string }> }>(
      `/coddy/sessions/${encodeURIComponent(sessionId)}/messages`,
      { headers },
    );
    if (!res.ok || !res.data) {
      setItems([]);
      return;
    }
    const next: TranscriptItem[] = [];
    for (const m of res.data.messages || []) {
      if (m.role === 'user') {
        next.push({ id: newId('u'), type: 'user_message', content: m.content || '' });
        continue;
      }
      if (m.role === 'assistant') {
        next.push({ id: newId('a'), type: 'assistant_message', content: m.content || '' });
      }
    }
    setItems(next);
  }

  async function pickSession(id: string) {
    setSessionHash(id);
    setSessionId(id);
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
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { ...headers, 'Content-Type': 'application/json' },
      body: JSON.stringify({ title }),
    });
    await loadSessions(true);
  }

  async function deleteSession(id: string) {
    const ok = window.confirm(`Delete chat ${id}?`);
    if (!ok) {
      return;
    }
    await fetch(`/coddy/sessions/${encodeURIComponent(id)}`, { method: 'DELETE', headers });
    if (id === sessionId) {
      pickSession(randomSessionId());
      return;
    }
    await loadSessions(true);
  }

  useEffect(() => {
    if (!sessionId) {
      return;
    }
    setTokenUsage(null);
    void loadSessions(true);
    void loadMessages();
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
        return [...prev, it];
      }
      const next = [...prev];
      const cur = next[idx] as Extract<TranscriptItem, { type: 'tool_call' }>;
      const merged: Extract<TranscriptItem, { type: 'tool_call' }> = {
        ...cur,
        status: (update.status as any) || cur.status,
      };
      if (update.title !== undefined) merged.title = update.title;
      if (update.kind !== undefined) merged.kind = update.kind;
      if (update.argsText !== undefined) merged.argsText = update.argsText;
      if (update.resultText !== undefined) merged.resultText = update.resultText;
      next[idx] = merged;
      return next;
    });
  }

  async function streamResponses(text: string) {
    const userItem: TranscriptItem = { id: newId('u'), type: 'user_message', content: text };
    const assistantId = newId('a');
    const assistantItem: TranscriptItem = { id: assistantId, type: 'assistant_message', content: '', streaming: true };

    setItems((prev) => [...prev, userItem, assistantItem]);
    setTokenUsage(null);

    const res = await fetch('/v1/responses', {
      method: 'POST',
      headers: { ...headers, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'agent', input: text, stream: true }),
    });

    const sidHdr = res.headers.get(HDR);
    if (sidHdr && sidHdr !== sessionId) {
      setSessionHash(sidHdr);
      setSessionId(sidHdr);
    }

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

    while (true) {
      const step = await reader.read();
      if (step.done) {
        break;
      }
      const events = parseSSEBlocks(dec.decode(step.value, { stream: true }), carry);
      for (const ev of events) {
        if (ev.data === '[DONE]') {
          continue;
        }

        if (!ev.event) {
          try {
            const delta = JSON.parse(ev.data) as any;
            const piece = delta.choices?.[0]?.delta?.content || delta.choices?.[0]?.delta?.reasoning_content || '';
            if (piece) {
              setItems((prev) =>
                prev.map((it) =>
                  it.type === 'assistant_message' && it.id === assistantId ? { ...it, content: it.content + piece } : it,
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
            setTokenUsage(JSON.parse(ev.data) as TokenUsage);
          } catch {
            // ignore
          }
          continue;
        }

        if (ev.event === 'tool_call') {
          try {
            const t = JSON.parse(ev.data) as ToolCallUpdate;
            const patch: Partial<Extract<TranscriptItem, { type: 'tool_call' }>> & { toolCallId: string } = {
              toolCallId: t.toolCallId,
              status: (t.status as any) || 'pending',
            };
            if (t.title !== undefined) patch.title = t.title;
            if (t.kind !== undefined) patch.kind = t.kind;
            upsertToolCall(patch);
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
            if (status === 'in_progress' && text0) {
              upsertToolCall({ toolCallId: u.toolCallId, status, argsText: text0 });
            } else if ((status === 'completed' || status === 'failed' || status === 'cancelled') && text0) {
              upsertToolCall({ toolCallId: u.toolCallId, status, resultText: text0 });
            } else {
              upsertToolCall({ toolCallId: u.toolCallId, status });
            }
          } catch {
            // ignore
          }
          continue;
        }
      }
    }

    setItems((prev) =>
      prev.map((it) => (it.type === 'assistant_message' && it.id === assistantId ? { ...it, streaming: false } : it)),
    );

    void loadSessions(true);
  }

  return (
    <div className="shell">
      <NavRail onNewChat={() => pickSession(randomSessionId())} />
      <SessionsSidebar
        sessionId={sessionId}
        sessions={sessions}
        onPick={pickSession}
        onRename={(id: string) => void renameSession(id)}
        onDelete={(id: string) => void deleteSession(id)}
        onLoadMore={() => void loadSessions(false)}
      />
      <ChatScreen
        title={sessionId}
        items={items}
        draft={draft}
        tokenUsage={tokenUsage}
        onDraftChange={setDraft}
        onSend={(text: string) => {
          setDraft('');
          void streamResponses(text);
        }}
      />
    </div>
  );
}
