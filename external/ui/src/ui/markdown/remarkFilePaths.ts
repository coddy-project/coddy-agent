/**
 * A remark plugin: every path in the prose of a Markdown text
 * (internal/foo.go, ./run.sh:12) becomes a link to **`coddy-file:<path>`**,
 * which the renderer draws as a link to the Files window once the server says
 * the path is a file of the chat's workspace, and as the text it was
 * otherwise (FileReference.tsx). Code, and text that already is a link, are
 * left alone: inline code is read by the renderer itself.
 */

import { splitProsePaths } from "./fileReferences";

type MdNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MdNode[];
};

/** The scheme of the links this plugin writes. */
export const FILE_LINK_SCHEME = "coddy-file:";

function visit(node: MdNode): void {
  if (!node.children || node.type === "link" || node.type === "linkReference") {
    return;
  }
  const out: MdNode[] = [];
  for (const child of node.children) {
    if (child.type === "text" && child.value?.includes("/")) {
      for (const part of splitProsePaths(child.value)) {
        out.push(
          part.type === "text"
            ? { type: "text", value: part.value }
            : {
                type: "link",
                url: FILE_LINK_SCHEME + encodeURIComponent(part.value),
                children: [{ type: "text", value: part.value }],
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

export function remarkFilePaths() {
  return (tree: MdNode) => {
    visit(tree);
  };
}
