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
    await page.fill("#composer", "one\ntwo\nthree\nfour");
    const four = await geometry(page);
    check(
      `${at}: four lines grow the field`,
      four.field.height > floor + 1,
      `${floor.toFixed(1)} -> ${four.field.height.toFixed(1)}px`,
    );
    await page.fill("#composer", "a long prompt\n".repeat(60));
    const long = await geometry(page);
    // The block never rides over the header: the room it has at the floor.
    const room = empty.dock.top - empty.head.bottom - 8;
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

    // Expanded, the block reaches 8px under the header; folded, it is back.
    // The caret stays in the field through the press, so a phone's keyboard
    // stays open over the expanded field.
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
      `${at}: expanded, the docked block reaches the header`,
      near(open.dock.top, open.head.bottom + 8),
      `dock top ${open.dock.top.toFixed(1)}, header bottom ${open.head.bottom.toFixed(1)}`,
    );
    check(
      `${at}: the expand control stands on the wand's vertical line`,
      near(open.expand.right, open.wand.right) &&
        near(open.card.right - open.expand.right, 12) &&
        open.expand.bottom <= open.field.bottom + 6,
      `wand right ${open.wand.right.toFixed(1)}, control right ${open.expand.right.toFixed(1)}`,
    );
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

    check(`${at}: no sideways scroll`, !(await geometry(page)).sideways);
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
