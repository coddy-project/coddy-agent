/**
 * The changed-file paths arranged as a tree for the edits window's file panel.
 *
 * Pure so the shape - especially the chain collapsing - is testable without
 * rendering anything.
 */

export interface FileTreeNode {
  kind: "dir" | "file";
  /** What the row shows: a file name, or a (possibly collapsed) directory run. */
  label: string;
  /** For a file, the path exactly as the server reported it, so it can be matched
   *  back to a change; for a directory, the normalised prefix, used as a key. */
  path: string;
  children: FileTreeNode[];
}

interface Building {
  name: string;
  children: Map<string, Building>;
  /** Set on a leaf: the original, unnormalised path. */
  filePath?: string;
}

/** Split on either separator: the session scope reports OS paths, so on Windows
 *  they arrive with backslashes while the git scope uses forward slashes. */
function segmentsOf(path: string): string[] {
  return path.split(/[\\/]+/).filter((s) => s !== "");
}

export function buildFileTree(paths: string[]): FileTreeNode[] {
  const root: Building = { name: "", children: new Map() };

  for (const path of paths) {
    const segments = segmentsOf(path);
    if (segments.length === 0) {
      continue;
    }
    let node = root;
    segments.forEach((segment, i) => {
      let next = node.children.get(segment);
      if (!next) {
        next = { name: segment, children: new Map() };
        node.children.set(segment, next);
      }
      if (i === segments.length - 1) {
        next.filePath = path;
      }
      node = next;
    });
  }

  return finish(root, "");
}

/** Convert the working tree, collapsing single-child directory runs. */
function finish(node: Building, prefix: string): FileTreeNode[] {
  const out: FileTreeNode[] = [];
  for (const child of node.children.values()) {
    out.push(toNode(child, prefix));
  }
  // Directories first, then files, each alphabetically - the order a file tree
  // is read in.
  out.sort((a, b) => {
    if (a.kind !== b.kind) {
      return a.kind === "dir" ? -1 : 1;
    }
    return a.label.localeCompare(b.label);
  });
  return out;
}

function toNode(node: Building, prefix: string): FileTreeNode {
  if (node.filePath !== undefined && node.children.size === 0) {
    return { kind: "file", label: node.name, path: node.filePath, children: [] };
  }

  // A directory holding exactly one directory adds a row that says nothing on
  // its own, so the run is folded into a single "a/b/c" label.
  let label = node.name;
  let current = node;
  for (;;) {
    if (current.children.size !== 1) {
      break;
    }
    const only = current.children.values().next().value as Building;
    if (only.filePath !== undefined && only.children.size === 0) {
      break; // the single child is a file: this directory is the last row
    }
    label = label + "/" + only.name;
    current = only;
  }

  const path = prefix ? prefix + "/" + label : label;
  return { kind: "dir", label, path, children: finish(current, path) };
}

/** The paths in the order the tree shows them: folders first, each by name. */
export function treeOrder(paths: string[]): string[] {
  const out: string[] = [];
  const walk = (nodes: FileTreeNode[]) => {
    for (const node of nodes) {
      if (node.kind === "file") out.push(node.path);
      else walk(node.children);
    }
  };
  walk(buildFileTree(paths));
  return out;
}
