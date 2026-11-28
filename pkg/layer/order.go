package layer

import "slices"

// Within reports whether a is within b: the fields of a admit no caller that
// the fields of b do not admit. Group and user entries are compared byte for
// byte, so an entry of a that b does not list makes a not within b even when
// the entry admits no caller, which errs toward reading a change as a
// widening.
//
// The order is sound only because the evaluator admits through groups and
// users no caller that organization does not admit (VisibleWith), which is
// why the rule reads organization before the lists.
//
// Spec: §4.6 (Order and meet)
func Within(a, b Visibility) bool {
	switch {
	case b.Public:
		return true
	case a.Public:
		return false
	case b.Organization:
		return true
	case a.Organization:
		return false
	}
	return subset(a.Groups, b.Groups) && subset(a.Users, b.Users)
}

// Meet returns a visibility that admits only callers both a and b admit. It
// is within each of the two, so the meet of two visibilities that each name
// one other group admits no caller. It can admit fewer callers than both
// admit: a caller that a user entry of a and a group entry of b each admit
// is admitted by no entry the two share. The result shares no slice with its
// arguments.
//
// Spec: §4.6 (Order and meet)
func Meet(a, b Visibility) Visibility {
	switch {
	case a.Public:
		return cloneVisibility(b)
	case b.Public:
		return cloneVisibility(a)
	case a.Organization:
		return cloneVisibility(b)
	case b.Organization:
		return cloneVisibility(a)
	}
	return Visibility{Groups: intersect(a.Groups, b.Groups), Users: intersect(a.Users, b.Users)}
}

// SameVisibility reports whether a and b set the same fields, with the group
// and user lists compared as sets, so the order of the entries and a repeated
// entry make no difference.
//
// Spec: §4.6
func SameVisibility(a, b Visibility) bool {
	return a.Public == b.Public && a.Organization == b.Organization &&
		subset(a.Groups, b.Groups) && subset(b.Groups, a.Groups) &&
		subset(a.Users, b.Users) && subset(b.Users, a.Users)
}

// subset reports whether every entry of a is an entry of b.
func subset(a, b []string) bool {
	for _, v := range a {
		if !slices.Contains(b, v) {
			return false
		}
	}
	return true
}

// intersect returns the entries of a that b lists, in the order of a and
// without repeats, or nil when there are none.
func intersect(a, b []string) []string {
	var out []string
	for _, v := range a {
		if slices.Contains(b, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// cloneVisibility returns a copy of v that shares no slice with it.
func cloneVisibility(v Visibility) Visibility {
	v.Groups = slices.Clone(v.Groups)
	v.Users = slices.Clone(v.Users)
	return v
}
