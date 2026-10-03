# Proposal 0043: Bind Sigstore-keyless verification to a configured signer identity, to a transparency-log inclusion proof, and to a timestamp authority's attested time

- Issue: (to be filed)
- Status: Applied to spec (2026-10-03). Approval was decided on the user's behalf under the overnight authorization. Redesigned for Rekor v2 (inclusion proof to a signed checkpoint, RFC 3161 TSA time, trusted_root.json; podium-mcp drops sigstore-keyless), then re-reviewed to convergence (5 rounds). The recorded gap (Sign's outbound calls have no deadline) is a separate follow-up fix.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §4.7.9: the Sigstore-keyless bullet stops defining the missing identity and log-content checks as specified behavior. It states the acceptance conditions: an RFC 3161 timestamp over the signature from a trusted timestamp authority, whose time is the attested time; an RFC 6962 inclusion proof from the log entry to a checkpoint signed by a trusted transparency-log key; a hashedrekord v0.0.2 entry bound to the digest, signature, and leaf certificate; a certificate chain that carries the code-signing usage and is valid at the attested time; and a leaf whose subject alternative name and OIDC issuer match the configured policy (SPEC-1).
- §6.2: `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` is removed and replaced by `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, a path to Sigstore's `trusted_root.json`, whose certificate authorities, transparency-log keys, and timestamp authorities are honored only within their validity windows. `PODIUM_SIGSTORE_CERT_IDENTITY` and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` configure the identity policy `podium verify` enforces. `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and `PODIUM_SIGSTORE_TSA_URL` configure `podium sign` and default to the Sigstore public-good instances (SPEC-2).
- `pkg/sign`: a new `IdentityPolicy`, a trusted-root parser, an envelope that replaces `log_index` with a `tlog` object (`log_index`, `body`, `hashes`, `checkpoint`) and adds `timestamp`, a `Sign` that obtains a timestamp from a timestamp authority and an inclusion proof from Rekor v2, and an offline `Verify` with timestamp verification, inclusion-proof and checkpoint verification, entry binding, a chain check at the attested time, and the identity match. `fetchRekor` is deleted, and `pkg/audit/anchor.go` reads `tlog.log_index` (CODE-1, CODE-2, CODE-3).
- `cmd/podium` passes the trusted root, the signing endpoints with their §6.2 defaults, and the identity policy into `sign.SigstoreKeyless` (CODE-4). `podium-mcp` no longer accepts `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless`: its start refuses with `config.invalid`, because every delivery signature it verifies is registry-managed (§4.7.10), and it reads no `PODIUM_SIGSTORE_*` variable (SPEC-3, CODE-5).
- Tests: a stdlib-only envelope generator under `internal/testharness/sigstoreharness`, unit tests in `pkg/sign` and `pkg/audit`, bridge start tests at the unit and end-to-end levels, an end-to-end `podium verify` test, an updated live smoke that records a staging fixture, and an offline test over that fixture (TEST-1 to TEST-5).
- Docs, changelog, and manual validation follow (DOC-1, CL-1, MV-1).

**Fixed decisions.**

- Verification fails closed. A keyless verification with no certificate identity or no OIDC issuer configured accepts no envelope.
- The proposal adds no dependency. The verifier stays on the standard library (`crypto/x509`, `crypto/ecdsa`, `crypto/ed25519`, `crypto/sha256`, `crypto/sha512`, `encoding/asn1`, `encoding/base64`, `encoding/json`, and `encoding/pem`). The RFC 3161 token is parsed with `encoding/asn1` into the CMS structures it uses, and its signature is checked with `x509.Certificate.CheckSignature`, so no CMS or timestamp library is needed. sigstore-go, `digitorus/timestamp`, and `golang.org/x/crypto/cryptobyte` are not adopted.
- Identity variables: `PODIUM_SIGSTORE_CERT_IDENTITY` is a comma-separated list of exact email or URI subject alternative names, with surrounding whitespace removed from each entry and empty entries dropped. `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` is one exact issuer URL, with surrounding whitespace removed, and a value empty after the removal counts as unset. Both are compared byte for byte. No regular-expression variable exists.
- `pkg/sign` type and field names: `type IdentityPolicy struct { Identities []string; Issuer string }`, `func NewIdentityPolicy(identities, issuer string) IdentityPolicy`, `func (IdentityPolicy) Validate() error`, and the `SigstoreKeyless` field `Identity IdentityPolicy`. The name `Policy` is not used, because `pkg/sign` already uses it for `VerificationPolicy`.
- Matching reads email and URI subject alternative names only. A leaf whose only identity is an otherName SAN is refused. Fulcio marks that SAN extension critical, and Go's x509 parser records a critical SAN that holds no email, DNS, IP, or URI name as an unhandled critical extension, so the CODE-3 step 9 chain check refuses such a leaf with `cert chain` before the identity match runs. A leaf whose otherName-only SAN extension is non-critical reaches the step 11 identity match and is refused with `certificate identity mismatch`. `verifyLeafChain` does not tolerate the unhandled critical extension, because that would weaken the chain check.
- The issuer comes from Fulcio extension `1.3.6.1.4.1.57264.1.8` (a DER UTF8String). The deprecated `1.3.6.1.4.1.57264.1.1` (raw bytes) is read only when `.1.8` is absent. A present `.1.8` that fails to parse is refused and never falls back to `.1.1`.
- The log proof is an RFC 6962 inclusion proof to a signed checkpoint, verified offline. No SET is read, and no witness cosignature or consistency proof is checked. Key-ID hints in a checkpoint signature line are not interpreted: each trusted log key whose window contains the attested time is tried.
- The chain check runs at the RFC 3161 `genTime`, and only after the timestamp token verifies. Wall-clock time no longer decides validity. The `Now` field and the `now()` helper are deleted. The timestamp request carries no nonce, because nothing at verification time could check one.
- Entry binding: the log entry body decodes as hashedrekord with `kind` `hashedrekord` and `apiVersion` `0.0.2`. Its `spec.hashedRekordV002.data.algorithm` is `SHA2_256`, its `data.digest` equals the content-hash bytes, its `signature.content` equals the envelope signature bytes, and its `signature.verifier.x509Certificate.rawBytes` equals the leaf's `Raw`. The content hash must be `sha256:`.
- Envelope format: `{"cert", "signature", "tlog": {"log_index", "body", "hashes", "checkpoint"}, "timestamp"}`. `body` is the base64 canonicalized entry, `hashes` the base64 audit path from leaf to root, `checkpoint` the signed-note text, and `timestamp` the base64 DER RFC 3161 `TimeStampToken`. An envelope without `tlog` or `timestamp`, including every envelope minted before this change, is refused. No compatibility shim is added.
- `Verify` makes no network call. `RekorURL`, `TSAURL`, and `Client` are read only by `Sign`.
- `Sign` returns `ErrSigstoreUnavailable` when `FulcioURL`, `RekorURL`, `TSAURL`, or `OIDCToken` is empty, before any network call. `cmd/podium` fills the three URLs from `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and `PODIUM_SIGSTORE_TSA_URL`, and an unset or empty variable takes its §6.2 default: `https://fulcio.sigstore.dev`, `https://log2025-1.rekor.sigstore.dev`, and `https://timestamp.sigstore.dev/api/v1/timestamp`. The library applies no default, so a test that leaves a URL empty never reaches a public instance. From the CLI, only an unset `PODIUM_SIGSTORE_OIDC_TOKEN` produces the refusal. `PODIUM_SIGSTORE_OIDC_TOKEN` gets no §6.2 row, because a credential has no default.
- The trust input is Sigstore's `trusted_root.json` at `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, decoded with `encoding/json`. `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` is removed with no alias and is never read. `SigstoreKeyless.TrustRoot` keeps its name and holds the JSON bytes. `certificateAuthorities[].certChain.certificates` are the Fulcio chains, with the last certificate the anchor and the others intermediates. `tlogs[].publicKey` are the log keys. `timestampAuthorities[].certChain.certificates` are the timestamp-authority chains, with the last certificate the anchor. A `tlogs` key whose `keyDetails` is neither `PKIX_ECDSA_P256_SHA_256` nor `PKIX_ED25519`, or whose bytes do not parse as that type, is skipped. An authority or key is used only when its `validFor` window contains the attested time. A window includes both ends, an absent `start` or `end` leaves that side unbounded, and a key listed more than once is usable when any of its listings' windows contains the attested time. `ctlogs`, `mediaType`, and `tlogs[].logId` are not read. Podium neither fetches the file nor verifies TUF metadata, and the operator supplies an authentic copy. One file holds the three roles separately, so a timestamp-authority root never anchors a code-signing leaf and a certificate-authority root never anchors a timestamp signer. The CODE-3 step 9 pool comes from `certificateAuthorities` alone and the step 5 pool from `timestampAuthorities` alone, and TEST-2 case 3 pins both with `WithLeafIssuedByTSARoot` and `WithTimestampSignedByFulcioCA`.
- The proposal adds no §6.10 code and no policy sentinel. Every `Verify` failure, including a missing or incomplete identity policy and an unusable trust root, wraps `sign.ErrSignatureInvalid`, and `Validate` returns a plain error naming the variable.
- `pkg/audit` `extractRekorLogIndex` reads `tlog.log_index` through a nested pointer field and returns `-1` when it is absent. `Anchor`'s signature and the `audit.anchored` context keys are unchanged.
- `podium-mcp` accepts `registry-managed` and `noop` only. Every other `PODIUM_SIGNATURE_PROVIDER` value, `sigstore-keyless` included, refuses the start with `config.invalid` under every verification policy, before any material is read, with a message naming the value and `registry-managed`. `buildSignatureProvider` is deleted, and podium-mcp reads no `PODIUM_SIGSTORE_*` variable. `enforceSignaturePolicy` is unchanged. `podium sign` and `podium verify` keep `sigstore-keyless`.
- Test envelopes for every refusal are generated in-process on every run. One recorded fixture set under `pkg/sign/testdata/sigstore-staging/` (`envelope.json`, `trusted_root.json`, `meta.json`) is captured from the Sigstore staging instance by the live smoke when the test-only variable `PODIUM_SIGSTORE_RECORD_DIR` is set, and is verified offline on every run. The fixture pins the wire formats the generator cannot check independently: the RFC 3161 token the staging timestamp authority issues, the protojson encoding and checkpoint syntax Rekor v2 returns, and the hashedrekord v0.0.2 body field names. The recording uses a non-personal OIDC identity, because the committed leaf certificate carries its subject alternative name.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **The `pkg/sign` and `pkg/audit` tests break between CODE-1, CODE-2, CODE-3, and TEST-2.** Deleting `Now` stops `pkg/sign/sigstore_test.go` compiling, deleting `fetchRekor` stops `pkg/sign/rekor_helpers_test.go` compiling, and the `tlog.log_index` read in `pkg/audit/anchor.go` fails the top-level `log_index` assertions of the keyless fakes in `pkg/audit/anchor_test.go`. Step S4 lands them together.
- **`pkg/audit/anchor.go` reads the field this proposal moves.** `extractRekorLogIndex` decodes a top-level `log_index` from any `Sign` envelope and records it in the `audit.anchored` event. After the format change it returns `-1` silently unless CODE-2 updates it, and the existing anchor tests keep passing on hand-built envelopes that no longer match the format. Production anchoring signs with the Ed25519 anchor key (§8.6), so the code path matters only for a keyless anchor signer.
- **The test harness must not import `pkg/sign`.** `pkg/sign/rekor_helpers_test.go`, `pkg/sign/identity_policy_test.go`, and the new `pkg/sign/inclusion_test.go`, `pkg/sign/timestamp_test.go`, and `pkg/sign/trusted_root_test.go` are in package `sign`. A harness that imports `pkg/sign` would create an import cycle for those tests, so `internal/testharness/sigstoreharness` builds envelope JSON from its own types. That independence also catches format drift between `Sign` and the generator.
- **`PODIUM_SIGNATURE_PROVIDER` is shared by three binaries.** `podium sign` and `podium verify` read it as the `--provider` default (`cmd/podium/sign.go:27`, `:86`) and keep accepting `sigstore-keyless`. A shell that exports it for `podium verify` makes a podium-mcp started from the same shell refuse with `config.invalid`. The Claude Code, Claude Desktop, Cursor, OpenCode, Hermes, and Generic recipes in `docs/consuming/configure-your-harness.md` set `PODIUM_SIGNATURE_PROVIDER=registry-managed` in the MCP entry (`:163`, `:213`, `:258`, `:309`, `:459`, `:500` at the time of writing), so a bridge launched from them is unaffected. The Standalone recipe omits the variable (`docs/consuming/configure-your-harness.md:519` at the time of writing), and the Codex and Gemini sections give no example (`:349`, `:384`), so a bridge launched from such an entry inherits an exported `sigstore-keyless` and refuses to start. DOC-1(d) adds a sentence to the Standalone section telling a consumer whose shell exports another value to set `PODIUM_SIGNATURE_PROVIDER=registry-managed` in the MCP entry.
- **Deleting `buildSignatureProvider` breaks the `cmd/podium-mcp` tests.** `cmd/podium-mcp/main_helpers_test.go` calls it (`:487`, `:491`, `:501`, `:509`, `:536`, `:552` at the time of writing), and the "sigstore trust root readable" row of `TestLoadConfig_VerifierResolution` (`cmd/podium-mcp/config_env_test.go:846-849`) expects a `sigstore-keyless` verifier that the rewritten `resolveVerifier` refuses. Step S6 therefore lands CODE-5 with the `cmd/podium-mcp` unit dispositions of TEST-3.
- **`test/e2e` inherits the developer environment.** `mergeEnv` scrubs only `PODIUM_IDENTITY*`, `PODIUM_SESSION_TOKEN`, and `PODIUM_BIND`. A shell carrying the live-smoke `PODIUM_SIGSTORE_*` variables leaks into the subprocess, so TEST-4 overrides every `PODIUM_SIGSTORE_*` variable explicitly, setting unwanted ones to the empty string.
- **The test harness clock must drive `genTime`.** The current fake Rekor returns `time.Now().Unix()` (`pkg/sign/sigstore_test.go`). With the chain check at the timestamp time, the fake timestamp authority must stamp `genTime` from the harness clock (2026-01-15T12:00:00Z), or every round-trip falls outside the leaf window (11:55 to 12:15 UTC).
- **Rekor v2 wire details are pinned by the recorded fixture.** protojson writes `int64` fields (`logIndex`) as quoted strings and omits zero values, so `uploadRekor` decodes `logIndex` as a string, parses it with `strconv.ParseInt`, and treats an absent value as `0`. The v0.0.2 body field names come from the protobuf-specs `rekor/v2` hashedrekord message. `TestSigstoreKeyless_SignRecordsAbsentLogIndexAsZero` (TEST-2 case 6a) pins the absent-value rule, because the recorded fixture does not sit at index 0. `TestSigstoreKeyless_VerifiesRecordedStagingEnvelope` fails if the string encoding or the field names are wrong, so S9 runs on S4's branch and the two merge together. A Rekor v1 URL fails `podium sign` with `rekor: HTTP 404`. The default Rekor v2 shard URL rotates, so the implementer confirms `https://log2025-1.rekor.sigstore.dev` against Sigstore's public-good `signing_config` when landing SPEC-2.
- **`x509.VerifyOptions.KeyUsages` does not enforce the usage conditions alone.** Go skips the extended-key-usage check for a certificate with no extended-key-usage extension and passes a certificate that lists `x509.ExtKeyUsageAny` (`$GOROOT/src/crypto/x509/verify.go:995-1003` in Go 1.26). CODE-3 steps 5 and 9 therefore check the timestamp signer's and the leaf's `ExtKeyUsage` explicitly after the chain verifies, and TEST-2 case 3 pins both with EKU-less and any-purpose certificates.
- **The RFC 3161 signature covers the re-tagged signed attributes.** CMS signs the DER of `signedAttrs` with its `[0] IMPLICIT` tag (`0xA0`) replaced by the SET tag (`0x31`). Hashing the bytes as they appear in the token fails every genuine timestamp.
- **`test/e2e` signing cases must set every endpoint.** `envDefault` maps an empty `PODIUM_SIGSTORE_*_URL` to the public-good default, so a `podium sign` case that leaves a URL empty contacts the public instance. TEST-4 points all three URLs at the counting server in every signing case.
- **Sigstore's own `trusted_root.json` lists entries Podium does not use.** The public-good and staging files list CT logs and may list log keys of types other than P-256 and Ed25519. The parser must not refuse a file over an entry it does not use: it ignores `ctlogs` and skips an unsupported log key. TEST-2 case 5b pins this.
- **`rawBytes` is standard base64 with padding.** Declare the fields as `[]byte` so `encoding/json` decodes them, and declare `validFor.start` and `validFor.end` as `*time.Time` so an absent bound stays nil. `time.Time` parses the fractional-second RFC 3339 form protojson emits.
- **Proposals 0041 and 0034 edit or anchor on the §6.2 row this proposal renames.** Proposal 0041 (b) edits the row's MCP-server sentence, which SPEC-2(a) removes, and proposal 0034 inserts a row after it. Whichever proposal lands second rebases its anchor onto `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`.
- **`ErrSigstoreUnavailable` prints no code.** `spi.Error.Error()` returns the message only, so `podium sign` prints `sign failed: sign: sigstore-keyless not configured: ...`. TEST-4 asserts that text and does not assert the code string. Only the `registry-managed` arms of `loadSignatureProvider` prefix `config.signature_provider_unavailable:` (`cmd/podium/sign.go:247`, `:257`), so DOC-1(a) and DOC-1(e) scope the existing "exits non-zero naming `config.signature_provider_unavailable`" sentences to `registry-managed` and document the keyless refusal by its message.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §4.7.9 replaces the Sigstore-keyless bullet with the acceptance conditions, the fail-closed identity rule, attested-time validity, and the offline check.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2, SPEC-3. §6.2 replaces the trust-root row with `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` and adds the `PODIUM_SIGSTORE_CERT_IDENTITY`, `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and `PODIUM_SIGSTORE_TSA_URL` rows; §6.2 restricts the MCP server's `PODIUM_SIGNATURE_PROVIDER` values, §6.9 drops the `sigstore-keyless` clause, and §9.1 names `podium verify` as the keyless verifier.
      Levels: —. Depends on: S1
- [ ] **S3 · test** — TEST-1. `internal/testharness/sigstoreharness` generates the trusted root, leaves, keyless envelopes, tamper variants, and a fake Fulcio, Rekor v2, and timestamp-authority server in-process.
      Levels: unit. Depends on: S1, S2
      Interleave: this test step precedes the code steps because the rewritten tests in S4 and the tests in S8 build their envelopes with the harness.
- [ ] **S4 · code** — CODE-1, CODE-2, CODE-3, TEST-2. `IdentityPolicy`, the `tlog` envelope, the trusted-root parser, `Sign` requiring Rekor v2 and a timestamp authority, the offline `Verify`, the `pkg/audit` anchor read, and the rewritten `pkg/sign` and `pkg/audit` tests. Bundled because the `pkg/sign` tests do not compile, and the `pkg/audit` anchor tests fail, between the four deliverables.
      Levels: unit. Depends on: S1, S2, S3
- [ ] **S5 · code** — CODE-4. `podium sign` and `podium verify` read the trusted root from `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, the signing endpoints with their §6.2 defaults, and `PODIUM_SIGSTORE_CERT_IDENTITY` and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` into `sign.SigstoreKeyless`.
      Levels: unit, e2e. Depends on: S4
- [ ] **S6 · code** — CODE-5 and the `cmd/podium-mcp` unit part of TEST-3. `resolveVerifier` refuses `sigstore-keyless` with `config.invalid`; `buildSignatureProvider` is deleted; the `TestLoadConfig_VerifierResolution` rows are replaced, the registry-managed resolution test is rewritten, and the bridge and builder tests are deleted. Bundled because `cmd/podium-mcp/main_helpers_test.go` does not compile, and the "sigstore trust root readable" row fails, between CODE-5 and those test edits.
      Levels: unit. Depends on: S2
- [ ] **S7 · test** — The `test/e2e` part of TEST-3. `TestMCPStart_SigstoreKeylessProviderRefused` drives the start refusal through the binary.
      Levels: e2e. Depends on: S6
- [ ] **S8 · test** — TEST-4. End-to-end `podium verify --provider sigstore-keyless` and `podium sign --provider sigstore-keyless` through the binary, and the `TestLoadSignatureProvider` table for the §6.2 endpoint defaults.
      Levels: unit, e2e. Depends on: S3, S5
- [ ] **S9 · test** — TEST-5. The live smoke reads the identity policy, the TSA URL, and the trusted root, records the staging fixture under `PODIUM_SIGSTORE_RECORD_DIR`, and commits `pkg/sign/testdata/sigstore-staging/` with `TestSigstoreKeyless_VerifiesRecordedStagingEnvelope`. `RELEASING.md` documents the new inputs.
      Levels: unit (the live smoke is skipped by default; the recorded-fixture test runs by default). Depends on: S4. S9 runs on S4's branch, and the two merge together once the recorded fixture verifies.
- [ ] **S10 · docs** — DOC-1. CLI reference, harness configuration, error-code reference, operator guide, extension guide, `OPERATIONS.md`, and the env examples.
      Levels: —. Depends on: S5, S6, S9
- [ ] **S11 · docs** — CL-1. The `[Unreleased]` `Fixed`, `Changed`, and `Removed` entries, and the existing `Changed` delivery-attestation entry that CL-1 amends.
      Levels: —. Depends on: S5, S6
- [ ] **S12 · docs** — MV-1. Manual-validation scenario S83 for `podium sign` and `podium verify` against the Sigstore staging instance, and for the `podium-mcp` start refusal under `sigstore-keyless`.
      Levels: manual. Depends on: S5, S6

**Ordering constraints.** S1 and S2 land the contract every later step cites. S3 precedes every step that generates an envelope. S6 depends only on S2 because the start refusal reads no `pkg/sign` change, so S6 and S7 may proceed in parallel with S3, S4, and S5. S4 and S9 merge together, because the recorded fixture S9 captures is the only check of the wire formats S4 parses.

## Current state and the gap

`SigstoreKeyless.Verify` (`pkg/sign/sigstore.go`) parses the JSON envelope `{cert, signature, log_index}`. It walks the leaf certificate to the PEM trust root with `x509.Certificate.Verify` at `CurrentTime: s.now()`, which is wall-clock time because no production caller sets `Now` (`cmd/podium/sign.go` `loadSignatureProvider`, `cmd/podium-mcp/main.go` `buildSignatureProvider`). It then checks the ECDSA signature over the content-hash bytes. It never reads the leaf's subject alternative name or the Fulcio OIDC-issuer extension, and the struct has no identity field. Any certificate the trust root issued, for any OIDC identity, therefore verifies (finding C26).

The transparency-log check is a presence probe. When `RekorURL` is set and the envelope's own `log_index` is at least 0, `fetchRekor` (`pkg/sign/rekor.go`) issues a GET by log index and returns nil on any 2xx without reading the body. It verifies no SET and no inclusion proof, reads no integrated time, and does not compare the entry to the digest, the signature, or the certificate. `log_index` sits outside the signed bytes, so an envelope author sets it to `-1` and skips the check. `Sign` records only the log index: `uploadRekor` decodes `logID` and `integratedTime`, discards them, and never parses `body` or `verification.signedEntryTimestamp`. Because the chain check runs at wall-clock time, a genuine envelope stops verifying once its short-lived Fulcio certificate expires, and nothing in the envelope lets the verifier check the certificate at the log's attested time (finding C27). Only the test-only `Now` field hides that today.

§4.7.9 states the gap as defined behavior: the Sigstore-keyless bullet says the consumer-side verifier "does not check the certificate's signer identity and does not check the log entry's contents". Proposal 0028 deferred this work in its Non-goals, and proposal 0029 kept it out of scope. The fix therefore starts with a spec change.

### Paths that reach the verifier

- `podium verify --provider sigstore-keyless --signature <envelope>`, given either `<artifact>` (verified over the served `content_hash`) or `--content-hash` (`cmd/podium/sign.go` `verifyCmd`). This is the only path on which a genuine author-produced keyless envelope verifies.
- `podium-mcp` configured with `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless` and a policy above `never`. It passes every served `delivery_signature` to `SigstoreKeyless.Verify` through `enforceSignaturePolicy` and `sign.EnforceVerification`. An honest registry serves only registry-managed delivery envelopes, which this verifier refuses (§4.7.10). A compromised registry or an on-path party can instead serve substituted content, its matching `delivery_hash`, and a keyless envelope from any OIDC identity, and the bridge accepts it. This proposal removes the path: `podium-mcp` stops accepting `sigstore-keyless` (SPEC-3, CODE-5), so a keyless envelope never reaches a bridge verifier.

Registry stored-row admission uses only the registry-managed signer (`pkg/registry/core/admit.go`, `internal/serverboot/serverboot.go`), and no SDK verifies signatures.

### Trust inputs

The only trust input is `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` (§6.2), a PEM CertPool of Fulcio anchors. No Rekor public key input, no `trusted_root.json` input, and no identity-policy variable exist. `go.mod` and `go.sum` contain no Sigstore module, and `pkg/sign/sigstore.go` is written on the standard library alone.

## Decisions

- **Fix and fail closed.** This decision was taken for the user. A keyless verification with no identity policy configured never accepts an envelope.
- **Extend the standard-library verifier and do not adopt sigstore-go.** The problem statement assumed sigstore-go was already in `go.mod`. That premise is false, so the rule to extend an SDK the module already imports does not apply. sigstore-go would add TUF, in-toto, protobuf-specs, and transparency-dev to the module graph, and its bundle format does not match Podium's envelope. The checks this proposal needs are a SAN and extension read, a Merkle audit-path recomputation with SHA-256, an ECDSA or Ed25519 check of a signed-note checkpoint, an `encoding/asn1` parse of an RFC 3161 token and its CMS SignedData with `x509.Certificate.CheckSignature` over the re-tagged signed attributes, and JSON decodes of the trusted root and the hashedrekord body. All of them fit in the standard library, in roughly 250 lines.
- **One identity list and one issuer.** `PODIUM_SIGSTORE_CERT_IDENTITY` takes a comma-separated list of exact SANs, following the list convention of `PODIUM_SIGNATURE_VERIFY_KEY`. The list covers several signers without a regular expression, so the draft's `PODIUM_SIGSTORE_CERT_IDENTITY_REGEXP`, its mutual-exclusion rule, and its compile refusal are dropped. A regular-expression form can be added later if a need appears. Entries are compared byte for byte.
- **Inclusion proof to a signed checkpoint.** Rekor v2 serves no SET, so the log proof is the entry's RFC 6962 inclusion proof and the checkpoint it leads to. The verifier recomputes the root from `SHA-256(0x00 || body)`, the log index, and the hashes by the RFC 9162 §2.1.3.2 algorithm, requires the root and tree size the checkpoint states, and requires a checkpoint signature line that verifies under a trusted log key. Key-ID hints in the signature line are not interpreted: each trusted key is tried, which avoids depending on the per-algorithm key-ID derivation. A log that shows different trees to different clients is not detected, because no witness or consistency check runs.
- **Attested time from a timestamp authority.** The time comes from an RFC 3161 token over the SHA-256 of the signature bytes, verified under a timestamp authority of the trusted root at its own `genTime`. The chain check runs at `genTime`, and an expired certificate with a valid timestamp is accepted. The token carries no nonce, because nothing at verification time could check one and the imprint already binds the token to a fresh signature.
- **Entry binding.** The verifier decodes the proven entry body and requires the digest, the signature, and the certificate to match the envelope. Without the binding, a valid inclusion proof for an unrelated entry would pass.
- **Offline verification and a required log.** `Verify` reads everything it needs from the envelope and the trust root, so `fetchRekor` is deleted. The alternative of fetching the entry by index and verifying the returned SET online was rejected: it keeps an unsigned, envelope-chosen index as the lookup key and puts a network call inside verification. `Sign` requires `RekorURL` and `TSAURL`, because an envelope without a log entry or a timestamp can no longer verify.
- **Sigstore's `trusted_root.json` as the trust input (formerly OQ-2).** Sigstore distributes its certificate authorities, Rekor v2 log keys, and timestamp-authority chains in `trusted_root.json` through its TUF repository, with validity windows that a PEM concatenation loses. A PEM file cannot carry the timestamp authorities without hand extraction, and one PEM pool would mix timestamp-authority roots with Fulcio roots. The file is decoded with `encoding/json`, because its `rawBytes` fields are standard base64, which `[]byte` accepts. `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` replaces `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` with no alias, because Podium is pre-1.0. The `_FILE` suffix follows `PODIUM_SESSION_TOKEN_FILE` and the variable it replaces. Podium neither fetches the file nor verifies TUF metadata, and the operator supplies an authentic copy.
- **No new error code and no sentinel for the policy.** Every per-envelope failure wraps `sign.ErrSignatureInvalid` (`materialize.signature_invalid`) with a distinguishing message, as `pkg/sign/sigstore.go` already does. No caller branches on a policy failure once the bridge reads no policy, so the draft's `ErrIdentityPolicyMissing` and `ErrIdentityPolicyInvalid` sentinels are not added. `Validate` returns a plain error naming the variable, and `Verify` wraps it.
- **`podium-mcp` drops `sigstore-keyless` (formerly OQ-1).** The bridge verifies only §4.7.10 delivery envelopes, which are always registry-managed, so a `sigstore-keyless` bridge can verify no load. The provider value is removed from the values `podium-mcp` accepts, and the start refuses with `config.invalid`. The refusal reuses `resolveVerifier`'s existing unrecognized-name arm, so it applies under every policy, including `never`, and before any material is read. The alternative of keeping the value and refusing each delivery signature without verifying it was rejected: it kept a provider that refuses every signed load, a trust-root start check for a file the bridge never verified with, and a per-load branch. `buildSignatureProvider`, whose only reachable arm after the removal is `registry-managed`, is deleted.
- **Signing endpoints in §6.2 with public-good defaults.** `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and `PODIUM_SIGSTORE_TSA_URL` gain §6.2 rows with defaults, applied in `cmd/podium`. `PODIUM_SIGSTORE_OIDC_TOKEN` stays out of §6.2 because a credential has no default. Its absence refuses through `ErrSigstoreUnavailable`.
- **Generated envelopes plus one recorded fixture.** The generator under `internal/testharness/sigstoreharness` builds every refusal variant, because a recorded fixture cannot be tampered with selectively without re-signing. One recorded staging set pins the real wire formats, because a generator written from the same reading of the formats as the verifier would share its mistakes.
- **The audit anchor reads the nested index.** `extractRekorLogIndex` reads `tlog.log_index` through a nested pointer field, which keeps the distinction between a present zero and an absent field. Deleting the function and changing `Anchor`'s return type was the alternative, and it would touch every caller for a code path production does not exercise.

## Spec amendment: §4.7.9 Sigstore-keyless verification

**SPEC-1.** `spec/04-artifact-model.md`, §4.7.9 "Signing", the second bullet of the key-model list (line 912 at the time of writing). Replace:

> - **Sigstore-keyless.** An OIDC-attested signature with a transparency-log entry and no key management. No registry signing mode produces one; the consumer-side verifier does not check the certificate's signer identity and does not check the log entry's contents, so a keyless envelope attests that some certificate the configured trust root issued signed the digest, and nothing about who holds it.

with:

> - **Sigstore-keyless.** An OIDC-attested signature with a transparency-log entry and no key management. No registry signing mode produces one. A keyless envelope carries:
>   - the signer's certificate chain, leaf first;
>   - the signature over the SHA-256 digest;
>   - the transparency-log entry that records them: its log index, its entry body, the inclusion-proof hashes, and the signed checkpoint the proof leads to;
>   - an RFC 3161 timestamp token over the signature.
>
> A keyless envelope `podium sign` produces carries the log entry and the timestamp token, which it obtains from the transparency log and the timestamp authority configured under §6.2. A verifier accepts a keyless envelope only when all of the following hold:
>   - The timestamp token is signed by a certificate whose extended key usage lists time stamping and that chains to a timestamp authority of the configured trust root at the time the token states, and its message imprint is the SHA-256 digest of the envelope's signature. That time is the attested time.
>   - The RFC 6962 Merkle audit path from the entry body at the log index yields the root hash and tree size the checkpoint states.
>   - The checkpoint carries a signature that verifies under a transparency-log key of the configured trust root.
>   - The entry body is a `hashedrekord` version `0.0.2` entry that records the digest being verified, the envelope's signature, and the envelope's leaf certificate.
>   - The leaf certificate chains to a certificate authority of the configured trust root, lists code signing in its extended key usage, and is valid at the attested time.
>   - The signature verifies over the digest under the leaf's public key.
>   - One of the leaf's email or URI subject alternative names equals an entry of the certificate identity configured under §6.2. No other subject alternative name type is matched.
>   - The OIDC issuer the leaf's Fulcio issuer extension carries equals the OIDC issuer configured under §6.2.
>
> A certificate that carries no extended key usage, or whose extended key usage lists only the any-purpose usage, lists neither time stamping nor code signing. A trust-root authority or log key whose validity period excludes the attested time is not used. A verifier with no certificate identity or no OIDC issuer configured accepts no keyless envelope. Validity is evaluated at the attested time, so an envelope stays verifiable after its short-lived certificate expires. The verifier makes no network call. An envelope that carries no log entry or no timestamp token is refused, and every refusal is `materialize.signature_invalid`.

The registry-managed bullet and the paragraph that follows the list ("The signature the registry mints at ingest attests ...") are unchanged. §4.7.10 is unchanged. Its sentence that a consumer whose verifier is Sigstore-keyless refuses a present delivery signature with `materialize.signature_invalid` whenever its §4.7.9 policy verifies the signature no longer applies to any MCP server configuration, because the MCP server accepts no `sigstore-keyless` provider (SPEC-3), and the MCP server is the only consumer that applies a §4.7.9 policy (`PODIUM_VERIFY_SIGNATURES` is read by `podium-mcp` alone, `docs/reference/cli.md:821`). The sentence stays true as a statement about the verifier: `podium verify --provider sigstore-keyless` without `--signature` refuses the delivery envelope with `materialize.signature_invalid` because the envelope carries no certificate chain. The refusal reads `empty envelope` (CODE-3 step 3) once the identity policy and the trusted root are configured, and TEST-2 case 10 pins it.

## Spec amendment: §6.2 Sigstore trusted root, identity policy, and signing endpoints

**SPEC-2.** `spec/06-mcp-server.md`, §6.2 configuration table. Two edits land in one commit.

(a) Replace the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row (line 38 at the time of writing), which renames the variable to `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`:

> | `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` | Path to the PEM trust root `sigstore-keyless` verification checks an envelope's certificate chain against. The MCP server reads it only under the `sigstore-keyless` provider and a policy above `never`; unset, or naming no readable file, under that provider and such a policy refuses the MCP server's start with `config.signature_provider_unavailable`, naming the variable (§4.7.9, §6.9). `podium sign` and `podium verify` read it under `--provider sigstore-keyless` whatever the policy and do not refuse on an unset or unreadable value; `podium verify` then refuses each envelope as `materialize.signature_invalid`, because no trust root is configured. Environment only; no flag or config-file key | (unset) |

with:

> | `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` | Path to the Sigstore trusted-root document (`trusted_root.json`, in the format Sigstore distributes through its TUF repository) that `sigstore-keyless` verification uses (§4.7.9). Each `certificateAuthorities` entry supplies the DER certificates of `certChain.certificates`, ordered leaf to root, whose last certificate is a chain anchor and whose others are intermediates; a leaf certificate must chain to one of them. Each `tlogs` entry supplies a transparency-log key, the DER SubjectPublicKeyInfo in `publicKey.rawBytes`, under which a checkpoint must verify; a key whose `publicKey.keyDetails` is neither ECDSA P-256 nor Ed25519 is not used. Each `timestampAuthorities` entry supplies the certificate chain a timestamp token must chain to. An entry is used only when its `validFor` window contains the attested time. A window includes both ends, an absent `start` or `end` leaves that side unbounded, and a key listed more than once is usable when any of its windows contains the attested time. `ctlogs` is not read. Podium neither fetches the document nor verifies TUF metadata. The MCP server does not read it, because it accepts no `sigstore-keyless` provider (`PODIUM_SIGNATURE_PROVIDER`). `podium sign` and `podium verify` read the file under `--provider sigstore-keyless` whatever the policy and do not refuse to start on an unset or unreadable value. `podium verify` refuses each envelope as `materialize.signature_invalid`, naming the cause, in each of these cases: the file is unset or unreadable; it does not parse; it lacks a certificate authority, a usable transparency-log key, or a timestamp authority; `PODIUM_SIGSTORE_CERT_IDENTITY` yields no entry; or `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` is unset. Environment only; no flag or config-file key | (unset) |

(b) Insert five rows immediately after the amended row:

> | `PODIUM_SIGSTORE_CERT_IDENTITY` | The signer identities `podium verify` accepts under `--provider sigstore-keyless` (§4.7.9): one or more subject alternative names, each an email address or a URI, separated by commas. Surrounding whitespace is removed from each entry and empty entries are dropped. A keyless envelope verifies only when its leaf certificate carries an email or URI subject alternative name equal, byte for byte, to an entry. The refusal for an unset value is stated under `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`. Environment only; no flag or config-file key | (unset) |
> | `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` | The OIDC issuer URL `podium verify` accepts under `--provider sigstore-keyless` (§4.7.9). A keyless envelope verifies only when the issuer its leaf certificate's Fulcio issuer extension carries equals this value byte for byte. Surrounding whitespace is removed from the value, and a value that is empty after the removal counts as unset. The refusal for an unset value is stated under `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`. Environment only; no flag or config-file key | (unset) |
> | `PODIUM_SIGSTORE_FULCIO_URL` | Fulcio endpoint from which `podium sign --provider sigstore-keyless` requests the short-lived signing certificate (§4.7.9). An unset or empty value takes the default. No verifier reads it. Environment only; no flag or config-file key | `https://fulcio.sigstore.dev` |
> | `PODIUM_SIGSTORE_REKOR_URL` | Base URL of the Rekor v2 transparency log in which `podium sign --provider sigstore-keyless` records the entry through `POST /api/v2/log/entries`, receiving the inclusion proof and checkpoint the envelope carries (§4.7.9). A log that serves no `/api/v2` path fails the signing. An unset or empty value takes the default. No verifier reads it. Environment only; no flag or config-file key | `https://log2025-1.rekor.sigstore.dev` |
> | `PODIUM_SIGSTORE_TSA_URL` | RFC 3161 endpoint from which `podium sign --provider sigstore-keyless` requests the timestamp token over the signature (§4.7.9). An unset or empty value takes the default. No verifier reads it. Environment only; no flag or config-file key | `https://timestamp.sigstore.dev/api/v1/timestamp` |

## Spec amendment: `podium-mcp` signature providers

**SPEC-3.** Three edits land in one commit.

(a) `spec/06-mcp-server.md`, §6.2, the `PODIUM_SIGNATURE_PROVIDER` row (line 35 at the time of writing). Replace the description cell "Selected `SignatureProvider` (§9.1): `noop`, `registry-managed`, or `sigstore-keyless`. An unrecognized value refuses startup with `config.invalid`" with:

> Selected `SignatureProvider` (§9.1): `registry-managed` or `noop`. Every delivery signature the MCP server verifies is registry-managed (§4.7.10), so it accepts no `sigstore-keyless` provider. Any other value, `sigstore-keyless` included, refuses startup with `config.invalid` under every verification policy, with a message naming the value and `registry-managed`. `podium sign` and `podium verify` read this variable as their `--provider` default and also accept `sigstore-keyless` (§4.7.9)

(b) `spec/06-mcp-server.md`, §6.9, the row "A policy above `never` and no verification material" (line 428 at the time of writing). Replace ", to `registry-managed` when" with ", and to `registry-managed` when". Delete ", and to `sigstore-keyless` when `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` is unset or names no readable file". The `(§6.2)` that follows stays.

(c) `spec/09-extensibility.md`, §9.1, the `SignatureProvider` row (line 26 at the time of writing). Replace "`sigstore-keyless` signs through `podium sign` and verifies at the consumer" with "`sigstore-keyless` signs through `podium sign` and verifies through `podium verify`; the MCP server accepts no `sigstore-keyless` provider (§6.2)".

## Proposed solution

### CODE-1. `pkg/sign`: identity policy and the `SigstoreKeyless` field

New file `pkg/sign/identity_policy.go`, and the struct in `pkg/sign/sigstore.go`.

- `type IdentityPolicy struct { Identities []string; Issuer string }`. The doc comment states that matching covers email and URI SANs only, that an otherName SAN (for example Fulcio's username SAN, OID `1.3.6.1.4.1.57264.1.7`) is never matched, and that comparison is byte for byte. Cite `// Spec: §4.7.9, §6.2`.
- `func NewIdentityPolicy(identities, issuer string) IdentityPolicy` splits `identities` on commas, trims surrounding whitespace from each entry with `strings.TrimSpace`, drops empty entries, and trims surrounding whitespace from `issuer` with `strings.TrimSpace`, so a whitespace-only issuer is empty and `Validate` refuses it (SPEC-2(b)).
- `func (p IdentityPolicy) Validate() error` returns `errors.New("no certificate identity configured (PODIUM_SIGSTORE_CERT_IDENTITY)")` when `Identities` is empty, then `errors.New("no OIDC issuer configured (PODIUM_SIGSTORE_CERT_OIDC_ISSUER)")` when `Issuer` is empty.
- Unexported `func (p IdentityPolicy) match(leaf *x509.Certificate) error`:
  1. Collect `leaf.EmailAddresses` and the `String()` of each `leaf.URIs` entry. When none equals an entry of `Identities`, return `certificate identity mismatch: certificate carries <comma-joined SANs>`.
  2. Find extension `1.3.6.1.4.1.57264.1.8` in `leaf.Extensions`. When present, `asn1.Unmarshal` it into a string with the `utf8` tag; a parse error or non-empty trailing bytes returns `malformed OIDC issuer extension`, with no fallback. When absent, read `1.3.6.1.4.1.57264.1.1` as raw bytes. When neither is present, return `certificate carries no OIDC issuer extension`.
  3. When the issuer differs from `p.Issuer`, return `OIDC issuer mismatch: certificate carries <issuer>`.
- `SigstoreKeyless` gains `Identity IdentityPolicy` with a doc comment naming §4.7.9. Add `TSAURL string`, documented as required by `Sign` and unread by `Verify`. Rewrite the `TrustRoot` field comment: it holds the bytes of a Sigstore `trusted_root.json`, `Verify` parses it on every call, and `Sign` ignores it. The field name is unchanged, so `cmd/podium` and `cmd/podium-mcp` keep compiling between S4 and S6. Delete the `Now` field and the `now()` helper, and remove `Now` from the type's doc comment.

### CODE-2. `pkg/sign` and `pkg/audit`: the `tlog` envelope and a `Sign` that requires Rekor v2 and a timestamp authority

`pkg/sign/sigstore.go`, `pkg/sign/rekor.go`, the new `pkg/sign/timestamp.go`, and `pkg/audit/anchor.go`.

- Replace the envelope type:

  ```go
  type envelope struct {
  	Cert      string     `json:"cert"`
  	Signature string     `json:"signature"`
  	TLog      *tlogEntry `json:"tlog"`
  	Timestamp string     `json:"timestamp"` // base64 DER RFC 3161 TimeStampToken (CMS ContentInfo)
  }

  // tlogEntry is the Rekor v2 entry and inclusion proof a keyless envelope carries.
  // Spec: §4.7.9.
  type tlogEntry struct {
  	LogIndex   int64    `json:"log_index"`
  	Body       string   `json:"body"`       // base64 canonicalized hashedrekord v0.0.2 body
  	Hashes     []string `json:"hashes"`     // base64 audit path, leaf to root
  	Checkpoint string   `json:"checkpoint"` // signed-note text, as Rekor returns it
  }
  ```

- Delete `rekorRecord`, its `hashedRekord*` v0.0.1 types, `rekorEntryResponse`, `rekorEntry`, and `fetchRekor`.
- `uploadRekor(ctx, digest, sig []byte, leaf *x509.Certificate) (*tlogEntry, error)` posts to `strings.TrimRight(RekorURL, "/") + "/api/v2/log/entries"` with the body `{"hashedRekordRequestV002":{"digest":<b64>,"signature":{"content":<b64>,"verifier":{"x509Certificate":{"rawBytes":<b64 leaf DER>},"keyDetails":"PKIX_ECDSA_P256_SHA_256"}}}}`. It decodes `logIndex` (a string; absent means `0`, pinned by TEST-2 case 6a), `canonicalizedBody`, `inclusionProof.hashes`, and `inclusionProof.checkpoint.envelope`. It returns `rekor: response carries no canonicalized body`, `rekor: response carries no inclusion proof`, or `rekor: response carries no checkpoint` when the corresponding field is empty.
- `requestTimestamp(ctx, sig []byte) ([]byte, error)` in `timestamp.go` posts a DER `TimeStampReq{version 1, messageImprint{sha256, SHA-256(sig)}, certReq TRUE}` with `Content-Type: application/timestamp-query` to `TSAURL`. It decodes `TimeStampResp`, returns `tsa: status <n>` for a `PKIStatus` other than 0 or 1, and returns `tsa: response carries no token` when the token is absent. Otherwise it returns the token's DER `FullBytes`. No nonce is sent.
- `Sign` returns `fmt.Errorf("%w: FulcioURL, RekorURL, TSAURL, and OIDCToken are all required", ErrSigstoreUnavailable)` when any of the four is empty, before generating a key. It returns `content hash: algorithm <alg> is not sha256` for a non-`sha256` hash. It then runs `mintCert`, signs, calls `requestTimestamp` (wrapping errors as `tsa: ...`) and `uploadRekor` (wrapping errors as `rekor: ...`), and stores both in the envelope.
- Doc comments: the `SigstoreKeyless` type comment describes the `tlog` envelope and the offline `Verify`; the `RekorURL` field comment says it is required for `Sign` and unread by `Verify`; the `Client` field comment says it is used by `Sign` only; the `TSAURL` field comment says it is required for `Sign` and unread by `Verify`; the `ErrSigstoreUnavailable` comment lists `RekorURL` and `TSAURL`.
- `pkg/audit/anchor.go` `extractRekorLogIndex` decodes into `struct { TLog *struct { LogIndex *int64 \`json:"log_index"\` } \`json:"tlog"\` }` and returns `-1` when `TLog` or `LogIndex` is nil or the decode fails. Update the function comment to name `tlog.log_index`. `Anchor`'s signature and the `audit.anchored` context keys are unchanged.

### CODE-3. `pkg/sign`: offline `Verify`

`pkg/sign/sigstore.go`, `pkg/sign/rekor.go`, and the new `pkg/sign/trusted_root.go`, `pkg/sign/timestamp.go`, and `pkg/sign/inclusion.go`. Every error below is returned as `fmt.Errorf("%w: <message>", ErrSignatureInvalid)` or with `%v` appended for an underlying cause. Cite `// Spec: §4.7.9` on `Verify`, `verifyTimestamp`, `verifyInclusion`, and `bindEntry`, and `// Spec: §4.7.9, §6.2` on `parseTrustedRoot`.

`Verify(ctx, contentHash, signature)` runs these steps in order. Each step stays under the 50-line bound by delegating to a helper.

1. `s.Identity.Validate()`. A failure is wrapped and returned.
2. `parseTrustedRoot(s.TrustRoot)` in `trusted_root.go` returns a `trustedRoot` that holds the certificate authorities, the log keys, and the timestamp authorities, each with its `validFor` window. It decodes with `encoding/json` into a struct that declares only the members it reads: `certificateAuthorities[].certChain.certificates[].rawBytes` and `validFor`; `tlogs[].publicKey.rawBytes`, `keyDetails`, and `validFor`; and `timestampAuthorities[].certChain.certificates[].rawBytes` and `validFor`. Each `rawBytes` is `[]byte`, and `validFor` is `struct{ Start, End *time.Time }`.
   - An empty input returns `no trust root configured`. A decode error, including bad base64 or a bad timestamp, returns `trust root does not parse: <err>`.
   - A certificate-authority or timestamp-authority entry with no certificate returns `trust root authority <i> carries no certificate`. A certificate that fails `x509.ParseCertificate` returns `trust root certificate: <err>`.
   - A `tlogs` key whose `keyDetails` is not `PKIX_ECDSA_P256_SHA_256` or `PKIX_ED25519`, or whose bytes do not parse as that type, is skipped.
   - The function returns `trust root carries no certificate authority`, `trust root carries no usable transparency-log key`, or `trust root carries no timestamp authority` when the corresponding set is empty.
   - `(validFor) contains(t time.Time) bool` returns false when `Start` is set and `t` precedes it, or `End` is set and `t` follows it, and true otherwise. Each listing of a key is a separate entry with its own window, so a key listed more than once is usable when any listing's window contains the time.
   - The function runs once per `Verify` call. `podium verify` verifies one envelope per process.
3. Decode the envelope.
   - A JSON error returns `parse envelope: <err>`, and an empty `cert` or `signature` returns `empty envelope`.
   - A nil `tlog` returns `envelope carries no transparency-log entry`, and a `tlog` with an empty `body` or `checkpoint` returns `transparency-log entry is incomplete`.
   - An empty `timestamp` returns `envelope carries no timestamp`.
4. Base64-decode the signature. A failure returns `signature decode: <err>`.
5. `verifyTimestamp(root, tokenDER, sigBytes) (time.Time, error)` in `timestamp.go`:
   - Unmarshal a ContentInfo of type `1.2.840.113549.1.7.2` holding SignedData whose `eContentType` is `1.2.840.113549.1.9.16.1.4`.
   - Require exactly one SignerInfo carrying signed attributes, with a `contentType` attribute equal to the TSTInfo OID and a `messageDigest` attribute equal to the SignerInfo digest algorithm's hash of `eContent`. Accepted digest algorithms are SHA-256, SHA-384, and SHA-512.
   - Select the signer certificate from the token's `certificates` and every timestamp-authority chain certificate by the `sid` (IssuerAndSerialNumber against `RawIssuer` and `SerialNumber`, or SubjectKeyIdentifier against `SubjectKeyId`).
   - Check the signature with `cert.CheckSignature(alg, signedAttrsWithSETTag, sig)`, where `alg` maps `ecdsa-with-SHA256/384/512` and `sha256/384/512WithRSAEncryption`, plus `id-ecPublicKey` and `rsaEncryption` combined with the digest algorithm.
   - Unmarshal the TSTInfo fields from `version` through `genTime`. The later optional fields are not read.
   - Require `messageImprint` to be SHA-256 equal to `SHA-256(sigBytes)`.
   - Verify the signer certificate against each timestamp authority whose window contains `genTime` (roots: the chain's last certificate; intermediates: the rest plus the token's certificates; `KeyUsages: TimeStamping`; `CurrentTime: genTime`). When no authority's window contains `genTime`, return `timestamp authority not trusted at <RFC 3339 UTC>: no timestamp authority valid at that time`.
   - After the chain verifies, require the signer certificate's own `ExtKeyUsage` to contain `x509.ExtKeyUsageTimeStamping`, else return `timestamp authority not trusted at <RFC 3339 UTC>: signer certificate lacks the time-stamping usage`. The explicit check is required because `x509.Certificate.Verify` skips the usage check for a certificate with no extended-key-usage extension and passes one that lists `x509.ExtKeyUsageAny` (`checkChainForKeyUsage`, `$GOROOT/src/crypto/x509/verify.go:995-1003` in Go 1.26), so `KeyUsages` alone admits both. Whether the extension is critical is not checked. The code comment states both reasons.
   - Errors: `timestamp does not parse: <err>`, `timestamp carries <n> signers; want 1`, `timestamp signature does not verify`, `timestamp does not cover the signature`, and `timestamp authority not trusted at <RFC 3339 UTC>: <err>`.
   - The ESS signing-certificate attribute is not read. The code comment states that the chain check already binds the signer key.
   - Return `genTime` as T.
6. `verifyInclusion(root, env.TLog, T)` in `inclusion.go`:
   - Parse the checkpoint as signed-note text. The body lines are the origin, the decimal tree size, and the base64 32-byte root hash, with optional extension lines. A blank line follows, then one or more `— <name> <base64>` lines. A malformed checkpoint returns `checkpoint does not parse`.
   - Require `0 <= log_index < tree size`. Compute the root from `SHA-256(0x00 || body)` and the decoded hashes by the RFC 9162 §2.1.3.2 algorithm, consuming every hash. Any mismatch returns `inclusion proof does not verify`.
   - For each signature line, take the decoded bytes after the 4-byte key hint, and try each log key whose window contains T: ECDSA `VerifyASN1` over `SHA-256(note body)`, or Ed25519 `Verify` over the note body. When none verifies, return `checkpoint signature does not verify under a trusted log key`.
7. `pemDecodeChain(env.Cert)`. A failure returns `cert chain: <err>`.
8. `bindEntry(body, contentHash, sigBytes, leaf)`:
   - A `splitContentHash` failure returns `content hash: <err>`, and an algorithm other than `sha256` returns `content hash: algorithm <alg> is not sha256`.
   - The base64-decoded body must decode with `kind` `hashedrekord` and `apiVersion` `0.0.2`, else `log entry is not a hashedrekord v0.0.2 entry`.
   - `spec.hashedRekordV002.data.algorithm` must be `SHA2_256` and `data.digest` must equal the digest bytes, else `log entry does not bind the digest`.
   - `signature.content` must equal `sigBytes`, else `log entry does not bind the signature`.
   - `signature.verifier.x509Certificate.rawBytes` must equal `leaf.Raw`, else `log entry does not bind the certificate`.
9. When no certificate authority's window contains T, return `no certificate authority valid at timestamp time <RFC 3339 UTC>`. Otherwise run `leaf.Verify` with roots and intermediates from the certificate authorities valid at T plus the envelope intermediates, `KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}`, and `CurrentTime: T`. An `x509.CertificateInvalidError` with reason `x509.Expired` returns `certificate not valid at timestamp time <RFC 3339 UTC>: <err>`, and any other error returns `cert chain: <err>`. After `leaf.Verify` succeeds, require `leaf.ExtKeyUsage` to contain `x509.ExtKeyUsageCodeSigning`, else return `leaf lacks the code-signing usage`. The explicit check is required for the reason step 5 states: `KeyUsages` alone admits a leaf with no extended-key-usage extension or one that lists only `x509.ExtKeyUsageAny`.
10. The leaf key must be `*ecdsa.PublicKey` (else `leaf is not ECDSA`), and `ecdsa.VerifyASN1` over the digest bytes must pass (else `signature does not verify`). The digest bytes come from `decodeContentHash(contentHash)`. Its error branch returns `content hash: <err>` and cannot run once step 8 has passed, because `splitContentHash` in step 8 already hex-decodes the same string. The branch carries a comment naming that reason, per `.claude/rules/test-coverage.md`.
11. `s.Identity.match(leaf)`. A failure is wrapped and returned.

Delete `fetchRekor`. `Verify` keeps its `ctx` parameter for the `Provider` interface and makes no network call.

### CODE-4. `cmd/podium`: read the trusted root, the signing endpoints, and the identity policy

`cmd/podium/sign.go` `loadSignatureProvider`, `sigstore-keyless` arm. Read the trust root from `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` in place of `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` (`cmd/podium/sign.go:214`). Set `FulcioURL: envDefault("PODIUM_SIGSTORE_FULCIO_URL", defaultFulcioURL)`, `RekorURL: envDefault("PODIUM_SIGSTORE_REKOR_URL", defaultRekorURL)`, and `TSAURL: envDefault("PODIUM_SIGSTORE_TSA_URL", defaultTSAURL)`, where the three named constants in `cmd/podium/sign.go` hold `https://fulcio.sigstore.dev`, `https://log2025-1.rekor.sigstore.dev`, and `https://timestamp.sigstore.dev/api/v1/timestamp` and cite §6.2. Add `Identity: sign.NewIdentityPolicy(os.Getenv("PODIUM_SIGSTORE_CERT_IDENTITY"), os.Getenv("PODIUM_SIGSTORE_CERT_OIDC_ISSUER"))`. Keep the discarded `os.ReadFile` error, because §6.2 specifies a per-envelope refusal rather than a start refusal. Update the function comment to name both variables and cite §6.2. With no policy, `podium verify` prints `verify failed: ... no certificate identity configured (PODIUM_SIGSTORE_CERT_IDENTITY)` and exits 1. `podium sign` reads the variables and does not use them.

### CODE-5. `cmd/podium-mcp`: drop `sigstore-keyless`

`cmd/podium-mcp/main.go`. Replace `resolveVerifier` with the following, and delete `buildSignatureProvider` (`main.go:2372-2406`) and its doc comment:

```go
// resolveVerifier resolves the §4.7.9 verification material for the policy
// and provider name. A name other than registry-managed or noop refuses with
// config.invalid under every policy, before any material is read:
// sigstore-keyless is refused there too, because every delivery signature
// the bridge verifies is registry-managed (§4.7.10). Under never no material
// is resolved and the verifier is nil. noop under an enforcing policy refuses
// because it verifies nothing, and registry-managed refuses with
// config.signature_provider_unavailable when its key set does not resolve.
//
// Spec: §4.7.9, §4.7.10, §6.2, §6.9.
func resolveVerifier(policy sign.VerificationPolicy, name string) (sign.Provider, error) {
	switch name {
	case "noop", "registry-managed":
	default:
		return nil, fmt.Errorf("config.invalid: PODIUM_SIGNATURE_PROVIDER %q is not a podium-mcp provider; want registry-managed or noop (every delivery signature is registry-managed; sigstore-keyless applies to podium sign and podium verify only)", name)
	}
	if policy == sign.PolicyNever {
		return nil, nil
	}
	if name == "noop" {
		return nil, fmt.Errorf("config.signature_provider_unavailable: PODIUM_SIGNATURE_PROVIDER=noop verifies no signature and PODIUM_VERIFY_SIGNATURES=%s requires verification; select registry-managed, or set PODIUM_VERIFY_SIGNATURES=never", policy)
	}
	keys, err := registryManagedVerifyKey()
	if err != nil {
		return nil, fmt.Errorf("config.signature_provider_unavailable: %w; supply the verification material or set PODIUM_VERIFY_SIGNATURES=never", err)
	}
	return sign.RegistryManagedKey{Trusted: keys}, nil
}
```

`loadConfig` (`main.go:400`), `config.verifier`, `config.signatureProvider`, `applyConfigKV`, and `enforceSignaturePolicy` are unchanged. `sign.Noop.Verify`'s message (`pkg/sign/sign.go:98`) keeps naming `sigstore-keyless`, because only `podium verify --provider noop` reaches it, and `podium verify` accepts `sigstore-keyless`. podium-mcp never reaches it: `noop` is refused above `never`, and the verifier is nil under `never`. `errors` stays imported in `main.go` through its other uses.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Envelope minted before this change (top-level `log_index`, no `tlog`) | `podium verify` exits 1 with `envelope carries no transparency-log entry` | §4.7.9 "An envelope that carries no log entry or no timestamp token is refused" (SPEC-1); `CHANGELOG.md` `Changed` (CL-1), `docs/reference/cli.md` (DOC-1(b)) |
| Leaf expired at verification time, valid at the timestamp time | Accepted | §4.7.9 "stays verifiable after its short-lived certificate expires" (SPEC-1); DOC-1(b) |
| Timestamp time outside the leaf's validity window | Refused, `certificate not valid at timestamp time` | §4.7.9 "valid at the attested time" (SPEC-1); DOC-1(b), DOC-1(e) |
| Fulcio and timestamp-authority clocks disagree so `genTime` precedes the leaf's `NotBefore` | Refused (accepted; `Sign` requests the timestamp after the certificate is issued, so only clock skew between the two services produces this case) | §4.7.9 (SPEC-1); DOC-1(b) |
| Checkpoint signed by no trusted log key | Refused, `checkpoint signature does not verify under a trusted log key` | §4.7.9, §6.2 (SPEC-1, SPEC-2); DOC-1(c) |
| Inclusion proof with a wrong hash, a wrong index, or an extra or missing hash | Refused, `inclusion proof does not verify` | §4.7.9 (SPEC-1); DOC-1(b) |
| Negative `log_index`, such as `-1` | Refused, `inclusion proof does not verify` | §4.7.9 (SPEC-1); pinned by TEST-2 case 3 and `TestVerifyInclusion` |
| Trusted root with no usable `tlogs` key (absent, or every key neither P-256 nor Ed25519) | `podium verify` refuses each envelope, `no usable transparency-log key` | §6.2 (SPEC-2); DOC-1(c) |
| Trusted root with no `timestampAuthorities` | `podium verify` refuses each envelope, `no timestamp authority` | §6.2 (SPEC-2); DOC-1(c) |
| Certificate authority, timestamp authority, or log key whose `validFor` excludes the timestamp time | Not used. The refusal names the role that has no valid entry: `no certificate authority valid at timestamp time`, `timestamp authority not trusted`, or `checkpoint signature does not verify under a trusted log key` | §4.7.9 (SPEC-1), §6.2 (SPEC-2); DOC-1(c); pinned by TEST-2 case 5a |
| Trusted root that also lists `ctlogs`, an unsupported log key, and keys of other logs, as Sigstore's own file does | Accepted. Unused entries are ignored, and the unsupported key is skipped | §6.2 (SPEC-2); DOC-1(c); pinned by TEST-2 case 5b |
| A PEM file passed as `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` | `podium verify` refuses each envelope, `trust root does not parse` | §6.2 (SPEC-2); DOC-1(c) |
| Only the removed `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` set | `podium verify` refuses each envelope, `no trust root configured` | §6.2 (SPEC-2); CL-1 `Removed`; pinned by TEST-4 case 8 |
| Timestamp from a timestamp authority outside the trusted root, or a timestamp-authority leaf with no extended key usage or with only the any-purpose usage | Refused, `timestamp authority not trusted` | §4.7.9 (SPEC-1); DOC-1(b); pinned by TEST-2 case 3 |
| Code-signing leaf issued by a timestamp-authority root, or a timestamp token signed by a time-stamping certificate a certificate-authority root issued | Refused, `cert chain` for the leaf and `timestamp authority not trusted` for the token, because each role's anchors come only from its own trusted-root member | §4.7.9 (SPEC-1), §6.2 (SPEC-2(a)); not documented beyond the spec, because the role of each trusted-root member is a property of Sigstore's format; pinned by TEST-2 case 3 (`WithLeafIssuedByTSARoot`, `WithTimestampSignedByFulcioCA`) |
| Fulcio-chained leaf with no extended key usage or with only the any-purpose usage | Refused, `leaf lacks the code-signing usage` | §4.7.9 (SPEC-1); DOC-1(b); pinned by TEST-2 case 3 |
| Timestamp over a different signature | Refused, `timestamp does not cover the signature` | §4.7.9 (SPEC-1); DOC-1(b) |
| A trusted timestamp authority states a false time | Accepted (the timestamp authority is trusted for time) | §4.7.9 (SPEC-1); DOC-1(b) |
| `PODIUM_SIGSTORE_CERT_IDENTITY` unset, empty, or only commas | Refused, naming the variable | §4.7.9 "accepts no keyless envelope" (SPEC-1), §6.2 (SPEC-2); DOC-1(b), DOC-1(c) |
| `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` unset | Refused, naming the variable | §4.7.9, §6.2 (SPEC-1, SPEC-2); DOC-1(b), DOC-1(c) |
| Leaf whose only identity is an otherName SAN | Refused (accepted). A Fulcio leaf, whose SAN extension is critical, is refused at the chain check with `cert chain` (`x509: unhandled critical extension`). A leaf whose otherName-only SAN extension is non-critical is refused at the identity match with `certificate identity mismatch` | §4.7.9 "no other subject alternative name type is matched" (SPEC-1); DOC-1(c); pinned by TEST-2 case 3 (`WithOtherNameSANOnly`, with and without `WithNonCriticalSAN`) |
| Leaf carries several SANs | Accepted when any email or URI SAN equals any list entry; pinned by `TestIdentityPolicy_Match` and TEST-2 cases 1 and 3 | §4.7.9 (SPEC-1); DOC-1(c) |
| Identity differs only in letter case | Refused (accepted; byte comparison); pinned by `TestIdentityPolicy_Match` and TEST-2 case 3 | §6.2 "byte for byte" (SPEC-2); DOC-1(c) |
| Leaf with `.1.1` and no `.1.8` | The `.1.1` value is compared | §4.7.9 "the leaf's Fulcio issuer extension" (SPEC-1); not documented beyond the spec, because both are Fulcio issuer extensions |
| Leaf with a malformed `.1.8` | Refused, `malformed OIDC issuer extension`, with no fallback to `.1.1` | §4.7.9 (SPEC-1); not documented, because a conforming Fulcio never issues one |
| Leaf with neither issuer extension | Refused | §4.7.9 (SPEC-1); DOC-1(b) |
| The log operator signs a checkpoint for a tree it shows to no one else (split view) | Accepted (no witness cosignature or consistency proof is checked) | Non-goal; DOC-1(b) states that the verifier checks the inclusion proof and checkpoint signature only |
| An OIDC identity is compromised after it signed | Its envelopes keep verifying; no revocation exists (accepted) | §4.7.9 (SPEC-1); DOC-1(b) |
| `podium sign --provider sigstore-keyless` with `PODIUM_SIGSTORE_OIDC_TOKEN` unset | Exit 1, `sign failed: sign: sigstore-keyless not configured: ...`, no network call | Implementation behavior; DOC-1(a) |
| `PODIUM_SIGSTORE_FULCIO_URL`, `_REKOR_URL`, or `_TSA_URL` unset | `podium sign` uses the public-good default; an empty value does the same, and a set value overrides it | §6.2 (SPEC-2); DOC-1(a), DOC-1(c); pinned by `TestLoadSignatureProvider` (TEST-4 unit paragraph) |
| `PODIUM_SIGSTORE_REKOR_URL` names a Rekor v1 log | `podium sign` exits 1, `rekor: HTTP 404` | §6.2 "A log that serves no `/api/v2` path fails the signing" (SPEC-2); DOC-1(a); pinned by TEST-2 case 8a |
| Timestamp authority unreachable or returns a rejection status | `podium sign` exits 1, `tsa: ...`, no envelope | Implementation behavior; DOC-1(a) |
| Envelope whose entry is a hashedrekord v0.0.1 (Rekor v1) body | Refused, `not a hashedrekord v0.0.2 entry` | §4.7.9 (SPEC-1) |
| Content hash with an algorithm other than `sha256` | `podium sign` exits 1 before any network call, and `podium verify` refuses, `is not sha256` | §4.7.9 "the SHA-256 digest" (SPEC-1); pinned by TEST-2 cases 3 and 8b |
| `PODIUM_SIGSTORE_REKOR_URL` or `PODIUM_SIGSTORE_TSA_URL` set for `podium verify` | Ignored; no request is sent | §4.7.9 "The verifier makes no network call" (SPEC-1); DOC-1(b) |
| `podium-mcp` with `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless`, any policy | Start refused with `config.invalid`, naming `sigstore-keyless` and `registry-managed`; no trust root or key is read | §6.2 (SPEC-3(a)); DOC-1(d), DOC-1(e) |
| A shell exports `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless` for `podium verify` and starts `podium-mcp` from it | `podium-mcp` start refused with `config.invalid`. A harness entry that sets `PODIUM_SIGNATURE_PROVIDER=registry-managed` is unaffected. A Standalone entry, which omits the variable, inherits the exported value and refuses (accepted; DOC-1(d) tells such a consumer to set `registry-managed` in the MCP entry) | §6.2 (SPEC-3(a)); DOC-1(d) |
| `podium verify <artifact>` without `--signature` under `--provider sigstore-keyless` | The registry-managed delivery envelope (`{"key_id","signature"}`, `pkg/sign/registry_managed.go:70-73`) carries no `cert`, so it is refused with `empty envelope` (`materialize.signature_invalid`) once the identity policy and the trusted root are configured; with either unset, that refusal comes first | §4.7.10 (unchanged) "The envelope carries no certificate chain"; pinned by TEST-2 case 10; `docs/reference/cli.md` already states that `--provider sigstore-keyless` refuses it |
| Audit anchor signed by a keyless signer | `audit.anchored` records `tlog.log_index` | §8.6 (unchanged); not documented, because production anchoring uses the Ed25519 anchor key |

## Testing

**TEST-1 · unit, `internal/testharness/sigstoreharness` (new package).** Lands in S3. The package imports only the standard library and `internal/testharness` helpers, never `pkg/sign`.

- `New(t testing.TB) *Harness` builds:
  - a Fulcio root and an intermediate with the code-signing usage;
  - a timestamp-authority root and a timestamp-authority leaf with the critical time-stamping usage;
  - no extended-key-usage extension on either root, as on Sigstore's own roots, so a chain that ends at a root is never refused by the root's usages, and the cross-role cases below fail only because the root belongs to the other role;
  - an ECDSA P-256 log key and an Ed25519 log key;
  - a fixed clock at 2026-01-15T12:00:00Z.
- `(*Harness).TrustedRootJSON(opts ...RootOpt) []byte` returns a `trusted_root.json` with one certificate authority, one timestamp authority, and the P-256 log key. It uses Sigstore's member names: `mediaType`; `tlogs[]` with `baseUrl`, `hashAlgorithm`, `publicKey{rawBytes, keyDetails, validFor}`, and `logId{keyId}`; `certificateAuthorities[]` with `subject`, `uri`, `certChain.certificates[].rawBytes` ordered intermediate then root, and `validFor`; and `timestampAuthorities[]` in the same form. Timestamps are written as RFC 3339 with milliseconds and `Z`. Every window starts at the clock minus 1 hour and has no end unless an option sets it. Options:
  - `WithoutCAs()`, `WithoutTSAs()`, `WithoutTLogs()`
  - `WithEd25519LogKey()`, `WithUnsupportedLogKey()` (RSA `keyDetails`), `WithExtraTLog()` (an unrelated P-256 key), `WithCTLogs()` (a `ctlogs` entry whose `rawBytes` are not DER)
  - `WithCAValidFor(start, end *time.Time)`, `WithTSAValidFor(start, end *time.Time)`, `WithLogValidFor(windows ...Window)` (one `tlogs` entry per window, same key)
  - `WithGarbageCert()`, `WithMalformedJSON()`
  - `WithForeignCA()` replaces only `certificateAuthorities` with an unrelated root and intermediate, and keeps the timestamp authority and the log key, so a genuine envelope passes the timestamp and checkpoint steps and fails at the chain check
- `(*Harness).Envelope(t, contentHash string, opts ...EnvOpt) string` does the following:
  1. Issues the leaf (`NotBefore` clock minus 5 minutes, `NotAfter` clock plus 15 minutes, email SAN `alice@acme.com`, issuer extension `.1.8` = `https://accounts.acme.com`, extended key usage code signing) and signs the digest.
  2. Builds the hashedrekord v0.0.2 body.
  3. Places the body at index 5 of a 7-leaf RFC 6962 tree.
  4. Writes the audit path and a checkpoint signed by the configured log key.
  5. Issues an RFC 3161 token, a CMS SignedData with `contentType` and `messageDigest` signed attributes, at the harness clock over `SHA-256(signature)`.
  6. Returns the envelope JSON.

  It keeps `WithSAN(email)`, `WithURISAN(uri)`, `WithOtherNameSANOnly()`, `WithNonCriticalSAN()`, `WithIssuerExt(oid, raw)`, `WithoutIssuer()`, `WithoutTLog()`, `WithEntryDigest(hash)`, `WithEntrySignature(sig)`, `WithEntryCert(der)`, `WithEntryKind(kind, apiVersion)` (default `hashedrekord`, `0.0.2`), `WithForeignSignature()`, `WithEd25519Leaf()`, and `WithoutCodeSigning()`, each of which binds its leaf and signature in the v0.0.2 body. `WithOtherNameSANOnly()` writes the otherName SAN extension critical, as Fulcio does, and `WithNonCriticalSAN()` writes it non-critical so the leaf passes the chain check and reaches the identity match. `WithSAN(email)` replaces the default email SAN, and `WithURISAN(uri)` adds a URI SAN beside it, so the two combine into a leaf that carries one email SAN and one URI SAN. `WithForeignSignature()` signs the digest of a different content hash with the leaf key and writes those same signature bytes into the envelope `signature`, the v0.0.2 body's `signature.content`, and the RFC 3161 message imprint, while the body's `data.digest` stays the verified content hash, so every check before the signature check passes. `WithEd25519Leaf()` issues the leaf over an Ed25519 key, signs the digest with `ed25519.Sign`, and binds that leaf and signature in the body, the inclusion proof, and the timestamp as usual. `WithoutCodeSigning()` issues the leaf with the server-authentication extended key usage only, signs the digest, and binds and timestamps that leaf and signature as usual, so the chain to the certificate authority still holds and only the code-signing usage condition fails. It adds:
  - `WithLeafWithoutEKU()` issues the leaf with no extended-key-usage extension, and `WithLeafAnyUsage()` issues it with `x509.ExtKeyUsageAny` as its only extended key usage. Each signs the digest and binds and timestamps that leaf and signature as usual, so `leaf.Verify` with `KeyUsages` code signing passes and only the explicit CODE-3 step 9 check refuses the leaf.
  - `WithTimestampTime(t)`, `WithoutTimestamp()`, `WithTimestampOver(sig)`
  - `WithUntrustedTSA()`, `WithFlippedTimestampSignature()`
  - `WithLeafIssuedByTSARoot()` issues the leaf from the harness timestamp-authority root, with the code-signing usage, the default SAN, and the default issuer extension, and binds, logs, and timestamps that leaf and signature as usual. The envelope `cert` carries the leaf alone. The timestamp authority and the log key verify, and the leaf chains only to a root the trusted root lists under `timestampAuthorities`, so only a CODE-3 step 9 pool built from `certificateAuthorities` alone refuses it.
  - `WithTimestampSignedByFulcioCA()` signs the token with a certificate that the harness Fulcio root issued with the critical time-stamping usage, and embeds that certificate alone in the token's `certificates`. The token chains only to a root the trusted root lists under `certificateAuthorities`, so only a CODE-3 step 5 pool built from `timestampAuthorities` alone refuses it. The Fulcio root issues the certificate rather than the Fulcio intermediate, because the intermediate's code-signing usage would make `x509.Certificate.Verify` refuse the time-stamping chain in `checkChainForKeyUsage` (`$GOROOT/src/crypto/x509/verify.go:1009-1024` in Go 1.26) even under a merged pool, and the case would then not distinguish the two pools.
  - `WithTSALeafWithoutTimeStamping()` signs the token with a second timestamp-authority leaf, issued by the harness timestamp-authority root, that carries no extended-key-usage extension, and embeds that leaf in the token's `certificates`. `WithTSALeafAnyUsage()` does the same with a leaf whose only extended key usage is `x509.ExtKeyUsageAny`. In both, the chain to the trusted timestamp authority holds and `KeyUsages: TimeStamping` passes, so only the explicit CODE-3 step 5 check refuses the token.
  - `WithoutInclusionProof()` (omits `hashes` and `checkpoint`), `WithFlippedProofHash()`, `WithLogIndex(i)`, `WithTreeSize(n, i)`, `WithMalformedCheckpoint()`
  - `WithCheckpointSignedByForeignKey()`, `WithEd25519Checkpoint()`
- `(*Harness).TimestampToken(sig []byte, opts ...TSOpt) []byte` exposes the token builder for package-`sign` unit tests. Options:
  - `WithTwoSigners()`, `WithoutSignedAttrs()`, `WithMessageDigestMismatch()`
  - `WithTSTContentType(oid)`, `WithSignatureAlgorithm(oid)`, `WithoutEmbeddedCerts()`, `WithSubjectKeyIdentifierSID()`
- `(*Harness).FakeServer(t, opts ...ServerOpt) *httptest.Server` serves:
  - Fulcio `/api/v2/signingCert`;
  - Rekor `POST /api/v2/log/entries`, which returns a protojson `TransparencyLogEntry` with `logIndex` as a string, `canonicalizedBody`, and `inclusionProof{hashes, checkpoint{envelope}}`;
  - the timestamp authority at `POST /api/v1/timestamp`, which returns a DER `TimeStampResp` stamped at the harness clock.

  Options make each service fail, omit the body, the proof, or the checkpoint, or return timestamp status 2. `WithRekorLogIndexOmitted()` places the posted entry at index 0 of a 3-leaf tree, returns the inclusion proof and checkpoint for that position, and leaves `logIndex` out of the protojson response, as protojson does for a zero value. A request counter is exposed.
- `(*Harness).WriteFiles(t, dir string) Files` writes the trusted root as `trusted_root.json` and named envelopes for `test/e2e`.
- A unit test in the package checks that a generated checkpoint verifies under the exported log key, that a generated audit path recomputes the checkpoint root, and that a generated token's signed attributes verify under the timestamp-authority leaf.

**TEST-2 · unit, `pkg/sign` and `pkg/audit`.** Lands in S4 with CODE-1 to CODE-3. Each test carries `// Spec: §4.7.9`, or the annotation its case names, directly above its `func Test` line, and each refusal test keeps `// Matrix: §6.10 (materialize.signature_invalid)`, as `pkg/sign/sigstore_test.go` already does, so `matrix-audit` keeps passing. The proposal adds no matrix cell.

- `pkg/sign/sigstore_test.go` (package `sign_test`):
  1. `TestSigstoreKeyless_VerifyAcceptsBoundEnvelope`: a harness envelope verifies under `NewIdentityPolicy("alice@acme.com", "https://accounts.acme.com")`, under `NewIdentityPolicy(" bob@acme.com, ,alice@acme.com ", ...)`, and under `NewIdentityPolicy("https://github.com/acme/repo/.github/workflows/release.yml@refs/heads/main", " https://accounts.acme.com ")` for an envelope built with `WithSAN("carol@acme.com")` and `WithURISAN("https://github.com/acme/repo/.github/workflows/release.yml@refs/heads/main")`, where the email SAN is outside the list and the URI SAN is in it, and an envelope built with `WithEd25519Checkpoint()` under a trusted root built with `WithEd25519LogKey()`.
  2. `TestSigstoreKeyless_VerifyAtTimestampTime`: the accepted envelope's leaf `NotAfter` (2026-01-15T12:15Z) is in the past on the wall clock, and `Verify` accepts it with no clock injection. `WithTimestampTime(clock + 1h)` is refused with `certificate not valid at timestamp time`.
  3. `TestSigstoreKeyless_VerifyRefusals` (`// Spec: §4.7.9, §6.2`, because its cross-role rows pin the SPEC-2(a) rule that a leaf chains to a `certificateAuthorities` entry and a timestamp token to a `timestampAuthorities` entry), a table, each case asserting `errors.Is(err, sign.ErrSignatureInvalid)` and a message substring: SAN `bob@acme.com` against `alice@acme.com` (`certificate identity mismatch`); SAN `Alice@acme.com` against `alice@acme.com` (`certificate identity mismatch`, because the comparison is byte for byte); `WithSAN("carol@acme.com")` with `WithURISAN("https://github.com/acme/other")` against `alice@acme.com` (`certificate identity mismatch`); issuer `https://evil.example` (`OIDC issuer mismatch`); `WithOtherNameSANOnly` (`cert chain`, because Go's x509 parser refuses the critical otherName-only SAN extension Fulcio issues as an unhandled critical extension); `WithOtherNameSANOnly` with `WithNonCriticalSAN` (`certificate identity mismatch`); `WithoutIssuer` (`no OIDC issuer extension`); malformed `.1.8` beside a matching `.1.1` (`malformed OIDC issuer extension`); `WithoutTLog` (`no transparency-log entry`); `WithEntryDigest` of another hash (`does not bind the digest`); `WithEntrySignature` (`does not bind the signature`); `WithEntryCert` of another leaf (`does not bind the certificate`); `WithEntryKind("hashedrekord", "0.0.1")` (`not a hashedrekord v0.0.2 entry`); a genuine envelope verified against a tampered content hash (`does not bind the digest`, because the entry binding runs before the signature check); a content hash with no `alg:` prefix (`content hash`); `WithForeignSignature` (`signature does not verify`); `WithEd25519Leaf` (`leaf is not ECDSA`); `WithoutCodeSigning` (`cert chain`, because `leaf.Verify` with `KeyUsages` code signing refuses the leaf with an `x509.IncompatibleUsage` reason rather than `x509.Expired`); `WithLeafWithoutEKU` and `WithLeafAnyUsage` (each `leaf lacks the code-signing usage`, because `leaf.Verify` passes both and only the explicit usage check refuses them); a trusted root built with `WithForeignCA` (`cert chain`, because the timestamp authority and the log key still verify and only the leaf's chain fails at step 9); `WithLeafIssuedByTSARoot` (`cert chain`, because the leaf's only anchor is a timestamp-authority root, which the step 9 pool excludes); `WithTimestampSignedByFulcioCA` (`timestamp authority not trusted`, because the token's only anchor is a certificate-authority root, which the step 5 pool excludes); empty identity policy (`PODIUM_SIGSTORE_CERT_IDENTITY`); empty issuer (`PODIUM_SIGSTORE_CERT_OIDC_ISSUER`); trusted root `WithoutCAs` (`no certificate authority`); `WithoutTLogs` (`no usable transparency-log key`); `WithUnsupportedLogKey` as the only log key (`no usable transparency-log key`); `WithoutTSAs` (`no timestamp authority`); `WithTSAValidFor` ending before the clock (`timestamp authority not trusted`); `WithGarbageCert` (`trust root certificate`); `WithMalformedJSON` (`trust root does not parse`); PEM bytes as the trusted root (`trust root does not parse`); empty trust root (`no trust root configured`); malformed JSON (`parse envelope`); `WithoutTimestamp` (`no timestamp`); `WithTimestampOver` (`does not cover the signature`); `WithUntrustedTSA` (`timestamp authority not trusted`); `WithTSALeafWithoutTimeStamping` and `WithTSALeafAnyUsage` (each `signer certificate lacks the time-stamping usage`, inside the `timestamp authority not trusted` message); `WithFlippedTimestampSignature` (`timestamp signature does not verify`); `WithoutInclusionProof` (`incomplete`); `WithFlippedProofHash` (`inclusion proof does not verify`); `WithLogIndex(4)` (`inclusion proof does not verify`); `WithLogIndex(7)` (`inclusion proof does not verify`); `WithLogIndex(-1)` (`inclusion proof does not verify`, because the lower bound of the CODE-3 step 6 index check refuses it; this row pins the C27 bypass, in which `pkg/sign/sigstore.go:204` skips the log check today for a negative `log_index`); `WithCheckpointSignedByForeignKey` (`checkpoint signature does not verify`); `WithMalformedCheckpoint` (`checkpoint does not parse`); and a `sha512:` content hash (`is not sha256`).
  4. `TestSigstoreKeyless_VerifyRecomputesEveryTreePosition`: for tree sizes 1 through 9 and every index, a `WithTreeSize(n, i)` envelope verifies.
  5. `TestSigstoreKeyless_VerifyMakesNoNetworkCall`: `RekorURL` and `TSAURL` set and `Client` built on a `RoundTripper` that calls `t.Fatal`; `Verify` accepts.
  5a. `TestSigstoreKeyless_VerifyTrustedRootWindows`, a table at the timestamp time T (the harness clock). A log-key window ending exactly at T is accepted. A log-key window ending 1 second before T, and one starting 1 second after it, refuse with `checkpoint signature does not verify under a trusted log key`. A certificate-authority window starting exactly at T is accepted. A certificate-authority window ending 1 second before T refuses with `no certificate authority valid at timestamp time`. A timestamp-authority window ending 1 second before T refuses with `timestamp authority not trusted`. `WithLogValidFor` with windows [T−2h, T−1h] and [T−1s, open] is accepted.
  5b. `TestSigstoreKeyless_VerifyIgnoresUnusedTrustedRootEntries`: a root built with `WithEd25519LogKey`, `WithUnsupportedLogKey`, `WithExtraTLog`, and `WithCTLogs` accepts the default envelope.
  6. `TestSigstoreKeyless_RoundTrip`, rewritten: `Sign` against `FakeServer` produces an envelope whose `tlog` and `timestamp` fields are populated and which `Verify` accepts under the harness policy and trust root.
  6a. `TestSigstoreKeyless_SignRecordsAbsentLogIndexAsZero` (`// Spec: §4.7.9`): `Sign` against `FakeServer` with `WithRekorLogIndexOmitted()` succeeds, the envelope's `tlog.log_index` is present and equals 0, and `Verify` accepts the envelope under the harness policy and trust root.
  7. `TestSigstoreKeyless_UnconfiguredFails`, extended: each of `FulcioURL`, `RekorURL`, `TSAURL`, and `OIDCToken` empty in turn with the others set returns `ErrSigstoreUnavailable`, and the fake server's counter stays at 0.
  8. `TestSigstoreKeyless_SignRefusesIncompleteResponses`: `FakeServer` omits the body, then the inclusion proof, then the checkpoint, and then returns timestamp status 2. `Sign` fails naming each, and a timestamp-authority outage fails with `tsa:`.
  8a. `TestSigstoreKeyless_SignRefusesRekorV1` (`// Spec: §6.2`): `FulcioURL` and `TSAURL` point at `FakeServer`, and `RekorURL` points at a separate `httptest` server whose mux serves only `POST /api/v1/log/entries`, so `/api/v2/log/entries` answers 404. `Sign` fails with a message containing `rekor: HTTP 404`, and the v1 handler sees no request.
  8b. `TestSigstoreKeyless_SignRefusesNonSHA256`: `Sign` with a `sha512:` content hash fails with `is not sha256`, and the `FakeServer` request counter stays 0, because the algorithm check precedes `mintCert`.
  9. Delete `TestSigstoreKeyless_VerifyRejectsMissingRekorEntry`, and remove every `Now:` assignment. Port `VerifyDetectsTamperedHash`, `VerifyRejectsForeignTrustRoot` (onto a `WithForeignCA` trusted root, asserting `cert chain`), `VerifyRejectsMissingTrustRoot`, `SignFulcioOutage`, and `VerifyMalformedEnvelope` onto the harness, or fold them into case 3.
  10. `TestSigstoreKeyless_VerifyRefusesADeliveryEnvelope` (`// Spec: §4.7.9, §4.7.10` and `// Matrix: §6.10 (materialize.signature_invalid)`): an envelope from `sign.RegistryManagedKey{PrivateKey: <generated Ed25519 key>}.Sign`, verified by `SigstoreKeyless` with the harness identity policy and trusted root, returns `errors.Is(err, sign.ErrSignatureInvalid)` with `empty envelope`. It replaces the §4.7.10 coverage that `TestDeliverLoadArtifact_SigstoreKeylessRefusesADeliveryEnvelope` carried before TEST-3 deletes it.
- `pkg/sign/identity_policy_test.go` (package `sign`), over leaves the test builds: `TestNewIdentityPolicy_ParsesList` (whitespace, empty entries, only commas, an issuer with surrounding whitespace that parses to the trimmed URL, and a whitespace-only issuer that parses to empty); `TestIdentityPolicy_Validate` (missing identities, missing issuer, a whitespace-only issuer passed through `NewIdentityPolicy`, and both set); `TestIdentityPolicy_Match` (email SAN, URI SAN such as `https://github.com/acme/repo/.github/workflows/release.yml@refs/heads/main`, `.1.1` only, `.1.8` taking precedence over a disagreeing `.1.1`, `.1.8` with trailing bytes, no extension, otherName only, an email SAN `Alice@acme.com` under policy `alice@acme.com` refused with `certificate identity mismatch`, a leaf with an email SAN outside the list and a URI SAN in the list accepted, a leaf with a URI SAN outside the list and an email SAN in the list accepted, and a leaf with an email SAN and a URI SAN both outside the list refused with `certificate identity mismatch`).
- `pkg/sign/rekor_helpers_test.go` (package `sign`): delete `TestFetchRekor_NotFound`, `TestFetchRekor_NonOK`, and `TestFetchRekor_HappyPath`. Add `TestBindEntry` (each mismatch, a `SHA2_384` algorithm, and a non-base64 body).
- `pkg/sign/inclusion_test.go` (package `sign`, `// Spec: §4.7.9`): `TestVerifyInclusion` (the RFC 9162 algorithm over a reference tree, an index at or past the tree size, an index of -1 refused before the RFC 9162 loop converts it to an unsigned value, and leftover hashes) and `TestParseCheckpoint` (missing blank line, non-decimal size, a root that is not 32 bytes, and no signature line).
- `pkg/sign/timestamp_test.go` (package `sign`, `// Spec: §4.7.9`): `TestVerifyTimestamp` over `Harness.TimestampToken` and each `TSOpt`, asserting `timestamp carries 2 signers; want 1` and `timestamp signature does not verify`, and acceptance under `WithoutEmbeddedCerts` (the signer is found in the trusted-root chain) and `WithSubjectKeyIdentifierSID`.
- `pkg/sign/trusted_root_test.go` (package `sign`, `// Spec: §4.7.9, §6.2`): `TestParseTrustedRoot` covers each refusal in CODE-3 step 2 (empty input, malformed JSON, non-base64 `rawBytes`, a bad timestamp, an authority with no certificate, a non-DER certificate, no certificate authority, no usable log key, and no timestamp authority), a skipped unsupported key, `validFor` with and without `end`, and one accepted document written in the test source as a template with Sigstore's member names and filled with harness values. `TestValidityWindow_Contains` covers both ends, one second outside each end, and nil bounds. These tests do not go through `Verify`, so they carry no `// Matrix:` annotation.
- `pkg/audit/anchor_test.go`: rewrite the keyless fakes to the `tlog` form. `// Spec: §8.6`. Assert that `"log_index":"12345"` and `"log_index":"0"` are recorded from `tlog.log_index`, and that an envelope with a top-level `log_index` and no `tlog` records `-1`.

`Verify` holds no shared state, so no concurrent case is added; the existing `go test -race` lane covers the package.

**TEST-3 · unit and e2e, `cmd/podium-mcp` and `test/e2e`.** The `cmd/podium-mcp` bullets land in S6 with CODE-5, because the package's tests do not compile between the deletion of `buildSignatureProvider` and these edits. The `test/e2e` bullet lands in S7.

- `cmd/podium-mcp/config_env_test.go` `TestLoadConfig_VerifierResolution` (`// Spec: §4.7.9, §6.2`): replace the rows "sigstore trust root readable", "sigstore trust root unset", and "sigstore trust root unreadable" with "sigstore-keyless under never" (`policy: "never"`) and "sigstore-keyless under always" (`policy: "always"`, `keyFile: true`, `env: PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` set to `rootFile`). Both want `code: "config.invalid:"` and `msg: []string{"sigstore-keyless", "registry-managed"}`. The second row shows that the refusal precedes material resolution. Add `"registry-managed"` to the "noop under always" row's `msg`, and assert that its refusal does not contain `sigstore-keyless`. Keep the `rootFile` setup (`config_env_test.go:779-782`), renamed to write `trusted_root.json`, because the "sigstore-keyless under always" row points at it; its other users, the three deleted rows, go away. In the table comment, replace "and sigstore-keyless needs a readable trust root" with "and sigstore-keyless refuses with config.invalid under every policy".
- `cmd/podium-mcp/config_env_test.go` `hermetic`: drop `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` from the cleared list, because podium-mcp no longer reads it.
- `cmd/podium-mcp/main_helpers_test.go`: delete `TestBuildSignatureProvider` with its section banner. Rename `TestBuildSignatureProvider_RegistryManagedVerifyKey` to `TestResolveVerifier_RegistryManagedVerifyKey`, call `resolveVerifier(sign.PolicyAlways, "registry-managed")` in place of `buildSignatureProvider("registry-managed")`, and assert that the malformed-entry error starts with `config.signature_provider_unavailable:` and names `PODIUM_SIGNATURE_VERIFY_KEY`. `TestLoadConfig_VerifierResolution` already pins the deleted test's `registry-managed` no-material case ("nothing resolves"), and the deleted `noop` and `sigstore-keyless` construction cases have no remaining code.
- `cmd/podium-mcp/delivery_verify_test.go`: delete `TestDeliverLoadArtifact_SigstoreKeylessRefusesADeliveryEnvelope`. `loadConfig` can no longer produce a `sigstore-keyless` verifier, and the other `signedServer` tests in the file pin the registry-managed half of that test. TEST-2 case 10 carries its §4.7.10 half: a registry-managed delivery envelope refused by the Sigstore-keyless verifier.
- `cmd/podium-mcp/coverage_gaps_test.go:311,340` is unchanged: `applyConfigKV` copies the value verbatim, and validation belongs to `resolveVerifier`.
- `test/e2e/standalone_signing_journey_test.go`, new `TestMCPStart_SigstoreKeylessProviderRefused`, placed after `TestSignedArtifact_NoVerifyKeyRefusesUnderTheDefaults`, with `// Spec: §6.2` and `// Matrix: §6.10 (config.invalid)`. `mcpExec` runs with `PODIUM_REGISTRY=http://127.0.0.1:1`, `PODIUM_CACHE_DIR=<t.TempDir()>`, and `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless`, under the default policy, and sends one `load_artifact` call. Assert a non-zero exit and that stderr contains `config.invalid`, `sigstore-keyless`, and `registry-managed`.

**TEST-4 · e2e, `test/e2e/sigstore_keyless_verify_test.go` (new).** Lands in S8. `// Spec: §4.7.9, §6.2`. `sigstoreharness.WriteFiles` writes the trusted root as `trusted_root.json` and the envelopes to `t.TempDir()`. Every case passes the envelope string as the `--signature` value, an absolute `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and `PODIUM_SIGSTORE_TSA_URL` all pointing at the counting `httptest` server in every case, because an empty URL takes a public-good default, and explicit values for `PODIUM_SIGSTORE_OIDC_TOKEN`, `PODIUM_SIGSTORE_CERT_IDENTITY`, and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, with the empty string where a case wants the variable unset.

1. Matching identity and issuer: `podium verify --provider sigstore-keyless --content-hash <h> --signature <env>` exits 0 and stderr contains `verify ok`. The counting server sees no request.
2. `PODIUM_SIGSTORE_CERT_IDENTITY="bob@acme.com, alice@acme.com"`: exit 0, which shows the list is wired.
3. An envelope with SAN `bob@acme.com` under identity `alice@acme.com`: exit 1, stderr contains `verify failed` and `certificate identity mismatch`.
4. Identity empty: exit 1, stderr names `PODIUM_SIGSTORE_CERT_IDENTITY`.
5. Issuer empty: exit 1, stderr names `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`.
6. A pre-change envelope (`{"cert":...,"signature":...,"log_index":7}`): exit 1, stderr contains `no transparency-log entry`.
7. `podium sign --provider sigstore-keyless --content-hash <h>` with every URL on the counting server and `PODIUM_SIGSTORE_OIDC_TOKEN` empty: exit 1, stderr contains `sigstore-keyless not configured`, and the counter stays 0.
8. `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` empty and `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` naming the trusted root: exit 1, stderr contains `no trust root configured`, which shows the removed variable is unread.

Timestamp, inclusion-proof, and entry-binding failures stay at the unit level in TEST-2, because the binary adds no behavior to them.

`cmd/podium/cli_helpers_test.go` `TestLoadSignatureProvider` (`// Spec: §6.2`) gains a sigstore-keyless table, which lands with TEST-4 in S8. With `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and `PODIUM_SIGSTORE_TSA_URL` unset, and again set to the empty string, the returned `sign.SigstoreKeyless` carries `defaultFulcioURL`, `defaultRekorURL`, and `defaultTSAURL`, asserted against the literal §6.2 URLs. A set value overrides each one. `TrustRoot` holds the bytes of the file `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` names. The test mutates the environment through `t.Setenv`, so it is not parallel.

**TEST-5 · live and unit, `pkg/sign/sigstore_live_test.go`, `pkg/sign/sigstore_fixture_test.go`, and `RELEASING.md`.** Lands in S9. `TestSigstoreKeyless_LiveSmoke` reads `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` in place of `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` (`pkg/sign/sigstore_live_test.go:25`, `:33`), adds `PODIUM_SIGSTORE_REKOR_URL`, `PODIUM_SIGSTORE_TSA_URL`, `PODIUM_SIGSTORE_CERT_IDENTITY`, and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` to its skip gate, sets `TSAURL`, and, when `PODIUM_SIGSTORE_RECORD_DIR` is set, writes `envelope.json`, a copy of the trusted root as `trusted_root.json`, and `meta.json` (`{"content_hash", "identity", "issuer"}`) after `Verify` passes. It builds `Identity: sign.NewIdentityPolicy(...)` from the two policy variables, calls `Identity.Validate()` and `t.Fatalf` on a failure before `Sign`, and updates its doc comment. It does not derive the identity from the OIDC token, because a policy taken from the token that signed can only pass, and because a Fulcio SAN for a CI issuer is a workflow URI rather than the token's `sub`.

Add `TestSigstoreKeyless_VerifiesRecordedStagingEnvelope` (package `sign_test`, `// Spec: §4.7.9`) in a new file, `pkg/sign/sigstore_fixture_test.go`, with no build tag and no skip, so the default suite and the release gate run it. It does not go into `pkg/sign/sigstore_live_test.go`, which stays the Tier 2 file whose single test is `TestSigstoreKeyless_LiveSmoke` (`RELEASING.md:140`). It reads `pkg/sign/testdata/sigstore-staging/`, verifies the envelope offline under the recorded policy with a `Client` whose transport fails the test, and asserts that the same envelope against a different content hash is refused with `does not bind the digest`.

`RELEASING.md`, "Sigstore live tests are manual":

- Replace "the live smoke adds end-to-end coverage of a real Fulcio certificate and a real Rekor inclusion proof" with "the live smoke adds end-to-end coverage of a Fulcio certificate, an RFC 3161 timestamp from the staging timestamp authority, and a Rekor v2 inclusion proof and checkpoint from a running Sigstore instance, and of the entry-binding check against that log's entry body".
- Replace the mocked-suite list "(round-trip, tampered hash, foreign trust root, Fulcio outage, and missing Rekor entry)" with "(round-trip, tampered hash, foreign trust root, Fulcio and timestamp-authority outage, identity and issuer mismatch, a timestamp, inclusion proof, or checkpoint that does not verify, an entry that does not bind the envelope, and certificate validity at the timestamp time), and the release gate also runs `pkg/sign/sigstore_fixture_test.go`, which verifies the recorded staging envelope offline". The replacement ends inside the existing sentence, which continues ", so the signing logic is covered between manual runs".
- In the paragraph that begins "Point the manual run at the Sigstore staging instance" (`RELEASING.md:146`), replace "(`fulcio.sigstage.dev` / `rekor.sigstage.dev`)" with "(`fulcio.sigstage.dev`, the staging Rekor v2 shard, and `timestamp.sigstage.dev`)", and replace the skip sentence with "The test skips unless `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, `PODIUM_SIGSTORE_TSA_URL`, `PODIUM_SIGSTORE_OIDC_TOKEN`, `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, `PODIUM_SIGSTORE_CERT_IDENTITY`, and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` are all set."
- In the export block (`RELEASING.md:149-153`), set `PODIUM_SIGSTORE_REKOR_URL=<the staging Rekor v2 shard URL from the staging signing_config>`, replace `export PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=/path/to/sigstage-trust-bundle.pem` with `export PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=/path/to/sigstage-trusted_root.json`, and add `export PODIUM_SIGSTORE_TSA_URL=https://timestamp.sigstage.dev/api/v1/timestamp`, `export PODIUM_SIGSTORE_CERT_IDENTITY=<the email or URI SAN the token's identity receives>`, and `export PODIUM_SIGSTORE_CERT_OIDC_ISSUER=<the issuer URL the token came from>`. State that the file is the `trusted_root.json` target of the staging Sigstore TUF repository, that the Rekor endpoint must be a v2 log, and that running with `export PODIUM_SIGSTORE_RECORD_DIR=$PWD/pkg/sign/testdata/sigstore-staging` from the repository root refreshes the committed fixture. The value is absolute because `go test` runs the test with `pkg/sign` as its working directory, so a relative value resolves under `pkg/sign/`.
- In the staging-lane bullet (`RELEASING.md:163`), replace "ship the staging trust root as `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`" with "ship the staging `trusted_root.json` as `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`".

**Coverage.** `go test -coverpkg=./... -coverprofile=cover.out ./pkg/sign/... ./pkg/audit/... ./cmd/podium/... ./cmd/podium-mcp/... ./internal/testharness/sigstoreharness/...` reaches 85% on the new lines of `pkg/sign/identity_policy.go`, `pkg/sign/sigstore.go`, `pkg/sign/rekor.go`, `pkg/sign/timestamp.go`, `pkg/sign/inclusion.go`, `pkg/sign/trusted_root.go`, `pkg/audit/anchor.go`, and the rewritten `resolveVerifier` (in-process through `TestLoadConfig_VerifierResolution` and `TestResolveVerifier_RegistryManagedVerifyKey`, and in the subprocess through `TestMCPStart_SigstoreKeylessProviderRefused` under `GOCOVERDIR`). CODE-4's `loadSignatureProvider` defaults run in-process through `TestLoadSignatureProvider`, and the same lines run in the `podium` subprocess, measured with `GOCOVERDIR` over `test/e2e`. The `x509.Expired` branch is reached by TEST-2 case 2, and the CMS and signed-note parser branches by `TestVerifyTimestamp` and `TestParseCheckpoint`, the window refusals by case 5a, and the remaining `Verify` and `parseTrustedRoot` refusals by case 3 and `TestParseTrustedRoot`, including the step-8 content-hash refusal, `leaf is not ECDSA`, the code-signing usage refusals (`WithoutCodeSigning` through `leaf.Verify`, and `WithLeafWithoutEKU` and `WithLeafAnyUsage` through the explicit step 9 check), the explicit step 5 time-stamping usage refusal (`WithTSALeafWithoutTimeStamping` and `WithTSALeafAnyUsage`), and `signature does not verify`. The absent-`logIndex` branch of `uploadRekor` is reached by TEST-2 case 6a. The step-10 `decodeContentHash` error branch cannot run after step 8 passes and carries a comment naming why (CODE-3 step 10).

## Manual validation

**MV-1.** Add scenario S83 to `test/manual-validation.md` after S82, following the existing conventions. Lands in S12.

~~~~markdown
## S83: Sigstore-keyless sign and verify against the staging instance

**Goal.** Validate that `podium sign --provider sigstore-keyless` records a
Rekor v2 inclusion proof and a timestamp-authority timestamp, that
`podium verify` accepts the envelope only for the configured signer and
issuer, that it makes no network call, that the envelope still verifies after
the Fulcio certificate expires, and that `podium-mcp` refuses to start under
`sigstore-keyless`.

**Covers.** The §4.7.9 Sigstore-keyless acceptance conditions, the §6.2
`PODIUM_SIGSTORE_CERT_IDENTITY`, `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`,
trusted-root, and signing-endpoint rows, and the §6.2
`PODIUM_SIGNATURE_PROVIDER` restriction on the MCP server. The unit and
end-to-end tests run against an in-process Fulcio, Rekor, and timestamp
authority. This scenario covers what only a running Sigstore instance
establishes: the SAN and issuer extension Fulcio writes for a token, the body,
inclusion proof, and checkpoint Rekor v2 returns, the token the timestamp
authority issues, and the keys a published `trusted_root.json` carries.

**Why by hand.** The staging instance needs an OIDC token that a person or a
credentialed lane mints, and it writes every signature into a public log.

**Prerequisites.**

- An OIDC token the Sigstore staging Fulcio accepts, and the email or URI SAN
  and issuer URL that token produces. When none is available, skip the
  scenario and record the skip and the reason.
- The staging `trusted_root.json` from the Sigstore staging TUF repository, and
  the staging Rekor v2 shard URL from the staging `signing_config`.
- Built `podium` and `podium-mcp` binaries on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then export the
   staging coordinates and compute a content hash.

   ```bash
   export PODIUM_SIGSTORE_FULCIO_URL=https://fulcio.sigstage.dev
   export PODIUM_SIGSTORE_REKOR_URL=<staging Rekor v2 shard URL>
   export PODIUM_SIGSTORE_TSA_URL=https://timestamp.sigstage.dev/api/v1/timestamp
   export PODIUM_SIGSTORE_OIDC_TOKEN=<token>
   export PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=<staging trusted_root.json>
   export PODIUM_SIGSTORE_CERT_IDENTITY=<expected SAN>
   export PODIUM_SIGSTORE_CERT_OIDC_ISSUER=<expected issuer URL>
   H="sha256:$(printf 'podium s83' | shasum -a 256 | cut -d' ' -f1)"
   echo "$H"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and `$H` is
   `sha256:` followed by 64 hex characters.

2. Sign and inspect the envelope.

   ```bash
   podium sign --provider sigstore-keyless --content-hash "$H" > "$WORK/env.json"; echo "exit=$?"
   python3 -c 'import json,sys; e=json.load(open(sys.argv[1])); print(sorted(e)); print(sorted(e["tlog"]))' "$WORK/env.json"
   ```

   **Expect.** `exit=0`. The first list is
   `['cert', 'signature', 'timestamp', 'tlog']`, and the second is
   `['body', 'checkpoint', 'hashes', 'log_index']`. A `log_index` key at the
   top level, or a missing `timestamp` or `checkpoint`, is the defect this
   step catches.

3. Verify with the matching policy and with the network blocked for Rekor and
   the timestamp authority.

   ```bash
   PODIUM_SIGSTORE_REKOR_URL=http://127.0.0.1:1 PODIUM_SIGSTORE_TSA_URL=http://127.0.0.1:1 \
     podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   ```

   **Expect.** `verify ok` and `exit=0`. A connection error naming
   `127.0.0.1:1` means `Verify` still contacts the log or the timestamp
   authority.

4. Verify with another identity and with no identity.

   ```bash
   PODIUM_SIGSTORE_CERT_IDENTITY=bob@acme.com \
     podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   PODIUM_SIGSTORE_CERT_IDENTITY= \
     podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   ```

   **Expect.** The first run prints `verify failed:` with
   `certificate identity mismatch` and the SAN the certificate carries, then
   `exit=1`. The second prints `verify failed:` naming
   `PODIUM_SIGSTORE_CERT_IDENTITY`, then `exit=1`. `verify ok` on either run
   is the C26 defect.

5. Wait at least 15 minutes, so the 10-minute Fulcio certificate has expired,
   and repeat step 3.

   ```bash
   podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   ```

   **Expect.** `verify ok` and `exit=0`. A `certificate has expired` error is
   the C27 defect.

6. Sign with no OIDC token.

   ```bash
   PODIUM_SIGSTORE_OIDC_TOKEN= podium sign --provider sigstore-keyless --content-hash "$H"; echo "exit=$?"
   ```

   **Expect.** `sign failed:` with `sigstore-keyless not configured`, then
   `exit=1`, and no envelope on stdout.

7. Start `podium-mcp` under `sigstore-keyless`, under the default policy and
   under `never`.

   ```bash
   PODIUM_SIGNATURE_PROVIDER=sigstore-keyless PODIUM_REGISTRY=http://127.0.0.1:1 podium-mcp </dev/null; echo "exit=$?"
   PODIUM_SIGNATURE_PROVIDER=sigstore-keyless PODIUM_VERIFY_SIGNATURES=never PODIUM_REGISTRY=http://127.0.0.1:1 podium-mcp </dev/null; echo "exit=$?"
   ```

   **Expect.** Each run prints a line naming `config.invalid`,
   `sigstore-keyless`, and `registry-managed` on stderr, then a non-zero
   `exit=`. A run that starts serving, or that refuses with
   `config.signature_provider_unavailable`, is the defect this step catches.
~~~~

## Documentation changes

**DOC-1.** Lands in S10. Write all prose to `doc-style.md`. No new fenced runnable command is added to `docs/`, so no `tools/doccov/manifest.yaml` entry is needed.

(a) `docs/reference/cli.md`, `podium sign` section (line 740 at the time of writing). Replace "The `sigstore-keyless` provider produces an OIDC-attested signature with a transparency-log entry, configured through the `PODIUM_SIGSTORE_*` env vars." with:

> The `sigstore-keyless` provider obtains a short-lived certificate from the Fulcio endpoint at `PODIUM_SIGSTORE_FULCIO_URL` with the OIDC token in `PODIUM_SIGSTORE_OIDC_TOKEN` and signs the content hash. It then obtains an RFC 3161 timestamp over the signature from `PODIUM_SIGSTORE_TSA_URL` and records the signature in the Rekor v2 transparency log at `PODIUM_SIGSTORE_REKOR_URL`. The three endpoints default to the Sigstore public-good instances. When `PODIUM_SIGSTORE_OIDC_TOKEN` is unset, `podium sign` exits non-zero with `sign failed: sign: sigstore-keyless not configured` before it contacts any service, and the message names no error code. A Rekor v1 URL fails the signing. The envelope carries the log entry, its inclusion proof, the signed checkpoint, and the timestamp, so `podium verify` can check them without contacting any service.

In the same paragraph, replace "An invocation that cannot resolve the key it needs exits non-zero naming `config.signature_provider_unavailable`." with "A `registry-managed` invocation that cannot resolve the key it needs exits non-zero naming `config.signature_provider_unavailable`." The keyless arm of `loadSignatureProvider` (`cmd/podium/sign.go:213-220`) returns no error, and only the `registry-managed` arms prefix the code (`cmd/podium/sign.go:247`, `:257`).

(b) `docs/reference/cli.md`, `podium verify` section, after the paragraph that ends "and `--provider sigstore-keyless` refuses it." (line 758 at the time of writing). Add:

> Under `--provider sigstore-keyless`, `podium verify` checks the following against `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`:
> - The timestamp verifies under a timestamp authority in the file, is signed by a certificate whose extended key usage lists time stamping, and covers the signature.
> - The inclusion proof leads from the log entry to a checkpoint signed by a transparency-log key in the file.
> - The log entry records the content hash, the signature, and the envelope's certificate.
> - The certificate chains to a certificate authority in the file, lists code signing in its extended key usage, and was valid at the timestamp's time.
> - The signature verifies.
> - One of the certificate's email or URI subject alternative names appears in `PODIUM_SIGSTORE_CERT_IDENTITY`, and the certificate's OIDC issuer equals `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`.
>
> With either policy variable unset, every envelope is refused. Validity is checked at the timestamp's time, so an envelope keeps verifying after its certificate expires. The command checks no witness cosignature or consistency proof, and it makes no network call. An envelope that carries no log entry or no timestamp, including every envelope produced before this format, is refused. A certificate whose identity is compromised after it signed keeps verifying, because Sigstore-keyless certificates carry no revocation.

(c) `docs/reference/cli.md`, environment table (line 824 at the time of writing). Replace the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row and add five rows after it:

> | `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` | Trust root for the `sigstore-keyless` provider: a Sigstore `trusted_root.json` carrying the certificate authorities, the transparency-log keys, and the timestamp authorities, which Sigstore publishes through its TUF repository. Podium does not fetch the file or verify TUF metadata. Verification uses each entry only within its `validFor` window. `podium sign` and `podium verify` read it under `--provider sigstore-keyless` whatever the policy. An unset or unreadable value does not refuse the command. `podium verify` then refuses each envelope as `materialize.signature_invalid`, and it does the same when the file does not parse or lacks a certificate authority, a usable ECDSA P-256 or Ed25519 log key, or a timestamp authority. |
> | `PODIUM_SIGSTORE_CERT_IDENTITY` | The signer identities `podium verify --provider sigstore-keyless` accepts: one or more email addresses or URIs, separated by commas, each compared byte for byte with the certificate's email and URI subject alternative names. A certificate whose only subject alternative name is an otherName, such as a Fulcio username identity, is refused: Fulcio marks that extension critical, so the chain check refuses it with `cert chain`. Unset, every keyless envelope is refused. `podium-mcp` does not read it. |
> | `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` | The OIDC issuer URL `podium verify --provider sigstore-keyless` accepts, compared byte for byte with the issuer the certificate records after surrounding whitespace is removed from the value. Unset or blank, every keyless envelope is refused. `podium-mcp` does not read it. |
> | `PODIUM_SIGSTORE_FULCIO_URL` | Fulcio endpoint `podium sign --provider sigstore-keyless` requests the signing certificate from. Unset or empty, it takes `https://fulcio.sigstore.dev`. |
> | `PODIUM_SIGSTORE_REKOR_URL` | Rekor v2 log `podium sign --provider sigstore-keyless` records the entry in. Unset or empty, it takes `https://log2025-1.rekor.sigstore.dev`. A Rekor v1 URL fails the signing. |
> | `PODIUM_SIGSTORE_TSA_URL` | RFC 3161 timestamp authority `podium sign --provider sigstore-keyless` requests the timestamp from. Unset or empty, it takes `https://timestamp.sigstore.dev/api/v1/timestamp`. |

(d) `docs/consuming/configure-your-harness.md`. Replace the `PODIUM_SIGNATURE_PROVIDER` row's description (line 34 at the time of writing) with "`registry-managed` (default) or `noop`. Under a policy above `never`, `noop` refuses the start, because it verifies nothing. Any other value, including `sigstore-keyless`, refuses the start with `config.invalid`: the registry signs every delivery record with its registry-managed key, and `sigstore-keyless` applies to `podium sign` and `podium verify` only." Delete the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row (line 37 at the time of writing), because the MCP server does not read it. In the Standalone section (line 519 at the time of writing), after "so the recipe omits `PODIUM_SIGNATURE_PROVIDER` and `PODIUM_SIGNATURE_VERIFY_KEY`.", append "The MCP server inherits `PODIUM_SIGNATURE_PROVIDER` from the environment that launches it, so when the shell exports another value for `podium sign` or `podium verify`, such as `sigstore-keyless`, set `PODIUM_SIGNATURE_PROVIDER=registry-managed` in the MCP entry. Otherwise the MCP server refuses to start with `config.invalid`."

(e) `docs/reference/error-codes.md`. In the `materialize.signature_invalid` row (line 122 at the time of writing), replace "(tampered content, expired signature, unknown signer)" with "(tampered content, an unknown signer, or a Sigstore-keyless envelope whose certificate identity, OIDC issuer, timestamp, inclusion proof, checkpoint signature, or log entry does not verify or match, or whose certificate was not valid at the timestamp's time)". In the `config.signature_provider_unavailable` row (line 88 at the time of writing), replace "`podium sign` and `podium verify` exit non-zero with it when the key their command needs does not resolve." with "`podium sign` and `podium verify` under the `registry-managed` provider exit non-zero with it when the key their command needs does not resolve." The row does not describe the `podium sign --provider sigstore-keyless` refusal for an unset `PODIUM_SIGSTORE_OIDC_TOKEN`, because that refusal prints `ErrSigstoreUnavailable`'s message without the code (`pkg/spi/errors.go:33`, `cmd/podium/sign.go:62`); DOC-1(a) documents it by its message. In the `config.signature_provider_unavailable` row (line 88 at the time of writing), also replace "`noop`, which verifies nothing; `registry-managed` when" with "`noop`, which verifies nothing; and `registry-managed` when", and delete "; and `sigstore-keyless` when `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` is unset or unreadable". In the `config.invalid` row (line 78 at the time of writing), replace "when `PODIUM_SIGNATURE_PROVIDER` names no recognized provider" with "when `PODIUM_SIGNATURE_PROVIDER` names a value other than `registry-managed` or `noop`, including `sigstore-keyless`, which only `podium sign` and `podium verify` accept".

(f) `docs/deployment/operator-guide.md` (line 113 at the time of writing). Replace "A Sigstore envelope passed with `--signature` takes `--provider sigstore-keyless` with `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`, and verification fails with `no trust root configured` when the trust root is absent; the delivery signature itself is never a Sigstore envelope." with:

> A Sigstore envelope passed with `--signature` takes `--provider sigstore-keyless` with `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, a Sigstore `trusted_root.json` that holds the certificate authorities, the transparency-log keys, and the timestamp authorities, and with `PODIUM_SIGSTORE_CERT_IDENTITY` and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, which name the expected signer. Verification fails with `no trust root configured` when the trust root is absent, and with a message naming the variable when either policy variable is unset. The delivery signature itself is never a Sigstore envelope.

(g) `docs/deployment/operator-guide.md` (line 213 at the time of writing). Replace ", and a consumer configured for `sigstore-keyless` refuses it with `materialize.signature_invalid` wherever its policy verifies it." with ", and `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`."

(h) `docs/deployment/extending.md`, the `SignatureProvider` row (line 41 at the time of writing). After "fall back to `registry-managed`," insert "and `podium-mcp` accepts `registry-managed` and `noop` only, because every delivery signature it verifies is registry-managed;". The "and the `noop` provider refuses…" clause stays.

(i) `OPERATIONS.md`, "Sigstore (keyless signing)". At line 280, replace the skip sentence with the TEST-5 wording, which names `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, `PODIUM_SIGSTORE_TSA_URL`, `PODIUM_SIGSTORE_OIDC_TOKEN`, `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, `PODIUM_SIGSTORE_CERT_IDENTITY`, and `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`. In the table, change the `PODIUM_SIGSTORE_REKOR_URL` example to `<staging Rekor v2 shard URL>`, and replace the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row (line 287) with "| `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` | Path to the Sigstore `trusted_root.json` for the chosen instance. | `/path/to/sigstage-trusted_root.json` |". Add rows for `PODIUM_SIGSTORE_TSA_URL` (example `https://timestamp.sigstage.dev/api/v1/timestamp`), `PODIUM_SIGSTORE_CERT_IDENTITY`, `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, and `PODIUM_SIGSTORE_RECORD_DIR` (the absolute path of the fixture directory the live smoke writes, example `$PWD/pkg/sign/testdata/sigstore-staging` from the repository root, because `go test` runs the test in `pkg/sign`). In the reset block (line 334), replace `export PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=""` with `export PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=""`, and add `export PODIUM_SIGSTORE_TSA_URL=""`, `export PODIUM_SIGSTORE_CERT_IDENTITY=""`, and `export PODIUM_SIGSTORE_CERT_OIDC_ISSUER=""`.

(j) The env examples. In `test.env.example`, replace `# PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=` (line 142) with `# PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=`, replace the Rekor example `https://rekor.sigstage.dev` (line 140) with `<staging Rekor v2 shard URL>`, and add `# PODIUM_SIGSTORE_TSA_URL=https://timestamp.sigstage.dev/api/v1/timestamp`, `# PODIUM_SIGSTORE_CERT_IDENTITY=`, and `# PODIUM_SIGSTORE_CERT_OIDC_ISSUER=`. In `.env.example`, replace `export PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE=""` (line 30) with `export PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=""`, and add `export PODIUM_SIGSTORE_TSA_URL=""`.

**CL-1.** `CHANGELOG.md`, `[Unreleased]`. Lands in S11.

Under `### Fixed`:

> - **Sigstore-keyless verification checks the signer** (§4.7.9, §6.2): `podium verify --provider sigstore-keyless` accepts an envelope only when its certificate's email or URI subject alternative name appears in `PODIUM_SIGSTORE_CERT_IDENTITY` and its OIDC issuer equals `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, when its timestamp verifies under a timestamp authority in the trusted root, when its inclusion proof leads to a checkpoint signed by a log key in the trusted root, and when the log entry records the content hash, the signature, and the certificate. The certificate is checked at the timestamp's time, so an envelope keeps verifying after its Fulcio certificate expires. Verification makes no network call.

Under `### Changed`:

> - **The Sigstore-keyless envelope carries its log proof and a timestamp** (§4.7.9): the top-level `log_index` is replaced by a `tlog` object with `log_index`, `body`, `hashes`, and `checkpoint`, and a `timestamp` field carries an RFC 3161 token. Envelopes from earlier releases are refused.
> - **`podium sign --provider sigstore-keyless` uses Rekor v2 and a timestamp authority** (§6.2): `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and the new `PODIUM_SIGSTORE_TSA_URL` default to the Sigstore public-good instances, and a Rekor v1 URL fails the signing.
> - **The Sigstore trust root is `trusted_root.json`** (§6.2): `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` names Sigstore's `trusted_root.json`, and its certificate authorities, transparency-log keys, and timestamp authorities apply only within their validity windows.

Under `### Removed`:

> - **`PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`** (§6.2). It is no longer read. Set `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` instead.
> - **`podium-mcp` no longer accepts `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless`** (§6.2): the start refuses with `config.invalid`, naming `registry-managed`. Every delivery signature is registry-managed (§4.7.10), so the provider verified no load, and it accepted a keyless envelope from any OIDC identity. `podium sign` and `podium verify` keep `sigstore-keyless`.

CL-1 also amends an entry already in `CHANGELOG.md` `[Unreleased]` `### Changed`: the bullet that begins "**`load_artifact` serves a delivery attestation, and the response fields" (`CHANGELOG.md:555` at the time of writing). In its operator actions (`CHANGELOG.md:581-583` at the time of writing, where the anchor wraps across three lines and the replacement is re-wrapped to the entry's width), replace ", and a consumer configured for `sigstore-keyless` refuses it with `materialize.signature_invalid`." with "; `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`." The wording matches DOC-1(g), which makes the same change to `docs/deployment/operator-guide.md:213`, so the unreleased notes do not describe a refusal that the `### Removed` entry above makes impossible.

## Open questions

No question is open. The Decisions section records the resolutions of the former OQ-1 (`podium-mcp` drops `sigstore-keyless`), OQ-2 (`trusted_root.json` as the trust input), and OQ-3 (an inclusion proof to a signed checkpoint, with the attested time from a timestamp authority).

## Non-goals

- Adopting sigstore-go or any other Sigstore library, the protobuf Sigstore bundle format, or interoperability with cosign bundles.
- Rekor v1 entries, SET verification, witness cosignatures, and consistency proofs.
- Fetching `trusted_root.json` or verifying TUF metadata. The operator supplies the file.
- Reading the `ctlogs`, `mediaType`, or `tlogs[].logId` members of `trusted_root.json`. No SCT is verified, and each trusted log key is tried against the checkpoint signature.
- Certificate Transparency SCT verification of the Fulcio leaf.
- A regular-expression identity variable. The comma-separated list covers several signers, and a regular-expression form can be added later.
- An identity policy or a Rekor key requirement on `podium-mcp`. It accepts no `sigstore-keyless` provider (SPEC-3, CODE-5).
- Removing `noop` from the values `podium-mcp` accepts, or matching provider names case-insensitively as `podium sign` does (`cmd/podium/cli_helpers_test.go:122`).
- A registry signing mode that mints keyless envelopes at ingest, or serving stored keyless or author-key signatures. Both stay as §4.7.9 and §4.7.10 state.
- Changing the registry-managed provider, the §4.7.10 delivery attestation, or the verification policy values.
- Making `podium verify` refuse to start on an unreadable trust root. §6.2 keeps the per-envelope refusal.
- Printing the §6.10 code from `podium sign` when `ErrSigstoreUnavailable` fires. The CLI prints that sentinel's message alone, because `spi.Error.Error()` returns only the message (`pkg/spi/errors.go:33`). The `registry-managed` arms of `loadSignatureProvider` prefix the code explicitly (`cmd/podium/sign.go:247`, `:257`) and keep doing so.
- Recording fixtures from any source other than the release-lane live smoke, and an `UPDATE_SIGSTORE_FIXTURES` generator.
- Wiring the live Sigstore smoke into CI. It stays manual and release-only per `RELEASING.md`.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-03, automated)

- **DOC-1(e) documented a code the keyless `podium sign` path never prints.** `ErrSigstoreUnavailable` prints its message alone (`pkg/spi/errors.go:33`, `cmd/podium/sign.go:62`), and only the `registry-managed` arms prefix `config.signature_provider_unavailable:` (`cmd/podium/sign.go:247`, `:257`). DOC-1(e) no longer adds the keyless sentence to the `config.signature_provider_unavailable` row and instead scopes the existing `podium sign` and `podium verify` sentence to the `registry-managed` provider. DOC-1(a) documents the keyless refusal by its printed message and scopes the cli.md "exits non-zero naming `config.signature_provider_unavailable`" sentence to `registry-managed`. The Non-goal no longer says "for every provider", and the Watch-out names the doc scoping.
- **The tampered-content-hash case expected a refusal the entry binding pre-empts, leaving the signature branch untested.** TEST-2 case 3 now expects `does not bind the digest` for a tampered content hash. TEST-1 gains `WithForeignSignature()`, which binds the same non-verifying signature in the envelope and the SET-covered body, and `WithEd25519Leaf()`, and case 3 gains rows for `signature does not verify`, `leaf is not ECDSA`, and a malformed content hash. CODE-3 step 7 states that a `splitContentHash` failure returns `content hash: <err>`, and step 9 marks its `decodeContentHash` error branch unreachable with a comment. The Coverage paragraph names the reached branches and the unreachable one.
- **The spec-named "block that does not parse" trust-root refusal had no test.** TEST-1 gains the `WithGarbageCertBlock()` and `WithGarbageKeyBlock()` root options, and TEST-2 case 3 gains a row for each, asserting `sign.ErrSignatureInvalid` and the substring `trust root block`. The Coverage paragraph names both.

### Pass 2 (2026-10-03, automated)

- **The configured OIDC issuer was trimmed in CODE-1 but specified as compared byte for byte with no whitespace removal.** The trimming is kept, matching the identity list's convention in the same table. The SPEC-2(b) `PODIUM_SIGSTORE_CERT_OIDC_ISSUER` row now states that surrounding whitespace is removed from the value and that a value empty after the removal counts as unset. DOC-1(c) states the same removal and the blank case. CODE-1 names `strings.TrimSpace` for `issuer` and the resulting `Validate` refusal. `TestNewIdentityPolicy_ParsesList` gains a padded-issuer case and a whitespace-only-issuer case, `TestIdentityPolicy_Validate` gains the whitespace-only issuer, and TEST-2 case 1 verifies under a padded issuer.
- **Byte-for-byte identity comparison and multi-SAN matching had no test.** `TestIdentityPolicy_Match` gains a letter-case row (`Alice@acme.com` under `alice@acme.com`, refused), rows for a leaf whose email SAN is outside the list and URI SAN is in it and the reverse (both accepted), and a row where both SANs are outside the list (refused). TEST-1 states that `WithSAN` replaces the email SAN and `WithURISAN` adds a URI SAN, so the two combine. TEST-2 case 1 adds an envelope whose URI SAN alone matches, and case 3 adds the letter-case row and the both-outside row. The edge-case rows name the tests that pin them.

### Pass 3 (2026-10-03, automated)

- **SPEC-1's code-signing-usage acceptance condition had no refusal test and no harness option.** The TEST-1 `Envelope` default leaf now states that it carries the code-signing extended key usage, and TEST-1 gains `WithoutCodeSigning()`, which issues a leaf with the server-authentication usage only and binds it in the hashedrekord body and the SET as usual. TEST-2 case 3 gains a `WithoutCodeSigning` row asserting `sign.ErrSignatureInvalid` and the substring `cert chain`, which CODE-3 step 8 returns for every `leaf.Verify` error other than `x509.Expired` (`pkg/sign/sigstore.go:183` already passes `KeyUsages` code signing). An implementation that drops the `KeyUsages` option or widens it to `x509.ExtKeyUsageAny` now fails that row. The Coverage paragraph names the row.

### Redesign 1 (2026-10-03, automated)

The redesign at `tmp/redesign/0043-bind-sigstore-keyless-verification-to-a-configured-signer-id-redesign-1.md` reconciled parallel specifications of the mechanisms that kept changing across review rounds. Every edit in its ordered list applied against the current text, and none was skipped.

- **Log proof and attested time.** The SET model is replaced. Rekor v2 serves no SET, and a log-key holder could forge a SET for an entry never appended, which the proposal had accepted. The envelope now carries a `tlog` object (`log_index`, `body`, `hashes`, and `checkpoint`) and an RFC 3161 `timestamp`. `Verify` checks the timestamp under a trusted timestamp authority and takes its `genTime` as the attested time, recomputes the RFC 6962 inclusion proof, verifies the checkpoint signature under each trusted ECDSA P-256 or Ed25519 log key, binds a hashedrekord v0.0.2 entry, and runs the chain check at the attested time. `Sign` requires Fulcio, Rekor v2, and a timestamp authority. `PODIUM_SIGSTORE_FULCIO_URL`, `PODIUM_SIGSTORE_REKOR_URL`, and the new `PODIUM_SIGSTORE_TSA_URL` gain §6.2 rows with public-good defaults applied in `cmd/podium`. One recorded staging fixture under `pkg/sign/testdata/sigstore-staging/` pins the wire formats the in-process generator cannot check independently, and S4 and S9 merge together.
- **Trusted-root input.** `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` is removed with no alias and replaced by `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE`, a Sigstore `trusted_root.json`. A PEM file cannot carry timestamp authorities or validity windows, and one PEM pool would mix timestamp-authority roots with Fulcio roots. The parser lives in `pkg/sign/trusted_root.go`, honors every `validFor` window at the attested time, and skips log keys of unsupported types so Sigstore's own file parses.
- **`podium-mcp` signature providers.** `podium-mcp` no longer accepts `sigstore-keyless`. Every delivery signature it verifies is registry-managed, so the provider could verify no load. `resolveVerifier` refuses the value with `config.invalid` under every policy before reading any material, and `buildSignatureProvider` is deleted. A new SPEC-3 edits §6.2, §6.9, and §9.1, and the `cmd/podium-mcp` test edits move into S6 because the package's tests do not compile between the deletion and those edits.

The redesign deleted the SET, `integrated_time`, `log_id`, `verifySET`, the canonical-JSON SET watch-out, log-ID lookup and its uppercase case, the hashedrekord v0.0.1 binding with `rekorRecord` and the Rekor v1 response types, the PEM trust-root convention and its `trust root block` refusals, the CODE-5 delivery-signature short-circuit, the bridge trust-root start check, the §6.9 `sigstore-keyless` clause, the planned bridge integration tests, `TestBuildSignatureProvider`, `TestDeliverLoadArtifact_SigstoreKeylessRefusesADeliveryEnvelope` (whose §4.7.10 half moves to TEST-2 case 10), `TestVerifySET`, the uppercase-log-ID and SET-refusal tests, the bridge watch-outs, the matching edge-case rows, the `podium-mcp` changelog `Fixed` bullet, OQ-1 to OQ-3, and the Non-goals for inclusion proofs, Rekor v2, timestamp authorities, reading `trusted_root.json`, and the signing endpoints in §6.2.

Reconciliation beyond the listed edits: the Summary names the trusted root and the signing endpoints in the CODE-4 bullet; the harness watch-out names the new package-`sign` test files; S3 depends on S2, because the harness writes the SPEC-2 file format; S8 carries the `TestLoadSignatureProvider` unit table at the unit and e2e levels; the S9 levels state that the recorded-fixture test runs by default; the CODE-4 and TEST-5 headings name their new scope; and the MV-1 hash input names S83. The proposal has no files-touched section, so the file list lives in the CODE, TEST, and DOC items.

The redesign recorded these open decisions with defaults:

- **D1, the log-proof model.** The default is Rekor v2 with an inclusion proof, a signed checkpoint, and an RFC 3161 attested time. The alternative keeps a Rekor v1 SET and its integrated time, which needs no timestamp authority but keeps the forged-SET acceptance and stops working when the public-good Rekor v1 log stops accepting writes.
- **D2, the recorded staging fixture as a merge gate.** The default keeps the fixture, so S4 merges only after a credentialed staging run records it with a non-personal OIDC identity. The alternative relies on the manual live smoke before release.
- **D3, the Rekor v2 shard default.** The default hard-codes `https://log2025-1.rekor.sigstore.dev`, confirmed against the public-good `signing_config` when SPEC-2 lands. The alternative gives `PODIUM_SIGSTORE_REKOR_URL` no default and refuses signing when it is unset.

The redesign also records gaps it leaves open. `Sign` makes its outbound calls with no deadline, although `.claude/rules/code-best-practices.md` requires a timeout on every outbound call. Proposal 0041 (b) edits the §6.2 sentence SPEC-2(a) removes, so whichever proposal lands second rebases. The proposal needs another adversarial review before sign-off, because the Status line predates this redesign.

### Pass 4 (2026-10-03, automated)

- **The extended-key-usage conditions relied on `x509.VerifyOptions.KeyUsages`, which admits a certificate with no extended-key-usage extension or with only `x509.ExtKeyUsageAny`.** Go skips such certificates in `checkChainForKeyUsage` (`$GOROOT/src/crypto/x509/verify.go:995-1003` in Go 1.26), and `pkg/sign/sigstore.go:183` relies on `KeyUsages` alone today. CODE-3 step 5 now requires the timestamp signer certificate's `ExtKeyUsage` to contain `x509.ExtKeyUsageTimeStamping` after the chain verifies, refusing with `timestamp authority not trusted at <time>: signer certificate lacks the time-stamping usage`, and step 9 requires `leaf.ExtKeyUsage` to contain `x509.ExtKeyUsageCodeSigning` after `leaf.Verify`, refusing with `leaf lacks the code-signing usage`. SPEC-1 states that a certificate with no extended key usage, or with only the any-purpose usage, lists neither usage. TEST-1 defines `WithTSALeafWithoutTimeStamping()` as an EKU-less timestamp-authority leaf and adds `WithTSALeafAnyUsage()`, `WithLeafWithoutEKU()`, and `WithLeafAnyUsage()`. TEST-2 case 3 asserts the new messages for all four. DOC-1(b), the edge-case table, the Coverage paragraph, and a new Summary watch-out carry the same predicate.
- **`uploadRekor`'s rule that an absent `logIndex` means 0 had no test.** TEST-1's `FakeServer` gains `WithRekorLogIndexOmitted()`, which places the entry at index 0 and omits `logIndex` from the protojson response. TEST-2 gains case 6a, `TestSigstoreKeyless_SignRecordsAbsentLogIndexAsZero`, which asserts that `Sign` succeeds, that `tlog.log_index` is 0, and that `Verify` accepts the envelope. The CODE-2 bullet, the Rekor v2 watch-out, and the Coverage paragraph name the case.

### Pass 5 (2026-10-03, automated)

- **The lower bound of the inclusion-index check had no test, although a negative `log_index` is the C27 bypass.** `log_index` stays outside the signed bytes, and `pkg/sign/sigstore.go:204` skips the log check today when it is negative. Only the `0 <=` half of the CODE-3 step 6 predicate `0 <= log_index < tree size` refuses `-1` after this change. TEST-2 case 3 gains a `WithLogIndex(-1)` row expecting `inclusion proof does not verify`, `TestVerifyInclusion` gains an index of -1 beside the at-or-past-tree-size case, and the edge-case table gains a negative-`log_index` row naming both tests.

### Pass 6 (2026-10-03, automated)

- **No test pinned the separation of the certificate-authority and timestamp-authority trust roles.** `WithForeignCA` and `WithUntrustedTSA` use authorities that issued nothing in the envelope, so an implementation that merged `certificateAuthorities` and `timestampAuthorities` into one pool for the CODE-3 step 9 leaf check or the step 5 signer check passed every listed test. TEST-1 now states that neither harness root carries an extended-key-usage extension, and adds `WithLeafIssuedByTSARoot()` (a code-signing leaf issued by the timestamp-authority root) and `WithTimestampSignedByFulcioCA()` (a token signed by a time-stamping certificate the Fulcio root issued). The Fulcio root rather than the intermediate issues that certificate, because the intermediate's code-signing usage would make `checkChainForKeyUsage` (`$GOROOT/src/crypto/x509/verify.go:1009-1024` in Go 1.26) refuse the chain under a merged pool too. TEST-2 case 3 cites `// Spec: §4.7.9, §6.2` and gains rows asserting `cert chain` and `timestamp authority not trusted`. The Summary trusted-root decision names both pools and the pinning rows, and the edge-case table gains a cross-role row.
- **Correction: the `checkChainForKeyUsage` citation for the Fulcio-intermediate refusal pointed at the skip branches.** `$GOROOT/src/crypto/x509/verify.go:991-1003` in Go 1.26.3 holds the start of the chain walk and the branches that skip a certificate with no extended key usage or with `x509.ExtKeyUsageAny`, so the range shows a certificate being admitted. The TEST-1 `WithTimestampSignedByFulcioCA()` bullet and the resolution bullet above now cite `verify.go:1009-1024`, the `NextRequestedUsage` loop that crosses out a requested usage the certificate does not list and returns false once every requested usage is crossed out. The `verify.go:995-1003` citations elsewhere describe the skip and are unchanged.

### Implementation S4 (2026-10-03, automated)

- **A Fulcio otherName-only leaf is refused at the chain check rather than at the identity match.** Fulcio marks the SAN extension critical, and Go's x509 parser records a critical SAN that holds no email, DNS, IP, or URI name as an unhandled critical extension, so `leaf.Verify` at CODE-3 step 9 refuses the leaf with `cert chain: x509: unhandled critical extension` and the step 11 identity match never runs. The refusal holds either way. The Summary otherName decision, the edge-case row, DOC-1(c), TEST-1, and TEST-2 case 3 now state that a Fulcio otherName-only leaf is refused with `cert chain` and that `certificate identity mismatch` applies to a non-critical otherName-only SAN. TEST-1 gains `WithNonCriticalSAN()`, and TEST-2 case 3 carries a row for each form. `verifyLeafChain` is unchanged, because tolerating the unhandled critical extension would weaken the chain check.
