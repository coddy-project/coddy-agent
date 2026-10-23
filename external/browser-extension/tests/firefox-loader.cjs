const net = require("node:net");
const path = require("node:path");

// Test-only Firefox RDP connection for temporary addon installation.
async function installFirefox(port, directory, keepPanel = false) {
  const socket = await new Promise((resolve, reject) => {
    const s = net.connect(port, "127.0.0.1", () => resolve(s));
    s.once("error", reject);
  });
  let buffer = Buffer.alloc(0),
    waiter;
  const inbox = [],
    events = [];
  socket.on("data", (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
    for (;;) {
      const colon = buffer.indexOf(58);
      if (colon < 0) return;
      const size = Number(buffer.subarray(0, colon).toString());
      if (buffer.length < colon + 1 + size) return;
      const msg = JSON.parse(buffer.subarray(colon + 1, colon + 1 + size));
      buffer = buffer.subarray(colon + 1 + size);
      if (msg.type) {
        events.push(msg);
        continue;
      }
      if (waiter) {
        const w = waiter;
        waiter = null;
        w(msg);
      } else inbox.push(msg);
    }
  });
  const receive = () =>
    new Promise((resolve, reject) => {
      if (inbox.length) return resolve(inbox.shift());
      const timer = setTimeout(
        () => reject(new Error("Firefox RDP timed out")),
        10000,
      );
      waiter = (msg) => {
        clearTimeout(timer);
        resolve(msg);
      };
    });
  const request = async (message) => {
    const data = Buffer.from(JSON.stringify(message));
    socket.write(Buffer.concat([Buffer.from(`${data.length}:`), data]));
    const reply = await receive();
    if (reply.error) throw new Error(JSON.stringify(reply));
    return reply;
  };
  try {
    await receive();
    const root = await request({ to: "root", type: "getRoot" });
    if (process.env.CODDY_DEBUG_FIREFOX)
      console.log("RDP ROOT", JSON.stringify(root));
    await request({
      to: root.addonsActor,
      type: "installTemporaryAddon",
      addonPath: path.resolve(directory),
    });
    const list = await request({ to: "root", type: "listAddons" });
    const addon = list.addons.find((a) => a.id === "browser@coddy.dev");
    if (process.env.CODDY_DEBUG_FIREFOX || keepPanel) {
      const watcher = await request({ to: addon.actor, type: "getWatcher" });
      if (process.env.CODDY_DEBUG_FIREFOX)
        console.log("WATCHER", JSON.stringify(watcher));
      await request({
        to: watcher.actor,
        type: "watchTargets",
        targetType: "frame",
      });
      await new Promise((r) => setTimeout(r, 1000));
      if (keepPanel) {
        const target = events
          .map((e) => e.target)
          .find(
            (t) =>
              t?.url?.endsWith("/panel.html") && t.title === "Coddy Browser",
          );
        if (!target) throw new Error("Firefox extension panel target missing");
        return {
          ...addon,
          close: () => socket.destroy(),
          evaluate: async (text) => {
            const ack = await request({
              to: target.consoleActor,
              type: "evaluateJSAsync",
              text,
            });
            for (let i = 0; i < 100; i++) {
              const result = events.find(
                (e) =>
                  e.type === "evaluationResult" && e.resultID === ack.resultID,
              );
              if (result) {
                if (result.hasException)
                  throw new Error(
                    result.exceptionMessage || JSON.stringify(result.exception),
                  );
                const value = result.result;
                if (value?.type === "longString") {
                  const r = await request({
                    to: value.actor,
                    type: "substring",
                    start: 0,
                    end: value.length,
                  });
                  return r.substring;
                }
                return value?.type === "undefined" ? undefined : value;
              }
              await new Promise((r) => setTimeout(r, 50));
            }
            throw new Error("Firefox console evaluation timed out");
          },
        };
      }
      console.log("TARGETS", JSON.stringify(events));
      for (const e of events) {
        const form = e.target || e.targetForm;
        if (form?.consoleActor) {
          console.log(
            "LOGS",
            JSON.stringify(
              await request({
                to: form.consoleActor,
                type: "getCachedMessages",
                messageTypes: ["PageError", "ConsoleAPI"],
              }),
            ),
          );
        }
      }
    }
    if (process.env.CODDY_DEBUG_FIREFOX)
      console.log(
        "RDP TABS",
        JSON.stringify(await request({ to: "root", type: "listTabs" })),
      );
    if (!addon) throw new Error("Firefox did not register the extension");
    return addon;
  } finally {
    if (!keepPanel) socket.destroy();
  }
}
async function freePort() {
  return new Promise((resolve) => {
    const s = net.createServer();
    s.listen(0, "127.0.0.1", () => {
      const p = s.address().port;
      s.close(() => resolve(p));
    });
  });
}
module.exports = { installFirefox, freePort };
