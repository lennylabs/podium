import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The registry serves the bundle at /app/ behind http.StripPrefix, and the
// outer mux routes every other path to the meta-tool handler, so an asset
// reference rooted at / returns 404 and the page renders blank. Setting the
// public base to /app/ makes every emitted reference resolve under the mount.
//
// The output directory is web/bundle rather than web/dist because the
// repository's .gitignore carries a bare `dist/` entry and the bundle is
// committed: go:embed resolves at compile time, so a clean clone with only a
// Go toolchain must find the built files in the tree.
export default defineConfig({
  base: '/app/',
  plugins: [react()],
  build: {
    outDir: '../bundle',
    emptyOutDir: true,
  },
});
