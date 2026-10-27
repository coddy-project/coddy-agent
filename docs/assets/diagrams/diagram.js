// Arrows for the documentation diagrams. A diagram lays its boxes out with
// HTML and CSS, then names the arrows between them:
//
//   wires([
//     { from: "#b1", to: "#m1", color: "blue" },
//     { from: "#req", to: "#b3", fromSide: "bottom", toSide: "top", label: "writes" },
//   ]);
//
// Each arrow leaves the middle of one side of its first element and ends with
// an arrowhead at the middle of a side of its second. Sides default to right
// and left. route is "straight" or "elbow" (the default when the sides do not
// face each other on one line): an elbow runs along the first side's axis to
// halfway, turns, and finishes along the second side's axis; bend (0..1,
// default 0.5) moves that turn nearer the start or the end, so two elbows
// between the same columns do not run on one line. fromShift and
// toShift move an end along its side by a fraction of the side's length
// (-0.5..0.5). dash draws a dashed line. label is drawn on the longest
// segment, labelShift moves it by [dx, dy] pixels.
//
// Once the arrows are drawn the page records its height on <html
// data-height>, which scripts/render-diagrams.sh reads to size the picture.

(function () {
  const COLORS = {
    ink: "#374151",
    blue: "#2563eb",
    green: "#16a34a",
    gray: "#9ca3af",
    amber: "#d97706",
    purple: "#7c3aed",
    red: "#dc2626",
  };
  const NS = "http://www.w3.org/2000/svg";

  function el(name, attrs, parent) {
    const node = document.createElementNS(NS, name);
    for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
    if (parent) parent.appendChild(node);
    return node;
  }

  function anchor(rect, side, shift, origin) {
    const s = shift || 0;
    const x0 = rect.left - origin.left;
    const y0 = rect.top - origin.top;
    switch (side) {
      case "left":
        return { x: x0, y: y0 + rect.height * (0.5 + s), axis: "h", dir: -1 };
      case "top":
        return { x: x0 + rect.width * (0.5 + s), y: y0, axis: "v", dir: -1 };
      case "bottom":
        return { x: x0 + rect.width * (0.5 + s), y: y0 + rect.height, axis: "v", dir: 1 };
      default:
        return { x: x0 + rect.width, y: y0 + rect.height * (0.5 + s), axis: "h", dir: 1 };
    }
  }

  function points(a, b, route, bend) {
    const t = bend === undefined ? 0.5 : bend;
    if (route === "straight") return [a, b];
    if (a.axis === "h" && b.axis === "h") {
      if (Math.abs(a.y - b.y) < 1) return [a, b];
      const mx = a.x + (b.x - a.x) * t;
      return [a, { x: mx, y: a.y }, { x: mx, y: b.y }, b];
    }
    if (a.axis === "v" && b.axis === "v") {
      if (Math.abs(a.x - b.x) < 1) return [a, b];
      const my = a.y + (b.y - a.y) * t;
      return [a, { x: a.x, y: my }, { x: b.x, y: my }, b];
    }
    if (a.axis === "h") return [a, { x: b.x, y: a.y }, b];
    return [a, { x: a.x, y: b.y }, b];
  }

  function draw(list) {
    const body = document.body;
    const origin = body.getBoundingClientRect();
    const svg = el("svg", { class: "wires" });
    body.appendChild(svg);
    const defs = el("defs", {}, svg);
    for (const [name, color] of Object.entries(COLORS)) {
      const m = el(
        "marker",
        {
          id: "head-" + name,
          viewBox: "0 0 10 10",
          refX: "9",
          refY: "5",
          markerWidth: "7",
          markerHeight: "7",
          orient: "auto-start-reverse",
        },
        defs,
      );
      el("path", { d: "M0,0 L10,5 L0,10 z", fill: color }, m);
    }
    for (const w of list) {
      const from = document.querySelector(w.from);
      const to = document.querySelector(w.to);
      if (!from || !to) {
        throw new Error("diagram: no element for " + (from ? w.to : w.from));
      }
      const color = COLORS[w.color || "blue"] || w.color;
      const a = anchor(from.getBoundingClientRect(), w.fromSide || "right", w.fromShift, origin);
      const b = anchor(to.getBoundingClientRect(), w.toSide || "left", w.toShift, origin);
      // Leave a hair of room so the arrowhead does not sit on the border.
      const gap = 3;
      if (b.axis === "h") b.x -= gap * -b.dir;
      else b.y -= gap * -b.dir;
      const pts = points(a, b, w.route, w.bend);
      const d = pts.map((p, i) => (i ? "L" : "M") + p.x.toFixed(1) + "," + p.y.toFixed(1)).join(" ");
      const attrs = {
        d,
        fill: "none",
        stroke: color,
        "stroke-width": w.width || "2",
        "stroke-linejoin": "round",
        "marker-end": "url(#head-" + (w.color || "blue") + ")",
      };
      if (w.both) attrs["marker-start"] = attrs["marker-end"];
      if (w.dash) attrs["stroke-dasharray"] = "6 5";
      el("path", attrs, svg);
      if (w.label) {
        let best = 0;
        let len = -1;
        for (let i = 0; i + 1 < pts.length; i++) {
          const l = Math.hypot(pts[i + 1].x - pts[i].x, pts[i + 1].y - pts[i].y);
          if (l > len) {
            len = l;
            best = i;
          }
        }
        const p = pts[best];
        const q = pts[best + 1];
        const [dx, dy] = w.labelShift || [0, 0];
        const t = el(
          "text",
          {
            x: ((p.x + q.x) / 2 + dx).toFixed(1),
            y: ((p.y + q.y) / 2 + dy - 5).toFixed(1),
            fill: color,
            "text-anchor": "middle",
            stroke: "#ffffff",
            "stroke-width": "4",
            "paint-order": "stroke",
          },
          svg,
        );
        t.textContent = w.label;
      }
    }
  }

  function settle(list) {
    if (list && list.length) draw(list);
    const h = Math.ceil(document.body.getBoundingClientRect().height);
    document.documentElement.setAttribute("data-height", String(h));
  }

  window.wires = function (list) {
    const run = () => settle(list);
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(run);
    else window.addEventListener("load", run);
  };
})();
