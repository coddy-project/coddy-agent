const FRONTMATTER_DELIM = "---";

/** Splits a .plan.md file into YAML frontmatter and markdown body (matches server parser). */
export function splitPlanFileContent(raw: string): {
  frontmatter: string;
  body: string;
} {
  let s = raw.replace(/^\uFEFF/, "").trim();
  if (!s.startsWith(FRONTMATTER_DELIM)) {
    return {
      frontmatter: "",
      body: raw.replace(/\r\n/g, "\n").replace(/\n+$/, ""),
    };
  }
  let rest = s.slice(FRONTMATTER_DELIM.length).replace(/^\r?\n/, "");
  const closeIdx = rest.indexOf("\n" + FRONTMATTER_DELIM);
  if (closeIdx < 0) {
    return {
      frontmatter: "",
      body: raw.replace(/\r\n/g, "\n").replace(/\n+$/, ""),
    };
  }
  const frontmatter = rest.slice(0, closeIdx).trim();
  const body = rest
    .slice(closeIdx + FRONTMATTER_DELIM.length + 1)
    .replace(/^\r?\n/, "")
    .replace(/\n+$/, "");
  return { frontmatter, body };
}

/** Body for the plan editor: prefer API body, else strip frontmatter from full content. */
export function planEditorBody(content: string, bodyField?: string): string {
  const b = (bodyField ?? "").trim();
  if (b) return bodyField ?? "";
  return splitPlanFileContent(content).body;
}
