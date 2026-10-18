import { fireEvent } from "@testing-library/react";

import type { SettingsSearchEntry } from "./settingsSearch";

// What the drawer draws, read back from the page, and the comparison of it with
// the settings search index.

/**
 * One box of the page that carries a data-settings-anchor, with what it draws
 * of its own: nothing a box nested in it draws.
 */
export type DrawnBox = {
  /** Unique among the boxes of one reading. */
  id: number;
  anchor: string;
  kind: "field" | "row" | "group" | "landmark";
  /** Names the box draws, in document order. */
  names: string[];
  /** The tip of every (i) the box draws, in document order. */
  descriptions: string[];
  /** A list of objects: how many entry frames this instance draws of its own. */
  frames: number | null;
  /** The drawn list of objects (this instance) the box stands in. */
  list: { id: number; anchor: string } | null;
  /** Every (i) the box holds, nested boxes and the body below a heading included. */
  hints: number;
};

const NAME_NODES = "legend, label, h1, h2, h3, h4, h5, h6, .settings-label";
const ANCHORED = "[data-settings-anchor]";

/** The children of the given nodes that match a selector, one level down only. */
function childrenOf(nodes: readonly Element[], selector: string): Element[] {
  return nodes.flatMap((n) =>
    [...n.children].filter((c) => c.matches(selector)),
  );
}

function squash(text: string | null | undefined): string {
  return (text ?? "").replace(/\s+/g, " ").trim();
}

/** The box a node belongs to: the nearest anchored element above it. */
function ownerOf(node: Element): Element | null {
  return node.closest(ANCHORED);
}

/** The tip an (i) shows when it takes the focus. */
function tipOf(hint: HTMLElement): string {
  fireEvent.focus(hint);
  const tip = squash(document.querySelector('[role="tooltip"]')?.textContent);
  fireEvent.blur(hint);
  return tip;
}

/**
 * The names a field's box draws itself: legends, labels, headings and settings
 * labels outside every box nested in it (the outermost of nested ones), and
 * the accessible name of a switch whose label is state text.
 */
function fieldNames(box: Element): string[] {
  const nodes = [...box.querySelectorAll(NAME_NODES)].filter(
    (n) => ownerOf(n) === box,
  );
  const state = new Set<Element>();
  const names: string[] = [];
  for (const sw of box.querySelectorAll(".settings-switch-field")) {
    const own = ownerOf(sw) === box;
    const aria = sw
      .querySelector("[role='switch']")
      ?.getAttribute("aria-label");
    if (own && aria) {
      names.push(aria);
      sw.querySelectorAll("label").forEach((l) => state.add(l));
    }
  }
  for (const n of nodes) {
    if (state.has(n) || nodes.some((o) => o !== n && o.contains(n))) {
      continue;
    }
    names.push(squash(n.textContent));
  }
  return names.filter((n) => n !== "");
}

/**
 * Every box the page draws, read the way the settings search index is held to:
 * a field draws the names and (i) tips inside it and outside any box nested in
 * it, a row form its aria-label, a block or a hand-drawn landmark only its
 * heading and the (i) of that heading. Opening a tip is a focus, then a blur.
 */
export function drawnBoxes(root: ParentNode): DrawnBox[] {
  const boxes = [...root.querySelectorAll<HTMLElement>(ANCHORED)];
  const ids = new Map<Element, number>(boxes.map((b, i) => [b, i]));
  return boxes.map((box) => {
    const anchor = box.getAttribute("data-settings-anchor")!;
    const kind = anchor.startsWith("row:")
      ? "row"
      : anchor.startsWith("group:")
        ? "group"
        : anchor.startsWith("landmark:")
          ? "landmark"
          : "field";
    const ownHints = (within: ParentNode) =>
      [...within.querySelectorAll<HTMLElement>(".field-hint")].filter(
        (h) => ownerOf(h) === box,
      );
    let names: string[];
    let hints: HTMLElement[];
    if (kind === "row") {
      names = [squash(box.getAttribute("aria-label"))];
      hints = [];
    } else if (kind === "field") {
      names = fieldNames(box);
      hints = ownHints(box);
    } else {
      const [heading] = childrenOf([box], "legend, .appearance-section-label");
      names = heading ? [squash(heading.textContent)] : [];
      hints = heading
        ? [...heading.querySelectorAll<HTMLElement>(".field-hint")]
        : [];
    }
    // A list of objects: the entry frames this instance draws of its own, not
    // those of a list nested in one of its entries.
    const rows = childrenOf(
      childrenOf(childrenOf([box], "fieldset"), ".settings-array"),
      ".settings-array-row",
    );
    const isList =
      childrenOf(childrenOf([box], "fieldset"), ".settings-array").length > 0;
    const frames = childrenOf(
      childrenOf(rows, ".settings-array-row-field"),
      ".settings-array-entry",
    ).length;
    const frame = box.parentElement?.closest(".settings-array-entry");
    const listBox = frame ? ownerOf(frame) : null;
    return {
      id: ids.get(box)!,
      anchor,
      kind,
      names,
      descriptions: hints.map(tipOf),
      frames: isList ? frames : null,
      list: listBox
        ? {
            id: ids.get(listBox)!,
            anchor: listBox.getAttribute("data-settings-anchor")!,
          }
        : null,
      hints: box.querySelectorAll(".field-hint").length,
    };
  });
}

/** A legend or a section label the page draws, and the box it stands in. */
export type DrawnLegend = {
  text: string;
  /** The nearest box above it (or itself): null when it stands in none. */
  anchor: string | null;
};

/** What a heading of a block is: a fieldset's legend, or a hand-drawn section label. */
const HEADINGS = "legend, .appearance-section-label";

/**
 * Every legend and section label of the page with the box it belongs to, so
 * that a heading drawn outside the index can be found whatever drew it.
 */
export function drawnLegends(root: ParentNode): DrawnLegend[] {
  return [...root.querySelectorAll<HTMLElement>(HEADINGS)].map((heading) => ({
    text: squash(heading.textContent),
    anchor:
      heading.closest(ANCHORED)?.getAttribute("data-settings-anchor") ?? null,
  }));
}

/**
 * The headings of the page that are not the heading of an indexed box: one
 * that stands in no box, one whose box has no entry, and one its entry does not
 * name. Every legend the drawer draws is a result in its own right, so none
 * may escape.
 */
export function legendsOutsideTheIndex(
  entries: readonly SettingsSearchEntry[],
  legends: readonly DrawnLegend[],
): string[] {
  const byAnchor = new Map<string, SettingsSearchEntry>();
  for (const e of entries) {
    if (e.anchor !== null) {
      byAnchor.set(e.anchor, e);
    }
  }
  const out: string[] = [];
  for (const { text, anchor } of legends) {
    if (anchor === null) {
      out.push(`the legend "${text}" is drawn in no box`);
      continue;
    }
    const entry = byAnchor.get(anchor);
    if (entry === undefined) {
      out.push(`the legend "${text}" stands in ${anchor}, which has no entry`);
    } else if (!entry.names.includes(text)) {
      out.push(
        `the legend "${text}" stands in ${anchor}, whose entry is named ${JSON.stringify(entry.names)}`,
      );
    }
  }
  return out;
}

const sorted = (texts: readonly string[]) => [...texts].sort();
const same = (a: readonly string[], b: readonly string[]) =>
  JSON.stringify(sorted(a)) === JSON.stringify(sorted(b));
const quoted = (texts: readonly string[]) => JSON.stringify(sorted(texts));

/** What differs between a page's boxes and the index entries meant to match them. */
export type DrawingDifferences = {
  /** Boxes drawn without an entry, entries whose box is not drawn, a box drawn too often or too rarely. */
  anchors: string[];
  /** A field box that draws other names or descriptions than its entry. */
  fields: string[];
  /** A block, a landmark or a row form that does. */
  headings: string[];
};

/**
 * Compares the boxes of one page (a tab, or a row form of a list tab) with the
 * entries of the index that stand for them, exactly and in both directions.
 * An anchor outside every list of objects is drawn once; a field of a list of
 * objects is drawn once per entry frame its drawn list instance draws of its
 * own (none when the list draws none), so the index keeps no count. Names and
 * descriptions are compared as multisets.
 */
export function drawingDifferences(
  entries: readonly SettingsSearchEntry[],
  drawn: readonly DrawnBox[],
): DrawingDifferences {
  const out: DrawingDifferences = { anchors: [], fields: [], headings: [] };
  const byAnchor = new Map<string, SettingsSearchEntry>();
  for (const e of entries) {
    if (e.anchor === null) {
      continue;
    }
    if (byAnchor.has(e.anchor)) {
      out.anchors.push(`the index holds ${e.anchor} twice`);
    }
    byAnchor.set(e.anchor, e);
  }
  for (const d of drawn) {
    const e = byAnchor.get(d.anchor);
    if (e === undefined) {
      out.anchors.push(`${d.anchor} is drawn and has no entry`);
      continue;
    }
    const bucket = d.kind === "field" ? out.fields : out.headings;
    if (!same(d.names, e.names)) {
      bucket.push(
        `${d.anchor} draws the names ${quoted(d.names)}, the entry holds ${quoted(e.names)}`,
      );
    }
    if (!same(d.descriptions, e.descriptions)) {
      bucket.push(
        `${d.anchor} draws the descriptions ${quoted(d.descriptions)}, the entry holds ${quoted(e.descriptions)}`,
      );
    }
  }
  for (const e of byAnchor.values()) {
    const of = drawn.filter((d) => d.anchor === e.anchor);
    if (e.list === null) {
      const alone = of.filter((d) => d.list === null);
      if (alone.length !== 1) {
        out.anchors.push(
          `${e.anchor} is drawn ${alone.length} times, once expected`,
        );
      }
      if (of.length !== alone.length) {
        out.anchors.push(
          `${e.anchor} is drawn inside a list, the entry says it is not`,
        );
      }
      continue;
    }
    if (of.some((d) => d.list === null)) {
      out.anchors.push(
        `${e.anchor} is drawn outside ${e.list}, the entry says inside`,
      );
    }
    for (const list of drawn.filter((d) => d.anchor === e.list)) {
      const inside = of.filter((d) => d.list?.id === list.id).length;
      if (inside !== list.frames) {
        out.anchors.push(
          `${e.anchor} is drawn ${inside} times in one ${e.list}, which draws ${list.frames} entries`,
        );
      }
    }
  }
  return out;
}
