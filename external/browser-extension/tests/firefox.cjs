const { firefox } = require("playwright");
const path = require("node:path");
const assert = require("node:assert/strict");
const { installFirefox, freePort } = require("./firefox-loader.cjs");
module.exports = async () => {
  const port = await freePort();
  const context = await firefox.launchPersistentContext(
    process.env.CODDY_TEST_PROFILE,
    {
      headless: true,
      timeout: 30000,
      args: ["--start-debugger-server", String(port)],
      firefoxUserPrefs: {
        "devtools.chrome.enabled": true,
        "devtools.debugger.remote-enabled": true,
        "devtools.debugger.prompt-connection": false,
      },
    },
  );
  let panel;
  try {
    panel = await installFirefox(
      port,
      path.join(
        process.cwd(),
        "external/browser-extension/dist/coddy-firefox.xpi",
      ),
      true,
    );
    const evaluate = panel.evaluate;
    const wait = async (expression) => {
      for (let i = 0; i < 100; i++) {
        if (await evaluate(expression)) return;
        await new Promise((r) => setTimeout(r, 100));
      }
      throw new Error(
        "Firefox panel timeout: " +
          expression +
          "\n" +
          (await evaluate("document.body.innerText")),
      );
    };
    const form = `document.getElementById('address').value=${JSON.stringify(process.env.CODDY_TEST_URL)};document.getElementById('key').value=${JSON.stringify(process.env.CODDY_TEST_KEY)};document.getElementById('name').value='Firefox test';document.getElementById('connect-form').requestSubmit();undefined`;
    await evaluate(form);
    await wait(`document.getElementById('status').textContent==='Connected'`);
    const page = await context.newPage();
    await page.goto(process.env.CODDY_TEST_URL + "/example");
    await wait(
      `[...document.querySelectorAll('#tabs .tab')].some(e=>e.textContent.includes('Browser fixture'))`,
    );
    await evaluate(
      `{const e=[...document.querySelectorAll('#tabs .tab')].find(e=>e.textContent.includes('Browser fixture')).querySelector('input');e.checked=true;e.dispatchEvent(new Event('change'));}undefined`,
    );
    await evaluate(
      `globalThis.__testState=null;browser.runtime.sendMessage({type:'state'}).then(r=>globalThis.__testState=JSON.stringify(r.value));undefined`,
    );
    await wait(`globalThis.__testState!==null`);
    const state = JSON.parse(await evaluate("globalThis.__testState"));
    const tab = state.tabs.find((t) => t.title === "Browser fixture").id;
    const call = async (action, params = {}) => {
      const response = await fetch(
        process.env.CODDY_TEST_URL + "/test/action",
        {
          method: "POST",
          body: JSON.stringify({ action, tab_id: tab, params }),
        },
      );
      if (!response.ok) throw new Error(await response.text());
      return response.json();
    };
    const first = call("snapshot");
    await wait(
      `[...document.querySelectorAll('#permissions button')].some(b=>b.textContent==='Always allow site')`,
    );
    await evaluate(
      `[...document.querySelectorAll('#permissions button')].find(b=>b.textContent==='Always allow site').click();undefined`,
    );
    const snapshot = await first;
    assert.match(snapshot.text, /Browser fixture/);
    const ref = (name) => snapshot.elements.find((e) => e.name === name).ref;
    await call("click", { ref: ref("Click me") });
    await page.getByRole("button", { name: "Clicked", exact: true }).waitFor();
    await call("type", { ref: ref("Name"), text: "Firefox input" });
    assert.equal(
      await page.locator("input[aria-label=Name]").inputValue(),
      "Firefox input",
    );
    await call("type", { ref: ref("Editor"), text: "Edited", clear: true });
    assert.equal(await page.locator("[contenteditable]").innerText(), "Edited");
    await call("upload_file", { ref: ref("Upload") });
    assert.equal(
      await page.locator("input[type=file]").evaluate((e) => e.files[0].name),
      "example.txt",
    );
    await call("download_file", {
      url: process.env.CODDY_TEST_URL + "/download",
    });
    const picture = await call("screenshot");
    assert.match(picture.data_url, /^data:image\/png;base64,/);
    await evaluate(
      `document.getElementById('prompt').value='Test streaming permissions';document.getElementById('chat-form').requestSubmit();undefined`,
    );
    await wait(
      `[...document.querySelectorAll('#chat-permissions button')].some(b=>b.textContent==='Allow tool once')`,
    );
    await evaluate(
      `[...document.querySelectorAll('#chat-permissions button')].find(b=>b.textContent==='Allow tool once').click();undefined`,
    );
    await wait(
      `document.getElementById('messages').textContent.includes('Permission accepted')`,
    );
    await evaluate(`document.getElementById('stop').click();undefined`);
    await assert.rejects(call("click", { x: 10, y: 10 }), /not shared/);
    console.log(
      "PASS (firefox): registration, selected tabs, site permission, snapshot, DOM input/editor, upload, download, screenshot, streaming chat permission, Stop",
    );
  } finally {
    panel?.close();
    await context.close();
  }
};
