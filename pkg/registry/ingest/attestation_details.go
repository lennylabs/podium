package ingest

import (
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// refusedBundles lists up to 5 bundles that the signers refused, each with
// its attestation digest and reason. For a bundle whose signature, chain,
// and time verified under an instance a signer names, the entry also holds
// that instance and the certificate fields an operator compares with the
// layer's signers. A bundle that verified under no configured instance can
// carry a certificate its sender made, so its entry holds no certificate
// field.
//
// Spec: §4.7.11 (Layer step, Reported strings)
func refusedBundles(bundles []*snapshotBundle, signers []store.TrustSigner, a *AttestationCycle) []map[string]any {
	out := []map[string]any{}
	for _, b := range bundles {
		if len(out) == detailsBundles {
			break
		}
		entry := map[string]any{"digest": b.digest, "reason": string(b.reason)}
		for _, s := range signers {
			inst, _ := a.instance(s.Instance)
			if v := b.verified[inst.Name]; v != nil {
				addCertificate(entry, inst.Name, v.Certificate)
				break
			}
		}
		out = append(out, entry)
	}
	return out
}

// addCertificate adds the instance and the bounded certificate fields of a
// verified bundle to entry.
func addCertificate(entry map[string]any, instance string, c sign.CertificateFields) {
	entry["instance"] = instance
	for name, f := range map[string]sign.CertField{
		"issuer":                     c.Issuer,
		"identity":                   c.Identity,
		"source_repository":          c.SourceRepository,
		"source_repository_owner":    c.SourceRepositoryOwner,
		"source_repository_id":       c.SourceRepositoryID,
		"source_repository_owner_id": c.SourceRepositoryOwnerID,
		"source_repository_ref":      c.SourceRepositoryRef,
	} {
		if f.Present {
			entry[name] = attest.ReportString(f.Value)
		}
	}
}

// layerDigestFailure is layer step 3's mismatch: no accepted bundle carries
// the snapshot's layer digest. details lists up to 20 subjects of each kind:
// those only in the snapshot, those only in the accepted bundles of the
// covering requirement, and those in both with different digests, the first
// 5 of which carry the resolution state and the resolved fields the registry
// framed. subjects_agree is true when no subject differs, which places the
// difference in a layer file.
//
// Spec: §4.7.11 (Layer step)
func layerDigestFailure(name, digest string, input attest.Layer, accepted []*snapshotBundle, all []filesystem.ArtifactRecord) *AttestationError {
	listed := bundleSubjects(accepted)
	snapshot := map[string]string{}
	for _, s := range input.Subjects {
		snapshot[s.Name] = trimDigest(s.Digest)
	}
	records := map[string]*filesystem.ArtifactRecord{}
	for i := range all {
		if n, err := all[i].SubjectName(); err == nil {
			records[n] = &all[i]
		}
	}
	var onlySnapshot, onlyBundles []string
	different := []map[string]any{}
	for _, n := range sortedKeys(snapshot) {
		digests, ok := listed[n]
		switch {
		case !ok:
			onlySnapshot = append(onlySnapshot, n)
		case !digests[snapshot[n]]:
			entry := map[string]any{"subject": attest.ReportString(n), "digest": "sha256:" + snapshot[n], "listed": listedDigests(digests)}
			if len(different) < detailsFields {
				addResolution(entry, records[n])
			}
			different = append(different, entry)
		}
	}
	for _, n := range sortedKeys(listed) {
		if _, ok := snapshot[n]; !ok {
			onlyBundles = append(onlyBundles, n)
		}
	}
	diff := len(onlySnapshot) + len(onlyBundles) + len(different)
	return attestFailure(CodeAttestationMismatch, "layer_digest",
		"no accepted bundle carries the snapshot's layer digest "+digest,
		"layer_subject", attest.ReportString(name), "layer_digest", digest,
		"only_in_snapshot", attest.ReportStrings(onlySnapshot, detailsSubjects),
		"only_in_bundles", attest.ReportStrings(onlyBundles, detailsSubjects),
		"different", different[:min(len(different), detailsSubjects)],
		"subjects_agree", diff == 0)
}

// bundleSubjects returns the artifact subjects the bundles list, each name
// with its set of listed hexadecimal digests. Layer subjects and reserved
// names are left out.
func bundleSubjects(bundles []*snapshotBundle) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, b := range bundles {
		for _, s := range b.accepted[coveringRequirement].Statement.Subjects {
			if s.Name == "" || strings.HasPrefix(s.Name, ".podium/") {
				continue
			}
			if out[s.Name] == nil {
				out[s.Name] = map[string]bool{}
			}
			out[s.Name][s.Digest["sha256"]] = true
		}
	}
	return out
}

// listedDigests returns the bounded digests of a set in ascending order.
func listedDigests(set map[string]bool) []string {
	var out []string
	for _, d := range sortedKeys(set) {
		out = append(out, "sha256:"+attest.ReportString(d))
	}
	return out[:min(len(out), detailsNames)]
}

// addResolution adds the resolution state and the resolved fields of rec to
// entry.
func addResolution(entry map[string]any, rec *filesystem.ArtifactRecord) {
	if rec == nil {
		return
	}
	if rec.Unparseable != "" {
		entry["state"] = "unresolved"
		return
	}
	entry["state"] = "resolved"
	entry["type"] = attest.ReportString(string(rec.Fields.Type))
	entry["sensitivity"] = attest.ReportString(string(rec.Fields.Sensitivity))
	entry["deprecated"] = rec.Fields.Deprecated
	entry["replaced_by"] = attest.ReportString(rec.Fields.ReplacedBy)
}

// sortedKeys returns the keys of m in ascending byte order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
