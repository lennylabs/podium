package server

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/podium/pkg/identity"
)

// RuntimeKeyStore is the SPI the runtime endpoint consumes.
// In-memory and file-persisted registries both satisfy it.
type RuntimeKeyStore interface {
	Register(identity.RuntimeKey) error
	All() []identity.RuntimeKey
}

// RuntimeKeyEndpoint mounts the §6.3.2 admin endpoints that
// register and list trusted runtime signing keys. The registry
// passed in here is the same one the JWT verifier consults at
// request time.
type RuntimeKeyEndpoint struct {
	Registry  RuntimeKeyStore
	authAdmin func(*http.Request) error
	mode      *ModeTracker
}

// NewRuntimeKeyEndpoint returns an endpoint backed by reg.
func NewRuntimeKeyEndpoint(reg RuntimeKeyStore, mode *ModeTracker) *RuntimeKeyEndpoint {
	return &RuntimeKeyEndpoint{
		Registry:  reg,
		mode:      mode,
		authAdmin: func(*http.Request) error { return nil },
	}
}

// WithAdminAuth installs a non-default admin authorization hook.
func (e *RuntimeKeyEndpoint) WithAdminAuth(fn func(*http.Request) error) *RuntimeKeyEndpoint {
	e.authAdmin = fn
	return e
}

// Handler dispatches POST /v1/admin/runtime and GET
// /v1/admin/runtime.
func (e *RuntimeKeyEndpoint) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/admin/runtime", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			e.register(w, r)
		case http.MethodGet:
			e.list(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
				"method not allowed: "+r.Method)
		}
	})
	return mux
}

// RuntimeRegisterRequest is the POST body for /v1/admin/runtime.
type RuntimeRegisterRequest struct {
	Issuer    string `json:"issuer"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key_pem"`
}

// RuntimeRegisterResponse is the success body.
type RuntimeRegisterResponse struct {
	Issuer    string `json:"issuer"`
	Algorithm string `json:"algorithm"`
}

func (e *RuntimeKeyEndpoint) register(w http.ResponseWriter, r *http.Request) {
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
	var req RuntimeRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if req.Issuer == "" || req.Algorithm == "" || req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument",
			"issuer, algorithm, and public_key_pem are required")
		return
	}
	pub, err := identity.ParsePublicKeyPEM(req.PublicKey, req.Algorithm)
	if err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	if err := e.Registry.Register(identity.RuntimeKey{
		Issuer:    req.Issuer,
		Algorithm: req.Algorithm,
		Key:       pub,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, RuntimeRegisterResponse{
		Issuer:    req.Issuer,
		Algorithm: req.Algorithm,
	})
}

// RuntimeListResponse is the GET body.
type RuntimeListResponse struct {
	Runtimes []RuntimeRegisterResponse `json:"runtimes"`
}

func (e *RuntimeKeyEndpoint) list(w http.ResponseWriter, r *http.Request) {
	if err := e.authAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "auth.forbidden", err.Error())
		return
	}
	out := RuntimeListResponse{Runtimes: []RuntimeRegisterResponse{}}
	for _, k := range e.Registry.All() {
		out.Runtimes = append(out.Runtimes, RuntimeRegisterResponse{
			Issuer:    k.Issuer,
			Algorithm: k.Algorithm,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
