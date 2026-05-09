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

export interface LoadedArtifact {
  id: string;
  type: string;
  version: string;
  manifest_body: string;
  frontmatter: string;
  resources?: Record<string, string>;
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

export interface ClientOptions {
  registry: string;
  identityProvider?: string;
  overlayPath?: string;
  fetcher?: typeof fetch;
}

export class Client {
  readonly registry: string;
  readonly identityProvider: string;
  readonly overlayPath?: string;
  private readonly fetcher: typeof fetch;

  constructor(opts: ClientOptions) {
    this.registry = opts.registry.replace(/\/$/, "");
    this.identityProvider = opts.identityProvider ?? "oauth-device-code";
    this.overlayPath = opts.overlayPath;
    this.fetcher = opts.fetcher ?? fetch;
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
    });
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
    } = {},
  ): Promise<SearchResult> {
    const params: Record<string, unknown> = { top_k: opts.topK ?? 10 };
    if (query) params.query = query;
    if (opts.type) params.type = opts.type;
    if (opts.scope) params.scope = opts.scope;
    if (opts.tags?.length) params.tags = opts.tags.join(",");
    return this.get("/v1/search_artifacts", params) as Promise<SearchResult>;
  }

  async loadArtifact(id: string, version?: string): Promise<LoadedArtifact> {
    const params: Record<string, unknown> = { id };
    if (version) params.version = version;
    return this.get("/v1/load_artifact", params) as Promise<LoadedArtifact>;
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
    const resp = await this.fetcher(url.toString());
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
    const resp = await this.fetcher(url.toString());
    if (!resp.ok) {
      let envelope: Record<string, unknown> = {};
      try {
        envelope = (await resp.json()) as Record<string, unknown>;
      } catch {
        // ignore parse errors; fall through to generic error.
      }
      throw new RegistryError(
        (envelope.code as string) ?? "registry.unknown",
        (envelope.message as string) ?? `HTTP ${resp.status}`,
        Boolean(envelope.retryable),
      );
    }
    return resp.json();
  }
}
