# Critique of `podium.spec.md`

A structured review of the Podium technical specification, organized by perspective. Findings are tagged **[Critical]** (blocks a credible MVP), **[Significant]** (should be addressed before public release), or **[Minor]** (worth fixing for polish). Each finding carries an inline **_Fix:_** with a concrete proposed change.

---

## 0. Executive summary

The core ideas are sound:

- **Capability saturation is a real problem.** Lazy materialization is the right answer for catalogs that grow past a few hundred items.
- **"Author once, load anywhere"** via `HarnessAdapter` is a genuine differentiator vs harness-specific solutions (Claude Skills, Cursor Rules, Continue contexts).
- **Two-component split** (centralized registry + thin client-side MCP bridge) is the right shape and lets a single binary serve both managed runtimes and developer hosts.
- **Five-layer disclosure model** (modulo the labeling issue in §11 below) maps cleanly onto the three meta-tools.
- **RBAC / classification / lifecycle / overlays** are coherent enough to be implementable.

The biggest weaknesses, in priority order:

1. **The cold-reader on-ramp is too steep.** Opening jargon, the buried "two components" framing, and the absence of any concrete end-to-end walkthrough mean a reader needs many pages before the picture coheres.
2. **Versioning is under-specified across the stack.** Artifact version format, publish semantics, `latest` resolution, cache revalidation against immutable versions, pinning, and read/write consistency are missing or contradictory.
3. **Identity is hand-waved.** "OAuth-attested" appears dozens of times without specifying IdP contract, claim derivation (especially `team:<name>`), or how the registry trusts the runtime in `injected-session-token` mode.
4. **The local-overlay/search interaction is a real DX gap.** Search runs at the registry; the local overlay is merged client-side. A developer iterating on a local artifact cannot discover it via `search_artifacts`. The spec doesn't acknowledge this.
5. **No competitive positioning.** A reader cannot tell what Podium replaces, complements, or competes with.
6. **Authoring DX is undersold.** The spec is caller-centric; authoring tooling, the publish workflow's mechanics, IDE and CI integration are mostly absent.
7. **Enterprise governance is strong on RBAC and audit but weak on IdP/SSO/SCIM, data residency, retention, vulnerability management, and approval-workflow sophistication.**
8. **MCP fit is shallow.** The spec sits on MCP but doesn't engage with MCP's own primitives (resources, prompts, elicitation, roots, capabilities), and the naming collision between Podium's `tool` artifact type and MCP's "tool" concept will confuse every reader.

---

## 1. Ease of understanding (cold reader)

### Strengths

- §1.1 packages concrete value props in scannable bullets.
- §2.1's component diagram is genuinely clear.
- §3.2's "five layers" framing is a useful mental model once you're past the labeling issues.
- §1.4's decision/rationale table is a strong format.

### Weaknesses

- **[Significant]** The opening line — "enterprise registry for inference-time agentic AI artifacts" — packs three modifiers ("enterprise", "inference-time", "agentic") that all need unpacking before a reader knows what's being described. A reader without prior context cannot derive what _kind_ of system this is in one read.
  - _Fix:_ Replace the opening line with a plain-English lede: "Podium is a shared catalog for the things AI agents load at runtime — skills, agent definitions, prompts, reference docs, MCP server registrations. One canonical authoring format; each harness gets the layout it expects." Save "enterprise / inference-time / agentic" for paragraph two.
- **[Significant]** No motivating scenario or hello-world. A 10-line walkthrough — "Alice's agent calls `load_domain()`, picks `finance`, calls `search_artifacts("variance")`, calls `load_artifact("finance/close-reporting/run-variance-analysis")`, sees the manifest body and the materialized scripts under `/workspace/...`" — would do more for comprehension than the entire architecture section. The fragmentary frontmatter sketch in §4.3 isn't a runnable example.
  - _Fix:_ Add a §1.0 "A 90-second tour" with a 30-line example: a minimal `ARTIFACT.md`, the JSON-shape `load_artifact` response, and a tree-listing of what materializes on disk. Place before §1.1.
- **[Significant]** The "Two podium components" framing (line 24) is the most important sentence in the document and should be the second paragraph. It currently arrives after dense bullets describing properties of the system rather than its shape.
  - _Fix:_ Promote a one-line bold lede to the second paragraph of §1.1: "**Two pieces: a centralized registry (the system of record) and a thin client-side bridge (the MCP server callers run alongside their runtime).**" Move the existing "Two podium components" subsection up to immediately follow.
- **[Significant]** "MCP" is assumed knowledge throughout. A one-line definition (e.g., "Model Context Protocol — the wire protocol agents use to talk to tools and resources") is load-bearing and missing.
  - _Fix:_ On first MCP mention in §1.1, add a parenthetical: "(MCP = Model Context Protocol, the wire protocol agents use to talk to tools and resources; see <link>)". Add to glossary.
- **[Minor]** Section 3.2 is titled **"Five Layers of Disclosure"** but enumerates Layer 0 through Layer 5 — six layers. Beyond the count error, conflating gatekeeping (Layer 0), authoring quality (Layer 4), and feedback signals (Layer 5) with the actual disclosure surface (Layers 1–3) muddies the model. Consider renaming to "the disclosure surface" with three layers (browse / search / load) and treating scope filtering, description quality, and learn-from-usage as enabling concerns.
  - _Fix:_ Restructure §3.2 as "Three disclosure layers" (browse / search / load — matching the three meta-tools 1:1) with a sub-section "Three enabling concerns" (scope filtering, description quality, feedback reranking). Renumber and relabel throughout.
- **[Minor]** Terminology drifts:
  - "caller's runtime" vs "harness" — used overlapping but not synonymously.
  - "artifact" vs "package" vs "manifest" — related, often muddled.
  - "domain" vs "scope" vs "overlay" vs "tier" vs "layer" — five organizational concepts with overlapping names. (§4.7.1 uses "tiers"; §3.2 uses "layers"; §4.6 uses both.)
  - "profile" appears once in §6.7 ("profile and harness combinations") without being introduced.
  - _Fix:_ Add a Glossary appendix and do a global pass to canonicalize: **host** (replaces "caller's runtime" / "harness"); **artifact** (the conceptual unit), **package** (the on-disk directory), **manifest** (the `ARTIFACT.md` file specifically); **layer** (composition unit), **scope** (the OAuth claim that grants access to a layer), **overlay** (the contents authored under a scope) — drop "tier" entirely. Remove the lone "profile" reference or define it.
- **[Minor]** §2.1's component diagram shows static structure but no flow. A short sequence diagram for `load_artifact` (caller → MCP server → registry → object storage → MCP server adapter → caller filesystem) would orient readers far better than the static box diagram.
  - _Fix:_ Add a sequence diagram in §2.1 immediately after the box diagram, showing the seven-step flow: host → MCP server → registry control plane → object storage (presigned URL) → MCP server fetches → adapter translates → atomic write to host filesystem.
- **[Minor]** The frontmatter example in §4.3 mixes universal fields (`type`, `name`, `description`) with type-specific ones (`input`/`output` for `agent`; `delegates_to` for `agent`) in one block without visual separation. Splitting "common to all types" from "type-specific" sections would clarify which fields apply when.
  - _Fix:_ Split the §4.3 example into three labeled blocks: "Universal fields" (type, name, description, when_to_use, tags, sensitivity), "Caller-interpreted fields" (mcpServers, requiresApproval), and "Type-specific fields" (input/output for agent, delegates_to for agent, etc.). Each with a sentence framing.
- **[Minor]** No glossary. A spec at this scope should ship one.
  - _Fix:_ Add a Glossary appendix listing every load-bearing term once with a one-line definition.
- **[Minor]** No table of contents anchored to the headers; navigation in a 775-line document is hand-eye-bandwidth-bound.
  - _Fix:_ Add a TOC with anchored markdown links at the top of the spec, generated from headers §1 through §12.

---

## 2. Architectural / logical gaps

### Versioning (the biggest hole)

- **[Critical]** `load_artifact` takes "ID and version" (§5) but the version format is never specified. Semver? Monotonic integer? Content hash? The publish workflow doesn't say how a new version is created or whether the publisher chooses the version string.
  - _Fix:_ Adopt **semver** for the publisher-chosen version (e.g., `2.4.1`) plus an internal **content hash** as the durable cache key. Add a `version:` field to the §4.3 frontmatter schema; specify in §4.7 that the registry stores `(artifact_id, semver, content_hash)` triples; specify in §5 that `load_artifact` accepts `<id>` (resolves latest), `<id>@<semver>` (pinned), or `<id>@sha256:<hash>` (content-pinned).
- **[Critical]** "Each artifact version is immutable" (§4.7) but cache mode `always-revalidate` is described as `HEAD with If-None-Match` (§6.5). Against an immutable version, ETag revalidation is meaningless — there's nothing to revalidate. The intent is presumably "check if a _newer_ version exists for this ID," which is a different semantic. The cache-key dimension (ID? `(ID, version)`? `(ID, latest)`?) is unspecified.
  - _Fix:_ Clarify in §6.5 that revalidation applies to the _resolution_ `(id, "latest")` → `(id, semver)`, never to the manifest body. The body is keyed on content_hash and immutable. Cache stores `(id, "latest") → semver` with TTL (default 30s) and `(content_hash) → bytes` forever.
- **[Critical]** No `latest` semantic. Does `load_artifact("finance/foo")` resolve to "latest approved"? "Latest in caller's view"? Two callers in the same session that resolve "latest" milliseconds apart and get different versions — what's the consistency story? This is operationally critical and architecturally absent.
  - _Fix:_ Add §4.7.6 "Version resolution and consistency": `latest` = "the most recently approved version visible under the host's effective view, at resolution time." Resolution is registry-side. For session consistency, introduce an optional `session_id` arg on the meta-tools; the first `latest` lookup within a session is recorded and reused for all subsequent same-id lookups in that session.
- **[Significant]** No version pinning syntax shown in `extends:`, `delegates_to:`, or `mcpServers:`. If A `extends:` B and B is republished, does A inherit the new B silently? Diamond and cycle behavior in `extends:` chains is not specified.
  - _Fix:_ Specify in §4.3 that `extends:`, `delegates_to:`, and `mcpServers:` references support an optional `@<semver>` suffix. Default is `@latest` resolved at the _publishing_ artifact's publish time and stored as a hard pin in the published manifest's resolved form — so a parent's republish never silently changes a child. To pick up parent updates, republish the child. Cycle/diamond detection at publish time; multiple `extends:` parents not allowed in v1 (single scalar).
- **[Significant]** Bundled resources are described as "opaque versioned blobs" but the spec doesn't say whether they're content-addressed at storage (deduplicated across versions and artifacts) or per-(artifact-id, version) duplicated. The cache is content-addressed (§6.5); storage isn't.
  - _Fix:_ State explicitly in §4.7 that bundled resources are stored content-addressed by SHA-256 and deduplicated across all artifact versions within an org's storage namespace. Object-storage keys are `<org>/<sha256>`; manifest version records reference resources by hash.

### Identity, trust, and the IdP contract

- **[Critical]** The flow from "OAuth token" to scope claims `(org, [team:a, team:b], user:id)` is undocumented. OAuth tokens don't carry team membership natively. Where does that come from — a SCIM sync? An OIDC custom claim? A registry-side directory? Different choices have very different operational implications and trust boundaries.
  - _Fix:_ Add §6.3.1 "Claim derivation": the IdP returns a JWT with claims `{sub, org_id, email}`; team membership is resolved registry-side via SCIM 2.0 push from the IdP (the registry maintains a directory of `(user, teams)`). For IdPs without SCIM, an `IdpGroupMapping` adapter can read OIDC group claims and map to team names. Specify the claim shape and the fallback path.
- **[Critical]** `injected-session-token` mode says "the runtime brokers identity centrally" (§6.3), but the registry has no specified mechanism to verify that a token came from a trusted runtime rather than from any process that can write the env var. If the runtime mints arbitrary tokens, the entire RBAC model collapses to "trust the runtime." If the registry validates a signed claim from the runtime, that signing protocol needs to be specified.
  - _Fix:_ Add §6.3.2 "Runtime trust model": the injected token is a JWT signed by a runtime-specific signing key registered with the registry one-time at runtime onboarding. The registry verifies the signature on every call. Token must include `iss` (runtime identifier), `aud` (registry endpoint), `sub` (user id), `exp`, and `act` (actor — the user the runtime is acting on behalf of). Without a registered signing key, the registry rejects with `auth.untrusted_runtime`.
- **[Significant]** The OAuth protocol is unnamed. OIDC? RFC 8693 token exchange? Custom JWT? Real enterprises will need to know which IdPs (Okta, Entra ID, Auth0, Google Workspace) are supported and how.
  - _Fix:_ Specify OIDC as the primary protocol. Tested IdPs: Okta, Entra ID, Auth0, Google Workspace, Keycloak. SAML supported via OIDC bridge. Document in §6.3.
- **[Significant]** Token lifetime, refresh model, and revocation aren't specified. For long-lived agent sessions, this matters.
  - _Fix:_ Specify defaults: access-token TTL 15 min; refresh-token TTL 7 days; revocation via a registry-side blocklist with ≤60s propagation (cached in MCP server); device-code flow re-prompts on refresh-token expiry. Add to §6.3.
- **[Significant]** No fine-grained token scoping. A user's token grants visibility into their entire effective view. There's no way to mint "this token can only see `customer-support` artifacts" — useful for narrow integrations and for limiting blast radius.
  - _Fix:_ Add support for OAuth scope claims like `podium:read:finance/*` or `podium:load:finance/ap/pay-invoice@1.x`. Tokens with narrow scopes intersect with RBAC bindings; the smaller surface wins. Document in §6.3 and reflect in §4.7.2 RBAC evaluation.

### Local overlay × search × load_domain

- **[Critical]** §4.6 / §6.4 explicitly say layers 1–3 (org / team / user) resolve at the registry on every call; layer 4 (local) is merged client-side by the MCP server. Implication: **`search_artifacts` cannot find local-overlay artifacts** because the registry's index doesn't see them. A developer iterating on a local skill cannot test "will my agent discover this via search?" until they publish. This is a real DX-blocking gap and isn't acknowledged anywhere.
  - _Fix:_ Add §6.4.1 "Local search index": when `LocalOverlayProvider` is configured, the MCP server maintains a local BM25 index over local-overlay manifest text. `search_artifacts` calls fan out to both the registry and the local index, and the MCP server fuses results via reciprocal rank fusion before returning. Embeddings for the local index are out of scope for v1 (BM25-only); document the trade-off (lower recall on semantic queries against local artifacts, acceptable for the dev iteration loop).
- **[Significant]** `load_domain` results returned by the registry don't include local items either. The spec implies the MCP server post-merges them, but the merge mechanics — does a `local` `DOMAIN.md` `include:` glob resolve against the merged view or only the local view? — are not specified.
  - _Fix:_ Specify in §4.5.4 that the MCP server merges local-`DOMAIN.md` results into registry-returned domain maps post-hoc, after the registry returns. Globs in a local `DOMAIN.md` resolve against the merged (registry + local) view; globs in a remote `DOMAIN.md` resolve only against the registry view. Document the asymmetry explicitly with an example.
- **[Significant]** A local `DOMAIN.md` can shadow a remote one (per §4.5.4 merge rules), but the merge happens client-side. So two developers in the same workspace with different local overlays effectively see different `DOMAIN.md` resolutions, which is correct but confusing in shared contexts.
  - _Fix:_ Document the by-design behavior in §4.6 with an explicit "shared workspaces" warning: avoid local `DOMAIN.md` changes for paths that the team relies on; promote to user/team overlay before sharing.

### `extends:` and merge semantics

- **[Significant]** §4.6 specifies merge for `description`, prose, tags, security fields, allowlists/denylists. It does _not_ specify:
  - Multiple inheritance (can a manifest `extends:` two parents?). The frontmatter shows a scalar but doesn't forbid a list.
  - Cycle detection in `extends:` chains.
  - How `extends:` interacts with version pinning of the parent.
  - How user-extension caller-interpreted fields (`mcpServers:` lists, `requiresApproval:` lists) merge — list-append? list-replace? deep-merge?
  - _Fix:_ Specify in §4.6: `extends:` is a single scalar in v1 (no multiple inheritance). Cycle detection at publish time (rejected as `extends.cycle`). Parent version is resolved at child publish time and pinned. Merge defaults: scalar fields → child wins; list fields → append unless explicitly marked `merge: replace` in a child sentinel block; map fields → deep-merge with child wins on key collisions.
- **[Minor]** "Allowlists are intersected; denylists are unioned" — but the spec doesn't enumerate which fields are allowlists vs denylists. Without a registry of field semantics, lint can't enforce this.
  - _Fix:_ Ship a field-semantics table in §4.6 enumerating each known field and its merge classification (scalar / append-list / replace-list / allowlist / denylist / map). Extension types register their own field semantics via the `TypeProvider` SPI.

### Reverse dependency index

- **[Significant]** §4.7.3 lists "tag-based associations" as a dependency edge. Tags are a non-rigorous taxonomy; treating tag co-occurrence as an "X depends on Y" edge will produce a noisy index that's not safe for impact analysis or cascading review. If "deprecating B will warn dependents A" includes "A and B happen to share a tag," the warnings are meaningless and will be ignored.
  - _Fix:_ Drop "tag-based associations" from the dependency edge list in §4.7.3. Limit edges to explicit references: `extends:`, `delegates_to:`, and `mcpServers:` (where the referenced server has a registered `mcp-server`-type artifact — see naming-fix below). Tags continue to drive discovery and search ranking but do not create dependency edges.
- **[Significant]** `mcpServers:` references in artifact frontmatter are typically external server identifiers (npx package, URL). The reverse index can only follow these to `tool` artifacts if there's an explicit registration mapping. The spec implies this works automatically; it doesn't say how.
  - _Fix:_ Specify that `mcp-server`-type artifacts (renamed from `tool` per the MCP-fit section) declare a `server_identifier:` field naming the canonical npx package, URL, or command. The reverse index walks `mcpServers:` entries in other artifacts and matches against `server_identifier:` to resolve dependency edges. Document in §4.7.3.
- **[Minor]** `delegates_to:` is "well-known" — but well-known to whom? Is it a free-form list of artifact IDs, or constrained to `agent`-type artifacts? The spec says "type-specific runtime behaviour lives in callers" but the reverse index requires registry-side interpretation.
  - _Fix:_ Constrain `delegates_to:` entries to `agent`-type artifact IDs; lint enforces at publish time. Document in §4.3.

### Search-rank learning (Layer 5)

- **[Significant]** "The registry observes which artifacts actually get loaded after which queries" requires correlating `search_artifacts` calls with subsequent `load_artifact` calls. This needs a session/correlation primitive that the spec doesn't introduce. Naive correlation by `(identity, time-window)` is leaky and noisy. A non-trivial product feature is treated as a footnote.
  - _Fix:_ Introduce an optional `session_id` argument on `load_domain`, `search_artifacts`, and `load_artifact` (UUID generated by the host per agent session). The registry correlates within a session for Layer 5 reranking. Without `session_id`, no reranking signal is collected. Document in §5 and §3.2 (Layer 5 / enabling concern).
- **[Minor]** Privacy implication: per-caller query histories are valuable feedback but also sensitive. Audit retention and query-text retention need separate policies.
  - _Fix:_ Add §8.4 retention policy distinguishing event metadata (default 1 year) from query text (default redacted after 7 days, deleted after 30). Configurable per deployment.

### State and the "stateless" claim

- **[Significant]** §6.1 declares the MCP server "stateless. Holds no credentials of its own." But the same component:
  - Maintains a content-addressed disk cache (§6.5).
  - Stores OAuth tokens in the OS keychain (`oauth-device-code` mode).
  - Runs an fsnotify watcher with an in-memory index of the local overlay.
  - Writes a local audit log.
  - Materializes a working set on disk.
  - Caches presigned URLs.

  "Stateless" is misleading. The intended meaning seems to be "no server-side per-session state" — but the categorical claim contradicts most of §6. Reword to "no server-side session state; client-side cache and credentials only."
  - _Fix:_ Replace the §6.1 wording: "The podium MCP server holds no per-session server-side state. Local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes."

### Resource handling

- **[Significant]** "Bundled resources alongside the manifest are arbitrary files" — and `model files` and `datasets` are listed as supported types — but individual files are capped at "~1 MB as a soft cap" with "~10 MB total per package." Real model files and datasets exceed this routinely. The "higher-cap deployment configuration" workaround is hand-waved; without a clear path, a deployment that wants to ship 100 MB ONNX models has to reverse-engineer the cap surface.
  - _Fix:_ Introduce an "external resource" mechanism: a manifest can declare resources via `external_resources:` referencing pre-uploaded object-storage URLs with content hashes and signatures. The registry stores the URL+hash+size+signature, not the bytes; caps don't apply. Bundled bytes remain capped (1 MB / 10 MB defaults). Models and large datasets use the external mechanism. Document in §4.4.
- **[Significant]** Presigned URL expiry, retry on expiry, and signing TTL are not specified.
  - _Fix:_ Specify in §6.6 and §7.2: presigned URLs default to 60-minute TTL. On 403/expired during fetch, the MCP server retries by re-calling `load_artifact` for a fresh URL set (max 3 retries, exponential backoff). TTL is configurable via `PODIUM_PRESIGN_TTL_SECONDS`.
- **[Minor]** The publish-time linter validates that prose references resolve to bundled files (§4.4). What about references to URLs? To other artifacts? To frontmatter fields?
  - _Fix:_ Extend the prose-reference linter (§4.4) to resolve three reference kinds: bundled files (existence check), URLs (HTTP HEAD + 200/3xx), and artifact references (registry-side resolution). Frontmatter field references are out of scope.

### Workflow type

- **[Significant]** `workflow` is defined as "ordered, multi-step procedures that orchestrate skills or agents" but the spec gives no schema for workflow content, no example, no execution model. As specified, it's just a tag — the registry has no orchestration model and the type promises capabilities it doesn't deliver.
  - _Fix:_ For v1, demote `workflow` from first-class to extension type and remove from §4.1's first-class list. Re-introduce in v1.1 with a specified schema (`steps: [{call: <artifact_ref>, args: {…}}]`) and an execution-model contract (interpretation by host runtime, not by registry).

### Concurrency

- **[Significant]** No discussion of write conflicts on publish. Two publishers update the same artifact concurrently — CAS? ETag? Last-write-wins?
  - _Fix:_ Specify CAS via `if-match: <previous-version-content-hash>` on publish; conflict returns HTTP 412 with structured error `publish.conflict`. Document in §4.7.
- **[Minor]** Read during write semantics are presumably safe by virtue of immutable versions, but the invariant should be made explicit.
  - _Fix:_ Add a §4.7 "Version immutability invariant": "A `(artifact_id, version)` pair, once published, is bit-for-bit immutable forever. Readers in flight when a republish occurs continue to see their pinned version." Make this a load-bearing system invariant.

### Tenancy

- **[Significant]** Org isolation in Postgres is unspecified. Row-level security? Schema-per-org? Database-per-org? This affects multi-tenant security guarantees, the blast radius of SQL injection, and query patterns. For a multi-tenant centralized service, this is a top-three architectural decision.
  - _Fix:_ Specify **schema-per-org** with row-level checks on cross-org metadata tables. Schema-per-org gives clean drop-org semantics, isolates query patterns, and bounds blast radius. Document in §4.7.1 with the alternatives considered.
- **[Significant]** Team-name uniqueness scope: are team names global within an org? Are orgs identified by name or UUID? Can two orgs both have a `team:finance`?
  - _Fix:_ Specify that org IDs are UUIDs (with human-readable aliases for display); team names are unique within an org; the canonical scope claim form in tokens is `team:<org_id>/<team_name>`. Document in §4.7.1.

### Type system extensibility

- **[Significant]** "Type definitions are themselves versioned in the registry" (§12) but the type-definition schema isn't specified, the registration mechanism isn't specified, and §9's pluggable-interface table doesn't list a `TypeProvider`. Either type extension is via SPI plugin (and §9 should list it) or via runtime-registered schemas (and §4 should specify the schema). Currently it's neither.
  - _Fix:_ Add `TypeProvider` to §9 pluggable interfaces. A type definition is `(name, frontmatter_json_schema, lint_rules, optional adapter_hints)`. Type definitions are SPI plugins compiled into the registry binary; deployments can register additional types at build time. Runtime-registered types are out of scope for v1; remove the §12 risk-row claim that "type definitions are versioned in the registry" or rewrite to match the SPI model.

### Materialization

- **[Minor]** "Common conventions: `/workspace/current/<artifact-id>/<path>` for sandboxed runtimes" — hard-coded path conventions in a "designed for many harnesses" system suggest the abstraction is leaky. Why is this a convention, and what enforces it?
  - _Fix:_ Remove the `/workspace/current/` convention from §6.6. Materialization destination is purely caller-supplied via `PODIUM_MATERIALIZE_ROOT` or the per-call argument. Replace the example with neutral phrasing: "callers choose a destination root that fits their runtime's conventions."
- **[Minor]** Cache modes (`always-revalidate`, `offline-first`, `offline-only`) — set how? Per-call? Per-server? Per-call enables testing scenarios; per-server is operational. Not specified.
  - _Fix:_ Specify cache mode is server-startup config only via `PODIUM_CACHE_MODE`. Per-call override is out of scope for v1; document as future work.
- **[Minor]** Adapter output is "regenerated on each materialization" (§6.7). For artifacts loaded many times this wastes CPU; the trade-off ("avoids per-(artifact, harness) cache duplication") deserves more substance — adapter cost vs cache cost vs cache invalidation complexity.
  - _Fix:_ Keep regeneration as the default but add an optional in-memory memo cache keyed on `(content_hash, harness)` with a 5-minute TTL for repeated loads in the same session. Document the trade-off in §6.7.

### Error model

- **[Minor]** Errors are described as "structured" (§6.9) but the structure isn't specified. Field set? MCP error-code mapping? This needs a schema.
  - _Fix:_ Add §6.10 "Error model" specifying the envelope: `{code: string, message: string, details?: object, retryable: bool, suggested_action?: string}`. Codes are namespaced (`auth.*`, `rbac.*`, `publish.*`, `materialize.*`, etc.). Map to MCP `error` payloads per the MCP spec.

### Registry API protocol

- **[Minor]** §2.2 says the registry's API is "MCP/HTTP" — these are different protocols. Does the registry expose MCP for direct use (so a sufficiently-clever caller could skip the local MCP server)? Or is HTTP the wire and the MCP server translates? The slash phrasing obscures the actual contract.
  - _Fix:_ Clarify in §2.2 that the registry's wire protocol is **HTTP/JSON** (REST). The "MCP" surface is what the client-side MCP server exposes after translating registry HTTP responses into MCP tool results. Direct MCP access to the registry is not supported in v1.

---

## 3. Competitive landscape

The spec contains **zero** competitive analysis. A reader cannot tell what Podium replaces, complements, or competes with. For a project pitching itself to enterprises and to OSS adopters, this is a major omission. Adjacent prior art the spec should engage with:

- **Anthropic Skills (Claude Code, Claude Desktop).** Podium's `claude-code` adapter materializes to `.claude/agents/<name>.md` — i.e., Podium is positioned as a layer above Skills. The spec should say what Podium does that the native Skills system doesn't, and why an organization wouldn't just use Skills with Git.
- **MCP server marketplaces** (multiple emerging from major vendors). Podium's `tool` artifact type wraps `mcpServers:` declarations — why a parallel system to MCP's own discovery primitives?
- **LangChain Hub / LangSmith.** Direct competitor for `prompt` artifacts, with versioning, evaluation, observability.
- **PromptLayer / Langfuse / Helicone.** Prompt registry + observability.
- **HuggingFace Hub.** Direct competitor if the type system is extended to `model` and `dataset` (which the spec invites).
- **Cursor Rules, Continue.dev contexts, Cline, Aider.** Per-tool conventions Podium implicitly competes with for author mindshare.
- **Git monorepo + a thin CLI.** The lowest-tech competitor. Many teams already do this. The spec doesn't articulate why a Postgres-backed service is needed over a Git-of-truth model. "Authoring lives in Git" + "the runtime is a service" — what does the runtime add over GitHub itself, which already has search, RBAC, versioning, audit?

_Fix (covers all of the above):_ Add §1.5 "Where Podium fits" — a one-page comparison matrix with the alternatives above as columns and rows for: scope (single-harness vs cross-harness), discovery model (flat vs hierarchical vs lazy), governance (none / Git / RBAC / lifecycle), runtime contract (file conventions vs API), and "when to choose Podium." Be honest about cases where the alternative wins.

Differentiation gaps:

- **[Significant]** "Author once, load anywhere" via `HarnessAdapter` is the strongest differentiator and should be the lead positioning, not the third bullet.
  - _Fix:_ Promote to the first bullet of §1.1 and rewrite the elevator pitch around it: "Author your skills, agents, and prompts once in a canonical format; load them into any harness — Claude Code, Cursor, Codex, OpenCode — without forking per-harness."
- **[Significant]** Progressive disclosure addresses a real failure mode (capability saturation). The spec should quantify it — at how many artifacts does the baseline (everything in the system prompt) collapse? Naming the baseline gives the disclosure model teeth.
  - _Fix:_ Cite measured numbers in §3.1 (e.g., "tool-call accuracy degrades sharply past ~50 tools in the system prompt; teams in private preview run catalogs of 1–5K artifacts"). Use whatever public numbers exist; if none, run a baseline experiment and cite it.
- **[Significant]** Enterprise framing (RBAC, audit, classification) is a real moat vs lighter-weight options, but it shifts the buyer. The spec doesn't reconcile the enterprise pitch with an OSS adoption strategy.
  - _Fix:_ Add §1.6 "Project model": Apache 2.0 license, OSS-first development, optional commercial managed offering. State the buyer/adopter for each tier (individuals → small teams → enterprises) and what they get from each.

Positioning ambiguity:

- **[Significant]** No mention of pricing, hosted variant, commercial relationship to a sponsoring entity. For an "enterprise" product, this is the first question a buyer asks. For an OSS project, it shapes contribution patterns.
  - _Fix:_ Out of scope for the tech spec, but reference §1.6 "Project model" and link to a separate `BUSINESS-MODEL.md` if a managed offering exists.
- **[Minor]** No reference to standards bodies or the broader MCP working group. If Podium's canonical artifact format is intended to influence broader standards, say so; otherwise it's one more proprietary format to adopt.
  - _Fix:_ In §1.5, add a one-line statement of intent: "The canonical artifact format is intended for upstream contribution to an MCP-adjacent standard; until then, it is specified here." If no upstream intent, say "Podium ships its own format and does not seek standardization."

---

## 4. Usefulness / problem-solution fit

### Strong

- The capability-saturation problem is real and well-articulated.
- Lazy materialization is the right answer.
- HarnessAdapter solves real fragmentation pain.
- RBAC + classification + lifecycle answer real enterprise needs.

### Soft spots

- **[Significant]** **Most teams don't have 1000+ artifacts.** Progressive disclosure is solving a problem most adopters won't have for years. The spec should distinguish "problems Podium solves at any scale" from "problems Podium solves at scale," and should quantify the breakeven (50 artifacts? 500? 5000?). A small team will look at the depth of the spec and conclude "overkill."
  - _Fix:_ Add §1.3.1 "When you need Podium": below ~50 artifacts a flat directory + harness-native conventions is fine; 50–500 you start to feel the disclosure pain; above 500 Podium pays for itself outright. Cross-harness portability is valuable at any scale; governance is valuable above ~10 contributors. Be honest about the breakeven.
- **[Significant]** **"Author once, load anywhere" is partial.** Adapters can only translate features the target supports. An author who relies on a Claude-Code-specific feature (e.g., subagent declarations) will silently get a degraded materialization in Codex. The spec acknowledges this with "the adapter's job is mechanical translation, not interpretation" — but doesn't make the cost to the author explicit. In practice, authors must constrain themselves to a lowest-common-denominator feature set, or accept per-harness silent failures. This deserves an entire subsection on the author's burden.
  - _Fix:_ Add §6.7.1 "The author's burden": adapters translate only what the target supports; authors should write to a documented "core feature set" (enumerated in the spec) or accept per-harness degradation. Publish-time lint surfaces capability mismatches. Ship a per-harness compatibility matrix in the spec.
- **[Significant]** **Per-call network round-trip** is acknowledged (§7.1) but understated. For agents that browse the catalog speculatively (which good agents will), this is many round-trips per turn. Latency budget needs explicit numbers — e.g., "p99 < 200 ms for `load_domain`" — and a story for what happens when the network is bad.
  - _Fix:_ Add explicit p99 budgets in §7.1: 200ms for `load_domain` and `search_artifacts`; 500ms for `load_artifact` (manifest only); 2s for `load_artifact` with up to 10MB cached resources. Add a §7.4 "Degraded network" describing the offline-first cache mode, retry behavior, and what the host sees on registry timeout.
- **[Significant]** **MCP-only.** Many AI runtimes don't speak MCP (LangGraph, OpenAI Assistants API, Gemini Apps Functions, Bedrock Agents). Podium's reach is limited to MCP-speaking callers. Either non-MCP integration is on the roadmap (and should be flagged) or the spec should own this constraint as a deliberate limit.
  - _Fix:_ Own the limit explicitly in §1.4 ("MCP-only in v1"). Add a roadmap note in §10: thin language-specific SDKs (Python, TypeScript) wrap the same registry HTTP surface for non-MCP runtimes; planned for v1.1.
- **[Minor]** The "any AI artifact type" promise is broad enough to be brittle. Type heterogeneity is a strength but the spec doesn't say _which_ types it commits to (with first-class lint and adapter support) vs which are nominal extensions.
  - _Fix:_ In §4.1, distinguish "first-class types" (skill, agent, context, prompt — full lint, conformance suite, broad adapter coverage) from "registered extension types" (mcp-server, dataset, model, eval, etc. — schema only, no conformance commitment). Set adopter expectations.

---

## 5. Developer experience

The spec is overwhelmingly caller-centric. Authors are the daily users of any registry — their iteration loop, publish workflow, and tooling are the most consequential DX surfaces, and they're underdeveloped here.

### Author iteration loop

- **[Significant]** How does an author preview their artifact before publishing? The local overlay enables in-workspace iteration but **search doesn't see local artifacts** (per §2 above) — so the most common test ("will my agent find this when I ask for X?") cannot be performed without publishing.
  - _Fix:_ Same fix as §2.local-overlay-search above (ship a client-side BM25 index in the MCP server).
- **[Significant]** No `podium lint` for local validation against the publish-time rules.
  - _Fix:_ Add `podium lint <path>` to the CLI in §10 phase 12; runs the publish-time linter locally with no registry connectivity required. `podium lint --strict` adds optional checks (security scan, capability mismatch).
- **[Significant]** No `podium dry-run` or `podium materialize --harness=cursor` to inspect adapter output without invoking the agent.
  - _Fix:_ Add `podium materialize --harness=<value> --out=<dir> <artifact-path>` to the CLI. Useful for previewing adapter output and for build pipelines that produce harness-specific bundles.
- **[Significant]** No `podium test` or fixture-suite for authors. Adapters get a conformance suite (§6.7); authors should have analogous tooling for "does my skill actually work with my agent."
  - _Fix:_ Add `podium test <artifact>` that runs the artifact against an artifact-supplied fixture suite (`test/` subdirectory in the package). Deferred to v1.1; mention in §10 as future work.

### Publish workflow

- **[Significant]** §10 mentions `podium publish` but doesn't describe its inputs (a directory? a Git ref? a tarball uploaded to the API?), outputs, or error model.
  - _Fix:_ Specify in §7.3.1: `podium publish [<path>]` packages the directory's contents and uploads to the registry's HTTP API. Inputs: directory; outputs: `(artifact_id, version, status)`; errors via the structured error model (lint failures, RBAC denials, CAS conflict).
- **[Significant]** "Authoring lives in Git" + the registry is a service. Does `podium publish` push from a working tree to the API? Does the registry pull from Git on a webhook? Both? The spec doesn't pick one and the choice is consequential — push gives you a clean separation of source and runtime; pull gives you Git as the authoritative trigger.
  - _Fix:_ Pick **push** for v1 (`podium publish` from CI or developer host). Pull-from-Git is post-v1 future work; document in §10 as roadmap.
- **[Significant]** Draft → review → approved is a lifecycle (§4.7.4). How does a publisher initiate a review? Tag a reviewer? Comment? Reject and revise? The workflow primitives are unspecified — the spec assumes a UI/flow that isn't described.
  - _Fix:_ Add §4.7.4.1 "Review workflow primitives": `podium publish --draft` creates a draft; `podium review queue` lists pending reviews for the caller's reviewer scope; `podium review {approve,reject,changes-requested} <id> [--comment <text>]` actions a draft. Comments stored on the artifact's review record. UI-agnostic; clients can build on top.
- **[Minor]** No "published version metadata" surface (changelog, release notes, migration hints). Useful for downstream consumers.
  - _Fix:_ Add `release_notes:` field to the manifest frontmatter; the registry surfaces it in `load_artifact` responses and in `podium info <id>`.

### CI integration

- **[Significant]** Build pipelines are listed as a caller, but a CI use-case for _publishing_ (publish on merge, lint on PR) isn't specified. This is the first thing every team will want.
  - _Fix:_ Ship a reference GitHub Actions workflow in the example registry (§10 phase 13) that runs `podium lint` on PRs and `podium publish` on merge. Include in the spec as an appendix or link.
- **[Minor]** GitHub/GitLab/CircleCI integrations? Webhook contract? Not mentioned.
  - _Fix:_ Add §7.3.2 "Outbound webhooks": registry emits webhooks for publish, review-state-change, and deprecation events. Schema in spec; receivers can be configured per org.

### IDE integration

- **[Significant]** ARTIFACT.md is markdown with YAML frontmatter. Authors will want a JSON Schema for frontmatter completion in VS Code, error squiggles for invalid `type` fields, and a local lint that mirrors publish-time rules. Not mentioned.
  - _Fix:_ Ship `podium-frontmatter.schema.json` with the v1.0 release. Document a one-line VS Code config (`yaml.schemas` mapping) in §10/§13. Consider a Podium VS Code extension as future work.

### Debugging discoverability

- **[Significant]** "My skill isn't being suggested for the obvious query" — what tooling helps? Hybrid search ranking is opaque. There's no `podium why` or `podium search-explain` mentioned. Layer 5 reranking deepens the black box rather than illuminating it for authors.
  - _Fix:_ Add `podium search-explain "<query>" --artifact <id>` to the CLI. Returns the BM25 score, embedding cosine, RRF fused score, and any rerank deltas for the query/artifact pair. Useful for authors debugging discoverability.

### Multi-workspace overlay

- **[Minor]** One MCP server per workspace. If a developer has two related workspaces (a fork and the main), local overlays don't compose across them. Common case, not addressed.
  - _Fix:_ Out of scope for v1; document the limitation in §6.4. Stretch: support `PODIUM_OVERLAY_PATH=path1:path2` for multi-path local overlay (post-v1).

### Bundled scripts execution model

- **[Significant]** A skill bundles `scripts/variance.py`. Where does it execute? Under what permissions? Who installs Python? The spec defers to "the caller's runtime" (§4.4) — but for an author, this is a critical surface. Different harnesses execute scripts very differently (or not at all). Without an execution-model contract, "Author once" can't deliver here.
  - _Fix:_ Add §4.4.1 "Execution model contract": the MCP server materializes scripts; the host's runtime executes them. Authors declare runtime expectations in `runtime_requirements:` frontmatter (e.g., `python: ">=3.10"`, `node: ">=20"`, `system_packages: [...]`). Adapters surface these requirements to the host where supported. Hosts that cannot satisfy a requirement reject the artifact at load time with a structured error.

### `extends:` cache semantics

- **[Minor]** An artifact `extends:` another. If the parent updates, does the child see the change immediately? Cache invalidation through extends chains is unspecified.
  - _Fix:_ Same as §2.versioning above — `extends:` resolves at the child's publish time and pins to a specific parent semver. Parent updates don't propagate; republish the child to pick up changes.

### Onboarding friction

- **[Significant]** A new author wants to know: what's the smallest valid ARTIFACT.md, how do I publish it, how do I see it in my agent? The spec has no quickstart.
  - _Fix:_ Add §1.0 "Quickstart": minimal `ARTIFACT.md` (5 lines), `podium publish`, agent's MCP server is configured, agent calls `load_artifact` and sees output. ~30 lines including code blocks. Place before §1.1.

### Caller integration friction

- **[Significant]** A caller wants to wire up the MCP server. Where do the meta-tool descriptions go in the system prompt? How are they framed for the LLM? The descriptions of `load_domain` / `search_artifacts` / `load_artifact` are the entire interface the agent learns — they're load-bearing, and they're not in the spec. The spec leaves this entirely to the caller, but in practice this is the most consequential prompt-engineering decision in the system.
  - _Fix:_ Add §5.1 "Meta-tool descriptions and prompting guidance": ship the canonical tool descriptions (the strings the host exposes to the LLM via MCP) verbatim in the spec, plus prompting guidance on when to expose them, how to phrase the meta-tool intro to the model, and example system-prompt fragments for a few representative hosts.
- **[Minor]** No example system-prompt fragment for the meta-tools.
  - _Fix:_ Include 1-2 example fragments in §5.1 (above), e.g., "You have access to a Podium catalog. To explore: call `load_domain`. To find a specific artifact: call `search_artifacts`. To use one: call `load_artifact`. Only loaded artifacts contribute to your context."

---

## 6. Enterprise governance & security

### Governance — strong

- RBAC model (reader/publisher/reviewer/owner/admin) is sound.
- Sensitivity labels (low/medium/high) → review policy mapping is sound.
- Comprehensive audit event coverage (§8.1).
- `replaced_by:` for deprecation paths is the right primitive.

### Governance — gaps

- **[Critical]** **No SSO / SCIM / IdP integration model.** "OAuth-attested" is too vague. Real enterprises ask: OIDC-compatible? SAML? Specific IdPs (Okta, Entra ID, Google Workspace, Auth0)? Group-to-team mapping (SCIM 2.0)? Just-in-time provisioning?
  - _Fix:_ Specify OIDC + SCIM 2.0 as the primary integration model in §6.3 and §4.7.1. List tested IdPs (Okta, Entra ID, Auth0, Google Workspace, Keycloak); SAML supported via OIDC bridge. Group-to-team mapping documented; JIT provisioning supported via SCIM.
- **[Significant]** **No data-residency / multi-region story.** Global enterprises require it.
  - _Fix:_ Add §4.7.1.1 "Data residency": v1 is single-region per deployment; multi-region deployments run separate registries per region with no cross-region replication. Cross-region federation is roadmap.
- **[Significant]** **No retention policy** for audit logs, deprecated artifacts, or old versions. "Append forever" is not a strategy at enterprise scale.
  - _Fix:_ Add §8.4 "Retention" with defaults: audit events 1 year; query text 30 days; deprecated artifact versions 90 days post-sunset; soft-deleted artifacts 30 days. Configurable per deployment.
- **[Significant]** **No GDPR / right-to-erasure model.** A user leaves the org — what happens to their personal-overlay artifacts and to their attribution in audit logs?
  - _Fix:_ Add §8.5 "Erasure": `podium admin erase <user_id>` removes user-overlay artifacts, redacts the user identity in audit records (replaces with `redacted-<sha256(user_id+salt)>`), preserves audit event sequencing for integrity. Document soft-vs-hard-delete distinction.
- **[Significant]** **Approval workflows are minimal.** "One or more reviewers per sensitivity-driven policy" doesn't model real enterprise approval (manager + security + legal in parallel, time-boxed approvals, delegated approvals, four-eyes for high-sensitivity changes). The risks table mentions "admin override with audit" but there's no sophisticated approval engine.
  - _Fix:_ Add an `approval_policy:` field on artifacts (or inherited from sensitivity defaults). Schema supports: parallel reviewer groups, sequential stages, time-boxed approvals (auto-expire), delegated approvals (reviewer → designate). Defaults per sensitivity: low → single reviewer; medium → two reviewers; high → reviewer + security + manager. Document in §4.7.4.
- **[Significant]** **No change-window / freeze primitives.** Enterprises freeze production changes during release windows; deprecate-during-freeze should be blockable.
  - _Fix:_ Add `freeze_windows:` org-level config. Publish, deprecate, and overlay-edit operations are rejected during freeze unless `--break-glass` is passed (audited, requires admin justification). Document in §4.7.
- **[Significant]** **No license / IP / SBOM tracking** for bundled resources. A bundled binary should declare its license; a vulnerability scanner should be able to walk the dependency closure.
  - _Fix:_ Add `license:` (SPDX identifier) and `sbom:` (CycloneDX or SPDX inline or referenced) frontmatter fields. Registry surfaces them in `load_artifact`. Lint enforces license presence for sensitivity ≥ medium.
- **[Significant]** **No vulnerability management.** When a CVE drops on a bundled library, how does the registry surface affected artifact versions and notify owners?
  - _Fix:_ Add §4.7.7 "Vulnerability tracking": registry consumes CVE feeds, walks SBOM dependencies, surfaces affected artifacts via `podium vuln list`, notifies owners through configured channels. Stretch goal for v1.1; document the design surface now even if implementation is later.
- **[Minor]** No quotas (storage per org, search QPS, materialization rate) — necessary for cost control and abuse prevention.
  - _Fix:_ Add §4.7.8 "Quotas": per-org limits on storage, search QPS, materialization rate, audit volume. Admin-configurable. `podium quota` CLI surfaces current usage and limits.
- **[Minor]** "Admin can override review policy for emergencies, with the override recorded in audit" — but no break-glass framing (time-boxed, dual-signoff, post-hoc review). Enterprises will want this hardened.
  - _Fix:_ In §4.7.4, specify break-glass mechanics: requires dual-signoff (two admins); justification mandatory; auto-expires after 24h; queued for post-hoc review by the security team. Audit trail includes both signoffs and the justification text.

### Security — strong

- Bundled scripts inherit artifact sensitivity (§4.4 trust model).
- OS keychain for token storage on dev hosts.
- MCP server holds no long-lived credentials beyond what the IdentityProvider needs.
- Publish-time secret scanning is mentioned.

### Security — gaps

- **[Critical]** **Prompt injection** is listed as a risk (§12) but the mitigation ("manifests are authored by reviewed contributors") is dangerously thin. Prompts in artifacts run in the caller's agent context. If a `context` artifact embeds a snippet from a wiki page that turns out to contain an injection, RBAC didn't help — content provenance did. There's no content-provenance model, no per-source trust labeling, no "this prose body was authored vs imported" distinction.
  - _Fix:_ Add §4.4.2 "Content provenance": every prose section can declare a `source:` (`authored` | `imported` | `external-url`). The materialized prose carries provenance markers (e.g., `<!-- provenance: imported -->` framing) so the host can apply differential trust (e.g., quote imported content as data rather than treating it as instruction). Adapters propagate provenance to harnesses that support trust regions (e.g., Claude's `<untrusted-data>` convention).
- **[Critical]** **No artifact signing.** Bundled scripts and prompts execute in the caller's environment with the caller's privileges. Without cryptographic signing of artifact versions (Sigstore / cosign style), a caller cannot verify "this came from publisher X and hasn't been tampered with at the storage layer." For high-sensitivity skills this is table-stakes.
  - _Fix:_ Add §4.7.9 "Signing": each artifact version is signed by the publisher's key (Sigstore-keyless preferred; registry-managed key as fallback). Signatures are stored alongside content; the MCP server verifies signatures on materialization for sensitivity ≥ medium (configurable). `podium verify <id>` for ad-hoc verification. Signature failure aborts materialization with a structured error.
- **[Significant]** **Bundled-script supply chain.** Static analysis is mentioned but optional. There's no SBOM, no dependency pinning model, no advice on sandboxing. The trust model in §4.4 acknowledges the risk and stops there.
  - _Fix:_ Pair with the SBOM frontmatter field above: extend `runtime_requirements:` with `dependencies:` (lockfile-style for the script's own deps). Publish-time lint generates an SBOM if not provided. Document recommended sandboxing patterns (per harness) in §4.4.1.
- **[Significant]** **Sandboxing of bundled scripts** is delegated to the caller. Enterprise risk requires a baseline expectation. A "sandbox profile" is hinted at in §4.6 ("sandbox constraints" as a security-sensitive field) but never specified.
  - _Fix:_ Specify a `sandbox_profile:` frontmatter field with named values: `unrestricted` | `read-only-fs` | `network-isolated` | `seccomp-strict`. Adapters that target sandbox-capable runtimes wire this through; harnesses without sandbox capability MUST refuse to materialize an artifact with `sandbox_profile != unrestricted` (or be configured to ignore, with a loud warning).
- **[Significant]** **Audit log tamper-evidence.** Postgres-stored audit can be modified by anyone with database access. Hash-chained logs, append-only WORM storage, or external SIEM mirroring would harden this. The spec mentions external SIEM redirection but not the integrity guarantee.
  - _Fix:_ Add §8.6 "Audit integrity": every audit event carries a hash chain (event hash includes the previous event's hash); periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended. Detection of gaps is automated (gap → alert). Document SIEM mirroring as the operational integrity backstop.
- **[Significant]** **Search-result PII leakage.** Audit logs `search_artifacts` queries with caller identity (§8.1 + §8.2). PII redaction in §8.2 covers manifest-declared fields, not query content. A query like "policy for handling SSN 123-45-6789" lands in the audit log unredacted. Free-text query content needs its own redaction strategy.
  - _Fix:_ Specify in §8.2 that query text is regex-scrubbed for common PII patterns (SSN, credit-card, email, phone) before being written to audit. Patterns configurable via `PIIRedactionConfig`. Default-on; document the threat model.
- **[Significant]** **MCP server binary verification.** Callers download the MCP server binary; how do they verify it? Signed releases? Reproducible builds? Not mentioned.
  - _Fix:_ Sign release artifacts via Sigstore; publish checksums in release notes; commit to reproducible builds as a release-process requirement. Document verification steps in §6 and the release docs.
- **[Minor]** **Search exfiltration.** A reader can probe the visible catalog via search even without loading. By design, but worth calling out as an information-disclosure surface and a primitive for designing tighter scopes if needed.
  - _Fix:_ Support a per-artifact `search_visibility:` field with values `indexed` (default) | `direct-only`. Sensitive artifacts can opt out of search indexing while remaining loadable via direct ID. Document in §3.2 (Layer 2 / search) and §4.3.
- **[Minor]** **Token leakage from `injected-session-token` mode.** A token in an env var is readable by any process the runtime spawns. Worth documenting as a constraint on what runtimes can safely use this mode.
  - _Fix:_ In §6.3.2, document the requirement: the runtime owns the env-var/file lifecycle; tokens MUST be short-lived (≤15 min); the runtime MUST NOT pass the env var to user-controlled subprocesses. Recommend `PODIUM_SESSION_TOKEN_FILE` over env var when the runtime can write to a file with restrictive permissions.
- **[Minor]** **Adapter sandboxing.** Adapters are pluggable. A malicious adapter can do anything during materialization (which writes to the host filesystem). No threat model for adapters is given.
  - _Fix:_ Define an Adapter sandbox contract in §6.7: adapters MUST be no-network, MUST NOT write outside the materialization destination, MUST NOT spawn subprocesses. Enforced where Go runtime restrictions allow; documented as the contract for community adapters; conformance suite includes negative tests.

---

## 7. OSS community

### Tensions in the framing

- **[Significant]** "Enterprise registry" is the lead positioning. Hobbyists, solo developers, and small teams won't see themselves in the opening — they'll bounce.
  - _Fix:_ Add a parallel framing in §1.1: "For individuals and small teams: host your own catalog of skills and load them across whichever harness you use today. For enterprises: same registry, plus RBAC, audit, lifecycle, and overlay composition." Lead with the lighter framing.
- **[Significant]** The MVP build sequence is 13 phases, multi-year. Where's the "podium-lite" — a single binary with SQLite and a flat directory of artifacts, no RBAC, no overlays — that someone can run in five minutes for a personal project? Without a low-friction on-ramp, OSS adoption stalls.
  - _Fix:_ Add §10 phase 0: `podium serve --solo` ships in v1.0 — single binary, embedded SQLite, filesystem object storage, no auth, no overlays. Five-minute install for personal use; upgrade-in-place to the full deployment when ready.
- **[Significant]** **License is not stated.** Apache 2 vs MIT vs AGPL has huge implications for adoption (especially enterprise adoption, which the spec also targets). This needs to be explicit.
  - _Fix:_ State the license in §0 footer or near §1.1: **Apache 2.0**. Briefly justify (permissive, enterprise-friendly, common for infra projects).
- **[Significant]** **No project-governance story.** Maintainer model? RFC process for spec changes? TSC? For a spec this opinionated, the change process matters and signals openness.
  - _Fix:_ Reference a `GOVERNANCE.md` (separate doc) outlining maintainer model, RFC process for spec changes, and security disclosure policy. Link from §1.6 "Project model."

### Contribution surfaces are present but underdocumented

- **[Significant]** §9's pluggable interfaces are a real community-contribution asset (HarnessAdapter, IdentityProvider, RBACProvider, PublishLinter, etc.) — but there's no contribution flow, plugin distribution model, or registry of community plugins.
  - _Fix:_ Add §9.1 "Plugin distribution": for v1, plugins ship as Go modules importable into a registry build. Post-v1, support out-of-process plugins via a plugin protocol (subprocess + RPC). Reference a community plugin registry hosted at a project-owned URL.
- **[Significant]** How does someone propose a new artifact type? "Type system is extensible" but the extension contract is undefined (see §2 above). This blocks community-contributed types.
  - _Fix:_ Same as §2.type-system above: `TypeProvider` SPI plus a documented contribution flow ("propose a type via RFC; reference implementation via PR; reviewers per GOVERNANCE.md").
- **[Minor]** How are lint rules contributed? Is `PublishLinter` a single object that bundles rules or a registry of rules? Not specified.
  - _Fix:_ Specify `PublishLinter` as a registry of named rules, each implementing a `LintRule` interface. Community rules ship as Go modules. Document in §9.

### Community network effects

- **[Significant]** No federation / decentralization story. Could two registries import from each other? Could there be a "public Podium" with a curated set of common artifacts (style guides, code-review skills, etc.) that any enterprise can pull from? This is the network-effects flywheel for OSS.
  - _Fix:_ Add §1.6 roadmap note (and a §13 "Federation" section as future work): registries can mark themselves as upstream sources; downstream registries pull periodically; trust governed by signed catalog summaries. Out of scope for v1; documented as the v2 north star.
- **[Significant]** No public reference registry. A reader can't browse "100 example artifacts" to learn the format. Concrete examples drive adoption more than specs do.
  - _Fix:_ Commit to a public registry hosted by the project (e.g., `registry.podium.dev`) with curated examples. Document in §10 phase 13 and link from §1.1.
- **[Minor]** No mention of `podium plugin install` or any runtime extensibility — only build-time SPI extension. For OSS communities, pluggability at runtime expands the contributor pool dramatically.
  - _Fix:_ Out of scope for v1; SPI extension only. Note as future work in §9.

### Strengths

- The two-component split is OSS-friendly: registry server + thin client. Easy for contributors to grasp.
- The conformance test suite for adapters (§6.7) is OSS-friendly: clear bar for new adapters.
- The pluggable-interface philosophy is right.

---

## 8. MCP protocol fit (additional perspective)

This perspective deserves its own section because the entire architecture rests on MCP, but the spec doesn't engage with MCP idioms.

- **[Significant]** **MCP has `resources/list` and `resources/read`.** Podium models the catalog as three custom _tools_ instead of MCP resources. Why? Resources naturally support pagination, MIME types, subscription, and host-managed selection; tools require explicit calls. The trade-off is real and the spec should address it.
  - _Fix:_ Add §5.0 "Why tools, not resources": resources fit static lists and host-driven enumeration; Podium's catalog is parameterized navigation (`load_domain` takes a path; `search_artifacts` takes a query) and lazy materialization with side effects. Tools fit better. Document the trade-off and note that artifact bodies could optionally also be exposed as resources for hosts that prefer that pattern.
- **[Significant]** **MCP has `prompts/list` and `prompts/get`.** Podium has a `prompt` artifact type. Could `prompt` artifacts be projected as native MCP prompts so the host can offer them in slash menus? The spec is silent and is missing a free integration win.
  - _Fix:_ Add §5.2 "Prompt projection": when an artifact of `type: prompt` is loaded, the MCP server also exposes it via `prompts/get` so harnesses with slash-menu support can surface it. Opt-in via `expose_as_mcp_prompt: true` in frontmatter.
- **[Significant]** **Naming collision: Podium `tool` vs MCP "tool."** Podium's `tool` is "an MCP tool/server registration" (§4.1). MCP's "tool" is a callable. Reusing the word will confuse every reader and every prompt that references either. Suggest renaming to `tool-binding`, `mcp-server`, or `tool-source`.
  - _Fix:_ Rename Podium's `tool` artifact type to `mcp-server` everywhere. Document the rename in §4.1; update §4.7.3 reverse-index references; grep for `type: tool` and replace.
- **[Significant]** **MCP elicitation** is the right primitive for "ask the user something" flows like the OAuth device-code prompt. Not used.
  - _Fix:_ Use MCP elicitation for the OAuth device-code prompt and any future "ask the user" flows. Document in §6.3 (`oauth-device-code` provider).
- **[Minor]** **MCP roots** are the right primitive for workspace identification, which is exactly what `${WORKSPACE}/.podium/overlay/` resolution needs. Not used.
  - _Fix:_ Derive `PODIUM_OVERLAY_PATH` default from MCP roots when available (the `roots/list` response identifies the workspace). Document in §6.4.
- **[Minor]** **Capability negotiation.** Podium meta-tools would benefit from MCP capability declarations (e.g., "this server requires session correlation"). Not addressed.
  - _Fix:_ Declare server capabilities in the MCP `initialize` response: `{tools: true, prompts: <conditional>, sessionCorrelation: <conditional>}`. Document in §5.
- **[Minor]** **MCP version drift.** "Binary version mismatch with caller — refuse to start" — but MCP itself is versioned. The spec doesn't say how Podium handles MCP-protocol version skew between the MCP server and its caller.
  - _Fix:_ Add a row to §6.9 failure modes: "MCP protocol version mismatch — server negotiates down to caller's max supported MCP version; if no compatible version exists, fail with structured error `mcp.unsupported_version`."
- **[Minor]** The agent's view of Podium is entirely the three meta-tool descriptions — but those descriptions aren't in the spec. They are the most prompt-engineered part of the entire system; they should be in §5.
  - _Fix:_ Same as §5.caller-integration above (add §5.1 with canonical descriptions and prompting guidance).

---

## 9. Operations / SRE perspective (additional)

The spec is implementation-detailed but operationally thin.

- **[Significant]** **No deployment topology** for the registry. Single binary? Multi-replica behind a load balancer? Stateless front-end + stateful Postgres? Helm chart? K8s manifests? Bare-metal deployment guide?
  - _Fix:_ Add §13 "Deployment" with the reference architecture: stateless front-end (3+ replicas) behind a load balancer + managed or self-run Postgres + S3-compatible object storage. Helm chart shipped alongside v1.0. Single-node `--solo` mode for non-prod (tied to OSS phase 0 above).
- **[Significant]** **No runbook.** Common scenarios (Postgres failover, object-storage outage, IdP outage, full-disk on the registry node) aren't covered.
  - _Fix:_ Add §13.1 "Runbook" covering: Postgres failover, object-storage outage, IdP outage, full-disk on registry, audit-stream backpressure, runaway search QPS.
- **[Significant]** **No backup / restore story.** RPO/RTO targets? Cross-consistent backup of Postgres + object storage?
  - _Fix:_ Add §13.2 "Backup and restore": Postgres logical + physical backups; object-storage cross-region replication or snapshot; consistent restore via PITR + object-storage version history; default RPO 1h / RTO 4h.
- **[Significant]** **No migration story.** Postgres schema migrations? Online vs offline? Type-system migrations?
  - _Fix:_ Add §13.3 "Migrations": schema migrations bundled in the registry binary; expand-contract pattern for online migrations; type-system migrations versioned alongside the binary.
- **[Significant]** **No multi-region / replication story.** Read replicas? Write coordination? Object-storage replication?
  - _Fix:_ Add §13.4 "Multi-region": v1 single-region per deployment; cross-region read replicas via Postgres logical replication and object-storage replication; writes route to the primary region. Cross-region federation is post-v1.
- **[Significant]** **No capacity planning.** "Thousands of artifacts" is fine. "Millions of search queries per day" needs sizing.
  - _Fix:_ Add §13.5 "Sizing": baseline ("10K artifacts, 100 QPS, 1GB Postgres, 500GB object storage handles a typical mid-sized org") and scale guidance for 10× and 100× growth.
- **[Significant]** **Quotas / rate limits unaddressed.** A misbehaving caller can DoS the registry; no per-caller or per-org limits are specified.
  - _Fix:_ Same as §6.governance quotas above. Per-caller and per-org limits enforced at the front-end; structured errors on quota exhaustion.
- **[Significant]** **No CDN strategy.** Hot artifacts get pulled by every caller in an org; no edge-caching design.
  - _Fix:_ Add §13.6 "CDN": presigned URLs are CDN-friendly; recommend CloudFront/Fastly in front of object storage for hot artifacts. Document cache-control headers and the trade-off (TTL vs invalidation cost; immutable content_hash keys make this trivially safe).
- **[Significant]** **Audit log volume.** Every `load_domain` / `search_artifacts` / `load_artifact` is logged with full identity, scope, query, etc. At enterprise scale this is huge. No retention, sampling, or routing strategy.
  - _Fix:_ Same as retention fix above (§8.4) plus optional sampling for high-volume low-sensitivity event types (e.g., `load_domain` at 10% sample). Routing to external SIEM is configurable.
- **[Significant]** **No metrics surface.** Audit ≠ metrics. No mention of Prometheus, OpenTelemetry, or operator-facing observability for latency, cache hit rate, error rate, etc.
  - _Fix:_ Add §13.7 "Observability": Prometheus metrics endpoint on registry and MCP server (latency histograms, cache hit rate, error counts, RBAC denial counts); OpenTelemetry trace export; reference Grafana dashboard shipped with v1.0.
- **[Minor]** **Tracing.** "Trace ID" is mentioned but the propagation model (W3C Trace Context? B3?) and span structure aren't specified.
  - _Fix:_ Specify W3C Trace Context propagation across all calls (registry HTTP, MCP server invocations). Document span structure: one root span per `load_domain` / `search_artifacts` / `load_artifact` call with child spans for registry round-trip, object-storage fetch, adapter translation, materialization.
- **[Minor]** **Health checks.** Liveness/readiness probes for the registry and the MCP server aren't specified.
  - _Fix:_ Add §13.8 "Health and readiness": registry exposes `/healthz` (liveness) and `/readyz` (readiness — Postgres + object-storage reachable). MCP server exposes a `health` MCP tool returning registry connectivity + cache size + last successful call timestamp.

---

## 10. Naming & terminology (additional)

- **[Minor]** **"Podium"** doesn't telegraph "registry of AI artifacts." A podium is a raised stage. The metaphor (where the right skill steps up at the right moment) is plausible but not obvious. Naming is hard; the spec doesn't help the name carry weight (e.g., no tagline that uses the metaphor).
  - _Fix:_ Out of scope for spec changes (renaming is a marketing decision). Add a tagline to the doc header that anchors the metaphor: "Podium — the right skill steps up at the right moment." Link the metaphor to lazy materialization in §1.1.
- **[Minor]** **"Artifact"** is generic and overloaded in software (build artifact, ML artifact, JFrog Artifactory). Acceptable for genericity, but a glossary entry would help disambiguate from those neighbors.
  - _Fix:_ Add a glossary entry distinguishing Podium artifacts ("packaged AI authoring units — skills, agents, prompts, contexts, MCP-server registrations, etc.") from build/ML artifacts.
- **[Minor]** **"Caller"** is unusual; "client" or "host" is more standard. The spec uses both inconsistently.
  - _Fix:_ Standardize on **"host"** (matches MCP terminology — "MCP host"). Global find-and-replace for "caller" → "host."
- **[Minor]** **"Materialization"** is good and precise — keep it.
  - _Fix:_ No change. Keep "materialization."
- **[Minor]** **"Domain"** clashes with DNS domain and DDD domain. A reader has to context-switch each time.
  - _Fix:_ Keep the term (changing it would cascade through many sections), but add an explicit glossary disambiguation: "Podium domain — a node in the catalog hierarchy. Distinct from DNS domain or DDD domain."
- **[Minor]** **"Scope"** vs "tier" vs "layer" vs "overlay" — at least four terms for related concepts. §4.7.1 introduces "tiers"; §3.2 uses "layers"; §4.6 uses both. Pick one primary term and stick with it.
  - _Fix:_ Standardize as: **layer** = unit of composition (org / team / user / local); **overlay** = the contents authored under a layer; **scope** = the OAuth claim that grants access to a layer. Drop "tier" entirely. Global pass to fix wording.
- **[Minor]** **"Loopback bridge / loopback process"** (§6.1) — "loopback" is a network-stack term that doesn't fit. Suggest "client-side bridge" or "in-process bridge."
  - _Fix:_ Replace "loopback bridge / loopback process" with **"in-process bridge"** or **"local MCP bridge"** throughout.

---

## 11. Specific surface-level corrections

- **[Minor]** §3.2 is titled **"Five Layers of Disclosure"** but enumerates Layer 0 through Layer 5 — six entries. Also, the `<h4>` pattern in the description text alternates between `**Layer N — Title.**` formatting and prose, which makes scanning hard.
  - _Fix:_ Restructure as "Three disclosure layers + three enabling concerns" (per §1 fix above); use consistent `### Layer N — Title` headers.
- **[Minor]** §4.5.2 says "Globs are evaluated against the caller's effective view (org + team overlays + user + local)" — but per §4.6, the local overlay is merged client-side, so globs in a remote `DOMAIN.md` cannot include local paths. Worth resolving the inconsistency.
  - _Fix:_ Clarify in §4.5.2 that "effective view" is context-dependent: a remote `DOMAIN.md`'s globs resolve over the registry view (layers 1–3); a local `DOMAIN.md`'s globs resolve over the merged view (layers 1–4). Document the asymmetry with an example.
- **[Minor]** §6.6 references "inline threshold (~256 KB per resource, ~1 MB per file as a soft cap)" — and §7.2 uses identical language in a different context. The two thresholds (~256 KB and ~1 MB) need clearer roles: what's the inline-vs-presigned cutoff vs the publish-time per-file size cap?
  - _Fix:_ Re-section as three distinct thresholds with named roles: **Inline cutoff (256 KB)** — below this, body is returned in the `load_artifact` response; above, presigned URL. **Per-file soft cap (1 MB)** — publish-time warning above this. **Per-package soft cap (10 MB)** — publish-time error above this. Document in §6.6, §7.2, and §4.4.
- **[Minor]** The frontmatter sample (§4.3) shows `extends: finance/ap/pay-invoice` — a path. Elsewhere artifact IDs are referred to as "fully-qualified IDs." A note on the relationship (path == ID? path == one form of ID?) would help.
  - _Fix:_ State in §4.2 that the canonical artifact ID is the directory path under the registry root (e.g., `finance/ap/pay-invoice`). `extends:`, `delegates_to:`, and other refs use this ID, optionally suffixed with `@<semver>`. Add to glossary.
- **[Minor]** §9's `RegistryAuditSink` defaults to `RegistryStore` — same backing store. If they're collocated by default, is the interface real or aspirational? Worth disambiguating.
  - _Fix:_ Keep the interface; document that the default implementation writes to a separate Postgres table inside the same `RegistryStore` (different table, different access pattern, separately mockable for tests, separately routable to external SIEM).
- **[Minor]** §10 phase 8 says "IdentityProvider implementations: `oauth-device-code` (with OS keychain) and `injected-session-token`" — but `injected-session-token` is described in §6.3 as the MCP server reading a token from an env var or file the runtime updates. There's no specification of the contract between Podium and "the runtime" for token rotation; this should be a sub-spec.
  - _Fix:_ Add §6.3.2.1 "Token rotation contract": env-var change is observed at next call (no signal needed); SIGHUP triggers a forced re-read; `PODIUM_SESSION_TOKEN_FILE` is watched via fsnotify and re-read on change. Token rotation is the runtime's responsibility; the MCP server's only obligation is to read fresh on every call.
- **[Minor]** §11's verification list is comprehensive for unit/integration tests but has no performance, scale, or chaos tests.
  - _Fix:_ Add to §11: load tests (sustained 1K QPS for `search_artifacts`, 100 publish/min); soak tests (24h continuous); chaos tests (Postgres failover during load, object-storage stalls, network partitions between MCP server and registry, IdP outage during refresh).

---

## 12. Top-priority recommendations

If the author can only do five things to land the spec:

1. **Add a 1-page "What is Podium" with a concrete walkthrough** for cold readers — plain English, no MCP jargon, with a hello-world ARTIFACT.md and a sequence of meta-tool calls and what comes back.
2. **Specify versioning end-to-end** — version format, publish semantics, `latest` resolution, cache revalidation against immutable versions, pinning syntax in `extends:` / `delegates_to:` / `mcpServers:`, read-during-write consistency. Promote it to a top-level section.
3. **Specify the OAuth identity model** — IdP protocol(s), claim derivation (especially `team:<name>`), token lifetime/refresh/revocation, and the trust model the registry uses to validate identity in `injected-session-token` mode.
4. **Resolve the local-overlay × search × load_domain interaction.** Either (a) ship a client-side index in the MCP server so search can include local artifacts, (b) accept the gap and document it loudly with a `podium publish --draft` workflow that round-trips to the registry, or (c) push the local overlay to the registry transiently (with a TTL) so the registry index sees it.
5. **Add a competitive positioning section** — one page comparing to Anthropic Skills, Cursor Rules, MCP marketplaces, LangChain Hub, HuggingFace Hub, and the Git+CI baseline. The spec doesn't need to win every comparison; it needs to clarify _which adoption it's pursuing_ and _what alternatives the reader should weigh_.

If they can do ten:

6. Specify the artifact-signing and content-provenance model (Sigstore-style). High-sensitivity skills demand it.
7. Develop the authoring DX surface: `podium lint`, `podium dry-run`, `podium materialize`, `podium test`, JSON Schema for the frontmatter, a quickstart.
8. Specify the type-system extension contract — schema, registration, lint integration, role in §9.
9. Specify the MCP server's _system-prompt surface_ — the tool descriptions for `load_domain` / `search_artifacts` / `load_artifact`, with guidance on framing for the LLM.
10. Specify the deployment topology and operational concerns: backup/restore, multi-region, capacity planning, rate limits, metrics, health checks.

---

## Appendix: severity tally

- **Critical:** 7 findings — versioning (3), identity-trust contract (1), local-overlay/search gap (1), prompt-injection mitigation (1), artifact signing (1), SSO/SCIM model (1).
- **Significant:** ~50 findings, evenly distributed across architecture, DX, governance, security, and competitive positioning.
- **Minor:** ~30 findings — mostly clarity, wording, and specific surface corrections.

The Critical and Significant findings are concentrated in (a) versioning, (b) identity, (c) authoring DX, (d) competitive framing, and (e) the local-overlay/search interaction. Addressing those five clusters would lift the spec substantially.
