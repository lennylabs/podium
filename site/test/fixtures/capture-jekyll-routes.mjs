// Enumerates the route set the Jekyll build published, so route parity can be
// asserted after the corpus moves to the new generator.
//
// This is deliberately a standalone script rather than a call into the
// generator: a fixture produced by the code it guards would assert nothing.
// It applies Jekyll's documented output rules directly.
//
//   - a markdown file carrying frontmatter is a page, written to its source
//     path with an .html extension
//   - a `permalink` value overrides that path
//   - a markdown file with no frontmatter is a static file, copied verbatim
//   - everything under assets/ is copied verbatim
//
// Run against the pre-migration corpus:
//   node site/test/fixtures/capture-jekyll-routes.mjs docs > routes.json

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

const EXCLUDED_DIRS = new Set(["_site", ".jekyll-cache", ".sass-cache"]);
const EXCLUDED_FILES = new Set([
  "_config.yml",
  "Gemfile",
  "Gemfile.lock",
  ".gitignore",
]);

// Jekyll excludes dotfiles and underscore-prefixed entries from the output by
// default, so .DS_Store and .gitkeep never became routes.
function isExcluded(entry) {
  return entry.startsWith(".") || entry.startsWith("_") || EXCLUDED_FILES.has(entry);
}

function walk(dir, acc = []) {
  for (const entry of readdirSync(dir)) {
    if (isExcluded(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (!EXCLUDED_DIRS.has(entry)) walk(full, acc);
    } else {
      acc.push(full);
    }
  }
  return acc;
}

// Reads only the `permalink` key. A full YAML parse is unnecessary here and
// would pull a dependency into a fixture script.
function readPermalink(source) {
  if (!source.startsWith("---")) return null;
  const end = source.indexOf("\n---", 3);
  if (end === -1) return null;
  const match = source.slice(3, end).match(/^permalink:\s*(.+)$/m);
  return match ? match[1].trim().replace(/^["']|["']$/g, "") : null;
}

function hasFrontmatter(source) {
  return source.startsWith("---") && source.indexOf("\n---", 3) !== -1;
}

const root = process.argv[2] ?? "docs";
const routes = [];

for (const file of walk(root).sort()) {
  const rel = relative(root, file).split(sep).join("/");
  if (!rel.endsWith(".md")) {
    routes.push("/" + rel);
    continue;
  }
  const source = readFileSync(file, "utf8");
  if (!hasFrontmatter(source)) {
    routes.push("/" + rel);
    continue;
  }
  const permalink = readPermalink(source);
  if (permalink) {
    routes.push(permalink === "/" ? "/index.html" : permalink);
    continue;
  }
  routes.push("/" + rel.replace(/\.md$/, ".html"));
}

process.stdout.write(JSON.stringify(routes.sort(), null, 2) + "\n");
