import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ImageMarkupDialog, markedUpName } from "./ImageMarkupDialog";
import { installMarkupFakes } from "./markupFakes";

let fakes: ReturnType<typeof installMarkupFakes>;

beforeEach(() => {
  fakes = installMarkupFakes({ width: 400, height: 300 });
});

afterEach(() => {
  cleanup();
  fakes.restore();
});

// jsdom has no PointerEvent: a MouseEvent under the pointer event's name
// carries the coordinates the editor reads. The canvas has no layout either,
// so a client pixel is a picture pixel.
function pointer(type: string, el: Element, x: number, y: number) {
  const e = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: x,
    clientY: y,
  });
  Object.defineProperty(e, "pointerId", { value: 1 });
  fireEvent(el, e);
}

function drag(el: Element, from: [number, number], to: [number, number]) {
  pointer("pointerdown", el, ...from);
  pointer("pointermove", el, ...to);
  pointer("pointerup", el, ...to);
}

async function open(onDone = vi.fn(), onCancel = vi.fn()) {
  const file = new File(["raw"], "pasted-1.jpg", { type: "image/jpeg" });
  render(<ImageMarkupDialog file={file} onDone={onDone} onCancel={onCancel} />);
  const canvas = await screen.findByTestId("markup-canvas");
  return { canvas, onDone, onCancel };
}

describe("ImageMarkupDialog", () => {
  it("opens the picture with the pencil and red chosen and the tools of the console's window", async () => {
    const { canvas } = await open();
    expect(canvas).toHaveAttribute("width", "400");
    expect(canvas).toHaveAttribute("height", "300");
    for (const tool of ["pen", "ellipse", "rect", "arrow", "text", "crop"]) {
      expect(screen.getByTestId(`markup-tool-${tool}`)).toHaveAttribute(
        "aria-pressed",
        tool === "pen" ? "true" : "false",
      );
    }
    for (const c of [
      "red",
      "orange",
      "yellow",
      "green",
      "blue",
      "white",
      "black",
    ]) {
      expect(screen.getByTestId(`markup-color-${c}`)).toHaveAttribute(
        "aria-pressed",
        c === "red" ? "true" : "false",
      );
    }
    expect(screen.getByTestId("markup-undo")).toBeDisabled();
    expect(
      screen.getByRole("dialog", { name: "Mark up the picture" }),
    ).toBeInTheDocument();
  });

  it("draws with the chosen tool and colour, undoes, and hands back a PNG on Done", async () => {
    const { canvas, onDone } = await open();
    fireEvent.click(screen.getByTestId("markup-tool-rect"));
    fireEvent.click(screen.getByTestId("markup-color-blue"));
    drag(canvas, [20, 20], [120, 90]);
    expect(screen.getByTestId("markup-undo")).toBeEnabled();
    fireEvent.click(screen.getByTestId("markup-undo"));
    expect(screen.getByTestId("markup-undo")).toBeDisabled();
    drag(canvas, [30, 30], [130, 100]);
    fakes.calls.length = 0;
    fireEvent.click(screen.getByTestId("markup-done"));
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    const result = onDone.mock.calls[0]![0] as File;
    expect(result.name).toBe("pasted-1.png");
    expect(result.type).toBe("image/png");
    const rects = fakes.calls.filter((c) => c.op === "strokeRect");
    expect(rects).toHaveLength(1);
    expect(rects[0]!.args).toEqual([30, 30, 100, 70]);
  });

  it("a click with a shape tool draws nothing", async () => {
    const { canvas } = await open();
    fireEvent.click(screen.getByTestId("markup-tool-arrow"));
    drag(canvas, [50, 50], [51, 51]);
    expect(screen.getByTestId("markup-undo")).toBeDisabled();
  });

  it("types a label in any script and commits it with Enter", async () => {
    const { canvas, onDone } = await open();
    fireEvent.keyDown(screen.getByTestId("markup-dialog"), { key: "t" });
    expect(screen.getByTestId("markup-tool-text")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    pointer("pointerdown", canvas, 40, 50);
    const input = await screen.findByTestId("markup-label-input");
    fireEvent.change(input, { target: { value: "Привет" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.queryByTestId("markup-label-input")).toBeNull();
    fakes.calls.length = 0;
    fireEvent.click(screen.getByTestId("markup-done"));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    const label = fakes.calls.find((c) => c.op === "fillText");
    expect(label?.args).toEqual(["Привет", 40, 50]);
  });

  it("a press on the picture keeps the focus where the editor put it", async () => {
    const { canvas } = await open();
    fireEvent.click(screen.getByTestId("markup-tool-text"));
    const e = new MouseEvent("pointerdown", {
      bubbles: true,
      cancelable: true,
      button: 0,
      clientX: 5,
      clientY: 5,
    });
    Object.defineProperty(e, "pointerId", { value: 1 });
    fireEvent(canvas, e);
    // A browser moves the focus on the mousedown a press is followed by
    // unless the press was cancelled; the label's field must keep it.
    expect(e.defaultPrevented).toBe(true);
    expect(await screen.findByTestId("markup-label-input")).toHaveFocus();
  });

  it("Escape in a label drops the label and keeps the editor open", async () => {
    const { canvas, onCancel } = await open();
    fireEvent.click(screen.getByTestId("markup-tool-text"));
    pointer("pointerdown", canvas, 40, 50);
    const input = await screen.findByTestId("markup-label-input");
    fireEvent.change(input, { target: { value: "draft" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByTestId("markup-label-input")).toBeNull();
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.getByTestId("markup-undo")).toBeDisabled();
  });

  it("crops and turns the picture", async () => {
    const { canvas } = await open();
    fireEvent.click(screen.getByTestId("markup-tool-crop"));
    drag(canvas, [100, 50], [300, 150]);
    expect(canvas).toHaveAttribute("width", "200");
    expect(canvas).toHaveAttribute("height", "100");
    fireEvent.click(screen.getByTestId("markup-rotate"));
    expect(canvas).toHaveAttribute("width", "100");
    expect(canvas).toHaveAttribute("height", "200");
  });

  it("Cancel and Escape hand back nothing", async () => {
    const { onCancel, onDone } = await open();
    fireEvent.click(screen.getByTestId("markup-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
    act(() => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onDone).not.toHaveBeenCalled();
  });

  it("Enter is Done", async () => {
    const { onDone } = await open();
    fireEvent.keyDown(screen.getByTestId("markup-dialog"), { key: "Enter" });
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
  });

  it("Enter on a focused button is that button's press, not Done", async () => {
    const { onDone } = await open();
    const cancel = screen.getByTestId("markup-cancel");
    cancel.focus();
    const e = fireEvent.keyDown(cancel, { key: "Enter" });
    expect(e).toBe(true); // not prevented: the button gets its own press
    await new Promise((r) => setTimeout(r, 0));
    expect(onDone).not.toHaveBeenCalled();
  });

  it("a press on a tool keeps the focus on the editor, so Enter stays Done", async () => {
    const { onDone } = await open();
    const rect = screen.getByTestId("markup-tool-rect");
    expect(fireEvent.mouseDown(rect)).toBe(false);
    fireEvent.click(rect);
    fireEvent.keyDown(screen.getByTestId("markup-dialog"), { key: "Enter" });
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
  });

  it("Cancel while the PNG is being encoded hands nothing back", async () => {
    let finish: (b: Blob) => void = () => {};
    const encode = () => new Promise<Blob>((r) => (finish = r));
    const onDone = vi.fn();
    const onCancel = vi.fn();
    const file = new File(["raw"], "pasted-1.png", { type: "image/png" });
    render(
      <ImageMarkupDialog
        file={file}
        onDone={onDone}
        onCancel={onCancel}
        encode={encode}
      />,
    );
    await screen.findByTestId("markup-canvas");
    fireEvent.click(screen.getByTestId("markup-done"));
    fireEvent.click(screen.getByTestId("markup-cancel"));
    await act(async () => finish(new Blob(["png"], { type: "image/png" })));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onDone).not.toHaveBeenCalled();
  });

  it("shortcuts go by the key, so a Russian layout and caps lock keep them", async () => {
    const { canvas } = await open();
    const root = screen.getByTestId("markup-dialog");
    fireEvent.keyDown(root, { key: "к", code: "KeyR" });
    expect(screen.getByTestId("markup-tool-rect")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    fireEvent.keyDown(root, { key: "A", code: "KeyA" });
    expect(screen.getByTestId("markup-tool-arrow")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    drag(canvas, [10, 10], [120, 90]);
    expect(screen.getByTestId("markup-undo")).toBeEnabled();
    fireEvent.keyDown(root, { key: "я", code: "KeyZ", ctrlKey: true });
    expect(screen.getByTestId("markup-undo")).toBeDisabled();
  });

  it("Tab stays inside the editor", async () => {
    await open();
    const root = screen.getByTestId("markup-dialog");
    const done = screen.getByTestId("markup-done");
    const cancel = screen.getByTestId("markup-cancel");
    done.focus();
    fireEvent.keyDown(done, { key: "Tab" });
    expect(cancel).toHaveFocus();
    fireEvent.keyDown(cancel, { key: "Tab", shiftKey: true });
    expect(done).toHaveFocus();
    root.focus();
    fireEvent.keyDown(root, { key: "Tab", shiftKey: true });
    expect(done).toHaveFocus();
  });

  it("names the result after the original", () => {
    expect(markedUpName("shot.jpeg")).toBe("shot.png");
    expect(markedUpName("pasted-3.png")).toBe("pasted-3.png");
    expect(markedUpName("")).toBe("picture.png");
  });
});
