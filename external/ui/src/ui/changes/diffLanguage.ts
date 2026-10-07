/**
 * Which highlight.js grammar a changed file should be read with.
 *
 * Deliberately a lookup rather than highlight.js's own auto-detection: a diff
 * hands the highlighter a few lines at a time, and auto-detection on a fragment
 * that short guesses wildly - the same file would change colour scheme between
 * one hunk and the next. An unknown extension returns "" and the line renders
 * as plain text, which is the honest answer.
 */

const BY_EXTENSION: Record<string, string> = {
  ts: "typescript",
  tsx: "typescript",
  mts: "typescript",
  cts: "typescript",
  js: "javascript",
  jsx: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  go: "go",
  py: "python",
  rb: "ruby",
  php: "php",
  rs: "rust",
  java: "java",
  kt: "kotlin",
  kts: "kotlin",
  swift: "swift",
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  cs: "csharp",
  css: "css",
  scss: "scss",
  sass: "scss",
  less: "less",
  html: "xml",
  htm: "xml",
  vue: "xml",
  svg: "xml",
  xml: "xml",
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "ini",
  ini: "ini",
  cfg: "ini",
  conf: "ini",
  properties: "ini",
  md: "markdown",
  markdown: "markdown",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  sql: "sql",
  diff: "diff",
  patch: "diff",
  gradle: "groovy",
  dockerfile: "dockerfile",
};

/** Files that carry no extension but are still a known language. */
const BY_NAME: Record<string, string> = {
  dockerfile: "dockerfile",
  makefile: "makefile",
  gnumakefile: "makefile",
};

function baseNameOf(path: string): string {
  const normalized = path.replace(/[\\/]+/g, "/");
  const cut = normalized.lastIndexOf("/");
  return cut === -1 ? normalized : normalized.slice(cut + 1);
}

export function languageForPath(path: string): string {
  const name = baseNameOf(path).toLowerCase();
  if (name === "") {
    return "";
  }
  const byName = BY_NAME[name];
  if (byName) {
    return byName;
  }
  const dot = name.lastIndexOf(".");
  if (dot <= 0 || dot === name.length - 1) {
    return "";
  }
  return BY_EXTENSION[name.slice(dot + 1)] ?? "";
}
