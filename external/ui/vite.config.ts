/// <reference types="vitest/config" />

import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backend = (process.env.CODDY_UI_BACKEND || "").trim();

export default defineConfig({
  root: "src",
  publicDir: path.resolve(import.meta.dirname, "public"),
  plugins: [react()],
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
          return "[name][extname]";
        },
        chunkFileNames: "app.js",
        inlineDynamicImports: true,
      },
    },
  },
} as any);
