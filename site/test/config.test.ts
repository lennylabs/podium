import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { REPO_ROOT, SITE_DIR, loadConfig, readVersion } from "../src/build/config";
import { BuildError } from "../src/build/types";

const TEMP_ROOT = resolve(SITE_DIR, "test/.tmp");
const disposals: Array<() => void> = [];
const originalEnv = { ...process.env };

afterEach(() => {
  while (disposals.length > 0) disposals.pop()?.();
  process.env = { ...originalEnv };
});

function repoWithVersion(source: string): string {
  mkdirSync(TEMP_ROOT, { recursive: true });
  const root = mkdtempSync(join(TEMP_ROOT, "config-"));
  mkdirSync(join(root, "internal/buildinfo"), { recursive: true });
  writeFileSync(join(root, "internal/buildinfo/buildinfo.go"), source);
  disposals.push(() => rmSync(root, { recursive: true, force: true }));
  return root;
}

describe("readVersion", () => {
  it("reads the Version constant out of the Go source", () => {
    const root = repoWithVersion(
      'package buildinfo\n\nconst (\n\tVersion = "1.4.2"\n\tCommit  = "abc"\n)\n',
    );

    expect(readVersion(root)).toBe("1.4.2");
  });

  it("reads the version the repository itself declares", () => {
    expect(readVersion()).toMatch(/^\d+\.\d+\.\d+/);
  });

  it("fails when the source declares no Version constant", () => {
    const root = repoWithVersion('package buildinfo\n\nconst (\n\tCommit = "abc"\n)\n');

    expect(() => readVersion(root)).toThrow(/could not read the Version constant/);
  });
});

describe("loadConfig", () => {
  it("derives the documentation and output directories from the repository root", () => {
    const config = loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" });

    expect(config.docsDir).toBe(resolve(REPO_ROOT, "docs"));
    expect(config.outDir).toBe(resolve(SITE_DIR, "dist"));
    expect(config.repoUrl).toBe("https://github.com/lennylabs/podium");
    expect(config.editBase).toBe("https://github.com/lennylabs/podium/edit/main");
  });

  it("defaults the base path to the GitHub Pages prefix", () => {
    delete process.env["PODIUM_SITE_BASE_PATH"];

    expect(loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }).basePath).toBe(
      "/podium",
    );
  });

  it("reads the base path from the environment", () => {
    process.env["PODIUM_SITE_BASE_PATH"] = "/docs";

    expect(loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }).basePath).toBe(
      "/docs",
    );
  });

  it("treats a single slash as no prefix, which is what a custom domain needs", () => {
    process.env["PODIUM_SITE_BASE_PATH"] = "/";

    expect(loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }).basePath).toBe("");
  });

  it("strips a trailing slash from the base path", () => {
    process.env["PODIUM_SITE_BASE_PATH"] = "/podium/";

    expect(loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }).basePath).toBe(
      "/podium",
    );
  });

  it("reads the site origin from the environment", () => {
    process.env["PODIUM_SITE_URL"] = "https://docs.acme.com";

    expect(loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }).siteUrl).toBe(
      "https://docs.acme.com",
    );
  });

  it("reads the version from the repository when none is given", () => {
    const root = repoWithVersion('package buildinfo\n\nconst (\n\tVersion = "9.9.9"\n)\n');

    expect(loadConfig({ repoRoot: root }).version).toBe("9.9.9");
  });

  it("declares a failure threshold for the serialized search index", () => {
    expect(
      loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }).searchIndexLimitBytes,
    ).toBe(640 * 1024);
  });
});

describe("BuildError", () => {
  it("lists every diagnostic with its position", () => {
    const error = new BuildError([
      { file: "docs/a.md", line: 12, column: 3, message: "first problem" },
      { file: "docs/b.md", line: 4, column: null, message: "second problem" },
      { file: "site/src/styles/tokens.css", line: null, column: null, message: "third" },
    ]);

    expect(error.name).toBe("BuildError");
    expect(error.diagnostics).toHaveLength(3);
    expect(error.message).toContain("3 problem(s):");
    expect(error.message).toContain("docs/a.md:12:3  first problem");
    expect(error.message).toContain("docs/b.md:4:1  second problem");
    expect(error.message).toContain("site/src/styles/tokens.css  third");
  });

  it("is an Error, so a caller can throw and catch it as one", () => {
    expect(new BuildError([])).toBeInstanceOf(Error);
  });
});
