import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DocsView } from "./DocsView";
import { focusAt, scrollToKeep } from "./ImageLightbox";
import { docsCommandOpensPage, parseDocsCommand } from "./docsCommand";
import {
  askDraftFor,
  assignHeadingIds,
  outlineHeadings,
  sectionAnchorAt,
} from "./docsReader";

const contents = {
  object: "coddy.docs",
  version: "1.2.3",
  groups: [
    {
      id: "features",
      title: "Features",
      summary: "What it does.",
      pages: [
        { slug: "features/modes", title: "Operating modes", summary: "Modes." },
        { slug: "features/mentions", title: "Mentions", summary: "At mentions." },
      ],
    },
  ],
};

const mentionsPage = {
  object: "coddy.docs_page",
  version: "1.2.3",
  slug: "features/mentions",
  title: "Mentions",
  summary: "At mentions.",
  group: { id: "features", title: "Features" },
  anchor: "",
  markdown:
    "# Mentions\n\nIntro text.\n\n![The picker](https://raw.githubusercontent.com/x/y/main/docs/assets/p.png)\n\n## What a prompt can mention\n\nFiles and [modes](coddy:features/modes#agent).\n\n```bash\n# not a heading\n```\n\n### Line ranges\n\nRanges.\n\n## Completion\n\nThe picker.\n",
  headings: [
    { level: 1, text: "Mentions", anchor: "mentions" },
    { level: 2, text: "What a prompt can mention", anchor: "what-a-prompt-can-mention" },
    { level: 3, text: "Line ranges", anchor: "line-ranges" },
    { level: 2, text: "Completion", anchor: "completion" },
  ],
  prev: { slug: "features/modes", title: "Operating modes" },
  next: null,
  url: "https://coddy.dev/docs/features/mentions",
};

const hits = [
  {
    slug: "features/mentions",
    title: "Mentions",
    group: "Features",
    anchor: "completion",
    heading: "Completion",
    snippet: [{ text: "The " }, { text: "picker", hit: true }, { text: "." }],
  },
];

function stubFetch() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const json = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    if (url === "/coddy/docs") {
      return json(contents);
    }
    if (url.startsWith("/coddy/docs/page?ref=features%2Fmentions")) {
      return json(mentionsPage);
    }
    if (url.startsWith("/coddy/docs/page")) {
      return json({ error: { message: "no documentation page" } }, 404);
    }
    if (url.startsWith("/coddy/docs/search?q=picker")) {
      return json({ object: "coddy.docs_search", hits });
    }
    return json({ hits: [] });
  });
}

beforeEach(() => {
  vi.stubGlobal("fetch", stubFetch());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("DocsView", () => {
  it("opens the first page of the contents when no page is named", async () => {
    const onOpen = vi.fn();
    render(<DocsView slug={null} anchor={null} onOpen={onOpen} />);
    await waitFor(() =>
      expect(onOpen).toHaveBeenCalledWith("features/modes", null, { replace: true }),
    );
    // The version alone under the title: where the pages come from goes without saying.
    expect(await screen.findByText("Coddy 1.2.3")).toBeTruthy();
  });

  it("shows a page with its contents, sections, neighbours and working links", async () => {
    const onOpen = vi.fn();
    render(<DocsView slug="features/mentions" anchor={null} onOpen={onOpen} />);
    await screen.findByText("Intro text.");
    // The contents mark the page being read.
    const active = screen.getByText("Mentions", { selector: ".docs-toc-page" });
    expect(active.getAttribute("aria-current")).toBe("page");
    // Headings carry the anchors the server computed; a # line in code is not one.
    expect(document.getElementById("what-a-prompt-can-mention")?.tagName).toBe("H2");
    expect(document.getElementById("line-ranges")?.tagName).toBe("H3");
    expect(document.getElementById("completion")?.tagName).toBe("H2");
    // A link to another page opens it in the reader.
    expect(screen.getByText("modes").closest("a")?.getAttribute("href")).toBe(
      "#/docs/features/modes#agent",
    );
    // The page before it; there is none after.
    fireEvent.click(screen.getByTestId("docs-prev"));
    expect(onOpen).toHaveBeenCalledWith("features/modes");
    expect(screen.queryByTestId("docs-next")).toBeNull();
    // On this page lists the sections two levels deep.
    const outline = document.querySelector(".docs-outline");
    expect(outline?.textContent).toContain("Line ranges");
    expect(outline?.textContent).not.toContain("Mentions");
  });

  it("searches as the query is typed and opens a hit at its section", async () => {
    const onOpen = vi.fn();
    render(<DocsView slug="features/mentions" anchor={null} onOpen={onOpen} />);
    await screen.findByText("Intro text.");
    const input = screen.getByTestId("docs-search");
    fireEvent.change(input, { target: { value: "picker" } });
    const list = await screen.findByTestId("docs-hits");
    expect(list.textContent).toContain("Mentions › Completion");
    expect(list.querySelector("mark")?.textContent).toBe("picker");
    // The hits hang under the search box in the header; the contents stay.
    expect(list.closest(".docs-header")).not.toBeNull();
    expect(screen.getByTestId("docs-toc")).toBeTruthy();
    expect(input.getAttribute("aria-activedescendant")).toBe("docs-hit-0");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onOpen).toHaveBeenCalledWith("features/mentions", "completion");
    // Opening a hit folds the results away; focusing the box brings them back.
    expect(screen.queryByTestId("docs-hits")).toBeNull();
    fireEvent.focus(input);
    expect(screen.getByTestId("docs-hits")).toBeTruthy();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByTestId("docs-hits")).toBeNull();
    expect((input as HTMLInputElement).value).toBe("");
  });

  it("lists a hit whose section has no text of its own", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        const body = url.startsWith("/coddy/docs/search")
          ? { hits: [{ slug: "features/mentions", title: "Mentions", group: "Features", anchor: "completion", heading: "Completion", snippet: null }] }
          : url === "/coddy/docs"
            ? contents
            : mentionsPage;
        return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
      }),
    );
    render(<DocsView slug="features/mentions" anchor={null} onOpen={vi.fn()} />);
    await screen.findByText("Intro text.");
    fireEvent.change(screen.getByTestId("docs-search"), { target: { value: "completion" } });
    expect((await screen.findByTestId("docs-hits")).textContent).toContain("Mentions › Completion");
  });

  it("says when a search finds nothing", async () => {
    render(<DocsView slug="features/mentions" anchor={null} onOpen={vi.fn()} />);
    await screen.findByText("Intro text.");
    fireEvent.change(screen.getByTestId("docs-search"), { target: { value: "zzz" } });
    expect(await screen.findByTestId("docs-search-empty")).toBeTruthy();
  });

  it("asks the agent about the page with the page mentioned", async () => {
    const onAsk = vi.fn();
    render(
      <DocsView slug="features/mentions" anchor="completion" onOpen={vi.fn()} onAsk={onAsk} />,
    );
    await screen.findByText("Intro text.");
    const ask = screen.getByTestId("docs-ask");
    expect(ask.textContent).toBe("Ask the agent");
    fireEvent.click(ask);
    expect(onAsk).toHaveBeenCalledWith("@coddy:features/mentions#completion ");
  });

  it("opens an image in a modal that zooms, and closes it", async () => {
    render(<DocsView slug="features/mentions" anchor={null} onOpen={vi.fn()} />);
    await screen.findByText("Intro text.");
    fireEvent.click(screen.getByAltText("The picker"));
    const dialog = screen.getByRole("dialog");
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.textContent).toContain("The picker");
    const img = dialog.querySelector("img")!;
    expect(img.getAttribute("src")).toBe("https://raw.githubusercontent.com/x/y/main/docs/assets/p.png");
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
    // Fitted, the level is an icon; zoomed, the percentage. Both put the image back to fit.
    const level = screen.getByTestId("docs-lightbox-fit");
    expect(level.textContent).toBe("");
    expect(level.querySelector("svg")).not.toBeNull();
    expect(level.getAttribute("aria-label")).toBe("Fit to the window (0)");
    fireEvent.click(screen.getByTestId("docs-lightbox-zoom-in"));
    expect(dialog.getAttribute("data-zoom")).toBe("1");
    expect(level.textContent).toBe("100%");
    expect(level.getAttribute("aria-label")).toBe("100%, Fit to the window (0)");
    fireEvent.click(img);
    expect(dialog.getAttribute("data-zoom")).toBe("fit");
    fireEvent.click(img);
    expect(dialog.getAttribute("data-zoom")).toBe("2");
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("has no ask button where there is no chat to start", async () => {
    render(<DocsView slug="features/mentions" anchor={null} onOpen={vi.fn()} />);
    await screen.findByText("Intro text.");
    expect(screen.queryByTestId("docs-ask")).toBeNull();
  });

  it("reports a page the server does not have", async () => {
    render(<DocsView slug="features/nowhere" anchor={null} onOpen={vi.fn()} />);
    expect((await screen.findByTestId("docs-error")).textContent).toContain(
      "no documentation page",
    );
  });
});

describe("/docs in the composer", () => {
  it("is the command alone or with an argument, nothing else", () => {
    expect(parseDocsCommand("/docs")).toBe("");
    expect(parseDocsCommand("  /docs   telegram proxy  ")).toBe("telegram proxy");
    expect(parseDocsCommand("/docs\nfeatures/mentions")).toBe("features/mentions");
    expect(parseDocsCommand("/docsify")).toBeNull();
    expect(parseDocsCommand("see /docs")).toBeNull();
    expect(parseDocsCommand("/doc")).toBeNull();
  });

  it("opens a page named by its address or its title, and searches anything else", () => {
    // The console's rule: a reference, or the exact title of the page it found.
    expect(docsCommandOpensPage("features/mentions#completion", "Mentions")).toBe(true);
    expect(docsCommandOpensPage("coddy:features/modes", "Operating modes")).toBe(true);
    expect(docsCommandOpensPage("operating MODES", "Operating modes")).toBe(true);
    expect(docsCommandOpensPage("mentions", "Mentions")).toBe(true);
    expect(docsCommandOpensPage("proxy", "Telegram gateway")).toBe(false);
  });

  it("shows the search it was given in the reader", async () => {
    render(
      <DocsView
        slug="features/mentions"
        anchor={null}
        onOpen={vi.fn()}
        searchSeed={{ query: "picker", nonce: 1 }}
      />,
    );
    const box = (await screen.findByRole("combobox")) as HTMLInputElement;
    await waitFor(() => expect(box.value).toBe("picker"));
    expect(await screen.findByTestId("docs-hits")).toBeTruthy();
  });
});

describe("ImageLightbox zoom", () => {
  const stage = { left: 0, top: 50, width: 1000, height: 800 };

  it("keeps the point clicked under the pointer", () => {
    // Fitted: a 1000x500 image centred in the stage, clicked a quarter in.
    const fitted = { left: 0, top: 200, width: 1000, height: 500 };
    const focus = focusAt(stage, fitted, 250, 325);
    expect(focus).toEqual({ fx: 0.25, fy: 0.25, vx: 250, vy: 275 });
    // Doubled: the image now starts at the stage's padding, unscrolled.
    const zoomed = { left: 24, top: 50, width: 2000, height: 1000 };
    const to = scrollToKeep(focus, stage, zoomed, { left: 0, top: 0 });
    expect(to).toEqual({ left: 274, top: 0 });
    // Scrolled there, the point clicked is back under the pointer.
    expect(zoomed.left - to.left + 0.25 * zoomed.width).toBe(250 + stage.left);
  });

  it("keeps the middle of the view for the keys and the buttons", () => {
    const img = { left: -500, top: -100, width: 3000, height: 1500 };
    const focus = focusAt(stage, img);
    expect(focus.vx).toBe(500);
    expect(focus.vy).toBe(400);
    expect(focus.fx).toBeCloseTo(1000 / 3000);
    // One step out from 3x to 2x, drawn from the top-left of the content.
    const out = { left: 24 - 600, top: 50 - 200, width: 2000, height: 1000 };
    const to = scrollToKeep(focus, stage, out, { left: 600, top: 200 });
    expect(to.left).toBeCloseTo(24 + 2000 / 3 - 500);
  });

  it("never scrolls before the start", () => {
    const img = { left: 0, top: 50, width: 100, height: 100 };
    expect(scrollToKeep(focusAt(stage, img, 0, 50), stage, img, { left: 0, top: 0 })).toEqual({
      left: 0,
      top: 0,
    });
  });
});

describe("docsReader", () => {
  it("quotes the selection above the mention of the section", () => {
    expect(askDraftFor("features/mentions", null, "")).toBe("@coddy:features/mentions ");
    expect(askDraftFor("features/mentions", "completion", "line one\n\nline two ")).toBe(
      "> line one\n>\n> line two\n\n@coddy:features/mentions#completion ",
    );
  });

  it("keeps the sections two levels deep for the outline", () => {
    expect(
      outlineHeadings([
        { level: 1, text: "T", anchor: "t" },
        { level: 2, text: "A", anchor: "a" },
        { level: 4, text: "D", anchor: "d" },
        { level: 3, text: "B", anchor: "b" },
      ]).map((h) => h.anchor),
    ).toEqual(["a", "b"]);
  });

  it("pairs headings by level and text, so one the server does not count shifts nothing", () => {
    const root = document.createElement("div");
    // The quote's heading is drawn by the renderer but is not a heading of the page.
    root.innerHTML =
      "<h1>Title</h1><blockquote><h2>Quoted</h2></blockquote><h2>Real <code>x</code> one</h2><h3>Sub</h3>";
    assignHeadingIds(root, [
      { level: 1, text: "Title", anchor: "title" },
      { level: 2, text: "Real x one", anchor: "real-x-one" },
      { level: 3, text: "Sub", anchor: "sub" },
    ]);
    const ids = Array.from(root.querySelectorAll("h1, h2, h3")).map((el) => el.id);
    expect(ids).toEqual(["title", "", "real-x-one", "sub"]);
  });

  it("finds the section a node sits in", () => {
    const root = document.createElement("div");
    root.innerHTML =
      '<p id="p0">top</p><h2 id="a">A</h2><p id="p1">one</p><h3 id="b">B</h3><p id="p2">two</p>';
    const text = (id: string) => root.querySelector(`#${id}`)!.firstChild!;
    expect(sectionAnchorAt(root, text("p0"))).toBeNull();
    expect(sectionAnchorAt(root, text("p1"))).toBe("a");
    expect(sectionAnchorAt(root, text("p2"))).toBe("b");
    expect(sectionAnchorAt(root, root.querySelector("#a")!.firstChild!)).toBe("a");
  });
});
