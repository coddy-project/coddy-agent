import { useState } from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => cleanup());

/** A file the browser holds by its path: reading it is what can fail. */
function diskFile(name: string, read: () => Promise<ArrayBuffer>): File {
  const file = new File(["img"], name, { type: "image/png" });
  Object.defineProperty(file, "arrayBuffer", { value: read });
  return file;
}

const bytes = () => Promise.resolve(new TextEncoder().encode("img").buffer);

function Harness(props: { onFiles: (files: File[]) => void }) {
  const [files, setFiles] = useState<File[]>([]);
  props.onFiles(files);
  return (
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      attachedFiles={files}
      onAttachedFilesChange={setFiles}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />
  );
}

function dropOn(files: File[]) {
  const card = document.querySelector(".composer-card");
  expect(card).not.toBeNull();
  fireEvent.drop(card!, { dataTransfer: { files } });
}

test("a dropped file is replaced by a copy held in memory", async () => {
  const source = diskFile("capture.png", bytes);
  let current: File[] = [];
  render(<Harness onFiles={(f) => (current = f)} />);
  dropOn([source]);
  expect(screen.getAllByTestId("composer-attachment-chip")).toHaveLength(1);
  await waitFor(() => expect(current[0]).not.toBe(source));
  expect(current[0]?.name).toBe("capture.png");
  expect(current[0]?.type).toBe("image/png");
});

test("files dropped one after another keep their order whichever is read first", async () => {
  let releaseFirst: (b: ArrayBuffer) => void = () => {};
  const slow = diskFile(
    "first.png",
    () =>
      new Promise((resolve) => {
        releaseFirst = resolve;
      }),
  );
  const fast = diskFile("second.png", bytes);
  let current: File[] = [];
  render(<Harness onFiles={(f) => (current = f)} />);
  dropOn([slow]);
  dropOn([fast]);
  await waitFor(() => expect(current[1]).not.toBe(fast));
  expect(current.map((f) => f.name)).toEqual(["first.png", "second.png"]);
  releaseFirst(new TextEncoder().encode("img").buffer);
  await waitFor(() => expect(current[0]).not.toBe(slow));
  expect(current.map((f) => f.name)).toEqual(["first.png", "second.png"]);
});

test("a file that cannot be read leaves the others and says which it was", async () => {
  const gone = diskFile("gone.png", () =>
    Promise.reject(new DOMException("gone", "NotReadableError")),
  );
  const kept = diskFile("kept.png", bytes);
  let current: File[] = [];
  render(<Harness onFiles={(f) => (current = f)} />);
  dropOn([kept, gone]);
  await waitFor(() => expect(current.map((f) => f.name)).toEqual(["kept.png"]));
  expect(screen.getByTestId("composer-attach-hint").textContent).toContain(
    "gone.png",
  );
});

test("a file removed before its copy arrives does not come back", async () => {
  let release: (b: ArrayBuffer) => void = () => {};
  const slow = diskFile(
    "removed.png",
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  let current: File[] = [];
  render(<Harness onFiles={(f) => (current = f)} />);
  dropOn([slow]);
  fireEvent.click(
    screen.getByRole("button", { name: /Remove attachment|removed\.png/i }),
  );
  await waitFor(() => expect(current).toHaveLength(0));
  release(new TextEncoder().encode("img").buffer);
  await vi.waitFor(() => expect(current).toHaveLength(0));
});
