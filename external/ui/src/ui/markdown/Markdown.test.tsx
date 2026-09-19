import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { Markdown } from "./Markdown";

const stylesPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "..",
  "styles.css",
);

afterEach(() => cleanup());

test.each([
  ["js", 'const message = "hello";', ".hljs-keyword"],
  ["javascript", 'const message = "hello";', ".hljs-string"],
  ["css", ".card { color: red; }", ".hljs-attribute"],
  ["postcss", ".table-wrapper { display: flex; }", ".hljs-attribute"],
  [
    "vue",
    '<!-- before --> <VirtualCheckboxGroup :container-height="60" />',
    ".hljs-name",
  ],
  ["html", '<div class="card">Hello</div>', ".hljs-attr"],
  ["json", '{"enabled": true}', ".hljs-attr"],
  ["ts", "const count: number = 42;", ".hljs-number"],
  ["python", "def greet():\n    return True", ".hljs-keyword"],
  ["go", "package main\nfunc main() {}", ".hljs-keyword"],
])("highlights an explicitly labelled %s block", (language, source, token) => {
  const { container } = render(
    <Markdown text={`\`\`\`${language}\n${source}\n\`\`\``} />,
  );
  const code = container.querySelector("pre code");
  expect(code?.querySelector(token)).not.toBeNull();
  expect(code?.textContent).toBe(`${source}\n`);
});

test.each(["", "unknown-language", "text"])(
  "keeps %s code literal without guessing",
  (language) => {
    const source = '<script>alert("hello")</script>';
    const { container } = render(
      <Markdown text={`\`\`\`${language}\n${source}\n\`\`\``} />,
    );
    expect(container.querySelector("pre code")?.textContent).toBe(
      `${source}\n`,
    );
    expect(container.querySelector("pre code span, script")).toBeNull();
  },
);

test("streamed incomplete fences highlight and copy the original source", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  const { container, rerender } = render(
    <Markdown text={'```js\nconst message = "hel'} />,
  );
  expect(container.querySelector(".hljs-keyword")?.textContent).toBe("const");
  rerender(<Markdown text={'```js\nconst message = "hello";\n```'} />);
  fireEvent.click(screen.getByRole("button", { name: /copy code/i }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith('const message = "hello";'),
  );
});

test("postcss fences highlight CSS tokens and copy the original source", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  const source =
    ".table-container { width: 100%; flex: 1; /* container */ }\n.table-wrapper { display: flex; flex-direction: column; }";
  const { container } = render(
    <Markdown text={`\`\`\`postcss\n${source}\n\`\`\``} />,
  );
  const code = container.querySelector("pre code.language-postcss");
  expect(code?.querySelector(".hljs-selector-class")?.textContent).toBe(
    ".table-container",
  );
  expect(code?.querySelector(".hljs-attribute")?.textContent).toBe("width");
  expect(code?.querySelector(".hljs-number")?.textContent).toBe("100%");
  expect(code?.querySelector(".hljs-comment")?.textContent).toBe(
    "/* container */",
  );
  fireEvent.click(screen.getByRole("button", { name: /copy code/i }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(source));
});

test("vue fences highlight markup and embedded JavaScript and CSS without executing them", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  const source = [
    '<template><!-- before --><VirtualCheckboxGroup :container-height="60" /></template>',
    '<script setup>const message = "hello";</script>',
    "<style scoped>.table-wrapper { display: flex; }</style>",
  ].join("\n");
  const { container, rerender } = render(
    <Markdown text={`\`\`\`vue\n${source}`} />,
  );
  expect(container.querySelector(".hljs-name")?.textContent).toBe("template");
  rerender(<Markdown text={`\`\`\`vue\n${source}\n\`\`\``} />);
  const code = container.querySelector("pre code.language-vue");
  expect(code?.querySelector(".hljs-comment")?.textContent).toBe(
    "<!-- before -->",
  );
  expect(code?.querySelector(".hljs-attr")?.textContent).toBe(
    ":container-height",
  );
  expect(
    code?.querySelector(".hljs-keyword")?.textContent,
  ).toBe("const");
  expect(
    code?.querySelector(".hljs-attribute")?.textContent,
  ).toBe("display");
  expect(code?.querySelector("script, style, template")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /copy code/i }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(source));
});

test("coddy-skill links render as chip spans", () => {
  render(<Markdown text="Try [/demo](coddy-skill:demo) now." />);
  const chip = screen.getByTestId("coddy-skill-span");
  expect(chip).toHaveAttribute("data-skill-name", "demo");
  expect(chip.textContent).toBe("/demo");
});

test("fenced code block wrapper keeps symmetric vertical margin in styles", () => {
  const css = readFileSync(stylesPath, "utf8");
  const m = /\.md-code\s*\{[^}]*\}/.exec(css);
  expect(m).not.toBeNull();
  expect(m![0]).toMatch(/margin:\s*12px\s+0/);
});

test("tables render inside horizontal scroll wrapper", () => {
  render(<Markdown text={`| A | B |\n| --- | --- |\n| one | two |`} />);
  const wrap = document.querySelector(".md-table-scroll");
  expect(wrap).not.toBeNull();
  expect(wrap?.querySelector("table")).not.toBeNull();
});

test("fenced code without language still shows Copy control", () => {
  const md = ["```", "AGENTS.md", "```"].join("\n");
  render(<Markdown text={md} />);
  expect(
    screen.getByRole("button", { name: /copy code/i }),
  ).toBeInTheDocument();
});

test("inline backtick code uses md-inline-code and copies on click", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });

  render(
    <Markdown text="Edit `docker-compose.yaml` and add `./qbittorrent:/config`." />,
  );

  const inlineCodes = screen.getAllByRole("button", { name: /copy code/i });
  const inline = inlineCodes[0]!;
  expect(inline).toHaveClass("md-inline-code");
  expect(inline.textContent).toBe("docker-compose.yaml");

  fireEvent.click(inline);
  expect(writeText).toHaveBeenCalledWith("docker-compose.yaml");
  await waitFor(() => {
    expect(inline).toHaveAttribute("title", "Copied");
  });
});

test("inline code uses native title tooltip for Copy", () => {
  render(<Markdown text="Run `make test` locally." />);
  const inline = screen.getByTestId("md-inline-code");
  expect(inline).toHaveAttribute("title", "Copy");
});

test("inline code styles use grey fill without border in css", () => {
  const css = readFileSync(stylesPath, "utf8");
  expect(css).toMatch(/--coddy-md-inline-code-fg:/);
  expect(css).toMatch(/--coddy-md-inline-code-bg:/);
  expect(css).not.toMatch(/--coddy-md-inline-code-bd:/);
  expect(css).toMatch(/\.md-inline-code[\s\S]*cursor:\s*pointer/);
  expect(css).toMatch(/\.md-inline-code[\s\S]*border-radius:\s*6px/);
  expect(css).toMatch(/\.md-inline-code[\s\S]*display:\s*inline-flex/);
  expect(css).toMatch(/\.md-inline-code[\s\S]*padding:\s*5px\s+7px\s+3px/);
  expect(css).toMatch(/\.md-inline-code[\s\S]*line-height:\s*10px/);
  expect(css).not.toMatch(/\.md-inline-code-inner/);
  expect(css).not.toMatch(/\.md-inline-code-tip/);
});

// A page of the built-in documentation links to another as
// coddy:<slug>#<anchor>, and the agent quotes pages the same way: the link
// opens the reader in the app instead of being dropped as an unknown scheme.
test("coddy: links open the documentation reader", () => {
  render(
    <Markdown text="See [Completion](coddy:features/mentions#completion) and [config](coddy:reference/config)." />,
  );
  const section = screen.getByText("Completion").closest("a");
  expect(section?.getAttribute("href")).toBe("#/docs/features/mentions#completion");
  expect(section?.getAttribute("target")).toBeNull();
  expect(screen.getByText("config").closest("a")?.getAttribute("href")).toBe(
    "#/docs/reference/config",
  );
});

test("an image or a link cannot carry a script", () => {
  const { container } = render(
    <Markdown text="![x](javascript:alert(1)) [y](javascript:alert(2)) [z](coddy:features/modes)" />,
  );
  expect(container.querySelector("img")?.getAttribute("src") || "").not.toMatch(/javascript/i);
  for (const a of Array.from(container.querySelectorAll("a"))) {
    expect(a.getAttribute("href") || "").not.toMatch(/javascript/i);
  }
});

test("images are loaded lazily and fit the column", () => {
  const { container } = render(
    <Markdown text="![shot](https://raw.githubusercontent.com/x/y/main/a.png)" />,
  );
  expect(container.querySelector("img")?.getAttribute("loading")).toBe("lazy");
  const css = readFileSync(stylesPath, "utf8");
  expect(css).toMatch(/\.md img\s*\{[^}]*max-width:\s*100%/);
});

// The agent points at a page as @coddy:<page>#<section>, the way the user
// mentions one: in an answer that is a link to the reader too.
test("an @coddy: reference in an answer opens the documentation reader", () => {
  const { container } = render(
    <Markdown text={"Read @coddy:operate/swarm#two-transports first. `@coddy:not/in/code` stays code."} />,
  );
  const link = container.querySelector("a.md-docs-link");
  expect(link?.textContent).toBe("@coddy:operate/swarm#two-transports");
  expect(link?.getAttribute("href")).toBe("#/docs/operate/swarm#two-transports");
  expect(container.querySelectorAll("a.md-docs-link")).toHaveLength(1);
});

// A video of the documentation is fetched from GitHub when it plays; the
// binary carries none.
test("a video file in an image slot plays in a video element", () => {
  const { container } = render(
    <Markdown text="![Video: swarm.mp4](https://raw.githubusercontent.com/x/y/1.1.55/docs/assets/video/swarm.mp4)" />,
  );
  const video = container.querySelector("video");
  expect(video?.getAttribute("src")).toBe(
    "https://raw.githubusercontent.com/x/y/1.1.55/docs/assets/video/swarm.mp4#t=0.1",
  );
  expect(video?.hasAttribute("controls")).toBe(true);
  expect(video?.getAttribute("preload")).toBe("metadata");
  expect(container.querySelector("img")).toBeNull();
});
