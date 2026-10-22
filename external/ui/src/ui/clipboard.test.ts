import { readdirSync, readFileSync } from "node:fs";
import { join, relative, win32 } from "node:path";
import ts from "typescript";
import { afterEach, expect, onTestFinished, test, vi } from "vitest";
import { copyText } from "./clipboard";
import { pageWithoutClipboardApi, selectedOnce } from "./clipboardTestKit";

const TEXT = "line one\nline two\n\tindented, with “quotes” and emoji 🚀";

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, "clipboard", {
    value,
    configurable: true,
    writable: true,
  });
}

afterEach(() => {
  setClipboard(undefined);
  document.body.innerHTML = "";
});

test("a clipboard API takes the text and the page does not copy by itself", async () => {
  const page = pageWithoutClipboardApi();
  const writeText = vi.fn(async (_text: string) => {});
  setClipboard({ writeText });
  expect(await copyText(TEXT)).toBe(true);
  expect(writeText).toHaveBeenCalledExactlyOnceWith(TEXT);
  expect(page.exec).not.toHaveBeenCalled();
});

test("without a clipboard API it copies inside the tap, from a read-only 16px selection", async () => {
  const page = pageWithoutClipboardApi();
  // iOS selects nothing on select(); only setSelectionRange does it there.
  const select = vi.spyOn(HTMLTextAreaElement.prototype, "setSelectionRange");
  onTestFinished(() => select.mockRestore());
  const pending = copyText(TEXT);
  // Before the first await: a tap's user activation does not outlive it on iOS.
  expect(page.exec).toHaveBeenCalledExactlyOnceWith("copy");
  expect(await pending).toBe(true);
  expect(select).toHaveBeenCalledExactlyOnceWith(0, TEXT.length);
  expect(page.copies).toEqual(selectedOnce(TEXT));
});

test("the text area is out of the way: fixed, invisible, gone afterwards", async () => {
  let area: HTMLTextAreaElement | undefined;
  const page = pageWithoutClipboardApi();
  page.exec.mockImplementationOnce((_command: string) => {
    area = document.querySelector("textarea") ?? undefined;
    return true;
  });
  await copyText(TEXT);
  expect(area?.isConnected).toBe(false);
  expect(area?.style.position).toBe("fixed");
  expect(area?.style.opacity).toBe("0");
  expect(document.querySelector("textarea")).toBeNull();
});

test("the focus goes back to what had it", async () => {
  const button = document.body.appendChild(document.createElement("button"));
  button.focus();
  let focusedDuring: Element | null = null;
  const page = pageWithoutClipboardApi();
  page.exec.mockImplementationOnce((_command: string) => {
    focusedDuring = document.activeElement;
    return true;
  });
  await copyText("x");
  expect((focusedDuring as unknown as Element).tagName).toBe("TEXTAREA");
  expect(document.activeElement).toBe(button);
});

test("a page that cannot copy reports a failure and cleans up", async () => {
  const button = document.body.appendChild(document.createElement("button"));
  button.focus();
  const page = pageWithoutClipboardApi(false);
  expect(await copyText(TEXT)).toBe(false);
  expect(page.copies).toEqual(selectedOnce(TEXT));
  expect(document.querySelector("textarea")).toBeNull();
  expect(document.activeElement).toBe(button);
});

test("an execCommand that throws, or is missing, is a failure", async () => {
  // jsdom has no execCommand at all.
  expect(await copyText(TEXT)).toBe(false);
  const page = pageWithoutClipboardApi();
  page.exec.mockImplementationOnce(() => {
    throw new Error("not allowed");
  });
  expect(await copyText(TEXT)).toBe(false);
  expect(document.querySelector("textarea")).toBeNull();
});

test("a clipboard API that refuses falls back to the page and reports what the page did", async () => {
  const page = pageWithoutClipboardApi();
  const writeText = vi.fn(async (_text: string) => {
    throw new DOMException("denied", "NotAllowedError");
  });
  setClipboard({ writeText });
  expect(await copyText(TEXT)).toBe(true);
  expect(writeText).toHaveBeenCalledOnce();
  expect(page.copies).toEqual(selectedOnce(TEXT));
  page.answer(false);
  expect(await copyText(TEXT)).toBe(false);
});

test("a clipboard API that refuses, on a page that cannot copy, is a failure", async () => {
  setClipboard({
    writeText: async () => {
      throw new Error("denied");
    },
  });
  expect(await copyText(TEXT)).toBe(false);
});

// Every text copy control goes through copyText, so each reports the real
// outcome and each works on a page without navigator.clipboard (plain HTTP
// from another machine: issue #566). The picture copy of components/ImageMenu
// writes a picture, which the page has no way to do; the test support of this
// file fakes the page's side of the copy.
const MAY_TOUCH_THE_CLIPBOARD = new Set([
  "ui/clipboard.ts",
  "ui/clipboardTestKit.ts",
  "ui/components/ImageMenu.tsx",
]);

/** A path under src as the allow-list spells it: with "/", whatever the platform joined it with. */
const underSrc = (path: string) => path.replaceAll("\\", "/");

/** The names a page copies text by: navigator.clipboard, its writeText, document.execCommand. */
const CLIPBOARD_NAMES = new Set(["clipboard", "writeText", "execCommand"]);

/**
 * Every place in a source file that names one of CLIPBOARD_NAMES, as an
 * identifier or as a literal key (`navigator["clipboard"]`), found in the
 * syntax tree so that a comment, a sentence in a string or `clipboardData` is
 * not a hit. Stricter than it has to be on purpose: a local that is merely
 * called `clipboard`, or an execCommand of another command, is a hit too and
 * is renamed or moved rather than excused.
 */
function clipboardUses(source: string, fileName: string): string[] {
  const file = ts.createSourceFile(
    fileName,
    source,
    ts.ScriptTarget.Latest,
    true,
  );
  const hits: string[] = [];
  const visit = (node: ts.Node) => {
    if (
      (ts.isIdentifier(node) || ts.isStringLiteralLike(node)) &&
      CLIPBOARD_NAMES.has(node.text)
    ) {
      const { line } = file.getLineAndCharacterOfPosition(node.getStart(file));
      const [where = ""] = node.parent.getText(file).split("\n");
      hits.push(`${line + 1}: ${where.slice(0, 60)}`);
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return hits;
}

test.each([
  ["a property access", "navigator.clipboard.writeText(t);"],
  ["a non-null assertion", "navigator.clipboard!.writeText(t);"],
  ["an optional chain", "navigator.clipboard?.writeText(t);"],
  ["an element access", 'navigator.clipboard["writeText"](t);'],
  ["a quoted clipboard", 'navigator["clipboard"].writeText(t);'],
  ["a cast navigator", "(navigator as any).clipboard.writeText(t);"],
  ["a destructured clipboard", "const { clipboard } = navigator;"],
  ["a destructured writeText", "const { writeText } = navigator.clipboard;"],
  ["a clipboard in a variable", "const c = props.clip; c.writeText(t);"],
  ["execCommand", 'document.execCommand("copy");'],
  ["an optional call", 'document.execCommand?.("copy");'],
  ["an element access call", 'document["execCommand"]("copy");'],
  ["a template literal key", "const { [`execCommand`]: run } = document;"],
  ["a pattern in a parameter", "function f({ clipboard }: Navigator) {}"],
  ["an assignment pattern", "({ clipboard: api } = navigator);"],
])("the source guard finds %s", (_spelling, code) => {
  expect(clipboardUses(code, "a.ts")).not.toEqual([]);
});

test("the source guard finds it in a component", () => {
  expect(
    clipboardUses(
      "export const A = () => <b onClick={() => navigator.clipboard.writeText(x)} />;",
      "a.tsx",
    ),
  ).toHaveLength(2);
});

test.each([
  ["the helper", "void copyText(text);"],
  ["clipboardData of a paste", "ev.clipboardData?.items;"],
  ["clipboardData destructured", "const { clipboardData } = ev;"],
  ["a comment", "// navigator.clipboard.writeText(t) and execCommand('copy')"],
  ["a string", 'const s = "navigator.clipboard.writeText(t)";'],
  ["the picture copy's own name", "const write = item.write;"],
])("the source guard leaves %s alone", (_what, code) => {
  expect(clipboardUses(code, "a.ts")).toEqual([]);
});

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sources(path);
    return /\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)
      ? [path]
      : [];
  });
}

test("the allow-list reads a Windows path as it reads any other", () => {
  const windows = win32.relative(
    "C:\\repo\\src",
    "C:\\repo\\src\\ui\\clipboard.ts",
  );
  expect(windows).toBe("ui\\clipboard.ts");
  expect(MAY_TOUCH_THE_CLIPBOARD.has(windows)).toBe(false);
  expect(MAY_TOUCH_THE_CLIPBOARD.has(underSrc(windows))).toBe(true);
});

test("no component copies text by itself", () => {
  const src = join(__dirname, "..");
  const files = new Map(
    sources(src).map((file) => [underSrc(relative(src, file)), file]),
  );
  // A renamed file must not leave a dead entry that excuses nothing.
  expect(
    [...MAY_TOUCH_THE_CLIPBOARD].filter((name) => !files.has(name)),
  ).toEqual([]);
  const offenders = [...files]
    .filter(([name]) => !MAY_TOUCH_THE_CLIPBOARD.has(name))
    .flatMap(([name, file]) =>
      clipboardUses(readFileSync(file, "utf8"), file).map(
        (hit) => `${name}:${hit}`,
      ),
    );
  expect(offenders).toEqual([]);
});
