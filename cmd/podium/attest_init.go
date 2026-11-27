package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/attest/ghactions"
	"github.com/lennylabs/podium/pkg/layer/source"
)

// attestInitOptions are the parsed flags and operand of `podium attest
// init`.
type attestInitOptions struct {
	dir      string
	roots    []string
	shared   ghactions.Repo
	reusable bool
	force    bool
	apiBase  string
}

// attestInitEnv is what `podium attest init` reads besides its arguments.
type attestInitEnv struct {
	getenv    func(string) string
	apiBase   string
	transport http.RoundTripper
	stdout    io.Writer
	stderr    io.Writer
}

// runAttestInit writes the signing workflow of §7.3.5 into the repository at
// <dir> and prints the command that registers each layer. It exits 2 on a
// usage error and 1 on a refusal or a failed write, and writes no file in
// either case.
//
//	podium attest init [<dir>] [--root <subpath>]... [--shared-workflow <owner>/<repo>] [--reusable] [--force]
//
// Spec: §7.3.5 (Writing the signing workflow)
func runAttestInit(ctx context.Context, args []string, env attestInitEnv) int {
	opts, code := parseAttestInitArgs(args, env)
	if code >= 0 {
		return code
	}
	origin, err := source.ReadOrigin(opts.dir)
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	repo, err := ghactions.ParseRepoURL(origin.URL)
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: the origin remote %q is not a repository on github.com, and podium attest init writes a GitHub Actions workflow; no file was written. In another CI system, sign the statements that `podium attest --format statement` writes with a keyless signing tool, as Other environments in the GitHub Actions signing guide describes.\n", origin.URL)
		return 1
	}
	// The file is checked before the request, so a refused run sends none;
	// the write checks it again.
	if _, err := workflowFileState(opts.dir, opts.force); err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	w := ghactions.Workflow{Kind: ghactions.KindReusable, Version: buildinfo.Version}
	var rec ghactions.Record
	var recErr error
	if !opts.reusable {
		timeout, _ := ghactions.TimeoutFromEnv(env.getenv) // validated in parseAttestInitArgs
		f := ghactions.Fetcher{Base: opts.apiBase, Token: ghactions.TokenFromEnv(env.getenv), Timeout: timeout, Transport: env.transport}
		rec, recErr = f.Fetch(ctx, repo)
		branch, err := defaultBranch(origin, rec, recErr, env.stderr)
		if err != nil {
			_, _ = fmt.Fprintf(env.stderr, "error: %v\n", err)
			return 1
		}
		w = ghactions.Workflow{Kind: ghactions.KindRepository, Version: buildinfo.Version, Branch: branch, Roots: opts.roots}
		if opts.shared != (ghactions.Repo{}) {
			w.Kind, w.Shared = ghactions.KindCaller, opts.shared.FullName()
		}
	}
	content, err := ghactions.Render(w)
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	path, err := writeWorkflowFile(opts.dir, content, opts.force)
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "error: %s\n", oneLine(err.Error()))
		return 1
	}
	_, _ = fmt.Fprintf(env.stdout, "Wrote %s\n", path)
	if opts.reusable {
		_, _ = fmt.Fprintf(env.stdout, "Commit the file and push it to the main branch of %s. Each layer repository then calls it with the workflow that this command writes there:\n  podium attest init --shared-workflow %s\n", repo.FullName(), ghactions.ShellQuote(repo.FullName()))
		return 0
	}
	writeRegistration(env.stdout, repo, rec, recErr, w.Branch, opts)
	return 0
}

// parseAttestInitArgs parses args. It returns code -1 with the options when
// the command proceeds, and the exit code otherwise, having written the
// reason.
func parseAttestInitArgs(args []string, env attestInitEnv) (attestInitOptions, int) {
	flags := flag.NewFlagSet("attest init", flag.ContinueOnError)
	setUsage(flags, "Write the GitHub Actions workflow that signs the layers of this repository.")
	flags.SetOutput(env.stderr)
	var roots stringSliceFlag
	flags.Var(&roots, "root", "a layer root below <dir> (repeatable; default the top of the repository)")
	shared := flags.String("shared-workflow", "", "write a workflow that calls podium-attest.yml in this <owner>/<repo>")
	reusable := flags.Bool("reusable", false, "write the shared workflow that layer repositories call")
	force := flags.Bool("force", false, "replace an existing .github/workflows/podium-attest.yml")
	testAPI := addGitHubAPITestFlag(flags)
	ops, err := parseOperands(flags, args)
	if err != nil {
		return attestInitOptions{}, parseExit(err)
	}
	opts := attestInitOptions{dir: ".", reusable: *reusable, force: *force, apiBase: githubAPIBase(env.apiBase, testAPI)}
	usage := func(format string, a ...any) (attestInitOptions, int) {
		_, _ = fmt.Fprintf(env.stderr, "error: "+format+"\n", a...)
		return opts, 2
	}
	if len(ops) > 1 {
		return usage("podium attest init takes at most one <dir>, got %d", len(ops))
	}
	if len(ops) == 1 {
		opts.dir = ops[0]
	}
	if opts.reusable && (len(roots) > 0 || *shared != "") {
		return usage("--reusable writes the shared workflow, which takes its roots from each caller; it takes no --root and no --shared-workflow")
	}
	seen := map[string]bool{}
	for _, r := range roots {
		trimmed, err := ghactions.CheckRoot(r)
		if err != nil {
			return usage("--root: %s", oneLine(err.Error()))
		}
		if seen[trimmed] {
			return usage("--root %q is named twice", r)
		}
		seen[trimmed] = true
		opts.roots = append(opts.roots, trimmed)
	}
	if *shared != "" {
		if opts.shared, err = ghactions.ParseShared(*shared); err != nil {
			return usage("--shared-workflow: %s", oneLine(err.Error()))
		}
	}
	if _, err := ghactions.TimeoutFromEnv(env.getenv); err != nil {
		return usage("%v", err)
	}
	return opts, -1
}

// defaultBranch returns the repository's default branch: the record's when
// the API answered, because a clone's record of the remote's default branch
// goes stale when the branch is renamed, and refs/remotes/origin/HEAD
// otherwise.
func defaultBranch(origin source.Origin, rec ghactions.Record, recErr error, stderr io.Writer) (string, error) {
	if recErr == nil {
		if origin.DefaultBranch != "" && origin.DefaultBranch != rec.DefaultBranch {
			_, _ = fmt.Fprintf(stderr, "warning: the GitHub API names %s as the default branch, and refs/remotes/origin/HEAD names %s; the workflow uses %s\n",
				rec.DefaultBranch, origin.DefaultBranch, rec.DefaultBranch)
		}
		return rec.DefaultBranch, nil
	}
	if origin.DefaultBranch == "" {
		return "", fmt.Errorf("the default branch of the origin remote is unknown: the repository holds no refs/remotes/origin/HEAD, and the GitHub API did not answer (%s); run `git remote set-head origin --auto` and run the command again", oneLine(recErr.Error()))
	}
	return origin.DefaultBranch, nil
}

// writeWorkflowFile writes content to .github/workflows/podium-attest.yml
// under dir and returns the path it wrote. It refuses a symbolic link or an
// entry other than a directory at .github or .github/workflows, and an
// existing entry at the file's path unless force is set, when it replaces a
// regular file alone.
func writeWorkflowFile(dir string, content []byte, force bool) (string, error) {
	path := filepath.Join(dir, filepath.FromSlash(ghactions.WorkflowPath))
	flags, err := workflowFileState(dir, force)
	if err != nil {
		return "", err
	}
	parent := dir
	for _, seg := range []string{".github", "workflows"} {
		parent = filepath.Join(parent, seg)
		// workflowFileState refused a parent that is no directory, so an
		// existing entry here is one.
		if err := os.Mkdir(parent, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create %s: %w", parent, err)
		}
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// workflowFileState returns the open flags that write the workflow file
// under dir: a new file, or with force the truncation of an existing
// regular file. It refuses a symbolic link or an entry other than a
// directory at .github or .github/workflows, an existing entry at the file
// without force, and a symbolic link or an entry other than a regular file
// with it.
func workflowFileState(dir string, force bool) (int, error) {
	parent := dir
	for _, seg := range []string{".github", "workflows"} {
		parent = filepath.Join(parent, seg)
		info, err := os.Lstat(parent)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return os.O_WRONLY | os.O_CREATE | os.O_EXCL, nil
		case err != nil:
			return 0, fmt.Errorf("read %s: %w", parent, err)
		case !info.IsDir():
			return 0, fmt.Errorf("%s is a symbolic link or no directory", parent)
		}
	}
	path := filepath.Join(dir, filepath.FromSlash(ghactions.WorkflowPath))
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return os.O_WRONLY | os.O_CREATE | os.O_EXCL, nil
	case err != nil:
		return 0, fmt.Errorf("read %s: %w", path, err)
	case !force:
		return 0, fmt.Errorf("%s exists; --force replaces it", path)
	case !info.Mode().IsRegular():
		return 0, fmt.Errorf("%s is a symbolic link or no regular file, which --force does not replace", path)
	}
	return os.O_WRONLY | os.O_TRUNC, nil
}

// writeRegistration prints the step after the file is written: the command
// that registers each layer, with the preset for a public repository and
// with the explicit flags for a private or internal one.
func writeRegistration(w io.Writer, repo ghactions.Repo, rec ghactions.Record, recErr error, branch string, opts attestInitOptions) {
	signer := ""
	if opts.shared != (ghactions.Repo{}) {
		signer = ghactions.Issuer + "=" + ghactions.SharedIdentity(opts.shared)
	}
	name := repo
	if recErr == nil && rec.FullName != repo.FullName() {
		name = rec.Repo()
		_, _ = fmt.Fprintf(w, "The origin remote names %s, and the GitHub API names the repository %s; the commands below use the current name.\n", repo.FullName(), rec.FullName)
	}
	trust := "--trust-preset " + ghactions.PresetName
	if signer != "" {
		trust += " --trust-signer " + ghactions.ShellQuote(signer)
	}
	switch {
	case recErr != nil:
		_, _ = fmt.Fprintf(w, "Could not read the repository's visibility from the GitHub API (%s). The preset below covers a public repository.\n", oneLine(recErr.Error()))
	case !rec.Public():
		_, _ = fmt.Fprintf(w, "The GitHub API reports %s as %s. Signing a private or internal repository needs GitHub Enterprise Cloud, which signs with GitHub's own Sigstore instance. The registry operator configures that instance as github in registry.yaml, with a trusted root file that keeps the line of GitHub's instance:\n  gh attestation trusted-root | grep fulcio.githubapp.com > github-trusted-root.json\n",
			rec.FullName, visibilityName(rec))
		trust = ghactions.ExplicitFlags(name, rec, signer, ghactions.PrivateInstance)
	}
	roots := opts.roots
	if len(roots) == 0 {
		roots = []string{""}
	}
	if len(roots) == 1 {
		_, _ = fmt.Fprintf(w, "Commit the file and push it to %s. Then register the layer:\n", branch)
	} else {
		_, _ = fmt.Fprintf(w, "Commit the file and push it to %s. Then register each layer:\n", branch)
	}
	for _, r := range roots {
		cmd := "podium layer register --id <id> --repo " + ghactions.ShellQuote(name.URL())
		if r != "" {
			cmd += " --root " + ghactions.ShellQuote(r)
		}
		_, _ = fmt.Fprintf(w, "  %s %s\n", cmd, strings.ReplaceAll(trust, "\n", "\n  "))
	}
}
