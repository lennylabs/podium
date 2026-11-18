// Runs the Go-generated SKILL.md metadata.version corpus through the workspace
// overlay. test/vectors/skill-metadata-version.json declares, for each
// SKILL.md and the ARTIFACT.md beside it, the version the overlay must report,
// and the Go generator fails when a readable case that Go parses resolves to
// another version, so a case that fails here is an SDK divergence from Go. A
// version: line nested in a multi-line flow collection or a multi-line quoted
// scalar is never read. A case with no artifact_md runs as a SKILL.md-only
// package and again beside the corpus default_artifact_md, which declares no
// version, and both runs report the same version.
//
// Spec: §4.3.4, §6.4

import { readFileSync } from "node:fs";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { LocalOverlay, type OverlayArtifact } from "./overlay.js";

interface VersionCase {
  name: string;
  skill_md: string;
  artifact_md?: string;
  go_version: string | null;
  sdk_version: string;
}

const CORPUS: { default_artifact_md: string; cases: VersionCase[] } = JSON.parse(
  readFileSync(new URL("../../../test/vectors/skill-metadata-version.json", import.meta.url), "utf-8"),
);
const SKILL_ONLY = CORPUS.cases.filter((c) => c.artifact_md === undefined);

describe("SKILL.md metadata.version corpus", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-version-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  const load = async (skillMD: string, artifactMD: string | undefined): Promise<OverlayArtifact | undefined> => {
    const pkg = join(dir, "overlay", "acme", "greet");
    await mkdir(pkg, { recursive: true });
    await writeFile(join(pkg, "SKILL.md"), skillMD);
    if (artifactMD !== undefined) await writeFile(join(pkg, "ARTIFACT.md"), artifactMD);
    const index = await LocalOverlay.load(join(dir, "overlay"));
    return index.get("acme/greet");
  };

  it.each(SKILL_ONLY)("$name", async ({ skill_md, sdk_version }) => {
    const art = await load(skill_md, undefined);
    expect(art?.type).toBe("skill");
    expect(art?.version).toBe(sdk_version);
  });

  it.each(CORPUS.cases)("$name beside an ARTIFACT.md", async ({ skill_md, artifact_md, sdk_version }) => {
    const art = await load(skill_md, artifact_md ?? CORPUS.default_artifact_md);
    expect(art?.type).toBe("skill");
    expect(art?.version).toBe(sdk_version);
    // The SKILL.md bytes are served as the file holds them, line ends included.
    expect(art?.skillRaw).toBe(skill_md);
  });
});
