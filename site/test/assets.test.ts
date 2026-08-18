import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import {
  copyStatics,
  emitFonts,
  emitSearchIndex,
  emitStylesheet,
  ensureDir,
  iconVersion,
  writeFile,
} from "../src/build/assets";
import { SITE_DIR } from "../src/build/config";
import { configFor } from "./support/corpus";

const TEMP_ROOT = resolve(SITE_DIR, "test/.tmp");
const disposals: Array<() => void> = [];

afterEach(() => {
  while (disposals.length > 0) disposals.pop()?.();
});

function scratch(): string {
  mkdirSync(TEMP_ROOT, { recursive: true });
  const root = mkdtempSync(join(TEMP_ROOT, "assets-"));
  disposals.push(() => rmSync(root, { recursive: true, force: true }));
  return root;
}

describe("ensureDir and writeFile", () => {
  it("creates every missing directory on the way to the file", () => {
    const root = scratch();
    const target = join(root, "a/b/c/page.html");

    writeFile(target, "<p>Body.</p>");

    expect(readFileSync(target, "utf8")).toBe("<p>Body.</p>");
  });

  it("overwrites a file that already exists", () => {
    const root = scratch();
    const target = join(root, "page.html");

    writeFile(target, "first");
    writeFile(target, "second");

    expect(readFileSync(target, "utf8")).toBe("second");
  });

  it("creates a directory that already exists without failing", () => {
    const root = scratch();

    ensureDir(join(root, "a"));
    ensureDir(join(root, "a"));

    expect(existsSync(join(root, "a"))).toBe(true);
  });
});

describe("emitFonts", () => {
  it("copies each declared weight and writes a rule naming it", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), {
      outDir: join(root, "out"),
      basePath: "/base",
    });

    const css = emitFonts(config);

    expect(css).toContain('font-family: "Space Grotesk";');
    expect(css).toContain('font-family: "JetBrains Mono";');
    expect(css).toContain('font-family: "Anton";');
    expect(css).toContain("font-display: swap;");
    expect(css).toContain(
      'src: url("/base/assets/fonts/space-grotesk-latin-400-normal.woff2") format("woff2");',
    );
    expect(
      existsSync(join(root, "out/assets/fonts/space-grotesk-latin-400-normal.woff2")),
    ).toBe(true);
  });

  it("serves fonts from the site's own origin rather than a font CDN", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), { outDir: join(root, "out") });

    expect(emitFonts(config)).not.toContain("//");
  });
});

describe("emitStylesheet", () => {
  it("writes one hashed file and returns its published URL", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), {
      outDir: join(root, "out"),
      basePath: "/base",
    });

    const href = emitStylesheet(config, "@font-face { font-family: X; }");

    expect(href).toMatch(/^\/base\/assets\/site-[0-9a-f]{8}\.css$/);
    expect(existsSync(join(root, "out/assets", href.split("/").pop()!))).toBe(true);
  });

  it("puts the token definitions ahead of the rules that read them", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), { outDir: join(root, "out") });

    const href = emitStylesheet(config, "");
    const css = readFileSync(join(root, "out/assets", href.split("/").pop()!), "utf8");

    expect(css.indexOf("--page:")).toBeLessThan(css.indexOf(".d-topbar"));
  });

  it("returns the same name for the same input", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), { outDir: join(root, "out") });

    expect(emitStylesheet(config, "a")).toBe(emitStylesheet(config, "a"));
  });

  it("returns a different name when the font rules change", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), { outDir: join(root, "out") });

    expect(emitStylesheet(config, "a")).not.toBe(emitStylesheet(config, "b"));
  });
});

describe("copyStatics", () => {
  it("publishes each file at the route it was discovered under", () => {
    const root = scratch();
    mkdirSync(join(root, "docs/assets/diagrams"), { recursive: true });
    writeFileSync(join(root, "docs/assets/diagrams/a.svg"), "<svg/>");
    writeFileSync(join(root, "docs/notes.md"), "# Notes\n");

    const config = configFor(root, join(root, "docs"), { outDir: join(root, "out") });

    copyStatics(config, [
      { route: "/assets/diagrams/a.svg", sourcePath: "docs/assets/diagrams/a.svg" },
      { route: "/notes.md", sourcePath: "docs/notes.md" },
    ]);

    expect(readFileSync(join(root, "out/assets/diagrams/a.svg"), "utf8")).toBe("<svg/>");
    expect(readFileSync(join(root, "out/notes.md"), "utf8")).toBe("# Notes\n");
  });

  it("copies nothing for an empty list", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"), { outDir: join(root, "out") });

    copyStatics(config, []);

    expect(existsSync(join(root, "out"))).toBe(false);
  });
});

describe("emitSearchIndex", () => {
  it("names the file after its contents so a stale index is never served", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"));

    const first = emitSearchIndex(config, '{"a":1}');
    const again = emitSearchIndex(config, '{"a":1}');
    const changed = emitSearchIndex(config, '{"a":2}');

    expect(first).toBe(again);
    expect(changed).not.toBe(first);
    expect(first).toMatch(/^\/assets\/search-index-[0-9a-f]{8}\.json$/);
  });

  it("writes the serialized index at the URL it returns", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"));

    const url = emitSearchIndex(config, '{"documentCount":3}');

    expect(readFileSync(join(config.outDir, url), "utf8")).toBe('{"documentCount":3}');
  });
});

describe("iconVersion", () => {
  it("changes only when an icon's bytes change", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"));
    const icon = "docs/assets/logo/mark.svg";
    writeFile(join(root, icon), "<svg>one</svg>");

    const before = iconVersion(config, [icon]);
    expect(iconVersion(config, [icon])).toBe(before);

    writeFile(join(root, icon), "<svg>two</svg>");
    expect(iconVersion(config, [icon])).not.toBe(before);
  });

  it("ignores an icon that is not on disk rather than failing the build", () => {
    const root = scratch();
    const config = configFor(root, join(root, "docs"));

    expect(iconVersion(config, ["docs/assets/logo/absent.svg"])).toMatch(/^[0-9a-f]{8}$/);
  });
});
