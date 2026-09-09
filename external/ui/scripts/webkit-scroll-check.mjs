#!/usr/bin/env node
/**
 * WebKit layout check for the surfaces Safari reports land on.
 *
 * Safari bugs are hard to act on without a Mac, but Playwright ships the same
 * WebKit build Safari is cut from (`playwright install webkit` pulls WebKit
 * 26.x for Safari 26.x), so a Linux box can reproduce them. This harness drives
 * a running `coddy http` server and asserts the invariants a scrollable dialog
 * has to keep on a short window.
 *
 * Usage, from external/ui:
 *   npm i --no-save playwright && npx playwright install webkit   # once, ~95 MB
 *   CODDY_URL=http://127.0.0.1:12345 \
 *   CODDY_FOLDER=/path/with/many/subdirs \
 *     npm run check:webkit
 *
 * CODDY_ENGINE=chromium runs the same checks in Chromium, which is how you tell
 * a WebKit-only regression from a layout bug that hits every engine.
 */

const URL_BASE = process.env.CODDY_URL || "http://127.0.0.1:12345";
const FOLDER = process.env.CODDY_FOLDER || "";
const ENGINE = process.env.CODDY_ENGINE || "webkit";

// Short windows first: that is where a capped dialog runs out of room.
const VIEWPORTS = [
  [786, 420],
  [786, 380],
  [1024, 300],
  [390, 720],
  [1280, 800],
];

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed. Run: npm i --no-save playwright && npx playwright install webkit",
  );
  process.exit(2);
}

const launcher = playwright[ENGINE];
if (!launcher) {
  console.error(`unknown CODDY_ENGINE ${ENGINE} (use webkit or chromium)`);
  process.exit(2);
}

const failures = [];

function check(label, ok, detail) {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? " " + detail : ""}`);
  if (!ok) {
    failures.push(label);
  }
}

// The same geometry read is taken twice per viewport: once on the dialog as it
// opens, once with the New folder name row expanded, since that row lands
// inside the scrollport and is the state a short window is most likely to
// break.
async function probe(page) {
  return page.evaluate(() => {
    const modal = document.querySelector(".workspace-modal");
    const list = document.querySelector(".workspace-modal-list");
    const open = document.querySelector('[data-testid="workspace-modal-open"]');
    const modalRect = modal.getBoundingClientRect();
    const openRect = open.getBoundingClientRect();
    return {
      clipped: modal.scrollHeight - modal.clientHeight,
      openInside:
        openRect.bottom <= modalRect.bottom + 0.5 &&
        openRect.top >= modalRect.top - 0.5,
      modalInViewport:
        modalRect.top >= -0.5 && modalRect.bottom <= innerHeight + 0.5,
      listOverflows: list.scrollHeight > list.clientHeight,
    };
  });
}

const browser = await launcher.launch();
try {
  for (const [width, height] of VIEWPORTS) {
    const page = await browser.newPage({ viewport: { width, height } });
    await page.goto(URL_BASE, { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(1200);
    await page.click('[data-testid="composer-workspace-chip"]');
    await page.waitForTimeout(300);
    await page.click('[data-testid="workspace-open-folder"]');
    await page.waitForSelector('[data-testid="workspace-folder-modal"]');
    await page.waitForTimeout(400);
    if (FOLDER) {
      await page.fill('[data-testid="workspace-modal-path"]', FOLDER);
      await page.press('[data-testid="workspace-modal-path"]', "Enter");
      await page.waitForTimeout(700);
    }

    const geometry = await probe(page);

    const at = `${ENGINE} ${width}x${height}`;
    // The dialog clips at its rounded corners, so anything laid out past its
    // height cap is unreachable: there is no scrollport around the chrome.
    check(
      `${at} dialog does not clip its own chrome`,
      geometry.clipped <= 0,
      `clipped=${geometry.clipped}px`,
    );
    check(`${at} Open button stays inside the dialog`, geometry.openInside);
    check(
      `${at} dialog stays inside the visible viewport`,
      geometry.modalInViewport,
    );

    if (geometry.listOverflows) {
      // A wheel gesture that runs past the last folder must stay in the list
      // instead of scrolling the page behind the modal.
      const box = await page.locator(".workspace-modal-list").boundingBox();
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      const pageBefore = await page.evaluate(() => window.scrollY);
      await page.mouse.wheel(0, 300);
      await page.waitForTimeout(300);
      const mid = await page.evaluate(() => ({
        list: document.querySelector(".workspace-modal-list").scrollTop,
        page: window.scrollY,
      }));
      check(
        `${at} folder list scrolls on wheel`,
        mid.list > 0,
        `scrollTop=${mid.list}`,
      );

      for (let i = 0; i < 24; i++) {
        await page.mouse.wheel(0, 400);
        await page.waitForTimeout(40);
      }
      await page.waitForTimeout(300);
      const after = await page.evaluate(() => window.scrollY);
      check(
        `${at} overscroll stays in the dialog`,
        after === pageBefore,
        `page ${pageBefore} -> ${after}`,
      );
    } else {
      console.log(
        `skip ${at} list fits, no scroll assertions (set CODDY_FOLDER)`,
      );
    }

    // New folder expands a name row at the top of the list. It is a child of
    // the scrollport, so the dialog must absorb it the same way it absorbs a
    // long listing: nothing laid out past the height cap, buttons still
    // reachable.
    await page.click('[data-testid="workspace-modal-new-folder"]');
    await page.waitForSelector(
      '[data-testid="workspace-modal-new-folder-name"]',
    );
    await page.waitForTimeout(200);
    const withRow = await probe(page);
    check(
      `${at} name row open: dialog does not clip its own chrome`,
      withRow.clipped <= 0,
      `clipped=${withRow.clipped}px`,
    );
    check(
      `${at} name row open: Open button stays inside the dialog`,
      withRow.openInside,
    );
    check(
      `${at} name row open: dialog stays inside the visible viewport`,
      withRow.modalInViewport,
    );
    const rowInside = await page.evaluate(() => {
      const modal = document.querySelector(".workspace-modal");
      const list = document.querySelector(".workspace-modal-list");
      const row = document.querySelector(".workspace-modal-row--new");
      const create = document.querySelector(
        '[data-testid="workspace-modal-new-folder-create"]',
      );
      const modalRect = modal.getBoundingClientRect();
      const rowRect = row.getBoundingClientRect();
      return {
        // The row is a flex: none sibling of the list, so it is whole and
        // visible at every height - inside the scrollport it would be taller
        // than the port itself on the shortest window here.
        outsideScrollport: !list.contains(row),
        visible:
          rowRect.top >= modalRect.top - 0.5 &&
          rowRect.bottom <= modalRect.bottom + 0.5,
        createInside:
          create.getBoundingClientRect().right <= modalRect.right + 0.5,
      };
    });
    check(
      `${at} name row open: row sits outside the scrollport`,
      rowInside.outsideScrollport,
    );
    check(`${at} name row open: row is whole and visible`, rowInside.visible);
    check(
      `${at} name row open: Create folder stays inside the dialog`,
      rowInside.createInside,
    );

    await page.close();
  }
} finally {
  await browser.close();
}

if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log("\nall checks passed");
