package adapter

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// userSkillSource builds the user-invoked skill team/ship from an ARTIFACT.md
// that states invocation: user and the given SKILL.md.
func userSkillSource(skillMD string) Source {
	return Source{
		ArtifactID:    "team/ship",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\ninvocation: user\n---\n"),
		SkillBytes:    []byte(skillMD),
	}
}

// frontmatterOf decodes the SKILL.md frontmatter into a map.
func frontmatterOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	s := string(b)
	end := strings.Index(s[4:], "\n---")
	if !strings.HasPrefix(s, "---\n") || end < 0 {
		t.Fatalf("no frontmatter in:\n%s", s)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s[4:4+end]), &m); err != nil {
		t.Fatalf("frontmatter does not decode: %v\n%s", err, s)
	}
	return m
}

// Spec: §4.3.4, §6.7 — skillMarkdown adds the user-only control for
// controlFrontmatter on a user-invoked skill: an absent key is injected as the
// unquoted first line, a contradicting value is replaced, an authored boolean
// true is kept byte for byte, and an any skill passes an authored control
// through. Every other frontmatter line stays byte-identical.
func TestSkillMarkdown_Control(t *testing.T) {
	t.Parallel()
	t.Run("absent key injected first", func(t *testing.T) {
		t.Parallel()
		got := string(skillMarkdown(userSkillSource("---\nname: ship\ndescription: Ship it.\n---\n\nBody.\n"), controlFrontmatter))
		want := "---\ndisable-model-invocation: true\nname: ship\ndescription: Ship it.\n---\n\nBody.\n"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	for _, authored := range []string{"false", `"true"`, "no", "|\n  true\n  more"} {
		authored := authored
		t.Run("replaces "+authored, func(t *testing.T) {
			t.Parallel()
			in := "---\nname: ship\n# keep this comment\ndisable-model-invocation: " + authored + "\n\n# trailing comment\ndescription: Ship it.\n---\n\nBody.\n"
			got := string(skillMarkdown(userSkillSource(in), controlFrontmatter))
			want := "---\nname: ship\n# keep this comment\ndisable-model-invocation: true\n\n# trailing comment\ndescription: Ship it.\n---\n\nBody.\n"
			if got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
			if v := frontmatterOf(t, []byte(got))[userOnlyKey]; v != true {
				t.Errorf("disable-model-invocation = %#v, want bool true", v)
			}
		})
	}
	t.Run("last key replaced", func(t *testing.T) {
		t.Parallel()
		got := string(skillMarkdown(userSkillSource("---\nname: ship\ndescription: d\ndisable-model-invocation: false\n---\nBody.\n"), controlFrontmatter))
		want := "---\nname: ship\ndescription: d\ndisable-model-invocation: true\n---\nBody.\n"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("duplicate keys collapse to one", func(t *testing.T) {
		t.Parallel()
		in := "---\nname: ship\ndisable-model-invocation: false\ndescription: d\ndisable-model-invocation: true\n---\nBody.\n"
		got := string(skillMarkdown(userSkillSource(in), controlFrontmatter))
		want := "---\nname: ship\ndisable-model-invocation: true\ndescription: d\n---\nBody.\n"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("crlf line endings", func(t *testing.T) {
		t.Parallel()
		in := "---\r\nname: ship\r\ndisable-model-invocation: false\r\ndescription: d\r\n---\r\nBody.\r\n"
		got := string(skillMarkdown(userSkillSource(in), controlFrontmatter))
		want := "---\r\nname: ship\r\ndisable-model-invocation: true\r\ndescription: d\r\n---\r\nBody.\r\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("flow mapping is re-encoded", func(t *testing.T) {
		t.Parallel()
		in := "---\n{name: ship, description: d, disable-model-invocation: false}\n---\nBody.\n"
		got := skillMarkdown(userSkillSource(in), controlFrontmatter)
		fm := frontmatterOf(t, got)
		if fm[userOnlyKey] != true || fm["name"] != "ship" {
			t.Errorf("frontmatter = %#v, want name ship and the control true", fm)
		}
		if !strings.HasSuffix(string(got), "Body.\n") {
			t.Errorf("body lost:\n%s", got)
		}
	})
	t.Run("indented and duplicated flow keys are re-encoded", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{
			"---\n  name: ship\n  description: d\n  disable-model-invocation: false\n---\nBody.\n",
			"---\n{name: ship, disable-model-invocation: false, description: d, disable-model-invocation: no}\n---\nBody.\n",
		} {
			got := skillMarkdown(userSkillSource(in), controlFrontmatter)
			if fm := frontmatterOf(t, got); fm[userOnlyKey] != true || fm["description"] != "d" {
				t.Errorf("%q: frontmatter = %#v", in, fm)
			}
			if n := strings.Count(string(got), userOnlyKey); n != 1 {
				t.Errorf("%q: %d control keys in:\n%s", in, n, got)
			}
		}
	})
	for _, in := range []string{
		"---\nname: ship\ndisable-model-invocation: true # authored\ndescription: d\n---\nBody.\n",
		"---\nname: ship\nflag: &on true\ndisable-model-invocation: *on\ndescription: d\n---\nBody.\n",
	} {
		in := in
		t.Run("authored true kept byte for byte", func(t *testing.T) {
			t.Parallel()
			if got := string(skillMarkdown(userSkillSource(in), controlFrontmatter)); got != in {
				t.Errorf("got:\n%s\nwant the input unchanged", got)
			}
		})
	}
	t.Run("any skill passes an authored control through", func(t *testing.T) {
		t.Parallel()
		in := "---\nname: ship\ndisable-model-invocation: false\ndescription: d\n---\nBody.\n"
		src := Source{ArtifactID: "team/ship", ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"), SkillBytes: []byte(in)}
		if got := string(skillMarkdown(src, controlFrontmatter)); got != in {
			t.Errorf("got:\n%s\nwant the input unchanged", got)
		}
	})
	t.Run("controlNone adds nothing", func(t *testing.T) {
		t.Parallel()
		in := "---\nname: ship\ndescription: d\n---\nBody.\n"
		if got := string(skillMarkdown(userSkillSource(in), controlNone)); got != in {
			t.Errorf("got:\n%s\nwant the input unchanged", got)
		}
	})
	t.Run("control precedes derived compatibility", func(t *testing.T) {
		t.Parallel()
		src := Source{
			ArtifactID:    "team/ship",
			ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\ninvocation: user\nruntime_requirements:\n  python: \">=3.10\"\n---\n"),
			SkillBytes:    []byte("---\nname: ship\ndescription: d\n---\nBody.\n"),
		}
		got := string(skillMarkdown(src, controlFrontmatter))
		want := "---\ndisable-model-invocation: true\ncompatibility: \"Requires Python >=3.10\"\nname: ship\ndescription: d\n---\nBody.\n"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("unparseable frontmatter takes the injected line", func(t *testing.T) {
		t.Parallel()
		in := "---\nname: [ship\n---\nBody.\n"
		got := string(skillMarkdown(userSkillSource(in), controlFrontmatter))
		if !strings.HasPrefix(got, "---\ndisable-model-invocation: true\n") {
			t.Errorf("got:\n%s\nwant the control injected first", got)
		}
	})
	t.Run("no frontmatter is left alone", func(t *testing.T) {
		t.Parallel()
		if got := string(withUserOnlyFrontmatter([]byte("Body only.\n"))); got != "Body only.\n" {
			t.Errorf("got %q", got)
		}
	})
}

// openAIYAMLDecode strictly decodes an openai.yaml output and returns its
// policy.allow_implicit_invocation value and the decoded document.
func openAIYAMLDecode(t *testing.T, b []byte) (any, map[string]any) {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output does not strictly decode: %v\n%s", err, b)
	}
	if n := strings.Count(string(b), implicitInvocationKey); n != 1 {
		t.Errorf("output holds %d %s keys, want 1:\n%s", n, implicitInvocationKey, b)
	}
	switch policy := doc["policy"].(type) {
	case map[string]any:
		return policy[implicitInvocationKey], doc
	case map[any]any:
		// A policy with a non-string key decodes to map[any]any.
		return policy[implicitInvocationKey], doc
	}
	t.Fatalf("policy is %#v, want a mapping:\n%s", doc["policy"], b)
	return nil, nil
}

// Spec: §6.7 — the codex adapter merges policy.allow_implicit_invocation:
// false into an authored agents/openai.yaml that strictly decodes to a
// mapping, keeps every other key, and replaces every other input with the
// minimal policy file. No input makes Adapt fail.
func TestOpenAIYAML_Merge(t *testing.T) {
	t.Parallel()
	minimal := []string{
		"", "# comment only\n", "---\n", "~\n", "- a\n- b\n", "a: [\n", "just a scalar\n",
		"policy: {a: 1}\npolicy: {b: 2}\n",
		"policy:\n  allow_implicit_invocation: true\n  allow_implicit_invocation: true\n",
	}
	for _, in := range minimal {
		if got := string(openAIYAMLPolicy([]byte(in))); got != minimalOpenAIYAML {
			t.Errorf("openAIYAMLPolicy(%q) = %q, want the minimal file", in, got)
		}
	}
	for _, in := range []string{
		"interface:\n  display_name: Ship\n",
		"interface: x\npolicy:\n  allow_implicit_invocation: true\n",
		"interface: x\npolicy:\n  allow_implicit_invocation: \"true\"\n",
		"interface: x\npolicy:\n  allow_implicit_invocation: \"false\"\n",
		"interface: x\npolicy:\n  allow_implicit_invocation: yes\n",
		"interface: x\npolicy: off\n",
		"interface: x\npolicy:\n  1: one\n",
	} {
		v, doc := openAIYAMLDecode(t, openAIYAMLPolicy([]byte(in)))
		if v != false {
			t.Errorf("%q: allow_implicit_invocation = %#v, want bool false", in, v)
		}
		if doc["interface"] == nil {
			t.Errorf("%q: interface key dropped", in)
		}
	}
	t.Run("alias keeps the anchor", func(t *testing.T) {
		t.Parallel()
		in := "base: &p {allow_implicit_invocation: true, mode: x}\npolicy: *p\n"
		var doc map[string]any
		if err := yaml.Unmarshal(openAIYAMLPolicy([]byte(in)), &doc); err != nil {
			t.Fatal(err)
		}
		policy := doc["policy"].(map[string]any)
		base := doc["base"].(map[string]any)
		if policy[implicitInvocationKey] != false || policy["mode"] != "x" {
			t.Errorf("policy = %#v", policy)
		}
		if base[implicitInvocationKey] != true || base["mode"] != "x" {
			t.Errorf("base = %#v, want it unchanged", base)
		}
	})
	t.Run("later documents ignored", func(t *testing.T) {
		t.Parallel()
		in := "interface: x\n---\npolicy: {allow_implicit_invocation: true}\n"
		out := openAIYAMLPolicy([]byte(in))
		v, doc := openAIYAMLDecode(t, out)
		if v != false || len(doc) != 2 || strings.Contains(string(out), "---") {
			t.Errorf("got %q, want one document with interface and the policy", out)
		}
	})
	t.Run("Adapt replaces the authored file", func(t *testing.T) {
		t.Parallel()
		src := userSkillSource("---\nname: ship\ndescription: d\n---\nBody.\n")
		src.Resources = map[string][]byte{openAIYAMLPath: []byte("- not\n- a mapping\n"), "scripts/ship.sh": []byte("echo\n")}
		out, err := Codex{}.Adapt(context.Background(), src)
		if err != nil {
			t.Fatalf("Adapt: %v", err)
		}
		n := 0
		for _, f := range out {
			if strings.HasSuffix(f.Path, openAIYAMLPath) {
				n++
				if string(f.Content) != minimalOpenAIYAML {
					t.Errorf("%s = %q, want the minimal file", f.Path, f.Content)
				}
			}
		}
		if n != 1 {
			t.Errorf("want one agents/openai.yaml, got %d in %v", n, filePaths(out))
		}
		if len(src.Resources) != 2 {
			t.Errorf("Adapt mutated the caller's resource map: %v", src.Resources)
		}
	})
}

// Spec: §6.7, §4.3.4 — a skill whose invocation is any keeps an authored
// agents/openai.yaml byte for byte on the codex adapter and the Codex
// marketplace emitter, and gains none when it bundles none.
func TestCodex_AnySkillKeepsAuthoredOpenAIYAML(t *testing.T) {
	t.Parallel()
	authored := []byte("interface:\n  display_name: Ship\npolicy:\n  allow_implicit_invocation: true\n")
	for _, skillMD := range []string{
		"---\nname: ship\ndescription: d\n---\nBody.\n",
		"---\nname: ship\ndescription: d\nmetadata:\n  invocation: \"any\"\n---\nBody.\n",
	} {
		for _, withFile := range []bool{true, false} {
			src := Source{ArtifactID: "team/ship", ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"), SkillBytes: []byte(skillMD), Plugin: finPlugin("codex")}
			if withFile {
				src.Resources = map[string][]byte{openAIYAMLPath: authored}
			}
			adapted, err := Codex{}.Adapt(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			emitted, err := CodexMarketplace{}.Component(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			for path, out := range map[string][]File{
				".agents/skills/ship/agents/openai.yaml":            adapted,
				"codex/finance-pack/skills/ship/agents/openai.yaml": emitted,
			} {
				n := 0
				for _, f := range out {
					if strings.HasSuffix(f.Path, openAIYAMLPath) {
						n++
					}
				}
				f, found := findFile(out, path)
				switch {
				case withFile && (!found || n != 1 || string(f.Content) != string(authored)):
					t.Errorf("%s: want the authored file once, got %v", path, filePaths(out))
				case !withFile && n != 0:
					t.Errorf("%s: want no agents/openai.yaml, got %v", path, filePaths(out))
				}
			}
		}
	}
}

// Spec: §4.3.4 — a skill whose SKILL.md does not resolve takes the
// ARTIFACT.md invocation value, and the any default when that file states
// none.
func TestUserInvoked_UnresolvableSkillTakesArtifactValue(t *testing.T) {
	t.Parallel()
	bad := []byte("---\nname: [ship\n---\nBody.\n")
	user := Source{ArtifactID: "team/ship", ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\ninvocation: user\n---\n"), SkillBytes: bad}
	anyS := Source{ArtifactID: "team/ship", ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"), SkillBytes: bad}
	out, err := OpenCode{}.Adapt(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findFile(out, ".opencode/commands/ship.md"); !ok || len(out) != 1 {
		t.Errorf("user: want only .opencode/commands/ship.md, got %v", filePaths(out))
	}
	out, err = OpenCode{}.Adapt(context.Background(), anyS)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findFile(out, ".opencode/skills/ship/SKILL.md"); !ok || len(out) != 1 {
		t.Errorf("any: want only .opencode/skills/ship/SKILL.md, got %v", filePaths(out))
	}
	if userInvoked(Source{ArtifactBytes: []byte("no frontmatter"), SkillBytes: bad}) {
		t.Error("an unparseable ARTIFACT.md resolved to user")
	}
}

// Spec: §4.3.4, §6.7 — the opencode command-file fallback carries the
// derived compatibility line, and keeps an authored one unchanged.
func TestOpenCode_UserInvokedDerivesCompatibility(t *testing.T) {
	t.Parallel()
	art := []byte("---\ntype: skill\nversion: 1.0.0\ninvocation: user\nruntime_requirements:\n  node: \">=20\"\n---\n")
	for _, tc := range []struct{ skill, want string }{
		{"---\nname: ship\ndescription: d\n---\nBody.\n", "---\ncompatibility: \"Requires Node >=20\"\nname: ship\ndescription: d\n---\nBody.\n"},
		{"---\nname: ship\ndescription: d\ncompatibility: Node 18\n---\nBody.\n", "---\nname: ship\ndescription: d\ncompatibility: Node 18\n---\nBody.\n"},
	} {
		out, err := OpenCode{}.Adapt(context.Background(), Source{ArtifactID: "team/ship", ArtifactBytes: art, SkillBytes: []byte(tc.skill)})
		if err != nil {
			t.Fatal(err)
		}
		f, ok := findFile(out, ".opencode/commands/ship.md")
		if !ok || string(f.Content) != tc.want {
			t.Errorf("got %v %q, want %q", filePaths(out), f.Content, tc.want)
		}
	}
}

// Spec: §4.3.4, §4.4.2, §6.7 — the claude-code skill arm applies both the
// user-only control and the provenance rewrite.
func TestClaudeCode_UserInvokedSkill(t *testing.T) {
	t.Parallel()
	src := userSkillSource("---\nname: ship\ndescription: d\n---\n\n<!-- begin imported source=\"https://wiki.example/x\" -->\nX\n<!-- end imported -->\n")
	src.Resources = map[string][]byte{"scripts/ship.sh": []byte("echo\n")}
	out, err := ClaudeCode{}.Adapt(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(out, ".claude/skills/ship/SKILL.md")
	if !ok {
		t.Fatalf("no SKILL.md in %v", filePaths(out))
	}
	if !strings.HasPrefix(string(f.Content), "---\ndisable-model-invocation: true\n") {
		t.Errorf("control missing:\n%s", f.Content)
	}
	if !strings.Contains(string(f.Content), "<untrusted-data") {
		t.Errorf("provenance not rewritten:\n%s", f.Content)
	}
	if _, ok := findFile(out, ".claude/skills/ship/scripts/ship.sh"); !ok {
		t.Errorf("resource missing: %v", filePaths(out))
	}
}

// Spec: §4.3.4, §7.8 — the Claude plugin skill carries the user-only control.
func TestClaudePluginSkill_UserInvoked(t *testing.T) {
	t.Parallel()
	src := userSkillSource("---\nname: ship\ndescription: d\n---\nBody.\n")
	src.Plugin = finPlugin("claude")
	out, err := ClaudeMarketplace{}.Component(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(out, "claude/finance-pack/skills/ship/SKILL.md")
	if !ok || !strings.HasPrefix(string(f.Content), "---\ndisable-model-invocation: true\n") {
		t.Errorf("got %v:\n%s", filePaths(out), f.Content)
	}
}

// Spec: §6.7 — the gemini command file carries the SKILL.md description and
// body, and the whole SKILL.md when it does not parse.
func TestGeminiSkillCommandTOML(t *testing.T) {
	t.Parallel()
	got := string(geminiSkillCommandTOML(userSkillSource("---\nname: ship\ndescription: Ship it.\n---\nRun $ARGUMENTS.\n")))
	want := "description = \"Ship it.\"\nprompt = \"\"\"\nRun $ARGUMENTS.\n\n\"\"\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	bad := "---\nname: [ship\n---\nBody.\n"
	got = string(geminiSkillCommandTOML(userSkillSource(bad)))
	if strings.Contains(got, "description =") || !strings.Contains(got, "name: [ship") {
		t.Errorf("unparseable SKILL.md: got %q", got)
	}
}

// Spec: §6.7 — claude-cowork has no project-scope output for any type.
func TestClaudeCowork_EmitsNothing(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{"skill", "agent", "rule", "hook", "mcp-server", "context", "command", "dataset"} {
		out, err := ClaudeCowork{}.Adapt(context.Background(), typeSource(ty))
		if err != nil || len(out) != 0 {
			t.Errorf("%s: got %v, %v; want no files", ty, filePaths(out), err)
		}
	}
}

// Spec: §4.1, §6.4 — an overlay artifact of a removed type reaches the
// adapters as an extension type: none writes the canonical layout, claude-code
// writes it under .claude/podium/<id>/, and every other adapter writes nothing.
// No adapter keeps a context or command arm.
func TestAdapters_RemovedTypeAsExtension(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{"command", "context"} {
		src := Source{
			ArtifactID:    "team/legacy-" + ty,
			ArtifactBytes: []byte("---\ntype: " + ty + "\nversion: 1.0.0\ndescription: legacy\n---\n\nBody.\n"),
			Resources:     map[string][]byte{"notes.md": []byte("n\n")},
		}
		for _, id := range append([]string{"none"}, firstClassAdapters...) {
			out := adaptSrc(t, id, src)
			for _, f := range out {
				if strings.Contains(f.Path, ".podium/context/") || strings.Contains(f.Path, "/commands/") || strings.Contains(f.Path, ".pi/prompts/") {
					t.Errorf("%s %s: removed-type output path %s", id, ty, f.Path)
				}
			}
			switch id {
			case "none":
				if _, ok := findFile(out, "team/legacy-"+ty+"/ARTIFACT.md"); !ok {
					t.Errorf("none %s: want the canonical layout, got %v", ty, filePaths(out))
				}
			case "claude-code":
				base := ".claude/podium/team/legacy-" + ty + "/"
				_, art := findFile(out, base+"ARTIFACT.md")
				_, res := findFile(out, base+"notes.md")
				if !art || !res || len(out) != 2 {
					t.Errorf("claude-code %s: got %v", ty, filePaths(out))
				}
			default:
				if len(out) != 0 {
					t.Errorf("%s %s: want no files, got %v", id, ty, filePaths(out))
				}
			}
		}
	}
}

// Spec: §7.8 — each emitter renders a user-invoked skill per §7.8: the Claude,
// Cursor, and Pi skills carry the control, Codex adds agents/openai.yaml,
// Gemini writes commands/<n>.toml and omits the resources, and the Hermes tap
// omits the skill.
func TestEmitters_UserInvokedSkill(t *testing.T) {
	t.Parallel()
	mk := func(prefix string) Source {
		src := userSkillSource("---\nname: ship\ndescription: d\n---\nBody.\n")
		src.Resources = map[string][]byte{"scripts/ship.sh": []byte("echo\n")}
		src.Plugin = finPlugin(prefix)
		return src
	}
	control := "---\ndisable-model-invocation: true\n"
	cases := []struct {
		emitter MarketplaceEmitter
		prefix  string
		check   func(t *testing.T, out []File)
	}{
		{ClaudeMarketplace{}, "claude", func(t *testing.T, out []File) {
			f, ok := findFile(out, "claude/finance-pack/skills/ship/SKILL.md")
			if !ok || !strings.HasPrefix(string(f.Content), control) {
				t.Errorf("claude: %v", filePaths(out))
			}
		}},
		{CursorMarketplace{}, "cursor", func(t *testing.T, out []File) {
			f, ok := findFile(out, "cursor/finance-pack/skills/ship/SKILL.md")
			if !ok || !strings.HasPrefix(string(f.Content), control) {
				t.Errorf("cursor: %v", filePaths(out))
			}
		}},
		{PiPackage{}, "pi", func(t *testing.T, out []File) {
			f, ok := findFile(out, "skills/ship/SKILL.md")
			if !ok || !strings.HasPrefix(string(f.Content), control) {
				t.Errorf("pi: %v", filePaths(out))
			}
		}},
		{CodexMarketplace{}, "codex", func(t *testing.T, out []File) {
			f, ok := findFile(out, "codex/finance-pack/skills/ship/agents/openai.yaml")
			skill, sok := findFile(out, "codex/finance-pack/skills/ship/SKILL.md")
			if !ok || string(f.Content) != minimalOpenAIYAML || !sok || strings.Contains(string(skill.Content), userOnlyKey) {
				t.Errorf("codex: %v", filePaths(out))
			}
		}},
		{GeminiExtension{}, "gemini", func(t *testing.T, out []File) {
			if _, ok := findFile(out, "commands/ship.toml"); !ok || len(out) != 1 {
				t.Errorf("gemini: want only commands/ship.toml, got %v", filePaths(out))
			}
		}},
		{HermesTap{}, "hermes", func(t *testing.T, out []File) {
			if len(out) != 0 {
				t.Errorf("hermes: want no files, got %v", filePaths(out))
			}
		}},
	}
	for _, tc := range cases {
		out, err := tc.emitter.Component(context.Background(), mk(tc.prefix))
		if err != nil {
			t.Fatalf("%s: %v", tc.emitter.ID(), err)
		}
		tc.check(t, out)
	}
	// A skill whose invocation is any has no Gemini extension component and
	// stays in the Hermes tap.
	anySkill := Source{ArtifactID: "team/ship", ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"), SkillBytes: []byte("---\nname: ship\ndescription: d\n---\nBody.\n")}
	if out, _ := (GeminiExtension{}).Component(context.Background(), anySkill); len(out) != 0 {
		t.Errorf("gemini any skill: got %v", filePaths(out))
	}
	if out, _ := (HermesTap{}).Component(context.Background(), anySkill); len(out) != 1 {
		t.Errorf("hermes any skill: got %v", filePaths(out))
	}
}
