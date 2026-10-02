import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The UI's own test runner. The cases render components into a DOM and stub
// the registry reads they issue, which is what drives the surfaces through
// the UI's own API calls rather than through a constructed request.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    // The stylesheet is processed and injected into the test document, so a
    // case can assert a layout rule the surfaces depend on against the CSS
    // the bundle ships rather than against a class name.
    css: true,
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    restoreMocks: true,
    // The Testing Library wait timeout and the wait wrapper every case shares.
    setupFiles: ['src/test-setup.ts'],
    // A case can hold several waits, and the setup file lets each run to five
    // seconds on a loaded runner. The case timeout stays above that sum, so a
    // case reports the wait that failed rather than the runner's own timeout.
    testTimeout: 20000,
  },
});
