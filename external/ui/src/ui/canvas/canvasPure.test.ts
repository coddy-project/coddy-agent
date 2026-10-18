import { describe, expect, test } from "vitest";
import {
  boundsOf,
  fitCamera,
  MAX_SCALE,
  MIN_SCALE,
  pan,
  toScreen,
  zoomAt,
} from "./camera";
import {
  BRIDGE_PROTOCOL,
  keepConsole,
  parseBridgeMessage,
  staysInFrame,
} from "./bridgeProtocol";
import { composeCanvasPrompt, pickLabel, type CanvasPick } from "./pickPrompt";
import { frameBase, normalizeBoard } from "./api";

describe("canvas camera", () => {
  test("fit shows every frame, centred, never above 100%", () => {
    const b = boundsOf([
      { x: 0, y: 0, width: 1280, height: 800 },
      { x: 1360, y: 0, width: 390, height: 844 },
    ])!;
    expect(b).toEqual({ x: 0, y: 0, width: 1750, height: 844 });
    const cam = fitCamera(b, { width: 1000, height: 700 });
    expect(cam.scale).toBeLessThan(1);
    const s = toScreen(cam, b);
    expect(s.x).toBeGreaterThanOrEqual(31);
    expect(s.x + s.width).toBeLessThanOrEqual(1000 - 31);
    const lone = fitCamera(
      { x: 0, y: 0, width: 390, height: 844 },
      { width: 2000, height: 1400 },
    );
    expect(lone.scale).toBe(1);
  });

  test("zoom keeps the point under the pointer and stays in bounds", () => {
    const cam = { x: 100, y: 50, scale: 0.5 };
    const z = zoomAt(cam, 2, { x: 300, y: 250 });
    expect(z.scale).toBe(1);
    // board point under (300, 250) before and after
    expect((300 - cam.x) / cam.scale).toBeCloseTo((300 - z.x) / z.scale);
    expect((250 - cam.y) / cam.scale).toBeCloseTo((250 - z.y) / z.scale);
    expect(zoomAt(cam, 1000, { x: 0, y: 0 }).scale).toBe(MAX_SCALE);
    expect(zoomAt(cam, 0.0001, { x: 0, y: 0 }).scale).toBe(MIN_SCALE);
    expect(zoomAt(cam, Number.NaN, { x: 0, y: 0 }).scale).toBe(0.5);
    expect(pan(cam, 10, -5)).toEqual({ x: 110, y: 45, scale: 0.5 });
    expect(boundsOf([])).toBeNull();
  });
});

describe("canvas bridge protocol", () => {
  test("reads only well-formed messages and cuts every string", () => {
    expect(parseBridgeMessage(null)).toBeNull();
    expect(parseBridgeMessage({ type: "ready" })).toBeNull();
    expect(parseBridgeMessage({ proto: "other", type: "ready" })).toBeNull();
    expect(
      parseBridgeMessage({ proto: BRIDGE_PROTOCOL, type: "evil" }),
    ).toBeNull();
    expect(
      parseBridgeMessage({ proto: BRIDGE_PROTOCOL, type: "ready" }),
    ).toEqual({
      type: "ready",
    });
    const pick = parseBridgeMessage({
      proto: BRIDGE_PROTOCOL,
      type: "pick",
      gen: 3,
      selector: "main > h1",
      tag: "h1",
      id: "",
      classes: ["hero", "big", 7, "a", "b", "c"],
      text: "x".repeat(1000),
      html: "<h1>" + "y".repeat(5000) + "</h1>",
      rect: { x: 10, y: "20", width: 30, height: 40 },
      styles: { color: "red", "bad-key": "x", fontSize: "32px" },
    });
    expect(pick?.type).toBe("pick");
    if (pick?.type !== "pick") throw new Error("not a pick");
    expect(pick.gen).toBe(3);
    expect(pick.element.text.length).toBe(300);
    expect(pick.element.html.length).toBe(2000);
    expect(pick.element.classes).toEqual(["hero", "big", "", "a", "b"]);
    expect(pick.element.rect).toEqual({ x: 10, y: 0, width: 30, height: 40 });
    expect(pick.element.styles).toEqual({ color: "red", fontSize: "32px" });
    const c = parseBridgeMessage({
      proto: BRIDGE_PROTOCOL,
      type: "console",
      gen: 1,
      level: "info",
      message: "m".repeat(5000),
    });
    expect(c).toMatchObject({ type: "console", level: "error" });
    if (c?.type !== "console") throw new Error("not console");
    expect(c.message.length).toBe(1000);
  });

  test("a document going away says so", () => {
    expect(
      parseBridgeMessage({ proto: BRIDGE_PROTOCOL, type: "leaving", gen: 0 }),
    ).toEqual({ type: "leaving", gen: 0, frame: "" });
  });

  test("a page stays in its frame only under the frame's own address", () => {
    const base = "https://relay.example/swarm/nodes/a/coddy/canvas/tok.sig/";
    expect(staysInFrame(base + "about.html", base)).toBe(true);
    expect(staysInFrame("https://evil.example/?" + base, base)).toBe(false);
    expect(staysInFrame("https://relay.example/coddy/sessions", base)).toBe(
      false,
    );
    expect(staysInFrame("anything", "")).toBe(false);
    expect(frameBase(base + "pages/about.html")).toBe(base);
    expect(
      frameBase("http://127.0.0.1:1/coddy/canvas/a.b/index.html?x=1"),
    ).toBe("http://127.0.0.1:1/coddy/canvas/a.b/");
  });

  test("the console keeps the newest lines", () => {
    let lines: number[] = [];
    for (let i = 0; i < 25; i++) lines = keepConsole(lines, i);
    expect(lines).toHaveLength(20);
    expect(lines[0]).toBe(5);
    expect(lines[19]).toBe(24);
  });
});

function pick(over: Partial<CanvasPick> = {}): CanvasPick {
  return {
    id: "p1",
    frameId: "fr_1",
    page: "site/index.html",
    size: "desktop 1280x800",
    note: "",
    element: {
      selector: "main > section.hero > h1",
      tag: "h1",
      id: "",
      classes: ["hero-title"],
      text: "Welcome",
      html: '<h1 class="hero-title">Welcome</h1>',
      rect: { x: 0, y: 0, width: 10, height: 10 },
      styles: {},
    },
    ...over,
  };
}

describe("canvas prompt", () => {
  test("one pick reads as the plan describes it", () => {
    expect(
      composeCanvasPrompt(
        [pick()],
        "Make the headline larger and use the accent colour.",
      ),
    ).toBe(
      "On the canvas, `site/index.html` (desktop 1280x800):\n\n" +
        "Element `main > section.hero > h1`:\n\n" +
        '```html\n<h1 class="hero-title">Welcome</h1>\n```\n\n' +
        "Make the headline larger and use the accent colour.",
    );
  });

  test("several picks are numbered, grouped by page, each with its note", () => {
    const out = composeCanvasPrompt(
      [
        pick({ note: "Bigger" }),
        pick({
          id: "p2",
          note: "Blue",
          element: {
            ...pick().element,
            selector: "#buy",
            tag: "button",
            id: "buy",
            html: "<button id=buy>```</button>",
          },
        }),
        pick({ id: "p3", page: "site/about.html", note: "" }),
      ],
      "",
    );
    expect(out).toContain("1. Element `main > section.hero > h1`:");
    expect(out).toContain("2. Element `#buy`:");
    expect(out).toContain("3. Element");
    expect(out).toContain("Bigger");
    expect(out).toContain("````html\n<button id=buy>```</button>\n````");
    expect(out.match(/On the canvas/g)).toHaveLength(2);
  });

  test("no picks is the text alone, and labels name the element", () => {
    expect(composeCanvasPrompt([], "  hi  ")).toBe("hi");
    expect(pickLabel(pick())).toBe("h1.hero-title");
    expect(pickLabel(pick({ element: { ...pick().element, id: "top" } }))).toBe(
      "h1#top",
    );
  });
});

describe("canvas board answers", () => {
  test("a board answer without frames is an empty board", () => {
    expect(normalizeBoard({} as never).frames).toEqual([]);
    expect(normalizeBoard(null).frames).toEqual([]);
    expect(
      normalizeBoard({
        frames: [null, { id: "x" }, { id: "fr_1", url: "/u" }],
      } as never).frames,
    ).toHaveLength(1);
  });
});
