package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
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
	// reorder. It is the §8.3 registry sink (a file sink or an EndpointSink
	// when redirected to a SIEM, F-8.3.1). Nil is a no-op.
	auditSink audit.Sink
	// auditFile is the file-backed form of the §8.3 registry sink, set only
	// when the sink writes a local log. The §8.5 erasure pass rewrites the
	// on-disk hash chain in place (audit.EraseUser), so it needs the concrete
	// file sink; it is nil when the registry is redirected to an external
	// endpoint, where the receiving aggregator owns erasure of the shipped
	// stream. spec: §8.3, §8.5.
	auditFile *audit.FileSink
	// reingestRunner runs the §7.3.1 ingest pipeline for a resolved layer
	// (the manual reingest and inbound-webhook triggers). serverboot wires
	// it with the source-provider resolver, linter, event publisher, audit
	// emitter, and freeze windows. Nil leaves the endpoint in record-intent
	// mode (the queue-only response) for harnesses that do not wire ingest.
	reingestRunner ReingestRunner
}

// BreakGlass carries a §4.7.2 break-glass override supplied on the manual
// reingest path (`podium layer reingest <id> --break-glass --justification
// <text>`). The ingest pipeline requires a non-empty justification and two
// distinct approvers before a freeze window is bypassed.
type BreakGlass struct {
	Justification string
	Approvers     []string
}

// ReingestRunner runs the §7.3.1 ingest pipeline for one layer and returns
// the result summary. bg is non-nil when the caller passed a break-glass
// override on the manual reingest path. A nil runner leaves the endpoint in
// record-intent mode.
type ReingestRunner func(ctx context.Context, cfg store.LayerConfig, bg *BreakGlass) (*ingest.Result, error)

// WithReingestRunner installs the ingest-pipeline driver the manual reingest
// and inbound-webhook handlers invoke. Without it, those handlers only record
// the intent (the queue-only response).
func (e *LayerEndpoint) WithReingestRunner(fn ReingestRunner) *LayerEndpoint {
	e.reingestRunner = fn
	return e
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
// The sink may be a file sink or an EndpointSink (SIEM redirect, F-8.3.1).
func (e *LayerEndpoint) WithAudit(sink audit.Sink) *LayerEndpoint {
	e.auditSink = sink
	return e
}

// WithEraseSink installs the file-backed sink the §8.5 erasure flow
// rewrites in place. Pass the same file sink given to WithAudit when the
// registry writes a local log; pass nil when redirected to an external
// endpoint, in which case the erase endpoint purges layers but performs no
// local-log redaction (the aggregator owns the shipped stream). F-8.3.1.
func (e *LayerEndpoint) WithEraseSink(file *audit.FileSink) *LayerEndpoint {
	e.auditFile = file
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
	// ForcePushPolicy sets the §7.3.1 per-layer force-push handling for a
	// git source. One of "" (tolerant), "tolerant", or "strict". On update
	// the empty string leaves the prior value; "tolerant" explicitly
	// resets it.
	ForcePushPolicy string `json:"force_push_policy,omitempty"`
	// RotateWebhookSecret requests a fresh HMAC webhook secret on the
	// update path (§12 "Per-layer HMAC secret rotated via `podium layer
	// update`"). When true on a git layer the handler regenerates
	// WebhookSecret and returns the new value once. Ignored on register.
	RotateWebhookSecret bool `json:"rotate_webhook_secret,omitempty"`
}

// validForcePushPolicy reports whether p is one of the §7.3.1 accepted
// force-push policy values ("" and "tolerant" both mean tolerant; "strict"
// rejects a rewritten history).
func validForcePushPolicy(p string) bool {
	switch p {
	case "", "tolerant", "strict":
		return true
	default:
		return false
	}
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
	mux.HandleFunc("/v1/layers/restore", e.restore)
	return mux
}

// EraseHandler returns the handler for the §8.5 GDPR right-to-erasure
// operation, mounted separately at /v1/admin/erase. POST only.
func (e *LayerEndpoint) EraseHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/admin/erase", e.erase)
	return mux
}

// eraseRequest is the POST /v1/admin/erase JSON body.
type eraseRequest struct {
	UserID string `json:"user_id"`
	Salt   string `json:"salt"`
}

// erase performs the §8.5 GDPR right-to-erasure for user_id. It (1)
// unregisters and soft-deletes every user-defined layer the user owns and
// the artifacts ingested from them, (2) redacts the user identity across the
// registry audit stream, and (3) appends a registry-sourced user.erased
// event naming the invoking admin (§8.1). Admin-only; rejected in read-only
// mode because it mutates catalogue state.
//
// The layer-purge events are emitted before the redaction pass so the erased
// owner is itself redacted out of those layer.user_registered records.
func (e *LayerEndpoint) erase(w http.ResponseWriter, r *http.Request) {
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
	if err := e.authAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
	var body eraseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if body.UserID == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "user_id is required")
		return
	}
	// spec §8.5 (F-8.5.5): the tombstone is redacted-<sha256(user_id+salt)>;
	// an empty salt makes it guessable, so reject it.
	if body.Salt == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "salt is required")
		return
	}
	// spec §8.5 (F-8.5.1): unregister and soft-delete every user-defined
	// layer the user owns. DeleteLayerConfig tombstones the layer and the
	// artifacts ingested from it (recoverable within the §8.4 30-day window).
	layers, err := e.store.ListLayerConfigs(r.Context(), e.tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	purged := []string{}
	for _, l := range layers {
		if !l.UserDefined || l.Owner != body.UserID {
			continue
		}
		if err := e.store.DeleteLayerConfig(r.Context(), e.tenantID, l.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		e.emitLayerEvent(r, l, "erase")
		purged = append(purged, l.ID)
	}
	// spec §8.5 (F-8.5.3): redact the user identity across the registry audit
	// stream and append the registry-sourced user.erased event naming the
	// invoking admin (F-8.5.4). The registry's §8.3 file sink is the same log
	// the retention and anchor schedulers operate on, so the redaction lands
	// on the authoritative stream. When the registry is redirected to an
	// external endpoint (no local file, F-8.3.1) there is no on-disk chain to
	// rewrite: the layers are still purged and the aggregator owns redaction
	// of the shipped stream.
	admin := callerIdentityString(e.identify(r))
	redacted := 0
	if e.auditFile != nil {
		redacted, err = audit.EraseUser(r.Context(), e.auditFile, body.UserID, body.Salt, admin)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"erased":                body.UserID,
		"layers_purged":         purged,
		"audit_events_redacted": redacted,
	})
}

// WebhookHandler returns the handler for the §7.3.1 inbound Git-provider
// webhook trigger, mounted separately at /v1/ingest/webhook/{id}. The layer
// id comes from the path so the URL `podium layer register` advertises is a
// clean per-layer endpoint. POST only; other methods get 405 from the mux.
func (e *LayerEndpoint) WebhookHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ingest/webhook/{id}", e.handleWebhook)
	return mux
}

// update handles PUT /v1/layers/update?id=ID. Body fields that are
// non-zero replace the corresponding LayerConfig field; zero
// fields keep the prior value. This shape avoids having to send
// the whole config and accidentally clear visibility filters.
//
// Allowed mutations: visibility (Public, Organization, Groups,
// Users), Ref, Root, LocalPath, Owner, ForcePushPolicy, and a
// webhook-secret rotation (rotate_webhook_secret). The store-bound
// identifying fields (TenantID, ID, SourceType, CreatedAt) are
// immutable.
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
	if !validForcePushPolicy(patch.ForcePushPolicy) {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"force_push_policy must be \"tolerant\" or \"strict\"")
		return
	}
	// spec: §7.3.1 — a non-empty force_push_policy replaces the prior
	// value; the empty string leaves it unchanged (consistent with the
	// other patch fields).
	if patch.ForcePushPolicy != "" {
		cfg.ForcePushPolicy = patch.ForcePushPolicy
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
	// spec: §12 — "Per-layer HMAC secret rotated via `podium layer
	// update`." A compromised secret is replaced here; only a git layer
	// carries one, so a rotation request on a non-git layer is rejected.
	rotated := false
	if patch.RotateWebhookSecret {
		if cfg.SourceType != "git" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument",
				"rotate_webhook_secret applies only to a git layer")
			return
		}
		secret, err := generateSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		cfg.WebhookSecret = secret
		rotated = true
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
	// spec §8.1: a config mutation (including a secret rotation) is an
	// auditable layer.config_changed / layer.user_registered event.
	e.emitLayerEvent(r, cfg, "update")
	resp := LayerRegisterResponse{Layer: cfg}
	// Return the freshly rotated secret once so the operator can register
	// it on the source repo; it is never echoed on a plain update.
	if rotated {
		resp.WebhookURL = "/v1/ingest/webhook/" + cfg.ID
		resp.WebhookSecret = cfg.WebhookSecret
	}
	writeJSON(w, http.StatusOK, resp)
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
	if !validForcePushPolicy(req.ForcePushPolicy) {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"force_push_policy must be \"tolerant\" or \"strict\"")
		return
	}
	// spec: §7.3.1 / §4.6 — resolve the registration class. An admin-defined
	// layer is declared by a tenant admin and requires admin authorization.
	// An authenticated non-admin caller registers a personal (user-defined)
	// layer rather than being rejected: §7.3.1 states "Authenticated users
	// register their own layers via podium layer register", and the §14.9
	// invocation carries no --user-defined flag, so the class is resolved
	// server-side from the caller's identity. An anonymous caller attempting
	// an admin-defined registration is still rejected with auth.forbidden.
	caller := e.identify(r)
	userDefined := req.UserDefined
	if !userDefined {
		if err := e.authAdmin(r); err != nil {
			if caller.IsAuthenticated && caller.Sub != "" {
				userDefined = true
			} else {
				writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
				return
			}
		}
	}

	cfg := store.LayerConfig{
		TenantID:        e.tenantID,
		ID:              req.ID,
		SourceType:      req.SourceType,
		Repo:            req.Repo,
		Ref:             req.Ref,
		Root:            req.Root,
		LocalPath:       req.LocalPath,
		UserDefined:     userDefined,
		ForcePushPolicy: req.ForcePushPolicy,
		CreatedAt:       time.Now().UTC(),
	}

	if userDefined {
		// spec: §4.6 / §7.3.1 — a user-defined layer has implicit visibility
		// users:[<registrant>]; the field is set automatically and cannot be
		// widened. Derive the owner from the authenticated identity so a
		// caller cannot register a layer owned by an arbitrary subject; with
		// no identity (a no-identity standalone/filesystem deployment, where
		// visibility is bypassed per §4.6) fall back to the request body.
		// Discard any caller-supplied public/organization/groups.
		if caller.IsAuthenticated && caller.Sub != "" {
			cfg.Owner = caller.Sub
		} else {
			cfg.Owner = req.Owner
		}
		// spec: §4.6 / §14.9 — a user-defined layer with no resolvable owner
		// has no visibility entries and is unreachable (visible to no one).
		// Reject the registration rather than persist an orphaned, invisible
		// row that not even the registrant can see.
		if cfg.Owner == "" {
			writeError(w, http.StatusForbidden, "auth.forbidden",
				"user-defined layer registration requires an authenticated identity")
			return
		}
		cfg.Users = []string{cfg.Owner}
	} else {
		cfg.Owner = req.Owner
		cfg.Public = req.Public
		cfg.Organization = req.Organization
		cfg.Groups = req.Groups
		cfg.Users = req.Users
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
		case "users":
			// spec §13.10: PODIUM_DEFAULT_LAYER_VISIBILITY=users selects the
			// standard "users: [<registrant>]" behavior. Derive the registrant
			// from the authenticated caller; with no authenticated identity
			// (anonymous standalone) the layer stays private (no filters),
			// which is the safe fallback for the `users` selection.
			if id := e.identify(r); id.IsAuthenticated && id.Sub != "" {
				cfg.Users = []string{id.Sub}
			}
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
		resp.WebhookURL = "/v1/ingest/webhook/" + cfg.ID
		resp.WebhookSecret = cfg.WebhookSecret
	}
	writeJSON(w, http.StatusCreated, resp)
}

// list handles GET /v1/layers. With ?deleted=true it returns the
// soft-deleted layers still inside the §8.4 30-day recovery window so an
// admin can see what RestoreLayerConfig can recover.
func (e *LayerEndpoint) list(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("deleted") == "true" {
		layers, err := e.store.ListDeletedLayerConfigs(r.Context(), e.tenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"layers": layers})
		return
	}
	layers, err := e.store.ListLayerConfigs(r.Context(), e.tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"layers": layers})
}

// restore handles POST /v1/layers/restore?id=ID. It clears the §8.4
// soft-delete tombstone on a layer unregistered by its owner (and the
// artifacts ingested from it), recovering them within the 30-day window.
// An admin-defined layer requires admin authorization; a user-defined
// layer's owner can recover their own layer.
func (e *LayerEndpoint) restore(w http.ResponseWriter, r *http.Request) {
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
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id query param required")
		return
	}
	// Locate the soft-deleted layer so authorization and the audit record
	// see its UserDefined / Owner attributes (a restored layer is hidden
	// from GetLayerConfig until the tombstone is cleared).
	deleted, err := e.store.ListDeletedLayerConfigs(r.Context(), e.tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	var cfg store.LayerConfig
	found := false
	for _, l := range deleted {
		if l.ID == id {
			cfg, found = l, true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "registry.not_found", "no recoverable layer: "+id)
		return
	}
	if !cfg.UserDefined {
		if err := e.authAdmin(r); err != nil {
			writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
			return
		}
	}
	if err := e.store.RestoreLayerConfig(r.Context(), e.tenantID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "registry.not_found", "no recoverable layer: "+id)
			return
		}
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	e.emitLayerEvent(r, cfg, "restore")
	writeJSON(w, http.StatusOK, map[string]any{"restored": id})
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

// reingestRequest is the optional POST /v1/layers/reingest body. The §7.3.1
// manual reingest path carries a break-glass override here so an operator can
// bypass an active freeze window (§4.7.2).
type reingestRequest struct {
	BreakGlass    bool     `json:"break_glass,omitempty"`
	Justification string   `json:"justification,omitempty"`
	Approvers     []string `json:"approvers,omitempty"`
}

// reingest handles POST /v1/layers/reingest?id=ID, the §7.3.1 manual reingest
// trigger ("forces a fresh snapshot regardless of the trigger model"). When a
// reingest runner is wired it resolves the layer's source provider and runs
// the ingest pipeline, returning the result summary. An optional break-glass
// body bypasses an active freeze window (§4.7.2). Without a runner the handler
// records the intent (the queue-only response).
func (e *LayerEndpoint) reingest(w http.ResponseWriter, r *http.Request) {
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
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id query param required")
		return
	}
	// The break-glass override is optional; an empty or absent body means a
	// plain reingest. Decode leniently so a nil body (CLI watch, tests) is
	// not an error.
	var body reingestRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
			return
		}
	}
	var bg *BreakGlass
	if body.BreakGlass {
		// spec: §4.7.2 — break-glass requires a justification. Reject the
		// request before touching the pipeline when it is missing.
		if body.Justification == "" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument",
				"break-glass requires a justification")
			return
		}
		bg = &BreakGlass{Justification: body.Justification, Approvers: body.Approvers}
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
	e.runIngestAndRespond(w, r, cfg, bg)
}

// runIngestAndRespond drives the ingest pipeline for a resolved layer (shared
// by the manual reingest and inbound-webhook triggers) and writes the §7.3.1
// result summary. With no runner wired it records the intent only.
func (e *LayerEndpoint) runIngestAndRespond(w http.ResponseWriter, r *http.Request, cfg store.LayerConfig, bg *BreakGlass) {
	if e.reingestRunner == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"queued":    cfg.ID,
			"queued_at": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}
	res, err := e.reingestRunner(r.Context(), cfg, bg)
	if err != nil {
		writeReingestError(w, err)
		return
	}
	// §0 quickstart: report the (artifact_id, version) pairs the snapshot
	// produced so the CLI can print the per-artifact confirmation line.
	arts := make([]map[string]string, 0, len(res.Ingested))
	for _, a := range res.Ingested {
		arts = append(arts, map[string]string{"id": a.ArtifactID, "version": a.Version})
	}
	// spec: §4.6 / §3.3 — surface the non-blocking ingest advisories (e.g. the
	// cross-layer license change, F-4.6.3) so a publisher reingesting at
	// runtime sees the same flags the boot path logs.
	advisories := make([]map[string]string, 0, len(res.Advisories))
	for _, a := range res.Advisories {
		advisories = append(advisories, map[string]string{
			"artifact_id": a.ArtifactID,
			"code":        a.Code,
			"severity":    string(a.Severity),
			"message":     a.Message,
		})
	}
	// spec: §7.3.1 — "Same version, different content_hash | Rejected as
	// ingest.immutable_violation", and ingest.immutable_violation is one of the
	// §7.3.1 error codes. Report each same-version content conflict as a
	// per-artifact object carrying the named code and the conflicting
	// (artifact_id, version) plus the old/new hashes so the author can tell
	// which artifact collided and bump its version (F-7.3.2), rather than the
	// opaque count this previously returned.
	conflicts := make([]map[string]any, 0, len(res.Conflicts))
	for _, c := range res.Conflicts {
		conflicts = append(conflicts, map[string]any{
			"artifact_id": c.ArtifactID,
			"version":     c.Version,
			"old_hash":    c.OldHash,
			"new_hash":    c.NewHash,
			"code":        "ingest.immutable_violation",
		})
	}
	// A snapshot whose only outcome is conflicts (nothing accepted, nothing
	// idempotent) is a pure rejection: surface it as the §6.10
	// ingest.immutable_violation error envelope (HTTP 409, matching
	// ingest.frozen / ingest.history_rewritten) so a manual reingest or webhook
	// delivery sees the rejection the spec promises. This mirrors the
	// pipeline's lint hard-error rule (ingest.Ingest returns ErrLintFailed only
	// when nothing else succeeded), so a mixed snapshot still returns 200 with
	// the accepted artifacts and the conflicts reported per-artifact.
	if len(res.Conflicts) > 0 && res.Accepted == 0 && res.Idempotent == 0 {
		first := res.Conflicts[0]
		msg := fmt.Sprintf("same-version content conflict: %s@%s already exists with different content; bump the version",
			first.ArtifactID, first.Version)
		if len(res.Conflicts) > 1 {
			msg = fmt.Sprintf("%s (and %d more)", msg, len(res.Conflicts)-1)
		}
		writeErrorDetails(w, http.StatusConflict, "ingest.immutable_violation", msg,
			map[string]any{"conflicts": conflicts})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"queued":        cfg.ID,
		"queued_at":     time.Now().UTC().Format(time.RFC3339),
		"layer":         cfg.ID,
		"accepted":      res.Accepted,
		"idempotent":    res.Idempotent,
		"conflicts":     conflicts,
		"lint_failures": len(res.LintFailures),
		"artifacts":     arts,
		"advisories":    advisories,
	})
}

// writeReingestError maps the ingest-pipeline sentinels to their §6.10
// structured error codes and HTTP status.
func writeReingestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ingest.ErrFrozen):
		writeError(w, http.StatusConflict, "ingest.frozen", err.Error())
	case errors.Is(err, ingest.ErrHistoryRewritten):
		writeError(w, http.StatusConflict, "ingest.history_rewritten", err.Error())
	case errors.Is(err, ingest.ErrLintFailed), errors.Is(err, ingest.ErrInvalidArtifact):
		writeError(w, http.StatusUnprocessableEntity, "ingest.lint_failed", err.Error())
	case errors.Is(err, ingest.ErrQuotaExceeded):
		writeError(w, http.StatusTooManyRequests, "quota.storage_exceeded", err.Error())
	case errors.Is(err, ingest.ErrAuditVolumeExceeded):
		writeError(w, http.StatusTooManyRequests, "quota.audit_volume_exceeded", err.Error())
	case errors.Is(err, ingest.ErrPublicModeSensitive):
		writeError(w, http.StatusUnprocessableEntity, "ingest.public_mode_rejects_sensitive", err.Error())
	case errors.Is(err, source.ErrSourceUnreachable):
		writeError(w, http.StatusBadGateway, "ingest.source_unreachable", err.Error())
	case errors.Is(err, source.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
	}
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
