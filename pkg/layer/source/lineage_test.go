package source

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/spi"
)

// lineageAnswering is a synthetic third source provider that answers lineage
// queries, as a custom LayerSourceProvider can.
type lineageAnswering struct{ Local }

func (lineageAnswering) ID() string           { return "synthetic-lineage" }
func (lineageAnswering) AnswersLineage() bool { return true }
func (lineageAnswering) Lineage(_ context.Context, _ LayerConfig, _, _ string, commits []string) ([]LineageAnswer, error) {
	out := make([]LineageAnswer, len(commits))
	for i := range out {
		out[i] = InLineage
	}
	return out, nil
}

// lineageDeclined implements the lineage member and declares that it answers
// no query, as a wrapper of a Git source can.
type lineageDeclined struct{ lineageAnswering }

func (lineageDeclined) AnswersLineage() bool { return false }

// Spec: §4.6 (Source types), §9.1 (Lineage query) — the registry reads a
// provider's lineage declaration without a snapshot. The Git source answers
// lineage queries and the local source does not. A synthetic third provider
// that implements the lineage member and declares it answers them, one that
// implements it and declares false does not, and one that does not implement
// it at all does not.
func TestAnswersLineage_Declaration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		p    Provider
		want bool
	}{
		{"git", Git{}, true},
		{"local", Local{}, false},
		{"synthetic provider that answers", lineageAnswering{}, true},
		{"synthetic provider that declines", lineageDeclined{}, false},
	} {
		if got := AnswersLineage(tc.p); got != tc.want {
			t.Errorf("AnswersLineage(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, ok := Provider(Local{}).(LineageProvider); ok {
		t.Errorf("the local source implements the lineage member")
	}
}

// Spec: §9.1 (Lineage query: No default) — a response whose answers differ in
// number from the request's commits, or that holds a value other than the
// three answers, is an ingest.source_unreachable failure. A missing answer
// never stands in for a pass.
func TestCheckLineageAnswers(t *testing.T) {
	t.Parallel()
	commits := []string{"a", "b"}
	for _, tc := range []struct {
		name    string
		answers []LineageAnswer
		ok      bool
	}{
		{"one answer per commit", []LineageAnswer{InLineage, NotDescendant}, true},
		{"every answer value", []LineageAnswer{NotInHistory, InLineage}, true},
		{"missing answer", []LineageAnswer{InLineage}, false},
		{"no answers", nil, false},
		{"extra answer", []LineageAnswer{InLineage, InLineage, InLineage}, false},
		{"unknown value", []LineageAnswer{InLineage, "maybe"}, false},
		{"empty value", []LineageAnswer{"", InLineage}, false},
	} {
		err := CheckLineageAnswers(commits, tc.answers)
		if tc.ok != (err == nil) {
			t.Errorf("%s: CheckLineageAnswers = %v, want ok %v", tc.name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrSourceUnreachable) {
			t.Errorf("%s: error %v is not ErrSourceUnreachable", tc.name, err)
		}
	}
}

// Spec: §9.1 (Lineage query) — each request member has a declared limit: a
// reference of 1 to 128 bytes, a base of at most 128 bytes, and 1 to 64
// commits of at most 128 bytes each. A request that breaks one is refused with
// registry.invalid_argument.
func TestValidateLineageRequest(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", MaxLineageStringBytes+1)
	limit := strings.Repeat("a", MaxLineageStringBytes)
	for _, tc := range []struct {
		name            string
		reference, base string
		commits         []string
		ok              bool
	}{
		{"at the limits", limit, limit, make([]string, MaxLineageCommits), true},
		{"empty base", "r", "", []string{"c"}, true},
		{"empty reference", "", "", []string{"c"}, false},
		{"long reference", long, "", []string{"c"}, false},
		{"long base", "r", long, []string{"c"}, false},
		{"no commits", "r", "", nil, false},
		{"too many commits", "r", "", make([]string, MaxLineageCommits+1), false},
		{"long commit", "r", "", []string{"c", long}, false},
	} {
		err := ValidateLineageRequest(tc.reference, tc.base, tc.commits)
		if tc.ok != (err == nil) {
			t.Errorf("%s: ValidateLineageRequest = %v, want ok %v", tc.name, err, tc.ok)
		}
		if err != nil {
			if e, ok := spi.AsError(err); !ok || e.Code != "registry.invalid_argument" {
				t.Errorf("%s: error %v does not carry registry.invalid_argument", tc.name, err)
			}
		}
	}
}

// Spec: §7.3.1 (Git fetch: Deepening, Termination) — the depth quadruples up
// to the round that requests 2147483647, Git's request for the complete
// history, which is the last round: 17 rounds from a starting depth of 1 and
// 15 from the default of 16, whatever the remote returns.
func TestNextDepth_Schedule(t *testing.T) {
	t.Parallel()
	rounds := func(start int) int {
		n := 1
		for d := start; d < fullHistoryDepth; d = nextDepth(d) {
			n++
		}
		return n
	}
	for _, tc := range []struct{ start, rounds int }{{1, 17}, {DefaultFetchDepth, 15}, {MaxFetchDepth, 2}} {
		if got := rounds(tc.start); got != tc.rounds {
			t.Errorf("rounds from depth %d = %d, want %d", tc.start, got, tc.rounds)
		}
	}
	if got := nextDepth(536870911); got != 2147483644 {
		t.Errorf("nextDepth(536870911) = %d, want 2147483644", got)
	}
	if got := nextDepth(536870912); got != fullHistoryDepth {
		t.Errorf("nextDepth(536870912) = %d, want %d", got, fullHistoryDepth)
	}
}

// Spec: §13.12 — a zero FetchDepth and a zero FetchTimeout select the
// defaults, and a depth above the setting's maximum is read as the maximum, so
// the first fetch of a call never requests the complete history.
func TestGit_FetchDefaults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ set, want int }{{0, 16}, {-3, 16}, {1, 1}, {MaxFetchDepth, MaxFetchDepth}, {fullHistoryDepth, MaxFetchDepth}} {
		if got := (Git{FetchDepth: tc.set}).fetchDepth(); got != tc.want {
			t.Errorf("fetchDepth with FetchDepth %d = %d, want %d", tc.set, got, tc.want)
		}
	}
	if got := (Git{}).fetchTimeout(); got != DefaultFetchTimeout {
		t.Errorf("fetchTimeout with no setting = %v, want %v", got, DefaultFetchTimeout)
	}
}

// Spec: §7.3.1 (Git fetch: Deadline) — the failure of a call that reached the
// deadline is ingest.source_unreachable with the details of the section, its
// message names the setting and its value, and it still matches
// ErrSourceUnreachable, which the HTTP edge maps.
func TestNewFetchDeadlineError(t *testing.T) {
	t.Parallel()
	err := newFetchDeadlineError(OperationLineageQuery, 90*time.Second, 64)
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Fatalf("error %v is not ErrSourceUnreachable", err)
	}
	if want := "source: unreachable: git lineage query did not finish within PODIUM_GIT_FETCH_TIMEOUT (1m30s)"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	e, ok := spi.AsError(err)
	if !ok || e.Code != "ingest.source_unreachable" || !e.Retryable {
		t.Fatalf("envelope = %+v, want a retryable ingest.source_unreachable", e)
	}
	want := map[string]any{"constraint": "fetch_deadline", "setting": "PODIUM_GIT_FETCH_TIMEOUT", "operation": "lineage_query", "depth": 64}
	for k, v := range want {
		if e.Details[k] != v {
			t.Errorf("details[%s] = %v, want %v", k, e.Details[k], v)
		}
	}
	if len(e.Details) != len(want) {
		t.Errorf("details = %v, want exactly %v", e.Details, want)
	}
	if got := newFetchDeadlineError(OperationSnapshot, time.Second, 16).Error(); !strings.Contains(got, "git snapshot did not finish") {
		t.Errorf("snapshot message = %q", got)
	}
	// The sentinel itself carries no details, so a plain unreachable failure
	// keeps an envelope without them.
	if e, _ := spi.AsError(ErrSourceUnreachable); len(e.Details) != 0 {
		t.Errorf("ErrSourceUnreachable carries details %v", e.Details)
	}
}

// Spec: §7.3.1 (Git fetch: Lineage query) — a commit that is not a full
// hexadecimal object name is not_in_history at once, and only such a request
// is decided with no fetch. Upper-case hexadecimal names the same commit.
func TestNewLineageQuery_NamesThatNeedNoFetch(t *testing.T) {
	t.Parallel()
	q := newLineageQuery("", []string{"HEAD", strings.Repeat("g", 40), strings.Repeat("a", 39)})
	if !q.decided() {
		t.Errorf("a request of names alone is undecided: %v", q.answers)
	}
	q = newLineageQuery(strings.Repeat("A", 40), []string{strings.Repeat("a", 40), "HEAD"})
	if q.decided() || q.answers[1] != NotInHistory || !q.baseValid || q.commits[0] != q.base {
		t.Errorf("query = %+v, want the hexadecimal commit undecided and equal to the base", q)
	}
}
