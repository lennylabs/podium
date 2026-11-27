package server

import (
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/store"
)

// WithUnappliedVisibility marks the endpoint as serving a registry that
// applies no layer visibility because it verifies no caller: a registry in
// public mode, or one started with no request-time identity verifier. The
// register, update, and restore responses then carry a warning for a layer
// whose declaration grants less than public, because the operator who stored
// that declaration would otherwise read the success response as the layer
// being hidden. serverboot keys the call on the absent verifier rather than
// on the configured provider label, so a label that resolves to no verifier
// is covered too.
//
// Spec: §4.6 (No-identity bypass), §7.3.1 (Unapplied-visibility warning)
func (e *LayerEndpoint) WithUnappliedVisibility() *LayerEndpoint {
	e.unappliedVisibility = true
	return e
}

// visibilityWarnings returns the §7.3.1 unapplied-visibility warning for the
// layer a write response reports, or nil when the endpoint applies visibility
// or the layer grants public. A nil result leaves the response's warnings
// member omitted, so a response that carries no warning is unchanged.
//
// Spec: §7.3.1 (Unapplied-visibility warning)
func (e *LayerEndpoint) visibilityWarnings(cfg store.LayerConfig) []string {
	if !e.unappliedVisibility || cfg.Public {
		return nil
	}
	return []string{unappliedVisibilityWarning(cfg)}
}

// unappliedVisibilityWarning returns the message that reports a layer whose
// declared visibility a registry verifying no caller does not apply. A layer
// that sets no visibility field is reported as private, which is what a
// `private` default stores, and its message says the private declaration
// applies once an identity provider is added.
//
// Spec: §7.3.1 (Unapplied-visibility warning)
func unappliedVisibilityWarning(cfg store.LayerConfig) string {
	declared := declaredVisibility(cfg)
	if declared == "private" {
		return fmt.Sprintf("layer %q is private, but this registry applies no layer visibility because it verifies no caller, so every caller sees the layer; the private declaration applies once an identity provider is added", cfg.ID)
	}
	return fmt.Sprintf("layer %q declares visibility %s, but this registry applies no layer visibility because it verifies no caller, so every caller sees the layer; the declaration takes effect once an identity provider is configured", cfg.ID, declared)
}

// declaredVisibility renders the visibility grants of a layer that sets no
// `public` grant in the §4.6 field syntax, joined with "; ". A layer that
// sets no grant renders as "private". visibilityWarnings never passes a
// public layer, so the public grant has no rendering here.
//
// Spec: §4.6
func declaredVisibility(cfg store.LayerConfig) string {
	var parts []string
	if cfg.Organization {
		parts = append(parts, "organization: true")
	}
	if len(cfg.Groups) > 0 {
		parts = append(parts, "groups: ["+strings.Join(cfg.Groups, ", ")+"]")
	}
	if len(cfg.Users) > 0 {
		parts = append(parts, "users: ["+strings.Join(cfg.Users, ", ")+"]")
	}
	if len(parts) == 0 {
		return "private"
	}
	return strings.Join(parts, "; ")
}
