package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/file"

	"github.com/lennylabs/podium/pkg/spi"
)

// fakeScheme is the URL scheme of the test transport. A remote URL with this
// scheme reaches a repository on disk through go-git's file transport, which
// runs `git upload-pack`, and the transport records each fetch and can change
// the depth the remote serves or stall a round.
const fakeScheme = "podium-test"

// TestMain installs the test transport before any test runs. go-git keeps its
// protocols in a package map with no lock, so installing it here, before the
// parallel tests start, keeps the map read-only while they run.
func TestMain(m *testing.M) {
	client.InstallProtocol(fakeScheme, fakeTransport{})
	os.Exit(m.Run())
}

// fakeRemote is the behaviour of one remote behind the test transport.
type fakeRemote struct {
	// serve, when set, returns the depth the remote serves in a round, given
	// the round's number from 1 and the depth the client requested.
	serve func(round, requested int) int
	// stallRound, when not zero, is the round whose fetch blocks until the
	// client's context ends.
	stallRound int

	mu        sync.Mutex
	requested []int
}

// record notes a fetch at depth and returns its round number.
func (r *fakeRemote) record(depth int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requested = append(r.requested, depth)
	return len(r.requested)
}

// rounds returns the depths the client requested, one per round.
func (r *fakeRemote) rounds() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.requested...)
}

// fakeRemotes maps the host of a test URL to the behaviour of its remote, so
// several remotes, each with its own record, can serve one repository.
var (
	fakeRemotes  sync.Map
	fakeRemoteID atomic.Int64
)

type fakeTransport struct{}

func (fakeTransport) NewUploadPackSession(ep *transport.Endpoint, auth transport.AuthMethod) (transport.UploadPackSession, error) {
	v, ok := fakeRemotes.Load(ep.Host)
	if !ok {
		return nil, fmt.Errorf("no fake remote %s", ep.Host)
	}
	local := *ep
	local.Protocol, local.Host = "file", ""
	s, err := file.DefaultClient.NewUploadPackSession(&local, auth)
	if err != nil {
		return nil, err
	}
	return &fakeSession{UploadPackSession: s, remote: v.(*fakeRemote)}, nil
}

func (fakeTransport) NewReceivePackSession(*transport.Endpoint, transport.AuthMethod) (transport.ReceivePackSession, error) {
	return nil, errors.New("the fake remote accepts no push")
}

// fakeSession rewrites the depth of each upload-pack request as its remote
// says, so a test can make a remote serve one more commit per round.
type fakeSession struct {
	transport.UploadPackSession
	remote *fakeRemote
}

func (s *fakeSession) UploadPack(ctx context.Context, req *packp.UploadPackRequest) (*packp.UploadPackResponse, error) {
	requested := 0
	if d, ok := req.Depth.(packp.DepthCommits); ok {
		requested = int(d)
	}
	round := s.remote.record(requested)
	if round == s.remote.stallRound {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.remote.serve != nil {
		req.Depth = packp.DepthCommits(s.remote.serve(round, requested))
	}
	return s.UploadPackSession.UploadPack(ctx, req)
}

// diskRepo is a bare repository on disk whose commit graph a test builds
// commit by commit. Every commit carries the empty tree and a distinct
// message, so no two commits share a hash.
type diskRepo struct {
	t    *testing.T
	dir  string
	repo *git.Repository
	tree plumbing.Hash
	seq  int
}

func newDiskRepo(t *testing.T) *diskRepo {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, true)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Tree{}).Encode(obj); err != nil {
		t.Fatalf("encode tree: %v", err)
	}
	tree, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("store tree: %v", err)
	}
	return &diskRepo{t: t, dir: dir, repo: repo, tree: tree}
}

// commit stores a commit with the given parents and returns its hash.
func (r *diskRepo) commit(parents ...plumbing.Hash) plumbing.Hash {
	r.t.Helper()
	sig := object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(1700000000, 0).UTC()}
	c := &object.Commit{Author: sig, Committer: sig, Message: strconv.Itoa(r.seq), TreeHash: r.tree, ParentHashes: parents}
	r.seq++
	obj := r.repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		r.t.Fatalf("encode commit: %v", err)
	}
	h, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		r.t.Fatalf("store commit: %v", err)
	}
	return h
}

// chain stores n commits, each the child of the one before, the first the
// child of parent (a root commit when parent is the zero hash), and returns
// them oldest first.
func (r *diskRepo) chain(parent plumbing.Hash, n int) []plumbing.Hash {
	r.t.Helper()
	out := make([]plumbing.Hash, n)
	for i := range out {
		if parent == plumbing.ZeroHash {
			parent = r.commit()
		} else {
			parent = r.commit(parent)
		}
		out[i] = parent
	}
	return out
}

// setRef points the reference name at h.
func (r *diskRepo) setRef(name string, h plumbing.Hash) {
	r.t.Helper()
	if err := r.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(name), h)); err != nil {
		r.t.Fatalf("set %s: %v", name, err)
	}
}

// remote registers behaviour as a new remote of the repository and returns
// the URL that reaches it through the test transport.
func (r *diskRepo) remote(t *testing.T, behaviour *fakeRemote) string {
	host := "remote" + strconv.FormatInt(fakeRemoteID.Add(1), 10)
	fakeRemotes.Store(host, behaviour)
	t.Cleanup(func() { fakeRemotes.Delete(host) })
	return fakeScheme + "://" + host + r.dir
}

// wantFetchDeadline fails the test unless err is the failure of a call of
// operation that reached the PODIUM_GIT_FETCH_TIMEOUT deadline in a round at
// depth, with the details of §7.3.1.
func wantFetchDeadline(t *testing.T, err error, operation string, depth int) {
	t.Helper()
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Fatalf("err = %v, want ErrSourceUnreachable", err)
	}
	env, ok := spi.AsError(err)
	if !ok || env.Code != "ingest.source_unreachable" {
		t.Fatalf("envelope = %+v, want ingest.source_unreachable", env)
	}
	want := map[string]any{"constraint": "fetch_deadline", "setting": "PODIUM_GIT_FETCH_TIMEOUT", "operation": operation, "depth": depth}
	if fmt.Sprint(env.Details) != fmt.Sprint(want) {
		t.Errorf("details = %v, want %v", env.Details, want)
	}
	if !strings.Contains(err.Error(), "did not finish within PODIUM_GIT_FETCH_TIMEOUT") {
		t.Errorf("error %q does not name the setting", err)
	}
}

// mainLayer is the configuration of a layer that tracks main at url.
func mainLayer(url string) LayerConfig { return LayerConfig{Repo: url, Ref: "main"} }

// wantRounds fails the test unless the remote saw one fetch per depth of want.
func wantRounds(t *testing.T, remote *fakeRemote, want ...int) {
	t.Helper()
	got := remote.rounds()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("fetch depths = %v, want %v", got, want)
	}
}

// wantAnswers runs a lineage query and fails the test unless it returns want.
func wantAnswers(t *testing.T, g Git, cfg LayerConfig, reference, base string, commits []string, want ...LineageAnswer) {
	t.Helper()
	got, err := g.Lineage(context.Background(), cfg, reference, base, commits)
	if err != nil {
		t.Fatalf("Lineage = %v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("answers = %v, want %v", got, want)
	}
	if err := CheckLineageAnswers(commits, got); err != nil {
		t.Errorf("CheckLineageAnswers on the Git source's response = %v", err)
	}
}

func hashes(hs ...plumbing.Hash) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.String()
	}
	return out
}

// Spec: §7.3.1 (Git fetch: Snapshot, Deepening) — a snapshot whose last
// ingested reference lies within the starting depth makes one fetch at that
// depth. One that lies beyond it deepens by a factor of four until the walk
// from the snapshot commit reaches it, and reports no rewrite. A first ingest
// makes one fetch.
func TestGitFetch_SnapshotDeepensToThePriorReference(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 100)
	r.setRef("refs/heads/main", main[99])
	for _, tc := range []struct {
		name   string
		prior  string
		rounds []int
	}{
		{name: "first ingest", prior: "", rounds: []int{16}},
		{name: "prior within the starting depth", prior: main[90].String(), rounds: []int{16}},
		{name: "prior beyond the starting depth", prior: main[50].String(), rounds: []int{16, 64}},
		{name: "prior at the root", prior: main[0].String(), rounds: []int{16, 64, 256}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			remote := &fakeRemote{}
			cfg := mainLayer(r.remote(t, remote))
			cfg.PriorRef = tc.prior
			snap, err := Git{}.Snapshot(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if snap.Reference != main[99].String() || snap.HistoryRewritten {
				t.Errorf("snapshot = %s rewritten %v, want %s with no rewrite", snap.Reference, snap.HistoryRewritten, main[99])
			}
			wantRounds(t, remote, tc.rounds...)
		})
	}
}

// Spec: §7.3.1 (Force-push detected, Git fetch: Snapshot) — under a bounded
// fetch, a rewritten history is decided only by the complete history of the
// tracked ref: the snapshot deepens to the round that holds the whole history
// and then reports the rewrite. A deleted and recreated signed ref is such a
// rewrite.
func TestGitFetch_SnapshotRewrittenHistory(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 30)
	// The old signed ref attests main[0..19], each run a commit whose parents
	// are the previous run and the attested main commit.
	signed := r.commit(main[0])
	for k := 1; k < 20; k++ {
		signed = r.commit(signed, main[k])
	}
	// The ref is deleted and recreated: its first run attests main[25].
	recreated := r.commit(main[25])
	r.setRef("refs/heads/main", recreated)
	remote := &fakeRemote{}
	cfg := mainLayer(r.remote(t, remote))
	cfg.PriorRef = signed.String()
	snap, err := Git{}.Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.HistoryRewritten {
		t.Errorf("snapshot of a recreated ref reports no rewrite")
	}
	wantRounds(t, remote, 16, 64)
}

// Spec: §7.3.1 (Git fetch) — the clone requests no tags, on the clone and on
// a deepening round, so a tag on a commit the tip does not reach brings no
// commit into the repository, and an answer about that commit is decided by
// the walk alone: a commit only a tag reaches is not in the history.
func TestGitFetch_TrackedRepositoryWithTags(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 40)
	orphan := r.chain(plumbing.ZeroHash, 3)
	r.setRef("refs/heads/main", main[39])
	r.setRef("refs/tags/v1", main[5])
	r.setRef("refs/tags/orphan", orphan[2])
	url := r.remote(t, &fakeRemote{})

	c := &gitCall{cfg: mainLayer(url)}
	if err := c.clone(context.Background(), DefaultFetchDepth); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := c.deepen(context.Background(), 20); err != nil {
		t.Fatalf("deepen: %v", err)
	}
	refs, err := c.repo.References()
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsTag() {
			t.Errorf("the clone holds tag %s", ref.Name())
		}
		return nil
	})
	commits, err := c.repo.CommitObjects()
	if err != nil {
		t.Fatalf("CommitObjects: %v", err)
	}
	held := 0
	_ = commits.ForEach(func(*object.Commit) error { held++; return nil })
	if held != 20 {
		t.Errorf("the clone holds %d commits after a fetch at depth 20, want 20", held)
	}

	wantAnswers(t, Git{}, mainLayer(url), main[39].String(), "", hashes(orphan[2], main[5]), NotInHistory, InLineage)

	// A layer that tracks a tag by its full name clones that ref, which the
	// no-tags mode leaves in place because the clone names it explicitly.
	snap, err := Git{}.Snapshot(context.Background(), LayerConfig{Repo: url, Ref: "refs/tags/v1", PriorRef: main[1].String()})
	if err != nil || snap.Reference != main[5].String() || snap.HistoryRewritten {
		t.Errorf("Snapshot of refs/tags/v1 = %+v, %v; want %s with no rewrite", snap, err, main[5])
	}
}

// Spec: §7.3.1 (Git fetch: Termination) — a remote that serves no deeper
// history than it holds leaves the walk truncated, so the round that adds no
// commit fails the call as ingest.source_unreachable: the second round brings
// in the 4 commits the remote holds beyond the first, and the third adds none. The snapshot does not
// report a rewrite, and the lineage query returns no answers.
func TestGitFetch_RemoteThatServesNoDeeperHistory(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 40)
	r.setRef("refs/heads/main", main[39])
	// The remote is itself a shallow clone that holds the last 20 commits.
	shallowDir := t.TempDir()
	if _, err := git.PlainClone(shallowDir, true, &git.CloneOptions{
		URL: "file://" + r.dir, ReferenceName: "refs/heads/main", SingleBranch: true, Depth: 20, Tags: git.NoTags,
	}); err != nil {
		t.Fatalf("shallow PlainClone: %v", err)
	}
	shallow := &diskRepo{t: t, dir: shallowDir}

	t.Run("snapshot", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		cfg := mainLayer(shallow.remote(t, remote))
		cfg.PriorRef = main[2].String()
		snap, err := Git{}.Snapshot(context.Background(), cfg)
		if !errors.Is(err, ErrSourceUnreachable) {
			t.Fatalf("Snapshot = %+v, %v; want ErrSourceUnreachable", snap, err)
		}
		wantRounds(t, remote, 16, 64, 256)
	})
	t.Run("lineage query", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		got, err := Git{}.Lineage(context.Background(), mainLayer(shallow.remote(t, remote)), main[39].String(), "", hashes(main[2]))
		if !errors.Is(err, ErrSourceUnreachable) || got != nil {
			t.Fatalf("Lineage = %v, %v; want no answers and ErrSourceUnreachable", got, err)
		}
		wantRounds(t, remote, 16, 64, 256)
	})
}

// oneMoreCommit serves one more commit in each round than in the round
// before, whatever depth the client requests.
func oneMoreCommit(round, _ int) int { return DefaultFetchDepth + round - 1 }

// Spec: §7.3.1 (Git fetch: Termination) — a remote that answers every fetch
// with one more commit passes the progress rule in each round, and the call
// still ends at the last depth of the schedule: 15 rounds from the default
// starting depth, the last of which requests the complete history. The call
// fails as ingest.source_unreachable, with no rewrite and no answers.
func TestGitFetch_RemoteThatServesOneMoreCommitPerRound(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 60)
	r.setRef("refs/heads/main", main[59])
	schedule := []int{16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304,
		16777216, 67108864, 268435456, 1073741824, 2147483647}

	t.Run("snapshot", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{serve: oneMoreCommit}
		cfg := mainLayer(r.remote(t, remote))
		cfg.PriorRef = main[0].String()
		if snap, err := (Git{}).Snapshot(context.Background(), cfg); !errors.Is(err, ErrSourceUnreachable) {
			t.Fatalf("Snapshot = %+v, %v; want ErrSourceUnreachable", snap, err)
		}
		wantRounds(t, remote, schedule...)
	})
	t.Run("lineage query", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{serve: oneMoreCommit}
		got, err := Git{}.Lineage(context.Background(), mainLayer(r.remote(t, remote)), main[59].String(), "", hashes(main[0]))
		if !errors.Is(err, ErrSourceUnreachable) || got != nil {
			t.Fatalf("Lineage = %v, %v; want no answers and ErrSourceUnreachable", got, err)
		}
		wantRounds(t, remote, schedule...)
	})
	t.Run("answer within reach", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{serve: oneMoreCommit}
		// main[40] is 19 commits behind the tip: the round that serves 19
		// commits names it as the parent of the walk's last commit.
		wantAnswers(t, Git{}, mainLayer(r.remote(t, remote)), main[59].String(), "", hashes(main[40]), InLineage)
		wantRounds(t, remote, 16, 64, 256, 1024)
	})
}

// Spec: §9.1 (Lineage query), §7.3.1 (Git fetch: Lineage query) — with no
// base, a commit in the history of the reference is in_lineage and one
// outside it is not_in_history, decided only once the history is complete. A
// commit that is not a full hexadecimal object name is not_in_history with no
// fetch, and a request of such names alone makes no fetch.
func TestGitLineage_NoBase(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 50)
	feature := r.chain(main[5], 2)
	r.setRef("refs/heads/main", main[49])
	r.setRef("refs/heads/feature", feature[1])

	t.Run("first ingest whose attested commit lies beyond the starting depth", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		wantAnswers(t, Git{}, mainLayer(r.remote(t, remote)), main[49].String(), "",
			hashes(main[49], main[40], main[2]), InLineage, InLineage, InLineage)
		wantRounds(t, remote, 16, 64)
	})
	t.Run("commit outside the history of the tracked ref", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		wantAnswers(t, Git{}, mainLayer(r.remote(t, remote)), main[49].String(), "",
			hashes(feature[1], main[45]), NotInHistory, InLineage)
		wantRounds(t, remote, 16, 64)
	})
	t.Run("names that are not full hexadecimal object names", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		url := r.remote(t, remote)
		wantAnswers(t, Git{}, mainLayer(url), main[49].String(), "",
			[]string{main[49].String()[:12], "main", ""}, NotInHistory, NotInHistory, NotInHistory)
		wantRounds(t, remote)
		wantAnswers(t, Git{}, mainLayer(url), main[49].String(), "",
			[]string{"HEAD", main[48].String()}, NotInHistory, InLineage)
		wantRounds(t, remote, 16)
	})
}

// signedHistory is a fixture of the recommended signing workflow: a linear
// main branch and a signed ref whose each run is a commit whose parents are
// the previous run and the main commit it attests.
type signedHistory struct {
	r    *diskRepo
	main []plumbing.Hash
}

// run adds a signing run on top of prev (none for a first run) that attests
// main[k].
func (s *signedHistory) run(prev plumbing.Hash, k int) plumbing.Hash {
	if prev == plumbing.ZeroHash {
		return s.r.commit(s.main[k])
	}
	return s.r.commit(prev, s.main[k])
}

func newSignedHistory(t *testing.T, mainLen int) *signedHistory {
	r := newDiskRepo(t)
	return &signedHistory{r: r, main: r.chain(plumbing.ZeroHash, mainLen)}
}

// Spec: §9.1 (Lineage query), §7.3.1 (Git fetch: Lineage query, Direction of
// the descent walk) — with a base, the descent question is answered by the
// walk from the attested commit, and in the fallback by the walk from the
// base. A re-run of an old signing run attests an ancestor of the base, and
// that attested commit is an ancestor of the reference through the run's
// second parent, so a walk from the reference that stopped at the base would
// place it in the lineage. The source answers not_descendant, from the walk
// that starts at the base while the attested commit's own history is still
// truncated, in one round.
func TestGitLineage_RerunOfAnOldSigningRun(t *testing.T) {
	t.Parallel()
	s := newSignedHistory(t, 200)
	var signed plumbing.Hash
	for k := 180; k <= 195; k++ {
		signed = s.run(signed, k)
	}
	rerunNear := s.run(signed, 190)
	rerunFar := s.run(signed, 100)
	s.r.setRef("refs/heads/near", rerunNear)
	s.r.setRef("refs/heads/far", rerunFar)
	base := s.main[195].String()

	t.Run("history stays truncated", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		cfg := LayerConfig{Repo: s.r.remote(t, remote), Ref: "near"}
		wantAnswers(t, Git{}, cfg, rerunNear.String(), base, hashes(s.main[190]), NotDescendant)
		wantRounds(t, remote, 16)
	})
	t.Run("attested commit far below the base", func(t *testing.T) {
		t.Parallel()
		remote := &fakeRemote{}
		cfg := LayerConfig{Repo: s.r.remote(t, remote), Ref: "far"}
		wantAnswers(t, Git{}, cfg, rerunFar.String(), base, hashes(s.main[100], s.main[195]), NotDescendant, InLineage)
		wantRounds(t, remote, 16, 64, 256)
	})
}

// Spec: §9.1 (Lineage query), §7.3.1 (Git fetch: Deepening) — two signed
// commits whose attested commits lie further apart on main than the starting
// depth: the walk from the newer attested commit is truncated before the base
// at depth 16, and one deepening round decides in_lineage. The recorded
// commit and the last ingested reference beyond the starting depth deepen the
// same way. An attested commit outside the history of the tracked ref is
// not_in_history with a base as well as without one.
func TestGitLineage_WithBase(t *testing.T) {
	t.Parallel()
	s := newSignedHistory(t, 100)
	older := s.run(plumbing.ZeroHash, 10)
	newer := s.run(older, 60)
	side := s.r.chain(s.main[30], 2)
	s.r.setRef("refs/heads/main", newer)
	s.r.setRef("refs/heads/side", side[1])

	remote := &fakeRemote{}
	cfg := mainLayer(s.r.remote(t, remote))
	wantAnswers(t, Git{}, cfg, newer.String(), s.main[10].String(),
		hashes(s.main[60], s.main[10], side[1]), InLineage, InLineage, NotInHistory)
	wantRounds(t, remote, 16, 64)

	t.Run("base that is not a full hexadecimal name", func(t *testing.T) {
		wantAnswers(t, Git{}, cfg, newer.String(), "not-a-commit", hashes(s.main[60]), NotDescendant)
	})
}

// Spec: §7.3.1 (Git fetch: Starting commit) — the starting commit of a lineage
// query is its reference, whatever the tracked ref points to when the query
// runs. After the ref moves on, the walk from the fetched tip must reach the
// reference first, deepening while it is truncated. A reference that the ref
// no longer contains fails the query as ingest.source_unreachable once the
// walk from the tip is complete.
func TestGitLineage_RefMovesAfterTheSnapshot(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 30)
	r.setRef("refs/heads/main", main[29])
	remote := &fakeRemote{}
	cfg := mainLayer(r.remote(t, remote))
	snap, err := Git{}.Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// The ref moves on by more commits than the starting depth.
	later := r.chain(main[29], 40)
	r.setRef("refs/heads/main", later[39])
	wantAnswers(t, Git{}, cfg, snap.Reference, "", hashes(main[25], later[39]), InLineage, NotInHistory)
	// The snapshot's single fetch, then the query's rounds.
	wantRounds(t, remote, 16, 16, 64, 256)

	// The ref is rewritten onto a history that does not hold the reference.
	rewritten := r.chain(plumbing.ZeroHash, 5)
	r.setRef("refs/heads/main", rewritten[4])
	got, err := Git{}.Lineage(context.Background(), cfg, snap.Reference, "", hashes(main[25]))
	if !errors.Is(err, ErrSourceUnreachable) || got != nil {
		t.Fatalf("Lineage after the ref lost the reference = %v, %v; want no answers and ErrSourceUnreachable", got, err)
	}
}

// Spec: §9.1 (Lineage query) — a reference that is not a full hexadecimal
// commit name is in no history, so the query fails as ingest.source_unreachable
// with no fetch, and a request that breaks a member limit is refused with
// registry.invalid_argument and no answers.
func TestGitLineage_InvalidRequests(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 2)
	r.setRef("refs/heads/main", main[1])
	remote := &fakeRemote{}
	cfg := mainLayer(r.remote(t, remote))
	if got, err := (Git{}).Lineage(context.Background(), cfg, "main", "", hashes(main[0])); !errors.Is(err, ErrSourceUnreachable) || got != nil {
		t.Errorf("Lineage with a symbolic reference = %v, %v; want ErrSourceUnreachable", got, err)
	}
	wantRounds(t, remote)
	if _, err := (Git{}).Lineage(context.Background(), cfg, main[1].String(), "", make([]string, 65)); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("Lineage with 65 commits = %v, want ErrInvalidConfig", err)
	}
	if _, err := (Git{}).Lineage(context.Background(), LayerConfig{Ref: "main"}, main[1].String(), "", hashes(main[0])); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("Lineage with no repo = %v, want ErrInvalidConfig", err)
	}
}

// Spec: §7.3.1 (Git fetch: Deadline), §13.12 — a call that reaches the
// PODIUM_GIT_FETCH_TIMEOUT deadline in a deepening round fails as
// ingest.source_unreachable with details that name the constraint, the
// setting, the operation, and the depth of the round that was running.
func TestGitFetch_DeadlineInADeepeningRound(t *testing.T) {
	t.Parallel()
	r := newDiskRepo(t)
	main := r.chain(plumbing.ZeroHash, 40)
	r.setRef("refs/heads/main", main[39])
	g := Git{FetchTimeout: 500 * time.Millisecond}

	t.Run("snapshot", func(t *testing.T) {
		t.Parallel()
		cfg := mainLayer(r.remote(t, &fakeRemote{stallRound: 2}))
		cfg.PriorRef = main[0].String()
		_, err := g.Snapshot(context.Background(), cfg)
		wantFetchDeadline(t, err, OperationSnapshot, 64)
	})
	t.Run("lineage query", func(t *testing.T) {
		t.Parallel()
		_, err := g.Lineage(context.Background(), mainLayer(r.remote(t, &fakeRemote{stallRound: 2})), main[39].String(), "", hashes(main[0]))
		wantFetchDeadline(t, err, OperationLineageQuery, 64)
	})
	t.Run("lineage query in its clone", func(t *testing.T) {
		t.Parallel()
		_, err := Git{FetchTimeout: 300 * time.Millisecond, FetchDepth: 4}.Lineage(context.Background(),
			mainLayer(r.remote(t, &fakeRemote{stallRound: 1})), main[39].String(), "", hashes(main[0]))
		wantFetchDeadline(t, err, OperationLineageQuery, 4)
	})
}

// Spec: §9.1 (Lineage query) — lineage queries and snapshots against one
// remote run concurrently without shared state: each call clones its own
// repository, and concurrent queries return the same answers. Run under -race.
func TestGitLineage_ConcurrentCalls(t *testing.T) {
	t.Parallel()
	s := newSignedHistory(t, 60)
	older := s.run(plumbing.ZeroHash, 10)
	newer := s.run(older, 50)
	s.r.setRef("refs/heads/main", newer)
	url := s.r.remote(t, &fakeRemote{})
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := Git{}.Lineage(context.Background(), mainLayer(url), newer.String(), s.main[10].String(), hashes(s.main[50], s.main[5]))
			if err != nil || fmt.Sprint(got) != fmt.Sprint([]LineageAnswer{InLineage, NotDescendant}) {
				errs <- fmt.Errorf("Lineage = %v, %v", got, err)
			}
		}()
		go func() {
			defer wg.Done()
			cfg := mainLayer(url)
			cfg.PriorRef = s.main[5].String()
			if snap, err := (Git{}).Snapshot(context.Background(), cfg); err != nil || snap.HistoryRewritten {
				errs <- fmt.Errorf("Snapshot = %+v, %v", snap, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
