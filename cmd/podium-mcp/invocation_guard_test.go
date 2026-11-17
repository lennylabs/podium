package main

import (
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §6.9, §4.3.4 — the registry path resolves a plain skill from the
// served frontmatter and its verbatim SKILL.md before the §6.9 guard runs, so
// a skill whose SKILL.md metadata states invocation: user fails on hermes,
// where both the skill row and the invocation: user row are ✗, with a
// materialize.untranslatable message that names both capabilities. A guard
// input built from the served frontmatter alone names type: skill only. The
// same skill with invocation any names type: skill alone, and nothing is
// written in either case.
func TestDeliver_UserInvokedSkillGuardNamesInvocation(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: skill\nversion: 0.0.0-auto.0\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"
	cases := []struct {
		name, invocation, want string
	}{
		{"user", "user", "invocation: user, type: skill"},
		{"any", "any", "cannot translate type: skill;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dest := t.TempDir()
			s := newTestServer(t, &config{harness: "hermes", materializeRoot: dest, verifyPolicy: sign.PolicyNever})
			resp := fixtureResp("ops/ping", fm)
			resp.Type = "skill"
			resp.SkillRaw = "---\nname: ping\ndescription: Ping the on-call channel.\nmetadata:\n  invocation: \"" +
				tc.invocation + "\"\n---\n\nPing.\n"
			resp = sealDelivery(resp)
			m, ok := s.deliverLoadArtifact(resp, deliverOpts{harness: "hermes", destination: dest}).(map[string]any)
			if !ok {
				t.Fatalf("deliver returned %T, want an error map", m)
			}
			msg, _ := m["error"].(string)
			if m["code"] != "materialize.untranslatable" || !strings.Contains(msg, tc.want) {
				t.Errorf("result = %v, want materialize.untranslatable containing %q", m, tc.want)
			}
			if tc.invocation == "any" && strings.Contains(msg, "invocation: user") {
				t.Errorf("message %q names invocation: user for an any skill", msg)
			}
			if entries, err := os.ReadDir(dest); err != nil || len(entries) != 0 {
				t.Errorf("destination holds %d entries (%v), want nothing written", len(entries), err)
			}
		})
	}
}
