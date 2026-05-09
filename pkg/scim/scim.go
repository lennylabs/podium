// Package scim implements the §6.3.1 SCIM 2.0 receiver. The
// registry exposes /scim/v2/Users and /scim/v2/Groups so an OIDC
// IdP (Okta, Entra ID, Workspace, Auth0, Keycloak) can push user
// and group memberships into the registry. The visibility
// evaluator queries scim_memberships when a layer config references
// a group that exists in the SCIM table.
//
// SCIM scope shipped here:
//
//   - Users: id, externalId, userName, emails, active.
//   - Groups: id, displayName, members.
//   - Filter expressions: eq, sw, co on userName / displayName.
//   - Bearer token auth via PODIUM_SCIM_TOKEN.
//
// Operations not shipped (return SCIM-conformant errors):
//
//   - PATCH operations beyond simple add/remove members and toggle
//     active. Most IdPs use full PUT to be safe.
//   - Bulk endpoint (/Bulk).
//   - Schemas / ServiceProviderConfig discovery beyond the
//     minimum the IdP needs to validate the endpoint.
package scim

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Errors returned by Store implementations.
var (
	// ErrNotFound is returned when a SCIM resource is missing.
	ErrNotFound = errors.New("scim: not found")
	// ErrConflict is returned when uniqueness is violated (e.g.,
	// two Users with the same userName).
	ErrConflict = errors.New("scim: conflict")
	// ErrInvalidFilter signals an unsupported SCIM filter; maps
	// to SCIM error code "invalidFilter".
	ErrInvalidFilter = errors.New("scim: invalid filter")
)

// User is the §6.3.1 user resource. The fields mirror the SCIM 2.0
// core schema but only carry what the visibility evaluator needs.
type User struct {
	ID         string
	ExternalID string
	UserName   string
	Email      string
	Active     bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Group is the §6.3.1 group resource.
type Group struct {
	ID          string
	DisplayName string
	MemberIDs   []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store is the SPI implementations satisfy.
type Store interface {
	CreateUser(ctx context.Context, u User) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	ListUsers(ctx context.Context, filter Filter) ([]User, error)
	ReplaceUser(ctx context.Context, id string, u User) (User, error)
	DeleteUser(ctx context.Context, id string) error

	CreateGroup(ctx context.Context, g Group) (Group, error)
	GetGroup(ctx context.Context, id string) (Group, error)
	ListGroups(ctx context.Context, filter Filter) ([]Group, error)
	ReplaceGroup(ctx context.Context, id string, g Group) (Group, error)
	DeleteGroup(ctx context.Context, id string) error

	// MembersOf returns the SCIM userNames of every user in the
	// named group. Used by the visibility evaluator to expand
	// `groups:` filters in layer config.
	MembersOf(ctx context.Context, groupName string) ([]string, error)
}

// Filter is the parsed §6.3.1 SCIM filter expression. Empty
// matches everything.
type Filter struct {
	Attribute string // userName | displayName | active | externalId
	Operator  string // eq | sw | co | pr
	Value     string
}

// Match reports whether s passes the filter.
func (f Filter) Match(attr, value string) bool {
	if f.Attribute == "" {
		return true
	}
	if f.Attribute != attr {
		return true // not our attribute
	}
	switch f.Operator {
	case "eq":
		return value == f.Value
	case "sw":
		return len(value) >= len(f.Value) && value[:len(f.Value)] == f.Value
	case "co":
		return contains(value, f.Value)
	case "pr":
		return value != ""
	}
	return false
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Memory is an in-memory Store. Used by tests and standalone
// deployments that don't need SCIM persistence across restarts.
type Memory struct {
	mu     sync.Mutex
	users  map[string]User
	groups map[string]Group
}

// NewMemory returns an empty in-memory SCIM store.
func NewMemory() *Memory {
	return &Memory{users: map[string]User{}, groups: map[string]Group{}}
}

// CreateUser stores u and returns the persisted form (with ID set).
func (m *Memory) CreateUser(_ context.Context, u User) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.UserName == "" {
		return User{}, errors.New("scim: userName is required")
	}
	for _, existing := range m.users {
		if existing.UserName == u.UserName {
			return User{}, ErrConflict
		}
	}
	if u.ID == "" {
		u.ID = "u-" + nextID()
	}
	now := time.Now().UTC()
	u.CreatedAt = now
	u.UpdatedAt = now
	m.users[u.ID] = u
	return u, nil
}

// GetUser returns the user or ErrNotFound.
func (m *Memory) GetUser(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

// ListUsers returns every user matching filter.
func (m *Memory) ListUsers(_ context.Context, filter Filter) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		if !filter.Match("userName", u.UserName) {
			continue
		}
		if !filter.Match("externalId", u.ExternalID) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// ReplaceUser overwrites the stored user with u (id from the path
// wins).
func (m *Memory) ReplaceUser(_ context.Context, id string, u User) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	u.ID = id
	u.CreatedAt = existing.CreatedAt
	u.UpdatedAt = time.Now().UTC()
	m.users[id] = u
	return u, nil
}

// DeleteUser removes the user; missing id is a no-op.
func (m *Memory) DeleteUser(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, id)
	for gid, g := range m.groups {
		filtered := g.MemberIDs[:0:0]
		for _, mid := range g.MemberIDs {
			if mid != id {
				filtered = append(filtered, mid)
			}
		}
		g.MemberIDs = filtered
		m.groups[gid] = g
	}
	return nil
}

// CreateGroup persists g and returns its committed form.
func (m *Memory) CreateGroup(_ context.Context, g Group) (Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g.DisplayName == "" {
		return Group{}, errors.New("scim: displayName is required")
	}
	for _, existing := range m.groups {
		if existing.DisplayName == g.DisplayName {
			return Group{}, ErrConflict
		}
	}
	if g.ID == "" {
		g.ID = "g-" + nextID()
	}
	now := time.Now().UTC()
	g.CreatedAt = now
	g.UpdatedAt = now
	m.groups[g.ID] = g
	return g, nil
}

// GetGroup returns the group or ErrNotFound.
func (m *Memory) GetGroup(_ context.Context, id string) (Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return Group{}, ErrNotFound
	}
	return g, nil
}

// ListGroups returns every group matching filter.
func (m *Memory) ListGroups(_ context.Context, filter Filter) ([]Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Group, 0, len(m.groups))
	for _, g := range m.groups {
		if !filter.Match("displayName", g.DisplayName) {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

// ReplaceGroup overwrites the stored group.
func (m *Memory) ReplaceGroup(_ context.Context, id string, g Group) (Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.groups[id]
	if !ok {
		return Group{}, ErrNotFound
	}
	g.ID = id
	g.CreatedAt = existing.CreatedAt
	g.UpdatedAt = time.Now().UTC()
	m.groups[id] = g
	return g, nil
}

// DeleteGroup removes the group; missing id is a no-op.
func (m *Memory) DeleteGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.groups, id)
	return nil
}

// MembersOf returns userName values for every user in the named
// group. Used by the §4.6 visibility evaluator to expand
// `groups:` filters.
func (m *Memory) MembersOf(_ context.Context, groupName string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.groups {
		if g.DisplayName != groupName {
			continue
		}
		out := make([]string, 0, len(g.MemberIDs))
		for _, mid := range g.MemberIDs {
			if u, ok := m.users[mid]; ok {
				out = append(out, u.UserName)
			}
		}
		return out, nil
	}
	return nil, nil
}

// nextID returns a short opaque identifier.
func nextID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}
