import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";

import { setEnv } from "../env/remoteEnv";
import { parseToolArtifacts } from "../chat/toolArtifacts";
import { ToolCallMessage } from "./ToolCallMessage";
import { ArtifactCard, placeArtifactMenu } from "./ToolArtifactCards";

const artifact = {
  id: "artifact-1",
  name: "release-notes.pdf",
  sha256: "a".repeat(64),
  size: 2048,
  url: "/coddy/sessions/s1/artifacts/artifact-1",
};
const unavailableArtifact = (({ url: _url, ...rest }) => rest)(artifact);

afterEach(() => {
  cleanup();
  setEnv({ mode: "local" });
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

test("normalizes artifact metadata and retains a missing download URL as unavailable", () => {
  expect(
    parseToolArtifacts([artifact, { ...artifact, id: "gone", url: "" }]),
  ).toEqual([artifact, { ...artifact, id: "gone", url: undefined }]);
  expect(parseToolArtifacts([{ ...artifact, sha256: "not-a-digest" }])).toEqual(
    [],
  );
});

test("renders completed share_file artifacts below the closed disclosure", () => {
  render(
    <ToolCallMessage
      toolCallId="share-1"
      title="share_file"
      status="completed"
      artifacts={[artifact]}
    />,
  );

  expect(screen.getByTestId("tool-artifact-card-artifact-1")).toBeVisible();
  expect(screen.getByText("release-notes.pdf")).toBeVisible();
  expect(screen.getByText("PDF · 2.0 KB")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Download release-notes.pdf" }),
  ).toBeNull();
  expect(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  ).toBeVisible();
  expect(screen.getByTestId("tool-details-share-1")).not.toHaveAttribute(
    "open",
  );
});

test("does not render artifacts for a pending or unrelated tool call", () => {
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="share-2"
      title="share_file"
      status="in_progress"
      artifacts={[artifact]}
    />,
  );
  expect(screen.queryByTestId("tool-artifact-card-artifact-1")).toBeNull();

  rerender(
    <ToolCallMessage
      toolCallId="other-1"
      title="write"
      status="completed"
      artifacts={[artifact]}
    />,
  );
  expect(screen.queryByTestId("tool-artifact-card-artifact-1")).toBeNull();
});

test("marks an artifact without a URL as unavailable", () => {
  render(
    <ToolCallMessage
      toolCallId="share-3"
      title="share_file"
      status="completed"
      artifacts={[unavailableArtifact]}
    />,
  );

  expect(screen.getByText("Download unavailable")).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  expect(screen.getByRole("menuitem", { name: "Download" })).toBeDisabled();
});

test("opens an inline image artifact in the shared lightbox", () => {
  const { container } = render(
    <ArtifactCard
      inline
      artifact={{
        ...artifact,
        name: "release-overview.png",
        previewUrl: "/coddy/sessions/s1/artifacts/artifact-1/preview",
      }}
    />,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Open release-overview.png" }),
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("release-overview.png");
  expect(
    screen.getByRole("img", { name: "release-overview.png" }),
  ).toHaveAttribute("src", "/coddy/sessions/s1/artifacts/artifact-1/preview");
  expect(
    container.querySelector(".inline-artifact-extension")?.parentElement,
  ).toHaveClass("tool-artifact-card");
});

test("opens the actions menu outside the card, which clips what overflows it", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const card = screen.getByTestId("inline-artifact-card-artifact-1");
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });

  fireEvent.click(trigger);
  const menu = screen.getByRole("menu");
  expect(card).not.toContainElement(menu);
  expect(menu.parentElement).toBe(document.body);
  expect(trigger).toHaveAttribute("aria-expanded", "true");
  expect(trigger).toHaveAttribute("aria-haspopup", "menu");
  expect(screen.getAllByRole("menuitem")).toHaveLength(6);
  // The first item that can be picked takes the focus, so the keyboard starts
  // inside the menu (Mention source is disabled: this file names no source).
  expect(
    screen.getByRole("menuitem", { name: "Mention source" }),
  ).toBeDisabled();
  expect(screen.getByRole("menuitem", { name: "Copy name" })).toHaveFocus();
});

test("draws the trigger's dots as an svg, not a text glyph", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  expect(trigger.querySelector("svg")).not.toBeNull();
  expect(trigger.textContent).toBe("");
});

test("the trigger closes the menu it opened", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });

  fireEvent.click(trigger);
  expect(screen.getByRole("menu")).toBeVisible();
  // A real click starts with a press on the trigger, which is outside the menu.
  fireEvent.mouseDown(trigger);
  fireEvent.click(trigger);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveAttribute("aria-expanded", "false");
});

test("a press outside the card and the menu closes the menu", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole("menu")).toBeNull();
});

test("Escape closes the menu wherever the focus is and hands it back to the trigger", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  fireEvent.click(trigger);
  trigger.focus();

  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveFocus();
});

test("the arrow keys move between the items that can be picked", () => {
  render(
    <ArtifactCard
      inline
      artifact={{
        ...unavailableArtifact,
        sourcePath: "/work/release-notes.pdf",
      }}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  const menu = screen.getByRole("menu");
  expect(
    screen.getByRole("menuitem", { name: "Mention source" }),
  ).toHaveFocus();

  fireEvent.keyDown(menu, { key: "ArrowDown" });
  expect(screen.getByRole("menuitem", { name: "Copy name" })).toHaveFocus();
  // Download is disabled without a URL and Reveal without a reveal URL, so up
  // from the first item wraps to Copy absolute path.
  fireEvent.keyDown(menu, { key: "ArrowUp" });
  fireEvent.keyDown(menu, { key: "ArrowUp" });
  expect(screen.getByRole("menuitem", { name: "Download" })).toBeDisabled();
  expect(
    screen.getByRole("menuitem", { name: "Copy absolute path" }),
  ).toHaveFocus();
  fireEvent.keyDown(menu, { key: "Home" });
  expect(
    screen.getByRole("menuitem", { name: "Mention source" }),
  ).toHaveFocus();
});

test("the menu follows its trigger while the page scrolls and closes once the trigger has left the window", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  const at = (top: number) =>
    vi
      .spyOn(trigger, "getBoundingClientRect")
      .mockReturnValue(
        DOMRect.fromRect({ x: 272, y: top, width: 28, height: 28 }),
      );

  at(100);
  fireEvent.click(trigger);
  expect(screen.getByRole("menu")).toHaveStyle({ top: "134px" });

  at(400);
  fireEvent.scroll(window);
  expect(screen.getByRole("menu")).toHaveStyle({ top: "434px" });

  at(-60);
  fireEvent.scroll(window);
  expect(screen.queryByRole("menu")).toBeNull();
});

test("places the menu under the trigger, above it at the window's foot, and inside the window", () => {
  const view = { width: 390, height: 720 };
  const menu = { width: 200, height: 238 };
  // Under the trigger, right edges together.
  expect(
    placeArtifactMenu({ top: 100, right: 360, bottom: 128 }, menu, view),
  ).toEqual({ left: 160, top: 134 });
  // A trigger at a phone's left edge keeps the menu on the screen.
  expect(
    placeArtifactMenu({ top: 100, right: 130, bottom: 128 }, menu, view),
  ).toEqual({ left: 8, top: 134 });
  // No room below: above the trigger.
  expect(
    placeArtifactMenu({ top: 650, right: 360, bottom: 678 }, menu, view),
  ).toEqual({ left: 160, top: 406 });
  // No room on either side: as low as the window allows.
  expect(
    placeArtifactMenu({ top: 120, right: 360, bottom: 148 }, menu, {
      width: 390,
      height: 300,
    }),
  ).toEqual({
    left: 160,
    top: 54,
  });
});

test("downloads a local artifact through a direct anchor without fetching", () => {
  const fetchSpy = vi.spyOn(window, "fetch");
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  render(
    <ToolCallMessage
      toolCallId="share-local"
      title="share_file"
      status="completed"
      artifacts={[artifact]}
    />,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.click(screen.getByRole("menuitem", { name: "Download" }));
  expect(click).toHaveBeenCalledTimes(1);
  expect(fetchSpy).not.toHaveBeenCalled();
});

test("downloads a relay-mounted artifact through the environment request and releases its blob URL", async () => {
  setEnv({
    mode: "remote",
    baseUrl: "https://relay.example/swarm/nodes/nas02",
    token: "secret",
  });
  const fetchSpy = vi
    .spyOn(window, "fetch")
    .mockResolvedValue(new Response(new Blob(["artifact"]), { status: 200 }));
  const createObjectURL = vi.fn(() => "blob:artifact-download");
  const revokeObjectURL = vi.fn();
  vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL });
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});

  render(
    <ToolCallMessage
      toolCallId="share-4"
      title="share_file"
      status="completed"
      artifacts={[artifact]}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.click(screen.getByRole("menuitem", { name: "Download" }));

  await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1));
  expect(fetchSpy.mock.calls[0]?.[0]).toBe(
    "https://relay.example/swarm/nodes/nas02" + artifact.url,
  );
  expect(
    new Headers(fetchSpy.mock.calls[0]?.[1]?.headers).get("Authorization"),
  ).toBe("Bearer secret");
  expect(click).toHaveBeenCalledTimes(1);
  await waitFor(() =>
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:artifact-download"),
  );
});
