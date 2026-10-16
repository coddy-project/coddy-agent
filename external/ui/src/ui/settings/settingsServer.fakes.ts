import { createElement, useEffect, useState, type ReactNode } from "react";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { onTestFinished, vi } from "vitest";
import { render } from "@testing-library/react";

import { ConfirmProvider } from "../components/useConfirm";
import { initLocale, setLocale } from "../i18n/i18n";
import { parseAppHash, setSettingsSectionHash } from "../scheduler/hashRoute";
import {
  captureTelegramLaunch,
  resetTelegramLaunchForTests,
} from "../telegram/launch";
import { defaultForSchema, type JsonSchema } from "./SchemaForm";
import { Settings } from "./Settings";
import {
  ensureSettingsConfig,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";

// The schemas coddy serve hands the settings form, as the Go builders print
// them (internal/config/testdata, kept current by TestUISchemaTestdataIsCurrent).
const testdata = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../../../internal/config/testdata",
);

function readSchema(name: string): JsonSchema {
  return JSON.parse(readFileSync(join(testdata, name), "utf8")) as JsonSchema;
}

/** The schema of an agent's Settings (config.UISchemaJSON). */
export const AGENT_SCHEMA: JsonSchema = readSchema("ui_schema.json");
/** The schema of a swarm relay's Settings (config.RelayUISchemaJSON). */
export const RELAY_SCHEMA: JsonSchema = readSchema("ui_schema_relay.json");

/**
 * The schema coddy serve hands the form of a binary that cannot run some
 * sections (config.UISchemaJSONHiding): the sections are gone from the
 * properties and from the property order. The settings document still holds
 * them, so a test that wants the server's whole answer keeps its document.
 */
export function schemaWithout(
  schema: JsonSchema,
  ...sections: string[]
): JsonSchema {
  const hidden = new Set(sections);
  return {
    ...schema,
    properties: Object.fromEntries(
      Object.entries(schema.properties ?? {}).filter(([k]) => !hidden.has(k)),
    ),
    "x-coddy-property-order": (schema["x-coddy-property-order"] ?? []).filter(
      (k) => !hidden.has(k),
    ),
  };
}

/** Values that look like secrets; no search may ever find one. */
export const SECRET_VALUES = {
  apiKey: "sk-live-9f3c1d7a52e4b608",
  apiKeyCommand: "vault-read-llm-credential-7731",
  proxyUrl: "http://proxyuser:pr0xyp4ss@egress.corp.example:3128",
  apiBase: "https://llm-gateway.corp.example/v2",
  telegramToken: "7391204455:AAHq-telegram-bot-secret",
  braveKey: "BSA-brave-search-secret-4471",
  searxngUrl: "https://searx.corp.example/search",
  relayToken: "relay-client-token-5528",
  pairingToken: "pairing-token-3190",
  metricsToken: "scrape-token-6204",
} as const;

/** A document to put in front of the form: what a server would hold for a user. */
export type SettingsDoc = Record<string, unknown>;

type PlainObject = Record<string, unknown>;

function isObjectItems(s: JsonSchema): boolean {
  return (
    s.type === "array" &&
    s.items !== undefined &&
    s.items.type === "object" &&
    s.items.properties !== undefined
  );
}

/** The defaults of a schema, with one entry in every list of objects. */
function filled(s: JsonSchema): unknown {
  if (s.type === "object" && s.properties) {
    const out: PlainObject = {};
    for (const [k, sub] of Object.entries(s.properties)) {
      out[k] = filled(sub);
    }
    return out;
  }
  if (isObjectItems(s)) {
    return [filled(s.items!)];
  }
  return defaultForSchema(s);
}

function setPath(doc: PlainObject, path: string, value: unknown): void {
  const keys = path.split(".");
  let at: PlainObject = doc;
  for (const key of keys.slice(0, -1)) {
    const next = at[key];
    if (next === null || typeof next !== "object" || Array.isArray(next)) {
      return;
    }
    at = next as PlainObject;
  }
  const last = keys[keys.length - 1]!;
  if (last in at) {
    at[last] = value;
  }
}

/**
 * A configuration document for a schema: its defaults, one entry in every list
 * of objects, and secret-looking values wherever the schema has a place for
 * one. An agent's schema also gets the rows the tests need: providers of every
 * type (the first with a proxy), a row without a name and two named "shared",
 * and two logical models.
 */
export function configFromSchema(schema: JsonSchema): SettingsDoc {
  const doc = filled(schema) as SettingsDoc;
  const providers = schema.properties?.["providers"];
  if (providers?.items) {
    const row = (over: PlainObject): PlainObject => ({
      ...(filled(providers.items!) as PlainObject),
      ...over,
    });
    doc["providers"] = [
      row({
        name: "openai",
        type: "openai",
        api_base: SECRET_VALUES.apiBase,
        api_key: SECRET_VALUES.apiKey,
        api_key_command: SECRET_VALUES.apiKeyCommand,
        proxy: SECRET_VALUES.proxyUrl,
      }),
      row({ name: "anthropic", type: "anthropic" }),
      row({ name: "neuraldeep", type: "neuraldeep" }),
      row({ name: "codex", type: "codex" }),
      row({ name: "devin", type: "devin" }),
      row({ name: "", type: "openai" }),
      row({ name: "shared", type: "openai" }),
      row({ name: "shared", type: "anthropic" }),
    ];
  }
  const models = schema.properties?.["models"];
  if (models?.items) {
    const row = (model: string): PlainObject => ({
      ...(filled(models.items!) as PlainObject),
      model,
    });
    doc["models"] = [row("demo/qwen3-27b"), row("demo/llama-3")];
  }
  setPath(doc, "gateways.telegram.token", SECRET_VALUES.telegramToken);
  setPath(doc, "tools.websearch.brave_api_key", SECRET_VALUES.braveKey);
  setPath(doc, "tools.websearch.searxng_url", SECRET_VALUES.searxngUrl);
  setPath(doc, "swarm.auth_token", SECRET_VALUES.relayToken);
  setPath(doc, "swarm.pairing_tokens", [SECRET_VALUES.pairingToken]);
  setPath(doc, "metrics.token", SECRET_VALUES.metricsToken);
  return doc;
}

export type ServerState = {
  schema: unknown;
  config: PlainObject;
  /** What PUT /coddy/config answers; ok unless a test says otherwise. */
  put: { status: number; body: PlainObject };
};

export function newServerState(
  init: { schema: unknown; config: PlainObject } & Partial<ServerState>,
): ServerState {
  return { put: { status: 200, body: { ok: true } }, ...init };
}

/**
 * A coddy serve behind fetch: the schema, the config document, a save, and the
 * routes the settings tabs read, each with an empty list in the shape the tab
 * reads. The state is read on every request, so a test that changes it (or
 * hands a getter that returns its current state) is heard by the next read.
 * A route it does not know is refused (the request rejects, and its address is
 * kept in `unexpected`) and fails the test when it finishes, so a tab that
 * starts asking for something new fails a test instead of silently getting an
 * answer or having its refusal swallowed.
 */
export function stubServer(source: ServerState | (() => ServerState)) {
  const state = typeof source === "function" ? source : () => source;
  const unexpected: string[] = [];
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    const server = state();
    const method = (init?.method ?? "GET").toUpperCase();
    const route = new URL(String(path), "http://coddy.test").pathname;
    const reply = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      json: async () => structuredClone(body),
    });
    if (route === "/coddy/config/schema") return reply(200, server.schema);
    if (route === "/coddy/config" && method === "GET") {
      return reply(200, server.config);
    }
    if (route === "/coddy/config/validate") return reply(200, { ok: true });
    if (route === "/coddy/config" && method === "PUT") {
      if (server.put.status === 200) {
        server.config = JSON.parse(String(init?.body)) as PlainObject;
      }
      return reply(server.put.status, server.put.body);
    }
    if (route === "/coddy/config/reasoning-levels") {
      return reply(200, { ok: true, levels: [] });
    }
    if (route === "/coddy/providers/models") {
      return reply(200, { ok: true, models: [] });
    }
    if (/^\/coddy\/providers\/[^/]+\/(codex|neuraldeep)-auth$/.test(route)) {
      return reply(200, { connected: false });
    }
    if (route === "/coddy/mcp") {
      return reply(200, { items: [], project_trust: "ask", workspace: "" });
    }
    if (route === "/coddy/mcp/project-trust") return reply(200, { ok: true });
    if (route === "/coddy/skills") return reply(200, { items: [] });
    if (route === "/coddy/skills/sources") {
      return reply(200, {
        workspace: "",
        project_trust: "ask",
        entries: [],
        errors: [],
      });
    }
    if (route === "/coddy/skills/updates") return reply(200, { items: [] });
    if (route === "/coddy/skills/available") return reply(200, { items: [] });
    if (route === "/coddy/subagents") {
      return reply(200, { items: [], workspace: "", policy: "ask" });
    }
    if (route === "/coddy/sessions") {
      return reply(200, { sessions: [], hasMore: false });
    }
    unexpected.push(`${method} ${path}`);
    throw new Error(`stubServer: no route for ${method} ${path}`);
  });
  vi.stubGlobal("fetch", fetch);
  // A tab that swallows the refusal must not let the test pass.
  onTestFinished(() => {
    if (unexpected.length > 0) {
      throw new Error(
        `stubServer: requests outside its routes: ${unexpected.join(", ")}`,
      );
    }
  });
  return Object.assign(fetch, { unexpected });
}

// ---- the page around the drawer: viewport, input device, language, Telegram

type Viewport = { width: number; touch: boolean };

function conditionHolds(condition: string, vp: Viewport): boolean {
  const px = /^\(\s*(max|min)-width:\s*(\d+)px\s*\)$/.exec(condition);
  if (px) {
    const limit = Number(px[2]);
    return px[1] === "max" ? vp.width <= limit : vp.width >= limit;
  }
  switch (condition.replace(/\s+/g, " ")) {
    case "(any-hover: none)":
    case "(hover: none)":
    case "(any-pointer: coarse)":
    case "(pointer: coarse)":
      return vp.touch;
    case "(any-hover: hover)":
    case "(hover: hover)":
    case "(any-pointer: fine)":
    case "(pointer: fine)":
      return !vp.touch;
    default:
      return false;
  }
}

/** Whether a media query holds on a viewport; the ones the SPA asks, nothing more. */
function queryHolds(query: string, vp: Viewport): boolean {
  return query.split(",").some((group) =>
    group
      .trim()
      .split(/\s+and\s+/)
      .every((condition) => conditionHolds(condition.trim(), vp)),
  );
}

const originalMatchMedia = window.matchMedia;

/** Makes window.matchMedia answer for a viewport width and an input device. */
export function setViewport(vp: Partial<Viewport> = {}): void {
  const viewport: Viewport = { width: 1280, touch: false, ...vp };
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: queryHolds(query, viewport),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}

const TELEGRAM_FRAGMENT = new URLSearchParams({
  tgWebAppData: "query_id=q1&user=%7B%22id%22%3A4242%7D&auth_date=1&hash=abc",
  tgWebAppVersion: "10.1",
  tgWebAppPlatform: "ios",
}).toString();

/** Opens the page as a Telegram Mini App: a captured launch, as telegram.test.tsx does. */
export function launchAsTelegram(): void {
  window.history.replaceState(null, "", `/#${TELEGRAM_FRAGMENT}`);
  captureTelegramLaunch(window);
}

export type PageOptions = {
  /** Viewport width in px; 1280 is the rail, up to 1199 the stacked shell. */
  width?: number;
  /** A touch-only device: no input can hover and a pointer is coarse. */
  touch?: boolean;
  lang?: "en" | "ru";
  telegram?: boolean;
};

/** Puts the page around a drawer in the state the options name. */
export function applyPage(opts: PageOptions = {}): void {
  initLocale("en");
  setLocale(opts.lang ?? "en");
  setViewport({
    ...(opts.width !== undefined ? { width: opts.width } : {}),
    ...(opts.touch !== undefined ? { touch: opts.touch } : {}),
  });
  resetTelegramLaunchForTests();
  window.sessionStorage.clear();
  if (opts.telegram) {
    launchAsTelegram();
  }
}

/** Undoes applyPage; call it from afterEach. */
export function resetPage(): void {
  window.matchMedia = originalMatchMedia;
  resetTelegramLaunchForTests();
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/");
  setLocale("en");
}

export type MountOptions = PageOptions & {
  schema?: JsonSchema;
  config?: SettingsDoc;
  /**
   * The tab, the row and the setting the address names
   * (`#/settings/<section>?id=<item>&field=<anchor>`).
   */
  section?: string | null;
  item?: string | null;
  field?: string | null;
  onClose?: () => void;
  /**
   * The drawer follows the address as App makes it: `section` and `item` are
   * written to the hash first, and a change of the hash (the drawer's own
   * writes included) is handed back to it as its `initialSection`,
   * `initialItem` and `initialField`. Without it the three stay what they were
   * mounted with.
   */
  routed?: boolean;
  /** Rendered before the drawer, so it listens first (an OpenRailScreen). */
  beside?: ReactNode;
};

/** Settings the way App mounts it: the address says which tab and row are open. */
function RoutedSettings(props: {
  onClose: () => void;
  relay: boolean;
}): ReactNode {
  const read = () => {
    const route = parseAppHash();
    return route.branch === "settings"
      ? { section: route.section, item: route.item, field: route.field }
      : { section: null, item: null, field: null };
  };
  const [route, setRoute] = useState(read);
  useEffect(() => {
    const on = () => setRoute(read());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return createElement(Settings, {
    onClose: props.onClose,
    initialSection: route.section,
    initialItem: route.item,
    initialField: route.field,
    relay: props.relay,
  });
}

/** The page, the stubbed server and the document of a drawer about to mount. */
function prepareDrawer(opts: MountOptions) {
  const schema = opts.schema ?? AGENT_SCHEMA;
  const config = opts.config ?? configFromSchema(schema);
  applyPage(opts);
  resetSettingsConfigForTests();
  const server = newServerState({ schema, config });
  const fetch = stubServer(server);
  const relay = schema["x-coddy-relay"] === true;
  const onClose = opts.onClose ?? (() => {});
  if (opts.routed) {
    // The address App would hold for this tab and row.
    if (opts.section) {
      setSettingsSectionHash(opts.section, {
        item: opts.item ?? null,
        field: opts.field ?? null,
      });
    } else {
      window.location.hash = "#/settings";
    }
  }
  const draw = () =>
    render(
      createElement(
        ConfirmProvider,
        null,
        opts.beside ?? null,
        opts.routed
          ? createElement(RoutedSettings, { onClose, relay })
          : createElement(Settings, {
              onClose,
              initialSection: opts.section ?? null,
              initialItem: opts.item ?? null,
              initialField: opts.field ?? null,
              relay,
            }),
      ),
    );
  return { draw, fetch, server };
}

/**
 * Mounts the Settings drawer over a stubbed server: the schema and the config
 * document (by default the agent's schema and configFromSchema of it), on a
 * page of the viewport, language and launch the options name. A relay's schema
 * mounts the relay's drawer. The first frame is the drawer waiting for its
 * copy of the configuration, as on the first open of a page.
 */
export function mountSettings(opts: MountOptions = {}) {
  const { draw, fetch, server } = prepareDrawer(opts);
  return { ...draw(), fetch, server };
}

/**
 * mountSettings for a drawer opened later in the life of a page: the copy of
 * the configuration is already read, so the first frame draws the tab (and the
 * row an item names) at once.
 */
export async function openSettings(opts: MountOptions = {}) {
  const { draw, fetch, server } = prepareDrawer(opts);
  await ensureSettingsConfig();
  return { ...draw(), fetch, server };
}
