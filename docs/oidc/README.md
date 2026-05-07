# OIDC integration cookbooks

Podium uses OIDC for identity in standard deployments. The registry doesn't ship its own user database — it accepts a JWT issued by your identity provider, validates the signature against the IdP's JWKS, and reads the `sub`, `email`, and `groups` claims to determine the caller's effective view (§4.6, §6.3).

These per-IdP guides walk through the setup steps. Each guide assumes you already have a Podium registry running at a known URL (e.g., `https://podium.your-org.example`) and you're configuring authentication for it.

## What's covered

| IdP | Guide | Notes |
| --- | --- | --- |
| Okta | [`okta.md`](okta.md) | Native group claim support; SCIM available. |
| Microsoft Entra ID | [`entra-id.md`](entra-id.md) | Formerly Azure AD. Group claim emits group _IDs_, not names — extra mapping needed. |
| Google Workspace | [`google-workspace.md`](google-workspace.md) | No group claim natively in OIDC; uses Cloud Identity API or directory mapping. |
| Auth0 | [`auth0.md`](auth0.md) | Group claim via custom action / rule. |
| Keycloak | [`keycloak.md`](keycloak.md) | Self-hosted; group claim via mapper. The Compose stack uses Keycloak's sibling Dex for evaluation deployments. |

## What every guide produces

Each guide ends with a working `~/.podium/registry.yaml` snippet:

```yaml
identity:
  provider: oidc
  issuer: https://<idp-issuer-url>
  audience: podium                          # or whatever you set when registering the app
  jwks_uri: https://<idp-issuer-url>/.well-known/jwks.json
  groups_claim: groups                      # IdP-specific; see each guide
  email_claim: email                        # standard
  sub_claim: sub                            # standard
```

…and a confirmation that `podium login` against your registry from a developer machine completes the device-code flow and prints the resolved identity (`sub`, `email`, groups).

## What every guide does not cover

- **TLS termination** — assumed to be handled by your load balancer / reverse proxy.
- **Network reachability** — the registry must be able to reach the IdP's JWKS endpoint outbound, and developers must be able to reach the IdP's verification endpoint from their browser.
- **SCIM provisioning** — covered separately in [§6.3.1 of the spec](../../spec/06-mcp-server.md#631-claim-derivation). The OIDC `groups` claim mechanism described in these guides is the immediate-but-on-login path; SCIM is the push-on-change path. Use SCIM if your IdP supports it and you need group-membership changes to apply without waiting for re-login.

## Common pitfalls (across IdPs)

- **Audience mismatch.** The IdP must issue tokens with `aud` matching what the registry expects. The registry rejects with `auth.audience_mismatch` if not.
- **Clock skew.** ±60s tolerance. Run NTP on registry hosts.
- **Stale JWKS.** The registry caches the JWKS for 5 minutes. After IdP key rotation, expect up to 5 minutes of `auth.signature_invalid` for the affected key.
- **Groups claim shape.** Some IdPs emit groups as a JSON array of names; others emit IDs that you have to resolve. The registry expects an array of strings — adapt with the IdP-specific configuration described in each guide.
- **Browser-mediated flows blocked.** Some corporate networks block the device-code verification URL pattern. Test with a real developer on a real network before declaring success.

## When to use SAML instead

If your org standardizes on SAML rather than OIDC, the registry supports SAML via an OIDC bridge (a small adapter that translates SAML assertions into OIDC tokens for the registry). This is documented separately; the per-IdP guides here all assume OIDC.
