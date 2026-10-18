#!/usr/bin/env node
/**
 * WebKit layout check for the surfaces Safari reports land on.
 *
 * Safari bugs are hard to act on without a Mac, but Playwright ships the same
 * WebKit build Safari is cut from (`playwright install webkit` pulls WebKit
 * 26.x for Safari 26.x), so a Linux box can reproduce them. This harness drives
 * a running `coddy serve` server and asserts the invariants a scrollable dialog
 * has to keep on a short window.
 *
 * The same is asked of Settings -> System (`#/settings/status`): with every
 * block the server's status document asks for drawn, the settings panel scrolls
 * to its end with the last block and the footer line in sight above the
 * drawer's buttons and the page behind it still, the copy button is reachable,
 * and every item of the summary can be focused and takes the focus, scrolled
 * to, to its block's heading. It runs twice per window: on the server's own
 * document, and on that document with a restart pending, a listen address
 * that moves, a relay that cannot be reached, prompts waiting and one item of
 * every kind in the summary (the status read answered by the script), since a
 * stand rarely has anything to say.
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

// ------------------------------------------------------------------ System

const PANE = '.settings-scroll[data-settings-tab="status"]';
/** The always-drawn blocks of the tab, and the two a build with metrics adds. */
const SYSTEM_BLOCKS = {
  installation: '[data-testid="system-installation"]',
  subsystems: '[data-testid="system-subsystems"]',
  disk: '[data-testid="system-disk"]',
  activity: '[data-testid="system-activity"]',
  charts: ".system-charts",
  providers: ".system-table",
};
/** The heading a summary item takes the focus to, by the block it names. */
const HEADINGS = {
  installation: `${SYSTEM_BLOCKS.installation} .system-block-title`,
  subsystems: `${SYSTEM_BLOCKS.subsystems} .system-block-title`,
  disk: `${SYSTEM_BLOCKS.disk} .system-block-title`,
  providers: ".system-block > h3[id$='-providers']",
};
/** The block an attention code points at (systemStatus.ts, attentionItem). */
const ATTENTION_BLOCK = {
  restart_required: "subsystems",
  disk_low: "disk",
  subsystem_down: "subsystems",
  provider_failing: "providers",
  swarm_join_down: "subsystems",
};

/** The relay the crowded document cannot reach, and for how long. */
const CROWDED_RELAY = "relay.example:12346";
const CROWDED_RELAY_DOWN_MIN = 12;
/** The address the crowded document's HTTP server moves to after a restart. */
const CROWDED_PENDING = "0.0.0.0:12399";

/**
 * The server's document with something to say in every line of the summary:
 * a restart pending (and refused, with a long reason) with a listen address
 * that moves, a relay it has not been registered with for 12 minutes, a
 * permission and two questions waiting, and one attention item of each kind
 * the build can raise, in the order the server lists them. Only what the build
 * can have: a provider is failing only where there is a metrics view to name
 * one.
 */
function crowded(doc) {
  const attention = [
    { code: "restart_required" },
    { code: "disk_low" },
    { code: "subsystem_down", subsystem: "httpserver" },
  ];
  const out = {
    ...doc,
    restartRequired: true,
    restartRefused:
      "listen tcp 127.0.0.1:12345: bind: address already in use (another coddy serve holds the port)",
    prompts: { permission: 1, question: 2 },
    subsystems: doc.subsystems.map((sub) =>
      sub.kind === "httpserver" && !sub.pendingAddress
        ? { ...sub, pendingAddress: CROWDED_PENDING }
        : sub,
    ),
  };
  if (doc.metrics) {
    attention.push({
      code: "provider_failing",
      provider: "a-provider-with-a-rather-long-name",
    });
    out.metrics = {
      ...doc.metrics,
      providers: [
        {
          name: "a-provider-with-a-rather-long-name",
          attempts: 1204,
          failures: 40,
          meanSeconds: 6.1,
          lastFailureAt: new Date(Date.now() - 600000).toISOString(),
        },
        { name: "local", attempts: 3, failures: 0, meanSeconds: 0.4 },
      ],
    };
  }
  attention.push({
    code: "swarm_join_down",
    relay: CROWDED_RELAY,
    offlineSince: new Date(
      Date.parse(doc.generatedAt) - CROWDED_RELAY_DOWN_MIN * 60000,
    ).toISOString(),
  });
  out.attention = attention;
  return out;
}

/** Where the panel and the lines under test are, in the window's coordinates. */
function paneGeometry(sel) {
  const pane = document.querySelector(sel);
  if (!pane) return null;
  const box = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { top: r.top, bottom: r.bottom, left: r.left, right: r.right };
  };
  const blocks = [...pane.querySelectorAll(".system-blocks > .system-block")];
  return {
    pane: box(pane),
    footer: box(document.querySelector(".settings-footer-actions")),
    last: box(blocks[blocks.length - 1]),
    foot: box(pane.querySelector(".system-foot")),
    copy: box(pane.querySelector(".system-copy")),
    scrollTop: pane.scrollTop,
    travel: pane.scrollHeight - pane.clientHeight,
    sideways: pane.scrollWidth - pane.clientWidth,
    pageSideways:
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
    pageY: window.scrollY,
    viewport: { w: innerWidth, h: innerHeight },
  };
}

const round = (n) => Math.round(n * 10) / 10;
const where = (g, b) =>
  b
    ? `top ${round(b.top)} bottom ${round(b.bottom)} against the panel's ${round(g.pane.top)} to ${round(g.pane.bottom)}`
    : "not drawn";
/** Whole inside the panel's visible part and the window's width. */
const inside = (g, b) =>
  !!b &&
  b.top >= g.pane.top - 1 &&
  b.bottom <= g.pane.bottom + 1 &&
  b.left >= -0.5 &&
  b.right <= g.viewport.w + 0.5;

async function systemScenario(browser) {
  const res = await fetch(`${URL_BASE}/coddy/status`).catch(() => null);
  const live = res?.ok ? await res.json().catch(() => null) : null;
  if (live?.object !== "coddy.status") {
    check(
      `${ENGINE} System: the server answers GET /coddy/status`,
      false,
      `status ${res ? res.status : "no answer"} from ${URL_BASE}`,
    );
    return;
  }
  for (const [width, height] of VIEWPORTS) {
    for (const [variant, doc, answer] of [
      ["live document", live, null],
      ["crowded summary", crowded(live), crowded(live)],
    ]) {
      const at = `${ENGINE} ${width}x${height} System (${variant})`;
      const page = await browser.newPage({ viewport: { width, height } });
      page.on("pageerror", (err) =>
        check(`${at} no page error`, false, err.message),
      );
      if (answer) {
        // The status route answered from inside the page: a request that
        // WebKit issues while the page loads can slip past page.route().
        await page.addInitScript((body) => {
          const real = window.fetch;
          window.fetch = function (input, init) {
            const url = typeof input === "string" ? input : input.url;
            return /\/coddy\/status(\?|$)/.test(url)
              ? Promise.resolve(
                  new Response(body, {
                    status: 200,
                    headers: { "Content-Type": "application/json" },
                  }),
                )
              : real.call(this, input, init);
          };
        }, JSON.stringify(answer));
      }
      await page.goto(`${URL_BASE}/#/settings/status`, {
        waitUntil: "domcontentloaded",
      });
      await page.waitForSelector('[data-testid="system-section"]', {
        timeout: 15000,
      });
      await page.waitForFunction(
        () =>
          document
            .querySelector("[data-testid=system-summary]")
            ?.getAttribute("data-state") !== "pending",
        null,
        { timeout: 15000 },
      );
      await page.waitForTimeout(400);
      const read = () => page.evaluate(paneGeometry, PANE);

      // Every block the document asks for is drawn.
      const wanted = ["installation", "subsystems", "disk", "activity"].concat(
        doc.metrics ? ["charts", "providers"] : [],
      );
      const missing = [];
      for (const name of wanted) {
        if ((await page.locator(SYSTEM_BLOCKS[name]).count()) === 0)
          missing.push(name);
      }
      check(
        `${at} every block of the document is drawn (${wanted.join(", ")})`,
        missing.length === 0,
        `missing: ${missing.join(", ")}`,
      );

      // The panel scrolls to its end by the wheel, and only the panel does.
      let g = await read();
      if (!g) {
        check(`${at} the status panel is drawn`, false, PANE);
        await page.close();
        continue;
      }
      const pageBefore = g.pageY;
      if (g.travel > 1) {
        await page.mouse.move(
          (g.pane.left + g.pane.right) / 2,
          (g.pane.top + g.pane.bottom) / 2,
        );
        for (let i = 0; i < 60 && g.travel - g.scrollTop > 1; i++) {
          await page.mouse.wheel(0, 400);
          await page.waitForTimeout(40);
          g = await read();
        }
        await page.waitForTimeout(300);
        g = await read();
        check(
          `${at} the panel scrolls to its end by the wheel`,
          g.travel - g.scrollTop <= 1,
          `scrollTop ${round(g.scrollTop)} of ${round(g.travel)}`,
        );
        check(
          `${at} the page behind the panel stays still`,
          g.pageY === pageBefore,
          `page ${pageBefore} -> ${g.pageY}`,
        );
      } else {
        console.log(`skip ${at} the panel fits, nothing to scroll`);
      }
      check(
        `${at} the last block ends inside the panel`,
        g.last !== null &&
          g.last.bottom <= g.pane.bottom + 1 &&
          g.last.bottom > g.pane.top,
        where(g, g.last),
      );
      check(
        `${at} the line under the blocks is in sight at the end`,
        inside(g, g.foot),
        where(g, g.foot),
      );
      check(
        `${at} the panel ends above the drawer's buttons`,
        g.footer === null || g.pane.bottom <= g.footer.top + 1,
        g.footer
          ? `panel bottom ${round(g.pane.bottom)}, buttons top ${round(g.footer.top)}`
          : "",
      );
      check(
        `${at} nothing scrolls sideways`,
        g.sideways <= 0 && g.pageSideways <= 0,
        `panel ${g.sideways}px, page ${g.pageSideways}px`,
      );

      // The copy button, scrolled to wherever it is, is whole in the panel and
      // takes a pointer (Playwright's trial click does the hit test).
      const copy = page.locator(".system-copy");
      if ((await copy.count()) === 0) {
        console.log(
          `skip ${at} the status names no config file, no copy button`,
        );
      } else {
        await copy.scrollIntoViewIfNeeded();
        await page.waitForTimeout(300);
        g = await read();
        check(
          `${at} the copy button is in the panel's view`,
          inside(g, g.copy),
          where(g, g.copy),
        );
        const hit = await copy.click({ trial: true, timeout: 3000 }).then(
          () => "",
          (err) => String(err.message).split("\n")[0],
        );
        check(`${at} the copy button takes a pointer`, hit === "", hit);
      }

      // What the crowded document adds to the page: the moving listen address
      // and the relay line under Subsystems, the two prompt counts in
      // Activity. Each is drawn once, with its value, and can be scrolled to.
      if (answer) {
        const reach = async (label, locator, text) => {
          const n = await locator.count();
          if (n !== 1) {
            check(`${at} ${label} is drawn once`, false, `${n} found`);
            return;
          }
          const shown = (await locator.textContent())
            .replace(/\s+/g, " ")
            .trim();
          check(
            `${at} ${label} reads as the document says`,
            text.test(shown),
            shown,
          );
          await locator.scrollIntoViewIfNeeded();
          await page.waitForTimeout(250);
          const g = await read();
          const bb = await locator.boundingBox();
          const b = bb && {
            top: bb.y,
            bottom: bb.y + bb.height,
            left: bb.x,
            right: bb.x + bb.width,
          };
          check(
            `${at} ${label} is in the panel's view`,
            inside(g, b),
            where(g, b),
          );
        };
        const dd = (block, label) =>
          page
            .locator(`${SYSTEM_BLOCKS[block]} dt`, { hasText: label })
            .locator("xpath=following-sibling::dd[1]");
        await reach(
          "the HTTP server's pending address",
          page
            .locator(`${SYSTEM_BLOCKS.subsystems} dt`, {
              hasText: "HTTP server",
            })
            .locator("xpath=following-sibling::dd[1]"),
          new RegExp(
            `after restart.*${CROWDED_PENDING.replace(/\./g, "\\.")}|${CROWDED_PENDING.replace(/\./g, "\\.")}.*after restart`,
          ),
        );
        await reach(
          "the relay line under Subsystems",
          page.locator(`${SYSTEM_BLOCKS.subsystems} .system-relay`),
          new RegExp(
            `Not registered with the relay ${CROWDED_RELAY.replace(/\./g, "\\.")} for ${CROWDED_RELAY_DOWN_MIN}\\s?m`,
          ),
        );
        await reach(
          "the Awaiting permission row",
          dd("activity", "Awaiting permission"),
          /^1$/,
        );
        await reach(
          "the Awaiting an answer row",
          dd("activity", "Awaiting an answer"),
          /^2$/,
        );
        check(
          `${at} the summary names the relay among its items`,
          (await page
            .locator(".system-summary .system-link", {
              hasText: "Relay unreachable",
            })
            .count()) === 1,
        );
      }

      // Every item of the summary can be focused, and takes the focus to the
      // heading of its block, scrolled to, from either end of the panel.
      const items = [
        ...(doc.attention ?? []).map((a) => ATTENTION_BLOCK[a.code]),
        ...(doc.unavailable ?? []).map((n) =>
          n === "disk" ? "disk" : "installation",
        ),
      ];
      const links = page.locator(".system-summary .system-link");
      const shown = await links.count();
      check(
        `${at} the summary offers a button for each of its ${items.length} items`,
        shown === items.length,
        `${shown} buttons; the summary reads ${JSON.stringify(
          await page.evaluate(() => {
            const el = document.querySelector("[data-testid=system-summary]");
            return {
              state: el?.getAttribute("data-state"),
              text: el?.textContent.trim().slice(0, 160),
            };
          }),
        )}`,
      );
      for (let i = 0; i < Math.min(shown, items.length); i++) {
        const link = links.nth(i);
        const name = (await link.textContent()).trim();
        await page.evaluate(
          ({ pane, end }) => {
            const el = document.querySelector(pane);
            el.scrollTop = end ? el.scrollHeight : 0;
          },
          { pane: PANE, end: i % 2 === 0 },
        );
        await page.waitForTimeout(150);
        await link.focus();
        const focused = await page.evaluate(
          (text) =>
            document.activeElement?.classList.contains("system-link") &&
            document.activeElement.textContent.trim() === text,
          name,
        );
        check(`${at} "${name}" can be focused`, focused === true);
        await link.click();
        const heading = HEADINGS[items[i]];
        const landed = await page
          .waitForFunction(
            ({ sel, pane }) => {
              const h = document.querySelector(sel);
              if (!h || document.activeElement !== h) return false;
              const r = h.getBoundingClientRect();
              const p = document.querySelector(pane).getBoundingClientRect();
              return r.top >= p.top - 1 && r.bottom <= p.bottom + 1;
            },
            { sel: heading, pane: PANE },
            { timeout: 2500 },
          )
          .then(
            () => true,
            () => false,
          );
        let seen = "";
        if (!landed) {
          seen = await page.evaluate(
            ({ sel, pane }) => {
              const h = document.querySelector(sel);
              const a = document.activeElement;
              const p = document.querySelector(pane).getBoundingClientRect();
              const r = h?.getBoundingClientRect();
              return `the cursor is in ${a?.tagName.toLowerCase()}${a?.className ? "." + String(a.className).trim().split(/\s+/).join(".") : ""}; the heading is ${r ? `at ${Math.round(r.top)} to ${Math.round(r.bottom)}` : "not drawn"} in a panel from ${Math.round(p.top)} to ${Math.round(p.bottom)}`;
            },
            { sel: heading, pane: PANE },
          );
        }
        check(
          `${at} "${name}" takes the focus to the ${items[i]} heading, in sight`,
          landed,
          seen,
        );
      }
      await page.close();
    }
  }
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
  await systemScenario(browser);
} finally {
  await browser.close();
}

if (failures.length > 0) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log("\nall checks passed");
