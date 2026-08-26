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
  catalog: '/v1/catalog',
  loadArtifact: '/v1/load_artifact',
  dependents: '/v1/dependents',
  layers: '/v1/layers',
  quota: '/v1/quota',
} as const;

/** ApiError carries the §6.10 error envelope: the machine-readable code the
 * page branches on, the prose message, the retry signal, the optional
 * remediation hint, and the machine-readable details a code carries. A
 * surface that renders a refusal in the place the reader caused it reads the
 * details rather than parsing the message. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly suggestedAction: string;
  readonly details: Record<string, unknown>;

  constructor(
    status: number,
    code: string,
    message: string,
    retryable: boolean,
    suggestedAction: string,
    details: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.suggestedAction = suggestedAction;
    this.details = details;
  }
}

/** identityRefusalCodes are the §6.10 codes the identity middleware answers a
 * read with when it could not verify the caller. A token past its exp returns
 * auth.token_expired, one failing signature, iss, or aud returns
 * auth.untrusted_token, and a runtime-signed token the registry holds no key
 * for returns auth.untrusted_runtime. */
const identityRefusalCodes: ReadonlySet<string> = new Set<string>([
  'auth.token_expired',
  'auth.untrusted_token',
  'auth.untrusted_runtime',
]);

/** isIdentityRefusal reports whether a failed read was refused because the
 * caller's identity could not be verified, which is the arm the catalog-scope
 * rule orders ahead of the scope arms. It reads the code rather than the
 * status: the registry answers 401 on refusals that verified the caller
 * perfectly well, auth.tenant_unknown among them, and the catalog-scope rule
 * leaves every other failure to the surface's own error state. */
export function isIdentityRefusal(err: unknown): boolean {
  return err instanceof ApiError && identityRefusalCodes.has(err.code);
}

interface ErrorEnvelope {
  code?: string;
  message?: string;
  retryable?: boolean;
  suggested_action?: string;
  details?: Record<string, unknown>;
}

// §13.2.1 puts a read-only marker on the registry's responses, so the page
// can present that state before a write is attempted rather than collecting
// one refusal per button press.
const readOnlyHeader = 'X-Podium-Read-Only';

// The middleware that sets the marker wraps the meta-tool mux alone. The
// layer endpoints, the posture read, and the authentication routes are mounted
// beside it and carry no marker on any mode, so a response from one of them
// reports nothing about the mode. Reading a missing header there as "the
// registry serves writes" would clear the banner on every panel read and leave
// the write controls live on a read-only registry.
const readOnlyMarked: ReadonlySet<string> = new Set<string>([
  paths.loadDomain,
  paths.searchArtifacts,
  paths.loadArtifact,
  paths.dependents,
]);

type ReadOnlyListener = (readOnly: boolean) => void;

const readOnlyListeners = new Set<ReadOnlyListener>();

/** subscribeReadOnly registers a listener for the read-only marker and
 * returns the unsubscribe the caller runs when it goes away. */
export function subscribeReadOnly(listener: ReadOnlyListener): () => void {
  readOnlyListeners.add(listener);
  return () => {
    readOnlyListeners.delete(listener);
  };
}

// The marker middleware wraps the meta-tool mux from inside the identity
// verification and the tenant router, so a refusal from either is written
// before that middleware is entered and carries no marker whatever the mode
// is. A response that did not reach the middleware therefore reports nothing
// about the mode, and the marker is republished only from one that did.
function publishReadOnly(path: string, response: Response): void {
  if (!readOnlyMarked.has(path.split('?')[0]) || !response.ok) {
    return;
  }
  const readOnly = response.headers.get(readOnlyHeader) === 'true';
  for (const listener of readOnlyListeners) {
    listener(readOnly);
  }
}

/** unreachable is the ApiError a call that never reached the registry takes:
 * a rejected fetch, a connection dropped mid-response, a DNS failure, or a
 * page left open while the registry went away. The rejection carries a
 * JavaScript exception rather than a §6.10 envelope, and the exception's own
 * text names the browser's internal failure. Every surface renders a refusal
 * as a code and a sentence, so the transport failure is given the same
 * structure here rather than being passed to the surfaces as a bare
 * exception. It takes registry.unavailable, which is the code §6.10 defines
 * for a dependency that did not answer, and it is retryable because the
 * condition clears when the registry answers again. */
function unreachable(): ApiError {
  return new ApiError(
    0,
    'registry.unavailable',
    'The registry could not be reached from this browser.',
    true,
    'Check that the registry is running and that this page can still reach it, then try again.',
  );
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  let text: string;
  try {
    response = await fetch(path, init);
    publishReadOnly(path, response);
    text = await response.text();
  } catch {
    throw unreachable();
  }
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
    envelope.details ?? {},
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
  /** raw_frontmatter carries the pre-merge manifest document on a response
   * whose frontmatter was re-serialized with a hidden parent stripped
   * (§4.6). It is the only place the artifact's own `extends:` reference
   * survives such a response. */
  raw_frontmatter?: string;
  /** deprecated, replaced_by, and deprecation_warning carry the §4.7.4
   * lifecycle signal. The registry keeps serving a retired artifact and
   * reports the state alongside the bytes, and it resolves the state through
   * an `extends:` merge, so the viewer reads these rather than the
   * frontmatter pair of the same names. replaced_by is an artifact
   * identifier, which the viewer links to. */
  deprecated?: boolean;
  replaced_by?: string;
  deprecation_warning?: string;
}

/** DependencyEdge is one graph edge served by the dependents endpoint. */
export interface DependencyEdge {
  from: string;
  to: string;
  kind: string;
}

/** LayerRecord mirrors store.LayerConfig, which the registry marshals
 * directly into every layer response. The casing is that struct's, which is
 * not uniformly snake_case, so each name here is read off the Go field: a
 * member the struct tags carries the tagged name, and every other member
 * carries the Go field name the marshaller falls back to. */
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
  force_push_policy?: string;
  last_ingested_at?: string;
  LastIngestedRef?: string;
  /** DeletedAt is when the layer was unregistered. It is set on the records
   * the deleted read returns and absent on every other layer, and the
   * recovery surface derives the erase date from it. The field carries no
   * omitempty tag, so an active layer marshals it as null rather than
   * omitting it. */
  DeletedAt?: string | null;
}

/** IngestSummary is the §7.3.1 reingest result. The registry runs the whole
 * pipeline inside the request, so this is what the panel presents once the
 * request returns. */
export interface IngestSummary {
  layer?: string;
  accepted?: number;
  idempotent?: number;
  lint_failures?: number;
  conflicts?: IngestConflict[];
  rejected?: IngestRejection[];
  advisories?: IngestAdvisory[];
  artifacts?: IngestedArtifact[];
  /** queued marks the arm a registry with no ingest runner wired answers
   * with: the request is recorded and there is no summary to read. */
  queued?: string;
}

/** IngestedArtifact is one (artifact_id, version) pair the snapshot left in
 * the layer. The list covers both counts the report presents, so `status`
 * says whether the pair was newly accepted or matched what was already
 * stored. */
export interface IngestedArtifact {
  id: string;
  version: string;
  status?: string;
}

export interface IngestConflict {
  artifact_id: string;
  version: string;
  old_hash?: string;
  new_hash?: string;
  code: string;
}

export interface IngestRejection {
  artifact_id: string;
  code: string;
  reason: string;
}

export interface IngestAdvisory {
  artifact_id: string;
  code: string;
  severity: string;
  message: string;
}

/** SearchFilters are the filters §13.10 fixes for the UI: the ones the SDK
 * and the CLI carry. */
export interface SearchFilters {
  query: string;
  type: string;
  scope: string;
  tags: string[];
}

interface DomainRead {
  body: Promise<LoadDomainResponse>;
}

/** domainReads holds the `load_domain` answers issued for the route the reader
 * is on, keyed by the URL they were issued against. Two independent parts of
 * the shell read the same level of the §4.2 hierarchy on a domain route: the
 * panel reads the domain it renders, and the sidebar tree reads that same
 * node's level when the eager tree read did not carry it. The tree's read is
 * issued once the root read resolves, which is after the panel's request has
 * left, so sharing only what is still in flight would still cost two round
 * trips. The map is dropped whenever the reader enters a route and whenever a
 * write moves the catalog, so it never answers for a page the registry has
 * since changed under. */
const domainReads = new Map<string, DomainRead>();

/** invalidateDomainReads drops every held `load_domain` answer. The shell runs
 * it on entering a route and on a layer write, and the test suite runs it
 * between cases because the map outlives a render. */
export function invalidateDomainReads(): void {
  domainReads.clear();
}

export function loadDomain(path: string, depth?: number): Promise<LoadDomainResponse> {
  const url = paths.loadDomain + query({ path, depth });
  const held = domainReads.get(url);
  if (held !== undefined) {
    return held.body;
  }
  const body = request<LoadDomainResponse>(url);
  domainReads.set(url, { body });
  // A failed read is not held: the surfaces offer a retry of it, and a held
  // failure would answer that retry without reaching the registry.
  void body.catch(() => {
    if (domainReads.get(url)?.body === body) {
      domainReads.delete(url);
    }
  });
  return body;
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

/** catalogArtifactIDs reads the §4.5.2 catalog under a scope: the canonical ID
 * of every artifact the caller can see below it, in one response the registry
 * does not truncate. It is how a listing states an artifact count per child
 * without a scoped search behind every entry, because `load_domain` reports
 * the subtree and no count. */
export async function catalogArtifactIDs(scope: string): Promise<string[]> {
  const body = await request<{ ids?: string[] | null }>(paths.catalog + query({ scope }));
  return body.ids ?? [];
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

/** LayerRegistration is the register request body. user_defined chooses the
 * layer class, which decides both the authorization the §7.3.1 layer-write
 * rule applies to every later write and whether the registry reads the
 * visibility axes: it fixes a user-defined layer's visibility to the
 * registrant and discards what the request carries there. */
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

/** LayerSecretResult carries the one-time credential. A git source returns a
 * webhook URL and an HMAC secret on registration and on a secret rotation,
 * and those two responses are the only places the secret is returned. A
 * local-path source returns neither, and so does an update that requests no
 * rotation. */
export interface LayerSecretResult {
  layer: LayerRecord;
  webhook_url?: string;
  webhook_secret?: string;
}

export function registerLayer(body: LayerRegistration): Promise<LayerSecretResult> {
  return request<LayerSecretResult>(paths.layers, {
    ...write,
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
}

/** LayerUpdate is the partial patch the update endpoint honours. A field the
 * patch omits keeps its prior value, and the identifying fields (the tenant,
 * the layer ID, and the source type) are immutable. The visibility axes are
 * honoured on an admin-defined layer alone: §4.6 fixes a user-defined layer's
 * visibility at registration, so the registry ignores them there and still
 * answers success. Each axis grants and none revokes, which is the same
 * patch the CLI drives. */
export interface LayerUpdate {
  ref?: string;
  root?: string;
  local_path?: string;
  force_push_policy?: string;
  rotate_webhook_secret?: boolean;
  public?: boolean;
  organization?: boolean;
  groups?: string[];
  users?: string[];
}

/** updateLayer patches one layer. A rotation returns the fresh HMAC secret
 * once, on the same terms as registration, so the result carries the same
 * one-time credential fields. */
export function updateLayer(id: string, patch: LayerUpdate): Promise<LayerSecretResult> {
  return request<LayerSecretResult>(`${paths.layers}/update${query({ id })}`, {
    ...write,
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(patch),
  });
}

/** reorderLayers re-sequences the layers it names. The endpoint assigns each
 * named layer an absolute order value taken from its position in the array
 * rather than swapping two stored values, so the array is the resulting order
 * of the whole class block rather than a single move. The composition order
 * decides how an extending artifact merges with its parent, and a request
 * that named a subset would leave the rows it omitted holding order values
 * that tie or invert against the ones it rewrote. Each layer in the array is
 * authorized on its own under the §7.3.1 layer-write rule. */
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

/** BreakGlass is the §4.7.2 override a reingest carries to run inside a
 * freeze window. The registry refuses it without a justification, and the
 * freeze rule requires two distinct approvers, so the surface that offers the
 * override collects both before it sends one. */
export interface BreakGlass {
  justification: string;
  approvers: string[];
}

/** reingestLayer runs the ingest pipeline for one layer. The request carries
 * a body only where the caller is overriding a freeze window, because the
 * endpoint reads break_glass, justification, and approvers there and reads
 * nothing from the body otherwise. */
export function reingestLayer(id: string, breakGlass?: BreakGlass): Promise<IngestSummary> {
  const path = `${paths.layers}/reingest${query({ id })}`;
  if (breakGlass === undefined) {
    return request<IngestSummary>(path, { ...write, method: 'POST' });
  }
  return request<IngestSummary>(path, {
    ...write,
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      break_glass: true,
      justification: breakGlass.justification,
      approvers: breakGlass.approvers,
    }),
  });
}

export function unregisterLayer(id: string): Promise<unknown> {
  return request<unknown>(paths.layers + query({ id }), { ...write, method: 'DELETE' });
}

/** QuotaEnvelope is the §4.7.8 quota read. The limits are marshalled from
 * store.Quota, which carries no field tags, so each member is named after the
 * Go field. The account menu reads one of them, the per-identity cap on
 * user-defined layers. */
export interface QuotaEnvelope {
  tenant_id?: string;
  limits?: {
    MaxUserLayers?: number;
    StorageBytes?: number;
    SearchQPS?: number;
    MaterializeRate?: number;
    AuditVolumePerDay?: number;
  };
}

/** readQuota takes the §4.7.8 quota read. It is an ordinary read an SDK makes
 * against the same endpoint, and the registry gates it on no role. */
export function readQuota(): Promise<QuotaEnvelope> {
  return request<QuotaEnvelope>(paths.quota);
}

/** signOut issues the sign-out route as a POST, which is the method the route
 * answers. The path is the one the posture read reports, so no authentication
 * route path is spelled in this bundle. */
export function signOut(path: string): Promise<unknown> {
  return request<unknown>(path, { ...write, method: 'POST' });
}
