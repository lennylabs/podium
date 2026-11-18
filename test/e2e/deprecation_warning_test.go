package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Spec: §4.7.4, §4.7.6, §7.2, §7.6.1 — through the binary, after a layer
// publishes team/notes 1.0.0 and then a deprecated 2.0.0 that names
// team/notes-v2: podium artifact show serves 1.0.0 and prints the warning
// naming 2.0.0 and its upgrade target on standard error, --json carries the
// deprecation members, a load pinned to 2.0.0 reports the served version as
// deprecated, and a HEAD revalidation carries X-Podium-Deprecation-Warning.
func TestDeprecation_LatestWarnsThroughTheCLI(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	l := newRepublishLayer(t, srv, "lifecycle")
	l.publishVersion(t, versionSpec{ID: "team/notes", Version: "1.0.0", Description: "Meeting notes template for weekly team syncs"})
	l.publishVersion(t, versionSpec{ID: "team/notes", Version: "2.0.0", Description: "Meeting notes template for weekly team syncs",
		Deprecated: true, ReplacedBy: "team/notes-v2"})
	const want = "version 2.0.0 of team/notes is deprecated; replaced_by team/notes-v2; this load serves version 1.0.0"

	human := runPodium(t, "", srv.cliEnv(), "artifact", "show", "team/notes", "--registry", srv.BaseURL)
	if human.Exit != 0 || !strings.Contains(human.Stderr, "warning: "+want) || strings.Contains(human.Stdout, want) {
		t.Errorf("artifact show: exit=%d stderr=%q, want exit 0 and the warning on stderr alone", human.Exit, human.Stderr)
	}

	js := runPodium(t, "", srv.cliEnv(), "artifact", "show", "team/notes", "--json", "--registry", srv.BaseURL)
	var got map[string]any
	if err := json.Unmarshal([]byte(js.Stdout), &got); err != nil {
		t.Fatalf("artifact show --json %q: %v", js.Stdout, err)
	}
	if got["version"] != "1.0.0" || got["deprecation_warning"] != want || got["replaced_by"] != "team/notes-v2" || got["deprecated"] != nil {
		t.Errorf("artifact show --json = %v, want 1.0.0 with the warning and replaced_by", got)
	}

	pinned := runPodium(t, "", srv.cliEnv(), "artifact", "show", "team/notes", "--version", "2.0.0", "--json", "--registry", srv.BaseURL)
	got = map[string]any{}
	if err := json.Unmarshal([]byte(pinned.Stdout), &got); err != nil {
		t.Fatalf("pinned --json %q: %v", pinned.Stdout, err)
	}
	if got["deprecated"] != true || got["deprecation_warning"] != "artifact is deprecated; replaced_by team/notes-v2" {
		t.Errorf("pinned 2.0.0 = %v, want deprecated with its own warning", got)
	}

	req, _ := http.NewRequest(http.MethodHead, srv.BaseURL+"/v1/load_artifact?id=team/notes", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if h := resp.Header.Get("X-Podium-Deprecation-Warning"); h != want {
		t.Errorf("HEAD X-Podium-Deprecation-Warning = %q, want %q", h, want)
	}
}
