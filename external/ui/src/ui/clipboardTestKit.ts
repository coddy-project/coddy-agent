import { onTestFinished, vi } from "vitest";

/** One run of the page's own copy: what the text area had selected and how it was set up. */
export type PageCopy = { text: string; readOnly: boolean; fontSize: string };

/**
 * A page served over plain HTTP from another machine: it has no
 * navigator.clipboard, and the page's own copy command answers `result` until
 * `answer` changes it. `copies` lists what each call found selected.
 */
export function pageWithoutClipboardApi(result = true) {
  Object.defineProperty(navigator, "clipboard", {
    value: undefined,
    configurable: true,
    writable: true,
  });
  let answer = result;
  const copies: PageCopy[] = [];
  const exec = vi.fn((_command: string) => {
    const area = [...document.querySelectorAll("textarea")].at(-1);
    if (area) {
      copies.push({
        text: area.value.slice(area.selectionStart, area.selectionEnd),
        readOnly: area.readOnly,
        fontSize: area.style.fontSize,
      });
    }
    return answer;
  });
  Object.defineProperty(document, "execCommand", {
    value: exec,
    configurable: true,
    writable: true,
  });
  onTestFinished(() => {
    delete (document as { execCommand?: unknown }).execCommand;
  });
  return {
    exec,
    copies,
    answer(next: boolean) {
      answer = next;
    },
  };
}

/** What a copy through the page leaves for `text`: one read-only 16px selection of all of it. */
export function selectedOnce(text: string): PageCopy[] {
  return [{ text, readOnly: true, fontSize: "16px" }];
}
