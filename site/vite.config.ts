import { resolve } from "node:path";
import { defineConfig } from "vite";

// Builds the browser bundle only. Page HTML is rendered by the build stage in
// src/build, which invokes this config through Vite's Node API.
//
// The entry stays small on purpose: everything a reader needs to read a page is
// already in the HTML, and this bundle adds the interactive behaviour on top.
// Island components are dynamically imported from the registry, so Vite emits
// one chunk per island and a page loads only what it declares.
export default defineConfig({
  publicDir: false,
  build: {
    outDir: resolve(import.meta.dirname, "dist/assets/site"),
    emptyOutDir: true,
    manifest: true,
    sourcemap: true,
    rollupOptions: {
      input: resolve(import.meta.dirname, "src/client/entry.ts"),
      output: {
        entryFileNames: "entry-[hash].js",
        chunkFileNames: "chunk-[name]-[hash].js",
        assetFileNames: "asset-[name]-[hash][extname]",
      },
    },
  },
});
