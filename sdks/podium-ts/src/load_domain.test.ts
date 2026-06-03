// Client.loadDomain workspace-overlay merge (F-4.5.2 / F-6.4.2). The merge
// mirrors the Go MCP bridge (cmd/podium-mcp/load_domain.go): the overlay is the
// highest-precedence layer in the caller's effective view (§6.4), composed onto
// the registry's rendered load_domain result client-side (§4.5.4, §4.5.5).

import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Client, type LoadDomainNotable, type LoadDomainResult } from "./index.js";

// overlayArtifact writes one overlay ARTIFACT.md package at the canonical id.
async function overlayArtifact(
  root: string,
  id: string,
  opts: { type?: string; desc?: string; body?: string } = {},
): Promise<void> {
  const pkg = join(root, ...id.split("/"));
  await mkdir(pkg, { recursive: true });
  const fm = `---\ntype: ${opts.type ?? "skill"}\nversion: 0.1.0\ndescription: ${opts.desc ?? ""}\n---\n${opts.body ?? "body"}\n`;
  await writeFile(join(pkg, "ARTIFACT.md"), fm);
}

// overlayDomain writes one DOMAIN.md at a domain path ("" for the overlay root).
async function overlayDomain(root: string, domainPath: string, frontmatterBody: string): Promise<void> {
  const dir = domainPath === "" ? root : join(root, ...domainPath.split("/"));
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "DOMAIN.md"), frontmatterBody);
}

function notableByID(resp: LoadDomainResult): Map<string, LoadDomainNotable> {
  const m = new Map<string, LoadDomainNotable>();
  for (const a of resp.notable) m.set(a.id, a);
  return m;
}

function subdomainByPath(resp: LoadDomainResult, path: string): LoadDomainResult["subdomains"][number] | undefined {
  return resp.subdomains.find((s) => s.path === path);
}

// hasSegPrefix reports whether id sits strictly under the scope prefix
// (segment-aligned), mirroring the Go fake registry's catalog scope filter.
function hasSegPrefix(id: string, prefix: string): boolean {
  return id.length > prefix.length && id.startsWith(prefix) && id[prefix.length] === "/";
}

// registryFetcher serves /v1/load_domain (the supplied payload) and /v1/catalog
// (the supplied artifacts, filtered to the requested scope prefix), the two
// endpoints the merge consults.
function registryFetcher(
  loadDomain: Record<string, unknown>,
  catalog: Array<{ id: string; type?: string; summary?: string }> = [],
): typeof fetch {
  return async (input) => {
    const url = new URL(String(input));
    if (url.pathname === "/v1/load_domain") {
      return new Response(JSON.stringify(loadDomain), { status: 200 });
    }
    if (url.pathname === "/v1/catalog") {
      const scope = url.searchParams.get("scope") ?? "";
      const arts = catalog.filter(
        (e) => scope === "" || e.id === scope || hasSegPrefix(e.id, scope),
      );
      return new Response(JSON.stringify({ ids: arts.map((a) => a.id), artifacts: arts }), {
        status: 200,
      });
    }
    return new Response("not found", { status: 404 });
  };
}

describe("loadDomain overlay merge (F-4.5.2/F-6.4.2)", () => {
  let dir: string;
  let overlay: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "podium-ld-"));
    overlay = join(dir, "overlay");
    await mkdir(overlay, { recursive: true });
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  // spec: §4.5.2/§4.5.4/§6.4 — with no workspace overlay the load_domain result
  // passes through the registry untouched.
  it("passes through unchanged with no overlay", async () => {
    const fetcher = registryFetcher({
      path: "finance",
      description: "Registry finance",
      keywords: ["money"],
      subdomains: [{ path: "finance/ap", name: "ap", description: "AP" }],
      notable: [{ id: "finance/x", type: "skill", summary: "x", source: "signal" }],
    });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("finance");
    expect(resp.description).toBe("Registry finance");
    expect(resp.notable.map((n) => n.id)).toEqual(["finance/x"]);
    expect(resp.subdomains.map((s) => s.path)).toEqual(["finance/ap"]);
  });

  // spec: §4.5.4 — an overlay DOMAIN.md body wins over the registry description
  // (highest-precedence layer) and keywords append-unique.
  it("merges overlay description body and keywords", async () => {
    await overlayDomain(
      overlay,
      "finance",
      "---\ndiscovery:\n  keywords: [ledger, draft]\n---\nLocal working notes for finance\n",
    );
    const fetcher = registryFetcher({
      path: "finance",
      description: "Registry finance",
      keywords: ["money", "ledger"],
      subdomains: [],
      notable: [],
    });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("finance");
    expect(resp.description?.trim()).toBe("Local working notes for finance");
    expect(resp.keywords).toEqual(["money", "ledger", "draft"]);
  });

  // spec: §4.5.5 — an overlay artifact that is a direct child of the requested
  // domain joins the notable candidate pool, tagged overlay-sourced; the
  // registry notable entry is retained.
  it("surfaces a direct-child overlay artifact as notable", async () => {
    await overlayArtifact(overlay, "finance/draft-helper", { desc: "in-progress finance helper" });
    const fetcher = registryFetcher({
      path: "finance",
      subdomains: [],
      notable: [{ id: "finance/x", type: "skill", summary: "x", source: "signal" }],
    });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("finance");
    const got = notableByID(resp);
    const d = got.get("finance/draft-helper");
    expect(d).toBeDefined();
    expect(d?.overlay).toBe(true);
    expect(d?.source).toBe("signal");
    expect(d?.summary).toBe("in-progress finance helper");
    expect(got.has("finance/x")).toBe(true);
  });

  // spec: §4.5.5 — an overlay artifact below an immediate child introduces that
  // child as a subdomain of the requested domain; the registry subdomain stays.
  it("introduces an overlay-only subdomain", async () => {
    await overlayArtifact(overlay, "finance/newteam/draft");
    await overlayDomain(overlay, "finance/newteam", "---\ndescription: New team workspace\n---\n");
    const fetcher = registryFetcher({
      path: "finance",
      subdomains: [{ path: "finance/ap", name: "ap", description: "AP" }],
      notable: [],
    });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("finance");
    const sd = subdomainByPath(resp, "finance/newteam");
    expect(sd).toBeDefined();
    expect(sd?.name).toBe("newteam");
    expect(sd?.description).toBe("New team workspace");
    expect(subdomainByPath(resp, "finance/ap")).toBeDefined();
  });

  // spec: §4.5.2 — a workspace-local DOMAIN.md include: resolves over the merged
  // view (registry catalog ∪ overlay), pulling in both a registry artifact and
  // an overlay artifact while excluding an out-of-scope one. The fetcher serves
  // both /v1/load_domain and /v1/catalog by inspecting the URL.
  it("resolves include over the merged registry-plus-overlay view", async () => {
    await overlayArtifact(overlay, "finance/ap/overlay-pay", { desc: "overlay pay" });
    await overlayDomain(overlay, "drafts", "---\ninclude:\n  - finance/ap/*\n---\n");
    const fetcher = registryFetcher(
      { path: "drafts", subdomains: [], notable: [] },
      [
        { id: "finance/ap/registry-pay", type: "skill", summary: "registry pay" },
        { id: "other/unrelated", type: "skill", summary: "nope" },
      ],
    );
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("drafts");
    const got = notableByID(resp);
    expect(got.has("finance/ap/registry-pay")).toBe(true);
    expect(got.has("finance/ap/overlay-pay")).toBe(true);
    expect(got.has("other/unrelated")).toBe(false);
  });

  // spec: §4.5.3 — an overlay DOMAIN.md unlisted: true removes the folder and
  // its subtree from the parent's enumeration.
  it("prunes an overlay-unlisted subdomain", async () => {
    await overlayDomain(overlay, "finance/secret", "---\nunlisted: true\n---\n");
    const fetcher = registryFetcher({
      path: "finance",
      subdomains: [
        { path: "finance/ap", name: "ap", description: "AP" },
        { path: "finance/secret", name: "secret", description: "Secret" },
      ],
      notable: [],
    });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("finance");
    expect(subdomainByPath(resp, "finance/secret")).toBeUndefined();
    expect(subdomainByPath(resp, "finance/ap")).toBeDefined();
  });

  // spec: §4.5.5 — an overlay DOMAIN.md at a child path overrides that child's
  // short description (frontmatter description, never the body).
  it("overrides a child description from the overlay frontmatter", async () => {
    await overlayDomain(
      overlay,
      "finance/ap",
      "---\ndescription: Local AP overrides\n---\nbody never shown for a child\n",
    );
    const fetcher = registryFetcher({
      path: "finance",
      subdomains: [{ path: "finance/ap", name: "ap", description: "Registry AP" }],
      notable: [],
    });
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("finance");
    const sd = subdomainByPath(resp, "finance/ap");
    expect(sd?.description).toBe("Local AP overrides");
  });

  // spec: §4.5.2 / §6.4 — a domain that exists only in the workspace overlay is
  // part of the effective view; the registry 404s it (it never sees the
  // overlay), so the SDK synthesizes an empty result and composes the overlay
  // onto it. The local include: resolves over the merged view.
  it("resolves an overlay-only domain the registry 404s", async () => {
    await overlayDomain(overlay, "drafts", "---\ndescription: Local drafts\ninclude:\n  - finance/ap/*\n---\n");
    await overlayArtifact(overlay, "finance/ap/overlay-pay", { desc: "overlay pay" });
    const catalog = [{ id: "finance/ap/registry-pay", type: "skill", summary: "registry pay" }];
    const fetcher: typeof fetch = async (input) => {
      const url = new URL(String(input));
      if (url.pathname === "/v1/load_domain") {
        return new Response(JSON.stringify({ code: "domain.not_found", message: "not here" }), {
          status: 404,
        });
      }
      if (url.pathname === "/v1/catalog") {
        const scope = url.searchParams.get("scope") ?? "";
        const arts = catalog.filter((e) => scope === "" || e.id === scope || hasSegPrefix(e.id, scope));
        return new Response(JSON.stringify({ ids: arts.map((a) => a.id), artifacts: arts }), { status: 200 });
      }
      return new Response("not found", { status: 404 });
    };
    const c = new Client({ registry: "http://reg", overlayPath: overlay, fetcher });
    const resp = await c.loadDomain("drafts");
    const ids = notableByID(resp);
    expect(ids.has("finance/ap/registry-pay")).toBe(true); // registry artifact via merged-view catalog
    expect(ids.has("finance/ap/overlay-pay")).toBe(true); // overlay artifact
    expect(resp.path).toBe("drafts");
  });
});
