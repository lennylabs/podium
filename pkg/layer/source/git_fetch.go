package source

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/lennylabs/podium/pkg/spi"
)

// DefaultFetchDepth is the §13.12 default of PODIUM_GIT_FETCH_DEPTH, the
// starting depth in commits of each call of the Git source.
//
// Spec: §7.3.1 (Git fetch), §13.12
const DefaultFetchDepth = 16

// MaxFetchDepth is the largest PODIUM_GIT_FETCH_DEPTH. The next value,
// fullHistoryDepth, is Git's request for the complete history, which only a
// deepening round sends.
//
// Spec: §13.12
const MaxFetchDepth = 2147483646

// fullHistoryDepth is the depth Git reads as a request for the complete
// history; `git fetch --unshallow` sends it. The round that requests it is the
// last round of a call.
//
// Spec: §7.3.1 (Git fetch: Deepening)
const fullHistoryDepth = 2147483647

// FetchDepthSetting names the operator setting that sets Git.FetchDepth.
const FetchDepthSetting = "PODIUM_GIT_FETCH_DEPTH"

// Members of the details of a call that reached the PODIUM_GIT_FETCH_TIMEOUT
// deadline.
//
// Spec: §7.3.1 (Git fetch: Deadline)
const (
	// FetchDeadlineConstraint is details.constraint of such a failure.
	FetchDeadlineConstraint = "fetch_deadline"
	// OperationSnapshot is details.operation of a snapshot.
	OperationSnapshot = "snapshot"
	// OperationLineageQuery is details.operation of a lineage query.
	OperationLineageQuery = "lineage_query"
)

// gitCall is one call of the Git source, which is one snapshot or one lineage
// query with every round it makes. It holds the in-memory repository that the
// call's clone creates and that its deepening rounds extend.
type gitCall struct {
	cfg   LayerConfig
	proxy transport.ProxyOptions
	auth  transport.AuthMethod
	repo  *git.Repository
	// depth is the depth of the latest round. The goroutine that runs the call
	// is the only one that reads or writes it.
	depth int
	// round mirrors depth for the call's owner, which reads it from another
	// goroutine when the deadline passes, to name the round that was running.
	round atomic.Int64
}

// fetchAt records depth as the depth of the round in flight.
func (c *gitCall) fetchAt(depth int) {
	c.depth = depth
	c.round.Store(int64(depth))
}

// clone clones the tracked ref alone into a new in-memory repository at
// depth, with no tags: go-git fetches every tag on a clone unless told
// otherwise, and each tag brings in commits that the tip does not reach.
//
// Spec: §7.3.1 (Git fetch)
func (c *gitCall) clone(ctx context.Context, depth int) error {
	c.fetchAt(depth)
	repo, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{
		URL:           c.cfg.Repo,
		Auth:          c.auth,
		ReferenceName: refNameFor(c.cfg.Ref),
		SingleBranch:  true,
		Depth:         depth,
		Tags:          git.NoTags,
		ProxyOptions:  c.proxy,
	})
	if err != nil {
		// Spec: §7.3.1 (Repository credentials). go-git and net/http print the
		// request URL, userinfo included, in a failed clone's error.
		return fmt.Errorf("%w: %s", ErrSourceUnreachable, RedactCloneError(c.cfg.Repo, err))
	}
	c.repo = repo
	return nil
}

// deepen fetches the tracked ref again into the call's repository at depth,
// with no tags. A fetch that brings in nothing new is not an error here: the
// progress rule of settle decides what an unchanged history means.
//
// Spec: §7.3.1 (Git fetch: Deepening)
func (c *gitCall) deepen(ctx context.Context, depth int) error {
	c.fetchAt(depth)
	err := c.repo.FetchContext(ctx, &git.FetchOptions{
		Auth:         c.auth,
		Depth:        depth,
		Tags:         git.NoTags,
		ProxyOptions: c.proxy,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("%w: %s", ErrSourceUnreachable, RedactCloneError(c.cfg.Repo, err))
	}
	return nil
}

// nextDepth is the depth of the round after a round at depth: four times the
// depth, and fullHistoryDepth once that would exceed it. The comparison runs
// before the multiplication so it cannot overflow a 32-bit int.
func nextDepth(depth int) int {
	if depth > fullHistoryDepth/4 {
		return fullHistoryDepth
	}
	return depth * 4
}

// progress identifies the walk that measures a call's progress after one
// evaluation, and how far that walk got.
type progress struct {
	start     plumbing.Hash
	reached   int
	truncated bool
}

func progressOf(start plumbing.Hash, w *commitWalk) progress {
	return progress{start: start, reached: len(w.reached), truncated: w.truncated}
}

// evalFunc evaluates the answers of a call over the repository as it stands.
// It reports whether every answer is decided and the progress of the walk
// that measures the call.
type evalFunc func(ctx context.Context, walks *walkCache) (decided bool, p progress, err error)

// settle evaluates the call's answers and deepens the history until every
// answer is decided. No setting limits the rounds: the progress rule, the
// schedule of depths, and the call's deadline end it. A call makes at most 17
// rounds from a starting depth of 1 and at most 15 from the default of 16,
// whatever the remote returns.
//
// Spec: §7.3.1 (Git fetch: Deepening, Termination)
func (c *gitCall) settle(ctx context.Context, eval evalFunc) error {
	var last *progress
	for {
		decided, p, err := eval(ctx, newWalkCache(c.repo))
		if err != nil || decided {
			return err
		}
		if !p.truncated {
			// Unreachable: every other walk of a call starts at a commit that
			// the measured walk reached, so a complete measured walk leaves no
			// answer undecided. The guard fails closed rather than loop.
			return fmt.Errorf("%w: git history of %s is complete and an answer is still undecided", ErrSourceUnreachable, p.start)
		}
		if last != nil && last.start == p.start && p.reached <= last.reached {
			return fmt.Errorf("%w: git fetch at depth %d added no commit to the truncated history of %s", ErrSourceUnreachable, c.depth, p.start)
		}
		if c.depth >= fullHistoryDepth {
			return fmt.Errorf("%w: git history of %s is still truncated after a fetch of the complete history", ErrSourceUnreachable, p.start)
		}
		last = &p
		if err := c.deepen(ctx, nextDepth(c.depth)); err != nil {
			return err
		}
	}
}

// fetchDeadlineError is the failure of a call that reached the
// PODIUM_GIT_FETCH_TIMEOUT deadline. It unwraps to an ingest.source_unreachable
// envelope that carries the details, which spi.AsError finds first, and to
// ErrSourceUnreachable, so errors.Is keeps matching the sentinel.
type fetchDeadlineError struct {
	envelope *spi.Error
}

func (e *fetchDeadlineError) Error() string   { return e.envelope.Message }
func (e *fetchDeadlineError) Unwrap() []error { return []error{e.envelope, ErrSourceUnreachable} }

// newFetchDeadlineError builds the failure of a call of operation that
// reached the deadline timeout during a round at depth.
//
// Spec: §7.3.1 (Git fetch: Deadline), §13.12
func newFetchDeadlineError(operation string, timeout time.Duration, depth int) error {
	what := "git snapshot"
	if operation == OperationLineageQuery {
		what = "git lineage query"
	}
	return &fetchDeadlineError{envelope: &spi.Error{
		Code:      ErrSourceUnreachable.Code,
		Message:   fmt.Sprintf("%s: %s did not finish within %s (%s)", ErrSourceUnreachable.Message, what, FetchTimeoutSetting, timeout),
		Retryable: ErrSourceUnreachable.Retryable,
		Details: map[string]any{
			"constraint": FetchDeadlineConstraint,
			"setting":    FetchTimeoutSetting,
			"operation":  operation,
			"depth":      depth,
		},
	}}
}

// runCall runs fn as one call of the Git source for operation under the
// FetchTimeout deadline, on every transport, so a remote that hangs, trickles
// data, or keeps the call deepening fails it as ErrSourceUnreachable instead
// of holding the layer's ingest cycle, and its claim, without bound.
//
// Spec: §7.3.1 (Source unreachable, Git fetch: Deadline), §13.12
func runCall[T any](ctx context.Context, g Git, cfg LayerConfig, operation string, fn func(context.Context, *gitCall) (T, error)) (T, error) {
	timeout := g.fetchTimeout()
	deadline := time.Now().Add(timeout)
	fetchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	proxyOpts, releaseDial := snapshotProxyOptions(fetchCtx, cfg.Repo)
	defer releaseDial()
	// The call owns the SSH agent connection of an ssh remote and closes it
	// when it returns, on success, on failure, and at the deadline.
	auth, releaseAuth := snapshotSSHAuth(cfg.Repo)
	defer releaseAuth()
	call := &gitCall{cfg: cfg, proxy: proxyOpts, auth: auth}
	v, err := bounded(fetchCtx, func(ctx context.Context) (T, error) { return fn(ctx, call) })
	// Only the expiry of this deadline names the setting. The call's own error
	// is dropped in that case, because its cause is the deadline.
	if err != nil && fetchDeadlinePassed(ctx, fetchCtx, deadline) {
		var zero T
		return zero, newFetchDeadlineError(operation, timeout, int(call.round.Load()))
	}
	return v, err
}

// fetchDeadlinePassed reports whether a failed call ended because the
// FetchTimeout deadline passed. A caller context that ended, or whose own
// deadline comes first, owns the failure, which keeps its own error.
//
// The clock is read as well as fetchCtx.Err: a context records its expiry
// only when its timer runs, and a transport's own timer or a peer's close at
// the same instant can fail the clone first, while Err is still nil.
func fetchDeadlinePassed(ctx, fetchCtx context.Context, deadline time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		return false
	}
	return errors.Is(fetchCtx.Err(), context.DeadlineExceeded) || !time.Now().Before(deadline)
}

// bounded runs fn and returns no later than ctx's end.
//
// fn runs in its own goroutine, and this function returns at ctx's end
// without waiting for it, because not every step of a fetch can be
// interrupted:
//
//   - go-git's git:// transport dials with net.Dial, with no deadline and no
//     hook, before it reads ctx, so a git:// remote whose address drops the
//     connection attempt keeps the goroutine in that dial until the operating
//     system abandons it, which takes a minute or two. It holds one connection
//     attempt and an empty in-memory storer, and once the dial ends it reads
//     the expired ctx, fails, and closes the connection.
//   - go-git resolves the deltas of a received pack, which it has already read
//     in full, without reading ctx, so a deadline that passes then leaves the
//     goroutine resolving them, for a time and with memory that grow with the
//     pack.
//
// Every other step ends at ctx's end: the http transport honours ctx on every
// request, the ssh transport dials through deadlineDialer, which closes the
// connection when ctx ends, and the history walks and the tree read check ctx
// between their steps. The goroutine starts no further round once ctx ends,
// because each evaluation begins with a walk that reads ctx first.
func bounded[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := fn(ctx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		// A call that finished as the deadline passed keeps its result.
		select {
		case r := <-done:
			return r.v, r.err
		default:
			var zero T
			return zero, fmt.Errorf("%w: %v", ErrSourceUnreachable, ctx.Err())
		}
	}
}
