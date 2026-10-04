// Podium TypeScript SDK — thin HTTP client over the registry API
// (spec §7.6). The client resolves the registry from sync.yaml, merges the
// workspace overlay client-side, and runs the §6.3 oauth-device-code flow
// via Client.login(). The config/overlay/oauth helpers load Node's fs/path
// lazily, so importing the SDK stays safe in edge bundles.

import { resolveRegistry, resolveVerification } from "./config.js";
import {
  POLICIES,
  checkDelivery,
  checkLinked,
  decodeBase64,
  manifestBodyOf,
  parseBatchResponse,
  parseLoadResponse,
  placeManifestDocument,
  plainJson,
  type BatchEntry,
  type ParsedLoad,
  type Verification,
  type VerifyPolicy,
} from "./delivery.js";
import { RegistryError, RegistryReadOnly, registryErrorFromEnvelope } from "./errors.js";
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
  PendingLogin,
  createPendingLogin,
  discoverIdp,
  finishPending,
  initiate,
  type DeviceCodeErrorReason,
  type Tokens,
} from "./oauth.js";

export { DeviceCodeError, PendingLogin, type DeviceCodeErrorReason, type Tokens };
export { RegistryError, RegistryReadOnly, registryErrorFromEnvelope };

// LoginOptions configures the device-authorization request that startLogin()
// and login() issue. Unset fields fall back to the PODIUM_OAUTH_* environment
// variables and then to RFC 8414 discovery against the registry.
export interface LoginOptions {
  clientID?: string;
  scopes?: string[];
  audience?: string;
  deviceAuthorizationEndpoint?: string;
  tokenEndpoint?: string;
}

// FinishLoginOptions bounds a finishLogin() call. timeoutMs runs from the
// finish call (10 minutes by default); signal cancels the polling.
export interface FinishLoginOptions {
  timeoutMs?: number;
  signal?: AbortSignal;
}

interface DeviceFlow {
  deviceUrl: string;
  tokenUrl: string;
  clientID: string;
  scopes: string[];
  audience: string;
}

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

// presignedSigV4 reports whether raw is an AWS Signature V4 presigned URL, one
// whose query carries a non-empty X-Amz-Signature parameter. spec §13.12: a
// consumer sends no credential when following an S3 presigned URL, and sends
// its token to the filesystem backend's /objects route, which authorizes the
// read against the caller. A URL that fails to parse is treated as not
// presigned, matching the Go consumers.
function presignedSigV4(raw: string): boolean {
  try {
    return (new URL(raw).searchParams.get("X-Amz-Signature") ?? "") !== "";
  } catch {
    return false;
  }
}

export interface MaterializeOptions {
  // Spec §2.2 / §7.6. The SDK embeds no harness adapter and writes the
  // canonical layout, which is the output of the `none` adapter. Any other
  // value throws before a file is written; run `podium sync --harness <name>`
  // for harness-native files.
  harness?: "none";
  // Override the fetcher used to pull §7.2 presigned large resources.
  // Defaults to the global fetch.
  fetcher?: typeof fetch;
}

// Spec §2.2 / §7.6: the type narrowing protects TypeScript callers only, so
// a plain JavaScript caller passing another harness is rejected at runtime.
function requireCanonicalHarness(harness: unknown): void {
  if (harness !== undefined && harness !== "none") {
    throw new Error(
      `materialize() writes the canonical layout only; harness must be "none", got ${JSON.stringify(harness)}. ` +
        "Use `podium sync --harness <name>` for harness-native files.",
    );
  }
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
//
// Spec: §4.7.10, §7.6.3 — every large resource is fetched and checked against
// its link's content_hash before the first file of the artifact is written, so
// a mismatch rejects with materialize.content_hash_mismatch and leaves no file
// of the artifact on disk. Every destination path is resolved first too, so a
// path that escapes the root rejects before any fetch.
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

  const files: Array<[string, Uint8Array | string]> = [[safeJoin("ARTIFACT.md"), args.frontmatter]];
  if (args.type === "skill") {
    // spec: §4.3.4 / §11 — prefer the verbatim SKILL.md the registry delivers;
    // fall back to frontmatter + body only when it is absent.
    files.push([safeJoin("SKILL.md"), args.skillRaw !== "" ? args.skillRaw : args.frontmatter + args.manifestBody]);
  }
  for (const rel of Object.keys(args.inline).sort()) {
    files.push([safeJoin(rel), args.inline[rel]]);
  }
  const links = Object.keys(args.large)
    .sort()
    .map((rel) => {
      const link = args.large[rel];
      if (!link?.url) throw new MaterializeError(`large resource ${rel} has no presigned URL`);
      return { rel, link, target: safeJoin(rel) };
    });
  for (const { rel, link, target } of links) {
    const resp = await args.fetcher(link.url);
    if (!resp.ok) {
      throw new MaterializeError(`fetch large resource ${rel}: HTTP ${resp.status}`);
    }
    const body = new Uint8Array(await resp.arrayBuffer());
    await checkLinked(body, link.content_hash ?? "", `large resource ${JSON.stringify(rel)}`);
    files.push([target, body]);
  }

  for (const [target, bytes] of files) {
    await fs.mkdir(path.dirname(target), { recursive: true });
    await fs.writeFile(target, bytes);
  }
  return files.map(([target]) => target);
}

// decodeInlineForMaterialize decodes a base64-flagged inline resource set
// (spec §4.1 / §7.2 resources_base64) back to raw bytes so a binary
// resource materializes uncorrupted. encoding/json replaces invalid UTF-8 in
// a string with U+FFFD, so the registry base64-encodes the whole inline set
// when any member is binary; the flag is response-wide. It decodes with
// decodeBase64, the §4.7.10 step 3 decoder that checked each served value at
// load, so materialize writes exactly the bytes the delivery hash covered.
function decodeInlineForMaterialize(
  resources: Record<string, string>,
  b64: boolean | undefined,
): Record<string, string | Uint8Array> {
  return Object.fromEntries(
    Object.entries(resources).map(([k, v]) => [k, b64 ? decodeBase64(v) : v]),
  );
}

// Spec §7.6 / §2.2 — the loaded-artifact object exposes materialize(to),
// which writes the canonical layout; resources are inline bytes;
// largeResources are §7.2 presigned references fetched on demand.
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
  // §4.1/§7.2: when true, every resources value is base64-encoded
  // so a binary bundled resource survives JSON transport. materialize decodes.
  resources_base64?: boolean;
  large_resources?: Record<string, LargeResourceLink>;
  deprecated?: boolean;
  replaced_by?: string;
  deprecation_warning?: string;
  // spec: §4.7.10, §7.6.3 — the registry's delivery attestation. On every
  // registry-served load the client recomputes delivery_hash from the served
  // record, compares it with this value, and applies its §4.7.9 policy to
  // delivery_signature before it returns the artifact. A §6.4
  // workspace-overlay load is exempt from the check and carries neither
  // field, because no registry served the record.
  delivery_hash?: string;
  delivery_signature?: string;

  constructor(data: Partial<LoadedArtifact>) {
    this.id = data.id ?? "";
    this.type = data.type ?? "";
    this.version = data.version ?? "";
    this.manifest_body = data.manifest_body ?? "";
    this.frontmatter = data.frontmatter ?? "";
    this.skill_raw = data.skill_raw;
    this.resources = data.resources;
    this.resources_base64 = data.resources_base64;
    this.large_resources = data.large_resources
      ? Object.fromEntries(Object.entries(data.large_resources))
      : undefined;
    this.deprecated = data.deprecated;
    this.replaced_by = data.replaced_by;
    this.deprecation_warning = data.deprecation_warning;
    this.delivery_hash = data.delivery_hash;
    this.delivery_signature = data.delivery_signature;
  }

  async materialize(to: string, opts: MaterializeOptions = {}): Promise<string[]> {
    requireCanonicalHarness(opts.harness);
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
// envelope carries the §6.10 code. A resource the registry holds inline on
// the manifest record travels inline, and materialize fetches every other
// resource from its presigned reference.
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
  // A resource the registry holds inline on the manifest record carries its
  // bytes inline (base64-encoded when inline_base64 is set), at any size and
  // whether or not an object store is configured; every other resource
  // carries presigned_url (§7.6.2). A loaded entry holds only the members
  // §4.7.10 step 2 reads: a link reference {path, presigned_url,
  // content_hash}, and an inline reference {path, content_hash, inline,
  // inline_base64} whose body passed step 6.
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
  // spec: §4.7.10, §7.6.3 — the registry's delivery attestation. On every ok
  // batch entry the client recomputes delivery_hash from the served record,
  // compares it with this value, and applies its §4.7.9 policy to
  // delivery_signature; an entry that fails becomes an error result whose
  // error is the RegistryError (§7.6.2). A §6.4 workspace-overlay load is
  // exempt from the check and carries neither field, because no registry
  // served the record.
  delivery_hash?: string;
  delivery_signature?: string;
  error?: {
    code: string;
    message: string;
    retryable?: boolean;
    // spec: §6.10 — a batch error item carries the full envelope.
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
    this.delivery_hash = data.delivery_hash;
    this.delivery_signature = data.delivery_signature;
    this.error = data.error;
  }

  async materialize(to: string, opts: MaterializeOptions = {}): Promise<string[]> {
    requireCanonicalHarness(opts.harness);
    if (this.status !== "ok") {
      // A delivery-check refusal is already the RegistryError.
      if (this.error instanceof RegistryError) throw this.error;
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
    // Spec: §4.7.10 step 2 — a reference with a non-empty presigned_url is a
    // link reference, fetched and checked against its content_hash; every
    // other reference is inline, its bytes base64-encoded when inline_base64
    // is set (§7.6.2).
    const refs = this.resources ?? [];
    const large: Record<string, LargeResourceLink> = Object.fromEntries(
      refs
        .filter((r) => r.presigned_url)
        .map((r) => [r.path, { url: r.presigned_url as string, content_hash: r.content_hash }]),
    );
    const inline: Record<string, string | Uint8Array> = Object.fromEntries(
      refs
        .filter((r) => !r.presigned_url)
        .map((r) => [r.path, r.inline_base64 ? decodeBase64(r.inline ?? "") : (r.inline ?? "")]),
    );
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

// recordText returns a record field as text. A field placed from a fetched
// manifest document holds the fetched bytes, which §7.6.3 returns decoded as
// UTF-8 with each invalid sequence replaced by U+FFFD.
function recordText(value: string | Uint8Array): string {
  return typeof value === "string" ? value : new TextDecoder().decode(value);
}

// loadedArtifactFrom builds the public LoadedArtifact from a verified
// load_artifact body. Every path-keyed object is a plain-object copy built with
// Object.fromEntries, so a "__proto__" path stays an own property.
function loadedArtifactFrom(parsed: ParsedLoad): LoadedArtifact {
  const rec = parsed.served.record;
  return new LoadedArtifact({
    id: recordText(rec.id),
    type: recordText(rec.type),
    version: recordText(rec.version),
    manifest_body: recordText(rec.manifest_body),
    frontmatter: recordText(rec.frontmatter),
    skill_raw: recordText(rec.skill_raw),
    // §4.1/§7.2: when resources_base64 is set every value is base64 text,
    // which materialize decodes back to raw bytes.
    resources: parsed.resources,
    resources_base64: parsed.resourcesBase64 || undefined,
    // §7.2 large resources travel as presigned references the consumer
    // fetches from object storage; materialize() pulls them.
    large_resources: parsed.largeResources,
    ...parsed.lifecycle,
    delivery_hash: parsed.served.hash,
    delivery_signature: parsed.served.signature || undefined,
  });
}

// batchResultFrom builds one BatchResult from a parsed §7.6.2 entry, verifying
// an ok entry. An ok entry that fails §4.7.10 steps 2 to 7 or the §4.7.9
// policy becomes status "error" carrying the RegistryError, and the other
// entries load.
//
// Spec: §4.7.10, §7.6.2, §7.6.3
async function batchResultFrom(entry: BatchEntry, verification: Verification): Promise<BatchResult> {
  if (!entry.served && !entry.error) {
    const envelope = plainJson(entry.members.get("error"));
    return new BatchResult({
      id: entry.id,
      status: "error",
      error: typeof envelope === "object" && envelope !== null && !Array.isArray(envelope)
        ? (envelope as BatchResult["error"])
        : undefined,
    });
  }
  let err = entry.error;
  if (!err && entry.served) {
    try {
      await checkDelivery(entry.served, verification);
    } catch (e) {
      // checkDelivery refuses only with RegistryError; anything else is a
      // defect that must surface rather than become an error entry.
      if (!(e instanceof RegistryError)) throw e;
      err = e;
    }
  }
  if (err || !entry.served) return new BatchResult({ id: entry.id, status: "error", error: err });
  const rec = entry.served.record;
  return new BatchResult({
    id: recordText(rec.id),
    status: "ok",
    type: recordText(rec.type),
    version: recordText(rec.version),
    content_hash: recordText(rec.content_hash),
    manifest_body: recordText(rec.manifest_body),
    frontmatter: recordText(rec.frontmatter),
    skill_raw: recordText(rec.skill_raw),
    resources: entry.references.map((r) => ({ ...r })),
    ...entry.lifecycle,
    delivery_hash: entry.served.hash,
    delivery_signature: entry.served.signature || undefined,
  });
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
  // spec: §4.7.9 / §7.6.3 — the delivery-check policy, which takes precedence
  // over PODIUM_VERIFY_SIGNATURES and defaults.verify_signatures. Unset, the
  // SDK default applies: always when a verification key is configured and
  // never otherwise.
  verifySignatures?: VerifyPolicy;
  // spec: §4.7.9 — a PODIUM_SIGNATURE_VERIFY_KEY key list that takes
  // precedence over the variable and the key file.
  verifyKeys?: string;
}

export class Client {
  readonly registry: string;
  readonly identityProvider: string;
  readonly overlayPath?: string;
  readonly cacheMode: CacheMode;
  private readonly fetcher: typeof fetch;
  private token: string;
  private readonly explicitVerification: { verifySignatures?: string; verifyKeys?: string };
  // The memoized §4.7.9 resolution; resolvedPolicy is set once it settles.
  private verificationPromise?: Promise<Verification>;
  private resolvedPolicy?: VerifyPolicy;
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
    // spec: §4.7.9 / §7.6.3 — an explicit policy is validated here, and the
    // policy and key set resolve on the first registry request, so the
    // constructor performs no I/O.
    if (opts.verifySignatures !== undefined && !POLICIES.includes(opts.verifySignatures)) {
      throw new Error(
        `verifySignatures must be one of ${POLICIES.join(" | ")}, got ${String(opts.verifySignatures)}`,
      );
    }
    this.explicitVerification = { verifySignatures: opts.verifySignatures, verifyKeys: opts.verifyKeys };
  }

  // verifySignatures is the resolved §4.7.9 policy, or undefined before the
  // client has resolved it.
  get verifySignatures(): VerifyPolicy | undefined {
    return this.resolvedPolicy;
  }

  // verification resolves the §4.7.9 policy and key set once and memoizes the
  // result, a rejection included. Every method that sends a request to the
  // registry awaits it before the request, so an unusable key refuses the call
  // before the registry is contacted; presigned object-store fetches do not
  // await it. The process global is read only when the runtime has one.
  //
  // Spec: §4.7.9, §7.6.3
  private verification(): Promise<Verification> {
    if (!this.verificationPromise) {
      const proc = globalThis.process;
      this.verificationPromise = resolveVerification(
        this.explicitVerification,
        proc?.env ?? {},
        proc ? proc.cwd() : "",
        proc ? (proc.env.HOME ?? proc.env.USERPROFILE) : undefined,
      ).then((v) => {
        this.resolvedPolicy = v.policy;
        return v;
      });
    }
    return this.verificationPromise;
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
    // spec: §4.7.9 / §7.6.3 — resolve the delivery-check policy and key set
    // before the client exists, so an unusable key rejects fromEnv.
    const home = process.env.HOME ?? process.env.USERPROFILE;
    const verification = await resolveVerification({}, process.env, process.cwd(), home);
    const client = new Client({
      registry,
      identityProvider: process.env.PODIUM_IDENTITY_PROVIDER,
      overlayPath: process.env.PODIUM_OVERLAY_PATH,
      // §6.3.2 injected session token: the env credential the MCP bridge also
      // reads, so the SDK reaches the registry as the same identity.
      token: process.env.PODIUM_SESSION_TOKEN,
      // §7.4 cache mode, shared with the MCP server and podium sync.
      cacheMode: (process.env.PODIUM_CACHE_MODE as CacheMode) || "always-revalidate",
    });
    client.verificationPromise = Promise.resolve(verification);
    client.resolvedPolicy = verification.policy;
    return client;
  }

  // guardOffline enforces §7.4 offline-only: the SDK has no local cache, so an
  // offline-only call is always a cache miss and throws the structured
  // network.offline_cache_miss error (the §6.10 network.* namespace, matching
  // the MCP server) before a request is issued.
  private guardOffline(): void {
    if (this.cacheMode === "offline-only") {
      throw new RegistryError(
        "network.offline_cache_miss",
        "offline-only mode: the registry was not contacted and the SDK keeps no offline cache",
      );
    }
  }

  // unreachableError maps a transport-level fetch rejection to the §7.4
  // network.registry_unreachable structured error. A connection
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

  // resolveDeviceFlow resolves the device-flow configuration for startLogin()
  // and login(): explicit options win, then the PODIUM_OAUTH_* environment
  // variables, then the registry's RFC 8414 metadata.
  private async resolveDeviceFlow(opts: LoginOptions): Promise<DeviceFlow> {
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
      // Discovery is a registry request, so the §4.7.9 resolution precedes it.
      await this.verification();
      const discovered = await discoverIdp(this.registry, this.fetcher);
      deviceUrl = discovered.deviceUrl;
      if (!tokenUrl) tokenUrl = discovered.tokenUrl;
    }
    if (!tokenUrl) tokenUrl = this.registry.replace(/\/$/, "") + "/oauth2/token";
    return { deviceUrl, tokenUrl, clientID, scopes, audience };
  }

  // Spec: §6.3 — startLogin() is the start call of the non-blocking pair. It
  // sends the device-authorization request and returns a PendingLogin whose
  // code expiry runs from this call. It prints nothing, opens no browser, and
  // does not poll, so the calling application decides how to show the code.
  async startLogin(opts: LoginOptions = {}): Promise<PendingLogin> {
    const flow = await this.resolveDeviceFlow(opts);
    const auth = await initiate(
      flow.deviceUrl,
      flow.clientID,
      flow.scopes,
      flow.audience,
      this.fetcher,
    );
    return createPendingLogin(auth, flow.tokenUrl, flow.clientID, this.fetcher, Date.now);
  }

  // Spec: §6.3 — finishLogin() is the finish call. It polls until the user
  // approves, the code expires, timeoutMs elapses, or signal aborts, and it
  // consumes the handle whatever the outcome. On success the access token is
  // held in memory on this client and attached as the Authorization: Bearer
  // credential on later requests (§7.6); it is neither persisted nor
  // refreshed. A handle belongs to the client whose startLogin() produced it.
  // Finishing it on another client is unsupported: the polling uses the
  // starting client's fetcher, but the token lands on this one.
  async finishLogin(
    pending: PendingLogin,
    opts: FinishLoginOptions = {},
  ): Promise<Tokens> {
    const tokens = await finishPending(pending, opts);
    this.token = tokens.accessToken;
    return tokens;
  }

  // Spec: §6.3 — login() composes the pair: startLogin(), the verification URL
  // and user code printed to stderr, then finishLogin(). It blocks until the
  // flow completes and keeps the blocking contract for scripts.
  async login(opts: LoginOptions & { timeoutMs?: number } = {}): Promise<Tokens> {
    const pending = await this.startLogin(opts);
    process.stderr.write(`Visit: ${pending.verificationUri}\n`);
    process.stderr.write(`User code: ${pending.userCode}\n`);
    return this.finishLogin(pending, { timeoutMs: opts.timeoutMs });
  }

  // spec: §4.5.4 / §4.5.5 / §5.1 — load_domain proxies the
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
    opts: { sessionID?: string; fetcher?: typeof fetch } = {},
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
    // spec: §4.7.10 / §7.6.3 — the body is decoded by the verification
    // procedure from its raw bytes, the manifest document is fetched and
    // checked, and the record is verified before anything is returned.
    const parsed = await parseLoadResponse(await this.getRaw("/v1/load_artifact", params));
    if (parsed.manifestLink) {
      await this.placeManifestBody(parsed, opts.fetcher ?? fetch);
    }
    await checkDelivery(parsed.served, await this.verification());
    return loadedArtifactFrom(parsed);
  }

  // placeManifestBody resolves a presigned manifest_body_url (spec §6.6) into
  // the served record. A canonical manifest above the 256 KB inline cutoff
  // arrives as a link with the inline manifest fields cleared. The document is
  // fetched from the link's exact-name presigned_url, so an unknown url member
  // is ignored as the Go consumers ignore it, and the fetched bytes are checked
  // against the link's content_hash before the body is derived. The record
  // keeps the fetched bytes rather than decoded text, because the registry
  // frames the raw document and a document that is not valid UTF-8 would
  // otherwise fail the delivery hash.
  private async placeManifestBody(parsed: ParsedLoad, fetcher: typeof fetch): Promise<void> {
    const link = parsed.manifestLink as { presigned_url: string; content_hash: string };
    // The client's token goes to a URL that is not SigV4 presigned (§13.12).
    const resp = await fetcher(link.presigned_url, {
      headers: presignedSigV4(link.presigned_url) ? {} : this.headers(),
    });
    if (!resp.ok) {
      throw new RegistryError("registry.unknown", `fetch manifest body: HTTP ${resp.status}`);
    }
    const doc = new Uint8Array(await resp.arrayBuffer());
    await checkLinked(doc, link.content_hash, "manifest_body_url document");
    placeManifestDocument(parsed.served, doc, manifestBodyOf(doc));
  }

  // Spec §7.6.2 — bulk fetch via POST /v1/artifacts:batchLoad. The
  // §7.6.2 hard cap is 50 IDs per request; this method splits
  // larger sets transparently. Each returned envelope carries
  // status="ok" with manifest bytes, or status="error" with a
  // §6.10 envelope. Partial failure does not throw. The request
  // selects no harness (§7.6.2), so the body carries only ids,
  // session_id, and version_pins.
  async loadArtifacts(
    ids: string[],
    opts: {
      sessionID?: string;
      versionPins?: Record<string, string>;
    } = {},
  ): Promise<BatchResult[]> {
    if (ids.length === 0) return [];
    // spec: §7.6.3 — resolve the §4.7.9 policy before the registry request.
    const verification = await this.verification();
    // §7.4 offline-only short-circuit before any network request.
    this.guardOffline();
    // Every chunk is parsed before any result is built, so a refused body in
    // any chunk rejects the call with no entries (§7.6.3).
    const entries: BatchEntry[] = [];
    const cap = 50;
    for (let i = 0; i < ids.length; i += cap) {
      const chunk = ids.slice(i, i + cap);
      const body: Record<string, unknown> = { ids: chunk };
      if (opts.sessionID) body.session_id = opts.sessionID;
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
        // the structured no-cache error.
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
      entries.push(...(await parseBatchResponse(new Uint8Array(await resp.arrayBuffer()))));
    }
    const out: BatchResult[] = [];
    for (const entry of entries) out.push(await batchResultFrom(entry, verification));
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
    // spec: §7.6.3 — resolve the §4.7.9 policy before the registry request.
    await this.verification();
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
      // structured no-cache error.
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
    return JSON.parse(new TextDecoder().decode(await this.getRaw(path, params)));
  }

  // getRaw issues a registry GET and returns the raw response body.
  private async getRaw(path: string, params: Record<string, unknown>): Promise<Uint8Array> {
    // spec: §7.6.3 — resolve the §4.7.9 policy before the registry request.
    await this.verification();
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
      // no-cache case.
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
    return new Uint8Array(await resp.arrayBuffer());
  }
}
