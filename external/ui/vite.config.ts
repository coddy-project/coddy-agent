/// <reference types="vitest/config" />

import path from "node:path";
import { readFileSync, readdirSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

const backend = (process.env.CODDY_UI_BACKEND || "").trim();

// Retain vendored grammar notices in the embedded JS distribution. The minifier
// otherwise strips source comments, including comments marked as licenses.
const grammarDirectory = path.resolve(
  import.meta.dirname,
  "src/ui/markdown/grammars",
);
const syntaxLicenseBanner = readdirSync(grammarDirectory)
  .filter((name) => name.endsWith(".LICENSE"))
  .sort()
  .map(
    (name) =>
      `/* ${name}\n${readFileSync(path.join(grammarDirectory, name), "utf8")}\n*/`,
  )
  .join("\n");

const syntaxLicensePlugin: Plugin = {
  name: "syntax-grammar-notices",
  enforce: "post",
  generateBundle(_options, bundle) {
    for (const output of Object.values(bundle)) {
      // The grammars are bundled into the entry; the lazy chunks carry their
      // own libraries' notices.
      if (output.type === "chunk" && output.isEntry)
        output.code += `\n${syntaxLicenseBanner}\n`;
    }
  },
};

// KaTeX's stylesheet lists every font three times (woff2, woff, ttf), and Vite
// emits whatever a stylesheet names, so the binary would embed all sixty files.
// Every browser the SPA supports reads woff2: keep that source only.
const katexWoff2OnlyPlugin: Plugin = {
  name: "katex-woff2-only",
  enforce: "pre",
  transform(code, id) {
    if (!/[\\/]katex[\\/]dist[\\/]katex(\.min)?\.css$/.test(id)) return null;
    return code.replace(
      /,\s*url\([^)]*\.woff\)\s*format\("woff"\)\s*,\s*url\([^)]*\.ttf\)\s*format\("truetype"\)/g,
      "",
    );
  },
};

export default defineConfig({
  root: "src",
  publicDir: path.resolve(import.meta.dirname, "public"),
  plugins: [react(), syntaxLicensePlugin, katexWoff2OnlyPlugin],
  test: {
    environment: "jsdom",
    setupFiles: ["./vitest.setup.ts"],
  },
  server: {
    port: 5173,
    strictPort: true,
    ...(backend
      ? {
          proxy: {
            "/v1": backend,
            "/coddy": backend,
            // A relay answers here, and mounts every node it reaches beneath
            // it, so a dev run pointed at one needs this too.
            "/swarm": backend,
            "/docs": backend,
            "/openapi.yaml": backend,
            "/openapi.json": backend,
          },
        }
      : {}),
  },
  build: {
    // Lightning CSS (Vite 8 default) can drop unprefixed `backdrop-filter` when `-webkit-backdrop-filter`
    // is present, which breaks blur in Firefox (and some stacks). Esbuild preserves both declarations.
    cssMinify: "esbuild",
    outDir: "../dist",
    emptyOutDir: true,
    sourcemap: true,
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        entryFileNames: "app.js",
        assetFileNames: (assetInfo: { name?: string | undefined }) => {
          if (assetInfo.name === "style.css") {
            return "styles.css";
          }
          // KaTeX's fonts: fetched from the chunk folder on first use.
          if (/\.(woff2?|ttf)$/.test(assetInfo.name || "")) {
            return "chunks/[name]-[hash][extname]";
          }
          return "[name][extname]";
        },
        // The renderers loaded on demand (Mermaid, KaTeX) and what they share.
        // Content-hashed, so ui.Handler() lets a browser keep them for good;
        // app.js itself keeps its fixed name.
        chunkFileNames: "chunks/[name]-[hash].js",
      },
    },
  },
  // The SharedWorker that holds GET /coddy/events for every tab
  // (src/ui/chat/eventsWorker.ts). It is its own script by necessity, so it gets a
  // fixed name next to app.js for go:embed. IIFE, not ES: the file then has no
  // import or export and runs whether a browser honours `type: "module"` or not.
  worker: {
    format: "iife",
    rollupOptions: {
      output: {
        entryFileNames: "events-worker.js",
      },
    },
  },
} as any);
