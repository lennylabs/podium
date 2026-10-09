# Proposal 0052: Report a git layer's `repo` without its URL credential in every response, clone error, notification, and boot log line

- Issue: (to be filed)
- Status: Implemented (2026-10-08). Signed off as staged, with the open questions settled as recorded in "Resolved decisions" (RD-1 through RD-5). Verified on 2026-10-08; the final adversarial review pass converged with no findings.
- Date: 2026-10-08

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.3.1 gains the paragraphs "Repository credentials" and "Re-registration with a reported `repo`". The first states that a `git` layer's `repo` is accepted and stored with its URL userinfo, that every response reports it with the userinfo removed, which values are reported as `[redacted]`, that the built-in `git` source's clone failure message carries no URL userinfo, and that the message is a fixed text with no upstream detail for a `repo` reported as `[redacted]`. The second states the registration guard (SPEC-1). The §7.3.1 **Errors** paragraph gains one clause (SPEC-2).
- `pkg/layer/source` gains one file, `redact.go`, with `RedactRepo`, `RedactCloneError`, and the `RedactedRepo` constant (CODE-1).
- The clone error is redacted where it is built, `pkg/layer/source/git.go:58`. That one edit covers the `502 ingest.source_unreachable` body on the manual reingest and on the inbound webhook route, and the §9.1 notification body (CODE-2).
- The layer endpoints redact `repo` on the copy each response marshals: inside `readableBy` for the list and reorder responses, and at the `LayerRegisterResponse` construction for register, update, and the update no-op (CODE-3).
- The declared-layer boot log line prints the redacted `repo` (CODE-4).
- `POST /v1/layers` refuses a registration that would replace a stored credential-bearing `repo` with the value a read reports for it, with `400 registry.invalid_argument` carrying `details.constraint: "redacted_repo"`, unless the request sets the new `force_repo_overwrite` member (CODE-5). `podium layer register` gains `--force-repo-overwrite` (CODE-6).
- Unit, integration, and end-to-end tests, manual scenario S89, the HTTP API and CLI references, the layers deployment page, and the `Fixed` and `Changed` changelog entries follow (TEST-1, TEST-2, TEST-3, DOC-1).

**Fixed decisions.**

- Registration and update never refuse, reject, warn on, or alter URL userinfo. The stored `repo` is unchanged, and the clone keeps reading the stored value. This is a user constraint and is closed.
- The fix is output redaction and one registration guard. It adds one register request member (`force_repo_overwrite`), one `podium layer register` flag (`--force-repo-overwrite`), and one `details.constraint` value (`redacted_repo`). These names were confirmed at sign-off review (Resolved decisions, RD-3). It adds no credential field, no `CloneOptions.Auth` wiring, no §6.10 error code, no `PODIUM_*` variable, no `registry.yaml` key, no SPI, and no stored layer field. It migrates no stored row.
- One userinfo rule covers every URL scheme: the whole userinfo is removed. `ssh://git@host/x.git` is reported as `ssh://host/x.git`.
- A value that does not match `^[^:]+://` is reported as stored. A value with no userinfo that passes the fail-closed check is reported byte-identical to the stored value.
- Fail closed: a `://` value that `url.Parse` rejects, and an `http` or `https` value with `@` after its host, is reported as the literal `[redacted]` and no part of it is echoed. The check runs whether or not the value also carries userinfo. The literal and the `@`-after-host class were confirmed at sign-off review (RD-2, RD-5).
- Redaction applies to every caller on every response, including a tenant admin and the layer's owner.
- The clone error is redacted where it is built, by pattern over the error text. The stored string is never used as a search-and-replace key. When the configured `repo` is in a fail-closed class, the upstream text is replaced with a fixed phrase. The rule covers the built-in `git` source only.
- The remote's response body that go-git appends on 401, 403, and 404 passes through the same pattern and is otherwise left as is.
- An update does not change `repo`, and a registration stores the `repo` it carries. One registration is refused: a request whose ID names a stored layer, a soft-deleted one inside its §8.4 recovery window included, whose stored `repo` is reported differently from how it is stored, and whose `repo` equals that reported value byte for byte, unless the request sets `force_repo_overwrite`. The refused `repo` never carries URL userinfo, so the first fixed decision holds unchanged. The guard applies to the HTTP `register` operation alone and runs after the layer write, local-source, and admin-only-fields refusals. The guard is folded in at the user's request (2026-10-08). The force member, the inclusion of a soft-deleted ID, and the absence of a Web UI control were confirmed at sign-off review (RD-4).
- §7.2.1 is unchanged. No general rule for a credential supplied inside another value is added.
- The helper lives in `pkg/layer/source`. `store.LayerConfig` gains no `MarshalJSON`.
- The Web UI source and `web/bundle` are unchanged.
- The change ships as a 0.5.2 patch release from `release/0.5.x` and is merged back into `main`. The `Fixed` changelog entry advises rotating any token that was stored in a layer `repo` on an earlier version (RD-1).

**Watch out for.**

- **Redact only the response copy.** `PutLayerConfig`, `layerConfigEqual`, `emitLayerEvent`, and the ingest runner read the same `cfg` the response is built from (`pkg/registry/server/layers.go:1282-1300`, `:1540-1555`, `:1757-1780`). A redacted value written back to the store removes the credential from the layer. TEST-2 asserts the stored row and the credential the remote receives after each response.
- **`readableBy` returns a projection after this change.** Its only callers pass the result straight to `writeJSON` (`pkg/registry/server/layers.go:1591`, `:1780`). A later caller that stores the result would lose credentials, so the doc comment says so.
- **`url.Parse` before the scheme check sends every scp-like remote to `[redacted]`.** `url.Parse` rejects `git@github.com:acme/x.git`. Test the `^[^:]+://` pattern first, in the order go-git classifies a remote.
- **The rendered URL is not the stored string.** go-git and net/http re-escape the userinfo, drop a default port, and append `/info/refs?service=git-upload-pack`. A `strings.ReplaceAll` of `cfg.Repo` in the error text misses it. TEST-1 pins this with a password containing `!`.
- **An unescaped `/` in the userinfo moves the credential out of the userinfo.** `https://tok/en@host/x.git` parses with no userinfo, host `tok`, and path `/en@host/x.git`. The fail-closed `@` check exists for this case and must not be skipped when the value also parses with userinfo (`https://bob@tok/en@host/x.git`). A fixture whose stray segment is an invalid port (`https://u:pa/ss@host/x`) hides the gap, because it fails to parse.
- **Single-letter credential fixtures give false passes and false failures.** The sentinel prefix `source: unreachable` contains `u`. Use `alice-user` and `s3cr3t`-style fixtures in every test.
- **`(*url.URL).Redacted()` does not fit.** It keeps the username, and a token is commonly the username of an `https` remote.
- **The registration guard compares against `stored`, the value `lookupLayerForWrite` already returned.** Do not add a second store read. The guard sits after the admin-only-fields block and before `cfg` is built, so a refused request mints no webhook secret, writes nothing, and records no event. Do not apply the comparison in `update`, which applies no `repo`. The refusal message names no part of the stored value.
- **A backquoted span in a `flag` usage string becomes the flag's value name.** `setUsage` prints through `PrintDefaults`, so the `--force-repo-overwrite` usage string carries no backquotes.
- **An existing test rejects a request member the layer object does not answer.** `TestLayerEndpoint_RequestAndResponseAgreeOnNames` (`pkg/registry/server/layer_wire_test.go:360`) fails for `force_repo_overwrite` until its `requestOnly` map names the member. CODE-5 stages that entry, and it lands in S6 with the member.
- **Parts of `test/e2e` skip silently on macOS.** Grep the run output for `SKIP` before counting either end-to-end case, TEST-2(d) or TEST-2(e), as verification.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1, SPEC-2. §7.3.1 gains the "Repository credentials" and "Re-registration with a reported `repo`" paragraphs, and its **Errors** paragraph gains one clause.
      Levels: —. Depends on: —
- [x] **S2 · code** — CODE-1. `pkg/layer/source/redact.go` with `RedactRepo`, `RedactCloneError`, and `RedactedRepo`.
      Levels: unit. Depends on: S1
- [x] **S3 · code** — CODE-2. `pkg/layer/source/git.go` builds the clone error through `RedactCloneError`.
      Levels: unit, integration. Depends on: S2
- [x] **S4 · code** — CODE-3. `pkg/registry/server/layers.go` redacts `repo` on each response copy through `wireLayer`.
      Levels: integration, e2e. Depends on: S2
- [x] **S5 · code** — CODE-4. `internal/serverboot/serverboot.go` logs the redacted `repo` for a declared git layer.
      Levels: integration, e2e. Depends on: S2
- [x] **S6 · code** — CODE-5, CODE-6. `pkg/registry/server/layers.go` refuses a registration that re-submits a reported `repo`, `pkg/registry/server/layer_wire_test.go` admits `force_repo_overwrite` as a request-only member, and `cmd/podium/layer.go` gains `--force-repo-overwrite`.
      Levels: integration, e2e. Depends on: S1, S2
- [x] **S7 · test** — TEST-1. Unit tests for the helper and for the clone error against a local HTTP remote.
      Levels: unit. Depends on: S3
- [x] **S8 · test** — TEST-2. Handler tests, the reingest and webhook integration test, the serverboot boot-log test, the end-to-end declared-layer case, and the end-to-end registration-guard case.
      Levels: integration, e2e. Depends on: S3, S4, S5, S6
- [x] **S9 · test** — TEST-3. Manual scenario S89.
      Levels: manual. Depends on: S3, S4, S6
- [x] **S10 · docs** — DOC-1. HTTP API reference, CLI reference, layers deployment page, and changelog.
      Levels: —. Depends on: S3, S4, S5, S6

**Ordering constraints.** S1 lands the rule every later step cites. S2 precedes S3, S4, S5, and S6 because each calls the helper. S3, S4, S5, and S6 are independent of each other. Land S2 through S8 in one pull request, so the 85% coverage bar on the new lines is met when the pull request is measured.

## Current state and the gap

### URL userinfo is the credential path Podium provides

The git source clones with `git.CloneOptions{URL: cfg.Repo}` and no `Auth` (`pkg/layer/source/git.go:47-56`). Podium has no credential field, flag, environment variable, or SPI for a clone (`pkg/layer/source/source.go:58-75`, `pkg/store/store.go:270-315`). A credential in the URL userinfo, as in `https://x-access-token:<token>@git.acme.com/acme/private.git`, is therefore the only credential path for an HTTPS remote. An SSH remote can authenticate through an ambient ssh-agent and `known_hosts` in the server process environment, which is go-git's default when `Auth` is nil. That is a host-level arrangement the shipped image and chart do not provide, and no key file can be uploaded. Refusing userinfo at registration would leave these operators with no way to register a private remote.

### Where the stored value is disclosed

1. **Layer responses.** `store.LayerConfig.Repo` is tagged `json:"repo"` (`pkg/store/store.go:282`) with no custom marshaler and no response DTO. `LayerRegisterResponse` embeds the store type (`pkg/registry/server/layers.go:945-949`). These write sites emit it: the update no-op (`pkg/registry/server/layers.go:1283`), update (`:1293-1300`), register (`:1550-1555`), the list on its live and `?deleted=true` arms (`:1582-1591`), and reorder (`:1780`). `readableBy` (`pkg/registry/server/layers.go:317-334`) filters layers and never fields. The list is not admin-gated. It returns the whole tenant list when `authAdmin` admits, which on a registry in public mode or with no identity provider is every caller (`internal/serverboot/serverboot.go:1512-1521`), and otherwise it returns each layer §4.6 admits to an authenticated caller. The Web UI renders the value (`web/ui/src/components/SourceCell.tsx:45`, `:175`).
2. **Clone error text.** `pkg/layer/source/git.go:58` wraps the go-git error as `fmt.Errorf("%w: %v", ErrSourceUnreachable, err)`. The text that go-git v5.19.2 and net/http produce depends on the failure:

   | Failure | Text |
   |:--|:--|
   | Remote status other than 401, 403, and 404 | go-git prints the full request URL with user and password. |
   | Transport failure (DNS, refused connection, TLS, timeout) | net/http prints the URL with the password masked and the username kept, so a token in the username position is printed. |
   | A `://` string that does not parse | The parse error quotes the raw string. |

   The rendered URL is not byte-equal to the stored value. The text reaches the `502 ingest.source_unreachable` body (`pkg/registry/server/layers.go:1998-1999`) on `POST /v1/layers/reingest` and on the inbound webhook route (`pkg/registry/server/webhook_ingest.go:87`, both through `runIngestAndRespond`, `pkg/registry/server/layers.go:1866-1881`). It also reaches the §9.1 operational notification body (`pkg/registry/server/layers.go:2017-2025`), which the log, webhook, and SMTP providers deliver. `internal/serverboot/reingest.go:92-94` and `pkg/registry/ingest/orchestrator.go:115-118` return the error unchanged.
3. **Boot log.** `bootstrapDeclaredLayers` logs `repo=%s` with `lc.Repo` for each declared git layer (`internal/serverboot/serverboot.go:679-680`).

### The spec has no rule for it

§7.3.1 requires `repo` on every layer object, and its never-carried rule covers the inbound webhook HMAC secret only. §7.2.1 covers a credential that the registry mints or rotates. No spec text mentions URL userinfo. The present disclosure therefore matches the spec, and the fix needs a staged spec edit before code.

### Surfaces that carry neither the repo nor the clone error text

These were traced and need no change.

- §7.6 change events and §7.3.2 outbound webhook payloads. `publishConfigChanged` publishes `{layer, action}` (`pkg/registry/server/layers.go:658-666`), and ingest events carry tenant, layer, refs, and counts (`pkg/registry/ingest/orchestrator.go:130-189`).
- §8.1 audit events. `emitLayerEvent` records action, `user_defined`, and owner (`pkg/registry/server/layers.go:518-524`), and no failed-ingest audit event exists.
- The erase, restore, unregister, and reingest-summary responses, which carry identifiers and counters.
- `pkg/registry/core`, which projects a layer to ID, precedence, and visibility.
- Metrics, and the SDKs, which make no `/v1/layers` call.
- The CLI. `cmd/podium/layer.go` sends `--repo` from a flag and prints server bodies, and `cmd/podium/admin_migrate.go:320-378` copies rows store to store.
- The other `ErrSourceUnreachable` wraps in `pkg/layer/source/git.go:63-76`, which interpolate ref, hash, and root.

## Decisions

- **Decision 1. Accept and store userinfo unchanged (user constraint, closed).** Registration and update never refuse, reject, warn on, or alter URL userinfo. The clone path keeps reading the stored value (`pkg/registry/ingest/orchestrator.go:109`, `pkg/layer/source/git.go:49`). No stored row is migrated, and existing rows are covered because redaction runs on output.
- **Decision 2. Smallest fix (user scope, closed).** The change is one helper file, its call sites, and the Decision 10 registration guard with its request member and CLI flag. It adds no error code, environment variable, configuration key, SPI, or stored layer field.
- **Decision 3. Classification order.** The helper classifies a value in the order go-git does (`transport.NewEndpoint`: scp-like, then file, then `url.Parse`). A string that does not match `^[^:]+://` is returned unchanged; this covers the scp-like form, a filesystem path, and the empty string. go-git's scheme pattern is in an internal package, so the helper restates it.
- **Decision 4. One userinfo rule for every scheme.** A matching string that parses with userinfo is reported with the whole userinfo removed, as `u.String()` after `u.User = nil`. The draft kept the username for schemes other than `http` and `https`. That split is dropped: go-git's ssh transport reads only the endpoint username, so the kept username protected a display detail, and the split cost a spec sentence, a helper branch, and test rows against the smallest-fix scope. Accepted consequences: `ssh://git@host/x.git` is reported as `ssh://host/x.git` while the scp-like `git@host:x.git` is reported as stored, and a redacted value can differ from the stored one in percent-escaping.
- **Decision 5. Byte-identical when nothing is removed.** A matching string that parses with no userinfo and passes the Decision 6 check is returned as the input bytes, without a round trip through `u.String()`, so an existing layer with no credential reads exactly as before.
- **Decision 6. Fail closed.** Two classes of value are reported as `[redacted]` with no part of the input echoed. The first is a `://` string that `url.Parse` rejects. Such a value can never clone, and `isFileTransportRepo` (`pkg/registry/server/layer_capabilities.go:75-84`) already classes it as admin-only at registration. The second is an `http` or `https` string with `@` anywhere after its host, in the path, query, or fragment. An unescaped `/` in a credential ends the URL authority early and leaves the credential in the host and path, where the userinfo rule cannot see it. The check runs whether or not the value also carries userinfo, so `https://bob@tok/en@host/x.git` is covered. Accepted cost: an `http` or `https` remote whose path legitimately contains `@` is reported as `[redacted]`. The check is limited to `http` and `https` because those are the schemes on which go-git sends a URL credential; an `ssh://` password is never read, and a `file://` path carries none.
- **Decision 7. Redact the clone error at its construction site.** `pkg/layer/source/git.go:58` is the only wrap that carries go-git text, so one edit covers the 502 body on both triggers, the notification body, and any log sink. Every `scheme://userinfo@` occurrence in the text loses its whole userinfo. When the configured repo is in a Decision 6 class, the upstream text is dropped and replaced with a fixed phrase, because a parse error quotes the raw string and the pattern cannot cross an unescaped `/`. `%w` on `ErrSourceUnreachable` is kept, so `errors.Is` classification and the 502 code are unchanged. The go-git error is flattened with `%v` today, so no error chain is lost. The alternative of scrubbing at the two sinks (`writeReingestError` at `pkg/registry/server/layers.go:1984` and `notifyIngestFailure` at `pkg/registry/server/layers.go:2017`) was rejected: it is two edits, it has no access to the configured repo for the fail-closed arm, and SPEC-1 scopes the rule to the built-in `git` source to match.
- **Decision 8. Every caller.** Redaction applies to a tenant admin and the layer's owner as well. A per-caller reveal needs a second code path and a decision on public-mode registries, where every caller takes the admin arm.
- **Decision 9. Helper placement.** `pkg/layer/source` owns the git remote string and builds the clone error. `pkg/registry/server` and `internal/serverboot` already import it (`pkg/registry/server/layers.go:20`, `internal/serverboot/serverboot.go:36`), so there is no new dependency edge and no utility package. A `MarshalJSON` on `store.LayerConfig` was rejected: `pkg/store` would depend on `pkg/layer/source`, and every other JSON use of the struct would inherit a hidden behavior.
- **Decision 10. Round trip and the registration guard (folded in at the user's request, 2026-10-08).** Update never applies `repo` (`pkg/registry/server/layers.go:1207-1249`), so a read followed by an update leaves the stored credential in place. Register is an upsert on (tenant, id) that stores `req.Repo` as sent (`pkg/registry/server/layers.go:1424`, `:1540`), so a registration that copies `repo` from a read would store the URL without its credential. The handler refuses that registration. The refusal fires when all of these hold: `lookupLayerForWrite` (`pkg/registry/server/layers.go:413`) reports that the request ID names a stored layer, which includes a soft-deleted layer inside its §8.4 recovery window; `source.RedactRepo(stored.Repo)` differs from `stored.Repo`; `req.Repo` equals `source.RedactRepo(stored.Repo)` byte for byte; and the request does not set `force_repo_overwrite`. The refusal is `400 registry.invalid_argument` with `details.constraint: "redacted_repo"`, on the pattern of `immutable_visibility`.
  - **Compatibility with Decision 1.** When `RedactRepo` changes a value, its output is `[redacted]` or a URL with no userinfo, so the refused `repo` never carries URL userinfo.
  - **Position.** The guard runs after the layer write authorization refusals, the local-source rule, and the admin-only-fields rule, so each of those keeps its own envelope and only a caller the write rule authorizes on the stored layer reaches it. It runs before the owner-less and cap refusals, the webhook-secret mint, the write, and the §8.1 event.
  - **Disclosure.** The refusal tells a caller the write rule authorizes that the stored `repo` differs from its reported form. On a registry with no identity provider configured, or one in public mode, every caller is on that arm. The message names no part of the stored value.
  - **Force.** With `force_repo_overwrite` set, the registration proceeds and stores `req.Repo` as sent. The member is read by this comparison alone: it is ignored when the comparison does not match, and `update` ignores it, as `register` ignores `rotate_webhook_secret`. It is required because unregistering does not clear the first condition for the recovery window, so without it no request could store the credential-free form of the same remote.
  - **Scope.** The guard is on the HTTP registration alone. The other writers of a layer row (`internal/serverboot/serverboot.go:544`, `:645`, `pkg/registry/ingest/orchestrator.go:200`, `cmd/podium/admin_migrate.go:378`, and reorder at `pkg/registry/server/layers.go:1757`) take no `repo` from a request and are outside it. An HTTP registration over a declared layer's ID is subject to the guard like any other, and what it stores reverts at the next start, as §7.3.1 already states for a declared layer.
  - **Accepted limits.** The lookup and the write are not atomic, so two concurrent registrations under one ID stay last-writer-wins, as `pkg/registry/server/layers.go:1348-1351` records. The guard matches the exact reported value; a hand-edited URL with no credential is a different string and is stored. The Web UI register form cannot send the member, so a form submission that types the reported value under an existing ID is refused, and the operator corrects it by typing the full URL.
  - **Shipped clients.** No shipped client copies `repo` from a read: `web/ui/src/surfaces/RegisterLayerForm.tsx:61` starts `repo` empty, `web/ui/src/surfaces/UpdateLayerForm.tsx` sends no `repo`, and the CLI takes `--repo` from a flag. "An update does not change `repo`" is new spec text; until now only `docs/reference/http-api.md` stated it.
- **Decision 11. Remote response bodies.** The body go-git appends on 401, 403, and 404 is remote-controlled text and carries no URL from go-git. It passes through the same pattern scrub and is otherwise left as is.
- **Decision 12. No general §7.2.1 sentence.** The draft added a sentence to §7.2.1 saying a credential supplied inside another value is returned by no response. It is dropped. It would be false for the §7.3.2 receiver `url`, which is returned on every read and can carry userinfo, and for a credential in a `repo` query or path, which this proposal excludes. §7.2.1 already points at §7.3.1.

## Spec amendment: §7.3.1 repository credentials

**SPEC-1.** `spec/07-external-integration.md`, §7.3.1. It lands in step S1. Insert the two paragraphs staged below, in order, immediately after the paragraph that begins "The layer object never carries the layer's inbound webhook HMAC secret under any name." and before the paragraph that begins "**User-defined layers.**":

> **Repository credentials.** A `git` layer's `repo` may carry a credential in the userinfo of its URL, as in `https://x-access-token:<token>@git.acme.com/acme/private.git`. A registration accepts that value and stores it unchanged, and the ingest clones with the stored value. Every response that carries the layer object reports a `repo` that is written as a URL with a scheme with its userinfo removed, for every caller, the layer's owner and a tenant admin included. A `repo` that is not written as a URL with a scheme, which covers the scp-like form `git@github.com:acme/x.git` and a filesystem path, is reported as stored. A `repo` written with a scheme that does not parse as a URL, and an `http` or `https` `repo` that carries `@` after its host, is reported as the fixed string `[redacted]`, because the registry cannot tell which part of such a value is a credential. The message the built-in `git` source reports for a failed clone carries no URL userinfo, so the `ingest.source_unreachable` response (§6.10) and the failed-ingest notification (§9.1) for that failure carry none. For a `repo` that is reported as `[redacted]`, that message is a fixed text that carries no part of the underlying error, because the underlying error can quote the `repo` in a form from which a credential cannot be removed. An update does not change `repo`. A registration replaces the stored `repo` with the value it carries, so a registration that reuses an ID supplies the full `repo` with its credential, subject to the rule below.
>
> **Re-registration with a reported `repo`.** A `register` request whose ID names a layer that exists in the tenant, where that layer's stored `repo` is reported by the rule above as a value other than the stored one, and whose own `repo` equals that reported value, is refused with `400 registry.invalid_argument` (§6.10) carrying `details.constraint: "redacted_repo"`, because storing the reported value would remove the credential the layer clones with. A layer that is soft-deleted and still inside its §8.4 recovery window is a layer that exists for this rule. The comparison is exact, byte for byte, and a reported `[redacted]` is compared on the same terms. A request that sets the boolean member `force_repo_overwrite` to true is not refused on this ground and stores the `repo` it carries. The member is read for this rule alone: it has no effect on a registration the rule would not refuse, and `update` ignores it. A registration whose `repo` differs from the reported value, which covers a different remote and the full URL with a credential, is unaffected, as is one against a layer whose stored `repo` is reported as stored and one whose ID names no stored layer. The rule is evaluated after the layer write authorization rule, the local-source authorization rule, and the admin-only registration fields rule of this section, so a registration those rules refuse keeps their refusal and only a caller the layer write authorization rule authorizes on the stored layer receives this one. A refused registration stores nothing, mints no webhook secret, and records no §8.1 event. The refusal's message carries no part of the stored `repo`, and it names the two corrections: the full repository URL, or `force_repo_overwrite`. The rule applies to the `register` operation alone. The write the registry makes at each start for a layer declared in the registry configuration is outside the rule.

**SPEC-2.** `spec/07-external-integration.md`, §7.3.1, the `**Errors.**` paragraph. It lands in step S1. Append to the closing enumeration, after the clause ending "(`registry.invalid_argument`, carrying `details.constraint: "immutable_visibility"`)" and before the final period:

> , and a registration whose `repo` is the value a read reports for the stored layer's credential-bearing `repo`, which the re-registration rule above refuses (`registry.invalid_argument`, carrying `details.constraint: "redacted_repo"`)

No other spec text changes. §7.2.1 is unchanged (Decision 12). The paragraphs create no new section and no new §6.10 code. `registry.invalid_argument` is an existing code with an existing matrix cell, and no `matrix-audit` cell enumerates `details.constraint` values (`tools/matrix` holds no reference to the member), so `speccov-drift` and `matrix-audit` need nothing beyond the tests that cite §7.3.1. The `[redacted]` literal and the names `force_repo_overwrite` and `redacted_repo` were confirmed at sign-off review (RD-2, RD-3).

## Proposed solution

### CODE-1. `pkg/layer/source/redact.go` (new)

The file holds two exported functions, one exported constant, one unexported predicate, one unexported constant, and two compiled patterns. It imports `net/url`, `regexp`, and `strings`.

```go
// RedactedRepo is what a response reports for a repo whose credential-bearing
// part cannot be identified.
//
// Spec: §7.3.1 (Repository credentials)
const RedactedRepo = "[redacted]"

// cloneErrorWithheld replaces the upstream clone error for a repo that
// RedactRepo reports as RedactedRepo. The upstream text quotes such a repo in
// a form the userinfo pattern cannot match.
const cloneErrorWithheld = "clone failed; the error text is withheld because the repository URL cannot be reported"

// schemeRE restates go-git's scheme test, which lives in an internal package.
// Testing it before url.Parse keeps scp-like remotes out of the parse-failure arm.
var schemeRE = regexp.MustCompile(`^[^:]+://`)

// urlUserinfoRE matches the userinfo of a URL inside error text. url.URL.String
// percent-escapes '/', '@', ':', '"', and whitespace in userinfo. go-git's
// Endpoint.String uses url.PathEscape, which leaves '@' and ':' raw, so the
// match is greedy to the last '@' before a '/', whitespace, or a quote.
var urlUserinfoRE = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/\s"]*@`)

// RedactRepo returns the form of a layer's repo that a response or a log line
// may carry. The stored value is never passed through it on the way to a clone.
func RedactRepo(repo string) string

// RedactCloneError returns the text of a failed clone's error with no URL userinfo.
func RedactCloneError(repo string, err error) string
```

`repoUnsafeToEcho(repo string) bool` is the fail-closed predicate (Decision 6). It returns true when `schemeRE` matches `repo` and either of these holds:

- `url.Parse(repo)` returns an error.
- The parsed scheme is `http` or `https`, and the text after the authority contains `@`. The text after the authority is the part of `repo` that follows `://`, starting at its first `/`, `?`, or `#`. `url.Parse` lowercases the scheme, so `HTTPS://` takes this arm.

`RedactRepo(repo)`:

1. When `schemeRE` does not match, return `repo`.
2. When `repoUnsafeToEcho(repo)`, return `RedactedRepo`.
3. Parse `repo`. When `u.User == nil`, return `repo` byte-identical.
4. Otherwise set `u.User = nil` and return `u.String()`.

`RedactCloneError(repo, err)`:

1. When `repoUnsafeToEcho(repo)`, return `cloneErrorWithheld`.
2. Otherwise return `urlUserinfoRE.ReplaceAllString(err.Error(), "$1")`.

The caller passes a non-nil `err`. The pattern is applied whatever `repo` holds, including an scp-like value, because the text is upstream-controlled.

**IMPLEMENTOR'S CHOICE:** whether `RedactRepo` and `repoUnsafeToEcho` share one `url.Parse` call or parse separately — the observable results must equal the steps above for every row of the TEST-1 tables.

### CODE-2. `pkg/layer/source/git.go:56-59`

Replace

```go
return nil, fmt.Errorf("%w: %v", ErrSourceUnreachable, err)
```

with

```go
// Spec: §7.3.1 (Repository credentials). go-git and net/http print the
// request URL, userinfo included, in a failed clone's error.
return nil, fmt.Errorf("%w: %s", ErrSourceUnreachable, RedactCloneError(cfg.Repo, err))
```

No other line in the file changes. `cloneOpts.URL` keeps `cfg.Repo`. The wraps at `:63-76` stay as they are.

### CODE-3. `pkg/registry/server/layers.go`

Add one unexported helper near `readableBy`:

```go
// wireLayer returns the copy of cfg a response carries. The caller keeps its
// own cfg for the store, the audit event, and the ingest runner.
//
// Spec: §7.3.1 (Repository credentials)
func wireLayer(cfg store.LayerConfig) store.LayerConfig {
	cfg.Repo = source.RedactRepo(cfg.Repo)
	return cfg
}
```

`LayerConfig` is passed by value and `Repo` is a string, so the caller's value is untouched.

- **`readableBy` (317-334).** Build `out` through `wireLayer` on both arms. The admin arm becomes a loop that appends `wireLayer(c)` in place of `append(out, configs...)`, and the visibility arm appends `wireLayer(c)`. Extend the doc comment: the returned slice is a response projection and must not be written back to the store. The result stays non-nil for an empty read.
- **Register, update, and the update no-op.** Change `LayerRegisterResponse{Layer: cfg}` to `LayerRegisterResponse{Layer: wireLayer(cfg)}` at 1283, 1293, and 1550. `resp.WebhookURL` and `resp.WebhookSecret` still read the unredacted `cfg`.

The unrouted-tenant list arm (1579) writes an empty slice and needs no change. The erase, restore, unregister, and reingest responses marshal no `LayerConfig`.

### CODE-4. `internal/serverboot/serverboot.go:679-680`

Change the log arguments to `lc.ID, source.RedactRepo(lc.Repo), lc.Ref`, with a one-line comment saying a declared `repo` may carry a URL credential and a log line carries no secret value. The file already imports `pkg/layer/source` (`internal/serverboot/serverboot.go:36`). The stored row is unchanged.

### CODE-5. `pkg/registry/server/layers.go`: the registration guard

`LayerRegisterRequest` (`pkg/registry/server/layers.go:697`) gains one member after `RotateWebhookSecret`:

```go
// ForceRepoOverwrite admits a registration the §7.3.1 re-registration rule
// would refuse: one whose repo is the value a read reports for the stored
// layer's credential-bearing repo. Read by that rule alone. Ignored on update.
ForceRepoOverwrite bool `json:"force_repo_overwrite,omitempty"`
```

Add one unexported helper beside `adminOnlyRegistrationFields`:

```go
// redactedRepoResubmitted reports whether repo is the value a read reports for
// a stored repo that carries a credential. Storing it would replace the
// credential-bearing remote with one that cannot clone a private repository.
//
// Spec: §7.3.1 (Re-registration with a reported repo)
func redactedRepoResubmitted(stored store.LayerConfig, repo string) bool {
	reported := source.RedactRepo(stored.Repo)
	return reported != stored.Repo && repo == reported
}
```

In `register`, insert one block after the admin-only-fields block (`pkg/registry/server/layers.go:1410-1418`) and before `cfg := store.LayerConfig{` (`:1420`). It reads `stored` and `exists` from the `lookupLayerForWrite` call at `:1352` and adds no store read:

```go
// spec: §7.3.1 — the re-registration rule. It runs below the write,
// local-source, and admin-only-fields rules so each keeps its envelope, and
// above every mutation so a refused request mints no secret and writes nothing.
if exists && !req.ForceRepoOverwrite && redactedRepoResubmitted(stored, req.Repo) {
	writeErrorDetails(w, http.StatusBadRequest, "registry.invalid_argument",
		"repo is the value a layer read reports for this layer, and the stored repository URL carries a credential that reads do not report; storing this value would remove it, so re-send the registration with the full repository URL, or set force_repo_overwrite (podium layer register --force-repo-overwrite) to store the value as sent",
		map[string]any{"constraint": "redacted_repo"})
	return
}
```

`update` is unchanged. It decodes the same struct (`pkg/registry/server/layers.go:1185`) and reads neither `patch.Repo` nor `patch.ForceRepoOverwrite`. `web/ui/src/api.ts` `LayerRegistration` gains no member; `RegistrationRefusal` (`web/ui/src/surfaces/RegisterLayerForm.tsx:1075-1082`) renders the envelope's code and message as it does for every other `400`.

The new member changes one existing test, in the same step. `TestLayerEndpoint_RequestAndResponseAgreeOnNames` (`pkg/registry/server/layer_wire_test.go:360`) reflects over the JSON tags of `LayerRegisterRequest` and fails for each member `store.LayerConfig` does not marshal, unless its `requestOnly` map (`:365`) names it. The guard adds no stored field, so replace the map and its comment (`pkg/registry/server/layer_wire_test.go:363-365`) with:

```go
// rotate_webhook_secret is an action the request asks for, and
// force_repo_overwrite is an admission flag the request sets. Neither is a
// stored member, so the response answers no field of either name.
requestOnly := map[string]bool{"rotate_webhook_secret": true, "force_repo_overwrite": true}
```

### CODE-6. `cmd/podium/layer.go`: `--force-repo-overwrite`

In `layerRegister` (`cmd/podium/layer.go:226`), add after the `forcePush` flag:

```go
forceRepoOverwrite := fs.Bool("force-repo-overwrite", false, "store --repo as given when it is the value 'podium layer list' reports for a layer whose stored URL carries a credential")
```

The usage string carries no backquotes, because `flag` reads a backquoted span as the flag's value name. Beside the `force_push_policy` assignment (`cmd/podium/layer.go:275-277`), add `if *forceRepoOverwrite { body["force_repo_overwrite"] = true }`. The member is sent only when the flag is set. `layerUpdate` gains no flag. Add the flag to the usage comment at `cmd/podium/layer.go:24`.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| `https://x-access-token:<token>@git.acme.com/acme/private.git` on any layer response | Reported as `https://git.acme.com/acme/private.git`; stored row unchanged; the clone sends the credential | §7.3.1 "with its userinfo removed" (SPEC-1); `docs/reference/http-api.md` layer object (DOC-1(a)) |
| Username-only token, `https://<token>@host/x.git` | Reported as `https://host/x.git` | Same |
| `ssh://git@host/x.git` | Reported as `ssh://host/x.git` (accepted, Decision 4) | §7.3.1 "with its userinfo removed" (SPEC-1); DOC-1(a) |
| scp-like `git@github.com:acme/x.git`, a filesystem path, or an empty `repo` on a `local` layer | Reported as stored | §7.3.1 "is reported as stored" (SPEC-1); DOC-1(a) |
| URL with a scheme and no userinfo | Reported byte-identical to the stored value | §7.3.1 (only userinfo is removed) (SPEC-1); DOC-1(a) |
| Redacted value differs from the stored one in percent-escaping | Accepted; the value is a display form | §7.3.1 "with its userinfo removed" (SPEC-1); DOC-1(a) |
| `://` value that does not parse | Reported as `[redacted]`; the clone error is the fixed withheld phrase | §7.3.1 "is reported as the fixed string `[redacted]`" and "is a fixed text that carries no part of the underlying error" (SPEC-1); DOC-1(a) for the reported value; DOC-1(c) "Private remotes" for the clone message and the correction |
| `http` or `https` value with `@` after its host, including a credential holding an unescaped `/` | Reported as `[redacted]`; the clone error is the fixed withheld phrase, so the operator loses the upstream diagnostic for that layer (accepted, Decision 6) | Same |
| `http` or `https` remote whose path legitimately contains `@` | Reported as `[redacted]`; a successful ingest is unaffected, and a failed clone reports the fixed withheld phrase (accepted) | Same |
| `ssh://` or other non-HTTP value whose credential holds an unescaped `/` | Not recognized; reported as stored. go-git reads no password on these schemes (accepted) | §7.3.1 scopes the `@` rule to `http` and `https` (SPEC-1); Non-goals |
| Credential in a URL query string or path segment | Not removed (deferred) | §7.3.1 names userinfo only (SPEC-1); DOC-1(a) "in the userinfo of the URL"; Non-goals |
| Tenant admin or layer owner reads the layer | Same redacted value as any caller (accepted, Decision 8) | §7.3.1 "for every caller, the layer's owner and a tenant admin included" (SPEC-1); DOC-1(a) |
| Read followed by update | Stored credential stays; update applies no `repo` | §7.3.1 "An update does not change `repo`" (SPEC-1); `docs/reference/http-api.md` Update a layer (existing text) |
| Registration that copies `repo` from a read of a layer whose stored `repo` carries a credential, including an admin re-registration of a user-defined layer and an ID that is soft-deleted inside its recovery window | `400 registry.invalid_argument`, `details.constraint: "redacted_repo"`; nothing stored, no webhook secret minted, no §8.1 event | §7.3.1 "Re-registration with a reported `repo`" (SPEC-1); DOC-1(b), DOC-1(c), DOC-1(d) |
| The same registration with `force_repo_overwrite: true` or `--force-repo-overwrite` | `201`; the stored `repo` is the value sent, with no credential; the next ingest of a private remote fails with `502 ingest.source_unreachable` (accepted, Decision 10) | Same |
| Re-registration with a different remote, or with the full URL and a credential | `201`; stored as sent; the guard does not fire | §7.3.1 "is unaffected" (SPEC-1); DOC-1(b) |
| Re-registration with the same `repo` against a layer whose stored `repo` carries no credential | `201`; the guard does not fire, with or without the member | Same |
| Caller the layer write rule does not authorize sends the reported `repo` | `403 auth.forbidden` with no `details.constraint`; the caller does not learn whether a credential is stored. A registration also on the local-source or admin-only-fields arm keeps that arm's `403` | §7.3.1 "is evaluated after" (SPEC-1); DOC-1(b) |
| `force_repo_overwrite` on an update body, or on a registration the guard would not refuse | Ignored | §7.3.1 "read for this rule alone" (SPEC-1); DOC-1(b) |
| Clone fails with a remote status other than 401, 403, and 404 | 502 body and notification carry the request URL with no userinfo and keep the status code | §7.3.1 "carries no URL userinfo" (SPEC-1); DOC-1(c) |
| Clone fails on transport (DNS, refused, TLS, timeout) | Same; the username net/http keeps is removed | Same |
| Clone fails with 401, 403, or 404 | Text is go-git's phrase plus the remote's body, which carries no URL from go-git; the pattern scrub still runs (accepted, Decision 11) | §7.3.1 (SPEC-1); Non-goals |
| Custom `LayerSourceProvider` that puts a URL in its error | Not redacted (deferred) | §7.3.1 "the built-in `git` source" (SPEC-1); Non-goals |
| Rows stored before the change | Covered with no migration, because redaction runs on output | §7.3.1 (SPEC-1); DOC-1(e) |
| Credential already disclosed by an earlier version | Not recalled by this change; the changelog entry advises rotation (RD-1) | DOC-1(e) |
| Stored `repo` at rest | Unencrypted, as today (deferred) | Non-goals |

## Testing

Every new test carries `// Spec: §7.3.1`. No new §6.10 code is added, so no `// Matrix:` annotation is owed. Credential fixtures are distinctive strings (`alice-user`, `s3cr3tpw`, `ghp_tok3n`), and each assertion checks that the output contains neither part.

### TEST-1. Unit tests for the helper and the clone error

Targets: `pkg/layer/source/redact_test.go` (new) and `pkg/layer/source/git_test.go` (extended), package `source_test`, with `t.Parallel()`.

- (a) `TestRedactRepo`, a table:

  | Input | Expected |
  |:--|:--|
  | `https://alice-user:s3cr3tpw@git.acme.com/acme/private.git` | `https://git.acme.com/acme/private.git` |
  | `https://ghp_tok3n@git.acme.com/acme/private.git` | `https://git.acme.com/acme/private.git` |
  | `http://alice-user:s3cr3tpw@git.acme.com:8080/acme/x.git` | `http://git.acme.com:8080/acme/x.git` |
  | `HTTPS://alice-user:s3cr3tpw@git.acme.com/acme/x.git` | `https://git.acme.com/acme/x.git` |
  | `ssh://git:s3cr3tpw@git.acme.com/acme/x.git` | `ssh://git.acme.com/acme/x.git` |
  | `ssh://git@git.acme.com/acme/x.git` | `ssh://git.acme.com/acme/x.git` |
  | `https://git.acme.com/acme/x.git` | input, byte-identical |
  | `https://git.acme.com/acme/my repo.git` | input, byte-identical (`u.String()` would escape the space) |
  | `HTTPS://git.acme.com/acme/x.git` | input, byte-identical (`u.String()` would lowercase the scheme) |
  | `git@github.com:acme/x.git` | input |
  | `/srv/git/acme.git` | input |
  | `file:///srv/git/a@b.git` | input |
  | empty string | empty string |
  | `https://alice-user:s3cr3t\x7fpw@host/x` | `[redacted]` |
  | `https://alice-user:s3cr3t/pw@host/x` | `[redacted]` |
  | `https://ghp_tok/3n@host/x.git` | `[redacted]` |
  | `https://alice-user:12/s3cr3tpw@host/x` | `[redacted]` |
  | `https://bob@ghp_tok/3n@host/x.git` | `[redacted]` |
  | `https://git.acme.com/acme/x.git?ref=a@b` | `[redacted]` |

  The two byte-identical rows whose `u.String()` differs from the input fail an implementation that always returns `u.String()`. The `ghp_tok/3n` and `12/s3cr3tpw` rows parse with no userinfo and pin the `@` check; the `s3cr3t/pw` row fails to parse and does not.
- (b) `TestRedactCloneError`, a table over literal upstream texts with repo `https://alice-user:s3cr3tpw@host/x.git` unless stated:
  - The go-git form `unexpected requesting "http://alice-user:s3cr3tpw@127.0.0.1:9/acme/private.git/info/refs?service=git-upload-pack" status code: 500` keeps `status code: 500` and the host and loses the userinfo.
  - The net/http forms `Get "http://ghp_tok3n@127.0.0.1:9/x": dial tcp ...` and `Get "http://alice-user:***@127.0.0.1:9/x": ...` lose the userinfo.
  - A userinfo holding raw `@` and `:` (`http://alice-user:s3@cr3t:pw@host/x`) loses all of it, which pins the greedy match.
  - A text with no URL is returned unchanged.
  - A text with two URLs loses both userinfos.
  - With each `[redacted]`-class repo from (a), the result equals the fixed withheld phrase whatever the upstream text, and contains neither credential part.
- (c) `TestGit_CloneErrorCarriesNoCredential` in `git_test.go`, against one `httptest.Server`:
  - User and password against a 500 response: `errors.Is(err, source.ErrSourceUnreachable)`, the text contains `500`, and the text contains neither credential part.
  - A username-only token against 500.
  - A password containing `!`, which the upstream text renders as `%21`; the assertion checks for neither the raw nor the escaped password.
  - A username-only token against a closed listener port.
  - An unparseable `://` repo: the text ends with the fixed withheld phrase and echoes no part of the input.
  - A 401 response: `authentication required` survives.

  One fall-through status is enough, because every status other than 401, 403, and 404 takes the same go-git arm.

### TEST-2. Handler, integration, boot, and end-to-end tests

- (a) `pkg/registry/server/layer_repo_credential_test.go` (new), on `newLayerHarness` plus the existing identity harness for the owner and non-admin arms. Each case registers a `git` layer with `https://alice-user:s3cr3tpw@git.acme.com/acme/private.git` and asserts both the response and the stored row from `GetLayerConfig`:
  - Register: `layer.repo` is `https://git.acme.com/acme/private.git`, `webhook_url` and `webhook_secret` are present, and the stored `Repo` is the full value.
  - Update, and an update that changes nothing (the no-op arm): `layer.repo` is redacted and the stored `Repo` is the full value.
  - List on the live arm and on `?deleted=true` after an unregister: `repo` is redacted on the admin arm and on the visibility arm for the layer's owner.
  - Reorder: every element's `repo` is redacted, and each stored `Repo` is still the full value after the reorder write.
  - One `ssh://git:s3cr3tpw@host/x.git` layer is reported as `ssh://host/x.git`, and one unparseable `://` repo is reported as `[redacted]`. The rest of the table stays in TEST-1(a).
  - `TestLayerRegister_RedactedRepoGuard`, subtests in the same file. Each seeds a `git` layer by registration with the credential-bearing `repo`, reads the reported value from the list response, and installs a recording `audit.Sink` through `WithAudit`. The registration that seeds each subtest is the case of an ID that names no stored layer.
    - **Refuse.** A registration of the same ID carrying the reported `repo` returns `400`, code `registry.invalid_argument`, and `details.constraint == "redacted_repo"`. The message contains `force_repo_overwrite` and neither credential part. The stored row from `GetLayerConfig` equals the row before the request, `WebhookSecret` and `CreatedAt` included, and the sink holds the one event the seeding recorded.
    - **Force.** The same body with `"force_repo_overwrite": true` returns `201`, and the stored `Repo` equals the reported value.
    - **Different remote.** `https://git.acme.com/acme/other.git` returns `201` and is stored.
    - **Full URL.** The full `repo` with its credential returns `201`, and the stored `Repo` is the full value.
    - **No stored credential.** A layer seeded with `https://git.acme.com/acme/x.git` and re-registered with that value returns `201`.
    - **Fail-closed class.** A layer seeded with `https://ghp_tok/3n@host/x.git` and re-registered with `repo: "[redacted]"` returns the `redacted_repo` refusal.
    - **Soft-deleted ID.** After `DELETE /v1/layers?id=`, the reported-`repo` registration returns the `redacted_repo` refusal, and `ListDeletedLayerConfigs` still holds the row with the full `Repo`.
    - **Unauthorized caller.** On `newClassHarness` with a non-admin `bob` and a user-defined layer owned by `alice` seeded through the store with the credential-bearing `repo`, bob's reported-`repo` registration returns `403 auth.forbidden` with an empty `details.constraint`.
    - **Admin-only arm keeps its envelope.** With `alice` as the caller on her own layer, a reported-`repo` registration that also sets `public: true` returns `403` with `admin_only_fields`.
    - **Admin re-registration of a user-defined layer.** With an admin caller and alice's seeded layer, the reported-`repo` registration returns the `redacted_repo` refusal and the stored row keeps `UserDefined` and `Owner`. The same registration with the full `repo` returns `201` and stores an admin-defined layer.
    - **Update ignores the member.** `PUT /v1/layers/update?id=` with `{"repo": <reported>, "force_repo_overwrite": true}` returns `200`, and the stored `Repo` is the full value.
  - No response body in the file contains `alice-user` or `s3cr3tpw`.
- (b) `test/integration/layer_repo_credential_test.go` (new), extending the pattern in `test/integration/reingest_pipeline_test.go:28`: `source.Git{}` as the runner and a recording notifier installed through `WithNotifier`. Mount both `endpoint.Handler()` and `endpoint.WebhookHandler()`, because the inbound route is a separate mount (`pkg/registry/server/layers.go:953`, `:1116`). One `httptest` remote answers 500 and records `r.BasicAuth()`. Register one layer whose `repo` is the remote's URL with `alice-user:s3cr3tpw`. Assert:
  - `POST /v1/layers/reingest` returns `502 ingest.source_unreachable` and the body contains neither credential part.
  - `POST /v1/ingest/webhook/{id}` with a valid signature returns the same.
  - The recorded notification body contains neither credential part.
  - The remote recorded `alice-user` and `s3cr3tpw` as basic auth. This pins the user constraint: after the redacting register response, the clone still sends the stored credential.
- (c) `internal/serverboot/declared_layers_test.go`, one test beside `TestBootstrapDeclaredLayers_GitProviderSeeded`. Declare a git layer with a credential-bearing repo, capture `log` output with the `log.SetOutput` pattern from `internal/serverboot/backend_config_test.go:233` (the test does not run in parallel), and assert that the `seeded declared git layer` line names the layer and the host and contains neither credential part, and that `GetLayerConfig` returns the full repo.
- (d) `test/e2e/declarative_layers_test.go`, one case. Reuse `declaredGitProviderConfig` with a credential-bearing `repoURL`, boot the binary, and assert that `GET /v1/layers` reports the repo without the credential and that the server log file contains neither credential part.
- (e) `test/e2e/layer_repo_credential_test.go` (new), `TestLayerRegister_RedactedRepoGuard_CLI`, against a registry started with `startServer(t, "")`, the subprocess stack `TestStandaloneLayer_UserDefinedOwnerManagedByLocalOperator` uses (`test/e2e/standalone_layer_test.go`), which runs on every platform. Through `runPodium`, run `podium layer register --id private --repo <credential URL> --ref main`, and read the reported `repo` from `GET /v1/layers`. `podium layer register --id private --repo <reported> --ref main` exits non-zero, and stderr holds `HTTP 400`, `registry.invalid_argument`, and `redacted_repo`. The same command with `--force-repo-overwrite` exits `0`. This test owns CODE-6.

Closing step: run `go test -coverpkg=./... -coverprofile=cover.out` over `./pkg/layer/source/...`, `./pkg/registry/server/...`, `./internal/serverboot/...`, and `./test/integration/...` and confirm the new lines reach 85%. Measure `cmd/podium/layer.go` through the subprocess profile in a separate run (`GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...`, then `go tool covdata textfmt`), because the CLI lines run in the spawned binary. Then run `make coverage-gate`.

### TEST-3. Manual scenario S89

See Manual validation.

## Manual validation

**TEST-3.** Add scenario S89 to `test/manual-validation.md` after S88, and the index row after the S88 row (`test/manual-validation.md:239`). If another proposal takes S89 first, use the next free S-number at apply time in the index row, the heading, and checklist step S9.

```markdown
| S89 | A git layer's URL credential is stored and used, and no response or log reports it | standalone | none | none | none |
```

The scenario text:

~~~~markdown
## S89: A git layer's URL credential is stored and used, and no response or log reports it

**Goal.** Validate that a `git` layer registered with a credential in its URL
userinfo is accepted, that the clone sends the credential, and that the
register response, the layer list, the failed-reingest error, and the server
log carry none of it, and that a re-registration carrying the listed remote
is refused unless it passes the override flag.

**Covers.** The §7.3.1 repository credentials and re-registration rules through the compiled binary
and the `podium layer` commands.

**Why by hand.** An operator reads the raw `podium layer register` and
`podium layer list` output and the reingest error on a terminal. The string
`s89-token` in any of them, or in the server log, is the defect this scenario
catches. A remote log with no `Authorization` header is the opposite defect:
the credential was removed from the stored layer.

**Steps.**

1. Run the isolation block. Start a stub remote that answers every request
   with 500 and logs the `Authorization` header, then serve a standalone
   registry.

   ```bash
   cat > "$WORK/remote.py" <<'EOF'
   import http.server, sys
   class H(http.server.BaseHTTPRequestHandler):
       def do_GET(self):
           open(sys.argv[1], "a").write((self.headers.get("Authorization") or "none") + "\n")
           self.send_response(500); self.end_headers()
       def log_message(self, *a): pass
   http.server.HTTPServer(("127.0.0.1", 8190), H).serve_forever()
   EOF
   python3 "$WORK/remote.py" "$WORK/remote.log" &
   REMOTE=$!
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8189 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8189/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8189
   ```

   **Expect.** `server_alive` reports the server running.

2. Register a layer whose remote carries a credential.

   ```bash
   podium layer register --registry "$PODIUM_REGISTRY" --id private --public \
     --repo "http://x-access-token:s89-token@127.0.0.1:8190/acme/private.git" --ref main | tee "$WORK/register.out"
   grep -c "s89-token" "$WORK/register.out"
   ```

   **Expect.** The command succeeds and prints the webhook URL and secret. The
   printed `repo` is `http://127.0.0.1:8190/acme/private.git`, and the count is
   `0`. A refusal of the registration is a defect.

3. List the layers.

   ```bash
   podium layer list --registry "$PODIUM_REGISTRY" | tee "$WORK/list.out"
   grep -c "s89-token\|x-access-token" "$WORK/list.out"
   ```

   **Expect.** The `private` layer's `repo` is
   `http://127.0.0.1:8190/acme/private.git`, and the count is `0`.

4. Reingest the layer, which fails against the stub.

   ```bash
   podium layer reingest --registry "$PODIUM_REGISTRY" private 2>&1 | tee "$WORK/reingest.out"
   grep -c "s89-token\|x-access-token" "$WORK/reingest.out"
   cat "$WORK/remote.log"
   ```

   **Expect.** The command reports `ingest.source_unreachable` with a message
   that names `127.0.0.1:8190` and status `500`. The count is `0`.
   `remote.log` holds at least one line beginning `Basic `, which shows the
   clone sent the stored credential. A line reading `none` on every request
   means the credential was lost.

5. Stop the stub so the next reingest fails on the connection, and reingest
   again.

   ```bash
   kill "$REMOTE"; wait "$REMOTE" 2>/dev/null
   podium layer reingest --registry "$PODIUM_REGISTRY" private 2>&1 | tee "$WORK/reingest2.out"
   grep -c "s89-token\|x-access-token" "$WORK/reingest2.out" "$WORK/srv.log"
   ```

   **Expect.** The command reports `ingest.source_unreachable` with a
   connection error. Both counts are `0`.

6. Re-register the layer with the remote exactly as step 3 listed it.

   ```bash
   podium layer register --registry "$PODIUM_REGISTRY" --id private --public \
     --repo "http://127.0.0.1:8190/acme/private.git" --ref main 2>&1 | tee "$WORK/reregister.out"
   grep -c "s89-token\|x-access-token" "$WORK/reregister.out"
   ```

   **Expect.** The command fails with HTTP 400, `registry.invalid_argument`,
   and `"constraint": "redacted_repo"`. The message names the full repository
   URL and `--force-repo-overwrite` as the two corrections. The count is `0`.

7. Repeat the registration with the flag.

   ```bash
   podium layer register --registry "$PODIUM_REGISTRY" --id private --public --force-repo-overwrite \
     --repo "http://127.0.0.1:8190/acme/private.git" --ref main
   ```

   **Expect.** The command succeeds and prints a webhook URL and secret. The
   stored remote now carries no credential.

**Cleanup.** Stop the server with `kill "$SRV"` and `rm -rf "$WORK"`.
~~~~

**IMPLEMENTOR'S CHOICE:** the stub remote's implementation and the exact `grep` filters — each step must still produce its Expect outcome as written, the stub must answer 500 and record whether basic auth arrived, and ports 8189 and 8190 must stay unused by every other scenario.

## Documentation changes

**DOC-1.** It lands in step S10. The per-value rule is stated once, in the HTTP API layer object table, and the other pages link to it.

(a) `docs/reference/http-api.md`, layer object table, `repo` row (line 376). Replace the Notes cell "The Git remote for a `git` source." with:

> The Git remote for a `git` source. A credential in the userinfo of the URL is not reported: a value written as a URL with a scheme is reported with its userinfo removed, for every caller. A value that is not written as a URL with a scheme, such as `git@github.com:acme/x.git`, is reported as stored. A value written with a scheme that does not parse as a URL, and an `http` or `https` value that carries `@` after its host, is reported as `[redacted]`.

(b) `docs/reference/http-api.md`, "Register a layer" (line 396). Add after the section's opening description:

> A `repo` URL may carry a credential in its userinfo. The registry stores the value as given and clones with it. A registration that names an existing layer ID replaces the stored `repo` with the value the request carries. A `repo` copied from a layer read holds no credential, so the request carries the full remote. A registration whose `id` names an existing layer, a soft-deleted one inside its recovery window included, and whose `repo` equals the value a read reports for a stored `repo` that carries a credential is refused with `400 registry.invalid_argument` carrying `details.constraint: "redacted_repo"`. The refusal is evaluated after the layer write, local-source, and admin-only-fields rules, stores nothing, and names no part of the stored value. Setting the boolean `force_repo_overwrite` to true admits that registration and stores the `repo` as sent. The field has no effect on any other registration, and `POST|PUT /v1/layers/update` ignores it.

The "Update a layer" section is unchanged.

(c) `docs/deployment/layers.md`. Add a subsection after the register examples, before "### Who may set a layer's owner and visibility":

> ### Private remotes
>
> A `git` layer for a private HTTPS repository carries its credential in the userinfo of the `--repo` URL:
>
> ```bash
> podium layer register --id team-private \
>   --repo "https://x-access-token:<token>@git.acme.com/acme/private.git" --ref main
> ```
>
> The registry stores the URL as given and clones with it. `podium layer list`, the register and update responses, and the `ingest.source_unreachable` message of a failed clone carry no credential. The first two report the remote on the terms the [layer object](../reference/http-api#layers) states. Percent-encode a `/` or an `@` inside the credential. A layer whose remote is listed as `[redacted]` reports a failed clone with a fixed message that carries no detail from the remote or the network. To restore the detail, re-register the layer with the credential percent-encoded, or with a URL that has no `@` after its host. Re-registering the layer's ID replaces the stored remote, so pass the full `--repo` with its credential each time. The registry refuses a re-registration whose `--repo` is the remote as `podium layer list` reports it, with `registry.invalid_argument` carrying `details.constraint: "redacted_repo"`. Pass `--force-repo-overwrite` to store that value and drop the credential.

In the paragraph under "### Who may register a local-source layer" that contains "Re-register it under an admin identity", and in the paragraph under "### Who may set a layer's owner and visibility" that describes re-registering a user-defined layer as admin-defined, add one sentence each:

> A re-registration of a `git` layer passes the full `--repo`, including any credential, because `podium layer list` reports the remote without it and the registry refuses the reported value (see [Private remotes](#private-remotes)).

**IMPLEMENTOR'S CHOICE:** the anchor fragment of the links to the layer object table in (c) and (d) — each must resolve on the built site to the section of `docs/reference/http-api.md` that holds the table.

(d) `docs/reference/cli.md`, `podium layer register` (line 443). Add one sentence after the synopsis:

> A `--repo` URL may carry a credential in its userinfo. The registry stores it as given, and `podium layer list` reports the remote without it (see the layer object in the [HTTP API reference](http-api#layers)). A registration under an existing layer ID whose `--repo` is that reported value is rejected with `registry.invalid_argument` carrying `details.constraint: "redacted_repo"`. `--force-repo-overwrite` admits it and stores the remote as given, without the credential.

In the synopsis, append ` [--force-repo-overwrite]` to the `--repo` line (`docs/reference/cli.md:448`).

(e) `CHANGELOG.md`, under `## [Unreleased]`, add a `### Fixed` group with:

> - **A git layer's URL credential is no longer reported** (§7.3.1): a `repo` such as `https://x-access-token:<token>@git.acme.com/acme/private.git` is still accepted, stored, and used to clone. The layer responses (`POST /v1/layers`, `POST|PUT /v1/layers/update`, `GET /v1/layers`, and `POST /v1/layers/reorder`), the `ingest.source_unreachable` message of a failed clone, the failed-ingest notification, and the boot log line for a declared layer no longer carry the credential. A `repo` that is reported as `[redacted]`, which is a value written with a scheme that does not parse as a URL or an `http` or `https` URL with `@` after its host, reports a failed clone with a fixed message that carries no upstream detail. Earlier versions returned the stored URL on a layer read that is not admin-gated. Rotate any token that was stored in a layer `repo` on an earlier version.

Add a `### Changed` group above `### Fixed`, with:

> - **Re-registering a layer with its reported `repo` is refused** (§7.3.1): `POST /v1/layers` under an existing layer ID, with a `repo` equal to the value a read reports for a stored `repo` that carries a credential, returns `400 registry.invalid_argument` carrying `details.constraint: "redacted_repo"`. The `force_repo_overwrite` body field and `podium layer register --force-repo-overwrite` admit it.

`docs/reference/error-codes.md` is unchanged: its `registry.invalid_argument` row (line 162) names no `details.constraint` value.

`docs/deployment/layers.md` and `docs/reference/cli.md` are already mapped in `tools/doccov/manifest.yaml`, so the new fenced block needs no manifest entry.

## Resolved decisions

The draft raised these as open questions. The reviewer settled them on 2026-10-08, and none remains open.

**RD-1. Release vehicle and disclosure wording.** The change ships as a 0.5.2 patch release from `release/0.5.x`, following the patch-release path in `.claude/rules/release-process.md`, and is merged back into `main`. The `Fixed` changelog entry advises operators to rotate any token that was stored in a layer `repo` on an earlier version (DOC-1(e)).

**RD-2. The placeholder literal.** A value in a fail-closed class is reported as `[redacted]`. An empty string was rejected because an empty `repo` on a `git` layer reads as missing data, and `[redacted]` is the placeholder the audit log already uses (`pkg/audit/retention.go:51`).

**RD-3. Names for the guard.** The request member is `force_repo_overwrite`, the CLI flag is `--force-repo-overwrite`, and the constraint value is `redacted_repo`. The earlier names `allow_redacted_repo` and `--allow-redacted-repo` are replaced throughout. A registration replaces the stored `repo` whether or not the member is set; the member is read only by the re-registration rule, and the flag help and DOC-1 say so.

**RD-4. Scope of the guard.** The force member is kept, a soft-deleted ID inside its §8.4 recovery window is covered, and the Web UI gains no control for the member.

**RD-5. The `@`-after-host class.** The fail-closed check on an `http` or `https` value with `@` after its host is kept (Decision 6).

## Non-goals

- Refusing, rejecting, warning on, or altering URL userinfo at registration or update, or changing the stored `repo` in any way. This is the user constraint. The Decision 10 guard refuses a `repo` that carries no userinfo and is outside it.
- An out-of-band clone credential mechanism: a credential field, a secret reference, a key-file upload, a `CloneOptions.Auth` wiring, or a credential SPI.
- Encryption of the stored `repo` at rest.
- A credential carried in a URL query string or path segment. The rule covers URL userinfo, plus the fail-closed `@` check on `http` and `https` values.
- Recognizing a credential that an unescaped `/` moved out of the userinfo of a URL under a scheme other than `http` and `https`.
- Any migration, rewrite, or scan of stored layer rows.
- A per-caller reveal of the full `repo` to a tenant admin or the layer owner, or a separate admin-gated read.
- Keeping the username of a non-HTTP URL in the reported value (Decision 4).
- A general §7.2.1 rule for credentials supplied inside other values, and redaction of the §7.3.2 receiver `url` (Decision 12).
- Changes to §7.6 change events, §7.3.2 outbound webhook payloads, or §8.1 audit events, which carry neither the repo nor the clone error text.
- Web UI source changes or a `web/bundle` rebuild. The UI displays the read value and never re-submits it, and its register form renders the `redacted_repo` refusal as it renders any other `400`. A form control for `force_repo_overwrite` is absent.
- Changing scp-like remotes, or documenting or configuring the ambient ssh-agent fallback for SSH remotes.
- Filtering the remote's response body that go-git appends to 401, 403, and 404 errors, beyond the URL-userinfo pattern.
- Redacting error text from a custom `LayerSourceProvider` plugin. The fix covers the built-in git provider's clone error.
- A new §6.10 error code, environment variable, or `registry.yaml` key, and any flag beyond `--force-repo-overwrite`.
- Applying the guard to `update`, to a declared layer's boot write, to `podium admin migrate`, or to a near match of the reported value.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-10-08)

The draft's per-change challenge revisions disagreed on one point, and the proposal resolves it as follows.

- **Per-scheme split or one rule.** The SPEC-1 revision asked for one userinfo rule for every scheme, while the CODE-1, TEST-2, and DOC-1 revisions kept the username for schemes other than `http` and `https`. The proposal takes the single rule (Decision 4), which is the smaller change and matches the SPEC-1 staged text. CODE-1 has no scheme branch for removal, TEST-1(a) and TEST-2(a) expect `ssh://host/x.git`, and DOC-1(a) states one rule.
- **The `@` check covers a value that also carries userinfo.** The CODE-1 revision applied the check only when the parsed URL had no userinfo. `https://bob@ghp_tok/3n@host/x.git` parses with userinfo `bob` and would have been reported as `https://ghp_tok/3n@host/x.git`. `repoUnsafeToEcho` now ignores whether userinfo is present, and TEST-1(a) carries the row.
- **The fixed clone-error phrase.** The draft's phrase said the URL does not parse, which is false for the `@` class. The phrase is now `clone failed; the error text is withheld because the repository URL cannot be reported`, defined once as `cloneErrorWithheld`.
- **The §7.2.1 sentence is dropped** (Decision 12), and the SPEC-1 error sentence is scoped to the built-in `git` source to match CODE-2.

### Pass 1 (2026-10-08, automated)

- **Web UI form paths in Decision 10.** Decision 10 cited the register and update forms under `web/ui/src/components/`, where neither file exists. Both forms live under `web/ui/src/surfaces/`. Decision 10 now cites `web/ui/src/surfaces/RegisterLayerForm.tsx:61` and `web/ui/src/surfaces/UpdateLayerForm.tsx`. The claim itself holds at the corrected paths, so the decision and the unchanged Web UI source stand.

### Pass 2 (2026-10-08, automated)

- **Sink function name in Decision 7.** The rejected-alternative sentence in Decision 7 named a function `handleIngestError`, which does not exist in the repository. The function that maps `ErrSourceUnreachable` to the 502 `ingest.source_unreachable` body is `writeReingestError` (`pkg/registry/server/layers.go:1984`), which `runIngestAndRespond` calls after `notifyIngestFailure` (`pkg/registry/server/layers.go:1879-1880`). Decision 7 now names `writeReingestError` and cites both sinks by line. The rationale for rejecting the alternative and every staged edit are unchanged.

### Redesign 1 (2026-10-08, automated)

The redesign at `tmp/redesign/0052-report-a-git-layer-s-repo-without-its-url-credential-in-ever-redesign-1.md` was applied in full. Every anchor it quotes occurred once in the proposal, and no edit was skipped.

- **Area redesigned: the round trip of a reported `repo` through registration.** The proposal accepted that a registration copying `repo` from a read stores a remote with no credential, and it instructed the implementor to add no guard. The user asked for the guard to be folded into this proposal (2026-10-08). `POST /v1/layers` now refuses that registration with `400 registry.invalid_argument` carrying `details.constraint: "redacted_repo"`, unless the request sets `force_repo_overwrite`. The refused value never carries URL userinfo, so Decision 1 and the first fixed decision stand unedited.
- **Why.** Output redaction makes the reported `repo` differ from the stored one, so a client that reads a layer and re-registers it would remove the credential the layer clones with, and the failure would surface only at the next ingest. The guard reads the stored row that `lookupLayerForWrite` already returns and persists nothing.
- **What was added.** SPEC-1 stages a second paragraph, "Re-registration with a reported `repo`", and SPEC-2 stages one clause in the §7.3.1 **Errors** paragraph. CODE-5 stages the guard, the `force_repo_overwrite` request member, and the `requestOnly` entry in `pkg/registry/server/layer_wire_test.go` that the existing name-parity test requires. CODE-6 stages `podium layer register --force-repo-overwrite`. TEST-2(a) gains the `TestLayerRegister_RedactedRepoGuard` subtests, and TEST-2(e) is a new end-to-end CLI case in `test/e2e/layer_repo_credential_test.go`. Scenario S89 gains steps 6 and 7. DOC-1(b), (c), (d), and (e) state the refusal and the override, and DOC-1(e) adds a `### Changed` changelog group. Open questions OQ-3 and OQ-4 are new.
- **What the redesign deleted.** The fixed decision "no guard is added" and the "Watch out for" instruction against adding a guard are removed. The final clause of the SPEC-1 paragraph, which said a registration copying `repo` from a read stores a remote with no credential, is removed. The TEST-2(a) round-trip case that asserted the silent overwrite is removed together with its conditional deletion instruction, and the edge-case row for the silent overwrite is replaced by the guard rows. The words "no flag" in the second fixed decision and "flag" in Decision 2 and in the last non-goal are removed, as is the paragraph count in the Summary's first bullet. The silent overwrite remains reachable only through `force_repo_overwrite`.
- **Reconciliation.** The guard step is S6, and the former S6 through S9 are S7 through S10, so every dependency names an earlier step. S8, S9, and S10 depend on S6. The ordering paragraph, the S-number reference in Manual validation, and the DOC-1 step reference follow the renumbering. Files added to the change are `pkg/registry/server/layer_wire_test.go`, `cmd/podium/layer.go`, and `test/e2e/layer_repo_credential_test.go`. `docs/reference/error-codes.md` and the Web UI source stay unchanged.
- **Open decisions the redesign recorded.**
  - OD-1, attribution. The guard is worded as folded in at the user's request, and its design details are listed under OQ-4 for sign-off. The relayed request does not state the conditions, the force member, or the names, so they are not marked as closed user decisions.
  - OD-2, the force member. `force_repo_overwrite` and `--force-repo-overwrite` are kept, so one request can still store the credential-free form of the same remote. The alternative is a guard with no new wire surface and no request that stores that form while the ID exists.
  - OD-3, soft-deleted IDs. The guard covers an ID inside its §8.4 recovery window, because it reads `exists` from `lookupLayerForWrite` as returned.
  - OD-4, checklist numbering. The guard step is inserted as S6 with renumbering.
  - OD-5, names. `force_repo_overwrite`, `--force-repo-overwrite`, and `redacted_repo` await confirmation under OQ-3.
- **Status line.** The Status line records a convergence that predates these edits. The staged spec, code, and tests changed after it, and the review loop resets the line.

### Pass 3 (2026-10-08, automated)

- **The withheld clone error for a `[redacted]`-class `repo` had no staged spec or doc text.** The fixed phrase that replaces the upstream clone error existed only in Decisions 6 and 7, CODE-1, the edge-case table, and the TEST-1(b) and TEST-1(c) assertions that cite §7.3.1. SPEC-1 and DOC-1(a) described the reported `repo` alone, and the DOC-1(c) clause said the `ingest.source_unreachable` message reports the remote, which is false for that class. The SPEC-1 "Repository credentials" paragraph now states that the built-in `git` source's clone failure message is a fixed text with no part of the underlying error for a `repo` reported as `[redacted]`. The staged "### Private remotes" subsection in DOC-1(c) states the same outcome and the correction, and its clause now says the listed outputs carry no credential. The edge-case rows for the fail-closed classes cite the new sentences, and the Summary's first bullet names the added statement. The spec sentence does not quote the phrase, so `cloneErrorWithheld` stays defined in CODE-1 alone. No code, test, or checklist step changes.
- **The DOC-1(e) changelog bullet kept the claim the previous bullet removed from DOC-1(c).** The staged `### Fixed` entry still said the `ingest.source_unreachable` message, the failed-ingest notification, and the boot log line report the remote with its userinfo removed. That statement is false for a `repo` reported as `[redacted]`, and it contradicted the new SPEC-1 sentence and the reworded DOC-1(c) text. The entry now says those outputs no longer carry the credential, and it adds one sentence stating that a `repo` reported as `[redacted]` reports a failed clone with a fixed message that carries no upstream detail. DOC-1(e) joins SPEC-1, DOC-1(c), the edge-case rows, and the Summary bullet as a site this pass edited.

### Sign-off review (2026-10-08)

- **Open questions resolved.** The reviewer settled the open questions, and the "Open questions" section is replaced by "Resolved decisions" (RD-1 through RD-5). The guard's request member and flag are renamed to `force_repo_overwrite` and `--force-repo-overwrite` throughout, the changelog entry gains the rotation sentence, and the statements that marked a name, the literal, or the guard's scope as awaiting confirmation now cite the resolved decision. The Redesign 1 record above keeps its open-decision list as written at the time, with the names in their final spelling.
