#!/usr/bin/env node
/**
 * Attachments check: pictures from a screenshot tool, driven in a real browser
 * against the real binary and a scripted model.
 *
 * A screenshot tool such as Spectacle hands the browser a file in a temporary
 * folder and deletes it at the next capture, so a File the browser holds only
 * by its path stops being readable. jsdom has no such files. The check drops and
 * pastes files from disk, deletes them the way the tool does, and follows the
 * pictures to the model byte for byte: their cards, the sent message, what the
 * model receives. It also holds a picture's row while the model is still busy,
 * which is where a queued message used to show a bare icon.
 *
 * Usage, from the repository root:
 *   make build TAGS="http ui"
 *   cd external/ui
 *   npm i --no-save playwright && npx playwright install chromium   # once
 *   CODDY_BIN=../../build/coddy npm run check:attachments
 *
 * Environment:
 *   CODDY_BIN           the binary (default ../../build/coddy from external/ui)
 *   CODDY_BROWSER_PATH  an installed Chromium instead of Playwright's download
 *   CODDY_PORT_BASE     first of two loopback ports (default 19900)
 *   CODDY_E2E_KEEP=1    leave the stand's files behind, for a look
 */

import { spawn } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import zlib from "node:zlib";

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.resolve(
  process.env.CODDY_BIN || path.join(here, "../../../build/coddy"),
);
const BROWSER_PATH = process.env.CODDY_BROWSER_PATH || "";
const PORT_BASE = Number(process.env.CODDY_PORT_BASE || 19900);
const KEEP = process.env.CODDY_E2E_KEEP === "1";

const failures = [];
function check(label, ok, detail = "") {
  console.log(`${ok ? "ok  " : "FAIL"} ${label}${detail ? " " + detail : ""}`);
  if (!ok) failures.push(label);
  return ok;
}

let playwright;
try {
  playwright = await import("playwright");
} catch {
  console.error(
    "playwright is not installed. Run: npm i --no-save playwright && npx playwright install chromium",
  );
  process.exit(2);
}
if (!fs.existsSync(BIN)) {
  console.error(`${BIN} not found; build it with: make build TAGS="http ui"`);
  process.exit(2);
}

/** A solid 48x32 PNG of one colour, so every picture has bytes of its own. */
function solidPng([r, g, b]) {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf) => {
    let c = 0xffffffff;
    for (const byte of buf) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(body));
    return Buffer.concat([len, body, sum]);
  };
  const width = 48;
  const height = 32;
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; // bit depth
  header[9] = 2; // RGB
  const row = Buffer.alloc(1 + width * 3);
  for (let x = 0; x < width; x++) row.set([r, g, b], 1 + x * 3);
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", zlib.deflateSync(Buffer.concat(Array(height).fill(row)))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

const sha = (buf) =>
  crypto.createHash("sha256").update(buf).digest("hex").slice(0, 12);

// ------------------------------------------------------------ scripted model

/** The prompts whose answer stays open until the script releases it. */
const HOLD_PROMPTS = new Set(["Start the long task", "Queued with a picture"]);

const model = {
  requests: [],
  holds: [],
  release() {
    const waiting = this.holds.splice(0);
    for (const go of waiting) go();
    return waiting.length;
  },
};

const textOf = (m) =>
  typeof m.content === "string"
    ? m.content
    : Array.isArray(m.content)
      ? m.content.map((p) => p.text || "").join("")
      : "";

/** The last message the operator typed: Coddy follows it with a turn context. */
function typedOf(messages) {
  for (const m of [...messages].reverse()) {
    if (m.role !== "user") continue;
    const t = textOf(m)
      .replace(/<coddy_session_assets>[\s\S]*?<\/coddy_session_assets>/g, "")
      .trim();
    if (t && !t.startsWith("<turn_context>")) return t;
  }
  return "";
}

/** The pictures of a request, in order, by the hash of their bytes. */
function imagesOf(messages) {
  const out = [];
  for (const m of messages) {
    if (m.role !== "user" || !Array.isArray(m.content)) continue;
    for (const part of m.content) {
      const url = part?.image_url?.url || "";
      const comma = url.indexOf(",");
      if (comma > 0) out.push(sha(Buffer.from(url.slice(comma + 1), "base64")));
    }
  }
  return out;
}

function startModel(port) {
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", async () => {
      const respondJson = (value) => {
        res.writeHead(200, {
          "Content-Type": "application/json",
          Connection: "close",
        });
        res.end(JSON.stringify(value));
      };
      if (req.url.endsWith("/models")) {
        respondJson({
          object: "list",
          data: [{ id: "coddy-demo", object: "model" }],
        });
        return;
      }
      let parsed = {};
      try {
        parsed = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
      } catch {
        // an empty answer below
      }
      if (!parsed.stream) {
        respondJson({
          id: "x",
          object: "chat.completion",
          model: "coddy-demo",
          choices: [
            {
              index: 0,
              finish_reason: "stop",
              message: { role: "assistant", content: "Attachments demo" },
            },
          ],
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        });
        return;
      }
      const messages = parsed.messages || [];
      const typed = typedOf(messages);
      model.requests.push({ typed, images: imagesOf(messages) });
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "close",
      });
      const send = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
      const chunk = (delta, finish = null) => ({
        id: "x",
        object: "chat.completion.chunk",
        model: "coddy-demo",
        choices: [{ index: 0, delta, finish_reason: finish }],
      });
      send(chunk({ role: "assistant", content: "" }));
      if (HOLD_PROMPTS.has(typed)) {
        send(chunk({ content: "Working on it. " }));
        const keepAlive = setInterval(() => res.write(": keepalive\n\n"), 1000);
        await new Promise((resolve) => model.holds.push(resolve));
        clearInterval(keepAlive);
      }
      send(chunk({ content: `Answer to: ${typed.slice(0, 60)}` }));
      send(chunk({}, "stop"));
      send({
        id: "x",
        object: "chat.completion.chunk",
        model: "coddy-demo",
        choices: [],
        usage: { prompt_tokens: 1, completion_tokens: 3, total_tokens: 4 },
      });
      res.end("data: [DONE]\n\n");
    });
  });
  return new Promise((resolve) =>
    server.listen(port, "127.0.0.1", () => resolve(server)),
  );
}

// ------------------------------------------------------------------- stand

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-attachments-"));
const home = path.join(scratch, "home");
const shots = path.join(scratch, "screenshot-tool");
fs.mkdirSync(home, { recursive: true });
fs.mkdirSync(shots, { recursive: true });

const procs = [];
function cleanup() {
  for (const p of procs) {
    try {
      p.kill("SIGTERM");
    } catch {
      // already gone
    }
  }
  if (!KEEP) fs.rmSync(scratch, { recursive: true, force: true });
}
process.on("exit", cleanup);
process.on("SIGINT", () => process.exit(130));
process.on("SIGTERM", () => process.exit(143));

const MODEL_PORT = PORT_BASE;
const NODE_PORT = PORT_BASE + 1;
const NODE = `http://127.0.0.1:${NODE_PORT}`;
await startModel(MODEL_PORT);
fs.writeFileSync(
  path.join(home, "config.yaml"),
  `providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:${MODEL_PORT}/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
    multimodal: true
agent:
  model: stub/coddy-demo
  queue_mode: steer
tools:
  permission_mode: bypass
logger:
  level: warn
  outputs: [stderr]
`,
);
const log = fs.openSync(path.join(scratch, "node.log"), "w");
procs.push(
  spawn(
    BIN,
    [
      "serve",
      "--config",
      path.join(home, "config.yaml"),
      "--home",
      home,
      "--cwd",
      home,
      "-H",
      "127.0.0.1",
      "-P",
      String(NODE_PORT),
    ],
    { stdio: ["ignore", log, log] },
  ),
);
for (let i = 0; ; i++) {
  const res = await fetch(`${NODE}/v1/models`).catch(() => null);
  if (res && res.ok) break;
  if (i > 160) throw new Error(`the node never came up; logs in ${scratch}`);
  await new Promise((r) => setTimeout(r, 250));
}

// ----------------------------------------------------------------- browser

const browser = await playwright.chromium.launch(
  BROWSER_PATH ? { executablePath: BROWSER_PATH } : {},
);

async function openChat() {
  const context = await browser.newContext({
    viewport: { width: 1280, height: 820 },
    colorScheme: "dark",
    locale: "en-US",
  });
  const page = await context.newPage();
  page.on("pageerror", (err) => console.log(`     page error: ${err.message}`));
  await page.goto(`${NODE}/?lang=en`);
  await composerOf(page).waitFor();
  return { context, page };
}

const composerOf = (page) => page.getByRole("textbox", { name: "Message" });

async function until(what, fn, timeout = 20000) {
  const t0 = Date.now();
  let last;
  while (Date.now() - t0 < timeout) {
    last = await fn();
    if (last) return last;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`timed out: ${what}`);
}

/** A picture of the screenshot tool: a file on disk with bytes of its own. */
function capture(name, rgb) {
  const file = path.join(shots, name);
  const bytes = solidPng(rgb);
  fs.writeFileSync(file, bytes);
  return { file, hash: sha(bytes) };
}

/**
 * Hands the page a File that exists only as a path on disk, the kind a
 * screenshot tool's temporary file becomes, and fires `type` with it.
 */
async function fireWithDiskFile(page, file, type) {
  const input = await page.evaluateHandle(() => {
    const el = document.createElement("input");
    el.type = "file";
    el.style.display = "none";
    document.body.append(el);
    return el;
  });
  await input.setInputFiles(file);
  await page.evaluate(
    ([el, type]) => {
      const dt = new DataTransfer();
      dt.items.add(el.files[0]);
      el.remove();
      if (type === "drop") {
        document.querySelector(".composer-card").dispatchEvent(
          new DragEvent("drop", {
            bubbles: true,
            cancelable: true,
            dataTransfer: dt,
          }),
        );
        return;
      }
      document.querySelector("textarea#composer").dispatchEvent(
        new ClipboardEvent("paste", {
          bubbles: true,
          cancelable: true,
          clipboardData: dt,
        }),
      );
    },
    [input, type],
  );
}

const cardCount = (page) =>
  page.getByTestId("composer-attachment-chip").count();

/** How many of the images under `selector` have a picture to show. */
const loadedImages = (page, selector) =>
  page.$$eval(
    selector,
    (imgs) => imgs.filter((i) => i.complete && i.naturalWidth > 0).length,
  );

/** Every image is decoded again, as a remount or a new row would have to. */
const decodableImages = (page, selector) =>
  page.$$eval(selector, async (imgs) => {
    let ok = 0;
    for (const img of imgs) {
      const probe = new Image();
      probe.src = img.src;
      ok += await new Promise((resolve) => {
        probe.onload = () => resolve(1);
        probe.onerror = () => resolve(0);
      });
    }
    return ok;
  });

const errorNotices = (page) =>
  page.$$eval("[role=alert]", (rows) =>
    rows.map((r) => r.textContent.trim()).filter(Boolean),
  );

const THUMB = ".msg-user-file-thumb";
const CARD_THUMB = "[data-testid=composer-attachment-thumb]";

// ---------------------------------------------------------------- scenarios

/** Drops from the tool, each capture deleting the one before it. */
async function dropsOutliveTheirFiles() {
  const { page, context } = await openChat();
  const taken = [];
  const palette = [
    [220, 40, 40],
    [40, 200, 60],
    [50, 70, 230],
  ];
  for (const [i, rgb] of palette.entries()) {
    const shot = capture(`capture-${i + 1}.png`, rgb);
    await fireWithDiskFile(page, shot.file, "drop");
    await until(`card ${i + 1}`, async () => (await cardCount(page)) === i + 1);
    // Give the composer its moment with the file, then the next capture
    // takes it away.
    await page.waitForTimeout(150);
    fs.rmSync(shot.file);
    taken.push(shot.hash);
  }
  check("three drops leave three cards", (await cardCount(page)) === 3);
  check(
    "every card can still show its picture after the file is gone",
    (await decodableImages(page, CARD_THUMB)) === 3,
    String(await decodableImages(page, CARD_THUMB)),
  );

  const before = model.requests.length;
  await composerOf(page).fill("Look at these");
  await composerOf(page).press("Enter");
  await until(
    "the answer or a refusal",
    async () =>
      model.requests.length > before || (await errorNotices(page)).length > 0,
    15000,
  );
  check(
    "sending raises no error",
    (await errorNotices(page)).length === 0,
    JSON.stringify(await errorNotices(page)),
  );
  const sent = model.requests[before];
  check(
    "the model gets every picture, in order, byte for byte",
    JSON.stringify(sent?.images ?? []) === JSON.stringify(taken),
    JSON.stringify(sent?.images ?? []),
  );
  await until(
    "the sent row's thumbnails",
    async () => (await loadedImages(page, THUMB)) === 3,
    15000,
  ).catch(() => null);
  check(
    "the sent message shows all three pictures",
    (await loadedImages(page, THUMB)) === 3,
    String(await loadedImages(page, THUMB)),
  );
  await context.close();
}

/** Pastes one after another, through the markup editor each opens. */
async function pastesOneAfterAnother() {
  const { page, context } = await openChat();
  const palette = [
    [200, 200, 30],
    [30, 200, 200],
    [200, 30, 200],
  ];
  for (const [i, rgb] of palette.entries()) {
    const shot = capture(`paste-${i + 1}.png`, rgb);
    await fireWithDiskFile(page, shot.file, "paste");
    await page.getByTestId("markup-done").waitFor({ timeout: 15000 });
    await page.getByTestId("markup-done").click();
    await until(
      `pasted card ${i + 1}`,
      async () => (await cardCount(page)) === i + 1,
    );
    fs.rmSync(shot.file);
  }
  check(
    "three pastes in a row keep three cards with pictures",
    (await decodableImages(page, CARD_THUMB)) === 3,
    String(await decodableImages(page, CARD_THUMB)),
  );

  // A paste the operator cancels takes only itself away.
  const dropped = capture("paste-dropped.png", [10, 10, 10]);
  await fireWithDiskFile(page, dropped.file, "paste");
  await page.getByTestId("markup-cancel").waitFor({ timeout: 15000 });
  await page.getByTestId("markup-cancel").click();
  check(
    "a cancelled paste leaves the earlier cards alone",
    (await cardCount(page)) === 3,
  );

  // A second paste while the first picture's editor is open or on its way
  // is not lost either.
  const before = model.requests.length;
  await composerOf(page).fill("Pasted one by one");
  await composerOf(page).press("Enter");
  await until("the model's request", () => model.requests.length > before);
  const sent = model.requests[before];
  check(
    "the model gets three different pasted pictures",
    sent?.images.length === 3 && new Set(sent.images).size === 3,
    JSON.stringify(sent?.images ?? []),
  );
  check(
    "sending raises no error",
    (await errorNotices(page)).length === 0,
    JSON.stringify(await errorNotices(page)),
  );
  await context.close();
}

/** A sent picture is a picture at once, not after the turn. */
async function pictureRowsShowAtOnce() {
  const { page, context } = await openChat();

  // Sent with the prompt: the row is drawn before any answer comes back.
  const direct = capture("direct.png", [90, 160, 240]);
  await fireWithDiskFile(page, direct.file, "drop");
  await until("the card", async () => (await cardCount(page)) === 1);
  await composerOf(page).fill("Start the long task");
  await composerOf(page).press("Enter");
  await until("the held answer", () => model.holds.length === 1, 30000);
  check(
    "a prompt's picture shows while the model is still answering",
    (await loadedImages(page, THUMB)) === 1,
    String(await loadedImages(page, THUMB)),
  );

  // Queued during the turn: the row appears where the agent read it.
  const queued = capture("queued.png", [240, 160, 90]);
  await fireWithDiskFile(page, queued.file, "drop");
  await until("the queued card", async () => (await cardCount(page)) === 1);
  await composerOf(page).fill("Queued with a picture");
  await composerOf(page).press("Enter");
  await until("the queue row", () =>
    page.getByTestId("composer-queue-item").count(),
  );
  model.release();
  await until(
    "the queued prompt's model call",
    () => model.requests.at(-1)?.typed === "Queued with a picture",
    30000,
  );
  await until("the queued prompt held", () => model.holds.length === 1, 30000);
  await until(
    "the queued row",
    () =>
      page.$$eval(".msg-user-body", (rows) =>
        rows.some((r) => r.textContent.includes("Queued with a picture")),
      ),
    15000,
  );
  await page.waitForTimeout(500);
  check(
    "a queued message's picture shows while the model is still answering it",
    (await loadedImages(page, THUMB)) === 2,
    `${await loadedImages(page, THUMB)} of 2, icons ${await page
      .locator(".msg-user-file-chip-icon")
      .count()}`,
  );
  model.release();
  await until(
    "the second answer",
    () =>
      page.$$eval(".md", (rows) =>
        rows.some((r) =>
          r.textContent.includes("Answer to: Queued with a picture"),
        ),
      ),
    30000,
  );
  check(
    "and after the turn, from the server",
    await until(
      "the thumbnails after the turn",
      async () => (await loadedImages(page, THUMB)) === 2,
      15000,
    ).catch(() => false),
  );
  await page.reload();
  check(
    "and after a reload",
    await until(
      "the thumbnails after a reload",
      async () => (await loadedImages(page, THUMB)) === 2,
      15000,
    ).catch(() => false),
  );
  await context.close();
}

/** A read that still fails at send is said on the start screen and loses nothing. */
async function aReadFailureIsSeenOnANewChat() {
  const { page, context } = await openChat();
  const shot = capture("unreadable.png", [70, 70, 70]);
  await fireWithDiskFile(page, shot.file, "drop");
  await until("the card", async () => (await cardCount(page)) === 1);
  await page.evaluate(() => {
    FileReader.prototype.readAsDataURL = function () {
      setTimeout(() => this.onerror?.(new ProgressEvent("error")), 0);
    };
  });
  const before = model.requests.length;
  await composerOf(page).fill("Not sent");
  await composerOf(page).press("Enter");
  check(
    "the operator is told why nothing was sent",
    await until(
      "the notice",
      () => page.getByText("Could not read the attached file").count(),
      15000,
    ).catch(() => false),
  );
  check(
    "and the message waits in the composer with its picture",
    (await composerOf(page).inputValue()) === "Not sent" &&
      (await cardCount(page)) === 1 &&
      model.requests.length === before,
  );
  await context.close();
}

/** What the page shows when a step times out, for whoever reads the run. */
async function dumpPages() {
  for (const context of browser.contexts()) {
    for (const page of context.pages()) {
      const text = await page
        .evaluate(() => {
          const rows = document.querySelector(".messages-inner");
          return (rows ? rows.innerText : document.body.innerText).slice(-800);
        })
        .catch(() => "");
      console.log(`--- ${page.url()}\n${text}`);
    }
  }
}

try {
  await dropsOutliveTheirFiles();
  await pastesOneAfterAnother();
  await pictureRowsShowAtOnce();
  await aReadFailureIsSeenOnANewChat();
} catch (err) {
  check(`the run stopped: ${err.message}`, false);
  await dumpPages();
}
await browser.close();

if (failures.length > 0) {
  console.log(`\n${failures.length} check(s) failed:`);
  for (const f of failures) console.log(`- ${f}`);
  process.exit(1);
}
console.log("\nattachments: all checks passed");
process.exit(0);
