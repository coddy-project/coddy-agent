import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DocsView } from "./DocsView";
import { askDraftFor, outlineHeadings, sectionAnchorAt } from "./docsReader";

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
    "# Mentions\n\nIntro text.\n\n## What a prompt can mention\n\nFiles and [modes](coddy:features/modes#agent).\n\n```bash\n# not a heading\n```\n\n### Line ranges\n\nRanges.\n\n## Completion\n\nThe picker.\n",
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
    expect(await screen.findByText("Coddy 1.2.3, built into this binary: no site involved")).toBeTruthy();
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
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onOpen).toHaveBeenCalledWith("features/mentions", "completion");
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByTestId("docs-hits")).toBeNull();
    expect(screen.getByTestId("docs-toc")).toBeTruthy();
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
    fireEvent.click(screen.getByTestId("docs-ask"));
    expect(onAsk).toHaveBeenCalledWith("@coddy:features/mentions#completion ");
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
