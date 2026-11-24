import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const shellSource = readFileSync(new URL("../shell.js", import.meta.url), "utf8");

class FakeClassList {
  constructor(owner) {
    this.owner = owner;
    this.values = new Set();
  }
  add(...names) {
    for (const name of names) this.values.add(name);
    this.owner.className = [...this.values].join(" ");
  }
  remove(...names) {
    for (const name of names) this.values.delete(name);
    this.owner.className = [...this.values].join(" ");
  }
  contains(name) {
    return this.values.has(name);
  }
}

class FakeStyle {
  constructor() {
    this.values = new Map();
    this.colorScheme = "";
  }
  setProperty(name, value) {
    this.values.set(name, String(value));
  }
  getPropertyValue(name) {
    return this.values.get(name) || "";
  }
  removeProperty(name) {
    this.values.delete(name);
  }
}

class FakeElement {
  constructor(tagName, ownerDocument) {
    this.tagName = tagName.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.parentElement = null;
    this.dataset = {};
    this.attributes = new Map();
    this.listeners = new Map();
    this.style = new FakeStyle();
    this.classList = new FakeClassList(this);
    this.className = "";
    this.hidden = false;
    this.disabled = false;
    this.scrollLeft = 0;
    this.scrollWidth = 0;
    this.clientWidth = 1000;
    this.title = "";
    this.value = "";
    this._textContent = "";
  }
  set textContent(value) {
    this._textContent = String(value);
    this.children = [];
  }
  get textContent() {
    return this._textContent;
  }
  get childElementCount() {
    return this.children.length;
  }
  get firstElementChild() {
    return this.children[0] || null;
  }
  appendChild(child) {
    child.parentElement = this;
    this.children.push(child);
    return child;
  }
  remove() {
    if (!this.parentElement) return;
    this.parentElement.children = this.parentElement.children.filter((child) => child !== this);
    this.parentElement = null;
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === "class") this.className = String(value);
  }
  getAttribute(name) {
    if (name === "class") return this.className || null;
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  hasAttribute(name) {
    return this.attributes.has(name);
  }
  removeAttribute(name) {
    this.attributes.delete(name);
  }
  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(listener);
  }
  removeEventListener(type, listener) {
    this.listeners.get(type)?.delete(listener);
  }
  dispatch(type, event = {}) {
    event.type = type;
    event.currentTarget = this;
    if (!event.target) event.target = this;
    if (!event.preventDefault) event.preventDefault = () => { event.defaultPrevented = true; };
    for (const listener of [...(this.listeners.get(type) || [])]) listener.call(this, event);
  }
  matches(selector) {
    if (selector === '[data-testid="nav-new-chat"]') {
      return this.getAttribute("data-testid") === "nav-new-chat";
    }
    if (selector === ".tab[aria-selected=\"true\"]") {
      return this.classList.contains("tab") && this.getAttribute("aria-selected") === "true";
    }
    if (selector.startsWith(".")) return this.classList.contains(selector.slice(1));
    if (selector.startsWith("#")) return this.getAttribute("id") === selector.slice(1);
    if (selector === "a[href]") return this.tagName === "A" && this.getAttribute("href") !== null;
    return this.tagName === selector.toUpperCase();
  }
  closest(selector) {
    for (let node = this; node; node = node.parentElement) {
      if (node.matches(selector)) return node;
    }
    return null;
  }
  querySelectorAll(selector) {
    const found = [];
    const visit = (node) => {
      for (const child of node.children) {
        if (child.matches(selector)) found.push(child);
        visit(child);
      }
    };
    visit(this);
    return found;
  }
  querySelector(selector) {
    if (selector === '[data-testid="composer-workspace-chip"]') selector = "[data-testid]";
    return this.querySelectorAll(selector)[0] || null;
  }
  getBoundingClientRect() {
    return { left: 0, right: 100, top: 0, bottom: 30, width: 100, height: 30 };
  }
  scrollIntoView() {}
  scrollBy() {}
  setPointerCapture() {}
  releasePointerCapture() {}
  focus() {}
}

class FakeDocument extends FakeElement {
  constructor() {
    super("#document", null);
    this.ownerDocument = this;
    this.baseURI = "http://desktop.test/";
    this.documentElement = new FakeElement("html", this);
    this.body = new FakeElement("body", this);
    this.documentElement.appendChild(this.body);
    this.appendChild(this.documentElement);
    this.cookie = "";
    this.visibilityState = "visible";
    this.ids = new Map();
    const root = this.createElement("div");
    root.setAttribute("id", "root");
    this.body.appendChild(root);
  }
  createElement(tagName) {
    if (tagName === "iframe") return new FakeFrame(tagName, this);
    return new FakeElement(tagName, this);
  }
  createTextNode(text) {
    const node = new FakeElement("#text", this);
    node.textContent = text;
    return node;
  }
  getElementById(id) {
    return this.ids.get(id) || this.documentElement.querySelector("#" + id);
  }
}

class FakeFrame extends FakeElement {
  constructor(tagName, ownerDocument) {
    super(tagName, ownerDocument);
    this.srcHistory = [];
    this.postedMessages = [];
    this._src = "";
    this.resetDocument();
  }
  resetDocument() {
    this.contentDocument = new FakeDocument();
    const location = new URL(this._src || "about:blank", "http://desktop.test/");
    this.contentWindow = {
      location,
      addEventListener: (type, listener) => {
        if (!this.windowListeners) this.windowListeners = new Map();
        if (!this.windowListeners.has(type)) this.windowListeners.set(type, new Set());
        this.windowListeners.get(type).add(listener);
      },
      getComputedStyle: () => ({ getPropertyValue: () => "" }),
      MutationObserver: class {
        constructor(callback) { this.callback = callback; }
        observe() {}
      },
      focus() {},
      postMessage: (message, origin) => this.postedMessages.push({ message, origin }),
      fetch: (...args) => this.ownerDocument.defaultFetch(...args),
    };
  }
  set src(value) {
    this._src = value;
    this.srcHistory.push(value);
    this.resetDocument();
  }
  get src() {
    return this._src;
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

function response(body = {}) {
  return { ok: true, json: async () => body, text: async () => "" };
}

function makeShell({ storedTabs, windowFetch } = {}) {
  const document = new FakeDocument();
  const ids = [
    "coddy-desktop-tabs", "menu-btn", "strip", "coddy-desktop-frames", "new-tab", "drag", "menu",
    "window-btns", "scroll-btns", "scroll-left", "scroll-right", "resize-edges",
    "win-min", "win-max", "win-close", "reload-tab", "copy-page-link", "page-actions", "copy-status",
  ];
  for (const id of ids) {
    const element = document.createElement(id === "coddy-desktop-frames" ? "main" : "div");
    element.setAttribute("id", id);
    document.ids.set(id, element);
    document.body.appendChild(element);
  }
  document.ids.get("menu").hidden = true;
  document.ids.get("window-btns").hidden = true;
  document.ids.get("scroll-btns").hidden = true;
  document.ids.get("resize-edges").hidden = true;
  for (const direction of ["n", "s", "w", "e", "nw", "ne", "sw", "se"]) {
    const edge = document.createElement("div");
    edge.dataset.direction = direction;
    document.ids.get("resize-edges").appendChild(edge);
  }

  const raf = new Map();
  const timers = new Map();
  let nextId = 1;
  const storage = new Map();
  if (storedTabs) storage.set("coddy.desktop.tabs.v1", JSON.stringify(storedTabs));
  const defaultFetch = async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, "http://desktop.test/");
    if (url.pathname === "/desktop/keys") return new Promise(() => {});
    if (url.pathname === "/desktop/window" && (!init.method || init.method === "GET")) {
      return response({ supported: true });
    }
    if (url.pathname === "/coddy/sessions") return response({ sessions: [] });
    if (url.pathname === "/desktop/menu") return response({ items: [] });
    return response({});
  };
  const fetch = windowFetch || defaultFetch;
  document.defaultFetch = fetch;
  const window = new FakeElement("window", document);
  window.outerWidth = 1000;
  window.outerHeight = 800;
  const context = vm.createContext({
    console,
    document,
    window,
    location: new URL("http://desktop.test/desktop/"),
    screen: { availWidth: 1920, availHeight: 1080 },
    localStorage: {
      getItem: (key) => storage.get(key) || null,
      setItem: (key, value) => storage.set(key, value),
    },
    fetch,
    Element: FakeElement,
    URL,
    AbortController,
    DOMException,
    encodeURIComponent,
    decodeURIComponent,
    requestAnimationFrame: (callback) => {
      const id = nextId++;
      raf.set(id, callback);
      return id;
    },
    cancelAnimationFrame: (id) => raf.delete(id),
    setTimeout: (callback) => {
      const id = nextId++;
      timers.set(id, callback);
      return id;
    },
    clearTimeout: (id) => timers.delete(id),
    setInterval: () => nextId++,
  });
  context.globalThis = context;
  const expose = `
    globalThis.__shellTest = {
      openNewChatTab,
      setSetting,
      resizeWindowFrom,
      syncFrameSessions,
      refreshSessions,
      labelOf,
      stateOf,
      locallyBusy,
      frameOf,
      tabs: () => tabs,
      frameLoadAttempts,
      frameLoadTimers,
    };
  `;
  const at = shellSource.lastIndexOf("\n})();");
  const instrumented = shellSource.slice(0, at) + expose + shellSource.slice(at);
  vm.runInContext(instrumented, context, { filename: "shell.js" });
  return {
    context,
    document,
    api: context.__shellTest,
    flushFrame: () => {
      const callbacks = [...raf.values()];
      raf.clear();
      for (const callback of callbacks) callback();
    },
    fireTimer: (id) => {
      const callback = timers.get(id);
      timers.delete(id);
      if (callback) callback();
    },
    settle: async () => {
      for (let i = 0; i < 12; i++) await Promise.resolve();
    },
  };
}

function pointer(edge, x, y) {
  return { button: 0, screenX: x, screenY: y, currentTarget: edge, pointerId: 1, preventDefault() {} };
}

test("resize requests stay ordered and move-end waits for the last resize", async () => {
  const actions = [];
  const resizes = [];
  const shell = makeShell({
    windowFetch: async (input, init = {}) => {
      const url = new URL(typeof input === "string" ? input : input.url, "http://desktop.test/");
      if (url.pathname === "/desktop/keys") return new Promise(() => {});
      if (url.pathname === "/desktop/window" && init.method === "POST") {
        const body = JSON.parse(init.body);
        actions.push(body.action + (body.action === "resize-by" ? ":" + body.x : ""));
        if (body.action === "resize-by") {
          const gate = deferred();
          resizes.push(gate);
          await gate.promise;
        }
        return response();
      }
      if (url.pathname === "/desktop/window") return response({ supported: true });
      if (url.pathname === "/coddy/sessions") return response({ sessions: [] });
      if (url.pathname === "/desktop/menu") return response({ items: [] });
      return response({});
    },
  });
  await shell.settle();
  const edge = shell.document.ids.get("resize-edges").children[7];
  shell.api.resizeWindowFrom(pointer(edge, 100, 100));
  await shell.settle();
  edge.dispatch("pointermove", pointer(edge, 110, 100));
  shell.flushFrame();
  assert.deepEqual(actions, ["resize-begin", "resize-by:10"]);
  edge.dispatch("pointermove", pointer(edge, 120, 100));
  shell.flushFrame();
  assert.deepEqual(actions, ["resize-begin", "resize-by:10"], "a second resize overtook the first");
  edge.dispatch("pointerup", pointer(edge, 120, 100));
  await shell.settle();
  assert.equal(actions.includes("move-end"), false, "the gesture ended before its resize answered");
  resizes[0].resolve();
  await shell.settle();
  shell.flushFrame();
  assert.deepEqual(actions, ["resize-begin", "resize-by:10", "resize-by:20"]);
  resizes[1].resolve();
  await shell.settle();
  assert.deepEqual(actions, ["resize-begin", "resize-by:10", "resize-by:20", "move-end"]);
});

test("an iframe load without a mounted SPA keeps the recovery watchdog armed", async () => {
  const shell = makeShell();
  const uid = shell.api.tabs()[0].uid;
  const frame = shell.api.frameOf.get(uid);
  assert.equal(frame.contentDocument.getElementById("root").childElementCount, 0);
  frame.dispatch("load");
  const timer = shell.api.frameLoadTimers.get(uid);
  assert.equal(typeof timer, "number", "the empty SPA shell cancelled its recovery timer");
  const before = frame.srcHistory.length;
  shell.fireTimer(timer);
  assert.equal(frame.srcHistory.length, before + 1, "the watchdog did not retry the empty SPA");
  assert.equal(shell.api.frameLoadAttempts.get(uid), 1);
  frame.contentDocument.getElementById("root").appendChild(frame.contentDocument.createElement("div"));
  frame.dispatch("load");
  assert.equal(shell.api.frameLoadAttempts.get(uid), 0);
  assert.equal(shell.api.frameLoadTimers.has(uid), false, "a mounted SPA left its retry timer running");
});

test("a frame loaded before React mounts still installs link handlers and keeps the mounted draft", () => {
  const shell = makeShell();
  const uid = shell.api.tabs()[0].uid;
  const frame = shell.api.frameOf.get(uid);
  frame.dispatch("load");
  assert.equal(frame.contentDocument.listeners.get("auxclick")?.size, 1, "middle-click handling was not installed");
  const timer = shell.api.frameLoadTimers.get(uid);
  const composer = frame.contentDocument.createElement("textarea");
  composer.value = "keep my draft";
  frame.contentDocument.getElementById("root").appendChild(composer);
  const before = frame.srcHistory.length;
  shell.fireTimer(timer);
  assert.equal(frame.srcHistory.length, before, "the watchdog reloaded a mounted SPA");
  assert.equal(composer.value, "keep my draft");
});

test("a late workspace answer does not replace a ready tab or its draft", async () => {
  const workspace = deferred();
  const shell = makeShell({
    storedTabs: [{ uid: "source", session: "sess_source", route: "#/s/sess_source", workspace: "/fallback" }],
    windowFetch: async (input, init = {}) => {
      const url = new URL(typeof input === "string" ? input : input.url, "http://desktop.test/");
      if (url.pathname === "/desktop/keys") return new Promise(() => {});
      if (url.pathname === "/coddy/workspace/context") {
        await workspace.promise;
        return response({ path: "/resolved" });
      }
      if (url.pathname === "/desktop/window") return response({ supported: true });
      if (url.pathname === "/coddy/sessions") return response({ sessions: [] });
      if (url.pathname === "/desktop/menu") return response({ items: [] });
      return response({});
    },
  });
  const source = shell.api.frameOf.get("source");
  source.contentWindow.location.hash = "#/s/sess_source";
  source.contentWindow.__coddyFetchShimmed = true;
  source.contentDocument.getElementById("root").appendChild(source.contentDocument.createElement("div"));
  shell.api.openNewChatTab("#/");
  const tab = shell.api.tabs().at(-1);
  const frame = shell.api.frameOf.get(tab.uid);
  const readyDocument = frame.contentDocument;
  const composer = readyDocument.createElement("textarea");
  composer.value = "a draft that must survive";
  readyDocument.getElementById("root").appendChild(composer);
  const srcBefore = frame.src;
  workspace.resolve();
  await shell.settle();
  assert.equal(frame.src, srcBefore, "the late answer reloaded the tab");
  assert.equal(frame.contentDocument, readyDocument, "the ready document was replaced");
  assert.equal(frame.contentDocument.querySelector("textarea").value, "a draft that must survive");
});

function workspaceHandoffScenario() {
  const workspace = deferred();
  const shell = makeShell({
    storedTabs: [{ uid: "source", session: "sess_source", route: "#/s/sess_source", workspace: "/fallback" }],
    windowFetch: async (input, init = {}) => {
      const url = new URL(typeof input === "string" ? input : input.url, "http://desktop.test/");
      if (url.pathname === "/desktop/keys") return new Promise(() => {});
      if (url.pathname === "/coddy/workspace/context") {
        await workspace.promise;
        return response({ path: "/resolved" });
      }
      if (url.pathname === "/desktop/window") return response({ supported: true });
      if (url.pathname === "/coddy/sessions") return response({ sessions: [] });
      if (url.pathname === "/desktop/menu") return response({ items: [] });
      return response({});
    },
  });
  const source = shell.api.frameOf.get("source");
  source.contentWindow.location.hash = "#/s/sess_source";
  source.contentWindow.__coddyFetchShimmed = true;
  source.contentDocument.getElementById("root").appendChild(source.contentDocument.createElement("div"));
  shell.api.openNewChatTab("#/");
  const tab = shell.api.tabs().at(-1);
  const frame = shell.api.frameOf.get(tab.uid);
  frame.contentDocument.getElementById("root").appendChild(frame.contentDocument.createElement("div"));
  return { shell, workspace, tab, frame };
}

function postFromFrame(shell, frame, data, origin = "http://desktop.test") {
  shell.context.window.dispatch("message", {
    origin,
    source: frame.contentWindow,
    data: { channel: "coddy.desktop", ...data },
  });
}

test("workspace handoff delivers the resolved folder to a ready frame without reloading it", async () => {
  const { shell, workspace, tab, frame } = workspaceHandoffScenario();
  const url = new URL(frame.src, "http://desktop.test/");
  assert.equal(url.searchParams.get("desktop_workspace_pending"), "1");
  assert.equal(url.searchParams.get("desktop_workspace"), "/fallback");
  const before = frame.src;
  postFromFrame(shell, frame, { type: "workspace-ready" });
  assert.equal(frame.postedMessages.length, 0, "the fallback was reported before resolution");
  workspace.resolve();
  await shell.settle();
  assert.equal(frame.src, before, "the resolved folder reloaded the mounted SPA");
  assert.equal(tab.workspace, "/resolved");
  assert.equal(frame.postedMessages[0].message.type, "workspace-resolved");
  assert.equal(frame.postedMessages[0].message.path, "/resolved");
  assert.equal(frame.postedMessages[0].origin, "http://desktop.test");
  postFromFrame(shell, frame, { type: "workspace-ready" });
  assert.equal(frame.postedMessages.length, 2, "a late ready request was not answered");
  postFromFrame(shell, frame, { type: "workspace-ready" }, "http://untrusted.test");
  assert.equal(frame.postedMessages.length, 2, "an unrelated origin reached the handoff");
});

test("workspace handoff keeps an explicit choice even when it equals the fallback", async () => {
  const { shell, workspace, tab, frame } = workspaceHandoffScenario();
  postFromFrame(shell, frame, { type: "workspace", path: "/fallback", manual: true });
  workspace.resolve();
  await shell.settle();
  assert.equal(tab.workspace, "/fallback", "the delayed source replaced the manual choice");
  assert.equal(frame.postedMessages[0].message.path, "/fallback");
});

function desktopFetchWithSessions(sessions) {
  return async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, "http://desktop.test/");
    if (url.pathname === "/desktop/keys") return new Promise(() => {});
    if (url.pathname === "/desktop/window") return response({ supported: true });
    if (url.pathname === "/coddy/sessions") return response({ sessions });
    if (url.pathname === "/desktop/menu") return response({ items: [] });
    return response({});
  };
}

let nextFrameScope = 1;
function mountSessionFetch(frame, fetch, scope) {
  frame.contentWindow.__coddyFetchShimmed = true;
  frame.contentWindow.fetch = fetch;
  const initial = scope || `frame-scope-${nextFrameScope++}`;
  frame.contentWindow.__coddySessionScope = typeof initial === "function" ? initial : () => initial;
}

test("leaving a chat for a route without a hash clears its old title", async () => {
  const shell = makeShell({
    storedTabs: [{ uid: "tab", session: "sess_old", route: "#/s/sess_old", workspace: "" }],
    windowFetch: desktopFetchWithSessions([]),
  });
  await shell.settle();
  const tab = shell.api.tabs()[0];
  const frame = shell.api.frameOf.get(tab.uid);
  mountSessionFetch(frame, async () => response({ sessions: [{ id: "sess_old", title: "Old chat" }] }));
  await shell.api.refreshSessions();
  assert.equal(shell.api.labelOf(tab), "Old chat");

  // The SPA's new-chat action removes the hash with history.replaceState.
  frame.contentWindow.location.hash = "";
  assert.equal(shell.api.syncFrameSessions(), true);
  assert.equal(tab.session, "");
  assert.equal(tab.route, "#/");
  assert.equal(shell.api.labelOf(tab), "New chat");
});

test("tabs with the same session id read their own iframe environment", async () => {
  const localRow = { id: "sess_same", title: "Local chat", turnActive: true };
  const remoteRow = { id: "sess_same", title: "Remote chat", questionPending: true };
  const remoteErrorRow = { id: "sess_same", title: "Remote error", lastErrorSeq: 7, lastReadSeq: 3 };
  const localCalls = [];
  const remoteCalls = [];
  const remoteErrorCalls = [];
  const shell = makeShell({
    storedTabs: [
      { uid: "local", session: "sess_same", route: "#/s/sess_same", workspace: "" },
      { uid: "remote", session: "sess_same", route: "#/s/sess_same", workspace: "" },
      { uid: "remote-error", session: "sess_same", route: "#/s/sess_same", workspace: "" },
    ],
    windowFetch: desktopFetchWithSessions([localRow]),
  });
  await shell.settle();
  // A shared storage value belongs to whichever frame wrote it last. It must
  // not decide where another frame's cached environment sends its requests.
  shell.context.localStorage.setItem("coddy_env", JSON.stringify({ mode: "remote", baseUrl: "https://wrong.example" }));
  mountSessionFetch(shell.api.frameOf.get("local"), async (input) => {
    localCalls.push(String(input));
    return response({ sessions: [localRow] });
  });
  mountSessionFetch(shell.api.frameOf.get("remote"), async (input, init = {}) => {
    remoteCalls.push({ input: String(input), authorization: init.headers?.Authorization || "" });
    return response({ sessions: [remoteRow] });
  });
  mountSessionFetch(shell.api.frameOf.get("remote-error"), async () => {
    remoteErrorCalls.push(1);
    return response({ sessions: [remoteErrorRow] });
  });
  await shell.api.refreshSessions();
  const [local, remote, remoteError] = shell.api.tabs();
  assert.equal(shell.api.labelOf(local), "Local chat");
  assert.equal(shell.api.labelOf(remote), "Remote chat");
  assert.equal(shell.api.stateOf(local), "busy");
  assert.equal(shell.api.stateOf(remote), "question");
  assert.equal(shell.api.labelOf(remoteError), "Remote error");
  assert.equal(shell.api.stateOf(remoteError), "error");
  assert.equal(remoteCalls.length, 1, "the remote iframe's authenticated fetch was not used");
  assert.match(remoteCalls[0].input, /^\/coddy\/sessions\?/);
  assert.equal(remoteCalls[0].authorization, "", "the shell supplied a credential instead of using the iframe shim");
  assert.equal(localCalls.length, 1);
  assert.equal(remoteErrorCalls.length, 1);
});

test("an unknown iframe environment and a failed read preserve state and local busy", async () => {
  const localRow = { id: "sess_same", title: "Wrong local chat", turnActive: false };
  const shell = makeShell({
    storedTabs: [
      { uid: "unknown", session: "sess_same", route: "#/s/sess_same", workspace: "" },
      { uid: "remote", session: "sess_same", route: "#/s/sess_same", workspace: "" },
    ],
    windowFetch: desktopFetchWithSessions([localRow]),
  });
  await shell.settle();
  const unknown = shell.api.tabs()[0];
  const remote = shell.api.tabs()[1];
  shell.api.locallyBusy.set("unknown", Date.now() - 5000);
  shell.api.locallyBusy.set("remote", Date.now() - 5000);
  let fail = false;
  let calls = 0;
  mountSessionFetch(shell.api.frameOf.get("remote"), async () => {
    calls++;
    if (fail) throw new Error("remote temporarily unavailable");
    return response({ sessions: [{ id: "sess_same", title: "Remote chat", turnActive: true }] });
  });
  await shell.api.refreshSessions();
  assert.equal(shell.api.labelOf(unknown), "Chat", "an unshimmed frame borrowed the local session");
  assert.equal(shell.api.stateOf(unknown), "busy", "an unknown environment expired local busy");
  assert.equal(shell.api.labelOf(remote), "Remote chat");
  fail = true;
  await shell.api.refreshSessions();
  assert.equal(shell.api.labelOf(remote), "Remote chat", "a failed read discarded the last good title");
  assert.equal(shell.api.stateOf(remote), "busy", "a failed read discarded the last good activity");
  assert.equal(shell.api.locallyBusy.has("remote"), true, "a failed read expired local busy");
  assert.equal(calls, 2);
});

test("overlapping refreshes share the request already in flight for a scope", async () => {
  const shell = makeShell({
    storedTabs: [{ uid: "remote", session: "sess_same", route: "#/s/sess_same", workspace: "" }],
    windowFetch: desktopFetchWithSessions([]),
  });
  await shell.settle();
  const gate = deferred();
  let calls = 0;
  mountSessionFetch(shell.api.frameOf.get("remote"), async () => {
    calls++;
    await gate.promise;
    return response({ sessions: [{ id: "sess_same", title: "Remote chat", turnActive: true }] });
  });
  const first = shell.api.refreshSessions();
  const second = shell.api.refreshSessions();
  gate.resolve();
  await Promise.all([first, second]);
  assert.equal(calls, 1, "overlapping polls started duplicate requests");
});

test("many tabs of one environment and credential scope share one poll", async () => {
  const storedTabs = Array.from({ length: 10 }, (_, index) => ({
    uid: `tab-${index}`,
    session: `sess_${index}`,
    route: `#/s/sess_${index}`,
    workspace: "",
  }));
  const sessions = Array.from({ length: 300 }, (_, index) => ({
    id: `sess_${index}`,
    title: `Chat ${index}`,
    turnActive: index % 2 === 0,
  }));
  const shell = makeShell({ storedTabs, windowFetch: desktopFetchWithSessions([]) });
  await shell.settle();
  let calls = 0;
  for (const tab of shell.api.tabs()) {
    mountSessionFetch(shell.api.frameOf.get(tab.uid), async () => {
      calls++;
      return response({ sessions });
    }, "shared-address-and-token-fingerprint");
  }
  await shell.api.refreshSessions();
  assert.equal(calls, 1, "one scope issued one request per tab");
  for (const tab of shell.api.tabs()) {
    assert.equal(shell.api.labelOf(tab), `Chat ${tab.session.slice(5)}`);
  }
  await shell.api.refreshSessions();
  assert.equal(calls, 2, "the next cycle did not reuse the whole response instead of polling once");
});

test("a scope change during a request rejects the old environment's answer", async () => {
  const shell = makeShell({
    storedTabs: [{ uid: "tab", session: "sess_same", route: "#/s/sess_same", workspace: "" }],
    windowFetch: desktopFetchWithSessions([]),
  });
  await shell.settle();
  const frame = shell.api.frameOf.get("tab");
  const oldAnswer = deferred();
  let scope = "scope-a";
  let oldCalls = 0;
  mountSessionFetch(frame, async () => {
    oldCalls++;
    if (oldCalls === 1) return response({ sessions: [{ id: "sess_same", title: "Old A" }] });
    await oldAnswer.promise;
    return response({ sessions: [{ id: "sess_same", title: "Stale A" }] });
  }, () => scope);
  await shell.api.refreshSessions();
  assert.equal(shell.api.labelOf(shell.api.tabs()[0]), "Old A");
  const oldPoll = shell.api.refreshSessions();
  scope = "scope-b";
  mountSessionFetch(frame, async () => response({ sessions: [{ id: "sess_same", title: "New B" }] }), () => scope);
  const newPoll = shell.api.refreshSessions();
  oldAnswer.resolve();
  await Promise.all([oldPoll, newPoll]);
  assert.equal(shell.api.labelOf(shell.api.tabs()[0]), "New B", "the old scope overwrote the new environment");
});

test("a failed old scope does not undo a successful poll of the new scope", async () => {
  const shell = makeShell({
    storedTabs: [{ uid: "tab", session: "sess_same", route: "#/s/sess_same", workspace: "" }],
    windowFetch: desktopFetchWithSessions([]),
  });
  await shell.settle();
  const frame = shell.api.frameOf.get("tab");
  const oldAnswer = deferred();
  let scope = "scope-a";
  mountSessionFetch(frame, async () => {
    await oldAnswer.promise;
    throw new Error("old scope unavailable");
  }, () => scope);
  shell.api.locallyBusy.set("tab", Date.now() - 5000);
  const oldPoll = shell.api.refreshSessions();
  scope = "scope-b";
  mountSessionFetch(frame, async () => response({ sessions: [{ id: "sess_same", title: "New B", turnActive: true }] }), () => scope);
  const newPoll = shell.api.refreshSessions();
  oldAnswer.resolve();
  await Promise.all([oldPoll, newPoll]);
  assert.equal(shell.api.labelOf(shell.api.tabs()[0]), "New B");
  assert.equal(shell.api.stateOf(shell.api.tabs()[0]), "busy");
  assert.equal(shell.api.locallyBusy.has("tab"), true);
});


test("bar padding and empty strip drag, interactive controls do not", async () => {
  for (const system of [false, true]) for (const targetName of ["coddy-desktop-tabs", "strip", "drag", "tab", "new-tab", "menu-btn", "reload-tab", "copy-page-link"]) {
    const actions = [];
    const shell = makeShell({windowFetch:async (url, init={}) => {
      if (url === "/desktop/keys") return new Promise(()=>{});
      if (url === "/desktop/window" && init.method === "POST") actions.push(JSON.parse(init.body).action);
      return response(url === "/desktop/window" ? {supported:true,...(system ? {frame:"system",left_inset:83} : {})} : {});
    }});
    await shell.settle();
    const header=shell.document.ids.get("coddy-desktop-tabs");
    const target=targetName === "tab" ? shell.document.ids.get("strip").children[0] : shell.document.ids.get(targetName);
    const zone=["strip","drag"].includes(targetName) ? target : header;
    const down=pointer(zone,100,100); down.target=target;
    zone.dispatch("pointerdown",down);
    zone.dispatch("pointermove",pointer(zone,120,110));
    await shell.settle(); shell.flushFrame(); await shell.settle();
    zone.dispatch("pointerup",pointer(zone,120,110));
    await shell.settle();
    const expected=["strip","drag"].includes(targetName) || (system && targetName === "coddy-desktop-tabs");
    assert.equal(actions.includes("move-begin"),expected,`drag start for ${targetName}`);
    assert.equal(actions.includes("move-by"),expected,`drag movement for ${targetName}`);
  }
});

test("system chrome uses its measured inset and no custom controls", async () => {
  const shell=makeShell({windowFetch:async (url) => {
    if(url === "/desktop/keys") return new Promise(()=>{});
    return response(url === "/desktop/window" ? {supported:true,frame:"system",left_inset:83} : {});
  }});
  await shell.settle();
  assert.equal(shell.document.documentElement.dataset.frame,"system");
  assert.equal(shell.document.documentElement.style.getPropertyValue("--window-left-inset"),"83px");
  assert.equal(shell.document.ids.get("window-btns").hidden,true);
  assert.equal(shell.document.ids.get("resize-edges").hidden,true);
});


test("system frame started framed retains measured room when unified again", async () => {
 let framed=true;
 const shell=makeShell({windowFetch:async (url,init={})=>{
  if(url === "/desktop/keys") return new Promise(()=>{});
  if(url === "/desktop/window") return response({supported:true,frame:"system",left_inset:83});
  if(url === "/desktop/settings") {if(init.method === "POST") framed=JSON.parse(init.body).framed; return response({framed})}
  return response({});
 }});
 await shell.settle();
 assert.equal(shell.document.documentElement.dataset.framed,"true");
 await shell.api.setSetting("framed",false);
 assert.equal(shell.document.documentElement.dataset.framed,"false");
 assert.equal(shell.document.documentElement.style.getPropertyValue("--window-left-inset"),"83px");
});


test("controls without system chrome leave the original root DOM unchanged", async () => {
 for (const supported of [false,true]) {
  const shell=makeShell({windowFetch:async (url)=>{
   if(url === "/desktop/keys") return new Promise(()=>{});
   return response(url === "/desktop/window" ? {supported} : {});
  }});
  await shell.settle();
  const root=shell.document.documentElement;
  assert.equal(Object.hasOwn(root.dataset,"frame"),false);
  assert.equal(Object.hasOwn(root.dataset,"framed"),false);
  assert.equal(root.style.getPropertyValue("--window-left-inset"),"");
 }
});


test("page actions follow the selected tab and its live route, not its initial src", async () => {
  const shell = makeShell({ storedTabs: [
    { uid: "a", route: "#/s/first" }, { uid: "b", route: "#/s/second" },
  ] });
  const writes = [], reloads = [];
  shell.context.navigator = { clipboard: { writeText: async (url) => writes.push(url) } };
  const strip = shell.document.ids.get("strip");
  for (const [i, uid] of ["a", "b"].entries()) {
    const frame = shell.api.frameOf.get(uid);
    const live = `http://desktop.test/?lang=en#/s/${uid}?view=files`;
    frame.contentWindow.location.href = live.replace("?lang", "?desktop_workspace=%2Fprivate&desktop_workspace_pending=1&lang");
    frame.contentWindow.location.reload = () => reloads.push(uid);
    strip.children[i].dispatch("click");
    shell.document.ids.get("reload-tab").dispatch("click");
    shell.document.ids.get("copy-page-link").dispatch("click");
    await shell.settle();
    assert.equal(writes[i], live);
    assert.equal(shell.document.ids.get("copy-status").textContent, "Link copied");
  }
  assert.deepEqual(reloads, ["a", "b"]);
});

test("copy reports failure without substituting the shell URL or overwriting the clipboard", async () => {
  const shell = makeShell();
  const frame = [...shell.api.frameOf.values()][0];
  const writes = [];
  shell.context.navigator = { clipboard: { writeText(url) { writes.push(url); return Promise.reject(new Error("denied")); } } };
  for (const href of ["about:blank", "https://elsewhere.test/", "http://desktop.test/#/settings"]) {
    frame.contentWindow.location.href = href;
    shell.document.ids.get("copy-page-link").dispatch("click");
    await shell.settle();
    assert.equal(shell.document.ids.get("copy-status").textContent, "Could not copy link");
  }
  assert.deepEqual(writes, ["http://desktop.test/#/settings"]);
});
