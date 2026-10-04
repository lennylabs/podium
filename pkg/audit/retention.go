package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Policy is one §8.4 retention rule: events of Type older than
// MaxAge are dropped during Enforce.
type Policy struct {
	Type   EventType
	MaxAge time.Duration
}

// queryContextField is the audit-event Context key that holds free-text
// search query strings (set by SearchArtifacts / SearchDomains). It is
// the §8.4 "Query text" retention category, distinct from the event
// metadata the per-type Policy governs.
const queryContextField = "query"

// queryBearingEvent reports whether an event type carries free-text query
// content subject to the §8.4 query-text retention window.
func queryBearingEvent(t EventType) bool {
	return t == EventDomainsSearched || t == EventArtifactsSearched
}

// QueryRetention is the §8.4 "Query text: 30 days (redacted to
// placeholders after 7 days)" rule. Query text is a category distinct
// from audit-event metadata: the surrounding event is kept under the
// per-type Policy, but its query field is replaced with Placeholder once
// it is older than PlaceholderAfter and removed entirely once it is older
// than DropAfter. A zero duration disables that stage.
type QueryRetention struct {
	PlaceholderAfter time.Duration
	DropAfter        time.Duration
	Placeholder      string
}

// DefaultQueryRetention returns the §8.4 defaults: placeholder after 7
// days, drop after 30 days.
func DefaultQueryRetention() *QueryRetention {
	return &QueryRetention{
		PlaceholderAfter: 7 * 24 * time.Hour,
		DropAfter:        30 * 24 * time.Hour,
		Placeholder:      "[redacted]",
	}
}

// apply transitions a single event's query field for its age. It returns
// true when the event was modified. A nil receiver, a non-query event, or
// an event with no query field is a no-op. The drop stage takes
// precedence over the placeholder stage for an event past both marks.
func (q *QueryRetention) apply(e *Event, now time.Time) bool {
	if q == nil || !queryBearingEvent(e.Type) {
		return false
	}
	cur, ok := e.Context[queryContextField]
	if !ok {
		return false
	}
	age := now.Sub(e.Timestamp)
	if q.DropAfter > 0 && age > q.DropAfter {
		delete(e.Context, queryContextField)
		return true
	}
	if q.PlaceholderAfter > 0 && age > q.PlaceholderAfter {
		if cur == q.Placeholder {
			return false
		}
		e.Context[queryContextField] = q.Placeholder
		return true
	}
	return false
}

// Enforce rewrites sink's underlying file with every event older
// than its per-type policy removed. The hash chain is rebuilt over
// the surviving events. When the rewrite changes the log it appends an
// audit.retention_enforced marker recording the superseded chain head,
// so an external anchor of the prior head can be reconciled and
// re-anchored (§8.6).
//
// When two policies cover the same Type, the most-restrictive
// (smallest MaxAge) wins — the §8.4 retention-by-default behavior.
//
// queryRet applies the §8.4 query-text window (placeholder at 7 days,
// drop at 30 days) to the surviving search events; pass nil to skip it.
//
// Returns the number of events dropped (query-text redaction of a kept
// event does not count as a drop). Errors are returned as-is; the file is
// left in its prior state on rewrite failure.
func Enforce(_ context.Context, sink *FileSink, now time.Time, policies []Policy, queryRet *QueryRetention) (int, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	events, err := readAllEvents(sink.path)
	if err != nil {
		return 0, err
	}
	maxAge := map[EventType]time.Duration{}
	for _, p := range policies {
		existing, ok := maxAge[p.Type]
		if !ok || p.MaxAge < existing {
			maxAge[p.Type] = p.MaxAge
		}
	}
	kept := events[:0:0]
	dropped := 0
	redacted := false
	for i := range events {
		e := events[i]
		if max, ok := maxAge[e.Type]; ok && now.Sub(e.Timestamp) > max {
			dropped++
			continue
		}
		// §8.4 query-text window: keep the event, age out its query field.
		if queryRet.apply(&e, now) {
			redacted = true
		}
		kept = append(kept, e)
	}
	if dropped == 0 && !redacted {
		return 0, nil
	}
	// §8.4/§8.6: dropping events rebuilds the hash chain, which invalidates
	// any external anchor of the prior chain head. Append a boundary marker
	// recording the superseded head and the drop count so a verifier
	// holding an older anchor can reconcile it with the truncated log and
	// an anchor scheduler re-anchors the new head. Query-text
	// redaction also rewrites the chain, but it removes no events; the
	// marker is reserved for drops so a redaction-only pass stays quiet.
	if dropped > 0 {
		supersededHead := ""
		if len(events) > 0 {
			supersededHead = events[len(events)-1].Hash
		}
		// Spec: §8.1: audit.retention_enforced describes the registry as a whole, so it records no tenant.
		kept = append(kept, Event{
			Type:      EventRetentionEnforced,
			Timestamp: now,
			Caller:    "system:retention",
			Target:    supersededHead,
			Context: map[string]string{
				"dropped":         fmt.Sprintf("%d", dropped),
				"superseded_head": supersededHead,
			},
		})
	}
	if err := rewriteWithChain(sink.path, kept); err != nil {
		return 0, err
	}
	if len(kept) > 0 {
		sink.lastHash = kept[len(kept)-1].Hash
	} else {
		sink.lastHash = ""
	}
	return dropped, nil
}

// EraseScope selects the records an erasure reads and rewrites. A record is
// in scope when it carries a tenant label and Tenant is empty or equal to that
// label, or when it carries no label and Unlabeled is set. A multi-tenant
// registry passes {Tenant: routed}, a single-tenant registry passes
// {Tenant: bound, Unlabeled: true}, and the offline CLI form passes
// {Unlabeled: true}, which reaches every record.
//
// Spec: §8.5
type EraseScope struct {
	// Tenant selects records labeled with this tenant. An empty Tenant
	// matches every tenant label, which only the offline whole-file form uses.
	Tenant string
	// Unlabeled also selects records that carry no tenant.
	Unlabeled bool
}

// ErrEraseScope reports an EraseScope that selects nothing coherent. The zero
// value is refused so a caller that forgot the tenant cannot erase across
// tenants.
var ErrEraseScope = errors.New("audit: erase scope names no tenant and excludes unlabeled records")

// includes reports whether a record carrying the tenant label recTenant is
// in scope.
func (s EraseScope) includes(recTenant string) bool {
	if recTenant == "" {
		return s.Unlabeled
	}
	return s.Tenant == "" || s.Tenant == recTenant
}

// EraseUser implements the §8.5 GDPR right-to-be-forgotten flow over the
// records scope selects: every directly-identifying field of the erased user
// (the sub-claim Caller, the attached CallerEmail, the CallerGroups
// membership, and any userID-bearing Context value) is replaced with the
// salted tombstone redacted-<sha256(user_id+salt)> or, for group membership,
// cleared. The chain is then rewritten over every record, so out-of-scope
// records keep their content and receive new hash and prev_hash values. The
// salted tombstone preserves cross-event correlation for SIEM consumers that
// know the salt while removing the original identifier.
//
// §8.5 takes a single <user_id> argument without fixing which identity field
// it denotes. §8.1 records a read event's caller as the OAuth sub-claim
// (Caller) with the email attached separately (CallerEmail), so the value a
// human knows for a GDPR request is usually the email while the sub-claim is
// what appears in the layer-owner context. EraseUser therefore
// first discovers every alias of the user by scanning in-scope events whose
// Caller or CallerEmail matches the passed userID and collecting both fields,
// so passing either the email or the sub-claim erases the complete identity.
// The alias set comes from in-scope records only: an alias seen only in
// another tenant's records must not drive redaction in the requesting tenant.
// The redaction pass then removes the email and group membership, not
// just the sub-claim, so the persisted record no longer carries the user's
// PII. Public-mode CallerNetwork attributes (source IP, X-Forwarded-User)
// describe the request path of a system:public call rather than the erased
// user's account identity and are left intact.
//
// EraseUser appends a user.erased audit event built by UserErasedEvent,
// labeled with scope.Tenant and recording the invoking admin and the chain
// head the rewrite superseded. The same userID with two salts produces two
// unrelated tombstones, which is the desired property.
//
// salt must be non-empty (§8.5): an empty salt reduces the
// tombstone to sha256(user_id), which is reversible by brute force or
// dictionary over candidate user IDs and defeats de-identification. The zero
// scope is refused with ErrEraseScope before the file is read.
//
// Returns the number of in-scope events transformed (excludes the appended
// user.erased event). The count covers nothing outside the scope, so it
// carries no information about other tenants' records.
func EraseUser(_ context.Context, sink *FileSink, userID, salt, admin string, scope EraseScope) (int, error) {
	if userID == "" {
		return 0, fmt.Errorf("audit.erase: userID is required")
	}
	if salt == "" {
		return 0, fmt.Errorf("audit.erase: salt is required (an empty salt yields a guessable tombstone)")
	}
	if scope == (EraseScope{}) {
		return 0, fmt.Errorf("audit.erase: %w", ErrEraseScope)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	events, err := readAllEvents(sink.path)
	if err != nil {
		return 0, err
	}
	aliases := eraseAliases(events, userID, scope)
	tombstone := tombstoneFor(userID, salt)
	transformed := 0
	for i := range events {
		if !scope.includes(events[i].Tenant) {
			continue
		}
		if redactEvent(&events[i], aliases, tombstone) {
			transformed++
		}
	}
	// Spec: §8.6: the user.erased record names the head this rewrite
	// supersedes so a verifier holding an anchor of it can reconcile.
	supersededHead := ""
	if len(events) > 0 {
		supersededHead = events[len(events)-1].Hash
	}
	events = append(events, UserErasedEvent(userID, salt, admin, scope.Tenant, transformed, supersededHead))
	if err := rewriteWithChain(sink.path, events); err != nil {
		return 0, err
	}
	sink.lastHash = events[len(events)-1].Hash
	return transformed, nil
}

// eraseAliases is the §8.5 alias-discovery pass: an in-scope event belongs
// to the erased user when the passed userID matches its sub-claim or its
// email. Both identifiers are collected from every such event so passing one
// form (e.g. the email) also redacts records that carry the other (e.g. the
// sub-claim in the layer-owner context). The empty string is never an alias,
// so events with an empty Caller or CallerEmail do not poison the set.
func eraseAliases(events []Event, userID string, scope EraseScope) map[string]bool {
	aliases := map[string]bool{userID: true}
	for i := range events {
		ev := &events[i]
		if !scope.includes(ev.Tenant) {
			continue
		}
		if ev.Caller == userID || ev.CallerEmail == userID {
			if ev.Caller != "" {
				aliases[ev.Caller] = true
			}
			if ev.CallerEmail != "" {
				aliases[ev.CallerEmail] = true
			}
		}
	}
	return aliases
}

// redactEvent replaces every alias in ev with the tombstone and reports
// whether anything changed.
func redactEvent(ev *Event, aliases map[string]bool, tombstone string) bool {
	mutated := false
	// Redact the full caller identity when either identity field
	// is an alias of the erased user: the sub-claim, the attached email,
	// and the group membership. Group names are quasi-identifiers, so the
	// membership is cleared rather than tombstoned.
	if aliases[ev.Caller] || aliases[ev.CallerEmail] {
		if ev.Caller != "" {
			ev.Caller = tombstone
		}
		if ev.CallerEmail != "" {
			ev.CallerEmail = tombstone
		}
		if len(ev.CallerGroups) > 0 {
			ev.CallerGroups = nil
		}
		mutated = true
	}
	// The erased user can also appear as a context value (e.g. the owner of
	// a registered layer). Redact only the matching value; the surrounding
	// caller may be a different principal, such as an admin acting on the
	// user's layer.
	for k, v := range ev.Context {
		if aliases[v] {
			ev.Context[k] = tombstone
			mutated = true
		}
	}
	return mutated
}

// UserErasedEvent builds the §8.5 user.erased record. EraseUser and the
// registry's endpoint-sink path both call it, so the tombstone and the
// context layout are defined once. The invoking admin is recorded as the
// event Caller and in the admin context field (§8.1: "Admin invoked the GDPR
// erasure command"); with no admin (an internal call) the Caller falls back
// to system:retention. supersededHead is the chain head the rewrite
// replaced, recorded as superseded_head when non-empty; an endpoint-sink
// erase rewrites no chain and passes "".
//
// Spec: §8.5, §8.6
func UserErasedEvent(userID, salt, admin, tenant string, transformed int, supersededHead string) Event {
	caller := admin
	if caller == "" {
		caller = "system:retention"
	}
	ctx := map[string]string{"transformed": fmt.Sprintf("%d", transformed)}
	if admin != "" {
		ctx["admin"] = admin
	}
	if supersededHead != "" {
		ctx["superseded_head"] = supersededHead
	}
	return Event{
		Type:      EventUserErased,
		Timestamp: time.Now().UTC(),
		Caller:    caller,
		Target:    tombstoneFor(userID, salt),
		Tenant:    tenant,
		Context:   ctx,
	}
}

// tombstoneFor returns the §8.5 audit redaction value
// redacted-<sha256(user_id+salt)>: the full 32-byte SHA-256 digest of the
// user id concatenated with the salt, with no delimiter between them.
func tombstoneFor(userID, salt string) string {
	h := sha256.Sum256([]byte(userID + salt))
	return "redacted-" + hex.EncodeToString(h[:])
}

// readAllEvents loads every event from a file-backed sink. A
// missing file yields nil, nil.
func readAllEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []Event{}
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			return nil, fmt.Errorf("audit: parse event: %w", err)
		}
		out = append(out, eventFromJSON(je))
	}
	return out, nil
}

// rewriteWithChain writes events to path under a fresh hash chain.
// Uses an atomic rename so partial writes don't corrupt the log.
func rewriteWithChain(path string, events []Event) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prev := ""
	for i := range events {
		events[i].PrevHash = prev
		h := sha256.Sum256(append(events[i].canonicalBody(), []byte(prev)...))
		events[i].Hash = hex.EncodeToString(h[:])
		prev = events[i].Hash
		line, err := json.Marshal(eventForJSON(events[i]))
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
