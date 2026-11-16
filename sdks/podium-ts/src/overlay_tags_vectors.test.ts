// Runs the Go-generated SKILL.md metadata.tags corpus through the workspace
// overlay. test/vectors/skill-metadata-tags.json declares, for each SKILL.md,
// the tags the overlay must report, and the Go generator fails when a readable
// case that Go parses resolves to other tags, so a case that fails here is an
// SDK divergence from Go.
//
// Spec: §4.3.4, §6.4

import { readFileSync } from "node:fs";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { LocalOverlay } from "./overlay.js";

interface TagsCase {
  name: string;
  skill_md: string;
  readable: boolean;
  go_tags: string[] | null;
  sdk_tags: string[];
}

const CORPUS: { cases: TagsCase[] } = JSON.parse(
  readFileSync(new URL("../../../test/vectors/skill-metadata-tags.json", import.meta.url), "utf-8"),
);

describe("SKILL.md metadata.tags corpus", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-tags-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it.each(CORPUS.cases)("$name", async ({ skill_md, sdk_tags }) => {
    const pkg = join(dir, "overlay", "acme", "greet");
    await mkdir(pkg, { recursive: true });
    await writeFile(join(pkg, "SKILL.md"), skill_md);
    const index = await LocalOverlay.load(join(dir, "overlay"));
    const art = index.get("acme/greet");
    expect(art?.type).toBe("skill");
    expect(art?.tags).toEqual(sdk_tags);
    if (sdk_tags.length > 0) {
      expect(index.search("", { tags: [sdk_tags[0]] }).map((a) => a.id)).toEqual(["acme/greet"]);
    }
  });
});
