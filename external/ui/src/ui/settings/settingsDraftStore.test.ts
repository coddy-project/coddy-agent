import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  ensureSettingsConfig,
  noteSettingsConfigReloaded,
  refreshSettingsConfig,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";
import {
  AUTOSAVE_MS,
  discardPendingSettings,
  editSettingsDraft,
  saveSettingsDraft,
  snapshotSettingsDraft,
} from "./settingsDraftStore";
import { setEnv } from "../env/remoteEnv";
import { setIn, type Doc } from "./settingsAutosave";

// Edge cases of the page-wide draft (issue #485): saves, copies of the
// server's config and edits arriving in every order.

const schema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      "x-coddy-save": "confirm-removal",
      items: { type: "object", properties: { name: { type: "string" } } },
    },
    agent: {
      type: "object",
      properties: { max_turns: { type: "integer" } },
    },
    gateways: {
      type: "object",
      properties: {
        telegram: {
          type: "object",
          properties: {
            enable: { type: "boolean", "x-coddy-save": "confirm" },
          },
        },
      },
    },
  },
};

let config: Doc;
let puts: Doc[];
let refuse: string | null;
/** While set, a PUT waits for it. */
let gate: Promise<void> | null;

function stubServer() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const reply = (status: number, body: unknown) => ({
        ok: status >= 200 && status < 300,
        status,
        json: async () => structuredClone(body),
      });
      if (path === "/coddy/config/schema") return reply(200, schema);
      if (path === "/coddy/config" && method === "GET") {
        return reply(200, config);
      }
      if (path === "/coddy/config" && method === "PUT") {
        const body = JSON.parse(String(init?.body)) as Doc;
        puts.push(body);
        if (gate) await gate;
        if (refuse !== null) return reply(400, { ok: false, error: refuse });
        const revision = `rev-${puts.length + 1}`;
        config = { ...body, revision };
        return reply(200, { ok: true, revision });
      }
      return reply(404, {});
    }),
  );
}

beforeEach(async () => {
  resetSettingsConfigForTests();
  vi.useFakeTimers({ shouldAdvanceTime: true });
  config = {
    revision: "rev-1",
    providers: [{ name: "demo" }, { name: "spare" }],
    agent: { max_turns: 40 },
    gateways: { telegram: {} },
  };
  puts = [];
  refuse = null;
  gate = null;
  stubServer();
  await ensureSettingsConfig();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setEnv({ mode: "local" });
  resetSettingsConfigForTests();
});

const draft = () => snapshotSettingsDraft();
const doc = () => draft().doc;
const turns = (d: Doc) => (d.agent as Doc).max_turns;
const pause = (ms = AUTOSAVE_MS + 50) => vi.advanceTimersByTimeAsync(ms);
const settle = async () => {
  for (let i = 0; i < 5; i++) await vi.advanceTimersByTimeAsync(0);
};

/** Holds every PUT until the returned function is called. */
function holdSaves(): () => void {
  let release: () => void = () => {};
  gate = new Promise<void>((r) => {
    release = () => {
      gate = null;
      r();
    };
  });
  return () => release();
}

test("a value put back while a save is on its way is saved after it", async () => {
  editSettingsDraft(setIn(doc(), ["agent", "max_turns"], 41));
  const release = holdSaves();
  await pause();
  expect(puts.map(turns)).toEqual([41]);
  editSettingsDraft(setIn(doc(), ["agent", "max_turns"], 40));
  release();
  await settle();
  await pause();
  await settle();
  expect(puts.map(turns)).toEqual([41, 40]);
  expect(turns(config)).toBe(40);
  expect(draft().unsaved).toBe(false);
});

test("a newer copy passed over while the form held a change is taken once the form is clean", async () => {
  editSettingsDraft(setIn(doc(), ["gateways", "telegram", "enable"], true));
  config = { ...config, agent: { max_turns: 70 }, revision: "rev-9" };
  noteSettingsConfigReloaded();
  await settle();
  expect(turns(doc())).toBe(40);
  discardPendingSettings();
  expect(turns(doc())).toBe(70);
  expect(doc().revision).toBe("rev-9");
  expect(draft().pending).toEqual([]);
});

test("the reason a save was refused goes once nothing is left unsaved", async () => {
  refuse = "agent.max_turns: too many";
  editSettingsDraft(setIn(doc(), ["agent", "max_turns"], 41));
  await pause();
  await settle();
  expect(draft().error).toBe("agent.max_turns: too many");
  editSettingsDraft(setIn(doc(), ["agent", "max_turns"], 40));
  expect(draft().unsaved).toBe(false);
  expect(draft().error).toBeNull();
});

test("a draft is never saved into another environment, and leaving for one does not ask", async () => {
  editSettingsDraft(setIn(doc(), ["gateways", "telegram", "enable"], true));
  const leave = () => {
    const e = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  };
  expect(leave()).toBe(true);
  // The page is on its way to another server: switchTo stores it before the
  // reload, and a page kept by a cancelled reload must not write there.
  setEnv({ mode: "remote", baseUrl: "http://other.test", token: "" });
  expect(leave()).toBe(false);
  expect(await saveSettingsDraft()).toBe(false);
  expect(puts).toEqual([]);
  expect(draft().pending).toEqual([]);
});

test("a read of the same schema again keeps following the rows of a list", async () => {
  // Renamed in place, the way the provider form edits a row.
  const rows = doc().providers as Doc[];
  editSettingsDraft(
    setIn(doc(), ["providers"], [{ ...rows[0], name: "demo2" }, rows[1]]),
  );
  expect(draft().pending).toEqual([]);
  // Hold the save, so the form still holds the rename when the read lands.
  const release = holdSaves();
  await pause();
  await refreshSettingsConfig();
  expect(draft().pending).toEqual([]);
  release();
  await settle();
});

test("an edit built from the document before a save keeps the revision that save answered with", async () => {
  const before = doc();
  editSettingsDraft(setIn(before, ["agent", "max_turns"], 41));
  await pause();
  await settle();
  expect(doc().revision).toBe("rev-2");
  // A handler that ran on the render before the save landed builds its edit
  // from the older document.
  editSettingsDraft(setIn(before, ["agent", "max_turns"], 42));
  expect(doc().revision).toBe("rev-2");
  await pause();
  await settle();
  expect(puts[1]!.revision).toBe("rev-2");
});
