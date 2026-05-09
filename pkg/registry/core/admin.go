package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// ErrForbidden signals an admin-only operation invoked by a
// non-admin caller. Maps to auth.forbidden in §6.10 / §4.7.2.
var ErrForbidden = errors.New("auth.forbidden")

// AdminAuthorize verifies that id has the admin role for the
// registry's tenant before an admin operation proceeds. The §4.7.2
// admin grant table backs the check; in public mode, ErrForbidden
// is returned because public-mode deployments do not authenticate
// callers (so no caller can ever hold the admin role).
func (r *Registry) AdminAuthorize(ctx context.Context, id layer.Identity) error {
	if id.IsPublic || !id.IsAuthenticated || id.Sub == "" {
		return fmt.Errorf("%w: admin operations require an authenticated identity", ErrForbidden)
	}
	ok, err := r.store.IsAdmin(ctx, id.Sub, r.tenantID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s is not an admin in %s", ErrForbidden, id.Sub, r.tenantID)
	}
	return nil
}

// GrantAdmin records an admin grant for userID in the tenant. The
// caller must already be an admin; the HTTP handler enforces this.
func (r *Registry) GrantAdmin(ctx context.Context, userID string) error {
	return r.store.GrantAdmin(ctx, store.AdminGrant{
		UserID: userID, OrgID: r.tenantID,
	})
}

// RevokeAdmin removes the (userID, tenant) grant. Idempotent: a
// missing grant is a no-op (the underlying store treats it that
// way too).
func (r *Registry) RevokeAdmin(ctx context.Context, userID string) error {
	return r.store.RevokeAdmin(ctx, userID, r.tenantID)
}

// EffectiveLayer is one entry in the result of ShowEffective.
type EffectiveLayer struct {
	LayerID string
	Visible bool
	Reason  string
}

// ShowEffective returns the layer-by-layer visibility for a target
// identity. Each entry carries a human-readable Reason so the
// operator can see why a given layer is (or is not) in the user's
// view.
func (r *Registry) ShowEffective(ctx context.Context, target layer.Identity) ([]EffectiveLayer, error) {
	out := make([]EffectiveLayer, 0, len(r.layers))
	for _, l := range r.layers {
		visible := layer.Visible(l, target)
		out = append(out, EffectiveLayer{
			LayerID: l.ID,
			Visible: visible,
			Reason:  visibilityReason(l, target, visible),
		})
	}
	return out, nil
}

// visibilityReason returns a stable one-liner explaining why l is
// visible to id (or not). Operators grep for these in support
// conversations.
func visibilityReason(l layer.Layer, id layer.Identity, visible bool) string {
	switch {
	case l.Visibility.Public:
		return "layer.public=true"
	case id.IsPublic && !visible:
		return "caller is anonymous; layer requires authentication"
	case l.Visibility.Organization && id.IsAuthenticated && visible:
		return "layer.organization=true and identity is authenticated"
	case visible:
		return "user matches layer.users or layer.groups"
	default:
		return "user is not in layer.users or layer.groups"
	}
}
