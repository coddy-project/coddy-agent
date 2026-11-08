import { afterEach, describe, expect, test, vi } from "vitest";
import { act, cleanup, render, screen, within } from "@testing-library/react";
import type { ComponentProps } from "react";
import { Composer } from "./Composer";
import { ExtensionRuntime, type ExtensionAPI } from "../extensions/runtime";
import { validateComposerControl } from "../extensions/composerControls";

let runtime: ExtensionRuntime | null = null;
vi.mock("../extensions/ExtensionsProvider", () => ({
  useExtensions: () => (runtime ? { runtime } : null),
}));

afterEach(() => {
  cleanup();
  runtime?.stop();
  runtime = null;
});

const fullProps: ComponentProps<typeof Composer> = {
  value: "hello",
  isEmpty: false,
  mode: "agent",
  modes: ["agent", "plan", "ask"],
  llmModelMultimodal: true,
  llmModels: ["openai/gpt-5"],
  llmModel: "openai/gpt-5",
  onLlmModelChange: () => {},
  llmReasoningLevels: ["low", "high"],
  llmReasoning: "low",
  onLlmReasoningChange: () => {},
  permissionMode: "ask",
  configuredPermissionMode: "ask",
  onPermissionModeChange: () => {},
  toolsetControl: {
    value: "default",
    choices: ["default", "coding"],
    onChange: () => {},
  },
  settingsOverrides: [
    { setting: "model", value: "openai/gpt-5", turnsLeft: 1, active: true },
  ],
  onModeChange: () => {},
  onChange: () => {},
  onSend: () => {},
};

const reducedProps: ComponentProps<typeof Composer> = {
  value: "hello",
  isEmpty: false,
  mode: "agent",
  modes: ["agent", "plan", "ask"],
  onModeChange: () => {},
  onChange: () => {},
  onSend: () => {},
};

function api(owner: string, signal: AbortSignal) {
  return { id: owner, signal } as ExtensionAPI;
}

describe("composer host controls in the extension pipeline", () => {
  test("mixed controls alter by namespaced key and follow conditional props", () => {
    runtime = new ExtensionRuntime(() => {});
    const activeRuntime = runtime;
    const controller = new AbortController();
    const disposals: string[] = [];
    const view = render(<Composer {...fullProps} />);

    act(() => {
      for (const owner of ["a", "b"]) {
        activeRuntime.composerRows.register(
          owner,
          api(owner, controller.signal),
          {
            id: "duplicate",
            placement: "tabs",
            title: owner,
            mount: (element) => {
              element.textContent = owner;
              return () => disposals.push(owner);
            },
          },
          validateComposerControl,
        );
      }
      activeRuntime.composerRows.register(
        "c",
        api("c", controller.signal),
        {
          id: "action",
          placement: "actions",
          mount: (element) => {
            element.textContent = "action";
            return () => disposals.push("c");
          },
        },
        validateComposerControl,
      );
      activeRuntime.composerAlters.register(
        "alter",
        api("alter", controller.signal),
        {
          id: "mixed",
          run: (list) => {
            const tabOrder = [
              "host/model",
              "b/duplicate",
              "host/attach",
              "a/duplicate",
              "host/mode",
              "host/permission",
              "host/overrides",
            ];
            const actionOrder = ["host/send", "c/action", "host/context"];
            const order =
              list[0]?.placement === "actions" ? actionOrder : tabOrder;
            return list
              .filter((row) => row.key !== "host/toolset")
              .map((row) => {
                if (row.key === "host/mode") {
                  return { ...row, title: "Rewired mode" };
                }
                if (row.key === "host/reasoning") {
                  return { ...row, visible: false };
                }
                if (row.key === "host/context") {
                  return { ...row, title: "Usage altered" };
                }
                if (row.key === "host/send") {
                  return { ...row, title: "Go" };
                }
                if (row.key === "a/duplicate") {
                  return { ...row, title: "First duplicate" };
                }
                return row;
              })
              .sort(
                (a, b) =>
                  order.indexOf(a.key ?? "") - order.indexOf(b.key ?? ""),
              );
          },
        },
        () => {},
      );
    });

    const tabs = document.querySelector(".composer-tabs")!;
    const tabControls = [
      ...tabs.querySelectorAll(
        "button.composer-tab, [data-extension-composer-control]",
      ),
    ];
    expect(
      tabControls.map(
        (element) =>
          element.getAttribute("data-extension-composer-control") ??
          element.getAttribute("aria-label"),
      ),
    ).toEqual([
      "Model",
      "b/duplicate",
      "Attach file",
      "a/duplicate",
      "Rewired mode",
      "Permissions",
    ]);
    expect(screen.queryByTestId("composer-toolset")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Reasoning level" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Rewired mode" }),
    ).toHaveAttribute("title", "Rewired mode");
    expect(screen.getByTestId("composer-overrides")).toBeInTheDocument();
    expect(
      document.querySelector(
        '[data-extension-composer-control="a/duplicate"] .extension-composer-control',
      ),
    ).toHaveAttribute("title", "First duplicate");
    expect(tabs).toHaveTextContent("a");
    expect(tabs).toHaveTextContent("b");

    expect(screen.getByRole("button", { name: "Go" })).toHaveAttribute(
      "id",
      "btn-send",
    );
    expect(screen.getByTestId("composer-context-ring-host")).toHaveAttribute(
      "aria-label",
      "Usage altered",
    );
    const actions = document.querySelector(".composer-bar-actions")!;
    const actionControls = [
      ...actions.querySelectorAll(
        "#btn-send, [data-extension-composer-control], [data-testid=composer-context-ring-host]",
      ),
    ];
    expect(
      actionControls.map(
        (element) =>
          element.id ||
          element.getAttribute("data-extension-composer-control") ||
          element.getAttribute("data-testid"),
      ),
    ).toEqual(["btn-send", "c/action", "composer-context-ring-host"]);

    view.rerender(<Composer {...reducedProps} />);
    expect(screen.queryByTestId("composer-attach-btn")).toBeNull();
    expect(screen.queryByRole("button", { name: "Model" })).toBeNull();
    expect(screen.queryByTestId("composer-permission")).toBeNull();
    expect(screen.queryByTestId("composer-overrides")).toBeNull();
    expect(
      document.querySelector('[data-extension-composer-control="a/duplicate"]'),
    ).toBeInTheDocument();
    expect(disposals).toEqual([]);

    view.rerender(<Composer {...fullProps} />);
    expect(screen.getByTestId("composer-attach-btn")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Model" })).toBeInTheDocument();
    expect(screen.getByTestId("composer-permission")).toBeInTheDocument();
    expect(screen.getByTestId("composer-overrides")).toBeInTheDocument();

    const genericActions = document.querySelector(
      '[data-extension-anchor="composer.actions"]',
    )!;
    expect(
      within(genericActions as HTMLElement).queryByText("action"),
    ).toBeNull();
    view.unmount();
    expect(disposals.sort()).toEqual(["a", "b", "c"]);
  });

  test("a failing alter leaves the previously altered host controls renderable", () => {
    runtime = new ExtensionRuntime(() => {});
    const activeRuntime = runtime;
    const controller = new AbortController();
    render(<Composer {...reducedProps} />);
    act(() => {
      activeRuntime.composerAlters.register(
        "alter",
        api("alter", controller.signal),
        {
          id: "retitle",
          run: (list) =>
            list.map((row) =>
              row.key === "host/send" ? { ...row, title: "Still sends" } : row,
            ),
        },
        () => {},
      );
      activeRuntime.composerAlters.register(
        "alter",
        api("alter", controller.signal),
        {
          id: "broken",
          run: () => {
            throw new Error("broken pass");
          },
        },
        () => {},
      );
    });
    expect(screen.getByRole("button", { name: "Still sends" })).toHaveAttribute(
      "id",
      "btn-send",
    );
    expect(screen.getByRole("button", { name: "Mode" })).toBeInTheDocument();
    expect(activeRuntime.snapshot().errors.alter).toContain("broken pass");
  });
});
