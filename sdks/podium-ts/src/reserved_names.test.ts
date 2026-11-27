// The §7.5.3 aliasing rule for .git and .podium in the TypeScript SDK.
// test/vectors/reserved-names.json holds the segments the Go generator checks
// against materialize.AliasesDotGit and materialize.AliasesDotPodium, so a
// case that fails here is an SDK divergence from Go. materialize() refuses a
// written segment that names .git, and an overlay package whose canonical ID
// names .git or .podium disables the overlay with a warning.
//
// Spec: §4.2, §6.4, §7.5.3, §7.6

import { readFileSync } from "node:fs";
import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Client, LoadedArtifact, MaterializeError } from "./index.js";
import { LocalOverlay, OverlayRefused } from "./overlay.js";
import { namesDotGit, namesDotPodium } from "./reserved_names.js";

interface ReservedCase {
  name: string;
  segment_hex: string;
  valid_utf8: boolean;
  names_git: boolean;
  names_podium: boolean;
}

const CORPUS: { cases: ReservedCase[] } = JSON.parse(
  readFileSync(new URL("../../../test/vectors/reserved-names.json", import.meta.url), "utf-8"),
);

// A segment that is not valid UTF-8 has no JavaScript string form, so the
// string-based SDK cannot receive it; those cases run in Go and Python only.
const STRING_CASES = CORPUS.cases.filter((c) => c.valid_utf8);

const decode = (hex: string): string => new TextDecoder("utf-8", { fatal: true }).decode(Buffer.from(hex, "hex"));

const ZWNJ = "\u200c";
const AGENT = "---\ntype: agent\nversion: 0.1.0\ndescription: Draft.\n---\n\nbody\n";

describe("reserved-name corpus", () => {
  // Spec: §7.5.3
  it("covers string cases and skips only invalid UTF-8", () => {
    expect(STRING_CASES.length).toBeGreaterThan(80);
    expect(CORPUS.cases.length - STRING_CASES.length).toBeLessThan(10);
  });

  // Spec: §7.5.3
  it.each(STRING_CASES.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    const seg = decode(c.segment_hex);
    expect(namesDotGit(seg)).toBe(c.names_git);
    expect(namesDotPodium(seg)).toBe(c.names_podium);
  });

  // Spec: §7.5.3 — a lone surrogate, which is what a malformed sequence
  // becomes in a string, reads as the end of the name, as Git reads it.
  it("reads a lone surrogate as the end of the name", () => {
    expect(namesDotGit(".git\ud800")).toBe(true);
    expect(namesDotGit(".gi\udc00t")).toBe(false);
    expect(namesDotPodium(".podium\udfff")).toBe(true);
    expect(namesDotGit(".git\u{1f600}")).toBe(false);
  });
});

describe("materialize and the aliasing rule", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-reserved-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  // Spec: §7.5.3, §7.6 — materialize() rejects a canonical ID or a resource
  // path with a segment that names .git, literally or under the aliasing
  // rule, and writes nothing.
  it.each([
    ["team/hooks", { ".git/config": "[core]\n" }],
    ["team/hooks", { "scripts/.GIT/config": "[core]\n" }],
    ["team/hooks", { "GIT~1/config": "[core]\n" }],
    ["team/hooks", { [ZWNJ + ".git/config"]: "[core]\n" }],
    ["team/hooks", { ".git./config": "[core]\n" }],
    ["GIT~1/hooks", {}],
    [ZWNJ + ".git", {}],
  ] as const)("refuses %s with %j", async (id, resources) => {
    const art = new LoadedArtifact({
      id,
      type: "agent",
      version: "1.0.0",
      manifest_body: "body\n",
      frontmatter: "---\ntype: agent\n---\n\nbody\n",
      resources: { ...resources },
    });
    const out = join(dir, "out");
    await expect(art.materialize(out)).rejects.toThrow(MaterializeError);
    await expect(art.materialize(out)).rejects.toThrow(/names \.git/);
    await expect(readdir(out)).rejects.toBeDefined();
  });

  // Spec: §7.6 — names that only resemble .git are ordinary resources.
  it("writes near misses", async () => {
    const art = new LoadedArtifact({
      id: "team/hooks",
      type: "agent",
      version: "1.0.0",
      manifest_body: "body\n",
      frontmatter: "---\ntype: agent\n---\n\nbody\n",
      resources: { ".gitignore": "x\n", ".github/ci.yml": "y\n", "GIT~2/config": "z\n" },
    });
    await art.materialize(dir);
    const root = join(dir, "team", "hooks");
    expect(await readFile(join(root, ".gitignore"), "utf8")).toBe("x\n");
    expect(await readFile(join(root, ".github", "ci.yml"), "utf8")).toBe("y\n");
    expect(await readFile(join(root, "GIT~2", "config"), "utf8")).toBe("z\n");
  });
});

describe("overlay and the aliasing rule", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-reserved-overlay-"));
  });
  afterEach(async () => {
    vi.restoreAllMocks();
    await rm(dir, { recursive: true, force: true });
  });

  const put = async (rel: string, text: string): Promise<void> => {
    const p = join(dir, "overlay", rel);
    await mkdir(join(p, ".."), { recursive: true });
    await writeFile(p, text);
  };

  // Spec: §4.2, §6.4, §6.9 — an overlay that holds a package whose canonical
  // ID names .git or .podium fails to load. The SDK skips the overlay with a
  // warning naming the path and serves none of its packages, for loadArtifact
  // and for loadDomain.
  it.each(["GIT~1", ZWNJ + ".git", "team/PODIUM~1"])("skips the overlay holding %s", async (bad) => {
    await put(`${bad}/ARTIFACT.md`, AGENT);
    await put(`${bad}/config`, "[core]\n\tfsmonitor = touch pwned\n");
    await put("team/draft/ARTIFACT.md", AGENT);
    await expect(LocalOverlay.load(join(dir, "overlay"))).rejects.toThrow(OverlayRefused);

    const warn = vi.spyOn(process, "emitWarning").mockImplementation(() => {});
    const c = new Client({
      registry: "http://reg",
      overlayPath: join(dir, "overlay"),
      fetcher: async () => new Response(JSON.stringify({ code: "registry.not_found", message: "no" }), { status: 404 }),
    });
    await expect(c.loadArtifact("team/draft")).rejects.toBeDefined();
    expect(warn).toHaveBeenCalled();
    const message = String(warn.mock.calls[0][0]);
    expect(message).toContain(join(dir, "overlay"));
    expect(message).toContain("overlay disabled");

    warn.mockClear();
    await c.loadDomain("team").catch(() => undefined);
    expect(warn).toHaveBeenCalled();
  });

  // Spec: §6.4 — an error other than the refusal still propagates.
  it("propagates an overlay read error other than the refusal", async () => {
    const err = new Error("boom");
    vi.spyOn(LocalOverlay, "load").mockRejectedValue(err);
    const c = new Client({ registry: "http://reg", overlayPath: join(dir, "overlay"), fetcher: async () => new Response("{}") });
    await expect(c.loadArtifact("team/draft")).rejects.toBe(err);
  });

  // Spec: §4.4 — the overlay leaves a directory or file named exactly .git out
  // of a package's bundled set, so a skill that is a Git clone loads and
  // materializes its working files alone; a name that only resolves to .git
  // stays in the set.
  it("leaves .git out of the bundled set", async () => {
    await put("team/pdf/ARTIFACT.md", AGENT);
    await put("team/pdf/scripts/run.sh", "echo hi\n");
    await put("team/pdf/.git/config", "[core]\n\tfsmonitor = touch pwned\n");
    await put("team/pdf/vendor/lib/.git", "gitdir: x\n");
    await put("team/pdf/GIT~2/notes.txt", "ok\n");
    const overlay = await LocalOverlay.load(join(dir, "overlay"));
    expect(Object.keys(overlay.artifacts.get("team/pdf")?.resources ?? {}).sort()).toEqual([
      "GIT~2/notes.txt",
      "scripts/run.sh",
    ]);
    const c = new Client({ registry: "http://reg", overlayPath: join(dir, "overlay"), fetcher: async () => new Response("{}") });
    const out = join(dir, "out");
    await (await c.loadArtifact("team/pdf")).materialize(out);
    await expect(readdir(join(out, "team", "pdf", ".git"))).rejects.toBeDefined();
    expect(await readFile(join(out, "team", "pdf", "scripts", "run.sh"), "utf8")).toBe("echo hi\n");
  });

  // Spec: §4.2, §6.4 — a directory that only resembles .git is an ordinary
  // domain.
  it("loads an overlay without an aliased package", async () => {
    await put("GIT~2/draft/ARTIFACT.md", AGENT);
    const overlay = await LocalOverlay.load(join(dir, "overlay"));
    expect(overlay.artifacts.has("GIT~2/draft")).toBe(true);
  });
});
