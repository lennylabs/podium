package server

// spec: SS 6.10 — the structured error envelope carries a `retryable`
// flag and a `suggested_action` remediation hint. errorCodeMeta is the
// per-code source of truth for both so every emission path (writeError,
// writeErrorDetails, writeQuotaError, writeCoreError, and the batch-load
// errorEnvelopeFor) reports them consistently rather than leaving them
// unset.
type errorCodeMeta struct {
	// retryable reports whether the condition clears on its own so the
	// caller may retry the same request without operator action.
	retryable bool
	// suggestedAction is the operator remediation hint. Empty when the
	// code has no single actionable remediation.
	suggestedAction string
}

// errorCodeRegistry maps a §6.10 namespaced code to its envelope
// defaults. Codes absent from the map default to retryable=false and an
// empty suggested_action. Entries cover both codes the server emits over
// HTTP today and the spec's canonical examples (auth.untrusted_runtime,
// quota.storage_exceeded) so the remediation text is defined in one
// place if any handler emits them.
var errorCodeRegistry = map[string]errorCodeMeta{
	// Transient conditions: the caller may retry.
	"registry.unavailable": {
		retryable:       true,
		suggestedAction: "Retry the request; if the condition persists, check the registry's health endpoint and logs.",
	},
	"quota.search_qps_exceeded": {
		retryable:       true,
		suggestedAction: "Reduce the search request rate or raise the tenant's search QPS quota.",
	},
	"quota.materialize_rate_exceeded": {
		retryable:       true,
		suggestedAction: "Reduce the load_artifact request rate or raise the tenant's materialize quota.",
	},
	// spec §13.2.1: read-only mode is a transient state the registry
	// leaves automatically once the Postgres primary is reachable again,
	// so a write rejected with registry.read_only succeeds on retry once
	// the registry recovers, with no operator action required.
	"registry.read_only": {
		retryable:       true,
		suggestedAction: "Retry the write once the registry leaves read-only mode; reads continue to serve from the replica.",
	},
	// spec §7.3.1 ingest-cases: "Same version, different content_hash |
	// Rejected as ingest.immutable_violation. The author bumps the version."
	// A stored (artifact_id, version) is immutable (§4.7), so retrying the
	// same content never succeeds; the remediation is a version bump.
	"ingest.immutable_violation": {
		suggestedAction: "Bump the artifact version; an existing version's content is immutable.",
	},
	// Hard caps: retrying without operator action does not succeed.
	"quota.layer_count_exceeded": {
		suggestedAction: "Remove an existing user-defined layer or raise the tenant's layer quota.",
	},
	"quota.storage_exceeded": {
		suggestedAction: "Remove unused artifacts or raise the tenant's storage quota.",
	},
	// The spec's canonical §6.10 example: an injected-session-token whose
	// issuer is not a registered runtime key. The remediation text is the
	// verbatim spec string.
	"auth.untrusted_runtime": {
		suggestedAction: "Add the runtime's signing key with 'podium admin runtime register --keys-file', then restart the registry.",
	},
	// §6.3.3 / §6.10: the registry-process oidc-jwt provider's codes. The
	// remediation names both accepted credential locations, because the
	// registry verifies the same token whether a gateway forwarded it in the
	// configured token header or the registry obtained it through the §6.3.4
	// exchange and returned it in the session cookie.
	"auth.token_expired": {
		suggestedAction: "Refresh the token. For 'injected-session-token' the runtime reissues it; for 'oidc-jwt' a gateway forwards a new token, and a browser session is renewed by signing in again.",
	},
	"auth.untrusted_token": {
		suggestedAction: "Verify the token reaching the registry comes from the issuer and audience configured for 'oidc-jwt' (PODIUM_OAUTH_ISSUER, PODIUM_OAUTH_AUDIENCE). A gateway-forwarded token is corrected at the gateway; a browser session is re-established by signing in again.",
	},
	"auth.tenant_unknown": {
		suggestedAction: "Provision the organization as a tenant, or forward a token whose org_id claim names an existing tenant.",
	},
	// §6.3.4 / §6.10: the browser acquisition flow's codes. Both are
	// permanent for the request that took them, so neither is retryable.
	// auth.csrf_invalid covers the browser-origin gate's refusal and the
	// sign-in callback's pre-authorization refusal on one axis.
	"auth.csrf_invalid": {
		suggestedAction: "Reload the web UI and retry the operation from it; if the registry is behind a gateway, pass the browser-facing Host header through unrewritten.",
	},
	"auth.exchange_failed": {
		suggestedAction: "Check the configured OAuth client credential and that the redirect URI the registry sends is registered with the identity provider for this client.",
	},
	// §7.3.3: tenant management is a multi-tenant-only capability. A
	// single-tenant or standalone registry has no additional tenant to manage,
	// so the operation never succeeds without changing the deployment mode.
	"registry.tenant_management_unavailable": {
		suggestedAction: "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) on a standard backend to manage tenants.",
	},
}

// enrichEnvelope fills the retryable flag and suggested_action from the
// per-code registry when the caller has not already set them. An
// explicitly-set retryable=true is never downgraded, and a caller-set
// suggested_action wins over the default.
func enrichEnvelope(e *ErrorResponse) {
	meta, ok := errorCodeRegistry[e.Code]
	if !ok {
		return
	}
	if meta.retryable {
		e.Retryable = true
	}
	if e.SuggestedAction == "" {
		e.SuggestedAction = meta.suggestedAction
	}
}
