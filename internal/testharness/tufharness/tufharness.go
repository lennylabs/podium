// Package tufharness serves a TUF repository from httptest for the tests of
// the public-good trusted root refresh (§4.7.11). The repository has the
// layout of the Sigstore TUF repository: consistent snapshots, a
// hash-prefixed trusted_root.json target, and one ed25519 key for every
// top-level role. A test starts its client from RootJSON and switches the
// server between answering, hanging, and failing.
package tufharness

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// TargetName is the target the Sigstore repository publishes.
const TargetName = "trusted_root.json"

// Mode is how the server answers.
type Mode int32

// The server modes.
const (
	// Serve answers every request from the repository.
	Serve Mode = iota
	// Hang accepts each request and answers none until the request ends.
	Hang
	// Fail answers every request with 503.
	Fail
)

// Repo is a TUF repository behind an httptest server.
type Repo struct {
	tb     testing.TB
	srv    *httptest.Server
	signer signature.Signer
	key    *metadata.Key
	mode   atomic.Int32
	hits   atomic.Int64
	stop   chan struct{}

	// mu guards files and the role versions that Publish advances.
	mu        sync.Mutex
	files     map[string][]byte
	rootJSON  []byte
	targetsV  int64
	snapshotV int64
	stampV    int64
}

// New publishes trustedRoot as the repository's trusted_root.json target
// and starts the server.
func New(tb testing.TB, trustedRoot []byte) *Repo {
	tb.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	must(tb, err)
	signer, err := signature.LoadED25519Signer(priv)
	must(tb, err)
	key, err := metadata.KeyFromPublicKey(pub)
	must(tb, err)
	r := &Repo{tb: tb, signer: signer, key: key, files: map[string][]byte{}, stop: make(chan struct{})}

	root := metadata.Root(time.Now().AddDate(1, 0, 0))
	for _, role := range []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS} {
		must(tb, root.Signed.AddKey(key, role))
	}
	_, err = root.Sign(signer)
	must(tb, err)
	r.rootJSON, err = root.ToBytes(false)
	must(tb, err)
	r.files["/1.root.json"] = r.rootJSON

	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	// Cleanups run last-in first-out: hanging handlers return before Close
	// waits for them.
	tb.Cleanup(r.srv.Close)
	tb.Cleanup(func() { close(r.stop) })
	r.Publish(trustedRoot)
	return r
}

// URL is the repository base URL, the mirror a client is pointed at.
func (r *Repo) URL() string { return r.srv.URL }

// RootJSON is the version 1 root.json a client starts from.
func (r *Repo) RootJSON() []byte { return r.rootJSON }

// SetMode switches how the server answers.
func (r *Repo) SetMode(m Mode) { r.mode.Store(int32(m)) }

// Requests counts the requests the server received.
func (r *Repo) Requests() int64 { return r.hits.Load() }

// Publish replaces the trusted_root.json target with trustedRoot under new
// targets, snapshot, and timestamp versions that expire in a year.
func (r *Repo) Publish(trustedRoot []byte) {
	r.publish(trustedRoot, time.Now().AddDate(1, 0, 0))
}

// PublishExpired publishes trustedRoot under a timestamp that has already
// expired, which a client refuses.
func (r *Repo) PublishExpired(trustedRoot []byte) {
	r.publish(trustedRoot, time.Now().Add(-time.Hour))
}

func (r *Repo) publish(trustedRoot []byte, stampExpires time.Time) {
	r.tb.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	expires := time.Now().AddDate(1, 0, 0)

	r.targetsV++
	targets := metadata.Targets(expires)
	targets.Signed.Version = r.targetsV
	tf, err := metadata.TargetFile().FromBytes(TargetName, trustedRoot, "sha256")
	must(r.tb, err)
	targets.Signed.Targets[TargetName] = tf
	targetsJSON := r.signed(targets)
	r.files[fmt.Sprintf("/%d.targets.json", r.targetsV)] = targetsJSON
	r.files["/targets/"+hex.EncodeToString(tf.Hashes["sha256"])+"."+TargetName] = trustedRoot

	r.snapshotV++
	snapshot := metadata.Snapshot(expires)
	snapshot.Signed.Version = r.snapshotV
	snapshot.Signed.Meta["targets.json"] = metadata.MetaFile(r.targetsV)
	r.files[fmt.Sprintf("/%d.snapshot.json", r.snapshotV)] = r.signed(snapshot)

	r.stampV++
	stamp := metadata.Timestamp(stampExpires)
	stamp.Signed.Version = r.stampV
	stamp.Signed.Meta["snapshot.json"] = metadata.MetaFile(r.snapshotV)
	r.files["/timestamp.json"] = r.signed(stamp)
}

// role is the metadata of any top-level role.
type role interface {
	Sign(signature.Signer) (*metadata.Signature, error)
	ToBytes(bool) ([]byte, error)
}

func (r *Repo) signed(m role) []byte {
	r.tb.Helper()
	_, err := m.Sign(r.signer)
	must(r.tb, err)
	out, err := m.ToBytes(false)
	must(r.tb, err)
	return out
}

func (r *Repo) serve(w http.ResponseWriter, req *http.Request) {
	r.hits.Add(1)
	switch Mode(r.mode.Load()) {
	case Hang:
		select {
		case <-req.Context().Done():
		case <-r.stop:
		}
		return
	case Fail:
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	r.mu.Lock()
	body, ok := r.files[req.URL.Path]
	r.mu.Unlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write(body)
}

func must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("tufharness: %v", err)
	}
}
