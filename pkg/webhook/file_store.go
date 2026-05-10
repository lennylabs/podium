package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileStore persists Receiver records to a JSON file. Suitable
// for §7.3.2 deployments where the receiver count is small (~10s)
// and write throughput is low; heavier deployments should use a
// SQL-backed Store.
//
// The file format is a flat array of Receiver structs, sorted by
// (TenantID, ID) for deterministic diffs.
type FileStore struct {
	mu        sync.Mutex
	path      string
	receivers map[string]Receiver // key: tenantID + "\x00" + id
}

// LoadFileStore reads path and returns a Store pre-populated with
// every record. Missing path yields an empty store that creates
// the file on first Put.
func LoadFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("webhook: path required")
	}
	out := &FileStore{path: path, receivers: map[string]Receiver{}}
	if err := out.load(); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *FileStore) load() error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("webhook: read %s: %w", f.path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var records []Receiver
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("webhook: parse %s: %w", f.path, err)
	}
	for _, r := range records {
		f.receivers[receiverKey(r.TenantID, r.ID)] = r
	}
	return nil
}

func (f *FileStore) save() error {
	all := make([]Receiver, 0, len(f.receivers))
	for _, r := range f.receivers {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].TenantID != all[j].TenantID {
			return all[i].TenantID < all[j].TenantID
		}
		return all[i].ID < all[j].ID
	})
	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// List returns every receiver registered for tenantID, sorted by
// ID for deterministic test output.
func (f *FileStore) List(_ context.Context, tenantID string) ([]Receiver, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Receiver{}
	for _, r := range f.receivers {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get returns the receiver for (tenantID, id) or an error
// matching the spec error namespace.
func (f *FileStore) Get(_ context.Context, tenantID, id string) (Receiver, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.receivers[receiverKey(tenantID, id)]
	if !ok {
		return Receiver{}, fmt.Errorf("webhook: receiver %q not found", id)
	}
	return r, nil
}

// Put writes a receiver and persists the file atomically.
func (f *FileStore) Put(_ context.Context, r Receiver) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receivers[receiverKey(r.TenantID, r.ID)] = r
	return f.save()
}

// Delete removes a receiver and persists the file atomically.
func (f *FileStore) Delete(_ context.Context, tenantID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.receivers, receiverKey(tenantID, id))
	return f.save()
}

func receiverKey(tenantID, id string) string {
	return tenantID + "\x00" + id
}
