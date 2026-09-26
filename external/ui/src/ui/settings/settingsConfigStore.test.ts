import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  ensureSettingsConfig,
  noteSettingsConfigReloaded,
  noteSettingsConfigSaved,
  refreshSettingsConfig,
  resetSettingsConfigForTests,
  snapshotSettingsConfig,
} from "./settingsConfigStore";

const schema = { type: "object", properties: {} };

type Pending = {
  path: string;
  resolve: (body: unknown) => void;
  fail: () => void;
};

/**
 * A server whose answers the test hands out: every GET waits in `pending`
 * until the test resolves it, so the order in which reads land is the test's.
 */
function heldServer() {
  const pending: Pending[] = [];
  const fetch = vi.fn(
    (path: string) =>
      new Promise((resolve) => {
        pending.push({
          path,
          resolve: (body: unknown) =>
            resolve({ ok: true, status: 200, json: async () => body }),
          fail: () =>
            resolve({ ok: false, status: 502, json: async () => ({}) }),
        });
      }),
  );
  vi.stubGlobal("fetch", fetch);
  return { fetch, pending };
}

/** A server that answers every GET at once with the current `config`. */
function answeringServer(state: { config: Record<string, unknown> }) {
  const fetch = vi.fn(async (path: string) => ({
    ok: true,
    status: 200,
    json: async () =>
      path.endsWith("/schema") ? schema : structuredClone(state.config),
  }));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

beforeEach(() => {
  resetSettingsConfigForTests();
});

afterEach(() => {
  vi.unstubAllGlobals();
  resetSettingsConfigForTests();
});

test("the first read asks for the schema and the config once, however many ask", async () => {
  const fetch = answeringServer({ config: { agent: { max_turns: 40 } } });

  const [a, b] = await Promise.all([
    ensureSettingsConfig(),
    ensureSettingsConfig(),
  ]);
  expect(fetch.mock.calls.map((c) => c[0]).sort()).toEqual([
    "/coddy/config",
    "/coddy/config/schema",
  ]);
  expect(a.config).toEqual({ agent: { max_turns: 40 } });
  expect(b).toBe(a);

  await ensureSettingsConfig();
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(snapshotSettingsConfig().schema).toEqual(schema);
});

test("a reload notice reads a held copy again and leaves an unread one alone", async () => {
  const state = { config: { agent: { max_turns: 40 } } as Record<string, unknown> };
  const fetch = answeringServer(state);

  noteSettingsConfigReloaded();
  expect(fetch).not.toHaveBeenCalled();

  await ensureSettingsConfig();
  state.config = { agent: { max_turns: 60 } };
  noteSettingsConfigReloaded();
  await vi.waitFor(() =>
    expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 60 } }),
  );
  expect(fetch).toHaveBeenCalledTimes(4);
});

test("an answer that lands after a newer one does not replace it", async () => {
  const { pending } = heldServer();
  const first = refreshSettingsConfig();
  const second = refreshSettingsConfig();
  const answer = (index: number, config: Record<string, unknown>) => {
    for (const p of pending.slice(index * 2, index * 2 + 2)) {
      p.resolve(p.path.endsWith("/schema") ? schema : config);
    }
  };

  answer(1, { agent: { max_turns: 2 } });
  await second;
  answer(0, { agent: { max_turns: 1 } });
  await first;

  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 2 } });
});

test("a failed read keeps the copy it would have replaced", async () => {
  answeringServer({ config: { agent: { max_turns: 40 } } });
  await ensureSettingsConfig();

  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 502, json: async () => ({}) })),
  );
  const result = await refreshSettingsConfig();

  expect(result.ok).toBe(false);
  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 40 } });
  expect(snapshotSettingsConfig().error).toBeNull();
});

test("a first read that failed is reported, and the next open asks again", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 502, json: async () => ({}) })),
  );
  await ensureSettingsConfig();
  expect(snapshotSettingsConfig().config).toBeNull();
  expect(snapshotSettingsConfig().error).toBe("502");

  const fetch = answeringServer({ config: { agent: { max_turns: 40 } } });
  await ensureSettingsConfig();
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 40 } });
  expect(snapshotSettingsConfig().error).toBeNull();
});

// A read asked for by a reload fails while an older read is still on its way;
// the older answer lands afterwards. It is the best copy there is, so it is
// shown, but it may predate the reload: the next open reads again.
test("an answer older than a failed read is kept, and the next open reads again", async () => {
  const { pending } = heldServer();
  const older = refreshSettingsConfig();
  const newer = refreshSettingsConfig();
  for (const p of pending.slice(2, 4)) {
    p.fail();
  }
  expect((await newer).ok).toBe(false);
  for (const p of pending.slice(0, 2)) {
    p.resolve(p.path.endsWith("/schema") ? schema : { agent: { max_turns: 1 } });
  }
  await older;
  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 1 } });

  const reads = vi.fn(async (path: string) => ({
    ok: true,
    status: 200,
    json: async () =>
      path.endsWith("/schema") ? schema : { agent: { max_turns: 3 } },
  }));
  vi.stubGlobal("fetch", reads);
  await ensureSettingsConfig();
  expect(reads).toHaveBeenCalledTimes(2);
  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 3 } });
});

// A save puts what it wrote into the copy at once; a read that was on its way
// before the save is older than it and does not bring the replaced values back.
test("a saved document is the copy until a read after the save replaces it", async () => {
  answeringServer({ config: { agent: { max_turns: 40 } } });
  await ensureSettingsConfig();
  const { pending } = heldServer();
  const before = refreshSettingsConfig();

  noteSettingsConfigSaved({ agent: { max_turns: 45 } });
  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 45 } });

  for (const p of pending) {
    p.resolve(p.path.endsWith("/schema") ? schema : { agent: { max_turns: 40 } });
  }
  await before;
  expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 45 } });
});

// A first read that failed leaves nothing to draw; the next reload notice (or the
// events stream coming back) asks again rather than waiting for a reopen.
test("a reload notice retries a first read that failed", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 502, json: async () => ({}) })),
  );
  await ensureSettingsConfig();
  expect(snapshotSettingsConfig().error).toBe("502");

  answeringServer({ config: { agent: { max_turns: 40 } } });
  noteSettingsConfigReloaded();
  await vi.waitFor(() =>
    expect(snapshotSettingsConfig().config).toEqual({ agent: { max_turns: 40 } }),
  );
  expect(snapshotSettingsConfig().error).toBeNull();
});
