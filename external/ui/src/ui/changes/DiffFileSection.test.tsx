import { afterEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { StrictMode } from "react";
import * as clipboard from "../clipboard";
import { DiffFileSection } from "./DiffFileSection";

vi.mock("../env/apiImage", () => ({
  useApiImageState: (url: string) => ({
    src: url || undefined,
    loading: false,
    failed: url.includes("failed.png"),
  }),
}));
import type { DiffView } from "./diffViewPrefs";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// A removal followed by an addition, around numbers that grow from two
// digits to three: the rows whose code used to start left of the others.
const PATCH = [
  "--- a/README.md",
  "+++ b/README.md",
  "@@ -98,3 +99,3 @@",
  " keep",
  "-old",
  "+new",
  " tail",
].join("\n");

function renderView(view: DiffView) {
  return render(
    <StrictMode>
      <DiffFileSection
        file={{
          path: "README.md",
          status: "modified",
          additions: 1,
          deletions: 1,
          binary: false,
          truncated: false,
          before: "keep\nold\ntail",
          after: "keep\nnew\ntail",
        }}
        patch={PATCH}
        loading={false}
        error=""
        collapsed={false}
        view={view}
        onToggle={() => {}}
        registerRef={() => {}}
      />
    </StrictMode>,
  );
}

// Every number cell takes its width from the file, so the code of a removed
// or an added line (one cell blank) starts where the context's does. Where it
// lands is measured in a real engine by scripts/diff-gutter-check.mjs.
test("every number cell of a diff takes its width from the file's widest number", () => {
  for (const view of ["unified", "split"] as const) {
    const { container, unmount } = renderView(view);
    const diff = container.querySelector<HTMLElement>(".dv-diff");
    expect(diff, view).not.toBeNull();
    // The new column reaches 101 while the old one stops at 100.
    expect(diff!.style.getPropertyValue("--dv-num-digits"), view).toBe("3");
    expect(container.querySelectorAll(".dv-num").length, view).toBeGreaterThan(
      0,
    );
    unmount();
  }
});

test("a file header offers rich preview, Viewed and whole-file context controls", () => {
  const onViewedChange = vi.fn();
  render(
    <DiffFileSection
      file={{
        path: "README.md",
        status: "modified",
        additions: 1,
        deletions: 1,
        binary: false,
        truncated: false,
        patch: "@@ -1,3 +1,3 @@\n one\n-old\n+new\n three\n",
        before: "one\nold\nthree\n",
        after: "one\nnew\nthree\n",
        digest: "digest-a",
      }}
      patch={"@@ -1,3 +1,3 @@\n one\n-old\n+new\n three\n"}
      loading={false}
      error=""
      collapsed={false}
      onToggle={() => {}}
      view="unified"
      onDiscard={() => {}}
      registerRef={() => {}}
      viewed={false}
      onViewedChange={onViewedChange}
    />,
  );

  expect(
    screen.getByRole("button", { name: "Display the rich diff" }),
  ).toBeTruthy();
  expect(screen.getByRole("button", { name: "Expand all lines" })).toBeTruthy();
  const head = document.querySelector(".dv-file-head")!;
  const title = document.querySelector(".dv-file-title")!;
  const copy = document.querySelector('[data-testid="dv-copy-README.md"]')!;
  expect(title.nextElementSibling).toBe(copy);
  expect(copy.parentElement).toBe(head);
  const stat = head.querySelector(".dv-file-stat")!;
  const actions = stat.querySelector(".dv-file-actions");
  expect(actions).not.toBeNull();
  const children = [...stat.children];
  expect(children.indexOf(actions!)).toBeGreaterThan(
    children.findIndex((el) => el.classList.contains("changes-add")),
  );
  expect(children.indexOf(actions!)).toBeLessThan(
    children.findIndex((el) => el.classList.contains("dv-viewed")),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Viewed" }));
  expect(onViewedChange).toHaveBeenCalledWith(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Display the rich diff" }),
  );
  expect(
    document.querySelector(".dv-rich-version--after")?.textContent,
  ).toContain("new");
  expect(screen.queryByText("Before")).toBeNull();
  expect(screen.queryByText("After")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Expand all lines" }));
  expect(
    screen.getByRole("button", { name: "Display the source diff" }),
  ).toHaveAttribute("aria-pressed", "true");
});

// Whole-file context is reversible. Returning to the patch also discards any
// context revealed through the gap controls; it does not fold the file card.
test("collapse all lines restores the original hunks", () => {
  renderView("unified");
  fireEvent.click(screen.getByRole("button", { name: "Expand all lines" }));
  const collapse = screen.getByRole("button", { name: "Collapse all lines" });
  expect(collapse).toHaveAttribute("aria-pressed", "true");
  expect(collapse.querySelector("svg")).toHaveClass("dv-expand-lines-collapse");
  fireEvent.click(collapse);
  expect(screen.queryByText("98")).not.toBeNull();
  expect(screen.queryByText("97")).toBeNull();
  const expand = screen.getByRole("button", { name: "Expand all lines" });
  expect(expand).toHaveAttribute("aria-pressed", "false");
});

test("an image rich diff shows both versions and their byte sizes", () => {
  render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "pixel.png",
        status: "modified",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        patch: "",
        image: true,
        beforeImage: true,
        afterImage: true,
        beforeSize: 1024,
        afterSize: 2048,
        digest: "digest-a",
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      onToggle={() => {}}
      view="unified"
      onDiscard={() => {}}
      registerRef={() => {}}
    />,
  );

  expect(screen.queryByText(/\+0/)).toBeNull();
  expect(screen.getByText("1.02 KB")).toBeTruthy();
  expect(screen.getByText("2.05 KB")).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Display the source diff" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Display the rich diff" }),
  ).toBeNull();
  const images = screen.getAllByRole("img");
  expect(images).toHaveLength(2);
  expect(images[0]!.getAttribute("src")).toContain("side=before");
  expect(images[1]!.getAttribute("src")).toContain("side=after");
});

test("a rich preview never hides a detail error", () => {
  render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "pixel.png",
        status: "modified",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        image: true,
      }}
      patch=""
      loading={false}
      error="detail failed"
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  expect(screen.getByText("detail failed")).toBeTruthy();
  expect(document.querySelector(".dv-rich")).toBeNull();
});

test("a failed raw image request says so instead of leaving an empty frame", () => {
  render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "failed.png",
        status: "modified",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        image: true,
        beforeImage: true,
        afterImage: true,
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  expect(screen.getAllByText("The image could not be loaded")).toHaveLength(2);
});

test("a mixed image and text replacement renders each side by its own type", () => {
  render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "asset.png",
        status: "modified",
        additions: 1,
        deletions: 1,
        binary: false,
        truncated: false,
        image: true,
        beforeImage: true,
        afterImage: false,
        after: "now plain text",
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  expect(screen.getAllByRole("img")).toHaveLength(1);
  expect(screen.getByText("now plain text")).toBeTruthy();
});

test("context arrows add ten lines and the file control reveals every line", () => {
  const before = Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join(
    "\n",
  );
  render(
    <DiffFileSection
      file={{
        path: "notes.txt",
        status: "modified",
        additions: 1,
        deletions: 1,
        binary: false,
        truncated: false,
        before,
        after: before.replace("line 15", "changed 15"),
      }}
      patch={"@@ -15 +15 @@\n-line 15\n+changed 15"}
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );

  fireEvent.click(screen.getByRole("button", { name: "Show 10 lines above" }));
  expect(screen.getByText("line 5")).toBeTruthy();
  expect(screen.queryByText("line 1")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Expand all lines" }));
  expect(screen.getByText("line 1")).toBeTruthy();
  expect(screen.getByText("line 30")).toBeTruthy();
});

test("a binary file shows the old and new byte sizes", () => {
  render(
    <DiffFileSection
      file={{
        path: "archive.bin",
        status: "modified",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        beforeSize: 10,
        afterSize: 20,
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  expect(screen.getByText("10 B")).toBeTruthy();
  expect(screen.getByText("20 B")).toBeTruthy();
});

test("a deleted binary file shows only its old size and no image preview", () => {
  const { container } = render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "gone.bin",
        status: "deleted",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        beforeSize: 6,
        afterSize: 0,
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  expect(screen.getByText("6 B")).toBeTruthy();
  expect(container.querySelector(".changes-add")).toBeNull();
  expect(container.querySelector(".dv-rich")).toBeNull();
  expect(screen.getByText("Binary file: no line diff to show.")).toBeTruthy();
});

test("a deleted image renders only its before version", () => {
  render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "gone.png",
        status: "deleted",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        image: true,
        beforeImage: true,
        afterImage: false,
        beforeSize: 42,
        afterSize: 0,
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  expect(document.querySelectorAll(".dv-rich-version")).toHaveLength(1);
  expect(screen.getByText("Before")).toBeTruthy();
  expect(screen.queryByText("After")).toBeNull();
  expect(screen.getByRole("img")).toHaveAttribute(
    "src",
    "/coddy/sessions/s1/changes/file?path=gone.png&side=before&raw=1",
  );
});

test("an image reports its width and height after loading", () => {
  render(
    <DiffFileSection
      sessionId="s1"
      file={{
        path: "pixel.png",
        status: "added",
        additions: 0,
        deletions: 0,
        binary: true,
        truncated: false,
        image: true,
        afterImage: true,
      }}
      patch=""
      loading={false}
      error=""
      collapsed={false}
      view="unified"
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
  const image = screen.getByRole("img");
  Object.defineProperties(image, {
    naturalWidth: { value: 1280 },
    naturalHeight: { value: 720 },
  });
  fireEvent.load(image);
  const dimensions = screen.getByText("W 1280 · H 720");
  expect(image.parentElement).toHaveClass("dv-image-frame");
  expect(dimensions.parentElement).not.toBe(image.parentElement);
});

test("cancels copy feedback timer on unmount", async () => {
  vi.useFakeTimers();
  vi.spyOn(clipboard, "copyText").mockResolvedValue(true);
  const { getByTestId, unmount } = renderView("unified");
  await act(async () => {
    fireEvent.click(getByTestId("dv-copy-README.md"));
  });
  expect(vi.getTimerCount()).toBe(1);
  unmount();
  expect(vi.getTimerCount()).toBe(0);
});

test("does not schedule feedback when copying finishes after unmount", async () => {
  vi.useFakeTimers();
  let resolve!: (ok: boolean) => void;
  vi.spyOn(clipboard, "copyText").mockReturnValue(
    new Promise((r) => {
      resolve = r;
    }),
  );
  const timeout = vi.spyOn(window, "setTimeout");
  const { getByTestId, unmount } = renderView("unified");
  fireEvent.click(getByTestId("dv-copy-README.md"));
  unmount();
  await act(async () => {
    resolve(true);
  });
  expect(timeout).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0);
});

test("repeated copies reset the 1200 ms feedback timer", async () => {
  vi.useFakeTimers();
  vi.spyOn(clipboard, "copyText").mockResolvedValue(true);
  const { getByTestId } = renderView("unified");
  const button = getByTestId("dv-copy-README.md");
  await act(async () => {
    fireEvent.click(button);
  });
  act(() => {
    vi.advanceTimersByTime(800);
  });
  await act(async () => {
    fireEvent.click(button);
  });
  expect(vi.getTimerCount()).toBe(1);
  act(() => {
    vi.advanceTimersByTime(1199);
  });
  expect(button.textContent).toBe("✓");
  act(() => {
    vi.advanceTimersByTime(1);
  });
  expect(button.querySelector("svg")).not.toBeNull();
});
