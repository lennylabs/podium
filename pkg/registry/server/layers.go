package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// DefaultMaxUserLayers is the §7.3.1 / §1.4 default cap on
// user-defined layers per identity: "Default cap: 3 user-defined
// layers per identity, configurable per tenant." A per-tenant
// store.Quota.MaxUserLayers (or WithMaxUserLayers) overrides it; a
// negative override disables the cap.
const DefaultMaxUserLayers = 3

// LayerEndpoint serves the §7.3.1 layer-management HTTP surface:
//
//	POST   /v1/layers          register a layer
//	GET    /v1/layers          list registered layers
//	DELETE /v1/layers?id=ID    unregister a layer
//	POST   /v1/layers/reorder  re-sequence the layer list
//
// Phase 10 plumbs admin authorization for admin-defined layers via
// the same AdminAuthorize check the core uses. User-defined layers
// (UserDefined=true) are registered by any authenticated caller and
// implicitly visible only to the registrant.
type LayerEndpoint struct {
	store    store.Store
	tenantID string
	mode     *ModeTracker
	// authAdmin returns nil when the caller is permitted to mutate
	// admin-defined layers. Tests inject a no-op; production wires
	// the registry's AdminAuthorize.
	authAdmin func(*http.Request) error
	// identify resolves the caller identity. It backs the §4.6 rule
	// that a user-defined layer's owner is the authenticated
	// registrant. Defaults to an anonymous caller.
	identify func(*http.Request) layer.Identity
	// defaultLayerVisibility is the fallback applied at register
	// time when an admin-defined layer arrives with no explicit
	// visibility. One of "public" | "organization" | "private".
	defaultLayerVisibility string
	// maxUserLayers overrides the §7.3.1 per-identity user-defined-layer
	// cap. Zero leaves resolution to the tenant quota then
	// DefaultMaxUserLayers; a negative value disables the cap. See
	// effectiveLayerCap.
	maxUserLayers int
	// auditSink records §8.1 layer.config_changed (admin-defined) and
	// layer.user_registered (personal) events on register, unregister, and
	// reorder. Nil is a no-op.
	auditSink *audit.FileSink
}

// WithDefaultVisibility installs the §4.6 fallback visibility for
// admin-defined layers that arrive at register time without
// explicit visibility settings.
func (e *LayerEndpoint) WithDefaultVisibility(v string) *LayerEndpoint {
	e.defaultLayerVisibility = v
	return e
}

// NewLayerEndpoint returns an endpoint backed by the given store +
// tenant + read-only mode tracker.
func NewLayerEndpoint(s store.Store, tenantID string, mode *ModeTracker) *LayerEndpoint {
	return &LayerEndpoint{
		store: s, tenantID: tenantID, mode: mode,
		authAdmin: func(*http.Request) error { return nil },
		identify:  func(*http.Request) layer.Identity { return layer.Identity{IsPublic: true} },
	}
}

// WithAdminAuth installs the admin-authorization callback.
func (e *LayerEndpoint) WithAdminAuth(fn func(*http.Request) error) *LayerEndpoint {
	e.authAdmin = fn
	return e
}

// WithIdentityResolver installs the caller-identity resolver used to
// derive a user-defined layer's owner from the authenticated registrant.
func (e *LayerEndpoint) WithIdentityResolver(fn func(*http.Request) layer.Identity) *LayerEndpoint {
	e.identify = fn
	return e
}

// WithMaxUserLayers overrides the §7.3.1 per-identity cap on
// user-defined layers. A positive value caps at that count; zero
// leaves the tenant-quota/DefaultMaxUserLayers resolution in place; a
// negative value disables the cap.
func (e *LayerEndpoint) WithMaxUserLayers(n int) *LayerEndpoint {
	e.maxUserLayers = n
	return e
}

// WithAudit installs the §8.3 audit sink the endpoint records layer
// lifecycle events to (§8.1 layer.config_changed / layer.user_registered).
func (e *LayerEndpoint) WithAudit(sink *audit.FileSink) *LayerEndpoint {
	e.auditSink = sink
	return e
}

// emitLayerEvent records the §8.1 audit event for a register or unregister
// action: layer.user_registered for a personal (user-defined) layer with
// its owner as caller, or layer.config_changed for an admin-defined layer.
func (e *LayerEndpoint) emitLayerEvent(r *http.Request, cfg store.LayerConfig, action string) {
	typ := audit.EventLayerConfigChanged
	if cfg.UserDefined {
		typ = audit.EventLayerUserRegistered
	}
	fields := map[string]string{"action": action, "user_defined": boolString(cfg.UserDefined)}
	// §8.1: record the personal-layer owner so a layer.user_registered event
	// names the user whose layer changed even when an admin acts on it.
	if cfg.Owner != "" {
		fields["owner"] = cfg.Owner
	}
	emitAuditEvent(e.auditSink, r, e.identify(r), typ, cfg.ID, fields)
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// effectiveLayerCap resolves the §7.3.1 user-defined-layer cap for the
// endpoint's tenant. Precedence: an explicit WithMaxUserLayers override,
// then the per-tenant store.Quota.MaxUserLayers, then
// DefaultMaxUserLayers. A non-zero value at any level wins (a negative
// value disables the cap). The resolved value is never zero, so the
// caller treats `cap > 0` as "enforce" and `cap < 0` as "unlimited".
func (e *LayerEndpoint) effectiveLayerCap(ctx context.Context) int {
	if e.maxUserLayers != 0 {
		return e.maxUserLayers
	}
	if t, err := e.store.GetTenant(ctx, e.tenantID); err == nil && t.Quota.MaxUserLayers != 0 {
		return t.Quota.MaxUserLayers
	}
	return DefaultMaxUserLayers
}

// LayerRegisterRequest is the POST /v1/layers JSON body.
type LayerRegisterRequest struct {
	ID           string   `json:"id"`
	SourceType   string   `json:"source_type"`
	Repo         string   `json:"repo,omitempty"`
	Ref          string   `json:"ref,omitempty"`
	Root         string   `json:"root,omitempty"`
	LocalPath    string   `json:"local_path,omitempty"`
	UserDefined  bool     `json:"user_defined,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	Public       bool     `json:"public,omitempty"`
	Organization bool     `json:"organization,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	Users        []string `json:"users,omitempty"`
}

// LayerRegisterResponse is the POST /v1/layers JSON response.
type LayerRegisterResponse struct {
	Layer         store.LayerConfig `json:"layer"`
	WebhookURL    string            `json:"webhook_url,omitempty"`
	WebhookSecret string            `json:"webhook_secret,omitempty"`
}

// Handler returns the http.Handler that dispatches to the layer
// subroutes.
func (e *LayerEndpoint) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/layers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			e.register(w, r)
		case http.MethodGet:
			e.list(w, r)
		case http.MethodDelete:
			e.unregister(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
				"method not allowed: "+r.Method)
		}
	})
	mux.HandleFunc("/v1/layers/reorder", e.reorder)
	mux.HandleFunc("/v1/layers/reingest", e.reingest)
	mux.HandleFunc("/v1/layers/update", e.update)
	return mux
}

// update handles PUT /v1/layers/update?id=ID. Body fields that are
// non-zero replace the corresponding LayerConfig field; zero
// fields keep the prior value. This shape avoids having to send
// the whole config and accidentally clear visibility filters.
//
// Allowed mutations: visibility (Public, Organization, Groups,
// Users), Ref, Root, LocalPath, Owner. The store-bound identifying
// fields (TenantID, ID, SourceType, CreatedAt) and webhook secrets
// are immutable.
func (e *LayerEndpoint) update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if e.mode != nil {
		if err := e.mode.CheckConfig(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "config.read_only", err.Error())
			return
		}
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id query param is required")
		return
	}
	cfg, err := e.store.GetLayerConfig(r.Context(), e.tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "registry.not_found", err.Error())
		return
	}
	// Mutating an admin-defined layer requires admin authorization; a
	// user-defined layer belongs to its registrant (§4.7.2).
	if !cfg.UserDefined {
		if err := e.authAdmin(r); err != nil {
			writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
			return
		}
	}
	var patch LayerRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if patch.Ref != "" {
		cfg.Ref = patch.Ref
	}
	if patch.Root != "" {
		cfg.Root = patch.Root
	}
	if patch.LocalPath != "" {
		cfg.LocalPath = patch.LocalPath
	}
	// spec: §4.6 — a user-defined layer's owner and implicit
	// users:[owner] visibility are fixed at registration and cannot be
	// widened, so visibility/owner patches are ignored for it. An admin
	// may edit an admin-defined layer's visibility.
	if !cfg.UserDefined {
		if patch.Owner != "" {
			cfg.Owner = patch.Owner
		}
		if patch.Public {
			cfg.Public = true
		}
		if patch.Organization {
			cfg.Organization = true
		}
		if len(patch.Groups) > 0 {
			cfg.Groups = patch.Groups
		}
		if len(patch.Users) > 0 {
			cfg.Users = patch.Users
		}
	}
	if err := e.store.PutLayerConfig(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, LayerRegisterResponse{Layer: cfg})
}

// register handles POST /v1/layers.
func (e *LayerEndpoint) register(w http.ResponseWriter, r *http.Request) {
	if e.mode != nil {
		if err := e.mode.CheckConfig(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "config.read_only", err.Error())
			return
		}
	}
	var req LayerRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if req.ID == "" || req.SourceType == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"id and source_type are required")
		return
	}
	// Admin-defined layers require admin authorization.
	if !req.UserDefined {
		if err := e.authAdmin(r); err != nil {
			writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
			return
		}
	}

	cfg := store.LayerConfig{
		TenantID:     e.tenantID,
		ID:           req.ID,
		SourceType:   req.SourceType,
		Repo:         req.Repo,
		Ref:          req.Ref,
		Root:         req.Root,
		LocalPath:    req.LocalPath,
		UserDefined:  req.UserDefined,
		Owner:        req.Owner,
		Public:       req.Public,
		Organization: req.Organization,
		Groups:       req.Groups,
		Users:        req.Users,
		CreatedAt:    time.Now().UTC(),
	}

	// spec: §4.6 / §7.3.1 — a user-defined layer has implicit visibility
	// users:[<registrant>]; the field is set automatically and cannot be
	// widened. Derive the owner from the authenticated identity so a
	// caller cannot register a layer owned by an arbitrary subject, and
	// discard any caller-supplied public/organization/groups/users.
	if cfg.UserDefined {
		if id := e.identify(r); id.IsAuthenticated && id.Sub != "" {
			cfg.Owner = id.Sub
		}
		cfg.Public = false
		cfg.Organization = false
		cfg.Groups = nil
		if cfg.Owner != "" {
			cfg.Users = []string{cfg.Owner}
		} else {
			cfg.Users = nil
		}
	}

	// spec: §7.3.1 / §1.4 — cap of N user-defined layers per identity
	// (default 3, configurable per tenant). Count the registrant's
	// existing user-defined layers and reject when accepting this one
	// would exceed the cap. Re-registering an already-owned layer (same
	// id) is an update and does not count as an additional layer.
	if cfg.UserDefined {
		if capN := e.effectiveLayerCap(r.Context()); capN > 0 {
			existing, err := e.store.ListLayerConfigs(r.Context(), e.tenantID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
				return
			}
			owned := 0
			for _, l := range existing {
				if l.UserDefined && l.Owner == cfg.Owner && l.ID != cfg.ID {
					owned++
				}
			}
			if owned+1 > capN {
				who := cfg.Owner
				if who == "" {
					who = "the registrant"
				}
				// spec: SS 6.10 — carry the machine-readable cap and the
				// caller's current count in `details` so a client can render
				// the limit without parsing the message (F-6.10.1).
				writeErrorDetails(w, http.StatusTooManyRequests, "quota.layer_count_exceeded",
					fmt.Sprintf("user-defined layer cap of %d reached for %s", capN, who),
					map[string]any{"limit": capN, "current": owned})
				return
			}
		}
	}

	// §4.6 / PODIUM_DEFAULT_LAYER_VISIBILITY: when no explicit
	// visibility is supplied by an admin-defined layer, fall back
	// to the deployment-configured default. "private" is the
	// safe default — admins must opt in to broader visibility.
	if !cfg.UserDefined && !cfg.Public && !cfg.Organization &&
		len(cfg.Groups) == 0 && len(cfg.Users) == 0 {
		switch e.defaultLayerVisibility {
		case "public":
			cfg.Public = true
		case "organization":
			cfg.Organization = true
		}
		// "private" / unset / unknown: leave the layer with no
		// visibility filters — only explicit grants will see it.
	}

	// Pick a default order: the highest existing ord + 10.
	if existing, err := e.store.ListLayerConfigs(r.Context(), e.tenantID); err == nil && len(existing) > 0 {
		cfg.Order = existing[len(existing)-1].Order + 10
	} else {
		cfg.Order = 10
	}

	// Generate the webhook HMAC secret for git sources (§7.3.1).
	if cfg.SourceType == "git" {
		secret, err := generateSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		cfg.WebhookSecret = secret
	}

	if err := e.store.PutLayerConfig(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	// spec §8.1: a user registering a personal layer emits
	// layer.user_registered; an admin adding an admin-defined layer emits
	// layer.config_changed.
	e.emitLayerEvent(r, cfg, "register")

	resp := LayerRegisterResponse{Layer: cfg}
	if cfg.SourceType == "git" {
		resp.WebhookURL = "/v1/layers/" + cfg.ID + "/webhook"
		resp.WebhookSecret = cfg.WebhookSecret
	}
	writeJSON(w, http.StatusCreated, resp)
}

// list handles GET /v1/layers.
func (e *LayerEndpoint) list(w http.ResponseWriter, r *http.Request) {
	layers, err := e.store.ListLayerConfigs(r.Context(), e.tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"layers": layers})
}

// unregister handles DELETE /v1/layers?id=ID.
func (e *LayerEndpoint) unregister(w http.ResponseWriter, r *http.Request) {
	if e.mode != nil {
		if err := e.mode.CheckConfig(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "config.read_only", err.Error())
			return
		}
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id query param required")
		return
	}
	cfg, err := e.store.GetLayerConfig(r.Context(), e.tenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "registry.not_found", "no such layer: "+id)
		} else {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		}
		return
	}
	if !cfg.UserDefined {
		if err := e.authAdmin(r); err != nil {
			writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
			return
		}
	}
	if err := e.store.DeleteLayerConfig(r.Context(), e.tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	// spec §8.1: unregistering a personal layer emits layer.user_registered;
	// removing an admin-defined layer emits layer.config_changed.
	e.emitLayerEvent(r, cfg, "unregister")
	writeJSON(w, http.StatusOK, map[string]any{"unregistered": id})
}

// reorder handles POST /v1/layers/reorder.
func (e *LayerEndpoint) reorder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if e.mode != nil {
		if err := e.mode.CheckConfig(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "config.read_only", err.Error())
			return
		}
	}
	var req struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	layers, err := e.store.ListLayerConfigs(r.Context(), e.tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	byID := map[string]store.LayerConfig{}
	for _, l := range layers {
		byID[l.ID] = l
	}
	// Verify the caller is authorized for each affected layer.
	for _, id := range req.Order {
		l, ok := byID[id]
		if !ok {
			writeError(w, http.StatusNotFound, "registry.not_found", "no such layer: "+id)
			return
		}
		if !l.UserDefined {
			if err := e.authAdmin(r); err != nil {
				writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
				return
			}
		}
	}
	for i, id := range req.Order {
		l := byID[id]
		l.Order = (i + 1) * 10
		if err := e.store.PutLayerConfig(r.Context(), l); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
	}
	updated, _ := e.store.ListLayerConfigs(r.Context(), e.tenantID)
	sort.Slice(updated, func(i, j int) bool { return updated[i].Order < updated[j].Order })
	// spec §8.1: reordering admin-defined layers emits layer.config_changed.
	if len(req.Order) > 0 {
		emitAuditEvent(e.auditSink, r, e.identify(r), audit.EventLayerConfigChanged,
			strings.Join(req.Order, ","), map[string]string{"action": "reorder"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"layers": updated})
}

// reingest handles POST /v1/layers/reingest?id=ID. The actual ingest
// pipeline runs externally; this endpoint records the intent so an
// orchestrator can pick it up. Phase 10 wires the trigger; the
// pipeline integration lands when the standalone server's ingest
// scheduler ships.
func (e *LayerEndpoint) reingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id query param required")
		return
	}
	cfg, err := e.store.GetLayerConfig(r.Context(), e.tenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "registry.not_found", "no such layer: "+id)
		} else {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"queued":    cfg.ID,
		"queued_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// generateSecret returns a 32-byte hex-encoded HMAC secret for git
// webhook delivery (§7.3.1).
func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generateSecret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ErrAdminRequired is the sentinel test code reuses when injecting
// "this caller is not admin" into authAdmin. Real callers wire
// pkg/registry/core.AdminAuthorize, which surfaces ErrForbidden.
var ErrAdminRequired = errors.New("admin: caller lacks admin role")
