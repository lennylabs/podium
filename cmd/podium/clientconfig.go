package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/sync"
)

// clientScope is one of the three §7.5.2 sync.yaml files together with
// its display label, on-disk path, and parsed contents (nil when the
// file is absent).
type clientScope struct {
	label string
	path  string
	cfg   *sync.SyncConfig
}

// defaultsKeys is the fixed print order for the merged defaults block.
// spec: §7.7 (config show).
var defaultsKeys = []string{"registry", "harness", "target", "profile"}

// loadClientScopes reads the three §7.5.2 config files in precedence
// order (low to high): user-global, project-shared, project-local.
// A missing workspace contributes only the user-global scope, matching
// LoadMergedConfig. spec: §7.5.2, §7.7.
func loadClientScopes(cwd, home string) ([]clientScope, string, error) {
	ws, ok := sync.DiscoverWorkspace(cwd)
	if !ok {
		ws = ""
	}
	var scopes []clientScope
	if home != "" {
		p := filepath.Join(home, ".podium", "sync.yaml")
		cfg, err := sync.ReadConfigFile(p)
		if err != nil {
			return nil, ws, err
		}
		scopes = append(scopes, clientScope{"~/.podium/sync.yaml", p, cfg})
	}
	if ws != "" {
		ps := filepath.Join(ws, ".podium", "sync.yaml")
		pc, err := sync.ReadConfigFile(ps)
		if err != nil {
			return nil, ws, err
		}
		scopes = append(scopes, clientScope{"<ws>/.podium/sync.yaml", ps, pc})
		pl := filepath.Join(ws, ".podium", "sync.local.yaml")
		lc, err := sync.ReadConfigFile(pl)
		if err != nil {
			return nil, ws, err
		}
		scopes = append(scopes, clientScope{"<ws>/.podium/sync.local.yaml", pl, lc})
	}
	return scopes, ws, nil
}

// defaultsField returns one defaults field by name, or "" when unset
// (or the scope is absent).
func defaultsField(cfg *sync.SyncConfig, field string) string {
	if cfg == nil {
		return ""
	}
	switch field {
	case "registry":
		return cfg.Defaults.Registry
	case "harness":
		return cfg.Defaults.Harness
	case "target":
		return cfg.Defaults.Target
	case "profile":
		return cfg.Defaults.Profile
	}
	return ""
}

// provValue is a resolved value with the scope label it came from.
type provValue struct {
	Value string `json:"value"`
	From  string `json:"from"`
}

// resolveDefaults merges the defaults block across scopes (highest
// precedence non-empty value wins) and records the winning scope per
// key. spec: §7.5.2 precedence, §7.7 provenance.
func resolveDefaults(scopes []clientScope) map[string]provValue {
	out := map[string]provValue{}
	for _, key := range defaultsKeys {
		for _, s := range scopes { // low → high; later overwrites earlier
			if v := defaultsField(s.cfg, key); v != "" {
				out[key] = provValue{Value: v, From: s.label}
			}
		}
	}
	return out
}

// profileResolution captures the winning profile body, the scope it
// won from, and every scope that defined it (for collision reporting).
type profileResolution struct {
	Profile sync.Profile
	Winner  string
	Defined []string
}

// resolveProfiles unions profiles across scopes with whole-profile
// overwrite on a name collision, recording each profile's winning scope
// and the full set of scopes that defined it. spec: §7.5.2 profile
// merge, §7.7 collisions.
func resolveProfiles(scopes []clientScope) map[string]profileResolution {
	out := map[string]profileResolution{}
	for _, s := range scopes { // low → high
		if s.cfg == nil {
			continue
		}
		for name, p := range s.cfg.Profiles {
			r := out[name]
			r.Profile = p
			r.Winner = s.label
			r.Defined = append(r.Defined, s.label)
			out[name] = r
		}
	}
	return out
}

// configClientShow prints the merged sync.yaml with per-key provenance
// for the active workspace. spec: §7.7 (podium config show).
func configClientShow(asJSON bool, explain string) int {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return configClientShowAt(cwd, home, asJSON, explain)
}

// configClientShowAt is the testable core of configClientShow: it takes
// the working directory and home directory explicitly.
func configClientShowAt(cwd, home string, asJSON bool, explain string) int {
	scopes, ws, err := loadClientScopes(cwd, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if explain != "" {
		return explainConfigKey(explain, scopes)
	}
	defs := resolveDefaults(scopes)
	profs := resolveProfiles(scopes)

	if asJSON {
		type collisionJSON struct {
			Profile string   `json:"profile"`
			Scopes  []string `json:"scopes"`
			Winner  string   `json:"winner"`
		}
		payload := map[string]any{}
		payload["workspace"] = ws
		payload["defaults"] = defs
		profOut := map[string]any{}
		var collisions []collisionJSON
		for name, r := range profs {
			profOut[name] = map[string]any{"from": r.Winner, "profile": r.Profile}
			if len(r.Defined) > 1 {
				collisions = append(collisions, collisionJSON{Profile: name, Scopes: r.Defined, Winner: r.Winner})
			}
		}
		payload["profiles"] = profOut
		payload["collisions"] = collisions
		_ = json.NewEncoder(os.Stdout).Encode(payload)
		return 0
	}

	// Human view: defaults block, then each profile, then a collision
	// summary line. spec: §7.7 example output.
	for _, key := range defaultsKeys {
		if pv, ok := defs[key]; ok {
			fmt.Fprintf(os.Stdout, "defaults.%s:   %s   (from %s)\n", key, pv.Value, pv.From)
		}
	}
	names := make([]string, 0, len(profs))
	for name := range profs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := profs[name]
		fmt.Fprintf(os.Stdout, "profiles.%s:   (from %s)\n", name, r.Winner)
		for _, line := range profileLines(r.Profile) {
			fmt.Fprintf(os.Stdout, "  %s\n", line)
		}
	}
	collisionCount := 0
	var collisionLines []string
	for _, name := range names {
		r := profs[name]
		if len(r.Defined) > 1 {
			collisionCount++
			collisionLines = append(collisionLines, fmt.Sprintf(
				"profiles.%s defined in %s; %s wins",
				name, strings.Join(r.Defined, " and "), r.Winner))
		}
	}
	if collisionCount > 0 {
		fmt.Fprintf(os.Stdout, "\nProfile collisions: %d (%s)\n", collisionCount, strings.Join(collisionLines, "; "))
	}
	return 0
}

// profileLines renders a profile's non-empty fields for the human view.
func profileLines(p sync.Profile) []string {
	var out []string
	if p.Harness != "" {
		out = append(out, "harness: "+p.Harness)
	}
	if p.Target != "" {
		out = append(out, "target: "+p.Target)
	}
	if len(p.Include) > 0 {
		out = append(out, "include: ["+strings.Join(p.Include, ", ")+"]")
	}
	if len(p.Exclude) > 0 {
		out = append(out, "exclude: ["+strings.Join(p.Exclude, ", ")+"]")
	}
	if len(p.Type) > 0 {
		out = append(out, "type: ["+strings.Join(p.Type, ", ")+"]")
	}
	return out
}

// explainConfigKey prints the full resolution chain for one key: the
// value held at each scope and which scope won. spec: §7.7 (--explain).
// key may be "registry"/"defaults.registry" or "profiles.<name>".
func explainConfigKey(key string, scopes []clientScope) int {
	norm := key
	if !strings.Contains(norm, ".") {
		norm = "defaults." + norm
	}
	switch {
	case strings.HasPrefix(norm, "defaults."):
		field := strings.TrimPrefix(norm, "defaults.")
		fmt.Fprintf(os.Stdout, "%s:\n", norm)
		winner := ""
		winVal := ""
		for _, s := range scopes {
			v := defaultsField(s.cfg, field)
			if v == "" {
				fmt.Fprintf(os.Stdout, "  %s: (unset)\n", s.label)
				continue
			}
			fmt.Fprintf(os.Stdout, "  %s: %s\n", s.label, v)
			winner = s.label // last (highest precedence) non-empty wins
			winVal = v
		}
		if winner == "" {
			fmt.Fprintf(os.Stdout, "  resolved: (unset in all scopes)\n")
		} else {
			fmt.Fprintf(os.Stdout, "  resolved: %s (from %s)\n", winVal, winner)
		}
		return 0
	case strings.HasPrefix(norm, "profiles."):
		name := strings.TrimPrefix(norm, "profiles.")
		fmt.Fprintf(os.Stdout, "%s:\n", norm)
		winner := ""
		for _, s := range scopes {
			if s.cfg == nil {
				fmt.Fprintf(os.Stdout, "  %s: (unset)\n", s.label)
				continue
			}
			if _, ok := s.cfg.Profiles[name]; ok {
				fmt.Fprintf(os.Stdout, "  %s: defined\n", s.label)
				winner = s.label
			} else {
				fmt.Fprintf(os.Stdout, "  %s: (unset)\n", s.label)
			}
		}
		if winner == "" {
			fmt.Fprintf(os.Stdout, "  resolved: (unset in all scopes)\n")
		} else {
			fmt.Fprintf(os.Stdout, "  resolved: defined (from %s)\n", winner)
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: unknown config key %q\n", key)
		return 2
	}
}
