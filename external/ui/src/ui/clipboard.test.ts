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

/**
 * What in a source file reaches for the clipboard by itself, found in the
 * syntax tree so that no spelling slips by (`clipboard!.writeText`,
 * `["writeText"]`, `execCommand?.("copy")`, a destructured `clipboard`, a
 * literal key in brackets) and a comment, a string or an array binding that
 * merely shares the name is not a hit: any access to a member called clipboard
 * or writeText, and any use of execCommand but a call with a literal command
 * other than "copy". A member named by an expression that is not a literal is
 * out of reach of a static check.
 */
function clipboardUses(source: string, fileName: string): string[] {
  const file = ts.createSourceFile(
    fileName,
    source,
    ts.ScriptTarget.Latest,
    true,
  );
  const hits: string[] = [];
  // The text of a key written as a literal, through the parentheses and casts
  // around it: "x", `x`, ("x"), "x" as const.
  const literalText = (expr: ts.Expression): string => {
    let e = expr;
    while (
      ts.isParenthesizedExpression(e) ||
      ts.isNonNullExpression(e) ||
      ts.isAsExpression(e) ||
      ts.isSatisfiesExpression(e) ||
      ts.isTypeAssertionExpression(e)
    ) {
      e = e.expression;
    }
    return ts.isStringLiteralLike(e) ? e.text : "";
  };
  const keyText = (key: ts.Node): string => {
    if (ts.isIdentifier(key) || ts.isStringLiteralLike(key)) return key.text;
    return ts.isComputedPropertyName(key) ? literalText(key.expression) : "";
  };
  // An object literal that is the target of an assignment, or part of one:
  // ({ clipboard } = navigator), ({ a: { clipboard } } = x).
  const isAssignmentPattern = (literal: ts.Node): boolean => {
    let at = literal;
    for (let up = at.parent; up; at = up, up = up.parent) {
      if (ts.isBinaryExpression(up)) {
        return (
          up.left === at && up.operatorToken.kind === ts.SyntaxKind.EqualsToken
        );
      }
      if (ts.isForInStatement(up) || ts.isForOfStatement(up)) {
        return up.initializer === at;
      }
      const inside =
        ts.isPropertyAssignment(up) ||
        ts.isArrayLiteralExpression(up) ||
        ts.isObjectLiteralExpression(up) ||
        ts.isSpreadAssignment(up) ||
        ts.isSpreadElement(up) ||
        ts.isParenthesizedExpression(up);
      if (!inside) return false;
    }
    return false;
  };
  const nameOf = (node: ts.Node): string => {
    if (ts.isPropertyAccessExpression(node)) return node.name.text;
    if (ts.isElementAccessExpression(node)) {
      return literalText(node.argumentExpression);
    }
    // const { clipboard } = navigator; const { ["clipboard"]: c } = navigator.
    // An array pattern names no member, and a rest binding names a local.
    if (ts.isBindingElement(node)) {
      return ts.isObjectBindingPattern(node.parent) && !node.dotDotDotToken
        ? keyText(node.propertyName ?? node.name)
        : "";
    }
    if (
      (ts.isPropertyAssignment(node) ||
        ts.isShorthandPropertyAssignment(node)) &&
      isAssignmentPattern(node.parent)
    ) {
      return keyText(node.name);
    }
    return "";
  };
  const isOtherCommand = (node: ts.Node): boolean => {
    let call = node.parent;
    while (ts.isNonNullExpression(call) || ts.isParenthesizedExpression(call)) {
      call = call.parent;
    }
    const first = ts.isCallExpression(call) ? call.arguments[0] : undefined;
    return (
      !!first &&
      ts.isStringLiteralLike(first) &&
      first.text.toLowerCase() !== "copy"
    );
  };
  const visit = (node: ts.Node) => {
    const name = nameOf(node);
    if (
      name === "clipboard" ||
      name === "writeText" ||
      (name === "execCommand" && !isOtherCommand(node))
    ) {
      const { line } = file.getLineAndCharacterOfPosition(node.getStart(file));
      hits.push(`${line + 1}: ${node.getText(file).slice(0, 60)}`);
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
  ["window.navigator", "window.navigator.clipboard.writeText(t);"],
  ["a destructured clipboard", "const { clipboard } = navigator;"],
  ["a renamed clipboard", "const { clipboard: api } = navigator;"],
  ["a destructured writeText", "const { writeText } = navigator.clipboard;"],
  ["a clipboard in a variable", "const c = props.clip; c.writeText(t);"],
  ["execCommand", 'document.execCommand("copy");'],
  ["an optional call", 'document.execCommand?.("copy");'],
  ["a non-null call", 'document.execCommand!("copy");'],
  ["an element access call", 'document["execCommand"]("copy");'],
  ["a template literal command", "(document as any).execCommand(`copy`);"],
  ["a command in a variable", "document.execCommand(command);"],
  ["a bound execCommand", "const run = document.execCommand.bind(document);"],
  ["a destructured execCommand", "const { execCommand } = document;"],
  [
    "a literal computed key of execCommand",
    'const { ["execCommand"]: run } = document; run("copy");',
  ],
  [
    "a template computed key of execCommand",
    "const { [`execCommand`]: run } = document;",
  ],
  [
    "a literal computed key of clipboard",
    'const { ["clipboard"]: api } = navigator;',
  ],
  ["a quoted key of clipboard", 'const { "clipboard": api } = navigator;'],
  [
    "a literal computed key of writeText",
    'const { ["writeText"]: write } = api;',
  ],
  [
    "a nested pattern with a computed key",
    'const { inner: { ["writeText"]: write } } = api;',
  ],
  [
    "a parenthesised computed key",
    'const { [("clipboard")]: api } = navigator;',
  ],
  ["a parenthesised element key", 'navigator[("clipboard")].writeText(t);'],
  ["a pattern in a parameter", "function f({ clipboard }: Navigator) {}"],
  ["an assignment pattern", "({ clipboard: api } = navigator);"],
  ["a shorthand assignment pattern", "({ clipboard } = navigator);"],
  ["a nested assignment pattern", "({ inner: { clipboard } } = x);"],
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
  ["a comment", "// navigator.clipboard.writeText(t) and execCommand('copy')"],
  ["a string", 'const s = "navigator.clipboard.writeText(t)";'],
  ["another command", 'document.execCommand("insertText", false, s);'],
  ["the picture copy's own name", "const write = item.write;"],
  ["an array binding", "const [clipboard] = history;"],
  [
    "an array binding of the other names",
    "const [writeText, execCommand] = a;",
  ],
  ["a local renamed to clipboard", "const { a: clipboard } = x;"],
  ["a plain variable", "const clipboard = 1; use(clipboard);"],
  ["a rest binding", "const { a, ...clipboard } = x;"],
  ["clipboardData destructured", "const { clipboardData } = ev;"],
  ["a computed key of another name", 'const { ["items"]: list } = ev;'],
  ["an object that has a clipboard key", "const o = { clipboard: 1 };"],
  ["an object on the right of an assignment", "x = { clipboard };"],
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
