package layer

import "strings"

// Scope is one parsed fine-grained OAuth scope grant (§6.3.1). The wire
// form is "podium:<action>:<resource>[@<version>]", e.g.
// "podium:read:finance/*" or "podium:load:finance/ap/pay-invoice@1.x".
// A trailing "/*" on the resource makes the grant cover a subtree;
// otherwise it names one artifact or domain path exactly. The optional
// "@version" pin applies to load grants and is matched per ScopeMatchesVersion.
type Scope struct {
	Action  string // "read", "load", or "write"
	Path    string // resource path with any trailing "/*" stripped
	Version string // version pin without the leading "@", "" when absent
	Subtree bool   // true when the wire form ended in "/*"
}

// ScopeSet is the parsed set of a caller's "podium:*" scopes. Construct
// one with ParseScopes. The zero value narrows nothing.
type ScopeSet struct {
	scopes []Scope
}

// ParseScopes parses the "podium:*" entries from raw scope strings (other
// scopes such as "openid" or "profile" are ignored). The returned set is
// used to intersect a caller's grants with layer visibility.
func ParseScopes(raw []string) ScopeSet {
	var set ScopeSet
	for _, s := range raw {
		sc, ok := parseScope(s)
		if ok {
			set.scopes = append(set.scopes, sc)
		}
	}
	return set
}

// parseScope parses a single "podium:<action>:<resource>[@version]" string.
// It returns ok=false for any scope outside the "podium:" namespace or
// with an unknown action.
func parseScope(s string) (Scope, bool) {
	const prefix = "podium:"
	if !strings.HasPrefix(s, prefix) {
		return Scope{}, false
	}
	rest := s[len(prefix):]
	action, resource, ok := strings.Cut(rest, ":")
	if !ok {
		return Scope{}, false
	}
	switch action {
	case "read", "load", "write":
	default:
		return Scope{}, false
	}
	sc := Scope{Action: action}
	if path, ver, ok := strings.Cut(resource, "@"); ok {
		sc.Path = path
		sc.Version = ver
	} else {
		sc.Path = resource
	}
	if strings.HasSuffix(sc.Path, "/*") {
		sc.Subtree = true
		sc.Path = strings.TrimSuffix(sc.Path, "/*")
	} else if sc.Path == "*" {
		// "podium:read:*" — the whole catalog.
		sc.Subtree = true
		sc.Path = ""
	}
	return sc, true
}

// Active reports whether the caller presented any "podium:*" scope. When
// false, no narrowing applies and the caller keeps full layer visibility.
func (set ScopeSet) Active() bool { return len(set.scopes) > 0 }

// AllowsRead reports whether the caller's scopes permit discovering
// artifactID. A read or a load grant both make a resource discoverable
// (you can see what you may load). When the set is inactive every
// resource is allowed.
func (set ScopeSet) AllowsRead(artifactID string) bool {
	if !set.Active() {
		return true
	}
	for _, sc := range set.scopes {
		if sc.Action != "read" && sc.Action != "load" {
			continue
		}
		if sc.matchesPath(artifactID) {
			return true
		}
	}
	return false
}

// AllowsLoad reports whether the caller's scopes permit loading artifactID
// at version. Only a load grant authorizes a load, and a grant carrying an
// "@version" pin must also match the resolved version. When the set is
// inactive every load is allowed.
func (set ScopeSet) AllowsLoad(artifactID, version string) bool {
	if !set.Active() {
		return true
	}
	for _, sc := range set.scopes {
		if sc.Action != "load" {
			continue
		}
		if !sc.matchesPath(artifactID) {
			continue
		}
		if sc.Version != "" && !ScopeMatchesVersion(sc.Version, version) {
			continue
		}
		return true
	}
	return false
}

// matchesPath reports whether sc covers the resource path id. A subtree
// grant matches the path itself and anything beneath it; a non-subtree
// grant matches the exact path.
func (sc Scope) matchesPath(id string) bool {
	if sc.Subtree {
		if sc.Path == "" {
			return true
		}
		if id == sc.Path {
			return true
		}
		return strings.HasPrefix(id, sc.Path+"/")
	}
	return id == sc.Path
}

// ScopeMatchesVersion reports whether a scope version pin matches a
// concrete version. The pin's dot-separated segments are compared
// left-to-right; an "x" or "*" segment in the pin matches any value in
// that position (so "1.x" matches "1.4.2" and "1.2.x" matches "1.2.9").
// A pin with fewer segments than the version matches when every pin
// segment matches its counterpart (so "1" matches "1.4.2"). A pin with
// more segments than the version does not match.
func ScopeMatchesVersion(pin, version string) bool {
	if pin == "" || pin == "x" || pin == "*" {
		return true
	}
	pinParts := strings.Split(pin, ".")
	verParts := strings.Split(version, ".")
	if len(pinParts) > len(verParts) {
		return false
	}
	for i, p := range pinParts {
		if p == "x" || p == "*" {
			continue
		}
		if p != verParts[i] {
			return false
		}
	}
	return true
}
