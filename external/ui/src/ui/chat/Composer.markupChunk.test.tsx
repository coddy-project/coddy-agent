import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { Composer } from "./Composer";
import { installMarkupFakes } from "../markup/markupFakes";

// A tab left open across an update asks for a chunk the new binary does not
// serve: the editor cannot load, and a pasted picture is attached as it is.
vi.mock("../markup/ImageMarkupDialog", () => {
  throw new Error("the chunk is gone");
});

afterEach(() => cleanup());

test("a pasted picture is attached as it is when the editor cannot load", async () => {
  const fakes = installMarkupFakes();
  try {
    render(
      <Composer
        value=""
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        llmModelMultimodal={true}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    const ta = screen.getByRole("textbox", { name: "Message" });
    const ev = new Event("paste", { bubbles: true, cancelable: true });
    const file = new File(["img"], "image.png", { type: "image/png" });
    Object.defineProperty(ev, "clipboardData", {
      value: {
        items: [{ kind: "file", type: file.type, getAsFile: () => file }],
      },
    });
    fireEvent(ta, ev);
    await waitFor(() =>
      expect(screen.getByText("pasted-1.png")).toBeInTheDocument(),
    );
    expect(screen.queryByTestId("markup-dialog")).toBeNull();
  } finally {
    fakes.restore();
  }
});
