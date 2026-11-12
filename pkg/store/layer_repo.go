package store

import (
	"database/sql"
	"errors"

	"github.com/lennylabs/podium/internal/repourl"
)

// RegisteredRepo is a git remote as registered, which may carry a URL
// credential. String and GoString return a fixed text, so %v, %+v, %#v, %s,
// and %q of a LayerConfig held directly or under exported fields print no
// credential. A LayerConfig under an unexported struct field, and a numeric
// verb, print the raw value, because fmt cannot call the methods there.
type RegisteredRepo string

// Present reports whether the store split a registered value off.
func (r RegisteredRepo) Present() bool { return r != "" }

// String returns a fixed text for a present value and the empty string
// otherwise, so a formatted LayerConfig carries no credential.
func (r RegisteredRepo) String() string {
	if !r.Present() {
		return ""
	}
	return repourl.Redacted
}

// GoString returns what String returns, which covers the %#v verb.
func (r RegisteredRepo) GoString() string { return r.String() }

// CloneRepo returns the remote a clone uses: the registered value when the
// store split one off, and Repo otherwise.
func (c LayerConfig) CloneRepo() string {
	if c.RegisteredRepo.Present() {
		return string(c.RegisteredRepo)
	}
	return c.Repo
}

// ErrRepoCredentialMismatch reports a LayerConfig whose RegisteredRepo does
// not redact to its Repo. The store writes nothing, because it cannot tell
// which of the two the caller meant to store.
var ErrRepoCredentialMismatch = errors.New("store: layer repo and registered repo disagree")

// SplitLayerRepo is the read rule every RegistryStore applies to a layer row.
// It takes the repo column and the repo_userinfo column, and it returns the
// LayerConfig's Repo and RegisteredRepo.
//
// A repo in the split class is returned as its reported form together with
// the registered bytes. Every other value is returned unchanged with an empty
// RegisteredRepo.
//
// Spec: §7.3.1 (Repository credentials)
func SplitLayerRepo(repo string, userinfo sql.NullString) (string, RegisteredRepo) {
	registered := recombineLayerRepo(repo, userinfo)
	parts, ok := repourl.Split(registered)
	if !ok {
		return registered, ""
	}
	return parts.Clean, RegisteredRepo(registered)
}

// recombineLayerRepo returns the registered remote for a row whose userinfo is
// held in its own column, and repo for every other row.
//
// The column is honored only when repo carries no userinfo of its own and the
// recombined value splits back into the same two parts. A column value that
// fails either test is ignored, so a row the column does not describe is read
// from its repo column alone.
func recombineLayerRepo(repo string, userinfo sql.NullString) string {
	if !userinfo.Valid {
		return repo
	}
	if _, split := repourl.Split(repo); split {
		return repo
	}
	joined, ok := repourl.Join(repo, userinfo.String)
	if !ok {
		return repo
	}
	parts, ok := repourl.Split(joined)
	if !ok || parts.Bare != repo || parts.RawUserinfo != userinfo.String {
		return repo
	}
	return joined
}

// registeredLayerRepo returns the registered remote a config carries: its
// RegisteredRepo when present and valid, and its Repo otherwise.
func registeredLayerRepo(cfg LayerConfig) (string, error) {
	if !cfg.RegisteredRepo.Present() {
		return cfg.Repo, nil
	}
	registered := string(cfg.RegisteredRepo)
	parts, ok := repourl.Split(registered)
	if !ok || parts.Clean != cfg.Repo {
		return "", ErrRepoCredentialMismatch
	}
	return registered, nil
}

// LayerRepoColumns is the write rule every SQL RegistryStore applies in
// PutLayerConfig. It returns the values of the repo and repo_userinfo columns.
//
// A registered value in the split class is returned as its bare form and a
// valid userinfo, the empty string included. Every other value is returned
// whole with an invalid NullString. A config whose RegisteredRepo is outside
// the split class or does not redact to its Repo returns
// ErrRepoCredentialMismatch.
//
// Spec: §7.3.1 (Repository credentials)
func LayerRepoColumns(cfg LayerConfig) (string, sql.NullString, error) {
	registered, err := registeredLayerRepo(cfg)
	if err != nil {
		return "", sql.NullString{}, err
	}
	parts, ok := repourl.Split(registered)
	if !ok {
		return registered, sql.NullString{}, nil
	}
	userinfo := sql.NullString{String: parts.RawUserinfo, Valid: true}
	// This branch cannot run for the current classifier: Join(Bare,
	// RawUserinfo) returns the registered bytes for every split-class value.
	// It keeps the registered bytes whole if a classifier change breaks that,
	// so a row is never written in a form that reads back without its
	// credential.
	if recombineLayerRepo(parts.Bare, userinfo) != registered {
		return registered, sql.NullString{}, nil
	}
	return parts.Bare, userinfo, nil
}

// layerRepoFormTarget returns the repo and repo_userinfo columns a stored row
// takes at its next write, and whether they differ from the stored ones. The
// target is the read rule followed by the write rule, so the start-time
// rewrite adds no class of its own and reads back as the stored row reads.
//
// Spec: §13.4, §7.3.1 (Repository credentials)
func layerRepoFormTarget(repo string, userinfo sql.NullString) (string, sql.NullString, bool) {
	clean, registered := SplitLayerRepo(repo, userinfo)
	targetRepo, targetUserinfo, err := LayerRepoColumns(LayerConfig{Repo: clean, RegisteredRepo: registered})
	if err != nil {
		// This branch cannot run: SplitLayerRepo returns a RegisteredRepo only
		// together with its Split(...).Clean as Repo, which is the condition
		// registeredLayerRepo checks. A row it would reach is kept as stored,
		// so the rewrite never writes a form the write rule refused.
		return repo, userinfo, false
	}
	changed := targetRepo != repo ||
		targetUserinfo.Valid != userinfo.Valid ||
		targetUserinfo.String != userinfo.String
	return targetRepo, targetUserinfo, changed
}
