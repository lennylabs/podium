package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// spec: SS 6.10 — every error the bridge returns to the MCP client is a
// structured envelope carrying the namespaced code, message, optional
// machine-readable details, retryable flag, and suggested_action. The
// human-readable summary stays on the `error` key so existing host
// tooling keeps a single string to display (F-6.10.2).

// registryError is a §6.10 envelope decoded from a registry HTTP error
// response. It implements error so it flows through the existing
// (body, error) return contract of fetchJSON, and exposes the discrete
// fields so callers can forward them without re-parsing a string.
type registryError struct {
	Status          int
	Code            string
	Message         string
	Details         map[string]any
	Retryable       bool
	SuggestedAction string
}

func (e *registryError) Error() string {
	if e.Code != "" {
		return e.Code + ": " + e.Message
	}
	return e.Message
}

// parseRegistryError decodes the §6.10 envelope from a >=400 registry
// response body. When the body is a structured envelope with a code, the
// discrete fields are preserved; otherwise the status and raw body are
// kept in the message, matching the prior "HTTP <status>: <body>" form.
func parseRegistryError(status int, body []byte) error {
	var env struct {
		Code            string         `json:"code"`
		Message         string         `json:"message"`
		Details         map[string]any `json:"details"`
		Retryable       bool           `json:"retryable"`
		SuggestedAction string         `json:"suggested_action"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Code != "" {
		return &registryError{
			Status:          status,
			Code:            env.Code,
			Message:         env.Message,
			Details:         env.Details,
			Retryable:       env.Retryable,
			SuggestedAction: env.SuggestedAction,
		}
	}
	return &registryError{Status: status, Message: fmt.Sprintf("HTTP %d: %s", status, body)}
}

// errorResult builds the §6.10 structured MCP error result from a
// message. When the message is prefixed with a namespaced code (for
// example "network.registry_unreachable: ..."), the code is split out
// and the retryable / suggested_action defaults for bridge-originated
// codes are filled in. Messages without a namespaced prefix (decode
// failures, "unknown tool", and similar internal errors) stay a bare
// {"error": "<msg>"} since they are outside the §6.10 code taxonomy.
func errorResult(msg string) map[string]any {
	code, rest := splitNamespacedCode(msg)
	if code == "" {
		return map[string]any{"error": msg}
	}
	retryable, suggested := bridgeCodeMeta(code)
	return errorEnvelope(code, rest, nil, retryable, suggested)
}

// errorResultFrom converts an error into the §6.10 structured result. A
// *registryError carries the registry-set envelope fields through
// unchanged; any other error falls back to errorResult on its message.
func errorResultFrom(err error) map[string]any {
	var re *registryError
	if errors.As(err, &re) && re.Code != "" {
		return errorEnvelope(re.Code, re.Message, re.Details, re.Retryable, re.SuggestedAction)
	}
	return errorResult(err.Error())
}

// errorEnvelope assembles the MCP error result map. `error` holds the
// human-readable "code: message" summary; the discrete envelope fields
// sit alongside it. details and suggested_action are omitted when empty.
func errorEnvelope(code, message string, details map[string]any, retryable bool, suggested string) map[string]any {
	summary := message
	if code != "" {
		summary = code + ": " + message
	}
	m := map[string]any{
		"error":     summary,
		"code":      code,
		"message":   message,
		"retryable": retryable,
	}
	if len(details) > 0 {
		m["details"] = details
	}
	if suggested != "" {
		m["suggested_action"] = suggested
	}
	return m
}

// bridgeCodeMeta returns the §6.10 retryable flag and remediation hint
// for codes the bridge originates itself. Registry-originated codes
// already carry these fields (decoded in fetchJSON), so they are not
// re-derived here.
func bridgeCodeMeta(code string) (retryable bool, suggested string) {
	switch code {
	case "network.registry_unreachable":
		return true, "Check network connectivity to the registry; the request can be retried once it is reachable."
	case "config.unknown_harness":
		return false, "Set PODIUM_HARNESS to a registered adapter identifier."
	case "materialize.signature_invalid":
		return false, "The registry served bytes whose signature did not validate; verify the artifact's signing provenance before use."
	}
	return false, ""
}

// splitNamespacedCode separates a leading "namespace.code: rest" prefix
// from a message. It returns an empty code when the prefix is not a
// §6.10-style namespaced code (lowercase dotted segments with no spaces).
func splitNamespacedCode(msg string) (code, rest string) {
	i := strings.Index(msg, ": ")
	if i <= 0 {
		return "", msg
	}
	prefix := msg[:i]
	if !isNamespacedCode(prefix) {
		return "", msg
	}
	return prefix, msg[i+2:]
}

// isNamespacedCode reports whether s looks like a §6.10 code: at least
// two dot-separated lowercase segments, e.g. "auth.untrusted_runtime".
func isNamespacedCode(s string) bool {
	if s == "" || s[len(s)-1] == '.' {
		return false
	}
	dot := false
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			// always allowed
		case r >= '0' && r <= '9', r == '_':
			if i == 0 {
				return false
			}
		case r == '.':
			if i == 0 {
				return false
			}
			dot = true
		default:
			return false
		}
	}
	return dot
}
