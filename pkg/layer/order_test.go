package layer

import (
	"fmt"
	"math/rand"
	"testing"
)

// orderCallers are the caller classes the order and the meet are checked
// against: anonymous callers with and without a subject, authenticated
// callers with an empty subject, with each named subject and email, with
// each group, a member of a group only through the resolver, and the
// public-mode caller.
func orderCallers() []Identity {
	return []Identity{
		{},
		{Sub: "u1"},
		{Groups: []string{"g1"}},
		{IsAuthenticated: true},
		{IsAuthenticated: true, Sub: "u1"},
		{IsAuthenticated: true, Sub: "u2"},
		{IsAuthenticated: true, Email: "u2"},
		{IsAuthenticated: true, Sub: "x", Groups: []string{"g1"}},
		{IsAuthenticated: true, Sub: "x", Groups: []string{"g2"}},
		{IsAuthenticated: true, Sub: "x", Groups: []string{""}},
		{IsAuthenticated: true, Sub: "u3"},
		{IsAuthenticated: true, Sub: "x"},
		{IsPublic: true},
	}
}

// orderResolver makes u3 a member of g1 through the §6.3.1 directory, and
// returns an empty member for the empty group name.
func orderResolver(g string) []string {
	switch g {
	case "g1":
		return []string{"u3"}
	case "":
		return []string{""}
	}
	return nil
}

// visibilities enumerates every visibility over the flags and the subsets of
// groups and users.
func visibilities(groups, users []string) []Visibility {
	var out []Visibility
	for flags := 0; flags < 4; flags++ {
		for gm := 0; gm < 1<<len(groups); gm++ {
			for um := 0; um < 1<<len(users); um++ {
				out = append(out, Visibility{
					Public:       flags&1 != 0,
					Organization: flags&2 != 0,
					Groups:       pick(groups, gm),
					Users:        pick(users, um),
				})
			}
		}
	}
	return out
}

func pick(values []string, mask int) []string {
	var out []string
	for i, v := range values {
		if mask&(1<<i) != 0 {
			out = append(out, v)
		}
	}
	return out
}

func admits(v Visibility, id Identity, resolve GroupResolver) bool {
	return VisibleWith(Layer{ID: "l", Visibility: v}, id, resolve)
}

// TestWithinAndMeet_AgreeWithTheEvaluator checks the order and the meet
// against the evaluator over every pair of visibilities on two groups, two
// users, and an empty entry of each, for every caller class, with and without
// the group resolver. The meet admits exactly the callers both admit and is
// within each of the two; a visibility within another admits no caller the
// other does not admit; and, over well-formed entries, a visibility that
// admits no caller the other does not admit is within it.
//
// Spec: §4.6 (Order and meet, Evaluator)
func TestWithinAndMeet_AgreeWithTheEvaluator(t *testing.T) {
	t.Parallel()
	callers := orderCallers()
	for _, tc := range []struct {
		name          string
		groups, users []string
		wellFormed    bool
	}{
		{"well-formed", []string{"g1", "g2"}, []string{"u1", "u2"}, true},
		{"empty entries", []string{"g1", "g2", ""}, []string{"u1", "u2", ""}, false},
	} {
		vs := visibilities(tc.groups, tc.users)
		for _, resolve := range []GroupResolver{nil, orderResolver} {
			for _, a := range vs {
				for _, b := range vs {
					checkPair(t, a, b, callers, resolve, tc.wellFormed, true)
				}
			}
		}
	}
}

// checkPair checks the order and the meet of a and b against the evaluator
// for each caller. The meet admits no caller that one of the two refuses.
// With exact, where no caller is admitted through entries of different kinds
// in a and b, it admits every caller both admit; a caller named by a user
// entry of one and a group entry of the other is admitted by both and by no
// entry the two share, which the meet refuses, erring toward admitting fewer.
func checkPair(t *testing.T, a, b Visibility, callers []Identity, resolve GroupResolver, wellFormed, exact bool) {
	t.Helper()
	m := Meet(a, b)
	if !Within(m, a) || !Within(m, b) {
		t.Fatalf("Meet(%+v, %+v) = %+v is not within both", a, b, m)
	}
	within := Within(a, b)
	widens := false
	for _, c := range callers {
		inA, inB := admits(a, c, resolve), admits(b, c, resolve)
		got := admits(m, c, resolve)
		if got && !(inA && inB) || exact && got != (inA && inB) {
			t.Fatalf("Meet(%+v, %+v) = %+v admits %+v: %v; the first admits it: %v, the second: %v", a, b, m, c, got, inA, inB)
		}
		if within && inA && !inB {
			t.Fatalf("Within(%+v, %+v) but %+v is admitted by the first alone", a, b, c)
		}
		// The public-mode caller sees every layer and tells no two apart.
		if inA && !inB {
			widens = true
		}
	}
	if wellFormed && !within && !widens {
		t.Fatalf("Within(%+v, %+v) is false, but no caller tells them apart", a, b)
	}
}

// TestMeet_RandomPairs checks the meet and the order on random pairs of
// visibilities drawn from a larger set of entries, with repeated entries.
//
// Spec: §4.6 (Order and meet)
func TestMeet_RandomPairs(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(46))
	entries := []string{"g1", "g2", "g3", "u1", "u2", "u3", ""}
	random := func() Visibility {
		v := Visibility{Public: rng.Intn(5) == 0, Organization: rng.Intn(4) == 0}
		for i := rng.Intn(4); i > 0; i-- {
			v.Groups = append(v.Groups, entries[rng.Intn(len(entries))])
		}
		for i := rng.Intn(4); i > 0; i-- {
			v.Users = append(v.Users, entries[rng.Intn(len(entries))])
		}
		return v
	}
	callers := orderCallers()
	for _, g := range []string{"g3", "u3"} {
		callers = append(callers, Identity{IsAuthenticated: true, Sub: g, Groups: []string{g}})
	}
	for i := 0; i < 20000; i++ {
		a, b := random(), random()
		checkPair(t, a, b, callers, orderResolver, false, false)
		if !a.Public && !b.Public && !a.Organization && !b.Organization && !SameVisibility(Meet(a, b), Meet(b, a)) {
			t.Fatalf("Meet is not symmetric on lists: %+v, %+v", a, b)
		}
	}
}

// TestMeet_SharesNoSlice checks that the meet copies the lists it returns,
// so a caller that edits the result edits neither argument.
//
// Spec: §4.6 (Order and meet)
func TestMeet_SharesNoSlice(t *testing.T) {
	t.Parallel()
	a := Visibility{Groups: []string{"g1"}, Users: []string{"u1"}}
	for _, m := range []Visibility{Meet(Visibility{Public: true}, a), Meet(a, Visibility{Organization: true})} {
		m.Groups[0], m.Users[0] = "changed", "changed"
		if a.Groups[0] != "g1" || a.Users[0] != "u1" {
			t.Fatalf("Meet returned a slice of its argument: %+v", a)
		}
	}
	got := Meet(Visibility{Groups: []string{"g1", "g2", "g1"}, Users: []string{"u1"}}, Visibility{Groups: []string{"g2", "g1"}})
	if fmt.Sprint(got.Groups) != "[g1 g2]" || got.Users != nil || got.Public || got.Organization {
		t.Fatalf("Meet = %+v, want groups [g1 g2] in the order of the first and no users", got)
	}
}

// TestSameVisibility compares the fields with the lists as sets.
//
// Spec: §4.6
func TestSameVisibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b Visibility
		want bool
	}{
		{Visibility{}, Visibility{}, true},
		{Visibility{Groups: []string{"a", "b"}}, Visibility{Groups: []string{"b", "a", "a"}}, true},
		{Visibility{Groups: []string{"a"}}, Visibility{Groups: []string{"a", "b"}}, false},
		{Visibility{Users: []string{"a", "b"}}, Visibility{Users: []string{"a"}}, false},
		{Visibility{Public: true}, Visibility{}, false},
		{Visibility{Organization: true}, Visibility{}, false},
	} {
		if got := SameVisibility(tc.a, tc.b); got != tc.want {
			t.Errorf("SameVisibility(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
