import { afterEach, beforeEach, expect, test } from "vitest";

import { setLocale, translate } from "../i18n/i18n";
import { messagesEn } from "../i18n/messages/en";
import { messagesRu } from "../i18n/messages/ru";
import { resetTelegramLaunchForTests } from "../telegram/launch";
import {
  buildSettingsSearchIndex,
  descriptionLine,
  makeSearchEntry,
  markWords,
  normalizeSearchText,
  searchSettings,
  searchWords,
  settingsIndexRowsKey,
  SETTINGS_LANDMARKS,
  SETTINGS_SEARCH_LIMIT,
  type SettingsSearchEntry,
} from "./settingsSearch";
import {
  AGENT_SCHEMA,
  RELAY_SCHEMA,
  SECRET_VALUES,
  configFromSchema,
  launchAsTelegram,
  schemaWithout,
  type SettingsDoc,
} from "./settingsServer.fakes";
import {
  deriveSettingsSections,
  SETTINGS_TEXTS,
  landmarkAnchor,
} from "./settingsSections";
import { schemaFieldLabel } from "./schemaI18n";
import {
  entriesInSchemaOrder,
  fieldAnchor,
  groupAnchor,
  type JsonSchema,
} from "./SchemaForm";
import { objectSectionGroups } from "./SettingsSection";

beforeEach(() => {
  setLocale("en");
  resetTelegramLaunchForTests();
});

afterEach(() => {
  setLocale("en");
  resetTelegramLaunchForTests();
});

/** One entry of a hand-made index, built the way the index builds its own. */
const entry = (
  over: Partial<Parameters<typeof makeSearchEntry>[0]> & { names: string[] },
): SettingsSearchEntry =>
  makeSearchEntry({ kind: "field", section: "tools", ...over });

/** What a search found, as the primary names in the order it shows them. */
function found(
  index: readonly SettingsSearchEntry[],
  query: string,
  limit?: number,
): string[] {
  return searchSettings(index, query, limit).hits.map((h) => h.entry.names[0]!);
}

/** Every query finds exactly these primary names. */
function expectFound(
  index: readonly SettingsSearchEntry[],
  queries: string[],
  names: string[],
) {
  for (const query of queries) {
    expect(found(index, query), query).toEqual(names);
  }
}

function indexOf(
  schema: JsonSchema,
  doc: SettingsDoc = configFromSchema(schema),
): SettingsSearchEntry[] {
  return buildSettingsSearchIndex({
    sections: deriveSettingsSections(schema),
    schema,
    doc,
  });
}

type Entries = readonly SettingsSearchEntry[];
const entriesAt = (index: Entries, anchor: string) =>
  index.filter((e) => e.anchor === anchor);
const inTab = (index: Entries, id: string) =>
  index.filter((e) => e.section === id);
const anchors = (entries: Entries) => entries.map((e) => e.anchor);

function only(
  index: readonly SettingsSearchEntry[],
  anchor: string,
  row: string | null = null,
): SettingsSearchEntry {
  const matches = entriesAt(index, anchor).filter((e) => e.row === row);
  expect(matches.map((e) => e.id)).toHaveLength(1);
  return matches[0]!;
}

/** The rows of the default document's provider list. */
const PROVIDER_ROWS = ["openai", "anthropic", "neuraldeep", "codex", "devin"];

/** The label the page draws for a field of a section or list item, English by default. */
function schemaLabel(domain: string, key: string): string {
  const node = AGENT_SCHEMA.properties![domain]!;
  const props = (node.properties ?? node.items!.properties)!;
  return schemaFieldLabel(domain, key, props[key]!.title, key);
}

// ---- matching, on hand-made entries

test("every word of the query has to occur in the own names, the config key, the surrounding names or the descriptions", () => {
  const index = [
    entry({
      names: ["Proxy URL"],
      keyPath: "providers[].proxy",
      where: ["LLM providers", "openai", "Advanced settings"],
      descriptions: ["Route requests through this proxy server"],
    }),
  ];
  // the name, the config key, the names around it, the descriptions; a word is
  // a substring, words may come from different places, in any order
  expectFound(
    index,
    [
      "proxy",
      "providers[]",
      "openai",
      "advanced openai",
      "route",
      "prox",
      "proxy openai route",
      "route proxy",
    ],
    ["Proxy URL"],
  );
  // every word has to occur somewhere
  expectFound(index, ["proxy nowhere", "nowhere"], []);
  // words do not join across the borders between two names
  expect(found([entry({ names: ["Max", "turns"] })], "maxturns")).toEqual([]);
});

test("own names and config keys come first with tabs and blocks ahead, then matches needing the surrounding names, then the descriptions", () => {
  const index = [
    entry({ names: ["Dir"], where: ["Memory"] }),
    entry({ names: ["Wait"], descriptions: ["Waits for a memory run"] }),
    entry({ names: ["Memory model"] }),
    entry({ kind: "section", names: ["Memory"], anchor: null }),
    entry({ kind: "group", names: ["Memory runs"], anchor: "group:m:runs" }),
    entry({ names: ["Directory"], keyPath: "memory.dir" }),
    entry({ kind: "landmark", names: ["Memory tools"] }),
    entry({ kind: "row", names: ["memory"], row: "memory" }),
    entry({ names: ["Needs both"], where: ["Memory"], descriptions: ["x"] }),
  ];
  // tier 1: tabs, blocks and landmarks in the drawer's order, then the rest in
  // the drawer's order; tier 2: the surrounding names; tier 3: descriptions
  const hits = searchSettings(index, "memory").hits;
  expect(hits.map((h) => h.entry.names[0])).toEqual([
    "Memory",
    "Memory runs",
    "Memory tools",
    "Memory model",
    "Directory",
    "memory",
    "Dir",
    "Needs both",
    "Wait",
  ]);
  expect(hits.map((h) => h.tier)).toEqual([1, 1, 1, 1, 1, 1, 2, 2, 3]);
  // a word needing the descriptions makes the whole match tier 3
  expect(
    searchSettings(index, "memory x").hits.map((h) => [
      h.entry.names[0],
      h.tier,
    ]),
  ).toEqual([["Needs both", 3]]);
});

test("a nested object, a list and a map are blocks: they lead the first tier ahead of the fields", () => {
  const index = indexOf(AGENT_SCHEMA);
  const kindOf = (anchor: string) =>
    index.find((e) => e.anchor === anchor)?.kind;
  expect(kindOf(fieldAnchor("tools", "websearch"))).toBe("block");
  expect(kindOf(fieldAnchor("tools", "command_allowlist"))).toBe("block");
  expect(kindOf(fieldAnchor("tools", "http_request.default_headers"))).toBe(
    "block",
  );
  expect(kindOf(fieldAnchor("tools", "output_limits.websearch"))).toBe("field");
  // a field an override draws stays a field, whatever its schema
  expect(kindOf(fieldAnchor("providers", "proxy"))).toBe("field");
  const hits = searchSettings(index, "web search").hits;
  const at = (anchor: string) =>
    hits.findIndex((h) => h.entry.anchor === anchor);
  expect(at(fieldAnchor("tools", "websearch"))).toBeGreaterThanOrEqual(0);
  expect(at(fieldAnchor("tools", "websearch"))).toBeLessThan(
    at(fieldAnchor("tools", "output_limits.websearch")),
  );
});

test("case and ё do not matter, and nothing fuzzy matches", () => {
  expect(normalizeSearchText("ЁЖ Ёж Её")).toBe("еж еж ее");
  const index = [
    entry({ names: ["Ёлка и Ежик"] }),
    entry({ names: ["Max Turns"] }),
    entry({ names: ["Café"] }),
  ];

  expectFound(index, ["ёЛКА", "елка", "ЕЖИК"], ["Ёлка и Ежик"]);
  expectFound(index, ["MAX turns"], ["Max Turns"]);
  // no folding of anything else: not accents, not transposed or missing letters
  expectFound(index, ["cafe", "mxa", "trns", "ёлки"], []);
  // the Latin e is not the Cyrillic е
  expectFound([entry({ names: ["Елка"] })], ["elka"], []);
});

test("a query of spaces searches nothing", () => {
  const index = [entry({ names: ["Max turns"] })];

  for (const query of ["", " ", "   ", "\t\n ", " "]) {
    expect(searchSettings(index, query)).toEqual({ hits: [], more: 0 });
  }
  expect(searchWords("  Max   TURNS ")).toEqual(["max", "turns"]);
  expect(searchWords("   ")).toEqual([]);
  expect(found(index, "  turns  ")).toEqual(["Max turns"]);
});

test("past the limit the matches are counted, not shown", () => {
  const index = Array.from({ length: 120 }, (_, i) =>
    entry({ names: [`Field ${i}`] }),
  );

  expect(SETTINGS_SEARCH_LIMIT).toBe(50);
  const shown = searchSettings(index, "field");
  expect(shown.more).toBe(70);
  // the ones shown are the first of the drawer's order
  expect(found(index, "field")).toEqual(
    Array.from({ length: 50 }, (_, i) => `Field ${i}`),
  );
  expect(searchSettings(index, "field", 10)).toMatchObject({ more: 110 });
  expect(searchSettings(index, "field 119", 10).more).toBe(0);
  expect(searchSettings(index, "field", 200)).toMatchObject({ more: 0 });
  expect(found(index, "field", 200)).toHaveLength(120);
  // the limit cuts the sorted list: a better tier is never pushed out
  const late = [
    ...Array.from({ length: 60 }, (_, i) =>
      entry({ names: [`Plain ${i}`], descriptions: ["target"] }),
    ),
    entry({ names: ["Target"] }),
  ];
  expect(found(late, "target")[0]).toBe("Target");
});

test("the marked parts of a text are exactly the words of the query", () => {
  /** The parts of the text with the marked ones in brackets. */
  const marked = (text: string, words: string[]) =>
    markWords(text, words)
      .map((p) => (p.hit ? `[${p.text}]` : p.text))
      .join("");
  const cases: [text: string, words: string[], marked: string][] = [
    ["Ignore system proxy", ["proxy", "ign"], "[Ign]ore system [proxy]"],
    // the text keeps its own case, the match does not depend on it
    ["PROXY url", ["proxy"], "[PROXY] url"],
    // overlapping and repeated words make one mark
    ["proxy proxy", ["pro", "proxy", "prox"], "[proxy] [proxy]"],
    ["Ёлка", ["елка"], "[Ёлка]"],
    ["nothing to mark", ["zzz"], "nothing to mark"],
    ["nothing", [], "nothing"],
  ];

  for (const [text, words, expected] of cases) {
    expect(marked(text, words), text).toBe(expected);
  }
  expect(markWords("", ["a"])).toEqual([]);
  // whatever the words, the parts are the text
  for (const text of ["Max turns (i)", "İstanbul İ", "a.b.c", "ёЁёе"]) {
    expect(
      markWords(text, searchWords("a b i е"))
        .map((p) => p.text)
        .join(""),
    ).toBe(text);
  }
});

test("a description-only match shows its description from shortly before the word", () => {
  const text =
    "Sends every request of this row through a corporate egress proxy, so nothing leaves the network directly, whatever the rest of the settings say.";
  const line = descriptionLine(text, ["egress"]);

  expect(line.startsWith("…")).toBe(true);
  const rest = line.slice(1);
  expect(text.endsWith(rest)).toBe(true);
  expect(rest).toContain("egress");
  // from shortly before the word, at a word boundary, and not cut at the end
  const lead = rest.indexOf("egress");
  expect(lead).toBeGreaterThan(0);
  expect(lead).toBeLessThanOrEqual(40);
  expect(text[text.length - rest.length - 1]).toBe(" ");
  // a word near the start needs no cut, and a text without one is shown whole
  for (const words of [["sends"], ["zzz"], []]) {
    expect(descriptionLine(text, words), words.join()).toBe(text);
  }
  // the first of the words that occur decides
  expect(descriptionLine(text, ["rest", "egress"])).toBe(line);
  // a word that is later than a space-less stretch is still shown
  const glued = `${"x".repeat(100)}needle tail`;
  expect(descriptionLine(glued, ["needle"])).toContain("needle");
});

test("a word found only in a second name is shown beside the primary name", () => {
  const proxy = entry({
    names: ["Proxy URL", "Ignore system proxy"],
    keyPath: "providers[].proxy",
    descriptions: ["Route requests through this proxy server"],
  });
  const second = (query: string, e = proxy) =>
    searchSettings([e], query).hits[0]?.secondMatched;
  const ignore = "Ignore system proxy";
  const cases: [string, string | null][] = [
    ["ignore", ignore],
    ["ignore proxy", ignore],
    ["url ignore", ignore],
    // found in the primary name, or in the key, the second name stays out
    ["proxy", null],
    ["url", null],
    ["providers", null],
    // not found in a name at all
    ["route", null],
  ];

  for (const [query, expected] of cases) {
    expect(second(query), query).toBe(expected);
  }
  // an entry with one name never has one
  expect(second("turns", entry({ names: ["Max turns"] }))).toBeNull();
});

// ---- the index, on the served schemas

test("the index lists the tabs, their blocks and their fields in the drawer's order", () => {
  const index = indexOf(AGENT_SCHEMA);
  const sections = deriveSettingsSections(AGENT_SCHEMA);
  const label = (id: string) => sections.find((s) => s.id === id)!.label;
  // the tabs, in the rail's order, each followed by what it holds
  const tabs = index.filter((e) => e.kind === "section");
  expect(tabs.map((e) => e.section)).toEqual(sections.map((s) => s.id));
  expect(tabs.map((e) => e.names[0])).toEqual(sections.map((s) => s.label));
  expect(tabs.every((e) => e.anchor === null && e.row === null)).toBe(true);
  for (const s of sections) {
    const own = inTab(index, s.id);
    expect(own[0]!.kind).toBe("section");
    const at = index.indexOf(own[0]!);
    expect(index.slice(at, at + own.length)).toEqual(own);
  }
  // an object tab with blocks: the blocks stand in the order the tab declares
  // them, and each field of a block after it and before the next one
  const agent = inTab(index, "agent");
  const declared = objectSectionGroups("agent")!;
  const blocks = agent.filter((e) => e.kind === "group");
  expect(anchors(blocks)).toEqual(
    declared.map((g) => groupAnchor("agent", g.id)),
  );
  expect(blocks.map((e) => e.names)).toEqual(declared.map((g) => [g.legend]));
  const agentSchema = AGENT_SCHEMA.properties!["agent"]!;
  const schemaKeys = entriesInSchemaOrder(
    agentSchema.properties!,
    agentSchema["x-coddy-property-order"],
  ).map(([k]) => k);
  declared.forEach((g, i) => {
    const from = agent.indexOf(blocks[i]!);
    const to =
      i + 1 < blocks.length ? agent.indexOf(blocks[i + 1]!) : agent.length;
    for (const path of g.paths!) {
      const field = only(index, fieldAnchor("agent", path));
      expect(agent.indexOf(field), path).toBeGreaterThan(from);
      expect(agent.indexOf(field), path).toBeLessThan(to);
      expect(field.where, path).toEqual([label("agent"), g.legend]);
    }
    // inside a block the fields keep the schema's order
    expect(
      agent.slice(from + 1, to).map((e) => e.keyPath),
      g.id,
    ).toEqual(
      schemaKeys.filter((k) => g.paths!.includes(k)).map((k) => `agent.${k}`),
    );
  });
  const turns = only(index, "agent:max_turns");
  expect(turns).toMatchObject({
    keyPath: "agent.max_turns",
    kind: "field",
    names: [schemaLabel("agent", "max_turns")],
  });
  // a section's own switch opens its form, outside every block
  expect(anchors(inTab(index, "supervisor").slice(0, 3))).toEqual([
    null,
    "supervisor:enable",
    "group:supervisor:checks",
  ]);
  // an object field is an entry of its own, followed by its children
  const limits = only(index, "tools:output_limits");
  const read = only(index, "tools:output_limits.read");
  expect(index.indexOf(read)).toBe(index.indexOf(limits) + 1);
  expect(read).toMatchObject({
    where: [...limits.where, limits.names[0]],
    keyPath: "tools.output_limits.read",
    list: null,
  });
  // a list of plain values, a map and a list of objects are one entry each
  const groupsList = only(index, "gateways:telegram.user_groups");
  for (const anchor of [
    "tools:command_allowlist",
    "tools:http_request.default_headers",
    groupsList.anchor!,
  ]) {
    expect(entriesAt(index, anchor), anchor).toHaveLength(1);
  }
  // its children carry the list and no count; the entry frame is no entry
  expect(groupsList.list).toBeNull();
  expect(only(index, "gateways:telegram.user_groups.name")).toMatchObject({
    list: groupsList.anchor,
    fallbackAnchor: groupsList.anchor,
    keyPath: "gateways.telegram.user_groups[].name",
    where: [...groupsList.where, groupsList.names[0]],
  });
  // a nested object inside an entry keeps the list
  expect(
    only(indexOf(RELAY_SCHEMA), "swarm:upstreams.dial.proxy"),
  ).toMatchObject({
    list: "swarm:upstreams",
    keyPath: "swarm.upstreams[].dial.proxy",
  });
  // a list tab: each row that can be opened is an entry, then its own fields,
  // row after row
  const providers = inTab(index, "providers");
  const rows = providers.filter((e) => e.kind === "row");
  expect(rows.map((e) => e.row)).toEqual(PROVIDER_ROWS);
  for (const row of rows) {
    const own = providers.filter((e) => e.row === row.row);
    const at = providers.indexOf(row);
    expect(own[0]).toBe(row);
    expect(providers.slice(at, at + own.length)).toEqual(own);
  }
  // the blocks a tab draws by hand are landmarks, in the order they are
  // declared: those above the tab's forms come first, those below come last
  for (const id of [
    "appearance",
    "sessions_manager",
    "skills",
    "mcp_servers",
  ]) {
    const marks = (place: "before" | "after") =>
      SETTINGS_LANDMARKS.filter(
        (l) => l.section === id && l.row === undefined && l.place === place,
      ).map((l) => landmarkAnchor(l.section, l.id));
    const entries = inTab(index, id).slice(1);
    const [above, below] = [marks("before"), marks("after")];
    const middle = entries.slice(above.length, entries.length - below.length);
    expect(anchors(entries.slice(0, above.length)), id).toEqual(above);
    expect(anchors(entries.slice(entries.length - below.length)), id).toEqual(
      below,
    );
    expect(
      middle.some((e) => e.kind === "landmark"),
      id,
    ).toBe(false);
  }
  expect(only(index, "landmark:skills:auto_discovery").keyPath).toBe(
    "skills.auto_discovery",
  );
  // the field a landmark draws by hand is not indexed a second time
  expect(entriesAt(index, "skills:auto_discovery")).toEqual([]);
  expect(only(index, "landmark:mcp_servers:discovery").keyPath).toBe(
    "mcp.project_trust",
  );
  // a folded block carries no description; the Prompts tab nests its children
  expect(
    only(index, "group:providers:advanced", "openai").descriptions,
  ).toEqual([]);
  expect(anchors(inTab(index, "system"))).toEqual(
    expect.arrayContaining([
      "group:system.prompts:prompts",
      "system.prompts:dir",
    ]),
  );
  // nothing is indexed twice
  expect(new Set(index.map((e) => e.id)).size).toBe(index.length);
  const places = index
    .map((e) => `${e.section}|${e.row ?? ""}|${e.anchor}`)
    .filter((p) => !p.endsWith("|null"));
  expect(new Set(places).size).toBe(places.length);
  // text is folded once, when the index is built
  expect(turns.hay.own).toBe(
    normalizeSearchText(`${turns.names[0]}\nagent.max_turns`),
  );
  // a relay has its own tabs and no Sessions
  expect(
    indexOf(RELAY_SCHEMA)
      .filter((e) => e.kind === "section")
      .map((e) => e.section),
  ).toEqual(["appearance", "swarm", "logger"]);
  // without a schema only the tabs that need none are there, holding nothing
  // but their hand-drawn blocks
  const bare = buildSettingsSearchIndex({
    sections: deriveSettingsSections(null),
    schema: null,
    doc: {},
  });
  expect(
    bare.filter((e) => e.kind === "section").map((e) => e.section),
  ).toEqual(["appearance", "sessions_manager"]);
  expect(bare.every((e) => e.kind === "section" || e.kind === "landmark")).toBe(
    true,
  );
});

test("a field of a provider or model row is indexed once per row, under that row's name", () => {
  const index = indexOf(AGENT_SCHEMA);
  const providersLabel = translate("settings.section.providers.label");
  const ignoreSystem = translate("settings.providerProxy.ignoreSystem");
  const rowsOf = (anchor: string) => entriesAt(index, anchor).map((e) => e.row);

  expect(
    index
      .filter((e) => e.kind === "row" && e.section === "providers")
      .map((e) => [e.row, e.names, e.anchor, e.where]),
  ).toEqual(
    PROVIDER_ROWS.map((name) => [
      name,
      [name],
      "row:providers",
      [providersLabel],
    ]),
  );
  // the row's name leads the names around each of its fields
  expect(
    PROVIDER_ROWS.map((name) =>
      only(index, "providers:proxy", name).where.slice(0, 2),
    ),
  ).toEqual(PROVIDER_ROWS.map((name) => [providersLabel, name]));
  expect(rowsOf("providers:proxy")).toEqual(PROVIDER_ROWS);
  // models: one row each, the id's two names, no description
  const models = ["demo/qwen3-27b", "demo/llama-3"];
  expect(
    entriesAt(index, "models:model").map((e) => [
      e.row,
      e.names,
      e.descriptions,
    ]),
  ).toEqual(
    models.map((m) => [
      m,
      [schemaLabel("models", "model"), translate("settings.field.provider")],
      [],
    ]),
  );
  expect(only(index, "models:model", "demo/llama-3").keyPath).toBe(
    "models[].model",
  );
  // what an override declares is what is indexed
  const proxy = only(index, "providers:proxy", "openai");
  expect(proxy.names).toEqual([
    schemaLabel("providers", "proxy"),
    ignoreSystem,
  ]);
  expect(proxy.descriptions).toHaveLength(2);
  expect(only(index, "providers:api_base", "codex")).toMatchObject({
    names: [translate("codexAuth.fieldLabel")],
    descriptions: [translate("codexAuth.description")],
  });
  expect(only(index, "providers:api_base", "neuraldeep")).toMatchObject({
    names: [expect.any(String), translate("neuralDeepAuth.fieldLabel")],
    descriptions: [
      translate("neuralDeepApiBase.description"),
      translate("neuralDeepAuth.description"),
    ],
  });
  expect(only(index, "providers:api_base", "openai").names).toHaveLength(1);
  expect(only(index, "gateways:telegram.proxy")).toMatchObject({
    names: [expect.any(String), ignoreSystem],
    descriptions: [
      expect.any(String),
      translate("settings.gatewayProxy.ignoreSystemDesc"),
    ],
  });
  expect(entriesAt(index, "gateways:pachca.proxy")).toHaveLength(1);
  // which rows a field is indexed for: only the ones whose form draws it
  expect(rowsOf("providers:api_key")).toContain("openai");
  expect(rowsOf("providers:api_key")).not.toContain("codex");
  expect(rowsOf("providers:api_key_command")).toContain("devin");
  expect(rowsOf("providers:api_key_command")).not.toContain("codex");
  expect(rowsOf("providers:usage_limits_panel")).toEqual([
    "neuraldeep",
    "codex",
    "devin",
  ]);
  // the Models block of the provider form is a landmark of every row
  expect(rowsOf("landmark:providers:models")).toEqual(PROVIDER_ROWS);
  // the rows are the form's own: a type edited in the document is the type
  // the index reads
  const doc = configFromSchema(AGENT_SCHEMA);
  (doc["providers"] as Record<string, unknown>[])[0]!["type"] = "codex";
  expect(
    entriesAt(indexOf(AGENT_SCHEMA, doc), "providers:api_key").map(
      (e) => e.row,
    ),
  ).not.toContain("openai");
});

test("a row without a name and rows that share a name are left out, so no result opens the wrong row", () => {
  const rowsOf = (index: Entries, section: string) => [
    ...new Set(
      inTab(index, section)
        .filter((e) => e.row !== null)
        .map((e) => e.row),
    ),
  ];
  const defaults = indexOf(AGENT_SCHEMA);
  // the document has an unnamed provider and two called "shared"
  expect(rowsOf(defaults, "providers")).toEqual(PROVIDER_ROWS);
  expect(defaults.some((e) => e.row === "" || e.row === "shared")).toBe(false);
  expect(
    defaults.some(
      (e) => e.where.includes("shared") || e.names.includes("shared"),
    ),
  ).toBe(false);
  // a name is the row's trimmed label, as the list trims it
  const doc = configFromSchema(AGENT_SCHEMA);
  const providers = doc["providers"] as Record<string, unknown>[];
  providers[5]!["name"] = "  padded  ";
  providers[6]!["name"] = "twin";
  providers[7]!["name"] = " twin ";
  const trimmed = indexOf(AGENT_SCHEMA, doc);
  expect(rowsOf(trimmed, "providers")).toEqual([...PROVIDER_ROWS, "padded"]);
  expect(only(trimmed, "providers:proxy", "padded").where[1]).toBe("padded");
  // the same holds for logical models
  const models = doc["models"] as Record<string, unknown>[];
  models[1]!["model"] = "demo/qwen3-27b";
  expect(rowsOf(indexOf(AGENT_SCHEMA, doc), "models")).toEqual([]);
  models[1]!["model"] = "   ";
  expect(rowsOf(indexOf(AGENT_SCHEMA, doc), "models")).toEqual([
    "demo/qwen3-27b",
  ]);
  // a document without a list has no rows, and no field of one
  const bare = indexOf(AGENT_SCHEMA, {});
  expect(inTab(bare, "providers").every((e) => e.kind === "section")).toBe(
    true,
  );
  expect(entriesAt(bare, "agent:max_turns")).toHaveLength(1);
});

test("the rows key changes with the names and types of the rows and with nothing else", () => {
  const doc = configFromSchema(AGENT_SCHEMA);
  const key = settingsIndexRowsKey(doc);
  const row = (d: SettingsDoc, list: string, i: number) =>
    (d[list] as Record<string, unknown>[])[i]!;
  /** Whether the edit changes the key. */
  const changes = (edit: (d: SettingsDoc) => void) => {
    const copy = structuredClone(doc);
    edit(copy);
    return settingsIndexRowsKey(copy) !== key;
  };

  expect(key).toBe(settingsIndexRowsKey(structuredClone(doc)));
  expect(changes((d) => (row(d, "providers", 0)["name"] = "renamed"))).toBe(
    true,
  );
  expect(changes((d) => (row(d, "providers", 0)["type"] = "codex"))).toBe(true);
  expect(changes((d) => (row(d, "models", 0)["model"] = "x/y"))).toBe(true);
  expect(changes((d) => (row(d, "providers", 1)["api_key"] = "other"))).toBe(
    false,
  );
  expect(
    changes((d) => ((d["agent"] as Record<string, unknown>)["max_turns"] = 1)),
  ).toBe(false);
  expect(settingsIndexRowsKey({})).toBe(settingsIndexRowsKey({ agent: {} }));
});

test("values are never indexed: a key, a token or a URL of the document finds nothing", () => {
  const index = indexOf(AGENT_SCHEMA);
  const relay = indexOf(RELAY_SCHEMA);

  for (const [name, value] of Object.entries(SECRET_VALUES)) {
    for (const piece of [value, value.slice(0, 14), value.split("//").pop()!]) {
      expect(found(index, piece), `${name}: ${piece}`).toEqual([]);
      expect(found(relay, piece), `${name}: ${piece}`).toEqual([]);
    }
    const lowered = value.toLowerCase();
    for (const e of [...index, ...relay]) {
      const text = [e.hay.own, e.hay.where, e.hay.descriptions].join("\n");
      expect(text.includes(lowered), `${name} in ${e.id}`).toBe(false);
    }
  }
  // the names of the secrets are there
  expect(only(index, "tools:websearch.brave_api_key").names[0]).toBeTruthy();
  expect(found(index, "brave").length).toBeGreaterThan(0);
  // the one value that is read: a row's name
  expect(found(index, "qwen3-27b")).toContain("demo/qwen3-27b");
  expect(found(index, "anthropic")).toContain("anthropic");
});

test("the index speaks the page's language and keeps the config key as YAML spells it", () => {
  setLocale("ru");
  const russian = messagesRu["settings.schema.agent.max_turns.label"]!;
  const english = messagesEn["settings.schema.agent.max_turns.label"]!;
  expect(russian).toBeTruthy();
  expect(russian).not.toBe(english);
  const index = indexOf(AGENT_SCHEMA);
  const sections = deriveSettingsSections(AGENT_SCHEMA);

  const turns = only(index, "agent:max_turns");
  expect(turns.names).toEqual([russian]);
  expect(turns.keyPath).toBe("agent.max_turns");
  expect(turns.where[0]).toBe(translate("settings.section.agent.label"));
  expect(sections.find((s) => s.id === "agent")!.label).toBe(turns.where[0]);
  expect(turns.where[1]).toBe(translate("settings.group.agent.turn"));
  expect(only(index, "logger:levels.component").keyPath).toBe(
    "logger.levels[].component",
  );
  // found by the Russian name and by the key, not by the English name
  expect(found(index, russian)).toContain(russian);
  expect(found(index, "agent.max_turns")).toContain(russian);
  // the English name is not on the page, so it finds nothing by itself; the
  // key keeps its English words, so "max turns" still reaches the same field
  const memoryRoot = only(index, "memory:dir");
  expect(memoryRoot.names).toEqual([
    messagesRu["settings.schema.memory.dir.label"],
  ]);
  expect(
    searchSettings(
      index,
      messagesEn["settings.schema.memory.dir.label"]!,
    ).hits.map((h) => h.entry.id),
  ).not.toContain(memoryRoot.id);
  expect(found(index, english)).toContain(russian);
  // the landmarks speak it too
  expect(only(index, "landmark:appearance:language").names).toEqual([
    messagesRu["appearance.languageLabel"],
  ]);
  setLocale("en");
  expect(only(indexOf(AGENT_SCHEMA), "agent:max_turns").names).toEqual([
    english,
  ]);
});

// ---- the Metrics tab, which a binary without the metrics tag does not have

const METRICS_FIELDS = ["enable", "host", "port", "token"] as const;

/** The ids of what a query finds, best first. */
const idsFound = (index: Entries, query: string) =>
  searchSettings(index, query).hits.map((h) => h.entry.id);

test("the Metrics tab and each of its four fields are found by name, by config key and by description, in English and in Russian", () => {
  const english = ["Enabled", "Listen host", "Listen port", "Token"];
  for (const locale of ["en", "ru"] as const) {
    setLocale(locale);
    const index = indexOf(AGENT_SCHEMA);
    const tab = inTab(index, "metrics");
    const tabLabel = translate("settings.section.metrics.label");
    const names = METRICS_FIELDS.map((key) =>
      translate(`settings.schema.metrics.${key}.label`),
    );
    if (locale === "en") {
      expect([tabLabel, ...names]).toEqual(["Metrics", ...english]);
    } else {
      expect(tabLabel).toBe("Метрики");
      names.forEach((name, i) => expect(name, locale).not.toBe(english[i]));
    }

    // the tab, then its fields in the order the schema declares, under the tab
    expect(
      tab.map((e) => e.kind),
      locale,
    ).toEqual(["section", "field", "field", "field", "field"]);
    expect(
      tab.map((e) => e.names[0]),
      locale,
    ).toEqual([tabLabel, ...names]);
    expect(tab.slice(1).map((e) => e.keyPath)).toEqual(
      METRICS_FIELDS.map((key) => `metrics.${key}`),
    );
    expect(tab.slice(1).map((e) => e.anchor)).toEqual(
      METRICS_FIELDS.map((key) => fieldAnchor("metrics", key)),
    );
    expect(tab.slice(1).every((e) => e.where.join() === tabLabel)).toBe(true);
    // the section is where the rail puts it: after the logger, before the tail
    const order = deriveSettingsSections(AGENT_SCHEMA).map((s) => s.id);
    expect(order.indexOf("metrics")).toBe(order.indexOf("logger") + 1);

    const [tabEntry, ...fields] = tab;
    // by the tab's name: the tab leads, its fields follow
    expect(idsFound(index, tabLabel)[0], locale).toBe(tabEntry!.id);
    expect(idsFound(index, "metrics").slice(0, 5), locale).toEqual(
      locale === "en"
        ? tab.map((e) => e.id)
        : // the tab's Russian name is not "metrics", only the keys are
          fields.map((e) => e.id),
    );
    fields.forEach((field, i) => {
      // by the key as YAML spells it: this field and no other
      expect(idsFound(index, `metrics.${METRICS_FIELDS[i]}`), locale).toEqual([
        field.id,
      ]);
      // by its name, and by every word of its description
      expect(idsFound(index, names[i]!), locale).toContain(field.id);
      const description = translate(
        `settings.schema.metrics.${METRICS_FIELDS[i]}.desc`,
      );
      expect(
        idsFound(index, description),
        `${locale}: ${description}`,
      ).toContain(field.id);
    });
    // the tab by its own description
    expect(
      idsFound(index, translate("settings.section.metrics.desc")),
      locale,
    ).toContain(tabEntry!.id);
  }
  setLocale("en");
});

test("a tab the server leaves out for want of a build tag is neither in the index nor found, while the document still holds it", () => {
  const whole = indexOf(AGENT_SCHEMA);
  const hiddenSchema = schemaWithout(AGENT_SCHEMA, "metrics");
  const doc = configFromSchema(AGENT_SCHEMA);
  // GET /coddy/config answers the whole document, the hidden section included
  expect(doc["metrics"]).toBeTruthy();
  const index = indexOf(hiddenSchema, doc);

  expect(deriveSettingsSections(hiddenSchema).map((s) => s.id)).not.toContain(
    "metrics",
  );
  expect(inTab(index, "metrics")).toEqual([]);
  expect(
    index.filter(
      (e) =>
        e.keyPath?.startsWith("metrics") === true ||
        e.anchor?.startsWith("metrics:") === true,
    ),
  ).toEqual([]);
  // nothing else moved: the index is the whole one less the tab
  expect(index.map((e) => e.id)).toEqual(
    whole.filter((e) => e.section !== "metrics").map((e) => e.id),
  );
  for (const locale of ["en", "ru"] as const) {
    setLocale(locale);
    const metrics = inTab(indexOf(AGENT_SCHEMA), "metrics");
    const hidden = indexOf(hiddenSchema, doc);
    for (const query of [
      "metrics",
      "metrics.port",
      "listen port",
      translate("settings.section.metrics.label"),
      translate("settings.schema.metrics.port.label"),
      translate("settings.schema.metrics.token.desc"),
      SECRET_VALUES.metricsToken,
    ]) {
      const ids = idsFound(hidden, query);
      for (const e of metrics) {
        expect(ids, `${locale}: ${query}`).not.toContain(e.id);
      }
      expect(
        hidden.filter((e) => ids.includes(e.id) && e.section === "metrics"),
      ).toEqual([]);
    }
  }
  setLocale("en");
});

test("every landmark heading and every drawn text is a key of the default dictionary", () => {
  const pairs = SETTINGS_LANDMARKS.map((l) => `${l.section}/${l.id}`);
  expect(new Set(pairs).size).toBe(pairs.length);
  const sectionIds = new Set(
    deriveSettingsSections(AGENT_SCHEMA).map((s) => s.id),
  );

  for (const l of SETTINGS_LANDMARKS) {
    expect(sectionIds.has(l.section), `${l.section} is a tab`).toBe(true);
    expect(SETTINGS_TEXTS[l.heading], l.id).toBeTruthy();
    expect(landmarkAnchor(l.section, l.id)).toBe(
      `landmark:${l.section}:${l.id}`,
    );
  }
  for (const [id, text] of Object.entries(SETTINGS_TEXTS)) {
    const keys = Object.values(text) as string[];
    expect(keys.length, id).toBeGreaterThan(0);
    for (const key of keys) {
      expect(messagesEn[key], `${id}: ${key} (en)`).toBeTruthy();
      expect(messagesRu[key], `${id}: ${key} (ru)`).toBeTruthy();
    }
  }
  // The builder is only a string: a drawn anchor the index lacks is caught by
  // the comparison with the drawing, not by a throw in the component.
  expect(landmarkAnchor("nope", "theme")).toBe("landmark:nope:theme");
  // the headings are drawn from the constants: label and the heading's (i)
  const index = indexOf(AGENT_SCHEMA);
  expect(only(index, "landmark:mcp_servers:discovery")).toMatchObject({
    names: [translate("mcp.discovery.legend")],
    descriptions: [translate("mcp.discovery.description")],
  });
  expect(only(index, "landmark:appearance:theme").descriptions).toEqual([]);
  expect(only(index, "landmark:skills:installed").descriptions).toEqual([
    translate("skills.install.cliHint"),
  ]);
});

test("the Notifications landmark is left out where the tab draws none", () => {
  const notifications = "landmark:appearance:notifications";
  expect(entriesAt(indexOf(AGENT_SCHEMA), notifications)).toHaveLength(1);

  launchAsTelegram();
  const telegram = indexOf(AGENT_SCHEMA);
  expect(entriesAt(telegram, notifications)).toEqual([]);
  expect(
    telegram.filter((e) => e.section === "appearance").map((e) => e.anchor),
  ).toEqual([
    null,
    "landmark:appearance:theme",
    "landmark:appearance:language",
  ]);
});

// ---- cost

test("a large document builds its index and answers a query in a measured time", () => {
  const doc = configFromSchema(AGENT_SCHEMA);
  const providerRow = (doc["providers"] as Record<string, unknown>[])[0]!;
  const modelRow = (doc["models"] as Record<string, unknown>[])[0]!;
  doc["providers"] = Array.from({ length: 40 }, (_, i) => ({
    ...providerRow,
    name: `provider-${i}`,
    type: PROVIDER_ROWS[i % PROVIDER_ROWS.length],
  }));
  doc["models"] = Array.from({ length: 200 }, (_, i) => ({
    ...modelRow,
    model: `provider-${i % 40}/model-${i}`,
  }));

  const t0 = performance.now();
  const index = indexOf(AGENT_SCHEMA, doc);
  const built = performance.now() - t0;

  const queries = ["proxy", "provider-7 proxy", "max turns", "zzzz", "a"];
  const timings = queries.map((query) => {
    const start = performance.now();
    const result = searchSettings(index, query);
    return {
      query,
      ms: Number((performance.now() - start).toFixed(2)),
      shown: result.hits.length,
      more: result.more,
    };
  });
  // eslint-disable-next-line no-console
  console.log(
    `settings search: ${index.length} entries built in ${built.toFixed(1)} ms`,
    JSON.stringify(timings),
  );
  // sanity only; the suite asserts no timing
  expect(index.length).toBeGreaterThan(40 * 8 + 200 * 9);
  expect(timings[0]!.shown).toBeGreaterThan(0);
  expect(timings[3]).toMatchObject({ shown: 0, more: 0 });
});
