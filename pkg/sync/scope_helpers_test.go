package sync

import (
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// mkRecords builds synthetic records keyed by id; type defaults to context.
func mkRecords(ids ...string) []filesystem.ArtifactRecord {
	out := make([]filesystem.ArtifactRecord, len(ids))
	for i, id := range ids {
		out[i] = filesystem.ArtifactRecord{
			ID: id,
			Artifact: &manifest.Artifact{
				Type:    manifest.TypeContext,
				Version: "1.0.0",
			},
		}
	}
	return out
}

func mkRecordsWithTypes(pairs ...[2]string) []filesystem.ArtifactRecord {
	out := make([]filesystem.ArtifactRecord, len(pairs))
	for i, p := range pairs {
		out[i] = filesystem.ArtifactRecord{
			ID: p[0],
			Artifact: &manifest.Artifact{
				Type:    manifest.ArtifactType(p[1]),
				Version: "1.0.0",
			},
		}
	}
	return out
}

func idsOfRecords(records []filesystem.ArtifactRecord) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.ID
	}
	return out
}
