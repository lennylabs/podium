package ghactions

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/lennylabs/podium/pkg/attest"
)

// The actions the workflow runs, each pinned to the commit its release tag
// named when this release of Podium was built, so a moved tag changes
// nothing that signs. The comment names the tag for a reader and for a
// dependency update tool.
const (
	checkoutAction = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
	uploadAction   = "actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2"
	downloadAction = "actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2"
	attestAction   = "actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2"
)

// MinInstallVersion is the earliest podium release whose binaries carry a
// build provenance attestation, which the workflow's install step verifies.
// A workflow written by an older version, such as a source build of main
// before that release, installs this release, because an older one cannot
// pass the verification.
const MinInstallVersion = "v0.8.0"

// installVersion returns version, or MinInstallVersion when version orders
// before it by its numeric major, minor, and patch parts. A pre-release of
// MinInstallVersion orders before it.
func installVersion(version string) string {
	if versionLess(version, MinInstallVersion) {
		return MinInstallVersion
	}
	return version
}

// versionLess compares two vMAJOR.MINOR.PATCH[-suffix] strings numerically;
// a version with a suffix orders before the same version without one. A part
// that does not parse counts as 0.
func versionLess(a, b string) bool {
	pa, sa := versionParts(a)
	pb, sb := versionParts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return sa && !sb
}

// versionParts returns the numeric parts of v and whether it has a suffix.
func versionParts(v string) ([3]int, bool) {
	core, _, suffixed := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	core, _, _ = strings.Cut(core, "+")
	var out [3]int
	for i, p := range strings.SplitN(core, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out, suffixed
}

// SharedRef is the ref a caller workflow calls the shared workflow at. The
// signer identity of a shared workflow ends in refs/heads/ and this ref.
const SharedRef = "main"

// Kind selects which workflow Render writes.
type Kind string

const (
	// KindRepository signs the layers of the repository that holds it.
	KindRepository Kind = "repository"
	// KindCaller calls a shared workflow in another repository.
	KindCaller Kind = "caller"
	// KindReusable is the shared workflow a caller calls.
	KindReusable Kind = "reusable"
)

// Workflow describes one podium-attest.yml.
type Workflow struct {
	Kind Kind
	// Version is the podium release the workflow installs, with or without
	// its leading "v".
	Version string
	// Branch is the default branch the workflow triggers on and signs.
	// KindReusable reads it from the calling repository instead.
	Branch string
	// Roots are the layer roots, "" for the top of the repository. No root
	// is the top alone. KindReusable takes them from its caller.
	Roots []string
	// Shared is the "<owner>/<repo>" of the shared workflow a KindCaller
	// workflow calls.
	Shared string
}

//go:embed workflow.yml.tmpl
var workflowTemplate string

var workflowTmpl = template.Must(template.New("podium-attest").Delims("[[", "]]").Parse(workflowTemplate))

// ErrWorkflowValue reports a value the workflow cannot carry.
var ErrWorkflowValue = errors.New("ghactions: workflow value")

// safeName is the form of a branch, a root segment, and a version that the
// workflow writes into YAML and shell without quoting. It leaves out every
// character that YAML, a shell, a branch filter pattern, or a GitHub
// expression gives a meaning, such as $, {, *, and quotes.
var safeName = regexp.MustCompile(`\A[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*\z`)

// CheckRoot refuses a layer root that the workflow cannot carry: a root
// that `podium attest` refuses, and a root with a character other than an
// ASCII letter, a digit, ".", "_", "-", and "/". It returns the root without
// its leading and trailing "/".
//
// Spec: §7.3.5 (Writing the signing workflow)
func CheckRoot(root string) (string, error) {
	trimmed, err := attest.CheckLayerRoot(root)
	if err != nil {
		return "", err
	}
	if trimmed != "" && !safeName.MatchString(trimmed) {
		return "", fmt.Errorf("%w: the root %q holds a character other than an ASCII letter, a digit, \".\", \"_\", \"-\", and \"/\"", ErrWorkflowValue, root)
	}
	return trimmed, nil
}

// Render writes the workflow. It refuses a branch, a root, a version, or a
// shared repository that the workflow cannot carry.
//
// Spec: §7.3.5 (Writing the signing workflow)
func Render(w Workflow) ([]byte, error) {
	data, err := w.templateData()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := workflowTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("ghactions: render the workflow: %w", err)
	}
	return buf.Bytes(), nil
}

// templateData validates w and returns the values the template reads.
func (w Workflow) templateData() (map[string]any, error) {
	version := "v" + strings.TrimPrefix(w.Version, "v")
	if !safeName.MatchString(version) || strings.Contains(version, "/") {
		return nil, fmt.Errorf("%w: the podium version %q", ErrWorkflowValue, w.Version)
	}
	version = installVersion(version)
	data := map[string]any{
		"Kind":         string(w.Kind),
		"Version":      version,
		"SignedRef":    SignedRef,
		"WorkflowPath": WorkflowPath,
		"SharedRef":    SharedRef,
		"Checkout":     checkoutAction,
		"Upload":       uploadAction,
		"Download":     downloadAction,
		"Attest":       attestAction,
	}
	switch w.Kind {
	case KindReusable:
		return data, nil
	case KindRepository, KindCaller:
	default:
		return nil, fmt.Errorf("%w: kind %q", ErrWorkflowValue, w.Kind)
	}
	if !safeName.MatchString(w.Branch) {
		return nil, fmt.Errorf("%w: the default branch %q holds a character other than an ASCII letter, a digit, \".\", \"_\", \"-\", and \"/\"", ErrWorkflowValue, w.Branch)
	}
	roots, ignore, err := rootValues(w.Roots)
	if err != nil {
		return nil, err
	}
	data["Branch"], data["BranchList"] = w.Branch, "["+w.Branch+"]"
	data["Roots"], data["PathsIgnore"] = roots, ignore
	if w.Kind == KindCaller {
		shared, err := ParseShared(w.Shared)
		if err != nil {
			return nil, err
		}
		data["Shared"] = shared.FullName()
	}
	return data, nil
}

// rootValues returns the PODIUM_ROOTS value, where "/" names the top, and
// the attestation directory pattern of each root for paths-ignore.
func rootValues(roots []string) (string, []string, error) {
	if len(roots) == 0 {
		roots = []string{""}
	}
	list := make([]string, 0, len(roots))
	ignore := make([]string, 0, len(roots))
	for _, r := range roots {
		trimmed, err := CheckRoot(r)
		if err != nil {
			return "", nil, err
		}
		if trimmed == "" {
			list = append(list, "/")
			ignore = append(ignore, ".podium/attestations/**")
			continue
		}
		list = append(list, trimmed)
		ignore = append(ignore, trimmed+"/.podium/attestations/**")
	}
	return strings.Join(list, " "), ignore, nil
}

// ParseShared reads the "<owner>/<repo>" of a shared workflow's repository.
//
// Spec: §7.3.5 (Writing the signing workflow)
func ParseShared(v string) (Repo, error) {
	owner, name, found := strings.Cut(v, "/")
	if !found || !validSegment(owner) || !validSegment(name) {
		return Repo{}, fmt.Errorf("%w: the shared workflow repository %q is not <owner>/<repo>", ErrWorkflowValue, v)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// SharedIdentity returns the certificate identity of the shared workflow in
// repo, called at SharedRef.
func SharedIdentity(repo Repo) string {
	return "https://" + Host + "/" + repo.FullName() + "/" + WorkflowPath + "@refs/heads/" + SharedRef
}
