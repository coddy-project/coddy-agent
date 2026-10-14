#!/usr/bin/env node
// Capture the embedded Settings UI from a supplied binary, never a mock server.
// Run from the repository root with CODDY_BIN, CODDY_SCREENSHOT_DIR and
// CODDY_CAPTURE_PHASE=before|after. CODDY_PLAYWRIGHT_MODULE accepts an isolated
// playwright installation; CODDY_BROWSER_PATH selects an installed Chromium.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import { pathToFileURL } from "node:url";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createServer } from "node:net";
import { createServer as createHTTPServer } from "node:http";

const binary = path.resolve(process.env.CODDY_BIN);
const out = path.resolve(process.env.CODDY_SCREENSHOT_DIR);
const before = process.env.CODDY_CAPTURE_PHASE === "before";
const modulePath = process.env.CODDY_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  modulePath ? pathToFileURL(path.resolve(modulePath)).href : "playwright"
);
const root = fs.mkdtempSync(path.join(os.tmpdir(), "coddy-settings-demo-"));
const children = [];
const services = [];
const captures = [];
fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch({
  executablePath: process.env.CODDY_BROWSER_PATH || "/usr/bin/chromium",
  args: ["--no-sandbox"],
});
async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address();
  await new Promise((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve())),
  );
  return port;
}
async function startCatalogue() {
  const server = createHTTPServer((request, response) => {
    if (request.url !== "/coddy/llm/models") {
      response.writeHead(404).end();
      return;
    }
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(
      JSON.stringify({
        protocol: 1,
        data: [{ id: "example", revision: "demo", max_context_tokens: 131072 }],
      }),
    );
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  services.push(server);
  return `http://127.0.0.1:${server.address().port}`;
}
async function start(relay, catalogue) {
  const home = path.join(root, relay ? "relay" : "agent");
  const workspace = path.join(root, "demo-project");
  fs.mkdirSync(home, { recursive: true });
  fs.mkdirSync(workspace, { recursive: true });
  const port = await freePort();
  const config = relay
    ? `swarm:\n  name: demo-relay\n  auth_token: screenshot-fixture-only\nlogger:\n  level: error\n  outputs: [stderr]\n`
    : `providers:\n  - name: demo\n    type: coddy\n    api_base: ${catalogue}\n    proxy: none\nmodels:\n  - model: demo/example\nagent:\n  model: demo/example\nskills:\n  auto_discovery: false\nlogger:\n  level: error\n  outputs: [stderr]\n`;
  const file = path.join(home, "config.yaml");
  fs.writeFileSync(file, config);
  const args = ["serve", "--home", home, "--config", file, "--cwd", workspace];
  args.push(
    ...(relay
      ? [
          "--swarm",
          "--http=false",
          "--swarm-host",
          "0.0.0.0",
          "--swarm-port",
          String(port),
        ]
      : ["-H", "127.0.0.1", "-P", String(port)]),
  );
  const child = spawn(binary, args, {
    cwd: workspace,
    env: {
      ...process.env,
      HOME: home,
      CODDY_HOME: home,
      HTTP_PROXY: "",
      HTTPS_PROXY: "",
      ALL_PROXY: "",
      NO_PROXY: "*",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  children.push(child);
  let logs = "";
  child.stdout.on("data", (b) => {
    logs += b;
  });
  child.stderr.on("data", (b) => {
    logs += b;
  });
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 100; i++) {
    try {
      const response = await fetch(`${base}/coddy/config/schema`, {
        headers: { Authorization: "Bearer screenshot-fixture-only" },
      });
      if (response.ok) return { base, schema: await response.json() };
    } catch {}
    if (child.exitCode !== null) throw new Error(`Fixture exited: ${logs}`);
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`Fixture did not start: ${logs}`);
}
async function shot(page, name, width, locale) {
  await page.evaluate(() => document.activeElement?.blur());
  await page.evaluate(() => document.fonts.ready);
  assert.equal(await page.locator(".settings-error").count(), 0);
  const visibleText = await page.locator("body").innerText();
  const fieldValues = await page
    .locator("input, textarea")
    .evaluateAll((fields) => fields.map((field) => field.value).join("\n"));
  assert.doesNotMatch(
    `${visibleText}\n${fieldValues}`,
    /screenshot-fixture-only|127\.0\.0\.|localhost|\/home\/|\/tmp\/|192\.168\.|10\.\d+\.\d+\.\d+|172\.(?:1[6-9]|2\d|3[01])\.|Bearer\s+\S+/i,
    `${name}: screenshot must contain only neutral fixture values`,
  );
  const credentials = await page
    .locator('input[type="password"]')
    .evaluateAll((fields) => fields.map((field) => field.value));
  assert.ok(credentials.every((value) => value === ""));
  const metrics = await page
    .locator(".settings-scroll")
    .evaluate((element) => ({
      width: element.clientWidth,
      scrollWidth: element.scrollWidth,
    }));
  assert.ok(
    metrics.scrollWidth <= metrics.width + 1,
    `${name} ${locale} ${width}: Settings must not overflow horizontally`,
  );
  const filename = `${name}${locale === "ru" ? "-ru" : ""}-dark-${width}.png`;
  await page.screenshot({
    path: path.join(out, filename),
    animations: "disabled",
  });
  captures.push({ filename, locale, viewport: page.viewportSize(), metrics });
}
try {
  const catalogue = await startCatalogue();
  const agent = await start(false, catalogue);
  const relay = await start(true, catalogue);
  const provider = agent.schema.properties.providers.items.properties;
  for (const key of [
    "ca_file",
    "client_cert_file",
    "client_key_file",
    "tls_auto",
  ])
    assert.equal(Object.hasOwn(provider, key), before, key);
  assert.ok(provider.api_base && provider.proxy && provider.api_key);
  const swarm = relay.schema.properties.swarm.properties;
  assert.ok(swarm.tls);
  if (!before)
    assert.deepEqual(Object.keys(swarm.tls.properties).sort(), [
      "cert_file",
      "key_file",
    ]);
  assert.equal(Object.hasOwn(swarm, "node_tls"), before);
  for (const entry of [swarm.join, swarm.upstreams]) {
    const dial = entry.items.properties.dial.properties;
    assert.ok(dial.proxy);
    assert.equal(Object.hasOwn(dial, "ca_file"), before);
  }
  for (const locale of ["en", "ru"]) {
    const labels =
      locale === "ru"
        ? {
            ca: "Файл CA",
            joins: "Родительские релеи",
            upstreams: "Узлы релея",
            dial: "Подключение",
            proxy: "Прокси",
            add: "Добавить",
            cert: "Файл сертификата",
            key: "Файл ключа",
          }
        : {
            ca: "CA file",
            joins: "Joins",
            upstreams: "Upstreams",
            dial: "Dialling",
            proxy: "Proxy",
            add: "Add",
            cert: "Certificate file",
            key: "Key file",
          };
    for (const width of [1280, 390]) {
      const context = await browser.newContext({
        viewport: { width, height: width === 1280 ? 1000 : 844 },
        locale: locale === "ru" ? "ru-RU" : "en-US",
        extraHTTPHeaders: { Authorization: "Bearer screenshot-fixture-only" },
      });
      const page = await context.newPage();
      await page.goto(
        `${agent.base}/?lang=${locale}#/settings/providers?id=demo`,
      );
      await page.getByTestId("provider-model-example").waitFor();
      // Keep the captured endpoint neutral without fetching an external host.
      await page
        .getByLabel(locale === "ru" ? "Базовый URL API" : "API base URL", {
          exact: true,
        })
        .fill("https://models.example.com");
      const advanced = page.getByTestId("settings-group-advanced");
      await advanced.waitFor();
      await advanced.locator("button").first().click();
      await advanced.scrollIntoViewIfNeeded();
      assert.equal(
        await advanced.getByLabel(labels.ca, { exact: true }).count(),
        before ? 1 : 0,
      );
      assert.ok(await page.getByTestId("proxy-setting-direct").count());
      assert.ok(await page.getByTestId("proxy-setting-url").count());
      await shot(page, "provider-advanced", width, locale);
      await page.goto(`${relay.base}/?lang=${locale}#/settings/swarm`);
      await page.getByTestId("settings-save").waitFor();
      if (before) {
        const tls = page
          .locator("fieldset")
          .filter({ has: page.locator("legend", { hasText: /^TLS$/ }) })
          .first();
        await tls.scrollIntoViewIfNeeded();
        await shot(page, "relay-tls", width, locale);
        const node = page
          .locator("fieldset")
          .filter({
            has: page.locator("legend", {
              hasText: /Node.*TLS|Node tls|node_tls/i,
            }),
          })
          .first();
        if (await node.count()) {
          await node.scrollIntoViewIfNeeded();
          await shot(page, "relay-node-tls", width, locale);
        }
      } else {
        const tls = page
          .locator("fieldset")
          .filter({ has: page.locator("legend", { hasText: /^TLS$/ }) })
          .first();
        await tls.waitFor();
        assert.equal(await tls.locator("input").count(), 2);
        await tls
          .getByLabel(labels.cert, { exact: true })
          .fill("certs/relay.crt");
        await tls
          .getByLabel(labels.key, { exact: true })
          .fill("certs/relay.key");
        assert.equal(
          await page
            .locator("legend")
            .filter({ hasText: /Node.*TLS|Node tls|node_tls|PKI|mTLS/i })
            .count(),
          0,
        );
        await page.locator(".settings-scroll").evaluate((element) => {
          element.scrollTop = 0;
        });
        await shot(page, "relay-connection", width, locale);
        await tls.scrollIntoViewIfNeeded();
        await shot(page, "relay-tls", width, locale);
      }
      for (const [legend, name] of [
        [labels.joins, "relay-join-proxy"],
        [labels.upstreams, "relay-upstream-proxy"],
      ]) {
        const joins = page
          .locator("fieldset")
          .filter({
            has: page.locator("legend", { hasText: new RegExp(`^${legend}$`) }),
          })
          .first();
        await joins
          .getByRole("button", { name: labels.add, exact: true })
          .click();
        const dial = joins
          .locator("fieldset")
          .filter({
            has: page.locator("legend", {
              hasText: new RegExp(`^${labels.dial}$`),
            }),
          })
          .first();
        await dial.scrollIntoViewIfNeeded();
        assert.equal(
          await dial.getByLabel(labels.ca, { exact: true }).count(),
          before ? 1 : 0,
        );
        assert.ok(await dial.getByLabel(labels.proxy, { exact: true }).count());
        await shot(page, name, width, locale);
      }
      await context.close();
    }
  }
  const report = {
    phase: before ? "before" : "after",
    binary,
    binarySHA256: createHash("sha256")
      .update(fs.readFileSync(binary))
      .digest("hex"),
    assertions: `${before ? "Legacy TLS fields present" : "Relay certificate/key pair retained; PKI, mTLS and custom provider/dial TLS fields absent"}; HTTPS endpoint, proxy and auth schema retained; no horizontal Settings overflow`,
    captures,
  };
  fs.writeFileSync(
    path.join(out, "capture-report.json"),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  console.log(`Captured ${report.phase} Settings in ${out}`);
  console.log(JSON.stringify(report, null, 2));
} finally {
  await browser.close();
  for (const child of children) child.kill("SIGTERM");
  await Promise.all(
    children.map((child) =>
      child.exitCode !== null
        ? Promise.resolve()
        : new Promise((r) => child.once("exit", r)),
    ),
  );
  await Promise.all(
    services.map(
      (server) =>
        new Promise((resolve, reject) =>
          server.close((error) => (error ? reject(error) : resolve())),
        ),
    ),
  );
  fs.rmSync(root, { recursive: true, force: true });
}
