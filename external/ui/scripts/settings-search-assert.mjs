/**
 * The assertions of the Settings search that `settings-unsaved-check.mjs search`
 * and `telegram-mini-app-check.mjs` (the same drawer in a Mini App's frame)
 * share. jsdom has no layout, paint or real focus, so whether a revealed
 * setting is ringed, in sight and holds the cursor is a fact of a real engine.
 * Every function takes a Playwright `Page` or `Frame` and throws an `Error`
 * that says what did not hold; the caller names the step.
 *
 * Nothing here reads a label from a dictionary: a result is picked by its
 * anchor (`data-settings-target`, `data-settings-row` for the row of a list),
 * and the name it must hand the focus to is the one the page drew on that
 * result, so the Russian and the English page are held to the same code.
 */

const SEARCH_INPUT = ".settings-search-input";
const TILE_GRID = '[data-testid="settings-tile-grid"]';
const HEAD_BACK = ".settings-head-back";
/** How long a reveal may take to hold: the wait for the box (1 s) and a smooth scroll. */
const REVEAL_POLL_MS = 2000;
const LIST_WAIT_MS = 5000;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
/** The page that owns a target: a frame knows it, a page is it. */
const pageOf = (target) =>
  typeof target.page === "function" ? target.page() : target;

export const searchField = (target) => target.locator(SEARCH_INPUT).first();

/**
 * Polls `probe`, which answers `{ ok, ...what it saw }`, until `ok`, and
 * returns that answer; at the deadline throws what `fail` makes of the last one.
 */
async function until(ms, probe, fail) {
  const deadline = Date.now() + ms;
  for (;;) {
    const seen = await probe();
    if (seen.ok) return seen;
    if (Date.now() > deadline) throw new Error(fail(seen));
    await sleep(60);
  }
}

/** A tap where the browser has touch, a click where it has none. */
export async function press(target, locator) {
  const touch = await target.evaluate(() => navigator.maxTouchPoints > 0);
  await (touch ? locator.tap() : locator.click());
}

export const pressField = (target) => press(target, searchField(target));
export const pressKey = (target, key) => pageOf(target).keyboard.press(key);
/** Moves the cursor off whatever holds it, as a click on blank ground does. */
export const blurActive = (target) =>
  target.evaluate(() => document.activeElement?.blur?.());

/** What holds the cursor, read in the page: `{ text, name }` (`text`: a text control). */
function cursorHolder() {
  const a = document.activeElement;
  if (!a) return { text: false, name: "nothing" };
  const classes =
    typeof a.className === "string" && a.className.trim()
      ? "." + a.className.trim().split(/\s+/).join(".")
      : "";
  return {
    text:
      a.isContentEditable ||
      ["INPUT", "TEXTAREA", "SELECT"].includes(a.tagName),
    name: a.tagName.toLowerCase() + classes,
  };
}

/**
 * The results' options, in the list the field controls (aria-controls). A field
 * that names no list is a broken combobox, not a reason to take any listbox.
 */
async function optionsOf(target) {
  const id = await searchField(target).getAttribute("aria-controls");
  if (!id) {
    throw new Error("The search field names no listbox in aria-controls.");
  }
  return target.locator(`[id=${JSON.stringify(id)}] button[role="option"]`);
}

/**
 * The touch-only media query is what the check believes it is, before any step
 * relies on it: a touch reveal focuses the box, so no keyboard rises, and the
 * app decides that by this very query (`snapshotTouchOnly()`).
 */
export async function assertTouchOnly(target, expected) {
  const got = await target.evaluate(
    () => matchMedia("(any-hover: none) and (any-pointer: coarse)").matches,
  );
  if (got !== expected) {
    throw new Error(
      `(any-hover: none) and (any-pointer: coarse) is ${got}, the check expects ${expected}`,
    );
  }
}

/** Whether the page runs the stacked shell (tiles and a head arrow), below 1200px. */
export const isNarrowShell = (target) =>
  target.evaluate(() => matchMedia("(max-width: 1199px)").matches);

/**
 * The results drawn now, in order: `{index, anchor, row, selected,
 * hasDescription, name}`, `name` being the primary name the result draws (its
 * first line, without a matched second name that may follow as " · <second>").
 */
export async function shownResults(target) {
  const options = await optionsOf(target);
  return options.evaluateAll((nodes) => {
    const norm = (text) => text.replace(/[\s\u00a0]+/g, " ").trim();
    return nodes
      .map((node, index) => {
        const title = node.querySelector(".settings-hit-title");
        const kids = [...node.children].filter((k) => k.textContent.trim());
        const first = title || (kids.length > 1 ? kids[0] : node);
        return {
          index,
          shown: node.getClientRects().length > 0,
          anchor: node.dataset.settingsTarget ?? "",
          row: node.dataset.settingsRow ?? null,
          selected: node.getAttribute("aria-selected") === "true",
          hasDescription: node.querySelector(".settings-hit-desc") !== null,
          name: norm(first.innerText.split("\n").find((l) => l.trim()) || "")
            .split(" · ")[0]
            .trim(),
        };
      })
      .filter((r) => r.shown)
      .map(({ shown, ...result }) => result);
  });
}

/**
 * The narrow shell goes back to its tiles through the head's arrow (twice from
 * a row's form: it leads to the list first); the field is drawn over them only.
 */
export async function backToTiles(target) {
  for (let hop = 0; hop < 4; hop++) {
    const back = target.locator(HEAD_BACK).first();
    if (!(await back.isVisible().catch(() => false))) break;
    const label = await back.getAttribute("aria-label");
    await press(target, back);
    await target
      .waitForFunction(
        ({ grid, headBack, previous }) => {
          const g = document.querySelector(grid);
          const b = document.querySelector(headBack);
          return (
            (g && g.getClientRects().length > 0) ||
            !b ||
            b.getAttribute("aria-label") !== previous
          );
        },
        { grid: TILE_GRID, headBack: HEAD_BACK, previous: label },
        { timeout: 5000 },
      )
      .catch(() => {
        throw new Error(
          "The head's arrow did not lead back: the tiles never came.",
        );
      });
  }
}

/**
 * Brings the cursor to the search field the way a person does. Narrow: back
 * to the tiles, then a tap on the field. Wide: the focused element is blurred
 * and `/` goes to a page with no text field holding the cursor.
 */
export async function openSearch(target, { narrow }) {
  if (narrow) {
    await backToTiles(target);
    await searchField(target)
      .waitFor({ state: "visible", timeout: 5000 })
      .catch(() => {
        throw new Error("The search field is not drawn on the narrow shell.");
      });
    await pressField(target);
  } else {
    await blurActive(target);
    const held = await target.evaluate(cursorHolder);
    if (held.text) {
      throw new Error(
        `${held.name} still holds the cursor: "/" would be typed`,
      );
    }
    await pressKey(target, "/");
  }
  await target
    .waitForFunction(
      (sel) => document.activeElement === document.querySelector(sel),
      SEARCH_INPUT,
      { timeout: 3000 },
    )
    .catch(async () => {
      const held = await target.evaluate(cursorHolder);
      throw new Error(
        `The search field did not take the cursor (${held.name} holds it).`,
      );
    });
}

/**
 * Puts the query into the field and waits for the results. `typed` types it
 * key by key into an emptied field (which clears the search, so nothing chosen
 * before is left), otherwise it is set in one event, as a paste does.
 */
export async function searchFor(target, query, { typed = false } = {}) {
  const input = searchField(target);
  if (typed) {
    await input.fill("");
    await input.pressSequentially(query, { delay: 15 });
  } else {
    await input.fill(query);
  }
  const options = await optionsOf(target);
  await until(
    LIST_WAIT_MS,
    async () => {
      const value = await input.inputValue();
      const count = await options.count();
      return { ok: value === query && count > 0, value, count };
    },
    ({ value, count }) =>
      `No results for "${query}"${typed ? " typed key by key" : ""} within ${LIST_WAIT_MS} ms (the field holds "${value}", ${count} results).`,
  );
  // A frame for the results to settle on the query just typed.
  await target.evaluate(
    () =>
      new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      ),
  );
}

/**
 * Picks the result with this anchor (and row, for a list's row) and returns
 * the primary name it draws. Choosing hides the results and keeps the query.
 */
async function openResult(target, { anchor, row }) {
  const named = `${anchor}${row == null ? "" : ` and row ${row}`}`;
  const { match } = await until(
    LIST_WAIT_MS,
    async () => {
      const shown = await shownResults(target).catch(() => []);
      const found = shown.filter(
        (r) => r.anchor === anchor && (row == null || r.row === row),
      );
      if (found.length > 1) {
        throw new Error(
          `${found.length} results carry ${named}: name the row.`,
        );
      }
      return { ok: found.length === 1, match: found[0], shown };
    },
    ({ shown }) =>
      `No result with anchor ${named} (shown: ${shown.map((r) => r.anchor + (r.row ? `@${r.row}` : "")).join(", ") || "none"}).`,
  );
  const option = (await optionsOf(target)).nth(match.index);
  await option.scrollIntoViewIfNeeded();
  await press(target, option);
  return match.name;
}

/**
 * Everything one poll of a reveal reads, in the page, again on every poll: a
 * smooth scroll may still be running when the ring appears.
 */
function probeReveal({ anchor, row, focus, names, textInput }) {
  const norm = (s) => (s || "").replace(/[\s\u00a0]+/g, " ").trim();
  const describe = (el) =>
    el
      ? el.tagName.toLowerCase() +
        (el.id ? "#" + el.id : "") +
        (typeof el.className === "string" && el.className.trim()
          ? "." + el.className.trim().split(/\s+/).join(".")
          : "")
      : "nothing";

  // The box: the first one in document order whose dataset equals the
  // strings, inside the row's form when a row is named.
  const roots =
    row == null
      ? [document]
      : [
          ...document.querySelectorAll(".settings-detail[data-settings-row]"),
        ].filter((e) => e.dataset.settingsRow === row);
  let box = null;
  for (const root of roots) {
    box =
      root instanceof HTMLElement && root.dataset.settingsAnchor === anchor
        ? root
        : ([...root.querySelectorAll("[data-settings-anchor]")].find(
            (el) => el.dataset.settingsAnchor === anchor,
          ) ?? null);
    if (box) break;
  }
  const label = `${anchor}${row == null ? "" : `@${row}`}`;
  if (!box) {
    const missing = {
      ok: false,
      why: `no box with data-settings-anchor=${anchor}${row == null ? "" : ` in the form of row ${row}`}`,
    };
    return { ring: missing, bounds: missing, focus: missing, label };
  }

  // The ring is an outline over an inset wash (or, in a theme that paints it
  // so, a shadow): either one painted on the marked box counts.
  const cs = getComputedStyle(box);
  const marked = box.classList.contains("is-search-target");
  const outlined =
    cs.outlineStyle !== "none" && Number.parseFloat(cs.outlineWidth) > 0;
  const shadowed = cs.boxShadow !== "none";
  const ring = {
    ok: marked && (outlined || shadowed),
    why: `.is-search-target ${marked}, outline ${cs.outlineStyle} ${cs.outlineWidth}, box-shadow ${shadowed ? "painted" : "none"}`,
  };

  // In sight: inside the window and the part of the scroll area on screen, the
  // outline (3px off the box, 2px wide: 5px) included. A box taller than the
  // room is aligned by its top, 12px under the edge, instead of centred.
  const rect = box.getBoundingClientRect();
  const scroller = box.closest(".settings-scroll");
  const s = scroller?.getBoundingClientRect();
  const visible = {
    left: Math.max(0, s?.left ?? 0),
    top: Math.max(0, s?.top ?? 0),
    right: Math.min(innerWidth, s?.right ?? innerWidth),
    bottom: Math.min(innerHeight, s?.bottom ?? innerHeight),
  };
  const [slack, reach, margin] = [1, 5, 12];
  const tall = rect.height + 2 * reach > visible.bottom - visible.top;
  const r = (n) => Math.round(n * 10) / 10;
  const bounds = {
    ok:
      scroller !== null &&
      rect.width > 0 &&
      rect.height > 0 &&
      rect.left - reach >= visible.left - slack &&
      rect.right + reach <= visible.right + slack &&
      (tall
        ? rect.top >= visible.top - slack &&
          rect.top <= visible.top + margin + reach + slack
        : rect.top - reach >= visible.top - slack &&
          rect.bottom + reach <= visible.bottom + slack),
    why: `box top ${r(rect.top)} bottom ${r(rect.bottom)} left ${r(rect.left)} right ${r(rect.right)}, visible part top ${r(visible.top)} bottom ${r(visible.bottom)} left ${r(visible.left)} right ${r(visible.right)}, ${tall ? "taller than the room: aligned by its top" : "the 5px of outline included"}${scroller ? "" : " (no .settings-scroll around the box)"}`,
  };

  // The accessible name of a control, from aria-labelledby, aria-label or its
  // <label>; the hint beside a label is no part of it.
  const textOf = (node) => {
    const copy = node.cloneNode(true);
    copy
      .querySelectorAll(".field-hint, [aria-hidden='true'], script, style")
      .forEach((e) => e.remove());
    return norm(copy.textContent);
  };
  const nameOf = (el) =>
    norm(
      (el.getAttribute("aria-labelledby") || "")
        .split(/\s+/)
        .map((id) =>
          textOf(document.getElementById(id) ?? document.createElement("i")),
        )
        .join(" "),
    ) ||
    norm(el.getAttribute("aria-label")) ||
    norm([...(el.labels ?? [])].map(textOf).join(" "));
  const controls = [
    ...box.querySelectorAll(
      "input, select, textarea, button, [role=switch], [role=combobox], [role=checkbox], [tabindex]",
    ),
  ].filter(
    (el) =>
      !el.matches(
        ".field-hint, [disabled], [aria-disabled='true'], input[type=hidden]",
      ),
  );
  const control = controls.find((el) => names.includes(nameOf(el))) ?? null;

  // "control-or-box": the control when the box has one of that name, else the
  // box, as a list of values whose items carry no name of their own.
  const expected =
    focus === "box" ? box : focus === "control" ? control : (control ?? box);
  const active = document.activeElement;
  // A text field, not a switch or a checkbox drawn before it.
  const isText =
    !!active &&
    active.matches(
      "input:not([type=checkbox]):not([type=radio]):not([role=switch]), textarea",
    );
  const note =
    focus === "control" && !control
      ? `no enabled control named ${JSON.stringify(names[0])} in the box (named: ${JSON.stringify(controls.map(nameOf).filter(Boolean))}); `
      : "";
  return {
    ring,
    bounds,
    focus: {
      ok: expected !== null && active === expected && (!textInput || isText),
      why: `${note}the cursor is in ${describe(active)}, expected ${describe(expected)}${textInput ? " (a text field)" : ""}`,
    },
    label,
  };
}

/**
 * Holds a reveal to account: within `REVEAL_POLL_MS` the box of the setting
 * must carry its ring, lie inside the part of its scroll area that is on
 * screen, and have the focus, all three at once. `name` is what `openResult`
 * returned (a result may append " · <second name>" to its primary one, so the
 * part before that is a name too); `focus` is
 *   "control": the control in the box with that accessible name (a Proxy URL
 *     result focuses the URL input, never the Ignore-system-proxy switch drawn
 *     before it), a text field with `textInput`;
 *   "box": the box itself, as on a touch screen, where no keyboard may rise,
 *     so no text control holds the cursor either;
 *   "control-or-box": the control when the box has one of that name, else the
 *     box, as a list of values whose items carry no name of their own.
 */
export async function assertRevealed(
  target,
  { anchor, row, focus, name, textInput = false },
) {
  if (!["control", "box", "control-or-box"].includes(focus)) {
    throw new Error(
      `assertRevealed: focus is control, box or control-or-box, not ${focus}`,
    );
  }
  const names = [name, String(name).split(" · ")[0]].filter(
    (n, i, all) => n && all.indexOf(n) === i,
  );
  // A part that held at one poll and not at the last was lost on the way (the
  // ring fades after 1.6 s): not the same failure as one that never held.
  const held = { ring: false, bounds: false, focus: false };
  await until(
    REVEAL_POLL_MS,
    async () => {
      const last = await target.evaluate(probeReveal, {
        anchor,
        row,
        focus,
        names,
        textInput,
      });
      for (const k of Object.keys(held)) held[k] ||= last[k].ok;
      return { ok: Object.keys(held).every((k) => last[k].ok), last };
    },
    ({ last }) =>
      `${last.label} was not revealed within ${REVEAL_POLL_MS} ms:\n    ${Object.keys(
        held,
      )
        .filter((k) => !last[k].ok)
        .map(
          (k) =>
            `${k} (${held[k] ? "held earlier, then lost" : "never held"}): ${last[k].why}`,
        )
        .join("\n    ")}`,
  );
  const holder = focus === "box" && (await target.evaluate(cursorHolder));
  if (holder?.text) {
    throw new Error(
      `On a touch screen no text control should take the cursor after a reveal, since the keyboard would rise, but ${holder.name} holds it.`,
    );
  }
}

/** Picks the result with this anchor (and row), holds its reveal to account, returns the name it drew. */
export async function revealResult(target, { anchor, row, focus, textInput }) {
  const name = await openResult(target, { anchor, row });
  await assertRevealed(target, { anchor, row, focus, name, textInput });
  return name;
}

// ------------------------------------------------- the life of the query

/**
 * What the drawer looks like now: `open`, `query` (null while the field is not
 * drawn, as over an open tab on the narrow shell), `fieldFocused`,
 * `resultsShowing`, `rowOpen` (the row of the row form on screen, or null) and
 * `tiles` (the narrow shell's).
 */
async function drawerState(target) {
  return target.evaluate(() => {
    const q = (sel) => document.querySelector(sel);
    const drawn = (el) => !!el && el.getClientRects().length > 0;
    const input = q(".settings-search-input");
    const id = input && input.getAttribute("aria-controls");
    const list = id ? document.getElementById(id) : null;
    const options = list
      ? [...list.querySelectorAll('button[role="option"]')].filter(drawn)
      : [];
    const forms = [
      ...document.querySelectorAll(".settings-detail[data-settings-row]"),
    ].filter(drawn);
    return {
      open: q('[data-testid="settings-screen"]') !== null,
      query: drawn(input) ? input.value : null,
      fieldFocused: !!input && document.activeElement === input,
      resultsShowing: options.length > 0,
      rowOpen: forms.length > 0 ? forms[0].dataset.settingsRow : null,
      tiles: drawn(q('[data-testid="settings-tile-grid"]')),
    };
  });
}

/** Every key of `expected` equals the one `drawerState` reads, within two seconds. */
export async function assertDrawerState(target, expected) {
  await until(
    2000,
    async () => {
      const state = await drawerState(target);
      const wrong = Object.keys(expected).filter(
        (k) => state[k] !== expected[k],
      );
      return { ok: wrong.length === 0, state, wrong };
    },
    ({ state, wrong }) =>
      `The drawer is ${JSON.stringify(state)}, but ${wrong.map((k) => `${k} should be ${JSON.stringify(expected[k])}`).join(" and ")}.`,
  );
}

/** One Escape, and the drawer it should leave behind. */
export async function escapeTo(target, expected) {
  await pressKey(target, "Escape");
  await assertDrawerState(target, expected);
}

/**
 * The highlighted result (aria-selected), and no other, is the one at `which`:
 * an index, or `{anchor, row}` of the result chosen before.
 */
export async function assertHighlight(target, which) {
  const named =
    typeof which === "number"
      ? `Result ${which + 1}`
      : `The result chosen before (${which.anchor}${which.row == null ? "" : ` of ${which.row}`})`;
  await until(
    3000,
    async () => {
      const shown = await shownResults(target);
      const selected = shown.filter((r) => r.selected);
      const wanted =
        typeof which === "number"
          ? shown[which]
          : shown.find(
              (r) =>
                r.anchor === which.anchor &&
                (which.row == null || r.row === which.row),
            );
      return {
        ok: !!wanted && selected.length === 1 && selected[0] === wanted,
        wanted,
        selected,
        count: shown.length,
      };
    },
    ({ wanted, selected, count }) =>
      `${named} should be the highlighted one, but ${wanted ? (selected.length ? `result ${selected[0].index + 1} is` : "none is") : `it is not among the ${count} results shown`}.`,
  );
}

/**
 * Results order by where the words are found: every result whose own name
 * holds them comes before any that shows a description line. Both kinds must
 * be present, or the check proves nothing.
 */
export async function assertTiers(target, query) {
  const shown = await shownResults(target);
  const named = shown.filter((r) => !r.hasDescription).length;
  if (named === 0 || named === shown.length) {
    throw new Error(
      `A search for "${query}" should show results that match by name and by description, but it shows ${named} of the first and ${shown.length - named} of the second.`,
    );
  }
  const first = shown.findIndex((r) => r.hasDescription);
  const late = shown.findIndex((r, i) => i > first && !r.hasDescription);
  if (late !== -1) {
    throw new Error(
      `Results that match by name should come before those that match by description, but "${shown[late].name}" (result ${late + 1}) matches by name and follows "${shown[first].name}" (result ${first + 1}), which shows a description.`,
    );
  }
}

/** The row form on screen is the row's own, and every ring painted lies inside it. */
export async function assertRowForm(target, row) {
  await until(
    REVEAL_POLL_MS,
    async () => {
      const seen = await target.evaluate((wanted) => {
        const forms = [
          ...document.querySelectorAll(".settings-detail[data-settings-row]"),
        ].filter((el) => el.getClientRects().length > 0);
        const rings = [...document.querySelectorAll(".is-search-target")];
        const strays = rings.filter((el) => {
          const form = el.closest(".settings-detail[data-settings-row]");
          return !form || form.dataset.settingsRow !== wanted;
        });
        return {
          rows: forms.map((f) => f.dataset.settingsRow),
          rings: rings.length,
          strays: strays.length,
        };
      }, row);
      return {
        ok: seen.rows.length === 1 && seen.rows[0] === row && !seen.strays,
        seen,
      };
    },
    ({ seen }) =>
      `The result of the row ${row} should open that row's form and ring a setting inside it, but the forms on screen are ${JSON.stringify(seen.rows)} and ${seen.strays} of ${seen.rings} rings lie outside it.`,
  );
}

// ------------------------------------- the address, groups and the badge

/**
 * The address `#/settings/<tab>?id=<row>&field=<anchor>` holds what a choice
 * should leave in it: each of `tab`, `field` and `id` given equals the
 * address's own, null meaning the address carries none.
 */
export async function assertAddress(target, want) {
  const keys = Object.keys(want).filter((k) => want[k] !== undefined);
  await until(
    2000,
    async () => {
      const seen = await target.evaluate(() => {
        const [path, query = ""] = location.hash.replace(/^#/, "").split("?");
        const params = new URLSearchParams(query);
        const parts = path.split("/").filter(Boolean);
        return {
          hash: location.hash,
          tab: parts[0] === "settings" ? (parts[1] ?? null) : null,
          id: params.get("id"),
          field: params.get("field"),
        };
      });
      return { ok: keys.every((k) => seen[k] === want[k]), seen };
    },
    ({ seen }) =>
      `The address is ${seen.hash}, but ${keys
        .filter((k) => seen[k] !== want[k])
        .map(
          (k) =>
            `${k} should be ${want[k] === null ? "absent" : JSON.stringify(want[k])}`,
        )
        .join(" and ")}.`,
  );
}

/**
 * The tab stands open and no setting is ringed, for as long as a reveal would
 * take to show a ring (an address naming a setting that is gone only opens its
 * tab). Fails on a ring at any moment and on an error line the drawer draws.
 */
export async function assertTabOnly(target, tab) {
  const deadline = Date.now() + 1800;
  while (Date.now() < deadline) {
    const seen = await target.evaluate(() => ({
      rings: document.querySelectorAll(".is-search-target").length,
      error: document.querySelector(".settings-error")?.textContent.trim(),
    }));
    if (seen.rings > 0) {
      throw new Error("A setting is ringed, though none was found.");
    }
    if (seen.error !== undefined) {
      throw new Error(`The drawer shows an error: ${seen.error}`);
    }
    await sleep(80);
  }
  const drawn = await target.evaluate(
    (id) =>
      [
        ...document.querySelectorAll(".settings-scroll[data-settings-tab]"),
      ].some(
        (el) => el.dataset.settingsTab === id && el.getClientRects().length > 0,
      ) ||
      // the narrow shell mounts the open tab alone
      document.querySelector(".settings-mobile-detail") !== null,
    tab,
  );
  if (!drawn) throw new Error(`The tab ${tab} should be drawn, but it is not.`);
  await assertAddress(target, { tab });
}

/**
 * A group (a fieldset with a legend) of a tab or of a row's form: the one with
 * this `anchor`, else the first whose anchor starts with `prefix` and whose
 * legend has text (`folded`: one that stands shut). `{anchor, legend, open}`.
 */
function probeGroup({ anchor, prefix, row, folded }) {
  const scopes =
    row == null
      ? [document]
      : [
          ...document.querySelectorAll(".settings-detail[data-settings-row]"),
        ].filter(
          (e) => e.dataset.settingsRow === row && e.getClientRects().length > 0,
        );
  for (const scope of scopes) {
    for (const box of scope.querySelectorAll("[data-settings-anchor]")) {
      const id = box.dataset.settingsAnchor;
      if (anchor !== undefined ? id !== anchor : !id.startsWith(prefix)) {
        continue;
      }
      const open = box.classList.contains("is-open");
      // A group that can fold carries --collapsible, and stands shut until it
      // is open.
      if (
        folded &&
        (!box.classList.contains("settings-fieldset--collapsible") || open)
      ) {
        continue;
      }
      const copy = box.querySelector("legend")?.cloneNode(true);
      copy
        ?.querySelectorAll(".field-hint, [aria-hidden='true'], svg")
        .forEach((e) => e.remove());
      const legend = (copy?.textContent ?? "")
        .replace(/[\s\u00a0]+/g, " ")
        .trim();
      if (anchor !== undefined || legend) return { anchor: id, legend, open };
    }
  }
  return null;
}

/** The first group of a tab or of a row's form: its anchor (`group:<domain>:<id>`) and the legend it draws. */
export async function readGroup(target, { prefix, row, folded }) {
  const { found } = await until(
    5000,
    async () => {
      const found = await target.evaluate(probeGroup, { prefix, row, folded });
      return { ok: found !== null, found };
    },
    () =>
      `No ${folded ? "folded " : ""}group with an anchor starting ${prefix}${row == null ? "" : ` in the form of ${row}`} is drawn, so there is no legend to search for.`,
  );
  return { anchor: found.anchor, legend: found.legend };
}

/** A group the search opened is drawn and unfolded, whether it stood shut or not. */
export async function assertGroupOpen(target, anchor, row) {
  await until(
    2000,
    async () => {
      const group = await target.evaluate(probeGroup, { anchor, row });
      return { ok: group?.open === true, group };
    },
    ({ group }) =>
      `The group ${anchor} should be drawn and unfolded, but ${group ? "it stands shut" : "it is not drawn"}.`,
  );
}

/**
 * The "/" badge: drawn while the field is empty and does not hold the cursor,
 * gone while it holds the cursor or a query, never drawn on a touch-only device.
 */
export async function assertKbd(target, shown) {
  await until(
    2000,
    async () => {
      const seen = await target.evaluate((sel) => {
        const input = document.querySelector(sel);
        const kbd = document.querySelector(".settings-search-kbd");
        return {
          drawn: !!kbd && kbd.getClientRects().length > 0,
          text: kbd ? kbd.textContent.trim() : null,
          query: input ? input.value : null,
          focused: !!input && document.activeElement === input,
        };
      }, SEARCH_INPUT);
      return {
        ok: seen.drawn === shown && (!shown || seen.text === "/"),
        seen,
      };
    },
    ({ seen }) =>
      `The badge is ${seen.drawn ? `drawn (${JSON.stringify(seen.text)})` : "not drawn"} with the field ${seen.query === "" ? "empty" : `holding ${JSON.stringify(seen.query)}`} and ${seen.focused ? "focused" : "not focused"}, but it should be ${shown ? 'drawn as "/"' : "gone"}.`,
  );
}
