package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// trustFlags are the §7.3.1 trust policy flags `podium layer register` and
// `podium layer update` share. The signer flags build a policy of one
// requirement, and a requirements file sends a policy of several.
type trustFlags struct {
	mode      *string
	layerRoot *string
	signers   stringSliceFlag
	// keys are the files --trust-public-key names, each holding the PEM
	// block of a key-based signer.
	keys stringSliceFlag
	// members maps each binding flag to the signer member it sets.
	members map[string]*string
	// bools maps each boolean signer flag to its member.
	bools map[string]*bool
	// clear sends an empty trust_requirements; update only.
	clear *bool
	// reqFile names a requirements file whose trust_requirements the
	// command sends.
	reqFile *string
	// types are the trust_require_types to send, and clearTypes sends an
	// empty list; update only.
	types      stringSliceFlag
	clearTypes *bool
}

// trustInputError is a trust flag failure that is no usage error, such as a
// requirements file that cannot be read or that the file rules refuse; the
// command exits 1 for it.
type trustInputError struct{ err error }

func (e trustInputError) Error() string { return e.err.Error() }

// trustBodyExit returns the exit status of a trust body failure: 1 for an
// input the command read, 2 for a usage error.
func trustBodyExit(err error) int {
	var in trustInputError
	if errors.As(err, &in) {
		return 1
	}
	return 2
}

// trustSignerStringFlags are the binding flags that set one string member on
// every signer.
var trustSignerStringFlags = [][3]string{
	{"trust-source-repository", "source_repository", "source repository URI each --trust-signer must carry"},
	{"trust-source-owner", "source_repository_owner", "source repository owner URI each --trust-signer must carry"},
	{"trust-source-repository-id", "source_repository_id", "source repository identifier each --trust-signer must carry"},
	{"trust-source-owner-id", "source_repository_owner_id", "source repository owner identifier each --trust-signer must carry"},
	{"trust-source-repository-ref", "source_repository_ref", "source repository ref each --trust-signer must carry, such as refs/heads/main"},
	{"trust-instance", "instance", "Sigstore instance of each --trust-signer (default public-good)"},
}

// trustSignerBoolFlags are the binding flags that set one boolean member on
// every signer.
var trustSignerBoolFlags = [][3]string{
	{"trust-any-source", "any_source", "accept a --trust-signer certificate whatever its source repository"},
	{"trust-any-ref", "any_ref", "accept a --trust-signer certificate whatever its source repository ref"},
}

// addTrustFlags defines the trust policy flags on fs. update adds
// --clear-trust-requirements and --clear-trust-require-types.
//
// Spec: §7.3.1 (Layer CLI)
func addTrustFlags(fs *flag.FlagSet, update bool) *trustFlags {
	f := &trustFlags{
		mode:      fs.String("trust", "", "layer trust mode: none, warn, or require"),
		layerRoot: fs.String("trust-layer-root", "", "root under which a local layer's content was signed"),
		reqFile:   fs.String("trust-requirements-file", "", "send the trust_requirements of a requirements file, such as `podium layer show <id> --requirements` writes"),
		members:   map[string]*string{},
		bools:     map[string]*bool{},
	}
	fs.Var(&f.signers, "trust-signer", "trusted signer as <issuer>=<identity> (repeatable)")
	fs.Var(&f.keys, "trust-public-key", "file holding the PEM public key of a key-based signer, such as cosign.pub (repeatable)")
	fs.Var(&f.types, "trust-require-type", "artifact type that a failed attestation drops under warn (repeatable)")
	for _, d := range trustSignerStringFlags {
		f.members[d[0]] = fs.String(d[0], "", d[2])
	}
	for _, d := range trustSignerBoolFlags {
		f.bools[d[0]] = fs.Bool(d[0], false, d[2])
	}
	if update {
		f.clear = fs.Bool("clear-trust-requirements", false, "remove the layer's trust requirements")
		f.clearTypes = fs.Bool("clear-trust-require-types", false, "remove the layer's trust_require_types")
	}
	return f
}

// body returns the trust policy members of a request body for the flags in
// set. The binding flags apply to every --trust-signer and are refused
// without one, because a requirement with no signer accepts nothing; a
// key-based signer that --trust-public-key adds takes none of them.
// --trust-requirements-file sends the file's requirements and is refused
// beside a signer or binding flag, which would build another policy.
//
// Spec: §7.3.1 (Layer CLI)
func (f *trustFlags) body(set map[string]bool) (map[string]any, error) {
	out := map[string]any{}
	if set["trust"] {
		out["trust_mode"] = *f.mode
	}
	if set["trust-layer-root"] {
		out["trust_layer_root"] = *f.layerRoot
	}
	if err := f.typesMember(set, out); err != nil {
		return nil, err
	}
	var binding []string
	for _, d := range append(append([][3]string{}, trustSignerStringFlags...), trustSignerBoolFlags...) {
		if set[d[0]] {
			binding = append(binding, "--"+d[0])
		}
	}
	if set["trust-requirements-file"] {
		return f.fileMember(out, binding)
	}
	if len(f.signers) == 0 && len(binding) > 0 {
		return nil, fmt.Errorf("%s apply to a --trust-signer; name one", strings.Join(binding, ", "))
	}
	if !f.namesSigners() {
		if f.clear != nil && *f.clear {
			out["trust_requirements"] = []any{}
		}
		return out, nil
	}
	if f.clear != nil && *f.clear {
		return nil, errors.New("--clear-trust-requirements cannot be combined with --trust-signer or --trust-public-key")
	}
	signers := make([]map[string]any, 0, len(f.signers)+len(f.keys))
	for _, raw := range f.signers {
		issuer, identity, ok := strings.Cut(raw, "=")
		if !ok || issuer == "" || identity == "" {
			return nil, fmt.Errorf("--trust-signer %q is not <issuer>=<identity>", raw)
		}
		s := map[string]any{"issuer": issuer, "identity": identity}
		for _, d := range trustSignerStringFlags {
			if set[d[0]] {
				s[d[1]] = *f.members[d[0]]
			}
		}
		for _, d := range trustSignerBoolFlags {
			if set[d[0]] {
				s[d[1]] = *f.bools[d[0]]
			}
		}
		signers = append(signers, s)
	}
	keys, err := f.keySigners()
	if err != nil {
		return nil, err
	}
	out["trust_requirements"] = []any{map[string]any{"signers": append(signers, keys...)}}
	return out, nil
}

// namesSigners reports whether the flags name a signer of the requirement
// they build: a --trust-signer or a --trust-public-key.
func (f *trustFlags) namesSigners() bool {
	return len(f.signers) > 0 || len(f.keys) > 0
}

// keySigners reads each --trust-public-key file and returns a key-based
// signer with its text as public_key. The text is sent as it is, and the
// registry applies the §4.7.11 key rules to it. A file the command cannot
// read is a trustInputError.
//
// Spec: §7.3.1 (Layer CLI), §4.7.11 (Key-based signers)
func (f *trustFlags) keySigners() ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(f.keys))
	for _, file := range f.keys {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, trustInputError{fmt.Errorf("read the --trust-public-key file: %w", err)}
		}
		out = append(out, map[string]any{"public_key": string(raw)})
	}
	return out, nil
}

// typesMember sets trust_require_types from --trust-require-type, or the
// empty list from --clear-trust-require-types, which cannot be combined.
func (f *trustFlags) typesMember(set map[string]bool, out map[string]any) error {
	clearTypes := f.clearTypes != nil && *f.clearTypes
	switch {
	case set["trust-require-type"] && clearTypes:
		return errors.New("--clear-trust-require-types cannot be combined with --trust-require-type")
	case set["trust-require-type"]:
		out["trust_require_types"] = []string(f.types)
	case clearTypes:
		out["trust_require_types"] = []string{}
	}
	return nil
}

// fileMember sets trust_requirements from the requirements file
// --trust-requirements-file names. The file's layer_roots is not read. A
// file the command cannot read or that the file rules refuse is a
// trustInputError.
//
// Spec: §7.3.1 (Layer CLI), §4.7.11 (Requirements file)
func (f *trustFlags) fileMember(out map[string]any, binding []string) (map[string]any, error) {
	switch {
	case f.namesSigners() || len(binding) > 0:
		return nil, errors.New("--trust-requirements-file cannot be combined with --trust-signer, --trust-public-key, or a binding flag, which build another policy")
	case f.clear != nil && *f.clear:
		return nil, errors.New("--clear-trust-requirements cannot be combined with --trust-requirements-file")
	}
	raw, err := os.ReadFile(*f.reqFile)
	if err != nil {
		return nil, trustInputError{fmt.Errorf("read the requirements file: %w", err)}
	}
	file, err := layer.ParseRequirementsFile(raw)
	if err != nil {
		return nil, trustInputError{fmt.Errorf("%s: %w", *f.reqFile, err)}
	}
	out["trust_requirements"] = file.TrustRequirements
	return out, nil
}

// checkSingleRequirement reads the layer through GET /v1/layers before an
// update sends a requirement built from the signer flags, and refuses the
// update when the layer stores more than one requirement, because the
// replacement would drop the additional requirements, or when the read
// fails or does not return the layer. A layer with several requirements is
// updated with --trust-requirements-file. It returns the requirements it
// read, which the update sends as expected_trust_requirements so a
// requirement another writer adds between the read and the update makes the
// registry refuse it; nil when the flags send no requirement.
//
// Spec: §7.3.1 (Layer CLI, Patch semantics on the update body)
func (f *trustFlags) checkSingleRequirement(registry, id string) (json.RawMessage, error) {
	if !f.namesSigners() {
		return nil, nil
	}
	out, status := doJSON(registry+"/v1/layers", "GET", nil)
	if status >= 400 {
		return nil, fmt.Errorf("read layer %s before replacing its requirements: HTTP %d", id, status)
	}
	raw, err := findLayerObject(out, id)
	if err != nil {
		return nil, fmt.Errorf("read layer %s before replacing its requirements: %w", id, err)
	}
	var l struct {
		TrustRequirements []json.RawMessage `json:"trust_requirements"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("decode the trust_requirements of layer %s: %w", id, err)
	}
	if n := len(l.TrustRequirements); n > 1 {
		return nil, fmt.Errorf("layer %s stores %d trust requirements, and --trust-signer or --trust-public-key would replace them with one; "+
			"edit the file `podium layer show %s --requirements` writes and send it with --trust-requirements-file", id, n, id)
	}
	expected, err := json.Marshal(l.TrustRequirements)
	if err != nil {
		return nil, fmt.Errorf("encode the trust_requirements of layer %s: %w", id, err)
	}
	return expected, nil
}

// layerShow prints the layer object with the given ID from the layer read,
// as indented JSON, so an operator sees the stored trust policy with the
// members the registry derived.
//
// Spec: §7.3.1 (Layer CLI)
func layerShow(args []string) int {
	fs := flag.NewFlagSet("layer show", flag.ContinueOnError)
	setUsage(fs, "Print one registered layer, with its trust policy.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	requirements := fs.Bool("requirements", false, "write the layer's trust_requirements as a requirements file for podium attest check")
	fs.SetOutput(os.Stderr)
	id, nargs, err := parsePositional(fs, args)
	if err != nil {
		return parseExit(err)
	}
	if nargs != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium layer show <id> [--requirements]")
		return 2
	}
	*registry = resolveLayerRegistry(*registry)
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/layers", "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "show failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	write := writeLayerObject
	if *requirements {
		write = writeLayerRequirements
	}
	if err := write(os.Stdout, out, id); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !*requirements {
		// The layer object holds the layer, so the read above found it.
		raw, _ := findLayerObject(out, id)
		writeAttestationNotes(os.Stderr, raw)
	}
	return 0
}

// writeLayerObject writes the layer object with ID id from a layer list body
// to w as indented JSON, in the member order the registry sent.
func writeLayerObject(w io.Writer, body []byte, id string) error {
	raw, err := findLayerObject(body, id)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	// raw decoded in findLayerObject, so it is valid JSON and Indent cannot
	// fail on it; the guard keeps an impossible failure visible.
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("format layer %s: %w", id, err)
	}
	buf.WriteByte('\n')
	_, err = w.Write(escapeControls(buf.Bytes()))
	return err
}

// writeLayerRequirements writes the trust_requirements of the layer with ID
// id from a layer list body to w as a §4.7.11 requirements file, which
// `podium attest check --requirements` reads. The requirements carry the
// members the registry derived, so the check applies the bindings the
// registry applies. A layer with no requirement is an error.
//
// Spec: §4.7.11 (Requirements file), §7.3.1
func writeLayerRequirements(w io.Writer, body []byte, id string) error {
	raw, err := findLayerObject(body, id)
	if err != nil {
		return err
	}
	var l struct {
		TrustRequirements []store.TrustRequirement `json:"trust_requirements"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return fmt.Errorf("decode the trust_requirements of layer %s: %w", id, err)
	}
	if len(l.TrustRequirements) == 0 {
		return fmt.Errorf("layer %s has no trust requirement", id)
	}
	b, err := layer.MarshalRequirementsFile(l.TrustRequirements)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// findLayerObject returns the layer object with ID id from a layer list
// body.
func findLayerObject(body []byte, id string) (json.RawMessage, error) {
	var list struct {
		Layers []json.RawMessage `json:"layers"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode the layer list: %w", err)
	}
	for _, raw := range list.Layers {
		var head struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &head) == nil && head.ID == id {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("no layer %s in the layers this caller can read", id)
}
