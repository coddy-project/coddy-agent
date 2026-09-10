import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { Markdown } from "./Markdown";
import fixtures from "./neuraldeepLanguageFixtures.json";

afterEach(cleanup);

test("Prolog highlights variables and numbers even when unquoted atoms stay plain", () => {
  const { container } = render(
    <Markdown text={'```prolog\nanswer(X) :- X = 42, write("hello").\n```'} />,
  );
  expect(container.querySelector(".hljs-symbol")?.textContent).toBe("X");
  expect(container.querySelector(".hljs-number")?.textContent).toBe("42");
  expect(container.querySelector(".hljs-string")?.textContent).toBe('"hello"');
});

test.each(["urq", "urql", "tads", "tads3", "unrealscript"])(
  "leaves unsupported %s labels as literal text without guessing",
  (label) => {
    const source = '<script>alert("example")</script>';
    const { container } = render(
      <Markdown text={`\`\`\`${label}\n${source}\n\`\`\``} />,
    );
    const code = container.querySelector("pre code");
    expect(code?.querySelector("span, script")).toBeNull();
    expect(code?.textContent).toBe(`${source}\n`);
  },
);

// Captured from NeuralDeep; replayed offline without credentials or model calls.
test.each(fixtures)("renders NeuralDeep $language ($label)", (fixture) => {
  const { container } = render(<Markdown text={fixture.markdown} />);
  const code = container.querySelector("pre code");
  expect(code).not.toBeNull();
  const original = fixture.markdown.split("\n").slice(1, -1).join("\n");
  expect(code?.textContent).toBe(`${original}\n`);
  if (fixture.highlight) {
    expect(code?.querySelector('span[class*="hljs-"]')).not.toBeNull();
  } else {
    expect(code?.querySelector("span")).toBeNull();
  }
  expect(code?.querySelector("script, style, iframe")).toBeNull();
});

test.each([
  ["react", 'const View = () => <div title="hello" />;'],
  ["react-jsx", 'const View = () => <div title="hello" />;'],
  ["react-tsx", "const count: number = 42;"],
  ["asm", "mov eax, 42 ; answer"],
  ["xslt", '<xsl:value-of select="title"/>'],
  ["objectpascal", "begin Writeln('hello'); end."],
  ["object-pascal", "begin Writeln('hello'); end."],
  ["game-maker-language", "var score = 42;"],
  ["gd", "extends Node\nvar score = 42"],
  ["objective-c", "@interface Example : NSObject\n@end"],
  ["visualbasic", 'Dim message As String = "hello"'],
  ["pl/sql", "BEGIN SELECT 42 INTO answer FROM dual; END;"],
  ["transactsql", "DECLARE @answer int = 42; SELECT @answer;"],
  ["opencl", "__kernel void add(__global float* values) { values[0] = 1.0f; }"],
  ["cuda", "__global__ void add(float* values) { values[0] = 1.0f; }"],
])("highlights the %s fence alias", (label, source) => {
  const { container } = render(
    <Markdown text={`\`\`\`${label}\n${source}\n\`\`\``} />,
  );
  const code = container.querySelector("pre code");
  expect(code?.querySelector('span[class*="hljs-"]')).not.toBeNull();
  expect(code?.textContent).toBe(`${source}\n`);
});
