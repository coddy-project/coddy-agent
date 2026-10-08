#!/usr/bin/env node
/**
 * Long prompts and reading check (issue #342): the composer's height, its
 * expanded state, the Quote button and the jumps of the transcript, measured
 * in a real engine against a running `coddy serve`.
 *
 * jsdom has no layout, so the vitest suite pins the wiring (ComposerHeight,
 * ChatScreen, UserMessage tests) and this harness checks what it adds up to on
 * screen, with a mouse on a desktop and with a finger on phones held upright
 * and sideways:
 *
 * - the field grows with a draft and stops at eight lines or 40% of the
 *   visible viewport, never under its floor;
 * - expanded, the docked block reaches 8px under the chat header, and the
 *   control stands on the wand's vertical line in the field's bottom right
 *   corner; folded, the field is back to the height of its text;
 * - a selection in the last answer offers Quote inside the band between the
 *   header and the docked block - above the selection with a mouse, below it
 *   with a finger when it fits - and a press appends the quote to the draft;
 * - another conversation opens on its newest message after the reader scrolled
 *   up in the first;
 * - with a finger, pulling the page down offers only the jump to the top, and
 *   the jump lands at the top;
 * - the page never scrolls sideways.
 *
 * It needs a server with two sessions of a few turns each (the first two of
 * `GET /coddy/sessions`, or CODDY_SESSIONS=sess_a,sess_b). Usage, from
 * external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_URL=http://127.0.0.1:18081 npm run check:long-prompts
 *
 * CODDY_ENGINE=webkit runs it in WebKit; CODDY_BROWSER_PATH points Chromium at
 * an installed browser instead of the one Playwright downloads.
 */

const URL_BASE = (process.env.CODDY_URL || "http://127.0.0.1:18081").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";

// [width, height, touch]: a desktop with a mouse, phones upright and sideways.
const VIEWPORTS = [
  [1280, 860, false],
  [390, 844, true],
  [320, 568, true],
  [844, 390, true],
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
  console.error(`unknown CODDY_ENGINE ${ENGINE} (use chromium or webkit)`);
  process.exit(2);
}

async function sessions() {
  const fromEnv = (process.env.CODDY_SESSIONS || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  if (fromEnv.length >= 2) return fromEnv;
  const res = await fetch(`${URL_BASE}/coddy/sessions?limit=20`);
  if (!res.ok) throw new Error(`GET /coddy/sessions: ${res.status}`);
  const body = await res.json();
  const rows = Array.isArray(body) ? body : body.sessions || body.items || [];
  const ids = rows
    .map((r) => r.id || r.sessionId || r.session_id)
    .filter((id) => typeof id === "string" && id.startsWith("sess_"));
  if (ids.length < 2) {
    throw new Error(
      "the server needs two sessions with a few turns each (or CODDY_SESSIONS)",
    );
  }
  return ids.slice(0, 2);
}

const failures = [];
function check(label, ok, detail) {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? ` ${detail}` : ""}`);
  if (!ok) failures.push(label);
}

const near = (a, b, tol = 1) => Math.abs(a - b) <= tol;

/** A stand that cannot show what a check measures fails, never passes idle. */
function require(ok, what) {
  if (!ok) {
    console.error(`the stand cannot run this check: ${what}`);
    process.exit(2);
  }
}

/** The text a range reads, with its whitespace as the draft holds it. */
const squash = (text) => text.replace(/\s+/g, " ").trim();

/** The geometry every check reads, in one evaluate. */
async function geometry(page) {
  return page.evaluate(() => {
    const box = (sel) => document.querySelector(sel)?.getBoundingClientRect();
    const ta = document.querySelector("#composer");
    const cs = getComputedStyle(ta);
    return {
      field: box("#composer"),
      card: box(".composer-card"),
      dock: box(".chat-bottom-inner"),
      head: box(".chat-title-column"),
      wand: box('[data-testid="composer-enhance-btn"]'),
      expand: box('[data-testid="composer-expand"]'),
      pill: box(".rail-pill"),
      bar: box(".composer-bar"),
      stacked: window.matchMedia("(max-width: 1199px)").matches,
      rhythm:
        parseFloat(
          getComputedStyle(document.documentElement).getPropertyValue(
            "--coddy-top-rhythm",
          ),
        ) || 0,
      lineHeight: parseFloat(cs.lineHeight),
      chrome:
        parseFloat(cs.paddingTop) +
        parseFloat(cs.paddingBottom) +
        parseFloat(cs.borderTopWidth) +
        parseFloat(cs.borderBottomWidth),
      viewport: window.visualViewport?.height ?? window.innerHeight,
      sideways:
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth + 1,
      draft: ta.value,
    };
  });
}

async function press(page, touch, selector) {
  if (touch) await page.tap(selector);
  else await page.click(selector);
}

/** A finger dragged over the transcript, from one height to another. */
async function swipe(cdp, x, fromY, toY) {
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [{ x, y: fromY }],
  });
  for (let i = 1; i <= 8; i++) {
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [{ x, y: fromY + ((toY - fromY) * i) / 8 }],
    });
  }
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
}

/**
 * Waits for a fling to come to rest: a tap on a page still moving only stops
 * it, on a phone as in an emulated one.
 */
async function settle(page) {
  let last = -1;
  for (let i = 0; i < 40; i++) {
    const now = (await scrollState(page)).top;
    if (Math.abs(now - last) < 0.5) return;
    last = now;
    await page.waitForTimeout(150);
  }
}

async function scrollState(page) {
  return page.evaluate(() => {
    const el = document.querySelector(".chat-scroll");
    const doc = el && el.scrollHeight > el.clientHeight + 1 ? null : true;
    if (doc) {
      const vv = window.visualViewport?.height ?? window.innerHeight;
      return {
        top: window.scrollY,
        end: document.documentElement.scrollHeight - vv,
      };
    }
    return { top: el.scrollTop, end: el.scrollHeight - el.clientHeight };
  });
}

const [first, second] = await sessions();
const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
try {
  for (const [width, height, touch] of VIEWPORTS) {
    const at = `${width}x${height}${touch ? " touch" : ""}`;
    const context = await browser.newContext({
      viewport: { width, height },
      hasTouch: touch,
      isMobile: touch,
      locale: "en-US",
    });
    const page = await context.newPage();
    await page.goto(`${URL_BASE}/?lang=en#/s/${first}`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector('[data-testid="user-message-body"]');
    await page.waitForTimeout(800);
    const scrolls = await scrollState(page);
    require(scrolls.end >
      height, `${first} is shorter than two screens at ${at}; give it more turns`);
    require(await page.evaluate(
      () =>
        document.querySelectorAll(".messages-inner .msg-assistant p").length >
        0,
    ), `${first} has no answer with a paragraph to select`);

    // The field follows its text up to its ceiling.
    await page.fill("#composer", "");
    const empty = await geometry(page);
    const floor = empty.field.height;
    // One step from the window's edge to the top bar and from the bar to the
    // header (the stacked shell; a desktop has its rail at the side).
    if (empty.stacked) {
      check(
        `${at}: the top bar and the header keep one step of the top rhythm`,
        near(empty.pill.top, empty.rhythm) &&
          near(empty.head.top - empty.pill.bottom, empty.rhythm),
        `edge->bar ${empty.pill.top.toFixed(1)}, bar->header ${(empty.head.top - empty.pill.bottom).toFixed(1)}, step ${empty.rhythm}`,
      );
    }
    // The expand control stands over the composer's right edge, apart from
    // the field, one jump slot above the block when no jump is offered.
    check(
      `${at}: the expand control stands over the composer's right edge`,
      near(empty.expand.right, empty.card.right) &&
        near(empty.dock.top - empty.expand.bottom, 10) &&
        empty.expand.bottom <= empty.card.top,
      `control ${empty.expand.top.toFixed(0)}-${empty.expand.bottom.toFixed(0)}, block top ${empty.dock.top.toFixed(0)}`,
    );
    await page.fill("#composer", "one\ntwo\nthree\nfour");
    const four = await geometry(page);
    check(
      `${at}: four lines grow the field`,
      four.field.height > floor + 1,
      `${floor.toFixed(1)} -> ${four.field.height.toFixed(1)}px`,
    );
    await page.fill("#composer", "a long prompt\n".repeat(60));
    const long = await geometry(page);
    // The block never rides over the header: the room it has at the floor,
    // less the strip the expand control stands in.
    const room = empty.dock.top - empty.head.bottom - 2 * empty.rhythm - 34;
    const ceiling = Math.max(
      floor,
      Math.min(
        8 * long.lineHeight + long.chrome,
        0.4 * long.viewport,
        floor + room,
      ),
    );
    check(
      `${at}: a long draft stops at its ceiling`,
      near(long.field.height, ceiling),
      `${long.field.height.toFixed(1)}px, ceiling ${ceiling.toFixed(1)}px`,
    );

    // Expanded, the control stands one step under the header and the card one
    // step under it; folded, the field is back. The caret stays in the field
    // through the press, so a phone's keyboard stays open.
    await page.focus("#composer");
    await press(page, touch, '[data-testid="composer-expand"]');
    await page.waitForTimeout(150);
    check(
      `${at}: the field keeps the focus through the press`,
      await page.evaluate(() => document.activeElement?.id === "composer"),
      await page.evaluate(() => document.activeElement?.tagName ?? "none"),
    );
    const open = await geometry(page);
    check(
      `${at}: expanded, header, control and card stand one step apart`,
      near(open.expand.top - open.head.bottom, open.rhythm) &&
        near(open.card.top - open.expand.bottom, open.rhythm),
      `header->control ${(open.expand.top - open.head.bottom).toFixed(1)}, control->card ${(open.card.top - open.expand.bottom).toFixed(1)}, step ${open.rhythm}`,
    );
    // The skills picker opens inside the expanded field, at its foot, and
    // dims nothing.
    await page.fill("#composer", "/");
    await page.waitForTimeout(800);
    const picker = await page.evaluate(() => {
      const m = document.querySelector(
        ".slash-menu--sheet, .slash-menu--portal",
      );
      const bd = document.querySelector(".slash-sheet-backdrop");
      if (!m) return null;
      const r = m.getBoundingClientRect();
      return {
        top: r.top,
        bottom: r.bottom,
        dim: bd ? getComputedStyle(bd).backgroundColor : "rgba(0, 0, 0, 0)",
      };
    });
    if (picker) {
      check(
        `${at}: expanded, the skills picker opens at the field's foot`,
        picker.top >= open.card.top - 1 && picker.bottom <= open.bar.top + 1,
        `picker ${picker.top.toFixed(0)}-${picker.bottom.toFixed(0)}, card top ${open.card.top.toFixed(0)}, bar ${open.bar.top.toFixed(0)}`,
      );
      check(
        `${at}: the skills picker dims nothing`,
        picker.dim === "rgba(0, 0, 0, 0)" || picker.dim === "transparent",
        picker.dim,
      );
    } else {
      console.log(`skip ${at}: the server lists no skills, no picker to place`);
    }
    await page.keyboard.press("Escape");
    await page.fill("#composer", "a long prompt\n".repeat(60));
    await press(page, touch, '[data-testid="composer-expand"]');
    await page.waitForTimeout(150);
    const folded = await geometry(page);
    check(
      `${at}: folded, the field is back to its text`,
      near(folded.field.height, long.field.height),
      `${folded.field.height.toFixed(1)}px`,
    );
    await page.fill("#composer", "");

    // A selection in the last answer offers Quote inside the band.
    const placed = await page.evaluate(() => {
      const paras = [
        ...document.querySelectorAll(".messages-inner .msg-assistant p"),
      ];
      const para = paras[paras.length - 1];
      para.scrollIntoView({ block: "center" });
      const range = document.createRange();
      range.selectNodeContents(para);
      const sel = getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      return para.textContent.trim();
    });
    await page.waitForSelector('[data-testid="transcript-quote"]', {
      timeout: 3000,
    });
    // The page may still be scrolling the selection into view (WebKit lets
    // it settle over a few frames); the button follows it, so read it at rest.
    let lastTop = null;
    for (let i = 0; i < 20; i++) {
      await page.waitForTimeout(100);
      const top = await page.evaluate(
        () =>
          document
            .querySelector('[data-testid="transcript-quote"]')
            ?.getBoundingClientRect().top ?? null,
      );
      if (top !== null && top === lastTop) break;
      lastTop = top;
    }
    const quote = await page.evaluate(() => {
      const b = document
        .querySelector('[data-testid="transcript-quote"]')
        .getBoundingClientRect();
      const s = getSelection().getRangeAt(0).getBoundingClientRect();
      return {
        b: { top: b.top, bottom: b.bottom, left: b.left, right: b.right },
        s: { top: s.top, bottom: s.bottom },
      };
    });
    const g = await geometry(page);
    check(
      `${at}: Quote keeps between the header and the docked block`,
      quote.b.top >= g.head.bottom - 1 &&
        quote.b.bottom <= g.dock.top + 1 &&
        quote.b.left >= 0 &&
        quote.b.right <= width,
      JSON.stringify(quote.b),
    );
    const below = quote.b.top >= quote.s.bottom - 1;
    // The page decides by the input device it sees, which an engine's
    // emulation may not report as the viewport flag says (WebKit's does not).
    const finger = await page.evaluate(
      () =>
        window.matchMedia("(any-hover: none) and (any-pointer: coarse)")
          .matches,
    );
    const roomBelow = quote.s.bottom + 10 + 32 <= g.dock.top;
    const roomAbove = quote.s.top - 10 - 32 >= g.head.bottom;
    check(
      `${at}: Quote stands ${finger ? "below" : "above"} the selection when it fits`,
      finger
        ? below || !roomBelow
        : quote.b.bottom <= quote.s.top + 1 || !roomAbove,
      `selection ${quote.s.top.toFixed(0)}-${quote.s.bottom.toFixed(0)}, button ${quote.b.top.toFixed(0)}-${quote.b.bottom.toFixed(0)}`,
    );
    await press(page, touch, '[data-testid="transcript-quote"]');
    await page.waitForTimeout(150);
    const quoted = (await geometry(page)).draft;
    check(
      `${at}: a press puts the quote in the draft`,
      quoted.startsWith("> ") &&
        quoted.endsWith("\n\n") &&
        squash(quoted).includes(
          squash(placed).split(" ").slice(0, 3).join(" "),
        ),
      JSON.stringify(quoted.slice(0, 60)),
    );
    await page.fill("#composer", "");

    // Another conversation opens on its newest message.
    await page.evaluate(() => {
      const el = document.querySelector(".chat-scroll");
      if (el && el.scrollHeight > el.clientHeight + 1) el.scrollTop = 0;
      else window.scrollTo(0, 0);
    });
    await page.waitForTimeout(300);
    await page.evaluate((id) => {
      location.hash = `#/s/${id}`;
    }, second);
    await page.waitForTimeout(1500);
    const opened = await scrollState(page);
    require(opened.end >
      height, `${second} is shorter than two screens at ${at}; give it more turns`);
    check(
      `${at}: another conversation opens on its newest message`,
      opened.end - opened.top < 80,
      `${opened.top.toFixed(0)} of ${opened.end.toFixed(0)}`,
    );

    // With a finger, pulling the page down offers only the jump to the top.
    if (touch && ENGINE !== "chromium") {
      console.log(
        `skip ${at}: the swipe and the jump to the top need Chromium's touch input`,
      );
    }
    if (touch && ENGINE === "chromium") {
      const cdp = await context.newCDPSession(page);
      await swipe(cdp, width / 2, height * 0.3, height * 0.6);
      await settle(page);
      const shown = await page.evaluate(() => ({
        up: document.querySelector('[data-testid="chat-scroll-top"]').dataset
          .visible,
        down: document.querySelector('[data-testid="chat-scroll-bottom"]')
          .dataset.visible,
      }));
      check(
        `${at}: pulling the page down offers only the jump to the top`,
        shown.up === "true" && shown.down === "false",
        JSON.stringify(shown),
      );
      await page.tap('[data-testid="chat-scroll-top"]');
      await page.waitForTimeout(1200);
      const landed = await scrollState(page);
      check(
        `${at}: the jump to the top lands at the top`,
        landed.top < 80,
        `${landed.top.toFixed(0)}`,
      );
    }

    // An on-screen keyboard that resizes the page (Android's
    // interactive-widget=resizes-content, emulated by a shorter window): the
    // expanded composer keeps its steps and ends above the keyboard.
    if (touch) {
      await page.fill("#composer", "");
      await page.focus("#composer");
      await press(page, touch, '[data-testid="composer-expand"]');
      await page.setViewportSize({ width, height: Math.round(height * 0.6) });
      await page.waitForTimeout(400);
      const kb = await geometry(page);
      check(
        `${at}: a keyboard that resizes the page keeps the expanded composer above it`,
        kb.card.bottom <= Math.round(height * 0.6) + 1 &&
          // Keeps its step under the header, unless the room left is less
          // than the field's floor and there is nothing more it can give.
          (near(kb.expand.top - kb.head.bottom, kb.rhythm) ||
            near(kb.field.height, floor)),
        `card ${kb.card.top.toFixed(0)}-${kb.card.bottom.toFixed(0)} in ${Math.round(height * 0.6)}`,
      );
      await page.setViewportSize({ width, height });
      await page.waitForTimeout(300);
    }

    check(`${at}: no sideways scroll`, !(await geometry(page)).sideways);
    await context.close();
  }

  // A keyboard that overlays the page (iOS Safari): the layout keeps its
  // height and only the visual viewport shrinks, which a stand-in emulates.
  {
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      hasTouch: true,
      isMobile: true,
      locale: "en-US",
    });
    await context.addInitScript(() => {
      const target = new EventTarget();
      const state = { height: window.innerHeight, offsetTop: 0 };
      const vv = {
        get height() {
          return state.height;
        },
        get width() {
          return window.innerWidth;
        },
        get offsetTop() {
          return state.offsetTop;
        },
        get offsetLeft() {
          return 0;
        },
        get pageTop() {
          return window.scrollY + state.offsetTop;
        },
        get pageLeft() {
          return 0;
        },
        get scale() {
          return 1;
        },
        addEventListener: (...a) => target.addEventListener(...a),
        removeEventListener: (...a) => target.removeEventListener(...a),
        dispatchEvent: (e) => target.dispatchEvent(e),
      };
      Object.defineProperty(window, "visualViewport", {
        configurable: true,
        get: () => vv,
      });
      window.__keyboard = (keyboardPx, offsetTop = 0) => {
        state.height = window.innerHeight - keyboardPx;
        state.offsetTop = offsetTop;
        target.dispatchEvent(new Event("resize"));
        target.dispatchEvent(new Event("scroll"));
      };
    });
    const page = await context.newPage();
    await page.goto(`${URL_BASE}/?lang=en#/s/${first}`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector('[data-testid="user-message-body"]');
    await page.waitForTimeout(800);
    await page.focus("#composer");
    await page.tap('[data-testid="composer-expand"]');
    for (const [keyboardPx, offsetTop] of [
      [336, 0],
      [336, 120],
    ]) {
      await page.evaluate(
        ([k, o]) => window.__keyboard(k, o),
        [keyboardPx, offsetTop],
      );
      await page.waitForTimeout(400);
      const kb = await geometry(page);
      const visibleBottom = offsetTop + 844 - keyboardPx;
      check(
        `390x844 overlaying keyboard (page pushed ${offsetTop}px): the expanded composer stays in the visible area`,
        kb.card.bottom <= visibleBottom + 1 &&
          kb.expand.top >= Math.max(kb.head.bottom, offsetTop) - 1,
        `card ${kb.card.top.toFixed(0)}-${kb.card.bottom.toFixed(0)}, visible ${offsetTop}-${visibleBottom}`,
      );
    }
    await context.close();
  }
} finally {
  await browser.close();
}

if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log("\nall checks passed");
