// Optional Windows test of the local Firefox adapter. It uses an isolated
// browser and a local fixture; no native-host registry entry is installed.
const { firefox } = require("playwright");
const { spawn, execFileSync } = require("node:child_process");
const http = require("node:http");
const path = require("node:path");
const assert = require("node:assert/strict");
const { once } = require("node:events");
(async () => {
  const server = http.createServer((req, res) => {
    res.setHeader("Content-Type", "text/html");
    res.end(
      '<title>Coddy native input fixture</title><textarea aria-label="Editor" style="width:500px;height:200px"></textarea><script>window.lastTrustedKey=false;document.addEventListener("keydown",e=>{window.lastTrustedKey=e.isTrusted})</script>',
    );
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const context = await firefox.launchPersistentContext(
    process.env.CODDY_TEST_PROFILE,
    { headless: false, timeout: 30000 },
  );
  try {
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.bringToFront();
    execFileSync(
      "powershell.exe",
      [
        "-NoProfile",
        "-Command",
        "(New-Object -ComObject WScript.Shell).AppActivate('Coddy native input fixture') | Out-Null",
      ],
      { windowsHide: true },
    );
    await page.getByRole("textbox", { name: "Editor" }).focus();
    await page.waitForFunction(() => document.hasFocus());
    const metrics = await page.evaluate(() => ({
      title: document.title,
      origin_x: mozInnerScreenX,
      origin_y: mozInnerScreenY,
      scale: devicePixelRatio,
      screen_scale: devicePixelRatio,
      width: innerWidth,
      height: innerHeight,
      has_focus: document.hasFocus(),
    }));
    const call = (params, cancelAfter) =>
      new Promise((resolve, reject) => {
        const child = spawn(
          path.join(process.cwd(), ".cache/bin/coddy-browser-host.exe"),
          ["test-host.json", "browser@coddy.dev"],
          { windowsHide: true },
        );
        let buffer = Buffer.alloc(0);
        const timer = setTimeout(() => {
          child.kill();
          reject(new Error("Native host timeout"));
        }, 30000);
        child.on("error", reject);
        child.stdout.on("data", (bytes) => {
          buffer = Buffer.concat([buffer, bytes]);
          if (
            buffer.length >= 4 &&
            buffer.length >= 4 + buffer.readUInt32LE()
          ) {
            clearTimeout(timer);
            resolve(JSON.parse(buffer.subarray(4, 4 + buffer.readUInt32LE())));
          }
        });
        const data = Buffer.from(JSON.stringify({ ...metrics, ...params })),
          header = Buffer.alloc(4);
        header.writeUInt32LE(data.length);
        child.stdin.write(Buffer.concat([header, data]));
        if (cancelAfter !== undefined)
          setTimeout(() => child.stdin.end(), cancelAfter);
      });
    let result = await call({
      action: "type",
      text: "Native Firefox input",
      clear: true,
    });
    assert.equal(result.ok, true, JSON.stringify(result));
    await page.waitForFunction(
      () => document.querySelector("textarea").value === "Native Firefox input",
    );
    result = await call({ action: "press", key: "Ctrl+A" });
    assert.equal(result.ok, true, JSON.stringify(result));
    assert.equal(await page.evaluate(() => window.lastTrustedKey), true);
    result = await call(
      { action: "type", text: "cancel me ".repeat(3000), clear: true },
      10,
    );
    assert.match(result.error, /cancelled|canceled/);
    console.log(
      "PASS: Firefox native Unicode input, trusted shortcut, cancellation on native-port EOF",
    );
  } finally {
    await context.close();
    server.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
