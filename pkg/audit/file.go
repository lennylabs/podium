package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileSink writes audit events as JSON Lines to ~/.podium/audit.log
// or a configured override path (§8.3 LocalAuditSink). Concurrent
// appends are safe under typical event sizes per §8.3:
// "POSIX PIPE_BUF-bounded atomic writes." A single shared log written by
// multiple processes is a forest of per-writer hash chains; Verify
// validates it accordingly (see Verify).
type FileSink struct {
	mu       sync.Mutex
	path     string
	lastHash string
}

// NewFileSink opens (or creates) path and returns a hash-chained
// FileSink. If path is empty, defaults to ~/.podium/audit.log.
//
// On open, the sink scans the existing file (when present) and
// recovers the last event's hash so the chain continues across
// server restarts.
func NewFileSink(path string) (*FileSink, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".podium", "audit.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	sink := &FileSink{path: path}
	last, err := lastChainHash(path)
	if err != nil {
		return nil, err
	}
	sink.lastHash = last
	return sink, nil
}

// lastChainHash reads the existing log file (if any) and returns
// the last event's Hash. Missing file returns "" so the next
// Append seeds a fresh chain. Empty / corrupt last lines fall
// back to the most-recent parseable line.
func lastChainHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var last string
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			continue
		}
		last = je.Hash
	}
	return last, nil
}

// Append writes the next event in the chain.
func (f *FileSink) Append(_ context.Context, e Event) error {
	normalizeEventClient(&e)
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	_, _, err := f.appendChained(e)
	return err
}

// appendChained chains e onto the file's head under f.mu, writes its line
// in one call, and returns the chained event and its JSON line without the
// newline. The head advances only after the write succeeds, so a failed
// open or write leaves lastHash unchanged and the event unchained (§8.3
// Loss accounting). A part of the line that a failed write stored stays in
// the file: the head is read from the file only by NewFileSink, because
// re-reading it here could hide a loss that Verify reports.
//
// Spec: §8.3, §8.6
func (f *FileSink) appendChained(e Event) (Event, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	e = chainEvent(e, f.lastHash)
	line, err := encodeLine(e)
	// jsonEvent holds only strings, ints, bools, and string slices and maps,
	// so encoding cannot fail; the error is kept rather than discarded.
	if err != nil {
		return Event{}, nil, err
	}
	file, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Event{}, nil, err
	}
	defer func() { _ = file.Close() }()
	// A write to a file that opened for appending fails only on a device
	// error or a full disk, which a test cannot induce portably. The failed
	// open above takes the same return, and the tests pin that path.
	if _, err := file.Write(append(line[:len(line):len(line)], '\n')); err != nil {
		return Event{}, nil, err
	}
	f.lastHash = e.Hash
	return e, line, nil
}

// chainEvent sets e's PrevHash to prev and its Hash to
// sha256(body || prev_hash) (§8.6).
func chainEvent(e Event, prev string) Event {
	e.PrevHash = prev
	hash := sha256.Sum256(append(e.canonicalBody(), []byte(prev)...))
	e.Hash = hex.EncodeToString(hash[:])
	return e
}

// encodeLine returns e's JSON record line without the newline, the bytes
// every destination carries (§8.1, §8.3).
func encodeLine(e Event) ([]byte, error) {
	return json.Marshal(eventForJSON(e))
}

// Verify re-reads the audit log and validates the hash chain. Returns
// ErrChainBroken on any mismatch.
//
// §9 scopes the local sink's concurrency guarantee to PIPE_BUF-bounded
// atomic appends: several MCP server processes can append to one
// ~/.podium/audit.log concurrently (§14.13). Each process chains off its
// own in-process lastHash, so the file is a forest of per-writer chains
// rather than one linear chain. Verifying it as a single linear chain
// reported a spurious ErrChainBroken the moment two writers interleaved.
// Verification therefore checks, per §8.6, that every event's
// own hash satisfies event_hash = sha256(body || prev_hash), and that a
// non-empty PrevHash references the Hash of some earlier event in the log.
// A single-writer log is the degenerate one-chain case and still verifies.
// Tampering with an event body breaks its self-hash, and deleting an
// interior event leaves a later event's PrevHash dangling; both surface as
// ErrChainBroken.
func (f *FileSink) Verify(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	seen := map[string]bool{}
	idx := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			return errChainAt(ErrChainBroken, idx, "unparseable event")
		}
		e := eventFromJSON(je)
		want := sha256.Sum256(append(e.canonicalBody(), []byte(e.PrevHash)...))
		if hex.EncodeToString(want[:]) != e.Hash {
			return errChainAt(ErrChainBroken, idx, "Hash mismatch")
		}
		if e.PrevHash != "" && !seen[e.PrevHash] {
			return errChainAt(ErrChainBroken, idx, "PrevHash references no earlier event")
		}
		seen[e.Hash] = true
		idx++
	}
	return nil
}

// Path returns the file the sink writes to. Used by tests + operator
// commands.
func (f *FileSink) Path() string { return f.path }

// jsonEvent is the wire form of an event in the JSON-Lines log.
//
// The caller identity attributes are nested under a single "caller" object
// so their wire keys are the dotted names §8.1, §13.2.2, and §13.10 use:
// caller.identity, caller.email, caller.groups, caller.network, and
// caller.public_mode. A SIEM consumer keying on the dotted path
// caller.public_mode resolves the nested field directly. spec: §8.1.
type jsonEvent struct {
	Type           string            `json:"type"`
	Timestamp      string            `json:"timestamp"`
	TraceID        string            `json:"trace_id,omitempty"`
	Caller         *jsonCaller       `json:"caller,omitempty"`
	Target         string            `json:"target,omitempty"`
	Context        map[string]string `json:"context,omitempty"`
	Tenant         string            `json:"tenant,omitempty"`
	ResolvedLayers []string          `json:"resolved_layers,omitempty"`
	ResultSize     int               `json:"result_size,omitempty"`
	SchemaVersion  int               `json:"schema_version,omitempty"`
	Hash           string            `json:"hash"`
	PrevHash       string            `json:"prev_hash,omitempty"`
}

// jsonCaller is the wire form of the §8.1 caller-identity attributes. The
// nested keys serialize the dotted names the spec illustrates: the OAuth
// sub-claim under caller.identity, the attached email and groups, the
// public-mode source network under caller.network, and the public-mode flag
// under caller.public_mode. The public-mode-only fields (network,
// public_mode) are omitted for authenticated callers. spec: §8.1, §13.2.2,
// §13.10.
type jsonCaller struct {
	Identity   string       `json:"identity,omitempty"`
	Email      string       `json:"email,omitempty"`
	Groups     []string     `json:"groups,omitempty"`
	Network    *jsonNetwork `json:"network,omitempty"`
	Client     *jsonClient  `json:"client,omitempty"`
	PublicMode bool         `json:"public_mode,omitempty"`
}

// jsonClient is the wire form of CallerClient (§8.1 caller.client). Each
// member appears only when non-empty.
type jsonClient struct {
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Host        string `json:"host,omitempty"`
	HostVersion string `json:"host_version,omitempty"`
	Harness     string `json:"harness,omitempty"`
}

// jsonNetwork is the wire form of CallerNetwork (§8.1 caller.network).
type jsonNetwork struct {
	SourceIP      string `json:"source_ip,omitempty"`
	ForwardedUser string `json:"forwarded_user,omitempty"`
}

func eventForJSON(e Event) jsonEvent {
	return jsonEvent{
		Type:           string(e.Type),
		Timestamp:      e.Timestamp.UTC().Format(time.RFC3339Nano),
		TraceID:        e.TraceID,
		Caller:         callerForJSON(e),
		Target:         e.Target,
		Context:        e.Context,
		Tenant:         e.Tenant,
		ResolvedLayers: e.ResolvedLayers,
		ResultSize:     e.ResultSize,
		SchemaVersion:  e.SchemaVersion,
		Hash:           e.Hash,
		PrevHash:       e.PrevHash,
	}
}

// callerForJSON builds the nested caller object, or nil when the event
// carries no caller attributes at all (so the "caller" key is omitted
// entirely rather than serialized as an empty object). Every caller
// attribute takes part in the check: an event whose only caller attribute is
// left out of it loses that attribute on disk and then fails Verify.
func callerForJSON(e Event) *jsonCaller {
	if e.Caller == "" && e.CallerEmail == "" && len(e.CallerGroups) == 0 &&
		e.CallerNetwork == nil && e.CallerClient == nil && !e.PublicMode {
		return nil
	}
	return &jsonCaller{
		Identity:   e.Caller,
		Email:      e.CallerEmail,
		Groups:     e.CallerGroups,
		Network:    networkForJSON(e.CallerNetwork),
		Client:     clientForJSON(e.CallerClient),
		PublicMode: e.PublicMode,
	}
}

func eventFromJSON(je jsonEvent) Event {
	t, _ := time.Parse(time.RFC3339Nano, je.Timestamp)
	e := Event{
		Type:           EventType(je.Type),
		Timestamp:      t,
		TraceID:        je.TraceID,
		Target:         je.Target,
		Context:        je.Context,
		Tenant:         je.Tenant,
		ResolvedLayers: je.ResolvedLayers,
		ResultSize:     je.ResultSize,
		SchemaVersion:  je.SchemaVersion,
		Hash:           je.Hash,
		PrevHash:       je.PrevHash,
	}
	if je.Caller != nil {
		e.Caller = je.Caller.Identity
		e.CallerEmail = je.Caller.Email
		e.CallerGroups = je.Caller.Groups
		e.CallerNetwork = networkFromJSON(je.Caller.Network)
		e.CallerClient = clientFromJSON(je.Caller.Client)
		e.PublicMode = je.Caller.PublicMode
	}
	return e
}

func networkForJSON(n *CallerNetwork) *jsonNetwork {
	if n == nil {
		return nil
	}
	return &jsonNetwork{SourceIP: n.SourceIP, ForwardedUser: n.ForwardedUser}
}

func networkFromJSON(n *jsonNetwork) *CallerNetwork {
	if n == nil {
		return nil
	}
	return &CallerNetwork{SourceIP: n.SourceIP, ForwardedUser: n.ForwardedUser}
}

func clientForJSON(c *CallerClient) *jsonClient {
	if c == nil {
		return nil
	}
	j := jsonClient(*c)
	return &j
}

func clientFromJSON(c *jsonClient) *CallerClient {
	if c == nil {
		return nil
	}
	cc := CallerClient(*c)
	return &cc
}

// splitLines mirrors strings.Split on '\n' but operates on bytes so
// we don't allocate a string per line.
func splitLines(data []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// fmtErr is a small helper kept available for parity with the other
// audit sink helpers.
func fmtErr(format string, args ...any) error { return fmt.Errorf(format, args...) }
