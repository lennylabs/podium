package e2e

// End-to-end tests for the §7.2 / §7.6.2 data plane in the
// standalone-without-storage deployment (§13.11): a real `podium serve
// --standalone` process with PODIUM_OBJECT_STORE=none, ingesting a filesystem
// registry that bundles binary resources. Covers F-4.1.1 (binary inline
// resources base64-encoded so JSON does not corrupt them), F-7.2.1 (a resource
// above the inline cutoff served inline rather than failing without an object
// store), and F-7.6.4 (the batch path delivers inline resources instead of
// dropping them).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// rdBinary builds a deterministic byte slice that is not valid UTF-8 (it opens
// with 0xff 0xfe), so the server must base64-encode it on the wire.
func rdBinary(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i % 256)
	}
	if n >= 2 {
		out[0], out[1] = 0xff, 0xfe
	}
	return out
}

// startNoStoreServer boots a standalone server with the object store disabled
// (PODIUM_OBJECT_STORE=none), ingesting the given filesystem registry.
func startNoStoreServer(t *testing.T, reg string) *serverProc {
	t.Helper()
	return startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_OBJECT_STORE=none"},
		"serve", "--standalone", "--layer-path", reg)
}

// Spec: §4.1/§7.2 (F-4.1.1, F-7.2.1) — with no object store, load_artifact
// serves every bundled resource inline regardless of size, and base64-encodes
// the inline set (resources_base64) when any member is binary so the bytes
// survive JSON transport. Without the fixes a binary resource is corrupted by
// the U+FFFD replacement and a large resource fails with registry.unavailable.
func TestResourceDelivery_NoStoreLoadArtifactInlineBinary(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	binary := rdBinary(64)
	large := rdBinary(256*1024 + 1024) // above the 256 KB inline cutoff
	srv := startNoStoreServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":       brSkillArtifact,
		id + "/SKILL.md":          brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/data/blob.bin":     string(binary),
		id + "/data/big.bin":      string(large),
		id + "/scripts/clean.txt": "plain text\n",
	}))

	var resp struct {
		Resources      map[string]string `json:"resources"`
		ResourcesB64   bool              `json:"resources_base64"`
		LargeResources map[string]any    `json:"large_resources"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id="+id, &resp)

	if len(resp.LargeResources) != 0 {
		t.Errorf("no object store: nothing should presign, got %v", resp.LargeResources)
	}
	if !resp.ResourcesB64 {
		t.Fatalf("resources_base64 should be set when a binary resource is inline: %+v", resp)
	}
	// The whole inline set is base64 (response-wide flag); every member decodes.
	for path, want := range map[string][]byte{
		"data/blob.bin":     binary,
		"data/big.bin":      large,
		"scripts/clean.txt": []byte("plain text\n"),
	} {
		got, err := base64.StdEncoding.DecodeString(resp.Resources[path])
		if err != nil {
			t.Errorf("%s: decode: %v", path, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: corrupted (%d vs %d bytes)", path, len(got), len(want))
		}
	}
}

// Spec: §7.6.2 (F-7.6.4) — the batch path delivers inline resources when no
// object store is configured, rather than dropping them. A binary resource
// carries inline_base64 so its bytes survive transport. Without the fix the
// batch consumer materializes a package missing files the single-load path
// would have written.
func TestResourceDelivery_NoStoreBatchLoadInline(t *testing.T) {
	t.Parallel()
	id := "finance/close-reporting/run-variance-analysis"
	binary := rdBinary(48)
	srv := startNoStoreServer(t, writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/data/blob.bin":       string(binary),
		id + "/scripts/variance.py": "print('variance')\n",
	}))

	st, body := postJSON(t, srv.BaseURL+"/v1/artifacts:batchLoad",
		map[string]any{"ids": []string{id}})
	if st != 200 {
		t.Fatalf("batchLoad = HTTP %d: %s", st, body)
	}
	var envs []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Resources []struct {
			Path         string `json:"path"`
			PresignedURL string `json:"presigned_url"`
			Inline       string `json:"inline"`
			InlineBase64 bool   `json:"inline_base64"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(body, &envs); err != nil {
		t.Fatalf("decode batch: %v\n%s", err, body)
	}
	if len(envs) != 1 || envs[0].Status != "ok" {
		t.Fatalf("batch envelopes = %+v, want one ok item", envs)
	}
	got := map[string][]byte{}
	for _, r := range envs[0].Resources {
		if r.PresignedURL != "" {
			t.Errorf("%s: no object store, presigned_url should be empty", r.Path)
		}
		if r.InlineBase64 {
			dec, err := base64.StdEncoding.DecodeString(r.Inline)
			if err != nil {
				t.Errorf("%s: decode: %v", r.Path, err)
				continue
			}
			got[r.Path] = dec
		} else {
			got[r.Path] = []byte(r.Inline)
		}
	}
	if len(got) != 2 {
		t.Fatalf("batch resources = %d, want 2 (none dropped): %+v", len(got), envs[0].Resources)
	}
	if !bytes.Equal(got["data/blob.bin"], binary) {
		t.Errorf("binary batch resource corrupted")
	}
	if string(got["scripts/variance.py"]) != "print('variance')\n" {
		t.Errorf("text batch resource = %q", got["scripts/variance.py"])
	}
}
