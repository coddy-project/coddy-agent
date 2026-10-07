import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";

const uiRoot = path.resolve(import.meta.dirname);
const dist = path.join(uiRoot, "dist");

await mkdir(uiRoot, { recursive: true });

const indexSrc = path.join(dist, "index.html");
const html = await readFile(indexSrc, "utf8");

const patched = html
  .replaceAll(/href="\.\/?styles\.css"/g, 'href="/styles.css"')
  .replaceAll(/src="\.\/?app\.js"/g, 'src="/app.js"')
  .replaceAll(/href="\/styles\.css"/g, 'href="/styles.css"')
  .replaceAll(/src="\/app\.js"/g, 'src="/app.js"');

await writeFile(path.join(uiRoot, "index.html"), patched);
await cp(path.join(dist, "styles.css"), path.join(uiRoot, "styles.css"));
await cp(path.join(dist, "app.js"), path.join(uiRoot, "app.js"));
await cp(
  path.join(dist, "events-worker.js"),
  path.join(uiRoot, "events-worker.js"),
);

// The chunks loaded on demand (Mermaid, KaTeX and its fonts). Their names carry
// a content hash, so the folder is replaced whole: a stale chunk from an older
// build must not end up embedded next to the new ones. Source maps stay behind.
const chunksDest = path.join(uiRoot, "chunks");
await rm(chunksDest, { recursive: true, force: true });
await cp(path.join(dist, "chunks"), chunksDest, {
  recursive: true,
  filter: (src) => !src.endsWith(".map"),
});

const docsAssets = path.join(uiRoot, "..", "..", "docs", "assets");
const faviconFiles = [
  ["coddy-logo-mark-flat.svg", "coddy-favicon.svg"],
  ["favicon-32.png", "favicon-32.png"],
  ["favicon.ico", "favicon.ico"],
  ["apple-touch-icon.png", "apple-touch-icon.png"],
];
for (const [srcName, destName] of faviconFiles) {
  await cp(path.join(docsAssets, srcName), path.join(uiRoot, destName));
}
