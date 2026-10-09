import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ImageMenuHost } from "./ImageMenu";
import { ImageLightbox } from "./ImageLightbox";
import { t } from "../i18n/i18n";

/**
 * A right click on a picture of the app - a file of the workspace, an image
 * in the chat, a diagram, the full-screen viewer - offers to copy the picture
 * and to save it, the way a desktop app does. A picture from another site
 * keeps the browser's own menu.
 */

const PNG = new Blob(["\u0089PNG bytes"], { type: "image/png" });
let write: ReturnType<typeof vi.fn>;

class FakeClipboardItem {
  constructor(readonly items: Record<string, Blob | Promise<Blob>>) {}
}

beforeEach(() => {
  write = vi.fn(async (_items: unknown[]) => {});
  vi.stubGlobal("ClipboardItem", FakeClipboardItem);
  Object.defineProperty(navigator, "clipboard", {
    value: { write },
    configurable: true,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: unknown) =>
      String(input).startsWith("blob:")
        ? new Response(PNG, { headers: { "Content-Type": "image/png" } })
        : new Response("", { status: 404 }),
    ),
  );
  vi.stubGlobal(
    "URL",
    Object.assign(URL, {
      createObjectURL: () => "blob:http://localhost:3000/saved",
      revokeObjectURL: () => {},
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function picture(src = `blob:${location.origin}/picture`) {
  render(
    <>
      <ImageMenuHost />
      <img src={src} alt="A picture" data-image-name="logo.png" />
    </>,
  );
  return screen.getByAltText("A picture");
}

test("a right click on a picture of the app offers to copy it and to save it", () => {
  const img = picture();
  const browserMenu = fireEvent.contextMenu(img, { clientX: 40, clientY: 50 });
  expect(browserMenu).toBe(false);
  const menu = screen.getByRole("menu");
  expect(
    [...menu.querySelectorAll('[role="menuitem"]')].map((b) => b.textContent),
  ).toEqual([t("image.copy"), t("image.save")]);
});

test("Copy image puts the picture on the clipboard as a PNG", async () => {
  const img = picture();
  fireEvent.contextMenu(img);
  fireEvent.click(screen.getByRole("menuitem", { name: t("image.copy") }));
  await waitFor(() => expect(write).toHaveBeenCalledTimes(1));
  const item = (write.mock.calls[0]![0] as FakeClipboardItem[])[0]!;
  const png = await item.items["image/png"];
  expect(png?.type).toBe("image/png");
  expect(screen.queryByRole("menu")).toBeNull();
  await screen.findByText(t("image.copied"));
});

test("Save image downloads the picture under its name", async () => {
  const clicked: { download: string; href: string }[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    clicked.push({ download: this.download, href: this.href });
  });
  const img = picture();
  fireEvent.contextMenu(img);
  fireEvent.click(screen.getByRole("menuitem", { name: t("image.save") }));
  await waitFor(() => expect(clicked).toHaveLength(1));
  expect(clicked[0]).toEqual({
    download: "logo.png",
    href: "blob:http://localhost:3000/saved",
  });
});

test("a picture from another site keeps the browser's own menu", () => {
  const img = picture("https://example.test/remote.png");
  expect(fireEvent.contextMenu(img)).toBe(true);
  expect(screen.queryByRole("menu")).toBeNull();
});

test("without a clipboard that takes pictures, the menu offers to save only", () => {
  vi.stubGlobal("ClipboardItem", undefined);
  const img = picture();
  fireEvent.contextMenu(img);
  expect(screen.queryByRole("menuitem", { name: t("image.copy") })).toBeNull();
  screen.getByRole("menuitem", { name: t("image.save") });
});

test("a click beside the menu or Escape puts it away", () => {
  const img = picture();
  fireEvent.contextMenu(img);
  fireEvent.pointerDown(document.body);
  expect(screen.queryByRole("menu")).toBeNull();
  fireEvent.contextMenu(img);
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
});

// The full-screen viewer closes on Escape too; the menu over it goes first.
test("in the full-screen viewer Escape puts the menu away and leaves the picture open", async () => {
  const onClose = vi.fn();
  render(
    <>
      <ImageMenuHost />
      <ImageLightbox
        src={`blob:${location.origin}/full`}
        alt="Full picture"
        onClose={onClose}
      />
    </>,
  );
  const img = screen.getByAltText("Full picture");
  expect(fireEvent.contextMenu(img)).toBe(false);
  screen.getByRole("menu");
  await act(async () => {
    fireEvent.keyDown(document.activeElement || document.body, {
      key: "Escape",
    });
  });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(onClose).not.toHaveBeenCalled();
});

test("a picture inside something with a menu of its own keeps that menu", () => {
  render(
    <>
      <ImageMenuHost />
      <div onContextMenu={(e) => e.preventDefault()}>
        <img src={`blob:${location.origin}/card`} alt="Card picture" />
      </div>
    </>,
  );
  fireEvent.contextMenu(screen.getByAltText("Card picture"));
  expect(screen.queryByRole("menu")).toBeNull();
});
