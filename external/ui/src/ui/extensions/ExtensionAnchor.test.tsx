import { afterEach, describe, expect, test, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { ExtensionAnchor } from "./ExtensionAnchor";
import { ExtensionRuntime, type ExtensionAPI } from "./runtime";
import { validateComposerControl } from "./composerControls";

let runtime: ExtensionRuntime | null = null;
vi.mock("./ExtensionsProvider", () => ({
  useExtensions: () => (runtime ? { runtime } : null),
}));

afterEach(() => {
  cleanup();
  runtime?.stop();
  runtime = null;
  vi.restoreAllMocks();
});

describe("composer extension anchors", () => {
  test("registered controls mount and clean up with the anchor", () => {
    runtime = new ExtensionRuntime(() => {});
    const activeRuntime = runtime;
    const controller = new AbortController();
    const api = { id: "demo", signal: controller.signal } as ExtensionAPI;
    const dispose = vi.fn();
    const mount = vi.fn((element: HTMLElement) => {
      element.textContent = "Registered";
      return dispose;
    });
    activeRuntime.composerRows.register(
      "demo",
      api,
      { id: "button", placement: "actions", mount },
      validateComposerControl,
    );

    const view = render(<ExtensionAnchor name="composer.actions" />);
    const box = document.querySelector(
      '[data-extension-anchor="composer.actions"]',
    ) as HTMLElement;
    expect(box).toHaveTextContent("Registered");
    expect(mount).toHaveBeenCalledOnce();
    expect(activeRuntime.anchors.get("composer.actions")).toBe(box);

    const handle = activeRuntime.composerRows.register(
      "demo",
      api,
      {
        id: "second",
        placement: "actions",
        mount: (element) => {
          element.textContent = "Second";
        },
      },
      validateComposerControl,
    );
    expect(box).toHaveTextContent("RegisteredSecond");
    handle.remove();
    expect(box).toHaveTextContent("Registered");

    view.unmount();
    expect(dispose).toHaveBeenCalledOnce();
    expect(activeRuntime.anchors.get("composer.actions")).toBeNull();
    expect(box).toBeEmptyDOMElement();
  });
});
