# 6. MCP Server

## 6.1 The Bridge

The Podium MCP server is a thin in-process bridge. It exposes the meta-tools to the host's runtime over MCP and forwards calls to the registry. It holds no per-session server-side state. Local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

A single Go binary serves every deployment context. The host configures it via env vars, command-line flags, or a config file.

**Requires a server-source registry.** The MCP server speaks HTTP and does not work against a filesystem-source registry (§13.11).

### 6.1.1 Tool Result Format

A `tools/call` response is an MCP `CallToolResult`. The bridge carries the meta-tool's result in two fields. `structuredContent` holds the typed domain object, the fields documented in §5 (such as `results` and `total_matched` for `search_artifacts`, or `id`, `manifest_body`, and `materialized_at` for `load_artifact`). `content` holds the same object serialized as a single JSON text block, so a host that renders `result.content` (Claude Code, Claude Desktop, Cursor, VS Code) displays the output to the model. A `CallToolResult` with no `content` block renders as empty, and the model receives no tool output.

`isError` is set to `true` when the domain object is a §6.10 error envelope, so the host marks the call as failed. A tool-level failure is reported as a `CallToolResult` with `isError: true`. A JSON-RPC `error` is reserved for protocol faults such as an unknown method or malformed parameters.

The meta-tool fields are reachable under `structuredContent`. The result top level carries the MCP envelope keys (`content`, `structuredContent`, and `isError`), so a consumer reads `result.structuredContent.<field>` for the meta-tool output.

## 6.2 Configuration

Top-level configuration parameters (env-var form shown; `--flag` and config-file equivalents are accepted):

| Parameter                    | Description                                                                                                                      | Default                                                                                       |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `PODIUM_REGISTRY`            | Registry source: URL (HTTP) or filesystem path. See §7.5.2 for dispatch.                                                         | (read from `sync.yaml`'s `defaults.registry` per §7.5.2 if unset; required if neither is set) |
| `PODIUM_IDENTITY_PROVIDER`   | Selected identity provider implementation                                                                                        | `oauth-device-code`                                                                           |
| `PODIUM_HARNESS`             | Selected harness adapter                                                                                                         | `none` (write canonical layout as-is)                                                         |
| `PODIUM_OVERLAY_PATH`        | Workspace path for the `local` overlay                                                                                           | (unset → layer disabled)                                                                      |
| `PODIUM_CACHE_DIR`           | Content-addressed cache directory                                                                                                | `~/.podium/cache/`                                                                            |
| `PODIUM_CACHE_MODE`          | `always-revalidate` / `offline-first` / `offline-only`                                                                           | `always-revalidate`                                                                           |
| `PODIUM_AUDIT_SINK`          | Local audit destination (path or external endpoint). When set without a value (or set to `default`), uses `~/.podium/audit.log`. | (unset → registry audit only)                                                                 |
| `PODIUM_MATERIALIZE_ROOT`    | Default destination root for `load_artifact`                                                                                     | (host specifies per call)                                                                     |
| `PODIUM_PRESIGN_TTL_SECONDS` | Override for presigned URL TTL                                                                                                   | 3600                                                                                          |
| `PODIUM_VERIFY_SIGNATURES`   | Verify artifact signatures on materialization                                                                                    | `medium-and-above`                                                                            |

Provider-specific options are passed as additional env vars (e.g., `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_ENV`).

## 6.3 Identity Providers

Identity providers attach the caller's OAuth-attested identity to every registry call. `oauth-device-code` is the client-side acquisition provider: the consumer obtains and caches the token through the device-code flow. `injected-session-token` is the provider the registry verifies server-side, by checking the runtime's signature on each call (§6.3.2). §6.3.4 specifies the browser acquisition flow, in which a registry serving the web UI (§13.10) obtains the `oidc-jwt` credential for a browser through a server-side authorization-code exchange.

- **`oauth-device-code`** _(default)_. Interactive device-code flow on first use; tokens cached in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux). Refreshes transparently. Defaults: access-token TTL 15 min, refresh-token TTL 7 days, revocation propagation ≤60s. Options: `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_TOKEN_KEYCHAIN_NAME`.

  How the verification URL surfaces depends on the consumer:
  - **MCP server** uses MCP elicitation; the host displays the URL and code in the agent UI.
  - **`podium sync`, `podium login`, and other CLI commands** print the URL and code to stderr, attempt to open the URL in the system browser (via `open` on macOS, `xdg-open` on Linux, `start` on Windows), and poll the IdP's token endpoint until the user completes the flow or a 10-minute timeout elapses. `--no-browser` skips the auto-open. Output is suppressed under `--json`; the prompt is replaced with a structured `auth.device_code_pending` event emitted on stderr.
  - **SDK** raises `DeviceCodeRequired` with the URL and code; calling code is responsible for surfacing it to the user. `Client.login()` performs the same blocking poll-until-completion the CLI uses.

- **`injected-session-token`**. The MCP server reads a signed JWT from an env var or file path configured by the runtime. The runtime is responsible for token issuance and refresh. Options: `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE`.
- **(Extensible.)** Additional implementations register through the `IdentityProvider` interface (§9).

### 6.3.1 Claim Derivation

The IdP returns a JWT with claims `{sub, org_id, email, exp, iss, aud, groups?}`. Group membership is resolved registry-side via SCIM 2.0 push from the IdP; the registry maintains a directory of `(user_id → groups)`.

Under `oidc-jwt` the claim read as the caller's subject and the claim read for group membership are named by configuration (§6.3.3), so a deployment whose IdP emits neither `sub` nor `groups` names the claims it does emit. A group claim is read in the multi-value form and in the single-string form an IdP emits for a caller in exactly one group. The single-string form yields one group whose name is the claim value; it is not split on any separator.

For IdPs without SCIM, the `IdpGroupMapping` adapter reads OIDC group claims from the token and maps them to group names per a registry-side configuration. The adapter maps group values. The claim that carries them is named by `PODIUM_OAUTH_GROUPS_CLAIM` under `oidc-jwt` (§6.3.3) and is `groups` under `injected-session-token` (§6.3.2).

Tested IdPs: Okta, Entra ID, Auth0, Google Workspace, Keycloak. SAML supported via OIDC bridge.

Fine-grained narrowing via OAuth scope claims (e.g., `podium:read:finance/*`, `podium:load:finance/ap/pay-invoice@1.x`); narrow scopes intersect with the caller's layer visibility, and the smaller surface wins.

**Per-request tenant selection.** On a multi-tenant registry, the registry selects each request's tenant (§4.7.1) from the organization value carried by the authenticated identity: the verified `org_id` claim under `oidc-jwt` (§6.3.3), or the `X-Podium-User-Org` header under `trusted-headers`. The value is an org ID or an org-name alias (§4.7.1); the registry resolves an alias to its org ID and selects that org's layer list (§4.6) and audit stream for the request. Under `oidc-jwt`, a value that resolves to no provisioned tenant is rejected with `auth.tenant_unknown` (§6.10); under `trusted-headers`, the request is treated as anonymous and sees public visibility only. A single-tenant registry, whether a standalone backend (§13.10) or a standard backend started without `PODIUM_MULTI_TENANT`, resolves every authenticated request against its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing.

### 6.3.2 Runtime Trust Model (`injected-session-token`)

The injected token is a JWT signed by a runtime-specific signing key that the deployment configures the registry to trust at runtime onboarding. The registry verifies the signature on every call. Required claims:

- `iss`: runtime identifier (must match a registered runtime).
- `aud`: registry endpoint.
- `sub`: user id the runtime is acting on behalf of.
- `act`: actor (the runtime itself).
- `exp`: expiry.

Without a registered signing key, the registry rejects with `auth.untrusted_runtime`.

**Establishing the trusted key set.** A runtime signing key is a trust anchor for the whole registry. A token signed with it carries its own `sub` and `org_id`, so the key mints an identity for any subject in any organization, including one holding a per-tenant admin grant (§4.7.2) or the instance-operator grant (§4.7.1); an already-trusted runtime can therefore present itself as any administrator. The registry takes its trusted key set from operator-supplied configuration read at startup and exposes no request-time registration API. `PODIUM_RUNTIME_KEYS_PATH` (§13.12) names the file that carries the set. Each record in that file names the runtime's issuer, its JWS algorithm, and its public key as a PKIX PEM block, and `podium admin runtime register` writes a record into it. The registry names the trusted issuers in its startup log. Under `injected-session-token` a registry whose configuration supplies no usable key refuses to start with `config.runtime_keys_unavailable` (§13.12), because such a registry rejects every call with `auth.untrusted_runtime` and can accept no first key.

Every replica of a deployment (§13.1) is configured with the same trusted key set, and a key added or rotated there takes effect at each replica's next start.

#### 6.3.2.1 Token Rotation Contract

- Env-var change is observed at next registry call (no signal needed; the MCP server reads fresh on every call).
- SIGHUP triggers a forced re-read.
- `PODIUM_SESSION_TOKEN_FILE` is watched via fsnotify and re-read on change.

Token rotation is the runtime's responsibility; the MCP server's only obligation is to read fresh on every call. Recommended TTLs: ≤15 min. Prefer `PODIUM_SESSION_TOKEN_FILE` over env var when the runtime can write to a file with restrictive permissions.

### 6.3.3 Server-Side Request Authentication

`oidc-jwt` and `trusted-headers` are registry-process identity providers. `oidc-jwt` serves a deployment where the registry verifies the caller's token itself, whether a gateway forwarded that token or the registry obtained it through the §6.3.4 exchange. `trusted-headers` serves a deployment that runs the registry behind a gateway that has already authenticated the caller (an OIDC ingress, an OAuth2 proxy, an identity-verifying sidecar, or a non-OIDC corporate SSO). Both are selected by the registry's `PODIUM_IDENTITY_PROVIDER`. They are not client-side providers: the MCP server's `PODIUM_IDENTITY_PROVIDER` admits only `oauth-device-code` and `injected-session-token`, and rejects these two values at startup. A Podium client behind a gateway that has already authenticated the caller, under either provider, sends no credential of its own, because identity is supplied by the gateway. Both apply on a standalone (§13.10) or a standard (§13.1) backend, and both are mutually exclusive with public mode: setting either alongside `PODIUM_PUBLIC_MODE` fails at startup with `config.public_mode_with_idp` (§13.10).

Both record the caller's subject and `email` and match them against `users:` layer visibility (§4.6). They derive the caller's organization (the §4.7.1 tenant) from the authenticated identity rather than a client-supplied value: `oidc-jwt` from the verified `org_id` claim, and `trusted-headers` from the `X-Podium-User-Org` header. On a single-tenant registry, whether a standalone backend (§13.10) or a standard backend started without `PODIUM_MULTI_TENANT`, the registry resolves every authenticated caller to its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing. On a multi-tenant registry, the organization value selects the tenant per §6.3.1. Each value resolves groups differently, as described below.

**`oidc-jwt` (verified).** The gateway forwards the caller's IdP-signed JWT in the header named by `token_header` (default `Authorization`). The registry parses the named header's value as the standard HTTP Bearer credential regardless of the header name: the value must be `Bearer <token>`, the prefix is matched case-insensitively, and surrounding whitespace is trimmed from the token. A header value without the prefix carries no bearer credential, as do an omitted header and a `Bearer ` value that trims to empty.

The credential is accepted in a second location. Where the browser flow (§6.3.4) is enabled, a token the registry itself obtained through the §6.3.4 exchange may arrive in the `__Host-podium_session` cookie instead of the configured token header, and the registry verifies it identically against the issuer JWKS for the same `aud`. Because it is the same credential, the second location adds no verification rule and no refusal. The registry reads the configured token header first, and a bearer credential found there decides the request's identity; it reads `__Host-podium_session` only when the configured token header carries no bearer credential and the browser flow is enabled on that registry. A registry with the browser flow disabled reads no cookie at all, and a stale `__Host-podium_session` sent to it carries nothing. The two locations are never merged, and no request draws part of its identity from each: a gateway that authenticated the request is the authority in that deployment, and a registry-set cookie does not displace a gateway-forwarded identity.

A request whose configured token header carries no bearer credential is anonymous and sees public visibility only (§4.6). Where the browser flow is enabled, such a request is anonymous only when it also presents no valid `__Host-podium_session` cookie, meaning a cookie whose token verifies against the issuer JWKS for the same `aud`; the configured token header is still read first. Where the flow is disabled the rule applies to the configured token header alone.

The registry verifies the token on every request. It selects the signing key by `kid` from the issuer's JWKS, resolved from the OIDC discovery document at `<issuer>/.well-known/openid-configuration` and refreshed when the cached key set is older than `jwks_cache_ttl_seconds` (default 300) or when a token presents a `kid` absent from the cached set. It checks the signature against an asymmetric algorithm (RSA, ECDSA, or EdDSA; symmetric algorithms are rejected, so a public key cannot be replayed as an HMAC secret), and validates `iss` against the accepted issuers, `aud` against `PODIUM_OAUTH_AUDIENCE`, and the `exp`/`nbf` window. On success it records the caller's subject and `email`, derives the organization from the verified `org_id` claim, and resolves groups through SCIM or the `IdpGroupMapping` adapter (§6.3.1) applied to the token's group claim. A token that fails signature, `iss`, or `aud` validation is rejected with `auth.untrusted_token`, and an expired token with `auth.token_expired`. While the issuer JWKS is unreachable at runtime, verification fails closed and the request is anonymous rather than rejected.

The accepted issuers are the configured `issuer` and the `access_token_issuer` the same discovery document publishes. The registry reads `access_token_issuer` once, when it resolves that document, and compares it to a token's `iss` as a string. It never fetches the second value and never resolves keys from it: the signing keys come from the `jwks_uri` in the configured issuer's `https` document in either case, so the set of trusted signing keys is the same under both accepted values. A discovery document that publishes no `access_token_issuer` leaves the configured `issuer` as the sole accepted value. When the document publishes an `access_token_issuer` that differs from the configured `issuer`, the registry names both accepted values in its startup log. AD FS is the deployment this rule covers: it serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token, so no single configured value can serve as the discovery base and the expected `iss` at once.

`PODIUM_OAUTH_SUBJECT_CLAIM` names the claim the registry reads as the caller's subject, and `PODIUM_OAUTH_GROUPS_CLAIM` names the claim it reads for group membership. When `PODIUM_OAUTH_SUBJECT_CLAIM` is set, the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. When it is unset, the subject is `sub`. When `PODIUM_OAUTH_GROUPS_CLAIM` is set, the registry reads group membership from the named claim; when it is unset, it reads `groups`. Both settings are read only under `oidc-jwt`, and the §6.3.2 injected-session-token verifier reads `sub` and `groups`. An AD FS deployment sets both, because its access tokens carry a pairwise subject under a claim other than `sub` and its issuance rules emit group membership under a full claim-type URI unless the rule is authored with a short name.

The claim named by `PODIUM_OAUTH_SUBJECT_CLAIM` must identify one principal for the life of the deployment, and the IdP must not reassign one principal's value to another. The registry matches the recorded subject against `users:` layer visibility (§4.6), stores it as the owner of a user-defined layer (§7.3.1), matches it against per-tenant admin grants (§4.7.2) and the instance-operator grant (§4.7.1), and records it as the audit caller identity (§8.1). A reassigned value therefore transfers layer visibility, layer ownership, and any admin or operator grant with it. A deployment that sets `PODIUM_OAUTH_SUBJECT_CLAIM` lists values of the named claim in `PODIUM_OPERATOR_ADMINS` and `PODIUM_BOOTSTRAP_ADMINS`, because operator and admin authorization match the recorded subject and have no email fallback, while `users:` visibility matches the subject or the email.

The `issuer` must use the `https` scheme: the registry fetches the discovery document and JWKS over it, and an `http` endpoint would let a man-in-the-middle substitute a signing key. The registry rejects a non-`https` issuer at startup with `config.invalid_issuer_scheme`. `PODIUM_OAUTH_AUDIENCE` is required under `oidc-jwt`, so the required `aud` claim is always verified and a token issued for a different relying party that shares the issuer cannot be accepted; an unset audience fails startup with `config.oidc_jwt_audience_unset`. If the discovery document or JWKS is unreachable at startup, the registry fails to start. This mode trusts the issuer's signing key alone and no element of the network path, so the registry may be directly reachable without an authentication bypass.

**`trusted-headers` (delegated).** The gateway authenticates the caller by any means and injects the resolved identity as request headers: `X-Podium-User-Sub`, `X-Podium-User-Email`, `X-Podium-User-Groups` (comma-separated), and `X-Podium-User-Org`. The registry records `sub` and `email` from these headers and performs no verification of their contents. Groups come from `X-Podium-User-Groups` directly; SCIM and the `IdpGroupMapping` adapter are not consulted, because there is no token to read and the gateway is the source of truth. A request without identity headers is anonymous and sees public visibility only. `trusted-headers` raises no authentication error: a missing or distrusted identity yields anonymous, public-only visibility rather than a rejection.

This mode rests on the operational assumption, which the registry cannot verify, that every request arrived through the gateway and that the gateway removed any client-supplied `X-Podium-User-*` headers before setting its own. When `PODIUM_TRUSTED_PROXY_SECRET` is set, the registry honors the identity headers only on a request whose `X-Podium-Proxy-Secret` header matches the configured value under a constant-time comparison, and treats any other request as anonymous; when the secret is unset, the secret header is ignored and the identity headers are honored on every request.

Because `trusted-headers` reads identity from headers it cannot verify, the identity it trusts is exactly the set of clients that can reach the bind address, so the provider constrains the bind at startup. (`oidc-jwt`, which verifies every token regardless of the network path, carries no bind restriction.) On a single-tenant registry, a loopback bind (`127.0.0.0/8`, `::1`) is always allowed; a non-loopback bind fails to start with `config.trusted_headers_public_bind` unless `PODIUM_TRUSTED_PROXY_SECRET` or `--allow-public-bind` is set. On a multi-tenant registry, where `X-Podium-User-Org` selects among tenants and a co-resident process can reach a loopback bind, co-residency does not authenticate the gateway, so the proxy secret is required regardless of bind address, including loopback; an unset secret fails to start with `config.trusted_headers_multitenant_no_secret`. The proxy secret is the registry's only request-level control over header trust, because the registry serves HTTP and TLS terminates upstream. The `--allow-public-bind` flag records the operator's assumption that an upstream control the registry cannot verify, such as mutual TLS, a firewall, or a network policy, keeps the registry reachable only through the gateway.

The `X-Podium-User-*` and `X-Podium-Proxy-Secret` headers are request inputs read only in `trusted-headers` mode. They are unrelated to the §13.2.1 read-only response headers, and `trusted-headers` does not consult the `X-Forwarded-User` audit annotation (§8.1) as an authorization input.

### 6.3.4 Browser Acquisition Flow (`oidc-jwt`)

Where a registry serving the web UI (§13.10) enables the browser flow, a browser acquires the `oidc-jwt` credential through the registry rather than holding one of its own. The registry runs the OAuth 2.0 authorization-code flow with PKCE against the identity provider: the sign-in route redirects the browser to the provider's authorization endpoint, and the callback route exchanges the returned authorization code for an access token server-side and returns that token to the browser in the `__Host-podium_session` cookie. §7.3.4 specifies the routes, the single-use pre-authorization transaction they carry, and what each outcome sets and clears.

The token the cookie carries is the same IdP-signed access token a device-code consumer presents, and it carries the registry's resolved audience because the authorization request asks the IdP for that audience the way the device-code flow does. The flow therefore adds no credential to §6.3.3: the registry verifies the token in the cookie exactly as it verifies one in the configured token header, against the issuer JWKS for the same `aud`. The registry keeps no session record, mints no session identifier, and holds no session key. A deployment whose IdP issues opaque access tokens cannot use the browser flow, on the same terms the `oidc-jwt` path already imposes on a forwarded token, and so cannot a deployment whose IdP neither honors the audience parameter nor is configured to mint the registry's resolved audience for this client.

Options: `PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, `PODIUM_WEB_UI_REDIRECT_URI`, `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT`, `PODIUM_OAUTH_AUDIENCE`. The client identifier, the client credential, the redirect URI, the authorization endpoint, and the token endpoint are the acquisition values. They are required in addition to the issuer and audience `oidc-jwt` already requires (§6.3.3), and a registry that enables the browser flow with any of them empty fails startup with `config.web_ui_auth_unconfigured` (§13.10). `PODIUM_OAUTH_AUDIENCE` carries the registry's resolved audience, which the authorization request sends; it is the audience `oidc-jwt` already requires, and the browser flow adds no key of its own for it. The keys that enable and tune the flow in the registry process are documented in §13.10.

`PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` is the device-authorization endpoint of the §6.3 `oauth-device-code` flow, and the browser flow does not read it. The authorization endpoint the sign-in route redirects to is `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, and it is the only key the flow reads for that purpose. The startup guard does not accept the device-code key in place of it: a configuration that sets `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` and leaves `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` empty fails the authorization-endpoint conjunct and fails startup with `config.web_ui_auth_unconfigured` naming the key the flow needs, so an operator who sets only the device-code key gets a startup refusal rather than a redirect to nowhere.

**The authorization request.** The sign-in route mints the `state` and the PKCE `code_verifier`, returns both in the `__Host-podium_auth` cookie, and redirects the browser to `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` carrying the query parameters in the table below and no others. The enumeration is closed.

| Parameter | Value | Where it comes from |
|:--|:--|:--|
| `response_type` | the literal `code` | fixed |
| `client_id` | `PODIUM_WEB_UI_OAUTH_CLIENT_ID` | configuration |
| `redirect_uri` | `PODIUM_WEB_UI_REDIRECT_URI`, sent byte-identically here and on the token request | configuration |
| `scope` | `PODIUM_WEB_UI_OAUTH_SCOPES`, space-delimited, defaulting to `openid profile email groups` | configuration |
| `audience` | the registry's resolved audience | configuration |
| `state` | minted per transaction: 32 random bytes, base64url-encoded without padding | minted |
| `code_challenge` | the base64url encoding, without padding, of the SHA-256 digest of the verifier, where the verifier is 32 random bytes in the same encoding, which is 43 characters and satisfies RFC 7636 §4.1 | derived |
| `code_challenge_method` | the literal `S256`, always sent | fixed |

`code_challenge_method` is always sent because RFC 7636 §4.3 makes `plain` the default when the parameter is absent, and under `plain` the challenge is the verifier itself, travelling through the browser's address bar and the IdP's redirect chain, which removes the property PKCE is here for.

The scope set defaults to `openid profile email groups` rather than to `openid` alone, because the §6.3.1 `IdpGroupMapping` adapter reads a group claim out of the token and a token issued without the scope that carries the group claim carries none, so every group-scoped visibility decision would narrow silently for a browser caller while the same subject sees more from the CLI. The value is configured rather than fixed because the scope that puts a group claim on the access token is IdP-specific, and an IdP that defines no such scope refuses the authorization request outright.

**The token request.** The callback exchanges the authorization code at `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` with the form fields in the table below and no others.

| Field | Value |
|:--|:--|
| `grant_type` | the literal `authorization_code` |
| `code` | the `code` the callback query carries |
| `redirect_uri` | `PODIUM_WEB_UI_REDIRECT_URI`, byte-identical to the value the authorization request sent, which RFC 6749 §4.1.3 requires of a request whose authorization leg carried one |
| `client_id` | `PODIUM_WEB_UI_OAUTH_CLIENT_ID` |
| `client_secret` | `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, always sent as a form field |
| `code_verifier` | the verifier `__Host-podium_auth` holds, per RFC 7636 §4.5 |

The request is a POST carrying `Content-Type: application/x-www-form-urlencoded` and `Accept: application/json`, and its non-`200` body is decoded as the RFC 6749 §5.2 error envelope. The client credential is sent as a form field rather than as an HTTP Basic credential, and it is always sent, because a registry that enables the browser flow with an empty client credential fails startup (§13.10) and no running registry reaches an omitted field. §6.10 states which code each exchange failure returns.

The exchange consumes the ID token for nothing. `__Host-podium_session` carries the access token, and every resolved subject comes from verifying that access token under §6.3.3, so the registry reads no ID-token claim. The authorization request therefore sends no `nonce` and the callback compares none: `state` binds the callback to the browser that started the transaction and `code_verifier` binds the exchange to the client that started it, and there is no third token for a third value to bind.

**The browser-origin gate.** A credential a browser attaches by itself authenticates any request the browser can be induced to make, so every write the registry exposes to a browser is otherwise forgeable across origins. The registry refuses a state-changing request that carries cross-site browser-origin evidence.

The gate is scoped by the evidence the request carries rather than by which credential authenticated it. The session cookie is not the only credential a browser attaches by itself: where a gateway fronts the registry under `oidc-jwt` or `trusted-headers`, §13.10 serves the UI from the same registry process behind the same gateway, and there the browser's credential is the gateway's own ambient session, which the gateway converts into the configured token header on every request the browser can be induced to make, including a cross-origin form POST. A gate scoped to the session cookie would leave those writes forgeable, so the predicate below reads the request.

- **What counts as state-changing.** A request is state-changing for this gate when its HTTP method is other than `GET`, `HEAD`, or `OPTIONS`. The predicate is the method rather than the handler's effect, because the gate runs before the handler and has nothing else to read, and because the safe methods are the ones a browser issues as an ordinary navigation or subresource load.
- **The refusal.** Every state-changing request, other than the sign-in and callback routes, that carries cross-site browser-origin evidence is refused before the handler runs with `403` `auth.csrf_invalid` (§6.10), whatever credential authenticated it.
- **What counts as cross-site evidence.** Browser-origin evidence is cross-site when the request carries a `Sec-Fetch-Site` header whose value is other than `same-origin` or `none`, or an `Origin` header whose host and port differ from the host and port the request's own `Host` header names. The scheme is not compared. An HTTP `Host` header carries no scheme, and a registry behind a TLS-terminating gateway cannot observe the browser-facing one, since the registry serves HTTP and TLS terminates upstream (§6.3.3). A predicate that compared the scheme would refuse every panel write on the deployment §13.10 describes and on the §13.1 topology, where a browser on `https://registry.acme.com/app/` sends `Origin: https://registry.acme.com` to a registry whose own request scheme is `http`. Omitting the scheme term admits a downgrade origin such as `http://<host>` as same-origin, which costs this gate nothing, because §13.10 requires the flow's redirect URI to be an `https` URL or a loopback `http` URL and a browser therefore holds no session credential to present on such an origin.
- **A deployment the comparison does not serve.** Comparing against `Host` is what keeps the gate free of a configuration key for the registry's public origin. One deployment that comparison does not serve is a gateway that rewrites `Host` to an upstream service name: there a legitimate same-origin write from the UI carries an `Origin` whose host differs from `Host`, so every write from the UI is refused with `403` `auth.csrf_invalid` while every CLI and SDK write keeps succeeding. The remedy is to pass the browser-facing `Host` through unrewritten.
- **What is admitted.** A state-changing request carrying neither header carries no browser-origin evidence and is admitted, which is what a CLI, an SDK, or any other non-browser client sends. The gate is triggered by evidence and never requires a proof of same origin, so a request that proves nothing is admitted rather than refused; a gate that swept those requests in would break every non-browser writer on a registry that enables the browser flow. That admitted case is the gate's residual: a browser that sent neither header would be indistinguishable from a non-browser client. Every browser that can reach a web UI deployment sends `Sec-Fetch-Site` on a cross-site request and `Origin` on a cross-origin form POST.
- **The gate is not conditional on the browser flow.** It reads the request rather than the deployment, so it runs on every state-changing request the registry serves, including on a registry that enables no browser flow and on one that serves no web UI. A second enablement axis would leave the gateway-fronted `trusted-headers` deployment, where the browser flow cannot be enabled at all, outside a control its own forgery case needs, and it would cost a non-browser client nothing either way. The gate is stated in this section because the browser flow is what makes a browser-borne credential reachable in the first place; the predicate itself names no deployment.
- **Sign-out is inside the gate.** Sign-out answers on `POST`, so the method predicate covers it. A forged sign-out is a denial of service against a signed-in operator, so a sign-out the gate refuses returns `403` `auth.csrf_invalid` before the handler runs and clears no cookie.
- **Sign-in and the callback are outside the gate.** Both answer on `GET`, so the method predicate leaves them outside it, and the exclusion is stated by name as well, so that an implementation that widens the method predicate does not pull them in. Each is a top-level navigation, and a browser that already holds `__Host-podium_session` from an earlier sign-in sends that cookie on both, so under an unqualified predicate every re-sign-in would be refused, no session would ever be established for that browser, and no recovery would remain, since re-running sign-in is the only recovery an expired session has. What binds both routes is the single-use pre-authorization transaction carrying `state` and the PKCE verifier in `__Host-podium_auth` (§7.3.4), which refuses exactly the forged and replayed callbacks a same-origin check would. A forced cross-origin sign-in can do no more than replace the victim's own `__Host-podium_auth` cookie with a transaction the registry mints for that same browser, which the victim's own IdP session then completes.
- **Cookies.** The gate sets no cookie and reads none. `SameSite` is a defense in depth on the cookies this flow sets rather than the control the gate rests on, which is why the evidence check does not consult it.

The gate carries no request-side value. It is the browser-origin evidence check above and nothing else: it sets no cookie, requires no header, and mints no server-stored token, so it reads and writes no state. A double-submit cookie and its matching request header are deliberately absent. Keyed to authentication by `__Host-podium_session`, such a value would attach to no forged request, because that cookie is `SameSite=Lax` and a `SameSite=Lax` cookie reaches a cross-site request only on a top-level navigation with a safe method, and the same-site cross-origin forgery it would otherwise reach is already refused twice by the evidence check, once on `Sec-Fetch-Site: same-site` and once on the `Origin` host comparison. On a browser old enough to send neither header it is not a control at all, because a stateless double submit carries nothing a server-side comparison could distinguish from a value the registry issued, and its only control is the `__Host-` prefix that such a browser does not enforce. Against no incremental refusal it would cost a page-readable value on an origin that renders author-controlled markup, and a failure mode in which a browser holding a live session and no such cookie is refused on every write with no recovery.

## 6.4 Workspace Local Overlay

The workspace local overlay is a per-developer set of artifact packages (`ARTIFACT.md` for every type, plus `SKILL.md` for skills) and `DOMAIN.md` files that merge as the **highest-precedence layer in the caller's effective view** (§4.6). It's the path most teams use for in-progress work that isn't ready to share.

**Path resolution.** Every consumer (MCP server, `podium sync`, language SDKs) honors the same lookup:

1. `PODIUM_OVERLAY_PATH` if set (`Client(overlay_path=...)` on the SDK takes precedence over the env var).
2. The MCP server falls back to MCP roots when available: the `roots/list` response identifies the workspace, and the overlay defaults to `<workspace>/.podium/overlay/` if that directory exists.
3. `podium sync` and the SDK fall back to `<CWD>/.podium/overlay/` if that directory exists.
4. Otherwise: layer disabled.

The MCP server watches the resolved path via fsnotify and re-indexes on change. `podium sync` reads it once per invocation and again on each watcher event when `--watch` is set. The SDK reads it on each `Client.search_artifacts` and `Client.load_artifact` call (cached for the duration of a `session_id`).

Format: same `ARTIFACT.md` (plus `SKILL.md` for skills) and frontmatter as the registry; merge semantics are identical to registry-side layers.

The workspace local overlay is **orthogonal to the registry-side `local` source type** (§4.6): the workspace overlay is merged in by the consumer (MCP server, sync, or SDK) and is visible only to the developer running it, while a registry-side `local`-source layer is read by the registry process and surfaced to whichever identities the layer's visibility declaration allows.

To promote a workspace artifact to a shared layer, copy it into the appropriate Git repo (or registry-side `local` path), commit, and merge.

### 6.4.1 Local Search Index

When `LocalOverlayProvider` is configured, the MCP server maintains a local BM25 index over local-overlay manifest text. `search_artifacts` calls fan out to both the registry and the local index; the MCP server fuses results via reciprocal rank fusion before returning.

The default is BM25-only. Local artifacts have lower recall on semantic queries than registry artifacts, which is acceptable for the developer iteration loop where the goal is "find my draft," not "outrank everything else." Authors who want better local recall can configure the MCP server with an external embedding provider and a vector store via the `LocalSearchProvider` SPI (§9.1). Backends include `sqlite-vec` (embedded, single-file; matching the standalone registry's default in §13.10), a local pgvector instance, or a managed service (Pinecone, Weaviate Cloud, Qdrant Cloud). Cost and identity for any external service are the operator's to manage.

## 6.5 Cache

Disk cache at `${PODIUM_CACHE_DIR}/<sha256>/`. Two cache layers:

- **Resolution cache.** Maps `(id, "latest")` to `semver`. TTL 30s by default. Revalidated via HEAD on hit when `PODIUM_CACHE_MODE=always-revalidate`.
- **Content cache.** Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition).

Cache modes (set at server startup via `PODIUM_CACHE_MODE`):

- `always-revalidate` (default): HEAD-revalidate the resolution cache on every call.
- `offline-first`: use cached resolution and content if present; only call the registry on miss.
- `offline-only`: never call the registry; cache only.

Index DB: BoltDB or SQLite. `podium cache prune` for cleanup.

In contexts where the home directory is ephemeral, the host points `PODIUM_CACHE_DIR` at an ephemeral or shared volume.

## 6.6 Materialization

On `load_artifact(<id>)`, the registry returns the canonical manifest body inline (or via presigned URL if above the inline cutoff) and presigned URLs for bundled resources. Materialization on the MCP server runs in five steps:

1. **Fetch.** The MCP server downloads each resource (or reads it from the cache) into a temporary staging area. On 403/expired during fetch, retries with a fresh URL set (max 3 attempts, exponential backoff).
2. **Verify.** Signature verification (per `PODIUM_VERIFY_SIGNATURES`) and content-hash match. Bundle contents (scripts, configs, SBOMs) are not introspected; vulnerability scanning is a CI/CD concern per §1.1.
3. **Adapt.** The configured `HarnessAdapter` (§6.7) translates the canonical artifact into the harness's native layout (file names, frontmatter conventions, directory structure) without changing the underlying bytes of bundled resources unless the adapter declares it needs to.
4. **Hook.** Configured `MaterializationHook` plugins (§9.1) run in declared order over the adapter's output, with read+rewrite access to per-file bytes plus the manifest for context. Each hook can rewrite a file, drop it, or emit warnings; the next hook receives the previous hook's output. No-op when no hooks are configured. Hooks share the adapter sandbox contract (§6.7): no network, no subprocess, no writes outside the materialization destination.
5. **Write.** The MCP server writes the adapted output atomically to a host-configured destination path (`.tmp` + rename), ensuring the destination either contains a complete copy or nothing.

The destination path comes from the host, either via `PODIUM_MATERIALIZE_ROOT` or per-call in the `load_artifact` arguments.

When `PODIUM_HARNESS=none` (the default), step 3 is a no-op: the canonical layout is written directly. Hosts that want raw artifacts (build pipelines, evaluation harnesses, custom scripts) leave the adapter unset. The hook step (4) runs whether or not an adapter is configured.

## 6.7 Harness Adapters

The `HarnessAdapter` translates a canonical artifact into the format a specific harness expects. It runs at materialization time on the MCP server, between fetch and write.

**Supported harnesses.** The harnesses below ship with a built-in adapter. Each adapter value is selected via `PODIUM_HARNESS` (or via the per-call `harness:` argument). For per-harness specifics about skills, hooks, plugins, and other harness-native concepts, refer to the harness's own documentation; the harness's documentation is the source of truth.

| Adapter value    | Harness | Documentation |
| ---------------- | --- | --- |
| `none`           | Generic / raw output. No harness-specific translation. | n/a |
| `claude-code`    | Anthropic Claude Code (CLI). | [code.claude.com/docs](https://code.claude.com/docs/) |
| `claude-desktop` | Anthropic Claude Desktop (desktop chat app). | [claude.com/download](https://claude.com/download), [Skills in Claude](https://support.claude.com/en/articles/12512180-use-skills-in-claude) |
| `claude-cowork`  | Anthropic Claude Cowork (web product for organizations, claude.ai). | [claude.com/plugins](https://claude.com/plugins), [Manage Cowork plugins](https://support.claude.com/en/articles/13837433-manage-claude-cowork-plugins-for-your-organization) |
| `cursor`         | Cursor IDE. | [cursor.com/docs](https://cursor.com/docs) |
| `codex`          | OpenAI Codex (CLI and IDE). | [developers.openai.com/codex](https://developers.openai.com/codex) |
| `gemini`         | Google Gemini CLI. | [geminicli.com/docs](https://geminicli.com/docs) |
| `opencode`       | OpenCode. | [opencode.ai/docs](https://opencode.ai/docs) |
| `pi`             | Pi (pi-mono coding agent). | [github.com/badlogic/pi-mono](https://github.com/badlogic/pi-mono/tree/main/packages/coding-agent) |
| `hermes`         | Hermes Agent (Nous Research). | [hermes-agent.nousresearch.com/docs](https://hermes-agent.nousresearch.com/docs/) |

The adapter set grows as new harnesses appear. Custom adapters register through the `HarnessAdapter` SPI (§9.1).

**Adapter outputs.** Each adapter writes an artifact into the target harness's native location for that type, using one of these mechanisms:

- **Standalone file.** The artifact becomes a discrete file, or a skill folder, that the harness discovers natively. This covers most cells in the matrix below.
- **Bundled resources.** A skill's `scripts/`, `references/`, and `assets/` subfolders ship verbatim inside the skill's native folder. Reference material that belongs to a skill travels this way as part of the skill package (§4.4); it is not a separate artifact.
- **Inject.** The artifact body is merged into a shared always-loaded instruction file (`AGENTS.md` or `GEMINI.md`) between Podium-managed markers, so a later sync reconciles only Podium's section and leaves the rest of the file intact.
- **Config-merge.** A `hook` or `mcp-server` artifact is merged into the harness's shared configuration file as a Podium-owned entry keyed by the artifact ID. The adapter emits a structured fragment and the materialization layer performs the read-merge-write, so the operator's other entries are preserved and removing the artifact removes its entry.

**Adapter modes.** An adapter has a project-files mode and, for a harness with a git-repo distribution, a marketplace mode. The project-files mode runs when `podium sync` renders a `kind: workspace` target, writing the harness's native project-level files into a workspace, and the MCP server consumes it through `load_artifact`. The target-path table below grades the project-files mode. A harness with a git-repo distribution also has a marketplace, extension, package, or tap mode selected when `podium sync` renders a `kind: marketplace` target (§7.8), which renders the catalog into a repository the harness imports. The `claude-cowork` project-files materialization no longer emits the plugin and marketplace layout; that layout moves to publishing (§7.8), and `claude-cowork` consumes the published Claude marketplace.

A `✗` cell means the harness has no native concept for that type, or the only native location is outside the materialization target. Materialization writes project-level files only, so a surface that exists solely at user or operating-system scope is `✗` here. The §6.7.1 capability matrix grades each `✗` cell. When an artifact declares `target_harnesses:`, ingest lint errors against a `✗` cell for a named harness and warns against a `⚠` cell. When `target_harnesses:` is absent, ingest stays permissive and the `✗` cell is enforced at materialization (§6.9) against the harness the artifact loads onto. An author scopes a non-portable artifact to the harnesses it supports with `target_harnesses:`.

`type: context` has no native concept in any harness. A `type: context` artifact materializes to a harness-neutral `.podium/context/<artifact-id>/` directory, identical across every adapter. Reference material that belongs to a skill is shipped as that skill's bundled `references/` resources instead, not as a separate context artifact.

The target for each remaining type, by adapter (paths are project-relative; `<n>` is the artifact's leaf name):

| Type         | claude-code               | claude-desktop | claude-cowork        | cursor                     | codex                  | opencode                    | gemini                     | pi                  | hermes                |
| ------------ | ------------------------- | -------------- | -------------------- | -------------------------- | ---------------------- | --------------------------- | -------------------------- | ------------------- | --------------------- |
| `skill`      | `.claude/skills/<n>/SKILL.md` | ✗          | ✗ | `.cursor/skills/<n>/SKILL.md` | `.agents/skills/<n>/SKILL.md` | `.opencode/skills/<n>/SKILL.md` | `.gemini/skills/<n>/SKILL.md` | `.pi/skills/<n>/SKILL.md` | ✗                |
| `agent`      | `.claude/agents/<n>.md`   | ✗              | ✗      | `.cursor/agents/<n>.md`    | `.codex/agents/<n>.toml` | `.opencode/agents/<n>.md` | `.gemini/agents/<n>.md`    | ✗                   | ✗                     |
| `command`    | `.claude/commands/<n>.md` | ✗              | ✗    | `.cursor/commands/<n>.md`  | ✗                      | `.opencode/commands/<n>.md` | `.gemini/commands/<n>.toml` | `.pi/prompts/<n>.md` | ✗                    |
| `rule`       | `.claude/rules/<n>.md`    | ✗              | ✗ | `.cursor/rules/<n>.mdc`    | `AGENTS.md` (inject)   | `AGENTS.md` (inject)        | `GEMINI.md` (inject)       | `AGENTS.md` (inject) | `.cursor/rules/<n>.mdc` |
| `hook`       | `settings.json` (cfg)     | ✗              | ✗   | `.cursor/hooks.json` (cfg) | `config.toml` (cfg)    | ✗                       | `settings.json` (cfg)      | ✗                   | ✗                     |
| `mcp-server` | `.mcp.json` (cfg)         | ✗              | ✗          | `.cursor/mcp.json` (cfg)   | `config.toml` (cfg)    | `opencode.json` (cfg)       | `settings.json` (cfg)      | ✗                   | ✗                     |

`none` writes the canonical layout (`ARTIFACT.md`, `SKILL.md`, bundled resources) without translation. The config-merge targets resolve to the harness's project-scope config file: `.claude/settings.json`, `.cursor/hooks.json` and `.cursor/mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, and root `opencode.json`.

Notes on partial and migrating surfaces:

- Codex custom prompts are user-scope (`~/.codex/prompts/`) and are being folded into skills, so `command` is `✗` for Codex. Cursor is likewise folding command files into skills; authors targeting it should prefer `type: skill`.
- Hermes reads project-level `.cursor/rules/*.mdc`, `AGENTS.md`, and `.cursorrules`, so its `rule` output reuses the Cursor `.mdc` format. Its skill, command, hook, and MCP surfaces live under user-scope `~/.hermes/`, so they are `✗` for project-level materialization.
- Claude Desktop configures MCP servers at user or operating-system scope (`claude_desktop_config.json`, or a `.mcpb` bundle) and exposes no project-level surface, so project materialization produces no Claude Desktop output. Configure it out of band.
- OpenCode hooks are JavaScript or TypeScript plugin modules and Pi hooks are extension code, so `hook` is `✗` for both.

**Harness distribution formats.** A harness with a git-repo distribution is a publish target for marketplace publishing (§7.8). The table records each harness's distribution and the fixed location of its repository manifest. It records the distribution location per harness and is not graded, so it is separate from the §6.7.1 capability matrix.

| Harness | Git-repo distribution | Manifest (fixed location) | Components | Publish target |
| --- | --- | --- | --- | --- |
| `claude-code`, `claude-desktop`, `claude-cowork` | marketplace, shared format | `.claude-plugin/marketplace.json` (root) | `skills/`, `agents/`, `commands/`, `hooks/`, and `.mcp.json` | yes (one Claude marketplace) |
| `codex` | marketplace | `.agents/plugins/marketplace.json` (root) | `skills/`, `hooks/hooks.json`, `.app.json`, and `.mcp.json` | yes |
| `cursor` | team marketplace | `.cursor-plugin/marketplace.json` (root) | `skills/`, `rules/*.mdc`, and `mcp.json` | yes |
| `gemini` | extension | `gemini-extension.json` (root); one extension per repository | `commands/*.toml`, the context file, and `mcpServers` | yes |
| `pi` | git package | root `package.json` with a `pi.skills` array and a `pi-package` keyword | `skills/<name>/SKILL.md` | yes |
| `hermes` | skills tap | no root manifest; skills discovered under `skills/` | `skills/<name>/SKILL.md` with `references/`, `scripts/`, and `assets/` | yes |
| `opencode` | none (npm packages only) | n/a | n/a | no |
| `none` | none (raw canonical output) | n/a | n/a | no |

Claude Code, Claude Desktop, and Claude Cowork read the same `.claude-plugin/marketplace.json`, so one Claude marketplace serves all three. The Claude, Codex, and Cursor manifests sit at distinct fixed locations, so they do not collide and can coexist in one repository, each read only by its own harness. OpenCode distributes through npm packages and `none` writes raw canonical output, so neither is a publish target.

`claude-cowork` is a publish consumer of the Claude marketplace. Its only remaining project-scope materialization output is the `type: context` directory. A plugin-layout type on `claude-cowork` (`skill`, `agent`, `command`, `rule`, `hook`, or `mcp-server`) is a `✗` cell, which both `podium sync` and `load_artifact` fail per §6.9. A cowork user obtains those artifacts by importing the published Claude marketplace.

**What an adapter does.** Mechanical translation:

- Frontmatter mapping (canonical fields → harness equivalents)
- Prose body composition (canonical body → harness's system-prompt section)
- Resource layout (bundled resources → paths the harness expects)
- Type-specific behavior (`type: skill` → skill; `type: agent` → agent definition)

**What an adapter does not do.** Adapters do not invent semantics. Fields the harness has no equivalent for are left out (or carried in an `x-podium-*` extension namespace if the harness tolerates one).

**Plugin descriptor.** The adapter input carries a plugin descriptor: the plugin name, an optional description, and the harness subtree prefix. A marketplace emitter (§7.8) uses it to render an artifact into a named plugin under the harness subtree and to contribute the per-plugin manifest entry keyed by the plugin name. The project-files mode leaves the descriptor unset.

**Configuration per call.** Hosts can override the harness for a single `load_artifact` call by passing `harness: <value>` in the call arguments.

**Adapter sandbox contract.** Adapters MUST be no-network, MUST NOT write outside the materialization destination, MUST NOT spawn subprocesses. Enforced where Go runtime restrictions allow; documented as the contract for community adapters; conformance suite includes negative tests.

**Cache behavior.** The cache stores canonical artifact bytes (§6.5). Adapter output is regenerated on each materialization by default. An optional in-memory memo cache keyed on `(content_hash, harness)` with 5-minute TTL is enabled for sessions that load the same artifact repeatedly.

**Conformance test suite.** Every built-in adapter is covered by the §11 materialization suite: a canonical artifact set is materialized through each adapter, the exact harness-native output is pinned (paths and file contents) against golden files, each produced file is checked to parse and satisfy the target harness's config schema (JSON config keys, TOML tables, `SKILL.md` and `.mdc` frontmatter), and re-materialization into the same tree is asserted idempotent. Driving a real harness binary against the materialized output to spawn an agent end-to-end is an opt-in integration check that runs only where the harness binary is available.

**Versioning.** Adapter behavior is versioned alongside the MCP server binary. Profile and harness combinations that need a newer adapter behavior pin a minimum MCP server version; older binaries refuse to start.

### 6.7.1 The Author's Burden

Adapters can only translate features the target harness supports. Authors who use harness-specific features will get degraded materializations elsewhere.

Two mitigations:

1. **Core feature set.** A documented subset of canonical fields and patterns that the project-scope adapters support natively. Authors writing to the core feature set get author-once, load-anywhere materialization across the harnesses with a project-level surface. A harness configured out of band (claude-desktop has no project-scope surface) sits outside this subset, so an artifact that targets it relies on `target_harnesses:` rather than the core set.
2. **Capability matrix.** A compatibility table maintained alongside the adapters. Ingest-time lint surfaces capability mismatches: "field `X` is used but adapter `cursor` cannot translate it."

Authors who must use a non-portable feature can declare `target_harnesses:` in frontmatter to opt out of cross-harness materialization for that artifact.

**Capability matrix (maintained in sync with adapter implementations).** Legend: ✓ supported natively, ⚠ supported via fallback (ingest lint warns when a declared `target_harnesses:` names the harness, and materialization emits the degraded fallback), ✗ not supported (ingest lint errors when a declared `target_harnesses:` names the harness, and materialization onto that harness otherwise fails per §6.9). When `target_harnesses:` is absent, ingest applies no capability diagnostic and the per-harness grade is enforced at materialization. The matrix grades type-level materialization, frontmatter-field fidelity, and the `rule_mode` and `hook_event` rows. `rule` and `hook` are graded by their dedicated rows rather than a type row.

Type materialization (can the harness materialize an artifact of this type at project scope):

| Type         | claude-code | claude-desktop | claude-cowork | cursor | codex | opencode | gemini | pi  | hermes |
| ------------ | ----------- | -------------- | ------------- | ------ | ----- | -------- | ------ | --- | ------ |
| `skill`      | ✓           | ✗              | ✗             | ✓      | ✓     | ✓        | ✓      | ✓   | ✗      |
| `agent`      | ✓           | ✗              | ✗             | ✓      | ✓     | ✓        | ✓      | ✗   | ✗      |
| `context`    | ✓           | ✗              | ✓             | ✓      | ✓     | ✓        | ✓      | ✓   | ✓      |
| `command`    | ✓           | ✗              | ✗             | ✓      | ✗     | ✓        | ✓      | ✓   | ✗      |
| `mcp-server` | ✓           | ✗              | ✗             | ✓      | ✓     | ✓        | ✓      | ✗   | ✗      |

Frontmatter-field fidelity, measured on a `type: agent` carrier (does the field survive the harness's agent output):

| Field              | claude-code | claude-desktop | claude-cowork | cursor | codex | opencode | gemini | pi  | hermes |
| ------------------ | ----------- | -------------- | ------------- | ------ | ----- | -------- | ------ | --- | ------ |
| `description`      | ✓           | ✗              | ✗             | ✓      | ✓     | ✓        | ✓      | ✗   | ✗      |
| `mcpServers`       | ✓           | ✗              | ✗             | ✓      | ✗     | ✓        | ✓      | ✗   | ✗      |
| `delegates_to`     | ✓           | ✗              | ✗             | ✓      | ✗     | ✓        | ✓      | ✗   | ✗      |
| `requiresApproval` | ✓           | ✗              | ✗             | ✓      | ✗     | ✓        | ✓      | ✗   | ✗      |
| `sandbox_profile`  | ✓           | ✗              | ✗             | ✓      | ✗     | ✓        | ✓      | ✗   | ✗      |

A field row records whether the value survives the harness's agent materialization. The pass-through `.md` agents (claude-code, cursor, opencode, gemini) preserve every field; the Codex TOML agent keeps `name` and `description` and drops the rest; a harness with no project-level agent surface (claude-desktop, claude-cowork, pi, hermes) drops the row.

Rule modes (`type: rule`) and hook events (`type: hook`):

| Capability            | claude-code | claude-desktop | claude-cowork | cursor | codex | opencode | gemini | pi  | hermes |
| --------------------- | ----------- | -------------- | ------------- | ------ | ----- | -------- | ------ | --- | ------ |
| `rule_mode: always`   | ✓           | ✗              | ✗             | ✓      | ✓     | ✓        | ✓      | ✓   | ✓      |
| `rule_mode: glob`     | ✓           | ✗              | ✗             | ✓      | ⚠     | ⚠        | ⚠      | ⚠   | ✓      |
| `rule_mode: auto`     | ⚠           | ✗              | ✗             | ✓      | ⚠     | ⚠        | ⚠      | ⚠   | ✓      |
| `rule_mode: explicit` | ⚠           | ✗              | ✗             | ✓      | ⚠     | ⚠        | ⚠      | ⚠   | ✓      |
| `hook_event` (any)    | ✓           | ✗              | ✗             | ⚠      | ✓     | ✗        | ✓      | ✗   | ✗      |

A rule mode is ✓ where the adapter produces native per-file or per-mode scoping, and ⚠ where the rule degrades to an always-loaded block. Cursor and Hermes write `.cursor/rules/*.mdc`, which carry every mode natively. Claude Code writes `.claude/rules/` files: `always` loads at launch (native), `glob` uses the native `paths:` list (native), and `auto` and `explicit` fall back to a load-always file because Claude Code has no description-attach or mention-only rule mode. The `AGENTS.md` and `GEMINI.md` inject harnesses (codex, opencode, gemini, pi) honor `always` natively and degrade the scoped modes to the always-loaded block.

The `hook_event` row summarizes hook support at the field level. Per-event coverage (which canonical events from §4.3.5 each adapter translates) is tracked in the adapter implementation rather than in this spec; the row marks ✓ when the adapter config-merges the common events (`session_start`, `session_end`, `pre_tool_use`, `post_tool_use`, `stop`, `pre_compact`) and ⚠ when only a subset translates. Cursor is ⚠ because it exposes per-category subtype events (`beforeShellExecution` and the like) rather than the generic tool events. For the harness's own current event surface, refer to the harness's documentation.

## 6.8 Process Model

The MCP server is a stdio subprocess spawned by its host. The host is responsible for lifecycle (spawn, signal handling, shutdown).

- **Developer hosts.** One subprocess per workspace, spawned when the workspace opens and torn down when the workspace closes.
- **Managed agent runtimes.** One subprocess per session, spawned by the runtime's bootstrap glue alongside the agent.

## 6.9 Failure Modes

| Failure                                       | Behavior                                                                                                                                                    |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Registry offline                              | Serve from cache; return explicit "offline" status on fresh `load_domain` / `search_domains` / `search_artifacts`.                                                             |
| Workspace overlay path missing                | Skip the workspace local overlay; warn once.                                                                                                                |
| Auth token expired (`oauth-device-code`)      | Trigger refresh; if interactive refresh required, surface in tool response with reauth instructions via MCP elicitation.                                    |
| Auth token expired (`injected-session-token`, `oidc-jwt`) | Reject with `auth.token_expired`. The host's runtime is responsible for refresh; under `oidc-jwt` a gateway-forwarded token is replaced by the gateway, and a browser session is renewed by signing in again (§6.3.4). |
| Untrusted runtime (`injected-session-token`)  | Reject with `auth.untrusted_runtime`. The deployment adds the runtime's signing key to the registry's trusted key set (§6.3.2) and restarts the registry. |
| Untrusted token (`oidc-jwt`)                  | Reject with `auth.untrusted_token`. The token failed signature, `iss`, or `aud` validation against the accepted issuers and the configured audience (§6.3.3), in either accepted credential location.                       |
| Verified token names no tenant (`oidc-jwt`)   | Reject with `auth.tenant_unknown`. The token verified, but its `org_id` names no provisioned tenant on a multi-tenant registry.                              |
| Cross-site browser-origin evidence on a state-changing request, or a browser sign-in callback whose pre-authorization transaction does not validate | Reject with `403` `auth.csrf_invalid`. §6.3.4 states each predicate. |
| Browser sign-in code exchange refused by the IdP | Reject with `502` `auth.exchange_failed`. The IdP answered the exchange and refused it; the operator checks the configured client credential and the registered redirect URI (§6.3.4). |
| Visibility denial on a call                   | Return a structured error naming the unreachable resource (without leaking the layer's existence); log to the registry audit stream as `visibility.denied`. |
| Materialization destination unwritable        | Fail the `load_artifact` call with a structured error; nothing partial is left on disk.                                                                     |
| Signature verification failure                | Fail with `materialize.signature_invalid`; do not write to disk.                                                                                            |
| Unknown `PODIUM_HARNESS` value                | Refuse to start; CLI lists the available adapter values.                                                                                                    |
| Adapter cannot translate an artifact          | Fail with structured error naming the missing translation; suggest `harness: none` for raw output.                                                          |
| Binary version mismatch with host caller      | Refuse to start; host's CLI prompts an update.                                                                                                              |
| MCP protocol version mismatch                 | Negotiate down to host's max supported MCP version; if no compatible version, fail with `mcp.unsupported_version`.                                          |
| Quota exhausted                               | Structured error (`quota.storage_exceeded` etc.); operation rejected.                                                                                       |
| Runtime requirement unsatisfiable             | Fail with `materialize.runtime_unavailable`; lists the unsatisfied requirement.                                                                             |

## 6.10 Error Model

All errors use a structured envelope:

```json
{
  "code": "auth.untrusted_runtime",
  "message": "Runtime 'managed-runtime-x' is not registered with the registry.",
  "details": { "runtime_iss": "managed-runtime-x" },
  "retryable": false,
  "suggested_action": "Add the runtime's signing key with 'podium admin runtime register --keys-file', then restart the registry."
}
```

The registry-process providers (§6.3.3) add `auth.*` codes. `auth.token_expired` reports an expired `injected-session-token` token, or an expired `oidc-jwt` token in either accepted credential location; it carries no `details`, because the expiry is reported without an issuer field:

```json
{
  "code": "auth.token_expired",
  "message": "The authenticated token has expired.",
  "retryable": false,
  "suggested_action": "Refresh the token. For 'injected-session-token' the runtime reissues it; for 'oidc-jwt' a gateway forwards a new token, and a browser session is renewed by signing in again."
}
```

`auth.untrusted_token` reports an `oidc-jwt` token that failed signature, `iss`, or `aud` verification, in either accepted credential location. `details.token_iss` carries the rejected token's issuer, distinct from the runtime code's `details.runtime_iss`:

```json
{
  "code": "auth.untrusted_token",
  "message": "Token from issuer 'https://acme.okta.com/oauth2/default' failed verification.",
  "details": { "token_iss": "https://acme.okta.com/oauth2/default" },
  "retryable": false,
  "suggested_action": "Verify the token reaching the registry comes from the issuer and audience configured for 'oidc-jwt' (PODIUM_OAUTH_ISSUER, PODIUM_OAUTH_AUDIENCE). A gateway-forwarded token is corrected at the gateway; a browser session is re-established by signing in again."
}
```

`auth.tenant_unknown` reports a verified `oidc-jwt` token whose `org_id` names no provisioned tenant on a multi-tenant registry. The token verified, so the failure is tenancy rather than authentication: `details.token_org_id` carries the unresolved organization value:

```json
{
  "code": "auth.tenant_unknown",
  "message": "Verified token names organization 'globex' which is not a provisioned tenant.",
  "details": { "token_org_id": "globex" },
  "retryable": false,
  "suggested_action": "Provision the organization as a tenant, or forward a token whose org_id claim names an existing tenant."
}
```

The browser acquisition flow (§6.3.4) adds `auth.*` codes. `auth.csrf_invalid` is answered with `403` and covers two refusals on one axis: a state-changing request the §6.3.4 browser-origin gate refuses, which the registry answers before the handler runs and whatever credential authenticated the request, and a browser sign-in callback the single-use pre-authorization transaction refuses. §6.3.4 states each predicate. The two refusals are disjoint by route, because the sign-in and callback routes are outside the gate:

```json
{
  "code": "auth.csrf_invalid",
  "message": "The request was refused because it did not pass the browser-origin check.",
  "retryable": false,
  "suggested_action": "Reload the web UI and retry the operation from it; if the registry is behind a gateway, pass the browser-facing Host header through unrewritten."
}
```

`auth.exchange_failed` is answered with `502` and reports a browser sign-in callback whose code exchange the IdP answered and refused, such as an `invalid_grant` response or a refusal caused by a wrong client credential. It is a permanent failure for that request, so its envelope carries `retryable: false`. An IdP the registry could not reach for the exchange, and one whose token endpoint answered with a `5xx`, are transient failures against a dependency the registry called and are reported with `registry.unavailable` instead:

```json
{
  "code": "auth.exchange_failed",
  "message": "The identity provider refused the authorization code exchange.",
  "retryable": false,
  "suggested_action": "Check the configured OAuth client credential and that the redirect URI the registry sends is registered with the identity provider for this client."
}
```

The tenant-management endpoints (§7.3.3) reuse existing codes for authorization and lookup and add one code in the `registry.*` namespace. A request without operator authorization is rejected with `auth.forbidden`, the code the per-tenant admin endpoints already return. A `PATCH` or `DELETE` naming an unknown tenant is rejected with `registry.tenant_not_found`, the code `GetTenant` already surfaces. A `POST`, `PATCH`, or `DELETE /v1/admin/tenants` request on a read-only registry (§13.2.1) is rejected with `registry.read_only`, the code every other write endpoint returns; the registry checks this after operator authorization and before mutating the store. `registry.tenant_management_unavailable` reports a `/v1/admin/tenants` request on a single-tenant or standalone backend (§13.10), where multi-tenancy is out of scope; it joins the existing `registry.*` namespace and introduces no new namespace:

```json
{
  "code": "registry.tenant_management_unavailable",
  "message": "Tenant management requires a multi-tenant registry started with PODIUM_MULTI_TENANT.",
  "retryable": false,
  "suggested_action": "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) on a standard backend to manage tenants."
}
```

Codes are namespaced (`auth.*`, `config.*`, `ingest.*`, `materialize.*`, `quota.*`, `mcp.*`, `network.*`, `registry.*`, `domain.*`). Mapped to MCP error payloads per the MCP spec. A meta-tool error envelope is returned from `tools/call` as the `CallToolResult` carrying `isError: true`, with the envelope under `structuredContent` (§6.1.1).

## 6.11 Host Configuration Recipes

The Podium MCP server is a stdio binary the host spawns alongside its other MCP servers. Each host has its own MCP config format; the snippets below show what to add for the common harnesses. All three reuse the same env-var contract from §6.2.

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS; equivalents on Windows/Linux):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-desktop"
      }
    }
  }
}
```

**Claude Code** (project-level `.claude/mcp.json` or user-level `~/.claude/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-code",
        "PODIUM_OVERLAY_PATH": "${WORKSPACE}/.podium/overlay/"
      }
    }
  }
}
```

**Cursor** (Settings → MCP, or `~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "cursor"
      }
    }
  }
}
```

**OpenCode** (`opencode.json` at the project root or `~/.config/opencode/opencode.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "opencode"
      }
    }
  }
}
```

**Pi** (`~/.pi/mcp.json` or project-local `.pi/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "pi"
      }
    }
  }
}
```

**Hermes** (`~/.config/hermes/mcp.json` or project-local `.hermes/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "hermes"
      }
    }
  }
}
```

**Standalone (no env override).** When `podium serve` has auto-bootstrapped `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080` (§13.10), or `podium init --global --standalone` has written it explicitly (§7.7), the MCP server resolves the registry from there and the env var can be omitted.

For other MCP-speaking hosts (custom runtimes, non-major harnesses), the same snippet pattern applies; `PODIUM_HARNESS=none` writes the canonical layout when no harness-specific adapter is configured.
