import { cp, mkdir, readFile, writeFile } from "node:fs/promises";
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
