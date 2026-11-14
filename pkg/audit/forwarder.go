package audit

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math/bits"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/podium/internal/clock"
)

// The §8.3 Delivery constants. The spec fixes them, so none has an
// operator override (§13.12).
//
// Spec: §8.3
const (
	// maxRecordBytes is the largest record any request may carry. A larger
	// record is dropped with reason rejected when it is pushed.
	maxRecordBytes = 1 << 20
	// maxBatchRecords and maxBatchBytes cut a batch.
	maxBatchRecords = 500
	maxBatchBytes   = 1 << 20
	// flushInterval is the cadence at which a partial batch is sent.
	flushInterval = time.Second
	// requestTimeout bounds every request, from the sender or the flush.
	requestTimeout = 10 * time.Second
	// backoffInitial and backoffMax bound the retry delay's cap.
	backoffInitial = time.Second
	backoffMax     = 60 * time.Second
	// failureLogInterval bounds the delivery-failure log line to one per
	// destination per interval while the destination keeps failing.
	failureLogInterval = time.Minute
)

// ticker is the flush cadence's ticker, so a test steps it by hand.
type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// The forwarder's time hooks. internal/clock exposes only Now, so the flush
// tick, the backoff sleep, and the request deadline go through these, and
// export_test.go replaces them, following pkg/webhook's newTimer. A
// forwarder copies them when it is built, so a test that replaces one
// before Open affects only the streams it opens.
var (
	newTicker      = func(d time.Duration) ticker { return realTicker{t: time.NewTicker(d)} }
	sleep          = sleepContext
	requestContext = context.WithTimeout
)

// sleepContext waits for d or for ctx to end, whichever comes first.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// sendResult is a receiver's answer to one batch. body is the start of the
// response body, which only the OTLP destination reads. bodyErr is set when
// body is incomplete: the read failed, or the body is longer than
// maxResponseBody.
type sendResult struct {
	status     int
	retryAfter string
	body       []byte
	bodyErr    error
}

// batchSender sends one batch to a network destination's receiver on ctx,
// which carries the request deadline. The HTTP and OTLP destinations
// implement it, and the forwarder classifies the result.
type batchSender interface {
	send(ctx context.Context, batch []record) (sendResult, error)
}

// outcome is what the forwarder does after one attempt.
type outcome int

const (
	outcomeDone    outcome = iota // delivered, or the one record dropped as rejected
	outcomeSplit                  // split the batch in half and send each half
	outcomeRetry                  // wait out the backoff and resend the batch
	outcomeStopped                // the context ended; the batch stays queued
)

// forwarder is one HTTP or OTLP destination's queue and sender (§8.3
// Delivery). push never blocks on the receiver. One sender goroutine sends
// batches from the head of the queue, and a record leaves the queue only
// when it is delivered or counted as dropped, so a batch held across a
// retry counts toward the bound and the queue depth.
//
// Spec: §8.3
type forwarder struct {
	name string
	dest batchSender
	// drop and queue are the stream's dropped and queued methods bound to
	// this destination's kind and name; the forwarder holds no Observer.
	drop  func(reason string, n int)
	queue func(delta int)
	clock clock.Clock
	rand  io.Reader

	newTicker      func(time.Duration) ticker
	sleep          func(context.Context, time.Duration) error
	requestContext func(context.Context, time.Duration) (context.Context, context.CancelFunc)

	// ready is signalled when the queue holds a full batch.
	ready chan struct{}

	// mu guards the queue (records, bytes), closed, cancel, and done.
	// Push reports AddQueued under mu, and removal reports the matching
	// negative delta under mu, so the observer never sees a removal before
	// its push.
	mu      sync.Mutex
	max     int64
	records []record
	bytes   int64
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}

	// backoff and the failure-log state belong to whichever goroutine is
	// sending: the sender, and after it exits, close. close waits for the
	// sender to exit before it sends, so they need no lock.
	backoff   time.Duration
	failing   bool
	failedLog time.Time
}

// newForwarder builds the forwarder of one network destination of s. Its
// queue holds at most s's resolved QueueMaxBytes of serialized records.
func newForwarder(s *Stream, kind destKind, name string, dest batchSender) *forwarder {
	return &forwarder{
		name:           name,
		dest:           dest,
		drop:           func(reason string, n int) { s.dropped(kind, name, reason, n) },
		queue:          func(delta int) { s.queued(kind, delta) },
		clock:          s.opts.Clock,
		rand:           s.rand,
		newTicker:      newTicker,
		sleep:          sleep,
		requestContext: requestContext,
		ready:          make(chan struct{}, 1),
		max:            s.opts.QueueMaxBytes,
		backoff:        backoffInitial,
	}
}

// push queues r. A record larger than 1 MiB is counted as rejected, and a
// record that does not fit in the remaining bound as queue_full, even on an
// empty queue. After close has started, r is counted as shutdown.
//
// Spec: §8.3
func (f *forwarder) push(r record) {
	size := int64(len(r.line))
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.closed:
		f.drop(ReasonShutdown, 1)
	case size > maxRecordBytes:
		f.drop(ReasonRejected, 1)
	case f.bytes+size > f.max:
		f.drop(ReasonQueueFull, 1)
	default:
		f.records = append(f.records, r)
		f.bytes += size
		f.queue(1)
		if f.fullLocked() {
			select {
			case f.ready <- struct{}{}:
			default:
			}
		}
	}
}

// fullLocked reports whether the queue holds a full batch. The caller holds
// f.mu.
func (f *forwarder) fullLocked() bool {
	return len(f.records) >= maxBatchRecords || f.bytes >= maxBatchBytes
}

func (f *forwarder) hasFullBatch() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fullLocked()
}

// peek returns the next batch from the head of the queue without removing
// it: at most 500 records and at most 1 MiB, and at least one record, which
// push guarantees is at most 1 MiB.
func (f *forwarder) peek() []record {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, size := 0, 0
	for n < len(f.records) && n < maxBatchRecords {
		l := len(f.records[n].line)
		if n > 0 && size+l > maxBatchBytes {
			break
		}
		size += l
		n++
	}
	return slices.Clone(f.records[:n])
}

// remove takes n records off the head of the queue and returns them. Only
// the goroutine that is sending calls it, and push only appends, so the
// head is the batch that goroutine sent.
func (f *forwarder) remove(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeLocked(n)
}

func (f *forwarder) removeLocked(n int) {
	for _, r := range f.records[:n] {
		f.bytes -= int64(len(r.line))
	}
	clear(f.records[:n])
	f.records = f.records[n:]
	f.queue(-n)
}

// start runs the sender on a context derived from ctx that only close
// cancels.
func (f *forwarder) start(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ctx, f.cancel = context.WithCancel(ctx)
	f.done = make(chan struct{})
	go f.run(ctx)
}

// run is the sender goroutine. It sends full batches as they form and
// everything queued on each tick. A tick that fires during a backoff sleep
// sends nothing, because the goroutine is inside the sleep.
func (f *forwarder) run(ctx context.Context) {
	defer close(f.done)
	t := f.newTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.ready:
			f.sendQueued(ctx, true)
		case <-t.C():
			f.sendQueued(ctx, false)
		}
	}
}

// sendQueued sends from the head of the queue until the queue is empty, or
// holds no full batch when fullOnly is set, or ctx ends. A split resends
// each half from the held records, in order, so removal is always from the
// head.
func (f *forwarder) sendQueued(ctx context.Context, fullOnly bool) {
	var pending [][]record
	for ctx.Err() == nil {
		if len(pending) == 0 {
			if fullOnly && !f.hasFullBatch() {
				return
			}
			batch := f.peek()
			if len(batch) == 0 {
				return
			}
			pending = [][]record{batch}
		}
		batch := pending[0]
		switch out, delay := f.attempt(ctx, batch); out {
		case outcomeDone:
			pending = pending[1:]
		case outcomeSplit:
			h := len(batch) / 2
			pending = append([][]record{batch[:h], batch[h:]}, pending[1:]...)
		case outcomeRetry:
			if f.sleep(ctx, delay) != nil {
				return
			}
		case outcomeStopped:
			return
		}
	}
}

// attempt sends batch once under requestContext(ctx, requestTimeout) and
// applies the §8.3 status classes. A request that ends because ctx ended
// leaves the batch queued. A request that times out while ctx is live is a
// network error and is retried.
//
// Spec: §8.3
func (f *forwarder) attempt(ctx context.Context, batch []record) (outcome, time.Duration) {
	rctx, cancel := f.requestContext(ctx, requestTimeout)
	res, err := f.dest.send(rctx, batch)
	cancel()
	switch {
	case err != nil && ctx.Err() != nil:
		return outcomeStopped, 0
	case err == nil && res.status >= 200 && res.status < 300:
		f.remove(len(batch))
		f.delivered()
		return outcomeDone, 0
	case err == nil && splitStatus(res.status):
		if len(batch) > 1 {
			return outcomeSplit, 0
		}
		f.remove(1)
		f.drop(ReasonRejected, 1)
		return outcomeDone, 0
	default:
		return outcomeRetry, f.failed(res, err)
	}
}

// splitStatus reports the statuses that split a batch: the receiver refused
// the content of one or more records.
func splitStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity
}

// delivered resets the backoff after a 2xx response and logs the recovery
// of a failing destination.
func (f *forwarder) delivered() {
	f.backoff = backoffInitial
	if f.failing {
		f.failing = false
		log.Printf("audit: delivery to %s recovered", f.name)
	}
}

// failed logs a retryable failure and returns the delay before the retry:
// a draw uniform in [0, cap], raised to the Retry-After delay when that is
// larger. The cap then doubles up to backoffMax whether or not the
// response carried Retry-After.
//
// Spec: §8.3
func (f *forwarder) failed(res sendResult, err error) time.Duration {
	f.logFailure(failureText(res, err))
	delay := f.draw(f.backoff)
	if err == nil {
		delay = max(delay, f.retryAfter(res.retryAfter))
	}
	f.backoff = min(2*f.backoff, backoffMax)
	return delay
}

// logFailure writes the failure line at the first failure, at a transition
// from delivering, and at most once per failureLogInterval while failing.
func (f *forwarder) logFailure(text string) {
	now := f.clock.Now()
	if f.failing && now.Sub(f.failedLog) < failureLogInterval {
		return
	}
	f.failing = true
	f.failedLog = now
	log.Printf("audit: delivery to %s failed: %s; retrying", f.name, text)
}

// failureText describes a failed attempt without the request URL, whose
// query can carry a credential: *url.Error quotes the full URL, so only its
// cause is kept.
func failureText(res sendResult, err error) string {
	if err == nil {
		return "status " + strconv.Itoa(res.status)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// draw returns a delay uniform in [0, ceiling] from the injected Rand. A
// Rand that fails yields the ceiling, the longest delay, so a failing
// source never turns the backoff into a tight retry loop.
func (f *forwarder) draw(ceiling time.Duration) time.Duration {
	var b [8]byte
	if _, err := io.ReadFull(f.rand, b[:]); err != nil {
		return ceiling
	}
	hi, _ := bits.Mul64(binary.BigEndian.Uint64(b[:]), uint64(ceiling)+1)
	return time.Duration(hi)
}

// retryAfter converts a Retry-After value, in seconds or as an HTTP-date
// against the injected clock, to a delay in [0, backoffMax]. An absent,
// unparseable, or past value gives 0.
//
// Spec: §8.3
func (f *forwarder) retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		d = time.Duration(min(max(secs, 0), int64(backoffMax/time.Second))) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(f.clock.Now())
	}
	return min(max(d, 0), backoffMax)
}

// close stops the queue from accepting records, stops the sender, and
// waits for it to exit. It then sends the whole queue from its head, under
// the batch, split, and retry rules and with each request bounded by
// requestContext(ctx, requestTimeout), until the queue is empty or ctx
// ends, and counts what remains as shutdown.
//
// The records left undelivered are reported only through the bound drop
// method, so the drop log's per-minute limit governs their one log line.
// close returns nil for them: an error describing them would reach
// Stream.Shutdown's log.Printf and write a second, unlimited line for the
// same destination and reason.
//
// Spec: §8.3
func (f *forwarder) close(ctx context.Context) error {
	f.mu.Lock()
	f.closed = true
	cancel, done := f.cancel, f.done
	f.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	f.sendQueued(ctx, false)
	f.dropRemaining()
	return nil
}

// dropRemaining counts every record still queued as shutdown and empties
// the queue, reporting nothing when it is already empty.
func (f *forwarder) dropRemaining() {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.records)
	if n == 0 {
		return
	}
	f.removeLocked(n)
	f.drop(ReasonShutdown, n)
}

// lockedReader serializes reads of a Rand that the stream's forwarders
// share, because a test's reader need not be safe for concurrent use.
type lockedReader struct {
	mu sync.Mutex
	r  io.Reader
}

func (l *lockedReader) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Read(p)
}
