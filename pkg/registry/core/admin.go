package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/layer"
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
