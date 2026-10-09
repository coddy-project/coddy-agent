import { describe, expect, it } from "vitest";
import { isLiteralDollarMath, normalizeMathDelimiters } from "./mathDelimiters";

describe("normalizeMathDelimiters", () => {
  it("turns \\( \\) into inline dollars", () => {
    expect(normalizeMathDelimiters("Energy \\(E = mc^2\\) here")).toBe(
      "Energy $E = mc^2$ here",
    );
  });

  it("turns \\[ \\] into a display block on its own lines", () => {
    expect(normalizeMathDelimiters("Sum:\n\\[\n\\sum_i x_i\n\\]\nend")).toBe(
      "Sum:\n$$\n\\sum_i x_i\n$$\nend",
    );
    expect(normalizeMathDelimiters("Sum: \\[ a+b \\] end")).toBe(
      "Sum: \n$$\na+b\n$$\n end",
    );
  });

  it("leaves fenced code untouched", () => {
    const text = "```latex\n\\(x\\) and \\[y\\]\n```\nthen \\(z\\)";
    expect(normalizeMathDelimiters(text)).toBe(
      "```latex\n\\(x\\) and \\[y\\]\n```\nthen $z$",
    );
  });

  it("leaves tilde fences and indented fences untouched", () => {
    const text = "  ~~~\n  \\(x\\)\n  ~~~\n";
    expect(normalizeMathDelimiters(text)).toBe(text);
  });

  it("leaves inline code spans untouched", () => {
    expect(normalizeMathDelimiters("run `echo \\(x\\)` now \\(y\\)")).toBe(
      "run `echo \\(x\\)` now $y$",
    );
    expect(normalizeMathDelimiters("``a ` \\(x\\)`` b")).toBe(
      "``a ` \\(x\\)`` b",
    );
  });

  it("does not pair delimiters across a blank line", () => {
    const text = "open \\( here\n\nclose \\) there";
    expect(normalizeMathDelimiters(text)).toBe(text);
  });

  it("keeps an escaped backslash before a paren", () => {
    // \\( is a literal backslash followed by a paren in Markdown.
    expect(normalizeMathDelimiters("path C:\\\\(x) ok")).toBe(
      "path C:\\\\(x) ok",
    );
  });

  it("leaves escaped brackets and parentheses in prose alone", () => {
    for (const text of [
      "item a\\[0\\] here",
      "as noted \\(see below\\) now",
      "x\\(y\\)",
      "\\[0\\]",
    ]) {
      expect(normalizeMathDelimiters(text)).toBe(text);
    }
  });

  it("converts short names and operators", () => {
    expect(normalizeMathDelimiters("let \\(n\\) and \\(i+1\\)")).toBe(
      "let $n$ and $i+1$",
    );
  });

  it("keeps a display formula inside its quote or list item", () => {
    expect(
      normalizeMathDelimiters("> The formula \\[x^2\\] is here\n> and more"),
    ).toBe("> The formula $$x^2$$ is here\n> and more");
    expect(normalizeMathDelimiters("- see \\[a+b\\] there\n- next")).toBe(
      "- see $$a+b$$ there\n- next",
    );
  });

  it("does not pair delimiters across list items or quote depths", () => {
    for (const text of ["- \\(a+\n- b\\)", "> \\(a+\nb\\)", "\\(a+\n> b\\)"]) {
      expect(normalizeMathDelimiters(text)).toBe(text);
    }
  });

  it("leaves indented code and fences inside quotes untouched", () => {
    for (const text of [
      "Run:\n\n    echo \\(n\\)\n",
      "- find\n\n      find . \\( -name a -o -name b \\)",
      "> ~~~\n> echo \\(x+1\\)\n> ~~~",
    ]) {
      expect(normalizeMathDelimiters(text)).toBe(text);
    }
  });

  it("does not pair across a CRLF blank line", () => {
    const text = "\\(a\r\n\r\nb+c\\)";
    expect(normalizeMathDelimiters(text)).toBe(text);
  });

  it("returns text without delimiters or dollar pairs unchanged", () => {
    const text = "Plain $5 text with (parens) and [brackets].";
    expect(normalizeMathDelimiters(text)).toBe(text);
  });

  it("escapes a dollar whose partner sits in a code span, and a literal pair's opener", () => {
    expect(normalizeMathDelimiters("Set ${A}/x and `echo $B` now")).toBe(
      "Set \\${A}/x and `echo $B` now",
    );
    expect(normalizeMathDelimiters("from $10 to $25 a month")).toBe(
      "from \\$10 to $25 a month",
    );
    expect(normalizeMathDelimiters("Take $x^2$ and `a$b`")).toBe(
      "Take $x^2$ and `a$b`",
    );
    expect(normalizeMathDelimiters("$$\nx\n$$")).toBe("$$\nx\n$$");
  });

  // A long page is mostly prose full of code spans (the web UI guide: 300 KB,
  // thousands of spans). Looking every span up at every character made the
  // pass quadratic and took two seconds there, on every render of the page.
  it("stays linear in the code spans of a long stretch of prose", () => {
    const text = "Costs $5 here. " + "Run `cmd` then `x`. ".repeat(20_000);
    const started = performance.now();
    expect(normalizeMathDelimiters(text)).toBe(text);
    expect(performance.now() - started).toBeLessThan(2000);
  });
});

describe("isLiteralDollarMath", () => {
  it("treats prices and padded spans as text", () => {
    expect(isLiteralDollarMath("5 and ")).toBe(true);
    expect(isLiteralDollarMath(" x")).toBe(true);
    expect(isLiteralDollarMath("")).toBe(true);
  });

  it("treats tight spans as math", () => {
    expect(isLiteralDollarMath("x^2")).toBe(false);
    expect(isLiteralDollarMath("\\alpha + \\beta")).toBe(false);
  });
});
