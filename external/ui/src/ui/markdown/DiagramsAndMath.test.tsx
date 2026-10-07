import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

// The renderers are chunks loaded on demand; here they are fakes the tests drive.
const mermaid = vi.hoisted(() => ({
  initialize: vi.fn(),
  parse: vi.fn(async (_text: string) => true),
  render: vi.fn(async (_id: string, text: string) => ({
    svg: `<svg xmlns="http://www.w3.org/2000/svg" width="100%" style="max-width: 200px;" viewBox="0 0 200 100"><text>${text.length}</text></svg>`,
  })),
}));
const katex = vi.hoisted(() => ({
  // KaTeX builds the formula's nodes into the element it is given.
  render: vi.fn(
    (source: string, el: HTMLElement, opts: { displayMode?: boolean }) => {
      const node = document.createElement("span");
      node.className = opts.displayMode ? "katex katex-display" : "katex";
      node.textContent = source;
      el.replaceChildren(node);
    },
  ),
}));
const loaders = vi.hoisted(() => ({
  mermaidFails: false,
  katexFails: false,
}));
vi.mock("./renderers", () => ({
  loadMermaid: () =>
    loaders.mermaidFails
      ? Promise.reject(
          new TypeError(
            "Failed to fetch dynamically imported module: /chunks/mermaid.js",
          ),
        )
      : Promise.resolve(mermaid),
  loadKatex: () =>
    loaders.katexFails
      ? Promise.reject(new TypeError("Failed to fetch"))
      : Promise.resolve(katex),
}));

import { Markdown } from "./Markdown";
import { STREAM_SETTLE_MS } from "./DiagramBlock";
import {
  cachedPicture,
  clearPictureCache,
  mermaidThemeVariables,
  parseCssColor,
  renderPicture,
  standaloneSvg,
  type DiagramPalette,
} from "./pictureRender";
import { resetMathForTests } from "./MathFormula";

function blobText(b: Blob): Promise<string> {
  return new Promise((resolve) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.readAsText(b);
  });
}

const fence = (lang: string, body: string) =>
  "```" + lang + "\n" + body + "\n```";
const FLOW = "flowchart LR\n  A --> B";

let writeText: ReturnType<typeof vi.fn>;
let createObjectURL: ReturnType<typeof vi.fn>;
let blobs: Blob[];

beforeEach(() => {
  clearPictureCache();
  resetMathForTests();
  loaders.mermaidFails = false;
  loaders.katexFails = false;
  mermaid.initialize.mockClear();
  mermaid.parse.mockClear();
  mermaid.render.mockClear();
  katex.render.mockClear();
  writeText = vi.fn(() => Promise.resolve());
  Object.assign(navigator, { clipboard: { writeText } });
  blobs = [];
  createObjectURL = vi.fn((b: Blob) => {
    blobs.push(b);
    return "blob:fake";
  });
  Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
  document.documentElement.dataset.theme = "dark";
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("Mermaid and SVG fences", () => {
  test("draws a Mermaid fence as a picture by default", async () => {
    render(<Markdown text={fence("mermaid", FLOW)} />);
    const img = await screen.findByTestId("md-figure-img");
    expect(img.getAttribute("src")).toMatch(/^data:image\/svg\+xml/);
    // Sized from the viewBox, so an <img> can lay it out.
    expect(img.getAttribute("width")).toBe("200");
    expect(img.getAttribute("height")).toBe("100");
    expect(screen.getByTestId("md-figure").dataset.view).toBe("picture");
    expect(mermaid.initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        securityLevel: "strict",
        startOnLoad: false,
        htmlLabels: false,
      }),
    );
  });

  test("the source icon is off by default, shows the source when pressed and the picture again", async () => {
    render(<Markdown text={fence("mermaid", FLOW)} />);
    await screen.findByTestId("md-figure-img");
    const toggle = screen.getByTestId("md-figure-source-toggle");
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    expect(toggle.getAttribute("aria-label")).toBe("Show the source code");
    // Copy belongs to the source view, not the picture.
    expect(screen.queryByTestId("md-figure-copy")).toBeNull();
    fireEvent.click(toggle);
    expect(screen.queryByTestId("md-figure-img")).toBeNull();
    expect(
      screen.getByTestId("md-figure").querySelector(".md-figure-source pre")!
        .textContent,
    ).toContain("A --> B");
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(toggle.getAttribute("aria-label")).toBe("Show the picture");
    fireEvent.click(toggle);
    expect(screen.getByTestId("md-figure-img")).toBeTruthy();
  });

  test("the copy button sits on the source and copies it", async () => {
    render(<Markdown text={fence("mermaid", FLOW)} />);
    await screen.findByTestId("md-figure-img");
    fireEvent.click(screen.getByTestId("md-figure-source-toggle"));
    const copy = screen.getByTestId("md-figure-copy");
    expect(copy.parentElement!.classList.contains("md-figure-source")).toBe(
      true,
    );
    fireEvent.click(copy);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(FLOW));
  });

  test("downloads the source as diagram.mmd and the picture as diagram.svg", async () => {
    const clicks: string[] = [];
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(function (this: HTMLAnchorElement) {
        clicks.push(this.download);
      });
    render(<Markdown text={fence("mermaid", FLOW)} />);
    await screen.findByTestId("md-figure-img");
    expect(screen.getByTestId("md-figure-download-mmd").textContent).toBe(
      "MMD",
    );
    expect(screen.getByTestId("md-figure-download-svg").textContent).toBe(
      "SVG",
    );
    fireEvent.click(screen.getByTestId("md-figure-download-mmd"));
    fireEvent.click(screen.getByTestId("md-figure-download-svg"));
    expect(clicks).toEqual(["diagram.mmd", "diagram.svg"]);
    expect(await blobText(blobs[0]!)).toBe(FLOW);
    expect(await blobText(blobs[1]!)).toContain("<svg");
    click.mockRestore();
  });

  test("a diagram that does not parse shows its source and the reason", async () => {
    mermaid.parse.mockRejectedValueOnce(
      new Error(
        "Parse error on line 2:\n...A -->\n------^\nExpecting 'NODE', got 'EOF'",
      ),
    );
    render(<Markdown text={fence("mermaid", "flowchart LR\n  A -->")} />);
    const error = await screen.findByTestId("md-figure-error");
    expect(error.textContent).toContain("Parse error on line 2:");
    expect(error.textContent).toContain("Expecting 'NODE', got 'EOF'");
    expect(screen.getByTestId("md-figure").dataset.view).toBe("code");
    const toggle = screen.getByTestId(
      "md-figure-source-toggle",
    ) as HTMLButtonElement;
    expect(toggle.disabled).toBe(true);
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(mermaid.render).not.toHaveBeenCalled();
  });

  test("a renderer that does not load leaves the code readable", async () => {
    loaders.mermaidFails = true;
    render(<Markdown text={fence("mermaid", FLOW)} />);
    const error = await screen.findByTestId("md-figure-error");
    expect(error.textContent).toBe(
      "Could not load the diagram renderer: reload the page",
    );
    expect(screen.getByTestId("md-figure").textContent).toContain("A --> B");
  });

  test("a streamed fence is drawn once its text settles, not for every token", async () => {
    vi.useFakeTimers();
    const { rerender } = render(
      <Markdown text={fence("mermaid", "flowchart LR")} />,
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(mermaid.render).toHaveBeenCalledTimes(1);
    for (const tail of ["\n  A", "\n  A -->", "\n  A --> B"]) {
      rerender(<Markdown text={fence("mermaid", "flowchart LR" + tail)} />);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(STREAM_SETTLE_MS / 3);
      });
    }
    expect(mermaid.render).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(STREAM_SETTLE_MS);
    });
    expect(mermaid.render).toHaveBeenCalledTimes(2);
    expect(mermaid.render.mock.calls[1]![1]).toBe("flowchart LR\n  A --> B");
  });

  test("a half-written fence in a streamed reply is not an error until the reply ends", async () => {
    mermaid.parse.mockRejectedValue(
      new Error("Parse error on line 2:\nExpecting 'NODE', got 'EOF'"),
    );
    const { rerender } = render(
      <Markdown streaming text={fence("mermaid", "flowchart LR\n  A -->")} />,
    );
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByTestId("md-figure-error")).toBeNull();
    expect(screen.getByTestId("md-figure-pending")).toBeTruthy();
    rerender(<Markdown text={fence("mermaid", "flowchart LR\n  A -->")} />);
    expect(
      await screen.findByTestId("md-figure-error", {}, { timeout: 2000 }),
    ).toBeTruthy();
    mermaid.parse.mockReset();
    mermaid.parse.mockImplementation(async () => true);
  });

  test("a theme switch draws the diagram again in the new colours", async () => {
    render(<Markdown text={fence("mermaid", FLOW)} />);
    await screen.findByTestId("md-figure-img");
    expect(mermaid.render).toHaveBeenCalledTimes(1);
    await act(async () => {
      document.documentElement.dataset.theme = "light";
    });
    await waitFor(() => expect(mermaid.render).toHaveBeenCalledTimes(2), {
      timeout: 2000,
    });
    expect(mermaid.initialize.mock.lastCall![0].themeVariables.darkMode).toBe(
      false,
    );
  });

  test("a row mounted again reuses the picture instead of running Mermaid", async () => {
    const first = render(<Markdown text={fence("mermaid", FLOW)} />);
    await screen.findByTestId("md-figure-img");
    first.unmount();
    render(<Markdown text={fence("mermaid", FLOW)} />);
    expect(screen.getByTestId("md-figure-img")).toBeTruthy();
    expect(mermaid.render).toHaveBeenCalledTimes(1);
  });

  test("an SVG fence is drawn through an image, never inlined into the page", async () => {
    const svg =
      '<svg viewBox="0 0 40 20"><script>alert(1)</script><rect width="40" height="20"/></svg>';
    const { container } = render(<Markdown text={fence("svg", svg)} />);
    const img = await screen.findByTestId("md-figure-img");
    expect(img.getAttribute("width")).toBe("40");
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("svg rect")).toBeNull();
    expect(mermaid.render).not.toHaveBeenCalled();
    expect(screen.getByTestId("md-figure").dataset.kind).toBe("svg");
  });

  test("an SVG fence saves its source as image.svg and has no MMD button", async () => {
    const clicks: string[] = [];
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(function (this: HTMLAnchorElement) {
        clicks.push(this.download);
      });
    render(<Markdown text={fence("svg", '<svg viewBox="0 0 4 2"/>')} />);
    await screen.findByTestId("md-figure-img");
    expect(screen.queryByTestId("md-figure-download-mmd")).toBeNull();
    fireEvent.click(screen.getByTestId("md-figure-download-svg"));
    expect(clicks).toEqual(["image.svg"]);
    click.mockRestore();
  });

  test("an oversized SVG is not drawn", async () => {
    const huge = `<svg viewBox="0 0 1 1"><desc>${"x".repeat(600 * 1024)}</desc></svg>`;
    render(<Markdown text={fence("svg", huge)} />);
    expect(
      (await screen.findByTestId("md-figure-error")).textContent,
    ).toContain("larger than 512 KB");
  });

  test("an SVG fence that is not an SVG shows its source", async () => {
    render(<Markdown text={fence("svg", "<div>not svg</div>")} />);
    expect(
      (await screen.findByTestId("md-figure-error")).textContent,
    ).toContain("not an SVG document");
  });

  test("other fences stay code blocks", () => {
    const { container } = render(<Markdown text={fence("xml", "<svg/>")} />);
    expect(screen.queryByTestId("md-figure")).toBeNull();
    expect(container.querySelector(".md-code pre")).toBeTruthy();
  });

  test("the picture opens in the image viewer", async () => {
    render(<Markdown text={fence("mermaid", FLOW)} />);
    await screen.findByTestId("md-figure-img");
    fireEvent.click(screen.getByRole("button", { name: "Open the picture" }));
    expect(document.querySelector(".docs-lightbox")).toBeTruthy();
  });
});

test("a diagram queued for a theme the page has left is neither drawn nor cached", async () => {
  document.documentElement.dataset.theme = "light";
  await expect(renderPicture("mermaid", FLOW, "dark")).rejects.toThrow(
    /theme changed/,
  );
  expect(cachedPicture("mermaid", FLOW, "dark")).toBeUndefined();
  expect(mermaid.render).not.toHaveBeenCalled();
});

describe("diagram colours follow the theme and stay readable", () => {
  // WCAG relative luminance and contrast ratio.
  const lum = (h: string) => {
    const [r, g, b] = [1, 3, 5]
      .map((i) => parseInt(h.slice(i, i + 2), 16) / 255)
      .map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
    return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
  };
  const contrast = (a: string, b: string) => {
    const [x, y] = [lum(a), lum(b)].sort((m, n) => n - m);
    return (x! + 0.05) / (y! + 0.05);
  };
  // The dark and light themes' own tokens (styles.css), as the browser resolves them.
  const dark: DiagramPalette = {
    dark: true,
    background: [24, 24, 27],
    text: [244, 244, 245],
    accent: [167, 139, 250],
    series: [
      [167, 139, 250],
      [134, 239, 172],
      [147, 197, 253],
      [253, 230, 138],
      [240, 171, 252],
      [103, 232, 249],
      [253, 164, 175],
    ],
    font: "sans-serif",
  };
  const light: DiagramPalette = {
    dark: false,
    background: [236, 236, 238],
    text: [24, 24, 27],
    accent: [124, 58, 237],
    series: [
      [124, 58, 237],
      [17, 105, 49],
      [13, 92, 173],
      [121, 82, 12],
      [147, 48, 106],
      [14, 100, 110],
      [185, 28, 28],
    ],
    font: "sans-serif",
  };

  // A theme whose accent is too dark to carry a label on its own surface.
  const deepAccent: DiagramPalette = {
    ...dark,
    accent: [147, 51, 234],
    series: [[147, 51, 234], ...dark.series.slice(1)],
  };

  test.each([
    ["dark", dark],
    ["light", light],
    ["dark with a deep accent", deepAccent],
  ])(
    "%s: edges, labels, pie slices and Gantt bars contrast",
    (_name, palette) => {
      const v = mermaidThemeVariables(palette) as Record<string, string>;
      // Edges and lines against the surface (WCAG non-text contrast is 3:1).
      expect(contrast(v.lineColor!, v.background!)).toBeGreaterThanOrEqual(3);
      // Node text on node fills, Gantt text on task bars, mindmap labels on their sections.
      expect(
        contrast(v.primaryTextColor!, v.primaryColor!),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(v.taskTextColor!, v.taskBkgColor!),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(v.taskTextColor!, v.doneTaskBkgColor!),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(v.taskTextColor!, v.activeTaskBkgColor!),
      ).toBeGreaterThanOrEqual(4.5);
      for (let i = 0; i < palette.series.length; i++) {
        // A slice stands out from the surface, and its percentage reads on it.
        expect(
          contrast(v[`pie${i + 1}`]!, v.background!),
          `pie${i + 1}`,
        ).toBeGreaterThanOrEqual(3);
        expect(
          contrast(v.pieSectionTextColor!, v[`pie${i + 1}`]!),
          `pie${i + 1} label`,
        ).toBeGreaterThanOrEqual(4.5);
        if (i < 8)
          expect(
            contrast(v[`gitBranchLabel${i}`]!, v[`git${i}`]!),
            `git${i} label`,
          ).toBeGreaterThanOrEqual(4.5);
        expect(
          contrast(v[`cScaleLabel${i}`]!, v[`cScale${i}`]!),
          `cScale${i}`,
        ).toBeGreaterThanOrEqual(4.5);
      }
      expect(v.darkMode).toBe(palette.dark);
    },
  );

  test("two tokens of one colour do not give two slices alike", () => {
    const v = mermaidThemeVariables({
      ...dark,
      series: [
        [136, 192, 208],
        [163, 190, 140],
        [136, 192, 208],
        [180, 142, 173],
      ],
    }) as Record<string, string>;
    expect(new Set([v.pie1, v.pie2, v.pie3]).size).toBe(3);
    expect(v.pie3).not.toBe(v.pie1);
  });

  test("reads the colours a browser serialises", () => {
    expect(parseCssColor("rgb(24, 24, 27)")).toEqual([24, 24, 27, 1]);
    expect(parseCssColor("rgba(0, 0, 0, 0.5)")).toEqual([0, 0, 0, 0.5]);
    expect(parseCssColor("rgb(0 0 0 / 50%)")).toEqual([0, 0, 0, 0.5]);
    expect(parseCssColor("color(srgb 1 0.5 0 / 0.25)")).toEqual([
      255, 127.5, 0, 0.25,
    ]);
    expect(parseCssColor("oklch(0.5 0.1 200)")).toBeNull();
  });
});

describe("standaloneSvg", () => {
  test("adds the namespace and a pixel size an <img> can use", () => {
    const out = standaloneSvg(
      '<svg viewBox="0 0 30 10" width="100%" style="max-width: 30px; color: red"></svg>',
    );
    expect(out).toMatchObject({ width: 30, height: 10 });
    expect(out.svg).toContain('xmlns="http://www.w3.org/2000/svg"');
    expect(out.svg).toContain('width="30"');
    expect(out.svg).not.toContain("max-width");
    expect(out.svg).toContain("color: red");
  });

  test("keeps an explicit size and derives the missing side", () => {
    expect(
      standaloneSvg('<svg width="60" viewBox="0 0 30 10"/>'),
    ).toMatchObject({ width: 60, height: 20 });
    expect(standaloneSvg('<svg width="12px" height="8px"/>')).toMatchObject({
      width: 12,
      height: 8,
    });
  });

  test("refuses what is not SVG", () => {
    expect(() => standaloneSvg("<html></html>")).toThrow();
    expect(() => standaloneSvg("<svg")).toThrow();
  });
});

describe("formulas", () => {
  test("typesets inline dollars in place", async () => {
    render(<Markdown text={"Energy $E = mc^2$ here"} />);
    await waitFor(() =>
      expect(document.querySelector(".md-math-inline .katex")).toBeTruthy(),
    );
    expect(katex.render).toHaveBeenCalledWith(
      "E = mc^2",
      expect.any(HTMLElement),
      expect.objectContaining({
        displayMode: false,
        trust: false,
        throwOnError: false,
      }),
    );
  });

  test("an inline formula copies its source with the dollars on click", async () => {
    render(<Markdown text={"Energy $E = mc^2$ here"} />);
    await waitFor(() =>
      expect(document.querySelector(".md-math-inline .katex")).toBeTruthy(),
    );
    const node = screen.getByTestId("md-math-inline");
    expect(node.getAttribute("title")).toContain("$E = mc^2$");
    fireEvent.click(node);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("$E = mc^2$"));
  });

  test("display dollars and a math fence are formula blocks with a source switch", async () => {
    render(
      <Markdown text={"$$\n\\frac{a}{b}\n$$\n\n" + fence("math", "x^2")} />,
    );
    await waitFor(() =>
      expect(
        document.querySelectorAll(".md-math-display .katex-display"),
      ).toHaveLength(2),
    );
    const block = screen.getAllByTestId("md-math-block")[0]!;
    fireEvent.click(
      block.querySelector('[data-testid="md-figure-source-toggle"]')!,
    );
    expect(block.querySelector("pre")!.textContent).toBe("\\frac{a}{b}");
    fireEvent.click(block.querySelector('[data-testid="md-math-copy"]')!);
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("\\frac{a}{b}"));
  });

  test("LaTeX's own delimiters are typeset too", async () => {
    render(<Markdown text={"Inline \\(a+b\\) and\n\n\\[\nc+d\n\\]"} />);
    await waitFor(() =>
      expect(
        document.querySelector(".md-math-display .katex-display"),
      ).toBeTruthy(),
    );
    expect(screen.getByTestId("md-math-inline").dataset.source).toBe("a+b");
    expect(screen.getByTestId("md-math-block").textContent).toContain("c+d");
  });

  test("prices stay text", () => {
    const { container } = render(
      <Markdown text={"It costs $5 and $10 a month."} />,
    );
    expect(container.textContent).toBe("It costs $5 and $10 a month.");
    expect(screen.queryByTestId("md-math-inline")).toBeNull();
  });

  test.each([
    ["a shell variable", "Set ${CODDY_HOME}/hooks.json and ${CWD}/x, then go."],
    ["a PHC hash", 'The hash ("$argon2id$v=19$m=...$...") is written for you.'],
    ["variables back to back", "Paths $HOME$PATH stay."],
    ["a currency after a word", "It is US$5 or US$7 today."],
    ["a price range", "Plans from $5-$10 a month."],
    ["prices with a slash", "Either $5/$10 per seat."],
    ["a PATH line", "Add $HOME/.local/bin:$PATH to the profile."],
  ])("%s stays text", (_name, text) => {
    const { container } = render(<Markdown text={text} />);
    expect(screen.queryByTestId("md-math-inline")).toBeNull();
    expect(container.textContent).toBe(text);
  });

  test("Markdown between literal dollars keeps its formatting", () => {
    const { container } = render(
      <Markdown
        text={
          "The Basic plan is $10/month and the **Pro** plan is $25/month. Set $HOME and see [docs](http://x) for $PATH."
        }
      />,
    );
    expect(container.querySelector("strong")?.textContent).toBe("Pro");
    expect(container.querySelector("a")?.textContent).toBe("docs");
    expect(container.textContent).toBe(
      "The Basic plan is $10/month and the Pro plan is $25/month. Set $HOME and see docs for $PATH.",
    );
    expect(screen.queryByTestId("md-math-inline")).toBeNull();
  });

  test("a shell variable before a code span holding dollars keeps the code intact", () => {
    const { container } = render(
      <Markdown
        text={"Set ${CODDY_HOME}/hooks.json and `echo $HOME$PATH` now."}
      />,
    );
    expect(container.querySelector(".md-inline-code")?.textContent).toBe(
      "echo $HOME$PATH",
    );
    expect(container.textContent).toBe(
      "Set ${CODDY_HOME}/hooks.json and echo $HOME$PATH now.",
    );
    expect(screen.queryByTestId("md-math-inline")).toBeNull();
  });

  test("a formula next to code with a dollar in it is still a formula", async () => {
    const { container } = render(
      <Markdown text={"Take $x^2$ and run `a$b` too."} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId("md-math-inline").dataset.source).toBe("x^2"),
    );
    expect(container.querySelector(".md-inline-code")?.textContent).toBe("a$b");
  });

  test("a range written as a formula is a formula", async () => {
    render(<Markdown text={"The interval $5-10$ holds."} />);
    await waitFor(() =>
      expect(screen.getByTestId("md-math-inline").dataset.source).toBe("5-10"),
    );
  });

  test("a formula next to punctuation is still a formula", async () => {
    render(<Markdown text={"Take $x^2$, then ($y$)."} />);
    await waitFor(() =>
      expect(screen.getAllByTestId("md-math-inline")).toHaveLength(2),
    );
  });

  test("dollars in code stay code", () => {
    const { container } = render(
      <Markdown
        text={"Run `echo $HOME$PATH` now.\n\n" + fence("sh", "echo $a$")}
      />,
    );
    expect(screen.queryByTestId("md-math-inline")).toBeNull();
    expect(container.querySelector(".md-inline-code")!.textContent).toBe(
      "echo $HOME$PATH",
    );
  });

  test("a formula KaTeX cannot render shows its source, and the next formula still typesets", async () => {
    katex.render.mockImplementationOnce(() => {
      throw new Error("boom");
    });
    render(<Markdown text={"First $a+b$ then $c+d$."} />);
    await waitFor(() =>
      expect(document.querySelectorAll(".md-math-inline .katex")).toHaveLength(
        1,
      ),
    );
    const nodes = screen.getAllByTestId("md-math-inline");
    expect(nodes[0]!.tagName).toBe("CODE");
    expect(nodes[0]!.textContent).toBe("$a+b$");
  });

  test("a display formula switched to its source and back is typeset again", async () => {
    render(<Markdown text={"$$\nx^2\n$$"} />);
    await waitFor(() =>
      expect(document.querySelector(".md-math-display .katex")).toBeTruthy(),
    );
    const block = screen.getByTestId("md-math-block");
    fireEvent.click(
      block.querySelector('[data-testid="md-figure-source-toggle"]')!,
    );
    expect(block.querySelector(".md-math-display")).toBeNull();
    fireEvent.click(
      block.querySelector('[data-testid="md-figure-source-toggle"]')!,
    );
    expect(block.querySelector(".md-math-display .katex")?.textContent).toBe(
      "x^2",
    );
  });

  test("until KaTeX arrives the source is shown, and a failed load keeps it", async () => {
    loaders.katexFails = true;
    render(<Markdown text={"Energy $E = mc^2$ and\n\n$$\nx\n$$"} />);
    await new Promise((r) => setTimeout(r, 10));
    expect(screen.getByTestId("md-math-inline").textContent).toBe("$E = mc^2$");
    expect(screen.getByTestId("md-math-block").dataset.view).toBe("code");
  });
});

test("the documentation renders no formula by accident", () => {
  // Pages quote "$$", "${VAR}", "$HOME" and PHC hashes in prose and code; none
  // of it is math, and agents quote the same things in their answers.
  const docs = join(
    dirname(fileURLToPath(import.meta.url)),
    "..",
    "..",
    "..",
    "..",
    "..",
    "docs",
  );
  const pages = (readdirSync(docs, { recursive: true }) as string[]).filter(
    (p) => p.endsWith(".md"),
  );
  expect(pages.length).toBeGreaterThan(50);
  for (const page of pages) {
    const text = readFileSync(join(docs, page), "utf8");
    const { container } = render(<Markdown text={text} />);
    expect(
      container.querySelectorAll('[data-testid="md-math-inline"]').length,
      page,
    ).toBe(0);
    expect(
      container.querySelectorAll('[data-testid="md-math-block"]').length,
      page,
    ).toBe(0);
    cleanup();
  }
}, 120_000);

test("KaTeX with these options builds no link, no image and no raw HTML", async () => {
  const real = (await vi.importActual<typeof import("katex")>("katex")).default;
  const { KATEX_OPTIONS } = await import("./MathFormula");
  for (const tex of [
    "\\href{javascript:alert(1)}{x}",
    "\\url{https://example.com}",
    "\\includegraphics{https://example.com/a.png}",
    "\\htmlClass{evil}{x}",
    "\\htmlData{onclick=alert(1)}{x}",
  ]) {
    const box = document.createElement("div");
    real.render(tex, box, { ...KATEX_OPTIONS, displayMode: false });
    expect(
      box.querySelector("a, img, [href], [src], [onclick], .evil"),
      tex,
    ).toBeNull();
    // KaTeX shows the refused command as red source text instead.
    expect(box.querySelector(".katex-html")!.textContent, tex).toMatch(/^\\/);
  }
});
