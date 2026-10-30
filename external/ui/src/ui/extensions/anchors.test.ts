import { describe, expect, test, vi } from "vitest";
import { AnchorRegistry, isExtensionAnchorName } from "./anchors";

const el = () => document.createElement("div");

describe("anchor registry", () => {
  test("attach hands the box out and detach reports null", () => {
    const registry = new AnchorRegistry();
    const box = el();
    const seen: (HTMLElement | null)[] = [];
    registry.on("composer.actions", new AbortController().signal, (e) =>
      seen.push(e),
    );
    registry.attach("composer.actions", box);
    expect(registry.get("composer.actions")).toBe(box);
    registry.detach("composer.actions", box);
    expect(registry.get("composer.actions")).toBeNull();
    expect(seen).toEqual([box, null]);
  });

  test("a late listener hears the box that is already there", () => {
    const registry = new AnchorRegistry();
    const box = el();
    registry.attach("nav.rail", box);
    const listener = vi.fn();
    registry.on("nav.rail", new AbortController().signal, listener);
    expect(listener).toHaveBeenCalledWith(box);
  });

  test("the lifetime signal removes the listener", () => {
    const registry = new AnchorRegistry();
    const controller = new AbortController();
    const listener = vi.fn();
    registry.on("statusline", controller.signal, listener);
    controller.abort();
    registry.attach("statusline", el());
    expect(listener).not.toHaveBeenCalled();
  });

  test("unknown anchor names are refused", () => {
    const registry = new AnchorRegistry();
    registry.attach("nowhere", el());
    expect(registry.get("nowhere")).toBeNull();
    expect(() =>
      registry.on("nowhere", new AbortController().signal, vi.fn()),
    ).toThrow(/Unknown anchor/);
    expect(isExtensionAnchorName("composer.actions")).toBe(true);
    expect(isExtensionAnchorName("composer")).toBe(false);
  });
});
