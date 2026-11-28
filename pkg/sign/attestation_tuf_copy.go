package sign

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// verifyCachedTarget verifies that the TUF metadata cached in metaDir lists
// target as the trusted_root.json target, and returns the expiry of the
// cached timestamp. The cached root.json is the embedded TUF root, or the
// next version, signed by a threshold of the embedded root's root keys and
// of its own; the timestamp, snapshot, and targets metadata verify under
// that root, each names the version of the next, and the targets entry
// lists the length and hashes of target. The expiry of the metadata is not
// checked, because the copy is kept past the expiry of the timestamp role;
// the caller caps the copy's root time by the timestamp's expiry instead.
//
// Spec: §4.7.11 (Trusted root refresh)
func verifyCachedTarget(metaDir string, tufRoot, target []byte) (time.Time, error) {
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(metaDir, name)) }
	trusted, err := cachedTUFRoot(tufRoot, read)
	if err != nil {
		return time.Time{}, err
	}
	tsRaw, err := read("timestamp.json")
	if err != nil {
		return time.Time{}, err
	}
	ts, err := metadata.Timestamp().FromBytes(tsRaw)
	if err == nil {
		err = trusted.VerifyDelegate(metadata.TIMESTAMP, ts)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("cached timestamp: %w", err)
	}
	snapRaw, err := read("snapshot.json")
	if err != nil {
		return time.Time{}, err
	}
	snap, err := metadata.Snapshot().FromBytes(snapRaw)
	if err == nil {
		err = trusted.VerifyDelegate(metadata.SNAPSHOT, snap)
	}
	if err == nil {
		err = metaMatches(ts.Signed.Meta["snapshot.json"], snap.Signed.Version, snapRaw)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("cached snapshot: %w", err)
	}
	tgRaw, err := read("targets.json")
	if err != nil {
		return time.Time{}, err
	}
	tg, err := metadata.Targets().FromBytes(tgRaw)
	if err == nil {
		err = trusted.VerifyDelegate(metadata.TARGETS, tg)
	}
	if err == nil {
		err = metaMatches(snap.Signed.Meta["targets.json"], tg.Signed.Version, tgRaw)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("cached targets: %w", err)
	}
	tf, ok := tg.Signed.Targets[trustedRootTarget]
	if !ok {
		return time.Time{}, errors.New("the cached targets list no trusted_root.json")
	}
	if err := tf.VerifyLengthHashes(target); err != nil {
		return time.Time{}, fmt.Errorf("the copy is not the trusted_root.json the cached targets list: %w", err)
	}
	return ts.Signed.Expires, nil
}

// cachedTUFRoot returns the TUF root the cached metadata verifies under:
// the embedded root, or a cached next version signed by the embedded root.
// A cached root that rotated further than one version, or that is older,
// does not verify, so the copy counts as no copy until a refresh.
func cachedTUFRoot(tufRoot []byte, read func(string) ([]byte, error)) (*metadata.Metadata[metadata.RootType], error) {
	embedded, err := metadata.Root().FromBytes(tufRoot)
	if err != nil {
		return nil, fmt.Errorf("embedded TUF root: %w", err)
	}
	raw, err := read("root.json")
	if err != nil {
		return nil, err
	}
	if bytes.Equal(raw, tufRoot) {
		return embedded, nil
	}
	cached, err := metadata.Root().FromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("cached TUF root: %w", err)
	}
	switch cached.Signed.Version {
	case embedded.Signed.Version:
		return embedded, nil
	case embedded.Signed.Version + 1:
	default:
		return nil, fmt.Errorf("the cached TUF root is version %d, which the embedded version %d does not reach in one step", cached.Signed.Version, embedded.Signed.Version)
	}
	if err := embedded.VerifyDelegate(metadata.ROOT, cached); err != nil {
		return nil, fmt.Errorf("cached TUF root: %w", err)
	}
	if err := cached.VerifyDelegate(metadata.ROOT, cached); err != nil {
		return nil, fmt.Errorf("cached TUF root: %w", err)
	}
	return cached, nil
}

// metaMatches checks a meta entry against the version and the bytes of the
// metadata it names.
func metaMatches(m *metadata.MetaFiles, version int64, raw []byte) error {
	if m == nil {
		return errors.New("the referring metadata names no version")
	}
	if m.Version != version {
		return fmt.Errorf("version %d, the referring metadata names %d", version, m.Version)
	}
	if m.Length != 0 || len(m.Hashes) > 0 {
		return m.VerifyLengthHashes(raw)
	}
	return nil
}
