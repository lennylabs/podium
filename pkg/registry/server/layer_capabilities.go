package server

import (
	"net/http"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// LayerCapabilities reports what a caller may do on the §7.3.1 layer
// operations on this deployment. It is reported by the §7.3.4 posture read and
// is a prediction: the endpoint that runs the operation authorizes it.
//
// Spec: §7.3.4
type LayerCapabilities struct {
	ManageAnyLayer bool `json:"manage_any_layer"`
}

// Capabilities evaluates the caller's layer capabilities from the same
// authAdmin callback authorizeLocalSource takes its admin arm from, so the
// value a client renders on and the gate this endpoint applies are one
// expression.
//
// Spec: §7.3.4
func (e *LayerEndpoint) Capabilities(r *http.Request) LayerCapabilities {
	return LayerCapabilities{ManageAnyLayer: e.authAdmin(r) == nil}
}

// namesHostPath reports whether an operation names or re-reads a filesystem
// path on the registry host. A stored layer of a custom §9.1 source type that
// carries a path is included, because the orchestrator hands that path to the
// provider whatever the source type says.
//
// A "git" source is classified on its repository string alone. Git.Snapshot
// reads Repo, Ref, and Root and never the configured path, while the
// orchestrator copies LocalPath into the source config for every source type,
// so a stored git layer carrying a path reads none of it. Such a layer is
// producible today, because register copies req.LocalPath into the config with
// no source-type condition and update assigns cfg.LocalPath on any layer.
// Refusing it would confine nothing and would answer every webhook delivery
// for that layer 403 permanently, with no self-service recovery, because
// update treats an empty local_path as "leave unchanged".
//
// Spec: §7.3.1 (local-source authorization)
func namesHostPath(sourceType, localPath, repo string) bool {
	return sourceType == "local" ||
		(sourceType != "git" && localPath != "") ||
		isFileTransportRepo(repo)
}

// isFileTransportRepo reports whether a git repository string resolves to
// go-git's file transport rather than to a network transport. go-git's default
// protocol map registers "file" alongside http, https, ssh, and git, and its
// file client runs git-upload-pack against the named path, so
// "/srv/other-tenant" clones a host directory. Git.Snapshot validates nothing
// about cfg.Repo, so this is where the classification lives.
//
// It asks go-git rather than restating go-git's parser: transport.NewEndpoint
// is the same disambiguation Git.Snapshot's clone reaches, so a string this
// classifier admits is a string go-git does not resolve to a host path. A
// hand-written user@host:path predicate diverges from it in both directions:
// go-git rejects the scp-like reading whenever the segment before the first
// ":" carries a "/", which sends "/srv/repos@h:x" to the file transport, while
// "host:path" with no user prefix is ssh to go-git and is admitted here too.
//
// A string go-git cannot parse at all is treated as a host path, which is the
// fail-closed arm. An empty string is classified as nothing, because an empty
// repo names no path and the caller's other fields decide the arm.
//
// Spec: §7.3.1 (local-source authorization)
func isFileTransportRepo(repo string) bool {
	if repo == "" {
		return false
	}
	ep, err := transport.NewEndpoint(repo)
	if err != nil {
		return true
	}
	return ep.Protocol == "file"
}

// authorizeLocalSource refuses a caller the §4.7.2 admin arm does not admit on
// any operation that names or re-reads a filesystem path on the registry host.
// It runs after the write gate on update, restore, and reingest. On a register
// whose ID names no stored layer it is the only refusal for an authenticated
// caller, because the coarse gate there refuses only a caller who resolves no
// verified subject, so this is the arm that closes the arbitrary read.
//
// The message names the constraint and the remedy and names no filesystem
// path, so a refusal discloses nothing about the host's directory tree.
//
// Spec: §7.3.1
func (e *LayerEndpoint) authorizeLocalSource(w http.ResponseWriter, r *http.Request, sourceType, localPath, repo string) bool {
	if !namesHostPath(sourceType, localPath, repo) {
		return true
	}
	if e.authAdmin(r) == nil {
		return true
	}
	writeErrorDetails(w, http.StatusForbidden, "auth.forbidden",
		"a layer whose source is a filesystem path on the registry host may be registered, patched, restored, and reingested by a tenant admin alone; ask an administrator to run this operation, or use a git source that names a network repository",
		map[string]any{"constraint": "local_source"})
	return false
}
