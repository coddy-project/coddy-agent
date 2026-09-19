/**
 * A remark plugin: every **`@coddy:<page>#<section>`** in the prose of a
 * Markdown text becomes a link to **`coddy:<page>#<section>`**, which the
 * renderer sends to the documentation reader. Code, and text that already is
 * a link, are left alone.
 */

import { splitDocMentions } from "../docs/docMentions";

type MdNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MdNode[];
};

function visit(node: MdNode): void {
  if (!node.children || node.type === "link" || node.type === "linkReference") {
    return;
  }
  const out: MdNode[] = [];
  for (const child of node.children) {
    if (child.type === "text" && child.value?.includes("@coddy:")) {
      for (const part of splitDocMentions(child.value)) {
        out.push(
          part.type === "text"
            ? { type: "text", value: part.value }
            : {
                type: "link",
                url: `coddy:${part.ref}`,
                children: [{ type: "text", value: part.literal }],
              },
        );
      }
      continue;
    }
    visit(child);
    out.push(child);
  }
  node.children = out;
}

export function remarkDocMentions() {
  return (tree: MdNode) => {
    visit(tree);
  };
}
