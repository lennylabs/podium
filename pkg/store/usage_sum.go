package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// UsageDim names a usage_daily key column a UsageSumQuery groups by.
type UsageDim int

// The usage_daily key columns a sum can group by.
const (
	UsageByDay UsageDim = iota
	UsageByLayer
	UsageByArtifact
	UsageByClient
	UsageByHarness
)

// usageDimColumns maps each UsageDim to its usage_daily column. The SQL
// builders read column names from this table alone, so no request value is
// ever spliced into a statement.
var usageDimColumns = map[UsageDim]string{
	UsageByDay:      "day",
	UsageByLayer:    "layer",
	UsageByArtifact: "artifact_id",
	UsageByClient:   "client_kind",
	UsageByHarness:  "harness",
}

// UsagePath matches artifact IDs the way a §6.3.1 read scope does: Path alone
// when Subtree is false, and Path together with every ID beneath it when
// Subtree is true. A subtree match on the empty path matches every ID.
type UsagePath struct {
	Path    string
	Subtree bool
}

func (p UsagePath) matches(id string) bool {
	if !p.Subtree {
		return id == p.Path
	}
	return p.Path == "" || id == p.Path || strings.HasPrefix(id, p.Path+"/")
}

// UsageSumQuery selects and groups a tenant's usage rows. From and To are
// YYYY-MM-DD dates, both included. Layers, ArtifactIDs, and Paths each
// restrict the rows when non-empty and leave them unrestricted when empty;
// a caller that must match no row does not query. A row matches Paths when
// any entry matches its artifact ID. GroupBy names the key columns the sums
// are grouped by; an empty GroupBy sums every matching row into one result.
type UsageSumQuery struct {
	From, To    string
	Layers      []string
	ArtifactIDs []string
	Paths       []UsagePath
	GroupBy     []UsageDim
}

// UsageSum is one group of a UsageSumQuery: the key columns the query grouped
// by, set in UsageCount and left empty otherwise, the summed counters, and the
// latest days of the group with a non-zero loads or syncs count, "" for none.
type UsageSum struct {
	UsageCount
	LastLoaded string
	LastSynced string
}

// usageSumKey is the identity of one group: the grouped columns' values.
type usageSumKey [5]string

func (q UsageSumQuery) keyOf(c UsageCount) usageSumKey {
	var k usageSumKey
	for _, d := range q.GroupBy {
		k[d] = c.dim(d)
	}
	return k
}

func (c UsageCount) dim(d UsageDim) string {
	switch d {
	case UsageByDay:
		return c.Day
	case UsageByLayer:
		return c.Layer
	case UsageByArtifact:
		return c.ArtifactID
	case UsageByClient:
		return c.Client
	default:
		return c.Harness
	}
}

func (c *UsageCount) setDim(d UsageDim, v string) {
	switch d {
	case UsageByDay:
		c.Day = v
	case UsageByLayer:
		c.Layer = v
	case UsageByArtifact:
		c.ArtifactID = v
	case UsageByClient:
		c.Client = v
	default:
		c.Harness = v
	}
}

// validate reports a query no caller should build: a dimension outside the
// known set, which the SQL builders could not name.
func (q UsageSumQuery) validate() error {
	for _, d := range q.GroupBy {
		if _, ok := usageDimColumns[d]; !ok {
			return fmt.Errorf("store: sum usage: unknown dimension %d", d)
		}
	}
	return nil
}

// matchesRow reports whether c is a row q selects, by every filter but the
// tenant.
func (q UsageSumQuery) matchesRow(c UsageCount) bool {
	if c.Day < q.From || c.Day > q.To {
		return false
	}
	if len(q.Layers) > 0 && !containsString(q.Layers, c.Layer) {
		return false
	}
	if len(q.ArtifactIDs) > 0 && !containsString(q.ArtifactIDs, c.ArtifactID) {
		return false
	}
	if len(q.Paths) == 0 {
		return true
	}
	for _, p := range q.Paths {
		if p.matches(c.ArtifactID) {
			return true
		}
	}
	return false
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// sumUsageRows applies q to rows in memory. The Memory store uses it, and it
// is the reference the SQL stores' results are tested against.
func sumUsageRows(rows []UsageCount, q UsageSumQuery) []UsageSum {
	groups := map[usageSumKey]*UsageSum{}
	for _, c := range rows {
		if !q.matchesRow(c) {
			continue
		}
		k := q.keyOf(c)
		g := groups[k]
		if g == nil {
			g = &UsageSum{}
			for _, d := range q.GroupBy {
				g.setDim(d, c.dim(d))
			}
			groups[k] = g
		}
		g.Loads += c.Loads
		g.Syncs += c.Syncs
		g.SearchHits += c.SearchHits
		if c.Loads > 0 && c.Day > g.LastLoaded {
			g.LastLoaded = c.Day
		}
		if c.Syncs > 0 && c.Day > g.LastSynced {
			g.LastSynced = c.Day
		}
	}
	out := make([]UsageSum, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	sortUsageSums(out, q.GroupBy)
	return out
}

// sortUsageSums orders sums by the grouped columns in GroupBy order, the
// order every backend returns. The SQL stores sort here rather than in the
// statement, because Postgres orders text by the database collation, which
// can differ from byte order.
func sortUsageSums(sums []UsageSum, by []UsageDim) {
	sort.Slice(sums, func(i, j int) bool {
		for _, d := range by {
			a, b := sums[i].dim(d), sums[j].dim(d)
			if a != b {
				return a < b
			}
		}
		// Unreachable: distinct groups differ in a grouped column, and an
		// ungrouped sum holds at most one element.
		return false
	})
}

// usageDialect is what differs between the SQLite and Postgres statements of
// a usage sum: the placeholder syntax and how a day column is compared and
// read as YYYY-MM-DD text.
type usageDialect struct {
	// placeholder returns the parameter marker for the n-th argument, from 1.
	placeholder func(n int) string
	// dayParam wraps a day parameter marker for comparison with the day column.
	dayParam func(marker string) string
	// dayText renders a day-valued expression as YYYY-MM-DD text.
	dayText func(expr string) string
}

var sqliteUsageDialect = usageDialect{
	placeholder: func(int) string { return "?" },
	dayParam:    func(m string) string { return m },
	dayText:     func(e string) string { return e },
}

var postgresUsageDialect = usageDialect{
	placeholder: func(n int) string { return fmt.Sprintf("$%d", n) },
	dayParam:    func(m string) string { return m + "::date" },
	dayText:     func(e string) string { return "to_char(" + e + ", 'YYYY-MM-DD')" },
}

// usageSumSQL builds the statement and arguments of q for tenantID. Column
// names come from usageDimColumns; every value is a parameter.
func usageSumSQL(d usageDialect, tenantID string, q UsageSumQuery) (string, []any) {
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return d.placeholder(len(args))
	}
	var sel, group []string
	for _, dim := range q.GroupBy {
		col := usageDimColumns[dim]
		group = append(group, col)
		if dim == UsageByDay {
			sel = append(sel, d.dayText(col))
		} else {
			sel = append(sel, col)
		}
	}
	sel = append(sel,
		"COALESCE(SUM(loads), 0)", "COALESCE(SUM(syncs), 0)", "COALESCE(SUM(search_hits), 0)",
		d.dayText("MAX(CASE WHEN loads > 0 THEN day END)"),
		d.dayText("MAX(CASE WHEN syncs > 0 THEN day END)"))
	where := []string{
		"tenant_id = " + arg(tenantID),
		"day >= " + d.dayParam(arg(q.From)),
		"day <= " + d.dayParam(arg(q.To)),
	}
	if len(q.Layers) > 0 {
		where = append(where, "layer IN ("+argList(arg, q.Layers)+")")
	}
	if len(q.ArtifactIDs) > 0 {
		where = append(where, "artifact_id IN ("+argList(arg, q.ArtifactIDs)+")")
	}
	if len(q.Paths) > 0 {
		where = append(where, "("+pathsSQL(arg, q.Paths)+")")
	}
	stmt := "SELECT " + strings.Join(sel, ", ") + " FROM usage_daily WHERE " + strings.Join(where, " AND ")
	if len(group) > 0 {
		stmt += " GROUP BY " + strings.Join(group, ", ")
	}
	return stmt, args
}

func argList(arg func(any) string, values []string) string {
	marks := make([]string, len(values))
	for i, v := range values {
		marks[i] = arg(v)
	}
	return strings.Join(marks, ", ")
}

// pathsSQL renders the path matches as one disjunction. A subtree match
// compares a prefix with substr rather than LIKE, so an ID holding % or _
// needs no escaping. SQLite and Postgres count substr positions in
// characters, so the prefix length is its character count: a byte count
// would cut an ID holding a multi-byte character at the wrong place and
// match nothing.
//
// Spec: §8.7, §6.3.1
func pathsSQL(arg func(any) string, paths []UsagePath) string {
	terms := make([]string, 0, len(paths))
	for _, p := range paths {
		switch {
		case p.Subtree && p.Path == "":
			terms = append(terms, "1 = 1")
		case p.Subtree:
			prefix := p.Path + "/"
			terms = append(terms, "artifact_id = "+arg(p.Path)+
				" OR substr(artifact_id, 1, "+arg(utf8.RuneCountInString(prefix))+") = "+arg(prefix))
		default:
			terms = append(terms, "artifact_id = "+arg(p.Path))
		}
	}
	return strings.Join(terms, " OR ")
}

// scanUsageSums reads the rows of a usageSumSQL statement for q.
func scanUsageSums(rows usageRows, q UsageSumQuery) ([]UsageSum, error) {
	out := []UsageSum{}
	for rows.Next() {
		keys := make([]string, len(q.GroupBy))
		dest := make([]any, 0, len(keys)+5)
		for i := range keys {
			dest = append(dest, &keys[i])
		}
		var s UsageSum
		var lastL, lastS sql.NullString
		dest = append(dest, &s.Loads, &s.Syncs, &s.SearchHits, &lastL, &lastS)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i, dim := range q.GroupBy {
			s.setDim(dim, keys[i])
		}
		s.LastLoaded, s.LastSynced = lastL.String, lastS.String
		if s.Loads == 0 && s.Syncs == 0 && s.SearchHits == 0 && len(q.GroupBy) == 0 {
			// An ungrouped sum over no rows still returns one row; report
			// it as no groups, as the grouped form and Memory do.
			continue
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortUsageSums(out, q.GroupBy)
	return out, nil
}
