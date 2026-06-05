package e2e

import "testing"

// spec: §13.10 — a standalone registry booted with
// PODIUM_ENFORCE_SANDBOX_PROFILE=true refuses to ingest an artifact whose
// sandbox_profile the local host cannot honor (the host advertises only the
// default "unrestricted"), so that artifact is absent from the catalog while a
// compatible one ingests normally.
func TestStandalone_SandboxProfileIngestGateEnforced(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"eng/restricted/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Restricted widget.\nsensitivity: low\nsandbox_profile: seccomp-strict\n---\n\nbody\n",
		"eng/open/ARTIFACT.md":       "---\ntype: context\nversion: 1.0.0\ndescription: Open widget.\nsensitivity: low\nsandbox_profile: unrestricted\n---\n\nbody\n",
	})
	home := t.TempDir()
	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_ENFORCE_SANDBOX_PROFILE=true",
	}, "serve", "--standalone", "--layer-path", reg)

	// The seccomp-strict artifact was refused at ingest, so loading it fails.
	if st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id=eng/restricted"); st == 200 {
		t.Errorf("eng/restricted loaded (HTTP %d); want it rejected at ingest under PODIUM_ENFORCE_SANDBOX_PROFILE\nbody: %s", st, body)
	}
	// The unrestricted artifact is enforceable and ingests normally.
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=eng/open", nil)
}

// spec: §13.10 — without PODIUM_ENFORCE_SANDBOX_PROFILE the
// registry treats sandbox_profile as informational and ingests an artifact
// whose profile the host cannot enforce locally.
func TestStandalone_SandboxProfileInformationalByDefault(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"eng/restricted/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Restricted widget.\nsensitivity: low\nsandbox_profile: seccomp-strict\n---\n\nbody\n",
	})
	home := t.TempDir()
	srv := startServerArgs(t, []string{"HOME=" + home}, "serve", "--standalone", "--layer-path", reg)

	// No enforcement env: the restrictive profile is informational and loads.
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=eng/restricted", nil)
}
