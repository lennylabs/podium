// Podium TypeScript SDK — thin HTTP client over the registry API
// (spec §7.6). The client resolves the registry from sync.yaml, merges the
// workspace overlay client-side, and runs the §6.3 oauth-device-code flow
// via Client.login(). The config/overlay/oauth helpers load Node's fs/path
// lazily, so importing the SDK stays safe in edge bundles.

import { resolveRegistry } from "./config.js";
import {
  LocalOverlay,
  resolveOverlayPath,
  rrfFuse,
  fallbackDescription,
  globLiteralPrefix,
  resolveImports,
  matchAny,
  type OverlayArtifact,
  type OverlayDomain,
} from "./overlay.js";
import {
  DeviceCodeError,
  DEFAULT_TIMEOUT_MS,
  discoverIdp,
  initiate,
  poll,
  type Tokens,
} from "./oauth.js";

export { DeviceCodeError, type Tokens };

export interface ArtifactDescriptor {
  id: string;
  type: string;
  version?: string;
  description?: string;
  tags?: string[];
  score?: number;
  // spec: §7.6.1 — a search_artifacts result carries the artifact's
  // frontmatter (the documented {id, type, version, score, frontmatter}
  // schema). Absent on load_domain notable entries.
  frontmatter?: string;
}

export interface SearchResult {
  query?: string;
  total_matched: number;
  results?: ArtifactDescriptor[];
  domains?: Record<string, unknown>[];
}

// LoadDomainResult mirrors the /v1/load_domain wire envelope (§5). subdomains
// and notable are always present arrays, matching the registry; the merge
// composes the workspace overlay onto this same schema and re-emits it.
export interface LoadDomainResult {
  path: string;
  description?: string;
  keywords?: string[];
  subdomains: LoadDomainSubdomain[];
  notable: LoadDomainNotable[];
  note?: string;
}

// LoadDomainSubdomain mirrors a load_domain subdomain entry.
export interface LoadDomainSubdomain {
  path: string;
  name: string;
  description?: string;
  subdomains?: LoadDomainSubdomain[];
}

// LoadDomainNotable mirrors a load_domain notable entry ({id, type, summary,
// source, folded_from}, §7.6.1). overlay marks an entry surfaced from the
// workspace overlay, mirroring the search_artifacts overlay annotation; the
// registry never sets it.
export interface LoadDomainNotable {
  id: string;
  type?: string;
  version?: string;
  summary?: string;
  source?: string;
  folded_from?: string;
  overlay?: boolean;
}

// defaultRenderDepth is the §4.5.5 max_depth default. The SDK does not know
// the tenant's resolved max_depth, so an overlay-introduced subtree renders to
// the caller's requested depth, falling back to this default.
const DEFAULT_RENDER_DEPTH = 3;

// renderDepth reads the caller's requested depth, falling back to the §4.5.5
// default when unset or non-positive.
function renderDepth(depth?: number): number {
  return depth !== undefined && depth > 0 ? depth : DEFAULT_RENDER_DEPTH;
}

// uniqueAppend appends src to dst, dropping values already present, preserving
// order (§4.5.4 append-unique).
function uniqueAppend(dst: string[], src: string[]): string[] {
  const seen = new Set(dst);
  const out = [...dst];
  for (const v of src) {
    if (!seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}

// parentOf returns the parent domain path of a canonical id ("" for a
// top-level id).
function parentOf(p: string): string {
  const i = p.lastIndexOf("/");
  return i >= 0 ? p.slice(0, i) : "";
}

// joinSeg joins a domain path and a child segment.
function joinSeg(path: string, seg: string): string {
  return path === "" ? seg : path + "/" + seg;
}

// underRest returns id's remainder beyond prefix when id is at or under prefix
// (segment-aligned), or "" otherwise. An empty prefix returns id unchanged.
function underRest(id: string, prefix: string): string {
  if (prefix === "") return id;
  if (!id.startsWith(prefix)) return "";
  if (id.length === prefix.length) return "";
  if (id[prefix.length] !== "/") return "";
  return id.slice(prefix.length + 1);
}

// overlayHasDomainContent reports whether the overlay carries anything at or
// under path: a DOMAIN.md at path, a deeper DOMAIN.md, or an artifact under
// path. Gates synthesizing a result for an overlay-only domain the registry
// 404s (§4.5.2, §6.4).
function overlayHasDomainContent(
  path: string,
  domains: Map<string, OverlayDomain>,
  records: OverlayArtifact[],
): boolean {
  if (domains.has(path)) return true;
  for (const dp of domains.keys()) {
    if (dp !== path && underRest(dp, path) !== "") return true;
  }
  return records.some((r) => underRest(r.id, path) !== "");
}

// overlayArtifactDescriptor maps an overlay artifact to a load_domain notable
// descriptor, tagged as overlay-sourced (§4.5.5).
function overlayArtifactDescriptor(rec: OverlayArtifact): LoadDomainNotable {
  return {
    id: rec.id,
    type: rec.type,
    version: rec.version,
    summary: rec.description,
    overlay: true,
  };
}

// mergeNotable appends the overlay candidates to the registry notable list with
// overlay precedence on a shared id, tags each entry's §4.5.5 notable source
// (featured wins), orders featured entries first, and caps the result when the
// overlay DOMAIN.md sets a notable_count.
function mergeNotable(
  reg: LoadDomainNotable[],
  candidates: LoadDomainNotable[],
  featured: Set<string>,
  cap: number,
): LoadDomainNotable[] {
  const out: LoadDomainNotable[] = [];
  const idx = new Map<string, number>();
  for (const a of reg) {
    idx.set(a.id, out.length);
    out.push(a);
  }
  for (const c of candidates) {
    const i = idx.get(c.id);
    if (i !== undefined) {
      out[i] = c; // overlay precedence shadows the registry descriptor
      continue;
    }
    idx.set(c.id, out.length);
    out.push(c);
  }
  for (const a of out) {
    if (featured.has(a.id)) a.source = "featured";
    else if (!a.source) a.source = "signal";
  }
  const feat = out.filter((a) => a.source === "featured");
  const rest = out.filter((a) => a.source !== "featured");
  let merged = [...feat, ...rest];
  if (cap > 0 && merged.length > cap) merged = merged.slice(0, cap);
  return merged;
}

// mergedFeatured is the union of the registry's featured ids (notable entries
// already tagged source: featured) and the overlay DOMAIN.md featured list
// (§4.5.4 featured append-unique).
function mergedFeatured(reg: LoadDomainNotable[], od: OverlayDomain | undefined): Set<string> {
  const m = new Set<string>();
  for (const a of reg) {
    if (a.source === "featured") m.add(a.id);
  }
  if (od) {
    for (const f of od.featured) m.add(f);
  }
  return m;
}

// unlistedOverlay reports whether path resolves under an overlay-unlisted
// folder (the path or any ancestor has an overlay DOMAIN.md with unlisted:
// true). The root ("") carries no DOMAIN.md (§4.5.3).
function unlistedOverlay(path: string, domains: Map<string, OverlayDomain>): boolean {
  for (let p = path; p !== ""; p = parentOf(p)) {
    const d = domains.get(p);
    if (d && d.unlisted) return true;
  }
  return false;
}

// overlayChildDescription resolves a child/sibling subdomain's §4.5.5
// description: the overlay DOMAIN.md frontmatter description when set, otherwise
// the synthesized basename fallback. The prose body is never returned for a
// non-requested entry (§4.5.5).
function overlayChildDescription(path: string, domains: Map<string, OverlayDomain>): string {
  const d = domains.get(path);
  if (d && d.description !== "") return d.description;
  return fallbackDescription(path);
}

// overlayImmediateChildren returns the immediate subdomain child names under
// path implied by the overlay: a first segment beyond path that has an overlay
// artifact below it (id has a deeper segment) or an overlay DOMAIN.md at or
// below it. A direct child artifact (no deeper segment) is a notable entry, not
// a subdomain, and is excluded here.
function overlayImmediateChildren(
  path: string,
  domains: Map<string, OverlayDomain>,
  records: OverlayArtifact[],
): string[] {
  const seen = new Set<string>();
  const names: string[] = [];
  const add = (name: string): void => {
    if (name === "" || seen.has(name)) return;
    seen.add(name);
    names.push(name);
  };
  for (const rec of records) {
    const rest = underRest(rec.id, path);
    if (rest === "" || !rest.includes("/")) continue;
    add(rest.split("/", 1)[0]);
  }
  for (const dp of domains.keys()) {
    if (dp === path) continue;
    const rest = underRest(dp, path);
    if (rest === "") continue;
    add(rest.split("/", 1)[0]);
  }
  names.sort();
  return names;
}

// renderOverlaySubtree renders the overlay-only subdomain tree under path to
// depth levels, dropping unlisted subtrees (§4.5.5). It does not apply
// pass-through folding; an overlay-introduced subtree renders its literal
// directory structure.
function renderOverlaySubtree(
  path: string,
  depth: number,
  domains: Map<string, OverlayDomain>,
  records: OverlayArtifact[],
): LoadDomainSubdomain[] {
  if (depth <= 0) return [];
  const out: LoadDomainSubdomain[] = [];
  for (const name of overlayImmediateChildren(path, domains, records)) {
    const childPath = joinSeg(path, name);
    if (unlistedOverlay(childPath, domains)) continue;
    out.push({
      path: childPath,
      name,
      description: overlayChildDescription(childPath, domains),
      subdomains: renderOverlaySubtree(childPath, depth - 1, domains, records),
    });
  }
  out.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  return out;
}

// pruneUnlisted recursively drops every subdomain the overlay marks unlisted
// (the path or an ancestor carries unlisted: true), per §4.5.3.
function pruneUnlisted(
  subs: LoadDomainSubdomain[],
  domains: Map<string, OverlayDomain>,
): LoadDomainSubdomain[] {
  const out: LoadDomainSubdomain[] = [];
  for (const sd of subs) {
    if (unlistedOverlay(sd.path, domains)) continue;
    sd.subdomains = pruneUnlisted(sd.subdomains ?? [], domains);
    out.push(sd);
  }
  return out;
}

// mergeSubdomains extends the registry subdomain list with overlay-only
// children, overrides a child's description from an overlay DOMAIN.md, and
// drops every overlay-unlisted subtree (§4.5.3, §4.5.5).
function mergeSubdomains(
  reg: LoadDomainSubdomain[],
  path: string,
  depth: number,
  domains: Map<string, OverlayDomain>,
  records: OverlayArtifact[],
): LoadDomainSubdomain[] {
  const out = pruneUnlisted(reg, domains);
  const byPath = new Map<string, number>();
  out.forEach((sd, i) => byPath.set(sd.path, i));
  for (const name of overlayImmediateChildren(path, domains, records)) {
    const childPath = joinSeg(path, name);
    if (unlistedOverlay(childPath, domains)) continue;
    const i = byPath.get(childPath);
    if (i !== undefined) {
      const od = domains.get(childPath);
      if (od && od.description !== "") out[i].description = od.description;
      continue;
    }
    out.push({
      path: childPath,
      name,
      description: overlayChildDescription(childPath, domains),
      subdomains: renderOverlaySubtree(childPath, depth - 1, domains, records),
    });
  }
  out.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  return out;
}

// §7.2 large-resource reference: the response delivers bytes out of band
// via a presigned URL the consumer fetches from object storage.
export interface LargeResourceLink {
  url: string;
  content_hash?: string;
  size?: number;
  content_type?: string;
}

export interface MaterializeOptions {
  // Accepted per §2.2 ("The SDKs accept a harness parameter on
  // materialize()"). Harness-specific adaptation is the registry's shared
  // module (§2.2); this independent client writes the canonical (`none`)
  // layout and records the requested harness for forward compatibility.
  harness?: string;
  // Override the fetcher used to pull §7.2 presigned large resources.
  // Defaults to the global fetch.
  fetcher?: typeof fetch;
}

// Spec §6.6 sandbox contract: a resource path that escapes the destination
// root is rejected rather than written through the traversal.
export class MaterializeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MaterializeError";
  }
}

// materializeCanonical writes one artifact to disk in the canonical
// (`none`-adapter) layout under `<to>/<id>/` (spec §6.6, §6.7): ARTIFACT.md
// for every type, SKILL.md for skills (reconstructed as frontmatter +
// manifest body, mirroring the registry's server-source delivery), and each
// bundled resource at its package-relative path. Node-only filesystem and
// path modules load lazily so the SDK stays importable in edge bundles that
// never materialize.
async function materializeCanonical(args: {
  to: string;
  id: string;
  type: string;
  frontmatter: string;
  manifestBody: string;
  skillRaw: string;
  inline: Record<string, string | Uint8Array>;
  large: Record<string, LargeResourceLink>;
  fetcher: typeof fetch;
}): Promise<string[]> {
  if (!args.to) throw new MaterializeError("destination path is empty");
  const fs = await import("node:fs/promises");
  const path = await import("node:path");

  const rootAbs = path.resolve(args.to);
  const safeJoin = (rel: string): string => {
    const parts = rel
      .replace(/\\/g, "/")
      .split("/")
      .filter((p) => p !== "" && p !== ".");
    const target = path.resolve(rootAbs, args.id, ...parts);
    const base = path.resolve(rootAbs, args.id);
    if (target !== base && !target.startsWith(base + path.sep)) {
      throw new MaterializeError(`resource path escapes destination root: ${rel}`);
    }
    return target;
  };
  const write = async (target: string, bytes: Uint8Array | string): Promise<void> => {
    await fs.mkdir(path.dirname(target), { recursive: true });
    await fs.writeFile(target, bytes);
  };

  const written: string[] = [];
  const artPath = safeJoin("ARTIFACT.md");
  await write(artPath, args.frontmatter);
  written.push(artPath);

  if (args.type === "skill") {
    const skillPath = safeJoin("SKILL.md");
    // spec: §4.3.4 / §11 — prefer the verbatim SKILL.md the registry delivers;
    // fall back to frontmatter + body only when it is absent.
    await write(skillPath, args.skillRaw !== "" ? args.skillRaw : args.frontmatter + args.manifestBody);
    written.push(skillPath);
  }

  for (const rel of Object.keys(args.inline).sort()) {
    const target = safeJoin(rel);
    await write(target, args.inline[rel]);
    written.push(target);
  }

  for (const rel of Object.keys(args.large).sort()) {
    const link = args.large[rel];
    if (!link?.url) throw new MaterializeError(`large resource ${rel} has no presigned URL`);
    const resp = await args.fetcher(link.url);
    if (!resp.ok) {
      throw new MaterializeError(`fetch large resource ${rel}: HTTP ${resp.status}`);
    }
    const target = safeJoin(rel);
    await write(target, new Uint8Array(await resp.arrayBuffer()));
    written.push(target);
  }

  return written;
}

// decodeInlineForMaterialize decodes a base64-flagged inline resource set
// (spec §4.1 / §7.2 resources_base64, F-4.1.1) back to raw bytes so a binary
// resource materializes uncorrupted. encoding/json replaces invalid UTF-8 in
// a string with U+FFFD, so the registry base64-encodes the whole inline set
// when any member is binary; the flag is response-wide. Runs only inside
// materialize (a Node context), so the Node Buffer global is available.
function decodeInlineForMaterialize(
  resources: Record<string, string>,
  b64: boolean | undefined,
): Record<string, string | Uint8Array> {
  if (!b64) return resources;
  const out: Record<string, Uint8Array> = {};
  for (const [k, v] of Object.entries(resources)) {
    out[k] = Uint8Array.from(Buffer.from(v, "base64"));
  }
  return out;
}

// Spec §7.6 / §2.2 — the loaded-artifact object exposes
// materialize(to, { harness }). resources are inline bytes; largeResources
// are §7.2 presigned references fetched on demand.
export class LoadedArtifact {
  id: string;
  type: string;
  version: string;
  manifest_body: string;
  frontmatter: string;
  // spec: §4.3.4 / §11 — verbatim SKILL.md for a skill, delivered so the
  // materialized file is byte-identical to the authored source.
  skill_raw?: string;
  resources?: Record<string, string>;
  // §4.1/§7.2 (F-4.1.1): when true, every resources value is base64-encoded
  // so a binary bundled resource survives JSON transport. materialize decodes.
  resources_base64?: boolean;
  large_resources?: Record<string, LargeResourceLink>;
  deprecated?: boolean;
  replaced_by?: string;
  deprecation_warning?: string;

  constructor(data: Partial<LoadedArtifact>) {
    this.id = data.id ?? "";
    this.type = data.type ?? "";
    this.version = data.version ?? "";
    this.manifest_body = data.manifest_body ?? "";
    this.frontmatter = data.frontmatter ?? "";
    this.skill_raw = data.skill_raw;
    this.resources = data.resources;
    this.resources_base64 = data.resources_base64;
    this.large_resources = data.large_resources;
    this.deprecated = data.deprecated;
    this.replaced_by = data.replaced_by;
    this.deprecation_warning = data.deprecation_warning;
  }

  async materialize(to: string, opts: MaterializeOptions = {}): Promise<string[]> {
    return materializeCanonical({
      to,
      id: this.id,
      type: this.type,
      frontmatter: this.frontmatter,
      manifestBody: this.manifest_body,
      skillRaw: this.skill_raw ?? "",
      inline: decodeInlineForMaterialize(this.resources ?? {}, this.resources_base64),
      large: this.large_resources ?? {},
      fetcher: opts.fetcher ?? fetch,
    });
  }
}

// Spec §7.6.2 — one bulk-load envelope with a materialize() helper. Status
// is "ok" when the artifact resolved and "error" otherwise; the error
// envelope carries the §6.10 code. Batch resources travel as presigned
// references, so materialize fetches every resource.
export class BatchResult {
  id: string;
  status: "ok" | "error";
  type?: string;
  version?: string;
  content_hash?: string;
  manifest_body?: string;
  frontmatter?: string;
  // spec: §4.3.4 / §11 — verbatim SKILL.md for a skill (byte-identical).
  skill_raw?: string;
  // A resource carries presigned_url with an object store configured, or the
  // bytes inline (base64-encoded when inline_base64 is set) in the
  // standalone-without-storage mode (§7.6.2, F-7.6.4).
  resources?: {
    path: string;
    presigned_url?: string;
    content_hash?: string;
    inline?: string;
    inline_base64?: boolean;
  }[];
  deprecated?: boolean;
  replaced_by?: string;
  deprecation_warning?: string;
  error?: {
    code: string;
    message: string;
    retryable?: boolean;
    // spec: §6.10 — a batch error item carries the full envelope (F-6.10.1).
    details?: Record<string, unknown>;
    suggested_action?: string;
  };

  constructor(data: Partial<BatchResult>) {
    this.id = data.id ?? "";
    this.status = data.status ?? "error";
    this.type = data.type;
    this.version = data.version;
    this.content_hash = data.content_hash;
    this.manifest_body = data.manifest_body;
    this.frontmatter = data.frontmatter;
    this.skill_raw = data.skill_raw;
    this.resources = data.resources;
    this.deprecated = data.deprecated;
    this.replaced_by = data.replaced_by;
    this.deprecation_warning = data.deprecation_warning;
    this.error = data.error;
  }

  async materialize(to: string, opts: MaterializeOptions = {}): Promise<string[]> {
    if (this.status !== "ok") {
      // spec: §13.2.1 / §6.10 — re-raise the specific subclass so a
      // registry.read_only batch item surfaces as RegistryReadOnly.
      throw registryErrorFromEnvelope({
        code: this.error?.code ?? "registry.unknown",
        message: this.error?.message ?? `cannot materialize ${this.id}`,
        retryable: this.error?.retryable,
        details: this.error?.details,
        suggested_action: this.error?.suggested_action,
      });
    }
    // §7.6.2: a resource carries a presigned_url with an object store
    // configured. In the standalone-without-storage mode it carries the bytes
    // inline (base64-encoded when inline_base64 is set), so deliver those
    // rather than fetching a URL that does not exist (F-7.6.4).
    const large: Record<string, LargeResourceLink> = {};
    const inline: Record<string, string | Uint8Array> = {};
    for (const r of this.resources ?? []) {
      if (r.presigned_url) {
        large[r.path] = { url: r.presigned_url, content_hash: r.content_hash };
      } else {
        const v = r.inline ?? "";
        inline[r.path] = r.inline_base64 ? Uint8Array.from(Buffer.from(v, "base64")) : v;
      }
    }
    return materializeCanonical({
      to,
      id: this.id,
      type: this.type ?? "",
      frontmatter: this.frontmatter ?? "",
      manifestBody: this.manifest_body ?? "",
      skillRaw: this.skill_raw ?? "",
      inline,
      large,
      fetcher: opts.fetcher ?? fetch,
    });
  }
}

export interface DependencyEdge {
  from: string;
  to: string;
  kind: "extends" | "delegates_to" | "mcpServers";
}

export interface ScopePreview {
  layers: string[];
  artifact_count: number;
  by_type: Record<string, number>;
  by_sensitivity: Record<string, number>;
}

export interface RegistryEvent {
  event: string;
  trace_id?: string;
  timestamp?: string;
  actor?: Record<string, unknown>;
  data?: Record<string, unknown>;
}

export class RegistryError extends Error {
  constructor(
    public readonly code: string,
    message: string,
    public readonly retryable: boolean = false,
    // spec: §6.10 — the full envelope carries a machine-readable details map
    // (for example {runtime_iss: ...}) and an operator remediation hint.
    // Callers read both off the error (F-6.10.1); they default to an empty map
    // and empty string when the registry omits them.
    public readonly details: Record<string, unknown> = {},
    public readonly suggestedAction: string = "",
  ) {
    super(`${code}: ${message}`);
    this.name = "RegistryError";
  }
}

// RegistryReadOnly is thrown when a write is rejected because the
// registry is in §13.2.1 read-only mode (the §6.10 registry.read_only
// error code). It extends RegistryError, so callers that catch the base
// error keep working while callers that want to retry once the registry
// leaves read-only mode can catch this type specifically.
export class RegistryReadOnly extends RegistryError {
  constructor(
    message: string,
    retryable = false,
    details: Record<string, unknown> = {},
    suggestedAction = "",
  ) {
    super("registry.read_only", message, retryable, details, suggestedAction);
    this.name = "RegistryReadOnly";
  }
}

// registryErrorFromEnvelope builds the §6.10 error for a structured
// envelope, choosing the most specific subclass for the code.
// registry.read_only (§13.2.1) maps to RegistryReadOnly; every other code
// maps to the base RegistryError.
export function registryErrorFromEnvelope(env: {
  code?: unknown;
  message?: unknown;
  retryable?: unknown;
  details?: unknown;
  suggested_action?: unknown;
}): RegistryError {
  const code = (env.code as string) ?? "registry.unknown";
  const message = (env.message as string) ?? "";
  const retryable = Boolean(env.retryable);
  // spec: §6.10 — preserve the machine-readable details map and the operator
  // remediation hint so callers can read the full envelope (F-6.10.1).
  const details =
    env.details && typeof env.details === "object"
      ? (env.details as Record<string, unknown>)
      : {};
  const suggestedAction =
    typeof env.suggested_action === "string" ? env.suggested_action : "";
  if (code === "registry.read_only") {
    return new RegistryReadOnly(message, retryable, details, suggestedAction);
  }
  return new RegistryError(code, message, retryable, details, suggestedAction);
}

// spec: §11 (Search browse mode test) — the search top_k cap. Distinct from the
// §7.6.2 batch-load 50-ID cap; this bounds the number of returned search results.
const MAX_TOP_K = 50;

// checkTopK rejects top_k > 50 before the request is sent (spec §11, §6.10).
function checkTopK(topK: number): void {
  if (topK > MAX_TOP_K) {
    throw new RegistryError("registry.invalid_argument", "top_k > 50");
  }
}

// spec: §6.5 / §6.2 — the recognized PODIUM_CACHE_MODE values.
export type CacheMode = "always-revalidate" | "offline-first" | "offline-only";
const CACHE_MODES: readonly CacheMode[] = [
  "always-revalidate",
  "offline-first",
  "offline-only",
];

export interface ClientOptions {
  registry: string;
  identityProvider?: string;
  overlayPath?: string;
  fetcher?: typeof fetch;
  // spec: §7.6 — the session/access token the client attaches as its Bearer
  // credential so it reaches the registry with the same identity as the MCP
  // path. fromEnv reads it from PODIUM_SESSION_TOKEN.
  token?: string;
  // spec: §7.4 — the cache mode the SDK applies, shared with the MCP server
  // and podium sync. fromEnv reads it from PODIUM_CACHE_MODE.
  cacheMode?: CacheMode;
}

export class Client {
  readonly registry: string;
  readonly identityProvider: string;
  readonly overlayPath?: string;
  readonly cacheMode: CacheMode;
  private readonly fetcher: typeof fetch;
  private token: string;
  // spec §6.4 — the overlay index is read on demand and cached per
  // session_id ("cached for the duration of a session_id"). The empty-string
  // key holds the most recent no-session read, which is refreshed each call.
  private readonly overlayCache = new Map<string, LocalOverlay | null>();

  constructor(opts: ClientOptions) {
    this.registry = opts.registry.replace(/\/$/, "");
    this.identityProvider = opts.identityProvider ?? "oauth-device-code";
    // The explicit/env overlay candidate; the <CWD>/.podium/overlay/ fallback
    // is applied lazily on the first overlay read (§6.4).
    this.overlayPath = opts.overlayPath ?? process.env.PODIUM_OVERLAY_PATH;
    this.fetcher = opts.fetcher ?? fetch;
    this.token = opts.token ?? "";
    // spec: §7.4 — "podium sync and the SDKs apply the same cache modes." The
    // SDK keeps no persistent content cache, so always-revalidate and
    // offline-first both fetch on every call (nothing is cached to serve),
    // while offline-only "never contact the registry" and raises a structured
    // cache-miss error before any request.
    const mode = opts.cacheMode ?? "always-revalidate";
    if (!CACHE_MODES.includes(mode)) {
      throw new Error(
        `cacheMode must be one of ${CACHE_MODES.join(" | ")}, got ${String(mode)}`,
      );
    }
    this.cacheMode = mode;
  }

  // spec §14.4 / §13.10 — fromEnv "picks up registry URL from sync.yaml +
  // overlay path". The registry resolves from PODIUM_REGISTRY first, then the
  // project-local, project-shared, and user-global sync.yaml scopes (§7.5.2);
  // reading sync.yaml is async, so fromEnv returns a promise. When the
  // registry is unset across every scope the SDK reports the same
  // config.no_registry condition the CLI does (§6.10), pointing at
  // `podium init`.
  static async fromEnv(): Promise<Client> {
    const registry = await resolveRegistry(
      process.env.PODIUM_REGISTRY,
      process.cwd(),
      process.env.HOME ?? process.env.USERPROFILE,
    );
    if (!registry) {
      throw new RegistryError(
        "config.no_registry",
        "no registry configured: set PODIUM_REGISTRY, add defaults.registry to sync.yaml, or run `podium init`",
      );
    }
    return new Client({
      registry,
      identityProvider: process.env.PODIUM_IDENTITY_PROVIDER,
      overlayPath: process.env.PODIUM_OVERLAY_PATH,
      // §6.3.2 injected session token: the env credential the MCP bridge also
      // reads, so the SDK reaches the registry as the same identity.
      token: process.env.PODIUM_SESSION_TOKEN,
      // §7.4 cache mode, shared with the MCP server and podium sync.
      cacheMode: (process.env.PODIUM_CACHE_MODE as CacheMode) || "always-revalidate",
    });
  }

  // guardOffline enforces §7.4 offline-only: the SDK has no local cache, so an
  // offline-only call is always a cache miss and throws the structured
  // network.offline_cache_miss error (the §6.10 network.* namespace, matching
  // the MCP server) before a request is issued (F-7.4.3).
  private guardOffline(): void {
    if (this.cacheMode === "offline-only") {
      throw new RegistryError(
        "network.offline_cache_miss",
        "offline-only mode: the registry was not contacted and the SDK keeps no offline cache",
      );
    }
  }

  // unreachableError maps a transport-level fetch rejection to the §7.4
  // network.registry_unreachable structured error (F-7.4.2). A connection
  // refused or DNS failure rejects the fetch promise (a TypeError) before any
  // Response exists. The SDK keeps no content cache, so an unreachable registry
  // in any mode that contacts it (always-revalidate and offline-first;
  // offline-only short-circuits in guardOffline) is a no-cache miss. The error
  // mirrors the MCP bridge's namespaced code, retryable flag, and hint.
  private unreachableError(cause: unknown): RegistryError {
    const detail = cause instanceof Error ? cause.message : String(cause);
    return new RegistryError(
      "network.registry_unreachable",
      `the registry at ${this.registry} is unreachable: ${detail}`,
      true,
      {},
      "Check network connectivity to the registry; the request can be retried once it is reachable.",
    );
  }

  // headers returns request headers with the Bearer credential attached when
  // a token is configured (spec: §7.6).
  private headers(extra?: Record<string, string>): Record<string, string> {
    const h: Record<string, string> = { ...(extra ?? {}) };
    if (this.token) h.Authorization = `Bearer ${this.token}`;
    return h;
  }

  // overlayIndex reads the overlay on demand, applying the §6.4 CWD fallback
  // and caching per session_id. With no session_id the overlay is re-read on
  // each call so in-progress edits stay visible.
  private async overlayIndex(sessionID = ""): Promise<LocalOverlay | null> {
    if (sessionID && this.overlayCache.has(sessionID)) {
      return this.overlayCache.get(sessionID) ?? null;
    }
    const path = await resolveOverlayPath(
      this.overlayPath,
      undefined,
      process.cwd(),
    );
    let index: LocalOverlay | null = path ? await LocalOverlay.load(path) : null;
    if (index && index.artifacts.size === 0) index = null;
    if (sessionID) this.overlayCache.set(sessionID, index);
    return index;
  }

  // spec §14.8 / §7.7 — login() runs the §6.3 oauth-device-code flow before
  // any catalog calls. The IdP is discovered from the registry's RFC 8414
  // metadata (overridable via opts or the PODIUM_OAUTH_* env vars). The
  // verification URL and user code print to stderr; polling is bounded by
  // timeoutMs (10 minutes by default). On success the access token is stored
  // on the client and attached as the Authorization: Bearer credential on
  // every subsequent request (§7.6).
  async login(
    opts: {
      timeoutMs?: number;
      clientID?: string;
      scopes?: string[];
      audience?: string;
      deviceAuthorizationEndpoint?: string;
      tokenEndpoint?: string;
    } = {},
  ): Promise<Tokens> {
    const clientID =
      opts.clientID ?? process.env.PODIUM_OAUTH_CLIENT_ID ?? "podium-cli";
    const scopes = opts.scopes ?? ["openid", "profile", "email", "groups"];
    const audience = opts.audience ?? process.env.PODIUM_OAUTH_AUDIENCE ?? "";
    let deviceUrl =
      opts.deviceAuthorizationEndpoint ??
      process.env.PODIUM_OAUTH_AUTHORIZATION_ENDPOINT ??
      "";
    let tokenUrl = opts.tokenEndpoint ?? process.env.PODIUM_OAUTH_TOKEN_URL ?? "";
    if (!deviceUrl) {
      const discovered = await discoverIdp(this.registry, this.fetcher);
      deviceUrl = discovered.deviceUrl;
      if (!tokenUrl) tokenUrl = discovered.tokenUrl;
    }
    if (!tokenUrl) tokenUrl = this.registry.replace(/\/$/, "") + "/oauth2/token";

    const auth = await initiate(deviceUrl, clientID, scopes, audience, this.fetcher);
    process.stderr.write(`Visit: ${auth.verificationUri}\n`);
    process.stderr.write(`User code: ${auth.userCode}\n`);
    const tokens = await poll(tokenUrl, clientID, auth, {
      timeoutMs: opts.timeoutMs ?? DEFAULT_TIMEOUT_MS,
      fetcher: this.fetcher,
    });
    this.token = tokens.accessToken;
    return tokens;
  }

  // spec: §4.5.4 / §4.5.5 / §5.1 (F-4.5.2/F-6.4.2) — load_domain proxies the
  // registry's rendered result and, when a workspace overlay is configured,
  // composes the overlay DOMAIN.md set and overlay artifacts onto it
  // client-side. The overlay is the highest-precedence layer in the caller's
  // effective view (§6.4). With no overlay the behavior is identical to the
  // pre-merge proxy.
  //
  // depth is unset by default. The query parameter is omitted unless the
  // caller supplies one (get() drops undefined values), so the registry
  // applies its configured default max_depth (3) rather than the SDK forcing a
  // single rendered level.
  async loadDomain(path = "", depth?: number): Promise<LoadDomainResult> {
    const params = {
      ...(path ? { path } : {}),
      ...(depth !== undefined ? { depth } : {}),
    };
    const index = await this.overlayForDomain();
    if (!index || (index.domains.size === 0 && index.artifacts.size === 0)) {
      return (await this.get("/v1/load_domain", params)) as LoadDomainResult;
    }
    let reg: LoadDomainResult;
    try {
      reg = (await this.get("/v1/load_domain", params)) as LoadDomainResult;
    } catch (e) {
      // spec §4.5.2 / §6.4 — a domain that exists only in the workspace overlay
      // is part of the effective view, but the registry 404s it because it never
      // sees the overlay. Synthesize an empty result and compose the overlay onto
      // it; any other error propagates.
      if (
        e instanceof RegistryError &&
        e.code === "domain.not_found" &&
        overlayHasDomainContent(path, index.domains, [...index.artifacts.values()])
      ) {
        reg = { path, subdomains: [], notable: [] };
      } else {
        throw e;
      }
    }
    return this.mergeDomain(reg, path, depth, index);
  }

  // catalog issues GET /v1/catalog?scope=<scope> and returns the visible
  // artifact descriptors under that scope (§4.5.2 merged-view glob
  // resolution). A registry-side error degrades the merge to overlay-only
  // resolution rather than failing the call, so any rejection yields [].
  private async catalog(
    scope: string,
  ): Promise<Array<{ id: string; type?: string; summary?: string }>> {
    try {
      const body = (await this.get("/v1/catalog", scope ? { scope } : {})) as {
        artifacts?: Array<{ id: string; type?: string; summary?: string }>;
      };
      return body.artifacts ?? [];
    } catch {
      return [];
    }
  }

  // mergeDomain composes the workspace overlay onto the registry load_domain
  // result for path, per §4.5.4. With no overlay domains and no overlay
  // artifacts the registry result passes through unchanged.
  private async mergeDomain(
    reg: LoadDomainResult,
    path: string,
    depth: number | undefined,
    index: LocalOverlay | null,
  ): Promise<LoadDomainResult> {
    if (!index || (index.domains.size === 0 && index.artifacts.size === 0)) {
      reg.subdomains = reg.subdomains ?? [];
      reg.notable = reg.notable ?? [];
      return reg;
    }
    const domains = index.domains;
    const records = [...index.artifacts.values()];
    const od = domains.get(path);

    // §4.5.4 keywords append-unique. The root has no DOMAIN.md (§4.5.5), so a
    // description/keyword override applies only to a non-root requested path.
    if (path !== "" && od && od.keywords.length > 0) {
      reg.keywords = uniqueAppend(reg.keywords ?? [], od.keywords);
    }
    // §4.5.4 description/body, overlay highest precedence.
    if (path !== "" && od) {
      if (od.body.trim() !== "") reg.description = od.body;
      else if (od.description !== "") reg.description = od.description;
    }

    // §4.5.5 notable candidate pool extension.
    const candidates = await this.overlayNotableCandidates(path, od, records);
    reg.notable = mergeNotable(
      reg.notable ?? [],
      candidates,
      mergedFeatured(reg.notable ?? [], od),
      od?.notableCount ?? 0,
    );

    // §4.5.5 subdomain enumeration extension and §4.5.3 unlisted pruning.
    reg.subdomains = mergeSubdomains(
      reg.subdomains ?? [],
      path,
      renderDepth(depth),
      domains,
      records,
    );
    return reg;
  }

  // overlayNotableCandidates returns the overlay's contribution to the §4.5.5
  // notable candidate pool for path: the overlay's direct child artifacts
  // (after the merged DOMAIN.md exclude:) plus the overlay DOMAIN.md include:
  // set resolved over the merged view (registry catalog ∪ overlay).
  private async overlayNotableCandidates(
    path: string,
    od: OverlayDomain | undefined,
    records: OverlayArtifact[],
  ): Promise<LoadDomainNotable[]> {
    const include = od?.include ?? [];
    const exclude = od?.exclude ?? [];
    const out: LoadDomainNotable[] = [];
    const seen = new Set<string>();
    for (const rec of records) {
      if (parentOf(rec.id) !== path) continue;
      if (matchAny(exclude, rec.id)) continue;
      if (seen.has(rec.id)) continue;
      seen.add(rec.id);
      out.push(overlayArtifactDescriptor(rec));
    }
    if (include.length > 0) {
      for (const m of await this.resolveOverlayIncludes(include, exclude, records)) {
        if (seen.has(m.id)) continue;
        seen.add(m.id);
        out.push(m);
      }
    }
    return out;
  }

  // resolveOverlayIncludes resolves the overlay DOMAIN.md include:/exclude:
  // globs over the merged view (registry catalog ∪ overlay) per §4.5.2 and maps
  // each match to a notable descriptor: an overlay record (highest precedence)
  // when the id is in the overlay, otherwise the registry catalog descriptor.
  private async resolveOverlayIncludes(
    include: string[],
    exclude: string[],
    records: OverlayArtifact[],
  ): Promise<LoadDomainNotable[]> {
    const catalog = await this.fetchCatalogForIncludes(include);
    const overlayByID = new Map<string, OverlayArtifact>();
    for (const rec of records) overlayByID.set(rec.id, rec);
    const seen = new Set<string>();
    const ids: string[] = [];
    for (const id of catalog.keys()) {
      if (!seen.has(id)) {
        seen.add(id);
        ids.push(id);
      }
    }
    for (const id of overlayByID.keys()) {
      if (!seen.has(id)) {
        seen.add(id);
        ids.push(id);
      }
    }
    ids.sort();
    const out: LoadDomainNotable[] = [];
    for (const id of resolveImports(include, exclude, ids)) {
      const rec = overlayByID.get(id);
      if (rec) {
        out.push(overlayArtifactDescriptor(rec));
        continue;
      }
      const e = catalog.get(id);
      if (e) out.push(e);
    }
    return out;
  }

  // fetchCatalogForIncludes fetches the registry catalog descriptors needed to
  // resolve include over the merged view, keyed by id. It scopes each request
  // to the literal path prefix of an include pattern; a leading-glob pattern
  // widens the fetch to the whole visible catalog.
  private async fetchCatalogForIncludes(
    include: string[],
  ): Promise<Map<string, LoadDomainNotable>> {
    const prefixes = new Set<string>();
    let full = false;
    for (const p of include) {
      const pre = globLiteralPrefix(p);
      if (pre === "") {
        full = true;
        break;
      }
      prefixes.add(pre);
    }
    const out = new Map<string, LoadDomainNotable>();
    const fetch = async (scope: string): Promise<void> => {
      for (const e of await this.catalog(scope)) {
        out.set(e.id, { id: e.id, type: e.type, summary: e.summary });
      }
    };
    if (full) {
      await fetch("");
      return out;
    }
    for (const pre of prefixes) await fetch(pre);
    return out;
  }

  // overlayForDomain reads the overlay for the load_domain merge. Unlike
  // overlayIndex it keeps a domain-only overlay (DOMAIN.md files with no
  // overlay artifacts) visible, so an overlay that only re-describes or prunes
  // domains still composes onto the registry tree (§4.5.4).
  private async overlayForDomain(): Promise<LocalOverlay | null> {
    const overlayPath = await resolveOverlayPath(this.overlayPath, undefined, process.cwd());
    return overlayPath ? LocalOverlay.load(overlayPath) : null;
  }

  async searchDomains(
    query = "",
    opts: { scope?: string; topK?: number } = {},
  ): Promise<SearchResult> {
    // spec: §11 (Search browse mode test) — top_k > 50 is rejected with a
    // structured registry.invalid_argument error, enforced client-side in the
    // SDK as well as server-side at the registry (§6.10).
    checkTopK(opts.topK ?? 10);
    const params: Record<string, unknown> = { top_k: opts.topK ?? 10 };
    if (query) params.query = query;
    if (opts.scope) params.scope = opts.scope;
    return this.get("/v1/search_domains", params) as Promise<SearchResult>;
  }

  async searchArtifacts(
    query = "",
    opts: {
      type?: string;
      scope?: string;
      tags?: string[];
      topK?: number;
      // spec: §7.6 — session_id for session-consistent retrieval.
      sessionID?: string;
    } = {},
  ): Promise<SearchResult> {
    // spec: §11 (Search browse mode test) — client-side top_k cap, mirroring
    // the server's registry.invalid_argument rejection (§6.10).
    checkTopK(opts.topK ?? 10);
    const params: Record<string, unknown> = { top_k: opts.topK ?? 10 };
    if (query) params.query = query;
    if (opts.type) params.type = opts.type;
    if (opts.scope) params.scope = opts.scope;
    if (opts.tags?.length) params.tags = opts.tags.join(",");
    if (opts.sessionID) params.session_id = opts.sessionID;
    const body = (await this.get("/v1/search_artifacts", params)) as SearchResult;
    return this.fuseOverlay(body, query, opts);
  }

  // fuseOverlay merges workspace-overlay hits into the registry results via
  // RRF (spec §6.4, §6.4.1). The overlay is the highest-precedence layer, so
  // an overlay artifact's metadata wins over a same-id registry hit. With no
  // overlay configured the registry result passes through unchanged.
  private async fuseOverlay(
    body: SearchResult,
    query: string,
    opts: { type?: string; scope?: string; tags?: string[]; topK?: number; sessionID?: string },
  ): Promise<SearchResult> {
    const index = await this.overlayIndex(opts.sessionID ?? "");
    const registryResults = body.results ?? [];
    if (!index) return { ...body, results: registryResults };
    const topK = opts.topK ?? 10;
    // spec §6.4: thread `scope` into the overlay search so a scoped query
    // excludes out-of-scope overlay artifacts, matching the registry stream
    // and the Go MCP server.
    const hits = index.search(query, { type: opts.type, scope: opts.scope, tags: opts.tags, topK });
    if (hits.length === 0) return { ...body, results: registryResults };
    const overlayIDs = hits.map((h) => h.id);
    const registryIDs = registryResults.map((r) => r.id);
    const fused = rrfFuse([overlayIDs, registryIDs]);
    const byID = new Map<string, ArtifactDescriptor>();
    for (const r of registryResults) byID.set(r.id, { ...r });
    for (const h of hits) {
      byID.set(h.id, {
        id: h.id,
        type: h.type,
        version: h.version,
        description: h.description,
        tags: [...h.tags],
        score: fused.get(h.id) ?? 0,
      });
    }
    for (const r of registryResults) {
      if (!overlayIDs.includes(r.id)) {
        const d = byID.get(r.id);
        if (d) d.score = fused.get(r.id) ?? r.score ?? 0;
      }
    }
    const merged = [...byID.values()]
      .sort((a, b) => (b.score ?? 0) - (a.score ?? 0) || (a.id < b.id ? -1 : 1))
      .slice(0, topK);
    const extra = overlayIDs.filter((id) => !registryIDs.includes(id)).length;
    return { ...body, results: merged, total_matched: (body.total_matched ?? 0) + extra };
  }

  // spec: §7.6.1 — load_artifact accepts session_id for consistent latest
  // resolution within a session (§4.7.6).
  async loadArtifact(
    id: string,
    version?: string,
    opts: { sessionID?: string } = {},
  ): Promise<LoadedArtifact> {
    // spec §6.4 — the overlay is the highest-precedence layer, so an
    // in-progress overlay artifact resolves ahead of the registry. A pinned
    // version still goes to the registry: the overlay carries a single
    // working copy, not a version history.
    if (!version) {
      const index = await this.overlayIndex(opts.sessionID ?? "");
      const art = index?.get(id);
      if (art) {
        return new LoadedArtifact({
          id: art.id,
          type: art.type,
          version: art.version,
          manifest_body: art.body,
          frontmatter: art.frontmatter,
          skill_raw: art.skillRaw,
          resources: art.resources,
          large_resources: {},
        });
      }
    }
    const params: Record<string, unknown> = { id };
    if (version) params.version = version;
    if (opts.sessionID) params.session_id = opts.sessionID;
    const data = (await this.get("/v1/load_artifact", params)) as Partial<LoadedArtifact>;
    return new LoadedArtifact(data);
  }

  // Spec §7.6.2 — bulk fetch via POST /v1/artifacts:batchLoad. The
  // §7.6.2 hard cap is 50 IDs per request; this method splits
  // larger sets transparently. Each returned envelope carries
  // status="ok" with manifest bytes, or status="error" with a
  // §6.10 envelope. Partial failure does not throw.
  async loadArtifacts(
    ids: string[],
    opts: {
      sessionID?: string;
      harness?: string;
      versionPins?: Record<string, string>;
    } = {},
  ): Promise<BatchResult[]> {
    if (ids.length === 0) return [];
    // §7.4 offline-only short-circuit before any network request.
    this.guardOffline();
    const out: BatchResult[] = [];
    const cap = 50;
    for (let i = 0; i < ids.length; i += cap) {
      const chunk = ids.slice(i, i + cap);
      const body: Record<string, unknown> = { ids: chunk };
      if (opts.sessionID) body.session_id = opts.sessionID;
      if (opts.harness) body.harness = opts.harness;
      if (opts.versionPins) {
        const subset: Record<string, string> = {};
        for (const id of chunk) {
          if (opts.versionPins[id]) subset[id] = opts.versionPins[id];
        }
        if (Object.keys(subset).length > 0) body.version_pins = subset;
      }
      let resp: Response;
      try {
        resp = await this.fetcher(this.registry + "/v1/artifacts:batchLoad", {
          method: "POST",
          headers: this.headers({ "Content-Type": "application/json" }),
          body: JSON.stringify(body),
        });
      } catch (e) {
        // spec: §7.4 — an unreachable registry on the batch path also surfaces
        // the structured no-cache error (F-7.4.2).
        throw this.unreachableError(e);
      }
      if (!resp.ok) {
        let envelope: Record<string, unknown> = {};
        try {
          envelope = (await resp.json()) as Record<string, unknown>;
        } catch {
          // ignore parse errors
        }
        // spec: §13.2.1 / §6.10 — a write rejected with registry.read_only
        // surfaces as RegistryReadOnly (a RegistryError subclass).
        throw registryErrorFromEnvelope({
          code: envelope.code ?? "registry.unknown",
          message: envelope.message ?? `HTTP ${resp.status}`,
          retryable: envelope.retryable,
          details: envelope.details,
          suggested_action: envelope.suggested_action,
        });
      }
      const part = (await resp.json()) as Partial<BatchResult>[];
      out.push(...part.map((e) => new BatchResult(e)));
    }
    return out;
  }

  // Spec §7.6 — dependents_of returns reverse-dependency edges for
  // impact analysis (extends, delegates_to, mcpServers).
  async dependentsOf(artifactID: string): Promise<DependencyEdge[]> {
    const body = (await this.get("/v1/dependents", { id: artifactID })) as {
      edges?: DependencyEdge[];
    };
    return body.edges ?? [];
  }

  // Spec §3.5 — preview_scope returns aggregated metadata for the
  // calling identity's effective view (counts only).
  async previewScope(): Promise<ScopePreview> {
    return this.get("/v1/scope/preview", {}) as Promise<ScopePreview>;
  }

  // Spec §7.6 — subscribe streams change events. Phase 14 ships a
  // long-poll JSON-Lines variant; SSE / websocket land alongside the
  // server's outbound webhook subsystem.
  async *subscribe(eventTypes: string[]): AsyncIterable<RegistryEvent> {
    // §7.4 offline-only short-circuit before opening the event stream.
    this.guardOffline();
    const url = new URL(this.registry + "/v1/events");
    for (const t of eventTypes) {
      url.searchParams.append("type", t);
    }
    let resp: Response;
    try {
      resp = await this.fetcher(url.toString(), { headers: this.headers() });
    } catch (e) {
      // spec: §7.4 — an unreachable registry on the event stream surfaces the
      // structured no-cache error (F-7.4.2).
      throw this.unreachableError(e);
    }
    if (!resp.ok || !resp.body) {
      throw new RegistryError(
        "registry.unavailable",
        `subscribe HTTP ${resp.status}`,
      );
    }
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      buffer += decoder.decode(value, { stream: true });
      let nl = buffer.indexOf("\n");
      while (nl >= 0) {
        const line = buffer.slice(0, nl);
        buffer = buffer.slice(nl + 1);
        if (line.trim() !== "") {
          yield JSON.parse(line) as RegistryEvent;
        }
        nl = buffer.indexOf("\n");
      }
    }
  }

  private async get(path: string, params: Record<string, unknown>): Promise<unknown> {
    // §7.4 offline-only short-circuit before any network request.
    this.guardOffline();
    const url = new URL(this.registry + path);
    for (const [k, v] of Object.entries(params)) {
      if (v === undefined || v === null) continue;
      url.searchParams.set(k, String(v));
    }
    let resp: Response;
    try {
      resp = await this.fetcher(url.toString(), { headers: this.headers() });
    } catch (e) {
      // spec: §7.4 — a rejected fetch (no Response) is the always-revalidate
      // no-cache case (F-7.4.2).
      throw this.unreachableError(e);
    }
    if (!resp.ok) {
      let envelope: Record<string, unknown> = {};
      try {
        envelope = (await resp.json()) as Record<string, unknown>;
      } catch {
        // ignore parse errors; fall through to generic error.
      }
      // spec: §13.2.1 / §6.10 — registry.read_only surfaces as
      // RegistryReadOnly so read callers can detect the degraded mode.
      throw registryErrorFromEnvelope({
        code: envelope.code ?? "registry.unknown",
        message: envelope.message ?? `HTTP ${resp.status}`,
        retryable: envelope.retryable,
        details: envelope.details,
        suggested_action: envelope.suggested_action,
      });
    }
    return resp.json();
  }
}
