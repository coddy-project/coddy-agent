import { describe, expect, test } from "vitest";
import { buildFileTree, treeOrder } from "./fileTree";

describe("buildFileTree", () => {
  test("groups files under their directories", () => {
    const tree = buildFileTree(["docs/ui.md", "docs/api.md", "README.md"]);
    expect(tree.map((n) => n.label)).toEqual(["docs", "README.md"]);
    const docs = tree[0]!;
    expect(docs.kind).toBe("dir");
    expect(docs.children.map((c) => c.label)).toEqual(["api.md", "ui.md"]);
  });

  test("collapses a chain of single-child directories into one row", () => {
    const tree = buildFileTree(["external/ui/src/markdown/Markdown.tsx"]);
    expect(tree).toHaveLength(1);
    // The whole chain is one node, so a deep path does not cost four rows.
    expect(tree[0]!.label).toBe("external/ui/src/markdown");
    expect(tree[0]!.children.map((c) => c.label)).toEqual(["Markdown.tsx"]);
  });

  test("stops collapsing where a directory branches", () => {
    const tree = buildFileTree([
      "external/ui/a.ts",
      "external/docs/b.md",
    ]);
    expect(tree).toHaveLength(1);
    expect(tree[0]!.label).toBe("external");
    expect(tree[0]!.children.map((c) => c.label)).toEqual(["docs", "ui"]);
  });

  test("carries the full path on file nodes so a click can scroll to it", () => {
    const tree = buildFileTree(["a/b/c.txt"]);
    const file = tree[0]!.children[0]!;
    expect(file.kind).toBe("file");
    expect(file.path).toBe("a/b/c.txt");
  });

  test("handles Windows separators, which is what the session scope reports", () => {
    const tree = buildFileTree(["src\\ui\\App.tsx"]);
    expect(tree[0]!.label).toBe("src/ui");
    expect(tree[0]!.children[0]!.path).toBe("src\\ui\\App.tsx");
  });

  test("sorts directories before files", () => {
    const tree = buildFileTree(["z.txt", "a/inner.txt"]);
    expect(tree.map((n) => n.kind)).toEqual(["dir", "file"]);
  });
});

describe("treeOrder", () => {
  // The diffs read in the order the tree lists them, so the mark that follows
  // the reader's scroll walks the tree from top to bottom.
  test("lists the paths the way the tree reads, folders first", () => {
    expect(
      treeOrder(["README.md", "src/z.go", "docs/b.md", "src/a/x.go", "docs/a.md"]),
    ).toEqual(["docs/a.md", "docs/b.md", "src/a/x.go", "src/z.go", "README.md"]);
  });
});
