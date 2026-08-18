import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { REPO_ROOT } from "./config";

export const LOCKUP_SOURCES = {
  light: "docs/assets/logo/podium-lockup-light.svg",
  dark: "docs/assets/logo/podium-lockup-dark.svg",
} as const;

export type Lockup = {
  viewBox: string;
  /** Everything inside the root <svg>, minus the <title>. */
  inner: string;
};

/**
 * Reads a lockup from the design file it is drawn in.
 *
 * The markup is inlined into the page rather than referenced with an <img>,
 * because the wordmark is live SVG text set in Anton and an SVG loaded through
 * an <img> renders in an isolated document that cannot reach the page's
 * webfonts. Reading the file here rather than transcribing it keeps the design
 * files the single source: editing one changes the site on the next build.
 *
 * The <title> is dropped because the anchor that wraps the lockup carries the
 * accessible name, and two names on one link reads twice.
 */
function readLockup(relativePath: string): Lockup {
  const absolute = resolve(REPO_ROOT, relativePath);
  if (!existsSync(absolute)) {
    throw new Error(
      `the logo lockup is missing: ${relativePath}. The site inlines it, so the file has to exist at build time.`,
    );
  }

  const source = readFileSync(absolute, "utf8");

  const viewBox = source.match(/viewBox="([^"]+)"/)?.[1];
  if (viewBox === undefined) {
    throw new Error(`${relativePath} has no viewBox, so it cannot be scaled`);
  }

  const open = source.indexOf(">", source.indexOf("<svg"));
  const close = source.lastIndexOf("</svg>");
  if (open === -1 || close === -1) {
    throw new Error(`${relativePath} is not a single <svg> document`);
  }

  const inner = source
    .slice(open + 1, close)
    .replace(/<title>[\s\S]*?<\/title>/g, "")
    .trim();

  return { viewBox, inner };
}

export const LOCKUPS: Record<"light" | "dark", Lockup> = {
  light: readLockup(LOCKUP_SOURCES.light),
  dark: readLockup(LOCKUP_SOURCES.dark),
};
