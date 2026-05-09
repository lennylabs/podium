package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Enforce rewrites sink's underlying file with every event older
// than its per-type policy removed. The hash chain is rebuilt over
// the surviving events; external anchoring of the prior chain head
// must be redone after Enforce.
//
// When two policies cover the same Type, the most-restrictive
// (smallest MaxAge) wins — the §8.4 retention-by-default behavior.
//
// Returns the number of events dropped. Errors are returned as-is;
// the file is left in its prior state on rewrite failure.
func Enforce(_ context.Context, sink *FileSink, now time.Time, policies []Policy) (int, error) {
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
	for _, e := range events {
		if max, ok := maxAge[e.Type]; ok && now.Sub(e.Timestamp) > max {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	if dropped == 0 {
		return 0, nil
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

// EraseUser implements the §8.5 GDPR right-to-be-forgotten flow:
// every Caller and userID-bearing Context value matching userID is
// replaced with a salted hash, then the chain is rewritten over the
// transformed events. The salted-hash form preserves cross-event
// correlation for SIEM consumers that know the salt while removing
// the original identifier.
//
// EraseUser appends a user.erased audit event to the rewritten log.
// Pass a unique salt per tenant; the same userID with two salts
// produces two unrelated tombstones, which is the desired property.
//
// Returns the number of events transformed (excludes the appended
// user.erased event).
func EraseUser(_ context.Context, sink *FileSink, userID, salt string) (int, error) {
	if userID == "" {
		return 0, fmt.Errorf("audit.erase: userID is required")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	events, err := readAllEvents(sink.path)
	if err != nil {
		return 0, err
	}
	tombstone := tombstoneFor(userID, salt)
	transformed := 0
	for i := range events {
		ev := &events[i]
		mutated := false
		if ev.Caller == userID {
			ev.Caller = tombstone
			mutated = true
		}
		for k, v := range ev.Context {
			if v == userID {
				ev.Context[k] = tombstone
				mutated = true
			}
		}
		if mutated {
			transformed++
		}
	}
	// Append the user.erased record so the action is itself audited.
	events = append(events, Event{
		Type:      EventUserErased,
		Timestamp: time.Now().UTC(),
		Caller:    "system:retention",
		Target:    tombstone,
		Context:   map[string]string{"transformed": fmt.Sprintf("%d", transformed)},
	})
	if err := rewriteWithChain(sink.path, events); err != nil {
		return 0, err
	}
	if len(events) > 0 {
		sink.lastHash = events[len(events)-1].Hash
	}
	return transformed, nil
}

func tombstoneFor(userID, salt string) string {
	h := sha256.Sum256([]byte(userID + "|" + salt))
	return "erased:" + hex.EncodeToString(h[:8])
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
			f.Close()
			os.Remove(tmp)
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
