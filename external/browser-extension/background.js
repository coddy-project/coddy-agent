import { siteOf, serverURL, canRun } from "./policy.js";
import { pageAction } from "./page.js";
import { chromeInput, nativeInput } from "./input.js";
import { consumeSSE } from "./sse.js";

const api = globalThis.browser || chrome;
let config = {},
  socket,
  status = "Disconnected",
  epoch = 0,
  reconnectTimer,
  nativeReady = false;
let shared = [],
  allowed = [],
  pending = new Map(),
  active = new Map(),
  queues = new Map();
let grant = crypto.randomUUID();
const cancelled = new Set();
const capabilities = [
  "snapshot",
  "screenshot",
  "navigate",
  "click",
  "type",
  "press",
  "scroll",
  "drag",
  "upload_file",
  "download_file",
];
const nativeActions = ["click", "type", "press", "scroll", "drag"];
api.runtime.onInstalled.addListener((details) => {
  if (details.reason === "install")
    void api.tabs.create({ url: api.runtime.getURL("panel.html") });
});

function send(data) {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(data));
}
// The MV3 service worker is torn down ~30s after its last incoming event and
// takes in-memory state with it. Without this, a restart left `shared` empty,
// so the next publish() unshared every tab and dropped the server-side leases.
async function saveShared(ids) {
  shared = ids;
  await api.storage.local.set({ shared });
}
async function publish() {
  const tabs = [];
  const live = [];
  for (const id of shared) {
    let t;
    try {
      t = await api.tabs.get(id);
    } catch {
      continue;
    }
    live.push(id);
    try {
      siteOf(t.url);
      tabs.push({ id, title: (t.title || "").slice(0, 1000), url: t.url });
    } catch {}
  }
  // A tab id that no longer resolves is gone for good — a restored selection
  // after a browser restart, or one closed while the worker was down. Dropping
  // it only from this publish would leave a stale checkmark in the panel.
  if (live.length !== shared.length) await saveShared(live);
  send({ type: "tabs", tabs, grant });
}
function cancelAll() {
  epoch++;
  for (const [, p] of pending) p.resolve(false);
  pending.clear();
  for (const [, a] of active) a.abort.abort();
  active.clear();
}
async function stop() {
  cancelAll();
  await saveShared([]);
  grant = crypto.randomUUID();
  send({ type: "stop" });
  await api.action.setBadgeText({ text: "" });
}
async function connect() {
  clearTimeout(reconnectTimer);
  cancelAll();
  if (socket) {
    socket.onclose = null;
    socket.close();
  }
  if (!config.enabled) {
    status = "Disconnected";
    return;
  }
  let base;
  try {
    base = serverURL(config.address);
  } catch (e) {
    status = e.message;
    return;
  }
  if (!config.key || config.key.length < 32) {
    status = "Enter the extension key (at least 32 characters)";
    return;
  }
  status = "Connecting…";
  nativeReady = !!api.debugger;
  if (!nativeReady && config.nativeInput) {
    try {
      const result = await api.runtime.sendNativeMessage("dev.coddy.browser", {
        action: "ping",
      });
      nativeReady = !!result.ok;
    } catch (e) {
      status = `Native Firefox input unavailable: ${e.message}`;
      return;
    }
  }
  const ws = (socket = new WebSocket(
    base.replace(/^http/, "ws") + "/browser/v1/connect",
  ));
  let connectionError = "";
  let handshakeTimer = setTimeout(() => ws.close(), 10000);
  ws.onopen = () =>
    ws.send(
      JSON.stringify({
        type: "hello",
        version: 1,
        token: config.key,
        id: config.id,
        name: config.name || "Browser",
        capabilities: [
          ...capabilities,
          nativeReady ? "native_input" : "dom_input",
        ],
      }),
    );
  ws.onmessage = async (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.type === "ready") {
        clearTimeout(handshakeTimer);
        status = "Connected";
        await publish();
      }
      if (msg.type === "error") {
        connectionError = msg.error;
        status = msg.error;
        ws.close();
      }
      if (msg.type === "cancel") {
        cancelled.add(msg.id);
        active.get(msg.id)?.abort.abort();
        const p = pending.get(msg.id);
        if (p) {
          p.resolve(false);
          pending.delete(msg.id);
        }
      }
      if (msg.type === "command") {
        const generation = epoch;
        const previous = queues.get(msg.tab_id) || Promise.resolve();
        const task = previous
          .catch(() => {})
          .then(() => run(msg, generation, ws));
        queues.set(msg.tab_id, task);
        void task.finally(() => {
          if (queues.get(msg.tab_id) === task) queues.delete(msg.tab_id);
        });
      }
    } catch (e) {
      status = String(e.message || e);
    }
  };
  ws.onclose = () => {
    clearTimeout(handshakeTimer);
    cancelAll();
    status = connectionError || "Disconnected — reconnecting";
    if (config.enabled) reconnectTimer = setTimeout(connect, 3000);
  };
  ws.onerror = () => {
    connectionError = "Connection failed: check address, TLS and key";
    status = connectionError;
  };
}

async function approve(cmd, origin, abort) {
  if (allowed.includes(origin)) return;
  const accepted = await new Promise((resolve) => {
    const request = {
      id: cmd.id,
      origin,
      session: cmd.session_id,
      tab: cmd.tab_id,
      resolve,
    };
    pending.set(cmd.id, request);
    abort.addEventListener(
      "abort",
      () => {
        pending.delete(cmd.id);
        resolve(false);
      },
      { once: true },
    );
    void api.action.setBadgeText({ text: "?" });
    void api.action.setBadgeBackgroundColor({ color: "#d98c10" });
    // Opening permission UI is a human action; a webpage cannot approve it.
    void api.notifications
      .create(`permission-${cmd.id}`, {
        type: "basic",
        iconUrl: "icon.svg",
        title: "Coddy needs website access",
        message: `${origin}\nOpen Coddy in the toolbar to allow or decline.`,
      })
      .catch(() => {});
  });
  pending.delete(cmd.id);
  if (!pending.size) await api.action.setBadgeText({ text: "" });
  if (!accepted) throw new Error("Website access denied or cancelled");
}

async function script(tabID, action, params, allFrames = false) {
  const target = { tabId: tabID };
  if (allFrames) target.allFrames = true;
  else if (Number.isInteger(params.frame_id))
    target.frameIds = [params.frame_id];
  const results = await api.scripting.executeScript({
    target,
    func: pageAction,
    args: [action, params],
  });
  if (allFrames)
    return results.map((r) => ({ frame_id: r.frameId, ...r.result }));
  const r = results[0];
  if (!r || r.error || !r.result)
    throw new Error(
      r?.error?.message || "Page did not answer; take another snapshot",
    );
  return r.result;
}
async function transferRequest(id, method, data, signal) {
  if (!/^[a-f0-9]{48}$/.test(id || ""))
    throw new Error("Invalid file transfer");
  const response = await fetch(
    serverURL(config.address) + "/browser/v1/files/" + id,
    {
      method,
      headers: { Authorization: `Bearer ${config.key}` },
      body: data,
      signal,
      redirect: "error",
    },
  );
  if (!response.ok)
    throw new Error(`File transfer failed (${response.status})`);
  return response;
}
function toBase64(bytes) {
  let text = "";
  for (let i = 0; i < bytes.length; i += 32768)
    text += String.fromCharCode(...bytes.subarray(i, i + 32768));
  return btoa(text);
}
async function run(cmd, generation, ws) {
  const abort = new AbortController();
  active.set(cmd.id, { abort, tab: cmd.tab_id, session: cmd.session_id });
  const check = () => {
    if (
      generation !== epoch ||
      abort.signal.aborted ||
      cancelled.has(cmd.id) ||
      socket !== ws ||
      cmd.grant !== grant
    )
      throw new Error("Stopped, tab access changed, or disconnected");
  };
  try {
    check();
    if (!capabilities.includes(cmd.action))
      throw new Error("Unsupported browser action");
    let tab = await api.tabs.get(cmd.tab_id);
    canRun(shared, tab.id, tab.url, allowed);
    const origin = siteOf(tab.url);
    await approve(cmd, origin, abort.signal);
    check();
    tab = await api.tabs.get(cmd.tab_id);
    if (siteOf(tab.url) !== origin || !shared.includes(tab.id))
      throw new Error(
        "Page changed while waiting for permission; inspect it again",
      );
    const p = { ...(cmd.params || {}), expected_origin: origin };
    if (p.frame_id && p.frame_id !== 0)
      throw new Error(
        "Direct frame targeting is not available; use the page screenshot and native input",
      );
    let result;
    if (cmd.action === "navigate") {
      await approve(cmd, siteOf(p.url), abort.signal);
      check();
      await api.tabs.update(tab.id, { url: p.url });
      result = { navigating: p.url };
    } else if (cmd.action === "snapshot") {
      // Cross-origin frames require their own permission before inspection.
      // Inject only the top frame unless a particular frame was requested.
      result = await script(tab.id, "snapshot", p);
    } else if (cmd.action === "screenshot") {
      if (api.debugger)
        result = await chromeInput(api, tab.id, "screenshot", p);
      else {
        const previous = (
          await api.tabs.query({ windowId: tab.windowId, active: true })
        )[0];
        await api.tabs.update(tab.id, { active: true });
        try {
          result = {
            data_url: await api.tabs.captureVisibleTab(tab.windowId, {
              format: "png",
            }),
          };
        } finally {
          if (previous && previous.id !== tab.id)
            await api.tabs
              .update(previous.id, { active: true })
              .catch(() => {});
        }
      }
    } else if (cmd.action === "upload_file") {
      const response = await transferRequest(
        p.transfer_id,
        "GET",
        undefined,
        abort.signal,
      );
      const bytes = new Uint8Array(await response.arrayBuffer());
      check();
      if (bytes.length > 32 * 1024 * 1024)
        throw new Error("File exceeds 32 MiB");
      result = await script(tab.id, "upload_file", {
        ...p,
        base64: toBase64(bytes),
      });
    } else if (cmd.action === "download_file") {
      const address = new URL(p.url, tab.url);
      await approve(cmd, siteOf(address.href), abort.signal);
      check();
      const response = await fetch(address.href, {
        credentials: "include",
        redirect: "error",
        signal: abort.signal,
      });
      if (!response.ok) throw new Error(`Download failed (${response.status})`);
      const reader = response.body.getReader();
      const chunks = [];
      let count = 0;
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        count += value.length;
        if (count > 32 * 1024 * 1024) {
          await reader.cancel();
          throw new Error("File exceeds 32 MiB");
        }
        chunks.push(value);
      }
      check();
      await transferRequest(
        p.transfer_id,
        "POST",
        new Blob(chunks),
        abort.signal,
      );
      result = { bytes: count, filename: p.filename };
    } else if (nativeReady && nativeActions.includes(cmd.action)) {
      if (p.ref) Object.assign(p, await script(tab.id, "target", p));
      check();
      if (api.debugger)
        result = await chromeInput(api, tab.id, cmd.action, {
          ...p,
          cancelled: () => generation !== epoch || abort.signal.aborted,
        });
      else {
        await api.tabs.update(tab.id, { active: true });
        await api.windows.update(tab.windowId, { focused: true });
        const screen = await script(tab.id, "screen", {
          expected_origin: origin,
        });
        screen.screen_scale = screen.scale / (await api.tabs.getZoom(tab.id));
        if (cmd.action === "scroll") {
          p.x ??= screen.width / 2;
          p.y ??= screen.height / 2;
        }
        check();
        result = await nativeInput(
          api,
          { ...p, ...screen, action: cmd.action },
          abort.signal,
        );
        if (result.error) throw new Error(result.error);
      }
    } else result = await script(tab.id, cmd.action, p);
    check();
    send({ type: "reply", id: cmd.id, result });
  } catch (e) {
    if (socket === ws)
      send({ type: "reply", id: cmd.id, error: String(e.message || e) });
  } finally {
    active.delete(cmd.id);
    cancelled.delete(cmd.id);
  }
}

api.runtime.onMessage.addListener((msg, sender, respond) => {
  // Only packaged extension pages may change policy or read the server key.
  if (
    sender.id !== api.runtime.id ||
    !sender.url?.startsWith(api.runtime.getURL(""))
  )
    return false;
  (async () => {
    if (msg.type === "state") {
      const tabs = (await api.tabs.query({})).filter((t) => {
        try {
          siteOf(t.url);
          return true;
        } catch {
          return false;
        }
      });
      return {
        config: {
          address: config.address || "",
          name: config.name || "",
          enabled: !!config.enabled,
          keyConfigured: !!config.key,
          nativeInput: !!config.nativeInput,
        },
        status,
        tabs: tabs.map((t) => ({ id: t.id, title: t.title, url: t.url })),
        shared,
        allowed,
        pending: [...pending.values()].map(({ id, origin, session }) => ({
          id,
          origin,
          session,
        })),
        active: [...active.values()].map((a) => ({
          tab: a.tab,
          session: a.session,
        })),
        native: nativeReady,
      };
    }
    if (msg.type === "save") {
      const address = serverURL(msg.address);
      if (address !== config.address && !msg.key)
        throw new Error("Enter a key for the new server");
      if (address !== config.address) {
        allowed = [];
        await api.storage.local.set({ allowed });
      }
      config = {
        ...config,
        address,
        name: String(msg.name || "Browser").slice(0, 100),
        key: msg.key || config.key,
        enabled: true,
        nativeInput: !!msg.nativeInput,
      };
      await api.storage.local.set({ config });
      await stop();
      await connect();
      return { ok: true };
    }
    if (msg.type === "disconnect") {
      config.enabled = false;
      await api.storage.local.set({ config });
      await stop();
      await connect();
      return { ok: true };
    }
    if (msg.type === "share") {
      if (msg.ids.length > 100) throw new Error("Select at most 100 tabs");
      cancelAll();
      grant = crypto.randomUUID();
      await saveShared([...new Set(msg.ids)].filter(Number.isInteger));
      await publish();
      return { ok: true };
    }
    if (msg.type === "stop") {
      await stop();
      return { ok: true };
    }
    if (msg.type === "permission") {
      const p = pending.get(msg.id);
      if (!p) throw new Error("Permission request expired");
      if (msg.choice === "always") {
        allowed = [...new Set([...allowed, p.origin])];
        await api.storage.local.set({ allowed });
      }
      pending.delete(msg.id);
      p.resolve(msg.choice === "once" || msg.choice === "always");
      return { ok: true };
    }
    if (msg.type === "revoke") {
      allowed = allowed.filter((x) => x !== msg.origin);
      await api.storage.local.set({ allowed });
      await stop();
      return { ok: true };
    }
    if (msg.type === "chat") {
      const path = String(msg.path || "");
      if (!path.startsWith("/") || path.includes(".."))
        throw new Error("Invalid chat path");
      const headers = {
        Authorization: `Bearer ${config.key}`,
        "Content-Type": "application/json",
      };
      if (msg.session) headers["X-Coddy-Session-ID"] = msg.session;
      const response = await fetch(
        serverURL(config.address) + "/browser/v1/chat" + path,
        {
          method: msg.method || "GET",
          headers,
          body: msg.body ? JSON.stringify(msg.body) : undefined,
          redirect: "error",
        },
      );
      const text = await response.text();
      if (!response.ok)
        throw new Error(
          text.slice(0, 500) || `Chat request failed (${response.status})`,
        );
      return {
        data: text ? JSON.parse(text) : null,
        session: response.headers.get("X-Coddy-Session-ID"),
      };
    }
    throw new Error("Unknown extension request");
  })().then(
    (value) => respond({ value }),
    (e) => respond({ error: String(e.message || e) }),
  );
  return true;
});
api.runtime.onConnect.addListener((port) => {
  if (
    port.name !== "coddy-chat" ||
    port.sender?.id !== api.runtime.id ||
    !port.sender?.url?.startsWith(api.runtime.getURL(""))
  ) {
    port.disconnect();
    return;
  }
  const abort = new AbortController();
  port.onDisconnect.addListener(() => abort.abort());
  let running = false;
  port.onMessage.addListener(async (msg) => {
    if (running) return;
    running = true;
    try {
      const response = await fetch(
        serverURL(config.address) + "/browser/v1/chat/v1/responses",
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${config.key}`,
            "Content-Type": "application/json",
            // A new chat sends no session id at all: the server mints one and
            // only then does this key get to reach that conversation.
            ...(msg.session ? { "X-Coddy-Session-ID": msg.session } : {}),
          },
          body: JSON.stringify({
            model: "agent",
            input: msg.input,
            stream: true,
            metadata: msg.model ? { model: msg.model } : {},
          }),
          signal: abort.signal,
          redirect: "error",
        },
      );
      if (!response.ok) throw new Error((await response.text()).slice(0, 500));
      // The id the server minted for this turn is the conversation the panel
      // has to keep naming; a turn of a chat already under way reports none.
      const started = response.headers.get("X-Coddy-Session-ID");
      if (started)
        port.postMessage({ event: "session", data: { sessionId: started } });
      await consumeSSE(response.body, (event, data) =>
        port.postMessage({ event, data }),
      );
      port.postMessage({ event: "finished" });
    } catch (e) {
      try {
        port.postMessage({ event: "failed", error: String(e.message || e) });
      } catch {}
    } finally {
      running = false;
    }
  });
});
api.tabs.onRemoved.addListener(async (id) => {
  await saveShared(shared.filter((x) => x !== id));
  await publish();
});
api.tabs.onUpdated.addListener((id, info) => {
  if (shared.includes(id) && (info.url || info.title)) void publish();
});
api.alarms.onAlarm.addListener(() => {
  if (config.enabled && (!socket || socket.readyState === WebSocket.CLOSED))
    void connect();
  else send({ type: "ping" });
});
setInterval(() => send({ type: "ping" }), 20000);
async function initialize() {
  await api.alarms.create("coddy-connection", { periodInMinutes: 0.5 });
  const stored = await api.storage.local.get(["config", "allowed", "shared"]);
  config = stored.config || { id: crypto.randomUUID() };
  config.id ||= crypto.randomUUID();
  allowed = stored.allowed || [];
  shared = (stored.shared || []).filter(Number.isInteger);
  if (api.storage.local.setAccessLevel)
    await api.storage.local.setAccessLevel({ accessLevel: "TRUSTED_CONTEXTS" });
  await api.storage.local.set({ config });
  await connect();
}
void initialize().catch((e) => {
  status = String(e.message || e);
});
