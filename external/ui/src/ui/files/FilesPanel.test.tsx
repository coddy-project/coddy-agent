import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { FilesPanel } from "./FilesPanel";
import { FileMarkdown } from "./FileMarkdown";
import { t } from "../i18n/i18n";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("file content revalidates on focus and reports a concurrent rewrite", async () => {
  let version = 1;
  Element.prototype.scrollIntoView = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: unknown, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/tree"))
        return new Response(
          JSON.stringify({ entries: [], has_more: false, next_cursor: "" }),
        );
      if (init?.method === "HEAD")
        return new Response(null, {
          headers: {
            ETag: `"v${version}"`,
            "Content-Type": "text/plain",
            "Content-Length": "12",
            "Last-Modified": "Mon, 05 Oct 2026 12:00:00 GMT",
          },
        });
      return new Response(
        JSON.stringify({
          lines: [`content version ${version}`],
          offset: 0,
          next_offset: 1,
          has_more: false,
          etag: `"v${version}"`,
        }),
      );
    }),
  );
  render(
    <FilesPanel
      sessionId="s1"
      initialPath="notes.txt"
      onTab={() => {}}
      onClose={() => {}}
    />,
  );
  await screen.findByText("content version 1");
  version = 2;
  fireEvent.focus(window);
  await screen.findByText("content version 2");
  expect(screen.getByRole("status")).toHaveTextContent(t("files.changed"));
});

test("workspace Markdown blocks external images and never inserts raw HTML", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  render(
    <FileMarkdown
      sessionId="s1"
      path="docs/readme.md"
      text={
        "![remote](https://example.test/track.png)\n\n<script>window.stolen=true</script>"
      }
    />,
  );
  await waitFor(() =>
    expect(screen.getByText(t("files.loadExternalImage"))).toBeTruthy(),
  );
  expect(document.querySelector("script")).toBeNull();
  expect(document.querySelector("img")).toBeNull();
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText(t("files.loadExternalImage")));
  expect(document.querySelector("img")?.getAttribute("referrerpolicy")).toBe(
    "no-referrer",
  );
});
