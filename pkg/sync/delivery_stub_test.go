package sync

import (
	"context"
	"crypto/ed25519"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// neverDelivery resolves the §4.7.10 delivery check under never: the delivery
// hash is still recomputed and compared, and no signature is required.
func neverDelivery() (*sign.DeliveryCheck, error) {
	return &sign.DeliveryCheck{Policy: sign.PolicyNever}, nil
}

// deliverySigner is a registry-managed signing key a stub registry signs its
// served delivery hashes with.
type deliverySigner struct {
	key sign.RegistryManagedKey
}

// newDeliverySigner returns a signer over a fresh Ed25519 key.
func newDeliverySigner(t *testing.T) deliverySigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return deliverySigner{key: sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub}}
}

// check returns a DeliveryCheckFunc that verifies under always with this
// signer's public key trusted.
func (s deliverySigner) check() DeliveryCheckFunc {
	return func() (*sign.DeliveryCheck, error) {
		return &sign.DeliveryCheck{
			Policy:   sign.PolicyAlways,
			Verifier: sign.RegistryManagedKey{Trusted: []ed25519.PublicKey{s.key.PublicKey}},
		}, nil
	}
}

// seal sets delivery_hash on resp and, with a non-nil signer, a
// delivery_signature over it.
func seal(t *testing.T, resp map[string]any, signer *deliverySigner) map[string]any {
	t.Helper()
	testharness.SealDelivery(resp)
	if signer == nil {
		return resp
	}
	sig, err := signer.key.Sign(context.Background(), resp["delivery_hash"].(string))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	resp["delivery_signature"] = sig
	return resp
}
