import { describe, expect, test } from "vitest";
import { navSlots, splitNavItems, type NavItemId } from "./navOverflow";

const signedIn: NavItemId[] = ["history", "scheduler", "docs", "settings", "signOut"];
const relay: NavItemId[] = ["history", "scheduler", "swarm", "docs", "settings", "signOut"];

describe("splitNavItems", () => {
  test("an unmeasured bar keeps every item", () => {
    expect(splitNavItems(signedIn, null)).toEqual({ bar: signedIn, menu: [] });
  });

  test("a bar with room for every item keeps every item and has no menu", () => {
    expect(splitNavItems(signedIn, 5)).toEqual({ bar: signedIn, menu: [] });
    expect(splitNavItems(signedIn, 9)).toEqual({ bar: signedIn, menu: [] });
  });

  test("one slot short: the More button takes a slot, sign-out and docs fold first", () => {
    expect(splitNavItems(signedIn, 4)).toEqual({
      bar: ["history", "scheduler", "settings"],
      menu: ["docs", "signOut"],
    });
  });

  test("settings is the last foldable item to leave the bar", () => {
    expect(splitNavItems(signedIn, 3)).toEqual({
      bar: ["history", "settings"],
      menu: ["docs", "scheduler", "signOut"],
    });
    expect(splitNavItems(signedIn, 2)).toEqual({
      bar: ["history"],
      menu: ["docs", "scheduler", "settings", "signOut"],
    });
  });

  test("history and swarm never fold, even when the bar has no room at all", () => {
    expect(splitNavItems(relay, 0)).toEqual({
      bar: ["history", "swarm"],
      menu: ["docs", "scheduler", "settings", "signOut"],
    });
    expect(splitNavItems(relay, 5)).toEqual({
      bar: ["history", "scheduler", "swarm", "settings"],
      menu: ["docs", "signOut"],
    });
  });

  test("the menu lists docs, scheduler, settings and ends with sign-out", () => {
    expect(splitNavItems(relay, 2).menu).toEqual(["docs", "scheduler", "settings", "signOut"]);
  });

  test("more room never puts fewer items in the bar", () => {
    let previous = -1;
    for (let slots = 0; slots <= 8; slots++) {
      const { bar, menu } = splitNavItems(relay, slots);
      expect(bar.length).toBeGreaterThanOrEqual(previous);
      expect(bar.length + menu.length).toBe(relay.length);
      previous = bar.length;
    }
  });

  test("a bar without sign-out or docs folds what it has", () => {
    const plain: NavItemId[] = ["history", "scheduler", "settings"];
    expect(splitNavItems(plain, 3)).toEqual({ bar: plain, menu: [] });
    expect(splitNavItems(plain, 2)).toEqual({ bar: ["history"], menu: ["scheduler", "settings"] });
  });
});

describe("navSlots", () => {
  test("counts the icons that fit with the gaps between them", () => {
    expect(navSlots(234, 40, 4)).toBe(5);
    expect(navSlots(233, 40, 4)).toBe(5);
    expect(navSlots(215, 40, 4)).toBe(4);
    expect(navSlots(40, 40, 4)).toBe(1);
  });

  test("no room or no measurable icon is zero slots", () => {
    expect(navSlots(0, 40, 4)).toBe(0);
    expect(navSlots(-30, 40, 4)).toBe(0);
    expect(navSlots(300, 0, 4)).toBe(0);
  });
});
