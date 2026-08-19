# Proposal 0008: Authorization for the runtime signing-key and re-embed admin endpoints

- Issue: (to be filed)
- Status: Approved (2026-08-19). Converged after 17 adversarial review rounds (21 findings fixed).
- Date: 2026-08-18

## Summary

**What changes.**

- §6.3.2 gains the normative rule for where the trusted runtime signing-key set comes from: operator-supplied configuration read at startup, with no request-time registration API. The §6.9 untrusted-runtime row and the §6.10 `suggested_action` move with it (`spec/06-mcp-server.md`), and the §14.11 CI pipeline-setup step, which restates the onboarding registration, moves with it as well (`spec/14-common-scenarios.md`).
- §13.12 gains a `PODIUM_RUNTIME_KEYS_PATH` row covering the read timing, the owning process, and the startup failures, and mints `config.runtime_keys_unavailable` (`spec/13-deployment.md`).
- §4.7 names `POST /v1/admin/reembed`, its per-tenant `admin` authority, its §13.2.1 write classification, and the rule for a registry that authenticates no caller. §4.7.2 gains the matching admin-power bullet (`spec/04-artifact-model.md`), and §13.2.1 restates its write-endpoint sentence as a rule over the `/v1` catalog and administrative endpoints, with the §6.3.1 SCIM receiver named as the stated exception, so the enumeration stops reading as the closed set (`spec/13-deployment.md`).
- `POST` and `GET /v1/admin/runtime` are deleted, along with `RuntimeKeyEndpoint` and its allow-all authorization default (`pkg/registry/server/runtime_endpoint.go`, `internal/serverboot/serverboot.go`).
- `podium admin runtime register` writes the seed file through `--keys-file` and `podium admin runtime list` is removed (`cmd/podium/admin_runtime.go`).
- `serverboot` fails closed on the runtime key seed: an unreadable or unparseable file aborts startup under every identity provider, and an empty key set aborts startup under `injected-session-token` (`internal/serverboot/serverboot.go`, `internal/serverboot/identity_verify.go`).
- The §13.1.1 evaluation stack stops selecting `injected-session-token`, publishes its port on the host loopback interface, and pins `PODIUM_DEFAULT_LAYER_VISIBILITY: "private"`. The meta-tools, `/v1/quota`, `/v1/events`, `/objects/`, and `POST /v1/admin/reembed` answer 401 `auth.untrusted_runtime` today, because the stack selects a verifying provider over an empty key set, while `GET /v1/layers`, the ingest webhook, and `/healthz` are unaffected, and the boot guard turns that runtime failure into a startup failure. The registry then authenticates no caller, so §13.1.1 gains a spec amendment recording the posture and the loopback publish, and §7.7 restates the `podium login` no-op predicate over the resolved registry URL so one predicate serves both sections (`docker-compose.yml`, `spec/13-deployment.md`, `spec/07-external-integration.md`, `test/e2e/deployment_compose_test.go`).
- `handleReembed` gains the per-tenant admin gate and the read-only gate its siblings already carry, and the boot path records whether the deployment authenticates any caller through the new `server.WithUnauthenticatedReembed()` option (`pkg/registry/server/server.go`, `internal/serverboot/serverboot.go`).

**Fixed decisions.**

- The runtime trust root is boot-time configuration. Do not add a request-time registration path, gated or otherwise.
- `/v1/admin/runtime` is deleted with no shim, no redirect, and no dual path. Podium is pre-1.0.
- `podium admin runtime list` is deleted. The target is a local JSON file of public keys that `cat` or `jq` reads.
- The re-embed authority is the per-tenant `admin` role (§4.7.2). The instance-operator role confers no per-tenant rights and does not gate the endpoint.
- The re-embed gate is scoped to `handleReembed`. Do not touch `requireAdmin`, `/v1/webhooks`, `/v1/admin/grants`, `/v1/admin/show-effective`, or the `as_admin` override.
- The re-embed gate is skipped only where the boot path recorded that the deployment authenticates no caller, which is `cfg.publicMode || cfg.identityProvider == ""`. Do not re-derive that fact inside the server from `s.idVerifier`, which is nil on a registry that names an identity provider this build installs no verifier for.
- `PODIUM_RUNTIME_KEYS_PATH` is environment-only. It gains no `identity_provider:` config-file key.
- One new error code, `config.runtime_keys_unavailable`, covers the unreadable, unparseable, unset, and empty cases.
- An unreadable or unparseable keys file aborts startup under every identity provider. An empty key set aborts startup only under `injected-session-token`, because no other provider consults the store.
- §13.2.1 states its write rule over the `/v1` catalog and administrative endpoints and names the §6.3.1 SCIM receiver as the exception. The enumeration becomes named examples, and no documentation page mirrors it.
- The §13.1.1 evaluation stack selects no identity provider and publishes its port on the host loopback interface. Open question 4 records the alternative.
- The `register` verb is kept, so the §6.10 `suggested_action` gains `--keys-file` rather than a new command name.

**Watch out for.**

- **The e2e suite is the trap.** Every call site of `injRegisterRuntime` (`test/e2e/injected_token_helpers_test.go:73`) registers the runtime key over HTTP after the process boots, across the files listed in TEST-1. They are the evidence the removed path worked, and every one of them breaks. Migrate them to a seeded file first (S4), while the endpoint still exists, so the migration commit is green on its own. The migration is per boot site rather than per call site, and each site both seeds and names the file. Writing the seed file without adding `PODIUM_RUNTIME_KEYS_PATH` to that boot's environment slice leaves a file no process opens, and the boot then aborts. A helper that seeds a keypair of its own instead of the caller's compiles and leaves every token that test signs untrusted.
- **Ordering matters across S5 to S7.** The CLI must stop calling the endpoint (S5) before the mount is dropped (S6), and the mount must be dropped before the file is deleted (S7), or the tree does not compile: `internal/serverboot/serverboot.go:1201` constructs `NewRuntimeKeyEndpoint`, and `cmd/podium/admin_runtime_test.go:33` does too.
- **A path typo does not reach the error branch.** `LoadFilePersistedRuntimeKeyRegistry` returns a nil error with an empty registry for a nonexistent file (`pkg/identity/runtime_persist.go:62-63`), and returns a nil error for a zero-length file (`:67-69`). The error branches are the read failure at `:65`, the malformed JSON at `:72`, the unparseable PEM at `:77`, and the register failure at `:84`. The empty-set guard is what catches a typo; the load-error guard is what catches a corrupt file. Both are needed and they are separate branches.
- **A registry can name an identity provider and still verify nobody.** `PODIUM_IDENTITY_PROVIDER=oidc` is a free-form label that `identity.Default` does not carry (`pkg/identity/registry.go:63-83`), so `selectIdentityProvider` returns no provider (`internal/serverboot/identity_verify.go:136`), `identityVisibilityGuard` exempts it (`:92-96`, `:100`), and no verifier is installed; `test/e2e/auth_oidc_test.go:1014-1023` boots exactly that way. `s.idVerifier` is therefore nil on a registry the operator did configure, which is why the re-embed gate reads a boot-recorded option instead of `s.idVerifier`.
- **`bootRegistryWithAdmin` builds a gated server.** It passes no `WithUnauthenticatedReembed` (`pkg/registry/server/admin_test.go:26-48`), so the gate is live there and `s.identity` falls through to the `WithIdentityResolver` value (`pkg/registry/server/server.go:1337`). `bootRegistryWithAdmin(t, "alice", nil)` reaches the handler, and `bootRegistryWithAdmin(t, "", nil)` answers 403.
- **`registryharness` must keep answering re-embed.** `internal/testharness/registryharness/registryharness.go:35` builds its server through `server.NewFromFilesystem`, and `pkg/registry/server/extra_handlers_test.go:66-161` posts to `/v1/admin/reembed` with no identity. `NewFromFilesystem` applies `WithUnauthenticatedReembed` itself, because a filesystem-source registry has no identity provider by definition (`spec/13-deployment.md:484`).
- **`rejectIfReadOnly` no-ops on a nil `ModeTracker`.** `bootRegistryWithAdmin` passes none, so a read-only assertion needs `server.WithMode(tracker)` (`pkg/registry/server/server.go:129`).
- **The removed route answers 404 only where no verifier runs.** `pathRequiresIdentity` (`pkg/registry/server/identity_verify.go:73-83`) admits no exemption for `/v1/admin/runtime`, so an unauthenticated caller under `injected-session-token` gets 401 `auth.untrusted_runtime` rather than 404. Assert 404 on a standalone boot and for a verified caller only.
- **`TestAdminRuntimeRegister_HappyPath` writes a deliberately fake PEM body and expects exit 0** (`cmd/podium/layer_subcommands_test.go:359-375`). It passes today only because the CLI never parses the key. A parsing CLI flips it to a failure, so it needs a real key.
- **The committed compose stack is configured the way the boot guard rejects.** `docker-compose.yml:132` sets `PODIUM_IDENTITY_PROVIDER: "injected-session-token"` on the `registry` service, the service's environment block (`:120-140`) sets no `PODIUM_RUNTIME_KEYS_PATH`, the service mounts nothing (its last key is `ports:` at `:141`, and the `volumes:` list at `:144` is the top-level one), and the image supplies no default (`Dockerfile:71` runs `/usr/local/bin/podium-server`, whose main is `serverboot.Run()` at `cmd/podium-server/main.go:20`). The stack boots today and fails at request time instead: the verifier runs over an empty key set, so every inner-mux request that `pathRequiresIdentity` covers answers 401 `auth.untrusted_runtime`. The guard converts that into an abort of the one command `spec/13-deployment.md:16` and `docs/deployment/clustered.md:97` both say brings the stack up, and nothing in the automated lanes catches it: the live bring-up is skipped (`test/e2e/standard_deployment_test.go:195-196`), the structural test only parses the YAML (`test/e2e/deployment_compose_test.go:108`), and `make services-up` starts postgres, minio, and bootstrap alone (`Makefile:193-194`). CODE-2 changes the compose file in the same step as the guard, S10 amends §13.1.1 to state the resulting posture, and Decision 16 and open question 4 record the provider choice.
- **The hand-run scenarios need reordering rather than a flag swap.** `test/manual-validation.md` S12 and S28 start the server and then register the runtime key, so once the boot guard lands the boot aborts with `config.runtime_keys_unavailable`, the health check never succeeds, and the register command has no registry to reach. The register step moves ahead of the boot and the serve environment gains `PODIUM_RUNTIME_KEYS_PATH`. S13 and S35 inherit the S12 procedure by reference.
- **A prior generalization was rejected.** An earlier draft put the no-caller carve-out inside `requireAdmin`, which would reopen `/v1/webhooks` and `/v1/admin/grants` to anonymous callers on a no-identity-provider bind and reverse `spec/07-external-integration.md:125` and `proposals/0004-webhook-hardening.md:36`. That path is closed; see "Decisions".

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §6.3.2 establishes the trusted key set from configuration, and the §6.9 row, the §6.10 `suggested_action`, and the §14.11 pipeline-setup step in `spec/14-common-scenarios.md` follow it.
      Levels: — Depends on: —
- [ ] **S2 · spec** — SPEC-2. §13.12 documents `PODIUM_RUNTIME_KEYS_PATH` and mints `config.runtime_keys_unavailable`.
      Levels: — Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §4.7 and §4.7.2 name the re-embed endpoint, its authority, and the no-caller rule, and §13.2.1's write-endpoint sentence becomes a rule with named examples.
      Levels: — Depends on: —
- [ ] **S10 · spec** — SPEC-4. §13.1.1 records that the evaluation stack authenticates no caller, that its port publishes on the host loopback interface, and that the seeded admin grant is a forward-compatibility seed rather than a working credential, and §7.7 in `spec/07-external-integration.md` restates the `podium login` no-op predicate over the resolved registry URL.
      Levels: — Depends on: —
- [ ] **S4 · test** — TEST-1. Every injected-session-token end-to-end boot site, helper and inline alike, seeds `PODIUM_RUNTIME_KEYS_PATH` before the process starts, replacing the post-boot HTTP registration. Lands before the code steps so the migration commit is green while the endpoint still exists.
      Levels: e2e. Depends on: S2
- [ ] **S5 · code** — CODE-3. `podium admin runtime register` writes the seed file through `--keys-file`, `list` is removed, and the `auth.untrusted_runtime` remediation string follows §6.10.
      Levels: unit, integration, e2e. Depends on: S1, S4
- [ ] **S6 · code** — CODE-2. `serverboot` fails closed on the runtime key seed, drops the `/v1/admin/runtime` mount, and the §13.1.1 compose stack stops selecting `injected-session-token`, publishes 8080 on the host loopback interface, and pins `PODIUM_DEFAULT_LAYER_VISIBILITY: "private"`, in one edit.
      Levels: e2e. Depends on: S2, S5, S10
- [ ] **S7 · code** — CODE-1. `RuntimeKeyEndpoint` and its tests are deleted, `RuntimeKeyVerifierStore` narrows to the surface the verifier uses, and the `ErrReadOnly` doc comment states the rule in place of its enumeration.
      Levels: e2e. Depends on: S6
- [ ] **S8 · code** — CODE-4. `handleReembed` gains the per-tenant admin gate and the read-only gate, `server.WithUnauthenticatedReembed()` carries the boot-time no-caller fact, and `bootstrapOptions` plus `server.NewFromFilesystem` set it.
      Levels: integration, e2e. Depends on: S3
- [ ] **S9 · docs** — DOCS-1. The reference, getting-started, consuming, and deployment pages, the README, and the manual-validation scenarios follow the removed endpoint, the new flag, and the new error code. No documentation page restates the §13.2.1 write set under this proposal.
      Levels: e2e. Depends on: S3, S5, S6, S7, S8, S10

## Current state and the gap

### The runtime signing-key endpoint writes the registry's trust root with no authorization

`POST` and `GET /v1/admin/runtime` register and list the §6.3.2 runtime signing keys. `internal/serverboot/serverboot.go:1201` builds the endpoint with `server.NewRuntimeKeyEndpoint(runtimeKeys, mode)` and never chains `WithAdminAuth`, so both handlers run the allow-all default seeded at `pkg/registry/server/runtime_endpoint.go:32` (`POST` at `runtime_endpoint.go:73`, `GET` at `:115`). The route is mounted at `serverboot.go:1213` on serverboot's own mux, ahead of `mux.Handle("/", srv.Handler())` at `:1224`, so Go's exact-pattern precedence keeps it off the registry chain and `pathRequiresIdentity` (`pkg/registry/server/identity_verify.go:73`) is never consulted for it. The only wrapper on that mux is `otelhttp`.

Under `PODIUM_IDENTITY_PROVIDER=injected-session-token` the same `runtimeKeys` value backs the endpoint and the request-time verifier (`serverboot.go:1047`, `:1095`), and the in-code comment at `serverboot.go:1044-1046` states that a key registered over HTTP is trusted immediately. `pkg/identity/runtime.go:204` derives the identity from the token's own claims and binds nothing about `sub` to the registered issuer, and `pkg/identity/runtime.go:92` replaces an existing issuer's key, so an unauthenticated `POST` lets the caller mint a token for any subject, including a bootstrap admin, and then grant itself admin through `/v1/admin/grants`. The sequence is already a green end-to-end test minus the attacker framing (`test/e2e/auth_admin_rbac_test.go:66-101` with `test/e2e/injected_token_helpers_test.go:73-84`). The registration is also unaudited: `RuntimeKeyEndpoint` has no sink field and `register` emits nothing (`runtime_endpoint.go:73-108`), unlike the sibling grant handler at `pkg/registry/server/admin.go:52`.

Applying the sibling `LayerEndpoint` callback from `serverboot.go:1190` does not resolve this. Under `injected-session-token` a verified identity requires a trusted runtime key, so a request-time gate on the first registration is circular, and the per-tenant `AdminAuthorize` (`pkg/registry/core/admin.go:21`) is scoped to one tenant while the key store is one process-wide value that mints tokens for every tenant. The exposure also reaches past the first key: because `sub` and `org_id` are unconstrained, an already-trusted runtime can mint a token for the operator's own subject, so even a correctly operator-gated registration endpoint would let one trusted runtime install further trust anchors.

### The re-embed endpoint checks only the request method

`POST /v1/admin/reembed` (registered at `pkg/registry/server/server.go:370`, handled at `:869`) checks the method and nothing else, despite the doc comment at `:868` claiming "Admin-only in production deployments". It is on the inner mux, so a caller is authenticated where a verifier is installed, but any authenticated caller triggers a full-catalog pass that embeds every manifest and every `DOMAIN.md` through the configured provider (`pkg/registry/core/reembed.go:125-160`) and then calls `PurgeModelExcept`, deleting the tenant's non-current-model vector rows (`:165-172`). No quota applies, because `QuotaLimiter` exposes only `AllowSearch` and `AllowMaterialize` (`pkg/registry/server/rate_limit.go:93`, `:109`). Where no verifier is installed the middleware is a pass-through (`pkg/registry/server/identity_verify.go:40-42`), so the caller is anonymous. The handler also omits `rejectIfReadOnly`, which every sibling admin write performs (`pkg/registry/server/admin.go:31`, `pkg/registry/server/tenants.go:142`, `pkg/registry/server/webhooks.go:49`), so a §13.2.1 read-only registry still accepts a full re-embed.

### The spec defines neither path

A sweep of `spec/` for `/v1/` returns only `/v1/artifacts`, `/v1/load_artifact`, `/v1/scope/preview`, `/v1/layers`, `/v1/layers/reingest`, `/v1/webhooks`, `/v1/model.onnx`, and `/v1/admin/tenants`, and `git log -S "admin/runtime" -- spec/` returns no commits. `spec/06-mcp-server.md:68` says only that the key is "registered with the registry one-time at runtime onboarding", naming no interface, no caller, and no authorization. `spec/04-artifact-model.md:756` names `podium admin reembed` with no endpoint and no authorization, and the only generic statement is an error listing, "admin-only operations attempted by a non-admin (`auth.forbidden`)" (`spec/07-external-integration.md:97`), which names no endpoint. `PODIUM_RUNTIME_KEYS_PATH`, which already loads a hand-authorable JSON key file at boot (`serverboot.go:1048-1055`, `pkg/identity/runtime_persist.go:36-88`), appears nowhere in `spec/` or `docs/`. The authorization model therefore has to be settled in the spec before it can be implemented.

## Decisions

1. **The runtime trust root is boot-time configuration rather than a request-time write.** A runtime signing key mints tokens for any subject in any organization (`pkg/identity/runtime.go:194-209`), so establishing it is an instance-level act by the operator who owns the process configuration. The repository already breaks the identical first-grant circularity this way: `PODIUM_BOOTSTRAP_ADMINS` at `internal/serverboot/serverboot.go:748-750`, whose comment names the chicken-and-egg outright, and `PODIUM_OPERATOR_ADMINS` at `:757-765`. `PODIUM_RUNTIME_KEYS_PATH` already loads a JSON array of `{issuer, algorithm, public_key_pem}` records before any request is served (`serverboot.go:1048-1055`, `pkg/identity/runtime_persist.go:53-57`), so the mechanism exists and needs specifying rather than inventing.

2. **One registration path survives, so the HTTP registration surface is removed rather than gated.** `pkg/identity` ships only the in-memory `RuntimeKeyRegistry` and the per-process `FilePersistedRuntimeKeyRegistry`; no `RegistryStore`-backed key table exists. `spec/13-deployment.md:5` puts 3+ stateless replicas behind a load balancer, so an HTTP registration today lands on whichever replica the load balancer picked and the other replicas keep rejecting that runtime until they restart. Removing the endpoint therefore loses no working fleet capability and leaves a single canonical way to establish trust. The argument in §6.3.2 that any already-trusted runtime can mint an operator token reinforces it: gating the endpoint on the operator role would still let one trusted runtime install further trust anchors.

3. **The spec states the fleet requirement and leaves the storage mechanism to §13.12.** Every other trust root in the spec is fleet-consistent: `oidc-jwt` resolves signing keys from the issuer JWKS with a refresh TTL (`spec/06-mcp-server.md:96`, `spec/13-deployment.md:477`), and admin and operator grants live in Postgres (`spec/04-artifact-model.md:801`, `spec/13-deployment.md:6`) seeded from boot environment variables into the shared store. §6.3.2 therefore says that every replica is configured with the same key set and does not write "process-local file per replica" into normative prose, so a later store-backed seed becomes an implementation change rather than a spec amendment.

4. **A registry that can verify nobody refuses to start.** Under `injected-session-token` an empty key set makes the verifier reject every meta-tool call with `auth.untrusted_runtime`, and with HTTP registration gone there is no recovery path. `internal/serverboot/identity_verify.go:118` (`injectedTokenAudienceGuard`) is the precedent: a provider-scoped boot guard carrying a `config.*` code. One new code, `config.runtime_keys_unavailable`, covers the unset, unreadable, unparseable, and empty cases, and the current `log.Printf` warning at `serverboot.go:1049-1051` becomes a hard failure.

5. **An unreadable or unparseable key file aborts startup under every identity provider, while an empty key set aborts startup only under `injected-session-token`.** A trust-anchor file the registry cannot parse should never boot silently, and the read at `serverboot.go:1048` is already outside the provider branch. A file that carries no key is a different case: `load` returns nil for the missing file (`pkg/identity/runtime_persist.go:61-63`), for the zero-length one (`:67-69`), and for a body holding `[]`, which falls through the loop at `:74-86` to the `return nil` at `:87`. Under `oidc-jwt`, `trusted-headers`, or a standalone bind all three resolve to an empty key set that no code path consults, and startup proceeds. The §13.12 row and the edge-case table state that predicate in the same terms. This reconciles two review rounds that reached opposite conclusions; see "Resolved in adversarial review".

6. **The re-embed authority is the per-tenant admin role (§4.7.2) rather than the instance operator role.** The pass is tenant-scoped in code (`pkg/registry/core/reembed.go:65`, `:72`), and `spec/04-artifact-model.md:780` states the operator role confers no per-tenant rights. The §4.7.2 bullet list is already not the closed set of admin powers: `spec/07-external-integration.md:125` assigns webhook receiver CRUD to the per-tenant admin role, and `spec/08-audit-and-observability.md:24` attributes GDPR erasure to an admin. Naming re-embed there extends an existing concept.

7. **The no-caller exception is scoped to re-embed, and the boot path decides it.** An earlier draft added a `WithLocalOperatorAdmin` option consulted by `requireAdmin` (`pkg/registry/server/admin.go:113`), which also gates `/v1/admin/grants` (`admin.go:23`), `/v1/admin/show-effective` (`:86`), receiver CRUD (`pkg/registry/server/webhooks.go:25`, `:106`), and the `as_admin` override (`server.go:813`, `:974`). That reverses `proposals/0004-webhook-hardening.md:36` and the landed `spec/07-external-integration.md:125`, and in public mode it would let an anonymous caller write a persistent `(identity, org_id, "admin")` row that outlives the mode. The option therefore survives in a form `handleReembed` alone reads, `server.WithUnauthenticatedReembed()`, and `serverboot` sets it from the predicate the sibling `LayerEndpoint` admin callback already uses, `cfg.publicMode || cfg.identityProvider == ""` (`internal/serverboot/serverboot.go:1195`).

    An in-server reading of the same fact does not hold. `s.idVerifier != nil && !s.publicMode` was staged as that reading, on the ground that `identityVisibilityGuard` refuses to boot a selected provider with no verifier. The guard returns nil whenever the provider is not selected (`internal/serverboot/identity_verify.go:100-101`), and `selectIdentityProvider` selects nothing for a value `identity.Default` does not carry (`identity_verify.go:136`, `pkg/identity/registry.go:63-83`). A registry started with `PODIUM_IDENTITY_PROVIDER=oidc` therefore boots with no verifier (`internal/serverboot/identity_verify.go:92-96`, `test/e2e/auth_oidc_test.go:1014-1023`), while `StartupConfig.Validate` counts that same value as an identity provider for the public-mode exclusion (`pkg/registry/server/config_validate.go:88`). The in-server reading would open re-embed to an anonymous caller on that deployment, and it would do so in the same process where the `/v1/layers` admin operations deny one, so two gates would disagree about one registry. `server.WithIdentityResolver` (`pkg/registry/server/server.go:166`) has no non-test caller, so a gated `handleReembed` reads one of two identities. Under `injected-session-token`, `oidc-jwt`, and `trusted-headers` it reads the identity the verification middleware stored on the request context (`pkg/registry/server/server.go:1334-1336`). On the free-form-label bind the middleware is a pass-through (`pkg/registry/server/identity_verify.go:40-41`) and `s.identity` falls back to the anonymous-public default `server.New` installs (`server.go:256-261`), which `AdminAuthorize` refuses before it consults the grant table (`pkg/registry/core/admin.go:22-23`), so the gate answers `auth.forbidden` on that deployment as well.

8. **Re-embed becomes a §13.2.1 write, and §13.2.1 stops being read as a closed list.** `handleReembed` omits `rejectIfReadOnly` while `pkg/registry/server/admin.go:31` and `pkg/registry/server/tenants.go:142` perform it, so a read-only registry currently accepts a full-catalog re-embed against a store it cannot write. CODE-4 adds the call. The classification is stated once, in §4.7 Edit A, the way `spec/07-external-integration.md:152` states it for the tenant endpoints. The set itself is defined in code by the `rejectIfReadOnly` call sites (`pkg/registry/server/webhook_ingest.go:35`, `layers.go:351`, `:443`, `:547`, `:755`, `:803`, `:843`, `:915`, `admin.go:31`, `webhooks.go:49`, `:132`, `:190`, `tenants.go:142`, `:184`, `:240`), and no prose site tracks it. `grep -rn "freeze toggle" . --exclude-dir=.git --exclude-dir=node_modules` returns the six sites that restate the §13.2.1 list today: `spec/13-deployment.md:40`, `deploy/runbook.md:18-20`, `docs/deployment/operator-guide.md:132`, `docs/reference/http-api.md:640`, `docs/reference/error-codes.md:151`, and `pkg/registry/server/readonly.go:13`. Edit C converts the spec sentence to a rule with named examples, which makes the four prose restatements at `deploy/runbook.md:18-20`, `docs/deployment/operator-guide.md:132`, `docs/reference/http-api.md:640`, and `docs/reference/error-codes.md:151` accurate as illustrations and stages no edit to any of them. The doc comment at `pkg/registry/server/readonly.go:10-15` is the exception: CODE-1 rewrites it, because that step deletes the `runtime_endpoint.go:74` call site the comment's `runtime-key issuance` entry names. Two facts are pre-existing and stay out of scope: `freeze toggles` and login-driven token issuance name no route, and `/v1/admin/erase`, the `/v1/webhooks` receiver CRUD routes, and layer `restore` reject without being named anywhere. A third fact is handled inside Edit C rather than deferred: the SCIM writes at `pkg/scim/handler.go:78`, `:82`, `:84`, `:96`, `:100`, and `:102` persist users and group memberships to the registry store (`internal/serverboot/serverboot.go:80`) over the receiver mounted on the registry's own mux (`pkg/registry/server/server.go:384`) and consult no `ModeTracker`. The current closed enumeration makes no claim about them, so a rule stated over every state-mutating endpoint would create a spec-versus-code divergence that does not exist today. Edit C therefore bounds the rule to the `/v1` catalog and administrative endpoints and names the §6.3.1 receiver as the exception, and gating the SCIM writes stays separate work.

9. **`PODIUM_RUNTIME_KEYS_PATH` is environment-only.** `spec/13-deployment.md:480` makes `identity_provider.audience` file-settable, so this row splits one provider's required configuration across two mechanisms. The staged code reads only `os.Getenv` at `internal/serverboot/serverboot.go:1048`, and a config-file key would need parsing work in `internal/serverboot/yaml_config.go` that no staged code change carries. The asymmetry is recorded here so a reader of `spec/13-deployment.md:563` does not have to discover that a file-configured `injected-session-token` registry still needs one environment value.

10. **The keys file has a single writer.** `save()` rewrites the whole array from the snapshot loaded at command start and writes through the fixed name `r.path + ".tmp"` (`pkg/identity/runtime_persist.go:100-122`), guarded only by an in-process mutex (`:29`). That is safe today because the registry process is the sole writer, and a CLI writer makes concurrent invocations possible. The proposal takes the documented-constraint option rather than adding a lockfile: `docs/reference/cli.md` states that the keys file has a single writer and that concurrent `register` invocations are undefined. The staged spec text keeps its normative core and does not carry the operational constraint.

11. **`podium admin runtime list` is removed rather than repointed at the file.** The target becomes a local JSON file of public keys the operator owns, which `cat` or `jq` reads. The withholding argument does not apply, because `runtimeKeyJSON` stores only PKIX public keys (`pkg/identity/runtime_persist.go:53-56`). Removing it also drops `test/e2e/plugin_spi_test.go:413-428`, `test/e2e/standard_deployment_test.go:1314-1339`, `cmd/podium/layer_subcommands_test.go:377-386`, and the `list` row in `docs/reference/cli.md:569-585` instead of rewriting them.

12. **`register` keeps its verb, and `podium admin runtime` is named a local form.** `podium admin erase --local` (`cmd/podium/admin.go:226-227`) is the precedent for an admin subcommand that operates on a local file rather than a registry, so the divergence from the `--registry`-shaped `podium admin tenant` family (`spec/13-deployment.md:506-515`) is deliberate. Keeping `register` holds the §6.10 `suggested_action` edit to appending `--keys-file` and the restart sentence.

13. **`--keys-file` is required and takes no environment default.** `PODIUM_RUNTIME_KEYS_PATH` is a registry-process variable (`internal/serverboot/serverboot.go:1048`), so defaulting the flag to it on the operator's shell yields an empty path and surfaces the library error `runtime: path required` (`pkg/identity/runtime_persist.go:37-40`) in place of the CLI's own message, whose text is asserted at `test/e2e/standard_deployment_test.go:1309`.

14. **`register` validates the key at authoring time.** `code-serverboot-bootstrap` makes a malformed keys file a startup failure, so `register` validates the algorithm against the key type through `identity.ParsePublicKeyPEM` (`pkg/identity/parse_pem.go:23`) when the operator writes the record rather than at the next restart, and writes the PEM into JSON without hand-escaping.

15. **No compatibility surface is kept.** Podium is pre-1.0, so the removed endpoints and the removed `--registry` form of `podium admin runtime` land as a MINOR bump with no deprecation flag, no dual path, and no redirect.

16. **The §13.1.1 evaluation stack stops selecting `injected-session-token`, publishes 8080 on loopback, and pins its layer-visibility default.**

    The stack does not work today. `docker-compose.yml:132` selects `injected-session-token`, the environment block at `:120-140` names no `PODIUM_RUNTIME_KEYS_PATH`, the service mounts nothing, and the image supplies no default (`Dockerfile:71`, `cmd/podium-server/main.go:20`), so `serverboot` installs the request-time verifier over an empty key set (`internal/serverboot/serverboot.go:1088-1097`) and every inner-mux request that `pathRequiresIdentity` covers answers 401 `auth.untrusted_runtime` before a handler runs, which excludes `/healthz`, `/readyz`, and the `/scim/` paths (`pkg/registry/server/identity_verify.go:39-54`, `:73-81`). The three commands at `spec/13-deployment.md:18-22` themselves succeed, because none of them reaches the inner mux: `podium init` writes a `sync.yaml` file and opens no connection (`cmd/podium/main.go:1216-1330`), and `podium login` short-circuits on `http://localhost:8080` with "requires no authentication; nothing to do." and exit code 0 (`cmd/podium/login.go:57-59`, `:181`). Every subsequent inner-mux call, the meta-tools, `/v1/quota`, `/v1/events`, `/objects/`, and `POST /v1/admin/reembed`, answers 401. The boot guard does not create that defect. It converts a silent runtime failure into a startup failure, which is why the compose file changes in the same step.

    The registry service therefore sets no `PODIUM_IDENTITY_PROVIDER`, which leaves it on the posture `identityVisibilityGuard` exempts (`internal/serverboot/identity_verify.go:99-101`). Every caller then resolves to the anonymous-public identity `server.New` installs (`pkg/registry/server/server.go:256-261`), and `pkg/layer/composer.go:65-67` returns true for that identity before it reads a layer's declared visibility, so every layer is visible to every caller. The route groups move as follows. On the inner mux the meta-tools, `/v1/quota`, `/v1/events`, `/objects/`, and `POST /v1/admin/reembed` move from 401 to admitted, the last through the `server.WithUnauthenticatedReembed()` that the same `cfg.identityProvider == ""` arm passes under CODE-4. `/v1/admin/grants` and `/v1/admin/show-effective` move from 401 to 403 `auth.forbidden`, because `requireAdmin` gets no carve-out (Decision 7) and `AdminAuthorize` refuses the anonymous-public identity before it reads the grant table (`pkg/registry/core/admin.go:22-23`). The `/v1/admin/tenants` routes move from 401 to 404 `registry.tenant_management_unavailable`, because `tenantAdminGate` finds no tenant router on a stack that sets no `PODIUM_MULTI_TENANT` (`pkg/registry/server/tenants.go:105-107`, `internal/serverboot/serverboot.go:1151`). On serverboot's own mux, which `withIdentityVerification` never wraps (`pkg/registry/server/server.go:397`, `internal/serverboot/serverboot.go:1204-1224`), the layer mutations and `/v1/admin/erase` move from 403 to admitted, and `GET /v1/layers` served the full layer list unauthenticated in both configurations.

    The same edit publishes the registry port on the host loopback interface, `127.0.0.1:8080:8080` in place of `8080:8080` at `docker-compose.yml:141-142`. That bounds the widened surface to the host running the stack. It breaks no documented command, because `spec/13-deployment.md:20` and the compose header comment at `docker-compose.yml:19` address the stack as `http://localhost:8080`, and `docs/deployment/clustered.md:97` names no address for it at all (`grep -n "localhost:8080" docs/deployment/clustered.md` returns nothing). It applies the rule the registry already enforces in code for the comparable posture: `pkg/registry/server/config_validate.go:96-99` refuses a non-loopback bind under public mode without an explicit opt-in.

    The same edit adds `PODIUM_DEFAULT_LAYER_VISIBILITY: "private"` to the service. `internal/serverboot/serverboot.go:1992-1997` resolves that default from the identity provider, and the resolved value reaches endpoint-registered admin layers at `pkg/registry/server/layers.go:664-675` and bootstrap layers through `defaultBootstrapVisibility` at `serverboot.go:508-523`, `:646`, and `:844`. The pin changes nothing a caller observes on this stack, because the anonymous-public identity bypasses the visibility check either way. It governs the value persisted into the `LayerConfig` row, which becomes load-bearing if an operator later adds an identity provider, and it holds §13.1.1 out of a default `spec/13-deployment.md:177` scopes to §13.10 standalone deployments. `spec/13-deployment.md:493` defines the variable as an override the registry applies verbatim regardless of the identity provider.

    No provider this build verifies at request time is reachable here. `verifiedProviders` names `injected-session-token`, `oidc-jwt`, and `trusted-headers` (`internal/serverboot/identity_verify.go:89`). `oidc-jwt` requires an https issuer (`internal/serverboot/identity_verify.go:252`) and the bundled Dex serves http. `trusted-headers` requires `PODIUM_TRUSTED_PROXY_SECRET` on a non-loopback bind (`pkg/registry/server/config_validate.go:117-130`) plus a gateway the stack does not run. `injected-session-token` requires a key set, and the seed file holds public keys alone (`pkg/identity/runtime_persist.go:53-56`), so a committed seed file satisfies the boot guard and still leaves nobody able to mint a token. Minting needs the private half on the developer's host, and the runtime image carries `podium-server` alone on a distroless base (`Dockerfile:56`, `:64`, `:71`) while `podium admin runtime register` consumes an existing PEM and generates none (`cmd/podium/admin_runtime.go:37-69`). Committing a private key is a policy the project declines rather than a technical bar; `deploy/compose/dex/config.yaml` already commits evaluation-stack credentials, asserted at `test/e2e/deployment_compose_test.go:165-169`, which requires `staticClients`, `staticPasswords`, and `alice@acme.com` in the committed config. Reaching a verifying evaluation stack requires funding a keypair generator and the `podium` CLI in the runtime image, or TLS on the bundled Dex with a trusted CA bundle in the registry image. Open question 4 records that choice.

## Spec amendment: §6.3.2 Runtime trust bootstrap

The edits below land together, in §6.3.2 of `spec/06-mcp-server.md`, in the downstream restatements that file carries in §6.9 and §6.10, and in the §14.11 pipeline-setup step in `spec/14-common-scenarios.md`.

**Edit A.** Replace the opening sentence of §6.3.2 at `spec/06-mcp-server.md:68`. The current sentence reads "The injected token is a JWT signed by a runtime-specific signing key registered with the registry one-time at runtime onboarding." The replacement paragraph, which keeps the rest of the line and the claim list that follows it unchanged:

> The injected token is a JWT signed by a runtime-specific signing key that the deployment configures the registry to trust at runtime onboarding. The registry verifies the signature on every call. Required claims:

**Edit B.** Insert the following two paragraphs into §6.3.2, immediately after "Without a registered signing key, the registry rejects with `auth.untrusted_runtime`." (`spec/06-mcp-server.md:76`) and before the `#### 6.3.2.1 Token Rotation Contract` heading (`:78`):

> **Establishing the trusted key set.** A runtime signing key is a trust anchor for the whole registry. A token signed with it carries its own `sub` and `org_id`, so the key mints an identity for any subject in any organization, including one holding a per-tenant admin grant (§4.7.2) or the instance-operator grant (§4.7.1); an already-trusted runtime can therefore present itself as any administrator. The registry takes its trusted key set from operator-supplied configuration read at startup and exposes no request-time registration API. `PODIUM_RUNTIME_KEYS_PATH` (§13.12) names the file that carries the set. Each record in that file names the runtime's issuer, its JWS algorithm, and its public key as a PKIX PEM block, and `podium admin runtime register` writes a record into it. The registry names the trusted issuers in its startup log. Under `injected-session-token` a registry whose configuration supplies no usable key refuses to start with `config.runtime_keys_unavailable` (§13.12), because such a registry rejects every call with `auth.untrusted_runtime` and can accept no first key.
>
> Every replica of a deployment (§13.1) is configured with the same trusted key set, and a key added or rotated there takes effect at each replica's next start.

**Edit C.** Replace the untrusted-runtime row of the §6.9 failure-mode table (`spec/06-mcp-server.md:324`), which currently reads "Runtime must register signing key with registry":

> | Untrusted runtime (`injected-session-token`)  | Reject with `auth.untrusted_runtime`. The deployment adds the runtime's signing key to the registry's trusted key set (§6.3.2) and restarts the registry. |

**Edit D.** Replace the `suggested_action` value in the §6.10 canonical envelope example (`spec/06-mcp-server.md:347`). The other fields of the example are unchanged:

> ```json
> {
>   "code": "auth.untrusted_runtime",
>   "message": "Runtime 'managed-runtime-x' is not registered with the registry.",
>   "details": { "runtime_iss": "managed-runtime-x" },
>   "retryable": false,
>   "suggested_action": "Add the runtime's signing key with 'podium admin runtime register --keys-file', then restart the registry."
> }
> ```

**Edit E.** Replace the first pipeline-setup step of §14.11 (`spec/14-common-scenarios.md:159`), which currently reads "1. CI obtains a runtime-issued JWT (per `injected-session-token`, §6.3.2). The runtime's signing key is registered with the registry one-time.":

> 1. CI obtains a runtime-issued JWT (per `injected-session-token`, §6.3.2). The deployment configures the registry to trust the runtime's signing key at startup (§6.3.2, §13.12).

Anchors: Edit A replaces the first sentence of the paragraph at `spec/06-mcp-server.md:68`, above the `iss`/`aud`/`sub`/`act`/`exp` bullet list. Edit B lands between `:76` and the `6.3.2.1` heading, and `:76` itself is unchanged because it stays accurate for a token whose `iss` is absent from the trusted set. Edit C replaces one table row between the token-expiry row and the untrusted-forwarded-token row. Edit D changes one field of one fenced JSON block, and the other fields of that example are unchanged. Edit E replaces the numbered step at `spec/14-common-scenarios.md:159`, above the `2. Pipeline step:` item at `:160`; the rest of §14.11 is unchanged, and the §14.11 citations in `pkg/sync/sync.go:82`, `test/e2e/sync_target_layout_test.go:9`, and their neighbours refer to the sync and lock-file steps rather than to this one, so they need no follow-up.

## Spec amendment: §13.12 `PODIUM_RUNTIME_KEYS_PATH`

**Edit A.** Replace the table lead-in at `spec/13-deployment.md:469`, adding the process qualifier and admitting `injected-session-token` to the subsection:

> The gateway-delegated providers (§6.3.3) and the `injected-session-token` provider (§6.3.2) introduce the following registry-process variables. `oidc-jwt` also reuses `PODIUM_OAUTH_AUDIENCE` (§6.3) for the `aud` claim, which it requires.

**Edit B.** Add one row to the variable table, after the `PODIUM_TRUSTED_PROXY_SECRET` row (`spec/13-deployment.md:478`):

> | `PODIUM_RUNTIME_KEYS_PATH` | Path to the JSON file holding the §6.3.2 trusted runtime signing keys, whose record format §6.3.2 defines. The registry process reads it before it binds a listener and never writes it; the MCP server does not read it. A key added to the file takes effect at the next process start. A file the registry cannot read or parse fails startup with `config.runtime_keys_unavailable` under every identity provider. Under `injected-session-token` a key set is also required, so an unset path, or a path naming a file that carries no key, fails startup with the same code; under the other providers a path naming a file that carries no key, including a missing file and an empty one, resolves to an empty key set and startup proceeds. Environment only; no config-file key. | (unset; required under `injected-session-token`) |

Anchors: Edit A replaces the sentence between the identity-provider subsection lead (`:467`) and the table header (`:471`). Edit B appends a row to the table that ends at `:478`, taking the same default-cell form the required `PODIUM_OAUTH_ISSUER` row uses at `:473`. The `identity_provider:` object sentence at `:480` is unchanged, because the variable carries no config-file key.

## Spec amendment: §13.1.1 evaluation-stack identity posture and the §7.7 `podium login` no-op predicate

The edits below land together, in §13.1.1 of `spec/13-deployment.md` and in the §7.7 `podium login` behavior sentence in `spec/07-external-integration.md`.

**Edit A.** Replace the fenced command block at `spec/13-deployment.md:18-22`, whose third line reads `podium login    # device-code flow against the bundled Dex IdP`:

> ```bash
> docker compose up -d
> podium init --global --registry http://localhost:8080
> ```

**Edit B.** Replace the `dex` bullet at `spec/13-deployment.md:29`:

> - **`dex`**: OIDC IdP, retained for device-code evaluation. The registry service selects no identity provider, so it does not consult Dex. `podium login` against this stack prints the §7.7 no-auth notice and exits, because §7.7 treats `http://localhost:8080` and `http://127.0.0.1:8080` as no-auth registries. A device-code flow against Dex requires publishing the registry at another address.

**Edit C.** Replace the `bootstrap` bullet at `spec/13-deployment.md:30`:

> - **`bootstrap`**: one-shot container that creates the MinIO bucket, then exits. The registry's OIDC client is registered declaratively in the Dex config, and the default tenant and the admin grant named by `PODIUM_BOOTSTRAP_ADMINS` are seeded by the registry at boot, consistent with §13.10 standalone self-seeding. The grant is a forward-compatibility seed. The evaluation stack selects no identity provider, so no caller presents that identity until an operator configures one.

**Edit D.** Insert after the "**Not production-grade.**" paragraph at `spec/13-deployment.md:32`:

> **The evaluation stack authenticates no caller.** The `registry` service sets no `PODIUM_IDENTITY_PROVIDER`, so the registry resolves every caller as anonymous-public, every layer is visible regardless of its declared `visibility:`, and the layer-management and erase endpoints admit any request. The service publishes its port on the host loopback interface for that reason, and it sets `PODIUM_DEFAULT_LAYER_VISIBILITY=private` so a layer registered against the stack carries a private declaration if an identity provider is added later. Of the §13.2.2 detection signals, `/healthz` reports `ready` rather than `public` on this posture, `podium status` reports the same value, and no public-mode startup banner is emitted, while the audit signals do fire: read calls record `caller.identity: "system:public"` and `caller.public_mode: true`. The registry's startup log line reads `mode=standalone`, which is not one of the §13.2.2 signals. Configure a verified provider (§6.3.2, §6.3.3) before exposing the stack beyond the host.

**Edit E.** Replace the `podium login` no-op sentence at `spec/07-external-integration.md:727`, which currently reads "`podium login` is a no-op when the resolved registry is a filesystem path (no auth) or points at a `--standalone` server (no auth). In both cases it prints a notice and exits.":

> `podium login` is a no-op when the resolved registry is a filesystem path, and when the resolved registry URL is `http://127.0.0.1:8080` or `http://localhost:8080`, which the client treats as a no-auth registry whatever that registry configures. In each case it prints a notice and exits.

Edit E is required by Edit B. §7.7 states the no-op condition in terms of what the registry is, while the §13.1.1 evaluation stack is the standard topology in single-replica form (`spec/13-deployment.md:16`, `docker-compose.yml:123`, `:125`) and runs no `--standalone` bind (`Dockerfile:71` runs `podium-server`, whose main is `serverboot.Run()` at `cmd/podium-server/main.go:20`). §7.7 as written therefore predicts a device-code flow against the compose registry, while the implementation short-circuits on the address (`cmd/podium/login.go:177-182`, consulted at `:57-59`). Restating §7.7 over the resolved URL makes one predicate serve both sections, and Edit B cross-references it rather than repeating it. `docs/reference/cli.md:120` already states the URL form of the predicate, so Edit E brings the spec into line with the documentation and the implementation and the DOCS-1 list gains no entry for it.

Anchors: Edit A replaces the fenced block between `:17` and `:23`. Edits B and C replace one list item each. Edit D inserts one paragraph between `:32` and the `## 13.2 Runbook` heading at `:34`. Edit E replaces one sentence in `spec/07-external-integration.md`, the last paragraph of §7.7 (`## 7.7 Onboarding: podium init, podium config show, podium login` at `:650`), above the `## 7.8 Marketplace Publishing` heading; the multi-endpoint paragraph at `:725` is unchanged, and no other §7.7 sentence states the predicate.

## Spec amendment: §4.7, §4.7.2, and §13.2.1 re-embed authorization

**Edit A.** Append the following to the "**Model versioning and re-embedding**" paragraph at `spec/04-artifact-model.md:756`, after the sentence ending "stale-dimension rows are purged.":

> The re-embed runs over the caller's tenant and is authorized by the per-tenant `admin` role (§4.7.2). `POST /v1/admin/reembed` rejects a caller without that role with `auth.forbidden` (§6.10), and it is a write endpoint under §13.2.1, so a read-only registry rejects it with `registry.read_only`. A registry started with no identity provider configured, or one started in public mode (§13.10), authenticates no caller, so no caller can hold the admin role and the re-embed endpoint admits the request there; the local operator owns the process and triggers the pass after a model change. Configuring an identity provider makes the gate live, whether or not the registry verifies callers itself. This exception is specific to re-embed and does not extend to the other admin-gated endpoints, whose posture is defined in §4.7.2 and §7.3.2.

**Edit B.** Add one bullet to the `admin` role list in §4.7.2, after "Trigger manual reingests across any layer in the tenant." (`spec/04-artifact-model.md:798`) and before the visibility-override bullet (`:799`):

> - Trigger a catalog re-embed after an embedding-model change (§4.7).

**Edit C.** Replace the write-endpoint sentence at `spec/13-deployment.md:40`. The sentence currently reads as a closed enumeration, and it is inaccurate in both directions: `POST /v1/admin/erase` (`pkg/registry/server/layers.go:351`), the `/v1/webhooks` receiver CRUD routes (`pkg/registry/server/webhooks.go:49`, `:132`, `:190`), and layer `restore` (`pkg/registry/server/layers.go:755`) reject with `registry.read_only` and are not named, while `freeze toggles` and `podium login`-driven token issuance are named and correspond to no route (`internal/serverboot/serverboot.go:1204-1224`, `cmd/podium/login.go:177-182`). Edit C states the rule over the `/v1` catalog and administrative endpoints, names the §6.3.1 SCIM receiver as the stated exception so the rule does not reach routes the implementation does not gate, and demotes the list to named examples, which is what makes Edit A's classification of re-embed true without naming re-embed here. Both routeless entries stay in the example list. Dropping them is a separate correction, because the same two names appear in the four documentation restatements this proposal leaves unedited (Decision 8), and the demotion to examples already stops the sentence from being read as the write set:

> When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to **read-only mode**: read endpoints (`load_domain`, `search_domains`, `search_artifacts`, `load_artifact`, `load_artifacts`) continue to serve from the replica; every `/v1` catalog and administrative endpoint that mutates registry state is rejected with the structured error `registry.read_only`. Ingest webhooks, layer admin operations, freeze toggles, admin grants, tenant management, and `podium login`-driven token issuance against the local IdP-mediated session table are named examples and do not bound the rule. The §6.3.1 SCIM 2.0 receiver at `/scim/v2/` is outside this write set; its writes are not gated by read-only mode. Each endpoint's own section states its classification, as §7.3.3 does for the tenant-management endpoints and §4.7 does for the catalog re-embed.

Anchors: Edit A extends the paragraph that begins "**Model versioning and re-embedding.**" and precedes the "### Dual-write semantics for external vector backends" heading at `:758`. Edit B lands inside the bullet list under "**The `admin` role.**" at `:794`, grouping the two trigger bullets. Nothing after `spec/04-artifact-model.md:801` changes. Edit C replaces one sentence in `spec/13-deployment.md`, the first paragraph under the `### 13.2.1 Read-Only Mode` heading at `:38`, and the health-state-machine paragraph at `:42` is unchanged. Commit `adbf25d` added `tenant management,` to the sentence as a deliverable of proposal 0002, but the same proposal also stated the classification in the endpoints' own sections at `spec/07-external-integration.md:152` and `spec/06-mcp-server.md:386`, and it is that by-reference statement Edit A follows. Commit `db941a4` is the counter-case: it added the `rejectIfReadOnly` calls for `/v1/admin/erase`, the `/v1/webhooks` receiver CRUD routes, and the layer operations, and it touched no file under `spec/` or `docs/`. After Edit C the sentence is a rule with named examples, so a change in the set edits no documentation page and no doc comment. Edit C writes `spec/13-deployment.md:40`, and the §13.1.1 amendment writes `:18-32` in the same file; the ranges do not overlap.

## Proposed solution

### CODE-1: remove the runtime-key HTTP endpoint

Delete `pkg/registry/server/runtime_endpoint.go`, `pkg/registry/server/runtime_endpoint_test.go`, and `pkg/registry/server/runtime_endpoint_paths_test.go`. The file is the escalation surface: an allow-all authorization default at line 32 that both handlers consult, on a type with no identity resolver and no audit sink. With the trust root established at boot there is nothing left for it to do, and deleting it removes the class of defect rather than gating it. Leaving an exported `NewRuntimeKeyEndpoint` with an allow-all default in `pkg/registry/server` after its only mount is gone would preserve a footgun in the library that §2.2 makes the behavioral surface, and `options_test.go:75-89` currently pins allow-all as asserted behavior.

Delete `TestRuntimeKeyEndpoint_WithAdminAuth` (`pkg/registry/server/options_test.go:53-72`) and `TestRuntimeKeyEndpoint_DefaultAdminAuthIsNoop` (`:75-89`), then remove the now-unused `github.com/lennylabs/podium/pkg/identity` import, whose only uses in that file are at `:56` and `:77`. `net/http`, `net/http/httptest`, and `strings` stay, because other tests in the file use them at `:99`, `:121`, and `:153`. `strReader` lives in `options_test.go:19`, so deleting `runtime_endpoint_paths_test.go` is safe, and `bootRuntimeEndpoint` and `errForbidden` are used only inside that file.

Narrow `pkg/identity.RuntimeKeyVerifierStore` (`pkg/identity/runtime.go:68-72`) to `All` and `JWTVerifier`. It has two consumers, `serverboot.go:1047` and `internal/serverboot/identity_verify.go:24`, and after this removal neither calls `Register`. The CLI writer uses the concrete `*FilePersistedRuntimeKeyRegistry` that `LoadFilePersistedRuntimeKeyRegistry` returns (`pkg/identity/runtime_persist.go:36`), so `Register` stays on the concrete `*RuntimeKeyRegistry` (`runtime.go:74`) and `*FilePersistedRuntimeKeyRegistry` (`runtime_persist.go:88`), and the CLI and the loader are unaffected. Rewrite the doc comment at `pkg/identity/runtime.go:64-67`, which reads "the admin register/list endpoint plus the request-time JWT verifier" and goes stale with the endpoint.

Rewrite the `ErrReadOnly` doc comment at `pkg/registry/server/readonly.go:10-15`, which enumerates write endpoints and names the call this step deletes at `pkg/registry/server/runtime_endpoint.go:74`. The enumeration is already inaccurate in the other direction, because `/v1/admin/erase` (`layers.go:351`) and the `/v1/webhooks` receiver CRUD routes (`webhooks.go:49`, `:132`, `:190`) reject without appearing in it. Replace the list with the rule, so no later change to the call sites makes the comment stale:

```go
// ErrReadOnly signals a write rejected because the registry is running
// in §13.2.1 read-only mode. It maps to the registry.read_only §6.10
// code. Every /v1 catalog and administrative handler that mutates
// registry state rejects with this single code; the §6.3.1 SCIM
// receiver is outside that set. The call sites below are the set, and
// the spec defines no separate config-rejection code.
```

The `// RuntimeKeyStore is the SPI the runtime endpoint consumes` comment at `runtime_endpoint.go:10` does not correspond to a §9.1 SPI. `spec/09-extensibility.md` names no runtime-key SPI, so the deletion needs no §9.1 amendment.

The `cmd/podium` test files that construct `NewRuntimeKeyEndpoint` are handled in CODE-3, which rewrites the CLI they exercise. CODE-1 owns the `pkg/registry/server` files alone.

### CODE-2: serverboot fails closed on the runtime key seed and drops the mount

**Hoist the path and record the load failure** at `internal/serverboot/serverboot.go:1047-1056`:

```go
// §6.3.2 runtime trust keys: the trusted key set is operator-supplied
// configuration read before the listener binds. A file the registry
// cannot read or parse is a corrupt trust anchor and aborts startup
// under every provider (§13.12). A file that carries no key, including
// an absent and an empty one, resolves to an empty key set, which only
// the injected-session-token verifier
// consults, so runtimeKeyBootstrapGuard adjudicates it inside that
// branch.
var runtimeKeys identity.RuntimeKeyVerifierStore = identity.NewRuntimeKeyRegistry()
runtimeKeysPath := os.Getenv("PODIUM_RUNTIME_KEYS_PATH")
if runtimeKeysPath != "" {
    persisted, err := identity.LoadFilePersistedRuntimeKeyRegistry(runtimeKeysPath)
    if err != nil {
        return fmt.Errorf("config.runtime_keys_unavailable: PODIUM_RUNTIME_KEYS_PATH=%q could not be read (§6.3.2): %w", runtimeKeysPath, err)
    }
    runtimeKeys = persisted
}
```

The `else` branch no longer logs a success line. The accepted-issuer log moves into the `injected-session-token` branch, where the set is meaningful.

**Add the provider-scoped empty-set guard** in `internal/serverboot/identity_verify.go`, beside `injectedTokenAudienceGuard` at `:118`:

```go
// runtimeKeyBootstrapGuard refuses startup when injected-session-token is
// selected and no trusted runtime signing key was seeded. §6.3.2 establishes
// the key set from PODIUM_RUNTIME_KEYS_PATH at boot; with an empty set the
// verifier rejects every call with auth.untrusted_runtime and the registry has
// no path to a first key. Other providers never consult the store (§13.12), so
// a file that carries no key is not a boot failure under them. A file the
// registry cannot read is rejected at the load site under every provider.
func runtimeKeyBootstrapGuard(identityProvider, path string, keys identity.RuntimeKeyVerifierStore) error {
    if identityProvider != "injected-session-token" {
        return nil
    }
    if len(keys.All()) > 0 {
        return nil
    }
    return fmt.Errorf("config.runtime_keys_unavailable: PODIUM_IDENTITY_PROVIDER=injected-session-token verifies every call against a trusted runtime signing key, and PODIUM_RUNTIME_KEYS_PATH (%q) supplied none (§6.3.2); write one with 'podium admin runtime register --keys-file'", path)
}
```

An unset path falls through to the empty-set arm, whose message already names the variable, so no separate branch is needed.

**Call it once**, after `injectedTokenAudienceGuard` at `serverboot.go:1092-1094`:

```go
if err := runtimeKeyBootstrapGuard(cfg.identityProvider, runtimeKeysPath, runtimeKeys); err != nil {
    return err
}
seeded := runtimeKeys.All()
issuers := make([]string, 0, len(seeded))
for _, k := range seeded {
    issuers = append(issuers, k.Issuer)
}
log.Printf("identity provider: injected-session-token (trusting runtime issuers %s from %s)",
    strings.Join(issuers, ", "), runtimeKeysPath)
```

The line matches the `oidc-jwt` accepted-issuers line at `serverboot.go:1128`. The issuer list is formatted at the call site from `runtimeKeys.All()` rather than through a helper, so this step adds one package-level identifier to `internal/serverboot`, `runtimeKeyBootstrapGuard`. `All` returns the keys sorted by issuer (`pkg/identity/runtime.go:96-99`), which `*FilePersistedRuntimeKeyRegistry` inherits from the embedded `*RuntimeKeyRegistry` (`pkg/identity/runtime_persist.go:26-27`), so the logged order is deterministic. `strings` is already imported at `internal/serverboot/serverboot.go:24`, and `All` survives the `RuntimeKeyVerifierStore` narrowing CODE-1 performs.

**Delete `serverboot.go:1201` and `:1213`**, the endpoint construction and the mux mount.

**Bring the §13.1.1 evaluation stack under the guard**, in the same step, because the compose file is a deployment artifact with no compiled call site and nothing else would catch it. Delete the `PODIUM_IDENTITY_PROVIDER: "injected-session-token"` line at `docker-compose.yml:132`. Change the published port at `:141-142` from `"8080:8080"` to `"127.0.0.1:8080:8080"`. Add `PODIUM_DEFAULT_LAYER_VISIBILITY: "private"` to the same environment block. Rewrite the service comment at `:104-108`, whose claim that injected-session-token is "the only mode this build verifies server-side" is false against `verifiedProviders` (`internal/serverboot/identity_verify.go:89`): the replacement states that the service selects no identity provider and therefore resolves every caller as anonymous-public, that the port publishes on the host loopback interface for that reason, that `PODIUM_DEFAULT_LAYER_VISIBILITY` holds the persisted layer declaration at `private`, and that Dex is retained for device-code evaluation but not consulted, because `podium login` against `http://localhost:8080` reports that the registry needs no authentication and exits (`cmd/podium/login.go:177-182`). Rewrite the usage header at `docker-compose.yml:17-20`, which carries the same three-command block as `spec/13-deployment.md:18-22` including `podium login    # device-code flow against the bundled Dex IdP`: drop that line so the header matches the §13.1.1 block Edit A stages, leaving `docker compose up -d` and `podium init --global --registry http://localhost:8080`. Rewrite the seeding comment at `internal/serverboot/serverboot.go:744-749`, which names the same `docker compose up` to `podium init` to `podium login` workflow as the reason the grant is seeded: the replacement states that the seed establishes the first admin grant for whatever identity provider an operator configures, and it names no `podium login` step. Rewrite the header comment at `deploy/compose/dex/config.yaml:1-9`, which opens by naming §13.1.1 and states that Dex "backs the `podium login` device-code flow (RFC 8628)" and that the issuer is reached from "the host (where `podium login` runs)": the replacement states that Dex is retained for device-code evaluation, that the registry service selects no identity provider and does not consult it, and that reaching Dex through `podium login` requires publishing the registry at an address other than `http://localhost:8080` or `http://127.0.0.1:8080` (`cmd/podium/login.go:177-182`). Leave `PODIUM_OAUTH_AUDIENCE` and `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` at `:133-134` in place, so selecting a provider later re-adds one line; both are inert on this posture, and `:134` is inert today as well, because `pkg/identity/registry.go:64` reads it only for the `oauth-device-code` provider. Decision 16 records the reasoning and open question 4 records the reviewer decision.

Test site this step owns: `test/e2e/deployment_compose_test.go`. `TestDeployCompose_RegistryServiceWiring` (`:91`, `checks` map and its loop at `:102-121`) asserts `"PODIUM_IDENTITY_PROVIDER": func(v string) bool { return v != "" }` at `:108`, which fails once the variable is gone. Remove that entry from the `checks` map and add `"PODIUM_DEFAULT_LAYER_VISIBILITY": func(v string) bool { return v == "private" }` in its place, which fails if a later edit drops the pin and lets the resolved default follow the identity-provider setting again. Add a `Ports` assertion in the same case: every published entry on the `registry` service names a host interface, and that interface is a loopback address. `composeService` already models `Ports` (`test/e2e/deployment_compose_test.go:31`), so the case needs no parser change, and it fails the moment an edit republishes the anonymous-everything registry on every interface. Add the paired runtime-key invariant as a separate case over every service in the parsed file: a service whose `PODIUM_IDENTITY_PROVIDER` is `injected-session-token` also sets a non-empty `PODIUM_RUNTIME_KEYS_PATH`, and one of its `volumes:` entries has a container-side path (the segment after the first `:`) equal to that value or a directory prefix of it. `composeService` already models `Environment` and `Volumes` (`test/e2e/deployment_compose_test.go:24-33`), so the case needs no parser change. The invariant holds vacuously while the compose registry names no provider, and it fails the moment `injected-session-token` is reinstated without a seed, which is the regression the guard would otherwise ship silently. The rest of the case is unchanged, including the Dex assertion at `:123-125`, which reads `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` and stays green because that entry stays in the compose file.

### CODE-3: `podium admin runtime register --keys-file`

`cmd/podium/admin_runtime.go:37-90` stops calling the registry and writes the seed file instead. `identity.LoadFilePersistedRuntimeKeyRegistry` plus `Register` already performs load, validate, and atomic write-back (`pkg/identity/runtime_persist.go:36-126`), so the command reuses the library rather than adding a writer.

- `register` takes `--keys-file` (required, no environment default), `--issuer`, `--algorithm`, and `--public-key-file`. It reads the PEM, parses it through `identity.ParsePublicKeyPEM` so a mismatched algorithm fails at authoring time, and calls `Register` on the loaded file registry. The exit-2 flag-validation message keeps the wording asserted at `test/e2e/standard_deployment_test.go:1309`, with `--keys-file` in place of `--registry`.
- `adminRuntimeList` and the `list` case in `adminRuntimeCmd` (`cmd/podium/admin_runtime.go:25-26`, `71-90`) are deleted, along with the `list` entry in `printGroupHelp` (`:13-16`) and the line at `cmd/podium/main.go:170`.
- The `auth.untrusted_runtime` remediation string at `pkg/registry/server/error_envelope.go:64` becomes the §6.10 text staged above, and the literals pinned at `pkg/registry/server/error_envelope_test.go:80`, `cmd/podium-mcp/error_envelope_test.go:59`, `cmd/podium-mcp/error_envelope_test.go:148`, and `test/integration/mcp_error_envelope_test.go:23` follow it.
- `tools/minttoken/main.go:49` and `:62` print the new form.

Test sites this step owns: `cmd/podium/admin_runtime_test.go` (delete; `TestAdminRuntimeRegister_End2End` at `:20` constructs `server.NewRuntimeKeyEndpoint` at `:33`, and `TestAdminRuntimeRegister_MissingFlags` at `:50` passes `--registry`), `cmd/podium/admin_runtime_persist_test.go` (rewrite), `cmd/podium/layer_subcommands_test.go:340-390` (rewrite the `--registry`-shaped cases), `cmd/podium/cli_helpers_test.go:273`, `test/e2e/plugin_spi_test.go:398-448`, and `test/e2e/standard_deployment_test.go:1275-1339`.

`cmd/podium/cli_helpers_test.go:273` is the other reference to `adminRuntimeList` in the package's test build, alongside `layer_subcommands_test.go:381`, and it sits outside the `layer_subcommands_test.go:340-390` range the list already owns. Remove the `"adminRuntimeList": adminRuntimeList,` entry from the `TestSubcommands_HelpExitsZero` map (`:255-273`); leaving it fails to compile `cmd/podium` at S5, which stops that step's unit level from running at all. `TestDispatchers_NoArgsExit2_HelpExit0` at `:285` keeps its `"adminRuntimeCmd": adminRuntimeCmd` entry at `:293`, because the `admin runtime` dispatcher group survives with `register` alone.

### CODE-4: the re-embed admin gate and read-only gate

**Record the no-caller fact at construction.** `pkg/registry/server` gains a field beside `publicMode` (`server.go:37`) and the option that sets it:

```go
// unauthenticatedReembed records that this deployment authenticates no
// caller, so no caller can hold the §4.7.2 admin role. handleReembed is
// the only reader. The zero value gates the endpoint, so a Server built
// without the option is the fail-closed case.
unauthenticatedReembed bool

// WithUnauthenticatedReembed declares that the deployment authenticates no
// caller (§13.10: no identity provider configured, or public mode), so
// POST /v1/admin/reembed admits an unauthenticated caller and the local
// operator who owns the process is the de facto admin (§4.7). handleReembed
// alone reads it. Do not consult it from requireAdmin: that reopens
// /v1/admin/grants, /v1/admin/show-effective, the receiver CRUD, and the
// as_admin override to anonymous callers (§7.3.2, §4.7.2).
func WithUnauthenticatedReembed() Option {
    return func(s *Server) { s.unauthenticatedReembed = true }
}
```

The field is written during construction and never afterwards, because an `Option` runs inside `server.New` and no handler assigns it. It is set at the sites below, and no site clears it:

- `bootstrapOptions` (`internal/serverboot/serverboot.go:2174-2183`) appends it when `c.publicMode || c.identityProvider == ""`, beside the existing `c.publicMode` arm, matching the `LayerEndpoint` admin callback at `:1195`. Every `serverboot` deployment reaches the server through `server.New(registry, bootOpts...)` (`:1164`), so that arm covers the standard, standalone, and public-mode binds.
- `server.NewFromFilesystem` (`pkg/registry/server/server.go:274`) applies it ahead of the caller's options, because a filesystem-source registry has no identity provider by definition (`spec/13-deployment.md:484`). That covers `internal/testharness/registryharness/registryharness.go:35` and the other filesystem-source constructions without a per-caller edit.

**Insert the gate** into `pkg/registry/server/server.go` at `:869`, after the existing method check and before the query parsing at `:875`:

```go
// spec: §4.7/§4.7.2 — a full pass embeds every manifest and every DOMAIN.md
// through the configured provider and purges the previous model's rows, so it
// is gated on the per-tenant admin role. A deployment that authenticates no
// caller (§13.10 standalone with no identity provider, §13.10 public mode)
// records that at boot through WithUnauthenticatedReembed, and there the
// local operator is the de facto admin.
if !s.unauthenticatedReembed {
    if err := s.requireAdmin(r); err != nil {
        writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
        return
    }
}
// spec: §13.2.1 — the pass writes vectors, so a read-only registry refuses it.
if rejectIfReadOnly(w, s.mode) {
    return
}
```

The doc comment at `server.go:868` is rewritten to state the gate rather than claim it.

**What a wrong value costs, and what observes it.** With the option absent on a no-identity-provider boot, an anonymous `POST /v1/admin/reembed` answers 403 and `podium admin reembed --registry` fails, which `test/e2e/standard_deployment_test.go:1467` and `test/e2e/vector_reembed_test.go:197` report. With the option present on a registry that does authenticate callers, an anonymous caller runs a full pass, which the end-to-end case that boots `PODIUM_IDENTITY_PROVIDER=oidc` and the integration case that builds a server without the option both report. The levels split the way `.claude/rules/test-coverage.md` requires: the boot predicate runs only inside the spawned binary and is pinned end-to-end, and the handler branch is pinned in `pkg/registry/server`.

`requireAdmin` also returns a `core.ErrUnavailable`-wrapped store failure (`admin.go:113-121`, `pkg/registry/core/admin.go:26-27`), which this preamble reports as 403 `auth.forbidden`. The siblings at `admin.go:26` and `admin.go:89` behave the same way, so this change mirrors them deliberately. Mapping `core.ErrUnavailable` to 503 `registry.unavailable` across all three sites is out of scope here.

### TEST-1: move the end-to-end suite onto the seeded bootstrap

Every `injected-session-token` end-to-end test registers its runtime key over HTTP after boot, which is the path being removed, and the same tests are the evidence that the path worked.

- Replace `injRegisterRuntime(t, srv, pemPath)` (`test/e2e/injected_token_helpers_test.go:73-84`) with `injSeedRuntimeKeys(t, pemPath) string`, which writes `[{"issuer": injIssuer, "algorithm": "RS256", "public_key_pem": <pem>}]` to a temp path and returns it.
- Every registry boot that selects `injected-session-token` takes the same edit. It calls `injSeedRuntimeKeys` above its `startServerArgs` call, and it appends `"PODIUM_RUNTIME_KEYS_PATH=" + path` to that call's environment slice. Neither half stands alone: `serverboot` reads the key set only from the path the environment variable names (`internal/serverboot/serverboot.go:1048`), so a seeded file the environment does not name is a file no process opens, and `runtimeKeyBootstrapGuard` then aborts that boot with `config.runtime_keys_unavailable` before a listener binds.
- The registry boots are the environment slices passed to `startServerArgs`. `grep -rn "PODIUM_IDENTITY_PROVIDER=injected-session-token" test/e2e/` enumerates the candidates, and the boots among them are the helpers `injServer` (`injected_token_helpers_test.go:90-103`), `bootWebhookAdminServer` (`notification_sink_helpers_test.go:319-359`), `msStartStandardServerEnv` (`standard_stack_parity_test.go:105-143`), `ruStartUpgradedServer` (`server_ops_rolling_upgrade_test.go:272-290`), `startAuthServer` (`authserver_harness_test.go:134-175`), `scimvisStartServer` (`auth_scim_visibility_test.go:37-64`), and `mlVisServer` (`multilayer_journeys_test.go:214-236`), together with the inline boots at `auth_admin_rbac_test.go:66-73` and `:203-210`, `admin_visibility_override_test.go:43-50`, `auth_idp_group_mapping_test.go:64-73`, `auth_oidc_test.go:816-823`, and `server_operations_test.go:660-666`.
- The seeded key has to be the key the caller signs with, so a helper that does not already hold the PEM gains it as a parameter. `injServer` and `mlVisServer` take `pemPath` today, and `bootWebhookAdminServer` and `startAuthServer` mint the keypair themselves (`notification_sink_helpers_test.go:325`, `authserver_harness_test.go:137`), so those helpers seed from what they already hold. `msStartStandardServerEnv`, `msStartStandardServer` (`standard_stack_parity_test.go:95-98`), `ruStartUpgradedServer`, and `scimvisStartServer` hold no PEM and gain a `pemPath` parameter, and their callers pass the path `injKeyPair` returned: `standard_stack_parity_test.go:259-261`, `lifecycle_migration_chain_test.go:140-142`, `large_resource_data_plane_test.go:85-87`, `server_ops_rolling_upgrade_test.go:358-360`, `:408-409`, `:479-481`, and `:549-550`, and `auth_scim_visibility_test.go:96-100` and `:165-169`. A helper that mints a keypair of its own instead compiles cleanly and leaves every token those tests sign untrusted.
- In `server_ops_rolling_upgrade_test.go` the two replicas of a test share one seeded path, which preserves the test's claim that one runtime key verifies against both binaries.
- Each `injRegisterRuntime` call then drops out, because the boot that precedes it seeds. The call sites are `injected_token_helpers_test.go:101`, `notification_sink_helpers_test.go:356`, `authserver_harness_test.go:160`, `multilayer_journeys_test.go:234`, `standard_stack_parity_test.go:261`, `lifecycle_migration_chain_test.go:142`, `large_resource_data_plane_test.go:87`, `server_ops_rolling_upgrade_test.go:360`, `:409`, `:481`, and `:550`, `auth_scim_visibility_test.go:100` and `:169`, `auth_admin_rbac_test.go:74` and `:211`, `admin_visibility_override_test.go:51`, `auth_idp_group_mapping_test.go:74`, `auth_oidc_test.go:824`, and `server_operations_test.go:667`. S4 deletes the function, so a site left behind fails to compile package `e2e` and stops S4's own level from running. The helper doc comments that describe the post-boot registration state the seeded form instead, for example `standard_stack_parity_test.go:88` and `server_ops_rolling_upgrade_test.go:271`.
- Some `PODIUM_IDENTITY_PROVIDER=injected-session-token` hits are not registry boots and take no seed. The MCP-bridge and CLI environments (`standard_deployment_test.go:1014`, `:1028`, `:1055`, `standard_stack_parity_test.go:210`, `:356`, `harness_materialization_test.go:1309`, and `sdk_clients_test.go:421`, `:764`, `:781`) configure a client process that reads no key set. `TestInjectedToken_AudienceUnsetFailsStartup` (`standalone_server_test.go:392-400`) boots deliberately without `PODIUM_OAUTH_AUDIENCE` and asserts `config.injected_token_audience_unset`; `injectedTokenAudienceGuard` runs ahead of `runtimeKeyBootstrapGuard` (`internal/serverboot/serverboot.go:1092-1094` and the call site CODE-2 adds after it), so that assertion holds unchanged and the case stays unseeded.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
| --- | --- | --- |
| A registry started with `injected-session-token` and no usable key set | Startup aborts with `config.runtime_keys_unavailable` naming `PODIUM_RUNTIME_KEYS_PATH`; no listener binds | §6.3.2 Edit B and the §13.12 row; `docs/reference/error-codes.md` `config.*` catalog |
| A keys file the registry cannot read or parse, under any identity provider | Startup aborts with `config.runtime_keys_unavailable` carrying the underlying read or parse error | §13.12 row; `docs/reference/error-codes.md` `config.*` catalog |
| A keys file that carries no key, including a missing file and an empty one, under `oidc-jwt`, `trusted-headers`, or a standalone bind | The key set is empty and startup proceeds, because no code path consults the store | §13.12 row |
| `docker compose up -d` against the §13.1.1 evaluation stack after this change | The registry starts, because the compose registry service selects no identity provider and the guard is provider-scoped. Before this change the stack booted and answered 401 `auth.untrusted_runtime` to the meta-tools, `/v1/quota`, `/v1/events`, `/objects/`, and `POST /v1/admin/reembed`, because it selected a verifying provider over an empty key set. After it, the meta-tools, `/v1/quota`, `/v1/events`, `/objects/`, and `POST /v1/admin/reembed` are admitted as an anonymous-public caller that sees every layer, `/v1/admin/grants` and `/v1/admin/show-effective` answer 403 `auth.forbidden`, the `/v1/admin/tenants` routes answer 404 `registry.tenant_management_unavailable`, and the `/v1/layers` mutations and `/v1/admin/erase` are admitted. The port publishes on the host loopback interface, so the surface is reachable from the host running the stack | §13.1.1 Edits A to D; Decision 16 and open question 4; `docker-compose.yml`, the `registry` service comment |
| `/healthz` on the §13.1.1 stack after this change | Reports `mode: ready`. `Server.modeBanner` returns `public` only under `s.publicMode` (`pkg/registry/server/server.go:625`, `:633-641`), which `serverboot` sets from `cfg.publicMode` alone (`internal/serverboot/serverboot.go:2177`), so a registry that resolves every caller as anonymous-public is not reported as public and no public-mode startup banner is emitted. `podium status` prints the same `ready` value, because it reads that field (`cmd/podium/status.go:88-90`). The §13.2.2 audit signals do fire: read calls record `caller.identity: "system:public"` and `caller.public_mode: true` (`pkg/registry/server/audit_context.go:62`, `:138-139`) over the identity `server.New` installs (`pkg/registry/server/server.go:259-260`). The startup log line reads `mode=standalone` (`internal/serverboot/serverboot.go:1417`, `:2128-2136`), which §13.2.2 does not name as a signal. Accepted; widening the `/healthz` mode signal is separate work | §13.1.1 Edit D; Non-goals |
| The `PODIUM_BOOTSTRAP_ADMINS` grant on the §13.1.1 stack after this change | The grant row is written at boot (`internal/serverboot/serverboot.go:748-751`) and no caller can present that identity, so it is unreachable until an operator configures a verified provider | §13.1.1 Edit C |
| A key added to the file while the registry runs | The running process keeps its boot-time set; the key takes effect at the next start, on each replica independently | §6.3.2 Edit B, second paragraph; `docs/deployment/clustered.md` |
| Two concurrent `podium admin runtime register --keys-file` invocations against one file | Undefined. `save()` rewrites the whole array from the snapshot loaded at command start and uses a fixed temp name, so one key can be lost. Accepted rather than fixed with a lockfile | `docs/reference/cli.md`, `podium admin runtime register` |
| `GET` or `POST /v1/admin/runtime` after removal, on a bind with no verifier | 404 from the registry mux | `docs/reference/http-api.md`, Admin and operations |
| `GET` or `POST /v1/admin/runtime` after removal, unauthenticated under a verifying provider | 401 `auth.untrusted_runtime`, because `pathRequiresIdentity` covers the path and the verifier runs first | §6.10 `auth.untrusted_runtime`; `docs/reference/error-codes.md` |
| A verified non-admin calls `POST /v1/admin/reembed` | 403 `auth.forbidden` | §4.7 Edit A; `docs/reference/http-api.md`, Reembed |
| An anonymous caller calls `POST /v1/admin/reembed` on a registry with no identity provider | The request is admitted; the local operator is the de facto admin. Once CODE-2 deletes the compose `PODIUM_IDENTITY_PROVIDER` line, the §13.1.1 evaluation stack is one such registry, and the same edit publishes its port on the host loopback interface | §4.7 Edit A; §13.1.1 Edit D; Decision 16; `docs/reference/http-api.md`, Reembed |
| An anonymous caller calls `POST /v1/admin/reembed` on a registry that names an identity provider this build installs no verifier for (`PODIUM_IDENTITY_PROVIDER=oidc`) | 403 `auth.forbidden`. Configuring an identity provider makes the gate live, and no caller resolves to an admin there, so the endpoint answers no one until the deployment authenticates callers. `/v1/layers` admin operations already behave this way on that bind (`internal/serverboot/serverboot.go:1195`) | §4.7 Edit A; Decision 7 |
| An anonymous caller calls `POST /v1/admin/reembed` on a public-mode registry | The request is admitted and a full pass bills the configured embedding provider. Accepted, because public mode authenticates no caller and no caller can hold the admin role. Open question 1 records the alternative | §4.7 Edit A; `docs/reference/http-api.md`, Reembed |
| `POST /v1/admin/reembed` on a read-only registry | 503 `registry.read_only` | §4.7 Edit A, which states the classification the way §7.3.3 states it for tenant management; `docs/reference/http-api.md` under "Admin and operations", whose lead-in at `:430` already carries the read-only rule for every route in the section |
| A store failure inside the re-embed admin check | Reported as 403 `auth.forbidden` rather than 503 `registry.unavailable`, matching `handleAdminGrants` and `handleAdminShowEffective` | Not stated in `spec/`; recorded here as a deliberate mirror of the sibling handlers |
| The artifact-scoped re-embed path (`ReembedOne`) | Emits no audit event, unchanged by this proposal, while the full pass emits `embedding.reembed_in_progress` and `embedding.reembed_purged` | §4.7; deferred, see Non-goals |
| A runtime key registration | Recorded in the registry startup log as the trusted issuer set, with no audit event and no §8.1 row | §6.3.2 Edit B; `docs/deployment/clustered.md` |

## Testing

Levels follow `.claude/rules/test-coverage.md`: the boot path and the CLI run inside the spawned binary and take end-to-end tests, handler branches take integration tests in `pkg/registry/server`, and pure helpers take unit tests.

**Boot guard (e2e, `test/e2e/`).**

- `// Spec: §6.3.2` — `PODIUM_IDENTITY_PROVIDER=injected-session-token` with `PODIUM_RUNTIME_KEYS_PATH` unset exits non-zero and the stderr names `config.runtime_keys_unavailable`.
- `// Spec: §13.12` — `injected-session-token` with `PODIUM_RUNTIME_KEYS_PATH` naming a file holding `[]` exits non-zero with the same code, which is the empty-set arm distinct from the unset arm.
- `// Spec: §13.12` — `PODIUM_RUNTIME_KEYS_PATH` naming a file holding malformed JSON, under `PODIUM_IDENTITY_PROVIDER` unset, exits non-zero with `config.runtime_keys_unavailable`. This pins the "under every identity provider" clause of the §13.12 row and is the case Decision 5 settles.
- `// Spec: §13.12` — `PODIUM_RUNTIME_KEYS_PATH` naming a nonexistent file, and then a file holding `[]`, under `PODIUM_IDENTITY_PROVIDER` unset, each boot and answer `/healthz`. This pins the whole "carries no key" arm of the §13.12 row against the unparseable arm, over the distinction `pkg/identity/runtime_persist.go` draws between the nil returns at `:61-63`, `:67-69`, and `:87` and the parse error at `:72`.
- `// Spec: §6.3.2` — a seeded keys file boots and the startup log names the trusted issuer, so the accepted-issuer line is observable.
- `// Spec: §13.1.1` — in `test/e2e/deployment_compose_test.go`, every service in the parsed `docker-compose.yml` that sets `PODIUM_IDENTITY_PROVIDER: injected-session-token` also sets a non-empty `PODIUM_RUNTIME_KEYS_PATH` and mounts a volume whose container-side path covers it. The case runs without Docker, and it fails on a compose file the boot guard would refuse. `TestDeployCompose_RegistryServiceWiring` drops its `PODIUM_IDENTITY_PROVIDER` presence check in the same edit, asserts `PODIUM_DEFAULT_LAYER_VISIBILITY == "private"` in its place, which pins the resolved default against the identity-provider deletion across `pkg/registry/server/layers.go:664-675` and `defaultBootstrapVisibility` (`internal/serverboot/serverboot.go:508-523`, `:646`, `:844`), and asserts that every published port on the `registry` service names a loopback host interface.

**Removed route (e2e, `test/e2e/`).**

- `// Spec: §6.3.2` — `GET` and `POST /v1/admin/runtime` return 404 on a `serve --standalone` boot, where no verifier runs and the request reaches the registry mux.
- `// Spec: §6.3.2` — `GET /v1/admin/runtime` with a valid bearer token under `injected-session-token` returns 404. Do not assert 404 for an unauthenticated caller under a verifying provider: `pathRequiresIdentity` (`pkg/registry/server/identity_verify.go:73-81`) admits no exemption for that path, so the response is 401 `auth.untrusted_runtime`.

**CLI (unit in `cmd/podium`, e2e in `test/e2e/`).**

- `// Spec: §6.3.2` — `podium admin runtime register --keys-file <tmp> --issuer alice-runtime --algorithm RS256 --public-key-file <pem>` writes the file, and a second `register` for a different issuer preserves the first record. This covers the load-modify-write path that `save()` performs from a snapshot.
- `// Spec: §6.3.2` — `register` with a PEM body that does not match `--algorithm` exits non-zero and names the parse failure, which is the authoring-time validation Decision 14 adds. This replaces `TestAdminRuntimeRegister_HappyPath`, whose fake PEM body passes only because the current CLI never parses it.
- `// Spec: §13.12` — `register` with `--keys-file` omitted exits 2 with the flag-validation message, and the message does not name `--registry`.
- `// Spec: §6.3.2` — `podium admin runtime list` is not a subcommand: the dispatcher exits 2 and the group help lists `register` alone.
- `// Spec: §6.3.2` — a keys file written by `register` boots a registry under `injected-session-token` and a token signed by that key verifies. This is the round-trip that replaces the deleted `admin_runtime_persist_test.go` server pair.

**Re-embed authorization (integration in `pkg/registry/server`, e2e in `test/e2e/`).**

- `// Spec: §4.7.2` — `bootRegistryWithAdmin(t, "", nil)` resolves every caller as anonymous-public and answers `POST /v1/admin/reembed` with 403 and the envelope code `auth.forbidden`. The helper passes no `WithUnauthenticatedReembed`, so the gate is live and the resolver supplies the identity `requireAdmin` reads.
- `// Spec: §4.7.2` — `bootRegistryWithAdmin(t, "alice", nil)`, where `alice` holds an admin grant, admits the request past the gate.
- `// Spec: §4.7` — `bootRegistryWithAdmin(t, "", nil, server.WithUnauthenticatedReembed())` admits the same anonymous request past the gate, which pins the exception the boot path sets.
- `// Spec: §4.7` — the re-embed exception is scoped: on that same server, `POST /v1/admin/grants` and `POST /v1/webhooks` still answer 403 `auth.forbidden`. The case adds `server.WithWebhooks` so the receiver route is mounted, because `pkg/registry/server/server.go:379-382` registers it only when a webhook worker is wired. This is the regression guard against reopening `proposals/0004-webhook-hardening.md:36`.
- `// Spec: §13.2.1` — in `pkg/registry/server/readonly_writes_test.go`, the file that already owns `TestAdminGrants_RejectedInReadOnly` (`:33`) and `TestWebhookReceiverWrites_RejectedInReadOnly` (`:95`), a sibling case boots `bootRegistryWithAdmin(t, "alice", nil, server.WithMode(tracker))` with the tracker in the read-only state and asserts `POST /v1/admin/reembed` answers 503 with envelope code `registry.read_only`. `rejectIfReadOnly` no-ops on a nil `ModeTracker` (`pkg/registry/server/readonly.go:73-76`), so the `WithMode` option is required for the assertion to mean anything. The gate is a handler branch reachable in process, so this level owns it and no end-to-end case is added.
- `// Spec: §4.7` — the existing cases at `pkg/registry/server/extra_handlers_test.go:66-161` run on `registryharness.New`, which builds through `server.NewFromFilesystem`, and stay green unchanged. They become the regression evidence that the filesystem-registry path stays reachable.
- `// Spec: §4.7` — a `serve --standalone` boot with `PODIUM_IDENTITY_PROVIDER=oidc`, the free-form label that installs no request-time verifier (`test/e2e/auth_oidc_test.go:1014-1023`), answers an anonymous `POST /v1/admin/reembed` with 403 `auth.forbidden`. This pins the boot predicate against the `s.idVerifier` reading it replaces, and it is end-to-end because the predicate lives in the spawned binary.
- `// Spec: §4.7` — `POST /v1/admin/reembed` on a `serve --standalone` boot with no identity provider stays reachable (`test/e2e/standard_deployment_test.go:1467-1483`), and `podium admin reembed --registry` stays green (`test/e2e/vector_reembed_test.go:197`). Update the stale "not admin-gated" comment at `standard_deployment_test.go:1471` and the "un-gated" comment at `vector_backend_config_test.go:563`, and confirm the standalone re-embed sites at `vector_backend_config_test.go:560`, `:586`, `:609`, `:620`, `:640`, `:658`, `:674`, `:689`, and `:703` stay green. A site that boots with an identity provider configured needs an admin token rather than a comment change.

**Migrated suite (e2e, `test/e2e/`).**

- `// Spec: §6.3.2` — the whole `injected-session-token` suite passes with the key seeded from `PODIUM_RUNTIME_KEYS_PATH` and no post-boot registration. Passing at all proves the file was read before the listener bound, which is why `cmd/podium/admin_runtime_persist_test.go` drops its second `serverboot.Run` and its `waitForHealthz`.

## Open questions

1. **Whether public mode is exempt from the re-embed gate.** The staged design admits the request, matching `spec/04-artifact-model.md`'s new §4.7 sentence and preserving today's behavior on every deployment where no caller can be authenticated. The alternative denies it, on the ground that public mode may bind a non-loopback address through `--allow-public-bind` (§13.10) and a full pass bills the configured embedding provider, so an anonymous re-embed costs money there in a way it does not on a loopback standalone bind. Choosing the alternative changes the boot predicate to `!cfg.publicMode && cfg.identityProvider == ""`, so a public-mode bind no longer passes `server.WithUnauthenticatedReembed()`, and it changes the staged §4.7 text. The public-mode exclusion has to be written explicitly, because dropping the `cfg.publicMode` arm on its own leaves public mode exempt: `StartupConfig.Validate` rejects public mode combined with a named identity provider (`pkg/registry/server/config_validate.go:88`, `spec/13-deployment.md:212`), and `serverboot` reads the provider straight from the environment with no default (`internal/serverboot/serverboot.go:1814`), so a public-mode boot that names no provider still satisfies `cfg.identityProvider == ""` and still receives the option. It is a reviewer decision rather than an implementor's.

2. **Removal versus gating for `/v1/admin/runtime`.** The staged design deletes the endpoint and makes the boot-time file the only path. The alternative gates it on the instance operator role (`pkg/registry/core/tenant.go:26`) with the same file as the bootstrap, which keeps live single-replica registration. It costs a second registration path, an amendment to `spec/04-artifact-model.md:780` and `spec/13-deployment.md:501` widening the operator role past `/v1/admin/tenants`, an identity resolver and an audit sink on the endpoint with a new audit event type and §8.1 row, and it leaves the per-replica inconsistency unfixed. It also does not close the escalation, because an already-trusted runtime can mint a token for the operator's subject and install further trust anchors.

3. **Whether closing the `requireAdmin` divergence is worth a follow-up.** `internal/serverboot/serverboot.go:1195` exempts the layer and erase endpoints on a no-identity-provider bind while `requireAdmin` carries no equivalent, which is why `podium layer register` succeeds on a standalone registry and `/v1/webhooks` answers 403. This proposal leaves the divergence in place. A follow-up that closes it has to argue against `proposals/0004-webhook-hardening.md:36` and `spec/07-external-integration.md:125` on their merits, and it is not decided as a side effect of a re-embed fix.

4. **Whether the §13.1.1 evaluation stack keeps verifying callers.** The staged design removes `PODIUM_IDENTITY_PROVIDER` from the compose `registry` service, so the stack boots on one command and authenticates no caller. Decision 16 states the transition per route group and the reasoning, and it records that the meta-tools, `/v1/quota`, `/v1/events`, `/objects/`, and `POST /v1/admin/reembed` answer 401 `auth.untrusted_runtime` today for the opposite reason. The cost is bounded by the loopback publish the same edit makes: the widened surface reaches the host running the stack and nothing beyond it. A reviewer who wants the evaluation stack to verify callers funds one of two missing pieces, each a separate change with its own edit sites: a keypair generator reachable from a compose service together with the `podium` CLI in the runtime image, or TLS on the bundled Dex plus a trusted CA bundle in the registry image so `oidc-jwt` clears its https issuer check (`internal/serverboot/identity_verify.go:252`). Any answer satisfies these constraints together: `docker compose up -d` alone starts the registry, which `spec/13-deployment.md:16` and `docs/deployment/clustered.md:97` both assert; no private key is committed; the persisted layer-visibility declaration stays `private`; and §13.1.1 states the posture the stack actually runs.

## Documentation changes

- `docs/reference/http-api.md:457-464` — delete the "Runtime signing keys" block, which documents the removed `POST` and `GET /v1/admin/runtime`.
- `docs/reference/http-api.md:23` — rewrite "The runtime registers its signing key with the registry one-time at runtime onboarding" to name the boot-time file: the deployment configures the registry to trust the runtime's signing key at startup through `PODIUM_RUNTIME_KEYS_PATH`, written with `podium admin runtime register --keys-file`, and the registry verifies the signature on every call.
- `docs/consuming/custom-via-sdk.md:240` — the same sentence appears verbatim on the SDK-facing identity-provider page and takes the same rewrite. A reader following it today is told to perform a registration the removal deletes.
- `docs/getting-started/how-it-works.md:409-410` — the same claim appears inside the `injected-session-token` bullet of the identity-provider list ("The runtime registers its signing key once with the registry"). It takes the same rewrite, kept to the two lines the bullet occupies.
- `docs/reference/http-api.md`, the "Reembed" block at `:449-455` — add one clause covering the no-caller exception for this endpoint. The section lead-in at `:430` already states the admin requirement and the read-only rule for every route under "Admin and operations", so re-embed conforms to it and the clause records only the exception. Do not state a general rule about admin endpoints on no-auth deployments, which would contradict `docs/deployment/operator-guide.md:153` and `docs/reference/http-api.md:118` and `:150`.
- `docs/reference/cli.md:569-584` — rewrite `podium admin runtime`. Drop the `list` subcommand and its row, replace `--registry` with a required `--keys-file` on `register`, name the command a local form alongside `podium admin erase --local`, and state that the keys file has a single writer and that concurrent `register` invocations are undefined.
- `docs/reference/cli.md:753` — add a `PODIUM_RUNTIME_KEYS_PATH` row to the environment-variable table, marked a registry-process boot setting like the `PODIUM_MULTI_TENANT` and `PODIUM_OPERATOR_ADMINS` rows beside it.
- `docs/reference/error-codes.md:17` — update the `suggested_action` in the envelope example to the §6.10 string. This lands with or after CODE-3, which changes `pkg/registry/server/error_envelope.go:64`, so the reference never describes a string the binary does not emit.
- `docs/reference/error-codes.md:56` — rewrite the `auth.untrusted_runtime` row, which repeats the "registered with the registry" claim.
- `docs/reference/error-codes.md`, after `:72` — add a `config.runtime_keys_unavailable` row matching the `config.injected_token_audience_unset` row's phrasing: "`PODIUM_IDENTITY_PROVIDER=injected-session-token` with no trusted runtime signing key: `PODIUM_RUNTIME_KEYS_PATH` is unset or names a file with no key. Also raised under any provider when the named file cannot be read or parsed."
- No page that restates the §13.2.1 write list is edited. `grep -rn "freeze toggle" . --exclude-dir=.git --exclude-dir=node_modules` returns `docs/reference/error-codes.md:151`, `docs/deployment/operator-guide.md:132`, `docs/reference/http-api.md:640`, `deploy/runbook.md:18-20`, and the code comment at `pkg/registry/server/readonly.go:13`. Edit C makes the spec sentence a rule with named examples, so each of these remains accurate as an illustration, and none of them acquires an obligation to track the call sites. `deploy/runbook.md` carries the same list and is likewise unedited; it is referenced from `docs/deployment/clustered.md:23` and is gated by no `tools/doccov` entry.
- `docs/deployment/clustered.md:97` — the paragraph states "The registry seeds the first tenant and admin grant itself at boot, from the default tenant plus the identity in `PODIUM_BOOTSTRAP_ADMINS`." Add that the evaluation stack selects no identity provider, so the seeded grant is unreachable until an operator configures one, and that the registry port publishes on the host loopback interface.
- `docs/deployment/clustered.md`, after `:171` — describe the registry-process keys file in the paragraph that already covers the registry's own `PODIUM_IDENTITY_PROVIDER`, rather than inside the consumer-side provider bullet list at `:166-169`.
- `docs/deployment/oidc/index.md:45` — reword "a key registered with the registry" and repoint the operator-guide cross-reference at the page that carries the runtime-trust setup.
- `docs/deployment/integrations.md:76` — reword "a registered runtime key for `injected-session-token`" to name the trusted key set the deployment configures at startup through `PODIUM_RUNTIME_KEYS_PATH`. It is the only line on that page carrying the claim. The `injected-session-token` row of the provider table at `:88` reads "A managed runtime signs a per-session JWT and the registry verifies it on every call.", which asserts signing and verification alone and stays accurate after every staged edit, so the row is unchanged.
- `README.md:172-173` — update "verifies tokens signed by a runtime key registered with `podium admin runtime register`" to the `--keys-file` form. `test/e2e/deployment_modes_test.go:409` carries the old spelling in a skip message and follows.
- `test/manual-validation.md`, the S12 block at `:759-773` — restructure the procedure rather than swapping the flag, because the register step has to run before the server starts. Keep `go run ./tools/minttoken --keys "$WORK/keys"`, then write the keys file with `podium admin runtime register --keys-file "$WORK/keys/runtimes.json" --issuer manual-runtime --algorithm RS256 --public-key-file "$WORK/keys/runtime-pub.pem"`, then export `PODIUM_RUNTIME_KEYS_PATH="$WORK/keys/runtimes.json"` alongside the existing `PODIUM_IDENTITY_PROVIDER` and `PODIUM_OAUTH_AUDIENCE` exports, and only then run `podium serve`. Leaving the register call where it is leaves the scenario unrunnable: `runtimeKeyBootstrapGuard` aborts that boot with `config.runtime_keys_unavailable`, so no listener binds, the `curl .../healthz` line at `:770` exhausts its retries, and the register command after it has no registry to reach. Update the step-3 prose at `:759-760` to match the new order.
- `test/manual-validation.md`, the S28 block at `:1833-1847` — apply the same restructure against its own bind address, and update the step-2 prose at `:1833-1834` in the same edit.
- `test/manual-validation.md:815` (S13) and `:2344` (S35) — both inherit the S12 procedure by reference ("register the runtime key (as in S12...)"), so the referring sentence follows the restructured block and names the pre-boot keys file rather than a post-boot registration.

`docs/reference/cli.md` is gated by `D-cli` (`tools/doccov/manifest.yaml:29`), whose test asserts the `runtime` subcommand at `test/e2e/cli_reference_test.go:273`, so that file is edited alongside the CLI reference.

## Resolved in adversarial review

Review rounds populate this section. Each pass entry states one defect in one sentence and names the live section that now carries its fix; the live section holds the citations. The three entries below are kept in full, because a reader of the staged text would otherwise find the §13.12 row, the boot guard beside it, and the narrow form of the re-embed option unexplained.

**Scope of the runtime-key load failure.** One review round revised the §13.12 row to say that an unreadable or unparseable keys file fails startup "under every identity provider", on the ground that a malformed trust anchor should never boot silently. A second round independently revised the boot guard to be provider-scoped, on the ground that a load failure under `oidc-jwt`, `trusted-headers`, or a standalone bind would block startup over a store no code path consults. The proposal takes the §13.12 row as staged, because it is normative spec text, and splits the code into two branches so it is true: the load error aborts at the load site under every provider, and the empty-set check stays inside the `injected-session-token` branch. Decision 5 records the resolution, and the third and fourth boot-guard tests pin both halves.

**Smaller reconciliations inside the staged text.** The §6.3.2 insertion named a read-back subcommand that the CLI change removes, and it deferred the on-disk record format to §13.12 while the §13.12 row cross-referenced §6.3.2 for it. The staged §6.3.2 text states the format once and names no read-back subcommand, and the §13.12 row keeps the cross-reference.

**A change item was folded rather than kept.** An earlier draft carried a separate `requireAdmin` change adding a server-wide `WithLocalOperatorAdmin` option and a `serverboot` predicate helper. Review found it reopens `/v1/webhooks` and `/v1/admin/grants` to anonymous callers, contradicting `spec/07-external-integration.md:125` and `proposals/0004-webhook-hardening.md:36`, and that a strictly smaller form exists. It is folded into CODE-4 as `server.WithUnauthenticatedReembed()`, an option `handleReembed` alone reads, and Decision 7 records why.

### Pass 1 (2026-08-18, automated)

- The re-embed no-caller predicate was read in the server from `s.idVerifier` and was wider than the staged §4.7 rule. Decision 7 and CODE-4 carry the boot-path predicate.
- Two documentation pages asserting the removed runtime-registers-its-key mechanism were in no edit list. The DOCS-1 list names both.
- The manual-validation scenarios needed reordering rather than a flag swap, because the register step has to run before the boot. The DOCS-1 list stages the restructure.
- Correction to this pass: Decision 7 now names both branches a gated `handleReembed` reaches and the mechanism that closes the gate on each.

### Pass 2 (2026-08-18, automated)

- The §14.11 pipeline-setup step carried no staged replacement text and was attributed to an edit against a different file. Edit E of the §6.3.2 amendment carries it.

### Pass 3 (2026-08-18, automated)

- The DOCS-1 list staged a rewording for a second line that carries no claim to reword. That bullet now names one line and records the other as unchanged.

### Pass 4 (2026-08-18, automated)

- Open question 1 offered an alternative predicate that leaves public mode exempt, so the two options were identical on the deployment the question is about. Open question 1 states the corrected predicate and why the public-mode exclusion has to be explicit.
- The `runtime_persist.go` load-branch citations named the wrong lines, so Decision 5 cited an error return as evidence of a nil return. Decision 5, the "Watch out for" entry, and the boot-guard test bullets cite the branches individually.

### Pass 5 (2026-08-18, automated)

- The shipped §13.1.1 compose stack was configured the way the boot guard rejects, and `docker-compose.yml` was in no edit list. Decision 16, CODE-2, the compose edge-case row, and open question 4 carry it.
- §4.7 Edit A classified re-embed as a §13.2.1 write while §13.2.1's own enumeration excluded it. Decision 8 and Edit C of the re-embed amendment carry it; Redesign 1 replaced the enumeration edit this pass staged.
- CODE-3's test-site list omitted one of the references to `adminRuntimeList`, which would have failed to compile `cmd/podium` at S5. CODE-3 names the site with its edit.
- Correction to this pass: the compose provider deletion widens the stack's posture rather than preserving it, which Decision 16 states per route group.

### Pass 6 (2026-08-18, automated)

- TEST-1 staged the seed-and-name edit for a subset of the `injected-session-token` boot sites and a bare seed call for the rest, which leaves a file no process opens. TEST-1 states the seed-and-name edit as the rule for every such boot and enumerates them.
- TEST-1's call-site list omitted one call, and the helpers it named minted keypairs of their own rather than seeding the caller's key. TEST-1 gives those helpers a `pemPath` parameter and lists every surviving call site.
- Decision 16's "before" posture over-scoped the identity-verification middleware, which wraps the inner mux alone. Decision 16 and the compose edge-case row state the transition per route group.
- Correction to this pass: the "after" half is stated per route group as well, separating the admitted set from the two admin groups that stay closed.

### Pass 7 (2026-08-18, automated)

- Deleting the compose identity provider also flipped the resolved layer-visibility default, which two enumerations stated was outside the widening. Decision 16 and CODE-2 carry the `PODIUM_DEFAULT_LAYER_VISIBILITY` pin and the structural test that holds it.

### Redesign 1 (2026-08-18, automated)

Two areas were redesigned in this round: the §13.1.1 compose identity posture, and the §13.2.1 read-only write set. Both were reworked because the proposal reasoned from a premise the working tree contradicts, and both redesigns replace a mechanism with a smaller one.

- **The compose posture was staged as a bounded trade, and both configurations fail §13.1.1 as written.** The stack does not work today, because it selects a verifying provider over an empty key set. Decision 16 states that, the published port moves to the host loopback interface, and §13.1.1 gains an amendment (S10) recording the posture, the loopback publish, and the unreachable `PODIUM_BOOTSTRAP_ADMINS` grant.
- **Pass 5 staged a documentation mirror for a spec enumeration that nothing keeps in sync.** The §13.2.1 write set is defined by the `rejectIfReadOnly` call sites, and the prose list has drifted from it in both directions with nothing failing. Edit C states the rule and demotes the list to named examples, §4.7 Edit A carries the classification by reference, and the mirror edits are withdrawn as recorded below.
- **What the redesign deleted.** Edit C's insertion of `catalog re-embed` into the §13.2.1 enumeration is gone, and with it the framing that §13.2.1 defines the write set by a maintained list. The three DOCS-1 mirror bullets are gone, as is CODE-4's obligation to touch the `ErrReadOnly` comment and the split-comment reasoning that appeared in CODE-1, CODE-4, and the Pass 5 bullet. Decision 16's derivation of why no other identity provider is reachable is compressed to one paragraph, and its "the posture change is a widening, and it is recorded here at its full extent" framing is replaced by the per-route-group statement and the loopback bound. Two candidate edits from the input specifications were dropped as defective: a bullet for `OPERATIONS.md:240-242`, which names the compose file and the `make` targets and carries no registry URL, and an in-place rewrite of the Pass 5 bullet, which this section reverses in the record instead.
- **What stays out of scope.** `freeze toggles` and `podium login`-driven token issuance name no route. `/v1/admin/erase`, the `/v1/webhooks` receiver CRUD routes, and layer `restore` reject with `registry.read_only` without being named in any enumeration. The SCIM writes at `pkg/scim/handler.go:78`, `:82`, `:84`, `:96`, `:100`, and `:102` consult no `ModeTracker`, so Edit C names the §6.3.1 receiver as the stated exception and gating those writes is separate work. `docker-compose.yml:67`, the `dex` service comment, names the `podium login` device-code flow and is staged by no edit.
- **Open decision recorded by the redesign.** Whether the compose file publishes 8080 on the host loopback interface. Option A, staged here, publishes `127.0.0.1:8080:8080`, which bounds the unauthenticated write surface to the host running the stack, costs one line, and applies the rule `pkg/registry/server/config_validate.go:96-99` already enforces for public mode; it breaks no documented command, because `spec/13-deployment.md:20` and `docker-compose.yml:19` address the stack as `http://localhost:8080` and `docs/deployment/clustered.md:97` names no address. Option B leaves `8080:8080`, which keeps the stack reachable from another machine and leaves the full cost at open question 4. Option A is applied. A reviewer who prefers Option B removes the loopback clauses from the Summary compose bullet, S6, S10, Decision 16, CODE-2, the compose edge-case row, the compose testing bullet, open question 4, and the `docs/deployment/clustered.md:97` documentation bullet, and every other change in this round stands.

### Pass 8 (2026-08-18, automated)

- The empty-keys-file arm under the non-injected providers had no landing text, so the edge-case table and Decision 5 stated an outcome the normative row did not. The §13.12 row states it, and a boot-guard test bullet pins it.
- Correction to this pass: the `return nil` citation for the `[]` body was off by one and is corrected at all three sites.

### Pass 9 (2026-08-18, automated)

- §13.1.1 Edit B stated a `podium login` no-op predicate that §7.7 contradicts, and `spec/07-external-integration.md` was in no edit list. Edit E of the §13.1.1 amendment restates the predicate over the resolved registry URL so one predicate serves both sections.

### Pass 10 (2026-08-18, automated)

- CODE-2's staged boot code called a helper no step defines, which would have failed to compile `internal/serverboot`. CODE-2 builds the issuer slice at the call site.

### Pass 11 (2026-08-19, automated)

- **This section had grown into a second copy of the staged text.** The Pass 1 to Pass 10 bodies restated the reasoning and the citations of the sections they point at, and two had become verbatim duplicates of live text: the CODE-2 log-line justification and the §13.1.1 Edit E lead-in. A citation carried in two places drifts, and this is the copy nobody re-verifies. Each pass finding is now one sentence naming the defect and the live section that carries its fix, with no citation a live section also carries. The three lead-in entries and the Redesign 1 record of withdrawn edits and of the loopback option are kept in full, because no live section states them. The pruning removed record rather than specification, so it leaves no detail to an implementor, mints no blank, and changes neither the checklist, the per-step file lists, nor the testing plan.
- **Two citation residues were corrected while the section was open.** The §13.2.1 Edit C lead-in cited the discovery-URL construction in `cmd/podium/login.go` for a claim about the no-auth-registry predicate, and it now cites `isNoAuthRegistry`. The same lead-in states why the two routeless entries stay in the staged example list, which the staged §13.2.1 text and Decision 8 previously left unreconciled.

## Non-goals

- A `RegistryStore`-backed runtime key table that would allow live, fleet-wide key registration. `pkg/identity` ships only the in-memory and per-process file registries, and `spec/13-deployment.md:5` runs 3+ replicas, so HTTP registration already reaches one replica. A shared table with its own cross-org row-level-security treatment is separate work.
- Any change to `oidc-jwt`, `trusted-headers`, or `oauth-device-code` identity resolution, or to the §6.3.1 per-request tenant selection.
- An embedding-cost quota. `QuotaLimiter` (`pkg/registry/server/rate_limit.go:93`, `:109`) gains no `AllowReembed`; the admin gate is the control this proposal adds.
- A bind restriction or proxy-secret gate on any admin endpoint. Authorization rather than network position is the control, and the §6.3.3 bind rules stay specific to `trusted-headers`.
- Flipping the allow-all `authAdmin` default in `NewLayerEndpoint` (`pkg/registry/server/layers.go:165-171`). `serverboot` always chains `WithAdminAuth` there, so the default closes no live hole and inverting it churns the layer test suite.
- A new audit event type. With registration moved to boot, the trusted issuer set is a startup fact recorded in the startup log beside the other identity-provider lines, so `pkg/audit` and the §8.1 table are unchanged.
- An audit record for the artifact-scoped re-embed path (`ReembedOne`, `pkg/registry/core/reembed.go:203-212`), which is silent today while the full pass emits `embedding.reembed_in_progress` and `embedding.reembed_purged`.
- A lockfile or unique temp name in `pkg/identity/runtime_persist.go`. Decision 10 documents the single-writer constraint instead.
- Any deprecation shim, redirect, or dual code path for the removed HTTP endpoints or the removed `--registry` form of `podium admin runtime`.
- Widening the `/healthz` mode banner so a registry that authenticates no caller is distinguishable from `ready`. `Server.modeBanner` reports `public` only under `s.publicMode` (`pkg/registry/server/server.go:625`, `:633-641`), which `serverboot` sets from `cfg.publicMode` alone (`internal/serverboot/serverboot.go:2177`). The §13.2.2 audit signals still fire on the §13.1.1 posture, so the deferral costs one of the detection signals rather than all of them.
