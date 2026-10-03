# Proposal 0038: Replace the SDK DeviceCodeRequired contract with a non-blocking start_login/finish_login pair in both SDKs

- Issue: (to be filed)
- Status: Implemented (2026-10-02). Signed off as staged. OQ-1: keep the DeviceCodeError.reason attribute as drafted.
- Date: 2026-10-02

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §6.3: the SDK sub-bullet of `oauth-device-code` stops naming `DeviceCodeRequired` and states the non-blocking pair, its single-use handle, its expiry and timeout bounds, its cancellation input, and the `DeviceCodeError.reason` values. The parent bullet scopes the first-use trigger, keychain caching, and transparent refresh to the CLI and the MCP server, and states that the SDK runs the flow only when the calling code invokes it and holds its token in memory (SPEC-1).
- Python SDK: `sdks/podium-py/podium/_oauth.py` anchors code expiry at the start call, gains a cancellation input, a `reason` on `DeviceCodeError`, and the `PendingLogin` handle (CODE-1). `sdks/podium-py/podium/client.py` gains `Client.start_login()` and `Client.finish_login()`, reimplements `Client.login()` on top of them, and removes `DeviceCodeRequired` (CODE-2).
- TypeScript SDK: `sdks/podium-ts/src/oauth.ts` gains the same mechanics with an `AbortSignal` and a module-private handle state (CODE-3). `sdks/podium-ts/src/index.ts` gains `client.startLogin()` and `client.finishLogin()` and reimplements `client.login()` on top of them (CODE-4).
- Tests: the Python and TypeScript SDK suites pin every outcome of the pair against their stub IdPs, and the Go import smoke test stops importing `DeviceCodeRequired` (TEST-1, TEST-2, TEST-3).
- Docs, changelog, and manual validation follow: the SDK consumer guide, the `auth.token_expired` row of the error-code reference, the Python SDK README, the `[Unreleased]` changelog entry, and scenario S78 (DOC-1, DOC-2, CL-1, MV-1).

**Fixed decisions.**

- Option (c) is settled (user decision, 2026-10-02). `Client.login()` stays blocking with its signature unchanged in both SDKs, and both SDKs gain the pair. Option (a), raising on every tokenless call, is rejected.
- `DeviceCodeRequired` is removed from the spec and from `podium-py` with no shim. `pkg/identity`'s `ErrDeviceCodeRequired` is a separate MCP-side Go sentinel and stays.
- Names: Python `Client.start_login(...) -> PendingLogin` and `Client.finish_login(pending, *, timeout=..., cancel=None, sleep=None) -> Tokens`; TypeScript `startLogin(opts?) -> Promise<PendingLogin>` and `finishLogin(pending, opts?) -> Promise<Tokens>`. `PendingLogin` is exported from the package root of both SDKs.
- Public handle fields: Python `verification_uri`, `verification_uri_complete` (`None` when the IdP omits it), `user_code`, `expires_in`, and `interval`; TypeScript `verificationUri`, `verificationUriComplete` (`undefined` when omitted), `userCode`, `expiresInMs`, and `intervalMs`. The device code is never public.
- Code expiry runs from the start call. The timeout runs from the finish call. Polling ends at the earlier bound, with reason `expired` or `timeout` respectively.
- A handle is single-use. The first finish call consumes it at entry, before any I/O, whatever the outcome. A later finish call raises reason `consumed` and sends no token request.
- Entry order of a finish call: consumed check, mark consumed, cancelled check, local expiry check, then polling. TypeScript first rejects a handle with no registered state as reason `failed` and sends no request. Python guards the consumed check-and-set with a `threading.Lock`, and TypeScript sets the flag before its first `await`. The poll loop re-checks cancelled, expired, and timeout after every wait and before every token request.
- `DeviceCodeError.reason` takes exactly `denied`, `expired`, `timeout`, `cancelled`, `consumed`, or `failed`. The values are SDK-local and are not §6.10 error codes. Existing messages are unchanged. OQ-1 asks whether the reviewer prefers subclasses; the draft stages the `reason` attribute.
- The start call takes the configuration parameters `login()` takes, plus Python's `clock` for tests. One private helper per SDK (`_resolve_device_flow`, `resolveDeviceFlow`) resolves the `PODIUM_OAUTH_*` fallbacks and discovery for both the start call and `login()`.
- Cancellation is the only new input to the finish call: Python takes `cancel: threading.Event | None`, and TypeScript takes `signal?: AbortSignal`. Python cancellation takes effect at the next check and does not interrupt an in-flight `urllib` request. The TypeScript sleep, `fetch`, and response-body read all observe the signal, and a cancelled finish sets `cause` to `signal.reason`.
- Python's pair is synchronous and TypeScript's is async. Neither SDK gains an asyncio or a synchronous variant.
- The finish call installs the access token on the client it is called on. The token is held in memory for the life of the client and is neither persisted nor refreshed. A handle belongs to the client whose start call produced it; finishing it on another client is unsupported and documented, with no runtime guard.
- `start_login`/`startLogin` prints nothing, opens no browser, and does not poll. `login()` prints `Visit: <uri>` and `User code: <code>` to stderr, and only Python's `login(open_browser=True)` opens a browser.
- §7.6 and §14.8 are not edited. The proposal adds no environment variable, flag, §6.10 error code, endpoint, SPI, or Go product code.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each §6.3 edit by its quoted current text.
- **Removing `DeviceCodeRequired` breaks the Go end-to-end suite immediately.** `TestSDK_PyImport` (`test/e2e/sdk_clients_test.go`) imports it, so CODE-2 and TEST-3 land in one commit (step S3).
- **A `grep DeviceCodeRequired` still hits `pkg/identity` after this change.** `ErrDeviceCodeRequired` (`pkg/identity/identity.go`) is the MCP-side sentinel and stays. Historical proposals 0012 and 0013 also mention the SDK class and are not edited.
- **TypeScript `#private` fields cannot carry the handle state.** `Client.finishLogin` lives in `index.ts` and cannot read `#private` fields of a class declared in `oauth.ts`. CODE-3 keeps the state in a module-scoped `WeakMap` inside `oauth.ts`, and the polling runs there. Do not add a public `consume()` or `finish()` method to `PendingLogin`: `package.json` has no `exports` map, so every module under `src/` is importable, and a public method would let a caller burn a handle.
- **Python discovery does not raise `DeviceCodeError` today.** `discover_idp` (`sdks/podium-py/podium/_oauth.py`) lets a `urllib.error.HTTPError` or `URLError` escape, and `_post_form` wraps only `HTTPError`. CODE-1 wraps transport and decode failures as reason `failed`, and TEST-1 cases 15, 21, and 22 assert it.
- **`login()` changes one observable outcome.** Today `poll` uses one deadline of `min(timeout, expires_in)` and reports only `login timed out`. When an IdP returns an `expires_in` shorter than the timeout, `login()` now raises reason `expired` with `device code expired before the flow completed`. The changelog must not call `login()` unchanged.
- **The Python fixture polls with interval 0.** `cancel.wait(0)` returns at once, so a test that sets the `Event` from another thread is racy. TEST-1 drives cancellation through an injected `sleep`.
- **The SDK suites read `PODIUM_OAUTH_*` from the environment.** An operator shell can point a test at a real IdP. The new Python tests clear those variables, and the TypeScript tests stub them.
- **Fake timers must be installed before `startLogin`.** The TypeScript handle captures `Date.now` at initiation, and a reference captured before `vi.useFakeTimers()` keeps the real clock. No test under `sdks/podium-ts/src/` uses fake timers yet.
- **Only a `Spec:` comment directly above a test line is machine-checked.** `make speccov-drift` runs `./bin/speccov drift` (`Makefile:257-258`), and `tools/speccov/main.go:72` reads tests through `specparser.WalkTests`, which walks the whole repository except `vendor`, `node_modules`, `.git`, `testdata`, and `tmp` (`tools/internal/specparser/specparser.go:170-176`). It parses Python `test_*.py` and `*_test.py` files and TypeScript `*.test.ts` files (`specparser.go:194-201`) and reads the contiguous comment block immediately above each `def test_*` line or `it(`/`test(` call (`specparser.go:240-241`, `:301-319`). A lowercase `# spec §6.3` comment inside a test body is not read, a decorator line between the comment and `def` breaks the block, and a comment above a `describe` call attaches to no test. TEST-1 and TEST-2 therefore place the annotation directly above each test line, as `sdks/podium-py/tests/test_client.py:67` and `sdks/podium-ts/src/index.test.ts:13` do, and TEST-1, TEST-2, and TEST-3 all count toward `speccov-drift`.
- **`.claude/rules/test-coverage.md` calls the TypeScript suite "the Node test runner".** The suite runs on vitest (`sdks/podium-ts/package.json`). New TypeScript tests join the vitest suite; this proposal does not edit the rule.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §6.3 replaces the `DeviceCodeRequired` sub-bullet with the pair contract and scopes the first-use trigger, keychain caching, and transparent refresh to the CLI and the MCP server.
      Levels: —. Depends on: —
- [x] **S2 · code** — CODE-1. Python `_oauth` gains issue-time anchoring, cancellation, `DeviceCodeError.reason`, transport wrapping, and `PendingLogin`.
      Levels: unit, integration. Depends on: S1
- [x] **S3 · code** — CODE-2, TEST-3. Python `Client.start_login`/`finish_login` land, `login()` composes them, `DeviceCodeRequired` is removed, and `TestSDK_PyImport` imports the new names. Bundled because `TestSDK_PyImport` fails with `ImportError` the moment `DeviceCodeRequired` is removed.
      Levels: integration, e2e. Depends on: S2
- [x] **S4 · test** — TEST-1. Python SDK tests for every outcome of the pair against the stub IdP.
      Levels: integration. Depends on: S3
- [x] **S5 · code** — CODE-3. TypeScript `oauth.ts` gains issue-time anchoring, abortable sleep and fetch, `DeviceCodeErrorReason`, `PendingLogin`, `createPendingLogin`, and `finishPending`.
      Levels: unit, integration. Depends on: S1
- [x] **S6 · code** — CODE-4. TypeScript `startLogin`/`finishLogin` land on `Client`, and `login()` composes them.
      Levels: integration. Depends on: S5
- [x] **S7 · test** — TEST-2. TypeScript vitest tests for every outcome of the pair against the stub fetcher.
      Levels: integration. Depends on: S6
- [x] **S8 · docs** — DOC-1. The SDK consumer guide documents the non-blocking login, the anonymous requests before a finish call succeeds, and re-login on token expiry, and the `auth.token_expired` row of the error-code reference names the SDK remedy.
      Levels: —. Depends on: S3, S6
- [x] **S9 · docs** — DOC-2. The Python SDK README names the pair and cites §6.3.
      Levels: —. Depends on: S3
- [x] **S10 · docs** — CL-1. The `[Unreleased]` `Added` and `Removed` entries.
      Levels: —. Depends on: S3, S6
- [x] **S11 · docs** — MV-1. Manual-validation scenario S78 for the Python pair against a live IdP.
      Levels: manual. Depends on: S3

**Ordering constraints.** S1 lands the contract every later step cites. The Python steps (S2 to S4) and the TypeScript steps (S5 to S7) are independent of each other and may proceed in parallel. Within each SDK the oauth-module step precedes the client step, because the client delegates to it.

## Current state and the gap

§6.3 describes an SDK device-code API that does not exist. The SDK sub-bullet of `oauth-device-code` says "SDK raises `DeviceCodeRequired` with the URL and code; calling code is responsible for surfacing it to the user. `Client.login()` performs the same blocking poll-until-completion the CLI uses." No code raises `DeviceCodeRequired`. The Python SDK declares it in `sdks/podium-py/podium/client.py` with a stale docstring ("Stage 3 does not implement OAuth ... Phase 11") and re-exports it from `sdks/podium-py/podium/__init__.py`. Every device-flow failure in `sdks/podium-py/podium/_oauth.py` raises `DeviceCodeError`. The TypeScript SDK has no `DeviceCodeRequired` and throws only `DeviceCodeError` (`sdks/podium-ts/src/oauth.ts`). The only importer outside the package is `TestSDK_PyImport` in `test/e2e/sdk_clients_test.go`. `pkg/identity`'s `ErrDeviceCodeRequired` (`pkg/identity/identity.go`) is a separate MCP-side Go sentinel.

### The blocking login is the only entry point

`Client.login()` in Python (`client.py`) and TypeScript (`sdks/podium-ts/src/index.ts`) resolves the client ID, scopes, audience, and endpoints from arguments or the `PODIUM_OAUTH_*` variables, discovers the IdP when needed, calls `initiate`, prints `Visit:` and `User code:` to stderr, polls, and assigns the access token to the client in memory. Python `login()` opens the browser when `open_browser=True`. That flow suits terminal scripts. Notebooks, GUIs, and web apps need the URL and code in their own interface and receive no handle to do so.

### The existing steps and their gaps

Both SDKs already split the flow into `initiate` and `poll`, and `poll` handles `authorization_pending`, `slow_down` (+5 s), `expired_token`, and `access_denied`. Five gaps separate those steps from the decided contract.

1. `poll` computes its deadline when polling starts (`deadline = now + min(timeout, expires_in)`), and `DeviceAuth` records no issue time. A late finish call measures code expiry from the finish call.
2. Neither `poll` can be cancelled. Python takes no `Event`. TypeScript takes no `AbortSignal`, and its `setTimeout` sleep cannot be aborted.
3. Nothing marks a flow as used. A second poll of the same device code posts the grant again and reports an IdP error instead of caller misuse.
4. `verification_uri_complete` defaults to `""` when the IdP omits it, so it is never absent.
5. `DeviceCodeError` carries only a message, so a caller cannot tell a denial from an expiry, a timeout, a cancellation, or a reuse without matching strings.

Python discovery also leaks transport exceptions: `discover_idp` lets `urllib.error.HTTPError` and `URLError` escape, and `_post_form` wraps only `HTTPError`.

### The spec contradicts the SDK's in-memory token

The parent `oauth-device-code` bullet of §6.3 says tokens are "cached in the OS keychain" and "Refreshes transparently". Neither SDK has keychain code, and each parses `refresh_token` and never uses it. Keychain storage exists only in `pkg/identity/keychain.go` and `cmd/podium`. `docs/consuming/custom-via-sdk.md` already says the SDK does not persist the token. §7.6 states that "Identity providers ... are all the same as in the MCP path", which reads correctly once §6.3 states storage per consumer. §14.8 shows `client.login()`, which this proposal keeps.

## Decisions

- **Option (c) is settled.** The user chose it on 2026-10-02, and it is not reopened. Option (a) was rejected because a tokenless client sends no `Authorization` header, the default `identity_provider` value `oauth-device-code` is stored and never acted on, and §6.3.3 treats a credential-less request as anonymous. Raising on every tokenless call would break anonymous SDK use.
- **Names follow each SDK's conventions.** Python uses snake_case methods and second-valued floats. TypeScript uses camelCase and millisecond fields, matching the existing `DeviceAuth.intervalMs` and `expiresInMs`.
- **The handle exposes only what a UI shows or schedules on.** The device code, the token endpoint, the client ID, the transport, the clock, the issue time, and the consumed flag are private. The device code is a polling credential and no caller displays it, so Python's `PendingLogin.__repr__` omits it.
- **The start call takes the configuration parameters `login()` takes, and only those.** Python: `client_id`, `scopes`, `audience`, `device_authorization_endpoint`, `token_endpoint`, `opener`, plus `clock` for tests. TypeScript: `clientID`, `scopes`, `audience`, `deviceAuthorizationEndpoint`, and `tokenEndpoint`. The resolution with its `PODIUM_OAUTH_*` fallbacks and discovery moves into one private helper per SDK (`_resolve_device_flow`, `resolveDeviceFlow`) that both `login()` and the start call use.
- **Code expiry is measured from the start call.** The handle records its issue time from the injected clock (`time.monotonic` in Python, `Date.now` in TypeScript) before the device-authorization request, so the lifetime is never over-counted. A finish call on a handle whose lifetime has elapsed fails locally with reason `expired`. An IdP `expired_token` reply also maps to `expired`.
- **The timeout is measured from the finish call.** It bounds the caller's wait. The defaults are the existing `_oauth.DEFAULT_TIMEOUT` (600 s) and `DEFAULT_TIMEOUT_MS` (600 000 ms). Polling ends at the earlier of finish-start + timeout and issued-at + lifetime.
- **Cancellation is the only new input.** Python takes `cancel: threading.Event | None`. When no `sleep` is injected and `cancel` is set, the wait between token requests is `cancel.wait(interval)`. Cancellation takes effect at the next check and does not interrupt an in-flight `urllib` request. TypeScript takes `signal?: AbortSignal`, and the inter-poll sleep, the in-flight `fetch`, and the read of its response body all observe it. A cancelled finish raises reason `cancelled`; TypeScript sets `cause` to `signal.reason`.
- **No second variant.** Python's pair is synchronous and blocks the calling thread; another thread cancels it by setting the `Event`. TypeScript's pair is async. Neither SDK gains an asyncio or a synchronous variant.
- **The handle is single-use.** The first finish call consumes it at entry, before any I/O. Python guards the check-and-set with a `threading.Lock` on the handle, so of two concurrent finish calls exactly one proceeds. TypeScript sets the flag synchronously before the first `await`.
- **`DeviceCodeError.reason` is the typed failure.** The single-use rule needs a distinct reuse error, and a UI needs to tell a denial from an expiry without matching messages (code-best-practices: typed errors when callers branch). `failed` covers discovery, device authorization, transport, and unrecognized IdP errors. Python wraps `urllib.error.URLError` (including `HTTPError` outside `_post_form`'s JSON path), `OSError`, and `json.JSONDecodeError` from discovery and form posts as `failed` and chains the original with `from`. TypeScript wraps a `fetch` rejection that is not a cancellation, and a non-JSON discovery body, as `failed` with `cause`. The values never reach the registry, so `matrix-audit` is unaffected. OQ-1 asks whether the reviewer prefers subclasses.
- **The finish call installs the token on its own client.** It sets `self.token` / `this.token` and returns `Tokens`. A handle belongs to the client whose start call produced it, because it polls that client's resolved IdP. Finishing it on another client is unsupported and documented as such; no runtime guard is added, because the spec states no such rule.
- **`login()` composes the pair.** It calls the start call, prints the two stderr lines, opens `verification_uri_complete` when Python's `open_browser=True` and the value is not `None`, and calls the finish call with its `timeout` and `sleep`. Its signature, stderr output, and browser behavior are unchanged. Its failures now carry `reason`, and an IdP lifetime shorter than the timeout now ends with reason `expired`.
- **SPEC-2 is dropped.** §7.6 is not edited. "The cache" in §7.6 is the content cache defined in §7.4, and "Identity providers" points at §6.3, which SPEC-1 makes correct per consumer (see Non-goals).

## Spec amendment: §6.3 oauth-device-code SDK contract

**SPEC-1.** Two anchors in `spec/06-mcp-server.md`, §6.3 "Identity Providers". Both edits land in one commit.

(a) The `oauth-device-code` bullet (line 48 at the time of writing). Replace the opening of the bullet:

> **`oauth-device-code`** _(default)_. Interactive device-code flow on first use; tokens cached in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux). Refreshes transparently.

with:

> **`oauth-device-code`** _(default)_. Interactive device-code flow. The MCP server and the CLI run it on first use, cache tokens in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux), and refresh them transparently. The SDK runs the flow only when the calling code invokes it and holds its token in memory, as the SDK entry below states.

The rest of the bullet, from "Defaults: access-token TTL 15 min" through the `Options:` list, is unchanged.

(b) The SDK sub-bullet under "How the verification URL surfaces depends on the consumer:" (line 53 at the time of writing). Replace:

> **SDK** raises `DeviceCodeRequired` with the URL and code; calling code is responsible for surfacing it to the user. `Client.login()` performs the same blocking poll-until-completion the CLI uses.

with:

> **SDK** exposes the flow as a pair of calls. `start_login()` (Python) and `startLogin()` (TypeScript) perform the device-authorization request and return a pending-login handle that carries the verification URI, the complete verification URI when the IdP supplies one, the user code, the code's lifetime, and the initial poll interval. The start call prints nothing, opens no browser, and does not poll; calling code is responsible for surfacing the URL and code to the user. `finish_login(handle)` (Python) and `finishLogin(handle)` (TypeScript) poll the token endpoint until the user completes the flow, the IdP denies it, the code expires, the caller cancels, or the timeout elapses. The timeout defaults to 10 minutes and runs from the finish call. The code's lifetime runs from the start call, and a finish call on a handle whose code has expired fails without contacting the IdP. Python cancels a finish call through a `threading.Event`, and TypeScript through an `AbortSignal`. A handle is single-use: the first finish call consumes it whatever its outcome, and a later finish call on the same handle fails without contacting the IdP. Every failure surfaces as a `DeviceCodeError`, raised in Python and as the rejection of the returned Promise in TypeScript, whose `reason` is `denied`, `expired`, `timeout`, `cancelled`, `consumed`, or `failed`. The `failed` reason covers discovery, device-authorization, transport, and unrecognized IdP errors. These reasons are local to the SDK and are not §6.10 error codes. On success the finish call installs the access token on the client it was called on and returns the tokens. The SDK holds the token in memory for the life of the client and neither persists nor refreshes it; a client with no token sends anonymous requests (§6.3.3). `Client.login()` composes the two calls: it prints the URL and code to stderr, opens the complete verification URI in the system browser only when the Python caller passes `open_browser=True`, and blocks until the flow completes, is denied, expires, or times out.

The MCP server and CLI sub-bullets are unchanged.

## Proposed solution

### CODE-1. Python `_oauth`: anchoring, cancellation, reasons, and `PendingLogin`

`sdks/podium-py/podium/_oauth.py`.

- `DeviceCodeError.__init__(self, message: str, reason: str = "failed")` stores `self.reason`. Define the values as a `Literal` alias `DeviceCodeErrorReason` and module constants. Each existing raise passes its reason: the `expired_token` branch passes `expired`, the `access_denied` branch passes `denied`, `login timed out` passes `timeout`, and discovery, `initiate`, `_post_form`, and the unrecognized-error branch keep `failed`. Messages are unchanged.
- `discover_idp` and `_post_form` wrap `urllib.error.URLError`, `OSError`, and `json.JSONDecodeError` as `DeviceCodeError(..., "failed")` with `raise ... from exc`. `_post_form` keeps its existing parsing of an `HTTPError` JSON body for RFC 8628 §3.5 replies.
- `DeviceAuth` gains `issued_at: float`. `initiate(..., clock=time.monotonic)` sets `issued_at = clock()` before the POST.
- `poll(token_url, client_id, auth, *, timeout, opener, sleep=None, clock=time.monotonic, cancel: threading.Event | None = None)`: `timeout_at = clock() + timeout`; `expires_at = auth.issued_at + auth.expires_in`. A `_check()` helper raises, in order, `DeviceCodeError("login cancelled", "cancelled")` when `cancel` is set, `DeviceCodeError("device code expired before the flow completed", "expired")` when `clock() >= expires_at`, and `DeviceCodeError("login timed out", "timeout")` when `clock() >= timeout_at`. Each iteration runs `_check()`, waits (the injected `sleep` when given; else `cancel.wait(interval)` when `cancel` is not `None`; else `time.sleep`), runs `_check()` again, and only then posts the grant with the existing branch handling.
- New class `PendingLogin` with read-only properties `verification_uri`, `verification_uri_complete` (`None` when `DeviceAuth.verification_uri_complete == ""`), `user_code`, `expires_in`, and `interval`, and private attributes `_auth`, `_token_url`, `_client_id`, `_opener`, `_clock`, `_consumed`, and `_lock` (`threading.Lock`). `_consume()` raises `DeviceCodeError("login handle already finished", "consumed")` when already consumed, and otherwise sets the flag, under the lock. `__repr__` omits the device code.
- The module docstring describes the pair and `login()` as their composition and cites `spec §6.3`. The `DEFAULT_TIMEOUT` comment cites §6.3 alongside §7.7.

**IMPLEMENTOR'S CHOICE:** the `PendingLogin` constructor signature. It is not documented as public API, it accepts the resolved `DeviceAuth`, token URL, client ID, opener, and clock, and nothing it stores appears in `repr()` or a public attribute except the five display fields.

### CODE-2. Python `Client`: the pair, `login()`, and removal of `DeviceCodeRequired`

`sdks/podium-py/podium/client.py` and `sdks/podium-py/podium/__init__.py`.

- Delete `DeviceCodeRequired` from `client.py`, and its import and `__all__` entry from `__init__.py`. Export `PendingLogin` from `__init__.py` beside `DeviceCodeError` and `Tokens`.
- Add `import threading` to `client.py` (the file uses `from __future__ import annotations`, so a `TYPE_CHECKING` import is also acceptable) for the `cancel` annotation.
- Extract `_resolve_device_flow(client_id, scopes, audience, device_authorization_endpoint, token_endpoint, opener) -> (device_url, token_url, client_id, scopes, audience)` from the body of `login()`, with the existing env-var fallbacks, discovery, and `/oauth2/token` fallback.
- `start_login(self, *, client_id=None, scopes=None, audience="", device_authorization_endpoint="", token_endpoint="", opener=None, clock=time.monotonic) -> PendingLogin`: resolves the flow, calls `_oauth.initiate(..., opener=opener, clock=clock)`, and returns the handle. It writes nothing to stderr and opens no browser.
- `finish_login(self, pending, *, timeout=_oauth.DEFAULT_TIMEOUT, cancel=None, sleep=None) -> Tokens`: calls `pending._consume()`, then `_oauth.poll(pending._token_url, pending._client_id, pending._auth, timeout=timeout, opener=pending._opener, sleep=sleep, clock=pending._clock, cancel=cancel)`, sets `self.token = tokens.access_token`, and returns the tokens. The docstring states that a handle belongs to the client whose `start_login` produced it, that finishing it on another client is unsupported, and that the call cites `spec §6.3`.
- `login()` keeps its signature. Its body becomes: `p = self.start_login(...)`; print `Visit: {p.verification_uri}` and `User code: {p.user_code}` to stderr; when `open_browser and p.verification_uri_complete is not None`, call `_open_browser(p.verification_uri_complete)`; return `self.finish_login(p, timeout=timeout, sleep=sleep)`. Its docstring cites §6.3 and states that failures carry `DeviceCodeError.reason`, and that an IdP lifetime shorter than `timeout` ends with reason `expired`.

### CODE-3. TypeScript `oauth.ts`: anchoring, abort, reasons, and `PendingLogin`

`sdks/podium-ts/src/oauth.ts`.

- `export type DeviceCodeErrorReason = "denied" | "expired" | "timeout" | "cancelled" | "consumed" | "failed";`. `DeviceCodeError` gains `readonly reason: DeviceCodeErrorReason` and the constructor `(message: string, reason: DeviceCodeErrorReason = "failed", options?: { cause?: unknown })`, passing `options` to `super` (`engines.node >= 20` carries `Error` `cause`). Existing throw sites pass the reasons CODE-1 lists; messages are unchanged.
- `DeviceAuth` gains `issuedAtMs: number`. `initiate(deviceUrl, clientID, scopes, audience, fetcher, now = Date.now)` sets it before the POST. `discoverIdp` wraps a `resp.json()` failure and a `fetch` rejection as `failed` with `cause`.
- `postForm(url, form, fetcher, signal?)` passes `signal` to `fetch`. The same mapping applies to a rejection from `fetch` and to a rejection from `resp.json()`, because an abort that arrives after the response headers resolves `fetch` and then rejects the body read: when `signal?.aborted` is true, either rejection maps to `DeviceCodeError("login cancelled", "cancelled", { cause: signal.reason })`. Any other `fetch` rejection maps to `failed` with `cause`. Any other `resp.json()` failure keeps the existing message `HTTP <status>` (today's catch at `sdks/podium-ts/src/oauth.ts:66-70`) with reason `failed` and `cause` set to the body error.
- The sleep becomes `sleep(ms, signal?)`, which clears its timer and rejects with the `cancelled` error when the signal aborts.
- `poll(tokenUrl, clientID, auth, { timeoutMs, fetcher, now, signal })`: `timeoutAt = now() + timeoutMs`; `expiresAt = auth.issuedAtMs + auth.expiresInMs`. A `check()` helper throws, in order, `cancelled` (with `cause: signal.reason`) when `signal?.aborted`, `expired` when `now() >= expiresAt`, and `timeout` when `now() >= timeoutAt`. Each iteration runs `check()`, sleeps, runs `check()` again, and then posts the grant.
- `export class PendingLogin` with public `readonly` fields `verificationUri`, `verificationUriComplete?: string` (`undefined` when `DeviceAuth.verificationUriComplete === ""`), `userCode`, `expiresInMs`, and `intervalMs`, and no methods.
- A module-scoped `const pendingState = new WeakMap<PendingLogin, { auth: DeviceAuth; tokenUrl: string; clientID: string; fetcher: typeof fetch; now: () => number; consumed: boolean }>()`.
- `export function createPendingLogin(auth, tokenUrl, clientID, fetcher, now): PendingLogin` builds the handle and registers its state.
- `export async function finishPending(pending, opts: { timeoutMs?: number; signal?: AbortSignal } = {}): Promise<Tokens>` looks up the state. A handle with no state throws `DeviceCodeError("unknown login handle", "failed")` and sends no request. A consumed handle throws `DeviceCodeError("login handle already finished", "consumed")`. Otherwise it sets `consumed = true` before its first `await` and calls `poll` with `timeoutMs ?? DEFAULT_TIMEOUT_MS`, which runs the cancelled and expiry checks before any request.
- The header comment describes the pair and cites §6.3.

### CODE-4. TypeScript `Client`: the pair and `login()`

`sdks/podium-ts/src/index.ts`.

- Extract `private async resolveDeviceFlow(opts)` from the body of `login()`.
- `async startLogin(opts: { clientID?; scopes?; audience?; deviceAuthorizationEndpoint?; tokenEndpoint? } = {}): Promise<PendingLogin>` resolves the flow, calls `initiate(deviceUrl, clientID, scopes, audience, this.fetcher)`, and returns `createPendingLogin(auth, tokenUrl, clientID, this.fetcher, Date.now)`. It writes nothing to stderr.
- `async finishLogin(pending: PendingLogin, opts: { timeoutMs?: number; signal?: AbortSignal } = {}): Promise<Tokens>`: `const tokens = await finishPending(pending, opts); this.token = tokens.accessToken; return tokens;`. Its comment states the handle belongs to the client that started it.
- `login(opts)` keeps its signature: `const p = await this.startLogin(opts)`, writes `Visit: ${p.verificationUri}` and `User code: ${p.userCode}` to stderr, and returns `this.finishLogin(p, { timeoutMs: opts.timeoutMs })`.
- Re-export `PendingLogin`, `type DeviceCodeErrorReason`, `DeviceCodeError`, and `type Tokens` from `index.ts`. Do not re-export `createPendingLogin` or `finishPending`. Replace the `// spec §14.8 / §7.7` comment above `login` with one that cites §6.3 for the pair and states that `login()` composes it.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| IdP omits `verification_uri_complete` | Python `None`, TypeScript `undefined`; Python `login(open_browser=True)` opens nothing | §6.3 "the complete verification URI when the IdP supplies one" (SPEC-1); `docs/consuming/custom-via-sdk.md` (DOC-1) |
| Finish call after the code's lifetime elapsed | `DeviceCodeError` reason `expired`; no token request | §6.3 (SPEC-1); DOC-1 |
| Lifetime elapses during polling, before the timeout | Reason `expired`; no further token request | §6.3 "until ... the code expires" (SPEC-1); DOC-1 |
| Timeout elapses before the lifetime | Reason `timeout` | §6.3 "the timeout ... runs from the finish call" (SPEC-1); DOC-1 |
| `login()` with an IdP lifetime shorter than `timeout` | Reason `expired` with `device code expired before the flow completed`, where it previously said `login timed out` (accepted behavior change) | §6.3 (SPEC-1); `CHANGELOG.md` (CL-1) |
| IdP replies `expired_token` | Reason `expired` | §6.3 (SPEC-1); DOC-1 |
| IdP replies `access_denied` | Reason `denied` | §6.3 (SPEC-1); DOC-1 |
| IdP replies `slow_down` | Interval grows by 5 s; polling continues | RFC 8628 §3.5; existing behavior, no new text |
| IdP replies an unrecognized error, or discovery, device authorization, or transport fails | Reason `failed`; the original exception chained | §6.3 "The `failed` reason covers ..." (SPEC-1); DOC-1 |
| Second finish call on a handle, after any outcome | Reason `consumed`; no token request | §6.3 "A handle is single-use" (SPEC-1); DOC-1 |
| Two concurrent finish calls on one handle | Exactly one polls; the other raises `consumed` | §6.3 (SPEC-1); DOC-1 |
| `cancel` set or `signal` aborted before the finish call | Handle consumed; reason `cancelled`; no token request | §6.3 (SPEC-1); DOC-1 |
| Cancellation during a wait | Reason `cancelled`; no further token request | §6.3 (SPEC-1); DOC-1 |
| Python cancellation during an in-flight token request | The request completes; a success still installs the token, otherwise the next check raises `cancelled` (accepted) | §6.3 "Python cancels a finish call through a `threading.Event`" (SPEC-1); DOC-1 states that cancellation takes effect between requests |
| TypeScript abort during an in-flight token request, before the headers or while the body is read | `fetch` or `resp.json()` rejects; reason `cancelled` with `cause` set to `signal.reason` | §6.3 (SPEC-1); DOC-1 |
| Hung IdP connection with no cancellation | The Python `urllib` call and the TypeScript `fetch` carry no per-request timeout, so the finish call can outlast its timeout (accepted, deferred) | Non-goal; DOC-1 states that the timeout is checked between token requests |
| Handle finished on a client other than the one that started it | Polls the starting client's IdP and installs the token on the calling client (unsupported, accepted) | Docstrings (CODE-2, CODE-4); DOC-1 |
| TypeScript `PendingLogin` built with `new` instead of `startLogin` | Reason `failed`, message `unknown login handle`; no request | CODE-3; not documented, because the constructor is not a documented entry point |
| Client used before a finish call succeeds | Anonymous requests | §6.3.3 and §6.3 "a client with no token sends anonymous requests" (SPEC-1); DOC-1(b) |
| Process restart or a new `Client` | No token; the pair runs again | §6.3 "neither persists nor refreshes it" (SPEC-1); DOC-1 |
| Access token expires (15 min default TTL) | Requests fail with `auth.token_expired`; the SDK does not refresh, and the caller runs the pair or `login()` again (accepted) | §6.3 "neither persists nor refreshes it" (SPEC-1); DOC-1(b) and DOC-1(c) |
| Code that imports `podium.DeviceCodeRequired` | `ImportError` | §6.3 (SPEC-1); `CHANGELOG.md` `Removed` (CL-1) |

## Testing

**TEST-1 · integration, `sdks/podium-py/tests/test_resolution_overlay_login.py`.** Lands in S4. Each new or extended test carries `# Spec: §6.3` in the comment block directly above its `def test_*` line, below any decorator such as `@pytest.mark.parametrize`, so `speccov` reads it (see the Watch-out on machine-checked annotations).

- Generalize `_OAuthHandler` so a test scripts the `/token` replies as a list (`pending`, `slow_down`, `ok`, `expired_token`, `access_denied`, or an unknown error), toggles `verification_uri_complete` in the `/device` reply, can return 404 from discovery or `/device`, and can return 200 with a non-JSON body from `/device`. `token_polls` keeps counting token requests.
- Add a fixture that calls `monkeypatch.delenv(..., raising=False)` on `PODIUM_OAUTH_CLIENT_ID`, `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_OAUTH_TOKEN_URL`, used by every device-flow test, including the existing two.
- Tests use a fake clock (a mutable float) injected through `start_login(clock=...)` and an injected `sleep` that advances it and records each interval.
- Cases:
  1. `test_start_login_returns_handle_silently`: the five public fields match the fixture, `capsys` stderr is empty, `token_polls == 0`, and `"device_code" not in repr(handle)` and the fixture's device code is absent from `repr`.
  2. `test_start_login_complete_uri_absent_is_none`: the `/device` reply omits `verification_uri_complete`; the field is `None`.
  3. `test_finish_login_installs_token`: script `pending, ok`; the returned `Tokens.access_token` equals `client.token`, a following catalog call carries `Authorization: Bearer <token>`, and `refresh_token` is returned and not used for any request.
  4. `test_finish_login_slow_down_grows_interval`: script `slow_down, ok`; recorded waits are `[0, 5]`.
  5. `test_finish_login_denied`: reason `denied`, `client.token == ""`.
  6. `test_finish_login_idp_expired_token`: reason `expired`.
  7. `test_finish_login_after_local_expiry_sends_nothing`: advance the clock past `expires_in` between start and finish; reason `expired`, `token_polls == 0`.
  8. `test_finish_login_expires_in_loop_before_timeout`: `expires_in` 30, `timeout` 600, script always `pending`, sleep advances 10 s; reason `expired`, never `timeout`.
  9. `test_finish_login_timeout_runs_from_finish_call`: advance the clock 100 s between start and finish, `timeout=50`, `expires_in` 600, script always `pending`; at least one token request is sent (so the bound is not the start time) and the reason is `timeout`.
  10. `test_finish_login_cancel_before_call`: the `Event` is set first; reason `cancelled`, `token_polls == 0`, and a second finish raises `consumed`.
  11. `test_finish_login_cancel_between_polls`: script always `pending`; the injected `sleep` sets the `Event` on its second invocation; reason `cancelled`, `token_polls == 1`.
  12. `test_finish_login_cancel_wait_real_thread`: no injected sleep, fixture interval 1, a `threading.Timer` sets the `Event`; reason `cancelled`. No timing assertion.
  13. `test_finish_login_concurrent_single_winner`: two threads synchronized by a `threading.Barrier` call `finish_login` on one handle with script `ok`; exactly one returns tokens, exactly one raises `consumed`, and `token_polls == 1`.
  14. `test_finish_login_reuse_after_failure`: a denied finish, then a second finish raises `consumed` with `token_polls` unchanged.
  15. `test_start_login_failed_reasons`: parametrized over discovery 404, a discovery document without `device_authorization_endpoint`, `/device` 404 with a non-JSON body, and `/device` without `device_code`; each raises `DeviceCodeError` with reason `failed`, and the 404 cases chain a `urllib.error.HTTPError`.
  16. `test_finish_login_unknown_error_is_failed`: script an unrecognized error; reason `failed`.
  17. `test_login_composes_pair`: extend `test_login_runs_device_flow_and_authenticates` to assert stderr carries `Visit:` and `User code:` and that `open_browser=True` with no complete URI calls no browser (monkeypatch `_open_browser`).
  18. `test_login_expired_before_timeout`: `expires_in` shorter than `timeout`; reason `expired`.
  19. Extend `test_login_times_out_when_always_pending` to assert reason `timeout`.
  20. `test_import_surface`: `from podium import PendingLogin, DeviceCodeError, Tokens` succeeds and `hasattr(podium, "DeviceCodeRequired")` is false.
  21. `test_finish_login_unreachable_token_endpoint_is_failed`: `start_login(token_endpoint=...)` names a closed local port (bind a socket, read its port, close it); `finish_login` raises reason `failed` and `__cause__` is a `urllib.error.URLError` or an `OSError`. This case reaches the new non-`HTTPError` wrap in `_post_form`.
  22. `test_start_login_device_non_json_200_is_failed`: `/device` returns 200 with a non-JSON body; `start_login` raises reason `failed` and `__cause__` is a `json.JSONDecodeError`. This case reaches the new decode wrap in `_post_form`.

**TEST-2 · integration, `sdks/podium-ts/src/resolution_overlay_login.test.ts`.** Lands in S7. Each new or extended `it(` call carries `// Spec: §6.3` in the comment block directly above it, so `speccov` reads it. A comment above the `describe` call attaches to no test.

- Generalize `oauthFetcher` to script `/token` replies, toggle `verification_uri_complete`, fail `/device`, return a non-JSON 200 discovery body, reject `/token` with a `TypeError`, and honor `init.signal`: on `/token` it awaits a deferred promise when a test asks it to hang, and rejects with `new DOMException("aborted", "AbortError")` when the signal aborts; in body-abort mode it resolves at once with a `Response` whose body stream errors with that `DOMException` when the signal aborts. Stub the `PODIUM_OAUTH_*` variables to empty with `vi.stubEnv` and restore them in `afterEach`.
- Fake-timer discipline: call `vi.useFakeTimers()` before `startLogin` in every timing case, drive waits with `await vi.advanceTimersByTimeAsync(...)`, and call `vi.useRealTimers()` in `afterEach`.
- Cases mirror TEST-1 cases 1 to 11 and 13 to 19, with these TypeScript specifics:
  - Abort during the sleep (stub interval 1): reason `cancelled`, `cause === controller.signal.reason`, one token request.
  - Abort while a token request is in flight: reason `cancelled` and `err.cause === controller.signal.reason`.
  - Abort while the token response body is read: the stub resolves `/token` with a `Response` whose body is a `ReadableStream` that errors with `new DOMException("aborted", "AbortError")` when the signal aborts; reason `cancelled` and `err.cause === controller.signal.reason`.
  - Transport failure without an abort: the stub rejects `/token` with a `TypeError` while no signal has aborted; reason `failed` and `err.cause` is that `TypeError`.
  - Discovery returns 200 with a non-JSON body: `startLogin` rejects with reason `failed`, and `err.cause` is the error `resp.json()` raised.
  - Signal already aborted: reason `cancelled`, no token request, and a second finish rejects with `consumed`.
  - Concurrent finish: two `finishLogin` calls started in the same tick; one resolves, one rejects with `consumed`, one token request.
  - `slow_down`: stub interval 1; the second token request has not arrived after 5999 ms of advanced time past the first and has arrived after 6000 ms.
  - Local expiry: advance past `expiresInMs` between start and finish; reason `expired`, no token request.
  - A `PendingLogin` built with `new` rejects with reason `failed` and sends no request.
  - `startLogin` writes nothing to `process.stderr` (spy on `process.stderr.write`).
  - Import from `./index.js`: `PendingLogin`, `DeviceCodeError`, and the `DeviceCodeErrorReason` type; `instanceof PendingLogin` holds for the `startLogin` result.
  - Strengthen the existing timeout test to assert reason `timeout`.

**TEST-3 · e2e, `test/e2e/sdk_clients_test.go` `TestSDK_PyImport`.** Lands in S3 with CODE-2. Annotate `// Spec: §6.3`. Change the script to:

```python
from podium import Client, RegistryError, DeviceCodeError, PendingLogin
print('IMPORT_OK', Client.__name__, hasattr(Client, 'start_login'), hasattr(Client, 'finish_login'))
```

and assert stdout contains `IMPORT_OK Client True True`. Update the comment to say the test checks the documented names, including the §6.3 login pair.

Coverage: the new Python and TypeScript lines reach 85% through TEST-1 and TEST-2, measured by each SDK's own runner (`pytest --cov=podium`, `vitest run --coverage`). The one branch without a deterministic test, the Python in-flight request during cancellation, is covered by case 12's real-thread path.

## Manual validation

**MV-1.** Add scenario S78 to `test/manual-validation.md` after S77, following the existing conventions. Lands in S11.

~~~~markdown
## S78: The Python SDK login pair against a live IdP

**Goal.** Validate that `Client.start_login()` returns the verification URL and
the user code without printing or polling, that `Client.finish_login()` installs
the token after the user approves, that a finished handle cannot be reused, and
that a cancelled or denied flow ends with the matching `DeviceCodeError.reason`.

**Covers.** The §6.3 SDK contract for `oauth-device-code`. The SDK suites pin
each outcome against a stub IdP. This scenario covers what only a live IdP
establishes: the URL and code a person types into a real verification page,
and the reply the IdP sends on approval and on denial.

**Why by hand.** A notebook or GUI author reads the handle's fields and shows
them in their own interface. No automated test reads that the URL opens a real
verification page, that the code is accepted there, or that the terminal stays
silent while the start call runs.

**Prerequisites.**

- An IdP whose tenant publishes a device-authorization endpoint and a token
  endpoint, and a public client registered on it that may use the device-code
  grant. When none is available, skip the scenario and record the skip and the
  reason.
- `python3` 3.10 or later.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then export the
   IdP coordinates and the SDK path.

   ```bash
   export DEVICE_URL=<device authorization endpoint>
   export TOKEN_URL=<token endpoint>
   export CLIENT_ID=<device-code client id>
   export PYTHONPATH="$REAL_HOME/projects/podium/sdks/podium-py"
   unset PODIUM_OAUTH_CLIENT_ID PODIUM_OAUTH_AUDIENCE \
     PODIUM_OAUTH_AUTHORIZATION_ENDPOINT PODIUM_OAUTH_TOKEN_URL
   cat > "$WORK/pair.py" <<'PY'
   import os, sys, threading
   from podium import Client, DeviceCodeError
   c = Client(registry="http://localhost:1")
   kw = dict(client_id=os.environ["CLIENT_ID"],
             device_authorization_endpoint=os.environ["DEVICE_URL"],
             token_endpoint=os.environ["TOKEN_URL"])
   mode = sys.argv[1]
   p = c.start_login(**kw)
   print("URI", p.verification_uri, "CODE", p.user_code, "COMPLETE", p.verification_uri_complete)
   print("REPR", repr(p))
   cancel = threading.Event()
   if mode == "cancel":
       threading.Timer(3, cancel.set).start()
   try:
       t = c.finish_login(p, timeout=300, cancel=cancel)
       print("TOKEN_SET", bool(c.token) and c.token == t.access_token)
   except DeviceCodeError as e:
       print("REASON", e.reason)
   try:
       c.finish_login(p)
   except DeviceCodeError as e:
       print("REUSE", e.reason)
   PY
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and
   `$WORK/pair.py` exists.

2. Run the approve path, keeping the streams apart, and approve the request in a
   browser at the printed URI with the printed code.

   ```bash
   python3 -u "$WORK/pair.py" approve 2> "$WORK/err.txt" | tee "$WORK/out.txt"
   wc -c < "$WORK/err.txt"
   ```

   The `-u` flag keeps stdout unbuffered, so the `URI` line reaches the
   terminal while `finish_login` waits for the approval.

   **Expect.** The terminal and `out.txt` show a `URI` line with an `https` URL
   and a non-empty `CODE`, then `TOKEN_SET True`, then `REUSE consumed`.
   `err.txt` is 0 bytes.
   The `REPR` line does not contain the device code. A `Visit:` line in
   `err.txt`, or a second token on reuse, is the defect this step catches.

3. Run the cancel path and do not visit the URL.

   ```bash
   python3 "$WORK/pair.py" cancel
   ```

   **Expect.** Within about 3 s plus one poll interval the script prints
   `REASON cancelled`, then `REUSE consumed`. A run that blocks until the
   timeout is the defect this step catches.

4. Run the approve path again, and deny the request on the verification page.

   ```bash
   python3 "$WORK/pair.py" approve
   ```

   **Expect.** `REASON denied`, then `REUSE consumed`. `REASON failed` means
   the IdP's `access_denied` reply was not mapped.
~~~~

## Documentation changes

**DOC-1.** `docs/consuming/custom-via-sdk.md`, under `## Identity providers`, for (a) and (b), and `docs/reference/error-codes.md` for (c). Lands in S8. The example comment above `client.login()` near the top of the page stays as it is, because `login()` keeps its behavior.

(a) In the `oauth-device-code` bullet, replace "for the life of the process" with "for the life of the client", and append: "`Client.start_login()` and `Client.finish_login()` split the same flow for callers that show the URL and code in their own interface, as the next section describes."

(b) Add a subsection after the provider list:

~~~~markdown
### Non-blocking login

`Client.start_login()` (Python) and `client.startLogin()` (TypeScript) request a
device code and return a pending-login handle. The start call prints nothing
and does not poll. The handle carries the verification URL, the complete
verification URL when the IdP supplies one, the user code, the code's lifetime,
and the initial poll interval. Show the URL and the code in the application's
own interface, then call the finish call.

```python
import threading
from podium import Client, DeviceCodeError

client = Client(registry="https://podium.acme.com")
pending = client.start_login()
show_in_ui(pending.verification_uri, pending.user_code)

cancel = threading.Event()   # set from another thread to stop waiting
try:
    client.finish_login(pending, timeout=300, cancel=cancel)
except DeviceCodeError as err:
    report(err.reason)
```

```ts
import { Client, DeviceCodeError } from "@lennylabs/podium-sdk";

const client = new Client({ registry: "https://podium.acme.com" });
const pending = await client.startLogin();
showInUi(pending.verificationUri, pending.userCode);

const controller = new AbortController();
try {
  await client.finishLogin(pending, { timeoutMs: 300_000, signal: controller.signal });
} catch (err) {
  if (err instanceof DeviceCodeError) report(err.reason);
}
```

The finish call follows these rules:

- A handle is single-use. The first finish call consumes it whatever its
  outcome, and a later finish call fails with reason `consumed`.
- The code's lifetime runs from the start call. A finish call on an expired
  handle fails with reason `expired` and sends no token request.
- The timeout runs from the finish call and defaults to 10 minutes. The SDK
  checks it, the code's lifetime, and cancellation between token requests.
- Python cancels through a `threading.Event`, and TypeScript through an
  `AbortSignal`. A cancelled finish call fails with reason `cancelled`. Python
  stops at the next check and does not interrupt a token request in flight.
- `DeviceCodeError.reason` is `denied`, `expired`, `timeout`, `cancelled`,
  `consumed`, or `failed`. The `failed` reason covers discovery, device
  authorization, transport, and unrecognized IdP errors.
- On success the finish call stores the access token on the client it was
  called on. The token stays in memory for the life of the client, and the
  SDK neither persists nor refreshes it. Finish a handle on the client that
  started it.
- Until a finish call succeeds, the client sends anonymous requests and sees
  public visibility only. The SDK does not refresh the access token, so when
  it expires (15 minutes by default) requests fail with `auth.token_expired`.
  Run the pair or `login()` again to obtain a new token.

`client.login()` runs both calls, prints the URL and the code to stderr, and
blocks until the flow ends.
~~~~

(c) In `docs/reference/error-codes.md`, in the `auth.token_expired` row (line 59 at the time of writing), insert after "The MCP server triggers refresh on `oauth-device-code`;" the clause "an SDK client on `oauth-device-code` runs its login again, because the SDK does not refresh;". The registry emits this code for an expired token from `writeIdentityError` (`pkg/registry/server/identity_verify.go:92-100`), and the row today names a remedy for every consumer except the SDK.

The page's Python and TypeScript fenced blocks are not runnable under `tools/doccov`, so no manifest entry is needed. `docs/reference/error-codes.md` has no `tools/doccov/manifest.yaml` entry, and (c) adds no runnable block.

**DOC-2.** `sdks/podium-py/README.md`. Lands in S9. `sdks/podium-ts/README.md` contains no login or authentication content and is not edited. In the paragraph that describes `client.login()`, change the `(§7.7)` citation to `(§6.3)` and add after that sentence: "`client.start_login()` returns a single-use pending handle carrying the verification URL and the user code without printing or polling, and `client.finish_login(handle)` polls until the flow completes and installs the token on the client (§6.3)."

**CL-1.** `CHANGELOG.md`, `## [Unreleased]`. Lands in S10.

Under `### Added`:

> - **Non-blocking SDK login** (§6.3): the Python SDK gains `Client.start_login()` and `Client.finish_login()`, and the TypeScript SDK gains `client.startLogin()` and `client.finishLogin()`. The start call returns a single-use `PendingLogin` handle with the verification URL, the user code, the code lifetime, and the poll interval, and it neither prints nor polls. The finish call polls with a timeout and a cancellation input (`threading.Event`, `AbortSignal`) and installs the token in memory. `DeviceCodeError` gains a `reason` (`denied`, `expired`, `timeout`, `cancelled`, `consumed`, or `failed`). `Client.login()` keeps its signature and output and now composes the pair; when the device code expires before the timeout, it raises reason `expired` where it previously reported `login timed out`.

Under `### Removed`:

> - **`DeviceCodeRequired`** from the Python SDK. No code raised it. Catch `DeviceCodeError` instead.

## Open questions

**OQ-1. Typed failure reason.** `DeviceCodeError.reason` is new public surface in both SDKs. The single-use rule needs a distinct reuse error, and a UI needs to tell a denial from an expiry, which is why the draft adds it. Should the reviewer prefer `DeviceCodeError` subclasses (for example `DeviceCodeExpired` and `LoginHandleConsumed`), or should reuse raise a plain misuse error (Python `RuntimeError`, TypeScript `Error`) and leave `DeviceCodeError` without a reason? The draft stages the `reason` attribute.

## Non-goals

- Persisting or refreshing the SDK token. Keychain storage and `refresh_token` use remain CLI and MCP-server behavior.
- Raising on tokenless calls (option a), or starting a login automatically from the `identity_provider` setting. Anonymous SDK use stays as it is.
- An asyncio variant of the Python pair, or a synchronous TypeScript variant.
- An `openBrowser` option on the TypeScript `login()`, or browser opening in `start_login`/`startLogin`.
- Changing `pkg/identity`'s `ErrDeviceCodeRequired` or any MCP-server or CLI device-code behavior (the §6.3 MCP and CLI sub-bullets, §7.7 `podium login`).
- Editing historical proposals (0012, 0013) or untracked notes under `tmp/` that mention `DeviceCodeRequired`.
- SPEC-2, a §7.6 sentence carving the SDK token out of "the same as in the MCP path", is dropped. SPEC-1 already rewrites the §6.3 `oauth-device-code` bullet to state token storage per consumer, and §7.6's "Identity providers" points at that definition, in the same way §6.3 already varies URL surfacing by consumer without a §7.6 carve-out. "The cache" in §7.6 is the content cache that §7.4 defines ("`podium sync` and the SDKs apply the same cache modes"), so it is not a token cache. A second statement of the in-memory rule in §7.6 would add a place for the two sections to drift. The keychain mentions in §10 and §11 concern the MCP server and the CLI.
- Fixing the §7.6 statement that the SDK shares "the cache" with the MCP path, which conflicts with the SDK keeping no persistent content cache. Only the identity token is reconciled here.
- A per-request timeout on the Python `_post_form` `urllib` calls or the TypeScript `fetch` calls to the IdP. This gap predates the proposal, and Python cancellation takes effect at the next check.
- Correcting `.claude/rules/test-coverage.md`, which calls the TypeScript suite "the Node test runner" when it runs on vitest.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-02, automated)

- **TypeScript abort during the body read reported `failed`.** An abort after the response headers resolves `fetch` and rejects `resp.json()`, whose existing catch (`sdks/podium-ts/src/oauth.ts:66-70`) throws `HTTP <status>`. CODE-3's `postForm` bullet now applies the `signal?.aborted` to `cancelled` mapping to a rejection from either `fetch` or `resp.json()`, and keeps `HTTP <status>` with reason `failed` only for a non-aborted body failure. The Decisions cancellation bullet and the edge-case row name the body read, and TEST-2 gains a body-abort case with a stub body stream that errors on abort.
- **No test reached the new transport-failure wraps.** TEST-1 gains case 21 (unreachable token endpoint, `__cause__` a `URLError` or `OSError`) and case 22 (`/device` 200 with a non-JSON body, `__cause__` a `JSONDecodeError`), and the `_OAuthHandler` bullet gains the non-JSON 200 mode. TEST-2 gains a `/token` `TypeError` rejection without an abort and a non-JSON 200 discovery body, each asserting reason `failed` and `err.cause`, and the `oauthFetcher` bullet gains those modes. The Watch-out bullet on Python discovery names the asserting cases.
- **MV-1 step 2 hid the URI and code from the operator.** Step 2 redirected stdout to a file, so the block-buffered `URI` line never reached the terminal while `finish_login` waited. Step 2 now runs `python3 -u "$WORK/pair.py" approve 2> "$WORK/err.txt" | tee "$WORK/out.txt"`, keeps the `wc -c` stderr check, and its Expect names the terminal and `out.txt`.

### Pass 2 (2026-10-02, automated)

- **The rewritten §6.3 parent bullet still started the flow on first use for every consumer.** SPEC-1(a) kept "Interactive device-code flow on first use" as a shared opening, which contradicted SPEC-1(b)'s statement that a tokenless SDK client sends anonymous requests and the Non-goals exclusion of an automatic login. SPEC-1(a) now opens with "Interactive device-code flow", scopes the first-use trigger to the MCP server and the CLI together with keychain caching and refresh, and states that the SDK runs the flow only when the calling code invokes it. The Summary's SPEC-1 line names the scoped trigger.

### Pass 3 (2026-10-02, automated)

- **The proposal claimed `speccov` reads only Go `Spec` annotations.** `specparser.WalkTests` (`tools/internal/specparser/specparser.go:170-201`) parses Python `test_*.py` and `*_test.py` files and TypeScript `*.test.ts` files, and reads the comment block directly above each `def test_*` line or `it(`/`test(` call. The Watch-out bullet now states which comment placements the gate reads and cites `test_client.py:67` and `index.test.ts:13` as precedent. TEST-1 places `# Spec: §6.3` directly above each `def test_*` line, below any decorator, in place of the in-body `# spec §6.3` convention. TEST-2 places `// Spec: §6.3` above each `it(` call and drops the claim that the annotation is not machine-checked.

### Pass 4 (2026-10-02, automated)

- **Two edge-case rows cited DOC-1 for outcomes DOC-1 did not state.** The rows for a client used before a finish call succeeds and for an expired access token named DOC-1, whose staged text said neither that the client sends anonymous requests nor that an expired token fails with `auth.token_expired` and needs a new login. `docs/reference/error-codes.md:59` named a remedy for every consumer except the SDK. DOC-1(b) gains a rule bullet stating both outcomes, and a new DOC-1(c) inserts the SDK remedy into the `auth.token_expired` row, grounded in `writeIdentityError` (`pkg/registry/server/identity_verify.go:92-100`). The two rows now cite DOC-1(b) and DOC-1(c), the token-expiry row names `auth.token_expired`, and the S8 step and the Summary's docs line name the error-code reference.
