package server

import (
	"context"
	"fmt"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// WithDeliverySigner installs the registry-managed key that signs every served
// §4.7.10 delivery hash. handleLoadArtifact and loadOneForBatch read the one
// instance this sets, so the single-load and the batch paths cannot serve
// differently-attested records. A nil signer serves each delivery hash with an
// empty delivery signature.
func WithDeliverySigner(p sign.Provider) Option {
	return func(s *Server) { s.deliverySigner = p }
}

// deliveryRecordOf builds the §4.7.10 delivery record of an admitted load
// result. Every value is the one the response serves before the manifest-body
// channel moves the document out of line, and each resource contributes the
// content hash its ref carries rather than its body, so composing the record
// reads nothing from object storage. The per-caller ExtendsPin is excluded.
//
// Spec: §4.7.10.
func deliveryRecordOf(res *core.LoadArtifactResult) version.DeliveryRecord {
	rec := version.DeliveryRecord{
		ID:           res.ID,
		Version:      res.Version,
		Type:         res.Type,
		ContentHash:  res.ContentHash,
		Sensitivity:  res.Sensitivity,
		Frontmatter:  string(res.Frontmatter),
		ManifestBody: res.ManifestBody,
		SkillRaw:     string(res.SkillRaw),
		Resources:    make(map[string]string, len(res.Resources)),
	}
	for _, ref := range res.Resources {
		rec.Resources[ref.Path] = ref.ContentHash
	}
	return rec
}

// attestDelivery returns the delivery hash of res and, under a configured
// signer, its signature. It runs only over a result core.LoadArtifact admitted,
// so the registry countersigns only rows §13.4 stored-row admission passed.
//
// Spec: §4.7.10.
func (s *Server) attestDelivery(ctx context.Context, res *core.LoadArtifactResult) (hash, signature string, err error) {
	hash = version.DeliveryHash(deliveryRecordOf(res))
	if s.deliverySigner == nil {
		return hash, "", nil
	}
	signature, err = s.deliverySigner.Sign(ctx, hash)
	if err != nil {
		return "", "", fmt.Errorf("sign delivery hash for %s: %w", res.ID, err)
	}
	return hash, signature, nil
}
