#!/usr/bin/env node
/**
 * Browser checks of the Settings drawer, chosen by argument:
 *
 *   unsaved (the default)  issue #485: while the form holds an edit that is
 *                          not saved, Save stands out and the line before the
 *                          buttons says so;
 *   search                 the Settings search: a query finds a setting and the
 *                          drawer takes the reader to it (ring, bounds, focus),
 *                          on an agent's page and on a relay's own page.
 *
 * jsdom has no layout, so vitest cannot tell whether the unsaved line fits the
 * footer beside the buttons on a phone, whether a tap still reaches Save, or
 * whether a setting the search took you to is in sight, ringed and holding the
 * cursor. These drive a real `coddy serve` with the web UI and write nothing
 * (the unsaved scenario puts its edit back); still, point them at a throwaway
 * server with its own CODDY_HOME rather than one you use.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_SERVE_URL=http://127.0.0.1:18485 npm run check:settings-unsaved
 *   CODDY_BIN=../../build/coddy npm run check:settings-search
 *
 * The search scenario drives two stands, each with its own HOME and
 * CODDY_HOME. With CODDY_BIN set and CODDY_SERVE_URL not, it starts them from
 * that binary (`TAGS="http ui swarm"`; settings-search-stands.mjs: an agent
 * with one `openai` provider `demo`, no key and three logical models, and a
 * relay) on free ports and stops them at exit, failures included. With
 * CODDY_SERVE_URL it uses stands of yours instead:
 *   an agent  CODDY_SERVE_URL: `coddy serve` with one `openai` provider named
 *             `demo` (no key; no model is called) and two or more entries
 *             under `models:`;
 *   a relay   CODDY_RELAY_URL: `coddy serve` with `swarm.enable: true`, which
 *             asks for its client token even on loopback (CODDY_RELAY_TOKEN).
 * and fails without CODDY_RELAY_URL.
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same checks in another engine;
 * CODDY_BROWSER_PATH points chromium at an installed browser (for example
 * /usr/bin/chromium) instead of Playwright's own download;
 * CODDY_AUTH_TOKEN is sent as the bearer token of an agent that asks for one.
 */

import fs from "node:fs";
import path from "node:path";

import {
  assertAddress,
  assertDrawerState,
  assertGroupOpen,
  assertHighlight,
  assertKbd,
  assertRevealed,
  assertRowForm,
  assertTabOnly,
  assertTiers,
  assertTouchOnly,
  backToTiles,
  blurActive,
  escapeTo,
  isNarrowShell,
  openSearch,
  press,
  pressField,
  pressKey,
  readGroup,
  revealResult,
  searchField,
  searchFor,
  shownResults,
} from "./settings-search-assert.mjs";
import { startStands } from "./settings-search-stands.mjs";

const SCENARIOS = ["unsaved", "search"];
const SCENARIO = process.argv[2] || "unsaved";
if (!SCENARIOS.includes(SCENARIO)) {
  console.error(
    `unknown scenario ${SCENARIO} (use ${SCENARIOS.join(" or ")}; none means unsaved)`,
  );
  process.exit(2);
}

let BASE = (process.env.CODDY_SERVE_URL || "http://127.0.0.1:18485").replace(
  /\/+$/,
  "",
);
let RELAY = (process.env.CODDY_RELAY_URL || "").replace(/\/+$/, "");
let RELAY_TOKEN = process.env.CODDY_RELAY_TOKEN || "";
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const TOKEN = process.env.CODDY_AUTH_TOKEN || "";
const KEEP = process.env.CODDY_E2E_KEEP === "1";

// The search scenario starts its own stands from CODDY_BIN unless it is told
// where the stands are.
let stands = null;
if (
  SCENARIO === "search" &&
  process.env.CODDY_BIN &&
  !process.env.CODDY_SERVE_URL
) {
  const bin = path.resolve(process.env.CODDY_BIN);
  if (!fs.existsSync(bin)) {
    console.error(
      `${bin} not found; build it with: make build TAGS="http ui swarm"`,
    );
    process.exit(2);
  }
  process.on("SIGINT", () => process.exit(130));
  process.on("SIGTERM", () => process.exit(143));
  try {
    stands = await startStands(bin);
  } catch (err) {
    console.error(String(err.message || err));
    process.exit(2);
  }
  BASE = stands.agentUrl;
  RELAY = stands.relayUrl;
  RELAY_TOKEN = stands.relayToken;
  console.log(`stands from ${bin}: agent ${BASE}, relay ${RELAY}`);
}

if (SCENARIO === "search" && !RELAY) {
  console.error(
    "the search scenario needs stands: CODDY_BIN (it starts them), or CODDY_SERVE_URL and CODDY_RELAY_URL (a coddy serve with swarm.enable, for the relay's own Settings; CODDY_RELAY_TOKEN is its client token)",
  );
  process.exit(2);
}

// Both shipped locales, since the line's words are of a different length in
// each; a wide window, and phones held both ways, the smallest ones included.
const CASES = [
  { lang: "en", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 390, height: 844, touch: true },
  { lang: "ru", width: 360, height: 640, touch: true },
  { lang: "en", width: 320, height: 568, touch: true },
  { lang: "ru", width: 844, height: 390, touch: true },
  { lang: "ru", width: 568, height: 320, touch: true },
];

// The search runs in the four windows its behaviour splits on: a wide one with
// a mouse, a phone with a finger, a narrow window with a keyboard, a phone
// held sideways.
const SEARCH_CASES = [
  { lang: "en", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 390, height: 844, touch: true },
  { lang: "en", width: 390, height: 844, touch: false },
  { lang: "ru", width: 844, height: 390, touch: true },
];

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed. Run: npm i --no-save playwright && npx playwright install chromium",
  );
  process.exit(2);
}
const launcher = playwright[ENGINE];
if (!launcher) {
  console.error(
    `unknown CODDY_ENGINE ${ENGINE} (use chromium, webkit or firefox)`,
  );
  process.exit(2);
}

const failures = [];
const fail = (where, what) => {
  failures.push(`${where}: ${what}`);
  console.log(`  FAIL ${what}`);
};
const headers = TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {};

/** The context of one case: the language, the window and the input device. */
function contextOptions(c, extraHTTPHeaders) {
  return {
    viewport: { width: c.width, height: c.height },
    locale: c.lang === "ru" ? "ru-RU" : "en-US",
    hasTouch: c.touch,
    isMobile: c.touch && ENGINE !== "firefox",
    extraHTTPHeaders,
  };
}

// ----------------------------------------------------------------- unsaved

async function runUnsaved(browser) {
  for (const c of CASES) {
    const where = `${c.lang} ${c.width}x${c.height}${c.touch ? " touch" : ""}`;
    console.log(where);
    const ctx = await browser.newContext(contextOptions(c, headers));
    const page = await ctx.newPage();
    await page.goto(`${BASE}/?lang=${c.lang}#/settings/agent`);
    // Max turns, the first number of the ReAct loop tab; its label is
    // translated, so it is found by its place.
    const input = page.locator('.settings-body input[type="number"]').first();
    await input.waitFor();
    const before = await input.inputValue();
    await input.fill(String(Number(before || "40") + 1));
    await page.evaluate(() => document.activeElement?.blur());
    const m = await page.evaluate(() => {
      const rect = (el) => el.getBoundingClientRect();
      const save = document.querySelector('[data-testid="settings-save"]');
      const status = document.querySelector(
        '[data-testid="settings-save-status"]',
      );
      const reload = document.querySelector('[data-testid="settings-reload"]');
      const b = rect(save);
      const at = document.elementFromPoint(
        b.left + b.width / 2,
        b.top + b.height / 2,
      );
      const s = rect(status);
      const r = rect(reload);
      return {
        scroll:
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
        dirty: save.classList.contains("is-dirty"),
        text: status.textContent,
        saveInside:
          b.left >= 0 &&
          b.top >= 0 &&
          b.right <= innerWidth &&
          b.bottom <= innerHeight,
        saveHit: save.contains(at),
        statusClear: s.right <= r.left + 0.5 && s.width > 0,
        statusOneRow:
          Math.abs(s.top + s.height / 2 - (b.top + b.height / 2)) < 6,
      };
    });
    if (m.scroll > 0) fail(where, `the page scrolls sideways by ${m.scroll}px`);
    if (!m.dirty) fail(where, "Save is not highlighted over an unsaved edit");
    if (!m.text) fail(where, "the line before the buttons says nothing");
    if (!m.saveInside) fail(where, "Save is outside the window");
    if (!m.saveHit)
      fail(where, "a tap at the middle of Save lands on something else");
    if (!m.statusClear) fail(where, "the status line runs into the buttons");
    if (!m.statusOneRow)
      fail(where, "the status line is not on the buttons' row");
    // Put the value back: the highlight goes, and nothing is saved.
    await input.fill(before);
    await page.evaluate(() => document.activeElement?.blur());
    if (
      await page
        .getByTestId("settings-save")
        .evaluate((e) => e.classList.contains("is-dirty"))
    ) {
      fail(where, "a value put back still highlights Save");
    }
    await ctx.close();
  }
}

// ------------------------------------------------------------------ search

/** Thrown after a step failed and was reported, to leave the rest of its case. */
class CaseAborted extends Error {}

/** One step of a case: what a helper throws is a failure of that step. */
async function runStep(where, label, fn) {
  try {
    const result = await fn();
    console.log(`  ok   ${label}`);
    return result;
  } catch (err) {
    fail(where, `${label}: ${err.message}`);
    throw new CaseAborted(label);
  }
}

/** A context for one case, closed after `fn(ctx)`. */
async function inContext(browser, c, extraHTTPHeaders, fn) {
  const ctx = await browser.newContext(contextOptions(c, extraHTTPHeaders));
  ctx.setDefaultTimeout(10000);
  try {
    await fn(ctx);
  } finally {
    await ctx.close();
  }
}

/** A page whose script errors are failures of the case. */
async function newPage(ctx, where) {
  const page = await ctx.newPage();
  page.on("pageerror", (err) => fail(where, `page error: ${err.message}`));
  return page;
}

/**
 * A page of its own at `url` (an address that names a setting is a document
 * to load, not a navigation inside one) with the drawer drawn, closed after
 * `fn(page)`.
 */
async function onFreshPage(ctx, where, url, fn) {
  const page = await newPage(ctx, where);
  try {
    await page.goto(url);
    await page.getByTestId("settings-screen").waitFor();
    return await fn(page);
  } finally {
    await page.close();
  }
}

/**
 * What is wrong with where the results are drawn, as a sentence, or "": the
 * scroll area (the part of the list the reader sees) sits inside the drawer,
 * no result sticks out of it and nothing scrolls sideways.
 */
function resultsMisfit() {
  const q = (sel) => document.querySelector(sel);
  const drawer = q('[data-testid="settings-screen"]');
  const id = q(".settings-search-input")?.getAttribute("aria-controls");
  const list = (id && document.getElementById(id)) || q('[role="listbox"]');
  if (!drawer || !list) {
    return `${drawer ? "the results are" : "the drawer is"} not drawn`;
  }
  const area = list.closest(".settings-scroll") || list;
  const d = drawer.getBoundingClientRect();
  const a = area.getBoundingClientRect();
  const out = [...list.querySelectorAll('button[role="option"]')].filter(
    (o) => {
      const r = o.getBoundingClientRect();
      return r.right > a.right + 1 || r.left < a.left - 1;
    },
  ).length;
  const wide = (el) => el.scrollWidth - el.clientWidth;
  const sideways = [document.documentElement, list, area, drawer].map(wide);
  const inside =
    a.width > 0 &&
    a.left >= d.left - 1 &&
    a.right <= d.right + 1 &&
    a.top >= d.top - 1 &&
    a.bottom <= d.bottom + 1;
  return [
    !inside && "the results area is not inside the drawer",
    out > 0 && `${out} results stick out of the results area`,
    sideways.some((n) => n > 0) && `something scrolls sideways (${sideways})`,
  ]
    .filter(Boolean)
    .join("; ");
}

const TURNS = { anchor: "agent:max_turns" };
const PROXY = { anchor: "providers:proxy", row: "demo" };
const TEMPERATURE = "models:temperature";
const ADVANCED = "group:providers:advanced";

/** An agent's page: the search, the reveal and the life of the query. */
async function searchOnAgent(browser, c, where) {
  const step = (label, fn) => runStep(where, label, fn);
  const at = (hash) => `${BASE}/?lang=${c.lang}#${hash}`;
  // A touch screen focuses the box, so no keyboard rises; a mouse focuses the
  // control named like the setting.
  const focus = c.touch ? "box" : "control";
  await inContext(browser, c, headers, async (ctx) => {
    const page = await newPage(ctx, where);
    const reveal = (spec) => revealResult(page, { focus, ...spec });
    const address = (want) => assertAddress(page, want);
    const state = (want) => assertDrawerState(page, { open: true, ...want });
    const escape = (want) => escapeTo(page, { open: true, ...want });
    let narrow = false;
    await page.goto(at("/settings"));
    await step("the drawer, the input and the badge", async () => {
      await page
        .locator(
          '[data-testid="settings-tab-appearance"], [data-testid="settings-tile-appearance"]',
        )
        .first()
        .waitFor();
      await assertTouchOnly(page, c.touch);
      narrow = await isNarrowShell(page);
      if (narrow !== c.width < 1200) {
        throw new Error(`The shell is ${narrow ? "" : "not "}the narrow one.`);
      }
      // A touch-only device has no "/" key to show.
      await assertKbd(page, !c.touch);
    });

    // A query that is a config key: the badge gives way to the cursor and the
    // query, and the results sit inside the drawer.
    await step("max_turns is found, the badge is gone", async () => {
      await openSearch(page, { narrow });
      await assertKbd(page, false);
      await searchFor(page, "max_turns");
      await assertKbd(page, false);
    });
    // Measured, not asserted: a misfit is reported and the case goes on.
    const misfit = await page.evaluate(resultsMisfit);
    if (misfit) fail(where, `the results do not fit the drawer: ${misfit}`);

    // A setting of a tab: the tab opens and the setting is revealed.
    const turns = await step(`max_turns is revealed (${focus})`, async () => {
      const name = await reveal(TURNS);
      await address({ tab: "agent", field: TURNS.anchor, id: null });
      return name;
    });

    // A row of a list: the provider's proxy in its folded block. Results that
    // match by name come before those that match by description.
    const proxy = await step(`proxy is revealed (${focus})`, async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "proxy");
      await assertTiers(page, "proxy");
      // A mouse puts the cursor in the URL input, not on the switch before it.
      const name = await reveal({ ...PROXY, textInput: !c.touch });
      await assertGroupOpen(page, ADVANCED, PROXY.row);
      await address({ field: PROXY.anchor, id: PROXY.row });
      return name;
    });

    // The query is kept, the results hidden (the narrow field is drawn over
    // the tiles, so back to them first), and "/" or a tap brings them back
    // with the chosen one selected.
    await step("the query is kept and the choice comes back", async () => {
      if (narrow) await backToTiles(page);
      await state({ query: "proxy", resultsShowing: false });
      await openSearch(page, { narrow });
      await assertHighlight(page, PROXY);
    });

    // A setting of a model's row opens that row and not another.
    const rows = await step("temperature is found for each model", async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "temperature");
      const found = (await shownResults(page))
        .filter((r) => r.anchor === TEMPERATURE)
        .map((r) => r.row);
      if (found.length < 2) {
        throw new Error(
          `The Logical models tab should offer Temperature once for each of the stand's models, but ${found.length} results carry ${TEMPERATURE}.`,
        );
      }
      return found.slice(0, 2);
    });
    for (const row of rows) {
      await step(`temperature of ${row} is revealed (${focus})`, async () => {
        const chosen = { anchor: TEMPERATURE, row };
        if (row !== rows[0]) {
          await openSearch(page, { narrow });
          await assertHighlight(page, { anchor: TEMPERATURE, row: rows[0] });
        }
        await reveal(chosen);
        await assertRowForm(page, row);
        await address({ field: TEMPERATURE, id: row });
      });
    }
    // Leaving the setting takes it out of the address: the head's arrow and a
    // tap on a tile on the narrow shell, a click on the rail on the wide one.
    await step("leaving the setting clears the address", async () => {
      if (narrow) {
        await backToTiles(page);
        await address({ field: null });
      }
      const section = narrow ? "settings-tile-agent" : "settings-tab-agent";
      await press(page, page.getByTestId(section));
      await address({ field: null, id: null });
    });

    // The highlight, Enter, and an emptied field. A query pasted over the old
    // one does not hold the result chosen before, so the first is highlighted;
    // a click in the field leaves the highlight alone; Enter opens it;
    // emptying the field clears the search and the choice with it.
    await step("a pasted query highlights the first result", async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "proxy");
      await assertHighlight(page, 0);
    });
    const entered = await step("ArrowDown and a click keep it", async () => {
      await searchFor(page, "proxy", { typed: true });
      await pressKey(page, "ArrowDown");
      await assertHighlight(page, 1);
      await pressField(page);
      await assertHighlight(page, 1);
      return (await shownResults(page))[1];
    });
    await pressKey(page, "Enter");
    await step("Enter reveals the highlighted result", () =>
      assertRevealed(page, {
        ...entered,
        focus: c.touch ? "box" : "control-or-box",
      }),
    );
    await step("an emptied field forgets the choice", async () => {
      await openSearch(page, { narrow });
      await assertHighlight(page, entered);
      await searchField(page).fill("");
      await state({ query: "", resultsShowing: false });
      await blurActive(page);
      await assertKbd(page, !c.touch);
      await searchFor(page, "proxy");
      await assertHighlight(page, 0);
    });

    // The wide drawer hides the tab under the results and keeps its scroll
    // position (the narrow shell mounts none).
    if (!narrow) {
      await step("the tab behind the results keeps its scroll", async () => {
        const tab = '.settings-scroll[data-settings-tab="tools"]';
        const top = () =>
          page.evaluate(
            (sel) => document.querySelector(sel)?.scrollTop ?? -1,
            tab,
          );
        await page.getByTestId("settings-tab-tools").click();
        await page.waitForSelector(tab, { state: "visible" });
        const set = await page.evaluate((sel) => {
          const el = document.querySelector(sel);
          const travel = el.scrollHeight - el.clientHeight;
          el.scrollTop = Math.min(travel, 300);
          return { top: el.scrollTop, travel };
        }, tab);
        if (set.top < 100) {
          throw new Error(
            `The Tools and permissions tab should be long enough to scroll, but it has ${set.travel}px of travel, so this check would prove nothing.`,
          );
        }
        await openSearch(page, { narrow });
        await searchFor(page, "proxy");
        await escape({ query: "", resultsShowing: false });
        for (let i = 0; Math.abs((await top()) - set.top) > 1; i++) {
          if (i === 20) {
            throw new Error(
              `The tab should keep its scroll position, but it was scrolled to ${set.top}px and is at ${await top()}px after the search was cleared.`,
            );
          }
          await page.waitForTimeout(100);
        }
      });
    }

    // Escape takes one step back at a time: the search first, wherever the
    // cursor is, then the drawer's own step (a row back to its list, a tab
    // back to the tiles), and last the drawer itself.
    const openRowDemo = async () => {
      await reveal(PROXY);
      await state({ rowOpen: PROXY.row, resultsShowing: false });
    };
    await step("Escape clears the search, the cursor outside", async () => {
      await openSearch(page, { narrow });
      await searchFor(page, "proxy");
      await openRowDemo();
      await openSearch(page, { narrow });
      await blurActive(page);
      await state({ resultsShowing: true, fieldFocused: false });
      await escape({
        query: "",
        resultsShowing: false,
        ...(narrow ? { tiles: true } : { rowOpen: PROXY.row }),
      });
    });
    if (narrow) {
      await step("Escape: the row to its list, then to the tiles", async () => {
        await searchFor(page, "proxy");
        await openRowDemo();
        await escape({ rowOpen: null, tiles: false });
        await escape({ tiles: true });
      });
    } else {
      await step("Escape: the row back to its list", () =>
        escape({ rowOpen: null }),
      );
      await step("Escape in the field clears a hidden query", async () => {
        await openSearch(page, { narrow });
        await searchFor(page, "proxy");
        // A click that leaves the cursor where it is: a section of the rail
        // hides the results and keeps the query.
        await page.evaluate(() =>
          document.querySelector('[data-testid="settings-tab-agent"]').click(),
        );
        await state({
          query: "proxy",
          resultsShowing: false,
          fieldFocused: true,
        });
        await escape({ query: "" });
      });
    }
    await step("Escape with nothing left closes Settings", () =>
      escape({ open: false }),
    );

    // An address that names a setting opens it on a page of its own and
    // reveals it as a choice does: the tab, the row and the folded block open,
    // the ring, the cursor. An anchor that is gone only opens its tab.
    for (const link of [
      {
        hash: "/settings/providers?id=demo&field=providers%3Aproxy",
        ...PROXY,
        name: proxy,
      },
      {
        hash: "/settings/agent?field=agent%3Amax_turns",
        ...TURNS,
        name: turns,
      },
    ]) {
      await step(`a link to ${link.anchor} reveals it`, () =>
        onFreshPage(ctx, where, at(link.hash), async (p) => {
          await assertRevealed(p, { ...link, focus });
          if (link.row) await assertGroupOpen(p, ADVANCED, link.row);
        }),
      );
    }
    await step("a link to a missing setting opens its tab", () =>
      onFreshPage(ctx, where, at("/settings/agent?field=agent%3Agone"), (p) =>
        assertTabOnly(p, "agent"),
      ),
    );

    // A group is a result of its own: the tab (and the row) opens, a folded
    // group that is itself the target unfolds, and its fieldset is ringed and
    // holds the cursor. The legends are read from a page of their own, so the
    // group stands as the page draws it.
    for (const group of [
      { hash: "/settings/agent", prefix: "group:agent:" },
      {
        hash: "/settings/providers?id=demo",
        prefix: "group:providers:",
        row: "demo",
        folded: true,
      },
    ]) {
      const kind = group.folded ? "folded group of demo" : "first group";
      const found = await step(`the legend of the ${kind} is read`, () =>
        onFreshPage(ctx, where, at(group.hash), (p) => readGroup(p, group)),
      );
      await step(`"${found.legend}" finds the ${kind}`, () =>
        onFreshPage(ctx, where, at("/settings"), async (p) => {
          await openSearch(p, { narrow });
          await searchFor(p, found.legend);
          const chosen = { anchor: found.anchor, row: group.row };
          await revealResult(p, { ...chosen, focus: "box" });
          if (group.folded) await assertGroupOpen(p, found.anchor, group.row);
        }),
      );
    }
  });
}

/** The relay's own page. */
async function searchOnRelay(browser, c, where) {
  const step = (label, fn) => runStep(where, label, fn);
  const pairing = { anchor: "swarm:pairing_tokens" };
  // A list of values draws no control named like the list: the box itself
  // holds the cursor then, and always on a touch screen.
  const focus = c.touch ? "box" : "control-or-box";
  await inContext(browser, c, undefined, async (ctx) => {
    // A relay asks for its client token; the page takes it from the environment
    // it was told to talk to, which is itself.
    await ctx.addInitScript(
      ({ base, token }) => {
        try {
          localStorage.setItem(
            "coddy_env",
            JSON.stringify({ mode: "remote", baseUrl: base, token }),
          );
        } catch {
          // storage blocked: the page then asks for the token and the step fails
        }
      },
      { base: RELAY, token: RELAY_TOKEN },
    );
    const page = await newPage(ctx, where);
    await page.goto(`${RELAY}/?lang=${c.lang}#/settings`);
    await step("the relay's drawer is drawn with its Swarm relay tab", () =>
      page
        .locator(
          '[data-testid="settings-tab-swarm"], [data-testid="settings-tile-swarm"]',
        )
        .first()
        .waitFor({ timeout: 15000 }),
    );
    await step(`the input is ${c.touch ? "touch only" : "a mouse"}`, () =>
      assertTouchOnly(page, c.touch),
    );
    const narrow = await isNarrowShell(page);
    const name = await step(
      `swarm:pairing_tokens is found by "pairing" and revealed, in the Swarm relay tab`,
      async () => {
        await openSearch(page, { narrow });
        await searchFor(page, "pairing");
        const name = await revealResult(page, { ...pairing, focus });
        await assertAddress(page, {
          tab: "swarm",
          field: "swarm:pairing_tokens",
        });
        return name;
      },
    );
    await step("a link to a setting of the relay's own page reveals it", () =>
      onFreshPage(
        ctx,
        where,
        `${RELAY}/?lang=${c.lang}#/settings/swarm?field=swarm%3Apairing_tokens`,
        (p) => assertRevealed(p, { ...pairing, focus, name }),
      ),
    );
  });
}

/** Stops with a plain message when a stand is not what the scenario needs. */
async function preflight() {
  const get = (url, init) => fetch(url, init).catch(() => null);
  const die = (message) => {
    console.error(message);
    process.exit(2);
  };
  const status = (res) => (res ? res.status : "no answer");
  const agent = await get(`${BASE}/coddy/config/schema`, { headers });
  if (!agent?.ok) {
    die(
      `the agent at ${BASE} does not serve /coddy/config/schema (status ${status(agent)}); start it, or set CODDY_SERVE_URL${TOKEN ? "" : " and CODDY_AUTH_TOKEN if it asks for one"}`,
    );
  }
  const info = await get(`${RELAY}/swarm/info`);
  const body = info?.ok ? await info.json().catch(() => null) : null;
  if (body?.swarm !== true) {
    die(
      `${RELAY}/swarm/info does not answer as a relay; CODDY_RELAY_URL is a coddy serve with swarm.enable`,
    );
  }
  const config = await get(`${RELAY}/coddy/config`, {
    headers: RELAY_TOKEN ? { Authorization: `Bearer ${RELAY_TOKEN}` } : {},
  });
  if (!config?.ok) {
    die(
      `the relay at ${RELAY} refused /coddy/config (status ${status(config)}); CODDY_RELAY_TOKEN is its client token`,
    );
  }
}

async function runSearch(browser) {
  for (const c of SEARCH_CASES) {
    const where = `${c.lang} ${c.width}x${c.height}${c.touch ? " touch" : " keyboard"}`;
    console.log(where);
    for (const [page, run] of [
      ["", searchOnAgent],
      [" relay", searchOnRelay],
    ]) {
      try {
        await run(browser, c, where + page);
      } catch (err) {
        if (!(err instanceof CaseAborted)) {
          fail(where + page, err.stack || String(err));
        }
      }
    }
  }
}

if (SCENARIO === "search") {
  await preflight();
}
const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
console.log(`engine ${ENGINE}, scenario ${SCENARIO}`);
if (SCENARIO === "search") {
  await runSearch(browser);
} else {
  await runUnsaved(browser);
}

await browser.close();
if (stands) stands.stop({ keepLogs: KEEP || failures.length > 0 });
if (failures.length > 0) {
  console.error(
    `\n${failures.length} failure(s):\n${failures.join("\n")}${stands ? `\nstand logs in ${stands.logs}` : ""}`,
  );
  process.exit(1);
}
console.log(
  SCENARIO === "search"
    ? "\nsettings search: all checks passed"
    : "\nsettings unsaved changes: all checks passed",
);
