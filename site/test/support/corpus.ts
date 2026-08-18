import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

import { SITE_DIR, loadConfig } from "../../src/build/config";
import type { SiteConfig } from "../../src/build/types";

/**
 * Temporary corpora live inside the repository so the git lookup the pipeline
 * runs finds a worktree and returns quietly instead of writing to stderr.
 */
const TEMP_ROOT = resolve(SITE_DIR, "test/.tmp");

export const FIXTURE_CORPUS = resolve(SITE_DIR, "test/fixtures/corpus");
export const FIXTURE_ROOT = resolve(SITE_DIR, "test/fixtures");

export type TempCorpus = {
  config: SiteConfig;
  repoRoot: string;
  dispose: () => void;
};

/** Base configuration for a corpus that is not the repository's own docs tree. */
export function configFor(
  repoRoot: string,
  docsDir: string,
  overrides: Partial<SiteConfig> = {},
): SiteConfig {
  return loadConfig({
    repoRoot,
    docsDir,
    outDir: join(repoRoot, "out"),
    siteUrl: "https://example.test",
    basePath: "",
    version: "0.0.0-test",
    ...overrides,
  });
}

/** Writes a corpus to a scratch directory and returns a config pointing at it. */
export function makeCorpus(
  files: Record<string, string>,
  overrides: Partial<SiteConfig> = {},
): TempCorpus {
  mkdirSync(TEMP_ROOT, { recursive: true });
  const repoRoot = mkdtempSync(join(TEMP_ROOT, "corpus-"));
  const docsDir = join(repoRoot, "docs");

  for (const [path, contents] of Object.entries(files)) {
    const full = join(docsDir, path);
    mkdirSync(dirname(full), { recursive: true });
    writeFileSync(full, contents);
  }

  return {
    config: configFor(repoRoot, docsDir, overrides),
    repoRoot,
    dispose: () => rmSync(repoRoot, { recursive: true, force: true }),
  };
}
