import { cp, mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const uiRoot = path.resolve(import.meta.dirname);
const dist = path.join(uiRoot, 'dist');

await mkdir(uiRoot, { recursive: true });

const indexSrc = path.join(dist, 'index.html');
const html = await readFile(indexSrc, 'utf8');

const patched = html
  .replaceAll(/href="\.\/?styles\.css"/g, 'href="/styles.css"')
  .replaceAll(/src="\.\/?app\.js"/g, 'src="/app.js"')
  .replaceAll(/href="\/styles\.css"/g, 'href="/styles.css"')
  .replaceAll(/src="\/app\.js"/g, 'src="/app.js"');

await writeFile(path.join(uiRoot, 'index.html'), patched);
await cp(path.join(dist, 'styles.css'), path.join(uiRoot, 'styles.css'));
await cp(path.join(dist, 'app.js'), path.join(uiRoot, 'app.js'));
