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
// `podium layer update` share. The signer flags build the one requirement of
// the policy.
type trustFlags struct {
	mode      *string
	layerRoot *string
	signers   stringSliceFlag
	// members maps each binding flag to the signer member it sets.
	members map[string]*string
	// bools maps each boolean signer flag to its member.
	bools map[string]*bool
	// clear sends an empty trust_requirements; update only.
	clear *bool
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
// --clear-trust-requirements.
//
// Spec: §7.3.1 (Layer CLI)
func addTrustFlags(fs *flag.FlagSet, update bool) *trustFlags {
	f := &trustFlags{
		mode:      fs.String("trust", "", "layer trust mode: none, warn, or require"),
		layerRoot: fs.String("trust-layer-root", "", "root under which a local layer's content was signed"),
		members:   map[string]*string{},
		bools:     map[string]*bool{},
	}
	fs.Var(&f.signers, "trust-signer", "trusted signer as <issuer>=<identity> (repeatable)")
	for _, d := range trustSignerStringFlags {
		f.members[d[0]] = fs.String(d[0], "", d[2])
	}
	for _, d := range trustSignerBoolFlags {
		f.bools[d[0]] = fs.Bool(d[0], false, d[2])
	}
	if update {
		f.clear = fs.Bool("clear-trust-requirements", false, "remove the layer's trust requirement")
	}
	return f
}

// body returns the trust policy members of a request body for the flags in
// set. The binding flags apply to every --trust-signer and are refused
// without one, because a requirement with no signer accepts nothing.
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
	var binding []string
	for _, d := range append(append([][3]string{}, trustSignerStringFlags...), trustSignerBoolFlags...) {
		if set[d[0]] {
			binding = append(binding, "--"+d[0])
		}
	}
	if len(f.signers) == 0 {
		if len(binding) > 0 {
			return nil, fmt.Errorf("%s apply to a --trust-signer; name one", strings.Join(binding, ", "))
		}
		if f.clear != nil && *f.clear {
			out["trust_requirements"] = []any{}
		}
		return out, nil
	}
	if f.clear != nil && *f.clear {
		return nil, errors.New("--clear-trust-requirements cannot be combined with --trust-signer")
	}
	signers := make([]map[string]any, 0, len(f.signers))
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
	out["trust_requirements"] = []any{map[string]any{"signers": signers}}
	return out, nil
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
	_, err = w.Write(buf.Bytes())
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
