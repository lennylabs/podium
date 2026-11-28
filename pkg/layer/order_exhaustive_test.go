package layer

import "testing"

// TestWithinAndMeet_ExhaustiveOverEntryKinds checks the meet and the order
// against the evaluator over every pair of visibilities on groups and users
// that include empty, case-variant, and overlapping entries, with and without
// a resolver that returns an empty member, for callers named by a subject,
// an email, or a group.
//
// Spec: §4.6 (Order and meet, Evaluator)
func TestWithinAndMeet_ExhaustiveOverEntryKinds(t *testing.T) {
	t.Parallel()
	groups := [][]string{nil, {"g"}, {"h"}, {"g", "h"}, {""}, {"G"}}
	users := [][]string{nil, {"alice"}, {"a@x"}, {"alice", "a@x"}, {""}, {"bob"}}
	var vs []Visibility
	for _, p := range []bool{false, true} {
		for _, o := range []bool{false, true} {
			for _, g := range groups {
				for _, u := range users {
					vs = append(vs, Visibility{Public: p, Organization: o, Groups: g, Users: u})
				}
			}
		}
	}
	ids := []Identity{{}, {IsAuthenticated: true}, {IsAuthenticated: true, Sub: "alice"}, {IsAuthenticated: true, Email: "a@x"},
		{IsAuthenticated: true, Sub: "zed", Groups: []string{"g"}}, {IsAuthenticated: true, Sub: "bob"}, {Sub: "alice"}, {IsAuthenticated: true, Sub: "x", Email: "a@x", Groups: []string{"h"}},
		{IsAuthenticated: true, Sub: "carol"}}
	resolvers := []GroupResolver{nil, func(g string) []string {
		switch g {
		case "g":
			return []string{"carol", ""}
		case "":
			return []string{"carol"}
		}
		return nil
	}}
	for _, a := range vs {
		for _, b := range vs {
			m := Meet(a, b)
			w := Within(a, b)
			for _, rs := range resolvers {
				for _, id := range ids {
					va := VisibleWith(Layer{Visibility: a}, id, rs)
					vb := VisibleWith(Layer{Visibility: b}, id, rs)
					vm := VisibleWith(Layer{Visibility: m}, id, rs)
					if vm && (!va || !vb) {
						t.Fatalf("meet admits %+v: a=%+v b=%+v m=%+v", id, a, b, m)
					}
					if w && va && !vb {
						t.Fatalf("within wrong %+v: a=%+v b=%+v", id, a, b)
					}
				}
			}
			if !Within(m, a) || !Within(m, b) {
				t.Fatalf("meet not within: a=%+v b=%+v m=%+v", a, b, m)
			}
		}
	}
}
