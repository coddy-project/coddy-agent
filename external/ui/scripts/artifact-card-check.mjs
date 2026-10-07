#!/usr/bin/env node
/**
 * Shared file card check: the card of a file the agent shared is the size the
 * design gives it, its actions trigger mirrors the extension badge, and the menu
 * the trigger opens is on screen and answers the pointer.
 *
 * This is what the vitest suite cannot answer. jsdom has no layout, so nothing
 * there can say that a menu is cut off: the menu used to open inside the card,
 * which is `overflow: hidden`, and a click on the trigger drew nothing at all.
 * The same goes for where the trigger's dots land: as the text glyph "⋮" their
 * ink sat wherever the platform's font put it.
 *
 * It drives `src/artifact-card-check.html`, a stand that mounts the cards from
 * the real components against the real stylesheet, so it needs a vite dev
 * server and no backend at all.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   npx vite --port 5243 &
 *   CODDY_UI_URL=http://127.0.0.1:5243 npm run check:artifacts
 *
 * CODDY_ENGINE=webkit (or firefox) runs the same measurements in another engine;
 * CODDY_BROWSER_PATH points chromium at an installed browser (for example
 * /usr/bin/chromium) instead of Playwright's own download;
 * CODDY_ARTIFACT_TOLERANCE_PX raises the 1px allowance.
 */

const URL_BASE = (process.env.CODDY_UI_URL || "http://127.0.0.1:5243").replace(
  /\/+$/,
  "",
);
const ENGINE = process.env.CODDY_ENGINE || "chromium";
const TOLERANCE = Number(process.env.CODDY_ARTIFACT_TOLERANCE_PX || "1");
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
if (BROWSER_PATH && ENGINE !== "chromium") {
  console.error(
    "CODDY_BROWSER_PATH points at a Chromium; WebKit and Firefox run Playwright's own builds",
  );
  process.exit(2);
}

// The card's box from DESIGN.md (Shared file cards): 134x140 on a wide screen,
// at most 118 wide and 132 tall on a phone, where two cards share a row.
function expectedCard(width) {
  return width <= 599
    ? { width: Math.min(118, width / 2 - 28), height: 132 }
    : { width: 134, height: 140 };
}

// Both shipped locales, since the menu's words are of a different length in
// each; a wide window, a narrow desktop window, and phones held both ways, the
// smallest ones included. A phone is emulated as one - touch, no hover - and
// driven by taps: it never shows the trigger on hover, and a menu that only a
// mouse could open or close would pass a mouse-only run.
const CASES = [
  { lang: "en", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 1280, height: 800, touch: false },
  { lang: "ru", width: 390, height: 720, touch: false },
  { lang: "ru", width: 390, height: 844, touch: true },
  { lang: "ru", width: 360, height: 640, touch: true },
  { lang: "en", width: 320, height: 568, touch: true },
  { lang: "ru", width: 844, height: 390, touch: true },
  { lang: "ru", width: 568, height: 320, touch: true },
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

function check(label, ok, detail) {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? " " + detail : ""}`);
  if (!ok) {
    failures.push(label);
  }
}

const px = (n) => `${n > 0 ? "+" : ""}${n.toFixed(2)}px`;

// The geometry of every card on screen: its box, the badge's box, the trigger's
// box and the ink of the trigger's dots. The dots are measured on their shapes,
// not on the svg around them, since an off-centre viewBox is the error looked for.
async function cards(page) {
  return page.evaluate(() => {
    const box = (el) => {
      const b = el.getBoundingClientRect();
      return {
        left: b.left,
        right: b.right,
        top: b.top,
        bottom: b.bottom,
        width: b.width,
        height: b.height,
        cx: b.left + b.width / 2,
        cy: b.top + b.height / 2,
      };
    };
    const out = [];
    for (const card of document.querySelectorAll(".tool-artifact-card")) {
      const badge = card.querySelector(".inline-artifact-extension");
      const trigger = card.querySelector(".inline-artifact-menu-trigger");
      const shapes = trigger
        ? [...trigger.querySelectorAll("svg circle, svg path, svg rect")]
        : [];
      let ink = null;
      if (shapes.length > 0) {
        const boxes = shapes.map((s) => s.getBoundingClientRect());
        const left = Math.min(...boxes.map((b) => b.left));
        const right = Math.max(...boxes.map((b) => b.right));
        const top = Math.min(...boxes.map((b) => b.top));
        const bottom = Math.max(...boxes.map((b) => b.bottom));
        ink = { cx: (left + right) / 2, cy: (top + bottom) / 2 };
      }
      out.push({
        id: card.dataset.testid,
        card: box(card),
        badge: badge ? box(badge) : null,
        trigger: trigger ? box(trigger) : null,
        ink,
        glyph: trigger ? trigger.textContent.trim() : "",
      });
    }
    return out;
  });
}

// What the open menu is on screen: its box, and for each item whether the
// pointer at its centre reaches the item itself. A menu clipped by an ancestor
// or covered by a neighbour fails the second test even when it is in the DOM.
async function openMenu(page) {
  return page.evaluate(() => {
    const menus = [...document.querySelectorAll('[role="menu"]')].filter((m) =>
      m.matches(".inline-artifact-menu"),
    );
    if (menus.length !== 1) return { count: menus.length };
    const menu = menus[0];
    const b = menu.getBoundingClientRect();
    const items = [...menu.querySelectorAll('[role="menuitem"]')].map(
      (item) => {
        const r = item.getBoundingClientRect();
        const hit = document.elementFromPoint(
          r.left + r.width / 2,
          r.top + r.height / 2,
        );
        return {
          text: item.textContent.trim(),
          reachable: r.width > 0 && r.height > 0 && !!hit && item.contains(hit),
          clipped: item.scrollWidth > item.clientWidth + 1,
        };
      },
    );
    return {
      count: 1,
      box: { left: b.left, right: b.right, top: b.top, bottom: b.bottom },
      viewport: { width: innerWidth, height: innerHeight },
      items,
      focused: menu.contains(document.activeElement),
    };
  });
}

function onScreen(menu) {
  const { box, viewport } = menu;
  return (
    box.left >= 0 &&
    box.top >= 0 &&
    box.right <= viewport.width &&
    box.bottom <= viewport.height
  );
}

const browser = await launcher.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);
try {
  for (const { lang, width, height, touch } of CASES) {
    const context = await browser.newContext({
      viewport: { width, height },
      ...(touch ? { hasTouch: true, isMobile: ENGINE !== "firefox" } : {}),
    });
    const page = await context.newPage();
    await page.goto(`${URL_BASE}/artifact-card-check.html?lang=${lang}`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector(".tool-artifact-card");
    const at = `${ENGINE} ${lang} ${width}x${height}${touch ? " touch" : ""}`;
    const want = expectedCard(width);
    // A tap on a phone, a click elsewhere.
    const press = (locator) => (touch ? locator.tap() : locator.click());
    const pressAt = (x, y) =>
      touch ? page.touchscreen.tap(x, y) : page.mouse.click(x, y);

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - innerWidth,
    );
    check(
      `${at} the page does not scroll sideways`,
      overflow <= 0,
      `${overflow}px`,
    );

    const rows = await cards(page);
    check(
      `${at} the stand mounts every card`,
      rows.length >= 5,
      `${rows.length} cards`,
    );
    for (const row of rows) {
      const where = `${at} ${row.id}`;
      check(
        `${where} is ${want.width}x${want.height}`,
        Math.abs(row.card.width - want.width) <= 0.5 &&
          Math.abs(row.card.height - want.height) <= 0.5,
        `${row.card.width.toFixed(1)}x${row.card.height.toFixed(1)}`,
      );
      if (!row.badge || !row.trigger) {
        check(`${where} has a badge and a trigger`, false);
        continue;
      }
      check(
        `${where} the trigger is level with the badge`,
        Math.abs(row.trigger.cy - row.badge.cy) <= TOLERANCE,
        px(row.trigger.cy - row.badge.cy),
      );
      const leftInset = row.badge.cx - row.card.left;
      const rightInset = row.card.right - row.trigger.cx;
      check(
        `${where} the trigger mirrors the badge across the card`,
        Math.abs(rightInset - leftInset) <= TOLERANCE,
        px(rightInset - leftInset),
      );
      if (!row.ink) {
        check(
          `${where} draws its dots as an svg`,
          false,
          `glyph "${row.glyph}"`,
        );
        continue;
      }
      check(
        `${where} the dots are centred in the trigger`,
        Math.abs(row.ink.cx - row.trigger.cx) <= 0.5 &&
          Math.abs(row.ink.cy - row.trigger.cy) <= 0.5,
        `${px(row.ink.cx - row.trigger.cx)} ${px(row.ink.cy - row.trigger.cy)}`,
      );
    }

    // Cards that end an answer leave the gap above its foot to the foot's own
    // margin: the row's margin for the paragraph after it used to stand on top
    // of that and set the foot twice as far from the cards as from a paragraph.
    const feet = await page.evaluate(() =>
      [...document.querySelectorAll(".msg-assistant-foot")]
        .filter((foot) =>
          foot.previousElementSibling?.matches(".inline-artifacts"),
        )
        .map((foot) => {
          const cards = [
            ...foot.previousElementSibling.querySelectorAll(
              ".tool-artifact-card",
            ),
          ];
          const bottom = Math.max(
            ...cards.map((card) => card.getBoundingClientRect().bottom),
          );
          return {
            gap: foot.getBoundingClientRect().top - bottom,
            margin: parseFloat(getComputedStyle(foot).marginTop),
          };
        }),
    );
    check(
      `${at} the stand ends an answer with its cards`,
      feet.length > 0,
      `${feet.length} answers`,
    );
    for (const foot of feet) {
      check(
        `${at} the foot stands its own margin under the cards`,
        Math.abs(foot.gap - foot.margin) <= TOLERANCE,
        `${foot.gap.toFixed(2)}px, margin ${foot.margin}px`,
      );
    }

    const first = page.locator(".tool-artifact-card").first();
    const trigger = first.locator(".inline-artifact-menu-trigger");

    // A mouse finds the trigger on the card under the pointer; a touch screen,
    // which has no hover, shows it all the time.
    if (!touch) {
      await page.mouse.move(1, 1);
      await first.hover();
    }
    const opacity = Number(
      await trigger.evaluate((el) => getComputedStyle(el).opacity),
    );
    check(
      `${at} the trigger shows ${touch ? "without hover" : "on a hovered card"}`,
      opacity === 1,
      `opacity ${opacity}`,
    );

    await press(trigger);
    let menu = await openMenu(page);
    check(
      `${at} a ${touch ? "tap" : "click"} on the trigger opens one menu`,
      menu.count === 1,
      `${menu.count} menus`,
    );
    if (menu.count === 1) {
      check(
        `${at} the menu is inside the window`,
        onScreen(menu),
        JSON.stringify(menu.box),
      );
      const blocked = menu.items
        .filter((item) => !item.reachable)
        .map((item) => item.text);
      check(
        `${at} every menu item answers the pointer`,
        menu.items.length === 6 && blocked.length === 0,
        blocked.length
          ? `blocked: ${blocked.join(", ")}`
          : `${menu.items.length} items`,
      );
      check(
        `${at} no item is cut off at the menu's edge`,
        menu.items.every((item) => !item.clipped),
      );
      check(`${at} the menu takes the focus`, menu.focused);
    }

    // The trigger closes what it opened.
    await press(trigger);
    menu = await openMenu(page);
    check(
      `${at} a second ${touch ? "tap" : "click"} on the trigger closes the menu`,
      menu.count === 0,
      `${menu.count} menus`,
    );

    // Escape closes it and hands the focus back to the trigger.
    await press(trigger);
    await page.keyboard.press("Escape");
    menu = await openMenu(page);
    const focusBack = await trigger.evaluate(
      (el) => document.activeElement === el,
    );
    check(
      `${at} Escape closes the menu and returns the focus`,
      menu.count === 0 && focusBack,
      `${menu.count} menus, focus on trigger: ${focusBack}`,
    );

    // A press elsewhere closes it.
    await press(trigger);
    await pressAt(width - 4, height - 4);
    menu = await openMenu(page);
    check(
      `${at} a ${touch ? "tap" : "click"} outside closes the menu`,
      menu.count === 0,
      `${menu.count} menus`,
    );

    // The card's own context menu opens the same menu.
    if (!touch) {
      await first.click({ button: "right", position: { x: 20, y: 70 } });
      menu = await openMenu(page);
      check(
        `${at} a right click on the card opens the menu`,
        menu.count === 1,
        `${menu.count} menus`,
      );
      await page.keyboard.press("Escape");
    }

    // At the foot of the window the menu opens upward rather than off screen,
    // and a window too short for either side still holds the whole menu.
    const lastCard = page.locator(".tool-artifact-card").last();
    await lastCard.evaluate((el) => el.scrollIntoView({ block: "end" }));
    if (!touch) await lastCard.hover();
    const lastTrigger = lastCard.locator(".inline-artifact-menu-trigger");
    await press(lastTrigger);
    menu = await openMenu(page);
    if (menu.count === 1) {
      const triggerTop = await lastTrigger.evaluate(
        (el) => el.getBoundingClientRect().top,
      );
      const menuHeight = menu.box.bottom - menu.box.top;
      const roomAbove = triggerTop - 6 - menuHeight >= 8;
      const blocked = menu.items.filter((item) => !item.reachable);
      check(
        `${at} at the foot of the window the menu is on screen${roomAbove ? ", above the trigger" : ""}`,
        onScreen(menu) &&
          blocked.length === 0 &&
          (!roomAbove || menu.box.bottom <= triggerTop),
        JSON.stringify(menu.box),
      );
    } else {
      check(`${at} the last card opens its menu`, false, `${menu.count} menus`);
    }

    // A scroll of the page carries the menu with its trigger; one that takes
    // the trigger out of the window closes the menu rather than leave it over
    // the wrong rows. The first card has the stand's spacer below it, so the
    // page can scroll it both ways.
    await page.keyboard.press("Escape");
    await page.evaluate(() => window.scrollTo(0, 0));
    await press(trigger);
    const before = await openMenu(page);
    const scrolled = await trigger.evaluate((el) => {
      const box = el.getBoundingClientRect();
      const end = document.documentElement.scrollHeight - innerHeight;
      // A step the page can take that keeps the trigger in the window.
      const step = [40, -40].find(
        (d) =>
          scrollY + d >= 0 &&
          scrollY + d <= end &&
          box.top - d >= 0 &&
          box.bottom - d <= innerHeight,
      );
      const start = scrollY;
      window.scrollBy(0, step ?? 0);
      return scrollY - start;
    });
    await page.waitForTimeout(50);
    const after = await openMenu(page);
    if (before.count === 1 && after.count === 1) {
      const moved = after.box.top - before.box.top;
      check(
        `${at} the menu follows its trigger when the page scrolls`,
        scrolled !== 0 && Math.abs(moved + scrolled) <= TOLERANCE,
        `${px(moved)} for a scroll of ${scrolled}px`,
      );
    } else {
      check(
        `${at} the menu stays open through a short scroll`,
        false,
        `${before.count} then ${after.count} menus`,
      );
    }
    const gone = await trigger.evaluate((el) => {
      window.scrollBy(0, el.getBoundingClientRect().bottom + 10);
      return el.getBoundingClientRect().bottom <= 0;
    });
    await page.waitForTimeout(50);
    menu = await openMenu(page);
    check(
      `${at} the menu closes once its trigger has left the window`,
      gone && menu.count === 0,
      `${menu.count} menus, trigger above the window: ${gone}`,
    );

    // The transcript scrolls under the chat's sticky title and the docked
    // composer without leaving the window: a trigger under a layer like them
    // is out of sight too, and the menu must not stay over that layer.
    await page.evaluate(() => window.scrollTo(0, 0));
    await press(trigger);
    const opened = (await openMenu(page)).count;
    await trigger.evaluate((el) => {
      const cover = document.createElement("div");
      cover.className = "artifact-card-stand-cover";
      Object.assign(cover.style, {
        position: "fixed",
        left: "0",
        right: "0",
        top: "0",
        height: `${Math.ceil(el.getBoundingClientRect().bottom) + 2}px`,
        zIndex: "4",
        background: "#000",
      });
      document.body.append(cover);
      window.scrollBy(0, 1);
    });
    await page.waitForTimeout(50);
    menu = await openMenu(page);
    check(
      `${at} the menu closes once a layer covers its trigger`,
      opened === 1 && menu.count === 0,
      `${opened} then ${menu.count} menus`,
    );
    await page.evaluate(() =>
      document.querySelector(".artifact-card-stand-cover")?.remove(),
    );

    await context.close();
  }

  // A window shorter than the menu: the menu keeps inside it and scrolls its
  // items, rather than cut the last ones off.
  {
    const context = await browser.newContext({
      viewport: { width: 640, height: 170 },
    });
    const page = await context.newPage();
    await page.goto(`${URL_BASE}/artifact-card-check.html?lang=ru`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForSelector(".tool-artifact-card");
    const at = `${ENGINE} ru 640x170`;
    const first = page.locator(".tool-artifact-card").first();
    await first.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await first.hover();
    await first.locator(".inline-artifact-menu-trigger").click();
    const short = await page.evaluate(() => {
      const menu = document.querySelector(".inline-artifact-menu");
      if (!menu) return null;
      const items = [...menu.querySelectorAll('[role="menuitem"]')];
      const last = items[items.length - 1];
      last.scrollIntoView({ block: "nearest" });
      const box = menu.getBoundingClientRect();
      const r = last.getBoundingClientRect();
      const hit = document.elementFromPoint(
        r.left + r.width / 2,
        r.top + r.height / 2,
      );
      return {
        inside: box.top >= 0 && box.bottom <= innerHeight,
        scrolls: menu.scrollHeight > menu.clientHeight,
        lastReachable: !!hit && last.contains(hit),
      };
    });
    check(
      `${at} the menu stays inside a window shorter than it and scrolls its items`,
      !!short && short.inside && short.scrolls && short.lastReachable,
      JSON.stringify(short),
    );
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
