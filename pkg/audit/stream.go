package audit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/podium/internal/clock"
)

// ErrConfig marks an audit configuration that cannot be used. Every error
// Open returns wraps it, so a caller refuses startup on any Open error with
// config.audit_sink_unavailable (§13.12).
var ErrConfig = errors.New("audit: configuration unusable")

// ErrSealed is returned by Append once Shutdown has sealed the stream at the
// process's exit. The event is not chained and reaches no destination.
//
// Spec: §8.3
var ErrSealed = errors.New("audit: stream sealed")

// DefaultQueueMaxBytes is the §13.12 default of PODIUM_AUDIT_QUEUE_MAX_BYTES,
// the byte bound of each HTTP and OTLP destination's queue (64 MiB).
//
// Spec: §13.12
const DefaultQueueMaxBytes int64 = 67108864

// envQueueMaxBytes is the one operator-tunable delivery setting (§13.12).
const envQueueMaxBytes = "PODIUM_AUDIT_QUEUE_MAX_BYTES"

// ShutdownTimeout is the §8.3 shutdown flush budget.
//
// Spec: §8.3
const ShutdownTimeout = 5 * time.Second

// OpenOptions configures Open.
type OpenOptions struct {
	// Local marks the MCP server's local stream: the list comes from
	// PODIUM_AUDIT_SINK, accepts the token default, and refuses stdout.
	Local bool
	// Service is the §13.8 tracing service name, carried as the OTLP
	// resource's service.name.
	Service string
	// HeadersFile, CAFile, CertFile, and KeyFile are the §13.12 audit
	// transport settings of the HTTP and OTLP destinations.
	HeadersFile, CAFile, CertFile, KeyFile string
	// QueueMaxBytes bounds each HTTP and OTLP destination's queue. A value
	// of 0 or less takes DefaultQueueMaxBytes.
	QueueMaxBytes int64
	// TracingStdout reports that PODIUM_TRACING=stdout writes spans to
	// standard output, which refuses a stdout destination.
	TracingStdout bool
	// Stdout is the stdout destination's writer; nil uses os.Stdout.
	Stdout io.Writer
	// Observer receives every delivery count from the first Append; nil
	// makes every count a no-op. Nothing sets or clears it after Open.
	Observer Observer
	// Clock stamps default timestamps and times the drop log; nil uses
	// clock.Real{}.
	Clock clock.Clock
	// Rand is the randomness source of service.instance.id; nil uses
	// crypto/rand.Reader.
	Rand io.Reader
}

// withDefaults returns opts with every nil or non-positive field resolved.
func (opts OpenOptions) withDefaults() OpenOptions {
	if opts.QueueMaxBytes <= 0 {
		opts.QueueMaxBytes = DefaultQueueMaxBytes
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Rand == nil {
		opts.Rand = rand.Reader
	}
	return opts
}

// ParseQueueMaxBytes parses the PODIUM_AUDIT_QUEUE_MAX_BYTES value. An empty
// value is the default, and any other value must be a base-10 integer
// greater than 0.
//
// Spec: §13.12
func ParseQueueMaxBytes(raw string) (int64, error) {
	if raw == "" {
		return DefaultQueueMaxBytes, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: %s=%q is not a positive base-10 integer", ErrConfig, envQueueMaxBytes, raw)
	}
	return n, nil
}

// record is one chained event and its JSON line, without the newline, as a
// destination receives it.
type record struct {
	event Event
	line  []byte
}

// queueDest is an asynchronous destination, HTTP or OTLP (§8.3 Delivery).
// push hands it a chained record and never blocks on a receiver, start
// runs its sender on ctx, and close stops the sender and flushes the queue
// under ctx. A destination reports counts only through the dropped and
// queued methods its stream binds to it.
type queueDest interface {
	push(r record)
	start(ctx context.Context)
	close(ctx context.Context) error
}

// Stream is one process's audit stream (§8.3): it chains each event once and
// writes the chained record to every destination in its list. It implements
// Sink, the §9.1 RegistryAuditSink and LocalAuditSink seam.
//
// With a file destination, FileSink.lastHash is the chain head and the
// stream keeps no copy, so a rewrite of the file cannot leave a stale head
// here. Without one, head is the chain head.
//
// Spec: §8.3, §8.6, §9.1
type Stream struct {
	opts  OpenOptions
	drops *dropLog
	// instanceID is the OTLP resource's service.instance.id, outside the
	// hashed body.
	instanceID string

	file   *FileSink
	stdout io.Writer
	queues []queueDest
	cancel context.CancelFunc

	// mu serializes chaining an event with handing it to every destination,
	// so every destination receives the records in chaining order. It
	// guards head and sealed.
	mu     sync.Mutex
	head   string
	sealed bool

	closeOnce sync.Once
	closeErr  error
}

// newStream returns an empty stream over opts, which must be resolved.
func newStream(opts OpenOptions) *Stream {
	return &Stream{opts: opts, drops: newDropLog(opts.Clock)}
}

// Open parses the destination list value and opens a stream over it. Every
// error wraps ErrConfig, a file destination that cannot be opened for
// appending included, so no process runs with no audit stream (§13.12).
// The forwarders start last, after every step that can fail, so an Open
// that returns an error leaves no goroutine and nothing to close.
//
// Spec: §8.3, §13.12
func Open(ctx context.Context, value string, opts OpenOptions) (*Stream, error) {
	opts = opts.withDefaults()
	specs, err := parseDestinations(value, opts)
	if err != nil {
		return nil, err
	}
	s := newStream(opts)
	for _, spec := range specs {
		if err := s.addDestination(spec); err != nil {
			return nil, err
		}
	}
	id, err := uuid.NewRandomFromReader(opts.Rand)
	if err != nil {
		return nil, fmt.Errorf("%w: generate service.instance.id: %w", ErrConfig, err)
	}
	s.instanceID = id.String()
	s.startQueues(ctx)
	return s, nil
}

// addDestination opens the destination spec names and attaches it to s.
func (s *Stream) addDestination(spec destSpec) error {
	variable := listVariable(s.opts)
	switch spec.kind {
	case kindFile:
		file, err := NewFileSink(spec.path)
		if err != nil {
			return fmt.Errorf("%w: %s: open file destination %s: %w", ErrConfig, variable, spec.name(), err)
		}
		if err := probeAppend(file.Path()); err != nil {
			return fmt.Errorf("%w: %s: file destination %s cannot be opened for appending: %w", ErrConfig, variable, file.Path(), err)
		}
		s.file = file
		return nil
	default:
		return fmt.Errorf("%w: %s: the %s destination is not available in this build", ErrConfig, variable, spec.kind)
	}
}

// probeAppend opens path with the flags appendChained uses and closes it
// without writing, so a file the process can read but not append to
// refuses startup rather than failing every later write.
//
// Spec: §13.12
func probeAppend(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// startQueues starts every network destination's sender on a context the
// stream owns, which Close cancels.
func (s *Stream) startQueues(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, q := range s.queues {
		q.start(ctx)
	}
}

// OpenFile opens a stream whose only destination is the file at path, for
// the offline admin forms. It starts no network destination and does not
// probe the file, because the forms write it only through a rewrite that
// reports its own failure.
//
// Spec: §8.4, §8.5
func OpenFile(path string, opts OpenOptions) (*Stream, error) {
	file, err := NewFileSink(path)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	s := newStream(opts.withDefaults())
	s.file = file
	return s, nil
}

// AsSink converts s to a Sink, returning a nil interface for a nil s so the
// downstream "sink == nil" guards keep working. Every site that stores a
// stream as a Sink calls it.
//
// Spec: §9.1
func AsSink(s *Stream) Sink {
	if s == nil {
		return nil
	}
	return s
}

// File returns the file destination, or nil when the list names none or s
// is nil.
func (s *Stream) File() *FileSink {
	if s == nil {
		return nil
	}
	return s.file
}

// Append chains e and writes it to every destination. It returns an error
// only when it did not chain e: the file destination's write error, or
// ErrSealed. Every other destination's failure is counted and never
// returned (§8.3 Loss accounting).
//
// Spec: §8.1, §8.3, §8.6
func (s *Stream) Append(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		return ErrSealed
	}
	e.SchemaVersion = SchemaVersion
	if e.Timestamp.IsZero() {
		e.Timestamp = s.opts.Clock.Now().UTC()
	}
	normalizeEventClient(&e)
	r, err := s.chain(e)
	if err != nil {
		return err
	}
	s.handOff(r)
	return nil
}

// chain chains e through the file destination when the list names one, and
// against s.head otherwise. A failed file write is counted for the file
// destination and returned. The caller holds s.mu.
func (s *Stream) chain(e Event) (record, error) {
	if s.file != nil {
		chained, line, err := s.file.appendChained(e)
		if err != nil {
			s.dropped(kindFile, s.file.Path(), ReasonWriteError, 1)
			return record{}, err
		}
		return record{event: chained, line: line}, nil
	}
	chained := chainEvent(e, s.head)
	line, err := encodeLine(chained)
	// jsonEvent holds only strings, ints, bools, and string slices and maps,
	// so encoding cannot fail; the error is kept rather than discarded.
	if err != nil {
		return record{}, err
	}
	s.head = chained.Hash
	return record{event: chained, line: line}, nil
}

// handOff writes r to every destination other than the file: first to each
// HTTP and OTLP queue, then to stdout, whatever order the list names them
// in, so a record whose stdout write blocks is already queued or counted
// for each network destination. The caller holds s.mu.
//
// Spec: §8.3
func (s *Stream) handOff(r record) {
	for _, q := range s.queues {
		q.push(r)
	}
	if s.stdout == nil {
		return
	}
	line := make([]byte, 0, len(r.line)+1)
	line = append(append(line, r.line...), '\n')
	if _, err := s.stdout.Write(line); err != nil {
		s.dropped(kindStdout, tokenStdout, ReasonWriteError, 1)
	}
}

// Verify checks the file destination's chain (§8.6), and returns nil when
// the list names no file.
//
// Spec: §8.6
func (s *Stream) Verify(ctx context.Context) error {
	if s.file == nil {
		return nil
	}
	return s.file.Verify(ctx)
}

// Close flushes every network destination concurrently, each on ctx, so a
// hung receiver cannot spend another destination's budget, and then
// cancels the stream's context. It is idempotent: a later call flushes
// nothing and returns the first call's result. Append keeps chaining after
// Close, and each network destination counts such a record as shutdown.
//
// Spec: §8.3
func (s *Stream) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		errs := make([]error, len(s.queues))
		var wg sync.WaitGroup
		for i, q := range s.queues {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = q.close(ctx)
			}()
		}
		wg.Wait()
		if s.cancel != nil {
			s.cancel()
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

// Shutdown flushes the stream within ShutdownTimeout, seals it, and then
// writes every drop count the drop log still holds. The flush context
// derives from context.Background, so a cancelled serve context never
// shortens the flush. The drop log is written after Seal because a sealed
// stream counts no further drop, so no count arrives after the last line.
// It is the one call a process makes when it ends.
//
// Spec: §6.8, §8.3
func (s *Stream) Shutdown() {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		log.Printf("audit: shutdown flush: %v", err)
	}
	s.Seal()
	s.drops.flush()
}

// Seal makes every later Append return ErrSealed. It takes s.mu, so an
// Append in progress finishes its chaining write and its hand-off to every
// destination before Seal returns, and a record cannot be chained and then
// left neither delivered nor counted when the process exits. A sealed
// stream stays sealed.
//
// Spec: §6.8, §8.3
func (s *Stream) Seal() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sealed = true
	s.mu.Unlock()
}
