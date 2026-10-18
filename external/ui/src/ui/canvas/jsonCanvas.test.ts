import { describe, expect, test } from "vitest";
import {
  canvasColor,
  drawOrder,
  edgeShape,
  facingSide,
  insideGroup,
  isImageFile,
  isNoteFile,
  safeLinkUrl,
  sideAnchor,
} from "./jsonCanvas";
import {
  isCanvasFile,
  isCanvasPage,
  normalizeBoard,
  type CanvasItem,
} from "./api";
import {
  canvasLinkedFile,
  canvasShowView,
} from "../messages/StructuredToolCard";

/**
 * What the canvas draws of a JSON Canvas file besides its frames: colours,
 * the curves and arrows of its edges, what a group carries, and a board from
 * the server reduced to what can be drawn.
 */

const a = { x: 0, y: 0, width: 100, height: 50 };
const b = { x: 300, y: 0, width: 100, height: 50 };

describe("JSON Canvas colours", () => {
  test("a preset is Obsidian's colour, a hex stays, anything else is dropped", () => {
    expect(canvasColor("1")).toBe("#fb464c");
    expect(canvasColor("6")).toBe("#a882ff");
    expect(canvasColor("#0af")).toBe("#0af");
    expect(canvasColor("#00AAFF")).toBe("#00AAFF");
    for (const junk of ["7", "red", "#12", "url(x)", "#fff;background:red"]) {
      expect(canvasColor(junk)).toBeUndefined();
    }
    expect(canvasColor(undefined)).toBeUndefined();
  });
});

describe("JSON Canvas edges", () => {
  test("an edge without sides leaves and enters the facing sides", () => {
    expect(facingSide(a, b)).toBe("right");
    expect(facingSide(b, a)).toBe("left");
    expect(facingSide(a, { x: 0, y: 400, width: 100, height: 50 })).toBe(
      "bottom",
    );
    expect(sideAnchor(a, "right")).toEqual({ x: 100, y: 25 });
    expect(sideAnchor(a, "top")).toEqual({ x: 50, y: 0 });
  });

  test("the curve runs from side to side with an arrow at the end only", () => {
    const rects = new Map([
      ["a", a],
      ["b", b],
    ]);
    const shape = edgeShape({ id: "e", fromNode: "a", toNode: "b" }, rects, 1)!;
    expect(shape.d.startsWith("M 100 25 C ")).toBe(true);
    expect(shape.d.endsWith(", 300 25")).toBe(true);
    expect(shape.arrows).toHaveLength(1);
    expect(shape.arrows[0]!.startsWith("300,25 ")).toBe(true);
    expect(shape.mid).toEqual({ x: 200, y: 25 });

    const both = edgeShape(
      { id: "e", fromNode: "a", toNode: "b", fromEnd: "arrow", toEnd: "none" },
      rects,
      1,
    )!;
    expect(both.arrows).toHaveLength(1);
    expect(both.arrows[0]!.startsWith("100,25 ")).toBe(true);

    const named = edgeShape(
      {
        id: "e",
        fromNode: "a",
        fromSide: "bottom",
        toNode: "b",
        toSide: "top",
      },
      rects,
      1,
    )!;
    expect(named.d.startsWith("M 50 50 C ")).toBe(true);
    expect(named.d.endsWith(", 350 0")).toBe(true);
  });

  test("an edge to a node the board does not hold is not drawn", () => {
    expect(
      edgeShape(
        { id: "e", fromNode: "a", toNode: "gone" },
        new Map([["a", a]]),
        1,
      ),
    ).toBeNull();
  });
});

describe("JSON Canvas nodes", () => {
  test("a group carries what lies inside it and is drawn first", () => {
    const group = { x: -10, y: -10, width: 200, height: 200 };
    expect(
      insideGroup(group, [
        { id: "in", x: 0, y: 0, width: 50, height: 50 },
        { id: "half", x: 150, y: 0, width: 100, height: 50 },
        { id: "out", x: 500, y: 0, width: 50, height: 50 },
      ]),
    ).toEqual(["in"]);
    const items = [
      { id: "t", type: "text" },
      { id: "g", type: "group" },
    ] as CanvasItem[];
    expect(drawOrder(items).map((i) => i.id)).toEqual(["g", "t"]);
  });

  test("pictures and notes are told by name, links only when they are web addresses", () => {
    expect(isImageFile("img/logo.PNG")).toBe(true);
    expect(isImageFile("notes/a.md")).toBe(false);
    expect(isNoteFile("notes/a.md")).toBe(true);
    expect(isCanvasFile("design/Board.canvas")).toBe(true);
    expect(isCanvasFile("design/board.canvas.json")).toBe(false);
    // What the server runs: an .xhtml file is previewed, never put on the canvas.
    expect(isCanvasPage("site/index.html")).toBe(true);
    expect(isCanvasPage("site/INDEX.HTM")).toBe(true);
    expect(isCanvasPage("site/page.xhtml")).toBe(false);
    expect(safeLinkUrl("https://example.com/a")).toBe("https://example.com/a");
    expect(safeLinkUrl("javascript:alert(1)")).toBeUndefined();
    expect(safeLinkUrl("data:text/html,x")).toBeUndefined();
    expect(safeLinkUrl("not a url")).toBeUndefined();
  });
});

describe("a linked board from the server", () => {
  test("keeps the link, the nodes and the edges it can draw", () => {
    const b = normalizeBoard({
      session_id: "s",
      revision: 3,
      frames: [],
      link: { path: "design/shop.canvas", stamp: "[1,2]", error: "" },
      items: [
        { id: "n1", type: "text", x: 0, y: 0, width: 10, height: 10 },
        { id: "n2", type: "bogus", x: 0, y: 0, width: 10, height: 10 },
        { id: "n3", type: "text", x: NaN, y: 0, width: 10, height: 10 },
      ] as CanvasItem[],
      edges: [
        { id: "e1", fromNode: "n1", toNode: "n2" },
        { id: "e2", fromNode: "n1" },
      ] as never,
    });
    expect(b.link).toEqual({ path: "design/shop.canvas", stamp: "[1,2]" });
    expect(b.items.map((i) => i.id)).toEqual(["n1"]);
    expect(b.edges.map((e) => e.id)).toEqual(["e1"]);
    expect(normalizeBoard({ frames: [] }).link).toBeUndefined();
  });

  test("the canvas_show card reads an Obsidian node id and a linked file", () => {
    expect(
      canvasShowView(
        "Canvas frame 9a8b7c6d5e4f3a2b: site/index.html (phone 390x844), added.\nmore",
      ),
    ).toEqual({
      frameId: "9a8b7c6d5e4f3a2b",
      path: "site/index.html",
      size: "phone 390x844",
      reason: "added",
    });
    expect(
      canvasLinkedFile(
        "The canvas of this chat is now the file design/shop.canvas.\nIts file nodes...",
      ),
    ).toBe("design/shop.canvas");
    expect(
      canvasLinkedFile("Canvas frame fr_1: a.html (x), added."),
    ).toBeNull();
  });
});
