import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["test/**/*.test.ts", "test/**/*.test.tsx"],
    coverage: {
      provider: "v8",
      include: ["src/**/*.ts", "src/**/*.tsx"],
      exclude: ["src/client/**", "**/*.d.ts", "src/components/diagrams/**"],
      reporter: ["text", "json-summary", "html"],
    },
  },
});
