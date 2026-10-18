/*
 * Coddy canvas bridge (protocol coddy-canvas/1).
 *
 * Served into every HTML page of a canvas frame. The page runs in an opaque
 * origin, so the web UI around it cannot reach into it; this script is the
 * only thing the two say to each other, over postMessage:
 *
 *   frame -> window   ready, location, loaded, console, pick, escape
 *   window -> frame   hello, mode, highlight
 *
 * The window learns nothing it did not already know from "hello"; the bridge
 * learns the window's origin from it and from then on answers only that origin
 * and window.parent. Everything the bridge sends is untrusted text for the
 * window: the page can fake or remove this script, which only spoils its own
 * picks and reports.
 */
(function () {
  "use strict";
  var PROTO = "coddy-canvas/1";
  if (window.__coddyCanvasBridge) return;
  window.__coddyCanvasBridge = true;
  // Opened in a tab of its own: there is no window to talk to.
  if (window.parent === window) return;

  var parentWin = window.parent;
  var parentOrigin = null;
  var pending = [];
  var mode = "interact";
  var MAX_TEXT = 300;
  var MAX_HTML = 2000;
  var MAX_MESSAGE = 1000;

  function post(msg) {
    msg.proto = PROTO;
    if (parentOrigin === null) {
      if (pending.length < 200) pending.push(msg);
      return;
    }
    try {
      parentWin.postMessage(msg, parentOrigin);
    } catch (e) {
      /* the window went away */
    }
  }

  function cut(s, n) {
    s = String(s == null ? "" : s);
    return s.length > n ? s.slice(0, n) : s;
  }

  // The first message carries nothing: the window answers it with "hello".
  try {
    parentWin.postMessage({ proto: PROTO, type: "ready" }, "*");
  } catch (e) {
    /* nothing to talk to */
  }

  window.addEventListener("message", function (ev) {
    if (ev.source !== parentWin) return;
    var d = ev.data;
    if (!d || d.proto !== PROTO || typeof d.type !== "string") return;
    if (d.type === "hello") {
      if (parentOrigin === null) {
        parentOrigin = ev.origin && ev.origin !== "null" ? ev.origin : "*";
        post({ type: "location", href: String(location.href) });
        if (document.readyState === "complete") {
          post({ type: "loaded", title: cut(document.title, MAX_TEXT) });
        }
        var q = pending;
        pending = [];
        for (var i = 0; i < q.length; i++) post(q[i]);
      }
      return;
    }
    if (ev.origin !== parentOrigin && parentOrigin !== "*") return;
    if (d.type === "mode") {
      setMode(d.mode === "comment" ? "comment" : "interact");
    } else if (d.type === "highlight" && typeof d.selector === "string") {
      var el = null;
      try {
        el = document.querySelector(d.selector);
      } catch (e) {
        el = null;
      }
      outline(el);
    }
  });

  window.addEventListener("load", function () {
    post({ type: "loaded", title: cut(document.title, MAX_TEXT) });
  });

  // Console: the errors and warnings the page logs and what it does not catch.
  var budget = 50;
  setInterval(function () {
    budget = 50;
  }, 1000);
  function report(level, message, source, line, column) {
    if (budget <= 0) return;
    budget--;
    post({
      type: "console",
      level: level,
      message: cut(message, MAX_MESSAGE),
      source: cut(source || "", MAX_TEXT),
      line: line || 0,
      column: column || 0,
    });
  }
  function describe(args) {
    var parts = [];
    for (var i = 0; i < args.length; i++) {
      var a = args[i];
      if (a instanceof Error) parts.push(a.name + ": " + a.message);
      else if (typeof a === "object") {
        try {
          parts.push(JSON.stringify(a));
        } catch (e) {
          parts.push(String(a));
        }
      } else parts.push(String(a));
    }
    return parts.join(" ");
  }
  ["error", "warn"].forEach(function (level) {
    var orig = console[level];
    console[level] = function () {
      report(level === "warn" ? "warning" : "error", describe(arguments));
      return orig.apply(console, arguments);
    };
  });
  window.addEventListener(
    "error",
    function (ev) {
      var t = ev.target;
      if (t && t !== window && t.tagName) {
        report(
          "error",
          "Failed to load " + (t.src || t.href || t.tagName.toLowerCase()),
        );
        return;
      }
      report(
        "error",
        ev.error && ev.error.message
          ? (ev.error.name || "Error") + ": " + ev.error.message
          : ev.message,
        ev.filename,
        ev.lineno,
        ev.colno,
      );
    },
    true,
  );
  window.addEventListener("unhandledrejection", function (ev) {
    var r = ev.reason;
    report(
      "error",
      "Unhandled rejection: " +
        (r && r.message ? r.message : describe([r])),
    );
  });

  // Escape inside the frame still reaches the window, which owns the key.
  document.addEventListener(
    "keydown",
    function (ev) {
      if (ev.key === "Escape" && !ev.isComposing && !ev.repeat) {
        post({ type: "escape" });
        if (mode === "comment") ev.preventDefault();
      }
    },
    true,
  );

  // Picking. The outline lives in a shadow root of its own so the page's
  // styles never reach it and it never reaches the page's.
  var host = null;
  var box = null;
  function ensureBox() {
    if (box) return box;
    host = document.createElement("coddy-canvas-outline");
    host.style.cssText =
      "all:initial;position:fixed;inset:0;pointer-events:none;z-index:2147483647";
    var root = host.attachShadow ? host.attachShadow({ mode: "closed" }) : host;
    box = document.createElement("div");
    box.style.cssText =
      "position:fixed;pointer-events:none;border:2px solid #3b82f6;border-radius:3px;background:rgba(59,130,246,0.08);display:none;box-sizing:border-box";
    root.appendChild(box);
    (document.documentElement || document.body).appendChild(host);
    return box;
  }
  function outline(el) {
    var b = ensureBox();
    if (!el || !el.getBoundingClientRect) {
      b.style.display = "none";
      return;
    }
    var r = el.getBoundingClientRect();
    b.style.display = "block";
    b.style.left = r.left + "px";
    b.style.top = r.top + "px";
    b.style.width = r.width + "px";
    b.style.height = r.height + "px";
  }
  function pickable(t) {
    return t && t.nodeType === 1 && t !== host && t !== document.documentElement;
  }
  function selectorOf(el) {
    var esc =
      window.CSS && CSS.escape
        ? CSS.escape
        : function (s) {
            return String(s).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
          };
    var parts = [];
    var node = el;
    for (var depth = 0; node && node.nodeType === 1 && depth < 8; depth++) {
      var tag = node.tagName.toLowerCase();
      if (node.id) {
        try {
          if (document.querySelectorAll("#" + esc(node.id)).length === 1) {
            parts.unshift("#" + esc(node.id));
            break;
          }
        } catch (e) {
          /* an id the selector syntax cannot hold */
        }
      }
      if (tag === "body" || tag === "html") {
        parts.unshift(tag);
        break;
      }
      var part = tag;
      var classes = node.classList ? Array.prototype.slice.call(node.classList, 0, 2) : [];
      for (var i = 0; i < classes.length; i++) part += "." + esc(classes[i]);
      var parent = node.parentElement;
      if (parent) {
        var same = 0;
        var index = 0;
        for (var c = parent.firstElementChild; c; c = c.nextElementSibling) {
          if (c.tagName === node.tagName) {
            same++;
            if (c === node) index = same;
          }
        }
        if (same > 1) part += ":nth-of-type(" + index + ")";
      }
      parts.unshift(part);
      node = parent;
    }
    return parts.join(" > ");
  }
  function styleOf(el) {
    var out = {};
    try {
      var cs = getComputedStyle(el);
      var keys = [
        "color",
        "backgroundColor",
        "fontSize",
        "fontWeight",
        "fontFamily",
        "display",
        "margin",
        "padding",
        "borderRadius",
      ];
      for (var i = 0; i < keys.length; i++) out[keys[i]] = cut(cs[keys[i]], 120);
    } catch (e) {
      /* a node without styles */
    }
    return out;
  }
  function pick(el) {
    var r = el.getBoundingClientRect();
    post({
      type: "pick",
      selector: cut(selectorOf(el), MAX_TEXT),
      tag: el.tagName.toLowerCase(),
      id: cut(el.id || "", 120),
      classes: el.classList
        ? Array.prototype.slice.call(el.classList, 0, 5).map(function (c) {
            return cut(c, 60);
          })
        : [],
      text: cut((el.innerText || el.textContent || "").replace(/\s+/g, " ").trim(), MAX_TEXT),
      html: cut(el.outerHTML || "", MAX_HTML),
      rect: { x: r.left, y: r.top, width: r.width, height: r.height },
      styles: styleOf(el),
    });
  }
  function swallow(ev) {
    if (mode !== "comment") return;
    ev.preventDefault();
    ev.stopPropagation();
    if (ev.stopImmediatePropagation) ev.stopImmediatePropagation();
  }
  function onMove(ev) {
    if (mode !== "comment") return;
    if (pickable(ev.target)) outline(ev.target);
  }
  function onClick(ev) {
    if (mode !== "comment") return;
    swallow(ev);
    if (pickable(ev.target)) {
      outline(ev.target);
      pick(ev.target);
    }
  }
  function setMode(next) {
    mode = next;
    if (mode !== "comment") outline(null);
    if (document.documentElement) {
      document.documentElement.style.cursor = mode === "comment" ? "crosshair" : "";
    }
  }
  document.addEventListener("mousemove", onMove, true);
  document.addEventListener("click", onClick, true);
  ["mousedown", "mouseup", "pointerdown", "pointerup", "dblclick", "submit", "auxclick", "contextmenu"].forEach(
    function (type) {
      document.addEventListener(type, swallow, true);
    },
  );
})();
