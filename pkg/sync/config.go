package sync

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/version"
)

// Errors related to config files.
var (
	// ErrConfigNotFound signals that no sync.yaml was found in any
	// configured scope. Maps to config.no_registry in §6.10 when it
	// surfaces during a sync.
	ErrConfigNotFound = errors.New("config.not_found")
)

// SyncConfig is the in-memory representation of `.podium/sync.yaml`
// (spec §7.5.2). Fields not in the schema are preserved verbatim
// through Read/Write so handcrafted comments and scalar formatting
// stay intact across edits.
type SyncConfig struct {
	Defaults Defaults `yaml:"defaults,omitempty"`
	// ListingBudgets is the `listing_budgets:` block (§7.5.2, §7.5.8), held
	// undecoded. Readers that decode the whole file (the MCP server, the
	// bridge, the §4.7.9 policy resolution, `podium config show`) must keep
	// working when the block holds a value of the wrong YAML type, and
	// `podium sync save-as` must write the block back as it read it, so only
	// LoadMergedConfig type-checks and decodes it through listingBudgets. A
	// Kind of 0 means the file has no `listing_budgets` key.
	ListingBudgets yaml.Node          `yaml:"listing_budgets,omitempty"`
	Profiles       map[string]Profile `yaml:"profiles,omitempty"`
	Targets        []TargetEntry      `yaml:"targets,omitempty"`
}

// Defaults is the `defaults:` block.
type Defaults struct {
	Registry string `yaml:"registry,omitempty"`
	Harness  string `yaml:"harness,omitempty"`
	Target   string `yaml:"target,omitempty"`
	Profile  string `yaml:"profile,omitempty"`
	// Identity is the §4.6 effective-view principal a `kind: marketplace`
	// target inherits when it declares none of its own (§7.5.2). It is the
	// publishing identity whose visibility defines what reaches a public
	// marketplace; a `kind: workspace` target renders under the caller's own
	// identity and ignores it.
	Identity string `yaml:"identity,omitempty"`
	// VerifySignatures is the consumer-side §4.7.9 signature-verification
	// policy (never | always). It is operator-set and resolves across the
	// §7.5.2 scopes like every other defaults key; PODIUM_VERIFY_SIGNATURES
	// overrides it, and the §6.2 default when no scope sets it is always.
	VerifySignatures string `yaml:"verify_signatures,omitempty"`
	// MinServerVersion pins the minimum MCP server / CLI binary version this
	// configuration requires (§6.7 "Versioning": a profile or harness
	// combination that needs a newer adapter behavior pins a minimum, and an
	// older binary refuses to start). A profile pin overrides this default-wide
	// pin when it is higher.
	MinServerVersion string `yaml:"min_server_version,omitempty"`
}

// Profile is one entry under `profiles:`. Names without explicit
// values are normalized to empty slices on read.
type Profile struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`
	Target  string   `yaml:"target,omitempty"`
	Harness string   `yaml:"harness,omitempty"`
	// MinServerVersion pins the minimum binary version this profile requires
	// (§6.7 "Versioning"). A binary below this refuses to start when the
	// profile is active.
	MinServerVersion string `yaml:"min_server_version,omitempty"`
}

// TargetEntry is one entry under `targets:` (multi-target mode). Kind selects
// the output format (§7.5.2): "workspace" (the default, an empty value)
// materializes the project-files layout the harness reads, and "marketplace"
// renders the git-repo distribution layout (§7.8). A `kind: workspace` entry
// carries the harness, the target directory, and the scope fields (Profile,
// Include, Exclude, Type). A `kind: marketplace` entry carries the harness set
// (Harnesses), the git remote and branch (Git), the commit message, the plugin
// list, and the publishing identity. Both kinds may carry a Workflow.
type TargetEntry struct {
	ID      string   `yaml:"id"`
	Kind    string   `yaml:"kind,omitempty"`
	Harness string   `yaml:"harness,omitempty"`
	Target  string   `yaml:"target,omitempty"`
	Profile string   `yaml:"profile,omitempty"`
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	Type    []string `yaml:"type,omitempty"`

	// Marketplace fields (valid only on a `kind: marketplace` entry, §7.5.2).
	Harnesses     []string       `yaml:"harnesses,omitempty"`
	Git           GitRemote      `yaml:"git,omitempty"`
	CommitMessage string         `yaml:"commit_message,omitempty"`
	Identity      string         `yaml:"identity,omitempty"`
	Plugins       []PluginFilter `yaml:"plugins,omitempty"`

	// Workflow is the per-target prepare/publish command lists, available to
	// either kind (§7.5.2).
	Workflow Workflow `yaml:"workflow,omitempty"`
}

// ListingBudget is one decoded `listing_budgets` entry (§7.5.2), keyed in the
// file by adapter value, with the yaml keys max_chars, entry_overhead_chars,
// max_entry_chars, types, and check. A nil field is unset. A non-nil empty
// Types (`types: []`) and a non-nil pointer to "" (`check: ""`) are set and
// invalid, so validation can tell them from an absent key. It is never
// encoded: SyncConfig carries the block as an undecoded node.
type ListingBudget struct {
	MaxChars           *int
	EntryOverheadChars *int
	MaxEntryChars      *int
	Types              []string
	Check              *string
}

// listingBudgets type-checks and decodes the `listing_budgets` block. It
// returns nil for a file without the block or with a null block.
//
// yaml.v3 coerces a scalar into the Go kind it decodes to, loading
// `max_chars: 1.5` as 1 and `check: 1` as "1" without an error, and it
// decodes a `!!binary` scalar into a Go string, so neither a typed decode nor
// a decode into interface values enforces the §7.5.2 rule that each value's
// resolved YAML tag matches its key. The block is therefore decoded into
// yamlNode values: the decoder still follows aliases and applies `<<` merges,
// and each value keeps the node it resolved to, so its tag is checked
// directly. A key outside the schema is ignored, as an unknown key is anywhere
// in SyncConfig. The error names the adapter and the key; LoadMergedConfig
// wraps it with the file path and listing_budgets.
//
// Spec: §7.5.2
func (c *SyncConfig) listingBudgets() (map[string]ListingBudget, error) {
	if c.ListingBudgets.Kind == 0 {
		return nil, nil
	}
	var block yamlNode
	if err := c.ListingBudgets.Decode(&block); err != nil {
		return nil, err
	}
	if block.n == nil {
		return nil, nil
	}
	if block.n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("want a mapping keyed by adapter name, got %s", yamlTypeName(block.n))
	}
	var entries map[adapterKey]yamlNode
	if err := block.n.Decode(&entries); err != nil {
		return nil, err
	}
	out := make(map[string]ListingBudget, len(entries))
	// Sorted so a file with several mistyped entries reports the same one on
	// every run.
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		adapterID := string(k)
		lb, err := decodeListingBudget(adapterID, entries[k].n)
		if err != nil {
			return nil, err
		}
		out[adapterID] = lb
	}
	return out, nil
}

// yamlNode captures the node a value resolved to. The decoder follows an
// alias before it calls UnmarshalYAML, so n is never an alias node. A null
// value never reaches UnmarshalYAML and leaves n nil.
type yamlNode struct{ n *yaml.Node }

// UnmarshalYAML implements yaml.Unmarshaler.
func (v *yamlNode) UnmarshalYAML(n *yaml.Node) error {
	v.n = n
	return nil
}

// adapterKey is a `listing_budgets` key read as its scalar text. A key that
// does not resolve to a string, such as `1` or `true`, still names an entry,
// which §7.5.2 treats as an entry for an adapter this binary does not
// register: a run for another harness ignores it and `podium sync --check`
// reports it. A null key reads as "".
type adapterKey string

// UnmarshalYAML implements yaml.Unmarshaler. A collection key has no scalar
// text, so it reads as its type and position, which no adapter matches.
func (k *adapterKey) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*k = adapterKey(n.Value)
		return nil
	}
	*k = adapterKey(fmt.Sprintf("%s at line %d", yamlTypeName(n), n.Line))
	return nil
}

// entryKey is a key inside one `listing_budgets` entry. Only a key that
// resolves to a string can match a schema key, so any other key reads as ""
// and is ignored with the other unknown keys.
type entryKey string

// UnmarshalYAML implements yaml.Unmarshaler.
func (k *entryKey) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str" {
		*k = entryKey(n.Value)
	}
	return nil
}

// decodeListingBudget type-checks one entry. A null entry sets no key.
func decodeListingBudget(adapterID string, n *yaml.Node) (ListingBudget, error) {
	var lb ListingBudget
	if n == nil {
		return lb, nil
	}
	if n.Kind != yaml.MappingNode {
		return lb, fmt.Errorf("%s: want a mapping, got %s", adapterID, yamlTypeName(n))
	}
	var fields map[entryKey]yamlNode
	if err := n.Decode(&fields); err != nil {
		return lb, fmt.Errorf("%s: %w", adapterID, err)
	}
	var err error
	for _, f := range []struct {
		key entryKey
		dst **int
	}{
		{"max_chars", &lb.MaxChars},
		{"entry_overhead_chars", &lb.EntryOverheadChars},
		{"max_entry_chars", &lb.MaxEntryChars},
	} {
		if *f.dst, err = yamlInt(fields[f.key].n); err != nil {
			return lb, fmt.Errorf("%s.%s: %w", adapterID, f.key, err)
		}
	}
	if lb.Types, err = yamlStrings(fields["types"].n); err != nil {
		return lb, fmt.Errorf("%s.types: %w", adapterID, err)
	}
	if lb.Check, err = yamlString(fields["check"].n); err != nil {
		return lb, fmt.Errorf("%s.check: %w", adapterID, err)
	}
	return lb, nil
}

// yamlInt accepts a node whose resolved tag is !!int, or null for unset.
func yamlInt(n *yaml.Node) (*int, error) {
	if n == nil {
		return nil, nil
	}
	if n.ShortTag() != "!!int" {
		return nil, fmt.Errorf("want an integer, got %s", yamlTypeName(n))
	}
	var i int
	if err := n.Decode(&i); err != nil {
		return nil, fmt.Errorf("integer %s is out of range", n.Value)
	}
	return &i, nil
}

// yamlString accepts a node whose resolved tag is !!str, or null for unset.
func yamlString(n *yaml.Node) (*string, error) {
	if n == nil {
		return nil, nil
	}
	if n.ShortTag() != "!!str" {
		return nil, fmt.Errorf("want a string, got %s", yamlTypeName(n))
	}
	s := n.Value
	return &s, nil
}

// yamlStrings accepts a sequence whose items all resolve to !!str, or null
// for unset. An empty sequence returns a non-nil empty slice.
func yamlStrings(n *yaml.Node) ([]string, error) {
	if n == nil {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("want a list of strings, got %s", yamlTypeName(n))
	}
	// The items are walked rather than decoded, because a decode into a
	// slice drops a null item and would read `[~]` as an empty list. An
	// anchor cannot sit on an alias, so one step resolves an aliased item.
	out := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		if item.Kind == yaml.AliasNode {
			item = item.Alias
		}
		if item.ShortTag() != "!!str" {
			return nil, fmt.Errorf("want a list of strings, got an item that is %s", yamlTypeName(item))
		}
		out = append(out, item.Value)
	}
	return out, nil
}

// yamlTypeName names the resolved YAML type of a node for an error message.
// A nil node is a null value.
func yamlTypeName(n *yaml.Node) string {
	if n == nil {
		return "null"
	}
	switch tag := n.ShortTag(); tag {
	case "!!null":
		return "null"
	case "!!bool":
		return "a boolean"
	case "!!int":
		return "an integer"
	case "!!float":
		return "a float"
	case "!!str":
		return "a string"
	case "!!binary":
		return "a binary value"
	case "!!timestamp":
		return "a timestamp"
	case "!!seq":
		return "a list"
	case "!!map":
		return "a mapping"
	default:
		return "a value tagged " + tag
	}
}

// ConfigPath returns the canonical path to a workspace's sync.yaml.
func ConfigPath(workspace string) string {
	return filepath.Join(workspace, ".podium", "sync.yaml")
}

// ResolveRegistryPath resolves a sync.yaml `defaults.registry`
// value per §13.11.2: filesystem URLs return as-is, absolute
// paths return as-is, and relative paths resolve against the
// workspace. Empty input returns "".
func ResolveRegistryPath(workspace, registry string) string {
	if registry == "" {
		return ""
	}
	// HTTP(S) URLs and file:// URIs pass through unchanged; the
	// caller distinguishes filesystem from server source by
	// inspecting the scheme.
	if hasURLScheme(registry) {
		return registry
	}
	if filepath.IsAbs(registry) {
		return filepath.Clean(registry)
	}
	return filepath.Clean(filepath.Join(workspace, registry))
}

// hasURLScheme reports whether s begins with a URL scheme
// (http://, https://, file://). The check is intentionally
// strict: registry: ./relative is a path, not a URL.
func hasURLScheme(s string) bool {
	for _, prefix := range []string{"http://", "https://", "file://"} {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// isServerSource reports whether registry resolves to a Podium server
// under the §7.1 / §7.5.2 dispatch: an http:// or https:// URL routes to
// a server, and every other value (a bare path, a file:// URI) is a
// filesystem source. Run and Watch use it to reject a server URL with a
// canonical error instead of letting filesystem.Open mangle the URL into
// a bogus path under the working directory.
//
// spec: §7.1, §7.5.2 — "a URL routes to a Podium server, a filesystem
// path routes to local filesystem".
// IsServerSource is the exported form of isServerSource for callers outside
// the package (the CLI uses it to decide whether the §3.5 scope-preview
// endpoint is reachable over HTTP).
func IsServerSource(registry string) bool { return isServerSource(registry) }

func isServerSource(registry string) bool {
	for _, prefix := range []string{"http://", "https://"} {
		if len(registry) >= len(prefix) && registry[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// ReadConfig reads sync.yaml from the workspace's .podium/ directory.
// A missing file returns (nil, nil) so callers can distinguish "no
// config" from "invalid config" without an error type discriminator.
func ReadConfig(workspace string) (*SyncConfig, error) {
	data, err := os.ReadFile(ConfigPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cfg := &SyncConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("sync.yaml: %w", err)
	}
	return cfg, nil
}

// WriteConfig writes the SyncConfig back to the workspace's
// sync.yaml atomically via .tmp + rename.
func WriteConfig(workspace string, cfg *SyncConfig) error {
	return writeConfig(workspace, cfg, false)
}

// writeConfig writes cfg to the workspace's sync.yaml atomically. When
// ensureDefaults is true and cfg carries no defaults, an empty `defaults:`
// mapping is emitted ahead of `profiles:` so a freshly created sync.yaml has
// the structure §7.5.6 / §7.5.7 describe.
func writeConfig(workspace string, cfg *SyncConfig, ensureDefaults bool) error {
	dir := filepath.Join(workspace, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := marshalConfig(cfg, ensureDefaults)
	if err != nil {
		return err
	}
	final := ConfigPath(workspace)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// marshalConfig renders cfg to YAML. When ensureDefaults is true and the
// rendered config has no `defaults:` key (a zero-value Defaults is dropped by
// omitempty), an empty `defaults:` mapping is prepended so it precedes
// `profiles:`, matching the fresh-file layout `podium profile edit` produces.
//
// spec: §7.5.6 — a fresh sync.yaml is created "with the new profile and an
// empty defaults: block".
//
// A `listing_budgets` block is encoded through a detached copy (detachNode),
// because an alias in it can name an anchor under a key SyncConfig does not
// hold, which the encoder would otherwise fail to resolve.
func marshalConfig(cfg *SyncConfig, ensureDefaults bool) ([]byte, error) {
	if cfg.ListingBudgets.Kind != 0 {
		detached, err := detachNode(&cfg.ListingBudgets)
		if err != nil {
			return nil, fmt.Errorf("sync.yaml: listing_budgets: %w", err)
		}
		cp := *cfg
		cp.ListingBudgets = *detached
		cfg = &cp
	}
	var node yaml.Node
	if err := node.Encode(cfg); err != nil {
		return nil, err
	}
	if ensureDefaults && findConfigKey(&node, "defaults") == nil {
		node.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "defaults"},
			{Kind: yaml.MappingNode, Tag: "!!map"},
		}, node.Content...)
	}
	return yaml.Marshal(&node)
}

// detachNode returns a deep copy of n in which every alias is replaced by a
// detached copy of the node it names and every anchor is cleared, so the copy
// encodes without reference to any anchor outside it. Scalars, tags, styles,
// `<<` merge keys, and values of the wrong type are copied as they are, and
// yaml.v3 merges an inline `<<` mapping as it merged the alias. An alias that
// names a node enclosing it is an error: yaml.v3 parses it into a cycle, which
// has no finite copy.
//
// Spec: §7.5.6, §7.5.2
func detachNode(n *yaml.Node) (*yaml.Node, error) {
	return detachOnPath(n, map[*yaml.Node]bool{})
}

// detachOnPath copies n, where onPath holds the nodes on the copy path from
// the root to n. The set is what detects an alias to an enclosing node.
func detachOnPath(n *yaml.Node, onPath map[*yaml.Node]bool) (*yaml.Node, error) {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return nil, fmt.Errorf("unknown anchor %q referenced", n.Value)
		}
		if onPath[n.Alias] {
			return nil, fmt.Errorf("alias *%s names a node that encloses it", n.Value)
		}
		return detachOnPath(n.Alias, onPath)
	}
	onPath[n] = true
	defer delete(onPath, n)
	cp := *n
	cp.Anchor = ""
	cp.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c, err := detachOnPath(child, onPath)
		if err != nil {
			return nil, err
		}
		cp.Content[i] = c
	}
	return &cp, nil
}

// findConfigKey returns the value node for key in a mapping node, or nil.
func findConfigKey(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// requiredServerVersion returns the highest min_server_version pinned by the
// `defaults:` block or by any of the named profiles, and whether any pin was
// found (§6.7 "Versioning"). An unparsable pin is treated as higher than a
// parsable one so checkServerVersion surfaces it rather than dropping it.
func requiredServerVersion(defaults Defaults, profiles map[string]Profile, names []string) (string, bool) {
	pins := []string{defaults.MinServerVersion}
	for _, name := range names {
		if p, ok := profiles[name]; ok {
			pins = append(pins, p.MinServerVersion)
		}
	}
	highest := ""
	for _, p := range pins {
		if p == "" {
			continue
		}
		if highest == "" {
			highest = p
			continue
		}
		if c, err := version.Compare(p, highest); err != nil || c > 0 {
			highest = p
		}
	}
	return highest, highest != ""
}

// checkServerVersion verifies that binaryVersion satisfies the highest pin
// across defaults and the named profiles. It returns a
// config.server_version_too_old error when binaryVersion is below the pin, a
// config.invalid_min_version error when a pin (or the binary version) is
// unparsable, or nil when no pin applies or the binary satisfies it.
func checkServerVersion(binaryVersion string, defaults Defaults, profiles map[string]Profile, names []string) error {
	min, ok := requiredServerVersion(defaults, profiles, names)
	if !ok {
		return nil
	}
	atLeast, err := version.AtLeast(binaryVersion, min)
	if err != nil {
		return fmt.Errorf("config.invalid_min_version: cannot compare binary version %q against min_server_version %q: %w",
			binaryVersion, min, err)
	}
	if !atLeast {
		return fmt.Errorf("config.server_version_too_old: this binary is version %s but the configuration pins min_server_version %s; upgrade Podium to run this profile",
			binaryVersion, min)
	}
	return nil
}

// CheckServerVersion enforces the §6.7 "Versioning" pin against a raw
// SyncConfig: binaryVersion must satisfy the highest min_server_version pinned
// by defaults or by any of the named profiles. Pass every profile name for a
// server that can serve any profile (the MCP bridge). See checkServerVersion
// for the error codes.
func (cfg *SyncConfig) CheckServerVersion(binaryVersion string, profiles ...string) error {
	return checkServerVersion(binaryVersion, cfg.Defaults, cfg.Profiles, profiles)
}

// CheckServerVersion enforces the §6.7 "Versioning" pin against a merged
// (multi-scope) config: binaryVersion must satisfy the highest
// min_server_version pinned by the merged defaults or by the active profile.
// Pass the single resolved profile name (a `podium sync` run resolves exactly
// one profile). See checkServerVersion for the error codes.
func (cfg *MergedConfig) CheckServerVersion(binaryVersion string, profiles ...string) error {
	return checkServerVersion(binaryVersion, cfg.Defaults, cfg.Profiles, profiles)
}
