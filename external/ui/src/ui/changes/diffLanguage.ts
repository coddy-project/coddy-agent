/**
 * Which highlight.js grammar a file should be read with, in the edits window
 * and the Files window alike.
 *
 * Deliberately a lookup rather than highlight.js's own auto-detection: a diff
 * hands the highlighter a few lines at a time, and auto-detection on a fragment
 * that short guesses wildly - the same file would change colour scheme between
 * one hunk and the next. An unknown extension returns "" and the line renders
 * as plain text, which is the honest answer. Every name here is a grammar of
 * the shared registry (markdown/syntaxLanguages.ts); a test holds the two
 * together, since a name the highlighter lacks leaves a file plain silently.
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
  pyi: "python",
  pyw: "python",
  rb: "ruby",
  rake: "ruby",
  gemspec: "ruby",
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
  hh: "cpp",
  hpp: "cpp",
  hxx: "cpp",
  ino: "arduino",
  cu: "cpp",
  cuh: "cpp",
  mm: "objectivec",
  cs: "csharp",
  vb: "vbnet",
  fs: "fsharp",
  fsi: "fsharp",
  fsx: "fsharp",
  dart: "dart",
  ex: "elixir",
  exs: "elixir",
  hs: "haskell",
  lhs: "haskell",
  scala: "scala",
  sc: "scala",
  sbt: "scala",
  ml: "ocaml",
  mli: "ocaml",
  lua: "lua",
  r: "r",
  pl: "perl",
  pm: "perl",
  ps1: "powershell",
  psm1: "powershell",
  psd1: "powershell",
  lisp: "lisp",
  el: "lisp",
  scm: "scheme",
  ss: "scheme",
  tcl: "tcl",
  f: "fortran",
  for: "fortran",
  f90: "fortran",
  f95: "fortran",
  f03: "fortran",
  adb: "ada",
  ads: "ada",
  pas: "delphi",
  dpr: "delphi",
  asm: "x86asm",
  nasm: "x86asm",
  glsl: "glsl",
  vert: "glsl",
  frag: "glsl",
  geom: "glsl",
  comp: "glsl",
  hlsl: "hlsl",
  wgsl: "wgsl",
  gd: "gdscript",
  cob: "cobol",
  cbl: "cobol",
  cpy: "cobol",
  bas: "vba",
  vba: "vba",
  hx: "haxe",
  gml: "gml",
  css: "css",
  scss: "scss",
  sass: "scss",
  less: "less",
  html: "xml",
  htm: "xml",
  xhtml: "xml",
  vue: "xml",
  svelte: "xml",
  svg: "xml",
  xml: "xml",
  xsd: "xml",
  xsl: "xml",
  xslt: "xml",
  plist: "xml",
  csproj: "xml",
  fsproj: "xml",
  vbproj: "xml",
  xaml: "xml",
  json: "json",
  jsonc: "json",
  json5: "json",
  geojson: "json",
  webmanifest: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "ini",
  ini: "ini",
  cfg: "ini",
  conf: "ini",
  properties: "ini",
  md: "markdown",
  markdown: "markdown",
  mdx: "markdown",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  ksh: "bash",
  bat: "dos",
  cmd: "dos",
  sql: "sql",
  graphql: "graphql",
  gql: "graphql",
  proto: "protobuf",
  wat: "wasm",
  wast: "wasm",
  diff: "diff",
  patch: "diff",
  gradle: "groovy",
  groovy: "groovy",
  gvy: "groovy",
  cmake: "cmake",
  mk: "makefile",
  mak: "makefile",
  dockerfile: "dockerfile",
};

/** Files that carry no extension, or one that says less than the name, but are still a known language. */
const BY_NAME: Record<string, string> = {
  dockerfile: "dockerfile",
  containerfile: "dockerfile",
  makefile: "makefile",
  gnumakefile: "makefile",
  "cmakelists.txt": "cmake",
  jenkinsfile: "groovy",
  gemfile: "ruby",
  rakefile: "ruby",
  podfile: "ruby",
  vagrantfile: "ruby",
  brewfile: "ruby",
  ".bashrc": "bash",
  ".bash_profile": "bash",
  ".profile": "bash",
  ".zshrc": "bash",
  ".zprofile": "bash",
  ".env": "ini",
  ".editorconfig": "ini",
  ".gitconfig": "ini",
};

/**
 * Names that begin with a known name and go on after a dot: `Dockerfile.dev`,
 * `.env.local`. The part after the dot names a variant, not a language.
 */
const BY_NAME_PREFIX: Record<string, string> = {
  "dockerfile.": "dockerfile",
  "containerfile.": "dockerfile",
  ".env.": "ini",
};

/** Every grammar name the tables above hand the highlighter. */
export function everyMappedLanguage(): string[] {
  return [
    ...new Set([
      ...Object.values(BY_EXTENSION),
      ...Object.values(BY_NAME),
      ...Object.values(BY_NAME_PREFIX),
    ]),
  ];
}

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
  for (const [prefix, language] of Object.entries(BY_NAME_PREFIX)) {
    if (name.startsWith(prefix) && name.length > prefix.length) {
      return language;
    }
  }
  const dot = name.lastIndexOf(".");
  if (dot <= 0 || dot === name.length - 1) {
    return "";
  }
  return BY_EXTENSION[name.slice(dot + 1)] ?? "";
}
