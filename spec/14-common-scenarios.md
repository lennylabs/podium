# 14. Common Scenarios

End-to-end walkthroughs for common ways Podium gets used. Each scenario links to the relevant detail sections; the steps below show what an operator or developer actually types.

## 14.1 Local registry folder, one-shot, single workspace

A developer keeps artifacts in a local folder and materializes a subset into one project for Claude Code. Filesystem-source registry (§13.11). No daemon, single CLI invocation.

**One-time:**

1. Lay out artifacts in `~/podium-artifacts/` per §4.2 (each artifact a subdirectory containing `ARTIFACT.md`, plus `SKILL.md` for skills). The path is interpreted as a single `local`-source layer rooted at `~/podium-artifacts/`; for a multi-layer setup (each subdirectory a separate layer) opt in via `<path>/.registry-config` with `multi_layer: true` per §13.11.1.
2. `podium init --global --registry ~/podium-artifacts/` to write `~/.podium/sync.yaml` with `defaults.registry: ~/podium-artifacts/`. No server needed; the client reads the directory directly.

**Per project:**

3. `cd ~/projects/myapp/`.
4. Optional `.podium/sync.yaml`:
   ```yaml
   profiles:
     myapp:
       include: ["finance/**", "shared/policies/*"]
       exclude: ["finance/**/legacy/**"]
   ```
5. `podium sync --harness claude-code --profile myapp`. Default target = CWD; lock file at `<cwd>/.podium/sync.lock`.

**When to graduate to a server:** if the developer wants `load_artifact` lazy-loaded by Claude Code (progressive disclosure via MCP), they migrate by running `podium serve --standalone --layer-path ~/podium-artifacts/` and updating `~/.podium/sync.yaml` to point at `http://127.0.0.1:8080`. Same artifact directory; just add a daemon. See §13.11.6.

**Changing the subset:** edit `sync.yaml` (or `podium profile edit myapp ...`), re-run `podium sync`. Ad-hoc adjustments: `podium sync override --add/--remove`.

## 14.2 Local registry folder, one-shot, multiple workspaces

Same setup. Each project workspace runs sync independently.

```bash
cd ~/projects/finance-app/    && podium sync --harness claude-code --profile finance
cd ~/projects/marketing-tool/ && podium sync --harness claude-code --profile marketing
```

Each workspace has its own `<workspace>/.podium/sync.lock`. Both read from the same artifact directory; with filesystem source, no shared cache or registry process is needed (the source files are the cache).

## 14.3 Local registry folder, MCP discovery, multiple workspaces

Same artifact directory, but now the developer wants progressive disclosure (the agent calls `load_domain` / `search_domains` / `search_artifacts` / `load_artifact` at runtime rather than working from pre-materialized files). MCP requires a server, so this scenario graduates from filesystem source (§13.11) to standalone server (§13.10):

1. `podium serve --standalone --layer-path ~/podium-artifacts/`: starts the server against the same directory; auto-bootstraps `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080`.
2. Per project, add a Podium entry to the harness's MCP config (snippets per §6.11). The MCP server picks up the registry from `~/.podium/sync.yaml` (`defaults.registry`), so no extra env var is needed.
3. Optional `.podium/overlay/` per workspace for in-progress artifacts.

## 14.4 Local registry, custom harness via SDK, multiple workspaces

Each workspace runs an app built on `podium-py` (or `podium-ts`) plus an agent SDK like Claude Agent SDK. The SDK speaks HTTP, so this scenario uses a standalone server (graduated from §14.1's filesystem source; same artifact directory, just wrap it in `podium serve --standalone`).

```python
from podium import Client
from claude_agent_sdk import ...

client = Client.from_env()         # picks up registry URL from sync.yaml + overlay path
# ... custom discovery logic, search_artifacts → load_artifact → agent execution
```

## 14.5 Remote registry, one-shot, multiple workspaces

**Operator (centralized):**

1. Deploy the registry per §13.1 with chosen vector backend, embedding provider, OIDC IdP, S3.
2. Configure the tenant's layer list with Git-source layers and visibility rules (§4.6).
3. Set Git webhooks pointing at the ingest endpoint (§7.3.1).

**Per developer:**

4. `podium init --global --registry https://podium.acme.com`.
5. `podium login`: completes the device-code flow once, caches the token.
6. `cd <project>`, write `.podium/sync.yaml` with a profile, `podium sync --harness claude-code --profile <name>`.
7. Repeat per workspace; each has its own lock file.

## 14.6 Remote registry + workspace local overlay, one-shot, multiple workspaces

Operator setup as in §14.5. Per workspace:

1. Drop in-progress artifacts under `<workspace>/.podium/overlay/`.
2. `podium sync --harness claude-code --profile <name>`. The overlay path auto-resolves to `<CWD>/.podium/overlay/` per §6.4; no env var needed.

## 14.7 Remote registry + local overlay, MCP, multiple workspaces

Operator setup as in §14.5. Per workspace:

1. Configure the harness's MCP server entry (§6.11) with `PODIUM_REGISTRY` and `PODIUM_HARNESS`. `PODIUM_OVERLAY_PATH` is optional; when unset, the MCP server resolves the overlay from MCP roots (§6.4).
2. First call triggers OAuth device-code via MCP elicitation. Token caches in the OS keychain.
3. Drop workspace-local artifacts under `.podium/overlay/`. The MCP server's fsnotify watcher picks up changes.

## 14.8 Remote registry + local overlay, custom harness via SDK, multiple workspaces

Operator setup as in §14.5. Per workspace:

```python
client = Client(
    registry="https://podium.acme.com",
    identity_provider="oauth-device-code",
    overlay_path="./.podium/overlay/",
)
client.login()   # device-code flow before any catalog calls
# ... use search_artifacts / load_artifact in your runtime
```

## 14.9 Enterprise multi-layer setup

**Operator:**

1. Deploy registry with full stack: Postgres + chosen vector backend, S3, OIDC IdP with SCIM push.
2. Configure multiple Git repos as layers, with visibility per §4.6:
   ```yaml
   layers:
     - id: org-defaults
       source: { git: { repo: ..., ref: main, root: artifacts/ } }
       visibility: { organization: true }
     - id: team-finance
       source: { git: { repo: ..., ref: main } }
       visibility: { groups: [acme-finance] }
     - id: public-marketing
       source: { git: { repo: ..., ref: main } }
       visibility: { public: true }
   ```
3. Configure freeze windows, admin grants, signing (Sigstore-keyless or registry-managed).
4. `podium lint` runs as a required CI check on each layer's repo.

**Per author:** edit artifacts in the team's Git repo, open PR, merge. Webhook fires; registry ingests.

**Per consumer:** authenticates via OIDC, runs `podium sync` / MCP / SDK as in §14.5–14.8. Effective view composes admin layers (visibility-filtered) + user-defined layers + workspace local overlay.

**Personal user-defined layers:** `podium layer register --id my-experiments --repo git@github.com:joan/podium-experiments.git --ref main`. Capped at 3 per identity by default.

## 14.10 Standalone registry with a Git-source layer

A single-developer setup that mirrors a public Git repo (e.g., a community library) into a local standalone registry.

**One-time:**

1. `podium serve --standalone --layer-path ~/podium-artifacts` (single-binary server with the local layer for personal artifacts; auto-bootstraps `~/.podium/sync.yaml` pointing at the local server).
2. Register the Git layer:
   ```bash
   podium layer register --id community-skills \
     --repo https://github.com/podium-community/skills.git --ref main
   ```
3. The CLI prints the webhook URL it would expect; on a developer machine without a public ingress, ignore the webhook and pull manually instead:
   ```bash
   podium layer reingest community-skills
   # or, for periodic sync:
   podium layer watch community-skills --interval 1h
   ```

**Per project:** `podium sync` as in §14.1, scoping to the layers and paths the developer wants.

## 14.11 CI / build pipeline materialization

A build pipeline materializes a deterministic artifact set into a deploy artifact (e.g., a Docker image) without device-code interaction.

**Pipeline setup:**

1. CI obtains a runtime-issued JWT (per `injected-session-token`, §6.3.2). The runtime's signing key is registered with the registry one-time.
2. Pipeline step:
   ```bash
   export PODIUM_REGISTRY=https://podium.acme.com
   export PODIUM_IDENTITY_PROVIDER=injected-session-token
   export PODIUM_SESSION_TOKEN_FILE=/run/secrets/podium-token
   podium sync --harness claude-code --profile production --target ./build/.claude/
   ```
3. The lock file (`./build/.claude/.podium/sync.lock`) captures exactly which `(artifact_id, version, content_hash)` triples landed in the image. Commit it alongside the build for reproducibility.

`podium sync --dry-run --json` is useful in pre-flight to sanity-check what the build will include.

## 14.12 Air-gapped enterprise

Registry runs entirely on an internal network with no public ingress.

**Operator:**

1. Deploy registry per §13.1 inside the internal network. Identity via the org's internal OIDC IdP. Object storage on internal S3-compatible storage (MinIO or similar).
2. Layer Git repos hosted on internal Git server (GitLab/Gitea/internal GitHub Enterprise). Webhooks reach the registry over the internal network only.
3. Embedding provider: `embedded-onnx` (no external API calls) or `ollama` pointed at a local model server. Vector backend: pgvector (no external service).
4. Sigstore-keyless requires public OIDC infrastructure; air-gapped deployments use the registry-managed signing key path instead.

**Consumers:** internal endpoint only; OIDC flow stays inside the network.

## 14.13 Mixed-harness developer

A single developer uses Claude Code on one project and Cursor on another, sharing one OAuth identity and the same content cache.

1. `podium login --registry https://podium.acme.com` once. Token caches in the OS keychain.
2. Project A: Claude Code with the §6.11 Claude Code MCP snippet. Workspace overlay at `<project-a>/.podium/overlay/`.
3. Project B: Cursor with the §6.11 Cursor MCP snippet. Workspace overlay at `<project-b>/.podium/overlay/`.
4. Both share the same OS-keychain token, the same `~/.podium/cache/`, and (if so configured) the same user-wide audit log at `~/.podium/audit.log`.

## 14.14 Promote-to-shared workflow

A developer iterates on a new artifact in their workspace overlay, then promotes it to a shared layer.

1. Edit `<workspace>/.podium/overlay/finance/cashflow/forecast/ARTIFACT.md` (and `SKILL.md` if `forecast` is a skill). The MCP server (or sync, or SDK) sees it immediately.
2. Test the artifact end-to-end in the workspace.
3. When ready, `git mv` (or copy) the artifact into the team's Git layer repo, commit, open PR.
4. PR runs `podium lint` and any team-specific checks. Reviewers approve, merge.
5. Webhook fires; registry ingests.
6. Remove the now-redundant copy from `.podium/overlay/`.

`podium sync save-as` is the alternative path for capturing a curated set of overrides as a profile in `sync.yaml` rather than promoting individual artifacts to a shared layer.

## 14.15 Read-only viewer / auditor

A user with read-only visibility to several layers wants to browse the catalog without running sync or an MCP server.

```bash
export PODIUM_REGISTRY=https://podium.acme.com
podium login

podium domain show                                          # top-level map
podium domain show finance/close-reporting                  # drill in
podium search "variance analysis" --type skill --json
podium artifact show finance/close-reporting/run-variance-analysis --version 1.2.0
```

`podium sync --dry-run` provides the same view in materialization form (resolved profile + scope) without writing anything to disk.
