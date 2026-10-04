# Proposal 0050: Bound each podium sign Sigstore request with a per-request deadline set by PODIUM_SIGSTORE_REQUEST_TIMEOUT

- Issue: (to be filed)
- Status: Applied to spec (2026-10-04). Signed off under the overnight authorization. Signed off as staged. OQ-1: no retry, including for the TSA request; the operator reruns podium sign. OQ-2: keep the 60s default, overridable by PODIUM_SIGSTORE_REQUEST_TIMEOUT.
- Date: 2026-10-04

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §6.2 gains a `PODIUM_SIGSTORE_REQUEST_TIMEOUT` row: a per-request deadline, `60s` by default, on each Fulcio, timestamp-authority, and Rekor request `podium sign --provider sigstore-keyless` makes, with `config.invalid` for a value that is not a positive duration (SPEC-1).
- §4.7.9 gains one paragraph: `podium sign` sends each Sigstore request once under that deadline, and a request that fails, answers with an error status, or misses its deadline fails the signing with no envelope. SPEC-2 states which failures name the endpoint URL.
- `pkg/sign` gains `DefaultRequestTimeout`, a `SigstoreKeyless.RequestTimeout` field, and a per-request context deadline inside the shared `post` helper. Fulcio is routed through `post`, which removes its unbounded body reads (CODE-1).
- `cmd/podium/sign.go` parses the variable for `podium sign` alone and refuses an invalid value with `config.invalid` before any Sigstore request (CODE-2).
- Tests cover the unit level in `pkg/sign` and `cmd/podium` and the end-to-end level on the binary (TEST-1, TEST-2, TEST-3). The CLI reference and the `[Unreleased]` changelog follow (DOC-1, CL-1).

**Fixed decisions.**

- Each request has its own deadline, of length D. The three requests stay sequential (Fulcio, then the TSA, then Rekor), so a whole `Sign` waits at most about 3×D.
- The deadline covers sending the request and reading the whole response body.
- One variable, `PODIUM_SIGSTORE_REQUEST_TIMEOUT`, sets D for all three requests. It is environment-only, read only by `podium sign --provider sigstore-keyless`, and has its row in §6.2 directly after `PODIUM_SIGSTORE_TSA_URL`. It gets no §13.12 row.
- The default is `60s` (`sign.DefaultRequestTimeout`). OQ-2 asks the reviewer to confirm it.
- An unset value, or one that is empty after surrounding whitespace is removed, takes the default. Any other value that does not parse as a positive Go duration, including `0`, `0s`, a negative value, and a bare number such as `60`, refuses the command with `error: config.invalid: PODIUM_SIGSTORE_REQUEST_TIMEOUT ...` and exit 1. No configuration removes the bound.
- No request is retried. Rerunning `podium sign` is the retry. OQ-1 asks about a TSA-only retry.
- A timeout is reported on the existing uncoded failure path, `sign failed: <fulcio|tsa|rekor>: ...` with exit 1. The message names the endpoint URL and ends with `(no response within <D>)`. No §6.10 code is added, and timeouts are not mapped to `config.signature_provider_unavailable`.
- The library owns the default: a zero or negative `RequestTimeout` in `pkg/sign` means `DefaultRequestTimeout`. The CLI only parses the variable and sets the field.
- The deadline is a `context.WithTimeout` derived from each request's own context. It is never set as `http.Client.Timeout`, so an injected `Client` is still bounded and a cancelled parent context still cancels the request.
- `podium verify`, `SigstoreKeyless.Verify`, `podium-mcp`, server-source `podium sync`, and the SDKs do not change and do not read the variable.

**Watch out for.**

- **Spec and code line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **`post` keeps its signature.** `post(req *http.Request) ([]byte, error)` stays as it is, so `pkg/sign/rekor.go` and `pkg/sign/timestamp.go` need no edit. Derive the deadline from `req.Context()` inside `post`. Do not move request construction into the helper. `rekor.go` and `timestamp.go` keep their post-2xx error text; SPEC-2 does not require the URL there.
- **`cancel` must run after the body read.** The deadline must also bound `io.ReadAll`, so `defer cancel()` sits in `post` and fires on return, after the read. Cancelling right after `Do` returns would cut off every response body.
- **The duration prints in Go form.** `time.Duration.String()` prints `60s` as `1m0s`, so the default deadline appears as `(no response within 1m0s)`. Tests use `1s` and `300ms`, which print as written. No test asserts the default's text.
- **The parent-cancel case must not carry the suffix.** A caller's own cancellation or deadline is returned without the `(no response within ...)` suffix, under the predicate in CODE-1 step 4; TEST-1's parent-cancel and parent-deadline cases pin it.
- **Existing error substrings must survive.** `pkg/sign/sigstore_test.go` asserts `rekor: HTTP 503`, `tsa: HTTP 503`, and `rekor: HTTP 404` (`:462`, `:468`, `:498`). The new non-2xx format `HTTP %d from %s: %s` keeps `HTTP <status>` directly after the service prefix, so those assertions hold. Fulcio's own `fulcio: HTTP %d` prefix is removed in CODE-1, because `Sign` already prefixes `fulcio:` (`pkg/sign/sigstore.go:151`), which today yields `fulcio: fulcio: HTTP ...`.
- **The CLI parses the variable before `Sign` checks the token.** `loadSignatureProvider(..., keyForSign)` runs before `provider.Sign` (`cmd/podium/sign.go:55-60`). A stray `PODIUM_SIGSTORE_REQUEST_TIMEOUT` in a developer shell would turn `TestPodiumSign_SigstoreKeylessWithoutOIDCTokenRefused` into a `config.invalid` failure, so TEST-3 adds the variable to the `keylessFixture.env` defaults, and TEST-2 sets it to `""` in the existing endpoint-default test (`cmd/podium/cli_helpers_test.go:212`).
- **The `<artifact>` form reaches the registry first.** `resolveArtifactSignature` runs at `cmd/podium/sign.go:44`, before the variable is parsed at `:55`. Every text in this proposal therefore says "before it contacts any Sigstore endpoint" rather than "any service".
- **A hanging test handler can block `httptest.Server.Close`.** `Close` waits for in-flight handlers. Each hanging handler selects on `r.Context().Done()` and on a channel the test closes in `t.Cleanup` before the server closes.
- **The deadline also applies to the working fake requests.** A hang case for the TSA or Rekor first runs a real fake Fulcio request under the same deadline. Under `-race`, ECDSA leaf issuance in the fake Fulcio (`internal/testharness/sigstoreharness/server.go:96-126`) is slow, which is why TEST-1 uses a `1s` deadline rather than a few hundred milliseconds.
- **Parts of `test/e2e` skip silently on macOS.** Grep the run output for `SKIP` before claiming end-to-end verification.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §6.2 gains the `PODIUM_SIGSTORE_REQUEST_TIMEOUT` row after `PODIUM_SIGSTORE_TSA_URL`.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §4.7.9 gains the single-attempt, per-request-deadline paragraph after the keyless verifier paragraph.
      Levels: —. Depends on: —
- [ ] **S3 · code** — CODE-1. `pkg/sign` gains `DefaultRequestTimeout`, `RequestTimeout`, the deadline inside `post`, and Fulcio routed through `post`.
      Levels: unit. Depends on: S1, S2
- [ ] **S4 · code** — CODE-2. `podium sign` parses `PODIUM_SIGSTORE_REQUEST_TIMEOUT` and sets `RequestTimeout`; `podium verify` does not read it.
      Levels: unit, e2e. Depends on: S3
- [ ] **S5 · test** — TEST-1. `pkg/sign` deadline unit suite against hanging, slow-body, delayed, error-status, and oversized-Fulcio-response endpoints, plus the default-fallback, parent-cancel, and parent-deadline cases.
      Levels: unit. Depends on: S3
- [ ] **S6 · test** — TEST-2. `cmd/podium` parsing table, the verify-ignores-the-variable check, and the empty value in the existing endpoint-default test.
      Levels: unit. Depends on: S4
- [ ] **S7 · test** — TEST-3. End-to-end hanging-Fulcio and invalid-value cases on the binary, plus the fixture default and the invalid-value override in `TestPodiumVerify_SigstoreKeyless`.
      Levels: e2e. Depends on: S4
- [ ] **S8 · docs** — DOC-1. The `docs/reference/cli.md` environment row and the `podium sign` paragraph sentences.
      Levels: —. Depends on: S4
- [ ] **S9 · docs** — CL-1. The `[Unreleased]` `Fixed` entry.
      Levels: —. Depends on: S4

**Ordering constraints.** S1 and S2 land the text every later step cites. S3 precedes S4 because CODE-2 reads `sign.DefaultRequestTimeout` and sets `RequestTimeout`. S5 may proceed in parallel with S4. S6, S7, S8, and S9 need the CLI wiring from S4. Land S8 in the same pull request as S4 so no released build documents behavior it lacks.

## Current state and the gap

### Three outbound requests with no time bound

`podium sign --provider sigstore-keyless` makes three outbound POSTs, in this order: the Fulcio certificate request, the RFC 3161 timestamp request, and the Rekor v2 entry upload. `SigstoreKeyless.Sign` calls `mintCert` (`pkg/sign/sigstore.go:149`), then `requestTimestamp` (`:157`), then `uploadRekor` (`:161`).

Fulcio is sent with `s.httpClient().Do(req)` directly (`pkg/sign/fulcio.go:94`, `:101`), and its body is read with an unbounded `json.NewDecoder(resp.Body).Decode` (`:112`). On a non-2xx answer the body is read with an `io.ReadAll` that has no size limit (`:107`). The TSA and Rekor requests go through the shared `post` helper (`pkg/sign/sigstore.go:85-99`; `pkg/sign/timestamp.go:85`, `:91`; `pkg/sign/rekor.go:90`, `:96`). That helper reads the body with `io.ReadAll(io.LimitReader(...))` (`pkg/sign/sigstore.go:91`), which caps the size but sets no time bound.

`httpClient()` returns `http.DefaultClient` when `Client` is nil (`pkg/sign/sigstore.go:76-81`). The binary never sets `Client` (`cmd/podium/sign.go:247-261`), so production uses `http.DefaultClient`, which has no `Timeout`. `http.DefaultTransport` bounds only the dial (30s) and the TLS handshake (10s). It sets no `ResponseHeaderTimeout` and no overall deadline. `signCmd` passes `context.Background()` to `provider.Sign` (`cmd/podium/sign.go:60`), and `Sign` passes that context unchanged to all three requests. An endpoint that accepts the connection and never answers, or that sends the response body slowly, therefore blocks `podium sign` until the process is killed.

### The rules require a bound and the spec states none

`.claude/rules/code-best-practices.md` requires a timeout on every outbound call. It also requires that a non-spec default be operator-overridable and documented. Neither §4.7.9 nor the §6.2 `PODIUM_SIGSTORE_*` rows state a deadline, a retry, or a failure behavior for these calls. Adding a deadline is therefore a spec change. Proposal 0043 recorded this gap as a separate follow-up.

### How a failure is reported today

An endpoint failure is wrapped as an uncoded `fulcio: %w`, `tsa: %w`, or `rekor: %w` error (`pkg/sign/sigstore.go:151`, `:159`, `:163`), and the command prints it as `sign failed: ...` with exit 1 (`cmd/podium/sign.go:60-62`). The repository has no coded "endpoint unavailable" path. `ErrSigstoreUnavailable` (`config.signature_provider_unavailable`, `pkg/sign/sigstore.go:126-130`) is returned only for missing configuration (`:138-139`).

A transport error from `http.Client.Do` is a `*url.Error`, which formats as `Post "<url>": <cause>`, so a deadline that fires in `Do` already names the endpoint URL. A deadline that fires while the body is read in `post` is reported as `read response: ...` (`pkg/sign/sigstore.go:92-93`), which does not name the URL. Neither does the Fulcio decode error (`pkg/sign/fulcio.go:112-113`).

### Verification is offline

Under `sigstore-keyless`, `podium verify` makes no Sigstore call. `SigstoreKeyless.Verify` ignores its context and runs only local checks (`pkg/sign/sigstore.go:177-227`). Its trust root is a local file (`cmd/podium/sign.go:248`). §4.7.9 states that the verifier makes no network call.

## Decisions

- **One deadline per request.** Each of the three requests gets its own deadline of length D, so a slow Fulcio does not use up the time for the TSA and Rekor requests. The deadline covers sending the request and reading the whole response body, so a server that sends the body slowly is bounded too. The requests stay sequential, so a whole `Sign` waits at most about 3×D.
- **One variable.** `PODIUM_SIGSTORE_REQUEST_TIMEOUT` sets D for all three requests. Its value is a Go duration, matching the format of `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` (§13.12). Only `podium sign --provider sigstore-keyless` reads it. Its row goes in §6.2 directly after `PODIUM_SIGSTORE_TSA_URL`. It gets no §13.12 row, because §13.12 covers server-side backend configuration and its Signing subsection places consumer-side signing settings in §6.2 and §7.5.2. It is environment-only, with no flag and no config-file key, like the other `PODIUM_SIGSTORE_*` rows.
- **Default D = 60s.** The evidence comes from upstream sources that are not vendored in this repository (sigstore-go is absent from `go.mod` and the module cache), and the reviewer should confirm it. The sigstore-go signing example sets per-request timeouts of 30s for Fulcio and the TSA and 90s for Rekor, and cosign's whole-operation `--timeout` defaults to 3m. 60s is twice the upstream Fulcio and TSA values. With three sequential requests, the worst case is 3m, the same as cosign's overall default. The repository records no latency figures: the staging fixture `meta.json` holds only the hash, the identity, and the issuer.
- **An invalid value is refused.** A value that is set but invalid is refused rather than replaced by the default. `podium sign` exits 1 with `error: config.invalid: PODIUM_SIGSTORE_REQUEST_TIMEOUT ...` before any Sigstore request. This reuses the existing §6.10 code `config.invalid` and the code-prefix convention the CLI already uses for `config.signature_provider_unavailable` (`cmd/podium/sign.go:279`, `:290`). An unset value, or one that is empty after surrounding whitespace is removed, takes the default; the whitespace rule matches `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` in §6.2. Zero or a negative value is invalid, so no configuration removes the bound. This departs from the `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` precedent, which silently falls back to its default (`internal/serverboot/serverboot.go:297-303`), because an interactive CLI invocation should report an operator's typo instead of quietly ignoring it.
- **No retry.** The spec states that each request is sent once. The Rekor v2 upload appends to a public append-only log, and nothing in `spec/`, `pkg/sign`, or `internal/testharness/sigstoreharness` shows that a re-POST of the same `hashedrekord` v0.0.2 body is deduplicated, so a timed-out request that did land could leave a duplicate entry. Every successful Fulcio mint returns an SCT, meaning the certificate is logged to a public CT log, so a retried mint publishes another entry. The TSA request has no side effects, but retrying it alone saves only one round trip, and a retry would add a jittered-backoff path plus an attempt count, which is another non-spec default needing an override. Rerunning `podium sign` is the retry. OQ-1 asks whether the reviewer wants a TSA retry anyway.
- **Uncoded failure path.** A timeout is reported through the existing uncoded failure path: `sign failed: <fulcio|tsa|rekor>: ...`, exit 1. The message names the endpoint URL and the deadline that passed. No new §6.10 code is added. Timeouts are not mapped to `config.signature_provider_unavailable` either, because that code means missing configuration (`pkg/sign/sigstore.go:126-130`), and `spi.Error.Error()` prints only the message (`pkg/spi/errors.go:33`), so a coded error would need a formatting change that is out of scope.
- **The library owns the default.** The deadline lives in `pkg/sign` so the library has a bound even when no override is given. A new `SigstoreKeyless.RequestTimeout time.Duration` field defaults to `sign.DefaultRequestTimeout` (60s) when zero or negative. The CLI only parses the variable and sets the field. This differs on purpose from the endpoint URLs, where the library applies no default (`cmd/podium/sign.go:225-227`): a missing URL fails closed, but a missing timeout would leave the request unbounded.
- **A context deadline per request.** The deadline is created with `context.WithTimeout` from each request's own context. It is not set as `http.Client.Timeout`, so the `Client` test seam (`pkg/sign/sigstore.go:65-68`) keeps working, and a cancelled parent context still cancels the request. Replacing the `http.DefaultClient` fallback with `&http.Client{Timeout: ...}` was rejected: an injected `Client` bypasses it, and a timeout that fires during the body read surfaces without the URL.
- **`podium verify` is unchanged.** `SigstoreKeyless.Verify` makes no network call, and the verify path does not read the new variable, because the parsing happens only when a signer is built (`keyForSign`).

## Spec amendment: §6.2 PODIUM_SIGSTORE_REQUEST_TIMEOUT

**SPEC-1.** `spec/06-mcp-server.md`, §6.2 Configuration table. Insert a new row directly after the row that begins:

> | `PODIUM_SIGSTORE_TSA_URL` | RFC 3161 endpoint from which `podium sign --provider sigstore-keyless` requests the timestamp token over the signature (§4.7.9).

and directly before the row that begins `| \`PODIUM_HOST_PYTHON\` |`. The new row:

> | `PODIUM_SIGSTORE_REQUEST_TIMEOUT` | Deadline, as a duration, on each request `podium sign --provider sigstore-keyless` makes to Fulcio, the timestamp authority, and Rekor (§4.7.9). Each request has its own deadline, which covers sending the request and reading the whole response. Surrounding whitespace is removed from the value, and an unset value or one that is empty after the removal takes the default. A value that does not parse as a positive duration makes `podium sign --provider sigstore-keyless` exit non-zero with `config.invalid`, naming the variable, before it contacts any Sigstore endpoint. No verifier reads it. Environment only; no flag or config-file key | `60s` |

The row states configuration only. §4.7.9 (SPEC-2) states the single attempt and the failure behavior.

## Spec amendment: §4.7.9 signing request deadline

**SPEC-2.** `spec/04-artifact-model.md`, §4.7.9 Signing. Insert a new paragraph after the paragraph that ends:

> The verifier makes no network call. An envelope that carries no log entry or no timestamp token is refused, and every refusal is `materialize.signature_invalid`.

and before the paragraph that begins "The signature the registry mints at ingest attests the artifact's `content_hash`". The new paragraph:

> `podium sign` sends each request to the certificate authority, the timestamp authority, and the transparency log once, under the per-request deadline `PODIUM_SIGSTORE_REQUEST_TIMEOUT` sets (§6.2). A request that fails, answers with an error status, or misses its deadline fails the signing: `podium sign` writes no envelope and exits non-zero. When the request cannot be sent, answers with an HTTP error status, or misses its deadline, the error names the endpoint URL.

Do not insert it inside the paragraph that begins "A keyless envelope `podium sign` produces carries the log entry and the timestamp token", which leads into the verifier's bullet list. The paragraph does not fix the call order, and it does not repeat the per-request and whole-response details that the SPEC-1 row owns.

## Proposed solution

### CODE-1. `pkg/sign`: the per-request deadline in `post`, with Fulcio routed through it

Files: `pkg/sign/sigstore.go` and `pkg/sign/fulcio.go`. `pkg/sign/rekor.go` and `pkg/sign/timestamp.go` are unchanged; only errors produced inside `post` (send failure, non-2xx status, missed deadline) gain the URL, per SPEC-2. The Fulcio decode error names the URL only because CODE-1 already rewrites that line.

In `pkg/sign/sigstore.go`:

1. Add the exported constant and its doc comment:

   ```go
   // DefaultRequestTimeout is the deadline on each Fulcio, timestamp-authority,
   // and Rekor request Sign makes when RequestTimeout is not positive.
   //
   // Spec: §4.7.9, §6.2.
   const DefaultRequestTimeout = 60 * time.Second
   ```

2. Add the field to `SigstoreKeyless`, after `Client`:

   ```go
   // RequestTimeout is the deadline on each request Sign makes. It covers
   // sending the request and reading the whole response. Zero or a negative
   // value means DefaultRequestTimeout, so no value leaves a request unbounded.
   // When the caller's own context is cancelled or reaches its own deadline
   // first, Sign returns that error unchanged, without the "(no response
   // within D)" suffix that marks a missed per-request deadline.
   RequestTimeout time.Duration
   ```

3. Add the unexported accessor `func (s SigstoreKeyless) requestTimeout() time.Duration`, which returns `s.RequestTimeout` when positive and `DefaultRequestTimeout` otherwise.

4. Rewrite `post`, keeping its signature `post(req *http.Request) ([]byte, error)`:

   ```go
   func (s SigstoreKeyless) post(req *http.Request) ([]byte, error) {
   	parent := req.Context()
   	timeout := s.requestTimeout()
   	ctx, cancel := context.WithTimeout(parent, timeout)
   	defer cancel()
   	req = req.WithContext(ctx)
   	body, err := s.roundTrip(req)
   	if err != nil && errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil {
   		return nil, fmt.Errorf("%w (no response within %s)", err, timeout)
   	}
   	return body, err
   }
   ```

   `roundTrip` holds the current body of `post` with two changes to the error text. A `Do` error is returned unchanged, because it is a `*url.Error` that names the URL. A body-read error becomes `fmt.Errorf("read response from %s: %w", req.URL, err)`. A non-2xx answer becomes `fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, req.URL, body)`. Add no retry loop.

   **IMPLEMENTOR'S CHOICE:** whether the round trip is a separate unexported helper or inlined in `post`. Constraint: the deadline must be created before `Do` and cancelled only after the body read returns, and the suffix must be appended exactly once and only under the predicate shown.

5. Update the doc comments: `Client` and `httpClient` state that the per-request context deadline bounds each call, including calls made through an injected `Client`; `maxResponseBytes` states that it bounds a Fulcio, Rekor, or timestamp-authority response body.

In `pkg/sign/fulcio.go`, replace lines 101-114 (from `resp, err := s.httpClient().Do(req)` through the `decode fulcio response` error) with:

```go
raw, err := s.post(req)
if err != nil {
	return nil, nil, err
}
var parsed fulcioCertResponse
if err := json.Unmarshal(raw, &parsed); err != nil {
	return nil, nil, fmt.Errorf("decode response from %s: %w", req.URL, err)
}
```

This removes the direct `Do` call, the streaming `Decode`, the unbounded `io.ReadAll`, and the duplicated `fulcio:` prefix. Remove the `io` import from `fulcio.go` if nothing else uses it.

The resulting messages, as `podium sign` prints them:

- headers never arrive: `sign failed: fulcio: Post "<url>": context deadline exceeded (no response within 300ms)`;
- body stalls: `sign failed: tsa: read response from <url>: context deadline exceeded (no response within 300ms)`;
- error status: `sign failed: rekor: HTTP 503 from <url>: <body>`.

### CODE-2. `cmd/podium/sign.go`: parse the variable for `podium sign`

1. Change `sigstoreKeylessProvider()` to `sigstoreKeylessProvider(use keyUse) (sign.SigstoreKeyless, error)`. For `keyForSign` it calls the new helper below and sets `RequestTimeout` from it. For `keyForVerify` it does not read the variable and leaves `RequestTimeout` zero. Extend its doc comment to name `PODIUM_SIGSTORE_REQUEST_TIMEOUT` and the fact that only the sign path reads it.

2. Add the helper:

   ```go
   // sigstoreRequestTimeout reads PODIUM_SIGSTORE_REQUEST_TIMEOUT. An unset
   // value, or one that is blank after trimming, takes the library default. A
   // value that is not a positive duration is refused with config.invalid,
   // because an interactive command reports an operator's typo rather than
   // silently signing under a different bound.
   //
   // Spec: §4.7.9, §6.2.
   func sigstoreRequestTimeout() (time.Duration, error)
   ```

   Behavior: `raw := os.Getenv("PODIUM_SIGSTORE_REQUEST_TIMEOUT")`; `v := strings.TrimSpace(raw)`; an empty `v` returns `sign.DefaultRequestTimeout`; otherwise `time.ParseDuration(v)`; a parse error or a result `<= 0` returns `fmt.Errorf("config.invalid: PODIUM_SIGSTORE_REQUEST_TIMEOUT %q is not a positive duration such as 60s", raw)`.

3. In `loadSignatureProvider`, `case "sigstore-keyless"` returns `sigstoreKeylessProvider(use)`. The existing error branch in `signCmd` (`cmd/podium/sign.go:55-58`) prints `error: %v` with exit 1, so no change to `signCmd` is needed. `signCmd` keeps passing `context.Background()`, because the deadline is per request inside `Sign`.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| An endpoint accepts the connection and never sends response headers | That request fails after D. `podium sign` prints `sign failed: <service>: Post "<url>": context deadline exceeded (no response within <D>)`, writes no envelope, and exits 1. Later requests are not sent | §4.7.9 "A request that fails, answers with an error status, or misses its deadline fails the signing" (SPEC-2); `docs/reference/cli.md` `podium sign` paragraph (DOC-1) |
| An endpoint sends headers and then stalls the body | The same deadline fires during the body read; the message reads `read response from <url>: ...` with the same suffix | §6.2 "covers sending the request and reading the whole response" (SPEC-1); `docs/reference/cli.md` environment row (DOC-1) |
| Each request is slow but under D | The signing succeeds. A whole `Sign` can take up to about 3×D (3 minutes at the default); accepted | §6.2 "Each request has its own deadline" (SPEC-1); `docs/reference/cli.md` environment row (DOC-1) |
| A request misses its deadline after the service acted on it | A Fulcio certificate (logged to a public CT log) or a Rekor entry can exist for a signing that wrote no envelope, and a rerun records new ones; accepted, because no request is retried | §4.7.9 "sends each request ... once" and "writes no envelope" (SPEC-2); `docs/reference/cli.md` `podium sign` paragraph (DOC-1, the third staged sentence) |
| A transient TSA failure | The signing fails and the operator reruns `podium sign`, which mints a new certificate; accepted pending OQ-1 | §4.7.9 single-attempt sentence (SPEC-2); `docs/reference/cli.md` (DOC-1) |
| `PODIUM_SIGSTORE_REQUEST_TIMEOUT` unset, empty, or whitespace | D = 60s | §6.2 row (SPEC-1); `docs/reference/cli.md` environment row (DOC-1) |
| Value `abc`, `0`, `0s`, `-1s`, or a bare `60` | `error: config.invalid: PODIUM_SIGSTORE_REQUEST_TIMEOUT ...`, exit 1, no Sigstore request. With the `<artifact>` form, the registry lookup still runs first | §6.2 "before it contacts any Sigstore endpoint" (SPEC-1); `docs/reference/cli.md` environment row (DOC-1) |
| `podium verify --provider sigstore-keyless` with an invalid value set | Unaffected; verification proceeds offline | §6.2 "No verifier reads it" (SPEC-1); `docs/reference/cli.md` environment row "`podium verify` does not read it" (DOC-1) |
| A library caller cancels its own context during a request | `errors.Is(err, context.Canceled)`, no `no response within` suffix; TEST-1 parent-cancel case | No spec text: a `pkg/sign` library contract, stated in the `RequestTimeout` doc comment (CODE-1 step 2) |
| A library caller's own context deadline fires during a request | `errors.Is(err, context.DeadlineExceeded)`, no `no response within` suffix; TEST-1 parent-deadline case | No spec text: a `pkg/sign` library contract, stated in the `RequestTimeout` doc comment (CODE-1 step 2) |
| A library caller sets `RequestTimeout` to zero or a negative value | Each request carries a `DefaultRequestTimeout` deadline; TEST-1 default-fallback case | No spec text: library default, stated in the `RequestTimeout` doc comment (CODE-1) |
| A Fulcio response larger than 4 MiB | The body is truncated at `maxResponseBytes` and the signing fails with `fulcio: decode response from <url>: ...`; a genuine response is a few kilobytes | No spec text: implementation bound, stated in the `maxResponseBytes` comment (CODE-1) |
| A hung registry during the `<artifact>` form of `podium sign` or `podium verify` | Unchanged: the registry call through `doJSON` (`cmd/podium/layer.go:659-680`) has no deadline and still blocks | Deferred to a separate finding (Non-goals); no spec or docs text changes here |

## Testing

**TEST-1 · unit, `pkg/sign/sigstore_deadline_test.go` (new), package `sign_test`.** Lands in S5. Each test func carries `// Spec: §4.7.9, §6.2` directly above it. Run with `go test -race ./pkg/sign/`.

- A local hanging handler, defined in the test file, serves any path. In `headers` mode it blocks without writing. In `body` mode it writes a 200 status and a partial body, flushes, and blocks. Both modes select on `r.Context().Done()` and on a channel the test closes in `t.Cleanup` before the server closes. No option is added to `internal/testharness/sigstoreharness`.
- Hang cases, a table of four subtests, each `t.Parallel()`, each with `RequestTimeout = 1s`, the other endpoints on a working `FakeServer` with `WithRequestCounter`, and `fakeOIDCToken` (`pkg/sign/sigstore_test.go:29`) as the token:
  1. Fulcio, `headers` mode.
  2. TSA, `headers` mode.
  3. Rekor, `headers` mode.
  4. Fulcio, `body` mode. This case pins that the shared `post` bounds the body read and replaces the old streaming decode.

  Each case asserts that `Sign` returns within 5s, that the error starts with its service prefix (`fulcio:`, `tsa:`, or `rekor:`), that it contains the hanging server's URL, that `errors.Is(err, context.DeadlineExceeded)` holds, and that it ends with `(no response within 1s)`. The Fulcio and TSA cases also assert from the counter that no later request was sent.
- Per-request case: `RequestTimeout = 1s`, and each of the three requests is delayed by 600ms before it reaches the `FakeServer`. The total of about 1.8s exceeds the deadline while each request stays under it. Assert that `Sign` succeeds.

  **IMPLEMENTOR'S CHOICE:** how the 600ms delay is inserted in front of the `FakeServer`. Constraint: every one of the three requests must wait at least 600ms before its response headers, and no option is added to `sigstoreharness`.
- Default-fallback case, a table of three subtests: `RequestTimeout = 0`, `RequestTimeout = -1`, and the control `RequestTimeout = 1s`. Each runs against a working `FakeServer` with `Client` set to an `*http.Client` whose `Transport` is a recording `http.RoundTripper` defined in the test file. The recorder notes `time.Now()` and `req.Context().Deadline()` for each request and then delegates to `srv.Client().Transport`, the transport the existing `signer` helper uses (`pkg/sign/sigstore_test.go:57-67`). Each subtest asserts that `Sign` succeeds, that the recorder saw the Fulcio, TSA, and Rekor requests, and that every one of them carried a deadline. For `0` and `-1`, each deadline lies between `sign.DefaultRequestTimeout - 5s` and `sign.DefaultRequestTimeout` after that request's start; for `1s`, it lies between `0` and `1s`. This pins that a non-positive value falls back to the default rather than leaving the request unbounded or failing at once, which a working server alone cannot distinguish. It uses the existing `Client` seam (`pkg/sign/sigstore.go:65-68`), so no `export_test.go` hook is added.
- Parent-cancel case: Fulcio points at a `headers`-mode hanging handler, `RequestTimeout = 1m`, and the parent context is cancelled 100ms after `Sign` starts. Assert that `errors.Is(err, context.Canceled)` holds and that the error does not contain `no response within`.
- Parent-deadline case: Fulcio points at a `headers`-mode hanging handler, `RequestTimeout = 1m`, and `Sign` receives a parent from `context.WithTimeout(context.Background(), 200ms)`. Assert that `Sign` returns within 5s, that `errors.Is(err, context.DeadlineExceeded)` holds, and that the error does not contain `no response within`. This is the only case in which the transport error wraps `context.DeadlineExceeded` while `parent.Err()` is non-nil, so it pins the `parent.Err() == nil` conjunct of the suffix predicate: an implementation without that conjunct would label the caller's own deadline as `(no response within 1m0s)`.
- Error-status case, a table of three subtests over the existing `sigstoreharness` options `WithFulcioFailure`, `WithTSAFailure`, and `WithRekorFailure` (`internal/testharness/sigstoreharness/server.go:30-36`), each of which makes that service answer 503 on a `FakeServer`. Each case asserts that the error contains `<service>: HTTP 503 from <srv.URL>`, where `<service>` is `fulcio`, `tsa`, or `rekor` and `srv` is the `*httptest.Server` that `FakeServer` returns (`internal/testharness/sigstoreharness/server.go:74`). The Fulcio case also asserts that the error does not contain `fulcio: fulcio:`.
- Fulcio size-bound case: a Fulcio handler answers 200 with a JSON body longer than 4 MiB. Assert that `Sign` fails with an error that starts with `fulcio:` and contains `decode response from`, and that Rekor and the TSA saw no request.

**TEST-2 · unit, `cmd/podium/sign_test.go`.** Lands in S6. Tag with `// Spec: §6.2` and `// Matrix: §6.10 (config.invalid)`.

- `TestSigstoreRequestTimeout`, a table over `t.Setenv` values: unset, `""`, and `"  "` give `sign.DefaultRequestTimeout`; `"5s"` and `" 5s "` give 5s; `"abc"`, `"0"`, `"0s"`, `"-1s"`, and `"60"` give an error containing `config.invalid` and `PODIUM_SIGSTORE_REQUEST_TIMEOUT`.
- `TestLoadSignatureProvider_SigstoreVerifyIgnoresRequestTimeout`: with the variable set to `"abc"`, `loadSignatureProvider("sigstore-keyless", keyForVerify)` returns no error, and `loadSignatureProvider("sigstore-keyless", keyForSign)` returns the `config.invalid` error. With the variable set to `"5s"`, the `keyForSign` provider carries `RequestTimeout == 5s`.
- In the existing endpoint-default test (`cmd/podium/cli_helpers_test.go:212`), set `PODIUM_SIGSTORE_REQUEST_TIMEOUT` to `""` so a developer shell cannot break it.

**TEST-3 · e2e, `test/e2e/sigstore_keyless_sign_deadline_test.go` (new).** Lands in S7. Reuses `newKeylessFixture` and `runPodium` from `test/e2e/sigstore_keyless_verify_test.go`. It pins the wiring from the environment variable to `SigstoreKeyless.RequestTimeout` and the exit code and stderr of the binary; TEST-2 covers the parsing branches in-process.

- Add `"PODIUM_SIGSTORE_REQUEST_TIMEOUT": ""` to the defaults map in `keylessFixture.env` (`test/e2e/sigstore_keyless_verify_test.go:51-62`), so every keyless end-to-end test is insulated from the developer's shell.
- `TestPodiumSign_SigstoreKeylessHangingEndpointTimesOut` (`// Spec: §4.7.9, §6.2`), a single case. Fulcio points at a local handler that blocks on `r.Context().Done()`, with `PODIUM_SIGSTORE_REQUEST_TIMEOUT=300ms` and an unsigned JWT for `alice@acme.com` in `PODIUM_SIGSTORE_OIDC_TOKEN`. Before starting the clock, call `cmdharness.Bin(t, "podium")` so the lazy, `sync.Once`-guarded compile (`internal/testharness/cmdharness/cmdharness.go:102-103`), which under `GOCOVERDIR` adds `-cover` instrumentation (`internal/testharness/cmdharness/cmdharness.go:136`), finishes outside the timed window; `runPodium` then reuses the built binary (`test/e2e/helpers_test.go:131-134`). Time only the signing `runPodium` call. Assert exit 1, elapsed time of that call under 15s (`runPodium` has a 90s hard deadline), stderr containing `sign failed: fulcio:`, the hanging URL, and `no response within 300ms`, empty stdout, and `f.requests.Load() == 0`, so no TSA or Rekor request was sent.
- `TestPodiumSign_SigstoreKeylessInvalidRequestTimeoutRefused` (`// Spec: §6.2`, `// Matrix: §6.10 (config.invalid)`): `PODIUM_SIGSTORE_REQUEST_TIMEOUT=abc` and a non-empty token. Assert exit 1, stderr containing `config.invalid` and `PODIUM_SIGSTORE_REQUEST_TIMEOUT`, and zero requests.
- Add the override `PODIUM_SIGSTORE_REQUEST_TIMEOUT=abc` to the `matching identity and issuer` case of `TestPodiumVerify_SigstoreKeyless` (`test/e2e/sigstore_keyless_verify_test.go:93`). The case still expects `verify ok`, which pins that the verify path does not read the variable.
- Grep the run output for `SKIP`, and measure the subprocess coverage of CODE-2 with `GOCOVERDIR` per `.claude/rules/test-coverage.md`.

## Manual validation

No scenario is staged. The observable change is the exit code and stderr of `podium sign` against a hanging endpoint and against an invalid value, and TEST-3 drives the compiled binary against a loopback listener and asserts both. The staging-bound scenario S85 covers what only a running Sigstore instance establishes, and a hanging local listener uses none of it. The Non-goals record the dropped scenario and its reasons.

## Documentation changes

**DOC-1.** `docs/reference/cli.md`. Lands in S8. No `tools/doccov/manifest.yaml` change is needed, because the page is already listed and the change adds no fenced block.

(a) Environment table: insert after the `PODIUM_SIGSTORE_TSA_URL` row (currently line 884):

> | `PODIUM_SIGSTORE_REQUEST_TIMEOUT` | Read by `podium sign --provider sigstore-keyless`, environment only. Deadline on each request it makes to Fulcio, the timestamp authority, and Rekor, as a Go duration with a unit such as `60s`. Each request has its own deadline, which covers sending the request and reading the whole response. Unset or blank, it takes `60s`. A value that does not parse as a positive duration, including a bare number such as `60`, makes `podium sign` exit non-zero with `error: config.invalid` naming the variable before it contacts any Sigstore endpoint. `podium verify` does not read it. |

(b) The `podium sign` paragraph (currently line 769): after the sentence "The three endpoints default to the Sigstore public-good instances.", add:

> `podium sign` sends each request once and waits for each no longer than `PODIUM_SIGSTORE_REQUEST_TIMEOUT` (60 seconds by default). A request that does not complete within that time fails the command with `sign failed:` followed by the service (`fulcio:`, `tsa:`, or `rekor:`), the endpoint URL, and `no response within <deadline>`. A request that misses its deadline can still have reached the service, so a certificate or a log entry can exist for a signing that printed no envelope, and running `podium sign` again records new ones.

**CL-1.** `CHANGELOG.md`, `[Unreleased]`, under `### Fixed`. Lands in S9.

> - **`podium sign --provider sigstore-keyless` no longer hangs on an unresponsive Sigstore endpoint** (§4.7.9, §6.2): each Fulcio, timestamp-authority, and Rekor request has its own deadline, 60 seconds by default and set by `PODIUM_SIGSTORE_REQUEST_TIMEOUT`. A request that misses it fails the command, and the message names the endpoint. An invalid value is refused with `config.invalid`. No request is retried.

## Open questions

**OQ-1. A TSA-only retry.** Should the TSA request alone get one bounded, jittered retry? It has no side effects, and retrying it would avoid a second Fulcio mint (and a second CT log entry) when the operator reruns `podium sign` after a transient TSA failure. The draft retries nothing, which keeps the surface minimal and avoids a non-spec attempt count that would need its own override.

**OQ-2. The default.** Is 60s the right default? The evidence is upstream (sigstore-go's example per-request timeouts of 30s for Fulcio and the TSA and 90s for Rekor, and cosign's 3m whole-operation default) and could not be verified from this repository. A reviewer who wants Rekor v2's synchronous integration covered with the same margin as upstream can pick 90s, which makes the sequential worst case 4.5m.

## Non-goals

- Running the TSA and Rekor requests concurrently after Fulcio. Neither consumes the other's result, so this would cut the worst case from about 3×D to about 2×D, but it adds concurrency to a path that is currently a straight line.
- Adding a retry with jittered backoff to any of the three requests. See the Decisions and OQ-1.
- Adding a new §6.10 error code for an unavailable or timed-out Sigstore endpoint, mapping timeouts to `config.signature_provider_unavailable`, or changing `spi.Error.Error()` so it prints its code.
- A flag or `registry.yaml` key for the deadline. Every `PODIUM_SIGSTORE_*` setting is environment-only.
- Separate deadlines per endpoint (three variables).
- Any change to `podium verify`, `SigstoreKeyless.Verify`, `podium-mcp`, server-source `podium sync`, or the SDKs, none of which make Sigstore calls.
- Bounding the registry calls the `<artifact>` forms of `podium sign` and `podium verify` make through `doJSON` (`cmd/podium/layer.go:659-680`, which uses `http.DefaultClient`). That is a separate surface and a separate finding.
- Adding a hang or delay option to `internal/testharness/sigstoreharness`. The tests use a local hanging handler next to `FakeServer`.
- Extending manual scenario S85 with a hanging-endpoint step (dropped MV-1). TEST-3 already runs the compiled `podium` binary against a hanging `httptest` handler, which binds a loopback TCP listener, and asserts the exit code, the `sign failed: fulcio:` prefix, the hanging URL, and the `no response within` suffix, so the manual step adds no observable. S85 exists for what only the live staging instance establishes (`test/manual-validation.md:10496-10508`), and a hanging local listener uses no staging service. The sketched step also left a backgrounded listener on a fixed port with no cleanup, and S85's staging-token prerequisite (`test/manual-validation.md:10512-10514`) would skip this offline check whenever no token is available. `.claude/rules/test-coverage.md` places CLI behavior in `test/e2e`, where TEST-3 lives. If a hand check is wanted later, it belongs as a short offline step in a scenario not tied to staging.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-04, automated)

- No test pinned that an error-status failure names the endpoint URL. TEST-1 gains an error-status case over `WithFulcioFailure`, `WithTSAFailure`, and `WithRekorFailure` that asserts `<service>: HTTP 503 from <srv.URL>` and, for Fulcio, the absence of `fulcio: fulcio:`. The S5 checklist line names the error-status endpoints.
- The e2e elapsed-time bound in `TestPodiumSign_SigstoreKeylessHangingEndpointTimesOut` included the lazy, possibly coverage-instrumented build of the `podium` binary. TEST-3 now calls `cmdharness.Bin(t, "podium")` before starting the clock and times only the signing `runPodium` call.

### Pass 2 (2026-10-04, automated)

- SPEC-2 promised that every failed request names the endpoint, but the TSA PKIStatus rejection and the Rekor and TSA decode and missing-member errors arise after `post` returns a 2xx body and do not name the URL (`pkg/sign/timestamp.go:97-103`, `pkg/sign/rekor.go:102-122`). SPEC-2 now states that the error names the endpoint URL when the request cannot be sent, answers with an HTTP error status, or misses its deadline, which matches the errors CODE-1 rewrites in `post`. The Summary SPEC-2 line, the CODE-1 file list, the `post` Watch-out, and the TEST-1 error-status case state the same predicate. `pkg/sign/rekor.go` and `pkg/sign/timestamp.go` stay unchanged, so no new error text or test is added.

### Pass 3 (2026-10-04, automated)

- No test exercised the `parent.Err() == nil` conjunct of the deadline-suffix predicate, because the parent-cancel case already fails the `errors.Is(err, context.DeadlineExceeded)` conjunct. TEST-1 gains a parent-deadline case that calls `Sign` with a 200ms parent deadline against a hanging Fulcio under `RequestTimeout = 1m` and asserts `context.DeadlineExceeded` without the `no response within` suffix. The Edge-case table gains the matching row, the parent-cancel Watch-out names both cases, and the S5 checklist line names them.
- The default-fallback case passed whether a non-positive `RequestTimeout` took the default or left the request unbounded, since it ran only against a working server. TEST-1 now covers `0`, `-1`, and a `1s` control through a recording `RoundTripper` injected via the existing `Client` seam (`pkg/sign/sigstore.go:65-68`) and asserts that each of the Fulcio, TSA, and Rekor requests carried a deadline near `sign.DefaultRequestTimeout` (or near `1s` for the control). The Edge-case row for a zero or negative value names the case.
- The parent-cancel and parent-deadline Edge-case rows cited a contract in the `RequestTimeout` and `post` doc comments, but CODE-1 staged no such text and gives `post` no doc comment. The staged `RequestTimeout` doc comment in CODE-1 step 2 now states that a caller's own cancellation or deadline is returned unchanged, without the `(no response within D)` suffix, and both rows cite that comment.

### Pass 4 (2026-10-04, automated prune)

- The URL rule and the suffix predicate were restated in several sections, and the Summary SPEC-2 line had drifted into a contradiction with CODE-1 by saying that a decode failure keeps its existing message, while CODE-1 rewrites the Fulcio decode error to name the URL. Each rule now has one owner: SPEC-2 owns which failures name the endpoint URL, and CODE-1 step 4 owns the suffix predicate. The Summary SPEC-2 line points to SPEC-2. The `post` Watch-out and the CODE-1 file preamble drop the list of untouched decode, PKIStatus, and missing-member error sites and keep a one-line pointer. The parent-cancel Watch-out drops the restated predicate and names the TEST-1 cases. The TEST-1 error-status case keeps only its assertions, and the three library-contract Edge-case rows keep only the observable result and the TEST-1 case. This supersedes the Pass 2 note that those sections each state the predicate. No IMPLEMENTOR'S CHOICE marker was added, and the checklist, the files touched, and the tests are unchanged.
