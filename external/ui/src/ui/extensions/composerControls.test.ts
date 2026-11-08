import { describe, expect, test, vi } from "vitest";
import {
  ComposerControlHost,
  validateComposerAlter,
  validateComposerControl,
  type ComposerAlter,
  type ComposerControl,
} from "./composerControls";
import { Contributions, type Owned } from "./contributions";
import type { ExtensionAPI } from "./runtime";

function setup() {
  const controls = new Contributions<ComposerControl>(() => host.refresh());
  const alters = new Contributions<ComposerAlter>(() => host.refresh());
  const failed: string[] = [];
  const host = new ComposerControlHost(
    () => controls.rows,
    () => alters.rows,
    (key) => {
      failed.push(key);
    },
  );
  return { controls, alters, host, failed };
}

const api = (owner: string) =>
  ({ id: owner, signal: new AbortController().signal }) as ExtensionAPI;

describe("composer controls", () => {
  test("a control needs an id, a placement and a mount", () => {
    expect(() =>
      validateComposerControl({
        id: "x",
        placement: "nowhere" as never,
        mount: () => {},
      }),
    ).toThrow(/composer control/);
    expect(() => validateComposerAlter(undefined as never)).toThrow(
      /alter needs/,
    );
  });

  test("the assembled list runs alter passes in order", () => {
    const { controls, alters, host } = setup();
    controls.register(
      "a",
      api("a"),
      { id: "one", placement: "actions", title: "One", mount: () => {} },
      validateComposerControl,
    );
    controls.register(
      "b",
      api("b"),
      {
        id: "two",
        placement: "actions",
        title: "Two",
        order: 1,
        mount: () => {},
      },
      validateComposerControl,
    );
    alters.register(
      "a",
      api("a"),
      {
        id: "rename",
        run: (list) =>
          list.map((row) =>
            row.id === "one" ? { ...row, title: "Uno", order: 2 } : row,
          ),
      },
      () => {},
    );
    alters.register(
      "b",
      api("b"),
      {
        id: "hide",
        run: (list) => list.filter((row) => row.id !== "two" || row),
      },
      () => {},
    );
    const rows = host.descriptors("actions");
    expect(rows.map((row) => [row.id, row.title, row.order])).toEqual([
      ["two", "Two", 1],
      ["one", "Uno", 2],
    ]);
  });

  test("the boxes mount what survives, in the altered order", () => {
    const { controls, alters, host } = setup();
    const box = document.createElement("div");
    host.attach("actions", box, new AbortController().signal);
    const cleanup = vi.fn();
    controls.register(
      "a",
      api("a"),
      {
        id: "one",
        placement: "actions",
        mount: (el) => {
          el.textContent = "one";
          return cleanup;
        },
      },
      validateComposerControl,
    );
    controls.register(
      "b",
      api("b"),
      {
        id: "two",
        placement: "actions",
        mount: (el) => {
          el.textContent = "two";
        },
      },
      validateComposerControl,
    );
    expect([...box.children].map((c) => c.textContent)).toEqual(["one", "two"]);
    // An alter that reverses the order re-renders the boxes in place.
    alters.register(
      "c",
      api("c"),
      {
        id: "reverse",
        run: (list) => [...list].reverse(),
      },
      () => {},
    );
    expect([...box.children].map((c) => c.textContent)).toEqual(["two", "one"]);
    // A hidden control unmounts with its cleanup; the rest stay.
    alters.register(
      "c",
      api("c"),
      {
        id: "hide",
        run: (list) =>
          list.map((row) =>
            row.id === "one" ? { ...row, visible: false } : row,
          ),
      },
      () => {},
    );
    expect([...box.children].map((c) => c.textContent)).toEqual(["two"]);
    expect(cleanup).toHaveBeenCalledOnce();
  });

  test("an alter that throws is dropped, the list stays", () => {
    const { alters, host, failed } = setup();
    const owner = {};
    host.declareHostControls(
      [{ id: "mode", key: "host/mode", placement: "tabs", title: "Mode" }],
      owner,
    );
    alters.register(
      "a",
      api("a"),
      {
        id: "boom",
        run: () => {
          throw new Error("boom");
        },
      },
      () => {},
    );
    alters.register(
      "a",
      api("a"),
      {
        id: "invalid",
        run: () => [null] as never,
      },
      () => {},
    );
    expect(host.descriptors("tabs")).toEqual([
      expect.objectContaining({ key: "host/mode", title: "Mode" }),
    ]);
    expect(host.descriptors("actions")).toEqual([]);
    expect(failed).toEqual(["a/boom", "a/invalid", "a/boom", "a/invalid"]);
    expect(() => validateComposerAlter((() => {}) as never)).not.toThrow();
  });

  test("host and extension entries share retitle, reorder, hide and drop", () => {
    const { controls, alters, host } = setup();
    const owner = {};
    host.declareHostControls(
      [
        { id: "mode", key: "host/mode", placement: "tabs", title: "Mode" },
        {
          id: "context",
          key: "host/context",
          placement: "tabs",
          title: "Context",
        },
        { id: "send", key: "host/send", placement: "tabs", title: "Send" },
      ],
      owner,
    );
    for (const extension of ["a", "b"]) {
      controls.register(
        extension,
        api(extension),
        {
          id: "shared",
          placement: "tabs",
          title: extension,
          mount: (element) => {
            element.textContent = extension;
          },
        },
        validateComposerControl,
      );
    }
    alters.register(
      "alter",
      api("alter"),
      {
        id: "mixed",
        run: (list) =>
          list
            .filter((row) => row.key !== "host/send")
            .map((row) => {
              if (row.key === "host/mode") {
                return { ...row, title: "Rewired mode" };
              }
              if (row.key === "host/context") {
                return { ...row, visible: false };
              }
              if (row.key === "a/shared") {
                return { ...row, title: "First extension" };
              }
              return row;
            })
            .reverse(),
      },
      () => {},
    );

    const descriptors = host.descriptors("tabs");
    expect(descriptors.map((row) => row.key)).toEqual([
      "host/context",
      "host/mode",
      "b/shared",
      "a/shared",
    ]);
    expect(descriptors[1]).toMatchObject({
      id: "mode",
      title: "Rewired mode",
    });
    expect(
      descriptors.filter((row) => row.visible !== false).map((r) => r.key),
    ).toEqual(["host/mode", "b/shared", "a/shared"]);

    const box = document.createElement("div");
    host.attach("tabs", box, new AbortController().signal);
    expect([...box.children].map((child) => child.textContent)).toEqual([
      "b",
      "a",
    ]);
    expect(box.children[1]).toHaveAttribute("title", "First extension");
  });

  test("conditional host declarations and stale cleanup do not clear a successor", () => {
    const { host } = setup();
    const first = {};
    const second = {};
    const attach = {
      id: "attach",
      key: "host/attach",
      placement: "tabs" as const,
    };
    host.declareHostControls([attach], first);
    expect(host.descriptors("tabs").map((row) => row.key)).toEqual([
      "host/attach",
    ]);
    host.declareHostControls([], first);
    expect(host.descriptors("tabs")).toEqual([]);
    host.declareHostControls([attach], second);
    host.clearHostControls(first);
    expect(host.descriptors("tabs").map((row) => row.key)).toEqual([
      "host/attach",
    ]);
    host.clearHostControls(second);
    expect(host.descriptors("tabs")).toEqual([]);
  });
});
