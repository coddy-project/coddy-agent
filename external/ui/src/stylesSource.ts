import { existsSync, readFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// The stylesheet is styles.scss, an entry that @uses the partials of styles/
// in the order of the cascade. The source-reading suites grep the stylesheet
// as one text, so this module inlines the partials where Sass emits them:
// each @use is replaced by the partial's source the first time it is used and
// by nothing after that, the way Sass loads a module once. The text is written
// for the suites' rule parsers, which know CSS and not Sass: line comments
// (`// ...` on a line of their own) are left out, as Sass leaves them out of
// the CSS, and an interpolation loses its braces (`#{mix-text(8%)}` reads
// `mix-text(8%)`), or a `[^}]*` body capture would stop at them and a rule
// would look as if it lacked every declaration after the first one. Node only:
// tests.

const srcDir = dirname(fileURLToPath(import.meta.url));

/** The entry of the stylesheet, the file Vite compiles. */
export const stylesEntryPath = join(srcDir, "styles.scss");

const useRule = /^@use\s+"([^"]+)"[^;]*;[ \t]*$/gm;
const lineComment = /^[ \t]*\/\/.*\n/gm;
const interpolation = /#\{([^{}]*)\}/g;

/** A file's source as the suites read it (see above), its @use rules still in. */
function readable(path: string): string {
  return readFileSync(path, "utf8")
    .replace(lineComment, "")
    .replace(interpolation, "$1");
}

/** The file a `@use "<url>"` written in `from` loads: `_name.scss` or `name.scss`. */
function resolveUse(from: string, url: string): string {
  const base = join(dirname(from), url);
  for (const candidate of [
    join(dirname(base), `_${basename(base)}.scss`),
    `${base}.scss`,
  ]) {
    if (existsSync(candidate)) return candidate;
  }
  throw new Error(`${from}: cannot resolve @use "${url}"`);
}

function inline(path: string, seen: Set<string>, files: string[]): string {
  seen.add(path);
  files.push(path);
  return readable(path).replace(useRule, (_, url: string) => {
    if (url.startsWith("sass:")) return ""; // Sass's own modules: no file
    const target = resolveUse(path, url);
    return seen.has(target) ? "" : inline(target, seen, files);
  });
}

let cached: { source: string; files: string[] } | undefined;

function load(): { source: string; files: string[] } {
  if (!cached) {
    const files: string[] = [];
    const source = inline(stylesEntryPath, new Set(), files);
    cached = { source, files };
  }
  return cached;
}

/** The whole stylesheet as one source text, partials inlined in cascade order. */
export function stylesSource(): string {
  return load().source;
}

/** Every file of the stylesheet, the entry first, in the order they are inlined. */
export function stylesFiles(): string[] {
  return [...load().files];
}

/**
 * One partial's source as the entry names it ("screens/swarm-map" for
 * styles/screens/_swarm-map.scss), its line comments and @use rules left out:
 * for a suite that holds a rule of one surface wherever the surface sits in
 * the cascade.
 */
export function stylesPartial(name: string): string {
  return readable(resolveUse(stylesEntryPath, `styles/${name}`)).replace(
    useRule,
    "",
  );
}
