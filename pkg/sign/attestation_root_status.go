package sign

import (
	"context"
	"fmt"
	"time"
)

// AttestationRootStatusSource is a trusted root source that reports the
// root it uses: its digest and its root time. The §4.7.11 root record
// compares both across the processes that share a store.
type AttestationRootStatusSource interface {
	Status() AttestationRootStatus
}

// FileAttestationRoot is a trusted root read from a file at ReadAt, which is
// its root time.
//
// Spec: §4.7.11 (Trusted root files, Root record)
type FileAttestationRoot struct {
	Root   *AttestationRoot
	ReadAt time.Time
}

// AttestationRoot returns the root read from the file.
func (f FileAttestationRoot) AttestationRoot(context.Context) (*AttestationRoot, error) {
	return f.Root, nil
}

// Status reports the root and the time the file was read.
func (f FileAttestationRoot) Status() AttestationRootStatus {
	return StatusOf(f.Root, f.ReadAt)
}

// RootStatus reports the digest and the root time of the root that inst
// uses. A source that reports no status, such as a root a caller built,
// gives its digest with an unknown root time. usable is false when the root
// cannot verify a bundle now, such as a public-good root past its maximum
// age; such a process is behind every root record (§4.7.11 Root record).
//
// Spec: §4.7.11 (Root record)
func RootStatus(ctx context.Context, inst AttestationInstance) (st AttestationRootStatus, usable bool, err error) {
	if inst.Root == nil {
		return AttestationRootStatus{}, false, fmt.Errorf("attestation: instance %q has no trusted root", inst.Name)
	}
	if s, ok := inst.Root.(AttestationRootStatusSource); ok {
		st = s.Status()
	}
	r, rerr := inst.Root.AttestationRoot(ctx)
	if rerr == nil && r != nil && r.material != nil {
		if st.Digest == "" {
			st.Digest = r.Digest()
		}
		return st, true, nil
	}
	if st.Digest == "" {
		return AttestationRootStatus{}, false, fmt.Errorf("attestation: trusted root of instance %q: %w", inst.Name, rerr)
	}
	return st, false, nil
}
