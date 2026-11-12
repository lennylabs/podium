package serverboot

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// clientHeaderRead matches a call that parses or reads the X-Podium-Client
// value: the parser, or the header read through the audit constant.
var clientHeaderRead = regexp.MustCompile(`\.ParseClientHeader\b|Header\.Get\(\s*audit\.ClientHeader\s*\)`)

// clientHeaderReaders are the module-relative files allowed to read the
// header outside pkg/audit: the registry's audit metadata builder.
var clientHeaderReaders = map[string]bool{
	"pkg/registry/server/audit_context.go": true,
}

// moduleGoFiles enumerates every non-test Go file of the module as a
// module-relative slash path. The module pattern resolves from the test's
// package directory, where ./... would list only this package's subtree, and
// it leaves out the nested repository copies under .claude/worktrees/.
func moduleGoFiles(t *testing.T) (root string, files []string) {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}{{range .GoFiles}} {{.}}{{end}}",
		"github.com/lennylabs/podium/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	mod, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	root = strings.TrimSpace(string(mod))
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rel, err := filepath.Rel(root, fields[0])
		if err != nil {
			t.Fatalf("relative path of %s: %v", fields[0], err)
		}
		for _, name := range fields[1:] {
			files = append(files, filepath.ToSlash(filepath.Join(rel, name)))
		}
	}
	return root, files
}

// Spec: §7.2.2, §8.1 — the X-Podium-Client value is advisory and is never an
// input to authentication, authorization, visibility, quotas, sampling, or
// tenant selection. The guard pins that structurally: outside pkg/audit, only
// the registry's audit metadata builder parses or reads the header, so no
// other path can branch on it.
func TestClientHeaderGuard_OnlyTheAuditMetadataReadsTheHeader(t *testing.T) {
	root, files := moduleGoFiles(t)
	listed := map[string]bool{}
	for _, f := range files {
		listed[f] = true
	}
	// Positive control: an empty or narrowed enumeration would pass the
	// scan below vacuously.
	for _, want := range []string{"cmd/podium-mcp/main.go", "pkg/registry/server/audit_context.go"} {
		if !listed[want] {
			t.Fatalf("module enumeration lacks %s; listed %d files", want, len(files))
		}
	}
	for _, f := range files {
		if strings.HasPrefix(f, "pkg/audit/") || clientHeaderReaders[f] {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if m := clientHeaderRead.Find(src); m != nil {
			t.Errorf("%s reads the X-Podium-Client header (%s); only pkg/audit and pkg/registry/server/audit_context.go may", f, m)
		}
	}
}
