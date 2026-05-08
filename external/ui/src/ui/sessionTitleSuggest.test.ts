import { afterEach, expect, test, vi } from 'vitest';
import { startSuggestSessionTitle } from './sessionTitleSuggest';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

test('describe is requested before session id promise resolves', async () => {
  let releaseSid!: (id: string) => void;
  const sidPromise = new Promise<string>((resolve) => {
    releaseSid = resolve;
  });

  const order: string[] = [];

  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString();
    if (url.includes('/coddy/describe')) {
      order.push('describe');
      return new Response(JSON.stringify({ object: 'coddy.describe', short: 'My title' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    if (url.includes('/coddy/sessions/sess_x') && !url.includes('/messages')) {
      order.push('patch');
      return new Response(JSON.stringify({ object: 'coddy.session_patched' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('not found', { status: 404 });
  });

  startSuggestSessionTitle({
    userText: 'please explain async rust patterns in detail',
    sessionIdPromise: sidPromise,
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => {
    expect(order).toContain('describe');
  });
  expect(order).not.toContain('patch');

  releaseSid('sess_x');

  await vi.waitFor(() => {
    expect(order).toEqual(['describe', 'patch']);
  });
});

test('onShortReady runs after describe and before PATCH resolves', async () => {
  let releaseSid!: (id: string) => void;
  const sidPromise = new Promise<string>((resolve) => {
    releaseSid = resolve;
  });

  const order: string[] = [];
  const preview: Array<{ sid: string; title: string }> = [];

  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString();
    if (url.includes('/coddy/describe')) {
      order.push('describe');
      return new Response(JSON.stringify({ short: 'Fast title' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    if (url.includes('/coddy/sessions/sess_x') && !url.includes('/messages')) {
      order.push('patch');
      return new Response(JSON.stringify({ object: 'coddy.session_patched' }), { status: 200 });
    }
    return new Response('not found', { status: 404 });
  });

  startSuggestSessionTitle({
    userText: 'four word message here',
    sessionIdPromise: sidPromise,
    getPreviewSessionId: () => 'sess_x',
    onShortReady: (sid, title) => {
      order.push('short_ready');
      preview.push({ sid, title });
    },
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => {
    expect(order).toEqual(['describe', 'short_ready']);
  });
  expect(order).not.toContain('patch');

  releaseSid('sess_x');

  await vi.waitFor(() => {
    expect(order).toEqual(['describe', 'short_ready', 'patch']);
  });
  expect(preview).toEqual([{ sid: 'sess_x', title: 'Fast title' }]);
});

test('retries PATCH when session returns 404 until ok', async () => {
  let patchAttempts = 0;
  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString();
    if (url.includes('/coddy/describe')) {
      return new Response(JSON.stringify({ short: 'T' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    if (url.includes('/coddy/sessions/sid1')) {
      patchAttempts++;
      if (patchAttempts < 3) {
        return new Response('missing', { status: 404 });
      }
      return new Response(JSON.stringify({ object: 'coddy.session_patched' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('no', { status: 404 });
  });

  const applied: Array<{ sid: string; title: string }> = [];

  startSuggestSessionTitle({
    userText: 'one two three four',
    sessionIdPromise: Promise.resolve('sid1'),
    fetchImpl: fetchImpl as unknown as typeof fetch,
    onApplied: (sid, title) => applied.push({ sid, title }),
  });

  await vi.waitFor(() => {
    expect(applied).toEqual([{ sid: 'sid1', title: 'T' }]);
  });
});
