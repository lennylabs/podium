import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import type { SiteConfig } from "./types";

const SITE_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const REPO_ROOT = resolve(SITE_DIR, "..");

/**
 * Reads the version constant out of the Go source so the site and the binary
 * report the same number. A release overrides the constant with -ldflags, and
 * the site is rebuilt from the tagged tree, so reading the source is correct
 * at the point the site is generated.
 */
export function readVersion(repoRoot: string = REPO_ROOT): string {
  const source = readFileSync(
    resolve(repoRoot, "internal/buildinfo/buildinfo.go"),
    "utf8",
  );
  const match = source.match(/^\s*Version\s*=\s*"([^"]+)"/m);
  if (!match || match[1] === undefined) {
    throw new Error(
      "could not read the Version constant from internal/buildinfo/buildinfo.go",
    );
  }
  return match[1];
}

/**
 * The published site lives under a path prefix on GitHub Pages. Every emitted
 * URL carries it, and moving to a custom domain is a matter of setting it to
 * the empty string.
 */
export function loadConfig(overrides: Partial<SiteConfig> = {}): SiteConfig {
  const repoRoot = overrides.repoRoot ?? REPO_ROOT;
  const basePath = overrides.basePath ?? process.env.PODIUM_SITE_BASE_PATH ?? "/podium";

  return {
    siteUrl: process.env.PODIUM_SITE_URL ?? "https://lennylabs.github.io",
    basePath: basePath === "/" ? "" : basePath.replace(/\/$/, ""),
    repoRoot,
    docsDir: resolve(repoRoot, "docs"),
    outDir: resolve(SITE_DIR, "dist"),
    editBase: "https://github.com/lennylabs/podium/edit/main",
    repoUrl: "https://github.com/lennylabs/podium",
    version: overrides.version ?? readVersion(repoRoot),
    searchIndexLimitBytes: 600 * 1024,
    ...overrides,
  };
}

export { SITE_DIR, REPO_ROOT };
