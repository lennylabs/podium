package sign

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/version"
)

// DeliveryCheck is the §4.7.10 step 7 check every Go consumer of a
// registry-served record runs: the MCP server and server-source podium sync.
// Policy is the consumer's resolved §4.7.9 policy, and Verifier is the
// registry-managed verifier, nil under never.
type DeliveryCheck struct {
	// Policy is the resolved §4.7.9 verification policy.
	Policy VerificationPolicy
	// Verifier verifies delivery_signature. EnforceVerification never touches
	// it under never, so a check resolved under never may leave it nil.
	Verifier Provider
}

// Verify recomputes the delivery hash of rec, compares it with servedHash, and
// then applies the policy to (servedHash, servedSig). The error message leads
// with the §6.10 code the consumer returns.
//
// The hash comparison runs first and under every policy. The signature attests
// delivery_hash, so verifying it says nothing about bytes that do not
// reproduce that hash; running the comparison first makes a record whose bytes
// were altered report materialize.content_hash_mismatch whatever its
// signature. A missing required signature and a signature that does not
// validate keep separate codes because their operator remedies differ.
//
// Spec: §4.7.10, §6.6 step 2
func (c DeliveryCheck) Verify(ctx context.Context, rec version.DeliveryRecord, servedHash, servedSig string) error {
	if servedHash == "" {
		return errors.New("materialize.content_hash_mismatch: the response carries no delivery_hash")
	}
	if got := version.DeliveryHash(rec); got != servedHash {
		return fmt.Errorf("materialize.content_hash_mismatch: recomputed delivery hash %s does not match served %s", got, servedHash)
	}
	if err := EnforceVerification(ctx, c.Policy, c.Verifier, servedHash, servedSig); err != nil {
		if errors.Is(err, ErrSignatureMissing) {
			return fmt.Errorf("materialize.signature_missing: %w", err)
		}
		return fmt.Errorf("materialize.signature_invalid: %w", err)
	}
	return nil
}
