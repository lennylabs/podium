// Podium TypeScript SDK — thin HTTP client over the registry API
// (spec §7.6). Phase 14 ships the meta-tool surface plus device-code
// scaffolding; identity-provider integration matures in later phases.

export interface ArtifactDescriptor {
  id: string;
  type: string;
  version?: string;
  description?: string;
  tags?: string[];
  score?: number;
}

export interface SearchResult {
  query?: string;
  total_matched: number;
  results?: ArtifactDescriptor[];
  domains?: Record<string, unknown>[];
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
  inline: Record<string, string>;
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
      inline: this.resources ?? {},
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
  resources?: { path: string; presigned_url: string; content_hash?: string }[];
  deprecated?: boolean;
  replaced_by?: string;
  deprecation_warning?: string;
  error?: {
    code: string;
    message: string;
    retryable?: boolean;
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
      });
    }
    const large: Record<string, LargeResourceLink> = {};
    for (const r of this.resources ?? []) {
      large[r.path] = { url: r.presigned_url, content_hash: r.content_hash };
    }
    return materializeCanonical({
      to,
      id: this.id,
      type: this.type ?? "",
      frontmatter: this.frontmatter ?? "",
      manifestBody: this.manifest_body ?? "",
      skillRaw: this.skill_raw ?? "",
      inline: {},
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
  constructor(message: string, retryable = false) {
    super("registry.read_only", message, retryable);
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
}): RegistryError {
  const code = (env.code as string) ?? "registry.unknown";
  const message = (env.message as string) ?? "";
  const retryable = Boolean(env.retryable);
  if (code === "registry.read_only") {
    return new RegistryReadOnly(message, retryable);
  }
  return new RegistryError(code, message, retryable);
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

export interface ClientOptions {
  registry: string;
  identityProvider?: string;
  overlayPath?: string;
  fetcher?: typeof fetch;
  // spec: §7.6 — the session/access token the client attaches as its Bearer
  // credential so it reaches the registry with the same identity as the MCP
  // path. fromEnv reads it from PODIUM_SESSION_TOKEN.
  token?: string;
}

export class Client {
  readonly registry: string;
  readonly identityProvider: string;
  readonly overlayPath?: string;
  private readonly fetcher: typeof fetch;
  private readonly token: string;

  constructor(opts: ClientOptions) {
    this.registry = opts.registry.replace(/\/$/, "");
    this.identityProvider = opts.identityProvider ?? "oauth-device-code";
    this.overlayPath = opts.overlayPath;
    this.fetcher = opts.fetcher ?? fetch;
    this.token = opts.token ?? "";
  }

  static fromEnv(): Client {
    const registry = process.env.PODIUM_REGISTRY;
    if (!registry) {
      throw new Error("PODIUM_REGISTRY environment variable is required");
    }
    return new Client({
      registry,
      identityProvider: process.env.PODIUM_IDENTITY_PROVIDER,
      overlayPath: process.env.PODIUM_OVERLAY_PATH,
      // §6.3.2 injected session token: the env credential the MCP bridge also
      // reads, so the SDK reaches the registry as the same identity.
      token: process.env.PODIUM_SESSION_TOKEN,
    });
  }

  // headers returns request headers with the Bearer credential attached when
  // a token is configured (spec: §7.6).
  private headers(extra?: Record<string, string>): Record<string, string> {
    const h: Record<string, string> = { ...(extra ?? {}) };
    if (this.token) h.Authorization = `Bearer ${this.token}`;
    return h;
  }

  async loadDomain(path = "", depth = 1): Promise<Record<string, unknown>> {
    return this.get("/v1/load_domain", { ...(path ? { path } : {}), depth }) as Promise<
      Record<string, unknown>
    >;
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
    return this.get("/v1/search_artifacts", params) as Promise<SearchResult>;
  }

  // spec: §7.6.1 — load_artifact accepts session_id for consistent latest
  // resolution within a session (§4.7.6).
  async loadArtifact(
    id: string,
    version?: string,
    opts: { sessionID?: string } = {},
  ): Promise<LoadedArtifact> {
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
      const resp = await this.fetcher(this.registry + "/v1/artifacts:batchLoad", {
        method: "POST",
        headers: this.headers({ "Content-Type": "application/json" }),
        body: JSON.stringify(body),
      });
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
    const url = new URL(this.registry + "/v1/events");
    for (const t of eventTypes) {
      url.searchParams.append("type", t);
    }
    const resp = await this.fetcher(url.toString(), { headers: this.headers() });
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
    const url = new URL(this.registry + path);
    for (const [k, v] of Object.entries(params)) {
      if (v === undefined || v === null) continue;
      url.searchParams.set(k, String(v));
    }
    const resp = await this.fetcher(url.toString(), { headers: this.headers() });
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
      });
    }
    return resp.json();
  }
}
