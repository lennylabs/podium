# Proposal 0055: Retract the audit transparency-log anchoring claims and restate the opening sentence of §4.7.9

- Issue: (to be filed)
- Status: Approved (2026-11-08). Signed off as staged. Verified on 2026-11-08 after 3 adversarial review rounds (2 findings fixed).
- Date: 2026-11-08

This document stages spec, documentation, and Go comment changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off. No runtime behavior changes.

## Summary

**What changes.**

- §13.10 drops transparency-log anchoring and its "Sigstore stack" parenthetical from the standalone out-of-scope list, and gains one sentence saying that §8.6 local chain-head anchoring runs on a standalone server (SPEC-1). §13.11.3 drops the item from the remote-server-only list (SPEC-2).
- The opening sentence of §4.7.9 stops claiming author-key signing at commit time and states what the registry signs at ingest under each signing mode (SPEC-3).
- `README.md`, `docs/overview.md`, `docs/deployment/index.md`, `docs/getting-started/how-it-works.md`, `docs/deployment/local.md`, `docs/deployment/single-node.md`, `docs/deployment/progressive-adoption.md`, and `site/src/content/landing.ts` stop listing transparency-log anchoring as a clustered feature. The progressive-adoption control becomes local chain-head anchoring, stated with its file-sink condition and its trust limits (DOC-1 through DOC-4).
- Go comments in `pkg/audit`, `pkg/notification`, `pkg/registry/core`, and `internal/serverboot`, and two `test/e2e` file comments, describe local chain-head anchoring and stop listing anchor failure as an operational notification (CODE-1 through CODE-4, TEST-1).
- `CHANGELOG.md` gains a `### Documentation` group under `[Unreleased]` (DOC-5).

**Fixed decisions.**

- Retract the claims. Podium does not gain public transparency-log anchoring. This is maintainer decision D1 and is closed (D-1).
- §8.6, the §12 risk row, the §13.12 Audit anchoring table, the `PODIUM_SIGSTORE_*` rows, and the rest of §4.7.9 stay unchanged (D-2).
- The `docs/deployment/index.md` and `docs/deployment/local.md` lines that proposal 0035 deferred drop the item, because no tier offers transparency-log anchoring (D-3).
- Go changes are comments only. `extractRekorLogIndex`, the `log_index` Context key, and the -1 sentinel stay (D-4).
- Anchor failure is removed from the notification lists. It is not renamed, and the notification wiring is not added (D-5).
- Only §13.10 gains the local-anchoring sentence. §13.11.3 changes only its list item (D-6).
- §4.7.9 changes only its opening sentence, which names the signing mode and cites §13.12 (D-7).
- No new error code, variable, key, flag, HTTP member, SDK surface, heading, or behavioral test (D-8).
- The spec steps land and are verified before the comment and docs steps (D-9).
- The release vehicle is 0.5.3. The CHANGELOG entry goes under `[Unreleased]` (D-10).

**Watch out for.**

- **Two spec sentences that mention transparency-log anchoring stay.** §8.6 ("distinct from the transparency-log anchoring recommended above") and the §12 risk-table row ("periodic transparency-log anchoring recommended") are accurate. The closing grep in Testing hits exactly these two lines, and that result is the expected one.
- **The anchoring sentences in DOC-3, DOC-4, and DOC-5 carry the file-sink condition.** With an `http://` or `https://` `PODIUM_AUDIT_LOG_PATH`, the registry logs that anchoring is disabled and starts (`internal/serverboot/audit_anchor.go:84-88`). A sentence that names only the interval overstates when anchoring runs.
- **Do not replace anchor failure with ingest failure in the `Registry` comments.** The ingest-failure notification travels through `LayerEndpoint` (`pkg/registry/server/layers.go:2071-2080`, wired at `internal/serverboot/serverboot.go:1691`), and `Registry.Notify` has no non-test caller. CODE-3 removes the example lists from those comments.
- **SPEC-3 says "signing mode".** `podium serve --sign` takes precedence over `PODIUM_SIGN` (§13.12), so a sentence that names the variable alone is incomplete.
- **The dormant Rekor-index tests in `pkg/audit/anchor_test.go` stay, with their comments.** Their comments are conditional on a signer whose envelope carries a `tlog` object, and they describe that library path accurately. TEST-1 edits no line in that file.
- **SCIM is listed as standalone-out-of-scope in §13.10 and as remote-only in §13.11.3, while `docs/getting-started/how-it-works.md` says SCIM group push runs on a single node.** This proposal does not reconcile that. Leave those items as they are.
- **Line numbers drift.** Anchor each edit by the quoted text in this proposal, and treat the cited line numbers as hints.
- **`site/src/content/landing.ts` has no snapshot test.** `site/test/pages.test.tsx:439-469` asserts structural classes only. Run `npm test` and `npm run check` under `site/` anyway, because the array literal must stay valid TypeScript.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2. §13.10 drops the item and its parenthetical and gains the local-anchoring pointer, and §13.11.3 drops the item. Bundled because both edit `spec/13-deployment.md` and the same reader checks that the point appears once.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-3. The opening sentence of §4.7.9 states what the registry signs at ingest under each signing mode.
      Levels: —. Depends on: —
- [ ] **S3 · code** — CODE-1. The `Anchor` doc comment describes local chain-head anchoring, and the `extractRekorLogIndex` comment gains one sentence.
      Levels: unit. Depends on: S1, S2
- [ ] **S4 · code** — CODE-2. The `Scheduler`, `Run`, and `EventAuditAnchorFailed` comments drop the transparency-log and `podium admin anchor` wording.
      Levels: unit. Depends on: S1, S2
- [ ] **S5 · code** — CODE-3. The notification comments drop anchor failure, and the `Registry` comments carry no example list.
      Levels: unit. Depends on: S1, S2
- [ ] **S6 · code** — CODE-4. The `internal/serverboot` comments say "local chain-head anchoring".
      Levels: unit. Depends on: S1, S2
- [ ] **S7 · test** — TEST-1. The two `test/e2e` file comments stop describing transparency-log signing and anchor-failure notification.
      Levels: e2e. Depends on: S1, S2
- [ ] **S8 · docs** — DOC-1. The README, the deployment index, the overview, and the landing page drop the item from the clustered tier.
      Levels: unit (site). Depends on: S1, S2
- [ ] **S9 · docs** — DOC-2. `how-it-works.md` and `local.md` drop the item from the clustered additions.
      Levels: —. Depends on: S1, S2
- [ ] **S10 · docs** — DOC-3. `single-node.md` rewrites "What the tier omits" as prose.
      Levels: —. Depends on: S1, S2
- [ ] **S11 · docs** — DOC-4. `progressive-adoption.md` replaces the control with local chain-head anchoring.
      Levels: —. Depends on: S1, S2
- [ ] **S12 · docs** — DOC-5. The CHANGELOG `### Documentation` group under `[Unreleased]`.
      Levels: —. Depends on: S1–S11

**Ordering constraints.** S1 and S2 land first, each in its own commit, and `make coverage-gate` passes on each before any later step (D-9). S3 through S11 are independent of one another. S12 describes the whole change and lands last. Land every step in one pull request.

## Current state and the gap

### What the binary does

§8.6 recommends public-log anchoring for high-assurance deployments ("Periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended"). The mechanism §8.6 specifies is local chain-head anchoring: the registry signs `sha256:<chain head>` with a dedicated Ed25519 anchor key and appends an `audit.anchored` event, and "Local anchoring submits nothing to a transparency log".

The code matches §8.6.

- `loadAnchorSigner` returns only a `sign.RegistryManagedKey`, loaded or generated at `PODIUM_AUDIT_SIGNING_KEY_PATH`, default `~/.podium/standalone/audit.key` (`internal/serverboot/audit_anchor.go:77-105`).
- serverboot loads it at `internal/serverboot/serverboot.go:988` and passes it to `startAnchorScheduler` and `newReAnchor` (`internal/serverboot/serverboot.go:1708-1710`). Those two functions are the non-test callers of `audit.Scheduler` and `audit.Anchor` (`internal/serverboot/audit_anchor.go:152`, `:179`).
- No mode check gates the path, so a standalone server anchors whenever the interval is above 0 and the audit sink is a file. `test/e2e/audit_signing_journey_test.go` boots `serve --standalone` with anchoring on.
- The registry-managed envelope holds `key_id` and `signature` alone (`pkg/sign/registry_managed.go:96-113`), so `extractRekorLogIndex` returns -1 on every registry anchor (`pkg/audit/anchor.go:88-97`).
- Rekor submission happens on the `podium sign` Sigstore-keyless path alone (`pkg/sign/sigstore.go`, `pkg/sign/rekor.go:79`, `cmd/podium/sign.go:253-264`).
- No `podium admin anchor` command exists (`cmd/podium/admin.go:47-68`).
- `startAnchorScheduler`'s `OnFailure` calls `log.Printf` and nothing else (`internal/serverboot/audit_anchor.go:156-158`). `Registry.Notify` has no non-test caller. The wired operational notification is the ingest failure that `LayerEndpoint` fires (`pkg/registry/server/layers.go:2076`). An anchor failure therefore never reaches the NotificationProvider.

### Where the text overclaims

- **Spec.** §13.10 lists transparency-log anchoring among standalone features "present in the binary but inert without the supporting infrastructure (... a Sigstore stack for transparency anchoring, ...)" (`spec/13-deployment.md:258`). §13.11.3 lists it under features that "require specifically a remote server" (`spec/13-deployment.md:353`).
- **Docs.** `README.md:139`, `docs/overview.md:72`, `docs/deployment/index.md:15`, `docs/getting-started/how-it-works.md:230-232`, `docs/deployment/local.md:162`, `docs/deployment/progressive-adoption.md:108`, `docs/deployment/single-node.md:158`, and `site/src/content/landing.ts:391`.
- **Go comments.** `pkg/audit/anchor.go:12-17`, `pkg/audit/scheduler.go:10-17` and `:30-32`, `pkg/audit/audit.go:63-66`, `pkg/notification/notification.go:2-5`, `pkg/registry/core/core.go:91-93`, `:342-344`, and `:350-353`, `internal/serverboot/audit_retention.go:108-112`, `internal/serverboot/serverboot.go:2083`, `test/e2e/audit_signing_journey_test.go:3-4`, and `test/e2e/operational_notifications_test.go:5-7`.

The comments at `pkg/audit/anchor.go:23-26` and `:77-86`, and those in `pkg/audit/anchor_test.go`, describe `Anchor` and `extractRekorLogIndex` for any signer and are accurate. They stay.

The overclaim is a security defect. A reader of §13.10, §13.11.3, or `docs/deployment/progressive-adoption.md:108` expects publicly verifiable evidence of the audit chain state that holds across organizational boundaries. Podium produces an anchor signed by the operator's own key. Anyone holding that key can sign again. The signature covers the digest bytes alone, and `anchored_at` is event metadata outside the anchor signature (`pkg/audit/anchor.go:43-55`).

### The opening sentence of §4.7.9

§4.7.9 opens: "Each artifact version is signed by the author's key at commit time, or by a registry-managed key at ingest." Nothing signs at commit time, and no ingest path uses an author key. The ingest signing path is `pkg/registry/ingest/ingest.go:790-806`, which signs `content_hash` with the registry signer when one is set. `registrySignerFor` returns no signer under `none` and the registry-managed key otherwise (`internal/serverboot/signing.go:19-21`, `:83-92`). §13.12 accepts `registry-key` or `none` for `PODIUM_SIGN`. The same section already says that no registry signing mode produces a Sigstore-keyless signature, that a stored keyless or author-key signature is neither served nor verified on materialization, and that `podium sign` signs outside the ingest flow. §4.7.10 makes the registry-managed key the delivery signer. Git history (7e5dd7c3) shows the sentence was carried over unchanged when `spec.md` was split.

## Decisions

- **D-1. Retract (maintainer decision D1, closed).** Podium does not gain public transparency-log anchoring of the audit chain head. Public-log anchoring stays a §8.6 recommendation and may be proposed later as a feature.
- **D-2. Accurate text stays.** These are unchanged: the §8.6 recommendation and the §8.6 "Local chain-head anchoring" paragraph (`spec/08-audit-and-observability.md:79`, `:81`), the §12 risk-table row (`spec/12-operational-risks-and-mitigations.md:25`), the §13.12 Audit anchoring table (`spec/13-deployment.md:541-549`), the `PODIUM_SIGSTORE_*` rows (`spec/06-mcp-server.md:38-44`), the §4.7.9 key-model, keyless-envelope, and verification text (`spec/04-artifact-model.md:913-950`), `docs/deployment/clustered.md:33` (corrected by proposal 0035), `docs/deployment/operator-guide.md:249-254`, `docs/reference/cli.md:791-798` and `:906-908`, and `docs/reference/error-codes.md:90-92`.
- **D-3. Settles the item proposal 0035 deferred.** Proposal 0035 left `docs/deployment/index.md:15` and `docs/deployment/local.md:162` for this decision (`proposals/0035-refuse-startup-on-an-unusable-scim-store-path-and-on-an-unus.md:520`). No tier offers transparency-log anchoring, so both lines drop the item.
- **D-4. Comments only.** Identifiers, signatures, control flow, and the `audit.anchored` event Context stay. `extractRekorLogIndex`, the `log_index` key, and the -1 sentinel remain as dormant library behavior for a caller that passes a signer whose envelope carries a `tlog` object. The `extractRekorLogIndex` comment gains one sentence saying the registry's anchor key produces -1. Q-1 asks whether to remove the path.
- **D-5. Anchor failure leaves the notification lists.** The comments that list "transparency-anchor failure" or "anchor failure" as an operational notification drop the item, because no anchor failure reaches the NotificationProvider. Renaming it to "chain-head anchor failure" would keep a false claim. Wiring anchor failures into notifications is a non-goal.
- **D-6. One local-anchoring sentence, in §13.10.** In §13.10 and §13.11.3 only the anchoring item changes. §13.10 gains one sentence saying that §8.6 local chain-head anchoring runs on a standalone server as on any other registry process, citing §13.12 for the conditions. §13.11.3 changes only its list item, because §8.6 and §13.12 already state where local anchoring runs and that it submits nothing to a transparency log. The other list items and unrelated wording on those lines ("deployment shape" and the em-dash at `spec/13-deployment.md:258`) stay.
- **D-7. §4.7.9 changes its opening sentence alone.** The new sentence states that the registry signs each artifact version it ingests with its registry-managed key while the signing mode resolves to `registry-key`, signs none at ingest under `none`, and that `podium sign` signs outside the ingest flow. It cites §13.12 and says "signing mode", because `podium serve --sign` takes precedence over `PODIUM_SIGN`. Verification on materialization stays with the existing paragraph that states it with its policy exceptions. The later "author-key" mentions are negative statements that stay accurate.
- **D-8. No new surface and no test obligation.** No §6.10 error code, `PODIUM_*` variable, `registry.yaml` key, CLI flag, HTTP member, SDK surface, or spec heading changes. `speccov-drift` reads headings and test annotations (`tools/internal/specparser/specparser.go:117`, `:376`, `:403`), and the audited matrices exclude §4.7.9, §8.6, §13.10, and §13.11.3 (`tools/matrix/matrices.go:47-183`). The rewording creates no spec-coverage or matrix obligation, and no behavioral test is added.
- **D-9. Spec first.** Following `.claude/rules/spec-driven-development.md`, the spec edits land and are verified in their own commits (S1, S2) before the comment and docs edits.
- **D-10. Release vehicle.** The change ships in 0.5.3, as the user directed in the implementation plan (item 1b). It is documentation and comments only and goes in the CHANGELOG under `### Documentation` in `[Unreleased]`, which release prep moves into the release section.

## Spec amendment: §13.10 Standalone Deployment, "Out of scope for standalone"

**SPEC-1.** `spec/13-deployment.md`, §13.10, the paragraph that begins "**Out of scope for standalone.**" (line 258). Replace the whole paragraph.

Current text:

> **Out of scope for standalone.** Multi-tenancy, freeze windows, SCIM, transparency-log anchoring, outbound webhooks. These are present in the binary but inert without the supporting infrastructure (an IdP for SCIM, a Sigstore stack for transparency anchoring, etc.). They can be enabled individually when their dependencies are available. Vulnerability scanning is out of scope for every deployment shape, not just standalone — see §1.1, §4.7.7.

Replacement:

> **Out of scope for standalone.** Multi-tenancy, freeze windows, SCIM, and outbound webhooks. These are present in the binary but inert without the supporting infrastructure (an IdP for SCIM, etc.). They can be enabled individually when their dependencies are available. §8.6 local chain-head anchoring runs on a standalone server as on any other registry process (§13.12). Vulnerability scanning is out of scope for every deployment shape, not just standalone — see §1.1, §4.7.7.

The edit removes the list item and the "Sigstore stack" parenthetical, adds the conjunction before the last list item, and adds the pointer sentence. The last sentence is unchanged (D-6). No sentence about public transparency logs is added here, because §8.6 already states that local anchoring submits nothing to one.

## Spec amendment: §13.11.3 What's Available, remote-server list

**SPEC-2.** `spec/13-deployment.md`, §13.11.3, the second bullet under "Features that require **specifically a remote server** (not just any server):" (line 353). Replace that bullet alone.

Current text:

> - Multi-tenancy, SCIM, and transparency-log anchoring.

Replacement:

> - Multi-tenancy and SCIM.

No paragraph is added after the bullet list. §8.6 and §13.12 state where local chain-head anchoring runs, and SPEC-1 adds the standalone pointer in §13.10 (D-6).

## Spec amendment: §4.7.9 Signing, opening sentence

**SPEC-3.** `spec/04-artifact-model.md`, §4.7.9, the first paragraph under the heading (line 911). Replace the paragraph and keep "Two key models:" at its end, followed by the existing bullets.

Current text:

> Each artifact version is signed by the author's key at commit time, or by a registry-managed key at ingest. Two key models:

Replacement:

> The registry signs each artifact version it ingests with its registry-managed key while the signing mode resolves to `registry-key`, the default, and signs none at ingest under `none` (§13.12). `podium sign` signs outside the ingest flow (below). Two key models:

The paragraph on the signature the registry mints at ingest (line 934) keeps its statement of what a consumer verifies, including the `never` exception. The `podium verify` and `podium sign` paragraph (line 950) is the "below" the new sentence points to.

## Proposed solution

Each edit below changes comment text alone. After each code step, run `gofmt -l` on the touched files and `go vet` on the touched packages.

### CODE-1. `pkg/audit/anchor.go`

Replace the first paragraph of the `Anchor` doc comment (lines 12-17, from "// Anchor records the §8.6 transparency-anchor" through "// recorded at that time.") with:

```go
// Anchor records a §8.6 local chain-head anchor. It signs
// "sha256:<chain head>" with signer and appends the envelope in an
// audit.anchored event, so an auditor holding the signer's public key
// can check that the key attested that head. The registry passes its
// dedicated Ed25519 anchor key, and Anchor submits nothing to a
// transparency log. The signature covers the digest alone; the event's
// timestamp and anchored_at are hash-chained with the event but are not
// covered by the anchor signature.
```

The second and third paragraphs (lines 19-26) stay.

In the `extractRekorLogIndex` comment, after the first paragraph (ending "or does not decode." at line 80), append to that paragraph:

```go
// The registry's anchor key is a RegistryManagedKey, so registry anchors
// record -1.
```

The zero-based paragraph and the `// Spec: §8.6.` tag stay.

### CODE-2. `pkg/audit/scheduler.go` and `pkg/audit/audit.go`

Replace the `Scheduler` doc comment (`pkg/audit/scheduler.go:10-17`) with:

```go
// Scheduler runs Anchor periodically against the supplied sink. It
// implements the periodic §8.6 local chain-head anchor that
// PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS enables.
//
// The scheduler is best-effort: a failed attempt (a chain-head read,
// signing, or sink write error) is not fatal, and the next tick
// retries. Operators monitor the audit log for `audit.anchored` events
// and the `audit.anchor_failed` events the scheduler emits on failure.
```

Replace the `Run` doc comment (`pkg/audit/scheduler.go:30-32`) with:

```go
// Run blocks until ctx is canceled, calling Anchor every Interval.
// One immediate Anchor fires before the first sleep so an anchor of
// the current head exists promptly after startup.
```

Replace the `EventAuditAnchorFailed` comment (`pkg/audit/audit.go:63-66`) with:

```go
// EventAuditAnchorFailed records a failed periodic §8.6 chain-head
// anchor attempt (a chain-head read, signing, or sink write error) so
// operators monitoring the audit log, and SIEM mirrors, see anchoring
// failures rather than relying solely on process logs. spec: §8.6.
```

The `OnFailure` field comment ("the integration seam for tests + operational notifications") describes a seam a caller may use and stays.

### CODE-3. `pkg/notification/notification.go` and `pkg/registry/core/core.go`

`pkg/notification/notification.go:2-5` becomes:

```go
// Providers deliver operational notifications (ingest failure,
// embedding-provider outage past N minutes, layer auto-disable on
// force-push) to a fanout target configured by the operator via
// PODIUM_NOTIFICATION_PROVIDER.
```

The `notifier` field comment (`pkg/registry/core/core.go:91-93`) becomes:

```go
	// notifier delivers §9 operational notifications through the
	// configured NotificationProvider. Nil = no outbound notifications.
```

The `WithNotifier` doc comment (`pkg/registry/core/core.go:342-344`) becomes:

```go
// WithNotifier wires the §9 NotificationProvider so the registry can
// fire operational notifications through Notify without callers having
// to subscribe to the audit log.
```

The `Notify` doc comment (`pkg/registry/core/core.go:350-353`) becomes:

```go
// Notify is the registry-internal entry point that forwards to the
// configured notifier (no-op when none is wired). Exported so
// neighboring packages can fire events through the same channel.
```

The `Registry` comments carry no example list. The ingest-failure notification is delivered by `LayerEndpoint` (`pkg/registry/server/layers.go:2071-2080`, wired at `internal/serverboot/serverboot.go:1691`) rather than through the `Registry`'s notifier, so naming ingest failure there would replace one false example with another. The other items in the `notification.go` list are not re-verified (non-goal).

### CODE-4. `internal/serverboot/audit_retention.go` and `internal/serverboot/serverboot.go`

Replace the bullet at `internal/serverboot/audit_retention.go:108-112` ("The §8.6 transparency-anchor markers ... reconciliation trail.") with:

```go
//   - The §8.6 chain-head anchor and integrity markers (audit.anchored /
//     anchor_failed / gap_detected) and the §8.4/§8.6 audit.retention_enforced
//     boundary marker record the chain state a verifier needs to reconcile
//     an anchor it holds of a superseded head; aging them out would erase
//     the reconciliation trail.
```

At `internal/serverboot/serverboot.go:2083`, `// §8.6 transparency anchoring.` becomes `// §8.6 local chain-head anchoring.`, matching the existing wording at `internal/serverboot/serverboot.go:1702` and `internal/serverboot/audit_anchor.go:147`.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| An operator wants the audit chain head in a public transparency log | No deployment submits it. Accepted (D-1); a later feature proposal may add it | §8.6 "Local anchoring submits nothing to a transparency log and is distinct from the transparency-log anchoring recommended above"; `docs/deployment/single-node.md` "What the tier omits" (DOC-3), `docs/deployment/progressive-adoption.md` (DOC-4), `docs/deployment/clustered.md` "Audit across replicas" |
| Anchoring on a standalone server | Runs when the interval is above 0 and the sink is a file, as on any registry process | §13.10 "§8.6 local chain-head anchoring runs on a standalone server as on any other registry process (§13.12)" (SPEC-1); `docs/deployment/single-node.md` (DOC-3) |
| `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` unset or 0 | No anchor is written and the anchor key is never read | §13.12 "A value of 0 disables anchoring ... With anchoring disabled, the anchor key is never read"; `docs/reference/cli.md` `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` row |
| Interval above 0 with an `http://` or `https://` `PODIUM_AUDIT_LOG_PATH` | The registry logs that anchoring is disabled and starts. No anchor is written | §13.12 "With an `http://` or `https://` `PODIUM_AUDIT_LOG_PATH`, the registry logs that anchoring is disabled and starts"; `docs/reference/cli.md` row, `docs/deployment/progressive-adoption.md` (DOC-4) |
| A clustered replica that streams its audit to a SIEM endpoint | That replica writes no anchor | §13.12 as above; `docs/deployment/clustered.md` "Audit across replicas" |
| Anyone holding the anchor key signs a different head | The signature verifies under the anchor key's public half. The evidence stays within the operator's trust domain. Accepted (D-1) | §8.6 "An auditor verifies an anchor with the anchor key's public half"; `docs/deployment/progressive-adoption.md` (DOC-4) |
| An auditor asks when an anchor was made | `anchored_at` and the event timestamp are hash-chained with the event and outside the anchor signature. The anchor does not prove a time. Accepted (non-goal) | §8.6 "The anchor signature covers the digest bytes alone"; `docs/deployment/progressive-adoption.md` (DOC-4) |
| A periodic anchor attempt fails | An `audit.anchor_failed` event is appended and the failure is logged. No operational notification is sent. Accepted (D-5) | §8.6 "A failed periodic attempt is recorded as `audit.anchor_failed`"; no docs page, and no docs page claims a notification |
| A registry anchor's `audit.anchored` Context | `log_index` is -1 on every registry anchor. Accepted (D-4, Q-1) | No spec text names `log_index`; no docs page, internal |
| Ingest under the `registry-key` signing mode (the default) | Each ingested version carries a registry-managed signature over its `content_hash` | §4.7.9 opening sentence (SPEC-3); `docs/deployment/single-node.md` (DOC-3) |
| Ingest under the `none` signing mode | No ingest signature is minted | §4.7.9 opening sentence (SPEC-3), §13.12 `PODIUM_SIGN` row; `docs/deployment/operator-guide.md` signing refusals |
| A stored Sigstore-keyless or author-key signature on materialization | Neither served nor verified | §4.7.9 "is neither served nor verified on materialization"; no change, `docs/reference/cli.md` `podium verify` |
| Other items in the §13.10 and §13.11.3 lists (freeze windows, SCIM, outbound webhooks) | Unchanged and not re-verified. Deferred (non-goal) | §13.10, §13.11.3 as they stand |

## Testing

No behavioral test is added (D-8). The proposal changes no behavior, so there is no new non-happy path to pin. Each restated claim is already pinned by an existing test, which the implementor confirms passes unchanged:

- §13.10 local anchoring on standalone: `TestAuditSigningJourney_SignAnchorVerifyRedact` in `test/e2e/audit_signing_journey_test.go`, which boots `serve --standalone` with anchoring on and carries `// Spec: §8.6`. Run it with `-v` and confirm it does not report `SKIP`, because parts of `test/e2e` skip silently on macOS.
- §8.6 anchor contract and the -1 sentinel: `TestAnchor_RecordsChainHeadAndAppendsEvent` and the `extractRekorLogIndex` cases in `pkg/audit/anchor_test.go`.
- §4.7.9 ingest signing: the `// Spec: §4.7.9` tests in `pkg/registry/ingest/signing_test.go` and `pkg/registry/ingest/orchestrator_test.go`, and the `registrySignerFor` cases in `internal/serverboot/webui_sign_config_test.go`.
- The http-sink and anchor-key cases: `internal/serverboot/audit_anchor_test.go` and `test/e2e/audit_anchor_key_refusal_test.go`.

### TEST-1. `test/e2e` file comments

- `test/e2e/audit_signing_journey_test.go:3-4`: replace "Audit transparency-log signing, anchoring, verification, and PII redaction in one journey." with "Audit chain-head anchoring under the anchor key, chain verification, and PII redaction in one journey.", wrapped at the file's existing width.
- `test/e2e/operational_notifications_test.go:5-7`: remove "transparency-anchor failure, " from the parenthesized list and re-wrap, so the list reads "(ingest failure, embedding-provider outage, layer auto-disable)".

No assertion, test name, or `// Spec:` annotation changes. `pkg/audit/anchor_test.go` is not edited: its comments at lines 14-17, 62-66, 94-97, and 124-125 describe the library contract for any signer, and the dormant path stays (D-4).

**Commands per step.**

- S1, S2: `make speccov-drift matrix-audit`, and confirm neither reports a change.
- S3 through S6: `gofmt -l` on the touched files prints nothing, then `go build ./...` and `go test ./pkg/audit/... ./pkg/notification/... ./pkg/registry/core/... ./internal/serverboot/...`.
- S7: `go vet ./test/e2e/...`, then `go test ./test/e2e/ -run 'TestAuditSigningJourney|TestOps_' -v`.
- S8: `cd site && npm test && npm run check`.
- S8 through S11: `make docs-check`.
- Closing step: `make lint` and `make coverage-gate`.

**Verification for the whole change.** After S11, run:

```bash
grep -rn -e 'transparency-log anchoring' -e 'transparency-anchor' -e 'transparency anchoring' -e 'admin anchor' \
  README.md docs spec site/src pkg internal test
```

The expected hits are exactly `spec/08-audit-and-observability.md:81` ("distinct from the transparency-log anchoring recommended above") and `spec/12-operational-risks-and-mitigations.md:25`. §8.6 line 79 says "anchoring of the chain head to a public transparency log" and does not match, and the new sentences say "public transparency log", which does not match either. Also run `grep -rn "author's key\|commit time" spec docs` and confirm it prints nothing.

**Manual suite.** The change alters no output an operator or a consumer observes, so no scenario is staged. Scenario S78 in `test/manual-validation.md` exercises the anchoring startup refusals and is unaffected.

## Documentation changes

### DOC-1. Tier tables and the landing page

- `README.md:139` and `docs/deployment/index.md:15`, last cell: "Everything in single node, plus multi-tenancy, SCIM group sync, transparency-log anchoring of the audit chain, and high availability" becomes "Everything in single node, plus multi-tenancy, SCIM group sync, and high availability".
- `docs/overview.md:72`, last cell: "Multi-tenancy, SCIM group sync, transparency-log anchoring of the audit chain, and high availability" becomes "Multi-tenancy, SCIM group sync, and high availability".
- `site/src/content/landing.ts:391`: delete the array element `"Transparency-log anchoring of the audit chain",`, leaving `plus: ["Multi-tenancy", "SCIM group sync", "High availability"]` in the file's existing one-element-per-line layout. No site test changes.

### DOC-2. how-it-works and local

`docs/getting-started/how-it-works.md:230-232` becomes, keeping the bullet's line wrap:

```markdown
- **What it adds.** Multi-tenancy and high availability. SCIM group push,
  freeze windows, the hash-chained audit log, and its local chain-head
  anchoring run on a single node as well.
```

`docs/deployment/local.md:162`: "When multi-tenancy, SCIM group sync, transparency-log anchoring of the audit chain, or production availability is required, follow ..." becomes "When multi-tenancy, SCIM group sync, or production availability is required, follow [Clustered](clustered) and use `podium admin migrate-to-standard` to export the single-node state." The rest of the sentence is unchanged.

### DOC-3. single-node, "What the tier omits"

Replace the body under `## What the tier omits` (`docs/deployment/single-node.md:155-160`) with the text below. The heading stays, and no inbound link targets it.

```markdown
Single node omits multi-tenancy. A single-node deployment is single-tenant.

Artifact signing and local chain-head anchoring of the audit log run on a single node. The registry signs every artifact with its registry-managed key by default. When `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and the audit log is a file, the registry also anchors the audit chain head with its anchor key. No tier submits the chain head to a public transparency log.

When multi-tenancy starts mattering, see [Clustered](clustered).
```

The backup bullet at `docs/deployment/single-node.md:187` stays.

### DOC-4. progressive-adoption

Replace the bullet at `docs/deployment/progressive-adoption.md:108` with:

```markdown
- **Local chain-head anchoring** (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`) when auditors ask for signed checkpoints of the audit chain. The registry periodically signs the chain head with a dedicated anchor key (`PODIUM_AUDIT_SIGNING_KEY_PATH`) and appends an `audit.anchored` event, and an auditor verifies the anchor with the key's public half. Anchoring runs only when the registry audit sink is a file. A registry whose `PODIUM_AUDIT_LOG_PATH` names an `http://` or `https://` endpoint logs that anchoring is disabled. The signature covers the chain-head digest alone, and the operator holds the anchor key, so the evidence stays within the operator's trust domain and does not establish when an anchor was made. Podium does not submit the chain head to a public transparency log.
```

No fenced block on the page changes, so the `tools/doccov` classification is unaffected.

### DOC-5. CHANGELOG

Under `## [Unreleased]`, after the existing `### Changed` group, add:

```markdown
### Documentation

- **The spec and docs no longer claim transparency-log anchoring of the audit chain** (§13.10, §13.11.3): no deployment submits the audit chain head to a public transparency log, and §8.6 still recommends that anchoring for high-assurance deployments. The §8.6 local chain-head anchor runs on every tier, standalone included, when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 (the default 0 disables it) and the audit sink is a file. It signs the chain head with the anchor key at `PODIUM_AUDIT_SIGNING_KEY_PATH`. The README, the deployment pages, and the landing page no longer list transparency-log anchoring as a clustered feature.
- **The opening of §4.7.9 states what the registry signs at ingest**: the registry signs each artifact version it ingests with its registry-managed key unless the signing mode is `none`, and `podium sign` signs outside the ingest flow. The previous text claimed author-key signing at commit time, which no code performs.
```

## Open questions

- **Q-1. Remove the dormant Rekor log-index path in `pkg/audit`?** The path is `extractRekorLogIndex` (`pkg/audit/anchor.go:88-97`), the `log_index` key in the `audit.anchored` Context (`pkg/audit/anchor.go:53`), and the tests that exercise it (`pkg/audit/anchor_test.go:62-170`, with the `fakeIndexedSigner` and `envelopeSigner` fakes). Removing it drops a Context key that SIEM rules or external verifiers may read, so it is a code change with an observable audit-record difference. This proposal keeps the path and corrects its comments (D-4). Removal could go in a later MINOR together with any public-log anchoring design.

## Non-goals

- Implementing public transparency-log anchoring of the audit chain head, or wiring a Sigstore-keyless signer into the anchor path. D1 leaves this for a possible later feature proposal.
- Changing §8.6, the §12 risk-table row, the §13.12 Audit anchoring table, or the `PODIUM_SIGSTORE_*` rows.
- Any runtime behavior change in `pkg/audit` or `internal/serverboot`, including the `audit.anchored` event Context (the `log_index` key stays and records -1) and `extractRekorLogIndex` (see Q-1).
- Routing audit anchor failures to the NotificationProvider. The comments stop claiming it, and the wiring is not added.
- Re-verifying the other items in the edited lists. Examples are freeze windows and outbound webhooks being inert on standalone in §13.10, SCIM appearing as standalone-out-of-scope in §13.10 and as remote-only in §13.11.3 while `docs/getting-started/how-it-works.md` says SCIM group push runs on a single node, and the embedding-outage and auto-disable items in the notification comments. Each needs its own investigation.
- Doc-style cleanup of unrelated wording on the edited lines, such as "deployment shape" and the em-dash in §13.10.
- Adding behavioral tests or a guard test against reintroducing the claim (D-8).
- Signing or binding the anchor time. The §8.6 anchor signature covers the digest alone, and that design is unchanged.
- Editing the accurate comments in `pkg/audit/anchor_test.go` and at `pkg/audit/anchor.go:19-26`.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-08)

The draft's per-change challenge revisions were applied. Where they disagreed with the draft or with each other, the proposal resolves them as follows.

- **SPEC-1 and SPEC-2 state the local-anchoring point once.** The draft added a sentence to §13.10 and a paragraph after §13.11.3 that cited each other, and both restated §8.6 and §13.12 conditions. §13.10 now carries one pointer sentence, §13.11.3 changes its list item alone, and D-6 says so.
- **SPEC-3 is shorter.** The draft's opening sentence also restated verification on materialization, which line 934 already states and which is wrong under the `never` policy, and it named `PODIUM_SIGN` alone. The replacement says "signing mode" and cites §13.12. D-7 follows.
- **DOC-5's second bullet follows SPEC-3.** The DOC-5 revision kept a bullet saying a consumer verifies only the §4.7.10 delivery signature and naming `PODIUM_SIGN` values. The SPEC-3 revision directs the bullet to match the new sentence, so the bullet says the registry signs unless the signing mode is `none` and that `podium sign` signs outside ingest.
- **CODE-1 is narrowed to the first paragraph of the `Anchor` comment.** The rewrites of `pkg/audit/anchor.go:23-26` and `:77-80` corrected nothing false and added caller-specific claims. One sentence is appended to the `extractRekorLogIndex` comment instead, as D-4 describes.
- **CODE-3 removes the example lists from the `Registry` comments.** The draft kept "ingest failure" as their example, which is also false for the `Registry` notifier.
- **TEST-1 is narrowed to the two `test/e2e` comments.** The `pkg/audit/anchor_test.go` edits are dropped, because those comments are conditional and accurate, and the proposed rewording attributed the -1 to the anchor key in a test that uses `sign.Noop{}`.
- **DOC-3, DOC-4, and DOC-5 carry the file-sink condition.** Each draft sentence named the interval alone.
- **The whole-change verification paragraph moved from DOC-5 to Testing**, and its expected grep result is corrected to `spec/08-audit-and-observability.md:81` and `spec/12-operational-risks-and-mitigations.md:25`.
- **Step granularity.** D-9's single spec commit became S1 (SPEC-1 and SPEC-2, one file) and S2 (SPEC-3), so each step carries one concern.

### Pass 1 (2026-11-08, automated)

- **The Testing bullet for §4.7.9 ingest signing cited a file with no `registrySignerFor` case.** `internal/serverboot/rehash_test.go` mentions `registrySignerFor` only in the comment at line 28 and calls it nowhere. The bullet now names `internal/serverboot/webui_sign_config_test.go` alone, whose test at line 85 cites §4.7.9 and calls `registrySignerFor("none")` and `registrySignerFor("")`.
- **Correction to the entry above.** The entry first said the `rehash_test.go` tests cite §4.7.6 and §13.4. Several of them cite §4.7.9 (lines 1177, 1280, 1412, 1456, 1496, and 1559), so the entry now gives the verified reason for dropping the file: `grep -n registrySignerFor internal/serverboot/rehash_test.go` matches only the comment at line 28.
