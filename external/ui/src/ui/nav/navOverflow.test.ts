import { describe, expect, test } from "vitest";
import { splitNavItems, type NavItemId } from "./navOverflow";

const everything: NavItemId[] = [
  "history",
  "scheduler",
  "swarm",
  "docs",
  "extensions",
  "statistics",
  "settings",
  "environment",
  "signOut",
];

describe("splitNavItems", () => {
  // The bar is a fixed row whatever the window measures, so the split is the
  // same at 320px and at 1199px.
  test("the bar holds history, the scheduler, the environment and the swarm, in that order", () => {
    expect(splitNavItems(everything).bar).toEqual([
      "history",
      "scheduler",
      "environment",
      "swarm",
    ]);
  });

  test("everything else folds into the menu, settings and sign-out last", () => {
    expect(splitNavItems(everything).menu).toEqual([
      "extensions",
      "docs",
      "statistics",
      "settings",
      "signOut",
    ]);
  });

  test("a page that is not a relay has no swarm in the bar", () => {
    const noSwarm = everything.filter((id) => id !== "swarm");
    expect(splitNavItems(noSwarm).bar).toEqual([
      "history",
      "scheduler",
      "environment",
    ]);
  });

  test("an item the rail does not offer lands in neither list", () => {
    expect(splitNavItems(["history", "settings"])).toEqual({
      bar: ["history"],
      menu: ["settings"],
    });
    expect(splitNavItems([])).toEqual({ bar: [], menu: [] });
  });

  test("no item is ever in both lists or lost", () => {
    const { bar, menu } = splitNavItems(everything);
    expect(bar.filter((id) => menu.includes(id))).toEqual([]);
    expect([...bar, ...menu].sort()).toEqual([...everything].sort());
  });
});
