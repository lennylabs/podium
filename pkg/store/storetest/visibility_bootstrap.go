package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// visibilityView renders the visibility members of a layer read in one
// comparable string.
func visibilityView(cfg store.LayerConfig) string {
	pending := "-"
	if p := cfg.VisibilityPending; p != nil {
		pending = fmt.Sprintf("%v/%v/%q/%q", p.Public, p.Organization, p.Groups, p.Users)
	}
	return fmt.Sprintf("%v/%v/%q/%q %s %s", cfg.Public, cfg.Organization, cfg.Groups, cfg.Users, cfg.VisibilitySource, pending)
}

// layerBootstrapVisibility covers the visibility members of a layer that
// --layer-path bootstraps: visibility_source and visibility_pending written
// and read back by a layer write, on a live row and a soft-deleted one; the
// visibility a cycle's record write carries, with a source it keeps or sets
// and a pending value it sets or clears; the second write of a passed cycle,
// which applies only under the cycle's claim and the mark and commit its
// record write stored; and a list entry the stores cannot read back, which
// writes nothing.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer, Attestation record), §7.3.1
func layerBootstrapVisibility(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	t1 := time.Date(2026, 11, 27, 9, 0, 0, 123456789, time.UTC)
	var got []string
	read := func(note string) {
		cfg, err := s.GetLayerConfig(ctx, "t", "L")
		must(t, err)
		got = append(got, note+" "+visibilityView(cfg))
	}
	pending := &store.LayerVisibility{Groups: []string{"g1", "a b"}, Users: []string{"bob@acme.com"}}
	base := store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x",
		VisibilitySource: store.VisibilitySourceDefault, VisibilityPending: pending}
	must(t, s.PutLayerConfig(ctx, base))
	pending.Groups[0] = "edited after the write"
	read("put")
	must(t, s.DeleteLayerConfig(ctx, "t", "L"))
	deleted, err := s.ListDeletedLayerConfigs(ctx, "t")
	must(t, err)
	for _, d := range deleted {
		got = append(got, "deleted "+visibilityView(d))
	}
	must(t, s.RestoreLayerConfig(ctx, "t", "L"))
	_, _, err = s.ClaimLayerIngest(ctx, "t", "L", "c1")
	must(t, err)
	record := func(claim string, w store.AttestationWrite, note string) {
		err := s.WriteLayerAttestation(ctx, "t", "L", claim, w)
		got = append(got, fmt.Sprintf("%s superseded=%v", note, errors.Is(err, store.ErrIngestSuperseded)))
		read(note)
	}
	record("c1", store.AttestationWrite{Status: "ok", CheckedAt: t1, Advance: true, Mark: t1, Commit: "aaa",
		Visibility: &store.VisibilityWrite{Visibility: store.LayerVisibility{Organization: true}, Pending: &store.LayerVisibility{Public: true}}}, "passed")
	record("c0", store.AttestationWrite{Status: "ok", CheckedAt: t1,
		Visibility: &store.VisibilityWrite{Visibility: store.LayerVisibility{Public: true}}}, "other-claim")
	mark := t1.Truncate(time.Microsecond)
	second := func(claim string, m time.Time, commit, note string) {
		err := s.WriteLayerVisibility(ctx, "t", "L", claim, m, commit,
			store.VisibilityWrite{Visibility: store.LayerVisibility{Public: true}, Source: store.VisibilitySourceDeclared})
		got = append(got, fmt.Sprintf("%s superseded=%v", note, errors.Is(err, store.ErrIngestSuperseded)))
	}
	second("c0", mark, "aaa", "second-other-claim")
	second("c1", mark.Add(time.Second), "aaa", "second-other-mark")
	second("c1", mark, "bbb", "second-other-commit")
	second("c1", t1, "aaa", "second")
	read("second")
	record("c1", store.AttestationWrite{Status: "ingest.attestation_missing", CheckedAt: t1,
		Visibility: &store.VisibilityWrite{Visibility: store.LayerVisibility{Groups: []string{"g1"}}, Source: store.VisibilitySourceDefault, Pending: &store.LayerVisibility{Users: []string{}}}}, "failed")
	bad := store.VisibilityWrite{Visibility: store.LayerVisibility{Users: []string{"a\nb"}}}
	err = s.WriteLayerAttestation(ctx, "t", "L", "c1", store.AttestationWrite{Status: "x", CheckedAt: t1, Visibility: &bad})
	got = append(got, fmt.Sprintf("line-feed record %v", errors.Is(err, store.ErrLayerListEntry)))
	err = s.WriteLayerVisibility(ctx, "t", "L", "c1", t1, "aaa", bad)
	got = append(got, fmt.Sprintf("line-feed second %v", errors.Is(err, store.ErrLayerListEntry)))
	read("unchanged")
	// The members are ordinary members of the row: a layer write that
	// carries none clears them.
	must(t, s.PutLayerConfig(ctx, store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x"}))
	read("plain-put")
	want := strings.Join([]string{
		`put false/false/[]/[] default false/false/["g1" "a b"]/["bob@acme.com"]`,
		`deleted false/false/[]/[] default false/false/["g1" "a b"]/["bob@acme.com"]`,
		`passed superseded=false`,
		`passed false/true/[]/[] default true/false/[]/[]`,
		`other-claim superseded=true`,
		`other-claim false/true/[]/[] default true/false/[]/[]`,
		`second-other-claim superseded=true`,
		`second-other-mark superseded=true`,
		`second-other-commit superseded=true`,
		`second superseded=false`,
		`second true/false/[]/[] declared -`,
		`failed superseded=false`,
		`failed false/false/["g1"]/[] default false/false/[]/[]`,
		`line-feed record true`,
		`line-feed second true`,
		`unchanged false/false/["g1"]/[] default false/false/[]/[]`,
		`plain-put false/false/[]/[]  -`,
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Errorf("visibility members:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// layerVisibilityKeptAndConditional covers the writes that must not put a
// stale visibility back: PutLayerConfigKeepingVisibility keeps the stored
// visibility members of a live and a soft-deleted row and writes a new row
// as given, and SetLayerVisibilityIf applies only while the row holds the
// members the writer read, comparing lists in their stored form.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Start write, Writes that keep the visibility), §7.3.1
func layerVisibilityKeptAndConditional(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	var got []string
	read := func(id, note string) {
		cfg, err := s.GetLayerConfig(ctx, "t", id)
		must(t, err)
		got = append(got, fmt.Sprintf("%s %s order=%d", note, visibilityView(cfg), cfg.Order))
	}
	narrow := store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x", Groups: []string{"g"},
		VisibilitySource: store.VisibilitySourceDeclared, VisibilityPending: &store.LayerVisibility{Public: true}}
	must(t, s.PutLayerConfig(ctx, narrow))
	stale := narrow
	stale.Public, stale.Groups, stale.VisibilityPending, stale.Order = true, nil, nil, 20
	must(t, s.PutLayerConfigKeepingVisibility(ctx, stale))
	read("L", "kept")
	must(t, s.DeleteLayerConfig(ctx, "t", "L"))
	stale.Order = 30
	must(t, s.PutLayerConfigKeepingVisibility(ctx, stale))
	read("L", "kept-deleted")
	must(t, s.PutLayerConfigKeepingVisibility(ctx, store.LayerConfig{TenantID: "t", ID: "N", SourceType: "local", Users: []string{"alice"}, VisibilitySource: store.VisibilitySourceDefault}))
	read("N", "new")

	cur, err := s.GetLayerConfig(ctx, "t", "L")
	must(t, err)
	expect := cur.VisibilityState()
	next := store.VisibilityState{Visibility: store.LayerVisibility{Organization: true}, Source: store.VisibilitySourceDefault}
	set := func(note string, e store.VisibilityState) {
		err := s.SetLayerVisibilityIf(ctx, "t", "L", e, next)
		got = append(got, fmt.Sprintf("%s changed=%v", note, errors.Is(err, store.ErrVisibilityChanged)))
	}
	other := expect
	other.Visibility.Groups = []string{"h"}
	set("other-groups", other)
	other = expect
	other.Pending = nil
	set("other-pending", other)
	other = expect
	other.Source = store.VisibilitySourceDefault
	set("other-source", other)
	set("held", expect)
	read("L", "set")
	set("again", expect)
	err = s.SetLayerVisibilityIf(ctx, "t", "absent", expect, next)
	got = append(got, fmt.Sprintf("absent notfound=%v", errors.Is(err, store.ErrNotFound)))
	err = s.SetLayerVisibilityIf(ctx, "t", "L", next, store.VisibilityState{Visibility: store.LayerVisibility{Users: []string{"a\nb"}}})
	got = append(got, fmt.Sprintf("line-feed %v", errors.Is(err, store.ErrLayerListEntry)))
	want := strings.Join([]string{
		`kept false/false/["g"]/[] declared true/false/[]/[] order=20`,
		`kept-deleted false/false/["g"]/[] declared true/false/[]/[] order=30`,
		`new false/false/[]/["alice"] default - order=0`,
		`other-groups changed=true`,
		`other-pending changed=true`,
		`other-source changed=true`,
		`held changed=false`,
		`set false/true/[]/[] default - order=30`,
		`again changed=true`,
		`absent notfound=true`,
		`line-feed true`,
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Errorf("visibility writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// layerWriteConditionalOnVisibility covers PutLayerConfigIf: a write whose
// expected visibility members the row no longer holds writes nothing, not
// even the members outside the visibility, and one that matches writes the
// whole row.
//
// Spec: §7.3.1, §4.7.11 (Visibility of a path-bootstrapped layer: Writes that keep the visibility)
func layerWriteConditionalOnVisibility(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	row := store.LayerConfig{TenantID: "t", ID: "L", SourceType: "local", LocalPath: "/x", Groups: []string{"g"}, VisibilitySource: store.VisibilitySourceDeclared}
	must(t, s.PutLayerConfig(ctx, row))
	held, err := s.GetLayerConfig(ctx, "t", "L")
	must(t, err)
	stale := held.VisibilityState()
	stale.Visibility = store.LayerVisibility{Organization: true}
	next := row
	next.LocalPath, next.Groups, next.Users = "/y", []string{"g"}, []string{"bob"}
	var got []string
	put := func(note string, cfg store.LayerConfig, expect store.VisibilityState) {
		err := s.PutLayerConfigIf(ctx, cfg, expect)
		cur, rerr := s.GetLayerConfig(ctx, "t", "L")
		must(t, rerr)
		got = append(got, fmt.Sprintf("%s changed=%v notfound=%v %s %s", note, errors.Is(err, store.ErrVisibilityChanged), errors.Is(err, store.ErrNotFound), cur.LocalPath, visibilityView(cur)))
	}
	put("stale", next, stale)
	put("held", next, held.VisibilityState())
	absent := next
	absent.ID = "absent"
	put("absent", absent, held.VisibilityState())
	want := strings.Join([]string{
		`stale changed=true notfound=false /x false/false/["g"]/[] declared -`,
		`held changed=false notfound=false /y false/false/["g"]/["bob"] declared -`,
		`absent changed=false notfound=true /y false/false/["g"]/["bob"] declared -`,
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Errorf("conditional writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}
