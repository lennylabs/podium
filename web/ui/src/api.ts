// The registry HTTP client. The UI holds no privileged access: every call
// here is the call an SDK would make against the same endpoint (§13.10), and
// the page is served from the registry's own origin, so every path is
// relative and no API base URL is configurable.

/** The registry paths the UI calls. No authentication route is spelled
 * here: the posture read reports the sign-in and sign-out paths, and the
 * bundle uses what it reports. */
export const paths = {
  session: '/v1/ui/session',
  loadDomain: '/v1/load_domain',
  searchArtifacts: '/v1/search_artifacts',
  loadArtifact: '/v1/load_artifact',
  dependents: '/v1/dependents',
  layers: '/v1/layers',
} as const;

/** ApiError carries the §6.10 error envelope: the machine-readable code the
 * page branches on, the prose message, the retry signal, and the optional
 * remediation hint. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly suggestedAction: string;

  constructor(status: number, code: string, message: string, retryable: boolean, suggestedAction: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.suggestedAction = suggestedAction;
  }
}

/** isIdentityRefusal reports whether a failed read was refused because the
 * caller's identity could not be verified. The identity middleware answers
 * 401 on that path, and the catalog-scope rule orders that arm ahead of the
 * scope arms, so the page renders the refused state rather than an empty or a
 * filtered catalog. */
export function isIdentityRefusal(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}

interface ErrorEnvelope {
  code?: string;
  message?: string;
  retryable?: boolean;
  suggested_action?: string;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  const text = await response.text();
  if (!response.ok) {
    throw errorFrom(response.status, text);
  }
  return (text === '' ? {} : JSON.parse(text)) as T;
}

function errorFrom(status: number, body: string): ApiError {
  let envelope: ErrorEnvelope = {};
  try {
    envelope = JSON.parse(body) as ErrorEnvelope;
  } catch {
    // A response that is not the §6.10 envelope carries no code, and the
    // page falls back to the status alone rather than showing raw bytes.
  }
  return new ApiError(
    status,
    envelope.code ?? 'registry.unavailable',
    envelope.message ?? `The registry answered ${status}.`,
    envelope.retryable ?? false,
    envelope.suggested_action ?? '',
  );
}

function query(params: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') {
      search.set(key, String(value));
    }
  }
  const encoded = search.toString();
  return encoded === '' ? '' : `?${encoded}`;
}

/** DomainDescriptor is one subdomain entry. It nests, because load_domain
 * expands more than one level. */
export interface DomainDescriptor {
  path: string;
  name: string;
  description?: string;
  keywords?: string[];
  subdomains?: DomainDescriptor[];
}

/** ArtifactDescriptor is one catalog entry. The domain browser and the
 * search surface receive the same structure, so one row renders both. */
export interface ArtifactDescriptor {
  id: string;
  type: string;
  version?: string;
  description?: string;
  tags?: string[];
  score?: number;
  sensitivity?: string;
  folded_from?: string;
  source?: string;
  summary?: string;
}

export interface LoadDomainResponse {
  path: string;
  description?: string;
  keywords?: string[];
  subdomains: DomainDescriptor[];
  notable: ArtifactDescriptor[];
  note?: string;
}

export interface SearchResponse {
  query?: string;
  total_matched: number;
  results?: ArtifactDescriptor[];
}

export interface LargeResourceLink {
  presigned_url: string;
  content_hash: string;
  size: number;
  content_type?: string;
}

export interface LoadArtifactResponse {
  id: string;
  type: string;
  version: string;
  content_hash: string;
  manifest_body: string;
  frontmatter: string;
  skill_raw?: string;
  layer?: string;
  sensitivity?: string;
  resources?: Record<string, string>;
  resources_base64?: boolean;
  large_resources?: Record<string, LargeResourceLink>;
  manifest_body_url?: LargeResourceLink;
}

/** DependencyEdge is one graph edge served by the dependents endpoint. */
export interface DependencyEdge {
  from: string;
  to: string;
  kind: string;
}

/** LayerRecord mirrors store.LayerConfig, which the registry marshals
 * directly into every layer response. The casing is that struct's, which is
 * not uniformly snake_case, so each name here is read off the Go field. */
export interface LayerRecord {
  ID: string;
  SourceType: string;
  Repo?: string;
  Ref?: string;
  Root?: string;
  LocalPath?: string;
  Order: number;
  UserDefined?: boolean;
  Owner?: string;
  Public?: boolean;
  Organization?: boolean;
  Groups?: string[] | null;
  Users?: string[] | null;
  LastIngestedAt?: string;
  LastIngestedRef?: string;
}

/** SearchFilters are the filters §13.10 fixes for the UI: the ones the SDK
 * and the CLI carry. */
export interface SearchFilters {
  query: string;
  type: string;
  scope: string;
  tags: string[];
}

export function loadDomain(path: string, depth?: number): Promise<LoadDomainResponse> {
  return request<LoadDomainResponse>(paths.loadDomain + query({ path, depth }));
}

export function searchArtifacts(filters: SearchFilters, topK?: number): Promise<SearchResponse> {
  return request<SearchResponse>(
    paths.searchArtifacts +
      query({
        query: filters.query,
        type: filters.type,
        scope: filters.scope,
        tags: filters.tags.join(','),
        top_k: topK,
      }),
  );
}

export function loadArtifact(id: string, version?: string): Promise<LoadArtifactResponse> {
  return request<LoadArtifactResponse>(paths.loadArtifact + query({ id, version }));
}

export async function dependentsOf(id: string): Promise<DependencyEdge[]> {
  const body = await request<{ edges?: DependencyEdge[] }>(paths.dependents + query({ id }));
  return body.edges ?? [];
}

export async function listLayers(): Promise<LayerRecord[]> {
  const body = await request<{ layers?: LayerRecord[] | null }>(paths.layers);
  return body.layers ?? [];
}

// Every write below is state-changing, so the §6.3.4 browser-origin gate
// reads it. The gate reads the browser's own Sec-Fetch-Site and Origin
// headers, so a same-origin request from this page carries the evidence the
// gate admits without a header of its own, and the session cookie travels
// with it because the request is same-origin.
const write: RequestInit = { credentials: 'same-origin' };

/** LayerRegistration is the register request body. Visibility is fixed at
 * registration for a user-defined layer, so the panel offers these values
 * here and nowhere else. */
export interface LayerRegistration {
  id: string;
  source_type: string;
  repo?: string;
  ref?: string;
  root?: string;
  local_path?: string;
  user_defined?: boolean;
  public?: boolean;
  organization?: boolean;
  groups?: string[];
  users?: string[];
}

/** LayerRegisterResult carries the one-time credential. A git source returns
 * a webhook URL and an HMAC secret, and this response and a secret rotation
 * are the only places the secret is returned. A local-path source returns
 * neither. */
export interface LayerRegisterResult {
  layer: LayerRecord;
  webhook_url?: string;
  webhook_secret?: string;
}

export function registerLayer(body: LayerRegistration): Promise<LayerRegisterResult> {
  return request<LayerRegisterResult>(paths.layers, {
    ...write,
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
}

/** reorderLayers re-sequences the whole list. The composition order decides
 * how an extending artifact merges with its parent, so the panel sends the
 * resulting order rather than a single move. */
export function reorderLayers(order: string[]): Promise<unknown> {
  return request<unknown>(`${paths.layers}/reorder`, {
    ...write,
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ order }),
  });
}

export function restoreLayer(id: string): Promise<unknown> {
  return request<unknown>(`${paths.layers}/restore${query({ id })}`, { ...write, method: 'POST' });
}

/** listDeletedLayers returns the layers still inside the recovery window. An
 * unregistered layer is soft-deleted rather than erased, so the panel has a
 * surface for what is still recoverable. */
export async function listDeletedLayers(): Promise<LayerRecord[]> {
  const body = await request<{ layers?: LayerRecord[] | null }>(paths.layers + query({ deleted: 'true' }));
  return body.layers ?? [];
}

export function reingestLayer(id: string): Promise<unknown> {
  return request<unknown>(`${paths.layers}/reingest${query({ id })}`, { ...write, method: 'POST' });
}

export function unregisterLayer(id: string): Promise<unknown> {
  return request<unknown>(paths.layers + query({ id }), { ...write, method: 'DELETE' });
}

/** signOut issues the sign-out route as a POST, which is the method the route
 * answers. The path is the one the posture read reports, so no authentication
 * route path is spelled in this bundle. */
export function signOut(path: string): Promise<unknown> {
  return request<unknown>(path, { ...write, method: 'POST' });
}
