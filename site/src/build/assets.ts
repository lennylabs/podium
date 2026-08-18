import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

import { SITE_DIR } from "./config";
import type { SiteConfig, StaticFile } from "./types";

/** Weights the design uses, per family. Copying only these keeps the site small. */
const FONTS = [
  { family: "space-grotesk", cssName: "Space Grotesk", weights: [400, 500, 600, 700] },
  { family: "jetbrains-mono", cssName: "JetBrains Mono", weights: [400, 500, 700] },
  { family: "anton", cssName: "Anton", weights: [400] },
] as const;

export function ensureDir(path: string): void {
  mkdirSync(path, { recursive: true });
}

export function writeFile(path: string, contents: string | Buffer): void {
  ensureDir(dirname(path));
  writeFileSync(path, contents);
}

function hash(contents: string | Buffer): string {
  return createHash("sha256").update(contents).digest("hex").slice(0, 8);
}

/**
 * Copies fonts out of the installed packages and writes the @font-face rules.
 *
 * The fonts are served from the site's own origin rather than a font CDN, so a
 * page load makes no third-party request and the site keeps working offline.
 */
export function emitFonts(config: SiteConfig): string {
  const outDir = join(config.outDir, "assets/fonts");
  ensureDir(outDir);
  const rules: string[] = [];

  for (const font of FONTS) {
    for (const weight of font.weights) {
      const file = `${font.family}-latin-${weight}-normal.woff2`;
      const source = resolve(SITE_DIR, "node_modules/@fontsource", font.family, "files", file);
      if (!existsSync(source)) {
        throw new Error(
          `font file is missing: ${source}. Run npm install in site/ before building.`,
        );
      }
      copyFileSync(source, join(outDir, file));
      rules.push(
        [
          "@font-face {",
          `  font-family: "${font.cssName}";`,
          "  font-style: normal;",
          `  font-weight: ${weight};`,
          "  font-display: swap;",
          `  src: url("${config.basePath}/assets/fonts/${file}") format("woff2");`,
          "}",
        ].join("\n"),
      );
    }
  }

  return rules.join("\n\n");
}

/**
 * Concatenates the stylesheets in cascade order and writes one hashed file.
 *
 * Tokens come first because everything after them reads the variables they
 * define.
 */
export function emitStylesheet(config: SiteConfig, fontCss: string): string {
  const order = [
    "tokens.css",
    "base.css",
    "content.css",
    "docs.css",
    "landing.css",
    "search.css",
  ];

  const parts = [fontCss];
  for (const name of order) {
    const path = resolve(SITE_DIR, "src/styles", name);
    if (existsSync(path)) parts.push(readFileSync(path, "utf8"));
  }

  const css = parts.join("\n\n");
  const name = `site-${hash(css)}.css`;
  writeFile(join(config.outDir, "assets", name), css);
  return `${config.basePath}/assets/${name}`;
}

/**
 * Writes the prebuilt search index under a hashed name.
 *
 * The name changes with the contents, so the file can be cached indefinitely
 * and a reader never searches a stale corpus. A fixed name is served from the
 * browser's cache until it expires, which shows results for pages that have
 * since been renamed or removed.
 */
export function emitSearchIndex(config: SiteConfig, serialized: string): string {
  const name = `search-index-${hash(serialized)}.json`;
  writeFile(join(config.outDir, "assets", name), serialized);
  return `${config.basePath}/assets/${name}`;
}

/**
 * A stamp over the browser icons, for the query the icon links carry.
 *
 * Icons are referenced by a fixed path and a browser caches a favicon
 * aggressively, so replacing one leaves the old icon on screen until the cache
 * expires. The query changes only when an icon's bytes change.
 */
export function iconVersion(config: SiteConfig, paths: string[]): string {
  const digest = createHash("sha256");
  for (const path of paths) {
    const source = resolve(config.repoRoot, path);
    if (existsSync(source)) digest.update(readFileSync(source));
  }
  return digest.digest("hex").slice(0, 8);
}

/** Publishes files that carry no frontmatter, at the path they already live at. */
export function copyStatics(config: SiteConfig, statics: StaticFile[]): void {
  for (const file of statics) {
    const source = resolve(config.repoRoot, file.sourcePath);
    const target = join(config.outDir, file.route);
    ensureDir(dirname(target));
    copyFileSync(source, target);
  }
}
