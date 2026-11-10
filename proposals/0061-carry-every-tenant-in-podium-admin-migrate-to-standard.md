# Proposal 0061: Carry every tenant in podium admin migrate-to-standard

- Issue: (to be filed)
- Status: Applied to spec (2026-11-09). Verified on 2026-11-09 after 3 adversarial review rounds (3 findings fixed).
- Date: 2026-11-09

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is scheduled for the 0.6.0 release. It is a pre-1.0 behavior change and lands in a MINOR bump with no compatibility flag that restores the default-org-only pump.

## Summary

**What changes.**

- §13.10 *Upgrade path* states that `podium admin migrate-to-standard` carries every tenant the standalone store holds, with each tenant's record and its manifests, layer configuration, and admin grants, that a deactivated tenant stays deactivated, that the source's record is written over a same-ID target tenant, that it removes nothing from the target store apart from the §13.4 rewrite record, and that instance-operator grants are not carried (SPEC-1).
- `cmd/podium/admin_migrate.go` plans the migration from `ListTenants` in place of the two-ID probe, into an ordered per-tenant plan, and closes the source store it opens (CODE-1).
- `cmd/podium/admin_migrate.go` writes one tenant at a time in ascending ID order, writes each tenant record with `CreateTenant` followed by `UpdateTenant` so the active flag, quotas, and `expose_scope_preview` match the source, and surfaces both errors (CODE-2).
- Unit tests in `cmd/podium/admin_migrate_test.go` pin enumeration, record fidelity, same-ID collisions, the §13.4 failure on a copied manifest at a differing hash, the marker clear ahead of every tenant write, deterministic order, resume after a partial failure, and the surfaced tenant errors. `TestDeployment_MigrateToStandard` in `test/e2e/deployment_modes_test.go` grows into a multi-tenant run through the binary (TEST-1, TEST-2, TEST-3, TEST-4).
- Scenario S96 joins `test/manual-validation.md` (MV-1).
- `docs/reference/cli.md` and `docs/deployment/single-node.md` replace the default-org-only statement, and `CHANGELOG.md` deletes it from `[Unreleased]` and adds a `### Fixed` entry (DOC-1, DOC-2, DOC-3).

**Fixed decisions.**

- D-1: enumeration through `ListTenants`, every tenant, active or deactivated; the two-ID probe and `standaloneDefaultOrgID` are deleted.
- D-2: the carried record set per tenant is the tenant record, manifests, layer configs, and admin grants; nothing else is added.
- D-3: `CreateTenant` followed by `UpdateTenant` with the source record.
- D-4: a same-ID target tenant is the same tenant, and the source record is written over its mutable fields; no preflight and no name check. A failed tenant write is a CLI exit-1 message, and no §6.10 code is added.
- D-5: the source is authoritative on collision (subject to Q-2).
- D-6: the migration deletes nothing from the target.
- D-7: write order is the marker clear (only when the plan holds a manifest), then one tenant at a time in ascending ID order: record, manifests, layer configs, admin grants.
- D-8: no checkpoint; a re-run completes a partial target.
- D-9: `CreateTenant` and `UpdateTenant` errors stop the run.
- D-10: instance-level state (operator grants, configuration files, the signing key) is not carried; no SPI method is added, and §13.10 states that operator grants are not carried.
- D-11: a single-tenant source on an unbooted target ends in the same state as today.
- D-12: target routing is unchanged, and the command gains no flag or warning for it.
- D-13: the plan output keeps its four aggregate counts.
- D-14: `ctx` first on `planMigration` and `pumpStore`, `src.Close()` deferred, and `pumpStore` split so each function stays under about 50 lines.
- D-15: SPEC-1 lands and is verified before any code, and no compatibility flag keeps the default-org-only pump.

**Watch out for.**

- **`CreateTenant` alone loses the active flag on every backend.** SQLite and Postgres omit the `active` column from the insert and default it to true (`pkg/store/sqlite.go:204-214`, `pkg/store/postgres.go:489-512`), and Memory sets `Active = true` explicitly (`pkg/store/memory.go:56-68`). All three ignore an existing ID. `UpdateTenant` alone returns `ErrTenantNotFound` for a new ID. The pump needs both calls, in that order.
- **`UpdateTenant` ignores `Name`** (`pkg/store/store.go:387-393`). A same-ID tenant keeps the target's name. This is safe because IDs are name-derived (`pkg/registry/core/tenant.go:18-19`), and it is the reason D-4 needs no name check.
- **The marker clear moves ahead of the tenant writes.** Today `CreateTenant` runs before the clear (`cmd/podium/admin_migrate.go:356-358`). After CODE-2, a failed clear writes nothing at all, and TEST-3 pins that no `tenant:`, `update:`, or `put:` call follows a failed clear.
- **The existing marker test breaks on position.** `TestPumpStore_ClearsTheMarkerBeforeTheFirstWrite` checks `calls[1] == "put"` (`cmd/podium/admin_migrate_test.go`, about `:670`). After CODE-2, `calls[1]` is `tenant:default`, so no prefix comparison rescues it. TEST-3 replaces it with an index comparison.
- **`ListTenants` orders by ID string.** Production IDs are UUIDv5 values, so the order bears no relation to tenant names. Tests that assert order use literal IDs (`a`, `b`, `c`) on a SQLite source, which declares no foreign keys and accepts them.
- **`makeStandaloneDB` is shared.** Several e2e tests call it, each for its own temporary home. TEST-4 opens and writes the store returned by its own call only.
- **Dry runs need target flags.** `--target-store` defaults to `postgres`, and the target-flag validation runs before the dry-run return (`cmd/podium/admin_migrate.go:140-147`). Every dry run in MV-1 passes `--target-store=sqlite --target-sqlite`.
- **§13.4 forbids a re-run after a registry starts on the target.** MV-1 re-runs the command before any registry starts on the target store, and a test that re-runs after a start is wrong.
- **Parts of `test/e2e` skip silently on macOS.** Run `go test ./test/e2e/ -run TestDeployment_MigrateToStandard -v` and grep the output for `SKIP` before claiming TEST-4 passed.
- **Q-2 is resolved at sign-off: D-5 ships as staged.** The source's tenant record is written over a same-ID target tenant.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §13.10 *Upgrade path* paragraph states that the migration carries every tenant.
      Levels: unit. Depends on: —
- [ ] **S2 · code** — CODE-1, CODE-2, TEST-1, TEST-3. The `ListTenants` plan, the tenant-major pump with faithful records, the multi-tenant unit test, and the order, resume, and error tests. Bundled because CODE-1 changes the plan type that `pumpStore` and the existing marker test consume, so no part compiles alone, and TEST-1 lands with the behavior it pins.
      Levels: unit, e2e. Depends on: S1
- [ ] **S3 · test** — TEST-2. Same-ID collision subtests.
      Levels: unit. Depends on: S2
- [ ] **S4 · test** — TEST-4. `TestDeployment_MigrateToStandard` carries a multi-tenant standalone store through the binary and re-runs.
      Levels: e2e. Depends on: S2
- [ ] **S5 · test** — MV-1. Scenario S96 in `test/manual-validation.md`.
      Levels: manual. Depends on: S2
- [ ] **S6 · docs** — DOC-1. The `podium admin migrate-to-standard` paragraph in `docs/reference/cli.md`.
      Levels: —. Depends on: S1
- [ ] **S7 · docs** — DOC-2. The "Migrating to clustered" paragraph in `docs/deployment/single-node.md`.
      Levels: —. Depends on: S1
- [ ] **S8 · docs** — DOC-3. The `CHANGELOG.md` `[Unreleased]` edits.
      Levels: —. Depends on: S1–S7

**Ordering constraints.** S1 lands first and passes `make speccov-drift` and `make coverage-gate` before S2 (D-15). S3, S4, and S5 are independent of one another once S2 lands. S6 and S7 are independent of each other. S8 describes the whole change and lands last. Land every step in one pull request.

## Current state and the gap

`podium admin migrate-to-standard` (§13.4, §13.10 *Upgrade path*) moves a standalone registry's state into a standard deployment. Its metadata pump reads the `default` org alone. `planMigration` probes two IDs with `GetTenant`: the default org's UUIDv5 (`standaloneDefaultOrgID()`, `cmd/podium/admin_migrate.go:23-25`) and the literal `"default"`. For each tenant it finds, it lists manifests, layer configs, and admin grants (`cmd/podium/admin_migrate.go:309-337`). It never calls `ListTenants`, and the comment at `:299-308` says "Future multi-tenant migration plugs a ListTenants enumeration here". `pumpStore` writes only those planned tenants (`:354-391`). The object pump walks the whole source object root with no tenant filter (`:395-422`); object keys are content-addressed and carry no tenant prefix (`pkg/registry/core/core.go:2067-2070`). The audit step copies the single audit file byte for byte with `copyFile` when `--target-audit-log` is set (`:205-217`, `:436-457`).

A standalone registry can hold several tenants. §13.10 runs multi-tenancy on standalone when `PODIUM_MULTI_TENANT` is true, and an operator provisions tenants at runtime through `/v1/admin/tenants` (§4.7.1 *Provisioning*, §7.3.3; `pkg/store/sqlite.go:204`, `:235`). When such a source is migrated, every tenant other than the default org arrives on the target with no tenant row, manifests, layer configs, or admin grants, while its blobs and audit events are copied. The command prints no warning: the dry-run `tenants:` line counts only the probe hits (`:155-156`). The source rows remain in the SQLite store, so the loss is a failure to carry the rows to the target. Proposal 0056 documented this limit on purpose (its D-12 and Q-3) in §13.10, `docs/reference/cli.md`, and `docs/deployment/single-node.md`. The same text appears in the code comment at `cmd/podium/admin_migrate.go:299-300` and in `CHANGELOG.md` under `[Unreleased]`.

The current pump has further defects that matter once it carries every tenant.

- `CreateTenant` omits the `active` column, which defaults to true, and it is a silent no-op on an existing ID (`pkg/store/sqlite.go:204-214`, `:69-79`; `pkg/store/postgres.go:501-509`). `pumpStore` also discards its error (`_ = target.CreateTenant`, `cmd/podium/admin_migrate.go:357`). A deactivated source tenant therefore arrives active, and a target tenant with the same ID keeps its own quotas, `expose_scope_preview`, and active flag while the source's rows are written under it.
- The pump copies by phase across all tenants (every tenant record, then all manifests, then all layer configs, then all grants), and it ranges over Go maps keyed by tenant ID (`:274-282`, `:371-390`). The write order is therefore random, and no checkpoint exists.
- `planMigration` never closes the source store it opens (`:290`), and both functions bury `context.Background()` (`:310-334`, `:355`).

The existing SPI covers the fix with no new method. `ListTenants` on SQLite returns every tenant, active or not, in ID order (`pkg/store/sqlite.go:235-257`). `UpdateTenant` writes quotas, `expose_scope_preview`, and `active` (`pkg/store/store.go:387-393`; `pkg/store/sqlite.go:262-282`; `pkg/store/postgres.go:596-621`; `pkg/store/memory.go:96-108`). The per-tenant reads and writes are keyed by the ID in each argument. Operator grants have no list method (`pkg/store/store.go:399-406`), and the boot path seeds them from `PODIUM_OPERATOR_ADMINS` (§4.7.1 *Operator role*; `internal/serverboot/serverboot.go:433-440`). Tenant IDs are name-derived UUIDv5 values (`pkg/registry/core/tenant.go:18-19`). A same-ID tenant on the target is therefore the same named tenant, which reaches the target only when a registry booted on the target store first (the default org, `internal/serverboot/orgid.go:67-75`) or when an earlier run wrote it.

## Decisions

- **D-1. Enumeration.** `planMigration` enumerates the source tenants with `src.ListTenants(ctx)` (SQLite `SELECT ... FROM tenants ORDER BY id` with no active filter, `pkg/store/sqlite.go:235-257`). It carries every tenant, active and deactivated. The two-ID probe (`standaloneDefaultOrgID()` and the literal `"default"`) is deleted, because `ListTenants` returns both IDs whenever they exist, so a single-tenant source yields the same tenant set as today. `standaloneDefaultOrgID` and the `uuid` import leave `admin_migrate.go`, and the test that used the helper calls `core.OrgIDForName("default")` instead (`pkg/registry/core/tenant.go:18`), so the derivation is not duplicated.
- **D-2. Carried record set per tenant.** The command carries the tenant record and the record kinds it carries for the default org today: manifests via `ListManifests`, layer configs via `ListLayerConfigs`, and admin grants via `ListAdminGrants`. The audit log and the blobs keep their whole-copy behavior. Domains, dependency edges, soft-deleted manifests and layer configs, and vector-outbox rows are still not carried for any tenant, as is true for the default org today (`cmd/podium/admin_migrate.go:31-34`; `pkg/store/store.go:427-429`, `:435-446`, `:470`). Extending the record set is a non-goal.
- **D-3. Tenant record fidelity.** For each tenant the pump calls `target.CreateTenant(ctx, t)` and then `target.UpdateTenant(ctx, t)` with the source record. `CreateTenant` provisions the Postgres org schema and the row (`pkg/store/postgres.go:489-512`). `UpdateTenant` writes quotas, `expose_scope_preview`, and `active`. The target row therefore matches the source row in every field the store holds, and a deactivated source tenant stays deactivated, which keeps a deactivated tenant from resolving again on the target. This uses existing SPI methods only, so no new store method or column is needed.
- **D-4. Same-ID tenant on the target.** Tenant IDs are name-derived (`core.OrgIDForName`), and `UpdateTenant` never changes a name, so a same-ID tenant on the target is the same tenant, and the source record is written over its mutable fields. The source's rows are copied under that ID, and the existing per-record rules govern them: a manifest at the same hash is a no-op, a manifest at a differing hash fails with `ErrImmutableViolation` (`ingest.immutable_violation`), and §13.4 then requires the target recreated empty. Layer configs and grants are upserts, so the last write wins. The pump performs no preflight read and no name comparison: every production path derives the ID from the name (`ProvisionTenant`, `bootstrapDefaultTenant`), so a same-ID tenant with a different name arises only from hand-edited SQL or a UUIDv5 collision. A failed tenant write is a CLI exit-1 message, and no §6.10 code is added, because the command's other failures are plain stderr messages with exit 1 (`cmd/podium/admin_migrate.go:151-153`, `:171-173`).
- **D-5. The source is authoritative on collision.** The alternative was to keep the target's record and copy only the source's deactivation. That alternative makes the outcome depend on whether a registry booted on the target store before the first run (`bootstrapDefaultTenant` seeds the default org from the target's configuration, `internal/serverboot/orgid.go:67-75`). With the source authoritative, a booted target and an unbooted one end in the same state, and a re-run converges to the source. Q-2 asks the reviewer to confirm this policy.
- **D-6. Target rows absent from the source.** The migration removes no tenant and no row from the target store. A tenant provisioned only on the target, and rows a tenant holds only on the target, are left as they are. The current pump behaves the same way. The one deletion the command performs is the §13.4 removal of the target's record of the content-hash rewrite (`DELETE FROM data_migrations WHERE name = ?`, `pkg/store/sqlite.go:589`), which is unchanged.
- **D-7. Ordering.** Writes happen in this order: (1) the clear of `DataMigrationContentHashFraming`, only when the plan holds any manifest, as today (`cmd/podium/admin_migrate.go:359-370`); (2) one tenant at a time, in ascending tenant ID order as `ListTenants` returns them, writing the tenant record (D-3), then its manifests, then its layer configs, then its admin grants. The plan becomes an ordered slice of per-tenant entries in place of the three maps (`:274-282`), so the order is deterministic and an error names the tenant it stopped in. Clearing the marker before the first write keeps the §13.4 guarantee that a pump which fails partway leaves the target unmarked, a guarantee `TestPumpStore_ClearsTheMarkerBeforeTheFirstWrite` pins. After this change the clear also precedes the tenant record writes.
- **D-8. Interruption and re-run need no checkpoint.** A run that stops partway leaves the tenants before the failing one complete, the failing tenant partly written, and the later tenants absent. §13.4 forbids starting the target registry until a run succeeds. A repeated run rewrites every record idempotently: `CreateTenant` is a no-op on an existing ID, `UpdateTenant` writes the same values, a manifest at the same hash is a no-op, and layer configs and grants are upserts. The re-run therefore completes the target. The §13.4 conditions that require recreating the target empty (an immutability failure, a registry started on the target, or the source started on the new version) are unchanged. A re-run carries a tenant provisioned on the source between runs.
- **D-9. Errors surface.** The `CreateTenant` error is no longer discarded (`cmd/podium/admin_migrate.go:357`). A `CreateTenant` or `UpdateTenant` failure stops the run with `fmt.Errorf("create tenant %s: %w", ...)` or `fmt.Errorf("update tenant %s: %w", ...)`, which `adminMigrateToStandard` prints as `error: pump store: create tenant <id>: <cause>`. On Postgres, a tenant ID that fails `orgIDPattern` (`pkg/store/postgres.go:47`) now stops the run with that error, where today its rows fail later or are skipped silently.
- **D-10. Instance-level state is not carried, and §13.10 states it.** Operator grants are cross-org rows with no list method in the SPI (`pkg/store/store.go:399-406`), and their only provisioning path is the boot seed from `PODIUM_OPERATOR_ADMINS` (`internal/serverboot/serverboot.go:433-440`). The target registry seeds its own from its environment. No `ListOperators` SPI method is added (minimal surface, §9.1). The data-migration marker is store-global, and the command keeps clearing it as today. The signing key is placed by hand, as today (`docs/reference/cli.md`, the paragraph that begins "The target registry signs with the source's key."). The runtime keys file (`PODIUM_RUNTIME_KEYS_PATH`), the SCIM directory file (`PODIUM_SCIM_STORE_PATH`), and the webhook receiver file are configuration files outside the metadata and object stores, and the command does not touch them for any tenant, as today. The spec sentence covers operator grants because §13.12 defines that variable.
- **D-11. Single-tenant behavior.** A source whose store holds only the default org yields the same tenant set, the same manifests, layer configs, and admin grants, the same blob and audit copies, and the same `source plan:` and completion output as today. The unchanged unit tests pin this: `TestAdminMigrateToStandard_PumpsMetadataAndObjects`, `_MigratesStandaloneUUIDTenant` (with the helper swap of D-1), `_PreservesAdminGrants`, and `_DryRunWritesNothing`, together with the existing assertions of `TestDeployment_MigrateToStandard` and the `manifests:     1` assertion in `test/e2e/lifecycle_migration_chain_test.go`. On an unbooted target, the documented chart path (`docs/deployment/clustered.md`, the paragraph that begins "On a chart deployment, the target's rewrite runs in the migrate Job."), the target store ends in the same state as today. The observable differences from today are these. First, on a target where a registry booted before the first run, the default org's stored quotas and `expose_scope_preview` now take the source's values in place of the values the target's boot seeded (D-5). Second, a source default org that an operator deactivated stays deactivated on the target. Q-2 records the first difference.
- **D-12. Target routing is unchanged.** Carrying a tenant's rows is necessary for it to resolve but is not sufficient: only a target started with `PODIUM_MULTI_TENANT` and a request-time identity verifier routes requests to a tenant other than the default org (§6.3.1; §13.10 *Features enabled by configuration*). The CLI reference states this. SPEC-1 does not restate it, because §4.7.1 *Provisioning* and §13.10 already state it. The command gains no flag and no warning for it, because it cannot see the target's environment.
- **D-13. Plan output is unchanged.** The dry run and the run print the same four aggregate counts (`cmd/podium/admin_migrate.go:155-156`). The `tenants:` count now covers every tenant. No per-tenant listing is added.
- **D-14. Code hygiene within the touched functions.** `planMigration` and `pumpStore` take `ctx context.Context` first, and `adminMigrateToStandard` creates `context.Background()` once and passes it. `planMigration` defers `src.Close()`, so the source handle it opens is closed. `pumpStore` delegates each tenant to `pumpTenant`, so each function stays under about 50 lines, per `.claude/rules/code-best-practices.md`.
- **D-15. Spec first.** SPEC-1 lands and is verified before CODE-1 and CODE-2, per `.claude/rules/spec-driven-development.md`. The change is a pre-1.0 behavior change in a MINOR release, with no compatibility flag that would keep the default-org-only pump.

## Spec amendment: §13.10 Standalone Deployment, upgrade path

**SPEC-1.** `spec/13-deployment.md`, §13.10 Standalone Deployment.

**Rationale.** The *Upgrade path* paragraph states the default-org-only limit that proposal 0056 added. The code change contradicts it, and the decided fix replaces it. §13.4 states no tenant limit and keeps its content-hash and re-run rules unchanged. The earlier sentence "Layer config, admin grants, and audit history are preserved" carries no tenant limit and stays. The operator-grant sentence prevents a reading of "admin grants ... are preserved" that includes instance-operator grants, which the SPI cannot list. The routing condition is not restated, because §4.7.1 *Provisioning* and the §13.10 *Features enabled by configuration* paragraph state it, the latter with the identity-verifier condition.

**Anchor.** The paragraph that begins "**Upgrade path.** A standalone deployment migrates to standard via". Keep its first two sentences, which end "embeddings are re-computed against the target vector backend on first ingest." Replace the remaining two sentences, from "The migration carries the manifests, layer configuration, and admin grants of the `default` org alone." through "are copied whole, including those of every tenant.", with the text below. The paragraph after the edit reads in full:

> **Upgrade path.** A standalone deployment migrates to standard via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (covered in §13.4). Layer config, admin grants, and audit history are preserved; embeddings are re-computed against the target vector backend on first ingest. The migration carries every tenant the standalone store holds, active or deactivated. For each tenant it carries the tenant's record, with its name, quotas, `expose_scope_preview` setting, and active state, together with the tenant's manifests, layer configuration, and admin grants. A tenant deactivated on the source stays deactivated on the target. The object-store blobs, and the audit log when the migration copies it, are copied whole. When the target store already holds a tenant under a carried tenant's ID, the migration writes the source's quotas, `expose_scope_preview` setting, and active state over the target's record and copies the source's rows under that ID. Apart from the record of the content-hash rewrite that §13.4 has it remove, the migration removes nothing from the target store. Instance-operator grants (§4.7.1 Operator role) are not carried; the target registry seeds them at boot from its own `PODIUM_OPERATOR_ADMINS` (§13.12).

**Verification after landing.** Run `make speccov-drift`. §13.10 keeps its existing citations, and TEST-1, TEST-2, and TEST-4 add `// Spec: §13.10` citations in S2 to S4.

## Proposed solution

### CODE-1. `cmd/podium/admin_migrate.go`: plan every tenant through `ListTenants`

Targets: `standaloneDefaultOrgID` (`:19-25`), the `github.com/google/uuid` import (`:13`), `adminMigrateToStandard` (`:150`, `:171`), `migrationPlan` (`:274-282`), and `planMigration` (`:284-337`).

1. Delete `standaloneDefaultOrgID` and the `github.com/google/uuid` import.
2. Replace the plan type:

   ```go
   // tenantPlan holds one source tenant and the rows the pump carries for it.
   type tenantPlan struct {
   	tenant       store.Tenant
   	manifests    []store.ManifestRecord
   	layerConfigs []store.LayerConfig
   	adminGrants  []store.AdminGrant
   }

   type migrationPlan struct {
   	tenants       []tenantPlan // ascending tenant ID, as ListTenants returns them
   	manifestCount int
   	layerCount    int
   	adminCount    int
   }
   ```

3. `func planMigration(ctx context.Context, sqlitePath string) (*migrationPlan, error)` opens the source, defers `func() { _ = src.Close() }()`, and calls `tenants, err := src.ListTenants(ctx)`, wrapping a failure as `list tenants: %w`. For each tenant `t` it calls `ListManifests(ctx, t.ID)`, `ListLayerConfigs(ctx, t.ID)`, and `ListAdminGrants(ctx, t.ID)` with the existing error wrapping (`list manifests for %s: %w`, `list layer configs for %s: %w`, `list admin grants for %s: %w`), appends a `tenantPlan`, and adds to the counts. The doc comment states that the plan enumerates every tenant in the source store, active or deactivated, in ID order, and carries each tenant's record, manifests, layer configs, and admin grants, and it carries `// Spec: §13.10`. Delete the default-org comment block at `:299-308`. Keep a one-line note that soft-deleted rows, domains, and dependency edges are not carried.
4. In `adminMigrateToStandard`, create `ctx := context.Background()` once and pass it to `planMigration` (`:150`) and `pumpStore` (`:171`). The `source plan:` print at `:155-156` is unchanged and reads `len(plan.tenants)`.
5. Update the command doc comment at `:27-34` to say that the command copies every tenant together with each tenant's manifests, layer configs, and admin grants.

Compile-required test edits in the same commit:

- `cmd/podium/admin_migrate_test.go` `TestAdminMigrateToStandard_MigratesStandaloneUUIDTenant` (about `:265-267`) replaces `standaloneDefaultOrgID()` with `core.OrgIDForName("default")` and imports `github.com/lennylabs/podium/pkg/registry/core`, which `cmd/podium/admin_tenant_test.go` already imports. The guard message names `core.OrgIDForName`.
- The `migrationPlan` literal in `TestPumpStore_ClearsTheMarkerBeforeTheFirstWrite` moves to the `tenantPlan` slice, as TEST-3 states.

### CODE-2. `cmd/podium/admin_migrate.go`: faithful tenant records and tenant-major writes

Target: `pumpStore` (`:354-391`).

`pumpStore(ctx context.Context, plan *migrationPlan, target store.Store) error` keeps the existing §13.4 marker clear first, only when `plan.manifestCount > 0`, with its existing comment and its `clear data-migration marker: %w` wrapping. It then iterates `plan.tenants` in order and calls `pumpTenant` for each, returning the first error.

```go
// pumpTenant writes one tenant's record and rows to the target. UpdateTenant
// follows CreateTenant because CreateTenant ignores an existing ID and does
// not write the active flag, so CreateTenant alone would leave a deactivated
// source tenant active and keep a same-ID target tenant's quotas and
// expose_scope_preview. IDs are name-derived, so a same-ID target tenant is the
// same tenant and the source record is written over it (§13.10).
//
// Spec: §13.10
func pumpTenant(ctx context.Context, tp tenantPlan, target store.Store) error {
	if err := target.CreateTenant(ctx, tp.tenant); err != nil {
		return fmt.Errorf("create tenant %s: %w", tp.tenant.ID, err)
	}
	if err := target.UpdateTenant(ctx, tp.tenant); err != nil {
		return fmt.Errorf("update tenant %s: %w", tp.tenant.ID, err)
	}
	for _, m := range tp.manifests {
		if err := target.PutManifest(ctx, m); err != nil {
			return fmt.Errorf("put manifest %s/%s: %w", tp.tenant.ID, m.ArtifactID, err)
		}
	}
	for _, l := range tp.layerConfigs {
		if err := target.PutLayerConfig(ctx, l); err != nil {
			return fmt.Errorf("put layer config %s/%s: %w", tp.tenant.ID, l.ID, err)
		}
	}
	for _, g := range tp.adminGrants {
		if err := target.GrantAdmin(ctx, g); err != nil {
			return fmt.Errorf("grant admin %s/%s: %w", tp.tenant.ID, g.UserID, err)
		}
	}
	return nil
}
```

The change adds no preflight read, no name comparison, and no `errors` import. The `put manifest`, `put layer config`, and `grant admin` messages keep their current text.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| A standalone store holds the default org and tenants provisioned through §7.3.3 | Every tenant arrives on the target with its record, manifests, layer configs, and admin grants; the plan prints `tenants:` with the full count | §13.10 "The migration carries every tenant the standalone store holds, active or deactivated." (SPEC-1); `docs/reference/cli.md` (DOC-1), `docs/deployment/single-node.md` (DOC-2) |
| A source tenant is deactivated | It is carried with `active = false` and does not resolve on the target until an operator reactivates it | §13.10 "A tenant deactivated on the source stays deactivated on the target." (SPEC-1); §4.7.1 "A deactivated tenant stops resolving and is treated as unprovisioned"; `docs/reference/cli.md` (DOC-1), `docs/deployment/single-node.md` (DOC-2) |
| A registry booted on the target before the first run, so the target holds the default org with its own seeded `expose_scope_preview` | The target's default org takes the source's quotas, `expose_scope_preview`, and active state | §13.10 "When the target store already holds a tenant under a carried tenant's ID, the migration writes the source's quotas, `expose_scope_preview` setting, and active state over the target's record" (SPEC-1); `docs/reference/cli.md` (DOC-1), `CHANGELOG.md` (DOC-3); Q-2 |
| The target holds a tenant, or rows, that the source lacks | Left as they are | §13.10 "Apart from the record of the content-hash rewrite that §13.4 has it remove, the migration removes nothing from the target store." (SPEC-1); `docs/reference/cli.md` (DOC-1) |
| The target holds a copied manifest at a content hash that differs from the source's | The run exits 1 with `error: pump store: put manifest <tenant>/<id>: store: immutable_violation`; the target is recreated empty before another run | §13.4 "The target store is recreated empty before the command runs again when a run fails because the target holds a copied row at a content hash that differs from the source's" (unchanged); `docs/reference/cli.md`, the unchanged paragraph that begins "No registry process runs on the target store while the command runs" (`docs/reference/cli.md:713`), which sits two paragraphs below the DOC-1 paragraph, after the content-hash-rewrite and signing-key paragraphs, and stays consistent with it, because after this change the failure can occur for any carried tenant |
| A run stops partway, after a tenant write fails | Tenants before the failing one are complete, the failing tenant is partly written, and later tenants are absent; the target registry is not started; a re-run before any start completes the target | §13.4 "The target registry is started, or restarted when it was running before the command, only after a run of the command succeeds." (unchanged); `docs/reference/cli.md`, the unchanged paragraph that begins "No registry process runs on the target store while the command runs" (`docs/reference/cli.md:713`) |
| A tenant record write fails, including a Postgres tenant ID outside `orgIDPattern` | The run exits 1 with `error: pump store: create tenant <id>: <cause>` or `update tenant <id>: <cause>` before that tenant's rows are written | §13.4 "only after a run of the command succeeds" (unchanged); the CLI prints the error, and no docs text names the message |
| The target runs single-tenant, or multi-tenant without a request-time identity verifier | Tenants other than the default org are stored on the target and receive no requests | §13.10 *Features enabled by configuration* "it routes requests to tenants only on a registry with a request-time identity verifier" (unchanged); `docs/reference/cli.md` (DOC-1), `docs/deployment/single-node.md` (DOC-2) |
| The source holds instance-operator grants | They are not copied; the target seeds its own from `PODIUM_OPERATOR_ADMINS` | §13.10 "Instance-operator grants (§4.7.1 Operator role) are not carried" (SPEC-1); `docs/reference/cli.md` (DOC-1), `docs/deployment/single-node.md` (DOC-2) |
| Dependency edges | Not copied for any tenant; the next ingest regenerates them | `docs/reference/cli.md` "Dependency edges are regenerated by the next ingest." (unchanged, kept by DOC-1) |
| A directly seeded source holds the literal `default` ID, and the target booted with the UUIDv5 default org | The target holds two tenant rows named `default`, and the literal-ID tenant's rows are reachable by no name-derived routing | No registry release writes a literal tenant ID, so no spec or docs text states this; it is recorded here so the reviewer sees it was considered |

## Testing

**IMPLEMENTOR'S CHOICE:** the field values of the manifest records, layer configs, and admin grants that TEST-1, TEST-2, and TEST-3 seed — each record carries a distinct `(tenant, artifact ID, version)` key and a non-empty `ContentHash`, layer configs carry `TenantID` equal to their tenant, and admin grants carry `OrgID` equal to their tenant.

- **TEST-1, `TestAdminMigrateToStandard_MigratesEveryTenant`** in `cmd/podium/admin_migrate_test.go` (unit, `// Spec: §13.10`). It lands after SPEC-1 and in the same commit as CODE-1 and CODE-2.
  - Seed the source SQLite with three tenants:
    - `core.OrgIDForName("default")`, named `default`, with one manifest.
    - `core.OrgIDForName("globex")`, named `globex`, created with `Quota{MaxUserLayers: 3, SearchQPS: 5}` and `ExposeScopePreview` pointing to `false`. It holds one manifest, one layer config, and one admin grant for `bob@acme.com`.
    - `core.OrgIDForName("initech")`, named `initech`, with one manifest, then deactivated with `src.DeactivateTenant`.
  - Before closing the source, record `want, _ := src.ListTenants(ctx)`.
  - Run `adminMigrateToStandard` with `--source-sqlite`, `--target-store sqlite`, and `--target-sqlite`, inside the existing `captureStdout` (`cmd/podium/config_test.go:15`) wrapped around `captureStderr`. Add no capture helper. Require rc 0, and require stdout to contain `tenants:       3` and `manifests:     3`.
  - Open the target and assert:
    1. `reflect.DeepEqual` of `dst.ListTenants(ctx)` and `want`. This one check covers ID order, name, every quota field, the tri-state `expose_scope_preview`, and `Active`, so it fails on today's code both for the missing tenants and for initech's dropped deactivation.
    2. `ListManifests` returns exactly one row for each of the three tenant IDs.
    3. `ListLayerConfigs(globex)` returns the layer, and `IsAdmin(bob@acme.com, globex)` is true.
- **TEST-2, `TestAdminMigrateToStandard_SameIDTenantOnTarget`** in `cmd/podium/admin_migrate_test.go` (unit, `// Spec: §13.10`, and `// Spec: §13.4` on the second subtest).
  - Subtest "the source record wins over a same-ID target tenant". Pre-seed the target SQLite with the default org (`core.OrgIDForName("default")`, name `default`, `ExposeScopePreview` pointing to `false`, `Quota{SearchQPS: 1}`) and a target-only tenant `core.OrgIDForName("umbrella")` that holds one manifest. The source holds the default org with nil `ExposeScopePreview`, zero quota, and one manifest. After rc 0, the target default org has `ScopePreviewEnabled() == true` and `Quota.SearchQPS == 0`, it holds the source manifest, and the umbrella tenant record and its manifest are unchanged.
  - Subtest "a copied manifest at a differing hash stops the run". Pre-seed the target with `core.OrgIDForName("globex")` (name `globex`) holding manifest `x@1.0.0` at `sha256:aa`. The source holds globex with `x@1.0.0` at `sha256:bb`. Expect rc 1, and captured stderr contains `pump store: put manifest`, the globex ID, and `immutable_violation`. The target's `x@1.0.0` still carries `sha256:aa`.
- **TEST-3, order, resume, and surfaced tenant errors** in `cmd/podium/admin_migrate_test.go` (unit). It lands in S2 because CODE-1 changes the plan literal the existing marker test builds.
  1. `markerRecorder` (about `:589-621`): record `tenant:<id>` on `CreateTenant`, `update:<id>` on `UpdateTenant`, and `put:<tenantID>` on `PutManifest`. Add `createErr` and `updateErr` fields to the same type; add no separate wrapper type.
  2. `TestPumpStore_ClearsTheMarkerBeforeTheFirstWrite`: rewrite `newPlan` as `&migrationPlan{tenants: []tenantPlan{{tenant: store.Tenant{ID: "default", Name: "default"}, manifests: [...]}}, manifestCount: 2}` and pass `ctx` to `pumpStore`. In "a failed clear stops the copy", assert that no recorded call has the prefix `tenant:`, `update:`, or `put:`, which pins the D-7 rule that the clear precedes every target write. In "the clear precedes the first copy", replace the `calls[1] == "put"` check with two checks: `calls[0] == "marker"`, and the index of the first `put:` entry is greater than 0.
  3. `TestPumpStore_WritesTenantsInIDOrderAndResumes` (`// Spec: §13.4`, `// Spec: §13.10`). Seed the source SQLite with tenants `b`, `a`, and `c` in that order, two manifests each, and build the plan through `planMigration(ctx, path)`. Set the marker applied on a Memory target and wrap it in `&markerRecorder{Store: mem, putErrOn: 3, putErr: errors.New("disk full")}`, because `PutManifest` returns a fault only when `putErr` is non-nil and the call counter equals `putErrOn` (`cmd/podium/admin_migrate_test.go:610-617`), as the existing marker test does at `:661`. Assert that the error contains `put manifest b/`, that tenant `a` holds both manifests, and that `GetTenant(c)` returns `store.ErrTenantNotFound`. A comment states that this outcome follows from the ascending-ID order D-7 fixes, which SPEC-1 does not state. Re-run `pumpStore` with a fresh recorder and no fault on the same Memory store. Assert a nil error, six manifests across the three tenants, and a cleared marker.
  4. `TestPumpStore_TenantWriteErrorsStopThePump`: a table with a `createErr` case and an `updateErr` case on a one-tenant plan for tenant `a`. Assert that the error contains `create tenant a:` or `update tenant a:` respectively, and that no `put:` call was recorded. It carries no `// Spec:` tag, because it pins error surfacing under `.claude/rules/code-best-practices.md` rather than a spec section.
- **TEST-4, `TestDeployment_MigrateToStandard`** in `test/e2e/deployment_modes_test.go` (e2e, `// Spec: §13.10`). Extend the existing test in place of adding a sibling, so the setup is not duplicated.
  - Setup. After `src := makeStandaloneDB(t)`, open `src.db` with `store.OpenSQLite`. Create tenant `core.OrgIDForName("globex")` (name `globex`) with one manifest, one layer config, and one admin grant for `bob@acme.com`. Create tenant `core.OrgIDForName("initech")` (name `initech`) and call `DeactivateTenant` on it. Close the store. Hold `targetDB` and `targetObjects := t.TempDir()` in variables so both runs use the same target.
  - First run. Run the existing command with `--target-objects targetObjects`. Keep the exit-0 and `source plan:` checks, and add `tenants:       3`. Open the target with `store.OpenSQLite` and assert that `ListTenants` returns the three IDs, that the booted default org's manifest is present under `core.OrgIDForName("default")`, that globex's manifest, layer config, and `IsAdmin(bob@acme.com, globex)` are present, that `IsAdmin(bob@acme.com, defaultID)` is false, and that initech has `Active == false`. Close the target store.
  - Second run. Run the identical command again. Expect exit 0 and unchanged `ListTenants` and per-tenant manifest counts.
  - Keep the missing-`--source-sqlite` exit-2 negative case unchanged.

**Coverage.** Run `go test -coverpkg=./... -coverprofile=cover.out ./cmd/podium/ ./test/e2e/` and confirm the changed lines in `planMigration`, `pumpStore`, and `pumpTenant` reach 85%. The `ListManifests`, `ListLayerConfigs`, and `ListAdminGrants` error branches keep their current coverage; the new `list tenants` branch is reached only by a source whose `tenants` table cannot be read, and it carries a comment naming that condition if no test reaches it. When verifying TEST-4 locally, run `go test ./test/e2e/ -run TestDeployment_MigrateToStandard -v` and grep the output for `SKIP`.

## Manual validation

**MV-1.** Append scenario S96 to `test/manual-validation.md`, after S95, in the document's existing conventions. Ports 8198 and 8199 are unused by other scenarios.

````markdown
## S96: A multi-tenant standalone registry migrates every tenant

**Goal.** Validate that `podium admin migrate-to-standard` carries every
tenant of a standalone registry that runs with `PODIUM_MULTI_TENANT=true`
behind `trusted-headers`, including a deactivated tenant, that a repeated run
before any target start leaves the same state, and that the migrated target
routes the tenant the API created and refuses the deactivated one.

**Covers.** §13.10 Upgrade path and the §13.4 re-run rule, through the
compiled binary.

**Why by hand.** The tenants are provisioned through the live §7.3.3 API, and
the migrated target is then served in multi-tenant mode. An operator reads
the plan counts, the target's tenant rows, and the layer list of a caller in
the migrated tenant. A `tenants:` count of 1, a missing `globex-<pid>` row, a
layer row under the default org's ID, `initech-<pid>|1`, or a `globex-<pid>`
list without `dave-<pid>` is the defect this scenario catches.

**Prerequisites.** A built `podium` binary on `PATH` and `sqlite3`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then serve an
   empty multi-tenant standalone registry behind `trusted-headers`.

   ```bash
   S=$$
   export PODIUM_IDENTITY_PROVIDER=trusted-headers
   export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
   export PODIUM_OPERATOR_ADMINS=alice@acme.com
   PODIUM_MULTI_TENANT=true podium serve --standalone --no-embeddings --bind 127.0.0.1:8198 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8198/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8198
   as() { curl -s -H "X-Podium-Proxy-Secret: gateway-secret" -H "X-Podium-User-Sub: $1" -H "X-Podium-User-Org: $2" "${@:3}"; }
   ```

   **Expect.** `server_alive` reports the server running, and the log carries
   `multi-tenant mode: routing requests by organization`.

2. As alice, the operator, create the tenants `globex-<pid>` and
   `initech-<pid>`. As dave in `globex-<pid>`, register a user layer. As
   alice, deactivate `initech-<pid>`. Stop the server.

   ```bash
   for n in globex initech; do
     as alice@acme.com default -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
       -d "{\"name\":\"$n-$S\"}" "$URL/v1/admin/tenants" | tee "$WORK/$n.json"
   done
   GLOBEX=$(python3 -c "import json; print(json.loads(open('$WORK/globex.json').read().rsplit('HTTP',1)[0])['id'])")
   INITECH=$(python3 -c "import json; print(json.loads(open('$WORK/initech.json').read().rsplit('HTTP',1)[0])['id'])")
   as dave@acme.com "globex-$S" -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"dave-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/notes.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   as alice@acme.com default -w "\nHTTP %{http_code}\n" -X DELETE "$URL/v1/admin/tenants/$INITECH"
   kill "$SRV"; wait "$SRV" 2>/dev/null
   mkdir -p "$WORK/objects"
   ```

   **Expect.** Both creates and the registration answer `HTTP 201`, and the
   deactivation answers a 2xx status. `$GLOBEX` and `$INITECH` are UUIDs.

3. Dry-run the migration into a SQLite target.

   ```bash
   podium admin migrate-to-standard --dry-run \
     --source-sqlite "$WORK/podium.db" --source-objects "$WORK/objects" \
     --target-store=sqlite --target-sqlite "$WORK/target.db" --target-objects "$WORK/target-objects"
   ls "$WORK/target.db"
   ```

   **Expect.** The plan prints `tenants:       3` and `layer configs: 1`, and
   `ls` reports that `$WORK/target.db` does not exist. A `tenants:` count of 1
   is the defect.

4. Run the migration, then read the target's tenant rows and layer rows.

   ```bash
   podium admin migrate-to-standard \
     --source-sqlite "$WORK/podium.db" --source-objects "$WORK/objects" \
     --target-store=sqlite --target-sqlite "$WORK/target.db" --target-objects "$WORK/target-objects"; echo "exit=$?"
   sqlite3 "$WORK/target.db" "select name, active from tenants order by name;"
   sqlite3 "$WORK/target.db" "select tenant_id, id from layer_configs;"
   echo "globex id: $GLOBEX"
   ```

   **Expect.** `exit=0`. The tenant rows are `default|1`, `globex-<pid>|1`,
   and `initech-<pid>|0`. One layer row reads `$GLOBEX|dave-<pid>`. A missing
   `globex-<pid>` row, a layer row under the default org's ID, or
   `initech-<pid>|1` is the defect.

5. Before any registry starts on the target, run step 4's commands again.

   **Expect.** `exit=0` and the same rows as step 4. A different result means
   the re-run is not idempotent (§13.4).

6. Serve the target in multi-tenant mode. As dave in `globex-<pid>`, list
   layers. As dave in `initech-<pid>`, register a layer.

   ```bash
   PODIUM_SQLITE_PATH="$WORK/target.db" PODIUM_FILESYSTEM_ROOT="$WORK/target-objects" PODIUM_MULTI_TENANT=true \
     podium serve --standalone --no-embeddings --bind 127.0.0.1:8199 > "$WORK/target.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8199/healthz
   server_alive "$SRV" "$WORK/target.log"
   as dave@acme.com "globex-$S" "http://127.0.0.1:8199/v1/layers" \
     | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])"
   as dave@acme.com "initech-$S" -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"dave2-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/x.git\",\"ref\":\"main\"}" \
     "http://127.0.0.1:8199/v1/layers"
   ```

   **Expect.** The `globex-<pid>` list names `dave-<pid>`. The
   `initech-<pid>` registration answers `HTTP 403` with error code
   `auth.forbidden`, because a deactivated tenant does not resolve, as S90
   step 5 observes for an organization that names no tenant. A `globex-<pid>`
   list without `dave-<pid>`, or an `initech-<pid>` registration that
   succeeds, is the defect.

**Cleanup.** Stop the server with `kill "$SRV"` and `rm -rf "$WORK"`.
````

## Documentation changes

### DOC-1. `docs/reference/cli.md`, `podium admin migrate-to-standard`

Anchor: the paragraph that begins "Manifests, layer configs, admin grants, and content blobs are copied." Replace its first three sentences, from "Manifests, layer configs, admin grants, and content blobs are copied." through "which holds every tenant's events, is copied.", with the text below. Keep the rest of the paragraph unchanged, from "Dependency edges are regenerated by the next ingest." onward.

> The command carries every tenant in the source store, active or deactivated: each tenant's record (its name, quotas, `expose_scope_preview` setting, and active state) and its manifests, layer configs, and admin grants. The content blobs are copied whole, and with `--target-audit-log` the whole audit log is copied. When the target store already holds a tenant under a source tenant's ID, the command writes the source's quotas, `expose_scope_preview` setting, and active state over it. Apart from its record of the rewrite of stored content hashes, which the next paragraph describes, the command removes nothing from the target store. Instance-operator grants are not copied; the target seeds them from its own `PODIUM_OPERATOR_ADMINS`. A tenant other than the `default` org receives requests on the target only when the target runs in multi-tenant mode with an identity provider it verifies (see `PODIUM_MULTI_TENANT` under [CLI environment variables](#environment-variables)).

The signing key keeps its own paragraph ("The target registry signs with the source's key."). The runtime keys file and the SCIM directory file are not named, because their handling is unrelated to the tenant limit and SPEC-1 does not state it. If Q-2 is resolved with the alternative policy, the same-ID sentence changes to match.

### DOC-2. `docs/deployment/single-node.md`, "Migrating to clustered"

Anchor: the paragraph that begins "The migration carries the artifact manifests, layer configuration, and admin grants of the `default` org alone." Replace the whole paragraph with:

> The migration carries every tenant on the single node, with each tenant's artifact manifests, layer configuration, admin grants, quotas, and active state, and it copies the artifact content blobs and, with `--target-audit-log`, the whole audit log. A clustered target routes requests to a tenant other than the `default` org only when it is started with `PODIUM_MULTI_TENANT` and an identity provider that authenticates each caller. Instance-operator grants come from the target's own `PODIUM_OPERATOR_ADMINS`.

`docs/deployment/progressive-adoption.md` is left unchanged (see Non-goals).

### DOC-3. `CHANGELOG.md` `[Unreleased]` entry

1. In the `### Documentation` bullet that begins "**Multi-tenancy, freeze windows, SCIM, and outbound webhooks are documented as running on every registry server**", delete only the sentence that begins "`podium admin migrate-to-standard` carries the manifests, layer configuration, and admin grants of the `default` org alone" and ends "and the spec and the single-node page now say so." Leave the rest of the bullet unchanged.
2. Under `### Fixed`, add:

> - **`podium admin migrate-to-standard` carries every tenant** (§13.10): the metadata pump read the `default` org alone, so a source store holding other tenants left their records, manifests, layer configuration, and admin grants behind while it copied their content blobs and, with `--target-audit-log`, their audit events. The command now enumerates every tenant in the source store and carries each tenant's record, including its quotas, `expose_scope_preview` setting, and active state, with its manifests, layer configuration, and admin grants. A deactivated source tenant stays deactivated on the target. When the target already holds a tenant under the same ID, the source's record replaces the target's, so a target whose registry booted before the migration takes the source default org's quotas and `expose_scope_preview` setting. Instance-operator grants come from the target's `PODIUM_OPERATOR_ADMINS`.

If Q-2 is answered with the alternative policy (keep the target's record and copy only the deactivation), rewrite the collision sentence to match.

## Open questions

- **Q-2. Collision policy and the single-tenant guarantee.** With D-5 (source authoritative), a single-tenant migration into a target where a registry booted before the first run makes the target default org's quotas and `expose_scope_preview` the source's values in place of the values the target's boot seeded. On the documented unbooted-target path (`docs/deployment/clustered.md`, the chart migration paragraph), the result is identical to today. The alternative keeps the target's record on a same-ID collision and applies only the source's deactivation. It matches today's booted-target outcome exactly, but the result then depends on whether the target booted first. The recommendation is D-5. Resolved at sign-off: D-5 ships as staged.

## Non-goals

- Carrying soft-deleted manifests or layer configs (`ListManifestsIncludingDeleted`, `ListDeletedLayerConfigs`, `pkg/store/store.go:427-429`, `:470`), domain records (`:435-436`), dependency edges, or vector-outbox rows. The pump leaves these out for every tenant, the default org included, today. Extending the record set is separate work.
- Adding a Store SPI method to enumerate operator grants, or copying the source's operator grants. The target's `PODIUM_OPERATOR_ADMINS` is their only provisioning path (D-10).
- Copying the runtime keys file, the SCIM directory file, the webhook receiver file, or the signing key. These stay operator-placed.
- Filtering the object-store or audit-log copy by tenant. Both remain whole copies.
- Changing the §13.4 re-run, immutability, marker, or sign-stored-rows rules.
- Adding a per-tenant listing to the dry-run output, a new flag, a tenant-selection flag, or a warning about the target's `PODIUM_MULTI_TENANT` setting.
- Deleting target tenants or rows absent from the source.
- Refusing a same-ID target tenant whose name differs from the source's. Every production path derives the ID from the name and `UpdateTenant` never renames, so the state arises only from hand-edited SQL or a UUIDv5 collision (D-4).
- Carrying rows stored under a tenant ID that has no tenant record in the source. Such rows are not carried today and are not carried after this change.
- Relaxing the "Multi-tenant from the start" advice in `docs/deployment/progressive-adoption.md`. That page gives advice and does not state the limit, and changing the advice is a separate editorial choice.
- Making the Memory store a migration target. `openTargetStore` accepts only `postgres` and `sqlite` (`cmd/podium/admin_migrate.go:341-352`).

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-09)

- Draft open question Q-1 asked whether the relayed instruction "F1: leave the spec as is" named this item. F1 names the break-glass `--approver` docs and code mismatch, and the relayed instruction schedules this item for 0.6.0, so SPEC-1 stays and Q-1 is deleted.
- SPEC-1's draft sentence "The migration removes no tenant and no row from the target store" contradicted §13.4, which has the command remove the target's record of the content-hash rewrite. SPEC-1, DOC-1, and D-6 now except that record.
- SPEC-1's draft sentence about a run "repeated under the conditions §13.4 sets for a re-run" referred to conditions §13.4 does not set and misdescribed a same-hash manifest write as a rewrite. It is removed.
- SPEC-1's draft routing sentence repeated §4.7.1 *Provisioning* and §13.10 *Features enabled by configuration* while omitting the identity-verifier condition. It is removed, and DOC-1 and DOC-2 state the full condition.
- CODE-2's draft preflight and name-conflict refusal guarded a state no production path produces, because tenant IDs are name-derived and `UpdateTenant` never renames. The preflight, its TEST-2 subtest, and the refusal sentences in SPEC-1, DOC-1, and DOC-3 are removed, D-4 states the reasoning, and D-7 no longer lists a preflight step. The draft's `pumpTenant` also lacked its final `return nil`.
- TEST-1's draft offered to add a stdout capture helper, which already exists, asserted a cross-tenant grant check that passes on today's code, and checked record fields one at a time. It now reuses `captureStdout`, drops the vacuous check, and compares `ListTenants` results whole.
- TEST-3's draft claimed the existing `calls[1] == "put"` check survives with a prefix comparison. It does not, because `tenant:default` now sits at that position, so TEST-3 compares indexes and also pins that a failed clear precedes every tenant write.
- TEST-4's draft added a sibling test that booted a second standalone server. It now extends `TestDeployment_MigrateToStandard`, holds the target objects directory in a variable for the second run, and moves the `SKIP` grep to the verification notes.
- MV-1's draft dry run omitted the target flags and would exit 2, re-ran the migration after starting a registry on the target against §13.4, used a port another scenario holds, deactivated a tenant it never created, and added a single-tenant restart outside this change. S96 fixes each.
- DOC-1's draft repeated the copied record kinds, named configuration files unrelated to the tenant limit, and omitted the identity-provider condition. DOC-3's draft said audit events were always copied, promised a code-comment trail it did not change, and narrowed the defect to a source started with `PODIUM_MULTI_TENANT`. Both are corrected.
- TEST-2 gained a subtest for a copied manifest at a differing hash, because no migration test pins the §13.4 immutability failure and D-4 relies on it.

### Pass 1 (2026-11-09, automated)

- TEST-3 item 3 built the `markerRecorder` with `putErrOn: 3` and no `putErr`, so no fault fired, the pump wrote every tenant, and the partial-failure and resume assertions could not hold (`cmd/podium/admin_migrate_test.go:610-617`). TEST-3 item 3 now builds `&markerRecorder{Store: mem, putErrOn: 3, putErr: errors.New("disk full")}`, matching the existing use at `:661`, and its assertions are unchanged.
- The edge-case rows for a differing content hash and for a run that stops partway said no docs page states the recreate-empty rule and named `docs/deployment/clustered.md` for the start-only-after-success rule. `docs/reference/cli.md:713` states both, and the clustered migration section states neither. Both rows now cite the `docs/reference/cli.md` paragraph that begins "No registry process runs on the target store while the command runs", and the differing-hash row notes that the paragraph sits below the DOC-1 edit and must stay consistent with it.
- The differing-hash edge-case row said the `docs/reference/cli.md:713` paragraph sits directly below the DOC-1 paragraph. DOC-1 anchors on `docs/reference/cli.md:707`, and the content-hash-rewrite paragraph (`:709`) and the signing-key paragraph (`:711`) sit between them. The row now says the paragraph sits two paragraphs below the DOC-1 paragraph, after those two paragraphs, which agrees with the Pass 1 bullet that says it sits below the DOC-1 edit.
