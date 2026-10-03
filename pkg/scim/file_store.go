package scim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileStore persists the SCIM directory to a JSON file. The
// in-memory `Memory` store handles read paths; mutation paths
// delegate to it and then persist the file atomically.
//
// Suitable for §6.3.1 deployments with hundreds of users / groups
// and infrequent IdP pushes. Heavier deployments should use a
// SQL-backed store.
type FileStore struct {
	mu     sync.Mutex
	path   string
	memory *Memory
}

// LoadFileStore returns a SCIM store pre-populated with every record
// in path. It reads the file when present, creates the parent
// directory at load, and creates the file on the first mutation. A
// missing or empty file loads as an empty store. An unreadable file,
// a malformed file, and a parent directory that cannot be created or
// written are errors, prefixed `scim: read`, `scim: parse`,
// `scim: prepare`, and `scim: probe` respectively, so the caller can
// refuse startup rather than lose every record pushed afterwards.
//
// Spec: §6.3.1, §13.12
func LoadFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("scim: path required")
	}
	out := &FileStore{path: path, memory: NewMemory()}
	if err := out.load(); err != nil {
		return nil, err
	}
	if err := probeWritable(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return out, nil
}

// probeWritable confirms that save() can write the store: it creates the
// parent directory and then a temporary file in it. save() replaces the
// store by renaming <path>.tmp over it, so the directory's writability is
// what matters and the existing file's mode is not checked. The probe does
// not call save(), which would rewrite the operator's file on every start.
// A remove failure leaves a stray probe file and does not make the store
// unusable, so it is logged.
//
// Spec: §6.3.1, §13.12
func probeWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("scim: prepare %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".scim-probe-*")
	if err != nil {
		return fmt.Errorf("scim: probe %s: %w", dir, err)
	}
	name := f.Name()
	// Close and Remove cannot be made to fail in a unit test once CreateTemp
	// has succeeded on a local filesystem; the branches stay for a network
	// filesystem that reports a deferred write error at close.
	if err := f.Close(); err != nil {
		return fmt.Errorf("scim: probe %s: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		log.Printf("scim: remove probe file %s: %v", name, err)
	}
	return nil
}

type fileStoreSnapshot struct {
	Users  []User  `json:"users"`
	Groups []Group `json:"groups"`
}

func (f *FileStore) load() error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("scim: read %s: %w", f.path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var snap fileStoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("scim: parse %s: %w", f.path, err)
	}
	for _, u := range snap.Users {
		f.memory.users[u.ID] = u
	}
	for _, g := range snap.Groups {
		f.memory.groups[g.ID] = g
	}
	return nil
}

func (f *FileStore) save() error {
	users := make([]User, 0, len(f.memory.users))
	for _, u := range f.memory.users {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	groups := make([]Group, 0, len(f.memory.groups))
	for _, g := range f.memory.groups {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	body, err := json.MarshalIndent(fileStoreSnapshot{Users: users, Groups: groups}, "", "  ")
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

// CreateUser delegates to the embedded Memory store and persists.
func (f *FileStore) CreateUser(ctx context.Context, u User) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, err := f.memory.CreateUser(ctx, u)
	if err != nil {
		return User{}, err
	}
	if err := f.save(); err != nil {
		return User{}, err
	}
	return out, nil
}

// GetUser delegates to the embedded Memory store.
func (f *FileStore) GetUser(ctx context.Context, id string) (User, error) {
	return f.memory.GetUser(ctx, id)
}

// ListUsers delegates to the embedded Memory store.
func (f *FileStore) ListUsers(ctx context.Context, filter Filter) ([]User, error) {
	return f.memory.ListUsers(ctx, filter)
}

// ReplaceUser delegates and persists.
func (f *FileStore) ReplaceUser(ctx context.Context, id string, u User) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, err := f.memory.ReplaceUser(ctx, id, u)
	if err != nil {
		return User{}, err
	}
	if err := f.save(); err != nil {
		return User{}, err
	}
	return out, nil
}

// DeleteUser delegates and persists.
func (f *FileStore) DeleteUser(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.memory.DeleteUser(ctx, id); err != nil {
		return err
	}
	return f.save()
}

// CreateGroup delegates and persists.
func (f *FileStore) CreateGroup(ctx context.Context, g Group) (Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, err := f.memory.CreateGroup(ctx, g)
	if err != nil {
		return Group{}, err
	}
	if err := f.save(); err != nil {
		return Group{}, err
	}
	return out, nil
}

// GetGroup delegates to the embedded Memory store.
func (f *FileStore) GetGroup(ctx context.Context, id string) (Group, error) {
	return f.memory.GetGroup(ctx, id)
}

// ListGroups delegates to the embedded Memory store.
func (f *FileStore) ListGroups(ctx context.Context, filter Filter) ([]Group, error) {
	return f.memory.ListGroups(ctx, filter)
}

// ReplaceGroup delegates and persists.
func (f *FileStore) ReplaceGroup(ctx context.Context, id string, g Group) (Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, err := f.memory.ReplaceGroup(ctx, id, g)
	if err != nil {
		return Group{}, err
	}
	if err := f.save(); err != nil {
		return Group{}, err
	}
	return out, nil
}

// DeleteGroup delegates and persists.
func (f *FileStore) DeleteGroup(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.memory.DeleteGroup(ctx, id); err != nil {
		return err
	}
	return f.save()
}

// MembersOf delegates to the embedded Memory store.
func (f *FileStore) MembersOf(ctx context.Context, groupName string) ([]string, error) {
	return f.memory.MembersOf(ctx, groupName)
}
