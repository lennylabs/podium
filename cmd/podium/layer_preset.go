package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/attest/ghactions"
	"github.com/lennylabs/podium/pkg/store"
)

// presetRequest is what --trust-preset reads from `podium layer register`.
type presetRequest struct {
	name  string
	repo  string
	ref   string
	trust *trustFlags
	set   map[string]bool
}

// presetEnv is what resolving the preset reads besides the flags: the
// environment, the API base URL, and the transport of the one request.
type presetEnv struct {
	getenv    func(string) string
	apiBase   string
	transport http.RoundTripper
	stderr    io.Writer
}

// presetExclusiveFlags are the trust flags the preset sets itself. Beside
// the preset each is a usage error, because the preset would otherwise
// either drop the value or combine it with bindings it was not written for.
var presetExclusiveFlags = []string{
	"trust-layer-root", "trust-source-repository", "trust-source-owner", "trust-source-repository-id",
	"trust-source-owner-id", "trust-source-repository-ref", "trust-instance", "trust-any-source", "trust-any-ref",
}

// resolveTrustPreset resolves --trust-preset github-actions into the ref,
// trust_mode, and trust_requirements members of the registration. It
// returns code -1 with the members when the registration proceeds, and the
// exit code otherwise, having written the reason: 2 for a usage error, and 1
// for a refusal, which registers nothing and names the explicit flags. A
// --repo outside github.com sends no request.
//
// Spec: §7.3.1 (Trust preset)
func resolveTrustPreset(ctx context.Context, req presetRequest, env presetEnv) (map[string]any, int) {
	if code := checkPresetUsage(req, env.stderr); code >= 0 {
		return nil, code
	}
	timeout, err := ghactions.TimeoutFromEnv(env.getenv)
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: %v\n", err)
		return nil, 2
	}
	repo, err := ghactions.ParseRepoURL(req.repo)
	if err != nil {
		presetRefusal(env.stderr, fmt.Sprintf("--trust-preset %s covers a --repo on github.com, and %q is not one, so no request was sent", ghactions.PresetName, req.repo),
			ghactions.GenericFlags)
		return nil, 1
	}
	f := ghactions.Fetcher{Base: env.apiBase, Token: ghactions.TokenFromEnv(env.getenv), Timeout: timeout, Transport: env.transport}
	rec, err := f.Fetch(ctx, repo)
	if err != nil {
		presetRefusal(env.stderr, presetFetchReason(repo, err), ghactions.ExplicitFlags(repo, ghactions.Record{}, "", ""))
		return nil, 1
	}
	if rec.FullName != repo.FullName() {
		_, _ = fmt.Fprintf(env.stderr, "error: the GitHub API answers for %s with the record of %s: the repository was renamed, or the letter case of its name differs. An identity built from %s matches no certificate; nothing was registered. Register with --repo %s.\n",
			repo.FullName(), rec.FullName, repo.FullName(), rec.Repo().URL())
		return nil, 1
	}
	if !rec.Public() {
		presetRefusal(env.stderr, fmt.Sprintf("the GitHub API reports %s as %s, and --trust-preset %s covers public repositories. GitHub signs for a private or internal repository with its own Sigstore instance, which needs GitHub Enterprise Cloud and the github instance on the registry",
			rec.FullName, visibilityName(rec), ghactions.PresetName),
			ghactions.ExplicitFlags(repo, rec, "", ghactions.PrivateInstance))
		return nil, 1
	}
	p, err := ghactions.Resolve(rec, ghactions.Overrides{Mode: *req.trust.mode, Ref: req.ref, Signers: req.trust.signers})
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: %v\n", err)
		return nil, 2
	}
	members := map[string]any{
		"ref":                p.Ref,
		"trust_mode":         p.Mode,
		"trust_requirements": []store.TrustRequirement{p.Requirement},
	}
	writeResolvedPreset(env.stderr, rec.FullName, members)
	return members, -1
}

// checkPresetUsage returns 2, having written the reason, for a preset
// request that no record can make valid, and -1 otherwise.
func checkPresetUsage(req presetRequest, stderr io.Writer) int {
	if req.name != ghactions.PresetName {
		_, _ = fmt.Fprintf(stderr, "error: --trust-preset %q is no preset; the preset is %s\n", req.name, ghactions.PresetName)
		return 2
	}
	if req.repo == "" {
		_, _ = fmt.Fprintf(stderr, "error: --trust-preset %s applies to a --repo registration\n", ghactions.PresetName)
		return 2
	}
	var conflicts []string
	for _, name := range presetExclusiveFlags {
		if req.set[name] {
			conflicts = append(conflicts, "--"+name)
		}
	}
	if len(conflicts) > 0 {
		_, _ = fmt.Fprintf(stderr, "error: --trust-preset %s sets %s; register with the explicit trust flags in place of the preset to set them\n",
			ghactions.PresetName, strings.Join(conflicts, ", "))
		return 2
	}
	return -1
}

// presetFetchReason states why the record could not be read.
func presetFetchReason(repo ghactions.Repo, err error) string {
	var status *ghactions.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return fmt.Sprintf("the GitHub API answered HTTP 404 for %s. The repository does not exist, or it is private and GH_TOKEN or GITHUB_TOKEN holds no token that reads it", repo.FullName())
	}
	return fmt.Sprintf("the record of %s could not be read from the GitHub API: %v", repo.FullName(), err)
}

// presetRefusal writes a refusal of the preset with the explicit flags that
// register the layer without it.
func presetRefusal(w io.Writer, reason, flags string) {
	_, _ = fmt.Fprintf(w, "error: %s. Nothing was registered. Register the layer with the explicit trust flags:\n    %s\n", reason, flags)
}

// visibilityName names the record's visibility for a message.
func visibilityName(rec ghactions.Record) string {
	if rec.Visibility == "" {
		return "not public"
	}
	return rec.Visibility
}

// writeResolvedPreset writes the policy the preset resolved, so the operator
// reads what the registration sends.
func writeResolvedPreset(w io.Writer, fullName string, members map[string]any) {
	b, err := json.MarshalIndent(members, "", "  ")
	if err != nil {
		// The members are strings and plain structs, which always marshal.
		return
	}
	_, _ = fmt.Fprintf(w, "trust preset %s resolved for %s:\n%s\n", ghactions.PresetName, fullName, b)
}
