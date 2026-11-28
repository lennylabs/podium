package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/version"
)

// newAttestationChecker builds the §4.7.11 checker of the server: the
// consumer policy, and the bundles of the registry's bundle endpoint
// behind the attestation cache, under ${PODIUM_CACHE_DIR}/attestations/
// or in memory when the content cache is disabled. Under offline-only a
// bundle that is not cached is missing rather than fetched.
//
// Spec: §4.7.11 (Attestation check), §6.5
func (s *mcpServer) newAttestationChecker() *attest.Checker {
	a := s.consumer()
	var cache attest.BundleCache = &attest.MemoryCache{}
	if s.cfg.cacheDir != "" {
		cache = attest.DirCache{Dir: filepath.Join(s.cfg.cacheDir, "attestations"), MaxBytes: a.maxBytes}
	}
	return &attest.Checker{
		Policy:   a.policy,
		MaxBytes: a.maxBytes,
		Bundles: attest.CachedBundles{
			Cache: cache,
			Source: attest.HTTPBundleSource{
				BaseURL: s.cfg.registry, Client: s.http, MaxBytes: a.maxBytes,
				Prepare: s.prepareBundleRequest,
			},
			OfflineOnly: s.cfg.cacheMode == "offline-only",
		},
	}
}

// prepareBundleRequest attaches the caller credential and the headers of
// every registry call to a bundle fetch, so the fetch runs under the
// caller's read scope as a load_artifact request does. A credential that
// cannot be acquired leaves the request anonymous; the registry then
// answers it as it answers an anonymous caller.
//
// Spec: §4.7.11 (Attestation check, step 8), §7.3.5
func (s *mcpServer) prepareBundleRequest(req *http.Request) {
	if tok, err := s.bearerToken(); err == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if s.cfg.tenantID != "" {
		req.Header.Set("X-Podium-Tenant", s.cfg.tenantID)
	}
}

// startAttestationRefresh starts the refresh of each refreshed public-good
// root the policy verifies under; each refresh ends with ctx.
//
// Spec: §4.7.11 (Root copy of user-source rules)
func (s *mcpServer) startAttestationRefresh(ctx context.Context) {
	for _, r := range s.consumer().refreshers {
		go r.Run(ctx)
	}
}

// attestationCheck runs the §4.7.11 request binding, well-formed rule, and
// attestation check on a record that passed the §4.7.10 delivery check,
// before anything of it is cached, returned, or written. A failure is
// returned as a *attest.Failure; a warning of the mode warn is kept on resp
// for the load result.
//
// Spec: §4.7.11 (Request binding, Attestation check), §6.6 step 2
func (s *mcpServer) attestationCheck(resp *loadArtifactResponse, req attest.Request) error {
	checker := s.checker
	if checker == nil {
		// A server built without newServer, as a unit test of another step
		// builds one, holds no policy: the binding and the well-formed rule
		// still run.
		checker = &attest.Checker{}
	}
	res, err := checker.Verify(s.reqCtx(), attest.Delivery{Request: req, Served: servedForCheck(*resp)})
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "WARN: attestation check of %s: %v\n", req.ID, w)
		resp.attestationWarnings = append(resp.attestationWarnings, w.WarningText(req.ID))
	}
	return nil
}

// servedForCheck builds the served record the check reads from a verified
// response: the delivery record it reconstituted, and the deprecated,
// replaced_by, and attestations members. A record from the content cache
// carries the members cached with it; the cache keeps deprecated and
// replaced_by decoded, so they are written back in their JSON form.
func servedForCheck(resp loadArtifactResponse) version.Served {
	members := map[string]json.RawMessage{}
	if resp.Deprecated {
		members["deprecated"] = json.RawMessage("true")
	}
	if resp.ReplacedBy != "" {
		b, _ := json.Marshal(resp.ReplacedBy)
		members["replaced_by"] = b
	}
	if len(resp.Attestations) > 0 {
		members["attestations"] = resp.Attestations
	}
	return version.Served{Record: deliveryRecordOf(resp), Members: members}
}

// requestOf returns the request a delivery answers. Every load path passes
// the request it sent; a delivery that names none binds the record to its
// own ID with no pin, which a unit test of a later step reaches.
func requestOf(resp loadArtifactResponse, o deliverOpts) attest.Request {
	if o.request != nil {
		return *o.request
	}
	return attest.Request{ID: resp.ID}
}

// normalizeLoadArgs removes leading and trailing white space from the id
// and version arguments of a call before they are sent, so the request
// binding compares the values the request carried. A value of another
// JSON type is sent as its text, as before.
//
// Spec: §4.7.11 (Request binding)
func normalizeLoadArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	for _, k := range []string{"id", "version"} {
		if v, ok := out[k]; ok && v != nil {
			out[k] = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}
	return out
}

// verifyFailureResult converts a failure of the §6.6 step-2 checks to the
// load result: a §4.7.11 failure becomes the §6.10 envelope with its
// details.constraint and the requested ID, and every other error keeps the
// code its message leads with.
func verifyFailureResult(err error, requestedID string) map[string]any {
	f, ok := attest.FailureOf(err)
	if !ok {
		return errorResult(err.Error())
	}
	details := map[string]any{"artifact_id": requestedID}
	if f.Constraint != "" {
		details["constraint"] = f.Constraint
	}
	retryable, suggested := bridgeCodeMeta(f.Code)
	msg := strings.TrimPrefix(f.Error(), f.Code)
	msg = strings.TrimPrefix(strings.TrimPrefix(msg, " ("+f.Constraint+")"), ": ")
	return errorEnvelope(f.Code, msg, details, retryable, suggested)
}

// withLoadNotices adds the attestation_warning member to a load result: the
// warnings the check reported, the skipped overlay artifact of the
// requested ID, and the notices of a failed account lookup and of a
// machine file that fails its check, which §4.7.11 places in every load
// result because the standard error of an MCP server is often hidden.
//
// Spec: §4.7.11 (User home, Machine file, Workspace overlay), §6.1.1
func (s *mcpServer) withLoadNotices(result any, id string) any {
	m, ok := result.(map[string]any)
	if !ok {
		return result
	}
	var texts []string
	if w, ok := m["attestation_warning"].(string); ok && w != "" {
		texts = append(texts, w)
	}
	if id != "" && s.overlaySkippedFor(id) {
		f := attest.Failure{Code: attest.CodeAttestationUntrusted, Constraint: constraintOverlay}
		texts = append(texts, f.WarningText(id))
	}
	texts = append(texts, s.consumer().notices...)
	if len(texts) > 0 {
		m["attestation_warning"] = strings.Join(texts, "; ")
	}
	return m
}

// admitOverlay applies the §4.7.11 rules to the packages of the workspace
// overlay. A package whose ID or resource path breaks the well-formed rule
// is skipped and named on standard error, under every policy. A package
// whose ID the user sources resolve to require is not applied unless the
// user's sync.yaml or the machine file sets overlay to allow, and deny
// applies no package; the registry then serves that ID under the
// attestation check, and its load reports the skipped package. It returns
// the packages to apply and the IDs skipped by the policy.
//
// Spec: §4.7.11 (Workspace overlay, Well-formed requests and records), §6.4
func (s *mcpServer) admitOverlay(records []filesystem.ArtifactRecord) ([]filesystem.ArtifactRecord, map[string]bool) {
	var kept []filesystem.ArtifactRecord
	skipped := map[string]bool{}
	a := s.consumer()
	for _, rec := range records {
		if err := wellFormedOverlay(rec); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: skipping the workspace overlay package %q: %v\n", rec.ID, err)
			continue
		}
		switch {
		case a.overlay == "deny":
			skipped[rec.ID] = true
		case a.overlay == "allow":
			kept = append(kept, rec)
		case a.policy != nil && a.policy.Resolve(rec.ID).UserMode == attest.ModeRequire:
			skipped[rec.ID] = true
		default:
			kept = append(kept, rec)
		}
	}
	for id := range skipped {
		fmt.Fprintf(os.Stderr, "WARN: the workspace overlay package %q is not applied: the user policy requires an attestation for it\n", id)
	}
	return kept, skipped
}

// wellFormedOverlay applies the well-formed rule to the ID and the resource
// paths of an overlay package.
func wellFormedOverlay(rec filesystem.ArtifactRecord) error {
	if err := attest.CheckRequestedID(rec.ID); err != nil {
		return err
	}
	for p := range rec.Resources {
		if err := attest.CheckResourcePath(p); err != nil {
			return err
		}
	}
	return nil
}

// consumer returns the resolved consumer trust policy, an empty one for a
// server built without loadConfig.
func (s *mcpServer) consumer() *consumerAttestation {
	if s.cfg == nil || s.cfg.attestation == nil {
		return &consumerAttestation{}
	}
	return s.cfg.attestation
}

// overlaySkippedFor reports whether the overlay held a package for id that
// the policy did not apply.
func (s *mcpServer) overlaySkippedFor(id string) bool {
	s.overlayMu.RLock()
	defer s.overlayMu.RUnlock()
	return s.overlaySkipped[id]
}
