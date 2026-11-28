import { describe, expect, test } from "vitest";
import { navSlots, splitNavItems, type NavItemId } from "./navOverflow";

const signedIn: NavItemId[] = [
  "history",
  "scheduler",
  "docs",
  "settings",
  "signOut",
];
const relay: NavItemId[] = [
  "history",
  "scheduler",
  "swarm",
  "docs",
  "settings",
  "signOut",
];

describe("splitNavItems", () => {
  test("an unmeasured bar keeps every item", () => {
    expect(splitNavItems(signedIn, null)).toEqual({ bar: signedIn, menu: [] });
  });

  // Settings and extensions are the installation's own surfaces, not what a
  // session is doing: on a bar whose height they share with History they stay
  // behind More, however much room it grows.
  test("settings and extensions stay behind More however much room the bar has", () => {
    const withExtensions: NavItemId[] = [
      "history",
      "scheduler",
      "docs",
      "extensions",
      "settings",
      "signOut",
    ];
    expect(splitNavItems(withExtensions, 6)).toEqual({
      bar: ["history", "scheduler", "docs", "signOut"],
      menu: ["extensions", "settings"],
    });
    expect(splitNavItems(withExtensions, 20).menu).toEqual([
      "extensions",
      "settings",
    ]);
    expect(splitNavItems(signedIn, 5)).toEqual({
      bar: ["history", "scheduler", "docs", "signOut"],
      menu: ["settings"],
    });
  });

  test("one slot short: the More button takes a slot, sign-out and docs fold first", () => {
    expect(splitNavItems(signedIn, 4)).toEqual({
      bar: ["history", "scheduler", "docs"],
      menu: ["settings", "signOut"],
    });
  });

  test("the scheduler is the last foldable item to leave the bar", () => {
    expect(splitNavItems(signedIn, 3)).toEqual({
      bar: ["history", "scheduler"],
      menu: ["docs", "settings", "signOut"],
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
      bar: ["history", "scheduler", "swarm", "docs"],
      menu: ["settings", "signOut"],
    });
  });

  // The environment says where the page works and whether that host answers,
  // so it is always in sight: at the foot of the rail, above sign-out.
  test("the environment never folds and stands above sign-out", () => {
    const withEnv: NavItemId[] = [
      "history",
      "scheduler",
      "docs",
      "settings",
      "environment",
      "signOut",
    ];
    expect(splitNavItems(withEnv, 0)).toEqual({
      bar: ["history", "environment"],
      menu: ["docs", "scheduler", "settings", "signOut"],
    });
    expect(splitNavItems(withEnv, 4)).toEqual({
      bar: ["history", "scheduler", "environment"],
      menu: ["docs", "settings", "signOut"],
    });
    expect(splitNavItems(withEnv, 6).bar).toEqual([
      "history",
      "scheduler",
      "docs",
      "environment",
      "signOut",
    ]);
  });

  test("the menu lists extensions first, settings before sign-out", () => {
    expect(splitNavItems(relay, 2).menu).toEqual([
      "docs",
      "scheduler",
      "settings",
      "signOut",
    ]);
    const withExtensions: NavItemId[] = [
      "history",
      "scheduler",
      "swarm",
      "docs",
      "extensions",
      "statistics",
      "settings",
      "signOut",
    ];
    expect(splitNavItems(withExtensions, 0).menu).toEqual([
      "extensions",
      "docs",
      "statistics",
      "scheduler",
      "settings",
      "signOut",
    ]);
  });

  test("more room never puts fewer items in the bar", () => {
    let previous = -1;
    for (let slots = 0; slots <= 8; slots++) {
      const { bar, menu } = splitNavItems(relay, slots);
      expect(bar.length).toBeGreaterThanOrEqual(previous);
      expect(bar.length + menu.length).toBe(relay.length);
      expect(bar).not.toContain("settings");
      previous = bar.length;
    }
  });

  test("a bar without sign-out or docs folds what it has", () => {
    const plain: NavItemId[] = ["history", "scheduler", "settings"];
    expect(splitNavItems(plain, 3)).toEqual({
      bar: ["history", "scheduler"],
      menu: ["settings"],
    });
    expect(splitNavItems(plain, 2)).toEqual({
      bar: ["history"],
      menu: ["scheduler", "settings"],
    });
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
