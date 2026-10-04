package integration

import "github.com/lennylabs/podium/pkg/sign"

// neverDelivery is the sync.DeliveryCheckFunc the server-source sync tests
// pass: the §4.7.10 delivery hash is recomputed and compared on every record,
// and no delivery signature is required.
func neverDelivery() (*sign.DeliveryCheck, error) {
	return &sign.DeliveryCheck{Policy: sign.PolicyNever}, nil
}
