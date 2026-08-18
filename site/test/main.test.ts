import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

import { afterEach, describe, expect, it, vi } from "vitest";

import { SITE_DIR } from "../src/build/config";

describe("the command-line entry point", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    process.exitCode = 0;
  });

  // Spawns npx and checks the whole corpus, so it runs for seconds rather than
  // milliseconds and grows with the corpus. The runner's own limit is given the
  // same budget as the subprocess: leaving it at the default means the two
  // disagree, and the test fails on a slow machine while the command it runs is
  // still working.
  it(
    "checks the repository corpus and reports what it read",
    () => {
      const output = execFileSync(
        "npx",
        ["tsx", "src/build/main.ts", "check"],
        { cwd: SITE_DIR, encoding: "utf8", timeout: 120_000 },
      );

      expect(output).toMatch(/^checked \d+ pages, \d+ routes, no problems\n$/);
    },
    120_000,
  );

  it("rejects a command it does not implement", async () => {
    const argv = process.argv;
    process.argv = [argv[0] ?? "node", resolve(SITE_DIR, "src/build/main.ts"), "publish"];
    const stderr = vi.spyOn(process.stderr, "write").mockReturnValue(true);

    try {
      await import("../src/build/main");

      expect(stderr).toHaveBeenCalledWith(
        'unknown command "publish". Use build, check, or dev.\n',
      );
      expect(process.exitCode).toBe(1);
    } finally {
      process.argv = argv;
    }
  });
});
