import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// The viewer is compiled to two plain files — viewer.js (IIFE) and viewer.css —
// which the Go side inlines into every tour bundle. That shape is deliberate:
//
//   - Output goes to internal/viewer/assets and is COMMITTED. `go build` must
//     never require Node; the toolchain here is for changing the viewer, not for
//     building tds.
//   - IIFE, not ESM: the bundle is inlined into a <script> in a page opened via
//     file://, where module scripts are blocked by CORS.
//   - No code splitting, no asset URLs, no fetch. Everything the page needs must
//     already be in the document.
export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: "../internal/viewer/assets",
    emptyOutDir: false, // the directory holds Go-side files too
    cssCodeSplit: false,
    // Inlining is capped at a large value so no asset is ever emitted as a
    // separate file the bundle would have to carry.
    assetsInlineLimit: 100_000_000,
    lib: {
      entry: "src/main.tsx",
      name: "TdsViewer",
      formats: ["iife"],
      fileName: () => "viewer.js",
    },
    rollupOptions: {
      output: {
        assetFileNames: "viewer.[ext]",
      },
    },
    // Readable output is worth more than the last few KB: this ships inside
    // every tour, and a reader should be able to view-source it.
    minify: "esbuild",
    target: "es2020",
  },
});
