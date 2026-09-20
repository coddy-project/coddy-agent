import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ImageLightbox,
  MAX_ZOOM,
  ZOOM_LEVELS,
  fittedScale,
  movedPastSlop,
  panScroll,
  pinchZoom,
  touchMidpoint,
  touchSpan,
  wheelPixels,
  wheelZoom,
  zoomIn,
  zoomOut,
} from "./ImageLightbox";

afterEach(cleanup);

// jsdom draws nothing, so the arithmetic of a drag and a pinch is tested on
// its own: what the component adds on top is which numbers it reads.
describe("ImageLightbox gesture arithmetic", () => {
  it("pans the stage against the pointer and stops at the edges", () => {
    const max = { left: 400, top: 200 };
    // The picture follows the hand, so the stage scrolls the other way.
    expect(panScroll({ left: 100, top: 100 }, -30, -40, max)).toEqual({ left: 130, top: 140 });
    expect(panScroll({ left: 100, top: 100 }, 300, 300, max)).toEqual({ left: 0, top: 0 });
    expect(panScroll({ left: 100, top: 100 }, -1000, -1000, max)).toEqual({ left: 400, top: 200 });
    // Nothing to scroll: a picture smaller than the stage stays put.
    expect(panScroll({ left: 0, top: 0 }, 60, 60, { left: 0, top: 0 })).toEqual({
      left: 0,
      top: 0,
    });
  });

  it("counts a press that barely moved as a click, not a drag", () => {
    expect(movedPastSlop(0, 0)).toBe(false);
    expect(movedPastSlop(3, 0)).toBe(false);
    expect(movedPastSlop(0, 9)).toBe(true);
    expect(movedPastSlop(-5, 4)).toBe(true);
  });

  it("takes a pinch from the point between the two fingers", () => {
    expect(touchMidpoint({ x: 10, y: 20 }, { x: 30, y: 60 })).toEqual({ x: 20, y: 40 });
    expect(touchSpan({ x: 0, y: 0 }, { x: 3, y: 4 })).toBe(5);
  });

  it("carries a pinch as a factor between the fitted size and the top level", () => {
    // Fitted at 40% of the picture's own size, the fingers spread by half.
    expect(pinchZoom("fit", 0.4, 100, 150)).toBe(0.6);
    // Spreading past the top of the ladder stops there.
    expect(pinchZoom(1, 1, 100, 400)).toBe(MAX_ZOOM);
    // Closing the fingers back to the fitted size returns to fit.
    expect(pinchZoom(2, 0.4, 200, 20)).toBe("fit");
    // Between two levels the factor is kept, not snapped to a rung.
    expect(pinchZoom(1, 1, 100, 173)).toBe(1.73);
    // No span to compare against leaves the zoom where it was.
    expect(pinchZoom(1.5, 1, 0, 120)).toBe(1.5);
  });

  it("reads a wheel in pixels, whatever the device counts in", () => {
    // Pixels as they come, lines and pages as the pixels they stand for.
    expect(wheelPixels(120, 0)).toBe(120);
    expect(wheelPixels(-3, 1)).toBe(-3 * 16);
    expect(wheelPixels(1, 2)).toBe(400);
    expect(wheelPixels(Number.NaN, 0)).toBe(0);
  });

  it("zooms the wheel continuously, from the fitted size and up to the top level", () => {
    // One notch of a mouse is a step of about 15%, a trackpad's crumbs less.
    expect(wheelZoom(1, 1, -100)).toBe(1.15);
    expect(wheelZoom(1, 1, -10)).toBe(1.01);
    // Fitted at half, a wheel up starts growing from there.
    expect(wheelZoom("fit", 0.5, -100)).toBe(0.57);
    // Fitted, a wheel down has nowhere to go.
    expect(wheelZoom("fit", 0.5, 100)).toBe("fit");
    // Down from just above the fitted size lands back on fit, not below it.
    expect(wheelZoom(0.6, 0.5, 400)).toBe("fit");
    // Up past the top of the ladder stops there.
    expect(wheelZoom(2.5, 1, -5000)).toBe(MAX_ZOOM);
    // A wheel that reported nothing changes nothing.
    expect(wheelZoom(1.5, 1, 0)).toBe(1.5);
  });

  it("meets the ladder from a factor a pinch left behind", () => {
    expect(ZOOM_LEVELS).toEqual([1, 1.5, 2, 3]);
    // The rungs behave as they always did.
    expect(zoomIn("fit")).toBe(1);
    expect(zoomIn(1)).toBe(1.5);
    expect(zoomIn(1.5)).toBe(2);
    expect(zoomIn(2)).toBe(3);
    expect(zoomIn(3)).toBe(3);
    expect(zoomOut("fit")).toBe("fit");
    expect(zoomOut(3)).toBe(2);
    expect(zoomOut(1.5)).toBe(1);
    expect(zoomOut(1)).toBe("fit");
    // A pinch stopped at 173%: a button takes the next rung either way.
    expect(zoomIn(1.73)).toBe(2);
    expect(zoomOut(1.73)).toBe(1.5);
    // A pinch below the lowest rung: in goes to it, out goes back to fit.
    expect(zoomIn(0.6)).toBe(1);
    expect(zoomOut(0.6)).toBe("fit");
  });

  it("reads the fitted size as a fraction of the picture's own", () => {
    expect(fittedScale(400, 1000)).toBe(0.4);
    expect(fittedScale(1000, 1000)).toBe(1);
    // Nothing measured yet, and a picture never drawn larger than itself.
    expect(fittedScale(0, 1000)).toBe(1);
    expect(fittedScale(1200, 1000)).toBe(1);
  });
});

function open() {
  const onClose = vi.fn();
  render(<ImageLightbox src="/p.png" alt="A picture" onClose={onClose} />);
  const dialog = screen.getByRole("dialog");
  return {
    onClose,
    dialog,
    img: dialog.querySelector("img")!,
    stage: dialog.querySelector(".docs-lightbox-stage") as HTMLElement,
  };
}

// jsdom carries no PointerEvent, and testing-library then builds a bare Event
// that drops the coordinates: a MouseEvent under the pointer event's name
// carries everything the viewer reads.
function pointer(
  type: "pointerdown" | "pointermove" | "pointerup" | "pointercancel",
  el: Element,
  at: { id: number; kind: "mouse" | "touch"; x: number; y: number },
) {
  const e = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: at.x,
    clientY: at.y,
  });
  Object.defineProperty(e, "pointerId", { value: at.id });
  Object.defineProperty(e, "pointerType", { value: at.kind });
  fireEvent(el, e);
}

const mouse = (x: number, y: number) => ({ id: 1, kind: "mouse" as const, x, y });
const finger = (id: number, x: number, y: number) => ({ id, kind: "touch" as const, x, y });

describe("ImageLightbox gestures", () => {
  it("pans a zoomed picture on a press that travelled, and swallows its click", () => {
    const { dialog, img, stage, onClose } = open();
    fireEvent.click(screen.getByTestId("docs-lightbox-zoom-in"));
    expect(dialog.getAttribute("data-zoom")).toBe("1");

    pointer("pointerdown", img, mouse(200, 200));
    expect(stage.className).toContain("is-panning");
    pointer("pointermove", img, mouse(260, 240));
    pointer("pointerup", img, mouse(260, 240));
    expect(stage.className).not.toContain("is-panning");
    // The browser still fires the click of that press: it is not a zoom.
    fireEvent.click(img);
    expect(dialog.getAttribute("data-zoom")).toBe("1");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("keeps the click that toggles the zoom for a press that stayed put", () => {
    const { dialog, img } = open();
    fireEvent.click(screen.getByTestId("docs-lightbox-zoom-in"));
    pointer("pointerdown", img, mouse(200, 200));
    pointer("pointermove", img, mouse(201, 200));
    pointer("pointerup", img, mouse(201, 200));
    fireEvent.click(img);
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
    // And from fitted, the next press without travel zooms in again.
    pointer("pointerdown", img, mouse(200, 200));
    pointer("pointerup", img, mouse(200, 200));
    fireEvent.click(img);
    expect(dialog.getAttribute("data-zoom")).toBe("2");
  });

  it("does not close the viewer when a drag ends beside the picture", () => {
    const { img, stage, onClose } = open();
    fireEvent.click(screen.getByTestId("docs-lightbox-zoom-in"));
    pointer("pointerdown", img, mouse(200, 200));
    pointer("pointermove", img, mouse(20, 200));
    pointer("pointerup", stage, mouse(20, 200));
    fireEvent.click(stage);
    expect(onClose).not.toHaveBeenCalled();
    // A click beside the picture that is a click still closes it.
    fireEvent.click(stage);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("zooms with two fingers and returns to fit when they close again", () => {
    const { dialog, img } = open();
    pointer("pointerdown", img, finger(1, 100, 100));
    pointer("pointerdown", img, finger(2, 300, 100));
    pointer("pointermove", img, finger(2, 500, 100));
    expect(dialog.getAttribute("data-zoom")).toBe("2");
    pointer("pointermove", img, finger(2, 200, 100));
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
    pointer("pointerup", img, finger(2, 200, 100));
    pointer("pointerup", img, finger(1, 100, 100));
    // The pinch is not the click that toggles the zoom.
    fireEvent.click(img);
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
  });

  it("zooms on the wheel and keeps the page behind the viewer still", () => {
    const { dialog, stage } = open();
    const wheel = (deltaY: number, deltaMode = 0) => {
      const e = new WheelEvent("wheel", {
        deltaY,
        deltaMode,
        clientX: 200,
        clientY: 200,
        bubbles: true,
        cancelable: true,
      });
      fireEvent(stage, e);
      return e;
    };
    // The listener is not passive: the page behind the viewer stays put.
    expect(wheel(-100).defaultPrevented).toBe(true);
    expect(dialog.getAttribute("data-zoom")).toBe("1.15");
    wheel(-100);
    expect(dialog.getAttribute("data-zoom")).toBe("1.32");
    // Back down, and it stops at fit rather than shrinking past it.
    wheel(400);
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
    wheel(400);
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
  });
});
