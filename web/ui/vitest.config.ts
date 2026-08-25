import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The UI's own test runner. The cases render components into a DOM and stub
// the registry reads they issue, which is what drives the surfaces through
// the UI's own API calls rather than through a constructed request.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    restoreMocks: true,
  },
});
