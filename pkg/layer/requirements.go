package layer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/lennylabs/podium/pkg/store"
)

// RequirementsFile is the §4.7.11 requirements file: a layer trust policy's
// requirements apart from any layer, which `podium layer show <id>
// --requirements` writes and `podium attest check --requirements` reads.
//
// Spec: §4.7.11 (Requirements file)
type RequirementsFile struct {
	TrustRequirements []store.TrustRequirement `json:"trust_requirements"`
	// LayerRoots maps a layer ID to its trust_layer_root. It is kept as
	// written: `podium attest check` does not read it, so a value it would
	// not use refuses nothing there.
	LayerRoots json.RawMessage `json:"layer_roots,omitempty"`
}

// ParseRequirementsFile decodes raw as a requirements file. It refuses a
// value that is not one JSON object, an object member of another name at any
// level, and a file with no trust_requirements array, so a misspelled
// member is an error rather than a requirement that binds less. It applies
// no policy rule; CheckRequirements does.
//
// Spec: §4.7.11 (Requirements file)
func ParseRequirementsFile(raw []byte) (RequirementsFile, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f RequirementsFile
	if err := dec.Decode(&f); err != nil {
		return RequirementsFile{}, fmt.Errorf("%w: requirements file: %v", ErrTrustPolicy, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return RequirementsFile{}, fmt.Errorf("%w: requirements file: data after the object", ErrTrustPolicy)
	}
	if f.TrustRequirements == nil {
		return RequirementsFile{}, fmt.Errorf("%w: requirements file: no trust_requirements array", ErrTrustPolicy)
	}
	return f, nil
}

// CheckRequirements applies the §4.7.11 policy rules that read a
// requirement or a signer to reqs: at least one and at most
// MaxTrustRequirements requirements, a signer in each, and the signer rules
// with each instance known to instances. It returns nil or an error wrapping
// ErrTrustPolicy.
//
// Spec: §4.7.11 (Policy rules)
func CheckRequirements(reqs []store.TrustRequirement, instances TrustInstances) error {
	switch n := len(reqs); {
	case n == 0:
		return trustRefusal("trust_requirements holds no requirement")
	case n > MaxTrustRequirements:
		return trustRefusal("trust_requirements holds %d requirements; several requirements are not available yet, so a policy holds at most %d", n, MaxTrustRequirements)
	}
	for i, req := range reqs {
		if err := checkRequirement(fmt.Sprintf("trust_requirements[%d]", i), req, instances); err != nil {
			return err
		}
	}
	return nil
}

// MarshalRequirementsFile returns the requirements file that holds reqs, as
// indented JSON ending in a line feed, without layer_roots.
//
// Spec: §4.7.11 (Requirements file), §7.3.1
func MarshalRequirementsFile(reqs []store.TrustRequirement) ([]byte, error) {
	b, err := json.MarshalIndent(RequirementsFile{TrustRequirements: reqs}, "", "  ")
	// The file holds strings and booleans alone, which always encode; the
	// guard keeps a later member that can fail visible.
	if err != nil {
		return nil, fmt.Errorf("layer: encode the requirements file: %w", err)
	}
	return append(b, '\n'), nil
}
