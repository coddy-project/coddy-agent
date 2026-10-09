import { expect, test } from "vitest";
import { htmlPreviewDocument } from "./htmlPreview";

function parse(doc: string): Document {
  return new DOMParser().parseFromString(doc, "text/html");
}

// The preview is a document with nothing to fetch: its own policy comes
// first, before anything that could load.
test("the preview document opens with a policy that loads nothing", () => {
  const doc = parse(htmlPreviewDocument("<p>hello</p>"));
  const first = doc.head.firstElementChild;
  expect(first?.getAttribute("http-equiv")).toBe("Content-Security-Policy");
  const policy = first?.getAttribute("content") || "";
  expect(policy).toContain("default-src 'none'");
  expect(policy).toContain("style-src 'unsafe-inline'");
  expect(policy).toContain("img-src data:");
  expect(policy).not.toContain("script-src");
  expect(doc.body.textContent).toBe("hello");
});

test("scripts, a base, links and http-equiv metas do not reach the preview", () => {
  const doc = parse(
    htmlPreviewDocument(
      [
        "<html><head>",
        '<meta http-equiv="refresh" content="0;url=https://example.test/">',
        '<meta http-equiv="Content-Security-Policy" content="img-src *">',
        '<base href="https://example.test/">',
        '<link rel="stylesheet" href="https://example.test/a.css">',
        '<link rel="dns-prefetch" href="https://example.test/">',
        "<script>window.stolen = true</script>",
        "</head><body><h1>Report</h1>",
        '<script src="https://example.test/x.js"></script>',
        "</body></html>",
      ].join(""),
    ),
  );
  expect(doc.querySelector("script, base, link")).toBeNull();
  const metas = [...doc.querySelectorAll("meta[http-equiv]")];
  expect(metas).toHaveLength(1);
  expect(metas[0]?.getAttribute("content")).toContain("default-src 'none'");
  expect(doc.querySelector("h1")?.textContent).toBe("Report");
});

// A link would take the preview's frame to another site; it asks for a new
// window, which the frame's sandbox refuses. A link inside the page works.
test("a link out of the page asks for a window the sandbox refuses, one inside it works", () => {
  const doc = parse(
    htmlPreviewDocument(
      '<a id="out" href="https://example.test/" target="_self">out</a>' +
        '<a id="in" href="#part">in</a><h2 id="part">Part</h2>',
    ),
  );
  expect(doc.getElementById("out")?.getAttribute("target")).toBe("_blank");
  expect(doc.getElementById("in")?.hasAttribute("target")).toBe(false);
});

test("inline styles and embedded pictures stay", () => {
  const doc = parse(
    htmlPreviewDocument(
      '<style>h1 { color: red }</style><h1 style="font-size: 2em">T</h1>' +
        '<img src="data:image/png;base64,AAAA" alt="x">',
    ),
  );
  expect(doc.querySelector("style")?.textContent).toContain("color: red");
  expect(doc.querySelector("h1")?.getAttribute("style")).toContain("2em");
  expect(doc.querySelector("img")?.getAttribute("src")).toMatch(/^data:/);
});

test("the page keeps its doctype, and one without stays without", () => {
  expect(htmlPreviewDocument("<!DOCTYPE html><p>x</p>")).toMatch(
    /^<!DOCTYPE html>\n<html>/,
  );
  expect(htmlPreviewDocument("<p>x</p>")).toMatch(/^<html>/);
});

// An inline SVG's link names its address in xlink:href (or href), which the
// HTML link selector does not reach; it asks for a window all the same.
test("a link of an inline SVG asks for a window the sandbox refuses", () => {
  const doc = parse(
    htmlPreviewDocument(
      '<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">' +
        '<a id="old" xlink:href="https://example.test/old"><text>old</text></a>' +
        '<a id="new" href="https://example.test/new"><text>new</text></a>' +
        '<a id="part" xlink:href="#part"><text>part</text></a></svg>',
    ),
  );
  expect(doc.getElementById("old")?.getAttribute("target")).toBe("_blank");
  expect(doc.getElementById("new")?.getAttribute("target")).toBe("_blank");
  expect(doc.getElementById("part")?.hasAttribute("target")).toBe(false);
});
