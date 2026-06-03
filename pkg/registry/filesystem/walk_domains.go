package filesystem

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// DomainRecord is one DOMAIN.md discovered under a layer's tree, with its
// canonical domain path (the directory relative to the layer root,
// slash-separated; "" for a root-level DOMAIN.md), the containing layer, the
// parsed Domain, and the raw bytes.
//
// spec: §4.5.1 / §6.4 — the workspace overlay carries DOMAIN.md files that
// merge as the highest-precedence layer in the caller's effective view. The
// artifact walk (Walk) deliberately skips DOMAIN.md; this walk surfaces them
// so a consumer that exposes load_domain can apply the §4.5.4 client-side
// merge (F-4.5.2, F-6.4.2).
type DomainRecord struct {
	Path   string
	Layer  Layer
	Domain *manifest.Domain
	Raw    []byte
}

// WalkDomains walks every layer in r and returns the discovered DOMAIN.md
// records in a stable order: layer order first, alphabetical domain path
// within each layer. A DOMAIN.md whose frontmatter fails to parse is skipped
// (the linter reports it at ingest), mirroring mergedDomains and the lint
// walker. Unlike Walk, the root-level DOMAIN.md (path "") is retained: a
// domain path may be empty, even though an artifact ID may not.
func (r *Registry) WalkDomains() ([]DomainRecord, error) {
	var all []DomainRecord
	for _, layer := range r.Layers {
		recs, err := walkLayerDomains(layer)
		if err != nil {
			return nil, err
		}
		all = append(all, recs...)
	}
	return all, nil
}

func walkLayerDomains(layer Layer) ([]DomainRecord, error) {
	var records []DomainRecord
	walkErr := filepath.WalkDir(layer.Path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != layer.Path {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "DOMAIN.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dom, perr := manifest.ParseDomain(data)
		if perr != nil {
			// Malformed DOMAIN.md is skipped here; ingest-time lint reports it.
			return nil
		}
		rel, err := filepath.Rel(layer.Path, filepath.Dir(path))
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if key == "." {
			key = ""
		}
		records = append(records, DomainRecord{
			Path:   key,
			Layer:  layer,
			Domain: dom,
			Raw:    data,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records, nil
}
