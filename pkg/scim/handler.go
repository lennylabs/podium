package scim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Handler is the SCIM HTTP front-end. Mount under /scim/v2/.
//
// Routes:
//
//	GET    /Users[/{id}]
//	POST   /Users
//	PUT    /Users/{id}
//	DELETE /Users/{id}
//	GET    /Groups[/{id}]
//	POST   /Groups
//	PUT    /Groups/{id}
//	DELETE /Groups/{id}
//
// Auth: bearer token validated against the configured Tokens set.
type Handler struct {
	Store  Store
	Tokens map[string]bool // valid bearer tokens
}

// scimError serializes the SCIM-conformant error envelope IdPs
// expect (RFC 7644 §3.12).
type scimError struct {
	Schemas  []string `json:"schemas"`
	Detail   string   `json:"detail"`
	Status   string   `json:"status"`
	SCIMType string   `json:"scimType,omitempty"`
}

func writeSCIMError(w http.ResponseWriter, status int, scimType, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(scimError{
		Schemas:  []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
		Detail:   detail,
		Status:   fmt.Sprintf("%d", status),
		SCIMType: scimType,
	})
}

// Handler implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Bearer token auth. SCIM RFC 7644 leaves the auth scheme to
	// the deployment; bearer is the common choice.
	auth := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(auth, "Bearer ")
	if len(h.Tokens) > 0 && (auth == "" || tok == auth || !h.Tokens[tok]) {
		writeSCIMError(w, http.StatusUnauthorized, "", "missing or invalid bearer token")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/scim/v2")
	switch {
	case strings.HasPrefix(path, "/Users"):
		h.routeUsers(w, r, strings.TrimPrefix(path, "/Users"))
	case strings.HasPrefix(path, "/Groups"):
		h.routeGroups(w, r, strings.TrimPrefix(path, "/Groups"))
	case path == "/ServiceProviderConfig":
		writeSCIMError(w, http.StatusNotImplemented, "", "not implemented")
	default:
		writeSCIMError(w, http.StatusNotFound, "", "no such SCIM endpoint: "+path)
	}
}

func (h *Handler) routeUsers(w http.ResponseWriter, r *http.Request, suffix string) {
	suffix = strings.TrimPrefix(suffix, "/")
	switch {
	case suffix == "" && r.Method == http.MethodGet:
		h.listUsers(w, r)
	case suffix == "" && r.Method == http.MethodPost:
		h.createUser(w, r)
	case suffix != "" && r.Method == http.MethodGet:
		h.getUser(w, r, suffix)
	case suffix != "" && r.Method == http.MethodPut:
		h.replaceUser(w, r, suffix)
	case suffix != "" && r.Method == http.MethodDelete:
		h.deleteUser(w, r, suffix)
	default:
		writeSCIMError(w, http.StatusMethodNotAllowed, "", "method not allowed")
	}
}

func (h *Handler) routeGroups(w http.ResponseWriter, r *http.Request, suffix string) {
	suffix = strings.TrimPrefix(suffix, "/")
	switch {
	case suffix == "" && r.Method == http.MethodGet:
		h.listGroups(w, r)
	case suffix == "" && r.Method == http.MethodPost:
		h.createGroup(w, r)
	case suffix != "" && r.Method == http.MethodGet:
		h.getGroup(w, r, suffix)
	case suffix != "" && r.Method == http.MethodPut:
		h.replaceGroup(w, r, suffix)
	case suffix != "" && r.Method == http.MethodDelete:
		h.deleteGroup(w, r, suffix)
	default:
		writeSCIMError(w, http.StatusMethodNotAllowed, "", "method not allowed")
	}
}

// ----- Users -------------------------------------------------------------

type scimUser struct {
	Schemas    []string         `json:"schemas"`
	ID         string           `json:"id,omitempty"`
	ExternalID string           `json:"externalId,omitempty"`
	UserName   string           `json:"userName"`
	Active     bool             `json:"active"`
	Emails     []scimUserEmail  `json:"emails,omitempty"`
}

type scimUserEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
}

func userToSCIM(u User) scimUser {
	out := scimUser{
		Schemas:    []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		ID:         u.ID,
		ExternalID: u.ExternalID,
		UserName:   u.UserName,
		Active:     u.Active,
	}
	if u.Email != "" {
		out.Emails = []scimUserEmail{{Value: u.Email, Primary: true}}
	}
	return out
}

func scimToUser(s scimUser) User {
	out := User{
		ID: s.ID, ExternalID: s.ExternalID, UserName: s.UserName, Active: s.Active,
	}
	for _, e := range s.Emails {
		if e.Primary || out.Email == "" {
			out.Email = e.Value
		}
	}
	return out
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var s scimUser
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	created, err := h.Store.CreateUser(r.Context(), scimToUser(s))
	if err != nil {
		if errors.Is(err, ErrConflict) {
			writeSCIMError(w, http.StatusConflict, "uniqueness", err.Error())
			return
		}
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	writeSCIMResponse(w, http.StatusCreated, userToSCIM(created))
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request, id string) {
	u, err := h.Store.GetUser(r.Context(), id)
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", err.Error())
		return
	}
	writeSCIMResponse(w, http.StatusOK, userToSCIM(u))
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r.URL.Query().Get("filter"))
	if err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidFilter", err.Error())
		return
	}
	users, err := h.Store.ListUsers(r.Context(), filter)
	if err != nil {
		writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
		return
	}
	out := make([]scimUser, len(users))
	for i, u := range users {
		out[i] = userToSCIM(u)
	}
	writeSCIMResponse(w, http.StatusOK, scimList(out))
}

func (h *Handler) replaceUser(w http.ResponseWriter, r *http.Request, id string) {
	var s scimUser
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	updated, err := h.Store.ReplaceUser(r.Context(), id, scimToUser(s))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", err.Error())
			return
		}
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	writeSCIMResponse(w, http.StatusOK, userToSCIM(updated))
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.Store.DeleteUser(r.Context(), id); err != nil {
		writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Groups ------------------------------------------------------------

type scimGroup struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id,omitempty"`
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members,omitempty"`
}

type scimGroupMember struct {
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

func groupToSCIM(g Group) scimGroup {
	members := make([]scimGroupMember, 0, len(g.MemberIDs))
	for _, m := range g.MemberIDs {
		members = append(members, scimGroupMember{Value: m, Type: "User"})
	}
	return scimGroup{
		Schemas:     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		ID:          g.ID,
		DisplayName: g.DisplayName,
		Members:     members,
	}
}

func scimToGroup(s scimGroup) Group {
	ids := make([]string, 0, len(s.Members))
	for _, m := range s.Members {
		ids = append(ids, m.Value)
	}
	return Group{ID: s.ID, DisplayName: s.DisplayName, MemberIDs: ids}
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var s scimGroup
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	created, err := h.Store.CreateGroup(r.Context(), scimToGroup(s))
	if err != nil {
		if errors.Is(err, ErrConflict) {
			writeSCIMError(w, http.StatusConflict, "uniqueness", err.Error())
			return
		}
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	writeSCIMResponse(w, http.StatusCreated, groupToSCIM(created))
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request, id string) {
	g, err := h.Store.GetGroup(r.Context(), id)
	if err != nil {
		writeSCIMError(w, http.StatusNotFound, "", err.Error())
		return
	}
	writeSCIMResponse(w, http.StatusOK, groupToSCIM(g))
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r.URL.Query().Get("filter"))
	if err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidFilter", err.Error())
		return
	}
	groups, err := h.Store.ListGroups(r.Context(), filter)
	if err != nil {
		writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
		return
	}
	out := make([]scimGroup, len(groups))
	for i, g := range groups {
		out[i] = groupToSCIM(g)
	}
	writeSCIMResponse(w, http.StatusOK, scimList(out))
}

func (h *Handler) replaceGroup(w http.ResponseWriter, r *http.Request, id string) {
	var s scimGroup
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	updated, err := h.Store.ReplaceGroup(r.Context(), id, scimToGroup(s))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeSCIMError(w, http.StatusNotFound, "", err.Error())
			return
		}
		writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	writeSCIMResponse(w, http.StatusOK, groupToSCIM(updated))
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.Store.DeleteGroup(r.Context(), id); err != nil {
		writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- helpers ----------------------------------------------------------

// parseFilter accepts the SCIM filter subset Podium supports:
// `<attr> <op> "<value>"`. Unsupported operators return
// ErrInvalidFilter; an empty input returns the zero Filter (matches
// everything).
func parseFilter(input string) (Filter, error) {
	if input == "" {
		return Filter{}, nil
	}
	parts := strings.SplitN(input, " ", 3)
	if len(parts) < 2 {
		return Filter{}, fmt.Errorf("%w: %q", ErrInvalidFilter, input)
	}
	attr := parts[0]
	op := strings.ToLower(parts[1])
	value := ""
	if len(parts) == 3 {
		value = strings.Trim(parts[2], `"`)
	}
	switch op {
	case "eq", "sw", "co":
		if value == "" {
			return Filter{}, fmt.Errorf("%w: missing value", ErrInvalidFilter)
		}
	case "pr":
		// presence test; no value
	default:
		return Filter{}, fmt.Errorf("%w: unsupported operator %q", ErrInvalidFilter, op)
	}
	return Filter{Attribute: attr, Operator: op, Value: value}, nil
}

func writeSCIMResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// scimList wraps a results slice in the SCIM ListResponse shape.
func scimList[T any](items []T) map[string]any {
	return map[string]any{
		"schemas":      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
		"totalResults": len(items),
		"itemsPerPage": len(items),
		"startIndex":   1,
		"Resources":    items,
	}
}

// quiet unused-import linter when the build path doesn't reach
// context (for example when only the parser is exercised).
var _ = context.TODO
