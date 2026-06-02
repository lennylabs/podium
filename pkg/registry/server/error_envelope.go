package server

// spec: SS 6.10 — the structured error envelope carries a `retryable`
// flag and a `suggested_action` remediation hint. errorCodeMeta is the
// per-code source of truth for both so every emission path (writeError,
// writeErrorDetails, writeQuotaError, writeCoreError, and the batch-load
// errorEnvelopeFor) reports them consistently rather than leaving them
// unset (F-6.10.3, F-6.10.4).
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
		suggestedAction: "Register the runtime's signing key via 'podium admin runtime register'.",
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
