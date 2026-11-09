/*
 * The tab strip of the desktop window, which is also its title bar.
 *
 * Every tab is an iframe of the web UI of this same server, and a hidden tab
 * stays loaded, which is the whole reason tabs exist here: the chat behind one
 * keeps its transcript, its scroll position and its event stream while another
 * is in front. The streams do not multiply with the frames - the SPA holds one
 * GET /coddy/events for the whole origin through a SharedWorker, electing a
 * holder through Web Locks where there is none - but the memory does, so a
 * window with many tabs costs about as much as that many browser tabs.
 *
 * The frames are same-origin, which is what lets this page read a frame's hash
 * to learn which session it ended up on, and attach its key handler inside
 * each frame. Neither is cooperation from the SPA: the SPA is not changed.
 *
 * With no system frame the window has no title bar and no buttons of its own,
 * so this bar carries them. None of it is a web API: a page cannot minimise,
 * maximise or move the window it is drawn in, so the bar asks the process that
 * started the browser (POST /desktop/window), which asks the window manager.
 */
(() => {
  "use strict";

  const STORE_KEY = "coddy.desktop.tabs.v1";
  // How often the strip asks what its chats are doing. The dot on a tab is
  // only as live as this, so a window someone is looking at asks every couple
  // of seconds; one nobody is looking at asks rarely, since the answer is only
  // drawn when the window comes back.
  const SESSIONS_TICK_MS = 2000;
  const SESSIONS_HIDDEN_MS = 15000;
  // The colours are read off the chat on a slower beat: a theme changes when
  // somebody changes it in Settings, not between two turns.
  const THEME_REFRESH_MS = 10000;
  const TITLE_SETTLE_MS = 1500;
  // How often the strip reads the address of each frame. It is quick because
  // this is what names a tab the moment its chat gets an id, and it costs one
  // property read per tab on a same-origin frame.
  const FRAME_ROUTE_TICK_MS = 500;
  // How long to wait before asking for the next chord again after a poll that
  // could not be answered at all: a window with no launcher behind it must not
  // spin on a request that will keep failing.
  const CHORD_RETRY_MS = 2000;
  const DRAG_THRESHOLD_PX = 4;

  const strip = document.getElementById("strip");
  const frames = document.getElementById("coddy-desktop-frames");
  const root = document.documentElement;
  const newTabButton = document.getElementById("new-tab");
  const dragZone = document.getElementById("drag");
  const menuButton = document.getElementById("menu-btn");
  const menu = document.getElementById("menu");
  const windowButtons = document.getElementById("window-btns");
  const scrollButtons = document.getElementById("scroll-btns");
  const scrollLeft = document.getElementById("scroll-left");
  const scrollRight = document.getElementById("scroll-right");

  /** @type {{uid: string, session: string|null}[]} */
  let tabs = [];
  let activeUid = null;
  /** @type {Map<string, HTMLIFrameElement>} */
  const frameOf = new Map();
  /** The rows of GET /coddy/sessions, by session id: the title and the state. */
  const rows = new Map();
  /** Whether this window can be asked to minimise, maximise, move or close. */
  let windowDriven = false;
  /**
   * The switches of the menu, as the launcher has them. They are its own and
   * not this page's: an autostart entry, a tray icon and the system frame
   * belong to the machine, and the browser scopes its storage to the port,
   * which moves between launches.
   * @type {{framed: boolean, tray: boolean, autostart: boolean, start_minimised: boolean}}
   */
  let settings = {
    framed: false,
    tray: false,
    autostart: false,
    start_minimised: false,
  };
  /** Why a switch did not take, by its key, until it does. */
  const settingsError = Object.create(null);
  /**
   * The rows of the menu, as the launcher decides them. The same function
   * decides the tray icon's menu, so the two offer the same thing.
   * @type {{id: string, kind: string, label: string, checked?: boolean}[]}
   */
  let menuRows = [];

  const uid = () =>
    "t" + Date.now().toString(36) + Math.random().toString(36).slice(2, 7);

  // --- theme -------------------------------------------------------------

  // The colours the strip takes off the chat below it. Everything else it
  // draws is derived from them in shell.css, the way the SPA's mix-text() is,
  // so a theme this page has never heard of still reaches the bar, the tabs
  // and the state dots.
  //
  // The first four are the theme's own. The last four are the ground the chat
  // is actually drawn on, and leaving them out is what made the bar look wrong
  // in every theme but the light one: the SPA paints its page with two glows
  // over a vertical gradient (styles/_base.scss), and only in the light theme
  // does the top of that gradient happen to equal `--bg`. The bar sits exactly
  // where the top of that page would be, so it is painted with the same stack
  // and continues it rather than guessing at a flat colour.
  const THEME_VARS = [
    "--text",
    "--bg",
    "--accent",
    "--coddy-danger",
    "--coddy-canvas-gradient-top",
    "--coddy-canvas-gradient-bottom",
    "--coddy-canvas-glow-purple",
    "--coddy-canvas-glow-indigo",
  ];

  // Where each of them lands on this page.
  const THEME_TARGETS = [
    "--text",
    "--bg",
    "--accent",
    "--danger",
    "--canvas-top",
    "--canvas-bottom",
    "--canvas-glow-1",
    "--canvas-glow-2",
  ];

  // Before any chat has loaded there is no frame to read, and the SPA records
  // the reader's theme in a cookie scoped to the host rather than the port, so
  // it reaches this page whatever port the desktop ended up on. It says only
  // light or dark, which is the one distinction a first paint has to get right.
  function applyThemeFallback() {
    const match = document.cookie.match(/(?:^|;\s*)coddy_ui_theme=([^;]*)/);
    const theme = match ? decodeURIComponent(match[1]) : "";
    root.dataset.theme = /light/i.test(theme) ? "light" : "dark";
  }

  // The real colours, taken off the chat in front: the frames are same-origin,
  // so their computed style is readable, and the SPA needs no change to be
  // read. A frame still loading answers empty strings and is left alone.
  function syncTheme() {
    const frame = frameOf.get(activeUid);
    if (!frame) return;
    try {
      const doc = frame.contentDocument;
      if (!doc || !doc.documentElement) return;
      const style = frame.contentWindow.getComputedStyle(doc.documentElement);
      const values = THEME_VARS.map((name) =>
        style.getPropertyValue(name).trim(),
      );
      if (!values[0] || !values[1]) return;
      values.forEach((value, i) => {
        if (value) root.style.setProperty(THEME_TARGETS[i], value);
      });
    } catch {
      /* a frame that cannot be read keeps the fallback palette */
    }
  }

  // --- one look across every tab -------------------------------------------

  // A theme, the simplified mode and a wallpaper are chosen once and belong to
  // the window, not to the tab they were chosen in. Each tab is a document of
  // its own, though, and the SPA applies the choice to the document it is in:
  // the tab where Settings is open changes, and the others keep what they had
  // until they are loaded again. A tab opened after the change is already
  // right, since a fresh instance reads the cookie and the stored picture
  // itself - what needs carrying is the tabs that were already open.
  //
  // All of it lands on <html> and nothing else: three attributes and three
  // properties, with the rest done by CSS. So the strip watches the attributes
  // of each frame's root and copies them to the others, which is both the
  // whole of the change and no cooperation from the SPA, which is unchanged.
  const LOOK_ATTRS = ["data-theme", "data-simple", "data-wallpaper"];
  const LOOK_PROPS = ["--coddy-wallpaper", "--coddy-wallpaper-dim"];

  function lookOf(root) {
    const look = { attrs: {}, props: {}, scheme: root.style.colorScheme || "" };
    for (const name of LOOK_ATTRS) look.attrs[name] = root.getAttribute(name);
    for (const name of LOOK_PROPS)
      look.props[name] = root.style.getPropertyValue(name);
    return look;
  }

  // applyLook changes only what differs, and that is what stops the ring.
  //
  // Setting an attribute to the value it already holds still reports a change
  // to an observer, and an observer's callback arrives as a microtask - after
  // any flag the spreading side could have raised and lowered. So two frames
  // would hand the same look back and forth for ever. Touching nothing when
  // nothing differs makes the second round report nothing and the ring stop
  // after one.
  function applyLook(root, look) {
    for (const name of LOOK_ATTRS) {
      const value = look.attrs[name];
      if (value === null) {
        if (root.hasAttribute(name)) root.removeAttribute(name);
      } else if (root.getAttribute(name) !== value) {
        root.setAttribute(name, value);
      }
    }
    for (const name of LOOK_PROPS) {
      const value = look.props[name];
      if (root.style.getPropertyValue(name) === value) continue;
      if (value) root.style.setProperty(name, value);
      else root.style.removeProperty(name);
    }
    if (root.style.colorScheme !== look.scheme) {
      root.style.colorScheme = look.scheme;
    }
  }

  // spreadLook puts the look of one frame on every other.
  //
  // A wallpaper travels as the address of the picture, which the frame that
  // applied it made for the bytes it had read (URL.createObjectURL). That
  // address belongs to that document: it is readable from the others while
  // that tab is open, and goes dead when it is closed, which leaves those tabs
  // on the theme's own ground until anything applies a wallpaper again. The
  // alternative is reloading every tab, which is the one thing a tab exists
  // not to do.
  function spreadLook(from) {
    let look;
    try {
      look = lookOf(from.contentDocument.documentElement);
    } catch {
      return;
    }
    for (const frame of frameOf.values()) {
      if (frame === from) continue;
      try {
        applyLook(frame.contentDocument.documentElement, look);
      } catch {
        /* a frame still loading applies its own choice when it does */
      }
    }
    syncTheme();
  }

  // watchLook follows one frame's root. The observer belongs to the document
  // it watches and ends with it, so a frame that navigates is simply watched
  // again on its next load.
  function watchLook(frame) {
    try {
      const root = frame.contentDocument.documentElement;
      new frame.contentWindow.MutationObserver(() => spreadLook(frame)).observe(
        root,
        {
          attributes: true,
          attributeFilter: [...LOOK_ATTRS, "style"],
        },
      );
    } catch {
      /* a frame that cannot be watched still shows its chat */
    }
  }

  // --- what the window remembers -----------------------------------------

  function load() {
    try {
      const raw = localStorage.getItem(STORE_KEY);
      const parsed = raw ? JSON.parse(raw) : null;
      if (Array.isArray(parsed) && parsed.length) {
        tabs = parsed
          .filter((t) => t && typeof t === "object")
          .map((t) => ({
            uid: typeof t.uid === "string" && t.uid ? t.uid : uid(),
            session: typeof t.session === "string" ? t.session : null,
          }));
      }
    } catch {
      tabs = [];
    }
    if (!tabs.length) tabs = [{ uid: uid(), session: null }];
    activeUid = tabs[0].uid;
  }

  function save() {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify(tabs));
    } catch {
      /* a window that cannot remember its tabs still works */
    }
  }

  // --- the window --------------------------------------------------------

  async function askWindow(action, extra) {
    try {
      const res = await fetch("/desktop/window", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action, ...(extra || {}) }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  // The page is served the moment the surface is up and the browser maps its
  // window a second or two later, so one read at load time always answers
  // "no". It is asked again until there is a window, and then left alone.
  async function readWindowSupport(attempt = 0) {
    try {
      const res = await fetch("/desktop/window", {
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      if (res.ok) {
        const body = await res.json();
        windowDriven = body && body.supported === true;
      }
    } catch {
      windowDriven = false;
    }
    syncWindowButtons();
    if (windowDriven) {
      if (!menu.hidden) renderMenu();
      syncWindowShape();
      return;
    }
    if (attempt < 40) setTimeout(() => readWindowSupport(attempt + 1), 500);
  }

  // Minimise, maximise and close belong to the bar only while the bar is the
  // title bar. A strip that drew a close nothing answers would be worse than a
  // system frame, and a strip that drew all three beside the manager's own
  // would be two sets of the same buttons.
  function syncWindowButtons() {
    windowButtons.hidden = !windowDriven || settings.framed;
  }

  // The window's own corners are cut by the launcher while it floats and left
  // square while it fills the screen; this is the page keeping to the same
  // shape, so it never paints into a corner that was cut away.
  //
  // Whether the window is maximised is read off its size rather than asked for:
  // with no frame the window is exactly as big as the page, so a window that
  // covers the screen's working area is a maximised one, and the browser
  // reports a resize for every change of it.
  function syncWindowShape() {
    if (!windowDriven) return;
    const filled =
      Math.abs(window.outerWidth - screen.availWidth) <= 2 &&
      Math.abs(window.outerHeight - screen.availHeight) <= 2;
    document.documentElement.dataset.window = filled ? "maximised" : "floating";
  }

  // Dragging the bar moves the window, and the page drives the move from the
  // first pixel to the button coming up.
  //
  // The obvious way - handing the window to the window manager's own move loop
  // with _NET_WM_MOVERESIZE, the way a title bar does - leaves the window
  // stuck to the pointer: the button was pressed inside this page, so the
  // release is delivered here and the manager's loop never sees it end. The
  // page captures the pointer instead and asks for one move per frame, which
  // stops exactly when the gesture does.
  function dragWindowFrom(e) {
    if (!windowDriven || e.button !== 0) return;
    const zone = e.currentTarget;
    const startX = e.screenX;
    const startY = e.screenY;
    let dragging = false;
    let began = false;
    // The start of the gesture, once asked for: everything that ends it waits
    // on this, so a release while it is still in flight cannot leave the
    // window thinking a drag is open.
    let begun = null;
    let pending = null;
    let frame = 0;

    const send = () => {
      frame = 0;
      // Nothing is sent until the window has answered that it knows where the
      // drag began. The two are separate requests and the browser may deliver
      // them in either order: a move that overtook the start used to be
      // measured from wherever the last drag left the window, which threw the
      // window sideways for a frame or two before the start landed and put it
      // right. The window refuses such a move; the page does not send one.
      if (!began || !pending) return;
      const [dx, dy] = pending;
      pending = null;
      return askWindow("move-by", { x: dx, y: dy });
    };

    const onMove = (m) => {
      const dx = m.screenX - startX;
      const dy = m.screenY - startY;
      if (!dragging) {
        if (
          Math.abs(dx) < DRAG_THRESHOLD_PX &&
          Math.abs(dy) < DRAG_THRESHOLD_PX
        ) {
          return;
        }
        dragging = true;
        // Where the pointer took hold, so a window the window manager has
        // pinned - maximised, or tiled against an edge - can be let loose and
        // put back under the pointer instead of jumping out from under it.
        begun = askWindow("move-begin", { x: startX, y: startY }).then((ok) => {
          began = ok;
          // The pointer has been moving while this was in flight, and the last
          // place it reached is where the window belongs.
          if (ok && pending && !frame) frame = requestAnimationFrame(send);
          return ok;
        });
      }
      // One move per frame: a pointer reports far more often than a window
      // needs to be put somewhere, and every report is a request.
      pending = [dx, dy];
      if (!frame) frame = requestAnimationFrame(send);
    };

    const stop = () => {
      if (frame) cancelAnimationFrame(frame);
      // The gesture is ended in the order it was made: after the start it was
      // waiting on, and after the last move it asked for. Three independent
      // requests would be delivered in any order the browser liked - the last
      // move answered 409 because the gesture was already closed, or a
      // move-begin landing after the end and leaving one open for good.
      if (begun) {
        begun
          .then(() => send())
          .then(() => askWindow("move-end"))
          .catch(() => {
            /* the window is gone, and so is the drag */
          });
      }
      zone.removeEventListener("pointermove", onMove);
      zone.removeEventListener("pointerup", stop);
      zone.removeEventListener("pointercancel", stop);
      try {
        zone.releasePointerCapture(e.pointerId);
      } catch {
        /* the capture may already be gone */
      }
    };

    try {
      // With the pointer captured every move and the release come here, even
      // when the window slides out from under the pointer.
      zone.setPointerCapture(e.pointerId);
    } catch {
      /* without capture the listeners below still see most of the gesture */
    }
    zone.addEventListener("pointermove", onMove);
    zone.addEventListener("pointerup", stop);
    zone.addEventListener("pointercancel", stop);
  }

  // A link out of Coddy opens in this machine's own browser.
  //
  // Clicked inside a frame, a link to another site loads that site into the
  // frame: a page from the internet under a tab strip, with no address bar,
  // no back button and no way out but closing the tab. It is handed to the
  // desktop's own opener instead, which is where a person expects a link to
  // go. Coddy's own addresses are not links out and stay in the window.
  function watchLinks(doc) {
    doc.addEventListener(
      "click",
      (e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey) return;
        // Not `e.target instanceof Element`: the target belongs to the
        // frame's document and this script runs in the strip's, and every
        // document has constructors of its own, so the check is false for
        // every element of every chat. What is asked instead is whether the
        // node can answer the question.
        const target = e.target;
        const anchor =
          target && typeof target.closest === "function"
            ? target.closest("a[href]")
            : null;
        if (!anchor) return;
        let url;
        try {
          url = new URL(anchor.getAttribute("href") || "", doc.baseURI);
        } catch {
          return;
        }
        if (url.protocol !== "http:" && url.protocol !== "https:") return;
        if (url.origin === location.origin) return;
        e.preventDefault();
        openOutside(url.href);
      },
      true,
    );
  }

  async function openOutside(url) {
    try {
      await fetch("/desktop/open", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url }),
      });
    } catch {
      /* nothing opens, and the window is where it was */
    }
  }

  // --- frames -------------------------------------------------------------

  const addressOf = (tab) => (tab.session ? "/#/s/" + tab.session : "/");

  function ensureFrame(tab) {
    let frame = frameOf.get(tab.uid);
    if (frame) return frame;
    frame = document.createElement("iframe");
    frame.src = addressOf(tab);
    frame.title = "Chat";
    frame.addEventListener("load", () => onFrameLoad(tab.uid, frame));
    frameOf.set(tab.uid, frame);
    frames.appendChild(frame);
    return frame;
  }

  // A frame tells this page which session it is on through its own address,
  // which is cheaper and more reliable than asking the chat: the SPA writes
  // the session into the hash as soon as a new conversation has one.
  //
  // Three answers, not two. An id is the chat behind the tab. The empty string
  // is the new-chat route, where there is no chat yet and the tab has to say
  // so - a tab that kept the name of the chat somebody has just navigated away
  // from is the bug this distinction exists for. And null is every address
  // that says nothing about a chat at all - Settings, the scheduler, the
  // documentation - where the tab keeps the name it had.
  function sessionOfFrame(frame) {
    try {
      const hash = frame.contentWindow.location.hash || "";
      const m = hash.match(/^#\/s\/([A-Za-z0-9_-]+)/);
      if (m) return m[1];
      if (hash === "" || hash === "#" || hash === "#/") return "";
      return null;
    } catch {
      return null;
    }
  }

  // Take the chat every frame has moved to, and answer whether any of them
  // moved at all.
  //
  // The hashchange event is not enough on its own, and the reason is in the
  // SPA: a brand-new conversation is given its id with history.replaceState,
  // which fires no event, so a tab would stay called "New chat" after the chat
  // in it had a name. The frames are same-origin, so their addresses are read
  // on a beat as well - one property read per tab, and it catches every way of
  // navigating, event or no event.
  function syncFrameSessions() {
    let moved = false;
    for (const tab of tabs) {
      const frame = frameOf.get(tab.uid);
      if (!frame) continue;
      const session = sessionOfFrame(frame);
      if (session === null || session === (tab.session || "")) continue;
      tab.session = session;
      moved = true;
    }
    if (!moved) return false;
    save();
    renderStrip();
    // A chat is named after its first message, and that message is being saved
    // as this page learns the session exists, so the first read can
    // legitimately come back without a title. One more read shortly after is
    // the difference between a tab called "Chat" for ten seconds and a tab
    // that is named as soon as there is a name.
    refreshSessions();
    setTimeout(refreshSessions, TITLE_SETTLE_MS);
    return true;
  }

  function onFrameLoad(tabUid, frame) {
    const sync = () => {
      if (!tabs.some((t) => t.uid === tabUid)) return;
      if (!syncFrameSessions()) renderStrip();
    };
    try {
      frame.contentWindow.addEventListener("hashchange", sync);
      // A key pressed inside a frame is delivered to that frame's document and
      // never reaches this one, so the handler is attached there as well. It
      // is allowed because the frame is same-origin, and it has to happen on
      // every load because a navigation replaces the document.
      frame.contentDocument.addEventListener("keydown", onKeyDown);
      // A click in the chat closes the menu the way a click on the bar does.
      // It needs saying twice because a pointer event inside a frame is
      // delivered to that frame's document and stops there.
      frame.contentDocument.addEventListener("pointerdown", closeMenu);
      // A link to another site goes to this machine's own browser rather than
      // into the frame, and that is said per document for the same reason.
      watchLinks(frame.contentDocument);
      // A theme, the simplified mode or a wallpaper chosen in this chat belongs
      // to the window, so the choice is carried to every other tab (spreadLook)
      // rather than left in the document it was made in.
      watchLook(frame);
      // The chat that has just loaded is where the strip's own colours come
      // from, so the bar follows the theme of the window's first chat.
      syncTheme();
    } catch {
      /* a frame that will not be read from still shows its chat */
    }
    sync();
  }

  // --- the strip ----------------------------------------------------------

  function labelOf(tab) {
    if (!tab.session) return "New chat";
    const row = rows.get(tab.session);
    return (row && row.title) || "Chat";
  }

  // The state of the chat behind a tab, read off the session list exactly as
  // the History list reads it (sessions/sessionRowActivity.ts), including the
  // order the marks come in: work first, then a prompt waiting for a person,
  // then a failure, then an answer nobody has seen. The tab in front is the
  // one being read, so it carries neither an unread mark nor an unseen failure.
  const STATES = {
    busy: "Working",
    waiting: "Waiting for you",
    error: "The last turn failed",
    "error-seen": "The last turn failed",
    unread: "Finished, not read yet",
    idle: "Finished",
  };

  function stateOf(tab) {
    const row = tab.session ? rows.get(tab.session) : null;
    if (!row) return "idle";
    if (row.permissionPending || row.questionPending) return "waiting";
    if (row.turnActive || (row.backgroundRunning || 0) > 0) return "busy";
    const inFront = tab.uid === activeUid;
    const failed = row.lastErrorSeq || 0;
    if (failed > 0) {
      return !inFront && failed > (row.readActivitySeq || 0)
        ? "error"
        : "error-seen";
    }
    if (!inFront && row.unreadComplete) return "unread";
    return "idle";
  }

  function stateDot(tab) {
    const state = stateOf(tab);
    const span = document.createElement("span");
    span.className = "dot is-" + state;
    span.setAttribute("role", "img");
    span.setAttribute("aria-label", STATES[state]);
    span.title = STATES[state];
    return span;
  }

  function renderStrip() {
    strip.textContent = "";
    tabs.forEach((tab, index) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "tab";
      button.setAttribute("role", "tab");
      button.setAttribute("aria-selected", String(tab.uid === activeUid));
      button.appendChild(stateDot(tab));

      const label = document.createElement("span");
      label.className = "label";
      // A title is operator and model text drawn into a credentialed
      // same-origin document: it is written, never parsed.
      label.textContent = labelOf(tab);
      button.title =
        label.textContent + (index < 9 ? ` (Ctrl+${index + 1})` : "");
      button.appendChild(label);

      const close = document.createElement("button");
      close.type = "button";
      close.className = "close";
      close.textContent = "×";
      close.title = "Close this chat (Ctrl+W)";
      close.setAttribute("aria-label", "Close tab");
      close.addEventListener("click", (e) => {
        e.stopPropagation();
        closeTab(tab.uid);
      });
      button.appendChild(close);

      button.addEventListener("click", () => activate(tab.uid));
      button.addEventListener("auxclick", (e) => {
        if (e.button === 1) closeTab(tab.uid);
      });
      strip.appendChild(button);
    });
    syncScrollButtons();
  }

  function renderFrames() {
    tabs.forEach((tab) => {
      const frame = ensureFrame(tab);
      frame.hidden = tab.uid !== activeUid;
    });
    for (const [tabUid, frame] of frameOf) {
      if (!tabs.some((t) => t.uid === tabUid)) {
        frame.remove();
        frameOf.delete(tabUid);
      }
    }
  }

  function render() {
    renderStrip();
    renderFrames();
  }

  // --- walking a row too long for the bar ----------------------------------

  // With more chats than fit, the strip scrolls, and two arrows beside the
  // window's own buttons walk it. They are there only while there is somewhere
  // to walk to, and each one goes dim at its end of the row rather than
  // disappearing, so the buttons next to it do not move under the pointer.
  function syncScrollButtons() {
    const over = strip.scrollWidth - strip.clientWidth;
    scrollButtons.hidden = over <= 1;
    if (scrollButtons.hidden) return;
    const at = strip.scrollLeft;
    scrollLeft.disabled = at <= 1;
    scrollRight.disabled = at >= over - 1;
  }

  function scrollStrip(by) {
    // Most of a screenful, not all of it: a tab stays in sight across the
    // step, which is what tells the reader where the row went.
    strip.scrollBy({
      left: by * Math.max(120, strip.clientWidth * 0.7),
      behavior: "smooth",
    });
  }

  // The tab in front is brought into sight, which is what makes Ctrl+Tab
  // usable once the row is longer than the bar.
  function revealActiveTab() {
    const tab = strip.querySelector('.tab[aria-selected="true"]');
    if (tab && typeof tab.scrollIntoView === "function") {
      tab.scrollIntoView({
        block: "nearest",
        inline: "nearest",
        behavior: "smooth",
      });
    }
  }

  // --- actions ------------------------------------------------------------

  function activate(tabUid) {
    if (!tabs.some((t) => t.uid === tabUid)) return;
    activeUid = tabUid;
    render();
    revealActiveTab();
    syncTheme();
    const frame = frameOf.get(tabUid);
    if (frame) {
      try {
        frame.contentWindow.focus();
      } catch {
        /* focus is a courtesy */
      }
    }
  }

  // step walks the row by one, wrapping at both ends, which is what Ctrl+Tab
  // does in every tabbed application. One tab is not a cycle of one: it stays
  // where it is.
  function step(by) {
    if (tabs.length < 2) return;
    const at = tabs.findIndex((t) => t.uid === activeUid);
    if (at < 0) return;
    const next = (at + by + tabs.length) % tabs.length;
    activate(tabs[next].uid);
  }

  function openTab(cwd) {
    if (cwd) rememberWorkspace(cwd);
    const tab = { uid: uid(), session: null };
    tabs.push(tab);
    activeUid = tab.uid;
    save();
    render();
  }

  // Where the next new chat starts, for a tab asked for with a folder: a row
  // of the task bar's menu naming a recent workspace.
  //
  // It is written into the cookie the web interface itself keeps the last
  // picked folder in (chat/workspaceCookies.ts), because that is what the
  // start screen of a fresh chat reads, and the frames are this page's own
  // origin. The shape is the interface's: a JSON object of folder by
  // environment, and a window's chats are always the local one. The browser
  // check drives the real interface, so a change to that shape fails there
  // rather than quietly opening chats in the wrong folder.
  const WORKSPACE_COOKIE = "coddy_workspace_dir";

  function rememberWorkspace(cwd) {
    let dirs = {};
    const raw = readCookie(WORKSPACE_COOKIE);
    if (raw) {
      try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
          dirs = parsed;
        }
      } catch {
        /* an unreadable cookie is replaced by this one */
      }
    }
    dirs.local = cwd;
    const year = 365 * 24 * 60 * 60;
    document.cookie =
      WORKSPACE_COOKIE +
      "=" +
      encodeURIComponent(JSON.stringify(dirs)) +
      "; Path=/; Max-Age=" +
      year +
      "; SameSite=Lax";
  }

  function readCookie(name) {
    for (const part of document.cookie.split(";")) {
      const s = part.trim();
      if (s.startsWith(name + "=")) {
        try {
          return decodeURIComponent(s.slice(name.length + 1));
        } catch {
          return null;
        }
      }
    }
    return null;
  }

  function closeTab(tabUid) {
    const index = tabs.findIndex((t) => t.uid === tabUid);
    if (index < 0) return;
    tabs.splice(index, 1);
    // The window keeps a tab: closing the last chat would leave a blank frame
    // with no way back. Closing a tab never closes the window around it.
    if (!tabs.length) tabs.push({ uid: uid(), session: null });
    if (activeUid === tabUid) {
      activeUid = tabs[Math.min(index, tabs.length - 1)].uid;
    }
    save();
    render();
  }

  // --- titles -------------------------------------------------------------

  // What a tab draws - its name and the state of the chat behind it - comes
  // from the session list rather than from the frames: the page already has
  // the browser's credential for this origin, one request answers for every
  // tab, and the list is the same source the History list reads.
  async function refreshSessions() {
    try {
      // include_activity is what carries turnActive, the pending prompts and
      // the unread marks; without it every row comes back looking finished and
      // the dot on a tab never moves (the History list asks the same way).
      const res = await fetch(
        "/coddy/sessions?limit=200&include_activity=true",
        {
          credentials: "same-origin",
          headers: { Accept: "application/json" },
        },
      );
      if (!res.ok) return;
      const body = await res.json();
      const list = Array.isArray(body && body.sessions) ? body.sessions : [];
      rows.clear();
      for (const row of list) {
        if (row && typeof row.id === "string") rows.set(row.id, row);
      }
      renderStrip();
    } catch {
      /* the strip falls back to "Chat" and a finished mark */
    }
  }

  // --- the switches of the menu -------------------------------------------

  // They live with the launcher, not with the page: an entry in the session's
  // autostart folder, an icon in the tray and the window's frame outlive this
  // document, and only the process that started the browser can act on them.
  async function readSettings() {
    try {
      const res = await fetch("/desktop/settings", {
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      if (!res.ok) return;
      const body = await res.json();
      if (body && typeof body === "object") settings = { ...settings, ...body };
    } catch {
      /* the menu then draws the defaults, and a switch will say why */
    }
    syncWindowButtons();
    // The rows carry the ticks, so they are read again with the settings: the
    // launcher says a switch moved when it was the tray icon's menu that
    // moved it, and this is what redraws the menu in the bar.
    await refreshMenu();
  }

  // A switch moves only when the launcher says it did: the tray needs a tray
  // to put an icon in, the autostart entry needs a folder it can write, and a
  // tick that moved on its own would be a lie about the machine. What went
  // wrong stays on the row until the switch takes.
  async function setSetting(key, value) {
    delete settingsError[key];
    try {
      const res = await fetch("/desktop/settings", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ [key]: value }),
      });
      if (res.ok) {
        const body = await res.json();
        if (body && typeof body === "object")
          settings = { ...settings, ...body };
      } else {
        settingsError[key] = (await res.text()).trim() || "this did not take";
      }
    } catch {
      settingsError[key] = "the window did not answer";
    }
    syncWindowButtons();
    await refreshMenu();
  }

  // --- the chords the browser answers itself -------------------------------

  // Ctrl+T and Ctrl+W never arrive as key events here: a Chromium window
  // opened with --app answers both itself - a new browser window, and closing
  // the application - and a page cannot cancel either. The launcher takes them
  // at the X server and hands them over here, one long poll at a time. Every
  // other chord is read off the keyboard in onKeyDown.
  async function watchChords() {
    for (;;) {
      let chord = "";
      let cwd = "";
      try {
        const res = await fetch("/desktop/keys", {
          credentials: "same-origin",
          headers: { Accept: "application/json" },
        });
        if (!res.ok) {
          await sleep(CHORD_RETRY_MS);
          continue;
        }
        const body = await res.json();
        chord = body && typeof body.chord === "string" ? body.chord : "";
        cwd = body && typeof body.cwd === "string" ? body.cwd : "";
      } catch {
        await sleep(CHORD_RETRY_MS);
        continue;
      }
      if (chord === "new-tab") openTab(cwd);
      else if (chord === "close-tab") closeTab(activeUid);
      else if (chord === "next-tab") step(1);
      else if (chord === "prev-tab") step(-1);
      else if (chord === "reload-tab") reloadActiveFrame();
      else if (chord === "settings-moved") readSettings();
    }
  }

  // Reload the chat in front, and only it.
  //
  // The browser's own reload would reload this document, which is the strip:
  // every tab's frame would go back to its chat at once and the strip would be
  // rebuilt around them. A tab exists so that one chat can be worked on
  // without the others moving, so the key reloads the frame of the tab in
  // front and leaves the rest alone.
  function reloadActiveFrame() {
    const frame = frameOf.get(activeUid);
    if (!frame) return;
    try {
      frame.contentWindow.location.reload();
    } catch {
      // A frame that will not be reached from here is reloaded by being
      // pointed at its own address again.
      frame.src = frame.src;
    }
  }

  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  // --- the desktop menu ---------------------------------------------------

  // An action: it does something and the menu closes behind it.
  function menuAction(mark, text, onPick) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "row";
    row.setAttribute("role", "menuitem");
    const m = document.createElement("span");
    m.className = "mark";
    m.textContent = mark;
    row.appendChild(m);
    row.appendChild(document.createTextNode(text));
    row.addEventListener("click", () => {
      closeMenu();
      onPick();
    });
    return row;
  }

  // A switch: it stays where it is, so the menu stays open and the tick moves
  // once the launcher says the change actually happened.
  //
  // The tick is the launcher's answer (row.checked), not a value this page
  // remembers: the same switch is drawn in the tray icon's menu, which the
  // page cannot see, and a tick drawn from a copy taken when the window
  // opened went on saying what the machine was doing an hour ago.
  function menuSwitch(key, text, checked) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "row";
    row.setAttribute("role", "menuitemcheckbox");
    row.setAttribute("aria-checked", String(checked === true));
    const m = document.createElement("span");
    m.className = "mark";
    m.textContent = checked ? "✓" : "";
    row.appendChild(m);
    row.appendChild(document.createTextNode(text));
    if (settingsError[key]) {
      row.classList.add("failed");
      row.title = settingsError[key];
    }
    row.addEventListener("click", () => setSetting(key, !checked));
    return row;
  }

  function menuSeparator() {
    const line = document.createElement("div");
    line.className = "sep";
    line.setAttribute("role", "separator");
    return line;
  }

  // What an action row does. The bar's menu has one, New chat; the tray's menu
  // has the two that show and hide the window, which only it can offer.
  function pickMenuAction(id) {
    if (id === "new-chat") openTab();
  }

  // The rows come from the launcher rather than from a list written here,
  // because the tray icon draws the same rows from the same function on the Go
  // side, and the two menus have to offer the same thing.
  async function readMenuRows() {
    try {
      const res = await fetch("/desktop/menu", {
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      if (!res.ok) return;
      const body = await res.json();
      if (body && Array.isArray(body.items)) menuRows = body.items;
    } catch {
      /* the menu then draws the rows it last had, or nothing at all */
    }
  }

  function renderMenu() {
    menu.textContent = "";
    for (const row of menuRows) {
      if (!row || typeof row.id !== "string") continue;
      if (row.kind === "separator") {
        menu.appendChild(menuSeparator());
      } else if (row.kind === "switch") {
        menu.appendChild(menuSwitch(row.id, row.label, row.checked === true));
      } else {
        const mark = row.id === "new-chat" ? "+" : "";
        menu.appendChild(
          menuAction(mark, row.label, () => pickMenuAction(row.id)),
        );
      }
    }
  }

  // refreshMenu takes the rows again and redraws an open menu from them.
  async function refreshMenu() {
    await readMenuRows();
    if (!menu.hidden) renderMenu();
  }

  function openMenu() {
    renderMenu();
    menu.hidden = false;
    menuButton.setAttribute("aria-expanded", "true");
    // Read again on every open: the rows depend on what the window can be
    // asked, and the window appears a moment after this page does; the ticks
    // may have moved in the tray icon's menu since this one was last drawn.
    refreshMenu();
    // The listener is added on the next tick so that the click which opened
    // the menu does not immediately close it again.
    setTimeout(() => document.addEventListener("pointerdown", onAway), 0);
  }

  function closeMenu() {
    if (menu.hidden) return;
    menu.hidden = true;
    menuClosedAt = Date.now();
    menuButton.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", onAway);
  }

  // A pointer going down anywhere but inside the menu closes it: the bar, a
  // tab, another application, the button that opened it, and the chat below,
  // whose events land in a frame and never reach this document - that one is
  // said again on each frame.
  //
  // The button is deliberately not an exception here. A click is a pointer
  // going down and then a click event, so the menu is already closed by the
  // time the button is told about the click; what keeps it closed is the
  // guard below, and not an attempt to recognise the button in this handler -
  // which is harder than it looks, since the target of a click on it is the
  // path of the icon drawn inside it.
  function onAway(e) {
    const inside = e.target instanceof Element && e.target.closest("#menu");
    if (!inside) closeMenu();
  }

  // When the menu was last closed, so the click that closed it does not open
  // it again on its way up.
  let menuClosedAt = 0;
  const MENU_REOPEN_GUARD_MS = 300;

  function toggleMenu() {
    if (!menu.hidden) {
      closeMenu();
      return;
    }
    if (Date.now() - menuClosedAt < MENU_REOPEN_GUARD_MS) return;
    openMenu();
  }

  // --- keys ---------------------------------------------------------------

  // Ctrl is what a person expects, Alt is bound beside it, and on a Mac the
  // Command key stands with them.
  //
  // A Chromium window opened with --app has no tab strip of its own, so it
  // hands Ctrl+T, Ctrl+W and Ctrl+1..9 to the page instead of acting on them
  // (checked against Chrome 157: Ctrl+W there closes a tab of this strip and
  // leaves the application running, which it would not if the browser had kept
  // the key). Every one of them is cancelled, so the browser cannot act on it
  // afterwards, and Alt stands beside Ctrl for a browser that does keep it.
  // On Linux the launcher also takes Ctrl+T, Ctrl+W, Ctrl+R and Ctrl+Tab at
  // the X server (desktop/chords.go) and hands them back through /desktop/keys.
  //
  // The key is read off e.code, the physical key, and never off e.key: with a
  // Russian layout active Ctrl+T reports "т", and on a Mac Alt+T reports "†",
  // so a strip that matched the character answered neither.
  const chordModifier = (e) => e.ctrlKey || e.altKey || e.metaKey;

  function onKeyDown(e) {
    if (e.key === "Escape" && !menu.hidden) {
      closeMenu();
      return;
    }
    // Tab walks the row, and it is the one chord that takes Shift: forward
    // with Ctrl, back with Ctrl+Shift, the way every tabbed application does
    // it. It is handled before the guard below, which turns Shift away.
    if (e.code === "Tab" && chordModifier(e)) {
      e.preventDefault();
      step(e.shiftKey ? -1 : 1);
      return;
    }
    if (!chordModifier(e) || e.shiftKey) return;
    if (e.code === "KeyT") {
      e.preventDefault();
      openTab();
      return;
    }
    if (e.code === "KeyW") {
      e.preventDefault();
      closeTab(activeUid);
      return;
    }
    if (e.code === "KeyR") {
      e.preventDefault();
      reloadActiveFrame();
      return;
    }
    const digit = /^Digit([1-9])$/.exec(e.code);
    if (digit) {
      const index = Number(digit[1]) - 1;
      if (index < tabs.length) {
        e.preventDefault();
        activate(tabs[index].uid);
      }
    }
  }

  // --- start --------------------------------------------------------------

  applyThemeFallback();
  load();
  render();
  refreshSessions();
  readWindowSupport();
  readSettings();
  readMenuRows();
  watchChords();
  let lastSessions = Date.now();
  setInterval(() => {
    // A hidden window draws nothing, so it asks about its chats at a trickle
    // rather than every tick.
    const due =
      document.visibilityState === "visible" ||
      Date.now() - lastSessions >= SESSIONS_HIDDEN_MS;
    if (!due) return;
    lastSessions = Date.now();
    refreshSessions();
  }, SESSIONS_TICK_MS);
  // The frames are followed on a beat of their own, and a quicker one: a chat
  // started inside a tab is given its id without an event anybody can listen
  // for, and this is what puts its name on the tab.
  setInterval(syncFrameSessions, FRAME_ROUTE_TICK_MS);
  // The strip takes the chat's own colours again on its own beat, so a theme
  // changed in Settings reaches the bar without a reload.
  setInterval(syncTheme, THEME_REFRESH_MS);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") refreshSessions();
  });
  // A click that lands in another application, or in a chat that takes the
  // focus with it, closes the menu as well.
  window.addEventListener("blur", closeMenu);

  newTabButton.addEventListener("click", openTab);
  scrollLeft.addEventListener("click", () => scrollStrip(-1));
  scrollRight.addEventListener("click", () => scrollStrip(1));
  strip.addEventListener("scroll", syncScrollButtons, { passive: true });
  window.addEventListener("resize", syncScrollButtons);
  window.addEventListener("resize", syncWindowShape);
  document.addEventListener("keydown", onKeyDown);

  menuButton.addEventListener("click", toggleMenu);
  document
    .getElementById("win-min")
    .addEventListener("click", () => askWindow("minimise"));
  document.getElementById("win-max").addEventListener("click", async () => {
    await askWindow("maximise");
    // The resize event follows, but the window manager takes a moment and the
    // page should not be a frame behind its own window.
    setTimeout(syncWindowShape, 120);
  });
  document
    .getElementById("win-close")
    .addEventListener("click", () => askWindow("close"));

  // The bar is the title bar: the empty part of it, and the strip's own empty
  // part when the tabs do not fill it. Dragging moves the window and a double
  // click maximises or restores it, which is what a title bar does everywhere;
  // a chat is opened by the plus or by the chord, never by a stray double
  // click on the bar.
  for (const zone of [dragZone, strip]) {
    zone.addEventListener("pointerdown", (e) => {
      if (e.target === zone) dragWindowFrom(e);
    });
    zone.addEventListener("dblclick", (e) => {
      if (e.target !== zone || !windowDriven) return;
      askWindow("maximise").then(() => setTimeout(syncWindowShape, 120));
    });
  }
})();
