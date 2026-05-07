# Small-team rollout

This is a practical guide for getting Podium running for a small team — roughly 3 to 10 people, one or two harnesses (Claude Code and/or Cursor, typically), one shared catalog. If you're rolling out to a larger org, this guide will be too informal; the [operator's guide](operator-guide.md) is more useful starting around 20 people or wherever governance, on-call, and multi-tenant concerns enter the picture.

The shape: one platform-engineer-equivalent (you, probably) does the setup over an afternoon; the rest of the team starts authoring within a week.

## Three deployment shapes — pick one

Before installing anything, decide which deployment shape fits your team:

1. **Filesystem registry (lightest).** No daemon, no port. The catalog is a directory tree committed to git; every developer who clones the repo has the same catalog. Each person runs `podium sync` on their own machine to materialize the artifacts into their harness's directory. _Best when_: the team is fine with eager materialization (no need for the agent to lazy-load capabilities mid-session), and authoring goes through normal git PRs.
2. **Standalone server on a VM.** A single `podium serve --standalone` instance behind your VPN. Adds progressive disclosure — agents call MCP meta-tools at runtime to load capabilities incrementally, instead of materializing everything ahead of time. _Best when_: catalogs grow past a few hundred artifacts (eager materialization starts feeling heavy), or you want a single audit log capturing every load across the team.
3. **Standard deployment** (Postgres + S3 + OIDC + Helm). Heavier ops; the right call once you need OIDC identity-based visibility filtering, multi-tenancy, or production-grade availability. See the [operator's guide](operator-guide.md).

For most small teams, **start with the filesystem registry**. It has zero infrastructure, full git-native workflow, and you can graduate to a standalone server later by running `podium serve --standalone --layer-path /path/to/.podium/registry/` against the same directory — no migration required.

## Filesystem registry path

### Day 1 — set up the shared registry repo

Create a git repo for the shared catalog. The structure mirrors the `.podium/registry/` layout (each subdirectory is a layer; each artifact is a directory with `ARTIFACT.md`):

```
podium-artifacts/                 # the git repo
├── team-shared/                   # one layer
│   ├── DOMAIN.md
│   ├── finance/
│   │   └── close-reporting/
│   │       └── run-variance-analysis/
│   │           └── ARTIFACT.md
│   └── platform/
│       └── …
└── README.md
```

Push it to GitHub / GitLab / wherever the team lives.

### Day 1 — pick a workspace convention

Each developer who uses Podium will clone this repo (or a project repo that includes it as a submodule / vendored copy). Decide where in their workspace the registry lives. Two common patterns:

- **Per-project clone**: each consuming project has its own `.podium/registry/` (cloned from the shared repo, or vendored). Self-contained — the project repo carries everything.
- **Shared local clone**: every developer clones the registry repo once into `~/podium-artifacts/`, and every consuming project's `<workspace>/.podium/sync.yaml` points at that path. Saves disk space; updates via `git pull`.

Either works. Per-project is simpler; shared local is lighter when the registry is large.

### Day 1 — write the project sync.yaml

In each consuming project, commit `<workspace>/.podium/sync.yaml`:

```yaml
defaults:
  registry: ./.podium/registry/    # per-project clone
  # or: registry: ~/podium-artifacts/   # shared local clone
  harness: claude-code
  target: .claude/

profiles:
  default:
    include: ["team-shared/**"]
```

Git-ignore the local override file:

```
# .gitignore additions
.podium/sync.local.yaml
.podium/overlay/
.podium/sync.lock
```

`podium init` (run inside the project) writes both the sync.yaml and the gitignore entries automatically.

### Day 2 — onboard one person end-to-end

Before sending anything to the team, walk one person through it yourself. Catch the stupid stuff early.

For that person:

1. Install `podium`. (`podium-mcp` not needed for filesystem source.)
2. Clone the project (which includes the committed `<workspace>/.podium/sync.yaml`) and the shared registry (if separate).
3. `cd <project>` and run `podium config show` — confirm the merged config has the right registry path and harness.
4. `podium sync` — should materialize artifacts into `.claude/`.
5. Open Claude Code in the project; confirm the artifacts are usable.

If anything is awkward, fix the project's sync.yaml before scaling up. Common issues at this step are wrong relative paths in `defaults.registry` and missing `.gitignore` entries.

### Week 1 — onboard the rest

Send the team:

```
Podium is set up for this project — it materializes shared skills/agents/contexts
into .claude/ on demand. To get started:

1. Install podium (see <link>).
2. cd <project> && podium sync
3. Open Claude Code in the project. The skills should be available immediately.

To author a new skill, drop a directory under .podium/registry/team-shared/<your-domain>/
with an ARTIFACT.md, open a PR. After merge, everyone runs `git pull && podium sync`
and they pick it up.

Questions: <Slack channel or email>.
```

For watch-mode iteration, `podium sync --watch` re-materializes on every change. Useful while authoring.

## When to graduate to a standalone server

The filesystem registry covers most small-team eager workflows. Move to a standalone server when:

- **You want progressive disclosure.** Agents call MCP meta-tools at runtime to load only the capabilities they need for the current task, instead of materializing everything. This is the dominant trigger as catalogs grow past a few hundred artifacts.
- **You want a single audit log** capturing every load across the team, independent of any particular developer's machine.

Migration is mechanical:

1. On a chosen host, run `podium serve --standalone --layer-path /path/to/podium-artifacts/`. The host can be a small VM behind your VPN, or any always-on machine.
2. Each developer changes their `<workspace>/.podium/sync.yaml`:
   - Replace `defaults.registry: ./.podium/registry/` (or whatever path) with `defaults.registry: https://podium.your-team.example`.
   - Optional: add Podium MCP server to the harness config (snippets in §6.11 of the spec) so the agent can call meta-tools at runtime.
3. Done. Authoring loop unchanged — still a git PR + merge against the same registry repo. (The standalone server picks up changes via `podium layer reingest` or watcher.)

For OIDC identity-based visibility filtering or multi-tenancy, follow the [operator's guide](operator-guide.md) — that's the standard deployment.

## What this guide deliberately doesn't cover

- **Multi-tenancy.** Don't enable it until you have two or more meaningfully separate audiences. Filesystem source and standalone are both single-tenant by definition.
- **OIDC and identity-based visibility.** Out of scope for filesystem and standalone (no auth in either). When you actually need different developers to see different subsets of the catalog (and git-repo permissions aren't enough), graduate to a standard deployment.
- **Signing.** The default for filesystem and standalone (`PODIUM_VERIFY_SIGNATURES=never`) is fine until you have content where authorship integrity matters (e.g., production runbooks). When you do, set it to `medium-and-above` and start signing artifacts above that sensitivity.
- **SBOM / CVE tracking.** Out of scope until you're shipping skills that pull in third-party Python or shell scripts and want them tracked.
- **Freeze windows.** A small team doesn't need them.

When any of these starts hurting, read the relevant spec section and turn it on.
