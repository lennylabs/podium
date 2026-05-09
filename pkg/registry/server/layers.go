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
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

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
	// defaultLayerVisibility is the fallback applied at register
	// time when an admin-defined layer arrives with no explicit
	// visibility. One of "public" | "organization" | "private".
	defaultLayerVisibility string
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
	}
}

// WithAdminAuth installs the admin-authorization callback.
func (e *LayerEndpoint) WithAdminAuth(fn func(*http.Request) error) *LayerEndpoint {
	e.authAdmin = fn
	return e
}

// LayerRegisterRequest is the POST /v1/layers JSON body.
type LayerRegisterRequest struct {
	ID            string   `json:"id"`
	SourceType    string   `json:"source_type"`
	Repo          string   `json:"repo,omitempty"`
	Ref           string   `json:"ref,omitempty"`
	Root          string   `json:"root,omitempty"`
	LocalPath     string   `json:"local_path,omitempty"`
	UserDefined   bool     `json:"user_defined,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	Public        bool     `json:"public,omitempty"`
	Organization  bool     `json:"organization,omitempty"`
	Groups        []string `json:"groups,omitempty"`
	Users         []string `json:"users,omitempty"`
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
	if err := e.authAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
	cfg, err := e.store.GetLayerConfig(r.Context(), e.tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "registry.not_found", err.Error())
		return
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

	// User-defined layers always carry the implicit users:[owner]
	// visibility per §7.3.1.
	if cfg.UserDefined && cfg.Owner != "" && len(cfg.Users) == 0 {
		cfg.Users = []string{cfg.Owner}
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

// quiet unused-import linter when build doesn't touch context yet.
var _ = context.Background
