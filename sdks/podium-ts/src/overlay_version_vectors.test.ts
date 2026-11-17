// Runs the Go-generated SKILL.md metadata.version corpus through the workspace
// overlay. test/vectors/skill-metadata-version.json declares, for each
// SKILL.md, the version the overlay must report, and the Go generator fails
// when a readable case that Go parses resolves to another version, so a case
// that fails here is an SDK divergence from Go. A version: line nested in a
// multi-line flow collection or a multi-line quoted scalar is never read.
//
// Spec: §4.3.4, §6.4

import { readFileSync } from "node:fs";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { LocalOverlay } from "./overlay.js";

interface VersionCase {
  name: string;
  skill_md: string;
  go_version: string | null;
  sdk_version: string;
}

const CORPUS: { cases: VersionCase[] } = JSON.parse(
  readFileSync(new URL("../../../test/vectors/skill-metadata-version.json", import.meta.url), "utf-8"),
);

describe("SKILL.md metadata.version corpus", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-version-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it.each(CORPUS.cases)("$name", async ({ skill_md, sdk_version }) => {
    const pkg = join(dir, "overlay", "acme", "greet");
    await mkdir(pkg, { recursive: true });
    await writeFile(join(pkg, "SKILL.md"), skill_md);
    const index = await LocalOverlay.load(join(dir, "overlay"));
    const art = index.get("acme/greet");
    expect(art?.type).toBe("skill");
    expect(art?.version).toBe(sdk_version);
  });
});
