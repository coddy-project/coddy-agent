/**
 * A remark plugin: every line ending in the text of a Markdown body, which
 * CommonMark reads as a soft break (a space), becomes a line break, the way
 * Obsidian renders a note or a canvas card while "Strict line breaks" is off
 * and the way remark-breaks does. Only text nodes are split, so a code block,
 * inline code, a formula and raw HTML keep their newlines as they are.
 */

type MdNode = {
  type: string;
  value?: string;
  children?: MdNode[];
};

const LINE_ENDING = /\r\n|\r|\n/;

function visit(node: MdNode): void {
  if (!node.children) return;
  const out: MdNode[] = [];
  for (const child of node.children) {
    if (child.type === "text" && LINE_ENDING.test(child.value ?? "")) {
      child.value!.split(LINE_ENDING).forEach((part, i) => {
        if (i > 0) out.push({ type: "break" });
        if (part) out.push({ type: "text", value: part });
      });
      continue;
    }
    visit(child);
    out.push(child);
  }
  node.children = out;
}

export function remarkSoftBreaks() {
  return (tree: MdNode) => {
    visit(tree);
  };
}
