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
	return []string{unappliedVisibilityWarning(cfg, e.publicMode)}
}

// unappliedVisibilityWarning returns the message that reports a layer whose
// declared visibility a registry verifying no caller does not apply. A layer
// that sets no visibility field is reported as private, which is what a
// `private` default stores.
//
// The two registry postures need different remedies and reach. A public-mode
// registry refuses an identity provider (config.public_mode_with_idp), so its
// declaration applies only once public mode is turned off as well, and it
// admits a write only with the operator token. A registry with no verifier
// outside public mode admits a successful write only through the admin arm,
// which admits every caller there, so every caller can also update or
// unregister the layer.
//
// Spec: §7.3.1 (Unapplied-visibility warning)
func unappliedVisibilityWarning(cfg store.LayerConfig, publicMode bool) string {
	reason := "this registry verifies no caller and applies no layer visibility, so every caller sees the layer and can update or unregister it"
	remedy := "once an identity provider is"
	if publicMode {
		reason = "this registry runs in public mode and applies no layer visibility, so every caller sees the layer"
		remedy = "once public mode is turned off and an identity provider is"
	}
	declared := declaredVisibility(cfg)
	if declared == "private" {
		return fmt.Sprintf("layer %q is private, but %s; the private declaration applies %s added", cfg.ID, reason, remedy)
	}
	return fmt.Sprintf("layer %q declares visibility %s, but %s; the declaration takes effect %s configured", cfg.ID, declared, reason, remedy)
}

// declaredVisibility renders the visibility grants of a layer that sets no
// `public` grant in the §4.6 field syntax, joined with "; ". Each groups and
// users entry is double-quoted, because an entry may itself hold a comma or a
// space, and unquoted `users: [a, b]` would read the same for one entry "a, b"
// as for the two entries "a" and "b". A layer that sets no grant renders as
// "private". visibilityWarnings never passes a public layer, so the public
// grant has no rendering here.
//
// Spec: §4.6, §7.3.1 (Unapplied-visibility warning)
func declaredVisibility(cfg store.LayerConfig) string {
	var parts []string
	if cfg.Organization {
		parts = append(parts, "organization: true")
	}
	if len(cfg.Groups) > 0 {
		parts = append(parts, "groups: "+quotedList(cfg.Groups))
	}
	if len(cfg.Users) > 0 {
		parts = append(parts, "users: "+quotedList(cfg.Users))
	}
	if len(parts) == 0 {
		return "private"
	}
	return strings.Join(parts, "; ")
}

// quotedList renders entries as a bracketed list of Go-quoted strings.
func quotedList(entries []string) string {
	quoted := make([]string, len(entries))
	for i, entry := range entries {
		quoted[i] = fmt.Sprintf("%q", entry)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
