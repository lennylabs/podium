# Manual validation scenarios

This document is a set of end-to-end scenarios for validating a Podium build by
hand. Each scenario is a self-contained sequence a person runs in a terminal,
observes, and checks against an explicit list of expected results. The
scenarios cover the deployment modes (solo filesystem, standalone server, and
standard server), embeddings on and off, the local and managed vector backends,
single and multiple layers backed by real Git repositories, the four harness
adapters, and the governance features (per-caller visibility, admin RBAC,
signing, public mode, lifecycle, and migration). Later scenarios cover domain
modeling and discovery, authoring guardrails, sync profiles and scope filtering,
reverse-dependency impact, webhook-driven reingest, audit and right-to-be-forgotten
erasure, workspace overlays, offline-cache resilience, and importing an existing
skill tree.

## How to use this document

### Build the binaries under test

```bash
cd ~/projects/podium
make build
```

`make build` writes `bin/podium`, `bin/podium-server`, and `bin/podium-mcp`.
Every scenario uses these fresh binaries. A stale `podium` earlier on `PATH`
(for example a Homebrew install at `/opt/homebrew/bin/podium`) produces
misleading results, so each scenario puts `bin/` first on `PATH` and the index
below assumes that.

### Shell

The fenced blocks run under bash and under zsh, which is the macOS login shell.
Three differences between the two shells have produced silent failures in these
scenarios before, so a block that behaves differently under one of them is a
defect in this document. zsh does not word-split an unquoted expansion, it
reserves `GID` as a read-only parameter holding the process group ID, and it
expands `${1:+-H "Authorization: Bearer $1"}` as a single word, which drops the
header and sends an authenticated caller to the registry as anonymous. Each of
those reads as a product defect at the point it bites.

### Per-scenario isolation

Run each scenario in a fresh shell and start with this block. It redirects all
server and client state into a throwaway directory so the run never touches the
real `~/.podium`, and it puts the fresh build first on `PATH`.

```bash
export REAL_HOME="$HOME"
export PODIUM_BIN="$HOME/projects/podium/bin"
export PATH="$PODIUM_BIN:$PATH"; hash -r
export WORK="$(mktemp -d)"
export PODIUM_SQLITE_PATH="$WORK/podium.db"
export PODIUM_FILESYSTEM_ROOT="$WORK/objects"
export PODIUM_AUDIT_LOG_PATH="$WORK/audit.log"
export PODIUM_CACHE_DIR="$WORK/cache"
export PODIUM_TOKEN_KEYCHAIN_NAME="podium-manual-$$"
export PODIUM_SIGN_KEY_PATH="$WORK/registry-signing.key"
export GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" GOENV="$(go env GOENV)"
export DOCKER_CONFIG="${DOCKER_CONFIG:-$HOME/.docker}"
export HOME="$WORK/home"; mkdir -p "$HOME"
cd "$WORK"
unset PODIUM_REGISTRY PODIUM_HARNESS PODIUM_SESSION_TOKEN
server_alive() {
  until grep -q 'podium-server listening on ' "$2" 2>/dev/null || ! kill -0 "$1" 2>/dev/null; do sleep 1; done
  kill -0 "$1" 2>/dev/null || { echo "FAIL: server pid $1 exited; read $2" >&2; tail -n 5 "$2" >&2; return 1; }
}
which podium    # must print $PODIUM_BIN/podium
```

The registry signs at ingest by default and generates its signing key on first
run, and a standalone start bootstraps `~/.podium/sync.yaml`, so the block moves
`HOME` into `$WORK` and names the key file there. `PODIUM_SIGN_KEY_PATH` also
keeps a default-mode `podium serve` from being refused for a key that would sit
outside the directory holding `$PODIUM_SQLITE_PATH`, and a `podium-mcp` in the
same shell resolves its verification key from the same file. `REAL_HOME` keeps
the operator's home for the repository paths the scenarios reach, and the Go
and Docker exports keep the module cache, the build cache, and the Docker CLI's
plugins where they were, so `go run` and `docker compose` work under the new
`HOME`. The block sets these before it moves `HOME`, because each expands the
operator's real home. The block ends in `$WORK`, because `podium-mcp` and
the `podium` CLI discover a workspace `.podium/sync.yaml` by walking up from the
current directory, and a walk from the repository reaches the operator's real
`~/.podium/sync.yaml`, whose `defaults.verify_signatures` or
`defaults.registry` would then configure the run. A step that needs the
repository as its working directory names it: `go -C "$REAL_HOME/projects/podium"`
for `go run`, and a subshell `(cd "$REAL_HOME/projects/podium" && ...)` for
`make` and `docker compose`, so the scenario's shell stays in `$WORK`. A scenario whose subject is an unsigned registry sets
`PODIUM_SIGN=none` in its own steps, and S19, S64, S66, and S68 are the
scenarios that do.

Confirm `which podium` prints the path under `$PODIUM_BIN`. If it prints a
Homebrew or other path, the `PATH` export did not take; open a new shell and
repeat.

### Conventions

- Command flags come before positional arguments. `podium search --registry
  "$URL" "query"` works; `podium search "query" --registry "$URL"` does not.
- Server scenarios start `podium serve` in the background and bind a loopback
  port. The cleanup step stops the server and removes `$WORK`.
- Each scenario binds loopback ports that no other scenario binds. The
  exceptions are the scenarios that run against another scenario's running
  stack: S45 runs on S21's registry, S47 to S50, S55 to S57, S59, and S60 run
  on S44's, and S52 to S54 run on S51's. The Postgres and MinIO ports that
  `make services-up` publishes are shared infrastructure rather than a
  scenario's own bind.
- After a step's `/healthz` poll, run `server_alive "$SRV" <log>`, which the
  isolation block defines, with the PID variable and the log file the step
  recorded. The poll alone cannot tell the scenario's server from another
  process on the same port: a `podium serve` that fails to bind exits, and
  every later command then reads whatever else answers there. The function
  waits for the server's own `podium-server listening on` log line, which the
  server prints only after a successful bind, and then confirms the process
  is alive with `kill -0`. When the process has exited, it prints `FAIL:
  server pid <pid> exited` and the last lines of the log, and returns 1. Stop
  the scenario there and read the log. A step that starts a server by
  reference to another scenario runs the same check.
- A registry directory is a tree of artifact directories. `podium artifact
  scaffold --type <type> <path>` writes one artifact at `<path>`; the artifact
  name is the last path element.
- Scenarios that need live infrastructure name it under Prerequisites. When the
  infrastructure or credentials are absent, the scenario is skipped rather than
  forced. Record the skip and the reason.
- A fenced block nested under a numbered step is indented to sit inside that
  step. The indentation is markdown structure and is not part of the command:
  strip it before running a heredoc, or the document's leading spaces land
  inside the here-document and a YAML body becomes invalid.
- Read an HTTP scenario's error code rather than its status class. A mistyped
  route returns 404, which satisfies a check written for "a 4xx" while carrying
  none of the behavior the step is testing.
- Stop a scenario's server by the PID the scenario recorded. A pattern kill on
  the process name reaches servers another scenario or another session started.
- A scenario whose subject is a security control states, before the assertion,
  how the run confirms the control is switched on, and carries a negative
  control showing the check fails when it should. A control that is silently
  off produces the same green result as one that passes.

### Cleanup

Every server scenario ends with:

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
rm -rf "$WORK"
```

## Scenario index

| ID | Title | Deployment | Embeddings | Vector backend | Live infrastructure |
|:--|:--|:--|:--|:--|:--|
| S01 | Solo filesystem, one skill, Claude Code | solo | none | none | none |
| S02 | Every artifact type, Claude Code | solo | none | none | none |
| S03 | Multi-harness materialization | solo | none | none | none |
| S04 | Watch mode reconciles edits and deletes | solo | none | none | none |
| S05 | Multiple filesystem layers and precedence | solo | none | none | none |
| S06 | Standalone server, keyword search, no embeddings | standalone | none | none | none |
| S07 | Standalone server, semantic search with Ollama | standalone | Ollama | sqlite-vec | Ollama |
| S08 | Standalone server, semantic search with OpenAI | standalone | OpenAI | sqlite-vec | OpenAI key |
| S09 | Standalone server, one Git-source layer | standalone | none | none | none |
| S10 | Standalone server, multiple Git-source layers | standalone | none | none | none |
| S11 | MCP runtime inside a harness | standalone | none | none | none |
| S12 | Per-caller layer visibility | standalone | none | none | none |
| S13 | Admin RBAC through the CLI | standalone | none | none | none |
| S14 | Standard server, Postgres, S3, pgvector, OpenAI | standard | OpenAI | pgvector | Postgres, S3, OpenAI key |
| S15 | Standard server, managed vector backend | standard | OpenAI | Pinecone | Postgres, S3, Pinecone, OpenAI key |
| S16 | Standard server, self-embedding managed backend | standard | backend-side | Pinecone | Postgres, S3, Pinecone |
| S17 | Public mode and the sensitivity floor | standalone | none | none | none |
| S18 | Lifecycle, versioning, and deprecation | standalone | none | none | none |
| S19 | Signing and signature verification | standalone | none | none | none |
| S20 | Migration from standalone to standard | standalone then standard | none | pgvector | Postgres, S3 |
| S21 | Read-only fallback on a primary outage | standard | none | pgvector | severable Postgres, S3 |
| S22 | Domain modeling and discovery | standalone | none | none | none |
| S23 | Authoring guardrails: lint rejects invalid manifests | solo | none | none | none |
| S24 | Sync profiles and overrides | solo | none | none | none |
| S25 | Sync scope filtering by path and type | solo | none | none | none |
| S26 | Reverse-dependency impact analysis | standalone | none | none | none |
| S27 | Inbound webhook-driven reingest | standalone | none | none | none |
| S28 | Audit log and right-to-be-forgotten erasure | standalone | none | none | none |
| S29 | Workspace overlay merges local artifacts | standalone | none | none | none |
| S30 | Offline-first cache resilience | standalone | none | none | none |
| S31 | Import an existing skill tree into a layer | solo | none | none | none |
| S32 | Gateway-delegated identity with trusted-headers | standalone | none | none | none |
| S33 | Gateway-delegated providers fail closed on misconfig | standalone | none | none | none |
| S34 | Marketplace publishing through a `kind: marketplace` sync target | solo | none | none | local git |
| S35 | Webhook receiver hardening: admin gate and SSRF policy | standalone | none | none | none |
| S36 | Successful oidc-jwt verification against a live IdP | standalone | none | none | OIDC IdP (AD FS for the split-issuer steps) |
| S37 | `extends:` merged manifest, hidden parent, and inherited redaction | standalone | none | none | none |
| S38 | `extends:` child under a signing registry | standalone | none | none | none |
| S39 | same-ID `extends:` overlay and a three-level chain | standalone | none | none | none |
| S40 | `extends:` for a skill, and filesystem-versus-server parity | solo then standalone | none | none | none |
| S41 | inherited `audit_redact` over a forwarded audit stream | standalone | none | none | none |
| S42 | a deprecated parent in an `extends:` chain | standalone | none | none | none |
| S43 | The documented `registry.yaml` example starts a registry | standalone | none | none | any public https OIDC issuer |
| S44 | The web UI on a directly reachable `oidc-jwt` registry | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S45 | The runbook's read-only write set matches what the registry rejects | standard | none | pgvector | severable Postgres, S3 |
| S46 | Install the Helm chart into a cluster | standard (Kubernetes) | none | none | kind, helm, kubectl, Docker |
| S47 | Sign in through the UI and read a layer an anonymous caller does not get | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S48 | Register a layer through the panel and read the one-time secret | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S49 | Unregister a layer through the panel's confirmation | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S50 | A non-owner is refused on a destructive operation | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S51 | Browse the domain hierarchy through the web UI | standalone | none | none | none |
| S52 | Search through the web UI with the type, scope, and tag filters | standalone | none | none | none |
| S53 | Read an artifact through the viewer | standalone | none | none | none |
| S54 | Author on disk, reingest from the panel, and materialize | standalone | none | none | none |
| S55 | The register dialog offers only what the caller can take | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S56 | The panel presents per-row only what the caller may take | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S57 | A stale prediction still refuses, and the panel says so | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S58 | Every layer response reads in snake_case | standalone | none | none | none |
| S59 | A personal layer's owner and visibility are fixed | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S60 | An admin-defined layer's visibility narrows | standalone | none | none | Keycloak (Docker) + mkcert CA |
| S61 | SCIM membership does not grant under trusted-headers | standalone | none | none | none |
| S62 | A reingest that drops an artifact fails | standalone | none | none | none |
| S63 | The two sync modes compose a shared merge target identically | solo then standalone | none | none | none |
| S64 | A registry migrates its stored hashes on the first start | standalone | none | none | none |
| S65 | First run verifies with no configuration | standalone | none | none | none |
| S66 | Signing on an existing registry signs no stored row until an operator asks | standalone | none | none | none |
| S67 | `podium verify --provider noop` refuses | none | none | none | none |
| S68 | A stale `never` is announced | standalone | none | none | none |
| S69 | The registry refuses a row edited in its store | standalone | none | none | none |
| S70 | The hidden parent does not appear in any response body | standalone | none | none | none |
| S71 | An `extends:` child verifies through the MCP bridge and materializes | standalone | none | none | none |
| S72 | The artifact viewer's extends rail matches the caller's view | standalone | none | none | a desktop browser |
| S73 | A key rotation keeps every stored row loadable | standalone | none | none | none |
| S74 | A cached pre-rotation delivery signature recovers | standalone | none | none | none |
| S75 | `podium-server sign-stored-rows` refuses without a key and never writes one | standalone | none | none | none |
| S76 | Chart upgrade from v0.4.0 | standard (Kubernetes) | none | none | kind, helm, kubectl, Docker |
| S77 | A filesystem sync that drops a colliding artifact fails | solo, standalone | none | none | none |
| S78 | The registry refuses an unusable SCIM store, audit sink, or audit anchor key | standalone | none | none | none |
| S79 | A workspace target's `$PODIUM_CHANGED` follows the bytes on disk | solo | none | none | none |
| S80 | Every harness output carries the derived skill compatibility line | solo | none | none | none |
| S81 | The Python SDK login pair against a live IdP | none (SDK only) | none | none | an IdP with a device-code client, a desktop browser |
| S82 | The Python SDK rejects a harness other than `none` on materialize | none (SDK only) | none | none | none |
| S83 | The change-event stream withholds events from layers the caller cannot see | standalone | none | none | none |
| S84 | A replayed stale latest is refused, and an honest regression is not | standalone | none | none | none |
| S85 | Sigstore-keyless sign and verify against the staging instance | none | none | none | Sigstore staging instance, an OIDC token it accepts |
| S86 | Webhook receivers are isolated per tenant on a multi-tenant registry | standard | none | none | Postgres, S3 |
| S87 | Layer operations act in the caller's tenant on a multi-tenant registry | standard | none | none | Postgres, S3 |
| S88 | Erasure on a multi-tenant registry redacts only the requesting tenant's audit records | standard | none | none | Postgres, S3 |

---

## S01: Solo filesystem, one skill, Claude Code

**Goal.** Validate the no-server path: author one skill into a filesystem
registry, configure a project, and materialize the skill into a Claude Code
workspace.

**Covers.** Solo deployment, `init`, `artifact scaffold`, `sync`, the
Claude Code adapter.

**Steps.**

1. Run the isolation block.
2. Create a registry with one skill.

   ```bash
   podium artifact scaffold --type skill --description "Greet a user politely" "$WORK/reg/greet"
   ```

3. Create a project and write its project-local configuration. `podium init`
   discovers the workspace by walking up from the current directory (§7.5.2), so
   change into the project first; that makes init write
   `$WORK/proj/.podium/sync.yaml`. The `--target` flag only sets the
   `defaults.target` value inside the file. The workspace discovery decides where
   the file goes.

   ```bash
   mkdir -p "$WORK/proj"
   cd "$WORK/proj"
   podium init --registry "$WORK/reg" --harness claude-code --target "$WORK/proj"
   ```

4. Materialize into the project.

   ```bash
   cd "$WORK/proj"
   podium sync
   ```

5. Inspect the materialized output.

   ```bash
   find "$WORK/proj/.claude" -type f
   podium status
   ```

**Expected.**

- Step 2 reports `Scaffolded skill at .../reg/greet/` and the directory holds
  `ARTIFACT.md` and `SKILL.md`.
- Step 3 writes `$WORK/proj/.podium/sync.yaml`.
- Step 4 reports one artifact materialized through the `claude-code` adapter.
- Step 5 lists a `greet` skill file under `$WORK/proj/.claude/` (the Claude Code
  skills layout). `podium status` shows `registry: $WORK/reg`, `harness:
  claude-code`, and `source: filesystem (no server to reach)`.

**Cleanup.** `rm -rf "$WORK"`.

---

## S02: Every artifact type, Claude Code

**Goal.** Validate that each artifact type ingests and materializes.

**Covers.** Skill, command, context, rule, hook, agent, and mcp-server types;
the Claude Code adapter across all of them.

**Steps.**

1. Run the isolation block.
2. Scaffold one artifact of each type.

   ```bash
   podium artifact scaffold --type skill   --description "A skill"   "$WORK/reg/my-skill"
   podium artifact scaffold --type command --description "A command" "$WORK/reg/my-command"
   podium artifact scaffold --type context --description "A context" "$WORK/reg/my-context"
   podium artifact scaffold --type rule --description "A rule" --rule-globs "**/*.go" --rule-mode always "$WORK/reg/my-rule"
   podium artifact scaffold --type hook --hook-event pre_tool_use --hook-action "echo hi" --description "A hook" "$WORK/reg/my-hook"
   podium artifact scaffold --type agent --delegates-to my-skill --description "An agent" "$WORK/reg/my-agent"
   podium artifact scaffold --type mcp-server --server-identifier acme-tools --description "An MCP server" "$WORK/reg/my-mcp"
   ```

3. Validate the registry and materialize.

   ```bash
   podium lint --registry "$WORK/reg"
   mkdir -p "$WORK/proj"
   cd "$WORK/proj"
   podium init --target "$WORK/proj" --registry "$WORK/reg" --harness claude-code
   podium sync
   find "$WORK/proj/.claude" "$WORK/proj/.podium/context" -type f | sort
   ls "$WORK/proj/.mcp.json"
   ```

**Expected.**

- Every scaffold command succeeds.
- `podium lint` reports `lint: no issues.`
- `podium sync` lists every scaffolded artifact under the `claude-code` adapter
  with its materialized path.
- The Claude Code adapter writes a file for each type at its per-type location.
  The skill, command, agent, and rule each land under `.claude/` (at
  `.claude/skills/my-skill/SKILL.md`, `.claude/commands/my-command.md`,
  `.claude/agents/my-agent.md`, and `.claude/rules/my-rule.md`). The hook merges
  into `.claude/settings.json`. The mcp-server writes the workspace-root
  `.mcp.json`. The context materializes to the harness-neutral
  `.podium/context/my-context/` directory that every adapter shares. The first
  `find` lists the `.claude/` and `.podium/context/` files, and the `ls` confirms
  the workspace-root `.mcp.json`.

**Cleanup.** `rm -rf "$WORK"`.

---

## S03: Multi-harness materialization

**Goal.** Validate that the same registry materializes through each harness
adapter into that harness's layout, and that `--harness none` writes raw
artifacts.

**Covers.** The claude-code, cursor, gemini, and codex adapters, and the `none`
adapter.

**Steps.**

1. Run the isolation block.
2. Create a small registry.

   ```bash
   podium artifact scaffold --type skill --description "Greet a user" "$WORK/reg/greet"
   podium artifact scaffold --type context --description "House style" "$WORK/reg/style"
   ```

3. Materialize once per harness into a separate target.

   ```bash
   for H in claude-code cursor gemini codex none; do
     mkdir -p "$WORK/out-$H"
     podium sync --registry "$WORK/reg" --harness "$H" --target "$WORK/out-$H"
     echo "=== $H ==="; find "$WORK/out-$H" -type f | sort
   done
   ```

**Expected.**

- Each harness run succeeds and writes into its own target directory.
- `claude-code` writes under `.claude/`, `cursor` under `.cursor/`, `gemini`
  under its Gemini layout, and `codex` under its Codex layout. The directory
  names differ per harness.
- `--harness none` writes the raw artifact files into the target root without a
  harness-specific wrapper directory. This is the documented behavior of the
  `none` adapter, so the absence of a `.claude`-style directory under
  `out-none` is correct.

**Cleanup.** `rm -rf "$WORK"`.

---

## S04: Watch mode reconciles edits and deletes

**Goal.** Validate that `podium sync --watch` re-materializes on a source edit
and removes a materialized artifact when its source is deleted.

**Covers.** Solo deployment, watch mode, add and delete reconciliation.

**Steps.**

1. Run the isolation block.
2. Create a registry and a project, then start a watch in the background.

   `podium init` writes the configuration into the workspace it discovers by
   walking up from the current directory (§7.5.2), so change into the project
   before running it; that writes `$WORK/proj/.podium/sync.yaml`. The `--target`
   flag only sets the `defaults.target` value inside the file.

   ```bash
   podium artifact scaffold --type skill --description "First skill" "$WORK/reg/alpha"
   mkdir -p "$WORK/proj"
   cd "$WORK/proj"
   podium init --target "$WORK/proj" --registry "$WORK/reg" --harness claude-code
   podium sync --watch > "$WORK/watch.log" 2>&1 &
   WATCH=$!
   sleep 2
   ```

3. Add a second skill, wait, then delete the first.

   ```bash
   podium artifact scaffold --type skill --description "Second skill" "$WORK/reg/beta"
   sleep 3
   find "$WORK/proj/.claude" -type d -name 'alpha' -o -type d -name 'beta'
   rm -rf "$WORK/reg/alpha"
   sleep 3
   find "$WORK/proj/.claude" -type d -name 'alpha'
   ```

4. Stop the watch: `kill "$WATCH" 2>/dev/null; wait "$WATCH" 2>/dev/null`.

**Expected.**

- After the add, both `alpha` and `beta` are materialized under `.claude`.
- After the delete, `alpha` is gone from `.claude` and `beta` remains.
- `watch.log` records a reconcile for each change.

**Cleanup.** `rm -rf "$WORK"`.

---

## S05: Multiple filesystem layers and precedence

**Goal.** Validate that a registry composed of two layers merges into one
effective view, and that a bare cross-layer name collision is rejected at
ingest rather than silently shadowed (§4.6).

**Covers.** Multiple layers, layer ordering, the merged effective view, the
collision-rejection rule.

**Steps.**

1. Run the isolation block.
2. Build a standalone server over a registry that declares two layers. Write a
   `registry.yaml` that names a base layer and a team layer, with the team layer
   second so it is higher precedence. Both layers contribute a `greet` skill,
   which collides on the canonical ID `greet`; the team layer also contributes a
   `deploy` skill that does not collide.

   ```bash
   mkdir -p "$WORK/base/greet" "$WORK/team/greet" "$WORK/team/deploy"
   podium artifact scaffold --type skill --description "Base greet" --force "$WORK/base/greet"
   podium artifact scaffold --type skill --description "Team greet override" --force "$WORK/team/greet"
   podium artifact scaffold --type skill --description "Team deploy" --force "$WORK/team/deploy"
   echo "BASE BODY" >> "$WORK/base/greet/SKILL.md"
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: base
         source:
           local:
             path: $WORK/base
       - id: team
         source:
           local:
             path: $WORK/team
   YAML
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8101 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8101/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8101
   ```

3. List layers, reingest the team layer to surface the collision report, and
   search.

   ```bash
   podium layer list --registry "$PODIUM_REGISTRY"
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   podium search --registry "$PODIUM_REGISTRY" "greet"
   podium search --registry "$PODIUM_REGISTRY" "deploy"
   podium artifact show --registry "$PODIUM_REGISTRY" greet
   ```

**Expected.**

- `layer list` shows `base` and `team` in order (`base` at `order` 1, `team` at
  `order` 2).
- `layer reingest team` reports `greet` rejected with code `ingest.collision`
  and a reason that names the artifact and the remedy and no layer:
  `cross-layer collision: "greet" is already contributed by another layer;
  declare extends: greet to overlay it`. A reason that names `base` is a §4.6
  disclosure to a layer owner who cannot read that layer. The registry log
  names both layers. The team layer's non-colliding `deploy` is ingested.
- Searching `greet` returns a single `greet` artifact whose description is the
  base layer's (`Base greet`), confirming the base artifact survives and the
  colliding team artifact was rejected rather than silently shadowing it.
- Searching `deploy` returns the team-only `deploy` skill.
- `artifact show greet` prints the base layer's body, which contains
  `BASE BODY`.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S06: Standalone server, keyword search, no embeddings

**Goal.** Validate the standalone server with embeddings disabled, served over a
filesystem registry, exercised through the CLI and the HTTP API.

**Covers.** Standalone deployment, keyword (BM25) search, `search`, `domain
show`, `artifact show`, and the HTTP endpoints.

**Steps.**

1. Run the isolation block.
2. Create a registry with a few artifacts in a couple of domains.

   ```bash
   podium artifact scaffold --type skill --description "Run the monthly finance close" "$WORK/reg/finance/run-close"
   podium artifact scaffold --type skill --description "Open a customer support ticket" "$WORK/reg/support/open-ticket"
   podium artifact scaffold --type context --description "Engineering deploy runbook" "$WORK/reg/eng/deploy-runbook"
   ```

3. Serve and query.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8102 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8102/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8102
   podium search --registry "$PODIUM_REGISTRY" "close the books"
   podium domain show --registry "$PODIUM_REGISTRY"
   podium artifact show --registry "$PODIUM_REGISTRY" finance/run-close
   curl -s "$PODIUM_REGISTRY/healthz"; echo
   ```

**Expected.**

- `healthz` returns HTTP 200.
- Keyword search for `close the books` ranks the `run-close` finance skill
  first by term overlap.
- `domain show` lists the `finance`, `support`, and `eng` domains.
- `artifact show finance/run-close` prints the finance skill's manifest and body.
  The canonical artifact ID is the directory path under the layer root (§7.6.1),
  so the domain-qualified `finance/run-close` resolves and the bare leaf name
  `run-close` does not.
- The server log shows embeddings disabled and no embedding-provider calls.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S07: Standalone server, semantic search with Ollama

**Goal.** Validate self-hosted embeddings: the standalone server embeds
artifacts with a local Ollama model and answers a paraphrased query that
keyword search would miss.

**Covers.** Standalone deployment, Ollama embeddings, sqlite-vec, semantic
search.

**Prerequisites.** A running Ollama daemon with an embedding model pulled, for
example `ollama pull nomic-embed-text`. If `curl -s
http://127.0.0.1:11434/api/tags` does not respond, skip this scenario and record
the reason.

**Steps.**

1. Run the isolation block.
2. Create a registry whose descriptions avoid the query's exact words.

   ```bash
   podium artifact scaffold --type skill --description "Reconcile the general ledger at period end" "$WORK/reg/finance/reconcile"
   podium artifact scaffold --type skill --description "Rotate the on-call schedule" "$WORK/reg/ops/rotate-oncall"
   ```

3. Serve with Ollama embeddings.

   ```bash
   export PODIUM_EMBEDDING_PROVIDER=ollama
   export PODIUM_OLLAMA_URL=http://127.0.0.1:11434
   export PODIUM_OLLAMA_MODEL=nomic-embed-text
   export PODIUM_VECTOR_BACKEND=sqlite-vec
   podium serve --standalone --layer-path "$WORK/reg" --bind 127.0.0.1:8103 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8103/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8103
   podium search --registry "$PODIUM_REGISTRY" "close the books for the month"
   ```

**Expected.**

- The server log shows embeddings enabled and Ollama calls during ingest.
- The query `close the books for the month` returns the `reconcile` finance
  skill as the top result through vector similarity, even though it shares no
  salient keyword with the description.
- Re-running the same query is stable across runs.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S08: Standalone server, semantic search with OpenAI

**Goal.** Validate hosted embeddings: the standalone server embeds artifacts
with an OpenAI model and answers a paraphrased query.

**Covers.** Standalone deployment, OpenAI embeddings, sqlite-vec, semantic
search.

**Prerequisites.** `OPENAI_API_KEY` in `test.env` with available quota. If the
key is absent, skip and record the reason. Load it with `set -a; source
$REAL_HOME/projects/podium/test.env; set +a`.

**Steps.**

1. Run the isolation block, then load the key.

   ```bash
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   ```

2. Create the same registry as S07 (the `reconcile` and `rotate-oncall` skills).
3. Serve with OpenAI embeddings.

   ```bash
   export PODIUM_EMBEDDING_PROVIDER=openai
   export PODIUM_EMBEDDING_MODEL=text-embedding-3-small
   export PODIUM_VECTOR_BACKEND=sqlite-vec
   podium serve --standalone --layer-path "$WORK/reg" --bind 127.0.0.1:8104 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8104/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8104
   podium search --registry "$PODIUM_REGISTRY" "close the books for the month"
   ```

**Expected.**

- The server log shows embeddings enabled and OpenAI calls during ingest.
- The paraphrased query returns the `reconcile` skill as the top result.
- An `insufficient_quota` response from OpenAI is reported clearly by the server
  rather than silently degrading; treat that as a skip, not a pass.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S09: Standalone server, one Git-source layer

**Goal.** Validate ingest from a real Git repository, and re-ingest after a new
commit.

**Covers.** Standalone deployment, Git-source layers, `layer register`, `layer
reingest`, source updates.

**Steps.**

1. Run the isolation block.
2. Create a real Git repository holding artifacts.

   ```bash
   mkdir -p "$WORK/repo" && cd "$WORK/repo" && git init -q
   podium artifact scaffold --type skill --description "Deploy the service" "$WORK/repo/deploy"
   git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "add deploy skill"
   ```

3. Serve an empty standalone registry, register the repository as a layer, then
   run the first manual reingest. Registering a Git source without a configured
   webhook leaves the layer at its initial commit until the first manual
   reingest, so the layer holds no searchable artifacts until `layer reingest`
   runs.

   ```bash
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8105 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8105/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8105
   podium layer register --registry "$PODIUM_REGISTRY" --id team --repo "$WORK/repo" --ref main --public
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   podium layer list --registry "$PODIUM_REGISTRY"
   podium search --registry "$PODIUM_REGISTRY" "deploy"
   ```

4. Add a second artifact in the repo, commit, and re-ingest.

   ```bash
   podium artifact scaffold --type skill --description "Roll back a deploy" "$WORK/repo/rollback"
   cd "$WORK/repo" && git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "add rollback skill"
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   podium search --registry "$PODIUM_REGISTRY" "rollback"
   ```

**Expected.**

- `layer register` succeeds and returns the webhook URL and HMAC secret. `layer
  list` shows the `team` layer with a Git source.
- The first `layer reingest` ingests the initial commit and prints `artifact:
  deploy@0.1.0   layer: team`. The first search then returns the `deploy` skill.
- After the new commit, `layer reingest` ingests it (the layer's
  `last_ingested_ref` advances to the new commit), and the post-reingest search
  returns the `rollback` skill.
- The reingest response reports the count accepted and any rejected with a
  reason, rather than a bare zero. An artifact dropped for a cross-layer
  collision is reported under `rejected` with `code: ingest.collision` and a
  reason.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S10: Standalone server, multiple Git-source layers

**Goal.** Validate composition across two real Git repositories registered as
two layers, including a higher-precedence layer overlaying a same-ID artifact
from a lower-precedence layer.

**Covers.** Standalone deployment, multiple Git layers, layer ordering, the
`extends:` overlay, the merged view.

**Steps.**

1. Run the isolation block.
2. Create two repositories. The `base` repository holds a `greet` skill. The
   `team` repository holds its own `greet` skill plus a unique `team-only`
   skill. Per §4.6, two layers contributing the same canonical ID is a
   forbidden silent shadow unless the higher-precedence artifact declares
   `extends: <id>`. Per §4.7.6 each artifact carries its own version, so the
   `team` overlay bumps its `version:` and declares `extends: greet` to overlay
   the `base` copy. The `extends:` field is top-level frontmatter in
   `ARTIFACT.md`.

   ```bash
   mkdir -p "$WORK/base" && cd "$WORK/base" && git init -q
   podium artifact scaffold --type skill --description "base greet" "$WORK/base/greet" --force
   git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "base"

   mkdir -p "$WORK/team" && cd "$WORK/team" && git init -q
   podium artifact scaffold --type skill --description "team greet" "$WORK/team/greet" --force
   # Overlay the base greet: bump the version and declare extends in ARTIFACT.md.
   python3 - "$WORK/team/greet/ARTIFACT.md" <<'PY'
   import sys
   p = sys.argv[1]; s = open(p).read()
   open(p, "w").write(s.replace("version: 0.1.0\n", "version: 0.2.0\nextends: greet\n"))
   PY
   podium artifact scaffold --type skill --description "Team only" "$WORK/team/team-only" --force
   git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "team"
   ```

3. Serve, register both layers with `team` second, reingest each layer, then
   query. Registering a Git source without a configured webhook leaves the
   layer at its initial commit until the first manual reingest (§7.3.1), so each
   layer holds no searchable artifacts until `layer reingest` runs.

   ```bash
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8106 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8106/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8106
   podium layer register --registry "$PODIUM_REGISTRY" --id base --repo "$WORK/base" --ref main --public
   podium layer register --registry "$PODIUM_REGISTRY" --id team --repo "$WORK/team" --ref main --public
   podium layer reingest --registry "$PODIUM_REGISTRY" base
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   podium layer list --registry "$PODIUM_REGISTRY"
   podium search --registry "$PODIUM_REGISTRY" "greet"
   podium search --registry "$PODIUM_REGISTRY" "team only"
   podium artifact show --registry "$PODIUM_REGISTRY" greet
   ```

**Expected.**

- `layer list` shows `base` then `team`.
- `layer reingest base` ingests `greet@0.1.0` into `base`. `layer reingest team`
  ingests both `greet@0.2.0` and `team-only@0.1.0` into `team` with no
  collision rejection, because the team `greet` declares `extends: greet`.
- Searching `greet` returns one merged `greet` whose description is the team
  layer's version (`team greet`); the two underlying versions collapse to a
  single entry in the results.
- Searching `team only` returns the `team-only` skill. The merged `greet` also
  matches because its description contains "team".
- `artifact show greet` returns version `0.2.0`, confirming the team overlay
  won.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S11: MCP runtime inside a harness

**Goal.** Validate the MCP bridge: a harness configured with `podium-mcp`
reaches a running registry and the meta-tools return live results.

**Covers.** Standalone deployment, the `podium-mcp` bridge, the MCP meta-tools.

**Steps.**

1. Run the isolation block.
2. Serve a small registry.

   ```bash
   podium artifact scaffold --type skill --description "Summarize a PR" "$WORK/reg/summarize-pr"
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8107 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8107/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8107
   ```

3. Drive the MCP bridge over stdio with two JSON-RPC requests: initialize, then
   list tools. The bridge verifies under the default `always` policy and
   resolves the registry's public key from `$PODIUM_SIGN_KEY_PATH`, which the
   isolation block exports and the server wrote.

   ```bash
   printf '%s\n%s\n' \
     '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"manual","version":"0"}}}' \
     '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
     | PODIUM_REGISTRY="$PODIUM_REGISTRY" podium-mcp 2>"$WORK/mcp.log" | head -40
   ```

4. Optionally, wire the bridge into Claude Code (`claude mcp add podium --
   env PODIUM_REGISTRY=$PODIUM_REGISTRY PODIUM_SIGN_KEY_PATH=$PODIUM_SIGN_KEY_PATH
   -- $PODIUM_BIN/podium-mcp`), open the harness, and ask it to search the
   catalog. The harness spawns the bridge outside this shell, so the entry
   names the key file the server wrote; without it the bridge resolves the key
   under the operator's real home and refuses to start with
   `config.signature_provider_unavailable`. This part is observed in the
   harness UI.

**Expected.**

- `initialize` returns a result with server info.
- `tools/list` returns the Podium meta-tools (the search and load tools).
- Inside the harness (optional step), a catalog search returns the
  `summarize-pr` skill.

**Cleanup.** Stop the server and `rm -rf "$WORK"`. Remove the harness MCP entry
if it was added.

---

## S12: Per-caller layer visibility

**Goal.** Validate that two authenticated callers see different artifacts when
a layer is restricted to a group, while a public layer is visible to both.

**Covers.** Standalone deployment, injected-session-token identity, per-layer
visibility, the mint helper in `tools/minttoken`.

**Steps.**

1. Run the isolation block.
2. Write a registry config with a public layer and a group-restricted layer.

   ```bash
   mkdir -p "$WORK/pub/handbook" "$WORK/eng/deploy"
   podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
   podium artifact scaffold --type skill --description "Engineering deploy" --force "$WORK/eng/deploy"
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: public-handbook
         source: { local: { path: $WORK/pub } }
         visibility: { public: true }
       - id: eng-internal
         source: { local: { path: $WORK/eng } }
         visibility: { groups: [engineering] }
   YAML
   ```

3. Generate a runtime key, write it into the registry's keys file, and boot the
   server in injected-session-token mode against that file. The registry reads
   the keys file before it binds a listener, so the register step runs first.
   Seed SCIM so the `engineering` group resolves.

   ```bash
   go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" >/dev/null 2>&1   # writes the keypair
   podium admin runtime register --keys-file "$WORK/keys/runtimes.json" --issuer manual-runtime --algorithm RS256 --public-key-file "$WORK/keys/runtime-pub.pem"
   export PODIUM_IDENTITY_PROVIDER=injected-session-token
   export PODIUM_RUNTIME_KEYS_PATH="$WORK/keys/runtimes.json"
   export PODIUM_OAUTH_AUDIENCE=https://podium.manual
   export PODIUM_SCIM_TOKENS=scim-secret
   export PODIUM_SCIM_STORE_PATH="$WORK/scim.json"
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8108 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8108/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8108
   ```

   Provision `alice@acme.com` into the `engineering` SCIM group and leave
   `bob@acme.com` out of it. Use the SCIM endpoint with the `scim-secret`
   bearer; the precise SCIM calls are in `docs/` and the
   `seedSCIM` helper in `test/e2e/authserver_harness_test.go`.

4. Mint a token for each caller and search.

   ```bash
   ALICE=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com --groups engineering)
   BOB=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
   echo "--- alice (engineering) ---"; PODIUM_SESSION_TOKEN="$ALICE" podium search --registry "$PODIUM_REGISTRY" ""
   echo "--- bob (no group) ---";      PODIUM_SESSION_TOKEN="$BOB"   podium search --registry "$PODIUM_REGISTRY" ""
   echo "--- anonymous ---";           podium search --registry "$PODIUM_REGISTRY" ""
   ```

**Expected.**

- alice sees both the public handbook and the engineering deploy skill.
- bob sees only the public handbook; the engineering deploy skill is filtered
  out and is also undiscoverable in search.
- The anonymous call is rejected with `auth.untrusted_runtime` (HTTP 401)
  because injected-session-token mode rejects unverified callers.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S13: Admin RBAC through the CLI

**Goal.** Validate that tenant-admin grants and revocations through the CLI
gate the admin surface, and that `show-effective` reports per-layer visibility
for a user.

**Covers.** Standalone deployment, `admin grant`, `admin revoke`, `admin
show-effective`, bootstrap admins.

**Steps.**

1. Run the isolation block.
2. Write the runtime key into the keys file and boot an injected-session-token
   server against it with `alice@acme.com` as a bootstrap admin, over a small
   registry (as in S12, with `PODIUM_BOOTSTRAP_ADMINS=alice@acme.com` added and
   `--bind 127.0.0.1:8109`). The register step and the
   `PODIUM_RUNTIME_KEYS_PATH` export both precede `podium serve`.
3. Exercise the admin surface as alice (admin) and bob (non-admin).

   ```bash
   ALICE=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com)
   BOB=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
   echo "--- bob attempts an admin grant (expect refusal) ---"
   PODIUM_SESSION_TOKEN="$BOB" podium admin grant --registry "$PODIUM_REGISTRY" carol@acme.com
   echo "--- alice grants bob admin ---"
   PODIUM_SESSION_TOKEN="$ALICE" podium admin grant --registry "$PODIUM_REGISTRY" bob@acme.com
   echo "--- bob can now grant carol ---"
   PODIUM_SESSION_TOKEN="$BOB" podium admin grant --registry "$PODIUM_REGISTRY" carol@acme.com
   echo "--- alice revokes bob ---"
   PODIUM_SESSION_TOKEN="$ALICE" podium admin revoke --registry "$PODIUM_REGISTRY" bob@acme.com
   echo "--- bob is refused again ---"
   PODIUM_SESSION_TOKEN="$BOB" podium admin grant --registry "$PODIUM_REGISTRY" dave@acme.com
   echo "--- effective visibility for alice ---"
   PODIUM_SESSION_TOKEN="$ALICE" podium admin show-effective --registry "$PODIUM_REGISTRY" alice@acme.com
   ```

**Expected.**

- bob's first grant is refused with an authorization error.
- alice's grant of bob succeeds, after which bob's grant of carol succeeds.
- After alice revokes bob, bob's next grant is refused again.
- `show-effective` prints the per-layer visibility decision for alice.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S14: Standard server, Postgres, S3, pgvector, OpenAI

**Goal.** Validate the standard deployment: Postgres registry store, S3 object
store, pgvector backend, OpenAI embeddings, including a large resource served
through an S3 presigned URL.

**Covers.** Standard deployment, `serve --strict`, pgvector, S3 presign, large
resources.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env` (Postgres DSN, S3 settings) and `OPENAI_API_KEY`. Skip if any is
absent.

**Steps.**

1. Run the isolation block.
2. Start services and load the environment. The Postgres registry store keeps a
   persistent volume across `make services-up` and `make services-down`, so a
   prior run's `(artifact_id, version)` pairs survive into this one. A
   re-ingested version with different bytes is rejected as
   `ingest.immutable_violation` (§4.7.6 version immutability), which would leave
   the prior run's resource-free `report` in place. Export a per-run artifact id
   so each run authors and queries a fresh artifact and the large-resource path
   is exercised against newly ingested bytes.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_VECTOR_BACKEND=pgvector
   export PODIUM_EMBEDDING_PROVIDER=openai
   export PODIUM_EMBEDDING_MODEL=text-embedding-3-small
   export REPORT="report-$$"
   ```

3. Author a registry that includes a large resource file, then serve in strict
   mode.

   ```bash
   podium artifact scaffold --type skill --description "Generate a quarterly report" "$WORK/reg/$REPORT"
   head -c 2000000 /dev/urandom | base64 > "$WORK/reg/$REPORT/big-template.txt"
   podium serve --strict --layer-path "$WORK/reg" --bind 127.0.0.1:8110 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8110/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8110
   podium config show --server | grep -E 'store|object_store|vector'
   podium search --registry "$PODIUM_REGISTRY" "quarterly report"
   podium artifact show --registry "$PODIUM_REGISTRY" "$REPORT"
   curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=$REPORT" \
     | python3 -c "import sys,json; d=json.load(sys.stdin); print('large_resources:', json.dumps(d.get('large_resources'), indent=2)); print('inline resources:', list((d.get('resources') or {}).keys()))"
   ```

**Expected.**

- `config show --server` reports the Postgres store, the S3 object store, and
  the pgvector backend.
- The server boots and `healthz` returns 200.
- Semantic search returns the `$REPORT` skill (a name of the form
  `report-<pid>`). Artifacts left in the persistent Postgres store by earlier
  runs may also appear in the result list.
- The large resource is stored in S3 and served through a presigned URL when
  loaded. The `load_artifact` response lists `big-template.txt` under
  `large_resources` with a presigned `http://localhost:9000/podium/...` URL and
  an empty inline `resources` map, so the control plane does not stream the
  large body inline (§7.2 sets the inline cutoff at 256 KB).

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and `(cd "$REAL_HOME/projects/podium" && make services-down)` when
finished with the standard-mode scenarios.

---

## S15: Standard server, managed vector backend

**Goal.** Validate a managed vector backend storing externally-computed
embeddings, with Postgres and S3 as the registry and object stores.

**Covers.** Standard deployment, Pinecone (or Weaviate or Qdrant) as the vector
backend with external embeddings.

**Prerequisites.** `make services-up`, `test.env` (Postgres, S3, `OPENAI_API_KEY`,
and the `PODIUM_PINECONE_*` settings for a dense index sized to the embedding
model). Skip if absent. The same scenario runs against Weaviate
(`PODIUM_WEAVIATE_*`) or Qdrant (`PODIUM_QDRANT_*`) by changing the backend
selection.

**Steps.**

1. Run the isolation block, start services, and load the environment as in S14,
   but select the managed backend. `PODIUM_PINECONE_INDEX` and the API key come
   from `test.env`. `PODIUM_PINECONE_NAMESPACE` sets a namespace prefix that is
   combined with the per-tenant ID for every vector; the default value is
   `default`. The shared `podium-test` index is reused across runs, so export a
   unique namespace per run to keep one run's vectors out of another's.

   The Postgres registry store keeps a persistent volume across `make
   services-up` and `make services-down`, and the org schema is keyed by a
   deterministic tenant ID, so a prior run's artifacts survive into this one
   under the same schema. Those artifacts stay in the result list and can
   outrank the two skills this run authors, because other scenarios leave
   finance and close-reporting artifacts that match the paraphrased query.
   Create a fresh throwaway database for this run so the ingest and the query
   see only the two skills below. The server requires the database to exist; it
   creates the per-org schema inside it but does not create the database itself.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_VECTOR_BACKEND=pinecone
   export PODIUM_EMBEDDING_PROVIDER=openai
   export PODIUM_EMBEDDING_MODEL=text-embedding-3-small
   export PODIUM_PINECONE_NAMESPACE="manual-s15-$$-$(date +%s)"
   export PGDB="podium_s15_$$"
   docker exec podium-postgres createdb -U podium "$PGDB"
   export PODIUM_POSTGRES_DSN="postgres://podium:podium@localhost:5432/$PGDB?sslmode=disable"
   ```

   The same scenario runs against Weaviate (`PODIUM_VECTOR_BACKEND=weaviate-cloud`,
   `PODIUM_WEAVIATE_*`) or Qdrant (`PODIUM_VECTOR_BACKEND=qdrant-cloud`,
   `PODIUM_QDRANT_*`). Those backends isolate per tenant with a stored
   `tenant_id` property and a deterministic object ID keyed by
   `tenant/artifact@version`, so they do not take a per-run namespace prefix.

2. Author the S07 registry (the `reconcile` and `rotate-oncall` skills), serve
   in strict mode on `127.0.0.1:8111`, and run a paraphrased query.

   ```bash
   podium artifact scaffold --type skill --description "Reconcile the general ledger at period end" "$WORK/reg/finance/reconcile"
   podium artifact scaffold --type skill --description "Rotate the on-call schedule" "$WORK/reg/ops/rotate-oncall"
   podium serve --strict --layer-path "$WORK/reg" --bind 127.0.0.1:8111 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8111/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8111
   podium config show --server | grep -E 'store|object_store|vector|embedding'
   sleep 8   # let the vector outbox drain worker upsert the two vectors
   curl -s http://127.0.0.1:8111/metrics | grep '^podium_vector_outbox_depth'
   podium search --registry "$PODIUM_REGISTRY" "close the books for the month"
   ```

**Expected.**

- `config show --server` reports the Pinecone backend, the per-run
  `vector_backend.namespace`, and the OpenAI embedding provider and model.
- The boot log records `hybrid search: vector=pinecone embedder=openai
  dim=1536` and the drain worker line `vector outbox: drain worker running
  (... backend=pinecone ...)`. The drain worker upserts the two vectors into
  the managed index under the per-run namespace and does not log a line for an
  individual upsert; the `podium_vector_outbox_depth` gauge returns to `0` once
  the batch is sent. To confirm the vectors landed in the per-run namespace,
  POST `{}` to the backend's `describe_index_stats` endpoint and read the count
  under `manual-s15-<pid>-<timestamp>_<tenant>`, which is `2`.
- The paraphrased query returns the `reconcile` skill as the top result. The
  fresh database holds only the two skills this run authored, so the result list
  is `Showing 2 of 2 results` with `finance/reconcile` first.
- Repeating the scenario against Weaviate or Qdrant produces the same ranking.

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and drop the throwaway database
with `docker exec podium-postgres dropdb -U podium "$PGDB"`.

---

## S16: Standard server, self-embedding managed backend

**Goal.** Validate a managed backend that computes embeddings itself (integrated
inference), with no external embedding provider configured.

**Covers.** Standard deployment, Pinecone integrated inference (or a Weaviate
vectorizer class), backend-side embedding.

**Prerequisites.** `make services-up`, `test.env` with a self-embedding index
configured (`PODIUM_PINECONE_SELFEMBED_INDEX` and
`PODIUM_PINECONE_INFERENCE_MODEL`, or the Weaviate or Qdrant equivalents). Skip
if absent.

**Steps.**

1. Run the isolation block, start services, and load the environment, selecting
   the self-embedding backend and leaving the external embedding provider unset.
   The self-embedding text is written only when the bootstrap ingest accepts a
   new `(artifact_id, version)`; an identical re-ingest is a no-op (§7 ingest
   cases) and enqueues nothing, so a shared Postgres store that already holds
   these IDs from a prior run leaves the backend index untouched. Point the
   server at a fresh registry store for this run so the ingest accepts the two
   artifacts and the drain worker sends their text to the backend, and export a
   unique `PODIUM_PINECONE_NAMESPACE` so the run's vectors stay out of the
   shared self-embedding index.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_VECTOR_BACKEND=pinecone
   export PODIUM_PINECONE_INDEX="$PODIUM_PINECONE_SELFEMBED_INDEX"
   export PODIUM_PINECONE_NAMESPACE="manual-s16-$$-$(date +%s)"
   unset PODIUM_EMBEDDING_PROVIDER PODIUM_EMBEDDING_MODEL
   export PGDB="podium_s16_$$"
   docker exec podium-postgres createdb -U podium "$PGDB"
   export PODIUM_POSTGRES_DSN="postgres://podium:podium@localhost:5432/$PGDB?sslmode=disable"
   ```

2. Author the S07 registry (the `reconcile` and `rotate-oncall` skills), serve
   in strict mode on `127.0.0.1:8112`, and run a paraphrased query.

   ```bash
   podium artifact scaffold --type skill --description "Reconcile the general ledger at period end" "$WORK/reg/finance/reconcile"
   podium artifact scaffold --type skill --description "Rotate the on-call schedule" "$WORK/reg/ops/rotate-oncall"
   podium serve --strict --layer-path "$WORK/reg" --bind 127.0.0.1:8112 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8112/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8112
   podium config show --server | grep -E 'store|object_store|vector|inference'
   sleep 8   # let the vector outbox drain worker send the two artifacts' text to the backend
   curl -s http://127.0.0.1:8112/metrics | grep '^podium_vector_outbox_depth'
   podium search --registry "$PODIUM_REGISTRY" "close the books for the month"
   ```

**Expected.**

- The server boots without an external embedding provider. The startup log
  records `hybrid search: vector=pinecone self-embedding=<model>` (the
  `<model>` is `PODIUM_PINECONE_INFERENCE_MODEL`), which reports that the
  backend embeds the artifact text server-side and the server computes no
  vectors locally. The query path stays non-degraded, so the backend's
  integrated inference is answering the search.
- The paraphrased query returns the `reconcile` skill as the top result.

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and drop the throwaway database
with `docker exec podium-postgres dropdb -U podium "$PGDB"`.

---

## S17: Public mode and the sensitivity floor

**Goal.** Validate public mode: anonymous callers read the catalog, and the
public-mode sensitivity ceiling rejects `medium` and `high` artifacts at ingest
so they never enter the catalog.

**Covers.** Standalone deployment, public mode, anonymous access, the
ingest-time sensitivity ceiling.

**Steps.**

1. Run the isolation block.
2. Author a registry with a low-sensitivity artifact and a high-sensitivity
   artifact.

   ```bash
   podium artifact scaffold --type context --sensitivity low  --description "Public FAQ" "$WORK/reg/faq"
   podium artifact scaffold --type skill   --sensitivity high --description "Production incident runbook" "$WORK/reg/incident"
   ```

3. Serve in public mode and query anonymously.

   ```bash
   podium serve --standalone --no-embeddings --public-mode --layer-path "$WORK/reg" --bind 127.0.0.1:8113 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8113/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8113
   podium status
   podium search --registry "$PODIUM_REGISTRY" ""
   podium artifact show --registry "$PODIUM_REGISTRY" faq
   podium artifact show --registry "$PODIUM_REGISTRY" incident
   ```

**Expected.**

- `podium status` reports `registry mode: public`. The scope preview lists one
  artifact (`faq`, `context`, `low`), confirming the `high` artifact never
  entered the catalog.
- The anonymous search and `artifact show faq` succeed. Public mode bypasses the
  visibility model (§4.6), so the anonymous caller reads the catalog without
  credentials.
- The `high`-sensitivity `incident` is rejected at ingest by the public-mode
  sensitivity ceiling (§13.10). The startup log line in `$WORK/srv.log` for the
  layer load reports `rejected=1`; the rejection carries the structured code
  `ingest.public_mode_rejects_sensitive`. The artifact never enters the catalog,
  so `artifact show incident` returns HTTP 404 with `registry.not_found`. Public
  mode does not filter sensitivity per caller at read time; the ingest ceiling is
  what keeps `incident` out.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S18: Lifecycle, versioning, and deprecation

**Goal.** Validate that publishing a new version supersedes the old, that
deprecating a version with a replacement removes it from default search, and
that loading a deprecated artifact surfaces the replacement.

**Covers.** Standalone deployment, versioning, deprecation, `replaced_by`.

**Steps.**

1. Run the isolation block.
2. Create a Git-source layer holding version 1.0.0 of a skill, serve, and
   register it (as in S09, on `127.0.0.1:8114`). The scaffold writes
   `version: 0.1.0`, so edit `$WORK/repo/deploy/ARTIFACT.md` to `version: 1.0.0`
   before the first commit.

   ```bash
   mkdir -p "$WORK/repo" && cd "$WORK/repo" && git init -q
   podium artifact scaffold --type skill --description "Deploy the service" "$WORK/repo/deploy"
   # set version: 1.0.0 in $WORK/repo/deploy/ARTIFACT.md, then:
   git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "add deploy skill 1.0.0"
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8114 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8114/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8114
   podium layer register --registry "$PODIUM_REGISTRY" --id team --repo "$WORK/repo" --ref main --public
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   ```

3. Publish version 2.0.0 by editing the artifact's `version` and committing,
   then re-ingest. A bare `artifact show` resolves `latest`, which is the most
   recently ingested non-deprecated version, so it reports 2.0.0.

   ```bash
   # bump the version in $WORK/repo/deploy/ARTIFACT.md to 2.0.0, then:
   cd "$WORK/repo" && git -c user.email=alice@acme.com -c user.name=alice commit -aqm "deploy 2.0.0"
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   podium artifact show --registry "$PODIUM_REGISTRY" deploy
   ```

4. Deprecate the artifact line in favor of the live 2.0.0 successor. Each
   `(artifact_id, version)` is immutable by content hash (§4.7.6), so an
   already-published version cannot be re-published with a changed `deprecated`
   flag. Deprecation is published as a new version that carries
   `deprecated: true` and a `replaced_by` upgrade target. Edit
   `$WORK/repo/deploy/ARTIFACT.md` to version 3.0.0 with those two frontmatter
   fields added, commit, re-ingest, then observe search and an explicit load of
   the deprecated version. Flags precede the positional id, so `--version 3.0.0`
   comes before `deploy`.

   ```bash
   # set version: 3.0.0 and add `deprecated: true` and
   # `replaced_by: deploy@2.0.0` to $WORK/repo/deploy/ARTIFACT.md, then:
   cd "$WORK/repo" && git -c user.email=alice@acme.com -c user.name=alice commit -aqm "deploy 3.0.0 deprecated"
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   podium search --registry "$PODIUM_REGISTRY" "deploy"
   podium artifact show --registry "$PODIUM_REGISTRY" --version 3.0.0 deploy
   ```

**Expected.**

- After the 2.0.0 re-ingest, `artifact show deploy` reports version 2.0.0 as
  current.
- After the deprecated 3.0.0 re-ingest, `artifact show deploy` still reports
  2.0.0, because `latest` skips the deprecated 3.0.0 (§4.7.6).
- Search returns the current 2.0.0, and the deprecated 3.0.0 is excluded from
  default results.
- An explicit load of the deprecated 3.0.0 surfaces the `replaced_by` pointer
  to `deploy@2.0.0` in the frontmatter, and the wire response carries a
  `deprecation_warning` that names the upgrade target.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S19: Signing and signature verification

**Goal.** Validate ingest-time signing and consumer-side verification: a signed
high-sensitivity artifact loads under the `always` policy, and an unsigned one
is refused.

**Covers.** Standalone deployment, `serve --sign registry-key`,
`PODIUM_VERIFY_SIGNATURES`, `podium verify`.

**Steps.**

1. Run the isolation block.
2. Author one high-sensitivity artifact and serve it with ingest signing
   enabled. The server log reports `ingest signing: registry-managed key` and
   the signing keypair is written to `PODIUM_SIGN_KEY_PATH` on first run.

   ```bash
   podium artifact scaffold --type skill --sensitivity high --description "Signed runbook" "$WORK/reg/signed-runbook"
   export PODIUM_SIGN_KEY_PATH="$WORK/registry-sign.key"
   podium serve --standalone --no-embeddings --sign registry-key --layer-path "$WORK/reg" --bind 127.0.0.1:8115 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8115/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8115
   grep "ingest signing" "$WORK/srv.log"
   ```

3. Confirm the registry serves a delivery signature, then load the artifact.
   `podium artifact show` prints the body without verifying; the delivery pair
   lives in the `load_artifact` response and consumer-side verification happens
   at materialization (next step).

   ```bash
   curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=signed-runbook" \
     | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["delivery_hash"]); print(d["delivery_signature"]); print(sorted(k for k in ("signature","raw_frontmatter","manifest_merged") if k in d))'
   export PODIUM_VERIFY_SIGNATURES=always
   podium artifact show --registry "$PODIUM_REGISTRY" signed-runbook
   ```

4. Verify the signature at the consumer. The MCP bridge enforces
   `PODIUM_VERIFY_SIGNATURES` at materialization. With `registry-managed`
   verification it needs the registry's signing public key, which the
   standalone server writes into the `public:` line of `PODIUM_SIGN_KEY_PATH`.
   Load the signed artifact through the bridge with the policy enforcing.

   ```bash
   export PODIUM_SIGNATURE_VERIFY_KEY="$(awk '/^public:/{print $2}' "$WORK/registry-sign.key")"
   echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"signed-runbook"}}}' \
     | PODIUM_HARNESS=none \
       PODIUM_MATERIALIZE_ROOT="$WORK/out" \
       PODIUM_SIGNATURE_PROVIDER=registry-managed \
       PODIUM_SIGNATURE_VERIFY_KEY="$PODIUM_SIGNATURE_VERIFY_KEY" \
       podium-mcp 2>/dev/null | python3 -m json.tool
   find "$WORK/out" -type f
   ```

5. Author a second high-sensitivity artifact, serve it on a separate port
   with signing turned off (`PODIUM_SIGN=none`) and its own key path, and load
   it through the bridge under the same enforcing policy. The registry signs by
   default, so this server turns signing off explicitly. An unsigned artifact
   is refused.

   ```bash
   podium artifact scaffold --type skill --sensitivity high --description "Unsigned runbook" "$WORK/reg-unsigned/unsigned-runbook"
   PODIUM_SIGN=none PODIUM_SIGN_KEY_PATH="$WORK/registry-sign2.key" \
   PODIUM_SQLITE_PATH="$WORK/podium2.db" PODIUM_FILESYSTEM_ROOT="$WORK/objects2" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg-unsigned" \
     --bind 127.0.0.1:8116 > "$WORK/srv-unsigned.log" 2>&1 &
   SRV2=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8116/healthz
   server_alive "$SRV2" "$WORK/srv-unsigned.log"
   echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"unsigned-runbook"}}}' \
     | PODIUM_REGISTRY=http://127.0.0.1:8116 \
       PODIUM_HARNESS=none \
       PODIUM_MATERIALIZE_ROOT="$WORK/out-unsigned" \
       PODIUM_VERIFY_SIGNATURES=always \
       PODIUM_SIGNATURE_PROVIDER=registry-managed \
       PODIUM_SIGNATURE_VERIFY_KEY="$PODIUM_SIGNATURE_VERIFY_KEY" \
       podium-mcp 2>/dev/null | python3 -m json.tool
   ```

6. Confirm the bridge rejects an unrecognized policy value at startup.

   ```bash
   echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"signed-runbook"}}}' \
     | PODIUM_REGISTRY=http://127.0.0.1:8115 PODIUM_VERIFY_SIGNATURES=sometimes podium-mcp; echo "exit=$?"
   ```

**Expected.**

- The server signs each artifact at ingest using the registry key. The server
  log reports `ingest signing: registry-managed key`, and the `load_artifact`
  response carries a `delivery_hash` (`sha256:<hex>`) and, in
  `delivery_signature`, a registry-managed envelope
  (`{"key_id":...,"signature":...}`) over that delivery hash. The third printed
  line is `[]`, because the response carries no `signature`,
  `raw_frontmatter`, or `manifest_merged` field.
- `podium artifact show` prints the signed artifact's body. The CLI read path
  does not verify; it confirms the artifact loads.
- With `PODIUM_VERIFY_SIGNATURES=always`, loading the signed high-sensitivity
  artifact through the MCP bridge recomputes the delivery hash, verifies the
  delivery signature, and materializes the artifact under `$WORK/out`.
- The unsigned artifact loaded under the same policy fails with
  `materialize.signature_missing` and writes nothing. No step here presents a
  signature that fails to verify. S69 step 6 covers that refusal,
  `materialize.signature_invalid`.
- `PODIUM_VERIFY_SIGNATURES` accepts `never` or `always`. Any other value exits
  the bridge with a nonzero status and the message `PODIUM_VERIFY_SIGNATURES
  must be never | always`.

**Cleanup.** Stop both servers (`kill "$SRV" "$SRV2"`) and `rm -rf "$WORK"`.

---

## S20: Migration from standalone to standard

**Goal.** Validate `admin migrate-to-standard`: state authored in a standalone
SQLite plus filesystem deployment lands in Postgres plus S3 with parity.

**Covers.** Standalone deployment, the migration command, standard deployment,
cross-store parity.

**Prerequisites.** `make services-up` and `test.env` (Postgres, S3). Skip if
absent. The scenario erases the local Postgres and MinIO volumes in step 3, so
any state another scenario left in those services is lost. Run it when no other
scenario's data is needed.

**Steps.**

1. Run the isolation block.
2. Build standalone state: author a registry, serve standalone, register a
   Git layer, and confirm a search returns results (as in S09, including its
   liveness check, on `127.0.0.1:8129`). Stop the standalone server.
3. Recreate the target stores empty. The migration refuses a copied row that
   the target already holds at a different content hash, so a Postgres volume
   carrying a row an earlier run wrote fails the command. `make services-down`
   does not serve here, because it keeps the volumes.

   ```bash
   (cd "$REAL_HOME/projects/podium" && docker compose down -v && make services-up)
   until [ "$(docker inspect -f '{{.State.Health.Status}}' podium-postgres 2>/dev/null)" = "healthy" ]; do
     sleep 2
   done
   until [ "$(docker inspect -f '{{.State.Status}}' podium-bootstrap 2>/dev/null)" = "exited" ]; do
     sleep 2
   done
   (cd "$REAL_HOME/projects/podium" && docker compose ps -a)
   ```

   **Expect.** Both loops return, so Postgres reports `healthy` and the bucket
   bootstrap has completed. `docker compose ps -a` then reports `postgres` and
   `minio` with a status that begins `Up`, and `bootstrap` with a status that
   begins `Exited (0)`, such as `Exited (0) 12 seconds ago`. No registry is started
   against these stores before step 4.

4. Load the standard-store environment and run the migration. The migration
   command takes its target from `--postgres <dsn>` and `--object-store <url>`
   (the §13.4 short form). The `--object-store` S3 URL carries the endpoint,
   bucket, credentials, region, and TLS toggle from `test.env`. The standalone
   source lives under `$WORK`, so name it with `--source-sqlite` and
   `--source-objects`. The `PODIUM_REGISTRY_STORE`, `PODIUM_OBJECT_STORE`, and
   `PODIUM_VECTOR_BACKEND` exports select the standard backends for the
   `podium serve --strict` run in step 5.

   ```bash
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres PODIUM_OBJECT_STORE=s3 PODIUM_VECTOR_BACKEND=pgvector
   S3URL="s3://${PODIUM_S3_ACCESS_KEY_ID}:${PODIUM_S3_SECRET_ACCESS_KEY}@localhost:9000/${PODIUM_S3_BUCKET}?region=${PODIUM_S3_REGION}&ssl=false"
   podium admin migrate-to-standard \
     --postgres "$PODIUM_POSTGRES_DSN" \
     --object-store "$S3URL" \
     --source-sqlite "$WORK/podium.db" \
     --source-objects "$WORK/objects"
   ```

4b. Confirm that the target refuses to serve until `sign-stored-rows` runs,
   then run it. The isolation block signs under
   `$WORK/registry-signing.key`, and the migrated Postgres store is outside
   that directory. The migration clears the target's record of the §13.4
   content-hash rewrite, so a start over the migrated rows is refused until
   the command records it.

   ```bash
   podium serve --strict --bind 127.0.0.1:8117; echo "exit=$?"
   podium admin sign-stored-rows --dry-run | tee "$WORK/dry-run.log"
   podium admin sign-stored-rows; echo "exit=$?"
   ```

   **Expect.** The start prints a non-zero `exit=` and an error naming
   `sign-stored-rows` and `--plan-digest`, and it serves nothing. The dry run
   lists the `deploy` row with `class=migrated signed_by=<key_id>`, because
   the standalone registry signed it under the same key. The run prints
   `exit=0`. Step 5 then serves as written. A start that serves the migrated
   Postgres store before `sign-stored-rows` ran is a defect.

5. Serve in strict mode against the standard stores and compare. Step 3
   recreated the Postgres and MinIO volumes, so the store holds the migrated
   rows alone and the listing below is the migrated set.

   ```bash
   podium serve --strict --bind 127.0.0.1:8117 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8117/healthz
   server_alive "$SRV" "$WORK/srv2.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8117
   podium layer list --registry "$PODIUM_REGISTRY"
   podium search --registry "$PODIUM_REGISTRY" "deploy"
   ```

**Expected.**

- The migration command reports the source plan it pumped into Postgres and S3:
  `tenants: 1`, `manifests: 1`, `layer configs: 1`, `admin grants: 0`, followed
  by `metadata migration complete (0 admin grant(s) preserved)` and `object
  migration complete (0 blob(s))`. The `deploy` skill stores its content inline
  in the manifest, so the filesystem object store holds no blobs and the object
  count is zero.
- The standard server lists the migrated `team` Git layer and returns the
  migrated `deploy` skill in a search for `deploy`, and those are the only rows
  the store holds.

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and `(cd "$REAL_HOME/projects/podium" && make services-down)`.

---

## S21: Read-only fallback on a primary outage

**Goal.** Validate that a standard deployment whose Postgres primary becomes
unreachable serves reads and refuses writes, then recovers.

**Covers.** Standard deployment, the read-only health state, write refusal,
recovery.

**Prerequisites.** A standard deployment whose Postgres can be stopped and
restarted independently (for example the `make services-up` Postgres container).
This scenario requires interrupting Postgres mid-run, so it is the hardest to
perform by hand; skip it if the database cannot be severed.

The read path during the outage depends on the database topology. §13.2.1
defines read-only mode as the state reached when the Postgres primary becomes
unreachable while a read replica stays up, and read endpoints serve from that
replica. The registry binary connects reads and writes through a single
`PODIUM_POSTGRES_DSN`, so replica-served reads require that DSN to point at an
endpoint that survives the primary outage (a connection pooler or replica
service). The `make services-up` stack runs a single Postgres with no replica.
Against that stack, stopping the single Postgres instance also stops reads, so
the read-continuity item below is observable on a primary-plus-replica deployment
instead. The mode flip, the write refusal, and the recovery are observable on
the single-Postgres stack.

**Steps.**

1. Run the isolation block, start services, load `test.env`, and serve in strict
   mode with Postgres and S3 (as in S14, on `127.0.0.1:8118`). Author the
   `report` skill from S14 and create the Git repository the write in step 2
   registers. Confirm a search works.

   ```bash
   podium artifact scaffold --type skill --description "Generate a quarterly report" "$WORK/reg/report"
   git -C "$WORK" init -q repo
   git -C "$WORK/repo" -c user.email=alice@acme.com -c user.name=alice commit -q --allow-empty -m init
   git -C "$WORK/repo" branch -M main
   ```

2. Stop the Postgres container (`docker stop` the database service), wait for the
   health probe to flip, and observe.

   ```bash
   podium status
   podium search --registry "$PODIUM_REGISTRY" "report"
   podium layer register --registry "$PODIUM_REGISTRY" --id new --repo "$WORK/repo" --ref main --public
   ```

3. Restart Postgres and confirm recovery.

**Expected.**

- After Postgres stops, `podium status` reports `registry mode: read_only`, and
  `/healthz` reports `mode: read_only`. The server log records `registry entered
  read_only mode after 3 probe failures` and the audit log records a
  `registry.read_only_entered` event.
- On a primary-plus-replica deployment, reads (search and load) continue to serve
  from the replica. On the single-Postgres `make services-up` stack there is no
  replica, so `podium search` returns HTTP 500 `registry.unavailable` while the
  primary is down; the read-continuity behavior is verified on a replica-backed
  deployment instead.
- The write (`layer register`) is refused with HTTP 503 `registry.read_only`.
- After Postgres restarts, the mode returns to ready after three consecutive
  probe successes, and `layer register` succeeds. The server log records
  `registry exited read_only mode` and the audit log records a
  `registry.read_only_exited` event.

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and `(cd "$REAL_HOME/projects/podium" && make services-down)`.

---

## S22: Domain modeling and discovery

**Goal.** Validate that a `DOMAIN.md` hierarchy defines the domain tree, and that
`domain show`, `domain search`, and `domain analyze` report it.

**Covers.** Standalone deployment, `DOMAIN.md` composition, `domain show`,
`domain search`, `domain analyze`.

**Steps.**

1. Run the isolation block.
2. Build a registry with two top-level domains and one nested domain, each
   carrying a `DOMAIN.md`, plus a few skills.

   ```bash
   mkdir -p "$WORK/reg/finance/close" "$WORK/reg/eng"
   cat > "$WORK/reg/finance/DOMAIN.md" <<'MD'
   ---
   description: "Finance team artifacts: AP, AR, close, and reporting."
   discovery:
     max_depth: 3
     fold_below_artifacts: 3
     keywords: [finance, accounting, close]
   ---

   # Finance

   Operations and reference material for the finance function.
   MD
   cat > "$WORK/reg/eng/DOMAIN.md" <<'MD'
   ---
   description: "Engineering runbooks and deploy automation."
   discovery:
     keywords: [engineering, deploy, infra]
   ---

   # Engineering
   MD
   podium artifact scaffold --type skill --description "Reconcile the general ledger at period end" "$WORK/reg/finance/close/reconcile"
   podium artifact scaffold --type skill --description "Post the monthly accrual journal" "$WORK/reg/finance/close/accrual"
   podium artifact scaffold --type skill --description "Roll out a service to production" "$WORK/reg/eng/deploy"
   ```

3. Serve and inspect the domain tree.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8119 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8119/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8119
   podium domain show --registry "$PODIUM_REGISTRY"
   podium domain search --registry "$PODIUM_REGISTRY" "accounting close"
   podium domain analyze --registry "$PODIUM_REGISTRY" --path finance
   ```

**Expected.**

- `domain show` renders the `finance`, `finance/close`, and `eng` domains, with
  the `DOMAIN.md` descriptions attached to `finance` and `eng`.
- `domain search "accounting close"` returns the `finance` domain and prints
  `Showing 1 of 1 results` (`total_matched: 1` under `--json`). The `finance` projection (its `DOMAIN.md` description plus
  the `finance, accounting, close` keywords) overlaps the query. With
  `--no-embeddings` the registry runs BM25 alone, so `eng` scores zero against
  this query and does not appear; the empty-query browse-all form
  (`domain search ""`) lists both domains.
- `domain analyze --path finance` prints domain-discovery metrics for the
  subtree: `artifact_count`, `recursive_count`, `child_count`,
  `passthrough_chain_length`, `tag_cluster_entropy`, and a per-child summary.
  The fold and split candidate lists apply the analyzer's own sparsity and
  tag-entropy heuristics (§4.5.5), independent of the `fold_below_artifacts` and
  `max_depth` rendering settings. This tree yields no candidates because
  `finance/close` holds two artifacts, which is above the fold threshold and
  below the split threshold.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S23: Authoring guardrails: lint rejects invalid manifests

**Goal.** Validate that `podium lint` accepts a valid registry and reports a
specific error for each kind of invalid artifact, before any server is involved.

**Covers.** Solo deployment, `lint`, required-field validation, the skill
name-match rule.

**Steps.**

1. Run the isolation block.
2. Author one valid skill and two invalid ones by hand.

   ```bash
   podium artifact scaffold --type skill --description "A valid skill" "$WORK/reg/good"

   # Invalid: SKILL.md has no description (a required field).
   mkdir -p "$WORK/reg/nodesc"
   printf -- '---\ntype: skill\nversion: 0.1.0\n---\n\n<!-- body in SKILL.md -->\n' > "$WORK/reg/nodesc/ARTIFACT.md"
   printf -- '---\nname: nodesc\n---\n\nbody\n' > "$WORK/reg/nodesc/SKILL.md"

   # Invalid: SKILL.md name does not match the leaf directory.
   mkdir -p "$WORK/reg/mismatch"
   printf -- '---\ntype: skill\nversion: 0.1.0\n---\n\n<!-- body in SKILL.md -->\n' > "$WORK/reg/mismatch/ARTIFACT.md"
   printf -- '---\nname: wrong-name\ndescription: Name does not match the directory\n---\n\nbody\n' > "$WORK/reg/mismatch/SKILL.md"
   ```

3. Lint the registry.

   ```bash
   podium lint --registry "$WORK/reg"; echo "exit=$?"
   ```

**Expected.**

- `podium lint` exits nonzero.
- It reports the missing-description violation for `nodesc` (a required-field
  error naming the `description` field).
- It reports the name-mismatch violation for `mismatch` (the SKILL.md `name`
  must equal the leaf directory).
- It does not report a violation for `good`. The output names each offending
  artifact, so a reader can map each message to its directory.

**Cleanup.** `rm -rf "$WORK"`.

---

## S24: Sync profiles and overrides

**Goal.** Validate that a sync profile captures a named subset, that
`profile edit` narrows it, and that `sync override` toggles a single artifact on
top of the resolved set.

**Covers.** Solo deployment, `sync save-as`, `profile edit`, `sync override`,
`sync --profile`.

**Steps.**

1. Run the isolation block.
2. Author a registry with three skills, configure a project, and materialize
   everything.

   ```bash
   podium artifact scaffold --type skill --description "Alpha skill" "$WORK/reg/alpha"
   podium artifact scaffold --type skill --description "Beta skill"  "$WORK/reg/beta"
   podium artifact scaffold --type skill --description "Gamma skill" "$WORK/reg/gamma"
   mkdir -p "$WORK/proj" && cd "$WORK/proj"
   podium init --registry "$WORK/reg" --harness claude-code --target "$WORK/proj"
   podium sync
   find "$WORK/proj/.claude/skills" -maxdepth 1 -mindepth 1 -type d | sort
   ```

3. Capture the current target as a profile, then narrow it to exclude `gamma`,
   and re-sync through the profile.

   ```bash
   podium sync save-as --profile minimal
   podium profile edit minimal --add-exclude 'gamma'
   podium sync --profile minimal
   find "$WORK/proj/.claude/skills" -maxdepth 1 -mindepth 1 -type d | sort
   ```

4. Force `gamma` back on with an ephemeral override and inspect the target. The
   override writes `gamma` through the adapter immediately, so the target carries
   it before any further sync runs.

   ```bash
   podium sync override --add 'gamma' --target "$WORK/proj"
   find "$WORK/proj/.claude/skills" -maxdepth 1 -mindepth 1 -type d | sort
   ```

5. Run a manual `podium sync`. Per §7.5.4 a manual sync (no `--watch`) is the
   "reset to baseline" gesture: it re-resolves the profile, rewrites the target,
   and clears the lock's `toggles`. The override is discarded and `gamma` is
   removed again.

   ```bash
   podium sync --profile minimal
   find "$WORK/proj/.claude/skills" -maxdepth 1 -mindepth 1 -type d | sort
   ```

6. Re-apply the override, then clear it with `--reset` instead of a manual sync.
   `--reset` clears the toggles and re-applies the profile's resolved set, which
   drops the `add`ed `gamma`.

   ```bash
   podium sync override --add 'gamma' --target "$WORK/proj"
   podium sync override --reset --target "$WORK/proj"
   find "$WORK/proj/.claude/skills" -maxdepth 1 -mindepth 1 -type d | sort
   ```

**Expected.**

- The first `sync` materializes `alpha`, `beta`, and `gamma`.
- `sync save-as --profile minimal` writes a `profiles.minimal` block into
  `$WORK/proj/.podium/sync.yaml`. `profile edit minimal --add-exclude 'gamma'`
  adds the exclude pattern. The profile sync then materializes `alpha` and
  `beta` only, and `gamma` is removed from the target.
- `sync override --add 'gamma'` reports `toggles.add: gamma` and re-materializes
  `gamma` immediately, so the target lists `alpha`, `beta`, and `gamma`.
- The manual `podium sync --profile minimal` clears the toggle and rewrites the
  target to `alpha` and `beta` only, removing `gamma`.
- After a second `sync override --add 'gamma'` followed by `sync override
  --reset`, the target lists `alpha` and `beta` only; `--reset` removes the
  `add`ed `gamma` the same way a manual sync would.

**Cleanup.** `rm -rf "$WORK"`.

---

## S25: Sync scope filtering by path and type

**Goal.** Validate that `sync --include`, `sync --exclude`, and `sync --type`
materialize only the requested subset.

**Covers.** Solo deployment, `sync` scope filters.

**Steps.**

1. Run the isolation block.
2. Author a registry with two domains and mixed types.

   ```bash
   podium artifact scaffold --type skill   --description "Close the books" "$WORK/reg/finance/close"
   podium artifact scaffold --type context --description "Finance policy"  "$WORK/reg/finance/policy"
   podium artifact scaffold --type skill   --description "Deploy service"  "$WORK/reg/eng/deploy"
   mkdir -p "$WORK/proj" && cd "$WORK/proj"
   podium init --registry "$WORK/reg" --harness claude-code --target "$WORK/proj"
   ```

3. Materialize subsets with each filter, into a fresh target each time.

   ```bash
   echo "--- include finance only ---"
   podium sync --include 'finance/**' --target "$WORK/inc"
   find "$WORK/inc" -type f | sort

   echo "--- exclude eng ---"
   podium sync --exclude 'eng/**' --target "$WORK/exc"
   find "$WORK/exc" -type f | sort

   echo "--- type skill only ---"
   podium sync --type skill --target "$WORK/onlyskill"
   find "$WORK/onlyskill" -type f | sort
   ```

**Expected.**

- `--include 'finance/**'` materializes only the two finance artifacts; `eng/deploy`
  is absent.
- `--exclude 'eng/**'` materializes the two finance artifacts and omits
  `eng/deploy`.
- `--type skill` materializes only the skills (`finance/close` and `eng/deploy`)
  and omits the `finance/policy` context.

**Cleanup.** `rm -rf "$WORK"`.

---

## S26: Reverse-dependency impact analysis

**Goal.** Validate that `podium impact` lists the artifacts that depend on a
given artifact through `extends` and `delegates_to` edges.

**Covers.** Standalone deployment, the dependency graph, `impact`.

**Steps.**

1. Run the isolation block.
2. Author a base skill, a skill that extends it, and an agent that delegates to
   it.

   ```bash
   podium artifact scaffold --type skill --description "Base deploy routine" "$WORK/reg/deploy-base"
   podium artifact scaffold --type skill --description "Pro deploy routine" "$WORK/reg/deploy-pro"
   podium artifact scaffold --type agent --delegates-to deploy-base --description "Release agent" "$WORK/reg/release-agent"
   # Make deploy-pro extend deploy-base (bump version and add the extends field).
   python3 - "$WORK/reg/deploy-pro/ARTIFACT.md" <<'PY'
   import sys
   p = sys.argv[1]; s = open(p).read()
   open(p, "w").write(s.replace("version: 0.1.0\n", "version: 0.2.0\nextends: deploy-base\n"))
   PY
   ```

3. Serve and query impact.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8120 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8120/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8120
   podium impact --registry "$PODIUM_REGISTRY" deploy-base
   ```

**Expected.**

- `impact deploy-base` lists `deploy-pro` (an `extends` dependent) and
  `release-agent` (a `delegates_to` dependent).
- A leaf artifact with no dependents (for example `release-agent`) reports an
  empty impact set.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S27: Inbound webhook-driven reingest

**Goal.** Validate that an HMAC-signed inbound webhook delivery triggers a layer
reingest, and that a delivery with a wrong signature is rejected.

**Covers.** Standalone deployment, Git-source layers, the inbound webhook
endpoint, HMAC verification.

**Steps.**

1. Run the isolation block.
2. Create a Git repository with one artifact, serve, and register it as a layer.
   Capture the layer's HMAC webhook secret from the register output. `podium
   layer register` writes the registration JSON to stdout with `webhook_url` and
   `webhook_secret` fields, and repeats the webhook URL on a labeled line on
   stderr.

   ```bash
   mkdir -p "$WORK/repo" && cd "$WORK/repo" && git init -q
   podium artifact scaffold --type skill --description "Deploy the service" "$WORK/repo/deploy"
   git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "deploy"
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8121 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8121/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8121
   podium layer register --registry "$PODIUM_REGISTRY" --id team --repo "$WORK/repo" --ref main --public > "$WORK/reg.out" 2> "$WORK/reg.err"
   SECRET=$(grep -hoiE 'webhook_secret"?[: =]+"?[A-Za-z0-9._-]{16,}' "$WORK/reg.out" "$WORK/reg.err" | grep -oE '[A-Za-z0-9._-]{16,}$' | head -1)
   echo "secret: ${SECRET:0:6}…"
   podium layer reingest --registry "$PODIUM_REGISTRY" team   # first ingest at commit 1
   ```

3. Add a second artifact, commit, then deliver a signed webhook to trigger a
   reingest instead of calling `layer reingest`.

   ```bash
   podium artifact scaffold --type skill --description "Roll back a deploy" "$WORK/repo/rollback"
   cd "$WORK/repo" && git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "rollback"
   BODY='{"ref":"refs/heads/main"}'
   SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $NF}')"
   curl -s -o /dev/null -w "valid delivery: %{http_code}\n" -X POST \
     -H "X-Hub-Signature-256: $SIG" -H "Content-Type: application/json" \
     --data "$BODY" "$PODIUM_REGISTRY/v1/ingest/webhook/team"
   sleep 2
   podium search --registry "$PODIUM_REGISTRY" "rollback"
   echo "--- wrong signature ---"
   curl -s -o /dev/null -w "bad delivery: %{http_code}\n" -X POST \
     -H "X-Hub-Signature-256: sha256=deadbeef" -H "Content-Type: application/json" \
     --data "$BODY" "$PODIUM_REGISTRY/v1/ingest/webhook/team"
   ```

**Expected.**

- The valid webhook delivery returns a 2xx and the layer reingests the new
  commit; the subsequent search returns the `rollback` skill.
- The wrong-signature delivery is rejected with a 4xx and the
  `ingest.webhook_invalid` code, and it does not reingest.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S28: Audit log and right-to-be-forgotten erasure

**Goal.** Validate that read calls are recorded in the audit log with the
caller's identity, and that `admin erase` redacts a subject's entries while the
hash chain still verifies.

**Covers.** Standalone deployment, injected-session-token identity, the audit
log, `admin erase`, `admin retention`.

**Steps.**

1. Run the isolation block.
2. Write the runtime key into the registry's keys file, then boot an
   injected-session-token server over a small registry against that file (as in
   S12, with `--bind 127.0.0.1:8122`). The audit log lands at
   `$PODIUM_AUDIT_LOG_PATH` from the isolation block.

   ```bash
   podium artifact scaffold --type skill --description "Quarterly report" "$WORK/reg/report"
   go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" >/dev/null 2>&1
   podium admin runtime register --keys-file "$WORK/keys/runtimes.json" --issuer manual-runtime --algorithm RS256 --public-key-file "$WORK/keys/runtime-pub.pem"
   export PODIUM_IDENTITY_PROVIDER=injected-session-token
   export PODIUM_RUNTIME_KEYS_PATH="$WORK/keys/runtimes.json"
   export PODIUM_OAUTH_AUDIENCE=https://podium.manual
   # The --layer-path layer is private by default once an identity provider is set (§13.12).
   export PODIUM_DEFAULT_LAYER_VISIBILITY=public
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8122 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8122/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8122
   ```

3. Generate audited activity as alice, then inspect the audit log.

   ```bash
   ALICE=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com)
   PODIUM_SESSION_TOKEN="$ALICE" podium search --registry "$PODIUM_REGISTRY" "report"
   PODIUM_SESSION_TOKEN="$ALICE" podium artifact show --registry "$PODIUM_REGISTRY" report
   grep -c alice "$PODIUM_AUDIT_LOG_PATH"
   ```

4. Erase alice from the local audit log, then re-inspect.

   ```bash
   podium admin erase --local --audit-path "$PODIUM_AUDIT_LOG_PATH" --operator admin@acme.com --salt 0123456789abcdef alice@acme.com
   grep -c alice@acme.com "$PODIUM_AUDIT_LOG_PATH" || echo "alice@acme.com no longer present"
   ```

**Expected.**

- After alice's search and load, the audit log contains entries that carry her
  subject and email.
- `admin erase` reports the count of entries it redacted for alice.
- After the erase, alice's email no longer appears in the audit log (it is
  replaced by a salted tombstone). No step here checks the audit hash chain,
  because no command runs the chain verification; the erase is specified to
  rewrite the record in place without breaking the chain.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S29: Workspace overlay merges local artifacts

**Goal.** Validate that a workspace-local overlay directory contributes its
artifacts to the effective view served through the MCP bridge, on top of the
registry.

**Covers.** Standalone deployment, the `podium-mcp` overlay
(`PODIUM_OVERLAY_PATH`), search and load over the merged view.

**Steps.**

1. Run the isolation block.
2. Serve a registry with one skill, and author a separate workspace-local
   overlay directory with a different skill.

   ```bash
   podium artifact scaffold --type skill --description "Registry-published skill" "$WORK/reg/published"
   podium artifact scaffold --type skill --description "Local draft skill not in the registry" "$WORK/overlay/local-draft"
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8123 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8123/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8123
   ```

3. Search through the bridge without and then with the overlay. The bridge
   resolves the registry's public key from `$PODIUM_SIGN_KEY_PATH`, which the
   isolation block exports.

   ```bash
   echo "--- no overlay: registry only ---"
   printf '%s\n%s\n' \
     '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}' \
     '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_artifacts","arguments":{"query":"skill"}}}' \
     | PODIUM_REGISTRY="$PODIUM_REGISTRY" podium-mcp 2>/dev/null | grep -o '"id":"[^"]*"' | sort -u

   echo "--- with overlay: registry + local-draft ---"
   printf '%s\n%s\n' \
     '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}' \
     '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_artifacts","arguments":{"query":"skill"}}}' \
     | PODIUM_REGISTRY="$PODIUM_REGISTRY" PODIUM_OVERLAY_PATH="$WORK/overlay" podium-mcp 2>/dev/null | grep -o '"id":"[^"]*"' | sort -u
   ```

**Expected.**

- Without the overlay, search returns `published` and not `local-draft`.
- With `PODIUM_OVERLAY_PATH` set, search returns both `published` and
  `local-draft`, confirming the overlay is merged into the effective view that
  the bridge serves.
- The overlay artifact is workspace-local: it is not present in the registry
  (a direct `podium search` against the registry does not return `local-draft`).

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S30: Offline-first cache resilience

**Goal.** Validate that the MCP bridge serves a previously-loaded artifact from
its content cache when the registry is unreachable, under the offline-first
cache mode.

**Covers.** Standalone deployment, the `podium-mcp` content cache,
`PODIUM_CACHE_MODE=offline-first`, `cache prune`.

**Steps.**

1. Run the isolation block.
2. Serve a registry and warm the bridge cache by loading an artifact once. The
   registry signs the artifact, and the bridge verifies it under the default
   `always` policy with the public key it resolves from `$PODIUM_SIGN_KEY_PATH`,
   which the isolation block exports.

   ```bash
   podium artifact scaffold --type skill --description "Cached runbook" "$WORK/reg/runbook"
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8124 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8124/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8124
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"runbook"}}}'
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   printf '%s\n%s\n' "$INIT" "$LOAD" | PODIUM_REGISTRY="$PODIUM_REGISTRY" PODIUM_CACHE_DIR="$WORK/cache" podium-mcp 2>/dev/null | grep -c '"runbook"'
   find "$WORK/cache" -type f | head
   ```

3. Stop the registry, then load the same artifact again in offline-first mode.

   ```bash
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   printf '%s\n%s\n' "$INIT" "$LOAD" \
     | PODIUM_REGISTRY="$PODIUM_REGISTRY" PODIUM_CACHE_DIR="$WORK/cache" PODIUM_CACHE_MODE=offline-first podium-mcp 2>"$WORK/offline.log" | grep -c '"runbook"'
   ```

4. Inspect prunable cache buckets.

   ```bash
   podium cache prune --dir "$WORK/cache" --days 0 --dry-run
   ```

**Expected.**

- The first load returns the `runbook` artifact and writes content into
  `$WORK/cache`.
- After the registry is stopped, the offline-first load still returns `runbook`
  from the cache rather than failing with a network error.
- `cache prune --dry-run` lists the cached bucket and reports that it would be
  removed, without deleting it.

**Cleanup.** `rm -rf "$WORK"` (the server is already stopped).

---

## S31: Import an existing skill tree into a layer

**Goal.** Validate that `podium import` converts a directory of plain skills into
a Podium-shaped layer that lints, serves, and is searchable.

**Covers.** Solo and standalone deployment, `import`, `lint`, search over the
imported layer.

**Steps.**

1. Run the isolation block.
2. Create a plain skills tree in the Claude skills layout (one `SKILL.md` per
   skill directory, without Podium's `ARTIFACT.md`).

   ```bash
   mkdir -p "$WORK/skills/greet" "$WORK/skills/summarize"
   printf -- '---\nname: greet\ndescription: Greet a user politely\n---\n\nGreet the user by name.\n' > "$WORK/skills/greet/SKILL.md"
   printf -- '---\nname: summarize\ndescription: Summarize a document\n---\n\nProduce a short summary.\n' > "$WORK/skills/summarize/SKILL.md"
   ```

3. Import the tree into a Podium layer, lint it, then serve and search.

   ```bash
   podium import --source "$WORK/skills" --target "$WORK/reg" --type skill
   find "$WORK/reg" -name ARTIFACT.md | sort
   podium lint --registry "$WORK/reg"; echo "lint exit=$?"
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8125 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8125/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8125
   podium search --registry "$PODIUM_REGISTRY" "greet"
   ```

**Expected.**

- `podium import` writes a Podium-shaped layer under `$WORK/reg`: each source
  skill becomes a directory with an `ARTIFACT.md` (declaring `type: skill` and a
  version) beside its `SKILL.md`.
- `podium lint` reports `lint: no issues.` on the imported layer.
- The standalone server ingests the imported skills and search returns `greet`.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S32: Gateway-delegated identity with trusted-headers

**Goal.** Validate that a standalone server fronted by a gateway trusts the
gateway-injected `X-Podium-User-*` identity headers, applies per-layer
visibility (§4.6) from them, and honors the headers only on a request carrying
the matching proxy secret.

**Covers.** Standalone deployment, the `trusted-headers` identity provider
(§6.3.3), gateway-injected identity headers, per-layer visibility, the
`PODIUM_TRUSTED_PROXY_SECRET` request-level gate.

**Steps.**

1. Run the isolation block.
2. Write a registry config with a public layer and a group-restricted layer.

   ```bash
   mkdir -p "$WORK/pub/handbook" "$WORK/eng/deploy"
   podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
   podium artifact scaffold --type skill --description "Engineering deploy" --force "$WORK/eng/deploy"
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: public-handbook
         source: { local: { path: $WORK/pub } }
         visibility: { public: true }
       - id: eng-internal
         source: { local: { path: $WORK/eng } }
         visibility: { groups: [engineering] }
   YAML
   ```

3. Boot the server in `trusted-headers` mode with a proxy secret. The bind is
   loopback, so no `--allow-public-bind` is needed.

   ```bash
   export PODIUM_IDENTITY_PROVIDER=trusted-headers
   export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8132 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8132/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8132
   ```

4. Issue requests as the gateway would, injecting identity headers plus the
   proxy secret. The `code` helper prints the HTTP status of a load.

   ```bash
   code() { curl -s -o /dev/null -w "%{http_code}\n" "$@"; }
   SEC="X-Podium-Proxy-Secret: gateway-secret"
   echo "alice handbook:   $(code -H "X-Podium-User-Sub: alice@acme.com" -H "X-Podium-User-Groups: engineering" -H "$SEC" "$URL/v1/load_artifact?id=handbook")"
   echo "alice deploy:     $(code -H "X-Podium-User-Sub: alice@acme.com" -H "X-Podium-User-Groups: engineering" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
   echo "bob deploy:       $(code -H "X-Podium-User-Sub: bob@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
   echo "anon deploy:      $(code "$URL/v1/load_artifact?id=deploy")"
   echo "anon handbook:    $(code "$URL/v1/load_artifact?id=handbook")"
   echo "no-secret deploy: $(code -H "X-Podium-User-Sub: alice@acme.com" -H "X-Podium-User-Groups: engineering" "$URL/v1/load_artifact?id=deploy")"
   ```

**Expected.**

- `alice handbook` and `alice deploy` return `200`: the engineering caller sees
  the public layer and the engineering layer.
- `bob deploy` and `anon deploy` return `404`: a non-member and an anonymous
  caller do not see the engineering layer.
- `anon handbook` returns `200`: the public layer is visible without identity.
- `no-secret deploy` returns `404`: identity headers without the matching
  `X-Podium-Proxy-Secret` are discarded, so the caller is anonymous.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S33: Gateway-delegated providers fail closed on misconfiguration

**Goal.** Validate that the gateway-delegated providers refuse to start on the
misconfigurations the startup guards cover, naming the config error code rather
than serving an unverifiable or forgeable registry.

**Covers.** The `config.invalid_issuer_scheme`, `config.oidc_jwt_audience_unset`,
`config.invalid_idp_group_mapping`, and `config.trusted_headers_public_bind`
startup guards, including the audience guard against a comma-separated list
whose every entry is blank and the group-mapping guard against a non-empty value
that resolves to no entry (§6.3.1, §6.3.3, §13.10, §13.12).

**Steps.**

1. Run the isolation block, then scaffold a one-artifact layer the server can
   load.

   ```bash
   mkdir -p "$WORK/reg/seed"
   podium artifact scaffold --type context --description "seed" --force "$WORK/reg/seed"
   ```

2. `oidc-jwt` with a non-`https` issuer is refused. This runs in the foreground
   and exits immediately.

   ```bash
   PODIUM_IDENTITY_PROVIDER=oidc-jwt \
     PODIUM_OAUTH_ISSUER=http://acme.okta.example/oauth2/default \
     PODIUM_OAUTH_AUDIENCE=https://podium.acme.example \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
   echo "exit=$?"
   ```

3. `oidc-jwt` without `PODIUM_OAUTH_AUDIENCE` is refused, and so is a list
   whose every entry is blank. Both runs exit immediately.

   ```bash
   PODIUM_IDENTITY_PROVIDER=oidc-jwt \
     PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
   echo "exit=$?"

   PODIUM_IDENTITY_PROVIDER=oidc-jwt \
     PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default \
     PODIUM_OAUTH_AUDIENCE=" , " \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
   echo "exit=$?"
   ```

4. `trusted-headers` on a non-loopback bind without a proxy secret or
   `--allow-public-bind` is refused.

   ```bash
   PODIUM_IDENTITY_PROVIDER=trusted-headers \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 0.0.0.0:8133
   echo "exit=$?"
   ```

5. A non-empty `PODIUM_IDP_GROUP_MAPPING` that carries a malformed entry is
   refused, and so is one that resolves to no `<claim-value>=<group-name>`
   entry. The two runs below exercise one arm each, and both exit immediately.

   ```bash
   PODIUM_IDENTITY_PROVIDER=oidc-jwt \
     PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default \
     PODIUM_OAUTH_AUDIENCE=https://podium.acme.example \
     PODIUM_IDP_GROUP_MAPPING=00g1financeOID \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
   echo "exit=$?"

   PODIUM_IDENTITY_PROVIDER=oidc-jwt \
     PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default \
     PODIUM_OAUTH_AUDIENCE=https://podium.acme.example \
     PODIUM_IDP_GROUP_MAPPING=" , " \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
   echo "exit=$?"
   ```

**Expected.**

- Step 2 exits non-zero and prints `config.invalid_issuer_scheme`.
- Step 3 exits non-zero and prints `config.oidc_jwt_audience_unset` on the run
  with the variable unset.
- Step 3 exits non-zero and prints `config.oidc_jwt_audience_unset` again on the
  run with `PODIUM_OAUTH_AUDIENCE=" , "`, because every entry is blank after
  trimming and the set resolves to no audience.
- Step 4 exits non-zero and prints `config.trusted_headers_public_bind`, naming
  the non-loopback bind address.
- Step 5 exits non-zero and prints `config.invalid_idp_group_mapping` on the run
  with `PODIUM_IDP_GROUP_MAPPING=00g1financeOID`, because the entry carries no
  `=`.
- Step 5 exits non-zero and prints `config.invalid_idp_group_mapping` again on
  the run with `PODIUM_IDP_GROUP_MAPPING=" , "`, because the value is non-empty
  and resolves to no entry.
- Each server refuses to start, so no background process is left to stop.

**Cleanup.** `rm -rf "$WORK"`.

---

## S34: Marketplace publishing through a `kind: marketplace` sync target

**Goal.** Validate that `podium sync` renders the catalog into a harness-native
marketplace repository and runs the target's operator-configured workflow to push
it to a git remote, that `--check` and `--dry-run` write nothing, and that a
re-run against an unchanged catalog produces no new commit (§7.5.2, §7.8).

**Covers.** The `kind: marketplace` target, `podium sync --config`, the Claude,
Codex, and Cursor marketplace emitters, the root manifest keys each vendor
format requires, plugin grouping by scope filter, the per-target `workflow`
(`prepare`/`publish`), `--check` and `--dry-run`, and reconciliation
(`skip_if_no_changes`).

**Prerequisites.** `git` on `PATH`. No server and no live infrastructure: the
remote is a local bare repository, so nothing is pushed off the machine. Set a
deterministic git identity for the workflow's commits.

```bash
export GIT_AUTHOR_NAME="podium-bot" GIT_AUTHOR_EMAIL="bot@acme.com"
export GIT_COMMITTER_NAME="podium-bot" GIT_COMMITTER_EMAIL="bot@acme.com"
```

**Steps.**

1. Run the isolation block.
2. Create a filesystem registry whose artifacts fall under two plugin paths.

   ```bash
   podium artifact scaffold --type skill --description "Quarterly close" "$WORK/reg/finance/close"
   podium artifact scaffold --type skill --description "Budget review"   "$WORK/reg/finance/budget"
   podium artifact scaffold --type skill --description "Refund helper"   "$WORK/reg/payment-helpers/refund"
   ```

3. Create a local bare repository and seed its `main` branch with one commit, so
   the workflow's `git clone --branch main` resolves.

   ```bash
   export REMOTE="$WORK/remote.git"
   git init --bare -b main "$REMOTE"
   seed="$(mktemp -d)"; git -C "$seed" init -b main
   git -C "$seed" commit --allow-empty -m init
   git -C "$seed" remote add origin "$REMOTE"; git -C "$seed" push origin main
   ```

4. Write a `sync.yaml` with one `kind: marketplace` target. Its `target:` is the
   working directory the `prepare` phase clones into, `harnesses:` is the harness
   set whose marketplace manifests coexist in one repository, and `plugins:`
   groups the artifacts by scope filter.

   ```bash
   mkdir -p "$WORK/proj/.podium"
   cat > "$WORK/proj/.podium/sync.yaml" <<YAML
   defaults:
     registry: $WORK/reg
     identity: publisher@acme.com
   targets:
     - id: acme-agents
       kind: marketplace
       target: $WORK/proj/build/acme-agents
       git:
         remote: $REMOTE
         branch: main
       harnesses: [claude-code, codex, cursor]
       commit_message: "Sync Podium catalog ({{.ChangedCount}} changes)"
       plugins:
         - name: finance-pack
           include: ["finance/**"]
         - name: helpers
           include: ["payment-helpers/**"]
       workflow:
         prepare:
           - sh: 'if [ -d "\$PODIUM_WORKDIR/.git" ]; then git -C "\$PODIUM_WORKDIR" fetch origin "\$PODIUM_GIT_BRANCH" && git -C "\$PODIUM_WORKDIR" reset --hard "origin/\$PODIUM_GIT_BRANCH"; else git clone --branch "\$PODIUM_GIT_BRANCH" "\$PODIUM_GIT_REMOTE" "\$PODIUM_WORKDIR"; fi'
         publish:
           - run: ["git", "-C", "\$PODIUM_WORKDIR", "add", "-A"]
           - run: ["git", "-C", "\$PODIUM_WORKDIR", "commit", "-m", "\$PODIUM_COMMIT_MESSAGE"]
             skip_if_no_changes: true
           - run: ["git", "-C", "\$PODIUM_WORKDIR", "push", "origin", "\$PODIUM_GIT_BRANCH"]
   YAML
   ```

5. Validate the config without materializing.

   ```bash
   podium sync --config "$WORK/proj/.podium/sync.yaml" --check
   echo "exit=$?"
   ```

6. Render to a temporary directory and print the substituted commands without
   pushing.

   ```bash
   podium sync --config "$WORK/proj/.podium/sync.yaml" --dry-run
   ```

7. Render and publish.

   ```bash
   podium sync --config "$WORK/proj/.podium/sync.yaml"
   ```

8. Inspect the pushed repository by cloning the remote.

   ```bash
   clone="$(mktemp -d)"; git clone -q "$REMOTE" "$clone"
   find "$clone" -name marketplace.json -o -name plugin.json | grep -v '/.git/' | sort
   ls -1 "$clone" | grep -v '^.git$'
   ```

9. Read the root keys of each vendor manifest. The Claude and Cursor formats
   require a root `owner` object carrying a `name`, and Claude Desktop refuses
   to import a marketplace repository whose manifest omits it. The Codex format
   defines no `owner`.

   ```bash
   python3 - "$clone" <<'PY'
   import json, pathlib, sys
   clone = pathlib.Path(sys.argv[1])
   for rel in [".claude-plugin/marketplace.json",
               ".cursor-plugin/marketplace.json",
               ".agents/plugins/marketplace.json"]:
       m = json.loads((clone / rel).read_text())
       if "owner" not in m:
           owner = "absent"
       elif isinstance(m["owner"], dict):
           owner = m["owner"].get("name", "object without a name")
       else:
           owner = f"not an object: {m['owner']!r}"
       plugins = sorted(p["name"] for p in m["plugins"])
       print(f"{rel}: name={m.get('name')} owner={owner} plugins={plugins}")
   PY
   ```

10. Re-run the sync against the unchanged catalog.

    ```bash
    before="$(git ls-remote "$REMOTE" main | cut -f1)"
    podium sync --config "$WORK/proj/.podium/sync.yaml"
    after="$(git ls-remote "$REMOTE" main | cut -f1)"
    [ "$before" = "$after" ] && echo "idempotent: no new commit" || echo "ERROR: new commit"
    ```

**Expected.**

- Step 5 exits 0 and writes no target tree (`$WORK/proj/build/acme-agents` holds
  no `.claude-plugin/`).
- Step 6 prints each `prepare` and `publish` command with its `PODIUM_*`
  variables substituted, and the remote's commit count is unchanged.
- Step 7 reports `changed: true`, lists `(manifest)` and the three `finance/` and
  `payment-helpers/` artifacts, and reports `published: true`. The remote gains
  one commit.
- Step 8 lists `.claude-plugin/marketplace.json`, `.agents/plugins/marketplace.json`,
  and `.cursor-plugin/marketplace.json` (the three vendor manifests coexisting at
  their fixed locations) plus the per-harness `claude/`, `codex/`, and `cursor/`
  plugin subtrees.
- Step 9 prints `name=acme-agents` and `plugins=['finance-pack', 'helpers']` for
  all three manifests. The Claude and Cursor manifests print
  `owner=acme-agents`, taken from the target `id`. The Codex manifest prints
  `owner=absent`, because the Codex format defines no root `owner`. A Claude or
  Cursor manifest that prints `owner=absent` or `owner=object without a name`
  fails its vendor schema and does not import.
- Step 10 prints `idempotent: no new commit`: the render produced no diff, so
  `skip_if_no_changes` suppressed the commit and the remote `main` is unchanged.

**Cleanup.** `rm -rf "$WORK"`.

---

## S35: Webhook receiver hardening: admin gate and SSRF policy

**Goal.** Validate that the webhook receiver CRUD endpoints (`/v1/webhooks`, §7.3.2)
require the per-tenant admin role, that the SSRF policy rejects a non-`https` or
private-address receiver URL by default, and that `PODIUM_WEBHOOK_ALLOWED_TARGETS`
overrides the address rejection.

**Covers.** Standalone deployment, injected-session-token identity, the receiver
authorization gate, the receiver-URL SSRF policy, the `PODIUM_WEBHOOK_ALLOWED_TARGETS`
allowlist, and the per-receiver `debounce` field.

**Steps.**

1. Run the isolation block.
2. Generate a runtime key (`go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys"`), write it
   into the keys file with `podium admin runtime register --keys-file`, then boot an
   injected-session-token standalone server against that file with `alice@acme.com`
   as a bootstrap admin over a one-artifact registry (as in S12 and
   S13, with `PODIUM_BOOTSTRAP_ADMINS=alice@acme.com`, `PODIUM_OAUTH_AUDIENCE=https://podium.manual`,
   `PODIUM_RUNTIME_KEYS_PATH="$WORK/keys/runtimes.json"`,
   and `--bind 127.0.0.1:8134`). Export `PODIUM_REGISTRY=http://127.0.0.1:8134`.
3. Mint an admin and a non-admin token, and exercise the receiver CRUD over HTTP.
   The token is sent as `Authorization: Bearer`. The addresses use the
   documentation range `203.0.113.0/24` (a public, non-private block) so a public
   `https` URL is accepted without a live receiver, since registration validates
   the URL but does not connect.

   ```bash
   ALICE=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com)
   BOB=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
   post() {
     if [ -n "$1" ]; then
       curl -s -w '\n%{http_code}\n' -X POST "$PODIUM_REGISTRY/v1/webhooks" \
         -H "Authorization: Bearer $1" -H 'Content-Type: application/json' -d "$2"
     else
       curl -s -w '\n%{http_code}\n' -X POST "$PODIUM_REGISTRY/v1/webhooks" \
         -H 'Content-Type: application/json' -d "$2"
     fi
   }

   echo "--- anonymous (no token): rejected ---"
   post "" '{"url":"https://203.0.113.10/h","event_filter":["layer.ingested"]}'
   echo "--- bob (non-admin): auth.forbidden ---"
   post "$BOB" '{"url":"https://203.0.113.10/h","event_filter":["layer.ingested"]}'
   echo "--- alice (admin) public https: created ---"
   post "$ALICE" '{"url":"https://203.0.113.10/h","event_filter":["layer.ingested"]}'
   echo "--- alice loopback https: SSRF address rejection ---"
   post "$ALICE" '{"url":"https://127.0.0.1:9443/h","event_filter":["layer.ingested"]}'
   echo "--- alice public http (not https): SSRF scheme rejection ---"
   post "$ALICE" '{"url":"http://203.0.113.10/h","event_filter":["layer.ingested"]}'
   echo "--- alice debounce field accepted, and the reported value is writable ---"
   CREATED=$(post "$ALICE" '{"url":"https://203.0.113.11/h","event_filter":["layer.ingested"],"debounce":"60s"}')
   echo "$CREATED"
   RID=$(printf '%s\n' "$CREATED" | sed '$d' | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   curl -s -w '\n%{http_code}\n' -X PUT "$PODIUM_REGISTRY/v1/webhooks/$RID" \
     -H "Authorization: Bearer $ALICE" -H 'Content-Type: application/json' \
     -d '{"debounce":"1m0s"}'
   ```

4. Stop the server, then boot a second one identically but with
   `PODIUM_WEBHOOK_ALLOWED_TARGETS=127.0.0.1` and `--bind 127.0.0.1:8135`
   (`PODIUM_REGISTRY=http://127.0.0.1:8135`), and register a loopback receiver.

   ```bash
   echo "--- alice loopback https with the host allowlisted: created ---"
   post "$ALICE" '{"url":"https://127.0.0.1:9443/h","event_filter":["layer.ingested"]}'
   ```

**Expected.**

- The anonymous POST is rejected (HTTP 401, `auth.untrusted_runtime`): injected-session-token
  mode rejects an unverified caller before the handler runs.
- bob's POST returns HTTP 403 with `auth.forbidden`, naming bob as not an admin: the
  receiver CRUD is admin-gated.
- alice's public `https` POST returns HTTP 201 and the created receiver (id,
  event filter, and the secret in full). The secret is revealed once at
  creation, and every later read, list, and update reports it as `***`.
- alice's loopback `https` POST returns `registry.invalid_argument` naming the
  disallowed host: the SSRF policy rejects a private address.
- alice's public `http` POST returns `registry.invalid_argument`: the SSRF policy
  requires `https`.
- alice's POST with `"debounce":"60s"` returns HTTP 201, and the created receiver
  reports `"debounce": "1m0s"`: the field is emitted as the duration string the
  request accepts rather than as a nanosecond count. The `PUT` that feeds that
  value back to the created receiver returns HTTP 200 and reports
  `"debounce": "1m0s"` again, so a client can read a receiver and write the read
  value back unchanged. A `registry.invalid_argument` on the `PUT` means the
  create reported a form the update path does not parse.
- On the allowlist server, the loopback `https` POST returns HTTP 201: an
  allowlisted host overrides the address rejection (the `https` requirement still
  applies).

**Cleanup.** Stop both servers and `rm -rf "$WORK"`.

---

## S36: Successful oidc-jwt verification against a live IdP

**Goal.** Validate that an access token issued by a live OIDC IdP authenticates
against a directly-reachable `oidc-jwt` registry (§6.3.3) and resolves
group-scoped layer visibility (§4.6). The scenario has a baseline part that runs
against any IdP in the §6.3.1 tested list whose tenant emits a group claim on
the access token, and an AD FS profile part that covers the split issuer,
`PODIUM_OAUTH_SUBJECT_CLAIM`, and the claim-type-URI group claim against a live
farm. The baseline part reads the group claim under the name the tenant emits,
so a namespaced or vendor-specific claim name is configuration rather than a
blocker.

**Covers.** The split issuer, both claim names, and the single-string group
encoding are asserted against a synthetic IdP by the unit tests in
`pkg/identity` and the integration tests in `internal/serverboot`. This scenario
covers what a live IdP establishes on its own: a discovery document the IdP
publishes, an access token the IdP signed, and the path from the bearer header
through verification to resolved visibility.

**Prerequisites.**

- An OIDC IdP whose discovery document is reachable from the registry host over
  `https` at `<issuer>/.well-known/openid-configuration`. A free Okta or Entra
  ID developer tenant is enough for the baseline part.
- A client registered on that IdP that can complete an authorization-code grant
  or a device-code grant, and whose access token the runner can read. Steps 2 to
  4 implement the device-code exchange, which needs a tenant whose discovery
  document publishes `device_authorization_endpoint`. A tenant that publishes no
  such endpoint completes an authorization-code exchange instead and exports the
  resulting access token as `TOKEN` before step 5. When neither grant is
  available, skip the scenario and record the skip and the reason.
- An `aud` value the issued access token carries, for `PODIUM_OAUTH_AUDIENCE`.
- A second `aud` value the same IdP stamps on a token for the same user, for the
  two-audience steps. A second client or a second API resource on the same
  tenant supplies one. The second token is a JWT for the same test user, and it
  carries the same group claim under the same claim name as the first token, so
  that step 9 compares two admissions that differ in `aud` alone. A tenant that
  cannot mint a second audience, and a second client that cannot be configured
  to emit that group claim under that name, both skip steps 9 and 10 and record
  the skip and the reason against this prerequisite.
- An access token that is a JWT the registry can verify, carrying that `aud` and
  a group claim for the test user. Okta issues a JWT from a custom authorization
  server such as `/oauth2/default` and an opaque token from the org server.
  Okta, Entra ID, and Auth0 each require tenant-side claim or scope
  configuration before a group claim reaches the access token, and Auth0 admits
  a namespaced claim name only (`docs/deployment/oidc/auth0.md`). Step 7 sets
  `PODIUM_OAUTH_GROUPS_CLAIM` when the emitted claim carries a name other than
  `groups`. When the tenant cannot be configured to emit a group claim, run the
  baseline part without the group-scoped layer and record the skip and the
  reason against this prerequisite.
- For the AD FS profile part, an AD FS farm whose issuance rules emit a group
  claim on the access token. When no farm is available, skip that part and
  record the skip and the reason.

A Podium client behind a gateway that has already authenticated the caller sends
no credential of its own (§6.3.3). This registry is directly reachable and
enables no browser flow, so both parts obtain the access token from the IdP
themselves and present it in the configured token header. A CLI, an SDK, or
another API client obtains that token through the device-code flow, and on a
registry that enables the §6.3.4 browser flow a browser obtains it through the
registry's own code exchange. The steps implement a raw device-code exchange
with `curl`.

**Steps (baseline part).**

1. Run the isolation block, then export the IdP coordinates. The issuer is the
   discovery base, without the `/.well-known/openid-configuration` suffix.

   ```bash
   export ISSUER=https://<tenant>.okta.example/oauth2/default
   export CLIENT_ID=<device-code client id>
   export AUD=<audience the IdP stamps on the access token>
   ```

2. Read the discovery document and take the device-code and token endpoints from
   it, so the step does not depend on one vendor's URL layout. A discovery
   document that publishes no `device_authorization_endpoint` fails the `DEV_EP`
   assignment with a named message. That tenant takes the authorization-code
   path named under Prerequisites, exports the resulting access token as
   `TOKEN`, and resumes at step 5.

   ```bash
   curl -sf "$ISSUER/.well-known/openid-configuration" > "$WORK/discovery.json"
   ep() { python3 -c "import json,sys;d=json.load(open(sys.argv[1]));k=sys.argv[2];print(d[k]) if k in d else sys.exit('discovery document publishes no '+k)" "$WORK/discovery.json" "$1"; }
   DEV_EP=$(ep device_authorization_endpoint) || echo "no device authorization endpoint; take the authorization-code path and resume at step 5"
   TOK_EP=$(ep token_endpoint)
   ```

3. Request a device code and open the printed `verification_uri_complete` in a
   browser. Approve as the test user before running step 4. A run on the
   authorization-code path skips this step and step 4. The `scope` value
   carries whatever the tenant needs to put a group claim on the access token:
   an Okta authorization-server scope such as `groups`, an Entra ID app-role or
   optional-claim configuration, or an Auth0 action that adds a namespaced
   claim.

   ```bash
   export SCOPE="openid email groups"
   curl -s -X POST "$DEV_EP" -d "client_id=$CLIENT_ID" -d "scope=$SCOPE" > "$WORK/device.json"
   python3 -m json.tool "$WORK/device.json"
   DEVICE_CODE=$(python3 -c "import json;print(json.load(open('$WORK/device.json'))['device_code'])")
   ```

4. Exchange the approved device code. The loop retries while the token endpoint
   answers `authorization_pending`, and it stops on any other response. The
   final `python3 -m json.tool` prints the response body, which names the error
   when the exchange did not produce an `access_token`.

   ```bash
   for _ in $(seq 1 30); do
     curl -s -X POST "$TOK_EP" \
       -d grant_type=urn:ietf:params:oauth:grant-type:device_code \
       -d "client_id=$CLIENT_ID" -d "device_code=$DEVICE_CODE" > "$WORK/token.json"
     grep -q '"access_token"' "$WORK/token.json" && break
     grep -q 'authorization_pending' "$WORK/token.json" || break
     sleep 5
   done
   python3 -m json.tool "$WORK/token.json"
   export TOKEN=$(python3 -c "import json;print(json.load(open('$WORK/token.json')).get('access_token',''))")
   [ -n "$TOKEN" ] || echo "no access token; approve the device code and re-run this step"
   ```

   Stop here when `TOKEN` is empty. An empty bearer reaches the registry as an
   anonymous request, and every load below would then report the anonymous
   result for an unrelated reason.

5. Decode the access-token payload and record `iss`, `aud`, the subject claim,
   and the group claim with its values. The decode fails when the IdP issued an
   opaque access token, which the JWT prerequisite excludes. A payload that
   carries no group claim means the tenant is not configured to emit one, and
   the group-scoped steps are skipped against that prerequisite.

   ```bash
   claims() {
     python3 - "$1" <<'PY'
   import base64, json, sys
   seg = sys.argv[1].split(".")[1]
   seg += "=" * (-len(seg) % 4)
   print(json.dumps(json.loads(base64.urlsafe_b64decode(seg)), indent=2, sort_keys=True))
   PY
   }
   claims "$TOKEN"
   ```

6. Put the raw group value from step 5 in the layer's `groups:` list. With
   `PODIUM_IDP_GROUP_MAPPING` unset the claim values pass through unmapped, so
   `groups:` lists the value the token carries (an Entra ID group object ID, an
   Okta group name, or whatever the IdP emits). A run that prefers a readable
   layer config instead sets `PODIUM_IDP_GROUP_MAPPING="<raw value>=engineering"`
   at step 7 and lists `engineering`. Record which form the run used. The
   YAML values are single-quoted, because a group value that contains a
   backslash (`ACME\Engineering`) fails to parse inside YAML double quotes and
   the server logs `warning: ignored registry.yaml` and serves no layers. A run
   skipping the group-scoped layer per the Prerequisites writes the
   `public-handbook` layer alone.

   ```bash
   export GROUP='<raw group value from step 5>'
   mkdir -p "$WORK/pub/handbook" "$WORK/eng/deploy"
   podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
   podium artifact scaffold --type skill --description "Engineering deploy" --force "$WORK/eng/deploy"
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: public-handbook
         source: { local: { path: $WORK/pub } }
         visibility: { public: true }
       - id: eng-internal
         source: { local: { path: $WORK/eng } }
         visibility: { groups: ['$GROUP'] }
   YAML
   ```

7. Boot the standalone server under `oidc-jwt` and read the provider line from
   the startup log. The registry fetches the discovery document and the JWKS at
   startup, so this step fails closed when the IdP is unreachable. Export
   `PODIUM_OAUTH_GROUPS_CLAIM` when the group claim observed at step 5 carries a
   name other than `groups`. Auth0 is that case, because it admits a namespaced
   claim name only. Leave the variable unset when the token carries a claim
   named `groups`.

   ```bash
   export PODIUM_IDENTITY_PROVIDER=oidc-jwt
   export PODIUM_OAUTH_ISSUER="$ISSUER"
   export PODIUM_OAUTH_AUDIENCE="$AUD"
   # export PODIUM_OAUTH_GROUPS_CLAIM='<group claim name from step 5>'
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8136 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8136/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8136
   grep "identity provider:" "$WORK/srv.log"
   ```

8. Load one artifact from each layer with the token, without it, and with a
   tampered signature. The first line asserts the precondition the rest of the
   step rests on.

   ```bash
   [ -n "$TOKEN" ] || echo "no access token; re-run the exchange at step 4 after approving"
   code() { curl -s -o /dev/null -w "%{http_code}\n" "$@"; }
   AUTH="Authorization: Bearer $TOKEN"
   echo "token handbook: $(code -H "$AUTH" "$URL/v1/load_artifact?id=handbook")"
   echo "token deploy:   $(code -H "$AUTH" "$URL/v1/load_artifact?id=deploy")"
   echo "anon handbook:  $(code "$URL/v1/load_artifact?id=handbook")"
   echo "anon deploy:    $(code "$URL/v1/load_artifact?id=deploy")"
   echo "tampered:       $(code -H "Authorization: Bearer ${TOKEN}AA" "$URL/v1/load_artifact?id=handbook")"
   curl -s -H "Authorization: Bearer ${TOKEN}AA" "$URL/v1/load_artifact?id=handbook"
   ```

9. Restart the registry with both audiences configured and confirm that a token
   carrying either one authenticates and resolves the same visibility. Obtain
   the second token by repeating steps 2 to 4 against the second client or
   resource and exporting it as `TOKEN2`, and export the audience it carries as
   `AUD2`. Decode `TOKEN2` with the `claims` helper from step 5 first, and read
   its `aud`, its subject claim, and its group claim off the printed payload.
   The two admissions this step compares differ in `aud` alone, so a `TOKEN2`
   that names a different subject, or that carries no group claim under the name
   step 7 exported, would make the `deploy` line report the second client's
   claim configuration rather than the registry's audience handling. A tenant
   that cannot mint a second audience, and a second token that fails this
   decode, both skip this step and step 10 and record the skip.

   ```bash
   [ -n "$TOKEN2" ] || echo "no second-audience token; skip step 9 and step 10 and record the skip"
   claims "$TOKEN2"
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   export AUD2=<second audience the IdP stamps on the access token>
   export PODIUM_OAUTH_AUDIENCE="$AUD,$AUD2"
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8136 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8136/healthz
   server_alive "$SRV" "$WORK/srv2.log"
   for T in "$TOKEN" "$TOKEN2"; do
     echo "handbook: $(code -H "Authorization: Bearer $T" "$URL/v1/load_artifact?id=handbook")"
     echo "deploy:   $(code -H "Authorization: Bearer $T" "$URL/v1/load_artifact?id=deploy")"
   done
   ```

10. Read the provider line from the two-audience run's startup log.

    ```bash
    grep "identity provider:" "$WORK/srv2.log"
    ```

**Expected (baseline part).**

- Step 4 prints a token response carrying `access_token`, and `TOKEN` is
  non-empty. An empty `TOKEN` fails the run at that step. A run on the
  authorization-code path exports `TOKEN` from that exchange and reaches the
  same state before step 5.
- Step 5 prints a decoded payload whose `aud` matches `PODIUM_OAUTH_AUDIENCE`
  and whose group claim carries the test user's membership. When that claim
  carries a name other than `groups`, the name is the value step 7 exports in
  `PODIUM_OAUTH_GROUPS_CLAIM`.
- The startup log carries `identity provider: oidc-jwt (verifying caller
  tokens against accepted issuers ... and accepted audiences ...)` naming the
  configured issuer and every configured audience. An IdP
  whose discovery document publishes no `access_token_issuer`, or publishes one
  equal to the configured issuer, leaves the configured issuer as the sole
  accepted value, so the line names one value.
- The startup log carries no `warning: ignored registry.yaml` line. That warning
  means the layer config was dropped and every load below would return `404`
  for an unrelated reason.
- `token handbook` returns `200`: the IdP-signed token verifies against the JWKS
  from the published discovery document and the caller sees the public layer.
- `token deploy` returns `200`: the verified token resolves the caller's group
  and the group-scoped layer is visible. A run that skipped the group claim per
  the Prerequisites has no `eng-internal` layer and records the skip in place of
  this line.
- `anon handbook` returns `200` and `anon deploy` returns `404`: a request
  carrying no bearer credential in the configured token header is anonymous and
  sees public visibility only. This registry enables no browser flow, so that
  header is the only location it accepts a credential in.
- The tampered request returns `401` with `auth.untrusted_token` and
  `details.token_iss` naming the token's issuer.
- Step 9's decode of `TOKEN2` prints a payload whose `aud` carries `$AUD2`,
  whose subject claim matches the one recorded at step 5, and whose group claim
  carries the test user's membership under the name step 7 exported. A payload
  that misses any of the three fails the prerequisite for the second token, and
  the run skips step 9 and step 10 and records the skip rather than reading the
  loads below as a registry result.
- Step 9 returns `200` for `handbook` and `200` for `deploy` under both tokens.
  The two tokens carry different `aud` values, and a caller admitted under
  either audience resolves the same visibility. A run that skipped the group
  claim per the Prerequisites has no `eng-internal` layer, so it asserts that
  `handbook` returns `200` under both tokens and records the skip in place of
  the `deploy` line.
- Step 10 prints a provider line naming both `$AUD` and `$AUD2` among the
  accepted audiences.
- A run that could not mint a second audience records the skip in place of the
  two preceding lines.

**Steps (AD FS profile part).** Skip these steps and record the skip when no AD
FS farm is available.

1. Stop the baseline server, then export the farm coordinates. `ADFS_HOST` is
   the federation service hostname.

   ```bash
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   export ADFS_HOST=<farm hostname>
   export ISSUER="https://$ADFS_HOST/adfs"
   export CLIENT_ID=<device-code client id>
   export AUD=<relying-party identifier the token carries in aud>
   ```

2. Capture the farm's discovery document as repository evidence. Redact the
   hostname, write the document under `test/fixtures/`, and record the observed
   `issuer`, `access_token_issuer`, and `jwks_uri`. The repository holds no other
   AD FS discovery document and the automated tests write their own, so this
   capture is what records an observed document behind the split-issuer rule.

   ```bash
   mkdir -p $REAL_HOME/projects/podium/test/fixtures
   curl -sf "$ISSUER/.well-known/openid-configuration" | python3 -m json.tool \
     | sed "s/$ADFS_HOST/adfs.acme.example/g" \
     > $REAL_HOME/projects/podium/test/fixtures/adfs-openid-configuration.redacted.json
   python3 -c "import json;d=json.load(open('$REAL_HOME/projects/podium/test/fixtures/adfs-openid-configuration.redacted.json'));print(d['issuer']);print(d.get('access_token_issuer'));print(d['jwks_uri'])"
   ```

3. Acquire an AD FS access token through steps 2 to 5 of the baseline part,
   which read the device-code and token endpoints from this discovery document.
   Record the token's `iss`, the value of the subject claim, and the value of
   the group claim `http://schemas.microsoft.com/ws/2008/06/identity/claims/groups`.

4. Write a registry config with a public layer, a group-scoped layer, and a
   layer scoped to the caller's subject. The `users:` entry carries the value of
   the claim named by `PODIUM_OAUTH_SUBJECT_CLAIM`, because that value is the
   recorded subject (§6.3.3).

   ```bash
   export GROUP='<group value from step 3>'
   export SUBJECT='<subject-claim value from step 3>'
   mkdir -p "$WORK/pub/handbook" "$WORK/eng/deploy" "$WORK/own/notes"
   podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
   podium artifact scaffold --type skill --description "Engineering deploy" --force "$WORK/eng/deploy"
   podium artifact scaffold --type context --description "Personal notes" --force "$WORK/own/notes"
   cat > "$WORK/adfs.yaml" <<YAML
   registry:
     layers:
       - id: public-handbook
         source: { local: { path: $WORK/pub } }
         visibility: { public: true }
       - id: eng-internal
         source: { local: { path: $WORK/eng } }
         visibility: { groups: ['$GROUP'] }
       - id: alice-notes
         source: { local: { path: $WORK/own } }
         visibility: { users: ['$SUBJECT'] }
   YAML
   ```

5. Boot the server with the AD FS profile and read the provider lines from the
   startup log. The subject claim name is the one observed at step 3; the
   reported farm emits `idsub`.

   ```bash
   export PODIUM_IDENTITY_PROVIDER=oidc-jwt
   export PODIUM_OAUTH_ISSUER="$ISSUER"
   export PODIUM_OAUTH_AUDIENCE="$AUD"
   export PODIUM_OAUTH_SUBJECT_CLAIM=idsub
   export PODIUM_OAUTH_GROUPS_CLAIM=http://schemas.microsoft.com/ws/2008/06/identity/claims/groups
   podium serve --standalone --no-embeddings --config "$WORK/adfs.yaml" --bind 127.0.0.1:8137 > "$WORK/adfs.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8137/healthz
   server_alive "$SRV" "$WORK/adfs.log"
   export URL=http://127.0.0.1:8137
   grep "identity provider:" "$WORK/adfs.log"
   ```

6. Load one artifact from each layer with the AD FS token, reusing the `code`
   helper from the baseline part. The first line asserts the precondition the
   rest of this part rests on: `TOKEN` holds the AD FS access token from step 3.
   An empty bearer reaches the registry as an anonymous request, and every load
   below would then report the anonymous result for an unrelated reason. Step 7
   asserts the issuer the token carries, which is what separates the AD FS token
   from a baseline token left in the shell.

   ```bash
   [ -n "$TOKEN" ] || echo "no AD FS access token; re-run the exchange at step 3"
   AUTH="Authorization: Bearer $TOKEN"
   echo "adfs handbook: $(code -H "$AUTH" "$URL/v1/load_artifact?id=handbook")"
   echo "adfs deploy:   $(code -H "$AUTH" "$URL/v1/load_artifact?id=deploy")"
   echo "adfs notes:    $(code -H "$AUTH" "$URL/v1/load_artifact?id=notes")"
   ```

7. Confirm that the same token is rejected without the subject setting. Stop the
   server, unset `PODIUM_OAUTH_SUBJECT_CLAIM`, boot again on another port, and
   replay the token. The `code` helper prints the status, and the second `curl`
   prints the error body, which carries `details.token_iss`.

   ```bash
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   unset PODIUM_OAUTH_SUBJECT_CLAIM
   podium serve --standalone --no-embeddings --config "$WORK/adfs.yaml" --bind 127.0.0.1:8138 > "$WORK/adfs-nosub.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8138/healthz
   server_alive "$SRV" "$WORK/adfs-nosub.log"
   echo "nosub handbook: $(code -H "$AUTH" "http://127.0.0.1:8138/v1/load_artifact?id=handbook")"
   curl -s -H "$AUTH" "http://127.0.0.1:8138/v1/load_artifact?id=handbook"
   ```

**Expected (AD FS profile part).**

- The captured document reports an `issuer` of `https://adfs.acme.example/adfs`,
  an `access_token_issuer` of `http://adfs.acme.example/adfs/services/trust`,
  and a `jwks_uri` under the `https` issuer. The redacted file is committed with
  the run.
- The token's `iss` equals the `access_token_issuer` value and differs from the
  configured issuer.
- The startup log names both accepted issuers on the provider line, and it
  carries one line naming the configured subject claim and one naming the
  configured group claim.
- `adfs handbook`, `adfs deploy`, and `adfs notes` all return `200`: the token
  stamped with the federation-service issuer verifies against the JWKS from the
  `https` discovery document, the claim-type-URI group claim resolves the
  group-scoped layer, and the configured subject claim resolves the `users:`
  layer.
- Step 7 prints `nosub handbook: 401` and an `auth.untrusted_token` body whose
  `details.token_iss` names the federation-service issuer recorded at step 3:
  the AD FS access token carries no `sub`, so the default subject claim rejects
  it and the deployment requires `PODIUM_OAUTH_SUBJECT_CLAIM`. The registry
  returns the same envelope for an unaccepted `iss` and for a bad signature, so
  the `token_iss` value is what attributes this rejection to the missing subject
  claim rather than to a token from the baseline IdP.

**Cleanup.** Stop any server still running and `rm -rf "$WORK"`. Keep
`test/fixtures/adfs-openid-configuration.redacted.json`, which lives in the
repository rather than in `$WORK`.

---

## S37: `extends:` merged manifest, hidden parent, and inherited redaction

**Goal.** Validate by hand what a consumer is served for an artifact that
declares `extends:`, across the four surfaces the merge reaches: the signature,
the body, the frontmatter an extension type authors, and the audit stream.

**Covers.** §4.6 field semantics and hidden parents, §4.7.9 signatures, §8.2
manifest-declared redaction, §11 filesystem-versus-server equivalence.

**Why by hand.** Each behavior below shipped broken at least once, and in every
case the automated suite passed: the assertions checked a substring rather than
a value, or the case skipped on the platform it was run on. Reading the served
bytes directly is what these steps are for.

**Steps.**

1. Run the isolation block.

2. Build a registry holding a parent and a child that inherits from it. The
   parent carries a frontmatter key `manifest.Artifact` does not declare, a
   comment naming itself, and a redaction directive. The child authors no
   prose, declares its own undeclared key, and declares neither a description
   nor a directive of its own.

   ```bash
   mkdir -p "$WORK/reg/shared/base" "$WORK/reg/team/derived"
   cat > "$WORK/reg/shared/base/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 1.0.0
   description: the base context
   sensitivity: medium
   # authored by the shared/base owners
   x_review_board: platform
   x_account: GB29-NWBK-0000
   audit_redact: [x_account]
   ---

   base prose
   EOF
   cat > "$WORK/reg/team/derived/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 2.0.0
   extends: shared/base@1.x
   x_runbook: ops/derived.md
   ---
   EOF
   ```

3. Serve the registry and read the child back.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8144 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8144/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8144
   curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=team/derived" | tee "$WORK/child.json" | python3 -m json.tool
   ```

   **Expect.** The response's `frontmatter` carries the child's `x_runbook`
   **and** the parent's `x_review_board: platform`, because §4.6's omitted-field
   rule makes an extension type's own fields inheritable. It carries the
   inherited `description: the base context`.

4. Check the hidden-parent guarantee (§4.6) on the served bytes, not on the
   parsed fields. The parent's ID must not appear anywhere in the served
   frontmatter, including inside a YAML comment the parent authored.

   ```bash
   python3 - "$WORK/child.json" <<'PY'
   import json, sys
   fm = json.load(open(sys.argv[1])).get("frontmatter", "")
   for probe in ("shared/base", "extends", "authored by the shared/base owners"):
       print(("LEAK  " if probe in fm else "ok    ") + probe)
   print(fm)
   PY
   ```

   **Expect.** Three `ok` lines. A `LEAK` on the comment probe is the §4.6
   violation a value-only check does not catch: a restored node carries its
   author's comments and the serializer re-emits them unless it clears them.

   The probe reads the `frontmatter` field, which is the served string the
   merge writes. The response carries no `raw_frontmatter`, `manifest_merged`,
   or `signature` field, and it names the parent in `extends_pin` only because
   this unauthenticated standalone caller can see every layer. S70 reads the
   whole raw body as a caller that cannot see the parent's layer.

5. Check the body. The child authored no prose, so it must be served none
   rather than the parent's.

   ```bash
   python3 -c "import json;d=json.load(open('$WORK/child.json'));print(repr(d.get('manifest_body','')))"
   ```

   **Expect.** An empty or whitespace-only string. `'base prose'` means the
   body carry-over is guarded on a non-empty child body again.

6. Check that search resolves the inherited description. The ingest fold
   writes the merged value to the indexed columns, so the child is findable by
   a description it never authored.

   ```bash
   podium search --registry "$PODIUM_REGISTRY" "base context"
   ```

   **Expect.** The child is listed under the inherited description. A result
   whose description is empty means the ingest fold did not run.

   The two surfaces agree on the descriptor columns and deliberately do not
   agree on the embedded `frontmatter` string: a search descriptor serves the
   child's authored block with `extends:` removed and merges nothing, which
   avoids a chain walk per result. Do not read a difference in that field as a
   defect here.

7. Check the §8.2 inherited redaction in the audit stream.

   ```bash
   grep -c "GB29-NWBK-0000" "$PODIUM_AUDIT_LOG_PATH" || true
   grep -o '"x_account":"[^"]*"' "$PODIUM_AUDIT_LOG_PATH" | tail -2
   ```

   **Expect.** Zero occurrences of the raw account value, and the child's
   `artifact.loaded` event carrying `"x_account": "[redacted]"` in its
   `context`. The directive is applied by substituting the value in place
   rather than by emitting a key list, so the masked entry is the observable.
   The child declares no directive of its own, so a directive that reaches the
   event at all is the inherited one.

8. Check the fail-closed arm. A child whose `extends` arrives through a YAML
   merge key keeps an operative reference inside the mapping it merges in, and
   the served block would name the parent, so the read is refused rather than
   served.

   ```bash
   mkdir -p "$WORK/reg/team/aliased"
   cat > "$WORK/reg/team/aliased/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 1.0.0
   description: aliased child
   base: &b
     extends: shared/base@1.x
   <<: *b
   ---

   aliased body
   EOF
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8145 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8145/healthz
   server_alive "$SRV" "$WORK/srv2.log"
   curl -s -o "$WORK/aliased.json" -w '%{http_code}\n' \
     "http://127.0.0.1:8145/v1/load_artifact?id=team/aliased"
   cat "$WORK/aliased.json"
   ```

   **Expect.** A 400 carrying `registry.invalid_argument`. A 200 whose
   frontmatter names `shared/base` is the leak this arm exists to prevent; a
   200 with the key silently dropped is also wrong, because a consumer cannot
   tell an inherited-as-nothing key from one the chain never set. Check the
   error code rather than the status class alone: a wrong URL also returns a
   4xx, so a status-only check passes against a route that does not exist.

   The refusal is at the read. Ingest accepts the artifact, so it stays
   listed by search and by `/v1/catalog` under its authored description, and
   the refusal arrives when a consumer loads it. Its search descriptor carries
   no frontmatter, so nothing leaks through discovery.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S38: `extends:` child under a signing registry

**Goal.** Validate that a signing registry serves an `extends:` child a
signature that verifies against the bytes it serves, and that a consumer
enforcing verification loads it.

**Covers.** §4.7.9 signatures, §4.6 merge, §6.6 materialization verification.

**Why by hand.** No test in the tree paired signing with `extends:` before
2026-08-20, so the suite was green while the two were mutually exclusive: the
merged record carried the root parent's envelope against the child's own
content hash, so verification could not succeed.

**Watch out for.** Two commands look like they exercise this and do not.
`PODIUM_SIGNATURE_PROVIDER` is read by `podium sign`, `podium verify`, and
`podium-mcp`; `podium serve` does not read it. Ingest signing is on unless
`PODIUM_SIGN=none` or `--sign none` turns it off. And a filesystem-source
`podium sync` runs no signature check, so `PODIUM_VERIFY_SIGNATURES` in front of it
has no effect. Server-source `podium sync` does verify, and it refuses an invalid
`PODIUM_VERIFY_SIGNATURES` value with exit status 2. A scenario built on either one passes whether or not
the defect is present.

**Steps.**

1. Run the isolation block.

2. Build a parent and an inheriting child.

```bash
mkdir -p "$WORK/reg/shared/base" "$WORK/reg/team/derived"
cat > "$WORK/reg/shared/base/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: the base context
sensitivity: medium
---

base prose
EOF
cat > "$WORK/reg/team/derived/ARTIFACT.md" <<'EOF'
---
type: context
version: 2.0.0
description: the derived context
sensitivity: medium
extends: shared/base@1.x
---

derived prose
EOF
```

3. Serve with ingest signing on, and confirm it is actually on before reading
   anything into the result.

```bash
podium serve --standalone --no-embeddings --sign registry-key \
  --layer-path "$WORK/reg" --bind 127.0.0.1:8139 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8139/healthz
server_alive "$SRV" "$WORK/srv.log"
export PODIUM_REGISTRY=http://127.0.0.1:8139
curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=team/derived" > "$WORK/child.json"
curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=shared/base" > "$WORK/parent.json"
python3 - "$WORK/child.json" "$WORK/parent.json" <<'PY'
import json, sys
c, p = (json.load(open(a)) for a in sys.argv[1:3])
print("child  sig empty:", not c.get("delivery_signature"))
print("parent sig empty:", not p.get("delivery_signature"))
print("same envelope:", c.get("delivery_signature") == p.get("delivery_signature"))
print("child delivery hash:", c.get("delivery_hash"))
print("parent delivery hash:", p.get("delivery_hash"))
print("child hash:", c.get("content_hash"))
print("parent hash:", p.get("content_hash"))
PY
```

   **Expect.** Neither `delivery_signature` is empty and the two envelopes
   differ, because each is minted over its own record's `delivery_hash`. The two
   delivery hashes differ, and the two content hashes differ. An empty
   delivery signature means signing never turned on and every later step is
   vacuous. An identical envelope across a child and its parent is the defect
   itself.

4. Load the child through the path that enforces verification. `podium-mcp`
   is the consumer that raises `materialize.signature_invalid`, and it is
   driven by a JSON-RPC request on stdin rather than by a flag.

```bash
PUBKEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
REQ='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"team/derived"}}}'
echo "$REQ" | PODIUM_REGISTRY="$PODIUM_REGISTRY" PODIUM_MATERIALIZE_DIR="$WORK/mat" \
  PODIUM_VERIFY_SIGNATURES=always \
  PODIUM_SIGNATURE_PROVIDER=registry-managed \
  PODIUM_SIGNATURE_VERIFY_KEY="$PUBKEY" \
  podium-mcp
```

   **Expect.** A JSON-RPC result whose `structuredContent.content_hash` is the
   child's own hash from step 3, and whose `manifest_body` is `derived prose` with its trailing newline.
   An error naming `materialize.signature_invalid` means the served delivery
   signature does not cover the served delivery hash, and an error naming
   `materialize.content_hash_mismatch` means the served bytes do not reproduce
   the served delivery hash. Either is the defect this scenario pins.

5. Negative control on the key. Without it the scenario cannot tell "verified"
   from "never checked", which is how its first version passed against the
   defect it was written for.

```bash
BOGUS="$(head -c 32 /dev/urandom | base64)"
echo "$REQ" | PODIUM_REGISTRY="$PODIUM_REGISTRY" PODIUM_MATERIALIZE_DIR="$WORK/mat2" \
  PODIUM_VERIFY_SIGNATURES=always \
  PODIUM_SIGNATURE_PROVIDER=registry-managed \
  PODIUM_SIGNATURE_VERIFY_KEY="$BOGUS" \
  podium-mcp
```

   **Expect.** An error carrying `signature_invalid: signature does not
   verify`. A success here means verification is not running, so step 4's pass
   proves nothing.

6. Control on the non-extends path, so a pass in step 4 is attributable to the
   merge rather than to verification being lenient for everything.

```bash
PARENT_REQ='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"shared/base"}}}'
echo "$PARENT_REQ" | PODIUM_REGISTRY="$PODIUM_REGISTRY" PODIUM_MATERIALIZE_DIR="$WORK/mat3" \
  PODIUM_VERIFY_SIGNATURES=always \
  PODIUM_SIGNATURE_PROVIDER=registry-managed \
  PODIUM_SIGNATURE_VERIFY_KEY="$PUBKEY" \
  podium-mcp
```

   **Expect.** The parent loads and reports its own content hash, which differs
   from the child's. The parent declares no `extends:`, so it exercises the
   unmerged path.

**Cleanup.** Stop the server by the PID this scenario recorded, and remove the
work directory. Do not pattern-kill by process name: another scenario or
another session may be running its own server.

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
rm -rf "$WORK"
```

---

## S39: same-ID `extends:` overlay and a three-level chain

**Goal.** Validate the two chain shapes the single parent-child case does not
reach: a child that overlays its own canonical ID from a lower-precedence
layer, and a chain deep enough that an inherited key travels two hops.

**Covers.** §4.6 field semantics, the §4.6 same-ID overlay exception, hidden
parents over a multi-hop chain.

**Why by hand.** The same-ID overlay is the case where the parent's ID and the
child's ID are equal, so a hidden-parent check written over "the parent's ID
must not appear" collides with the artifact's own identity. That collision took
the longest of any part of this work to settle, and the resolution is that the
check runs on inherited values rather than on the leaf's own. A three-level
chain is the shape where a middle member's contribution can be dropped without
either end looking wrong.

**Steps.**

1. Run the isolation block. `--layer-path` names a single registry root, so a
   multi-layer scenario registers each layer instead.

2. Build a base layer and an overlay layer that extends the same canonical ID.

```bash
mkdir -p "$WORK/base/greet" "$WORK/team/greet"
cat > "$WORK/base/greet/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: base greet
x_owner: platform
---

base prose
EOF
cat > "$WORK/team/greet/ARTIFACT.md" <<'EOF'
---
type: context
version: 2.0.0
extends: greet
x_runbook: ops/greet.md
---
EOF
```

3. Serve, register both layers with `team` at higher precedence, and read the
   overlay back.

```bash
podium serve --standalone --no-embeddings --bind 127.0.0.1:8140 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8140/healthz
server_alive "$SRV" "$WORK/srv.log"
export PODIUM_REGISTRY=http://127.0.0.1:8140
podium layer register --registry "$PODIUM_REGISTRY" --id base --local "$WORK/base" --public
podium layer register --registry "$PODIUM_REGISTRY" --id team --local "$WORK/team" --public
podium layer reingest --registry "$PODIUM_REGISTRY" base
podium layer reingest --registry "$PODIUM_REGISTRY" team
curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=greet" | tee "$WORK/greet.json" | python3 -m json.tool
```

   **Expect.** A 200. The served frontmatter carries the overlay's own
   `x_runbook`, the base layer's inherited `x_owner: platform`, and the
   inherited `description: base greet`. The served `version` is the overlay's
   `2.0.0`.

   A `registry.invalid_argument` refusal here is the collision this scenario
   exists to catch: the artifact's own ID equals its parent's, so a
   hidden-parent check that runs over the leaf's own keys refuses a legitimate
   overlay.

4. Confirm the overlay is still served, rather than being refused or emptied.

```bash
python3 - "$WORK/greet.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
fm = d.get("frontmatter", "")
for probe in ("x_runbook", "x_owner", "base greet"):
    print(("ok    " if probe in fm else "MISS  ") + probe)
print("version:", d.get("version"))
PY
```

   **Expect.** Three `ok` lines and version `2.0.0`. A `MISS` on `x_owner` is
   the inherited-key drop; a `MISS` on `x_runbook` is the leaf's own key being
   dropped, which is the more serious of the two.

5. Build a three-level chain in one layer and read the leaf.

```bash
mkdir -p "$WORK/deep/a" "$WORK/deep/b" "$WORK/deep/c"
cat > "$WORK/deep/a/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: grandparent
x_grandparent: gp-value
---

gp prose
EOF
cat > "$WORK/deep/b/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
extends: a@1.x
x_middle: mid-value
---
EOF
cat > "$WORK/deep/c/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
extends: b@1.x
x_leaf: leaf-value
---
EOF
podium layer register --registry "$PODIUM_REGISTRY" --id deep --local "$WORK/deep" --public
podium layer reingest --registry "$PODIUM_REGISTRY" deep
curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=c" | tee "$WORK/c.json" | python3 -m json.tool
```

   **Expect.** The leaf carries `x_leaf`, the middle's `x_middle`, the
   grandparent's `x_grandparent`, and the inherited `description: grandparent`.
   A missing `x_grandparent` with `x_middle` present means the fold stops after
   one hop.

   The leaf's `manifest_body` is empty. A body is never inherited, at any depth:
   `extends:` folds frontmatter and the child's prose replaces the parent's
   rather than being concatenated with it. `gp prose` appearing here is a body
   carried across two hops, which is the same defect as carrying it across one.

```bash
python3 -c "import json;print(repr(json.load(open('$WORK/c.json')).get('manifest_body','')))"
```

   **Expect.** An empty or whitespace-only string.

6. Check the hidden-parent guarantee over the whole chain, not just the
   immediate parent.

```bash
python3 - "$WORK/c.json" <<'PY'
import json, sys
fm = json.load(open(sys.argv[1])).get("frontmatter", "")
for probe in ("extends", "a@1.x", "b@1.x"):
    print(("LEAK  " if probe in fm else "ok    ") + probe)
PY
```

   **Expect.** Three `ok` lines. A `LEAK` on `a@1.x` means the check covers the
   immediate parent only and lets an ancestor through.

**Cleanup.** Stop the server by its recorded PID and remove `$WORK`.

---

## S40: `extends:` for a skill, and filesystem-versus-server parity

**Goal.** Validate the artifact type whose prose does not live in
`ARTIFACT.md`, and validate that the two registry modes materialize the same
bytes for the same `extends:` child.

**Covers.** §4.3.4 skills, §4.6 merge, §11 filesystem-versus-server
equivalence, §2.2 shared library.

**Why by hand.** A skill stores its body in `SKILL.md` and its frontmatter in
`ARTIFACT.md`, so the body rule reads differently for it than for every other
type, and the two sources differ by construction. Separately, the two extends
resolvers are distinct implementations of the same merge: repairing one alone
makes the modes disagree, and the disagreement is in materialized bytes rather
than in an error.

**Watch out for.** A filesystem-source `podium sync` does not lint, so a
fixture a server refuses still materializes here. A skill's `SKILL.md` `name`
must be lowercase and must equal its parent directory (`lint.invalid_name`,
`lint.skill_md_compliance`), and a fixture that breaks either one passes the
first half of this scenario and then kills the server in the second half with
`ingest.lint_failed` before it binds.

**Steps.**

1. Run the isolation block.

2. Build a skill parent and a skill child that authors no `SKILL.md` body.

```bash
mkdir -p "$WORK/reg/shared/base" "$WORK/reg/team/derived"
cat > "$WORK/reg/shared/base/ARTIFACT.md" <<'EOF'
---
type: skill
version: 1.0.0
x_review_board: platform
---
EOF
cat > "$WORK/reg/shared/base/SKILL.md" <<'EOF'
---
name: base
description: the base skill
---

base skill body
EOF
cat > "$WORK/reg/team/derived/ARTIFACT.md" <<'EOF'
---
type: skill
version: 2.0.0
extends: shared/base@1.x
x_runbook: ops/derived.md
---
EOF
cat > "$WORK/reg/team/derived/SKILL.md" <<'EOF'
---
name: derived
description: the derived skill
---

derived skill body
EOF
```

3. Materialize through the filesystem source.

```bash
mkdir -p "$WORK/fs-target"
podium sync --registry "$WORK/reg" --target "$WORK/fs-target" --harness none
find "$WORK/fs-target" -type f | sed "s|$WORK/fs-target/||" | sort
```

   **Expect.** Both artifacts materialize. The derived skill's `SKILL.md`
   carries `derived skill body` and its own `description`, because a skill's
   body and identity come from `SKILL.md` and follow the child rather than the
   parent. A skill's `name` is forced to equal its directory by
   `lint.skill_md_compliance`, so the child-versus-parent distinction lives in
   the description and the body rather than in the name.

4. Materialize the same registry through a server and compare byte for byte.

```bash
podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
  --bind 127.0.0.1:8141 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8141/healthz
server_alive "$SRV" "$WORK/srv.log"
mkdir -p "$WORK/srv-target"
podium sync --registry http://127.0.0.1:8141 --target "$WORK/srv-target" --harness none
find "$WORK/fs-target" -name ARTIFACT.md | wc -l   # must be 2, not 0
diff -r -x sync.lock "$WORK/fs-target" "$WORK/srv-target" && echo "IDENTICAL"
```

   Then compare the two lock files with the per-consumer fields removed.

```bash
diff <(grep -v -e '^target:' -e '^last_synced_at:' "$WORK/fs-target/.podium/sync.lock") \
     <(grep -v -e '^target:' -e '^last_synced_at:' "$WORK/srv-target/.podium/sync.lock") \
  && echo "LOCKS IDENTICAL"
```

   **Expect.** `LOCKS IDENTICAL`. A differing `content_hash` line means the
   filesystem consumer and the registry hash different inputs.

   **Expect.** A non-zero count, then `IDENTICAL`. The count runs first
   because an empty tree compared against an empty tree also reports no
   differences, which scores as a pass while proving nothing. The lock file is
   excluded rather than tolerated: its `target` and `last_synced_at` differ by
   construction between two consumers. Its `content_hash` entries match
   between the two modes, because both compute the §4.7.6 framed hash from
   the same inputs, and the lock comparison below checks them. Any difference in an `ARTIFACT.md` or
   `SKILL.md` is the §11 equivalence break that repairing one resolver alone
   produces.

5. Repeat the comparison for a harness that writes a native layout, so the
   parity covers adapter output rather than the neutral copy alone.

```bash
mkdir -p "$WORK/fs-cc" "$WORK/srv-cc"
podium sync --registry "$WORK/reg" --target "$WORK/fs-cc" --harness claude-code
podium sync --registry http://127.0.0.1:8141 --target "$WORK/srv-cc" --harness claude-code
find "$WORK/fs-cc" -name '*.md' | wc -l   # must be non-zero
diff -r -x sync.lock "$WORK/fs-cc" "$WORK/srv-cc" && echo "IDENTICAL"
```

   **Expect.** A non-zero count, then `IDENTICAL`, with the lock excluded for
   the same reason.

**Cleanup.** Stop the server by its recorded PID and remove `$WORK`.

---

## S41: inherited `audit_redact` over a forwarded audit stream

**Goal.** Validate that an inherited redaction directive is applied before the
event leaves the process, by reading what a receiver actually receives rather
than what the local log file holds.

**Covers.** §8.2 manifest-declared redaction, §8.3 audit sink selection, §4.6
inheritance.

**Why by hand.** Redaction that is applied only on the way to the local file
still leaks to an aggregator, and the two paths are different sinks: a
filesystem `PODIUM_AUDIT_LOG_PATH` selects a file sink, an `http(s)` value
selects an endpoint sink. A scenario that greps the local log cannot tell the
two apart, and the aggregator is the copy that leaves the operator's machine.

**Steps.**

1. Run the isolation block, then start a receiver that records every forwarded
   body verbatim.

```bash
cat > "$WORK/sink.py" <<'EOF'
import http.server, sys
OUT = sys.argv[1]
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("content-length", 0))
        open(OUT, "ab").write(self.rfile.read(n) + b"\n")
        self.send_response(204); self.end_headers()
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 8901), H).serve_forever()
EOF
python3 "$WORK/sink.py" "$WORK/forwarded.jsonl" &
SINK=$!
sleep 1
```

2. Build a parent carrying the sensitive field and the directive, and a child
   that declares neither.

```bash
mkdir -p "$WORK/reg/shared/base" "$WORK/reg/team/derived"
cat > "$WORK/reg/shared/base/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: the base context
sensitivity: medium
x_account: GB29-NWBK-0000
audit_redact: [x_account]
---

base prose
EOF
cat > "$WORK/reg/team/derived/ARTIFACT.md" <<'EOF'
---
type: context
version: 2.0.0
extends: shared/base@1.x
---
EOF
```

3. Serve with the audit stream pointed at the receiver rather than at a file,
   then read the child.

```bash
PODIUM_AUDIT_LOG_PATH="http://127.0.0.1:8901/ingest" \
podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
  --bind 127.0.0.1:8142 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8142/healthz
server_alive "$SRV" "$WORK/srv.log"
curl -s -o /dev/null "http://127.0.0.1:8142/v1/load_artifact?id=team/derived"
sleep 2
wc -l < "$WORK/forwarded.jsonl"
```

   **Expect.** At least one forwarded line. Zero lines means the endpoint sink
   was not selected and every later step is vacuous, so check `$WORK/srv.log`
   for the sink it chose before reading anything into the result.

4. Read what the receiver got.

```bash
grep -c "GB29-NWBK-0000" "$WORK/forwarded.jsonl" || true
python3 - "$WORK/forwarded.jsonl" <<'PY'
import json, sys
for line in open(sys.argv[1]):
    line = line.strip()
    if not line:
        continue
    try:
        ev = json.loads(line)
    except ValueError:
        continue
    if ev.get("target") == "team/derived":
        print(json.dumps(ev.get("context", {}), indent=2))
PY
```

   **Expect.** Zero occurrences of the raw account value in the forwarded
   stream, and the child's event carrying `"x_account": "[redacted]"` in its
   context. The raw value appearing here is a leak to the aggregator even when
   the local file is clean.

5. Confirm the directive reached the event by inheritance rather than by the
   field being absent. A missing field and a redacted field are different
   outcomes, and only one of them is redaction working.

```bash
python3 - "$WORK/forwarded.jsonl" <<'PY'
import json, sys
seen = False
for line in open(sys.argv[1]):
    line = line.strip()
    if not line:
        continue
    try:
        ev = json.loads(line)
    except ValueError:
        continue
    if ev.get("target") == "team/derived" and "x_account" in ev.get("context", {}):
        seen = True
print("x_account present in the child's event context:", seen)
PY
```

   **Expect.** `True`. `False` means the field never reached the event at all,
   so the scenario proves nothing about redaction: the inherited directive
   would look identical to a directive that was never applied.

**Cleanup.** Stop the server and the receiver by their recorded PIDs, then
remove `$WORK`.

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
kill "$SINK" 2>/dev/null; wait "$SINK" 2>/dev/null
rm -rf "$WORK"
```

---

## S42: a deprecated parent in an `extends:` chain

**Goal.** Validate what happens to a child whose parent is deprecated, across
the orderings that differ: a range reference that can avoid a deprecated
version, an explicit pin onto one, a line with no live version left, and a
parent deprecated after the child was already stored.

**Covers.** §4.6 inheritance, §4.7.6 pin resolution, §4.7.4 deprecation, §4.7
immutability.

**Why by hand.** Deprecation is per-version and a pin is frozen at the child's
ingest, so which ordering produced a state is invisible from the state itself.
The refusal also lands at ingest for some orderings and at read for others.

**Layout, before you start.** Both walkers key on the literal filename
`ARTIFACT.md`, and an artifact's id is its directory path relative to the layer
root. A registry directory therefore holds exactly one `(id, version)` at a
time, and any other file in it is captured as a bundled resource rather than as
a second version. Multiple versions of one id live only in the store,
accumulated across successive reingests of the same directory. Publishing a new
version means overwriting `ARTIFACT.md` in place and reingesting, not adding a
file beside it: a second file changes the artifact's content hash while its
version stays the same, which ingest refuses with
`ingest.immutable_violation`.

There is no deprecation verb. A version's `deprecated` flag is part of its
frontmatter and therefore part of its content hash, so a stored version cannot
be deprecated in place; the same refusal applies.

**Steps.**

1. Run the isolation block, serve an empty registry, and register one layer.

```bash
mkdir -p "$WORK/reg"
podium serve --standalone --no-embeddings --bind 127.0.0.1:8143 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8143/healthz
server_alive "$SRV" "$WORK/srv.log"
export PODIUM_REGISTRY=http://127.0.0.1:8143
podium layer register --registry "$PODIUM_REGISTRY" --id reg --local "$WORK/reg" --public
```

2. Publish a live parent and a child that references it by range.

```bash
mkdir -p "$WORK/reg/shared/base" "$WORK/reg/team/derived"
cat > "$WORK/reg/shared/base/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: base v1
---

base prose
EOF
cat > "$WORK/reg/team/derived/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
extends: shared/base@1.x
---
EOF
podium layer reingest --registry "$PODIUM_REGISTRY" reg
curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=team/derived" | python3 -c "import json,sys;d=json.load(sys.stdin);print('deprecated=',d.get('deprecated'));print('inherits base v1:', 'base v1' in d.get('frontmatter',''))"
```

   **Expect.** The child loads and inherits `base v1`. Its `deprecated` is
   absent or `false`; the field is omitted rather than emitted as `false`, so
   read the absence as the negative rather than looking for the word.

3. Deprecate the line by publishing a newer version, which means overwriting
   the same file. Confirm both versions are stored before continuing: no HTTP
   route lists versions, so read the store.

```bash
cat > "$WORK/reg/shared/base/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.1.0
description: base v1.1 deprecated
deprecated: true
---

base prose
EOF
podium layer reingest --registry "$PODIUM_REGISTRY" reg
sqlite3 "$PODIUM_SQLITE_PATH" "select artifact_id, version, deprecated from manifests order by artifact_id, version;"
```

   **Expect.** Two rows for `shared/base`: `1.0.0` with `0`, and `1.1.0` with
   `1`. One row means the overwrite did not land as a new version and every
   later step runs against the wrong registry.

4. Ingest a **fresh** range child while the deprecated version is already
   stored. A child already stored is idempotent on reingest and its pin is
   frozen, so re-reading it tests nothing about the selection rule.

```bash
mkdir -p "$WORK/reg/team/ranged"
cat > "$WORK/reg/team/ranged/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
extends: shared/base@1.x
---
EOF
podium layer reingest --registry "$PODIUM_REGISTRY" reg
sqlite3 "$PODIUM_SQLITE_PATH" "select artifact_id, extends_pin from manifests where extends_pin != '';"
```

   **Expect.** `team/ranged` pins `shared/base@1.0.0`, the live version, not
   the newer deprecated `1.1.0`. The range skipped the deprecated candidate.
   `team/derived` still pins `1.0.0` from its original ingest.

5. Pin a deprecated version explicitly. Read the reingest output: the rejection
   is printed by the CLI on stdout and never reaches the server log, and a grep
   of the server log matches unrelated boot lines whether or not the rejection
   happened.

```bash
mkdir -p "$WORK/reg/team/pinned"
cat > "$WORK/reg/team/pinned/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
extends: shared/base@1.1.0
---
EOF
podium layer reingest --registry "$PODIUM_REGISTRY" reg 2>&1 | grep -iE "rejected|conflict" || echo "NO REJECTION LINE"
```

   **Expect.** A line naming `team/pinned`, `ingest.invalid_artifact`, and
   deprecation, for example `extends: parent version shared/base@1.1.0 is
   deprecated`. A message claiming the parent was never published is a defect:
   the author named a stored version explicitly and is entitled to be told why
   it was refused.

   Note that `podium layer reingest` exits `1` here, because the cycle dropped
   `team/pinned`. Read the printed reason rather than inferring it from the
   exit status.

6. Exhaust a line: a parent whose only version is deprecated.

```bash
mkdir -p "$WORK/reg/shared/dead" "$WORK/reg/team/orphaned"
cat > "$WORK/reg/shared/dead/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: dead line
deprecated: true
---

dead prose
EOF
cat > "$WORK/reg/team/orphaned/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
extends: shared/dead@1.x
---
EOF
podium layer reingest --registry "$PODIUM_REGISTRY" reg 2>&1 | grep -iE "rejected|conflict" || echo "NO REJECTION LINE"
```

   **Expect.** `team/orphaned` refused with `ingest.invalid_artifact` and a
   reason naming that every stored version of the parent is deprecated. A
   range with no live candidate refuses rather than falling back to a
   deprecated one.

7. Record the read-versus-search disposition for a child that inherits the
   flag. **This state is not reachable on a current build**, and the step
   exists to keep the accepted deferral visible rather than to produce it.

   A child can inherit `deprecated: true` only by pinning a deprecated version,
   which steps 5 and 6 show is refused at ingest, and a stored version cannot
   be deprecated in place because the flag is part of its content hash. So the
   only artifacts in this state are ones stored before those ingest rules
   existed.

   When you need to observe the deferral, reconstruct it out of band in the
   throwaway store, which is not a product path:

```bash
sqlite3 "$PODIUM_SQLITE_PATH" "select artifact_id, version, deprecated from manifests where artifact_id like 'shared/%';"
```

   **Expect.** Record what you see. The known disposition is that
   `load_artifact` reports an inherited `deprecated: true` with its deprecation
   warning while the default `search_artifacts` filter reads the child's own
   stored column and does not exclude it. That divergence is an accepted
   deferral rather than a defect. Note also that the read path parses each
   record's stored frontmatter rather than its column, so flipping the column
   alone changes nothing on the read.

**Cleanup.** Stop the server by its recorded PID and remove `$WORK`.

---

## S43: The documented `registry.yaml` example starts a registry

**Goal.** Validate that the `identity_provider` block of the §13.12
`registry.yaml` example names a configuration the registry accepts at startup,
and that the two configurations it replaces are still refused.

**Covers.** The §13.12 config-file example, the `oidc-jwt` required key pair
(§6.3.3), and the startup refusals `config.identity_provider_unverified` and
`config.invalid_issuer_scheme`.

**Why by hand.** `TestReadYAMLConfig_SpecExampleNestedBlock`
(`internal/serverboot/backend_config_test.go`) and
`TestRegistryConfig_SpecExampleNestedInterpolation`
(`test/e2e/registry_config_format_test.go`) assert that the example parses and
reaches the resolved config. Neither starts a registry on it, so both stay
green against an example that parses and then refuses to boot. That is the
state the example was in until the §13.12 correction, and the same text had
already been copied into the Helm chart's `values.yaml`, where a default
`helm install` could not start.

**Prerequisites.** Network access to any `https` OIDC issuer that publishes a
discovery document. No account, no tenant, no client registration, and no token
are needed: the scenario asserts that the registry starts, and startup fetches
the discovery document and the JWKS without validating any token. A public
issuer therefore serves, and `https://accounts.google.com` and
`https://login.microsoftonline.com/common/v2.0` both work. Run the scenario
against one of those unless a tenant of your own is already configured.

Skip only when the host has no outbound network access at all. §6.3.3 fails
startup when the discovery document or the JWKS is unreachable, so an
unreachable issuer produces a refusal that resembles the failures the negative
controls are testing for and would score a false pass.

**A note on the example's issuer.** The example reads
`issuer: https://acme.okta.com/oauth2/default`, which resolves to nothing. The
block therefore cannot be pasted verbatim and started by anyone, and the steps
below substitute `$ISSUER`. What is under test is the set of keys the block
names, which is what the defect was about; the placeholder hostname is not.

**Steps.**

1. Run the isolation block, then name the IdP and the registry's own endpoint.

   ```bash
   export ISSUER="https://accounts.google.com"   # any https issuer; no trailing slash
   export AUD="http://127.0.0.1:8150"
   curl -fsS "$ISSUER/.well-known/openid-configuration" > /dev/null && echo "issuer reachable"
   ```

   **Expect.** `issuer reachable`. When the `curl` fails, stop and record the
   skip rather than continuing.

2. Write the §13.12 `identity_provider` block with the issuer substituted.

   ```bash
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     identity_provider:
       type: oidc-jwt
       issuer: $ISSUER
       audience: $AUD
   YAML
   ```

3. Start the registry on that config and record the PID.

   ```bash
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" \
     --bind 127.0.0.1:8150 > "$WORK/srv.log" 2>&1 &
   export SRV=$!
   sleep 3
   curl -fsS http://127.0.0.1:8150/healthz && echo && cat "$WORK/srv.log"
   server_alive "$SRV" "$WORK/srv.log"
   ```

   **Expect.** `/healthz` answers and the log carries no
   `config.identity_provider_unverified`, `config.oidc_jwt_audience_unset`, or
   `config.invalid_issuer_scheme`. A registry that exited leaves `curl` failing
   and the reason on the last line of `$WORK/srv.log`.

4. Confirm the provider is the one under test rather than an absent one.

   ```bash
   PODIUM_CONFIG_FILE="$WORK/registry.yaml" podium config show --server | grep -E "identity_provider|oauth_audience"
   ```

   `config show` takes the config path from `PODIUM_CONFIG_FILE` and defines no
   `--config` flag, which `serve` does. Passing `--config` here exits 1 with
   `flag provided but not defined: -config` before printing anything.

   **Expect.** `identity_provider` reads `oidc-jwt`, `identity_provider.issuer`
   reads `$ISSUER`, and `oauth_audience` reads `$AUD`. A registry that started
   with no provider at all would satisfy step 3 and fail here. The type key
   prints as `identity_provider` rather than `identity_provider.type`, so a
   literal grep for the latter finds nothing.

   The provenance column reads `registry.yaml` for `oauth_audience` on this
   run, matching `identity_provider` and `identity_provider.issuer`. When
   `identity_provider.audience` names a sequence, the value column joins the
   accepted audiences with commas.

5. **Sequence form.** Rewrite the block with `audience:` as a two-element
   sequence and read the row again.

   ```bash
   cat > "$WORK/registry-list.yaml" <<YAML
   registry:
     identity_provider:
       type: oidc-jwt
       issuer: $ISSUER
       audience:
         - $AUD
         - api://podium
   YAML
   PODIUM_CONFIG_FILE="$WORK/registry-list.yaml" podium config show --server | grep -E "oauth_audience"
   ```

   **Expect.** `oauth_audience` reads `$AUD,api://podium`, joined with a comma
   in the configured order, and its provenance column reads `registry.yaml`. A
   scalar `audience:` is one audience verbatim and is never split on a
   separator, so only the sequence form produces two values here.

   Then start a registry on that config, the way step 3 starts one on the
   scalar block. Reading the resolved row establishes that the sequence parses,
   and it establishes nothing about whether a registry boots on it, which is the
   gap this scenario exists to close.

   ```bash
   podium serve --standalone --no-embeddings --config "$WORK/registry-list.yaml" \
     --bind 127.0.0.1:8152 > "$WORK/srv-list.log" 2>&1 &
   SRV_LIST=$!
   sleep 3
   curl -fsS http://127.0.0.1:8152/healthz && echo && cat "$WORK/srv-list.log"
   server_alive "$SRV_LIST" "$WORK/srv-list.log"
   kill "$SRV_LIST" 2>/dev/null; wait "$SRV_LIST" 2>/dev/null
   ```

   **Expect.** `/healthz` answers and the log carries no
   `config.oidc_jwt_audience_unset`, `config.identity_provider_unverified`, or
   `config.invalid_issuer_scheme`. A registry that exited leaves `curl` failing
   and the reason on the last line of `$WORK/srv-list.log`.

6. **Negative control, the configuration §13.12 used to carry.** Stop the
   server, then start one on the pre-correction block.

   ```bash
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   cat > "$WORK/old.yaml" <<YAML
   registry:
     identity_provider:
       type: oauth-device-code
       audience: $AUD
       authorization_endpoint: $ISSUER
   YAML
   podium serve --standalone --no-embeddings --config "$WORK/old.yaml" \
     --bind 127.0.0.1:8151 > "$WORK/old.log" 2>&1
   echo "exit=$?"; tail -2 "$WORK/old.log"
   ```

   **Expect.** A non-zero exit and `config.identity_provider_unverified`. A run
   where this configuration also starts has the guard switched off, and step 3's
   success then establishes nothing; record the failure rather than the success.

7. **Negative control, the edit a reader makes when changing the type alone.**

   ```bash
   cat > "$WORK/half.yaml" <<YAML
   registry:
     identity_provider:
       type: oidc-jwt
       audience: $AUD
       authorization_endpoint: $ISSUER
   YAML
   podium serve --standalone --no-embeddings --config "$WORK/half.yaml" \
     --bind 127.0.0.1:8152 > "$WORK/half.log" 2>&1
   echo "exit=$?"; tail -2 "$WORK/half.log"
   ```

   **Expect.** A non-zero exit with `config.invalid_issuer_scheme`, reporting
   that `PODIUM_OAUTH_ISSUER` must be an `https` URL and quoting the empty value
   it got. The code is the same one step 3 lists among the failures whose
   absence proves success, because an unset issuer and a non-`https` issuer
   share it. `authorization_endpoint` is read for the device-code flow and
   `oidc-jwt` reads `issuer`, so renaming the type and keeping the endpoint key
   yields a registry that still does not start. This is the trap a reader is
   most likely to reproduce.

**Cleanup.** Stop the server by its recorded PID and remove `$WORK`.

---

## S44: The web UI on a directly reachable `oidc-jwt` registry

**Goal.** Validate that the web UI served by a directly reachable `oidc-jwt`
registry running the §6.3.4 browser flow resolves identity from what the request
carries: a browser that presents no credential reads the public artifacts, sees
neither restricted layer, and is offered the sign-in control the flow's
enablement puts on the page.

**Covers.** The §13.10 web-UI authentication paragraph, `oidc-jwt` on a
directly reachable registry (§6.3.3), the §6.3.4 browser-flow enablement guard,
the §7.3.4 posture read, and §4.6 visibility for an anonymous caller.

**Why by hand.** The assertion is what a person sees in the artifact list and in
the page's authentication control. No Go test reads a browser rendering, which is
why the previous §13.10 text could claim the UI ran a device-code flow with an
in-browser verification handoff and no test contradicted it.

**This is the stack the browser-flow scenarios run on.** S47 through S50, S55
through S57, S59, and S60 take their prerequisites and steps 1 to 4 from here and
then sign in, so a change to the Keycloak registration or the serve invocation
below reaches each of them.

**Bootstrap admin, for S56, S57, S59, and S60.** This stack seeds no tenant-admin
grant and sets no `PODIUM_BOOTSTRAP_ADMINS`, and `POST /v1/admin/grants` is
itself admin-gated, so no caller on the stack as written below can issue the
first grant. S56, S57, S59, and S60 need one, so a run that reaches them amends step
3. Before step 3's `podium serve`, create a second realm user `carol` the way
S50 step 1 creates `bob`, read her `sub` and her access token, and name that
`sub` as the bootstrap admin:

```bash
$KC create users -r master -s username=carol -s enabled=true -s email=carol@acme.com
$KC set-password -r master --username carol --new-password carol
export CAROL_TOKEN="$(curl -fsS -X POST "$ISSUER/protocol/openid-connect/token" \
  -d grant_type=password -d client_id=podium -d client_secret="$KC_SECRET" \
  -d username=carol -d password=carol \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")"
export CAROL_SUBJECT="$(python3 -c "import base64,json,os; p=os.environ['CAROL_TOKEN'].split('.')[1]; p+='='*(-len(p)%4); print(json.loads(base64.urlsafe_b64decode(p))['sub'])")"
export PODIUM_BOOTSTRAP_ADMINS="$CAROL_SUBJECT"
```

Carol is the bootstrap operator and never signs in to the UI. She exists so the
stack holds a tenant admin who issues the first grant and who owns none of the
layers these scenarios register, and `PODIUM_BOOTSTRAP_ADMINS` is the only route
to one on a registry whose grant table starts empty. A run of S44 through S50
alone skips this block, and the stack behaves as it does today.

A run that reaches S59 exports one further value in the same place, before step
3's `podium serve`:

```bash
export PODIUM_MAX_USER_LAYERS=10
```

S59 registers personal layers as the signed-in caller, and on a run that reaches
it that caller already owns the personal layers the earlier scenarios registered
under the same subject. `PODIUM_MAX_USER_LAYERS` raises the §7.3.1 per-identity
cap on user-defined layers, which is 3 by default, so those registrations are
admitted rather than refused with `429 quota.layer_count_exceeded`. The cap is
read once at startup, so a registry already serving under the default has to be
stopped and started again with the raised value. A run that stops short of S59
skips this export, and the stack serves under the default cap as it does today.

**Prerequisites.** A local Keycloak serving an `https` issuer the host trusts, a
confidential client registered for the authorization-code flow, and one access
token it issued for the negative control in step 5.

The registry fetches the OIDC discovery document and the JWKS at startup, so
the issuer has to be reachable and its certificate has to verify. Two failures
follow from that and are worth knowing before setting up, because each produces
a refusal that looks like the scenario failing rather than the IdP being
misconfigured:

- An `http` issuer is refused with `config.invalid_issuer_scheme` (§6.3.3).
  Keycloak's `start-dev` listens on `http://0.0.0.0:8080` and its discovery
  document reports an `http` issuer, so a plain `start-dev` container cannot
  serve this scenario.
- An `https` issuer whose certificate the host does not trust is refused with
  `oidc-jwt: issuer ... is unreachable at startup` wrapping
  `x509: certificate signed by unknown authority`. A self-signed certificate
  reaches this, so the certificate has to come from a CA in the host trust
  store. The registry reads no custom CA bundle and has no verification-skip
  switch.

1. Install `mkcert` and add its CA to the host trust store. This modifies the
   machine's trust store and prompts for an administrator password.

   ```bash
   brew install mkcert && mkcert -install
   ```

2. Issue a certificate for the loopback names Keycloak will serve.

   ```bash
   export KCERT="$(mktemp -d)"
   mkcert -cert-file "$KCERT/cert.pem" -key-file "$KCERT/key.pem" localhost 127.0.0.1
   ```

3. Start Keycloak with that certificate, publishing the `https` port.

   ```bash
   docker run -d --name kc-podium \
     -p 127.0.0.1:8443:8443 \
     -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
     -v "$KCERT:/certs:ro" \
     quay.io/keycloak/keycloak:26.7.2 start-dev \
     --https-certificate-file=/certs/cert.pem \
     --https-certificate-key-file=/certs/key.pem
   ```

   Wait for it to answer and confirm it reports an `https` issuer before going
   further. Keycloak takes several seconds to start, so poll rather than
   assuming:

   ```bash
   until curl -fsS -o /dev/null https://127.0.0.1:8443/realms/master/.well-known/openid-configuration 2>/dev/null; do sleep 2; done
   curl -fsS https://127.0.0.1:8443/realms/master/.well-known/openid-configuration \
     | python3 -c "import json,sys; print(json.load(sys.stdin)['issuer'])"
   ```

   **Expect.** `https://127.0.0.1:8443/realms/master`, fetched without `-k`. A
   `curl` that needs `-k` means the trust store step did not take, and the
   registry will refuse the issuer for the same reason.

4. Register a confidential client that issues a full access token, completes the
   authorization-code flow against the registry's callback, and grants the scope
   that carries the group claim. The built-in `admin-cli` client is not usable
   here: Keycloak 26 issues it a *lightweight* access token carrying only
   `exp, iat, jti, iss, typ, azp, sid, scope`, with no `sub` and no `aud` and a
   sixty-second lifetime. `pkg/identity/oidc_jwt.go` requires an audience and a
   subject, so that token cannot authenticate at all, and it would expire between
   here and step 5.

   ```bash
   KC="docker exec kc-podium /opt/keycloak/bin/kcadm.sh"
   $KC config credentials --server http://localhost:8080 --realm master --user admin --password admin
   $KC create clients -r master \
     -s clientId=podium -s enabled=true -s publicClient=false \
     -s directAccessGrantsEnabled=true -s standardFlowEnabled=true \
     -s 'redirectUris=["http://127.0.0.1:8153/v1/ui/auth/callback"]' \
     -s 'attributes."client.use.lightweight.access.token.enabled"=false' \
     -s 'attributes."access.token.lifespan"=1800'
   CID=$($KC get clients -r master -q clientId=podium --fields id --format csv --noquotes)
   $KC create clients/$CID/protocol-mappers/models -r master \
     -s name=podium-aud -s protocol=openid-connect -s protocolMapper=oidc-audience-mapper \
     -s 'config."included.client.audience"=podium' -s 'config."access.token.claim"=true'
   export KC_SECRET="$($KC get clients/$CID/client-secret -r master --fields value --format csv --noquotes)"
   echo "client secret length: ${#KC_SECRET}"
   ```

   **Expect.** A non-zero secret length. `publicClient=false` is what makes
   Keycloak generate one, and prerequisite 5, step 3, and every scenario that
   signs in read it. `standardFlowEnabled=true` and the redirect URI are what let
   the browser complete the authorization-code flow against the callback path the
   registry serves on `http://127.0.0.1:8153`. `directAccessGrantsEnabled=true`
   stays because step 5's password grant still needs it.

   Register the client scope that carries the group claim, create the realm group
   the group-scoped layer is keyed on, and put the `admin` user in it.

   ```bash
   $KC create client-scopes -r master -s name=groups -s protocol=openid-connect \
     -s 'attributes."include.in.token.scope"=true' || true
   SCID=$($KC get client-scopes -r master --fields id,name --format csv --noquotes | grep ',groups$' | cut -d, -f1)
   $KC create client-scopes/$SCID/protocol-mappers/models -r master \
     -s name=groups -s protocol=openid-connect -s protocolMapper=oidc-group-membership-mapper \
     -s 'config."claim.name"=groups' -s 'config."full.path"=false' \
     -s 'config."access.token.claim"=true' -s 'config."id.token.claim"=true' || true
   $KC update clients/$CID/optional-client-scopes/$SCID -r master
   $KC create groups -r master -s name=podium-comp
   GROUP_ID=$($KC get groups -r master -q search=podium-comp --fields id --format csv --noquotes)
   ADMIN_UID=$($KC get users -r master -q username=admin --fields id --format csv --noquotes)
   $KC update users/$ADMIN_UID/groups/$GROUP_ID -r master \
     -s realm=master -s userId=$ADMIN_UID -s groupId=$GROUP_ID -n
   ```

   **Expect.** The scope, the mapper, the group, and the membership are created,
   and the scope is assigned to the `podium` client as an optional client scope.
   A Keycloak version that already ships a `groups` client scope reports the
   creation as a conflict, which the `|| true` absorbs; the assignment is what
   that version needs. Without the assignment the client grants no such scope:
   Keycloak validates a requested scope against the client's default and optional
   client scopes and answers `invalid_scope`, so a sign-in would stop at the
   authorization endpoint on the scope set the browser flow sends by default.
   `full.path=false` makes the claim value `podium-comp` rather than a group
   path, which is the value step 3's group mapping reads.

   Give the realm `admin` user an email address.

   ```bash
   $KC update users/$ADMIN_UID -r master -s email=alice@acme.com -s emailVerified=true
   ```

   **Expect.** The command reports no error. The account cluster in the web
   shell renders the caller's own email where the posture read carries one, and
   the browser flow already requests the `profile email` scopes, so a realm user
   with no address exercises only the subject fallback and leaves the email
   reading unvalidated. S47 step 1 and step 3 read this address.

   `kcadm` targets `http://localhost:8080` from inside the container on purpose.
   `--server https://localhost:8443` fails with "Console is not active, but
   truststore password is required", and pointing its truststore at the PEM fails
   with "Failed to load truststore" because it expects a Java keystore. The
   registry still reaches Keycloak over `https`; only this admin CLI uses the
   container-local `http` port.

5. Mint the access token for step 5's negative control, and read the claims the
   later steps depend on.

   ```bash
   export ISSUER="https://127.0.0.1:8443/realms/master"
   export TOKEN="$(curl -fsS -X POST "$ISSUER/protocol/openid-connect/token" \
     -d grant_type=password -d client_id=podium -d client_secret="$KC_SECRET" \
     -d username=admin -d password=admin \
     | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")"
   python3 - <<'PY'
   import base64, json, os
   p = os.environ["TOKEN"].split(".")[1]
   p += "=" * (-len(p) % 4)
   c = json.loads(base64.urlsafe_b64decode(p))
   print("sub:", c.get("sub"), "| aud:", c.get("aud"))
   PY
   ```

   **Expect.** A `sub` value, and an `aud` that includes `podium`. An empty `sub`
   or a missing `aud` means the client registration in prerequisite 4 did not
   take, and steps 5 to 7 cannot run.

   `client_secret` is required because prerequisite 4 registers a confidential
   client, and Keycloak requires client authentication on the token endpoint for
   one. A request without it answers `invalid_client`, `curl -fsS` exits
   non-zero, and `TOKEN` is left empty before the registry is ever started.

   `aud` is a JSON array, typically `['podium', 'master-realm', 'account']`. Take
   the `podium` element alone for the audience the registry is configured with:
   §6.3.3 verifies the `aud` claim on every token, and a mismatch rejects the
   token in step 5 for a reason unrelated to what this scenario tests.

**Teardown for the IdP.** `docker rm -f kc-podium` and `rm -rf "$KCERT"`. The
`mkcert` CA stays in the trust store until removed with `mkcert -uninstall`.

**Steps.**

1. Run the isolation block. `ISSUER` and `TOKEN` come from the Prerequisites
   above and are already exported. Set the audience and the subject from the
   claims prerequisite 5 printed, and bind `127.0.0.1:8153`.

   ```bash
   export AUD="podium"                                  # the podium element of the aud array
   export SUBJECT="<the sub value prerequisite 5 printed>"
   export RESTRICTED_ID="salary-bands"                  # used by steps 5 and 7
   export GROUP_ARTIFACT_ID="comp-policy"               # used by S47
   ```

2. Build a registry with one public layer, one user-scoped layer, and one
   group-scoped layer, giving each restricted artifact a name that cannot be
   confused with the public one.

   ```bash
   mkdir -p "$WORK/pub/handbook" "$WORK/priv/salary-bands" "$WORK/comp/comp-policy"
   podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
   podium artifact scaffold --type context --description "Salary bands" --force "$WORK/priv/salary-bands"
   podium artifact scaffold --type context --description "Compensation policy" --force "$WORK/comp/comp-policy"
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     identity_provider:
       type: oidc-jwt
       issuer: $ISSUER
       audience: $AUD
     layers:
       - id: public-handbook
         source: { local: { path: $WORK/pub } }
         visibility: { public: true }
       - id: private-comp
         source: { local: { path: $WORK/priv } }
         visibility: { users: [$SUBJECT] }
       - id: comp-readers-policy
         source: { local: { path: $WORK/comp } }
         visibility: { groups: [comp-readers] }
   YAML
   ```

   The `users:` value is the token's subject rather than a username, because
   §6.3.3 keys `users:` visibility on the claim the registry reads as the
   subject. Naming the login name here leaves the restricted layer invisible to
   the very token step 5 uses, and step 5 then fails for a reason unrelated to
   what this scenario tests.

   The `groups:` value is the layer group name rather than the claim value the
   IdP emits. Step 3 maps the claim value `podium-comp` onto `comp-readers`, so
   the group a signed-in token carries reaches a visibility decision. This layer
   is what S47 reads after signing in, and it is invisible to an anonymous
   caller for the same reason `private-comp` is.

3. Start the registry with the UI and the §6.3.4 browser flow enabled, and
   record the PID. The acquisition values are environment-only, because one of
   them is a client credential and a credential passed on the command line is
   readable from the process table.

   ```bash
   export PODIUM_WEB_UI_AUTH=true
   export PODIUM_WEB_UI_OAUTH_CLIENT_ID=podium
   export PODIUM_WEB_UI_OAUTH_CLIENT_SECRET="$KC_SECRET"
   export PODIUM_WEB_UI_REDIRECT_URI="http://127.0.0.1:8153/v1/ui/auth/callback"
   export PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT="$ISSUER/protocol/openid-connect/auth"
   export PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT="$ISSUER/protocol/openid-connect/token"
   export PODIUM_WEB_UI_AUTH_TRANSACTION_TTL=10m
   export PODIUM_IDP_GROUP_MAPPING=podium-comp=comp-readers
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" \
     --web-ui --bind 127.0.0.1:8153 > "$WORK/srv.log" 2>&1 &
   export SRV=$!
   sleep 3
   ```

   `PODIUM_WEB_UI_OAUTH_SCOPES` is left unset, so the redirect sends the default
   set `openid profile email groups`, which is the set prerequisite 4's client
   now grants.

   No audience key is set here. Step 2's `registry.yaml` carries
   `identity_provider.audience`, and the sign-in redirect asks the IdP for the
   registry's resolved audience, so the token the cookie carries verifies under
   §6.3.3 like a token any other consumer presents. This stack is the
   operator-facing check on that rule: a redirect built by reading
   `PODIUM_OAUTH_AUDIENCE` would carry an empty audience here, and S47's catalog
   read after signing in would fail with `401` `auth.untrusted_token`.

   The bind stays `127.0.0.1:8153`, which is a loopback `http` origin and is the
   origin prerequisite 4's redirect URI names. A registry started with the
   browser flow enabled and any acquisition value missing exits before listening
   with `config.web_ui_auth_unconfigured`, so a failure here is read from
   `$WORK/srv.log` rather than from the browser.

4. Confirm the identity provider is switched on, and that the browser flow is
   mounted, before asserting what the UI hides.

   ```bash
   curl -fsS http://127.0.0.1:8153/healthz; echo
   server_alive "$SRV" "$WORK/srv.log"
   grep 'identity provider' "$WORK/srv.log"
   grep 'browser sign-in' "$WORK/srv.log"
   PODIUM_CONFIG_FILE="$WORK/registry.yaml" podium config show --server | grep '^identity_provider '
   ```

   **Expect.** `/healthz` reports `{"mode":"ready"}` rather than `mode: public`,
   the log line reads `identity provider: oidc-jwt (verifying caller tokens
   against accepted issuers $ISSUER and accepted audiences $AUD)`, a second
   line reads `web UI browser sign-in mounted at /v1/ui/auth/sign-in
   (§6.3.4)`, and `config show` prints
   `oidc-jwt`. A registry in public mode shows every artifact to everyone and
   would make step 6 pass for the wrong reason. A missing sign-in line means the
   browser flow is off, and S47 through S50, S55 through S57, S59, and S60
   cannot run.

   `config show` takes its path from `PODIUM_CONFIG_FILE` and defines no
   `--config` flag, which `serve` does; passing `--config` exits 1 with `flag
   provided but not defined: -config`. The variable is `PODIUM_CONFIG_FILE` and
   not `PODIUM_CONFIG`: the shorter name is read by nothing, and using it prints
   the table with every value blank, which reads like a broken registry rather
   than a mistyped variable.

5. **Negative control.** Confirm the restricted artifact exists and is served to
   an authenticated caller.

   ```bash
   curl -fsS -H "Authorization: Bearer $TOKEN" \
     "http://127.0.0.1:8153/v1/load_artifact?id=$RESTRICTED_ID" | head -c 200; echo
   ```

   **Expect.** The restricted artifact comes back. Without this step an empty or
   mis-registered restricted layer produces the same result in step 6 and the
   scenario passes on nothing. This step is also what gives step 7 its meaning.

6. Load the UI with no credential: no gateway in front, no header, no prior
   `podium login`, and no session cookie. Open `http://127.0.0.1:8153/app/` in a
   private browser window to see what a person sees, and issue the reads the
   scenario turns on directly, which is the machine-checkable path. On load the
   React bundle issues the posture read and the catalog reads it draws the page
   from, each carrying whatever the browser attaches and nothing else, which
   here is nothing.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/load_domain?path="; echo
   curl -sS "http://127.0.0.1:8153/v1/search_artifacts?query=salary"; echo
   ```

   **Expect.** HTTP 200. The `notable` list carries the public artifact and
   neither restricted one, and the search for the restricted artifact's name
   reports `total_matched: 0`. In the browser the page renders the same catalog
   scope, reports no authentication error, and shows a sign-in control: this
   deployment enables the browser flow and this caller resolves no subject,
   which is the case the §13.10 sign-in control rule renders sign-in for. The
   page shows no verification URL and no device code, because the registry runs
   no device-code flow for a browser, and it shows neither the account cluster
   nor the sign-out control inside it, because the same rule renders those for a
   caller who resolves a subject.

7. Confirm the restricted artifact is invisible rather than merely absent from a
   list, by requesting it directly with no credential.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/load_artifact?id=$RESTRICTED_ID" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `registry.not_found` at HTTP 404. Invisibility is served as
   not-found rather than as a forbidden code, so an anonymous caller learns
   nothing about whether the artifact exists.

   Do not pass `-f` or `-o /dev/null` here. `-f` makes curl exit 22 on a 4xx
   before printing, and `-o /dev/null` discards the body this step exists to
   read.

   Reading the error code does not by itself distinguish this from a typo: a
   mistyped id returns the same `registry.not_found`. What makes this step
   meaningful is step 5, where the same id returned the artifact in full to the
   authenticated caller. Run step 5 first and compare the two.

**Cleanup.** The teardown is conditional on whether the run continues. S47
through S50 sign in against this stack, so when any of them is being run, leave
the registry running, keep `$WORK`, and keep Keycloak and `$KCERT` in place, and
let the last of them perform the teardown. When none of them is being run, stop
the server by its recorded PID, run `rm -rf "$WORK"`, and remove the IdP with
`docker rm -f kc-podium` and `rm -rf "$KCERT"`.

---

## S45: The runbook's read-only write set matches what the registry rejects

**Goal.** Validate that the read-only-mode write set an operator reads in
`deploy/runbook.md` and `docs/reference/http-api.md` enumerates what the running
registry actually rejects, and that it names no endpoint the registry does not
serve.

**Covers.** §13.2.1 read-only mode, the `registry.read_only` error code, and the
two shipped restatements of the write set.

**Why by hand.** An operator reads the runbook during a database outage and
works from its list. The value is that the list matches the running registry.
Until the §13.2.1 correction the list named `podium login`-driven token issuance
against a session table, sending a reader looking for a credential-issuing
endpoint that has never existed, and no test compared the list to the routes the
registry registers. A registry that enables the §6.3.4 browser flow does serve
the §7.3.4 authentication routes, and none of them joins the write set: each is
served unchanged in read-only mode, none mints a registry credential, and none
writes registry state. This stack enables neither the web UI nor the browser
flow, so it registers none of them, which is what step 4 confirms.

**Prerequisites.** The S21 standard-deployment stack with a severable Postgres
primary. When it is unavailable, skip and record the skip.

Read-only mode needs no CLI toggle and none exists. The §13.2.1 probe runs from
boot whenever `read_only.probe_failures` is above zero, which it is by default
(three failures, five-second interval), so stopping the metadata store flips the
mode within roughly the probe interval times the failure count. Only step 5
needs more than the compose stack provides, and it says so.

**Relationship to S21.** S21 already brings a registry to read-only mode. Run
these steps as an extension of S21 rather than rebuilding the stack, and fold
them into S21 permanently if its setup already reaches this state.

**Steps.**

1. Follow S21 until the registry has fallen back to read-only mode against the
   replica.
2. Read the write set from the two shipped documents rather than from memory.

   ```bash
   grep -n "read-only" -A4 "$REAL_HOME/projects/podium/docs/reference/http-api.md" | grep -i "ingest webhooks"
   grep -n "Impact" -A4 "$REAL_HOME/projects/podium/deploy/runbook.md" | grep -i "ingest webhooks"
   ```

   **Expect.** Both enumerate ingest webhooks, layer admin operations, admin
   grants, and tenant management, and neither names freeze toggles. Neither
   write-set list names token issuance or a session table.
   `docs/reference/http-api.md` documents the §7.3.4 authentication route paths
   outside that list, and those routes are no members of it.

3. Issue each write request and read the error code from the body rather than
   the status class. `$PORT` is the port S21 bound, `127.0.0.1:8118` unless it
   was changed.

   ```bash
   B="http://127.0.0.1:$PORT"
   for r in "POST /v1/ingest/webhook/probe" \
            "POST /v1/layers" \
            "POST /v1/layers/update" \
            "POST /v1/layers/reorder" \
            "POST /v1/layers/reingest" \
            "POST /v1/layers/restore" \
            "DELETE /v1/layers" \
            "POST /v1/admin/erase" \
            "POST /v1/admin/reembed"; do
     METHOD="${r%% *}"
     TARGET="${r#* }"
     printf '%s %s -> ' "$METHOD" "$TARGET"
     curl -s -X "$METHOD" "$B$TARGET" -d '{}' | python3 -c "import json,sys; print(json.load(sys.stdin).get('code','(no code)'))" 2>/dev/null || echo "(unparseable)"
   done
   ```

   **Expect.** Every line reads `registry.read_only`, returned as HTTP 503.

   `/v1/admin/erase` and `/v1/admin/reembed` are rejected but appear in neither
   shipped document's list. That is consistent with §13.2.1, which says the
   named categories "do not bound the rule", and it means the documents describe
   less than the registry enforces. Record it rather than treating it as a
   failure.

   **Two of the named categories cannot reach `registry.read_only` on this
   setup, and a run that records them as failures is wrong.** `POST
   /v1/admin/grants` returns `403 auth.forbidden` because `requireAdmin` runs
   before `rejectIfReadOnly` (`pkg/registry/server/admin.go:22-33`), and with no
   identity provider configured no caller is an admin. `POST /v1/admin/tenants`
   returns `404 registry.tenant_management_unavailable` because
   `tenantAdminGate` runs first (`pkg/registry/server/tenants.go:138-143`). Both
   responses are identical to their healthy-registry baseline, so this step
   establishes nothing about them. Demonstrating either needs a registry with a
   real identity provider and an authenticated admin, which S21 does not set up.
   Take the baseline first and compare, rather than reading a 403 or a 404 as a
   read-only rejection.

   **No freeze endpoint is in the write set.** Freeze windows are
   configuration-only: they come from the `registry.yaml` `freeze_windows:` key
   (`internal/serverboot/yaml_config.go`, §4.7.2), are enforced during ingest,
   and are bypassed with `podium layer reingest --break-glass`. §13.2.1 does not
   name freeze toggles, and the break-glass reingest write is covered by the
   `POST /v1/layers/reingest` probe above.

4. Confirm this stack registers neither the registry's authentication routes nor
   the posture read, so the write set the two documents enumerate is the whole of
   what an operator can reach here.

   ```bash
   for p in /v1/ui/auth/sign-in /v1/ui/auth/callback /v1/ui/auth/sign-out /v1/ui/session; do
     printf '%s ' "$p"
     curl -s -o /dev/null -w '%{http_code}\n' "http://127.0.0.1:$PORT$p"
   done
   ```

   **Expect.** 404 on each, for two different reasons. Sign-in, the callback, and
   sign-out are mounted only when both conjuncts hold, and neither holds here:
   this stack enables neither the web UI nor the browser flow. The posture read
   is mounted on the web UI alone, and the web UI is off, which is what accounts
   for its 404. This stack also configures no identity provider, so nothing
   refuses any of the four probes ahead of route matching. A stack that does
   mount them answers each of these paths rather than `registry.read_only`,
   because none of them is a write.

   The clause proposal 0012 struck named a write endpoint the registry does not
   serve. That is what made it wrong rather than merely stale: an operator could
   not have exercised the write path it described even when the registry was
   healthy.

5. Confirm the read path still serves, so step 3's rejections are read-only mode
   rather than a registry that is simply down.

   ```bash
   curl -s -o /dev/null -w 'search %{http_code}\n' "$B/v1/search_artifacts?q=test"
   curl -s -o /dev/null -w 'load   %{http_code}\n' "$B/v1/load_artifact?id=<an-ingested-id>"
   ```

   **Expect, and only on a primary-plus-replica deployment.** Both answer 200
   from the replica, and the read responses carry `X-Podium-Read-Only: true`.

   **This step needs a topology the S21 prerequisites do not provide.** The
   compose stack runs a single Postgres with no replica, so severing the primary
   takes the read path down with the write path: every read returns `500
   registry.unavailable` wrapping `connect: connection refused`. On that stack
   the step is unobservable and is skipped rather than recorded as a failure.
   Run it only against a deployment that has a replica the registry can read
   from, and record the skip and the reason otherwise. Steps 1 to 4 are
   unaffected and stand on their own.

6. Check the runbook's detection signal against the running registry, since a
   detection step that misfires is worth as much as a wrong endpoint list.

   ```bash
   curl -s "$B/healthz"; echo
   curl -s "$B/readyz";  echo
   ```

   **Expect.** `/healthz` reports `{"mode":"read_only"}`.

   On a single-Postgres stack `/readyz` reports
   `{"mode":"not_ready","replication_lag_seconds":0}` rather than the
   `read_only` the runbook's detection step names, because the readiness probe
   fails against the severed store. An operator following the runbook on that
   topology misses the signal. On a replica-backed deployment the store probe
   passes and the runbook is right. Record which topology produced the reading.

**Cleanup.** As S21.

---

## S46: Install the Helm chart into a cluster

**Goal.** Validate that `deploy/helm/podium` installs into a running cluster and
produces a pod that passes its probes and serves the API, and that the identity
provider the chart once defaulted to is still refused at the deployment level.

**Covers.** The §13.1 standard topology as the chart expresses it, the chart's
probe configuration, and `config.identity_provider_unverified` (§6.3.3) reached
through a `helm install` rather than through a local process.

**Why by hand.** `test/chart/chart_test.go` reads `values.yaml` and the template
files with `os.ReadFile` and `yaml.Unmarshal`, and pins that the values are
internally consistent, which is what the three simultaneous defects that
prompted it needed. `test/chart/render_test.go` runs `helm template` and asserts
what the manifests contain, which covers the object-store volume pairing, the
runtime-keys path, the name derivation for the bundled database, and the keys a
default install must leave to the secret. Neither installs the chart. They
cannot see a chart that renders valid YAML and still produces a pod that never
becomes ready: a probe path that does not answer, a manifest the API server
rejects, a container that cannot write where its configuration points, or an
image reference that does not resolve. Each of those appears only when a cluster
runs the chart.

**Prerequisites.** `helm`, `kubectl`, `kind`, and a working Docker daemon. When
any is absent, skip and record the skip.

The chart's `appVersion` is `0.0.0-dev`, so the image it references is not
published anywhere and has to be built locally and loaded into the cluster. That
is a property of the development chart rather than a defect.

**The stock defaults are a production topology, not a self-contained one.** A
bare `helm install` with no overrides fails to render, naming
`signing.secretName`, because the registry signs at ingest by default and the
chart mounts its signing key from an operator-supplied Secret. A render
carrying only `--set signing.secretName=<name>` renders `PODIUM_REGISTRY_STORE=postgres`,
`PODIUM_OBJECT_STORE=s3`, and `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, and takes the
DSN, the bucket, and the issuer from the secret named by `existingSecret`. There
is no dependency-free configuration to fall back on: the chart mounts no volume
for a SQLite database, so `sqlite` has nowhere to write, and the `filesystem`
object store needs `objects.enabled` and a claim. A pod configured with
`sqlite` starts, fails to open its database under the distroless
image's read-only root, and crash-loops with `mkdir /nonexistent: read-only file
system` followed by `open store: ping sqlite: unable to open database file`. The
scenario therefore stands up Postgres and an S3-compatible store in the cluster
rather than trying to avoid them.

**Steps.**

1. Build the image the chart references and create the cluster.

   ```bash
   cd "$REAL_HOME/projects/podium"
   docker build -t ghcr.io/lennylabs/podium:0.0.0-dev .
   kind create cluster --name podium-s46
   kubectl wait --for=condition=Ready node --all --timeout=180s
   kind load docker-image ghcr.io/lennylabs/podium:0.0.0-dev --name podium-s46
   arch="$(docker version --format '{{.Server.Arch}}')"
   for image in pgsty/minio:RELEASE.2026-08-04T00-00-00Z pgsty/mc:RELEASE.2026-09-16T00-00-00Z \
       pgvector/pgvector:pg16; do
     docker image inspect "$image" >/dev/null 2>&1 || docker pull --platform "linux/$arch" "$image"
     docker save --platform "linux/$arch" -o "$WORK/image.tar" "$image"
     kind load image-archive "$WORK/image.tar" --name podium-s46
   done
   docker exec podium-s46-control-plane crictl images
   ```

   **Expect.** The build succeeds, the node reports `Ready`, and `kind load
   docker-image` reports the Podium image loading onto the node. `kind load
   image-archive` prints nothing when it succeeds, so read the loads from
   `crictl images`, which lists `ghcr.io/lennylabs/podium` at `0.0.0-dev`,
   `docker.io/minio/minio`, `docker.io/minio/mc`, and
   `docker.io/pgvector/pgvector` at their pinned tags. Skipping the load of the Podium
   image leaves the pod in `ErrImagePull`, because `0.0.0-dev` resolves to
   nothing in any registry. The loop loads the third-party images for the same
   reason S76 step 1 does: a pull from inside the kind node can be refused by
   Docker Hub for the pinned MinIO tags, which leaves `deployment/minio`
   unavailable in step 2.
   The remaining steps run from the repository, because the `helm` commands
   name the chart by its relative path. The scenario starts no `podium-mcp` and
   no `podium` CLI command that reads `sync.yaml`, so the workspace walk from
   this directory configures nothing.

2. Deploy the chart's dependencies into the cluster: Postgres for the metadata
   store and MinIO for object storage. Both run without persistence, which is
   sufficient here because the scenario asserts installation rather than
   durability.

   ```bash
   kubectl create deployment pg --image=pgvector/pgvector:pg16
   kubectl set env deployment/pg POSTGRES_USER=podium POSTGRES_PASSWORD=podium \
     POSTGRES_DB=podium PGDATA=/tmp/pgdata
   kubectl expose deployment pg --port=5432
   kubectl create deployment minio --image=pgsty/minio:RELEASE.2026-08-04T00-00-00Z -- \
     minio server /data
   kubectl set env deployment/minio MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin
   kubectl expose deployment minio --port=9000
   kubectl wait --for=condition=available --timeout=240s deployment/pg deployment/minio
   ```

   `PGDATA` is redirected to `/tmp` because the image's default data directory
   is not writable in this configuration.

   **Expect.** Both deployments report `condition met`.

3. Create the bucket the registry expects.

   ```bash
   kubectl run mc --image=pgsty/mc:RELEASE.2026-09-16T00-00-00Z --restart=Never --rm -i \
     --quiet --command -- sh -c \
     'for i in $(seq 1 30); do mc alias set m http://minio:9000 minioadmin minioadmin >/dev/null 2>&1 && break; sleep 2; done && mc mb -p m/podium'
   ```

   The loop retries the alias until MinIO answers. `kubectl create deployment`
   adds no readiness probe, so the Deployment reports `available` before MinIO
   listens on port 9000, and a single attempt can fail with `connection
   refused`.

   **Expect.** `Bucket created successfully`.

4. Generate the registry signing key with the command
   `docs/deployment/clustered.md` gives, with the scratch directory under
   `$WORK`, and create the Secret the chart's `signing.secretName` names.
   `podium admin signing-key generate` writes only the file `--key-file` names
   and opens no store.

   ```bash
   KEY_DIR="$(mktemp -d "$WORK/signing-key.XXXXXX")"
   podium admin signing-key generate --key-file "$KEY_DIR/registry-signing.key"
   ls -l "$KEY_DIR/registry-signing.key"
   kubectl create secret generic podium-signing-key \
     --from-file=registry-signing.key="$KEY_DIR/registry-signing.key"
   ```

   **Expect.** `generate` prints one base64 key and a
   `key_id=<16 hex digits> role=signing` line, `ls -l` reads `-rw-------` for
   the key file, and `kubectl` reports `secret/podium-signing-key created`.

5. Create the secret the chart's `existingSecret` names. The chart injects it
   with `envFrom`, so every key becomes an environment variable in the pod, and
   this is the only way to set a `PODIUM_*` variable the templates do not render.

   ```bash
   kubectl create secret generic podium-secrets \
     --from-literal=PODIUM_POSTGRES_DSN="postgres://podium:podium@pg:5432/podium?sslmode=disable" \
     --from-literal=PODIUM_S3_BUCKET=podium \
     --from-literal=PODIUM_S3_ENDPOINT="http://minio:9000" \
     --from-literal=PODIUM_S3_REGION=us-east-1 \
     --from-literal=AWS_ACCESS_KEY_ID=minioadmin \
     --from-literal=AWS_SECRET_ACCESS_KEY=minioadmin
   ```

   The secret is required by this configuration rather than by the chart.
   `--set existingSecret=""` drops the `envFrom` block, which a deployment that
   needs no secret depends on. Confirm that path still renders a manifest the
   API server accepts, because it once rendered `secretRef` with an empty name
   and was rejected outright:

   ```bash
   helm template t deploy/helm/podium --set existingSecret="" \
     --set signing.secretName=podium-signing-key \
     | kubectl apply --dry-run=server -f -
   ```

   **Expect.** Both objects report `created (server dry run)`. A failure naming
   `envFrom[0].secretRef.name: Required value` means the guard on the block has
   been lost. The render passes `signing.secretName` so that the only refusal
   it can show is that one.

6. Install the chart and wait for the pod.

   ```bash
   helm install podium deploy/helm/podium \
     --set replicaCount=1 \
     --set signing.secretName=podium-signing-key \
     --set config.identityProvider.type=""
   kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=podium --timeout=180s
   kubectl get pods -l app.kubernetes.io/name=podium
   kubectl logs deployment/podium-podium | grep 'podium-server listening on '
   ```

   `config.identityProvider.type` is emptied because the chart's `oidc-jwt`
   default needs a reachable `https` issuer, which this cluster does not have.
   An empty provider selects none, and the registry serves every caller as
   anonymous.

   Public mode is the other way to reach an open registry, and it requires both
   of its values. It binds loopback unless the bind is explicitly allowed, and
   the chart renders `PODIUM_BIND=0.0.0.0:8080` so the kubelet can reach the
   probes, so `config.publicMode` alone fails at startup with
   `config.public_bind_refused`. Setting both starts the pod and logs
   `mode=public` under the public-mode banner:

   ```bash
   helm upgrade podium deploy/helm/podium --set replicaCount=1 \
     --set signing.secretName=podium-signing-key \
     --set config.identityProvider.type="" \
     --set config.publicMode=true --set config.allowPublicBind=true
   kubectl rollout status deployment/podium-podium --timeout=180s
   kubectl logs deployment/podium-podium | grep 'podium-server listening on '
   ```

   Public mode and an identity provider are mutually exclusive; setting both
   fails with `config.public_mode_with_idp`.

   **Expect.** The pod reports `1/1 Running` with `0` restarts within roughly
   twenty seconds, and the first `grep` prints a timestamped `podium-server
   listening on [::]:8080 (mode=standalone)` line. The address reads `[::]`
   because Go reports a listener on the `0.0.0.0` wildcard as the dual-stack
   `[::]`. After the public-mode upgrade, the rollout completes and the second
   `grep` prints `podium-server listening on [::]:8080 (mode=public)`. A pod
   stuck at `0/1` with a rising restart count is the failure this scenario
   exists to catch; read `kubectl logs` for the reason rather than the pod
   status.

7. Confirm the registry serves through the Service rather than only inside the
   pod, which is what the probes and the Service selector together establish.

   ```bash
   kubectl port-forward svc/podium-podium 18080:8080 >/dev/null 2>&1 &
   PF=$!; sleep 4
   curl -sS http://127.0.0.1:18080/healthz
   curl -sS http://127.0.0.1:18080/readyz
   curl -sS -o /dev/null -w 'load_domain %{http_code}\n' "http://127.0.0.1:18080/v1/load_domain?path="
   kill $PF
   ```

   **Expect.** `/healthz` reports `{"mode": "public"}`, because step 6 ends on
   the public-mode upgrade, `/readyz` reports
   `{"mode": "ready", "replication_lag_seconds": 0}`, and `load_domain` answers
   `200`. Public mode is a `/healthz` signal and is absent from `/readyz`. These are the two paths the chart's liveness and readiness probes use,
   so a pod that is `Ready` and a `/healthz` that does not answer would mean the
   probes are pointed somewhere else.

8. **Negative control.** Re-install with the identity provider the chart once
   defaulted to and confirm the deployment refuses it. The upgrade passes
   `signing.secretName` so that it renders and the pod reaches the startup
   guard.

   ```bash
   helm upgrade podium deploy/helm/podium \
     --set replicaCount=1 \
     --set signing.secretName=podium-signing-key \
     --set config.identityProvider.type=oauth-device-code
   for i in $(seq 1 90); do
     NEW_POD="$(kubectl get pods -l app.kubernetes.io/name=podium \
       -o jsonpath='{range .items[*]}{.metadata.name} {.status.containerStatuses[0].state.waiting.reason}{"\n"}{end}' \
       | awk '$2 == "CrashLoopBackOff" {print $1}')"
     [ -n "$NEW_POD" ] && break
     sleep 2
   done
   kubectl get pods -l app.kubernetes.io/name=podium
   kubectl logs --previous "$NEW_POD" --tail=3
   ```

   The loop waits up to three minutes for a pod whose container is waiting in
   `CrashLoopBackOff` and records its name, so the log read names that pod
   alone. A label-selector log read would mix in the lines of the public-mode
   pod, which the rolling update keeps serving while the new pod fails.

   **Expect.** `NEW_POD` names the new pod, `kubectl get pods` lists it in
   `CrashLoopBackOff` beside the public-mode pod at `1/1 Running`, and its
   previous log carries
   `config.identity_provider_unverified`, naming `injected-session-token`,
   `oidc-jwt`, and `trusted-headers` as the providers the registry verifies. This
   is the defect that made a default `helm install` unable to start before the
   chart's `values.yaml` was corrected, reproduced at the deployment level rather
   than asserted against a file. A run where this pod becomes `Ready` means the
   startup guard is not doing its job, and step 6's success then establishes
   nothing. Such a run leaves `NEW_POD` empty after the loop, and the log read
   then fails for want of a pod name.

**Cleanup.**

```bash
helm uninstall podium
kind delete cluster --name podium-s46
docker rmi ghcr.io/lennylabs/podium:0.0.0-dev
rm -rf "$WORK"
```

---

## S47: Sign in through the UI and read a layer an anonymous caller does not get

**Goal.** Validate that the browser signs in through the registry, that the
registry performs the code exchange server-side and returns the resulting token
in an `HttpOnly` cookie, and that the signed-in page reads a group-scoped layer
the same browser could not read a moment earlier.

**Covers.** §6.3.4 browser acquisition, the §7.3.4 sign-in, callback, sign-out,
and posture routes, the §6.3.3 second accepted credential location, §6.3.1 group
mapping, §4.6 group-scoped visibility, and the §13.10 layer panel.

**Why by hand.** The assertion is a redirect chain a person completes at the
IdP's own login page and the view the page renders afterwards. No Go test drives
a browser through a live IdP, and no Go test reads what the page shows.

**Prerequisites.** The S44 stack: run S44's Prerequisites and steps 1 to 4, and
leave the registry running. When Keycloak or the `mkcert` CA is unavailable, skip
and record the skip.

**Steps.**

1. Read the deployment's posture as the page does, before signing in.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/ui/session"; echo
   ```

   **Expect.** HTTP 200 with `identity_provider_configured: true`,
   `public_mode: false`, and `browser_auth` reporting `enabled: true` with
   `sign_in_path: /v1/ui/auth/sign-in` and
   `sign_out_path: /v1/ui/auth/sign-out`. There is no `subject` key, because
   this request carries no credential. There is no `email` key for the same
   reason. The read needs no credential and refuses no request for lack of one.
   `layer_capabilities` is present and reports
   `manage_any_layer: false`, because this stack configures an identity provider
   and seeds no admin grant, so the caller the read reports on holds no
   tenant-admin role. The object is present on every answer, including this one,
   which carries no `subject`. A missing `layer_capabilities` key means the
   registry is serving a build from before the posture read reported
   capabilities, and the panel on that build renders every write control on
   every row.

   Read the same route again with the credential S44's prerequisite 5 minted.

   ```bash
   curl -sS -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:8153/v1/ui/session"; echo
   ```

   **Expect.** HTTP 200 with `subject` carrying a UUID and `email` carrying
   `alice@acme.com`. The two carry different values, which is the reading this
   command exists for: Keycloak issues an opaque UUID as the subject, and the
   email is the address S44's prerequisite 4 set on the realm `admin` user. An
   answer carrying `subject` and no `email` means that `$KC update` did not
   take, or that the token carries no `email` claim because the `email` scope
   was not granted.

2. Take the anonymous baseline for the group-scoped artifact.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/load_artifact?id=$GROUP_ARTIFACT_ID" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `registry.not_found` at HTTP 404. Without this baseline step 4
   establishes nothing: an artifact that is visible to everyone would produce the
   same reading afterwards.

3. Open `http://127.0.0.1:8153/app/#/layers` in a private browser window, read
   the layer panel, and click the sign-in control. Keycloak's login page
   appears; sign in as `admin` with the password `admin`. The address names the
   layers route because an empty hash resolves to the catalog route, and a
   reader who opens `/app/` alone is looking at the domain browser rather than
   at the panel.

   **Expect.** Before the click, the panel header draws no `Register layer`
   control and the empty state reads that the registry resolved no caller for
   this page rather than instructing the reader to register a layer. A
   `Register layer` control drawn here offers a registration the registry
   refuses, and an empty state that instructs the reader to register a layer
   points at a control this page does not carry. The list is empty rather than
   refused,
   because the visibility filter narrows to nothing for a caller with no
   subject. The sidebar's own catalog line reads "The catalog holds no domains.
   Its artifacts sit at the top of the hierarchy." here, because S44's public
   layer puts its one artifact at the layer root, so the root catalog read
   returns no subdomain and one notable entry.

   Then the browser leaves the registry for the authorization endpoint,
   returns to `http://127.0.0.1:8153/app/` after the login, and the account
   cluster stands where the sign-in control was: the top bar carries
   `alice@acme.com` beside an avatar reading `A`, and the sign-out control sits
   inside the menu that cluster opens. A top bar carrying the UUID step 1 read
   as `subject` means the posture read returned no `email`, or that the shell is
   a build from before the account cluster read it, and the reader is looking at
   a provider-chosen identifier where the design draws their own address. A
   browser that lands on
   an `invalid_scope` or `invalid_redirect_uri` error page at Keycloak means
   prerequisite 4's client scope or redirect URI did not take.

4. Read the group-scoped artifact in the signed-in page. Open the compensation
   policy from the catalog.

   **Expect.** The artifact the same browser could not see in step 2 renders in
   full. That result requires each of the following. The callback exchanged the
   code server-side and returned the IdP-signed token in
   `__Host-podium_session`. The token carries the `groups` claim value
   `podium-comp`, because the granted scope carries it. The registry's
   `PODIUM_IDP_GROUP_MAPPING` rewrites that value to the layer group name
   `comp-readers`. A page that renders the public
   handbook and refuses the compensation policy means the group claim did not
   reach the token, which is read from the browser's own network panel on the
   `load_artifact` response rather than guessed at.

   A `401` `auth.untrusted_token` on the catalog read after signing in means the
   token carries an audience the registry does not accept. On this stack that is
   the resolved-audience rule failing, because the audience lives in
   `registry.yaml` rather than in the environment.

5. Confirm no credential is reachable from JavaScript. In the browser console,
   read the document's cookies.

   ```javascript
   document.cookie
   ```

   **Expect.** The result names neither `__Host-podium_session` nor
   `__Host-podium_auth`. Both cookies are `HttpOnly`, so the page holds no token
   and an injected script on this origin can read none. This is the property the
   registry-mediated flow exists for, and the page rendering author-controlled
   markdown on the same origin is why.

6. Open the account cluster from the email in the top bar, press Sign out
   inside the menu, then reload.

   **Expect.** The page returns to the anonymous view: the compensation policy
   is gone from the catalog and the sign-in control is back. Signing out clears
   both cookies. The registry holds no session state to clear, so the same
   result follows on any replica.

**Cleanup.** As S44, on the same terms: leave the stack running when S48 through
S50 follow, and run S44's teardown only when this is the last scenario executed.

---

## S48: Register a layer through the panel and read the one-time secret

**Goal.** Validate that a signed-in reader registers a layer of their own from
the layer panel, that the registry returns the webhook URL and the HMAC secret
once, and that the panel presents the secret as shown once and never served
again.

**Covers.** §7.3.1 layer registration through the UI, the §13.10 layer panel,
the §6.3.4 session credential on a write, and the §6.3.4 browser-origin gate on
a same-origin write.

**Why by hand.** The assertion is that a person reads a credential on screen,
is told it is shown once, and can still copy it. No Go test reads a rendering,
and a secret the panel returned but did not present is indistinguishable from
one it presented badly in an API-level test.

**Prerequisites.** The S44 stack with a signed-in session, meaning S47 steps 1
to 3 completed and the browser still signed in.

**Steps.**

1. Create a Git repository the layer can source from.

   ```bash
   mkdir -p "$WORK/own-repo" && cd "$WORK/own-repo" && git init -q
   podium artifact scaffold --type skill --description "Roll a release" "$WORK/own-repo/release"
   git add -A && git -c user.email=alice@acme.com -c user.name=alice commit -qm "add release skill"
   ```

   This repository stands for the repository the reader would push to the remote
   URL step 2 registers. No step in S48, S49, or S50 reingests the layer, so the
   registration never fetches it.

2. Open the layer panel in the signed-in page, press Register layer, and fill
   the form. Choose "Git repository" as the source, give
   `https://git.acme.internal/alice/own-release.git` as the repository, `main`
   as the ref, and `own-release` as the layer ID.

   **Expect.** The form states that a layer of your own is visible to you alone
   and counts against the layer limit an administrator sets. The registration is
   stored user-defined with the signed-in caller as its owner and its visibility
   fixed to that subject, whatever visibility the request carried, which is why
   the form offers no visibility axes on this class.

   The form offers no layer-class control and no `Local folder` source option,
   because this caller holds no tenant-admin role and the registry would refuse
   both. A class control present here means the panel is predicting from
   something other than the posture read, and a `Local folder` option present
   here offers a registration the registry answers with `auth.forbidden`. A
   filesystem path given as the repository is refused for the same reason,
   because a repository string that resolves to the Git file transport names a
   path on the registry host.

3. Read what the panel returns.

   **Expect.** The reveal carries a "Shown once" label, the layer's webhook URL
   and its HMAC secret with a copy control on each, and a Done control that
   stays disabled until the reader ticks the acknowledgement. The reveal holds
   the screen until then, so a secret is not lost to an accidental navigation.
   Tick the acknowledgement, press Done, and confirm the panel returns to the
   layer list. Whether the secret is served again is a property of the API,
   which step 4 probes.

4. Confirm the layer is listed as this caller's and that the secret is not
   served again.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" \
     -w '\nstatus=%{http_code}\n' | grep -i -e '"id"' -e secret -e '"code"' -e status
   curl -sS "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" \
     | grep -o -e '"id":' -e '"[A-Z][A-Za-z]*":' -e '[Tt]enant' \
     | sort | uniq -c
   ```

   **Expect.** The first command lists `own-release`, and no line carries a
   secret value. The second command prints one line, `3 "id":`, counting one
   `id` member per layer this caller reads: `public-handbook`, `private-comp`,
   and `own-release`. `comp-readers-policy` is absent because it is visible only
   through `groups: [comp-readers]` and `$TOKEN` carries no `groups` claim: the
   `groups` client scope is optional, and prerequisite 5 mints this token with a
   password grant that requests no scope. That count is the evidence the
   pipeline read the layer list rather than an error envelope or an empty body,
   either of which prints nothing at all. No `"Xxx":` line and no `tenant` line
   appears,
   because §7.2.1 fixes the control plane on lower snake_case names and bars a
   layer record from restating the tenant it was read under. The first command
   carries `-i`, so its `'"id"'` pattern matches any casing and reports nothing
   about the spelling; the second command is what reads the spelling.
   The secret is returned on registration and on a rotation and is redacted from
   every other response, so once the reveal is dismissed it cannot be read back.
   In the panel the row carries the "yours" marker, which the panel draws by
   comparing the layer's stored owner against the subject the posture read
   reports. The list read answers the layers the caller may read, so the same
   request without the header returns `{"layers": []}`: this stack configures
   `oidc-jwt` and seeds no admin grant, so an unauthenticated caller is neither
   an admin nor an authenticated subject and reads no layers at all. The marker
   remains a rendering of ownership over the rows the caller can see. A `401`
   carrying `auth.token_expired` here means `$TOKEN`, minted in the
   prerequisite, has passed its Keycloak lifetime rather than that the read
   narrowed. Re-mint it with the prerequisite's command and re-run the step. The
   two outcomes are distinguishable from the terminal without inspecting the
   token, because the command prints the status line beside the filtered body: a
   refused read prints `status=401` and the envelope's `"code"` line, and a read
   that answered prints `status=200` and the layer lines it carries.

5. **Negative control.** Confirm the write needed the session. Issue the same
   registration from the terminal, which carries no cookie.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/layers" \
     -H 'Content-Type: application/json' \
     -d '{"id":"own-release-2","source_type":"git","repo":"https://git.acme.internal/alice/own-release.git","ref":"main","user_defined":true}' \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403 rather than a second registered
   layer. A user-defined layer's owner is derived from the authenticated
   registrant, and this request resolves no subject. A `200` here means the
   panel's registration in step 2 established nothing about the session.

**Cleanup.** As S44, on the same terms: leave the stack running when S49 or S50
follows, and run S44's teardown only when this is the last scenario executed.

---

## S49: Unregister a layer through the panel's confirmation

**Goal.** Validate that the panel's unregister control states what unregistering
does before it does it, that it holds the write until the reader performs a
deliberate act distinct from the press on the row, and that the unregistered
layer moves into the recoverable list.

**Covers.** §7.3.1 unregister through the UI, the §13.10 layer panel's
confirmation for a write whose effect reaches other callers, and the §8.4
recovery window.

**Why by hand.** The assertion is that a destructive action cannot be taken by a
single press and that the reader is told both halves of what it does. Both are
properties of a rendering.

**Prerequisites.** The S44 stack with the signed-in session and the
`own-release` layer S48 registered.

**Steps.**

1. In the layer panel, press Unregister on the `own-release` row.

   **Expect.** A confirmation appears rather than the layer disappearing, and no
   write is issued by this press. The confirmation states both halves of what
   unregistering does: it names the layer, says its artifacts disappear from
   every caller's view the next time they sync, and says the layer stays
   recoverable for the deployment's retention window, naming the date it is
   erased on, rather than being erased now.

2. Attempt to complete the unregister without performing the confirmation's own
   act.

   **Expect.** The confirmation carries a "Type the layer ID to confirm" field,
   and the "Unregister layer" control stays disabled until the typed value
   equals the layer ID. No write is issued before that, so the action cannot be
   taken by a second press on the row.

3. Type `own-release` into the confirmation field and press Unregister layer.

   **Expect.** The write is issued, the row leaves the layer list, and the layer
   appears under the recently-unregistered list the panel draws from the
   tenant's tombstoned layers, where it stays until the retention window lapses.
   Cancelling instead leaves the layer registered and issues no write.

4. Confirm the registry agrees with the panel.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" \
     -w '\nstatus=%{http_code}\n' | grep -e own-release -e '"code"' -e status || true
   ```

   **Expect.** `status=200` with no `own-release` line, read under the owner's
   own credential. The layer is restorable from the panel's recovery control
   until the §8.4 window runs out, which is what makes this an unregister rather
   than an erasure. As in S48, `status=401` beside a `"code"` line carrying
   `auth.token_expired` means the token minted in the prerequisite has expired;
   re-mint it and re-run the step, because a listing read from a refused request
   states nothing about the tombstone.

**Cleanup.** As S44, on the same terms: leave the stack running when S50 follows,
and run S44's teardown only when this is the last scenario executed.

---

## S50: A non-owner is refused on a destructive operation

**Goal.** Validate that the registry refuses a layer write from a signed-in
caller who neither owns the layer nor is a tenant admin, that the panel offers
no write control on a row that caller cannot write, and that a layer outside the
caller's view is absent from that caller's list and still refused when named
directly.

The refusal band on a row it was attempted from is validated by S57, which
presses a control the caller was offered and then lost.

**Covers.** The §7.3.1 layer read visibility rule, the §7.3.1 layer-write
authorization rule, `auth.forbidden`, and the §13.10 rule that the panel offers
a layer operation only where the caller may take it.

**Why by hand.** The assertion is that a second person, signed in through the
same UI, is offered no write control on a row that person can see, is refused
that same write when it is named directly from the terminal, and reads a list
that omits the layer that person cannot see. The absent control is the part no
Go test reads, and the panel presenting per-owner scoping as server-enforced
while the server failed open is the defect this closes. The rendering of a
refusal the panel does receive is read by S57.

**Prerequisites.** The S44 stack, and a layer owned by the `admin` user. Run S48
to register `own-release` and stop before S49, or re-register it. A second
browser profile, or a second browser, that has never signed in to this stack.
`__Host-podium_session` is one cookie per origin per browser profile and
Keycloak's own SSO cookie is per profile as well, so a second window of the
profile S47 signed in from cannot hold a second session.

**Steps.**

1. Create a second realm user.

   ```bash
   $KC create users -r master -s username=bob -s enabled=true
   $KC set-password -r master --username bob --new-password bob
   ```

   Mint bob's own token the way prerequisite 5 mints `TOKEN`, and read bob's
   `sub` out of it, so step 2 has a value to compare the posture body against
   and step 4 has a credential to issue its request with.

   ```bash
   export BOB_TOKEN="$(curl -fsS -X POST "$ISSUER/protocol/openid-connect/token" \
     -d grant_type=password -d client_id=podium -d client_secret="$KC_SECRET" \
     -d username=bob -d password=bob \
     | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")"
   export BOB_SUBJECT="$(python3 -c "import base64,json,os; p=os.environ['BOB_TOKEN'].split('.')[1]; p+='='*(-len(p)%4); print(json.loads(base64.urlsafe_b64decode(p))['sub'])")"
   echo "$BOB_SUBJECT"
   ```

   **Expect.** A `sub` value that differs from `$SUBJECT`, the admin `sub` S44
   exported. The subject claim is `sub` by default, so neither value is a login
   name and the two are distinguished by comparison rather than by reading a
   username out of them.

2. Open `http://127.0.0.1:8153/app/` in the second browser profile, sign in as
   `bob` with the password `bob`, and open the layer panel. Do not use a private
   window of the profile S47 signed in from: every window of a profile shares
   one cookie jar, so bob's callback would replace the admin session rather than
   establish a second one, and Keycloak's SSO cookie on that profile would
   return the sign-in without a prompt and mint a token for `admin`.

   Confirm the session this window holds is bob's before pressing anything
   destructive. Read the posture route in that same window, from the address bar
   or from the network panel.

   ```text
   http://127.0.0.1:8153/v1/ui/session
   ```

   **Expect.** The body reports `subject` equal to `$BOB_SUBJECT`, which differs
   from `$SUBJECT`. A body whose `subject` equals `$SUBJECT` means the sign-in
   reused the admin profile's SSO session, and every later step in this scenario
   would then exercise the owner arm of the rule rather than the non-owner arm
   it exists to test. The panel lists `public-handbook` alone. `private-comp`
   and `comp-readers-policy` are absent because bob's subject is in neither the
   `users:` list nor the `comp-readers` group, and `own-release` is absent
   because a user-defined layer is visible to its registrant alone.
   `public-handbook` is present because bob is authenticated and it is public. A
   panel listing more than `public-handbook` means the list read is still
   unfiltered, which is the failure this step now catches; an empty panel means
   bob's sign-in did not resolve a subject, and step 1 is re-run before
   continuing. A panel showing its refusal band with `auth.token_expired` or
   `auth.untrusted_token` instead of a row list means bob's session cookie no
   longer verifies, which is a refusal rather than a narrowing. Sign bob in
   again and re-run the step.

3. Read the `public-handbook` row in bob's panel.

   **Expect.** The row is listed, because bob's §4.6 view admits it, and it
   carries no write control at all: no Edit, no Unregister, no reingest control,
   and no overflow trigger. The actions cell is empty and holds the same width
   as a row that carries controls, so the list does not reflow. An Unregister
   control present here means the panel is still offering every write on every
   row, which is what this scenario now exists to catch.

4. Confirm the registry refuses the same operation when it is named directly, so
   the absent control is a prediction of the server rule rather than a
   substitute for it.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/layers?id=public-handbook" \
     -H "Authorization: Bearer $BOB_TOKEN" \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403. A `200` means the server let a
   non-owner delete another caller's layer, which is the failure this scenario
   exists to catch. The panel hiding the control and the registry refusing the
   request are two independent statements, and this step is the second one.

5. Confirm the layer bob cannot see is refused when it is named directly, using
   the token step 1 exported.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/layers?id=own-release" \
     -H "Authorization: Bearer $BOB_TOKEN" \
     -w '\nstatus=%{http_code}\n'
   ```

   The layer ID travels in the `id` query parameter, which is where the handler
   reads it. A request without it answers `400 registry.invalid_argument`
   before any authorization runs, and the handler reads no request body.

   **Expect.** `auth.forbidden` at HTTP 403. A `200` means the server let a
   non-owner delete another caller's layer, which is the failure this scenario
   exists to catch. The refusal names neither the owner nor the state of the
   session, because it carries neither, and it is the same refusal a caller who
   can see the layer receives, so the narrowed list discloses nothing further
   about it.

6. Read the list with a credential the registry cannot verify, then with none at
   all.

   ```bash
   curl -sS "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer not-a-token" \
     -w '\nstatus=%{http_code}\n'
   curl -sS "http://127.0.0.1:8153/v1/layers" -w '\nstatus=%{http_code}\n'
   ```

   Then, in bob's browser window, open DevTools, Application, Cookies, replace
   the value of the `__Host-podium_session` cookie with `not-a-token`, and
   reload the layers route.

   **Expect.** The first request answers `status=401` with code
   `auth.untrusted_token`. The second answers `status=200` with
   `{"layers": []}`. Those two outcomes are the rule this scenario turns on: a
   credential the registry fails to verify is refused, and a request the
   provider resolves as anonymous is answered with an empty list, which is what
   keeps the signed-out panel working on this `oidc-jwt` stack. A `200` on the
   first request means the layer endpoint is still resolving an unverifiable
   credential to the anonymous caller, which is the fail-open this scenario
   exists to catch. A `401` on the second means the refusal is keying on the
   resolved identity rather than on the verifier's error, which would sign every
   visitor out of the public panel.

   In the browser the panel renders its refusal band where it previously
   rendered bob's row list. The band carries `auth.untrusted_token`, the
   envelope's suggested action, and the line "Retrying does not clear this
   condition." in place of a retry, because the registry marks that code
   non-retryable and the band's retry is gated on that flag. Above it the shell
   renders its own refused-read banner, "The registry served no catalog for this
   request.", with a Try again control, because the shell's catalog read takes
   the same refusal from the same cookie. That pairing is the part no Go test
   reads. A panel showing an empty row list instead means the shell swallowed
   the refusal and is telling bob the tenant holds no layers he can see. Restore
   the session by signing bob in again before continuing.

7. Confirm the owner is still authorized. Return to the window S47 signed in
   from, whose session step 2 left untouched because bob signed in from a
   separate profile, and press Unregister on the `own-release` row.

   **Expect.** The confirmation appears and the write succeeds. Without this
   step a registry refusing every caller would pass step 3 for the wrong reason.

**Cleanup.** S50 is the last scenario on this stack, so run S44's teardown here:
stop the server by its recorded PID, run `rm -rf "$WORK"`, and remove the IdP
with `docker rm -f kc-podium` and `rm -rf "$KCERT"`.

---

## S51: Browse the domain hierarchy through the web UI

**Goal.** Validate that the web UI's domain browser renders the structure
`load_domain` returns: the root's subdomains, a nested domain, a domain large
enough to change how it is presented, and a sparse chain the registry
compresses into a single node.

**Covers.** The §13.10 domain-browser surface, §4.5.5 domain rendering
including passthrough-chain folding, and the §13.10 statement that the UI is a
thin client over the same endpoints the CLI calls.

**Why by hand.** The assertion is what a person sees in the tree and on the
page. No Go test reads a browser rendering, and the UI's structure is derived
from `load_domain` rather than served by it, so a divergence between the two is
invisible to a test of either one alone.

**Prerequisites.** None beyond the build. This is the stack S52, S53, and S54
run on, so a change here reaches each of them.

Run the isolation block from "How to use this document", then build the
registry. The corpus is shaped to exercise the browser: `eng/teams` holds
enough subdomains to change the presentation, and
`ops/regional/emea/payables` is a chain of single-child domains.

```bash
mk() { podium artifact scaffold --type "$1" --description "$2" --force "$WORK/reg/$3" >/dev/null; }
mkdir -p "$WORK/reg"
mk context "How a production deploy runs, stage by stage, and when to roll back." eng/platform/deploy-runbook
mk context "The deploy runbook with the canary stage made mandatory for tier-1 services." eng/platform/deploy-runbook-strict
mk command "Roll a service back to its previous released image." eng/platform/rollback
mk rule "Secrets never live in an artifact body, a manifest, or a bundled file." eng/security/secrets-policy
mk skill "Pay an approved vendor invoice through the finance warehouse." finance/ap/pay-invoice
mk agent "Reconcile a supplier ledger entry against the general ledger." ops/regional/emea/payables/reconcile
for t in ads api billing checkout content core data growth identity infra loyalty media mobile notify payments pricing reporting risk search shipping storefront support tax trust; do
  mk context "How the $t team runs its on-call rotation and what it pages for." "eng/teams/$t/oncall-guide"
done
```

Give the runbook a body that carries a table, a fenced block, and a reference to
another artifact, and bundle a file beside it. S53 reads this artifact.

````bash
cat > "$WORK/reg/eng/platform/deploy-runbook/ARTIFACT.md" <<'YAML'
---
type: context
name: deploy-runbook
version: 2.3.0
description: How a production deploy runs, stage by stage, and when to roll back.
tags: [deploy, platform, runbook]
sensitivity: low
---

# Deploy runbook

The production deploy runs in stages, and each stage gates the next.

| Step | Owner | Duration |
|:--|:--|--:|
| Build and sign the image | CI | 6 min |
| Canary to 5% of traffic | platform | 15 min |
| Full rollout | platform | 8 min |

Start the deploy from the release branch:

```bash
./scripts/deploy.sh --env production --canary 5
```

Roll back with [the rollback command](eng/platform/rollback) rather than
reverting the branch.
YAML
mkdir -p "$WORK/reg/eng/platform/deploy-runbook/scripts"
printf '#!/usr/bin/env bash\necho deploying\n' > "$WORK/reg/eng/platform/deploy-runbook/scripts/deploy.sh"
````

Give the second runbook an `extends:` on the first, and declare `tags` that omit
one tag the parent carries. S52 and S53 both read the difference.

```bash
cat > "$WORK/reg/eng/platform/deploy-runbook-strict/ARTIFACT.md" <<'YAML'
---
type: context
name: deploy-runbook-strict
version: 1.0.0
description: The deploy runbook with the canary stage made mandatory for tier-1 services.
extends: eng/platform/deploy-runbook
tags: [deploy, platform]
sensitivity: low
---

# Strict deploy runbook

Tier-1 services may not skip the canary stage.
YAML
cat > "$WORK/reg/finance/ap/pay-invoice/ARTIFACT.md" <<'YAML'
---
type: skill
version: 1.2.0
tags: [finance, ap, payments]
sensitivity: medium
---

<!-- Skill body lives in SKILL.md. -->
YAML
cat > "$WORK/reg/finance/ap/pay-invoice/SKILL.md" <<'YAML'
---
name: pay-invoice
description: Pay an approved vendor invoice through the finance warehouse.
license: MIT
---

Confirm AP approval, validate the vendor, then submit the payment.
YAML
podium lint --registry "$WORK/reg" --offline
```

**Expect.** `lint: no issues.` A run that reports an error here has a malformed
manifest, and every later step reads a registry that does not hold what the
scenario assumes. The most common cause is the document's leading indentation
landing inside a here-document; the blocks above are deliberately unindented.

Start the registry with the UI mounted.

```bash
podium serve --standalone --web-ui --no-embeddings \
  --layer-path "$WORK/reg" --bind 127.0.0.1:8462 > "$WORK/srv.log" 2>&1 &
export SRV=$!
for i in $(seq 1 40); do curl -sf http://127.0.0.1:8462/healthz >/dev/null && break; sleep 0.5; done
server_alive "$SRV" "$WORK/srv.log"
grep 'ingested layer' "$WORK/srv.log"
```

**Expect.** `ingested layer reg from $WORK/reg (accepted=30, idempotent=0,
rejected=0, advisories=0)`. A non-zero `rejected` or `advisories` means the
corpus above did not land as written, and the counts every later step asserts
will not match.

**Steps.**

1. Read the root domain from the API, so the UI has something to be checked
   against rather than merely inspected.

   ```bash
   curl -sS "http://127.0.0.1:8462/v1/load_domain?path=" \
     | python3 -c 'import json,sys; d=json.load(sys.stdin); print([s["path"] for s in d["subdomains"]])'
   ```

   **Expect.** `['eng', 'finance/ap', 'ops/regional/emea/payables']`. The second
   and third entries are already compressed by the registry: `finance` and `ops`
   each hold a single child, so §4.5.5 folds the passthrough chain and the
   response names the deepest node of each chain rather than its head.

2. Open `http://127.0.0.1:8462/app/` and read the root page and the sidebar.

   **Expect.** The heading reads `All domains` with `30 ARTIFACTS` and
   `3 DOMAINS` beside it. Three subdomain cards stand under `SUBDOMAINS`, named
   for the three paths step 1 printed. The sidebar's `CATALOG` tree carries
   `eng` with `platform`, `security`, and `teams` beneath it, and carries the
   two compressed chains as `finance/ap` and `ops/regional/emea/payables`, each
   drawn with its trailing node emphasized over the path that leads to it. The
   footer reads `1 layer · 30 artifacts`. The card count and the tree must agree
   with step 1: a UI that renders `finance` and `ops` as separate expandable
   nodes is showing a hierarchy the registry did not return.

3. Open the `eng/teams` domain, which holds more subdomains than a domain page
   presents as cards.

   **Expect.** The heading reads `teams` with `24 ARTIFACTS` and
   `24 SUBDOMAINS`. The subdomain region carries a `Grid` and `List` control and
   a `Show all 24 subdomains` control rather than drawing every card at once,
   and the sidebar's `teams` node lists the first several children followed by
   `+ 16 more`. This is the presentation change a domain of this size is
   supposed to produce; a page that draws 24 cards with no such control is
   rendering the small-domain treatment at a size it was not drawn for.

4. Open the compressed chain from the sidebar by pressing
   `ops/regional/emea/payables`, and compare it against the API.

   ```bash
   curl -sS "http://127.0.0.1:8462/v1/load_domain?path=ops" \
     | python3 -c 'import json,sys; d=json.load(sys.stdin); print([s["path"] for s in d["subdomains"]])'
   ```

   **Expect.** The API prints `['ops/regional/emea/payables']`, a single entry,
   and the page the sidebar opened is the domain holding `reconcile`. Neither
   `regional` nor `emea` is reachable as a page of its own from the tree,
   because neither is a node the registry returned.

5. Confirm the browser reads the same endpoint the CLI reads, rather than a
   surface of its own.

   ```bash
   podium domain show --registry http://127.0.0.1:8462 eng/platform
   ```

   **Expect.** The CLI lists the same artifacts the `eng/platform` page shows,
   under `notable:`: `deploy-runbook`, `deploy-runbook-strict`, and `rollback`. A disagreement
   between the two means the UI is deriving its view from something other than
   `load_domain`, which §13.10 does not permit.

**Cleanup.** Leave the server running for S52, S53, and S54. S54 carries the
teardown.

---

## S52: Search through the web UI with the type, scope, and tag filters

**Goal.** Validate that the UI's search offers the same `type`, `scope`, and
`tags` filters the SDK and CLI offer, that each filter changes the result set
the way the CLI's does, and that the address the page writes names the search it
ran.

**Covers.** The §13.10 search surface and its filter set, and the §13.10
statement that the UI is a thin client over the same endpoints.

**Why by hand.** The assertion is that two independent surfaces agree. A Go test
covers the endpoint; no test compares what the endpoint returns against what a
browser renders for the same query, which is where the filters are applied to a
reader.

**Prerequisites.** The S51 stack, left running.

**Steps.**

1. Take the CLI's answers for the queries the UI will run. These are the
   baseline every later step is checked against.

   ```bash
   R=http://127.0.0.1:8462
   ids() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["total_matched"], [x["id"] for x in d["results"]])'; }
   podium search --registry $R --json "deploy"                    | ids
   podium search --registry $R --json --type command "deploy"     | ids
   podium search --registry $R --json --tags runbook "deploy"     | ids
   podium search --registry $R --json --tags nosuchtag "deploy"   | ids
   podium search --registry $R --json --scope finance "invoice"   | ids
   podium search --registry $R --json --scope eng "invoice"       | ids
   ```

   **Expect.** In order: `2` with both runbooks, `0`, `2` with both runbooks,
   `0`, `1` with `finance/ap/pay-invoice`, and `0`. Flags precede the positional
   query; a command written the other way round returns nothing and reads as an
   empty result set rather than as a usage error.

   The `--tags runbook` result is the one worth pausing on.
   `deploy-runbook-strict` declares `tags: [deploy, platform]` and no `runbook`
   tag, and it matches because it declares `extends:` and the merged manifest
   carries the parent's tags. The `--tags nosuchtag` line is the negative
   control for the same filter: without it, a filter that silently matched
   everything would produce the same reading as one that works.

2. Open `http://127.0.0.1:8462/app/`, press the search control in the top bar,
   and search for `deploy`.

   **Expect.** The page reports `Showing 2 of 2 matches` and lists both
   runbooks, matching the CLI's first line.

3. Apply the type filter on the search surface and set it to `command`.

   **Expect.** The count falls to `Showing 0 of 0 matches`, matching the CLI's
   second line, and the type control reads `command` rather than `all`. Return
   it to `all` and the two matches come back.

4. Type the filter as an inline token instead of using the control. Clear the
   field, type `type:command deploy`, and press Return.

   **Expect.** The token is lifted out of the query and applied as the type
   filter: the type control reads `command`, the field holds `deploy` alone, and
   the count is `Showing 0 of 0 matches`. The literal string `type:command` must
   not remain in the field and must not be searched as keyword text.

5. Reload the page on the address the previous step produced.

   **Expect.** The same filter, the same field contents, and the same count. The
   address the page wrote and the search the page ran name one search, so
   arriving at that address by reload renders what typing produced. A page that
   shows a different result set after a reload has written an address that does
   not describe the search it performed.

6. Run the scope and tag cases against the UI and compare each against step 1.

   **Expect.** `scope:finance invoice` reports `Showing 1 of 1 match` for
   `finance/ap/pay-invoice`; `scope:eng invoice` reports `Showing 0 of 0
   matches`; `tag:runbook deploy` reports `Showing 2 of 2 matches`. Each line
   matches the CLI's answer for the same filter.

**Cleanup.** Leave the server running for S53 and S54.

---

## S53: Read an artifact through the viewer

**Goal.** Validate that the artifact viewer renders the body as markdown rather
than as source, presents the frontmatter as a property table, links an artifact
to the one it extends, lists bundled resources, and renders untrusted body
markup without executing it.

**Covers.** The §13.10 artifact-viewer surface, §4.3 manifest merging as it
reaches a reader, and the §13.10 rendering of body content the registry does not
control.

**Why by hand.** The assertion is what a person sees rendered, and, for the last
step, what a browser does not execute. No Go test reads a browser rendering or
observes a script that failed to run.

**Prerequisites.** The S51 stack, left running.

**Steps.**

1. Open `http://127.0.0.1:8462/app/#/artifact/eng%2Fplatform%2Fdeploy-runbook`.

   **Expect.** The heading reads `deploy-runbook` with its type and version
   beside it. The body renders as markdown: the table appears as a table with
   the column headings `Step`, `Owner`, and `Duration`, and the deploy command
   appears in a code block. Neither is shown as raw text with pipes and
   backticks, which is what a viewer that does not render markdown produces.
   Three tabs stand above the body: `Rendered`, `Frontmatter`, and
   `Resources 1`.

2. Follow the reference the body carries to another artifact.

   **Expect.** The words "the rollback command" are a link, and following it
   opens `eng/platform/rollback` inside the viewer. The reference is authored as
   the artifact ID `eng/platform/rollback`; a link that leaves the page for a
   registry error rather than opening the artifact has not been resolved to a
   route.

3. Open the `Resources 1` tab.

   **Expect.** The bundled `scripts/deploy.sh` is listed with its size, and a
   download control sits beside it. The count in the tab label matches the one
   file bundled beside the manifest.

4. Open `#/artifact/eng%2Fplatform%2Fdeploy-runbook-strict` and read its
   `Frontmatter` tab against what the file on disk declares.

   ```bash
   grep '^tags:' "$WORK/reg/eng/platform/deploy-runbook-strict/ARTIFACT.md"
   ```

   **Expect.** The file declares `tags: [deploy, platform]`. The property table
   shows `deploy`, `platform`, and `runbook`, because the artifact declares
   `extends:` and the viewer presents the merged manifest. The page also carries
   a link to `eng/platform/deploy-runbook`, the artifact this one extends. The
   inherited tag is the visible evidence that the merge reached the reader; a
   table showing only the two declared tags is presenting the authored manifest
   where the merged one is specified.

5. Add an artifact whose body carries markup a reader must never execute, and
   ingest it.

   ```bash
   podium artifact scaffold --type context \
     --description "A probe artifact whose body carries markup a reader must never execute." \
     --force "$WORK/reg/eng/security/render-probe" >/dev/null
   cat > "$WORK/reg/eng/security/render-probe/ARTIFACT.md" <<'YAML'
---
type: context
name: render-probe
version: 1.0.0
description: A probe artifact whose body carries markup a reader must never execute.
sensitivity: low
---

# Render probe

<script>window.__PWNED = true;</script>

<img src=x onerror="window.__PWNED = true">

<a href="javascript:window.__PWNED=true">a javascript url</a>

<iframe src="https://example.com"></iframe>
YAML
   podium lint --registry "$WORK/reg" --offline
   podium layer reingest --registry http://127.0.0.1:8462 reg | tail -1
   ```

   **Expect.** `lint: no issues.`, and the reingest accepts the new artifact.
   The `javascript:` URL is written as raw HTML rather than as a markdown link
   on purpose: authored as `[a javascript url](javascript:...)` lint refuses the
   artifact with `lint.prose_reference` and it never reaches the renderer, which
   would leave this step testing lint rather than the viewer.

6. Open `#/artifact/eng%2Fsecurity%2Frender-probe` and read the rendered body.

   **Expect.** The page shows the heading and three removal markers reading
   `(image removed)`, `(link removed)`, and `(embed removed)`. The link's text
   is still visible and the link is inert. Nothing renders an image, an inline
   frame, or the script's source text.

7. Confirm from the browser console that nothing in that body executed.

   ```javascript
   window.__PWNED
   ```

   **Expect.** `undefined`. Every one of the four vectors sets `window.__PWNED`
   if it runs, so a defined value names a renderer that executed author-supplied
   markup. This is the negative control the rendered page alone cannot give:
   a body whose script silently failed for an unrelated reason would look
   identical on screen.

**Cleanup.** Leave the server running for S54.

---

## S54: Author on disk, reingest from the panel, and materialize

**Goal.** Validate the loop an author actually runs: write an artifact into a
`local` layer, bring it into the catalog from the web UI's layer panel, read it
in the UI, and materialize it into a harness with `podium sync`.

**Covers.** The §13.10 layer panel's reingest operation and its report, §7.3.1
`local`-layer reingest and the immutability rule, and §7.5 filesystem delivery.

**Why by hand.** The assertion spans a filesystem edit, a browser control, and a
CLI materialization. Each half is covered by a test; nothing exercises the loop
a person runs across all three.

**Prerequisites.** The S51 stack, left running, with S53's `render-probe`
ingested.

**Steps.**

1. Confirm the artifact this scenario adds is not in the catalog yet.

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' \
     "http://127.0.0.1:8462/v1/load_artifact?id=eng/platform/freeze"
   ```

   **Expect.** `404`. Without this baseline the later read establishes nothing.

2. Write the artifact into the layer's directory. Do not reingest from the
   terminal; the panel does it in the next step.

   ```bash
   podium artifact scaffold --type command \
     --description "Freeze deploys for the duration of an incident." \
     --force "$WORK/reg/eng/platform/freeze"
   ```

   **Expect.** The scaffold writes `ARTIFACT.md`. The registry still answers
   `404` for it, because writing to a `local` layer's directory changes nothing
   in the registry until an ingest runs.

3. Open `http://127.0.0.1:8462/app/#/layers` and press `Reingest` on the `reg`
   row.

   **Expect.** The row reports the request running, and a report opens when it
   returns. The report reads `1 ACCEPTED`, `31 UNCHANGED`, `0 REJECTED`,
   `0 CONFLICTS`, and `0 LINT FAILURES`. The accepted count is the artifact step
   2 wrote, and the unchanged count is every artifact whose content the registry
   already holds at that version: the 30 the stack started with plus the probe
   S53 added. Running this scenario without S53 first gives `30 UNCHANGED`.

4. Read the new artifact in the UI.

   **Expect.** `eng/platform/freeze` is reachable from the `eng/platform`
   domain page and opens in the viewer. The catalog counter in the footer has
   risen by one.

5. Edit the artifact without bumping its version, and reingest from the panel
   again. This is the failure an author hits most often.

   ```bash
   sed -i '' 's/^description: Freeze deploys.*/description: Freeze every deploy for the duration of an incident./' \
     "$WORK/reg/eng/platform/freeze/ARTIFACT.md"
   ```

   **Expect.** The report reads `0 ACCEPTED` and `1 CONFLICTS`, and its
   `NEEDS ATTENTION` region states that a published version was republished with
   different content and directs the author to bump the version. The registry
   refuses the change rather than overwriting the stored bytes, which is the
   §7.3.1 immutability rule reaching a reader. The artifact keeps the
   description step 2 gave it.

6. Bump the version and reingest from the panel once more.

   ```bash
   sed -i '' 's/^version: 0.1.0/version: 0.2.0/' \
     "$WORK/reg/eng/platform/freeze/ARTIFACT.md"
   ```

   **Expect.** The report reads `1 ACCEPTED` and `0 CONFLICTS`, and the viewer
   shows the new description at version `0.2.0`.

7. Materialize the catalog into a harness and confirm the new artifact travels.

   ```bash
   mkdir -p "$WORK/proj" && cd "$WORK/proj"
   podium init --registry http://127.0.0.1:8462 --harness claude-code
   podium sync | grep -A1 'eng/platform/freeze'
   ```

   **Expect.** The sync output names `eng/platform/freeze  [reg]` with the path
   it wrote beneath it. The artifact an author wrote to a directory in step 2 has
   reached a harness-native file without any step outside this loop.

**Cleanup.** S54 is the last scenario on this stack.

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
rm -rf "$WORK"
```

---

## S55: The register dialog offers only what the caller can take

**Goal.** Validate that the register dialog offers no layer class and no source
the registry will refuse for this caller, and that the registry refuses the same
registration when it is named directly.

**Covers.** §7.3.1 local-source authorization, §7.3.1 admin-only registration
fields, §7.3.4 `layer_capabilities`, and the §13.10 layer panel.

**Why by hand.** The wrong output is a dialog offering a class and a source the
registry then refuses, which reads to the operator as a product that lost their
input, and only a human reading the dialog sees that.

**Prerequisites.** The S44 stack, re-stood by running S44's Prerequisites and
steps 1 to 6, with a signed-in session per S47 steps 1 to 3. `TOKEN` and
`SUBJECT` are the values S44's Prerequisites and step 1 export, and they belong
to the signed-in caller, who holds no tenant-admin grant on this stack. When
Keycloak or the `mkcert` CA is unavailable, skip and record the skip.

**Steps.**

1. Open the layer panel as the signed-in caller and press `Register layer`. Read
   the class control and the source control.

   **Expect.** The dialog carries no layer-class control and its source control
   offers no `Local folder` option. A class control present here means the form
   is predicting from something other than the posture read. A `Local folder`
   option present here offers a registration step 2 shows the registry refusing.

2. Register a `local`-source layer from the terminal with the same caller's
   token.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"id":"s55-local","source_type":"local","local_path":"/etc","user_defined":true}' \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403, with `"constraint": "local_source"`
   in the envelope's `details` and no filesystem path anywhere in the body. A
   `201` means the local-source rule is not wired on `register`, which is the
   defect this change closes.

3. Register a public layer from the terminal with the same caller's token.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"id":"s55-public","source_type":"git","repo":"https://git.acme.internal/alice/handbook.git","ref":"main","user_defined":true,"public":true}' \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403, with
   `"constraint": "admin_only_fields"` in the envelope's `details` and a message
   naming `public`. A `201` means the registry discarded the assertion and
   registered a layer the caller asked to be public as one visible to the
   registrant alone, which reports success on a registration that applied none
   of what was asked. Re-sending the same body without the `public` field
   answers `201`, which is what keeps the refusal scoped to the asserted field
   rather than to the registration.

**Cleanup.** Leave the stack running when S56 or S57 follows, and run S44's
teardown only when this is the last scenario executed.

---

## S56: The panel presents per-row only what the caller may take

**Goal.** Validate that the panel renders a layer operation only where the
caller may take it, and that a caller who holds the tenant-admin role gets every
control back, so a registry that hid every control from everyone does not pass.

**Covers.** §7.3.1 layer write authorization and local-source authorization,
§7.3.4 `layer_capabilities`, and the §13.10 layer panel.

**Why by hand.** The row actions are a rendering, and the failure this catches
is a control drawn for a caller who can never take it or withheld from a caller
who can.

**Prerequisites.** S55's prerequisite, with S44's bootstrap-admin note applied:
`carol` exists, `CAROL_TOKEN` and `CAROL_SUBJECT` are exported, and the registry
was started with `PODIUM_BOOTSTRAP_ADMINS="$CAROL_SUBJECT"`. Carol never signs
in to the UI; she exists so the first grant has an issuer.

**Steps.**

1. Grant the tenant-admin role to the signed-in caller, using carol's bootstrap
   token, and register a user-defined local layer as that caller while the grant
   is in force.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/admin/grants" \
     -H "Authorization: Bearer $CAROL_TOKEN" -H 'Content-Type: application/json' \
     -d "{\"user_id\":\"$SUBJECT\"}" -w '\nstatus=%{http_code}\n'
   mkdir -p "$WORK/notes" && printf -- '---\nid: note\ntype: context\n---\nnote\n' \
     > "$WORK/notes/ARTIFACT.md"
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 \
     --id s56-notes --local "$WORK/notes" --user-defined
   ```

   **Expect.** The grant answers `201` with `{"user_id": "<the subject>"}`, and
   the registration exits `0` with the stored record printed, carrying
   `"user_defined": true` and this caller's subject as `owner`. A `403` on the
   grant means `PODIUM_BOOTSTRAP_ADMINS` did not name carol's `sub`, and the
   prerequisite is re-run before continuing. A `403` on the registration means
   the grant did not take effect, because a non-admin may not name a filesystem
   path.

   The grant body carries `user_id` alone, and the grant lands in the caller's
   own tenant. `register` derives its source type from `--local` and declares no
   `--source` flag, and its flag set parses with `flag.ContinueOnError`, so an
   unknown flag exits non-zero before any request. `PODIUM_SESSION_TOKEN` is the
   credential the CLI attaches, so it acts as the same caller the browser
   session holds; S44's Prerequisites unset that variable, so exporting it on
   the command line is what makes the invocation authenticated at all.

2. Revoke the grant, reload the panel, and read the `s56-notes` row and the
   `public-handbook` row.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/admin/grants?user_id=$SUBJECT" \
     -H "Authorization: Bearer $CAROL_TOKEN" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** The revoke answers `204`. `s56-notes` carries `Edit` and
   `Unregister` behind its overflow control, because the caller owns it, and
   carries no reingest control, because it names a filesystem path and this
   caller is no longer a tenant admin. `public-handbook` is admin-defined, so it
   carries no write control at all and an empty actions cell of the same width.
   The drag handle on `public-handbook` is present, disabled, not draggable, and
   its accessible name states that the caller cannot reorder that set rather
   than instructing the reader to press an arrow key, while the handle on
   `s56-notes` stays live, because a move from it names the user-defined block,
   which holds this caller's own layers alone. A disabled handle on `s56-notes`
   means the client is reading the reorder predicate over the whole visible list
   rather than over the block the request names. A reingest control on
   `s56-notes` means the client is predicting at `unregister` or `reorder`
   rather than at `reingest`, so it is predicting the write arm alone where the
   server also applies the local-source rule.

3. Re-grant the role with the same command as step 1's first block, reload, and
   read the same two rows.

   **Expect.** Both rows carry every write control, the reingest control is back
   on `s56-notes`, and the drag handle is live on both rows, including
   `public-handbook`, which the admin arm now lets this caller write. Without
   this step a registry that hid every control from everyone would pass step 2
   for the wrong reason.

**Cleanup.** Leave the stack running when S57 follows, and otherwise run this
scenario group's teardown, which S57 records.

---

## S57: A stale prediction still refuses, and the panel says so

**Goal.** Validate that the panel treats its prediction as a prediction: an
operation it offered and the registry then refuses draws the refusal on the row
rather than reading as a failure of the page.

**Covers.** §7.3.4 `layer_capabilities` snapshot semantics, the §13.10 layer
panel, and §6.10 `auth.forbidden`.

**Why by hand.** Only a human sees whether the page keeps working around the
refused row.

**Prerequisites.** S56, run through step 3, so the tenant-admin grant is in
place and the panel is loaded under it.

**Steps.**

1. With the tenant-admin grant in place from S56 step 3, open the layer panel
   and leave it loaded without reloading it.

   **Expect.** `public-handbook` carries its full set of write controls.

2. Revoke the grant out of band, leaving the page loaded.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/admin/grants?user_id=$SUBJECT" \
     -H "Authorization: Bearer $CAROL_TOKEN" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `204`. The loaded page does not change, because it holds the
   posture read it took before the revocation.

3. Press `Unregister` on the `public-handbook` row from the still-loaded page
   and confirm the dialog.

   **Expect.** The write is refused, the row draws the refusal band naming
   `auth.forbidden`, the band offers Dismiss alone and reads
   `Retrying does not clear this condition.`, and the rest of the page keeps
   working. A page that blanks, signs the caller out, or reports a transport
   failure means the client is reading a refusal as a failure of the page rather
   than as the registry's answer.

**Teardown.** Revoke any remaining grant, delete `s56-notes`, and run S44's
teardown: stop the server by its recorded PID, run `rm -rf "$WORK"`, and remove
the IdP with `docker rm -f kc-podium` and `rm -rf "$KCERT"`.

---

## S58: Every layer response reads in snake_case

**Goal.** Validate that the layer endpoints emit the §7.2.1 control-plane
convention: every member name is lower snake_case, no response restates the
tenant the layer was read under, and the layer's HMAC secret appears in the
registration envelope alone. Validate that `git_provider` is settable over HTTP
and refused when it names no registered provider, and that the layer panel
still draws every column from the members the server emits.

**Covers.** §7.2.1 control-plane JSON conventions, §7.3.1 the layer object and
git-provider selection, and the §13.10 layer panel.

**Why by hand.** The Go suite pins each response's key set, and the panel is
where a divergence stops being visible: a cell that reads a member the server
does not emit renders permanently empty rather than failing, so no API-level
reading reports it. The `git_provider` write has no CLI flag, so the HTTP body
is the only path a person exercises.

**Prerequisites.** None beyond the build.

Run the isolation block from "How to use this document", then build a
one-artifact local layer and start the registry with the UI mounted.

```bash
mkdir -p "$WORK/reg"
podium artifact scaffold --type skill --description "Roll a release" "$WORK/reg/release"
podium serve --standalone --web-ui --no-embeddings \
  --layer-path "$WORK/reg" --bind 127.0.0.1:8464 > "$WORK/srv.log" 2>&1 &
export SRV=$!
for i in $(seq 1 40); do curl -sf http://127.0.0.1:8464/healthz >/dev/null && break; sleep 0.5; done
server_alive "$SRV" "$WORK/srv.log"
export PODIUM_REGISTRY=http://127.0.0.1:8464
grep 'ingested layer' "$WORK/srv.log"
```

**Expect.** `ingested layer reg from $WORK/reg (accepted=1, idempotent=0,
rejected=0, advisories=1)`. The advisory is `lint.thin_description`: the
description above is 14 characters, one short of the 15-character threshold.
Keep it as written, because a longer description reports `advisories=0`.

Define the reader every step below pipes a response into. It prints the member
names at each level of the document, the `id` and `deleted_at` value of each
layer record the document carries, the member names that are not lower
snake_case, how many times the string `tenant` appears in any casing, and
whether the document carries a secret. The record line is what makes the
ordering and the tombstone stamp readable, because the member-name walk reports
names alone. The block is unindented on purpose: the document's leading spaces
would land inside the Python program.

```bash
inspect() {
  python3 -c '
import json, re, sys
raw = sys.stdin.read()
doc = json.loads(raw)
def walk(node, path):
    if isinstance(node, dict):
        print(path, sorted(node))
        for key, value in node.items():
            walk(value, path + "." + key)
    elif isinstance(node, list) and node:
        walk(node[0], path + "[]")
walk(doc, "response")
records = next((v for v in doc.values() if isinstance(v, list) and v and isinstance(v[0], dict)), None)
if records is None and isinstance(doc.get("layer"), dict):
    records = [doc["layer"]]
if records:
    print("records:", [(r.get("id"), r.get("deleted_at")) for r in records])
print("go-cased keys:", sorted({k for k in re.findall(r"\"([A-Za-z_]+)\":", raw) if k != k.lower()}))
print("tenant mentions:", len(re.findall(r"(?i)tenant", raw)))
print("carries a secret value:", "webhook_secret" in doc)
'
}
```

**Steps.**

1. Register a git-source layer and read the registration envelope. The
   repository is never fetched, because registration validates the request and
   no step here reingests the layer.

   ```bash
   podium layer register --id own-release \
     --repo https://git.acme.internal/alice/own-release.git --ref main | inspect
   ```

   **Expect.** The envelope carries `['layer', 'webhook_secret',
   'webhook_url']`, and `response.layer` carries `['created_at', 'deleted_at',
   'git_provider', 'groups', 'id', 'last_ingested_ref', 'local_path', 'order',
   'organization', 'owner', 'public', 'ref', 'repo', 'root', 'source_type',
   'user_defined', 'users']`. `records: [('own-release', None)]`,
   `go-cased keys: []`, `tenant mentions: 0`, and
   `carries a secret value: True`. A member spelled `ID`, `Order`, or
   `UserDefined` here is the §7.2.1 violation this scenario exists to catch,
   and a `tenant` mention is the disclosure §7.2.1 bars: the layer's subject is
   the layer, and the registry holds the tenant as a boot constant the caller
   never supplied. `last_ingested_at` and `force_push_policy` are absent
   because neither is set on a layer that has never ingested and names no
   policy; both are omitted when empty rather than emitted as null.

2. Patch the layer and read the response.

   ```bash
   podium layer update --id own-release --ref release | inspect
   ```

   **Expect.** `response ['layer']` over the same member list step 1 printed,
   with `records: [('own-release', None)]`, `go-cased keys: []`,
   `tenant mentions: 0`, and `carries a secret value:
   False`. The update reports the layer under one name per value, so a client
   can feed a read back into a write, and the secret is not served again.

3. List the layers.

   ```bash
   podium layer list | inspect
   ```

   **Expect.** `response ['layers']` and `response.layers[]` carrying the
   step 1 members plus `last_ingested_at`, which the `reg` layer carries
   because it ingested at boot. `records: [('reg', None), ('own-release',
   None)]`, `go-cased keys: []`, `tenant mentions: 0`, and
   `carries a secret value: False`.

4. Re-sequence the layers and read the response.

   ```bash
   podium layer reorder own-release reg | inspect
   ```

   **Expect.** `response ['layers']` with `go-cased keys: []` and
   `tenant mentions: 0`. The record line reads `records: [('own-release',
   None), ('reg', None)]`, which is the re-sequencing the command asked for and
   the reverse of step 3's line. The member-name walk reports the first element,
   now `own-release`, so the member list is step 1's: `last_ingested_at` is
   absent on a layer that has never ingested. The reorder answers the whole list the
   caller may read, so it is a second reading of the same object under a
   different endpoint.

5. Unregister the layer and read the recoverable list.

   ```bash
   podium layer unregister own-release
   podium layer list --deleted | inspect
   ```

   **Expect.** The unregister prints `{"unregistered": "own-release"}`. The
   deleted list carries `own-release` under the member set step 1 printed, with
   `go-cased keys: []` and `tenant mentions: 0`. The record line reads
   `records: [('own-release', '<stamp>')]`, where the stamp is an RFC 3339 UTC
   time a few seconds old, so the tombstone the unregister wrote is readable
   rather than inferred. A
   live layer carries `deleted_at` as `null` rather than omitting it, so the
   live list and the recoverable list read the same member set.

6. Register a layer that names its git provider. The field has no CLI flag, so
   the request body is written by hand.

   ```bash
   curl -sS -X POST "$PODIUM_REGISTRY/v1/layers" -H 'Content-Type: application/json' \
     -d '{"id":"gl","source_type":"git","repo":"https://gitlab.acme.internal/alice/gl.git","ref":"main","git_provider":"gitlab"}' \
     -w '\nstatus=%{http_code}\n'
   podium layer list | python3 -c 'import json,sys; print([(l["id"], l["git_provider"]) for l in json.load(sys.stdin)["layers"]])'
   ```

   **Expect.** `status=201`, and the created layer reports `"git_provider":
   "gitlab"`. The list prints `[('reg', ''), ('gl', 'gitlab')]`, so the value
   the request set is the value a later read returns. A stored empty string on
   the `gl` row means the request body reached a field the registration path
   does not apply, and every inbound delivery to that layer would then be
   verified under the wrong signature scheme.

7. Register a layer naming a provider the registry does not carry.

   ```bash
   curl -sS -X POST "$PODIUM_REGISTRY/v1/layers" -H 'Content-Type: application/json' \
     -d '{"id":"bad","source_type":"git","repo":"https://x.acme.internal/a.git","ref":"main","git_provider":"gitea"}' \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `status=400` with `registry.invalid_argument` and the message
   `git_provider "gitea" names no registered git provider (registered:
   bitbucket, github, gitlab)`. The refusal names the field and lists what the
   registry accepts, so the value is rejected at registration rather than
   stored and left to fail every delivery.

8. Register one user-defined layer, so the panel has a row of each class to
   draw, then open the panel.

   ```bash
   podium layer register --id mine --user-defined --owner alice@acme.com \
     --repo https://git.acme.internal/alice/mine.git --ref main > /dev/null
   ```

   Open `http://127.0.0.1:8464/app/#/layers` and read the three rows, which
   stand in the precedence order the list reports: `reg`, `gl`, then `mine`.

   **Expect.** The header row reads `Layer`, `Source`, `Visibility`, and
   `Last ingest`. The `reg` row's Source cell carries a `local` chip with its
   path, and the `gl` and `mine` rows carry a `git` chip with `main` beside it
   and the repository beneath. The `mine` row states
   `order 3 · owner alice@acme.com` under its name, which is the class the
   panel reads from `user_defined` and `owner`, and its Visibility cell carries
   a `user: alice@acme.com` marker, which the panel reads from `users`. The
   `reg` and `gl` rows are public, so each carries the `public` marker. The
   `Last ingest` cell reads a relative age for `reg` and `never` for the two
   git rows, neither of which has ingested. A column that renders empty on
   every row, or a `mine` row that states no owner, means the panel is reading
   a member name the server no longer emits: the cell then draws permanently
   empty and nothing in the page or the response reports it.

**Cleanup.**

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
rm -rf "$WORK"
```

---

## S59: A personal layer's owner and visibility are fixed

**Goal.** Validate that the registry refuses a patch asserting `owner`,
`public`, `organization`, `groups`, or `users` against a stored user-defined
layer, that it refuses whichever caller sends it, that a patch restating what
the layer already stores is admitted, that the refusal rejects the whole
request, and that re-registering the layer's ID as an admin-defined layer is
the recourse that widens it.

**Covers.** The §7.3.1 immutable visibility rule and its
`details.constraint: "immutable_visibility"` discriminator, §4.6 user-defined
layer visibility, the §8.1 layer event a refused patch does not emit, and the
§13.10 layer panel's Edit dialog.

**Why by hand.** The Go suite pins the endpoint and the CLI arms. What it does
not read is the operator's path across them: that the refused patch left the
stored record, the audit log, and the layer's webhook secret alone, that the
recourse an administrator is pointed at actually widens the layer for a third
person, and that the Edit dialog on a personal row presents the visibility as a
statement of fact rather than as a control the registry would refuse.

**Prerequisites.** The S44 stack, with S44's bootstrap-admin note applied in
full: run S44's Prerequisites and steps 1 to 4, and apply that note's amendments
to step 3 for both S59 arms it covers, the `carol` bootstrap admin and the
raised `PODIUM_MAX_USER_LAYERS`. Leave the registry running. Carol is the tenant
admin who owns none of this scenario's layers: step 5 sends its second request
as her, step 7 sends the refused patch as her, and step 8 runs the recourse as
her. Without the bootstrap value the stack holds no tenant admin at all, and
neither arm can run; without the raised cap step 1 is refused with
`429 quota.layer_count_exceeded` on a stack the earlier scenarios have already
registered personal layers on. When Keycloak or the `mkcert` CA is unavailable,
skip and record the skip.

**Steps.**

1. Register two personal layers as the signed-in caller, who holds no
   tenant-admin grant, and read the class, the owner, and the visibility of the
   first.

   ```bash
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 --id s59-notes --user-defined \
     --repo https://git.acme.internal/alice/s59-notes.git --ref main \
     > "$WORK/s59-register.json"
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 --id s59-panel --user-defined \
     --repo https://git.acme.internal/alice/s59-panel.git --ref main > /dev/null
   python3 -c '
   import hashlib, json, os
   d = json.load(open(os.environ["WORK"] + "/s59-register.json"))
   l = d["layer"]
   print("user_defined:", l["user_defined"], "| owner:", l["owner"])
   print("public:", l["public"], "| organization:", l["organization"],
         "| groups:", l["groups"] or [], "| users:", l["users"] or [])
   digest = hashlib.sha256(d["webhook_secret"].encode()).hexdigest()[:12]
   open(os.environ["WORK"] + "/s59-secret.txt", "w").write(digest)
   open(os.environ["WORK"] + "/s59-secret", "w").write(d["webhook_secret"])
   print("secret digest:", digest, "| created_at:", l["created_at"],
         "| order:", l["order"])
   '
   python3 -c '
   import json, os
   json.dump(json.load(open(os.environ["WORK"] + "/s59-register.json"))["layer"],
             open(os.environ["WORK"] + "/s59-layer.json", "w"))
   '
   ```

   **Expect.** `user_defined: True`, `owner` carrying `$SUBJECT`,
   `public: False`, `organization: False`, `groups: []`, and
   `users: ['<the same subject>']`. That is the visibility §4.6 fixes on the
   class, and the registry derived it from the token rather than from the
   request. The secret digest is a twelve-character hex string over the inbound
   webhook secret, which the layer holds because it names a git source, and the
   program writes that digest to `$WORK/s59-secret.txt` for step 8 to compare
   against. It writes the secret itself to `$WORK/s59-secret`, which step 2
   signs a delivery with. `created_at` is the time of this registration, and
   `order` is the position the registry assigned the layer in the composition
   list. Record both, because step 8 reads them again on the record the
   re-registration writes. The
   second command
   writes the layer object to `$WORK/s59-layer.json`, which step 5 sends back
   verbatim.

   `PODIUM_SESSION_TOKEN` is the credential the CLI attaches, so the
   registration acts as the caller the browser session holds. S44's
   Prerequisites unset that variable, so naming it on the command line is what
   makes the invocation authenticated at all. The repository is never fetched,
   because no step here reingests either layer. `s59-panel` exists so step 9
   has a personal row to open after step 8 has converted `s59-notes` to the
   admin-defined class, and the raised `PODIUM_MAX_USER_LAYERS` the
   Prerequisites name is what admits both registrations on a registry that
   already holds this caller's earlier personal layers.

2. Attempt to widen the layer as its own owner, in a patch that also asks for a
   webhook-secret rotation.

   ```bash
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer update \
     --registry http://127.0.0.1:8153 --id s59-notes --public --group acme-eng \
     --rotate-webhook-secret
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the printed failure reads `update failed: HTTP 400`
   over a body carrying `"code": "registry.invalid_argument"`,
   `"constraint": "immutable_visibility"` under `details`, and a message opening
   `the fields groups, public cannot be patched on a user-defined layer`. Both
   asserted field names are present and they read in sorted order, so a client
   reporting them to a person lists them in a stable order across runs. An exit
   of `0` with the stored record printed means the registry is running a build
   from before the refusal, which discarded the widening and answered `200`, and
   every later step of this scenario then reads that discard rather than the
   rule. A `403` `auth.forbidden` here means the caller is not the layer's
   stored owner, and step 1 is re-run before continuing.

   Then read whether the secret step 1 recorded is still the layer's secret, by
   signing an inbound delivery with it the way S27 does.

   ```bash
   BODY='{"ref":"refs/heads/main"}'
   SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 \
     -hmac "$(cat "$WORK/s59-secret")" | awk '{print $NF}')"
   curl -sS -X POST -H "X-Hub-Signature-256: $SIG" \
     -H 'Content-Type: application/json' --data "$BODY" \
     "http://127.0.0.1:8153/v1/ingest/webhook/s59-notes" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** A status that is not `401`, over a body that does not carry
   `ingest.webhook_invalid`. The registry evaluates the refusal above the
   rotation, so the rotation this same patch asked for minted nothing, and the
   secret step 1 recorded still verifies. The delivery is not expected to
   complete an ingest: `https://git.acme.internal/alice/s59-notes.git` is
   unreachable, so the reading here is the signature verification outcome, and
   the status is `502` with `ingest.source_unreachable` on a registry that can
   reach no such host. A `401` `ingest.webhook_invalid` means the refused patch
   rotated the stored secret before returning, and the secret the Git host holds
   no longer signs a delivery this layer accepts.

3. Confirm the stored record is unchanged.

   ```bash
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer list --registry http://127.0.0.1:8153 \
     | python3 -c '
   import json, sys
   l = next(x for x in json.load(sys.stdin)["layers"] if x["id"] == "s59-notes")
   print(l["public"], l["organization"], l["groups"] or [], l["users"] or [], l["ref"])
   '
   ```

   **Expect.** `False False [] ['<the subject>'] main`. The refusal rejected the
   whole request, so neither field the patch carried was applied.

4. Confirm the refused attempt emitted no §8.1 layer event.

   ```bash
   grep '"target":"s59-notes"' "$PODIUM_AUDIT_LOG_PATH" \
     | grep -c '"action":"register"'
   grep '"target":"s59-notes"' "$PODIUM_AUDIT_LOG_PATH" \
     | grep -c '"action":"update"' || echo "no update event"
   ```

   **Expect.** `1` from the first command, the registration step 1 performed, and
   `no update event` from the second, because `grep -c` exits non-zero on a count
   of zero. The registry returns above every mutation the handler performs, so a
   refused patch writes no record and emits no event. A count of `1` or more on
   the update grep means the refusal is being evaluated after the write rather
   than before it.

   Both commands select on the event's `target`, which the registry sets to the
   layer's ID, so the counts read this scenario's layer alone. The audit log
   belongs to the registry S44 step 3 started, and S47 through S57 register their
   own layers into the same file, so a whole-file count reads their events too
   and is not the property this step exists for.

   The first command is the positive control, and it runs first because the
   second one alone proves nothing: `grep` also exits non-zero when the file is
   missing or unreadable, so `no update event` prints on a registry that wrote no
   audit log at all. A `grep` error naming a missing file, or a count of `0` from
   the first command, means `PODIUM_AUDIT_LOG_PATH` was not exported into the
   shell that ran S44 step 3's `podium serve`. Export it there, re-run steps 1
   and 2, and read this step again.

5. Send the layer object read in step 1 back verbatim, which asserts no field.
   Send it twice: once as the layer's own owner, and once as carol, a tenant
   admin who is not the owner.

   ```bash
   curl -sS -X PUT "http://127.0.0.1:8153/v1/layers/update?id=s59-notes" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d @"$WORK/s59-layer.json" -w '\nstatus=%{http_code}\n'
   curl -sS -X PUT "http://127.0.0.1:8153/v1/layers/update?id=s59-notes" \
     -H "Authorization: Bearer $CAROL_TOKEN" -H 'Content-Type: application/json' \
     -d @"$WORK/s59-layer.json" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `status=200` from both calls, each over the layer record, still
   carrying `"public": false`, `"organization": false`, empty `groups`, `users`
   holding the owner alone, and the same `owner`. The body restates `owner` and
   `users` at exactly the values the layer stores, and the rule compares those
   two fields against the stored values, so a client that reads a layer object
   and returns it unchanged is admitted. A `400` here means the comparison is
   reading presence rather than value, or that it is not exact, and every
   read-modify-write client on the endpoint is then refused on a patch that
   changes nothing.

   The second call is the arm the stored-owner comparison exists for. Carol
   holds the tenant-admin role from `PODIUM_BOOTSTRAP_ADMINS`, so the §7.3.1
   layer write rule admits her on a layer she does not own, and the `owner` the
   body carries names `$SUBJECT`, the S44 realm user's subject, rather than
   carol's own. A `200` on the first call and a `400` on the second means
   `owner` is compared against the calling subject rather than against the
   layer's stored owner, and every tenant admin
   that reads a layer object and returns it unchanged is then refused. The first
   call alone does not read that difference: on the owner, a comparison against
   the stored owner and a comparison against the caller's subject answer
   identically.

6. Confirm a patch on any other field still applies, so the refusal is scoped to
   the five fields.

   ```bash
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer update \
     --registry http://127.0.0.1:8153 --id s59-notes --ref release \
     | python3 -c 'import json,sys; print(json.load(sys.stdin)["layer"]["ref"])'
   ```

   **Expect.** `release`. `ref`, `root`, `git_provider`, `force_push_policy`,
   and the webhook-secret rotation stay patchable on a personal layer.

7. Run step 2's command as carol, the tenant admin who does not own the layer.

   ```bash
   PODIUM_SESSION_TOKEN="$CAROL_TOKEN" podium layer update \
     --registry http://127.0.0.1:8153 --id s59-notes --public --group acme-eng \
     --rotate-webhook-secret
   echo "exit=$?"
   ```

   **Expect.** Exactly step 2's reading: `exit=1`, HTTP 400,
   `registry.invalid_argument`, `"constraint": "immutable_visibility"`, and the
   two field names in sorted order. Carol owns no part of `s59-notes`, so the
   §7.3.1 layer write rule admits her on its admin arm alone, and this is the
   arm every neighbouring §7.3.1 rule admits a caller on. The rule reads the
   stored layer's class rather than the caller, so reaching it through the admin
   arm does not lift it, and that is where this rule parts from its three
   neighbours. A `403` `auth.forbidden` here means the registry never
   admitted carol as a tenant admin: `PODIUM_BOOTSTRAP_ADMINS` did not name her
   `sub`, and the prerequisite is re-run before continuing. A `200` means the
   rule consults the caller, and every tenant admin then widens a personal layer
   the owner cannot.

   The command is step 2's command, rotation flag included, so read the layer's
   secret again the way step 2 does, this time against the tenant admin's
   refusal.

   ```bash
   SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 \
     -hmac "$(cat "$WORK/s59-secret")" | awk '{print $NF}')"
   curl -sS -X POST -H "X-Hub-Signature-256: $SIG" \
     -H 'Content-Type: application/json' --data "$BODY" \
     "http://127.0.0.1:8153/v1/ingest/webhook/s59-notes" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** Step 2's reading again: a status that is not `401`, over a body
   that does not carry `ingest.webhook_invalid`. The refusal's placement above
   the rotation holds on a tenant admin as it does on the owner, so this patch
   minted nothing either. A `401` `ingest.webhook_invalid` means the admin arm
   reaches the rotation before the refusal, which step 8's digest comparison
   cannot recover: a secret rotated here and a secret replaced by the
   re-registration read the same there. `BODY` is the variable step 2 exported,
   so re-run step 2's `BODY` assignment first in a shell that has lost it.

8. Take the recourse: re-register the same ID as an admin-defined layer carrying
   the visibility the refused patch asked for, and confirm a third identity now
   reads it. Create that third identity the way S50 step 1 does, and skip the
   creation when S50 has already run in this shell.

   ```bash
   $KC create users -r master -s username=bob -s enabled=true
   $KC set-password -r master --username bob --new-password bob
   export BOB_TOKEN="$(curl -fsS -X POST "$ISSUER/protocol/openid-connect/token" \
     -d grant_type=password -d client_id=podium -d client_secret="$KC_SECRET" \
     -d username=bob -d password=bob \
     | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")"
   PODIUM_SESSION_TOKEN="$BOB_TOKEN" podium layer list --registry http://127.0.0.1:8153 \
     | python3 -c 'import json,sys; print(sorted(x["id"] for x in json.load(sys.stdin)["layers"]))'
   PODIUM_SESSION_TOKEN="$CAROL_TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 --id s59-notes \
     --repo https://git.acme.internal/alice/s59-notes.git --ref release \
     --public --group acme-eng \
     | python3 -c '
   import hashlib, json, os, sys
   d = json.load(sys.stdin)
   l = d["layer"]
   print("user_defined:", l["user_defined"], "| public:", l["public"],
         "| groups:", l["groups"] or [], "| users:", l["users"] or [], "| owner:", repr(l["owner"]))
   print("order:", l["order"], "| last_ingested_ref:", repr(l.get("last_ingested_ref", "")),
         "| last_ingested_at:", repr(l.get("last_ingested_at", "")))
   print("secret digest:", hashlib.sha256(d["webhook_secret"].encode()).hexdigest()[:12],
         "| step 1 digest:", open(os.environ["WORK"] + "/s59-secret.txt").read())
   print("created_at:", l["created_at"])
   '
   PODIUM_SESSION_TOKEN="$CAROL_TOKEN" podium layer list --registry http://127.0.0.1:8153 \
     | python3 -c '
   import json, sys
   print("highest order:", max((x["order"], x["id"]) for x in json.load(sys.stdin)["layers"]))
   '
   PODIUM_SESSION_TOKEN="$BOB_TOKEN" podium layer list --registry http://127.0.0.1:8153 \
     | python3 -c 'import json,sys; print(sorted(x["id"] for x in json.load(sys.stdin)["layers"]))'
   ```

   **Expect.** Bob's first list omits `s59-notes`, because a user-defined layer
   is visible to its registrant alone. The re-registration exits `0` and reports
   `user_defined: False`, `public: True`, `groups: ['acme-eng']`, empty `users`,
   and an empty `owner`, which is the visibility the refused patch asked for.
   Bob's second list carries `s59-notes`, so the recourse widened the layer for
   a caller who could not see it a moment earlier.

   The re-registration replaces the stored record rather than patching it, and
   the reported values are how that reads. `order` is recomputed at the tail of
   the layer list, so it is greater than the value step 1 printed, and the
   `highest order` line names `s59-notes` at exactly the reported value. Carol
   is a tenant admin, so her list carries every layer in the tenant and the
   maximum is the tenant-wide one. An `order` equal to the step 1 value means
   the re-registration preserved the record's position rather than replacing
   the record. `last_ingested_ref` and `last_ingested_at` are empty,
   so the next ingest reads the source afresh. `secret digest` differs from the
   `step 1 digest` printed beside it: the layer holds a new inbound webhook
   secret, and the old one is retired, so an operator who had registered the
   first secret at the Git host registers this one in its place or every
   inbound delivery to the layer is rejected. Two digests that match mean the
   re-registration carried the stored secret forward, and the operator's Git
   host configuration is then still current. `created_at` is later than the
   value step 1 printed, because the record is replaced rather than patched. The
   former owner also regains a slot against the per-identity user-defined layer
   cap.

   The re-registration runs as carol, because `POST /v1/layers` under a stored
   layer's ID is authorized on that layer's write rule, and her tenant-admin
   role admits her there. The class resolution routes an authenticated non-admin
   to the user-defined arm whatever the body says, so the layer's own owner
   never registers an admin-defined layer. Up to the moment this step converts
   the record, the same command as `$TOKEN` is refused with `auth.forbidden`
   carrying `"constraint": "admin_only_fields"` under `details`, because it
   asserts `public` and `groups` on the user-defined arm, and dropping those two
   flags is admitted rather than refused: it re-registers the layer as a
   personal one, resetting its webhook secret, order, registration time, and
   ingest history for no widening at all. After the conversion neither variant
   reaches that point. `s59-notes` is admin-defined, the S44 signed-in caller
   (`admin`, `$SUBJECT`) holds no tenant-admin grant on this stack, and the
   §7.3.1 layer write rule refuses that caller on the stored record with a bare
   `auth.forbidden` that carries no `details`.

9. Read the Edit dialog on a personal row. Open
   `http://127.0.0.1:8153/app/#/layers` in a private browser window and click
   the sign-in control. Keycloak's login page appears; sign in as `admin` with
   the password `admin`. The callback returns the browser to
   `http://127.0.0.1:8153/app/`, which resolves to the catalog rather than the
   layer panel, so re-open `http://127.0.0.1:8153/app/#/layers`. Open the
   overflow control on the `s59-panel` row and press `Edit`.

   **Expect.** The dialog's Visibility section is a statement rather than a
   control: it carries the text `you alone` and the sentence `A layer of your
   own is fixed to you at registration and cannot be widened.`, and it draws no
   Public checkbox, no Organization checkbox, and no field for group names or
   user identifiers. The `Ref`, `Root`, and force-push controls are present, and
   so is the webhook-secret rotation, because those fields stay patchable on the
   class. A checkbox or a members field drawn here offers a write the registry
   refuses with `immutable_visibility`, which is the divergence between the
   panel's prediction and the server rule that this step exists to catch.

   The text and the sentence the section prints are what separate this reading
   from a dialog that omits the Visibility section on every row. A dialog
   drawing no section at all carries neither `you alone` nor the sentence about
   registration, so it fails this step rather than passing it.

   The `s59-notes` row offers no contrast to read here. It is admin-defined
   after step 8, and the panel offers `Edit` on an admin-defined row only to a
   caller holding `manage_any_layer`. The S44 signed-in caller (`admin`,
   `$SUBJECT`) holds no tenant-admin grant on this stack, so that row's overflow
   control carries no `Edit` item and its dialog cannot be opened. The
   admin-defined rendering, which draws an operable checkbox for each of the
   `Public` and `Organization` axes and lists the stored group names and user
   identifiers as tokens the reader removes, is pinned by the
   `web/ui/src/surfaces.test.tsx` case `draws a granted visibility axis in the
   Edit dialog as an operable checkbox`. S60 reads that rendering on a caller
   who holds `manage_any_layer`.

**Teardown.** Run S44's teardown.

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
rm -rf "$WORK"
docker rm -f kc-podium; rm -rf "$KCERT"
```

---

## S60: An admin-defined layer's visibility narrows

**Goal.** Validate that `POST|PUT /v1/layers/update` withdraws a visibility axis
an admin-defined layer holds, that the withdrawal takes effect for a reading
identity holding no tenant-admin grant, and that the layer panel's Edit dialog
offers the axis as an operable control rather than as a statement of fact.

**Covers.** The §7.3.1 patch semantics on the visibility members, §4.6 for a
stored record that sets no visibility field, and the §13.10 layer panel's Edit
dialog on an admin-defined row.

**Why by hand.** The end-to-end suite drives the narrowing and the list read
through the built binaries. What it does not read is the browser rendering: that
the Edit dialog draws a checkbox for an axis the layer already grants, that
unchecking it and submitting is accepted, and that the row reports the narrowed
visibility afterwards.

**Prerequisites.** The S44 stack, with S44's bootstrap-admin note applied: run
S44's Prerequisites and steps 1 to 4, apply that note's amendment to step 3 so
`carol` exists and the registry was started with
`PODIUM_BOOTSTRAP_ADMINS="$CAROL_SUBJECT"`, and leave the registry running.
Carol is the bootstrap operator and never signs in to the UI; her token issues
the tenant-admin grant step 1 makes, and every write after that grant is the
signed-in caller's. The raised `PODIUM_MAX_USER_LAYERS` S59 asks for is not
needed here, because both layers this scenario registers are admin-defined and
the §7.3.1 per-identity cap counts user-defined layers alone. When Keycloak or
the `mkcert` CA is unavailable, skip and record the skip.

**Steps.**

1. Grant the tenant-admin role to the signed-in caller, using carol's bootstrap
   token, and register two public admin-defined layers as that caller.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/admin/grants" \
     -H "Authorization: Bearer $CAROL_TOKEN" -H 'Content-Type: application/json' \
     -d "{\"user_id\":\"$SUBJECT\"}" -w '\nstatus=%{http_code}\n'
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 --id s60-notes \
     --repo https://git.acme.internal/acme/s60-notes.git --ref main --public > /dev/null
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 --id s60-panel \
     --repo https://git.acme.internal/acme/s60-panel.git --ref main --public > /dev/null
   ```

   **Expect.** The grant answers `201` with `{"user_id": "<the subject>"}`, and
   both registrations exit `0`. A `403` on the grant means
   `PODIUM_BOOTSTRAP_ADMINS` did not name carol's `sub`, and the prerequisite is
   re-run before continuing. A `403` on a registration means the grant did not
   take effect, because `public` is an admin-only registration field.

2. Create the reading identity and confirm it sees both layers. Create it the
   way S50 step 1 does, and run the creation every time this scenario is run.
   This scenario's Prerequisites re-run S44's Prerequisites and steps 1 to 4,
   which recreate the `kc-podium` container on an empty user set, so a `bob`
   created by an earlier S50 or S59 run does not survive into this realm and a
   `BOB_TOKEN` minted against the earlier realm's keys no longer verifies. When
   bob already exists in the realm that is running, `$KC create users` reports a
   conflict and exits non-zero, which is harmless here, because the token mint
   below is what the rest of the step reads.

   ```bash
   $KC create users -r master -s username=bob -s enabled=true
   $KC set-password -r master --username bob --new-password bob
   export BOB_TOKEN="$(curl -fsS -X POST "$ISSUER/protocol/openid-connect/token" \
     -d grant_type=password -d client_id=podium -d client_secret="$KC_SECRET" \
     -d username=bob -d password=bob \
     | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")"
   PODIUM_SESSION_TOKEN="$BOB_TOKEN" podium layer list --registry http://127.0.0.1:8153 \
     | python3 -c 'import json,sys; print(sorted(x["id"] for x in json.load(sys.stdin)["layers"]))'
   ```

   **Expect.** Bob's list carries `s60-notes` and `s60-panel`, because a public
   layer is visible to every caller. Bob holds no tenant-admin grant, so the
   list is the §4.6 evaluation of each record rather than the whole tenant's
   layers, which is what makes step 3's re-read evidence of the withdrawal.

3. Withdraw the `public` axis from `s60-notes`, read the stored record the
   registry returns, and re-read bob's list.

   ```bash
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer update \
     --registry http://127.0.0.1:8153 --id s60-notes --public=false \
     | python3 -c '
   import json, sys
   l = json.load(sys.stdin)["layer"]
   print("public:", l["public"], "| organization:", l["organization"],
         "| groups:", l["groups"] or [], "| users:", l["users"] or [],
         "| ref:", l["ref"])
   '
   PODIUM_SESSION_TOKEN="$BOB_TOKEN" podium layer list --registry http://127.0.0.1:8153 \
     | python3 -c 'import json,sys; print(sorted(x["id"] for x in json.load(sys.stdin)["layers"]))'
   ```

   **Expect.** The patch exits `0` and reports `public: False` with an empty
   `groups`, an empty `users`, and `ref: main` unchanged, so the body's one
   member was applied and the members it omitted kept their stored values.
   Bob's list now carries `s60-panel` and omits `s60-notes`: the record sets no
   visibility field, matches no §4.6 condition, and reaches no composed view.
   A record still reporting `public: True` means the flag was dropped before the
   body was built, which is the defect this scenario exists to catch.

4. Read the Edit dialog on the admin-defined row and withdraw the axis through
   it. Open `http://127.0.0.1:8153/app/#/layers` in a private browser window and
   click the sign-in control. Keycloak's login page appears; sign in as `admin`
   with the password `admin`. The callback returns the browser to
   `http://127.0.0.1:8153/app/`, which resolves to the catalog, so re-open
   `http://127.0.0.1:8153/app/#/layers`. Open the overflow control on the
   `s60-panel` row, press `Edit`, uncheck `Public`, and submit.

   **Expect.** The dialog's Visibility section draws a `Public` checkbox that is
   checked and operable, an `Organization` checkbox that is unchecked, and the
   two token fields for group names and user identifiers. Unchecking `Public`
   and submitting is accepted, and the dialog stays open on a confirmation panel
   reading `Layer updated`, `Layer s60-panel is updated.`, and a `Done` control
   that closes it. The `s60-panel` row's visibility cell changes from `public`
   to `no grants`. A section drawing the granted axis as a
   label with no control, or a sentence telling the reader that a grant cannot
   be withdrawn, is the panel still built around the grant-only endpoint, and it
   withholds a write the registry now accepts. A submission the registry refuses
   means the form sent a member the caller may not set on this class.

   The `Edit` item appears on this row because step 1 granted the signed-in
   caller `manage_any_layer`. Without that grant the panel offers `Edit` on an
   admin-defined row to nobody, and the dialog cannot be opened at all, which
   S59 step 9 records.

**Teardown.** Run S44's teardown.

```bash
kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
rm -rf "$WORK"
docker rm -f kc-podium; rm -rf "$KCERT"
```

---

## S61: SCIM membership does not grant under `trusted-headers`

**Goal.** Validate that a registry running the `trusted-headers` identity
provider with the SCIM receiver mounted resolves a layer's `groups:` filter
from `X-Podium-User-Groups` alone, that a directory entry naming the caller's
`X-Podium-User-Sub` or `X-Podium-User-Email` value grants nothing on the data
plane or on the layer read, and that the receiver keeps serving its directory.

**Covers.** The `trusted-headers` identity provider (§6.3.3), the §6.3.1 SCIM
receiver and directory, per-layer visibility (§4.6), the §7.3.1 layer read,
and the startup line naming whether the directory decides a layer read.

**Prerequisites.** None beyond the build. No identity provider, container, or
certificate is needed.

**Steps.**

1. Run the isolation block.
2. Write a registry config with a public layer and a group-restricted layer.

   ```bash
   mkdir -p "$WORK/pub/handbook" "$WORK/eng/deploy"
   podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
   podium artifact scaffold --type skill --description "Engineering deploy" --force "$WORK/eng/deploy"
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: public-handbook
         source: { local: { path: $WORK/pub } }
         visibility: { public: true }
       - id: eng-internal
         source: { local: { path: $WORK/eng } }
         visibility: { groups: [engineering] }
   YAML
   ```

3. Boot the server in `trusted-headers` mode with a proxy secret and the SCIM
   receiver mounted. The bind is loopback, so no `--allow-public-bind` is
   needed.

   ```bash
   export PODIUM_IDENTITY_PROVIDER=trusted-headers
   export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
   export PODIUM_SCIM_TOKENS=scim-bearer
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8130 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8130/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8130
   grep -E "identity provider|SCIM" "$WORK/srv.log"
   ```

   **Expect.** `identity provider: trusted-headers (proxy secret required on
   every request)`, `SCIM 2.0 receiver mounted at /scim/v2/`, and `SCIM group
   expansion in layer visibility: false (identity provider
   "trusted-headers")`. The third line is the one this scenario exists for: it
   names the configuration in which a pushed directory grants nothing.

4. Push a SCIM user and place her in the `engineering` group.

   ```bash
   SCIM="Authorization: Bearer scim-bearer"
   CT="Content-Type: application/scim+json"
   SCIM_UID=$(curl -s -H "$SCIM" -H "$CT" -X POST "$URL/scim/v2/Users" \
     -d '{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"alice@acme.com","active":true}' \
     | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
   echo "scim user id: $SCIM_UID"
   curl -s -o /dev/null -w "group create: %{http_code}\n" -H "$SCIM" -H "$CT" -X POST "$URL/scim/v2/Groups" \
     -d "{\"schemas\":[\"urn:ietf:params:scim:schemas:core:2.0:Group\"],\"displayName\":\"engineering\",\"members\":[{\"value\":\"$SCIM_UID\"}]}"
   curl -s -H "$SCIM" "$URL/scim/v2/Users/$SCIM_UID" | grep -c "\"id\":\"$SCIM_UID\""
   curl -s -H "$SCIM" "$URL/scim/v2/Groups"
   ```

   **Expect.** `scim user id:` prints a non-empty identifier, `group create:
   201`, the fetch-by-id prints `1`, and the group listing shows
   `engineering` holding a member whose `value` is that identifier. The
   fetch-by-id is the assertion that the extraction captured a real user:
   the receiver answers `404` with a body carrying no identifier when the id
   names no user (`pkg/scim/handler.go:169-176`), and an empty capture
   requests the collection route, whose body carries the stored ids and never
   the string `"id":""`, so both failures print `0`. The group read is a
   display rather than an assertion, because the receiver stores a member
   value verbatim and validates nothing about it
   (`pkg/scim/handler.go:249-255`, `pkg/scim/scim.go:223-241`), so it echoes
   back whatever the create body interpolated. A lost identifier still
   creates the group, `MembersOf` then resolves no member
   (`pkg/scim/scim.go:295-311`), and step 5 would pass for a reason unrelated
   to this scenario. The capture variable avoids the name `UID`,
   which both shells reserve as a read-only parameter holding the process
   user id. The receiver accepts and stores the push on this registry
   exactly as it does on an `oidc-jwt` one; what differs is what reads it. A
   `404` from the create requests means `PODIUM_SCIM_TOKENS` was not set
   before the registry started.

5. Issue loads as the gateway would. alice is in the SCIM `engineering` group
   throughout.

   ```bash
   code() { curl -s -o /dev/null -w "%{http_code}\n" "$@"; }
   SEC="X-Podium-Proxy-Secret: gateway-secret"
   echo "scim-only sub:   $(code -H "X-Podium-User-Sub: alice@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
   echo "scim-only email: $(code -H "X-Podium-User-Sub: opaque-123" -H "X-Podium-User-Email: alice@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
   echo "asserted group:  $(code -H "X-Podium-User-Sub: alice@acme.com" -H "X-Podium-User-Groups: engineering" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
   echo "handbook:        $(code -H "X-Podium-User-Sub: alice@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=handbook")"
   echo "anon deploy:     $(code "$URL/v1/load_artifact?id=deploy")"
   ```

   **Expect.**

   - `scim-only sub` and `scim-only email` return `404`. The directory names
     alice in `engineering`, neither request carries the group header, and the
     layer is invisible on both routes to the grant.
   - `asserted group` returns `200`. This is the negative control: the same
     caller and the same layer, admitted on the group the gateway asserted. A
     run in which this line returns `404` means the fix withdrew the specified
     grant as well as the unspecified one.
   - `handbook` returns `200`, since the public layer needs no group.
   - `anon deploy` returns `404`.

6. Read the layer list as the same caller without the groups header.

   ```bash
   curl -s -H "X-Podium-User-Sub: alice@acme.com" -H "$SEC" "$URL/v1/layers" \
     | python3 -c 'import json,sys; print(sorted(l["id"] for l in json.load(sys.stdin)["layers"]))'
   ```

   **Expect.** `['public-handbook']`. The body is parsed rather than grepped
   for `"id":"`, because `writeJSON` indents the layer list
   (`pkg/registry/server/server.go:1438-1443`) and a compact pattern matches
   nothing whether or not the fix is applied. `eng-internal` is absent rather than
   refused, so the read discloses no identifier, source location, or
   visibility declaration for it. The layer read and the data plane agree,
   which is what one gated construction site produces.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

---

## S62: A reingest that drops an artifact fails

**Goal.** Validate that `podium layer reingest` exits 1 when the ingest cycle
dropped an artifact, that each dropped artifact is named on standard error
with its identifier, its error code, and its reason, that this holds on a
cycle that accepted nothing, and that a clean cycle still exits 0.

**Covers.** The §7.3.1 ingest outcome and the reingest exit status, and the
§13.10 public-mode sensitivity floor as the rejection this scenario uses.

**Why by hand.** The end-to-end suite reads the exit code and the two streams
through the harness. What it does not read is the operator's terminal: that
the accepted artifact and the dropped one arrive on different streams, that
redirecting standard output still leaves the rejection visible, and that the
shell's `$?` is what a cron job or a CI step would gate on.

**Prerequisites.** A built `podium` binary on `PATH`. No identity provider,
container, or certificate is required.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then create
   the three layer directories this scenario registers, then start a public-mode standalone registry that ingests none
   of them at boot. Every artifact sits under its own domain path, because a
   canonical artifact identifier derives from its domain directory and two
   layers contributing the same identifier is a cross-layer collision.

   ```bash
   export REG="http://127.0.0.1:8080"
   mkdir -p "$WORK/mixed/ops/runbook" "$WORK/mixed/ops/payroll" \
     "$WORK/rejected/finance/ledger" "$WORK/clean/ops/oncall"
   write_artifact() {
     cat > "$1" <<EOF
   ---
   type: context
   version: 1.0.0
   description: $2
   sensitivity: $3
   ---

   $2
   EOF
   }
   write_artifact "$WORK/mixed/ops/runbook/ARTIFACT.md" \
     "Restarting the ingest worker after a failed deploy." low
   write_artifact "$WORK/mixed/ops/payroll/ARTIFACT.md" \
     "Reconciling the monthly payroll export." medium
   write_artifact "$WORK/rejected/finance/ledger/ARTIFACT.md" \
     "Closing the monthly ledger." medium
   write_artifact "$WORK/clean/ops/oncall/ARTIFACT.md" \
     "Handing over the on-call pager." low
   PODIUM_PUBLIC_MODE=true podium serve --standalone --no-embeddings \
     > "$WORK/server.log" 2>&1 &
   echo "$!" > "$WORK/server.pid"
   sleep 2
   grep 'podium-server listening on ' "$WORK/server.log"
   kill -0 "$(cat "$WORK/server.pid")" || echo "FAIL: the server exited; read $WORK/server.log" >&2
   ```

   **Expect.** `grep` prints a timestamped `podium-server listening on
   127.0.0.1:8080 (mode=public)` line, `kill -0` prints nothing, and `$WORK/server.log` reports
   the registry started in public mode. No `--layer-path` is passed,
   so the registry ingests nothing at boot and the only ingests are the ones
   the steps below trigger. A `FAIL:` line or a missing `listening` line is a
   refusal to start. A refusal naming the address means the address is in use;
   restart with `PODIUM_BIND=127.0.0.1:8099`, set
   `export REG="http://127.0.0.1:8099"`, and repeat.

2. Register the mixed layer as a local source and reingest it, keeping the two
   streams apart.

   ```bash
   podium layer register --registry "$REG" --id s62-mixed \
     --local "$WORK/mixed" > /dev/null
   podium layer reingest --registry "$REG" s62-mixed \
     > "$WORK/out.txt" 2> "$WORK/err.txt"
   echo "exit=$?"
   cat "$WORK/out.txt"
   cat "$WORK/err.txt"
   ```

   **Expect.** `exit=1`. `$WORK/out.txt` carries one
   `artifact: ...   layer: s62-mixed` line for the runbook artifact and no
   line for the payroll artifact. `$WORK/err.txt` carries one `rejected:`
   line naming the payroll artifact, its error code, and the sensitivity
   reason, and carries no raw JSON body. `exit=0` is the shipped behavior
   this scenario exists to catch.

3. Register the layer holding a medium artifact alone and reingest it, so the
   cycle accepts nothing.

   ```bash
   podium layer register --registry "$REG" --id s62-rejected \
     --local "$WORK/rejected" > /dev/null
   podium layer reingest --registry "$REG" s62-rejected \
     > "$WORK/out2.txt" 2> "$WORK/err2.txt"
   echo "exit=$?"
   cat "$WORK/out2.txt"
   cat "$WORK/err2.txt"
   ```

   **Expect.** `exit=1`. `$WORK/err2.txt` carries the `rejected:` line naming
   the ledger artifact and its error code. `$WORK/out2.txt` is empty, and in
   particular carries no pretty-printed JSON body. A JSON body on standard
   output with `exit=0` is the shipped behavior on a cycle that accepted
   nothing.

4. Register the layer holding a low-sensitivity artifact alone and reingest it
   twice, so the second cycle changes nothing.

   ```bash
   podium layer register --registry "$REG" --id s62-clean \
     --local "$WORK/clean" > /dev/null
   podium layer reingest --registry "$REG" s62-clean; echo "exit=$?"
   podium layer reingest --registry "$REG" s62-clean; echo "exit=$?"
   ```

   **Expect.** Both report `exit=0` and both print the
   `artifact: ...   layer: s62-clean` line for the on-call artifact, because
   an unchanged artifact is reported on the same line as an accepted one and
   is not a drop.

**Cleanup.** `kill "$(cat "$WORK/server.pid")"` and `rm -rf "$WORK"`.

---

## S63: The two sync modes compose a shared merge target identically

**Goal.** Validate that a filesystem-source sync and a standalone-server sync
over the same directory write byte-identical merged and injected files when
two artifacts contend for one target, and that an edit to either contributor
is reported as a change.

**Covers.** The §7.5 materialization order, the §7.5.3 `artifacts:` list
order, the §11 filesystem-to-server equivalence requirement, and the change
comparison over a shared materialized path (§11's idempotent re-sync bullet
over the §7.5.3 lock entries).

**Why by hand.** The assertion is over the bytes of a file two artifacts wrote
into, read side by side from two targets that were materialized through
different registry sources. A run in which one mode orders by layer and the
other by ID differs only inside those files, and every per-artifact file
matches, which is what kept the divergence out of the suite.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above.

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`.

2. Build a two-layer registry whose colliding pairs straddle the layers. Two
   hooks share one event, two rules share one target, and each pair's
   canonical IDs sort opposite to `layer_order:`. The straddle is what makes
   the comparison non-vacuous: within one layer the filesystem walk already
   emits alphabetically by canonical ID, which is the order the server source
   produces globally, so a pair sitting inside one layer folds identically
   under both modes whatever the build does. No artifact is `type: command`,
   which is the §6.7.1 cell codex cannot translate.

```bash
mkdir -p "$WORK/reg/org-defaults" "$WORK/reg/team-finance"
cat > "$WORK/reg/.registry-config" <<'EOF'
multi_layer: true
layer_order:
  - org-defaults
  - team-finance
EOF
printf 'visibility:\n  public: true\n' > "$WORK/reg/org-defaults/.layer-config"
printf 'visibility:\n  public: true\n' > "$WORK/reg/team-finance/.layer-config"
podium artifact scaffold --type hook --hook-event pre_tool_use \
  --hook-action 'notify-send podium' --description "Notify on every tool call" \
  "$WORK/reg/org-defaults/z-hooks/notify" > /dev/null
podium artifact scaffold --type hook --hook-event pre_tool_use \
  --hook-action 'audit-log podium' --description "Audit every tool call" \
  "$WORK/reg/team-finance/a-hooks/audit" > /dev/null
podium artifact scaffold --type rule --rule-mode glob --rule-globs 'src/**/*.ts' \
  --description "Apply the org style rules" \
  "$WORK/reg/org-defaults/z-rules/style" > /dev/null
podium artifact scaffold --type rule --rule-mode glob --rule-globs 'src/**/*.ts' \
  --description "Apply the finance policy rules" \
  "$WORK/reg/team-finance/a-rules/policy" > /dev/null
find "$WORK/reg" -name ARTIFACT.md | sed "s|$WORK/reg/||" | sort
```

   **Expect.** Four paths, `org-defaults/z-hooks/notify/ARTIFACT.md`,
   `org-defaults/z-rules/style/ARTIFACT.md`,
   `team-finance/a-hooks/audit/ARTIFACT.md`, and
   `team-finance/a-rules/policy/ARTIFACT.md`. Each scaffold command supplies
   its type-specific required flag (`--hook-event` for a hook, `--rule-globs`
   for a glob rule), so no command blocks on a prompt reading standard input.

3. Materialize the registry through the filesystem source for both harnesses.

```bash
mkdir -p "$WORK/fs-cc" "$WORK/fs-codex"
podium sync --registry "$WORK/reg" --target "$WORK/fs-cc" --harness claude-code
podium sync --registry "$WORK/reg" --target "$WORK/fs-codex" --harness codex
```

   **Expect.** Each run lists the four artifacts in ascending canonical ID
   order, `a-hooks/audit`, `a-rules/policy`, `z-hooks/notify`, `z-rules/style`,
   with the `team-finance` artifacts first even though `team-finance` is
   second in `layer_order:`. Under `claude-code` both hooks report
   `.claude/settings.json`; under `codex` both hooks report
   `.codex/config.toml` and both rules report `AGENTS.md`. A listing in layer
   order is the behavior this scenario exists to catch.

4. Start a standalone server over the same directory and materialize through
   it.

```bash
podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
  --bind 127.0.0.1:8126 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8126/healthz
server_alive "$SRV" "$WORK/srv.log"
mkdir -p "$WORK/srv-cc" "$WORK/srv-codex"
podium sync --registry http://127.0.0.1:8126 --target "$WORK/srv-cc" --harness claude-code
podium sync --registry http://127.0.0.1:8126 --target "$WORK/srv-codex" --harness codex
```

   **Expect.** The health poll returns before either sync runs, and each sync
   prints the same four artifacts in the same order as step 3, against the
   same materialized paths.

5. Compare the two targets per harness.

```bash
find "$WORK/fs-cc" -type f ! -name sync.lock | wc -l      # must be 3, not 0
diff -r -x sync.lock "$WORK/fs-cc" "$WORK/srv-cc" && echo "IDENTICAL claude-code"
find "$WORK/fs-codex" -type f ! -name sync.lock | wc -l   # must be 2, not 0
diff -r -x sync.lock "$WORK/fs-codex" "$WORK/srv-codex" && echo "IDENTICAL codex"
```

   **Expect.** `3`, `IDENTICAL claude-code`, `2`, and `IDENTICAL codex`. The
   counts run first because an empty tree compared against an empty tree also
   reports no differences, which scores as a pass while proving nothing. The
   lock is excluded here because its `target` and `last_synced_at` differ by
   construction between two consumers; step 6 compares it on the fields that
   do not.

6. Compare the two locks on the fields that are not volatile, and read the
   list order.

```bash
for h in cc codex; do
  diff <(grep -v -e '^target:' -e '^last_synced_at:' "$WORK/fs-$h/.podium/sync.lock") \
       <(grep -v -e '^target:' -e '^last_synced_at:' "$WORK/srv-$h/.podium/sync.lock") \
    && echo "LOCKS IDENTICAL $h"
  grep -e '^    - id:' -e 'materialized_path:' "$WORK/fs-$h/.podium/sync.lock"
done
```

   **Expect.** `LOCKS IDENTICAL cc` and `LOCKS IDENTICAL codex`. Each lock
   carries four entries in ascending `id` order, and both hooks carry an entry
   of their own against the shared `materialized_path`
   (`.claude/settings.json` under `claude-code`, `.codex/config.toml` under
   `codex`), as do both rules against `AGENTS.md` under `codex`. A lock whose
   entries are in layer order rather than ascending `id` order, or which
   carries a single entry for a path two artifacts wrote, is the §7.5.3
   violation this step catches.

7. Read the composition inside the shared files.

```bash
cat "$WORK/fs-cc/.claude/settings.json"
cat "$WORK/fs-codex/AGENTS.md"
cat "$WORK/fs-codex/.codex/config.toml"
```

   **Expect.** In `.claude/settings.json` the `hooks.PreToolUse` array carries
   the `a-hooks/audit` entry first and the `z-hooks/notify` entry second,
   tagged by their `x-podium-id`. In `AGENTS.md` the
   `<!-- podium:begin:a-rules/policy -->` block precedes the
   `<!-- podium:begin:z-rules/style -->` block, and in `.codex/config.toml`
   the `# podium:begin:a-hooks/audit` block precedes the
   `# podium:begin:z-hooks/notify` block. Both orders are ascending canonical
   ID rather than layer order, in both modes.

8. Read the change signal through a `kind: workspace` target. No single-target
   `podium sync` output carries it, in either the human form or `--json`, so
   the signal is read from a `workflow.publish` command that echoes
   `$PODIUM_CHANGED`.

```bash
mkdir -p "$WORK/proj/.podium"
cat > "$WORK/proj/.podium/sync.yaml" <<YAML
defaults:
  registry: $WORK/reg
targets:
  - id: s63-workspace
    kind: workspace
    target: $WORK/proj/ws
    harness: claude-code
    workflow:
      publish:
        - sh: 'echo "changed=\$PODIUM_CHANGED"'
YAML
podium sync --config "$WORK/proj/.podium/sync.yaml" | tail -1
podium sync --config "$WORK/proj/.podium/sync.yaml" | tail -1
```

   **Expect.** `changed=true` for the first run, which materialized into an
   empty directory, then `changed=false` for the second, which wrote the same
   tree over itself.

9. Edit the hook whose lock entry the old per-path collapse discarded, and
   re-sync. The two hooks share `.claude/settings.json`, and a comparison
   keyed on the path alone kept the entry whose id sorts last, so an edit to
   `a-hooks/audit` moved a hash no comparison read.

```bash
sed -i '' 's|audit-log podium|audit-log podium --verbose|' \
  "$WORK/reg/team-finance/a-hooks/audit/ARTIFACT.md"
podium sync --config "$WORK/proj/.podium/sync.yaml" | tail -1
grep 'audit-log podium' "$WORK/proj/ws/.claude/settings.json"
```

   **Expect.** `changed=true`, and the merged file carries
   `audit-log podium --verbose`. `changed=false` is the behavior this step
   exists to catch: the file is rewritten, and a
   `skip_if_no_changes` publish command gated on the variable skips the target
   that changed. On Linux the edit is `sed -i` without the empty argument.

**Cleanup.** `kill "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S64: A registry migrates its stored hashes on the first start

**Goal.** Validate that an artifact whose stored hash the registry rewrote on
start loads through the MCP consumer with a matching content hash, that a §6.4
workspace overlay serves a hash that moves when its `SKILL.md` changes, and
that the registry's §13.4 stored-row admission refuses a row carrying a
pre-upgrade hash with `materialize.content_hash_mismatch` until the registry has
migrated it.

**Covers.** The §4.7.6 canonical serialization, the §6.6 step-2 delivery check,
the §6.4 overlay response, the §13.4 first-start rewrite, and the §13.4
stored-row admission that refuses an unmigrated row. A consumer cache bucket
the pre-upgrade binary wrote carries no per-ID delivery side files, so the new
`podium-mcp` treats it as a cache miss and serves nothing from it. The
automated suite covers that miss, because the bucket it needs is one the
pre-upgrade binary wrote.

**Why by hand.** The failure is a disagreement between two processes over a
serialization, and the refusal direction requires a store that was written by
one algorithm and read by another, which no in-process test constructs.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then turn
   signing off for every registry the scenario starts and verification off for
   every load. The scenario's subject is the unsigned content-hash path, and
   every **Expect** block below holds only for an unsigned row.

   ```bash
   export PODIUM_SIGN=none PODIUM_VERIFY_SIGNATURES=never
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`.

2. Author one skill carrying an `ARTIFACT.md`, a `SKILL.md`, and two bundled
   resources. A manifest-only artifact exercises one framed slot and hides a
   slot-order defect, so the two resources are what make the comparison
   non-vacuous.

   ```bash
   mkdir -p "$WORK/reg/close-reporting"
   podium artifact scaffold --type skill --description "Run the variance analysis" \
     "$WORK/reg/close-reporting/variance" > /dev/null
   printf 'print("variance")\n' > "$WORK/reg/close-reporting/variance/scripts.py"
   printf 'Variance explained.\n' > "$WORK/reg/close-reporting/variance/notes.md"
   ls "$WORK/reg/close-reporting/variance"
   ```

   **Expect.** `ARTIFACT.md`, `SKILL.md`, `notes.md`, and `scripts.py`.

3. Start the registry over that directory and load the artifact through the MCP
   consumer. The artifact's id carries its domain path, which `podium search`
   prints; `load_artifact` with the bare name returns `registry.not_found`.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8127 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8127/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8127
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"close-reporting/variance"}}}'
   printf '%s\n%s\n' "$INIT" "$LOAD" | podium-mcp 2>/dev/null \
     | tail -1 | grep -o '"content_hash":"[^"]*"' | tail -1
   head -n 3 "$WORK/srv.log"
   ```

   **Expect.** The first two lines of the server log are the standalone
   bootstrap's `standalone: wrote .../sync.yaml (defaults.registry: ...)` and
   `standalone: wrote .../registry.yaml`, and the third reports the rewrite
   over an empty store, `rehash: 0 rewritten, 0 already migrated, ...`. Each
   line carries a timestamp prefix. The load prints
   one `content_hash`, of the form
   `"content_hash":"sha256:cb81ac9dcecf8a644e8614e93f90851f1801151c00c64a4e90d12daacc2379b8"`.
   Record the value as the served hash; the digest covers the scaffolded
   manifest, so a different scaffold template produces a different value. A
   refusal here is the disagreement this scenario exists to catch.

4. Read the overlay's served hash on either side of an edit to its `SKILL.md`.

   ```bash
   mkdir -p "$WORK/overlay"
   podium artifact scaffold --type skill --description "Local draft skill" "$WORK/overlay/draft" > /dev/null
   LOADO='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"draft"}}}'
   printf '%s\n%s\n' "$INIT" "$LOADO" | PODIUM_OVERLAY_PATH="$WORK/overlay" podium-mcp 2>/dev/null \
     | tail -1 | grep -o '"content_hash":"[^"]*"' | tail -1
   printf '\nExtra local guidance.\n' >> "$WORK/overlay/draft/SKILL.md"
   printf '%s\n%s\n' "$INIT" "$LOADO" | PODIUM_OVERLAY_PATH="$WORK/overlay" podium-mcp 2>/dev/null \
     | tail -1 | grep -o '"content_hash":"[^"]*"' | tail -1
   ```

   **Expect.** Two `content_hash` values that differ, such as
   `sha256:8c90d58634b221cdd8c462d0c4149d7934bec895185e4bec0e16de9e8732cf26`
   and
   `sha256:57053072665ffa3fb0d78bc3e447c607793b50d42434d2720d2d18f0354b9904`.
   Two equal values are the defect this step catches: the overlay response
   would then be reporting a hash of the manifest alone.

5. Return the stored hash to the pre-upgrade value and read the refusal. The
   server is stopped for the edit and started again with the
   `content-hash-framing` marker still present, so the start skips the rewrite
   and holds the edited value. The registry's §13.4 stored-row admission
   recomputes the row's hash on the full load and refuses it before any
   consumer check runs. The `shasum` line computes the pre-upgrade
   digest by hand, as the concatenation of `ARTIFACT.md`, `SKILL.md`, and each
   bundled resource's path followed by its body in ascending path order, with
   no length prefixes; no shipped tool computes it. The load runs against an
   empty `$PODIUM_CACHE_DIR` in the default `always-revalidate` mode, so the
   consumer makes a full load, which admission answers. An `offline-first` load would not surface
   it, because a resolution hit serves the bucket it resolved and the
   recomputation then compares that bucket against itself.

   ```bash
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   D="$WORK/reg/close-reporting/variance"
   OLD=$( { cat "$D/ARTIFACT.md" "$D/SKILL.md"; printf 'notes.md'; cat "$D/notes.md"; \
            printf 'scripts.py'; cat "$D/scripts.py"; } | shasum -a 256 | cut -d' ' -f1 )
   sqlite3 "$PODIUM_SQLITE_PATH" \
     "update manifests set content_hash='sha256:$OLD' where artifact_id='close-reporting/variance';"
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8127 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8127/healthz
   server_alive "$SRV" "$WORK/srv2.log"
   grep -c rehash "$WORK/srv2.log"
   rm -rf "$PODIUM_CACHE_DIR"
   printf '%s\n%s\n' "$INIT" "$LOAD" | podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; print(json.load(sys.stdin)["result"]["structuredContent"]["error"])'
   ```

   **Expect.** `grep -c rehash` prints `0`, because the marker is set. The
   printed error begins with `materialize.content_hash_mismatch`, names
   `close-reporting/variance`, and contains no `sha256:` digest, and
   `$WORK/srv2.log` records the admission refusal with the artifact ID and its
   version. A successful load here means the registry is not checking
   stored-row admission.

6. Migrate the store and confirm the target rewrites the copied rows. The
   scenario's own server is stopped for the whole step, because no registry
   process runs on a store the command writes. The target is built by starting
   a registry once over a second fixture, which sets the target's marker, and
   stopping it; the second fixture shares no artifact id with the first, so no
   copied row collides with a row the target already holds.

   ```bash
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   mkdir -p "$WORK/reg2/ops"
   podium artifact scaffold --type skill --description "Rotate the ops keys" "$WORK/reg2/ops/rotate" > /dev/null
   PODIUM_SQLITE_PATH="$WORK/target.db" PODIUM_FILESYSTEM_ROOT="$WORK/target-objects" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg2" \
     --bind 127.0.0.1:8128 > "$WORK/target1.log" 2>&1 &
   T=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8128/healthz
   server_alive "$T" "$WORK/target1.log"
   kill "$T" 2>/dev/null; wait "$T" 2>/dev/null
   podium admin migrate-to-standard --target-store=sqlite \
     --target-sqlite "$WORK/target.db" --target-objects "$WORK/target-objects" \
     --source-sqlite "$WORK/podium.db" --source-objects "$WORK/objects"
   sqlite3 "$WORK/target.db" "select count(*) from data_migrations;"
   PODIUM_SQLITE_PATH="$WORK/target.db" PODIUM_FILESYSTEM_ROOT="$WORK/target-objects" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg2" \
     --bind 127.0.0.1:8128 > "$WORK/target2.log" 2>&1 &
   T=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8128/healthz
   server_alive "$T" "$WORK/target2.log"
   grep rehash "$WORK/target2.log"
   rm -rf "$PODIUM_CACHE_DIR"
   printf '%s\n%s\n' "$INIT" "$LOAD" | PODIUM_REGISTRY=http://127.0.0.1:8128 podium-mcp 2>/dev/null \
     | tail -1 | grep -o '"content_hash":"[^"]*"' | tail -1
   kill "$T" 2>/dev/null; wait "$T" 2>/dev/null
   ```

   **Expect.** The command reports `manifests: 1` in its source plan, then
   `metadata migration complete (0 admin grant(s) preserved)` and `object
   migration complete (4 blob(s))`. `select count(*) from data_migrations`
   prints `0`, which is the
   cleared marker. The target's start then logs
   `rehash: 1 rewritten, 1 already migrated, 0 signature_unverified, 0
   unreproducible, 0 body_missing, 0 body_unavailable (0 unread), 0 in
   conflict, 0 in error, 0 event(s) not appended`: the copied row is the
   rewritten one and the target's own row is the migrated one. The load prints
   step 3's served hash. A summary line reporting `0 rewritten` means the
   command did not clear the marker.

7. Repair the scenario's own store and confirm the rewrite runs once. Deleting
   the marker row returns the store to the state a pre-upgrade store is in.

   ```bash
   sqlite3 "$PODIUM_SQLITE_PATH" "delete from data_migrations where name='content-hash-framing';"
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8127 > "$WORK/srv3.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8127/healthz
   server_alive "$SRV" "$WORK/srv3.log"
   grep rehash "$WORK/srv3.log"
   rm -rf "$PODIUM_CACHE_DIR"
   printf '%s\n%s\n' "$INIT" "$LOAD" | podium-mcp 2>/dev/null \
     | tail -1 | grep -o '"content_hash":"[^"]*"' | tail -1
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8127 > "$WORK/srv4.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8127/healthz
   server_alive "$SRV" "$WORK/srv4.log"
   grep -c rehash "$WORK/srv4.log"
   ```

   **Expect.** The first start logs
   `rehash: 1 rewritten, 0 already migrated, 0 signature_unverified, 0
   unreproducible, 0 body_missing, 0 body_unavailable (0 unread), 0 in
   conflict, 0 in error, 0 event(s) not appended`, and the load that step 5
   refused prints step 3's served hash. The restart prints `0`, because the
   marker the first start recorded holds the rewrite back.

**Cleanup.** `kill "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S65: First run verifies with no configuration

**Goal.** Validate that a standalone registry and a `podium-mcp` consumer on
the same machine sign and verify with no signing configuration at all: the
registry signs by default, the bridge verifies under its `always` default, and
both resolve the same key file.

**Covers.** §4.7.9 key resolution, the §6.2 consumer defaults, the §13.10
signing default, the standalone bootstrap of `~/.podium/sync.yaml`, the
§4.7.10 delivery check in server-source `podium sync` and the Python SDK, and
§7.6.3.

**Why by hand.** The automated suites set the verification key explicitly in
their fixtures, so they pass when the consumer defaults ship without the
producer default or without the shared key resolution.
The SDK suites run against stub registries, so they pass even if the delivery
records a real registry serves, on a single load or in a batch, do not verify
in Python, for example because the batch entry omits `sensitivity`.

**Steps.**

1. Run the isolation block, then drop the store and key overrides so the run
   uses every default under the scratch `HOME`.

   ```bash
   unset PODIUM_SIGN_KEY_PATH PODIUM_SQLITE_PATH PODIUM_FILESYSTEM_ROOT
   ```

2. Serve one artifact with no signing flag, read the bootstrapped `sync.yaml`,
   and read the key file's mode.

   ```bash
   podium artifact scaffold --type skill --description "First run skill" "$WORK/reg/first-run" > /dev/null
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8165 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8165/healthz
   server_alive "$SRV" "$WORK/srv.log"
   cat "$HOME/.podium/sync.yaml"
   ls -l "$HOME/.podium/standalone/registry-signing.key"
   ```

   **Expect.** `sync.yaml` names the registry at `http://127.0.0.1:8165`, carries
   no `verify_signatures` key, and carries no `public:` or `private:` line. `ls
   -l` reads `-rw-------`.

3. Load the artifact through the bridge with no registry, policy, provider, or
   key in its environment.

   ```bash
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"first-run"}}}'
   printf '%s\n%s\n' "$INIT" "$LOAD" | podium-mcp 2>"$WORK/mcp.log" | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded " + r["content_hash"])'
   cat "$WORK/mcp.log"
   ```

   **Expect.** The load prints `loaded sha256:...`. The bridge's stderr carries
   no `WARN: signature verification is off` line. An exit naming
   `config.signature_provider_unavailable` means the bridge did not resolve the
   key file the registry wrote, and `materialize.signature_missing` means the
   registry did not sign at ingest.

4. Sync the workspace with no policy, provider, or key variable set.

   ```bash
   mkdir -p "$WORK/ws" && cd "$WORK/ws"
   env -u PODIUM_VERIFY_SIGNATURES -u PODIUM_SIGNATURE_VERIFY_KEY -u PODIUM_SIGN_KEY_PATH \
     podium sync --target "$WORK/out"; echo "exit=$?"
   ls "$WORK/out"
   ```

   **Expect.** `exit=0` and the `first-run` artifact under `$WORK/out`. An
   exit of 2 naming `config.signature_provider_unavailable` means sync did not
   resolve the key file that `podium-mcp` resolved in step 3.

5. Load the artifact through the Python SDK with no key, then with the
   registry's public key.

   ```bash
   python3 -m venv "$WORK/venv"
   "$WORK/venv/bin/pip" install -q -e "$REAL_HOME/projects/podium/sdks/podium-py[verify]"
   PUB=$(sed -n 's/^public: *//p' "$HOME/.podium/standalone/registry-signing.key")
   cat > "$WORK/sdk.py" <<'PY'
   from podium import Client
   c = Client(registry="http://127.0.0.1:8165")
   print("POLICY", c.verify_signatures)
   print("LOAD", c.load_artifact("first-run").id)
   print("BATCH", [r.status for r in c.load_artifacts(ids=["first-run", "first-run"])])
   PY
   (export HOME="$WORK/empty-home"; mkdir -p "$HOME"; "$WORK/venv/bin/python" "$WORK/sdk.py")
   (export HOME="$WORK/empty-home"; PODIUM_SIGNATURE_VERIFY_KEY="$PUB" "$WORK/venv/bin/python" "$WORK/sdk.py")
   ```

   **Expect.** The first run prints `POLICY never`, `LOAD first-run`, and
   `BATCH ['ok', 'ok']`. The second prints `POLICY always`, `LOAD first-run`,
   and `BATCH ['ok', 'ok']`. A `materialize.content_hash_mismatch` on a batch
   entry only means the registry did not serve `sensitivity` on batch entries.
   A `materialize.signature_invalid` in the second run means the SDK's
   envelope verification disagrees with the registry's signing.

6. Sync from a home that holds no key file, first with no key variable and
   then with an unrelated verification key.

   ```bash
   mkdir -p "$WORK/empty-home" "$WORK/ws2" && cd "$WORK/ws2"
   OTHER=$("$WORK/venv/bin/python" -c 'import base64
   from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
   from cryptography.hazmat.primitives import serialization as s
   k = Ed25519PrivateKey.generate().public_key()
   print(base64.b64encode(k.public_bytes(s.Encoding.Raw, s.PublicFormat.Raw)).decode())')
   (export HOME="$WORK/empty-home"
    env -u PODIUM_VERIFY_SIGNATURES -u PODIUM_SIGNATURE_VERIFY_KEY -u PODIUM_SIGN_KEY_PATH -u PODIUM_SIGNATURE_PROVIDER \
      podium sync --registry http://127.0.0.1:8165 --target "$WORK/out2"; echo "exit=$?")
   (export HOME="$WORK/empty-home"
    env -u PODIUM_VERIFY_SIGNATURES -u PODIUM_SIGN_KEY_PATH -u PODIUM_SIGNATURE_PROVIDER PODIUM_SIGNATURE_VERIFY_KEY="$OTHER" \
      podium sync --registry http://127.0.0.1:8165 --target "$WORK/out3"; echo "exit=$?")
   find "$WORK/out2" "$WORK/out3" -type f 2>/dev/null
   ```

   **Expect.** The first sync prints `exit=2` and its stderr names
   `config.signature_provider_unavailable`. This is the refusal a sync on a
   machine other than the registry's meets with no key configured. The second
   sync prints `exit=1` and its stderr names `materialize.signature_invalid`.
   `find` prints nothing, because neither sync writes an artifact file or a
   `sync.lock`.

**Cleanup.** `kill "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S66: Signing on an existing registry signs no stored row until an operator asks

**Goal.** Validate that turning signing on over a store whose rows were
ingested unsigned leaves those rows unsigned, that the registry refuses them to
every reader, that no automatic path attaches an envelope to them, and that
`sign-stored-rows --include-unsigned` with the plan digest of a reviewed dry
run signs them while the registry serves.

**Covers.** §13.4 stored-row admission, the §13.4 first-start rewrite's
completion record, the §13.4 `sign-stored-rows` command, the §13.4
plan-digest refusal, and §4.7.9 ingest signing.

**Why by hand.** The claim under test is that no automatic path re-signs a
stored row, including `podium layer reingest`, which reads as the obvious
repair, and that the operator command does. Reading the stored `signature`
column confirms what the registry holds rather than what a load reports.

**Steps.**

1. Run the isolation block.

2. Ingest one artifact with signing off, then stop the registry and read the
   store's record of the §13.4 rewrite.

   ```bash
   podium artifact scaffold --type skill --description "Early skill" "$WORK/reg/early" > /dev/null
   PODIUM_SIGN=none podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8166 > "$WORK/srv1.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8166/healthz
   server_alive "$SRV" "$WORK/srv1.log"
   kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
   sqlite3 "$PODIUM_SQLITE_PATH" "select count(*) from data_migrations where name='content-hash-framing';"
   ```

   **Expect.** The `sqlite3` query prints `1`: the first start recorded the
   §13.4 rewrite as complete. A `0` is a defect in the first-start record.

3. Restart with the default signing mode and load the earlier version through
   `curl` and through `podium-mcp`.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8166 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8166/healthz
   server_alive "$SRV" "$WORK/srv2.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8166
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   load_http() { curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=early&version=$1" \
     | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("code") or "loaded delivery_signature=" + repr(d.get("delivery_signature")))'; }
   load_mcp() { printf '%s\n%s\n' "$INIT" \
     "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"load_artifact\",\"arguments\":{\"id\":\"early\",\"version\":\"$1\"}}}" \
     | PODIUM_CACHE_DIR="$(mktemp -d "$WORK/cache.XXXXXX")" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded")'; }
   load_http 0.1.0
   load_mcp 0.1.0
   ```

   **Expect.** Both loads print an error beginning with
   `materialize.signature_missing`.

4. Reingest the layer and load the earlier version again, then read the stored
   `signature` column.

   ```bash
   podium layer reingest --registry "$PODIUM_REGISTRY" reg
   load_http 0.1.0
   load_mcp 0.1.0
   sqlite3 "$PODIUM_SQLITE_PATH" "select quote(signature) from manifests where artifact_id='early' and version='0.1.0';"
   ```

   **Expect.** Both loads print `materialize.signature_missing` again, and the
   `sqlite3` query prints `''`. A load that succeeds here means a reingest
   re-signed the stored row, which this scenario exists to catch.

5. Raise the artifact's version, reingest, and load both versions.

   ```bash
   sed -i.bak 's/^version: 0.1.0$/version: 0.2.0/' "$WORK/reg/early/ARTIFACT.md"
   podium layer reingest --registry "$PODIUM_REGISTRY" reg
   load_http 0.2.0
   load_mcp 0.2.0
   load_http 0.1.0
   ```

   **Expect.** The new version loads with a non-empty `delivery_signature`
   through `curl` and loads through the bridge. The earlier version still prints
   `materialize.signature_missing`.

6. With the registry from step 3 still serving, list the unsigned rows, sign
   them with the plan digest of that list, and read the stored `signature`
   column. The isolation block's `PODIUM_SQLITE_PATH` and
   `PODIUM_SIGN_KEY_PATH` are the ones that registry uses. The step runs in
   three parts.

   First, run the dry run and read its plan digest from the last line.

   ```bash
   podium admin sign-stored-rows --dry-run --include-unsigned | tee dry-run.log
   digest=$(sed -n 's/^dry-run: plan digest \(sha256:[0-9a-f]*\) .*/\1/p' dry-run.log)
   ```

   Second, store a second unsigned artifact after the review, then run with
   the reviewed digest. A signing-off registry on a second port, over the same
   store with its own layer path, ingests the new artifact without a signature
   and stops, while the registry from step 3 keeps serving.

   ```bash
   podium artifact scaffold --type skill --description "Late skill for the plan check" "$WORK/reg2/late" > /dev/null
   PODIUM_SIGN=none podium serve --standalone --no-embeddings --layer-path "$WORK/reg2" \
     --bind 127.0.0.1:8167 > "$WORK/srv3.log" 2>&1 &
   SRV3=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8167/healthz
   server_alive "$SRV3" "$WORK/srv3.log"
   kill "$SRV3" 2>/dev/null; wait "$SRV3" 2>/dev/null
   podium admin sign-stored-rows --include-unsigned --plan-digest="$digest"; echo "exit=$?"
   sqlite3 "$PODIUM_SQLITE_PATH" "select quote(signature) from manifests where artifact_id='early' and version='0.1.0';"
   ```

   Third, rerun the dry run, read its new digest, run with it, and load the
   earlier version again.

   ```bash
   podium admin sign-stored-rows --dry-run --include-unsigned | tee dry-run.log
   digest=$(sed -n 's/^dry-run: plan digest \(sha256:[0-9a-f]*\) .*/\1/p' dry-run.log)
   podium admin sign-stored-rows --include-unsigned --plan-digest="$digest"; echo "exit=$?"
   load_http 0.1.0
   load_mcp 0.1.0
   sqlite3 "$PODIUM_SQLITE_PATH" "select quote(signature) from manifests where artifact_id='early' and version='0.1.0';"
   ```

   **Expect.** The first dry run prints a `dry-run:` line for the
   `early@0.1.0` row with `sign=true signed_by=unsigned stored=sha256:...`, a
   `dry-run: plan mode=` line, a `dry-run: 0 unsigned left` line, which
   counts the rows the run would leave unsigned, and a last line `dry-run: plan digest
   sha256:... over K row(s)` whose `K` equals the number of row lines without
   `class=migrated`, and it changes nothing. The run with the reviewed digest
   prints a `plan:` line for `late@0.1.0`, prints `plan changed since the
   reviewed dry run` on stderr, and prints `exit=3`, and its `sqlite3` query
   prints `''`, the empty signature the column's `NOT NULL DEFAULT ''` holds,
   so the refused run wrote nothing to `early@0.1.0`. The rerun's dry run lists
   both unsigned rows, and the run with its digest prints `rehash: 0 unsigned
   left` and `exit=0`. With no restart, `load_http` prints `loaded` with a
   non-empty `delivery_signature` and `load_mcp` prints `loaded`, and the
   `sqlite3` query prints a non-empty signature. A run that signs a row its dry
   run never listed, a run that refuses a plan that did not change, and a load
   that succeeds only after a restart are the failures this step catches.

**Cleanup.** `kill "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S67: `podium verify --provider noop` refuses

**Goal.** Validate that the `noop` provider refuses the placeholder envelope it
produces, at the command an operator types.

**Covers.** The §4.7.9 `noop` provider and `podium sign` and `podium verify`.

**Why by hand.** The `noop:<content_hash>` placeholder is computable from any
served `content_hash`, so a permissive `noop` verifier admits a forged
envelope. The operator surface is where a reintroduced permissive verifier
would be trusted.

**Steps.**

1. Run the isolation block.

2. Sign a content hash with `noop` and verify the result with `noop`.

   ```bash
   H="sha256:$(printf 'podium' | shasum -a 256 | cut -d' ' -f1)"
   SIG="$(podium sign --provider noop --content-hash "$H")"
   echo "$SIG"
   podium verify --provider noop --content-hash "$H" --signature "$SIG"; echo "exit=$?"
   ```

   **Expect.** `podium sign` prints `noop:` followed by `$H`. `podium verify`
   prints `verify failed: signature_invalid: the noop provider does not
   verify` on stderr and `exit=1`. `exit=0` means the `noop` verifier accepts
   its own placeholder, which this scenario exists to catch.

**Cleanup.** `rm -rf "$WORK"`.

---

## S68: A stale `never` is announced

**Goal.** Validate that a machine carrying `defaults.verify_signatures: never`
in `~/.podium/sync.yaml`, as an earlier release's standalone bootstrap wrote it,
loads without verification and says so on the bridge's stderr.

**Covers.** The §7.5.2 `defaults.verify_signatures` key and the §4.7.9 policy
resolution.

**Why by hand.** No release rewrites that line, and a machine carrying it
verifies nothing, including against another registry. The startup warning is
the one place the operator learns that, and no assertion on a load result
observes it.

**Steps.**

1. Run the isolation block.

2. Serve an unsigned registry and write the stale line into the user-global
   `sync.yaml`, replacing the file the bootstrap wrote.

   ```bash
   podium artifact scaffold --type skill --description "Stale skill" "$WORK/reg/stale" > /dev/null
   PODIUM_SIGN=none podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8168 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8168/healthz
   server_alive "$SRV" "$WORK/srv.log"
   printf 'defaults:\n  registry: http://127.0.0.1:8168\n  verify_signatures: never\n' > "$HOME/.podium/sync.yaml"
   ```

3. Load through the bridge and read its stderr.

   ```bash
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"stale"}}}'
   printf '%s\n%s\n' "$INIT" "$LOAD" | podium-mcp 2>"$WORK/mcp.log" | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded")'
   cat "$WORK/mcp.log"
   ```

   **Expect.** The load prints `loaded`. The stderr carries one line beginning
   `WARN: signature verification is off because defaults.verify_signatures is
   never in` and naming `$HOME/.podium/sync.yaml`.

4. Negative control: remove the line and load again.

   ```bash
   printf 'defaults:\n  registry: http://127.0.0.1:8168\n' > "$HOME/.podium/sync.yaml"
   printf '%s\n%s\n' "$INIT" "$LOAD" | podium-mcp; echo "exit=$?"
   ```

   **Expect.** The bridge exits non-zero naming
   `config.signature_provider_unavailable`, because under the `always` default
   it needs a verification key and the unsigned registry wrote none. A
   successful load means the `never` did not come from the file.

**Cleanup.** `kill "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S69: The registry refuses a row edited in its store

**Goal.** Validate that the registry's stored-row admission refuses a row whose
stored bytes, signature, or signing key no longer agree, to a direct HTTP reader
and to the bridge alike, while a revalidating consumer keeps serving the copy it
fetched through an admitted load, and that a batch load carries small resources
inline.

**Covers.** §13.4 stored-row admission, §7.6.2 batch resource references, and
the §6.5 revalidation path.

**Why by hand.** A unit test over the served-record assembly cannot tell a
refusal that reaches the bridge from one that reaches a direct HTTP reader, and
the edits run against the SQLite file a deployment holds.

**Steps.**

1. Run the isolation block.

2. Author a skill carrying three bundled resources: a small text file, a small
   binary file, and a file above 256 KB. Serve it with the default signing mode
   and load it through `curl` and through `podium-mcp`, then keep a copy of the
   cache the bridge's load wrote.

   ```bash
   podium artifact scaffold --type skill --description "Admission fixture" "$WORK/reg/admit" > /dev/null
   printf 'small text\n' > "$WORK/reg/admit/notes.md"
   head -c 1024 /dev/urandom > "$WORK/reg/admit/blob.bin"
   head -c 300000 /dev/urandom | base64 > "$WORK/reg/admit/large.txt"
   serve() { podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8169 > "$WORK/srv.log" 2>&1 &
     SRV=$!
     curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8169/healthz
     server_alive "$SRV" "$WORK/srv.log"; }
   stop() { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }
   export PODIUM_REGISTRY=http://127.0.0.1:8169
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"admit"}}}'
   load_http() { curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=admit" \
     | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("code") or "loaded", d.get("message", ""))'; }
   load_mcp() { printf '%s\n%s\n' "$INIT" "$LOAD" | PODIUM_CACHE_DIR="$1" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded")'; }
   serve
   load_http
   load_mcp "$WORK/cache"
   cp -R "$WORK/cache" "$WORK/cache-warm"
   ```

   **Expect.** Both loads print `loaded`.

3. Read the batch load's resource references while the row is unedited.

   ```bash
   curl -s -X POST "$PODIUM_REGISTRY/v1/artifacts:batchLoad" -H 'Content-Type: application/json' \
     -d '{"ids":["admit"]}' | python3 -c '
   import sys, json
   for r in json.load(sys.stdin)[0]["resources"]:
       print(r["path"], "inline" in r, r.get("inline_base64", False), "presigned_url" in r)'
   ```

   **Expect.** `notes.md True False False`, whose `inline` equals the file's
   bytes; `blob.bin True True False`; and `large.txt False False True`, which
   also carries a `content_hash`. A small resource carrying a `presigned_url`
   means the batch path presigns a resource admission never read.

4. Stop the server, keep the row's stored `frontmatter` and `signature` in a
   side table, and append a byte to the stored `frontmatter`. The stored
   `content_hash` is unchanged. Restart and load through `curl`, through the
   bridge with a new empty cache, and through the bridge with a fresh copy of
   the warm cache. Restore the row.

   ```bash
   stop
   sqlite3 "$PODIUM_SQLITE_PATH" "create table s69_keep as select frontmatter, signature from manifests where artifact_id='admit';"
   sqlite3 "$PODIUM_SQLITE_PATH" "update manifests set frontmatter = frontmatter || ' ' where artifact_id='admit';"
   serve
   load_http
   load_mcp "$(mktemp -d "$WORK/cache.XXXXXX")"
   W="$(mktemp -d "$WORK/warm.XXXXXX")"; cp -R "$WORK/cache-warm/." "$W"; load_mcp "$W"
   stop
   sqlite3 "$PODIUM_SQLITE_PATH" "update manifests set frontmatter = (select frontmatter from s69_keep) where artifact_id='admit';"
   ```

   **Expect.** The `curl` and the empty-cache loads print
   `materialize.content_hash_mismatch`, each naming only the artifact. The
   warm-cache load prints `loaded`: the default `always-revalidate` bridge sends
   a HEAD, the registry answers the unchanged stored hash without admission,
   and the bridge serves the copy it fetched through the first, admitted load.

5. Clear the stored `signature` and repeat the three loads. Restore the row.

   ```bash
   sqlite3 "$PODIUM_SQLITE_PATH" "update manifests set signature = '' where artifact_id='admit';"
   serve
   load_http
   load_mcp "$(mktemp -d "$WORK/cache.XXXXXX")"
   W="$(mktemp -d "$WORK/warm.XXXXXX")"; cp -R "$WORK/cache-warm/." "$W"; load_mcp "$W"
   stop
   sqlite3 "$PODIUM_SQLITE_PATH" "update manifests set signature = (select signature from s69_keep) where artifact_id='admit';"
   ```

   **Expect.** The `curl` and the empty-cache loads print
   `materialize.signature_missing`. The warm-cache load prints `loaded`.

6. Restart under a freshly generated key file, load, then restart under the
   original key and load again.

   ```bash
   PODIUM_SIGN_KEY_PATH="$WORK/fresh.key" serve
   load_http
   load_mcp "$(mktemp -d "$WORK/cache.XXXXXX")"
   stop
   serve
   load_http
   load_mcp "$(mktemp -d "$WORK/cache.XXXXXX")"
   ```

   **Expect.** Under the fresh key both loads print
   `materialize.signature_invalid`, and under the original key both print
   `loaded`. The fresh key is generated because the start that runs after the
   first-start rewrite has completed does not refuse a missing key file, which
   is the lost-key case the operator guide describes.

**Why each edit.** The first edit catches a registry that checks the signature
and skips the hash, the second one that trusts the `signature` column's
absence, and the third a registry that skips the signature check. Each refusal
is read through a direct HTTP reader as well as through the bridge, which
catches a refusal that reaches one and not the other. The warm-cache loads
record that a revalidating consumer keeps serving its admitted copy while the
stored hash is unchanged, so a refusal is observed through a full load.

**Cleanup.** `stop` then `rm -rf "$WORK"`.

---

## S70: The hidden parent does not appear in any response body

**Goal.** Validate that a caller who cannot see the layer contributing an
`extends:` parent finds the parent's ID and layer in no response body the
registry serves for the merged child, and that a chain failure names only the
child.

**Covers.** §4.6 hidden parents (withheld), §4.7.3 reverse-index visibility,
§7.2 `extends_pin`, and §7.6.2 batch loading.

**Why by hand.** The check reads raw response bytes across every read surface.
A parsed-field assertion passes while the ID sits in another field, and each
surface has leaked the parent at least once through a field no assertion read.

**Steps.**

1. Run the isolation block.

2. Build a two-layer registry. The lower layer holds the parent and is visible
   to bob alone. The upper layer holds the merged child and is visible to alice
   and bob. carol is seeded as the tenant admin for the last step. The
   `trusted-headers` provider takes the caller from the `X-Podium-User-Sub`
   header, so no token is minted.

   ```bash
   mkdir -p "$WORK/lower/shared/base" "$WORK/upper/team/derived"
   cat > "$WORK/lower/shared/base/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 1.0.0
   description: the base context
   tags: [from-base]
   ---

   base prose
   EOF
   cat > "$WORK/upper/team/derived/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 2.0.0
   description: the derived context
   extends: shared/base@1.x
   ---

   derived prose
   EOF
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: shared-base
         source: { local: { path: $WORK/lower } }
         visibility: { users: [bob@acme.com] }
       - id: team-derived
         source: { local: { path: $WORK/upper } }
         visibility: { users: [alice@acme.com, bob@acme.com] }
   YAML
   PODIUM_IDENTITY_PROVIDER=trusted-headers PODIUM_BOOTSTRAP_ADMINS=carol@acme.com \
     podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" \
     --bind 127.0.0.1:8170 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8170/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8170
   as() { who="$1"; shift; curl -s -H "X-Podium-User-Sub: $who@acme.com" "$@"; }
   ```

3. Confirm the control is switched on. bob can see both layers, so his load of
   the child carries the parent pin and his reverse-index query on the parent
   returns the child's edge.

   ```bash
   as bob "$URL/v1/load_artifact?id=team/derived" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("extends_pin"))'
   as bob "$URL/v1/dependents?id=shared/base" | python3 -c 'import sys,json; print(json.load(sys.stdin)["edges"])'
   ```

   **Expect.** `shared/base@1.0.0`, then one edge from `team/derived` to
   `shared/base` of kind `extends`. A `None` or an empty list means the
   registry resolved no parent for anyone, and every later step is vacuous.

4. Read every surface as alice and count the parent's ID and the parent
   layer's ID in the raw bytes. The child's ID is counted as well, so a surface
   that answered an error envelope does not pass the scan.

   ```bash
   scan() { printf '%-36s child=%s shared/base=%s shared-base=%s\n' "$1" \
     "$(grep -o 'team/derived' "$WORK/body" | wc -l | tr -d ' ')" \
     "$(grep -o 'shared/base' "$WORK/body" | wc -l | tr -d ' ')" \
     "$(grep -o 'shared-base' "$WORK/body" | wc -l | tr -d ' ')"; }
   for p in "/v1/load_artifact?id=team/derived" "/v1/search_artifacts?query=" \
            "/v1/search_artifacts?query=derived" "/v1/catalog" "/v1/sync/manifest"; do
     as alice "$URL$p" > "$WORK/body"; scan "$p"
   done
   as alice "$URL/v1/dependents?id=team/derived" > "$WORK/body"; scan "/v1/dependents?id=team/derived"
   as alice -X POST -H 'Content-Type: application/json' -d '{"ids":["team/derived"]}' \
     "$URL/v1/artifacts:batchLoad" > "$WORK/body"; scan "/v1/artifacts:batchLoad"
   ```

   **Expect.** Every line reads `shared/base=0 shared-base=0`. Every line except
   the `/v1/dependents` one reads `child=` with a count of at least 1; the
   dependents body for the child is an empty edge list, because nothing
   depends on the child.

5. Query the reverse index on the parent as alice.

   ```bash
   as alice -w '%{http_code}\n' "$URL/v1/dependents?id=shared/base"
   as alice "$URL/v1/dependents?id=no/such-artifact"
   ```

   **Expect.** Status `200` with a body that parses to `{"edges": []}`, and the
   same body for the artifact that does not exist. A `403`, a `404`, or an edge
   list naming `team/derived` distinguishes the parent from an absent ID.

6. Read alice's load response keys.

   ```bash
   as alice "$URL/v1/load_artifact?id=team/derived" | python3 -c 'import sys,json; print(sorted(json.load(sys.stdin)))'
   ```

   **Expect.** The list carries `delivery_hash` and `delivery_signature` and
   carries no `extends_pin`, `raw_frontmatter`, `manifest_merged`, or
   `signature`.

7. Unregister the parent's layer as carol and load the child as alice again.

   ```bash
   as carol -X DELETE -w ' %{http_code}\n' "$URL/v1/layers?id=shared-base"
   as alice -w ' %{http_code}\n' "$URL/v1/load_artifact?id=team/derived"
   ```

   **Expect.** The unregister answers `200`. The load answers `404` with a
   `registry.not_found` envelope whose `message` is
   `registry.not_found: team/derived`. A message naming `shared/base` discloses
   the parent through the chain failure.

**Cleanup.** `kill "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S71: An `extends:` child verifies through the MCP bridge and materializes

**Goal.** Validate that the compiled `podium-mcp` recomputes the delivery hash
of a merged child served to a caller who cannot see the parent, verifies the
delivery signature, and materializes the merged bytes, and that one byte of the
served frontmatter altered in transit fails the load before anything is
written.

**Covers.** §4.7.10 delivery attestation, §6.6 step 2, the §6.9
delivery-hash mismatch row, and §7.2 integrity fields.

**Why by hand.** The refusal needs a response one process composed and another
process rejected, which no in-process test provides.

**Steps.**

1. Run the isolation block.

2. Build the two-layer registry from S70 step 2, without `PODIUM_BOOTSTRAP_ADMINS`,
   and serve it on its own port with the default signing mode.

   ```bash
   mkdir -p "$WORK/lower/shared/base" "$WORK/upper/team/derived"
   cat > "$WORK/lower/shared/base/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 1.0.0
   description: the base context
   tags: [from-base]
   ---

   base prose
   EOF
   cat > "$WORK/upper/team/derived/ARTIFACT.md" <<'EOF'
   ---
   type: context
   version: 2.0.0
   description: the derived context
   extends: shared/base@1.x
   ---

   derived prose
   EOF
   cat > "$WORK/registry.yaml" <<YAML
   registry:
     layers:
       - id: shared-base
         source: { local: { path: $WORK/lower } }
         visibility: { users: [bob@acme.com] }
       - id: team-derived
         source: { local: { path: $WORK/upper } }
         visibility: { users: [alice@acme.com, bob@acme.com] }
   YAML
   PODIUM_IDENTITY_PROVIDER=trusted-headers \
     podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" \
     --bind 127.0.0.1:8171 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8171/healthz
   server_alive "$SRV" "$WORK/srv.log"
   ```

3. Start two gateways in front of the registry. `podium-mcp` sends no identity
   headers, so each gateway stamps `X-Podium-User-Sub: alice@acme.com` on every
   request, as the gateway of a `trusted-headers` deployment does. The second
   gateway also replaces one byte of the `frontmatter` field in each
   `load_artifact` response, which is the in-transit alteration.

   ```bash
   cat > "$WORK/gateway.py" <<'PY'
   import http.server, json, os, sys, urllib.error, urllib.request

   UPSTREAM, USER, TAMPER = os.environ["UPSTREAM"], os.environ["AS_USER"], os.environ.get("TAMPER") == "1"
   DROP = {"host", "content-length", "accept-encoding", "connection"}

   class Gateway(http.server.BaseHTTPRequestHandler):
       def forward(self):
           n = int(self.headers.get("Content-Length") or 0)
           req = urllib.request.Request(UPSTREAM + self.path, data=self.rfile.read(n) if n else None, method=self.command)
           for k, v in self.headers.items():
               if k.lower() not in DROP and not k.lower().startswith("x-podium-user-"):
                   req.add_header(k, v)
           req.add_header("X-Podium-User-Sub", USER)
           try:
               resp = urllib.request.urlopen(req)
           except urllib.error.HTTPError as err:
               resp = err
           data = resp.read()
           if TAMPER and self.command == "GET" and self.path.startswith("/v1/load_artifact") and resp.status == 200:
               doc = json.loads(data)
               doc["frontmatter"] = doc["frontmatter"].replace("derived", "derivee", 1)
               data = json.dumps(doc).encode()
           self.send_response(resp.status)
           for k, v in resp.headers.items():
               if k.lower() not in ("content-length", "transfer-encoding", "connection"):
                   self.send_header(k, v)
           self.send_header("Content-Length", str(len(data)))
           self.end_headers()
           if self.command != "HEAD":
               self.wfile.write(data)

       do_GET = do_HEAD = do_POST = do_DELETE = forward

       def log_message(self, *args):
           pass

   http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Gateway).serve_forever()
   PY
   UPSTREAM=http://127.0.0.1:8171 AS_USER=alice@acme.com python3 "$WORK/gateway.py" 8172 &
   GW=$!
   UPSTREAM=http://127.0.0.1:8171 AS_USER=alice@acme.com TAMPER=1 python3 "$WORK/gateway.py" 8173 &
   GWT=$!
   curl -s --retry 20 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8172/healthz
   curl -s --retry 20 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8173/healthz
   ```

4. Read the raw response through the unaltering gateway.

   ```bash
   curl -s "http://127.0.0.1:8172/v1/load_artifact?id=team/derived" | python3 -c '
   import sys, json
   d = json.load(sys.stdin)
   print("delivery_hash:", d.get("delivery_hash", "")[:14], "delivery_signature set:", bool(d.get("delivery_signature")))
   print("removed fields present:", sorted(k for k in ("raw_frontmatter", "manifest_merged", "signature") if k in d))
   print("extends_pin present:", "extends_pin" in d)'
   ```

   **Expect.** `delivery_hash: sha256:` followed by hex, `delivery_signature
   set: True`, `removed fields present: []`, and `extends_pin present: False`.

5. Load the child through the bridge via the unaltering gateway, with the
   registry-managed verifier and the registry's public key.

   ```bash
   PUBKEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"team/derived"}}}'
   load_mcp() { printf '%s\n%s\n' "$INIT" "$LOAD" \
     | PODIUM_REGISTRY="$1" PODIUM_HARNESS=none PODIUM_MATERIALIZE_ROOT="$2" \
       PODIUM_CACHE_DIR="$(mktemp -d "$WORK/cache.XXXXXX")" \
       PODIUM_VERIFY_SIGNATURES=always PODIUM_SIGNATURE_PROVIDER=registry-managed \
       PODIUM_SIGNATURE_VERIFY_KEY="$PUBKEY" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded " + r["content_hash"])'; }
   load_mcp http://127.0.0.1:8172 "$WORK/out"
   cat "$WORK/out/team/derived/ARTIFACT.md"
   grep -rl "shared/base" "$WORK/out" || echo "no parent ID on disk"
   ```

   **Expect.** `loaded sha256:...`. The materialized `ARTIFACT.md` carries the
   child's `description: the derived context` and the inherited `from-base`
   tag, and the last line reads `no parent ID on disk`.

6. Load the child through the altering gateway into a new destination.

   ```bash
   load_mcp http://127.0.0.1:8173 "$WORK/out-altered"
   ls -A "$WORK/out-altered" 2>&1
   ```

   **Expect.** An error beginning
   `materialize.content_hash_mismatch: recomputed delivery hash`, and `ls`
   reports that `$WORK/out-altered` does not exist. A `loaded` line means the
   bridge materialized bytes the registry did not attest. Step 5 is the
   control that shows the same bridge, key, and registry load the unaltered
   record.

**Cleanup.** `kill "$GW" "$GWT" "$SRV"; wait "$SRV"` then `rm -rf "$WORK"`.

---

## S72: The artifact viewer's extends rail matches the caller's view

**Goal.** Validate that the web UI draws the outbound extends rail from the
served `extends_pin`, so a caller who cannot see the parent reads the same rail
as for an artifact that extends nothing, and makes no scoped catalog request
that would decide the question in the browser.

**Covers.** §13.10 artifact viewer, §4.6 hidden parents (withheld), and §7.2
`extends_pin`.

**Why by hand.** The assertion is what a reader sees rendered and which
requests the browser sends, and no Go test observes either.

**Prerequisites.** A desktop browser that can run two separate profiles.

**Steps.**

1. Run the isolation block.

2. Build and serve the two-layer registry from S71 step 2 with the web UI
   mounted, on port 8174 instead of 8171, by adding `--web-ui` to the
   `podium serve` line. Record its PID in `SRV`, and run
   `server_alive "$SRV" "$WORK/srv.log"` after the `/healthz` poll.

3. Write `gateway.py` as in S71 step 3, and start one unaltering gateway per
   identity. Each port is a separate browser origin.

   ```bash
   UPSTREAM=http://127.0.0.1:8174 AS_USER=alice@acme.com python3 "$WORK/gateway.py" 8175 &
   GWA=$!
   UPSTREAM=http://127.0.0.1:8174 AS_USER=bob@acme.com python3 "$WORK/gateway.py" 8176 &
   GWB=$!
   curl -s --retry 20 --retry-delay 1 --retry-all-errors -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8175/app/
   ```

   **Expect.** `200`.

4. In a browser profile used for nothing else, open the network tab, then open
   `http://127.0.0.1:8175/app/#/artifact/team%2Fderived`.

   **Expect.** The viewer shows `team/derived` and its relations rail reads
   "This artifact extends nothing." The network tab lists no request whose URL
   includes `/v1/catalog?scope=`. The unscoped `/v1/catalog` request the
   sidebar footer makes on every route is not part of the check.

5. In a second browser profile, open the network tab, then open
   `http://127.0.0.1:8176/app/#/artifact/team%2Fderived`.

   **Expect.** The relations rail shows the chip `shared/base@1.0.0`, drawn
   from the served `extends_pin`, and following it opens `shared/base`. This is
   the control that shows the rail draws a parent when the caller can see one.
   A rail reading "This artifact extends nothing." for bob means the viewer no
   longer reads `extends_pin`, and step 4 then proves nothing.

**Cleanup.** Close both browser profiles, `kill "$GWA" "$GWB" "$SRV"; wait "$SRV"`,
then `rm -rf "$WORK"`.


---

## S73: A key rotation keeps every stored row loadable

**Goal.** Validate that `podium admin signing-key rotate` keeps the previous
key on a `verify:` line, that a rotated registry admits every row the previous
key signed, that a consumer holding the printed list verifies across the
rotation, and that `sign-stored-rows` re-signs the stored rows so the retired
key can be removed.

**Covers.** §4.7.9 verification key set, `key_id`, and rotation, the §13.4
`sign-stored-rows` command, and the §6.2 list form of
`PODIUM_SIGNATURE_VERIFY_KEY`.

**Why by hand.** The surfaces under test are the rotate output an operator
copies into consumer configuration, the key file, and the summary line that
decides when a key is removed. The automated suites assert on parsed values
rather than on the text an operator reads.

**Steps.**

1. Run the isolation block.

2. Generate the key file, serve one skill on it, and load it through the
   bridge under the original key.

   ```bash
   podium admin signing-key generate --key-file "$PODIUM_SIGN_KEY_PATH"
   OLD_KEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   podium artifact scaffold --type skill --description "Rotation skill" "$WORK/reg/rotated" > /dev/null
   serve() { podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
       --bind 127.0.0.1:8177 > "$WORK/srv$1.log" 2>&1 & SRV=$!
     curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8177/healthz
     server_alive "$SRV" "$WORK/srv$1.log"; }
   stop() { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }
   serve 1
   export PODIUM_REGISTRY=http://127.0.0.1:8177
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"rotated"}}}'
   load_mcp() { printf '%s\n%s\n' "$INIT" "$LOAD" \
     | PODIUM_SIGNATURE_VERIFY_KEY="$1" PODIUM_CACHE_DIR="$(mktemp -d "$WORK/cache.XXXXXX")" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded")'; }
   load_mcp "$OLD_KEY"
   ```

   **Expect.** `generate` prints one base64 key and a
   `key_id=<16 hex digits> role=signing` line, and the load prints `loaded`.

3. Stop the registry, rotate the key file, and restart.

   ```bash
   stop
   podium admin signing-key rotate --key-file "$PODIUM_SIGN_KEY_PATH" | tee "$WORK/rotate.out"
   grep -c '^verify:' "$PODIUM_SIGN_KEY_PATH"
   KEY_SET="$(head -1 "$WORK/rotate.out")"
   NEW_KEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   serve 2
   ```

   **Expect.** The rotate output's first line holds two comma-separated base64
   keys, the second of which is `$OLD_KEY`, followed by a `role=signing` line
   and a `role=verify` line whose `key_id` matches step 2's. `grep -c` prints
   `1`.

4. Load through the bridge with the printed list, then with the old key alone.

   ```bash
   load_mcp "$KEY_SET"
   load_mcp "$OLD_KEY"
   ```

   **Expect.** The list-configured load prints `loaded`, and the old-key-only
   load prints an error beginning with `materialize.signature_invalid`,
   because the registry now signs every response under the new key. A
   `materialize.signature_invalid` on the first load means the rotated
   registry refused its own pre-rotation row.

5. Re-sign the stored rows while the registry serves.

   ```bash
   podium admin sign-stored-rows; echo "exit=$?"
   ```

   **Expect.** The summary prints `rehash: 1 rewritten, ...`, `rehash: 0
   unsigned left`, and `rehash: verify key <old key_id>: 0 row(s) still signed
   under it`, followed by `exit=0`.

6. Remove the `verify:` line, restart, and load with the new key alone.

   ```bash
   stop
   sed -i.bak '/^verify:/d' "$PODIUM_SIGN_KEY_PATH"
   serve 3
   load_mcp "$NEW_KEY"
   ```

   **Expect.** The load prints `loaded`. A `materialize.signature_invalid`
   means the row was still signed under the removed key, which step 5's zero
   count should have ruled out.

**Cleanup.** `stop` then `rm -rf "$WORK"`.

---

## S74: A cached pre-rotation delivery signature recovers

**Goal.** Validate that a `podium-mcp` cache holding a delivery signature made
under a key the consumer no longer trusts recovers through a refetch in the
default cache mode and in `offline-first`, and that `offline-only` refuses the
record until its cache is refilled.

**Covers.** The §6.5 cache-miss rule for a failing cached record, §4.7.10
rotation and cached delivery signatures, and the §7.4 cache modes.

**Why by hand.** The access log is the only place that shows how many requests
a load made, and the recovery path is only distinguishable from a stuck cache
by that count.

**Steps.**

1. Run the isolation block.

2. Generate the key file, serve one skill, and load it once in the default
   cache mode so the cache holds a delivery pair signed under the old key.

   ```bash
   podium admin signing-key generate --key-file "$PODIUM_SIGN_KEY_PATH" > /dev/null
   OLD_KEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   podium artifact scaffold --type skill --description "Cached skill" "$WORK/reg/cached" > /dev/null
   serve() { podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
       --bind 127.0.0.1:8178 > "$WORK/srv$1.log" 2>&1 & SRV=$!
     curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8178/healthz
     server_alive "$SRV" "$WORK/srv$1.log"; }
   stop() { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }
   serve 1
   export PODIUM_REGISTRY=http://127.0.0.1:8178
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"cached"}}}'
   load_mcp() { printf '%s\n%s\n' "$INIT" "$LOAD" \
     | PODIUM_SIGNATURE_VERIFY_KEY="$1" PODIUM_CACHE_DIR="$2" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(r.get("error") or "loaded")'; }
   loads() { grep -c 'access op=load_artifact status=200' "$WORK/srv2.log"; }
   load_mcp "$OLD_KEY" "$WORK/cache"
   ```

   **Expect.** `loaded`.

3. Rotate, re-sign the stored rows, drop the old key from the key file,
   keep a copy of the stale cache, and restart.

   ```bash
   stop
   podium admin signing-key rotate --key-file "$PODIUM_SIGN_KEY_PATH" > /dev/null
   podium admin sign-stored-rows > "$WORK/sign.out"; echo "exit=$?"
   grep 'still signed' "$WORK/sign.out"
   sed -i.bak '/^verify:/d' "$PODIUM_SIGN_KEY_PATH"
   NEW_KEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   cp -R "$WORK/cache" "$WORK/cache-stale"
   serve 2
   ```

   **Expect.** `exit=0`, then `rehash: verify key <old key_id>: 0 row(s)
   still signed under it`. Remove the `verify:` line only when both hold: a
   run that exits non-zero leaves the count incomplete, so stop and repair
   what its log names instead of continuing.

4. Load with the new key alone against the stale cache, counting the
   registry's load requests before and after.

   ```bash
   loads
   load_mcp "$NEW_KEY" "$WORK/cache"
   loads
   ```

   **Expect.** The counts read `0` and then `2`, and the load prints
   `loaded`. The two requests are the revalidation and the unconditional
   refetch, which the access log does not tell apart by method. A
   `materialize.signature_invalid` means the bridge served the stale pair
   instead of treating it as a cache miss.

5. Load again against the refreshed cache.

   ```bash
   load_mcp "$NEW_KEY" "$WORK/cache"
   loads
   ```

   **Expect.** `loaded`, and the count reads `3`: the revalidation alone.

6. Load in `offline-only` against the copy of the stale cache.

   ```bash
   PODIUM_CACHE_MODE=offline-only load_mcp "$NEW_KEY" "$WORK/cache-stale"
   loads
   ```

   **Expect.** An error beginning with `materialize.signature_invalid`, and
   the count still reads `3`.

7. Refill the stale copy in `offline-first`, then load it in `offline-only`
   again.

   ```bash
   PODIUM_CACHE_MODE=offline-first load_mcp "$NEW_KEY" "$WORK/cache-stale"
   loads
   PODIUM_CACHE_MODE=offline-only load_mcp "$NEW_KEY" "$WORK/cache-stale"
   loads
   ```

   **Expect.** Both loads print `loaded`. The `offline-first` load raises the
   count to `4`, and the `offline-only` load leaves it at `4`. An
   `offline-only` load that still fails means the refill step the rotation
   procedure gives does not recover an `offline-only` consumer.

**Cleanup.** `stop` then `rm -rf "$WORK"`.

---

## S75: `podium-server sign-stored-rows` refuses without a key and never writes one

**Goal.** Validate that `sign-stored-rows` refuses `--include-unsigned` when
signing is off, refuses when signing is on and the key file is absent, writes
no key file, and that `podium admin signing-key`
refuses to run without `--key-file` instead of resolving the operator's
personal key.

**Covers.** The §13.4 `sign-stored-rows` refusals, including the
`--include-unsigned` refusal with signing off, §13.12
`PODIUM_SIGN_KEY_PATH`, and the `--key-file` requirement of
`podium admin signing-key`.

**Why by hand.** The claim is about what the command leaves on disk and what
an operator reads on stderr, and a bare `rotate` on an operator's shell would
reach `~/.podium/standalone/registry-signing.key`.

**Steps.**

1. Run the isolation block. It names `$WORK/registry-signing.key`, which does
   not exist yet.

2. Run a `--include-unsigned` dry run with signing off.

   ```bash
   PODIUM_SIGN=none podium-server sign-stored-rows --include-unsigned --dry-run; echo "exit=$?"
   ls "$PODIUM_SIGN_KEY_PATH"
   ```

   **Expect.** The message begins with
   `config.signature_provider_unavailable: sign-stored-rows: --include-unsigned`,
   the status is `exit=1`, and `ls` reports that the key file does not exist.

3. Run it with signing on and the key file absent.

   ```bash
   podium-server sign-stored-rows; echo "exit=$?"
   ls "$PODIUM_SIGN_KEY_PATH"
   ```

   **Expect.** The message begins with `config.signature_provider_unavailable`
   and names the key path, the status is `exit=1`, and `ls` still reports that
   the key file does not exist. A key file here means the command generated a
   key.

4. Run `rotate` with no `--key-file`.

   ```bash
   podium admin signing-key rotate; echo "exit=$?"
   ls "$HOME/.podium/standalone/registry-signing.key"
   ```

   **Expect.** `error: --key-file is required` followed by the usage text, the
   status is `exit=2`, and `ls` reports that the file under the scenario's
   `HOME` does not exist.

**Cleanup.** `rm -rf "$WORK"`.

---

## S76: Chart upgrade from v0.4.0

**Goal.** Validate that the chart's upgrade procedure in
`docs/deployment/clustered.md` moves a clustered v0.4.0 install onto this
release with no registry pod serving an unmigrated store, that the run Job
refuses a plan other than the reviewed one, and that a registry pod refuses to
start over an unmigrated store in either signing mode.

**Covers.** The §13.4 stop precondition and rewrite as the chart's migrate Job
runs them, the chart's values-only migration guards, the §13.4 plan digest the
run Job passes, the §13.4 boot refusal as a serving rollout meets it, the
`post-install` hook on a reinstall over a restored store, the signing-off
migrate Job, and a fresh install with no acknowledgement.

**Why by hand.** `test/chart/migrate_render_test.go` covers what `helm template`
renders and every refusal that needs no cluster. The hook Job's lifecycle, the
exit status of a refused run, and the registry's behavior over rows v0.4.0
wrote appear only on a cluster. `TestChart_KindUpgradeFromV040` automates this
scenario under `make test-live-kind`; run the scenario by hand to read each
refusal and each log line as an operator does.

**Prerequisites.** `helm` v4, `kubectl`, `kind`, `git`, and a working Docker
daemon. When any is absent, skip and record the skip. The shell stays in
`$WORK`, as the isolation block leaves it. Step 1 names the repository in
`REPO` and the chart in `CHART`, and links `$WORK/deploy` to the repository's
`deploy` directory. The procedure's commands in `docs/deployment/clustered.md`
name the chart as `./deploy/helm/podium` and read and write
`podium-values.yaml`, `dry-run.log`, and `run.log` in the working directory, so
they run verbatim from `$WORK`, and every file the scenario writes stays there.
The isolation block's `HOME` also keeps the kind cluster's kubeconfig under
`$WORK`.

**Steps.**

1. Build both images and the CLI, generate the signing key, and create the
   cluster.

   ```bash
   REPO="$REAL_HOME/projects/podium"
   CHART="$REPO/deploy/helm/podium"
   ln -s "$REPO/deploy" "$WORK/deploy"
   git -C "$REPO" worktree add --detach "$WORK/v040" v0.4.0
   docker build -t podium-live:current "$REPO"
   docker build -t podium-live:v0.4.0 "$WORK/v040"
   go -C "$REPO" build -o "$WORK/podium" ./cmd/podium
   "$WORK/podium" admin signing-key generate --key-file "$WORK/registry-signing.key"
   kind create cluster --name podium-s76 --wait 180s
   kind load docker-image podium-live:current podium-live:v0.4.0 --name podium-s76
   arch="$(docker version --format '{{.Server.Arch}}')"
   for image in pgsty/minio:RELEASE.2026-08-04T00-00-00Z pgsty/mc:RELEASE.2026-09-16T00-00-00Z \
       pgvector/pgvector:pg16 postgres:17-alpine busybox:1.36; do
     docker image inspect "$image" >/dev/null 2>&1 || docker pull --platform "linux/$arch" "$image"
     docker save --platform "linux/$arch" -o "$WORK/image.tar" "$image"
     kind load image-archive "$WORK/image.tar" --name podium-s76
   done
   docker exec podium-s76-control-plane crictl images
   ```

   The loop loads the third-party images as single-platform archives and pulls
   only an image the host lacks, because Docker Hub refuses the pinned MinIO
   tags. When a pull is refused, obtain the image another way and rerun the
   loop. A pull from inside the kind node can be refused by Docker Hub, and
   `kind load docker-image` fails on a multi-platform image under Docker's
   containerd image store with `content digest ... not found`.

   **Expect.** Both builds succeed, `generate` prints one key and a
   `key_id=... role=signing` line, and the node reports `Ready`. `kind load
   image-archive` prints nothing when it succeeds, so read the loads from
   `crictl images`, which lists both `podium-live` tags and each third-party
   image at its pinned tag.

2. Deploy Postgres and MinIO, create the bucket and the Secrets, and seed the
   store with v0.4.0. MinIO keeps its data on a claim, because step 10 scales
   it to zero. The seed runs `podium-live:v0.4.0` as a standalone pod with
   `PODIUM_LAYER_PATH` on a ConfigMap layer that holds a small skill and a
   skill with a bundled resource of about 350 KB, above the 256 KB inline
   cutoff. The files below are the manifests and the layer that
   `backingManifest`, `seedPodManifest`, and `seedConfigMap` build in
   `test/chart/kind_upgrade_test.go`, with the namespace left to the current
   context so steps 15 and 16 reuse them.

   ```bash
   cat > "$WORK/backing.yaml" <<'YAML'
   apiVersion: apps/v1
   kind: Deployment
   metadata: {name: pg}
   spec:
     selector: {matchLabels: {app: pg}}
     template:
       metadata: {labels: {app: pg}}
       spec:
         containers:
           - name: pg
             image: pgvector/pgvector:pg16
             env:
               - {name: POSTGRES_USER, value: podium}
               - {name: POSTGRES_PASSWORD, value: podium}
               - {name: POSTGRES_DB, value: podium}
               - {name: PGDATA, value: /tmp/pgdata}
             ports: [{containerPort: 5432}]
   ---
   apiVersion: v1
   kind: Service
   metadata: {name: pg}
   spec:
     selector: {app: pg}
     ports: [{port: 5432}]
   ---
   apiVersion: v1
   kind: PersistentVolumeClaim
   metadata: {name: minio-data}
   spec:
     accessModes: [ReadWriteOnce]
     resources: {requests: {storage: 1Gi}}
   ---
   apiVersion: apps/v1
   kind: Deployment
   metadata: {name: minio}
   spec:
     strategy: {type: Recreate}
     selector: {matchLabels: {app: minio}}
     template:
       metadata: {labels: {app: minio}}
       spec:
         containers:
           - name: minio
             image: pgsty/minio:RELEASE.2026-08-04T00-00-00Z
             args: [server, /data]
             env:
               - {name: MINIO_ROOT_USER, value: minioadmin}
               - {name: MINIO_ROOT_PASSWORD, value: minioadmin}
             ports: [{containerPort: 9000}]
             readinessProbe: {httpGet: {path: /minio/health/ready, port: 9000}}
             volumeMounts: [{name: data, mountPath: /data}]
         volumes: [{name: data, persistentVolumeClaim: {claimName: minio-data}}]
   ---
   apiVersion: v1
   kind: Service
   metadata: {name: minio}
   spec:
     selector: {app: minio}
     ports: [{port: 9000}]
   YAML
   cat > "$WORK/seed-pod.yaml" <<'YAML'
   apiVersion: v1
   kind: Pod
   metadata: {name: seed}
   spec:
     restartPolicy: Never
     containers:
       - name: seed
         image: podium-live:v0.4.0
         imagePullPolicy: IfNotPresent
         envFrom: [{secretRef: {name: podium-secrets}}]
         env:
           - {name: PODIUM_LAYER_PATH, value: /layer}
           - {name: PODIUM_IDENTITY_PROVIDER, value: ""}
           - {name: PODIUM_BIND, value: "0.0.0.0:8080"}
           - {name: PODIUM_REGISTRY_STORE, value: postgres}
           - {name: PODIUM_OBJECT_STORE, value: s3}
           - {name: PODIUM_VECTOR_BACKEND, value: none}
           - {name: PODIUM_EMBEDDING_PROVIDER, value: none}
           - {name: HOME, value: /tmp}
         readinessProbe: {httpGet: {path: /healthz, port: 8080}, periodSeconds: 2}
         volumeMounts:
           - {name: layer, mountPath: /layer/demo/hello/greet/SKILL.md, subPath: greet-skill, readOnly: true}
           - {name: layer, mountPath: /layer/demo/hello/greet/ARTIFACT.md, subPath: greet-artifact, readOnly: true}
           - {name: layer, mountPath: /layer/demo/hello/bigref/SKILL.md, subPath: bigref-skill, readOnly: true}
           - {name: layer, mountPath: /layer/demo/hello/bigref/ARTIFACT.md, subPath: bigref-artifact, readOnly: true}
           - {name: layer, mountPath: /layer/demo/hello/bigref/references/big.md, subPath: bigref-body, readOnly: true}
           - {name: tmp, mountPath: /tmp}
     volumes:
       - name: tmp
         emptyDir: {}
       - name: layer
         configMap:
           name: seed-layer
   YAML
   mkdir -p "$WORK/seed"
   for name in greet bigref; do
     printf -- '---\nname: %s\ndescription: A seeded skill for the chart upgrade test.\n---\n\nSeeded by the kind upgrade test.\n' \
       "$name" > "$WORK/seed/$name-skill"
     printf -- '---\ntype: skill\nversion: 1.0.0\nwhen_to_use:\n  - "When the upgrade test runs."\nsensitivity: low\n---\n\n<!-- Skill body lives in SKILL.md. -->\n' \
       > "$WORK/seed/$name-artifact"
   done
   yes 'The chart upgrade test stores this line in object storage.' | head -n 6000 > "$WORK/seed/bigref-body"
   ls -l "$WORK/seed"
   ```

   Each pod mount is a `subPath`, because a projected ConfigMap volume exposes
   its files through `..data` symlinks and v0.4.0's filesystem layer ingests
   none of them. The vector backend and the embedding provider are off because
   their defaults require an OpenAI key the seed does not have.

   Then define the helpers the later steps call, and run them. `mc_run` runs
   an `mc` script in a throwaway pod inside the cluster, as S46 step 3 and the
   kind test's `mc` helper do, with the alias `m` retried until MinIO answers,
   so the host needs no `mc`. `rows` prints each org schema's manifest row
   count and unsigned count. `backing` deploys the services, creates the
   bucket `podium`, and creates the `podium-secrets` Secret with the keys S46
   step 5 gives. `seed` creates the layer ConfigMap and the seed pod, waits
   until the store holds both rows, and deletes the pod. `kubectl create
   configmap` writes no `last-applied-configuration` annotation, which matters
   because the ConfigMap exceeds that annotation's 256 KiB limit. The signing
   key Secret holds step 1's `$WORK/registry-signing.key` rather than a key
   generated as S46 step 4 does, because step 12 verifies the served
   signatures against that file's public key.

   ```bash
   mc_run() { kubectl run "mc-$$-$RANDOM" --image=pgsty/mc:RELEASE.2026-09-16T00-00-00Z \
     --restart=Never --rm -i --quiet --command -- sh -c \
     "for i in \$(seq 1 30); do mc alias set m http://minio:9000 minioadmin minioadmin >/dev/null 2>&1 && break; sleep 2; done && $1"; }
   rows() {
     kubectl exec -i deploy/pg -- psql -U podium -d podium -At <<'SQL'
   SELECT format('SELECT count(*), count(*) FILTER (WHERE signature = %L) FROM %I.manifests', '', table_schema)
     FROM information_schema.tables WHERE table_name = 'manifests' \gexec
   SQL
   }
   backing() {
     kubectl apply --server-side -f "$WORK/backing.yaml"
     kubectl rollout status deployment/pg --timeout=5m
     kubectl rollout status deployment/minio --timeout=5m
     until kubectl exec deploy/pg -- pg_isready -U podium -d podium >/dev/null 2>&1; do sleep 2; done
     mc_run 'mc mb -p m/podium'
     kubectl create secret generic podium-secrets \
       --from-literal=PODIUM_POSTGRES_DSN="postgres://podium:podium@pg:5432/podium?sslmode=disable" \
       --from-literal=PODIUM_S3_BUCKET=podium \
       --from-literal=PODIUM_S3_ENDPOINT="http://minio:9000" \
       --from-literal=PODIUM_S3_REGION=us-east-1 \
       --from-literal=AWS_ACCESS_KEY_ID=minioadmin \
       --from-literal=AWS_SECRET_ACCESS_KEY=minioadmin
   }
   seed() {
     kubectl create configmap seed-layer --from-file="$WORK/seed"
     kubectl apply -f "$WORK/seed-pod.yaml"
     kubectl wait --for=condition=Ready pod/seed --timeout=5m
     until [ "$(rows | awk -F'|' '{n += $1} END {print n + 0}')" -ge 2 ]; do sleep 2; done
     kubectl delete pod seed --wait=true
   }
   backing
   kubectl create secret generic podium-signing-key \
     --from-file=registry-signing.key="$WORK/registry-signing.key"
   seed
   mc_run 'mc ls --recursive m/podium'
   rows
   ```

   **Expect.** `ls -l` lists `bigref-artifact`, `bigref-body` at about
   354 KB, `bigref-skill`, `greet-artifact`, and `greet-skill`. The rollouts
   complete, `mc mb` prints `Bucket created successfully`, both Secrets and
   the ConfigMap report `created`, and `kubectl wait` prints `pod/seed
   condition met`. `mc_run` lists at least one object, which is the large
   resource's body, and `rows` prints a line such as `2|2`: two rows, both
   unsigned.

3. Install v0.4.0 from the worktree and prepare the values file.

   ```bash
   helm install podium "$WORK/v040/deploy/helm/podium" --set image.repository=podium-live \
     --set image.tag=v0.4.0 --set replicaCount=2 --set config.identityProvider.type="" \
     --wait --timeout 10m
   kubectl get pods -l app.kubernetes.io/name=podium
   helm get values podium -o yaml > "$WORK/podium-values.yaml"
   helm history podium --max 1
   export HELM_MAX_HISTORY=0
   ```

   Edit `$WORK/podium-values.yaml`: set `image.tag` to `current`, and add
   `signing.secretName: podium-signing-key` and `migration.previousImage:
   podium-live:v0.4.0`. Keep the shell that exported
   `HELM_MAX_HISTORY` for the remaining steps, so no later upgrade prunes the
   v0.4.0 revision that step 14 rolls back to.

   **Expect.** Two pods report `1/1 Running`, and `helm history` lists
   revision 1.

4. Try a default upgrade, and define the functions the later steps read:
   `current_pod` prints the name of a registry pod on `podium-live:current`,
   and `record` prints the count of the §13.4 completion record.

   ```bash
   helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=2 \
     --wait --timeout 5m
   kubectl get pods -l app.kubernetes.io/name=podium \
     -o 'custom-columns=NAME:.metadata.name,IMAGE:.spec.containers[0].image,READY:.status.containerStatuses[0].ready,RESTARTS:.status.containerStatuses[0].restartCount'
   current_pod() { kubectl get pods -l app.kubernetes.io/name=podium \
     -o jsonpath='{range .items[?(@.spec.containers[0].image=="podium-live:current")]}{.metadata.name}{"\n"}{end}' \
     | head -n 1; }
   current_pod
   kubectl logs --previous "$(current_pod)"
   record() { kubectl exec deploy/pg -- psql -U podium -d podium -At -c \
     "SELECT count(*) FROM public.data_migrations WHERE name = 'content-hash-framing'"; }
   record
   ```

   **Expect.** The upgrade fails once `--timeout` passes. `current_pod` prints
   the name of the pod on `podium-live:current`, which restarts, and its
   `kubectl logs --previous` names `sign-stored-rows`. Both v0.4.0 pods still
   report Ready, and `record` prints `0`. A pod on `podium-live:current` that
   reports Ready means a registry served the unmigrated store.

5. Create a pod that carries the registry's labels and fails, wait until it
   has failed, then run step 2 of the procedure with the v0.4.0 tag kept.

   ```bash
   kubectl run evicted-look-alike --image=busybox:1.36 --restart=Never \
     --labels=app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium -- sh -c 'exit 1'
   kubectl wait pod/evicted-look-alike --for=jsonpath='{.status.phase}'=Failed --timeout=3m
   sed 's/tag: current/tag: v0.4.0/' "$WORK/podium-values.yaml" > "$WORK/old-values.yaml"
   helm upgrade podium "$CHART" -f "$WORK/old-values.yaml" --set replicaCount=0
   kubectl wait --for=delete pod --timeout=10m \
     -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
     --field-selector=status.phase!=Succeeded,status.phase!=Failed
   kubectl get pods -l app.kubernetes.io/name=podium
   kubectl delete pod evicted-look-alike
   ```

   **Expect.** The first wait prints `pod/evicted-look-alike condition met`
   once the pod reaches phase `Failed`. The delete wait returns once the
   registry pods are deleted, and `kubectl get pods` then lists only
   `evicted-look-alike`, with status `Error`. The last command deletes it, so
   the later pod listings show registry pods alone. A delete wait started
   while `evicted-look-alike` is still `Pending` or `Running` times out after
   10 minutes, because `kubectl wait --for=delete` applies its field selector
   once, when it starts, and then waits for every pod it matched; nothing
   deletes the look-alike. The first wait prevents that.

6. Repeat step 2 of the procedure with the new tag, then back up the store.

   ```bash
   helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0
   kubectl get deployment podium-podium
   kubectl exec deploy/pg -- pg_dump -U podium -d podium -Fc -f /tmp/step2.dump
   kubectl exec deploy/pg -- ls -l /tmp/step2.dump
   mc_run 'mc mb -p m/podium-backup && mc mirror --overwrite m/podium m/podium-backup'
   mc_run 'mc ls --recursive m/podium-backup'
   ```

   **Expect.** The Deployment shows `0/0`, `ls -l` lists the dump, and the
   last `mc_run` lists the bucket's objects. Step 14 restores from this dump
   and this mirror.

7. Run step 3 of the procedure from `$WORK`, exactly as
   `docs/deployment/clustered.md` gives it, then run its completeness check
   and read the digest from the last line.

   ```bash
   grep -E '^dry-run: plan digest ' "$WORK/dry-run.log"
   digest=$(sed -n 's/^dry-run: plan digest \(sha256:[0-9a-f]*\) .*/\1/p' "$WORK/dry-run.log")
   ```

   **Expect.** The `helm upgrade` notes name `podium-podium-migrate` and the
   `grep -E '^dry-run: plan digest '` command. Both seeded skills appear on
   lines carrying `write=true sign=true signed_by=unsigned stored=sha256:...`.
   The log carries a `dry-run: plan mode=` header line, and its last line is
   `dry-run: plan digest sha256:... over K row(s)`, whose `K` equals the
   number of row lines without `class=migrated`. The count of `class=` lines
   equals the planned count, and `record` prints `0`.

8. Run step 4 of the procedure with a wrong digest, then with the reviewed
   digest and a different unsigned-row setting. The function below is step 4
   with the digest and any extra `--set` flags as arguments, and it streams
   the run log to `$WORK/run.log`.

   ```bash
   run_step4() {
     kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
     helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0 \
       --set migration.mode=run --set migration.planDigest="$1" "${@:2}" --timeout 30m &
     helm_pid=$!
     until kubectl get job podium-podium-migrate >/dev/null 2>&1 || ! kill -0 "$helm_pid" 2>/dev/null; do
       sleep 2
     done
     if kubectl get job podium-podium-migrate >/dev/null 2>&1; then
       kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > "$WORK/run.log"
     fi
     wait "$helm_pid"
   }
   exit_code() { kubectl get pod -l job-name=podium-podium-migrate \
     -o jsonpath='{.items[0].status.containerStatuses[0].state.terminated.exitCode}'; echo; }
   run_step4 sha256:0000000000000000000000000000000000000000000000000000000000000000
   kubectl get job podium-podium-migrate; exit_code; grep -c '^plan: ' "$WORK/run.log"
   run_step4 "$digest" --set migration.includeUnsigned=false
   kubectl get job podium-podium-migrate; exit_code; grep -c '^plan: ' "$WORK/run.log"
   ```

   **Expect.** Each `helm upgrade` exits non-zero, the Job reports `Failed`,
   `exit_code` prints `3`, `run.log` carries `plan:` lines and `plan changed
   since the reviewed dry run`, and `record` prints `0`. The second run's
   `plan: plan mode=` line reads `include_unsigned=false`. A failed Job leaves
   the reviewed digest valid, because the plan is unchanged.

   Then plant an unsigned row after the review and run with the reviewed
   digest.

   ```bash
   kubectl exec -i deploy/pg -- psql -U podium -d podium -At -v ON_ERROR_STOP=1 <<'SQL'
   DO $$
   DECLARE s text;
   BEGIN
     FOR s IN SELECT table_schema FROM information_schema.tables WHERE table_name = 'manifests' LOOP
       EXECUTE format('CREATE TEMP TABLE planted AS SELECT * FROM %I.manifests WHERE artifact_id = %L LIMIT 1', s, 'demo/hello/greet');
       EXECUTE format('UPDATE planted SET version = %L, signature = %L', '9.9.9-planted', '');
       EXECUTE format('INSERT INTO %I.manifests SELECT * FROM planted', s);
       DROP TABLE planted;
     END LOOP;
   END $$;
   SQL
   run_step4 "$digest"
   exit_code; grep -E '^plan: .*@9\.9\.9-planted class=' "$WORK/run.log"; record
   kubectl exec -i deploy/pg -- psql -U podium -d podium -At <<'SQL'
   SELECT format('DELETE FROM %I.manifests WHERE version = %L', table_schema, '9.9.9-planted')
     FROM information_schema.tables WHERE table_name = 'manifests' \gexec
   SQL
   ```

   **Expect.** `exit_code` prints `3`, `grep` prints the `plan:` line for the
   planted row, and `record` prints `0`. A run that exits 0 here attested a
   row the reviewed dry run never listed. Rerun step 7 and use its fresh
   digest from here on.

9. Try to serve after only the dry run, then return the release to zero
   replicas.

   ```bash
   helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --wait --timeout 5m
   kubectl logs --previous "$(current_pod)"
   record
   helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0
   kubectl wait --for=delete pod --timeout=10m \
     -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
     --field-selector=status.phase!=Succeeded,status.phase!=Failed
   ```

   **Expect.** The serving upgrade fails once `--timeout` passes, its pods
   restart, the previous log of the pod `current_pod` names carries
   `sign-stored-rows`, and `record` prints `0`. The wait returns once the pods
   are deleted.

10. Stop MinIO and run step 4 with the digest from step 8's closing rerun.

    ```bash
    kubectl scale deployment/minio --replicas=0
    run_step4 "$digest"
    exit_code; grep 'plan changed' "$WORK/run.log"; grep -E '^plan: .* class=body_unavailable' "$WORK/run.log"
    record
    kubectl get deployment podium-podium
    ```

    **Expect.** The run upgrade exits non-zero, the Job reports `Failed`,
    `exit_code` prints `3`, the log names `plan changed`, `run.log` carries
    `plan:` lines with `class=body_unavailable`, `record` prints `0`, and the
    Deployment shows `0/0` with no pod. The refused run wrote nothing.

11. Restore MinIO and wait until it answers through its Service.

    ```bash
    kubectl scale deployment/minio --replicas=1
    kubectl rollout status deployment/minio --timeout=5m
    mc_run 'mc ls m/podium'
    ```

    The rollout can finish before the Service routes to the new pod, and a
    dry run started then holds the object-held row back as
    `class=body_unavailable`, so `mc_run` retries until MinIO answers. Rerun
    step 3 of the procedure from `$WORK` as step 7 does, then run step 4 with
    its fresh digest.

    ```bash
    digest=$(sed -n 's/^dry-run: plan digest \(sha256:[0-9a-f]*\) .*/\1/p' "$WORK/dry-run.log")
    run_step4 "$digest"; echo "exit=$?"
    exit_code; grep 'rehash: 0 unsigned left' "$WORK/run.log"
    record
    rows
    ```

    **Expect.** `mc_run` lists the bucket's objects, and the dry run lists no
    `class=body_unavailable` row. The run prints `exit=0`, `exit_code` prints
    `0`, `grep` prints the `rehash: 0 unsigned left` line, `record` prints
    `1`, and `rows` reports `2|0`.

12. Run step 5 as documented, and verify a seeded artifact.

    ```bash
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --wait --timeout 10m
    kubectl get pods -l app.kubernetes.io/name=podium
    kubectl logs -l app.kubernetes.io/name=podium --tail=-1 | grep ' rewritten, '
    kubectl port-forward svc/podium-podium 18180:8080 >/dev/null 2>&1 &
    PF=$!; sleep 4
    PODIUM_SIGNATURE_VERIFY_KEY="$(awk '$1=="public:"{print $2}' "$WORK/registry-signing.key")" \
      "$WORK/podium" verify demo/hello/bigref --registry http://127.0.0.1:18180
    kill $PF
    ```

    **Expect.** Two pods report `1/1 Running` with `0` restarts, `grep` finds
    no rewrite summary, and `verify` prints `verify ok`.

    Then rerun the migration command on a serving pod, with a dry run first
    and its plan digest passed to the run.

    ```bash
    kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server \
      sign-stored-rows --include-unsigned --dry-run | tee "$WORK/rerun.log"
    kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server \
      sign-stored-rows --include-unsigned \
      --plan-digest="$(sed -n 's/^dry-run: plan digest \(sha256:[0-9a-f]*\) .*/\1/p' "$WORK/rerun.log")"
    ```

    **Expect.** The dry run lists no `signed_by=unsigned` row. The second
    `kubectl exec` command exits 0 and reports `0 rewritten` with every row
    already migrated.

13. Upgrade to a second tag of the same image.

    ```bash
    docker tag podium-live:current podium-live:current2
    kind load docker-image podium-live:current2 --name podium-s76
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set image.tag=current2 \
      --wait --timeout 10m
    helm get hooks podium
    ```

    **Expect.** The pods roll to `current2`, and `helm get hooks` prints
    nothing.

14. Roll back through the backup: stop the release, recreate the `podium`
    database from step 6's dump, mirror the backup bucket back, and roll back
    to revision 1, the v0.4.0 revision step 3 printed. The database is the
    scenario's `pg` Deployment, whose superuser is `podium`, so the drop
    connects to the `postgres` database.

    ```bash
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0
    kubectl wait --for=delete pod --timeout=10m \
      -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
      --field-selector=status.phase!=Succeeded,status.phase!=Failed
    kubectl exec deploy/pg -- psql -U podium -d postgres -c 'DROP DATABASE podium WITH (FORCE)'
    kubectl exec deploy/pg -- createdb -U podium podium
    kubectl exec deploy/pg -- pg_restore -U podium -d podium /tmp/step2.dump
    mc_run 'mc mirror --overwrite --remove m/podium-backup m/podium'
    record
    rows
    helm rollback podium 1 --wait --timeout 10m
    kubectl get pods -l app.kubernetes.io/name=podium
    ```

    **Expect.** `psql` prints `DROP DATABASE`, `createdb` and `pg_restore`
    exit 0, `record` prints `0`, and `rows` reports `2|2`. Two v0.4.0 pods report
    `1/1 Running` after the rollback.

    Then try to serve this release over the restored store.

    ```bash
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=2 \
      --wait --timeout 5m
    kubectl logs --previous "$(current_pod)"
    record
    ```

    **Expect.** The upgrade fails once `--timeout` passes, a pod on
    `podium-live:current` restarts and its previous log names
    `sign-stored-rows`, both v0.4.0 pods still report Ready, and `record`
    prints `0`.

    Then reinstall over the restored store with the dry run as a
    `post-install` hook, capturing the Job's log as step 3 of the procedure
    does with `helm install` in place of `helm upgrade`.

    ```bash
    helm uninstall podium --wait
    kubectl wait --for=delete pod --timeout=10m \
      -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
      --field-selector=status.phase!=Succeeded,status.phase!=Failed
    kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
    helm install podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0 \
      --set migration.mode=dry-run --set migration.previousImage=podium-live:v0.4.0 --timeout 30m &
    helm_pid=$!
    until kubectl get job podium-podium-migrate >/dev/null 2>&1 || ! kill -0 "$helm_pid" 2>/dev/null; do
      sleep 2
    done
    kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > "$WORK/dry-run.log"
    wait "$helm_pid"
    tail -n 1 "$WORK/dry-run.log"
    record
    kubectl delete job -l app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate
    kubectl get job -l app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate
    ```

    **Expect.** The install exits 0, the Job's log ends with the `dry-run:
    plan digest` line, `record` prints `0`, and the last command reports `No
    resources found`.

15. Create a second namespace, select it, and repeat steps 2 and 3 there
    without the signing Secret.

    ```bash
    kubectl create namespace podium-s76-off
    kubectl config set-context --current --namespace podium-s76-off
    backing
    seed
    rows
    helm install podium "$WORK/v040/deploy/helm/podium" --set image.repository=podium-live \
      --set image.tag=v0.4.0 --set replicaCount=2 --set config.identityProvider.type="" \
      --wait --timeout 10m
    helm get values podium -o yaml > "$WORK/podium-values.yaml"
    ```

    The `helm get values` line replaces the first namespace's values file,
    which no later step reads. Edit `$WORK/podium-values.yaml` as step 3 does,
    with `signing.mode: none`, `migration.includeUnsigned: false`, and
    `migration.previousImage: podium-live:v0.4.0` in place of the signing
    Secret. Repeat step 5. Then run step 3 of the procedure from `$WORK` as
    step 7 does, a serving upgrade, step 2's zero-replica command and its
    wait, and the run and serve steps of the procedure with the digest.

    ```bash
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --wait --timeout 5m
    kubectl logs --previous "$(current_pod)"
    record
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0
    kubectl wait --for=delete pod --timeout=10m \
      -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
      --field-selector=status.phase!=Succeeded,status.phase!=Failed
    ```

    Run step 4 of the procedure with the digest, read `record`, run step 5,
    and read the probes:

    ```bash
    kubectl rollout status deployment/podium-podium --timeout=5m
    kubectl get pods -l app.kubernetes.io/name=podium
    kubectl get deployment podium-podium \
      -o jsonpath='{.spec.template.spec.containers[0].startupProbe.httpGet.path}'; echo
    helm upgrade podium "$CHART" -f "$WORK/podium-values.yaml" --set replicaCount=0 \
      --set migration.mode=dry-run --set migration.includeUnsigned=true
    ```

    **Expect.** `rows` reports `2|2` after the seed. The dry-run log's header
    carries `signing_key=-`, and no row carries `sign=true`. The serving
    upgrade before the run fails, a pod on `podium-live:current` restarts, its
    `kubectl logs --previous` names `sign-stored-rows` and not
    `--plan-digest`, and `record` prints `0`. The run Job succeeds and
    `record` prints `1`. Both serving pods become Ready with `0` restarts,
    because the values file carries `replicaCount: 2` from this step's `helm
    get values`, and the probe path prints `/healthz`. The last command is
    refused naming `migration.includeUnsigned=false`.

16. Create a third namespace, select it, and deploy fresh Postgres and MinIO
    with the bucket, the `podium-secrets` Secret, and the signing key Secret,
    as step 2 does with no seed. Install the chart over the empty store, then
    uninstall it.

    ```bash
    kubectl create namespace podium-s76-fresh
    kubectl config set-context --current --namespace podium-s76-fresh
    backing
    kubectl create secret generic podium-signing-key \
      --from-file=registry-signing.key="$WORK/registry-signing.key"
    helm install podium "$CHART" --set image.repository=podium-live \
      --set image.tag=current --set signing.secretName=podium-signing-key \
      --set config.identityProvider.type="" --set replicaCount=1 --wait --timeout 10m
    kubectl get pods -l app.kubernetes.io/name=podium
    kubectl get jobs
    record
    helm uninstall podium --wait
    kubectl delete job -l app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate
    ```

    **Expect.** `mc mb` prints `Bucket created successfully`, and both Secrets
    report `created`. The install serves with one Ready pod and no Job,
    `record` prints `1`, and the delete reports `No resources found` and
    exits 0.

**Cleanup.**

The cluster holds every namespace, Job, and pod the scenario created, and
`$WORK` holds the values files, the logs, the staged manifests, the image
archive, the `deploy` link, and the kubeconfig.

```bash
kill "$PF" 2>/dev/null
kind delete cluster --name podium-s76
docker rmi podium-live:current podium-live:current2 podium-live:v0.4.0
git -C "$REAL_HOME/projects/podium" worktree remove --force "$WORK/v040"
rm -rf "$WORK"
```

---

## S77: A filesystem sync that drops a colliding artifact fails

**Goal.** Validate that `podium sync` against a filesystem registry drops the
higher-precedence copy of an unsanctioned cross-layer collision, names it on
standard error, materializes the rest, and exits 1; that a standalone server
over the same directory serves the same copy; and that a workspace overlay
artifact replaces the registry-side artifact silently even when it declares
`extends:`.

**Covers.** The §4.6 collision rule for registry-side layers, the §13.11.3
report and exit status, the §11 filesystem-to-server equivalence requirement
on a colliding directory, and the §6.4 overlay exception.

**Why by hand.** The end-to-end suite reads the exit code and the streams
through the harness. What it does not read is the operator's terminal: that
the materialized file a developer opens holds the lower layer's text, that the
rejection names the remedy in words an author can act on, and that `$?` is
what a CI step gates on.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build a
   two-layer registry in which both layers contribute `shared/clash` with no
   `extends:`, and the lower layer also holds `shared/other`.

   ```bash
   mkdir -p "$WORK/reg/org-defaults/shared/clash" "$WORK/reg/org-defaults/shared/other" \
     "$WORK/reg/team-finance/shared/clash"
   printf 'multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-finance\n' \
     > "$WORK/reg/.registry-config"
   printf 'visibility:\n  public: true\n' > "$WORK/reg/org-defaults/.layer-config"
   printf 'visibility:\n  public: true\n' > "$WORK/reg/team-finance/.layer-config"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: from-org\n---\n\nfrom-org\n' \
     > "$WORK/reg/org-defaults/shared/clash/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: other\n---\n\nother\n' \
     > "$WORK/reg/org-defaults/shared/other/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: from-team\n---\n\nfrom-team\n' \
     > "$WORK/reg/team-finance/shared/clash/ARTIFACT.md"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the three
   `ARTIFACT.md` files exist.

2. Sync through the filesystem source, keeping the two streams apart.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none \
     > "$WORK/out.txt" 2> "$WORK/err.txt"
   echo "exit=$?"
   cat "$WORK/err.txt"
   cat "$WORK/fs/shared/clash/ARTIFACT.md"
   ls "$WORK/fs/shared/other"
   ```

   **Expect.** `exit=1`. `$WORK/err.txt` carries one line beginning
   `rejected: shared/clash (ingest.collision): cross-layer collision` that
   ends with `declare extends: shared/clash to overlay it`. The materialized
   `shared/clash` holds `from-org`, and `shared/other` is present. `exit=0`
   or `from-team` is the shipped behavior this scenario exists to catch.

3. Repeat with `--dry-run` into a new target.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/dry" --harness none --dry-run; echo "exit=$?"
   ls "$WORK/dry" 2>&1
   ```

   **Expect.** `exit=1`, the same `rejected:` line, and `ls` reports that
   `$WORK/dry` does not exist.

4. Start a standalone server over the same directory and sync through it.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8131 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8131/healthz
   server_alive "$SRV" "$WORK/srv.log"
   podium sync --registry http://127.0.0.1:8131 --target "$WORK/srv" --harness none; echo "exit=$?"
   diff "$WORK/fs/shared/clash/ARTIFACT.md" "$WORK/srv/shared/clash/ARTIFACT.md" && echo same
   ```

   **Expect.** The server sync prints `exit=0` and no `rejected:` line, and
   `diff` prints `same`. A difference means the two deployment modes kept
   different copies.

5. Sanction the collision and sync the filesystem target again.

   ```bash
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: from-team\nextends: shared/clash\n---\n\nfrom-team\n' \
     > "$WORK/reg/team-finance/shared/clash/ARTIFACT.md"
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none; echo "exit=$?"
   cat "$WORK/fs/shared/clash/ARTIFACT.md"
   ```

   **Expect.** `exit=0`, no `rejected:` line, and the materialized artifact
   carries `description: from-team` with the `from-team` body.

6. Add a workspace overlay artifact that declares `extends:` on the same ID
   and sync with it.

   ```bash
   mkdir -p "$WORK/overlay/shared/clash"
   printf -- '---\ntype: context\nversion: 9.0.0\ndescription: from-overlay\nextends: shared/clash\n---\n\nfrom-overlay\n' \
     > "$WORK/overlay/shared/clash/ARTIFACT.md"
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none \
     --overlay "$WORK/overlay"; echo "exit=$?"
   cat "$WORK/fs/shared/clash/ARTIFACT.md"
   ```

   **Expect.** `exit=0`, no `rejected:` line, and the materialized file is
   byte-for-byte the overlay's authored `ARTIFACT.md`, including its
   `extends: shared/clash` line. Merged registry fields in the file, a
   `rejected:` line, or a non-zero exit means the §6.4 exception is not
   honored.

**Cleanup.** `kill "$SRV"` and `rm -rf "$WORK"`.

---

## S78: The registry refuses an unusable SCIM store, audit sink, or audit anchor key

**Goal.** Validate that `podium serve` refuses to start when a set
`PODIUM_SCIM_STORE_PATH` cannot hold the SCIM directory, when an enabled audit
anchor's file sink cannot be opened or its key cannot be loaded, and when the
anchor key is also the registry signing key, and that a deployment with
anchoring disabled never reads the anchor key.

**Covers.** The `config.scim_store_unavailable`, `config.audit_sink_unavailable`,
`config.audit_anchor_key_unavailable`, and `config.audit_anchor_key_shared`
startup refusals, the refusal without `PODIUM_SCIM_TOKENS`, and the
anchoring-disabled negative control (§6.3.1, §8.6, §13.12).

**Why by hand.** The end-to-end suite asserts the code in the output. It does
not show the operator's terminal: that the message names the path or the
`key_id` an operator has to act on, that it prints no private key, and that
`$?` is 1 for a supervisor to gate on.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block, then scaffold a one-artifact layer the server can
   load.

   ```bash
   mkdir -p "$WORK/reg/seed"
   podium artifact scaffold --type context --description "seed" --force "$WORK/reg/seed"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and
   `$WORK/reg/seed/ARTIFACT.md` exists.

2. A malformed SCIM store file is refused with `PODIUM_SCIM_TOKENS` unset. This
   runs in the foreground and exits immediately.

   ```bash
   printf '{' > "$WORK/scim.json"
   PODIUM_SCIM_STORE_PATH="$WORK/scim.json" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains `config.scim_store_unavailable`
   and `$WORK/scim.json`. No `listening on` line appears.

3. A SCIM store path that names a directory is refused.

   ```bash
   PODIUM_SCIM_STORE_PATH="$WORK" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains `config.scim_store_unavailable`.

4. An anchor key that is the registry signing key is refused. The isolation
   block already exports `PODIUM_SIGN_KEY_PATH`; the first command generates
   the registry key by starting and stopping the server once.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146 > "$WORK/srv.log" 2>&1 &
   PID=$!; server_alive "$PID" "$WORK/srv.log"; kill "$PID"; wait "$PID" 2>/dev/null
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60 PODIUM_AUDIT_SIGNING_KEY_PATH="$PODIUM_SIGN_KEY_PATH" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains `config.audit_anchor_key_shared`,
   a 16-character hexadecimal `key_id`, and both key paths. The output does not
   contain the `private:` value from `$PODIUM_SIGN_KEY_PATH`.

5. An anchor key file with no `private:` line is refused while anchoring is
   enabled.

   ```bash
   grep '^public:' "$PODIUM_SIGN_KEY_PATH" > "$WORK/anchor-public-only.key"
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60 PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor-public-only.key" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains
   `config.audit_anchor_key_unavailable` and `$WORK/anchor-public-only.key`.

6. The same key file with anchoring disabled starts, and the file is untouched.

   ```bash
   shasum "$WORK/anchor-public-only.key" > "$WORK/before.sum"
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=0 PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor-public-only.key" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146 > "$WORK/srv.log" 2>&1 &
   PID=$!; server_alive "$PID" "$WORK/srv.log"
   curl -fsS --retry 40 --retry-connrefused --retry-delay 1 http://127.0.0.1:8146/healthz; echo
   kill "$PID"; wait "$PID" 2>/dev/null
   shasum -c "$WORK/before.sum"
   ```

   **Expect.** `/healthz` answers, `srv.log` contains no
   `config.audit_anchor_key_unavailable`, and `shasum -c` prints `OK`.

7. An audit log path that names a directory is refused while anchoring is enabled.

   ```bash
   mkdir -p "$WORK/auditdir"
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60 PODIUM_AUDIT_LOG_PATH="$WORK/auditdir" PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor-new.key" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"; ls "$WORK/anchor-new.key"
   ```

   **Expect.** `exit=1`, the output contains `config.audit_sink_unavailable` and
   `$WORK/auditdir`, and `ls` reports that `anchor-new.key` does not exist.

**Expected.**

- Steps 2 and 3 exit 1 with `config.scim_store_unavailable`. A wrong build
  prints `warning: SCIM persistence disabled` and starts.
- Step 4 exits 1 with `config.audit_anchor_key_shared` and a `key_id`. A wrong
  build starts and anchors with the registry key.
- Step 5 exits 1 with `config.audit_anchor_key_unavailable`. A wrong build
  prints `warning: audit anchor disabled (signer)` and starts unanchored.
- Step 6 starts and leaves the key file unchanged. A wrong build refuses or
  rewrites the file.
- Step 7 exits 1 with `config.audit_sink_unavailable`. A wrong build prints
  `warning: audit sink disabled` and starts unanchored.

**Cleanup.** `rm -rf "$WORK"`.

---

## S79: A workspace target's `$PODIUM_CHANGED` follows the bytes on disk

**Goal.** Validate that a `kind: workspace` target's `skip_if_no_changes`
publish command runs when a sync rewrites a materialized file, including one
restored after a hand edit, and is skipped when the sync leaves every
materialized file byte-identical, including after the lock file is deleted
and after an `ARTIFACT.md` edit the harness output does not carry.

**Covers.** The §7.5.2 definition of `$PODIUM_CHANGED` for both target kinds.

**Why by hand.** The end-to-end suite counts publish runs. What it does not
read is the operator's terminal: the `skipped (no changes)` line a CI log
shows, and whether a commit step would have run after a sync restored a file
a teammate edited by hand.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build a
   single-layer registry with one skill and a `sync.yaml` whose workspace
   target appends a line to a counter file on every publish run.

   ```bash
   mkdir -p "$WORK/reg/team/hello" "$WORK/ws/.podium"
   printf -- '---\ntype: skill\nversion: 1.0.0\ndescription: hello\ntags: [a]\n---\n' \
     > "$WORK/reg/team/hello/ARTIFACT.md"
   printf -- '---\nname: hello\ndescription: hello\n---\n\nSay hello.\n' \
     > "$WORK/reg/team/hello/SKILL.md"
   printf 'defaults:\n  registry: %s\ntargets:\n  - id: claude-workspace\n    kind: workspace\n    harness: claude-code\n    target: %s\n    workflow:\n      publish:\n        - sh: "echo run >> %s"\n          skip_if_no_changes: true\n' \
     "$WORK/reg" "$WORK/out" "$WORK/count" > "$WORK/ws/.podium/sync.yaml"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and both files
   under `$WORK/reg/team/hello` exist.

2. Sync twice.

   ```bash
   cd "$WORK/ws"
   podium sync --config .podium/sync.yaml; echo "exit=$?"
   podium sync --config .podium/sync.yaml 2> "$WORK/err2.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err2.txt"
   ```

   **Expect.** Both runs print `exit=0`. The counter holds 1 line, and
   `$WORK/err2.txt` contains `skipped (no changes)`.

3. Edit the materialized `SKILL.md` by hand and sync.

   ```bash
   echo "local edit" >> "$WORK/out/.claude/skills/hello/SKILL.md"
   podium sync --config .podium/sync.yaml 2> "$WORK/err3.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err3.txt"
   grep -c "local edit" "$WORK/out/.claude/skills/hello/SKILL.md"
   ```

   **Expect.** `exit=0`, the counter holds 2 lines, `$WORK/err3.txt` has no
   `skipped (no changes)` line, and `grep -c` prints `0` because the sync
   restored the file. A counter of 1 is the shipped behavior this step
   exists to catch: the restored file would stay uncommitted.

4. Delete the lock file and sync.

   ```bash
   rm "$WORK/out/.podium/sync.lock"
   podium sync --config .podium/sync.yaml 2> "$WORK/err4.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err4.txt"
   ```

   **Expect.** `exit=0`, the counter still holds 2 lines, and
   `$WORK/err4.txt` contains `skipped (no changes)`. A counter of 3 means the
   variable still follows the lock.

5. Change only `tags:` in the authored `ARTIFACT.md` and sync.

   ```bash
   printf -- '---\ntype: skill\nversion: 1.0.0\ndescription: hello\ntags: [a, b]\n---\n' \
     > "$WORK/reg/team/hello/ARTIFACT.md"
   podium sync --config .podium/sync.yaml 2> "$WORK/err5.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err5.txt"
   ```

   **Expect.** `exit=0`, the counter still holds 2 lines, and
   `$WORK/err5.txt` contains `skipped (no changes)`, because Claude Code's
   output carries `SKILL.md` and no `tags:` field. A counter of 3 means the
   variable still follows the source content hash.

**Cleanup.** `cd /` and `rm -rf "$WORK"`.

---

## S80: Every harness output carries the derived skill compatibility line

**Goal.** Validate that `podium sync` writes a derived `compatibility` line
into a skill's `SKILL.md` for a non-Claude harness, keeps an authored value
unchanged, and leaves the `none` output untouched.

**Covers.** The §4.3.4 derivation scope, the §6.7 `none` exception, and the
§4.7.6 lock-hash stability the changelog states.

**Why by hand.** The unit and end-to-end tests parse the frontmatter. What
they do not read is the file a developer opens in the harness directory: that
the line sits at the top of the frontmatter, that it reads as a sentence an
agent can act on, and that the lock file does not churn.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then create a
   registry with one skill that declares runtime constraints and omits
   `compatibility`, and one skill that authors it.

   ```bash
   mkdir -p "$WORK/reg/tools/greet" "$WORK/reg/tools/authored"
   printf -- '---\ntype: skill\nversion: 1.0.0\nruntime_requirements:\n  python: ">=3.10"\nsandbox_profile: read-only-fs\n---\n\nGreet.\n' \
     > "$WORK/reg/tools/greet/ARTIFACT.md"
   printf -- '---\nname: greet\ndescription: Greets the user.\n---\n\nSay hello.\n' \
     > "$WORK/reg/tools/greet/SKILL.md"
   cp "$WORK/reg/tools/greet/ARTIFACT.md" "$WORK/reg/tools/authored/ARTIFACT.md"
   printf -- '---\nname: authored\ndescription: Authored compatibility.\ncompatibility: Needs a GPU.\n---\n\nRun it.\n' \
     > "$WORK/reg/tools/authored/SKILL.md"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the four
   files exist.

2. Sync to Cursor.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/cur" --harness cursor; echo "exit=$?"
   head -4 "$WORK/cur/.cursor/skills/greet/SKILL.md"
   cat "$WORK/cur/.cursor/skills/authored/SKILL.md"
   diff "$WORK/reg/tools/authored/SKILL.md" "$WORK/cur/.cursor/skills/authored/SKILL.md" && echo identical
   ```

   **Expect.** `exit=0`. The greet `SKILL.md` opens with `---` followed by
   `compatibility: "Requires Python >=3.10; sandbox: read-only-fs"`, then
   `name: greet`. The authored `SKILL.md` is identical to the source file, so
   `diff` prints `identical`, and it carries `compatibility: Needs a GPU.`
   only once. A greet file without the line is the shipped behavior this
   scenario exists to catch.

3. Sync to `none` and compare.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/raw" --harness none; echo "exit=$?"
   diff "$WORK/reg/tools/greet/SKILL.md" "$WORK/raw/tools/greet/SKILL.md" && echo identical
   ```

   **Expect.** `exit=0` and `identical`. A `compatibility:` line in the `none`
   output is a defect.

4. Record the lock hashes, re-sync Cursor, and compare.

   ```bash
   grep content_hash "$WORK/cur/.podium/sync.lock" > "$WORK/h1.txt"
   podium sync --registry "$WORK/reg" --target "$WORK/cur" --harness cursor; echo "exit=$?"
   grep content_hash "$WORK/cur/.podium/sync.lock" | diff "$WORK/h1.txt" - && echo stable
   ```

   **Expect.** `exit=0` and `stable`. A changed `content_hash` means the hash
   was computed over adapter output, which is a defect.

**Cleanup.** `cd /` and `rm -rf "$WORK"`.

---

## S81: The Python SDK login pair against a live IdP

**Goal.** Validate that `Client.start_login()` returns the verification URL and
the user code without printing or polling, that `Client.finish_login()` installs
the token after the user approves, that a finished handle cannot be reused, and
that a cancelled or denied flow ends with the matching `DeviceCodeError.reason`.

**Covers.** The §6.3 SDK contract for `oauth-device-code`. The SDK suites pin
each outcome against a stub IdP. This scenario covers what only a live IdP
establishes: the URL and code a person types into a real verification page,
and the reply the IdP sends on approval and on denial.

**Why by hand.** A notebook or GUI author reads the handle's fields and shows
them in their own interface. No automated test reads that the URL opens a real
verification page, that the code is accepted there, or that the terminal stays
silent while the start call runs.

**Prerequisites.**

- An IdP whose tenant publishes a device-authorization endpoint and a token
  endpoint, and a public client registered on it that may use the device-code
  grant. When none is available, skip the scenario and record the skip and the
  reason.
- `python3` 3.10 or later.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then export the
   IdP coordinates and the SDK path.

   ```bash
   export DEVICE_URL=<device authorization endpoint>
   export TOKEN_URL=<token endpoint>
   export CLIENT_ID=<device-code client id>
   export PYTHONPATH="$REAL_HOME/projects/podium/sdks/podium-py"
   unset PODIUM_OAUTH_CLIENT_ID PODIUM_OAUTH_AUDIENCE \
     PODIUM_OAUTH_AUTHORIZATION_ENDPOINT PODIUM_OAUTH_TOKEN_URL
   cat > "$WORK/pair.py" <<'PY'
   import os, sys, threading
   from podium import Client, DeviceCodeError
   c = Client(registry="http://localhost:1")
   kw = dict(client_id=os.environ["CLIENT_ID"],
             device_authorization_endpoint=os.environ["DEVICE_URL"],
             token_endpoint=os.environ["TOKEN_URL"])
   mode = sys.argv[1]
   p = c.start_login(**kw)
   print("URI", p.verification_uri, "CODE", p.user_code, "COMPLETE", p.verification_uri_complete)
   print("REPR", repr(p))
   cancel = threading.Event()
   if mode == "cancel":
       threading.Timer(3, cancel.set).start()
   try:
       t = c.finish_login(p, timeout=300, cancel=cancel)
       print("TOKEN_SET", bool(c.token) and c.token == t.access_token)
   except DeviceCodeError as e:
       print("REASON", e.reason)
   try:
       c.finish_login(p)
   except DeviceCodeError as e:
       print("REUSE", e.reason)
   PY
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and
   `$WORK/pair.py` exists.

2. Run the approve path, keeping the streams apart, and approve the request in a
   browser at the printed URI with the printed code.

   ```bash
   python3 -u "$WORK/pair.py" approve 2> "$WORK/err.txt" | tee "$WORK/out.txt"
   wc -c < "$WORK/err.txt"
   ```

   The `-u` flag keeps stdout unbuffered, so the `URI` line reaches the
   terminal while `finish_login` waits for the approval.

   **Expect.** The terminal and `out.txt` show a `URI` line with an `https` URL
   and a non-empty `CODE`, then `TOKEN_SET True`, then `REUSE consumed`.
   `err.txt` is 0 bytes.
   The `REPR` line does not contain the device code. A `Visit:` line in
   `err.txt`, or a second token on reuse, is the defect this step catches.

3. Run the cancel path and do not visit the URL.

   ```bash
   python3 "$WORK/pair.py" cancel
   ```

   **Expect.** Within about 3 s plus one poll interval the script prints
   `REASON cancelled`, then `REUSE consumed`. A run that blocks until the
   timeout is the defect this step catches.

4. Run the approve path again, and deny the request on the verification page.

   ```bash
   python3 "$WORK/pair.py" approve
   ```

   **Expect.** `REASON denied`, then `REUSE consumed`. `REASON failed` means
   the IdP's `access_denied` reply was not mapped.

**Cleanup.** `cd /` and `rm -rf "$WORK"`.

---

## S82: The Python SDK rejects a harness other than `none` on materialize

**Goal.** Validate that `materialize()` on a loaded artifact and on a batch
item writes the canonical layout for `harness="none"` and raises `ValueError`
for any other value without writing a file.

**Covers.** The §7.6 `harness` argument contract and the §2.2 statement that
the SDKs run no harness adapter.

**Why by hand.** The SDK unit tests assert the exception type and an empty
directory. A person reads the error message a caller sees in a terminal and
confirms that it names the canonical layout and points at `podium sync`.

**Prerequisites.** `python3` 3.10 or later.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then put the
   SDK on the path and write the script.

   ```bash
   export PYTHONPATH="$REAL_HOME/projects/podium/sdks/podium-py"
   cat > "$WORK/mat.py" <<'PY'
   import os, sys
   from podium import BatchResult, LoadedArtifact, RegistryError
   art = LoadedArtifact(id="a/b", type="context", version="1.0.0",
                        manifest_body="x\n", frontmatter="---\ntype: context\n---\n")
   bad = BatchResult(id="x/y", status="error",
                     error=RegistryError("visibility.denied", "no"))
   out = os.path.join(os.environ["WORK"], "out")
   os.makedirs(out, exist_ok=True)
   mode = sys.argv[1]
   try:
       if mode == "none":
           print("WROTE", art.materialize(out, harness="none"))
       elif mode == "claude":
           art.materialize(out, harness="claude-code")
       else:
           bad.materialize(out, harness="claude-code")
   except Exception as e:
       print("RAISED", type(e).__name__, e)
   print("FILES", sorted(os.listdir(out)))
   PY
   ```

   **Expect.** The script file exists.

2. Materialize with `harness="claude-code"`.

   ```bash
   python3 "$WORK/mat.py" claude
   ```

   **Expect.** `RAISED ValueError materialize() writes the canonical layout
   only; harness must be 'none', got 'claude-code'. Use `podium sync --harness
   <name>` for harness-native files.` followed by `FILES []`. A `FILES` line
   listing `a` is the shipped behavior this scenario exists to catch.

3. Materialize an error batch item with `harness="claude-code"`.

   ```bash
   python3 "$WORK/mat.py" batch-error
   ```

   **Expect.** `RAISED ValueError` with the same message, and `FILES []`.
   `RAISED VisibilityDenied` or any other registry error means the status
   check ran first, which is a defect.

4. Materialize with `harness="none"`.

   ```bash
   python3 "$WORK/mat.py" none
   cat "$WORK/out/a/b/ARTIFACT.md"
   ```

   **Expect.** A `WROTE` line naming `.../out/a/b/ARTIFACT.md`, `FILES ['a']`,
   and the file prints `---`, `type: context`, `---`.

**Cleanup.** `cd /` and `rm -rf "$WORK"`.

---

## S83: The change-event stream withholds events from layers the caller cannot see

**Goal.** Validate that `GET /v1/events` delivers each event only to callers
who can see the layer it names, that a visibility grant and a withdrawal take
effect on an open stream without reconnecting, that the withdrawing event still
reaches the caller who lost the layer, and that `podium sync --watch` removes
what it materialized from that layer.

**Covers.** The §7.6 change-event stream visibility rule and the §7.5.4 watcher
through the compiled binary. This is an operator check through the compiled
binary and the `podium sync` watcher, which the trusted-headers end-to-end test
cannot drive.

**Why by hand.** An operator reads the raw NDJSON stream of two callers side by
side and the files a watcher writes and deletes. A stream that carries an
`eng-internal` line for bob, or a watcher that keeps the `eng-internal` files
after the withdrawal, is the defect this scenario catches.

**Steps.**

1. Run the isolation block. Set up the S12 registry (layers `public-handbook`
   with `public: true` and `eng-internal` with `groups: [engineering]`), the
   runtime key, and the SCIM provisioning exactly as S12 steps 2 and 3 do, with
   `PODIUM_BOOTSTRAP_ADMINS=carol@acme.com` exported before `podium serve`.
   Mint three tokens.

   ```bash
   ALICE=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com --groups engineering)
   BOB=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
   CAROL=$(go -C "$REAL_HOME/projects/podium" run ./tools/minttoken --keys "$WORK/keys" --sub carol@acme.com --email carol@acme.com)
   printf '%s' "$BOB" > "$WORK/bob.tok"
   ```

   **Expect.** The three tokens are non-empty, and `server_alive` reports the
   server running.

2. Open a stream as alice and as bob, each logging to a file.

   ```bash
   curl -sN -H "Authorization: Bearer $ALICE" "$PODIUM_REGISTRY/v1/events" > "$WORK/alice.ndjson" &
   ALICE_CURL=$!
   curl -sN -H "Authorization: Bearer $BOB" "$PODIUM_REGISTRY/v1/events" > "$WORK/bob.ndjson" &
   BOB_CURL=$!
   ```

   **Expect.** Both files exist and stay empty or carry only `_heartbeat`
   lines.

3. Add an artifact to `eng-internal` and reingest it as carol.

   ```bash
   podium artifact scaffold --type skill --description "Engineering rollback" --force "$WORK/eng/rollback"
   PODIUM_SESSION_TOKEN="$CAROL" podium layer reingest --registry "$PODIUM_REGISTRY" eng-internal
   sleep 2
   grep -v _heartbeat "$WORK/alice.ndjson"; echo "--- bob ---"; grep -v _heartbeat "$WORK/bob.ndjson"
   ```

   **Expect.** alice's file carries `artifact.published` and `layer.ingested`
   lines naming `eng-internal`. bob's file carries no line other than
   `_heartbeat`. An `eng-internal` line in bob's file means the stream is
   unfiltered.

4. Grant bob the layer, then reingest.

   ```bash
   curl -s -X PUT -H "Authorization: Bearer $CAROL" -H 'Content-Type: application/json' \
     "$PODIUM_REGISTRY/v1/layers/update?id=eng-internal" -d '{"users":["bob@acme.com"]}'
   PODIUM_SESSION_TOKEN="$CAROL" podium layer reingest --registry "$PODIUM_REGISTRY" eng-internal
   sleep 2
   grep -v _heartbeat "$WORK/bob.ndjson"
   ```

   **Expect.** bob's file now carries a `layer.config_changed` line naming
   `eng-internal` with action `update`, followed by the `eng-internal`
   `layer.ingested` line. bob's `curl` was not restarted.

5. Start a watcher as bob, then withdraw the grant.

   ```bash
   mkdir -p "$WORK/bob-target"
   PODIUM_SESSION_TOKEN_FILE="$WORK/bob.tok" podium sync --watch --registry "$PODIUM_REGISTRY" --target "$WORK/bob-target" > "$WORK/watch.log" 2>&1 &
   WATCH=$!
   sleep 3; find "$WORK/bob-target" -path '*rollback*' -o -path '*deploy*'
   curl -s -X PUT -H "Authorization: Bearer $CAROL" -H 'Content-Type: application/json' \
     "$PODIUM_REGISTRY/v1/layers/update?id=eng-internal" -d '{"users":[]}'
   sleep 3
   PODIUM_SESSION_TOKEN="$CAROL" podium layer reingest --registry "$PODIUM_REGISTRY" eng-internal
   sleep 2
   tail -n 5 "$WORK/bob.ndjson"; find "$WORK/bob-target" -path '*rollback*' -o -path '*deploy*'
   ```

   **Expect.** Before the withdrawal, the `find` lists the `eng-internal`
   artifacts in bob's target. After it, bob's file carries a second
   `layer.config_changed` line for `eng-internal`, and no `eng-internal` line
   follows it although carol reingested the layer. The second `find` prints
   nothing. A missing withdrawal line, or `eng-internal` files left in the
   target, is the defect.

6. Optional: reorder with bob outside `eng-internal`.

   ```bash
   curl -s -X POST -H "Authorization: Bearer $CAROL" -H 'Content-Type: application/json' \
     "$PODIUM_REGISTRY/v1/layers/reorder" -d '{"order":["eng-internal","public-handbook"]}'
   sleep 2; grep reorder "$WORK/bob.ndjson" "$WORK/alice.ndjson"
   ```

   **Expect.** bob's reorder line reads `"layer":"public-handbook"`. alice's
   reads `"layer":"eng-internal,public-handbook"`. When the stored precedence
   already matches the requested order, no reorder line appears on either
   stream; swap the order and repeat.

**Cleanup.** `kill $ALICE_CURL $BOB_CURL $WATCH`, stop the server, and
`rm -rf "$WORK"`.

---

## S84: A replayed stale latest is refused, and an honest regression is not

**Goal.** Validate that `podium-mcp` refuses a replayed `latest` record signed by a live registry, accepts a pinned load of the same record, accepts a `latest` that stays put when a deprecated version is ingested, and loads again after `podium cache reset-revisions`.

**Covers.** §4.7.10 ingest time, the §6.5 revision mark, the §6.9 `materialize.stale_resolution` row, and the §6.10 envelope.

**Why by hand.** A person reads the bridge's error envelope and the reset command's output against a registry's own signed bytes, including the deprecation ingest that TEST-4 does not drive through the binaries.

**Steps.**

1. Run the isolation block.

2. Generate the key file, scaffold skill `fresh` at 1.0.0, serve it, capture its `latest` body, and load it once.

   ```bash
   podium admin signing-key generate --key-file "$PODIUM_SIGN_KEY_PATH" > /dev/null
   KEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   podium artifact scaffold --type skill --description "Fresh skill" "$WORK/reg/fresh" > /dev/null
   setver() { sed -i.bak "s/^version: .*/version: $1/" "$WORK/reg/fresh/ARTIFACT.md"; }
   setver 1.0.0
   serve() { podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
       --bind 127.0.0.1:8182 > "$WORK/srv$1.log" 2>&1 & SRV=$!
     curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8182/healthz
     server_alive "$SRV" "$WORK/srv$1.log"; }
   stop() { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }
   serve 1
   REG=http://127.0.0.1:8182
   curl -s "$REG/v1/load_artifact?id=fresh" -o "$WORK/v1.body"
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   load_mcp() { ARGS="{\"id\":\"fresh\"${2:+,\"version\":\"$2\"}}"
     printf '%s\n%s\n' "$INIT" "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"load_artifact\",\"arguments\":$ARGS}}" \
     | PODIUM_REGISTRY="$1" PODIUM_SIGNATURE_VERIFY_KEY="$KEY" PODIUM_CACHE_DIR="$WORK/cache" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(json.dumps({k:r[k] for k in ("code","details") if k in r}) if r.get("code") else "loaded")'; }
   load_mcp "$REG"
   ```

   **Expect.** `loaded`, and `$WORK/v1.body` holds a JSON object whose `version` is `1.0.0` and which carries an `artifact_revision` written as an RFC 3339 UTC timestamp with six fractional digits.

3. Bump to 2.0.0, restart, and load `latest`.

   ```bash
   stop; setver 2.0.0; serve 2
   load_mcp "$REG"
   ```

   **Expect.** `loaded`.

4. Stop the registry, start a stub on the registry's own address that answers every `load_artifact` request with the captured 1.0.0 body, and load `latest` through it. The stub binds `127.0.0.1:8182`, so `PODIUM_REGISTRY` is unchanged and the load meets the mark steps 2 and 3 recorded.

   ```bash
   cat > "$WORK/stub.py" <<'EOF'
   import http.server, sys
   body = open(sys.argv[1], "rb").read()
   class H(http.server.BaseHTTPRequestHandler):
       def _head(self):
           self.send_response(200)
           self.send_header("Content-Type", "application/json")
           self.send_header("ETag", '"stub-no-match"')
           self.send_header("Content-Length", str(len(body)))
           self.end_headers()
       def do_HEAD(self): self._head()
       def do_GET(self): self._head(); self.wfile.write(body)
       def log_message(self, *a): pass
   http.server.HTTPServer(("127.0.0.1", 8182), H).serve_forever()
   EOF
   replay() { python3 "$WORK/stub.py" "$WORK/v1.body" & STUB=$!; sleep 1; }
   unreplay() { kill "$STUB" 2>/dev/null; wait "$STUB" 2>/dev/null; }
   stop; replay
   load_mcp "$REG"
   ```

   **Expect.** A JSON object with `"code": "materialize.stale_resolution"` and `details` naming `artifact_id` `fresh`, `served_version` `1.0.0`, and a `served_revision` earlier than `reference_revision`. A `loaded` here means the check is missing or ran on a cache path.

5. Load the pinned version through the stub.

   ```bash
   load_mcp "$REG" 1.0.0
   ```

   **Expect.** `loaded`, because a pinned request is never compared with the mark.

6. Stop the stub, ingest a new version 3.0.0 born deprecated, and load `latest` from the registry.

   ```bash
   unreplay; setver 3.0.0
   awk '{print} /^version: 3\.0\.0$/{print "deprecated: true"}' "$WORK/reg/fresh/ARTIFACT.md" > "$WORK/a.md" \
     && mv "$WORK/a.md" "$WORK/reg/fresh/ARTIFACT.md"
   serve 3
   load_mcp "$REG"
   ```

   **Expect.** `loaded`, with `latest` still resolving to 2.0.0. A `materialize.stale_resolution` here means a deprecated ingest lowered the served ingest time.

7. Replay once more to show the mark still refuses it, then reset the mark and replay again.

   ```bash
   stop; replay
   load_mcp "$REG"
   podium cache reset-revisions --dir "$WORK/cache" fresh; echo "exit=$?"
   load_mcp "$REG"
   unreplay
   ```

   **Expect.** First a JSON object with `"code": "materialize.stale_resolution"`, then `cache: reset 1 revision mark(s)`, then `exit=0`, then `loaded`. Every load in this scenario names `$REG`, so the cache holds one mark, and the count is 1. The refusal before the reset and the `loaded` after it, under the same URL and the same replayed body, show that the reset removed the mark.

Each `load_mcp` is a separate `podium-mcp` process, so step 4 already shows the mark persisting across processes. The lock case (exit 1 while `podium-mcp` holds the index) is covered by TEST-4 only, because a one-shot `podium-mcp` does not hold the lock long enough to observe by hand.

---

## S85: Sigstore-keyless sign and verify against the staging instance

**Goal.** Validate that `podium sign --provider sigstore-keyless` records a
Rekor v2 inclusion proof and a timestamp-authority timestamp, that
`podium verify` accepts the envelope only for the configured signer and
issuer, that it makes no network call, that the envelope still verifies after
the Fulcio certificate expires, and that `podium-mcp` refuses to start under
`sigstore-keyless`.

**Covers.** The §4.7.9 Sigstore-keyless acceptance conditions, the §6.2
`PODIUM_SIGSTORE_CERT_IDENTITY`, `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`,
trusted-root, and signing-endpoint rows, and the §6.2
`PODIUM_SIGNATURE_PROVIDER` restriction on the MCP server. The unit and
end-to-end tests run against an in-process Fulcio, Rekor, and timestamp
authority. This scenario covers what only a running Sigstore instance
establishes: the SAN and issuer extension Fulcio writes for a token, the body,
inclusion proof, and checkpoint Rekor v2 returns, the token the timestamp
authority issues, and the keys a published `trusted_root.json` carries.

**Why by hand.** The staging instance needs an OIDC token that a person or a
credentialed lane mints, and it writes every signature into a public log.

**Prerequisites.**

- An OIDC token the Sigstore staging Fulcio accepts, and the email or URI SAN
  and issuer that Fulcio records in the certificate for that token. When none
  is available, skip the scenario and record the skip and the reason.
- The recorded issuer is not always the token's issuer. For a token from the
  staging login provider at `https://oauth2.sigstage.dev/auth`, Fulcio records
  the upstream identity provider, so a Google login yields
  `https://accounts.google.com`. The provider's tokens expire about a minute
  after login, so run step 2 immediately after obtaining one.
- The staging `trusted_root.json` from the Sigstore staging TUF repository, and
  the staging Rekor v2 shard URL from the staging `signing_config`.
- Built `podium` and `podium-mcp` binaries on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then export the
   staging coordinates and compute a content hash.

   ```bash
   export PODIUM_SIGSTORE_FULCIO_URL=https://fulcio.sigstage.dev
   export PODIUM_SIGSTORE_REKOR_URL=<staging Rekor v2 shard URL>
   export PODIUM_SIGSTORE_TSA_URL=https://timestamp.sigstage.dev/api/v1/timestamp
   export PODIUM_SIGSTORE_OIDC_TOKEN=<token>
   export PODIUM_SIGSTORE_TRUSTED_ROOT_FILE=<staging trusted_root.json>
   export PODIUM_SIGSTORE_CERT_IDENTITY=<expected SAN>
   export PODIUM_SIGSTORE_CERT_OIDC_ISSUER=<issuer recorded in the certificate>
   H="sha256:$(printf 'podium s85' | shasum -a 256 | cut -d' ' -f1)"
   echo "$H"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and `$H` is
   `sha256:` followed by 64 hex characters.

2. Sign and inspect the envelope.

   ```bash
   podium sign --provider sigstore-keyless --content-hash "$H" > "$WORK/env.json"; echo "exit=$?"
   python3 -c 'import json,sys; e=json.load(open(sys.argv[1])); print(sorted(e)); print(sorted(e["tlog"]))' "$WORK/env.json"
   ```

   **Expect.** `exit=0`. The first list is
   `['cert', 'signature', 'timestamp', 'tlog']`, and the second is
   `['body', 'checkpoint', 'hashes', 'log_index']`. A `log_index` key at the
   top level, or a missing `timestamp` or `checkpoint`, is the defect this
   step catches.

3. Verify with the matching policy and with the network blocked for Rekor and
   the timestamp authority.

   ```bash
   PODIUM_SIGSTORE_REKOR_URL=http://127.0.0.1:1 PODIUM_SIGSTORE_TSA_URL=http://127.0.0.1:1 \
     podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   ```

   **Expect.** `verify ok` and `exit=0`. A connection error naming
   `127.0.0.1:1` means `Verify` still contacts the log or the timestamp
   authority.

4. Verify with another identity and with no identity.

   ```bash
   PODIUM_SIGSTORE_CERT_IDENTITY=bob@acme.com \
     podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   PODIUM_SIGSTORE_CERT_IDENTITY= \
     podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   ```

   **Expect.** The first run prints `verify failed:` with
   `certificate identity mismatch` and the SAN the certificate carries, then
   `exit=1`. The second prints `verify failed:` naming
   `PODIUM_SIGSTORE_CERT_IDENTITY`, then `exit=1`. `verify ok` on either run
   means `Verify` accepts an envelope without matching the configured signer
   identity.

5. Wait at least 15 minutes, so the 10-minute Fulcio certificate has expired,
   and repeat step 3.

   ```bash
   podium verify --provider sigstore-keyless --content-hash "$H" --signature "$(cat "$WORK/env.json")"; echo "exit=$?"
   ```

   **Expect.** `verify ok` and `exit=0`. A `certificate has expired` error
   means the chain check runs at the wall-clock time rather than at the
   timestamp's time.

6. Sign with no OIDC token.

   ```bash
   PODIUM_SIGSTORE_OIDC_TOKEN= podium sign --provider sigstore-keyless --content-hash "$H"; echo "exit=$?"
   ```

   **Expect.** `sign failed:` with `sigstore-keyless not configured`, then
   `exit=1`, and no envelope on stdout.

7. Start `podium-mcp` under `sigstore-keyless`, under the default policy and
   under `never`.

   ```bash
   PODIUM_SIGNATURE_PROVIDER=sigstore-keyless PODIUM_REGISTRY=http://127.0.0.1:1 podium-mcp </dev/null; echo "exit=$?"
   PODIUM_SIGNATURE_PROVIDER=sigstore-keyless PODIUM_VERIFY_SIGNATURES=never PODIUM_REGISTRY=http://127.0.0.1:1 podium-mcp </dev/null; echo "exit=$?"
   ```

   **Expect.** Each run prints a line naming `config.invalid`,
   `sigstore-keyless`, and `registry-managed` on stderr, then a non-zero
   `exit=`. A run that starts serving, or that refuses with
   `config.signature_provider_unavailable`, is the defect this step catches.

---

## S86: Webhook receivers are isolated per tenant on a multi-tenant registry

**Goal.** Validate that on a multi-tenant registry each tenant's admin lists
and addresses only its own webhook receivers, that a request naming another
tenant's receiver is answered as an unknown receiver, that an unrouted request
is refused, and that an event of one tenant is delivered only to that tenant's
receivers.

**Covers.** The §7.3.2 receiver tenancy rule, §6.3.1 per-request tenant
selection under `trusted-headers`, and the receiver CRUD and delivery path
through the compiled binary.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env` (Postgres DSN, S3 settings), `psql`, `openssl`, and `python3`. Skip
if any is absent. Go on macOS verifies TLS against the system keychain and
ignores `SSL_CERT_FILE`, so on macOS run step 6 only after trusting
`$WORK/sink.pem` in the login keychain, or run the scenario on Linux.

**Why by hand.** An operator reads the raw POST bodies and `X-Podium-Signature`
headers at both listeners side by side, together with the two
`GET /v1/webhooks` lists. Any POST at bob's listener after carol's reorder, or
any receiver of the other tenant in either list, is the defect this scenario
catches.

**Steps.**

1. Run the isolation block. Start services, load the environment, and create a
   self-signed certificate for `127.0.0.1` that both listeners share.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export SFX="$$"
   openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=127.0.0.1" \
     -addext "subjectAltName=IP:127.0.0.1" \
     -keyout "$WORK/sink.key" -out "$WORK/sink.pem"
   cat > "$WORK/sink.py" <<'EOF'
   import http.server, ssl, sys
   class H(http.server.BaseHTTPRequestHandler):
       def do_POST(self):
           body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
           print("SIG", self.headers.get("X-Podium-Signature"), flush=True)
           print("BODY", body.decode(), flush=True)
           self.send_response(200); self.end_headers()
   srv = http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H)
   ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
   ctx.load_cert_chain(sys.argv[2], sys.argv[3])
   srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
   srv.serve_forever()
   EOF
   python3 "$WORK/sink.py" 9541 "$WORK/sink.pem" "$WORK/sink.key" > "$WORK/carol-sink.log" 2>&1 &
   python3 "$WORK/sink.py" 9542 "$WORK/sink.pem" "$WORK/sink.key" > "$WORK/bob-sink.log" 2>&1 &
   ```

   **Expect.** Both listeners are running, and both log files are empty.

2. Boot a multi-tenant `trusted-headers` server with carol as the bootstrap
   admin of the `default` tenant and dave as an operator.

   ```bash
   export SECRET="mv-proxy-$SFX"
   PODIUM_IDENTITY_PROVIDER=trusted-headers PODIUM_MULTI_TENANT=true \
   PODIUM_TRUSTED_PROXY_SECRET="$SECRET" \
   PODIUM_BOOTSTRAP_ADMINS="carol-$SFX@acme.com" \
   PODIUM_OPERATOR_ADMINS="dave-$SFX@acme.com" \
   PODIUM_WEBHOOK_ALLOWED_TARGETS=127.0.0.1/32 \
   SSL_CERT_FILE="$WORK/sink.pem" \
   PODIUM_NO_EMBEDDINGS=true \
   podium serve --bind 127.0.0.1:8184 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   export PODIUM_REGISTRY=http://127.0.0.1:8184
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "$PODIUM_REGISTRY/healthz"
   server_alive "$SRV" "$WORK/srv.log"
   as() { # as <user> <org> <curl args...>
     local u="$1" o="$2"; shift 2
     if [ -n "$o" ]; then
       curl -s -w '\n%{http_code}\n' -H "X-Podium-Proxy-Secret: $SECRET" \
         -H "X-Podium-User-Sub: $u" -H "X-Podium-User-Org: $o" "$@"
     else
       curl -s -w '\n%{http_code}\n' -H "X-Podium-Proxy-Secret: $SECRET" \
         -H "X-Podium-User-Sub: $u" "$@"
     fi
   }
   ```

   **Expect.** `server_alive` reports the server running. The boot passes no
   layer path, because step 6 registers the scenario's layers over HTTP, and
   `PODIUM_NO_EMBEDDINGS=true` keeps search BM25-only, so the boot needs no
   embedding provider or API key. The `as` helper sends the organization header
   through an explicit branch, so the block behaves the same under bash and zsh.

3. dave provisions the `globex` tenant, and the operator seeds bob's admin grant
   in it directly in Postgres. No API grants a new tenant's first admin.

   ```bash
   as "dave-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/admin/tenants" \
     -H 'Content-Type: application/json' -d "{\"name\":\"globex-$SFX\"}" | tee "$WORK/tenant.json"
   GLOBEX_ID=$(sed '$d' "$WORK/tenant.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   psql "$PODIUM_POSTGRES_DSN" -c "INSERT INTO \"org_$GLOBEX_ID\".admin_grants (user_id, org_id, granted_at) VALUES ('bob-$SFX@globex.com', '$GLOBEX_ID', now());"
   ```

   **Expect.** The provisioning request returns HTTP 201 with the tenant's ID,
   and `psql` reports `INSERT 0 1`.

4. carol registers a receiver for her listener, and bob registers one for his.

   ```bash
   as "carol-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/webhooks" \
     -H 'Content-Type: application/json' -d '{"url":"https://127.0.0.1:9541/h"}' | tee "$WORK/ra.json"
   as "bob-$SFX@globex.com" "globex-$SFX" -X POST "$PODIUM_REGISTRY/v1/webhooks" \
     -H 'Content-Type: application/json' -d '{"url":"https://127.0.0.1:9542/h"}' | tee "$WORK/rb.json"
   RA=$(sed '$d' "$WORK/ra.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   RB=$(sed '$d' "$WORK/rb.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   ```

   **Expect.** Both requests return HTTP 201 with the receiver and its unmasked
   secret.

5. Each admin lists receivers, and carol addresses bob's receiver by its `id`.

   ```bash
   as "carol-$SFX@acme.com" default "$PODIUM_REGISTRY/v1/webhooks"
   as "bob-$SFX@globex.com" "globex-$SFX" "$PODIUM_REGISTRY/v1/webhooks"
   as "carol-$SFX@acme.com" default "$PODIUM_REGISTRY/v1/webhooks/$RB"
   as "carol-$SFX@acme.com" default -X PUT "$PODIUM_REGISTRY/v1/webhooks/$RB" \
     -H 'Content-Type: application/json' -d '{"disabled":true}'
   as "carol-$SFX@acme.com" default -X DELETE "$PODIUM_REGISTRY/v1/webhooks/$RB"
   as "bob-$SFX@globex.com" "globex-$SFX" "$PODIUM_REGISTRY/v1/webhooks/$RB"
   as "carol-$SFX@acme.com" initech "$PODIUM_REGISTRY/v1/webhooks"
   as "carol-$SFX@acme.com" "" "$PODIUM_REGISTRY/v1/webhooks"
   ```

   **Expect.** carol's list holds `$RA` alone, and bob's list holds `$RB` alone.
   carol's `GET` and `PUT` of `$RB` return HTTP 404 with `registry.not_found`.
   carol's `DELETE` of `$RB` returns HTTP 204. bob's `GET` of `$RB` then
   returns HTTP 200 with `disabled: false`. The `initech` request and the
   request with no organization header each return HTTP 403 with
   `auth.forbidden`.

6. carol registers two user-defined layers whose git URLs are under `.invalid`,
   so no fetch runs, and reorders them in reverse registration order, which
   publishes `layer.config_changed`. Each body sets `user_defined`, because
   carol administers the `default` tenant and an admin registration without it
   is admin-defined, which publishes its own `layer.config_changed`.

   ```bash
   for id in "mv-a-$SFX" "mv-b-$SFX"; do
     as "carol-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/layers" \
       -H 'Content-Type: application/json' \
       -d "{\"id\":\"$id\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/acme/$id.git\",\"ref\":\"main\",\"user_defined\":true}"
   done
   as "carol-$SFX@acme.com" default -X POST "$PODIUM_REGISTRY/v1/layers/reorder" \
     -H 'Content-Type: application/json' -d "{\"order\":[\"mv-b-$SFX\",\"mv-a-$SFX\"]}"
   sleep 5
   cat "$WORK/carol-sink.log"; echo ---; cat "$WORK/bob-sink.log"
   ```

   **Expect.** Each registration returns HTTP 201, and the reorder returns HTTP
   200. `carol-sink.log` holds a `SIG` line with a non-empty signature and a
   `BODY` line whose `event` is `layer.config_changed` and whose `data.layer`
   names both layers. `bob-sink.log` is empty.

---

## S87: Layer operations act in the caller's tenant on a multi-tenant registry

**Goal.** Validate that on a multi-tenant registry the layer endpoints read and
write the layer list of the tenant the caller's organization names, that a
bootstrap admin can manage the default tenant's layers, that the advertised
webhook URL carries the tenant ID, and that a caller whose organization names
no tenant lists nothing and cannot write.

**Covers.** §6.3.1 per-request tenant selection and §7.3.1 Tenant selection on
the layer endpoints, through the compiled binary under `trusted-headers`.

**Why by hand.** An operator reads the raw layer lists of callers in different
tenants side by side. A user-defined class on carol's admin register, a dave layer
in carol's list, or a `webhook_url` without the tenant ID is the defect this
scenario catches. The standard-stack end-to-end test skips silently on macOS
without the stack.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env` (Postgres DSN, S3 settings). Skip if either is absent.

**Steps.**

1. Run the isolation block. Start services, load the environment, and boot a
   multi-tenant registry behind `trusted-headers`.

   ```bash
   (cd "$REAL_HOME/projects/podium" && make services-up)
   set -a; source $REAL_HOME/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_MULTI_TENANT=true
   export PODIUM_IDENTITY_PROVIDER=trusted-headers
   export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
   export PODIUM_OPERATOR_ADMINS=olivia@acme.com
   export PODIUM_BOOTSTRAP_ADMINS=carol@acme.com
   S=$$
   podium serve --no-embeddings --bind 127.0.0.1:8188 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8188/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8188
   as() { curl -s -H "X-Podium-Proxy-Secret: gateway-secret" -H "X-Podium-User-Sub: $1" -H "X-Podium-User-Org: $2" "${@:3}"; }
   ```

   **Expect.** `server_alive` reports the server running, and the log carries
   `multi-tenant mode: routing requests by organization`.

2. Provision a per-run tenant as the operator. The `podium` CLI sends only a
   bearer token, so this step uses curl with the trusted headers.

   ```bash
   as olivia@acme.com default -X POST -H 'Content-Type: application/json' \
     -d "{\"name\":\"globex-$S\"}" "$URL/v1/admin/tenants" | tee "$WORK/tenant.json"
   GLOBEX=$(python3 -c "import json,sys; print(json.load(open('$WORK/tenant.json'))['id'])")
   echo "globex tenant id: $GLOBEX"
   ```

   **Expect.** The response carries an `id`, and `$GLOBEX` is a UUID rather
   than the string `globex-<pid>`.

3. As carol (org `default`), register an admin-defined Git layer.

   ```bash
   as carol@acme.com default -w "\nHTTP %{http_code}\n" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"team-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/acme/team.git\",\"ref\":\"main\"}" "$URL/v1/layers" \
     | tee "$WORK/team.json"
   python3 -c "import json; print('user_defined:', json.loads(open('$WORK/team.json').read().rsplit('HTTP',1)[0])['layer']['user_defined'])"
   ```

   **Expect.** `HTTP 201` and `user_defined: False`. `user_defined: True`
   means the admin gate still checks the unrouted tenant, which resolves
   carol's registration to the user-defined class.

4. As dave (org `globex-<pid>`), register a user layer, then list as dave and
   as carol.

   ```bash
   as dave@acme.com "globex-$S" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"dave-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/notes.git\",\"ref\":\"main\"}" "$URL/v1/layers" \
     | python3 -c "import json,sys; print('webhook_url:', json.load(sys.stdin).get('webhook_url'))"
   echo "dave sees:  $(as dave@acme.com "globex-$S" "$URL/v1/layers" | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])")"
   echo "carol sees: $(as carol@acme.com default "$URL/v1/layers" | python3 -c "import json,sys; print([l['id'] for l in json.load(sys.stdin)['layers']])")"
   ```

   **Expect.** `webhook_url` contains `/v1/ingest/webhook/$GLOBEX/dave-<pid>`.
   dave's list contains `dave-<pid>` and not `team-<pid>`. carol's list
   contains `team-<pid>` and not `dave-<pid>`.

5. As erin (org `initech-<pid>`, which names no tenant), list and register.

   ```bash
   echo "erin list: $(as erin@acme.com "initech-$S" "$URL/v1/layers")"
   as erin@acme.com "initech-$S" -X POST -H 'Content-Type: application/json' \
     -d "{\"id\":\"erin-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/erin/x.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   ```

   **Expect.** The list is `{"layers":[]}`. The register answers `403` with
   error code `auth.forbidden`.

**Cleanup.** Delete `team-<pid>` and `dave-<pid>` with `DELETE /v1/layers?id=...`
as carol and dave, stop the server, `rm -rf "$WORK"`, and
`(cd "$REAL_HOME/projects/podium" && make services-down)` when finished with the
standard-mode scenarios.

---

## S88: Erasure on a multi-tenant registry redacts only the requesting tenant's audit records

**Goal.** Validate that `POST /v1/admin/erase` on a multi-tenant registry
purges and redacts only in the caller's tenant, leaves other tenants' records
and unlabeled records unchanged, re-anchors the chain, and that the offline form then redacts the remainder with a chain that
still verifies.

**Covers.** §8.5 tenant-scoped erasure, the §8.1 `tenant` attribute, and the
§8.6 re-anchor after an erasure, over §6.3.1 routing, through the compiled
binary under `trusted-headers`.

**Why by hand.** An operator reads the raw audit log across tenants. A globex
record carrying dave's tombstone after carol's erase, a record carrying
`"tenant":"podium:unrouted"`, a `user.erased` with no `tenant`, or an
`audit.gap_detected` after the restart is the defect this scenario catches.
The standard-stack end-to-end test skips silently on macOS.

**Prerequisites.** Local Postgres and MinIO from `make services-up`, plus
`test.env`. Skip if either is absent.

**Steps.**

1. Run S87 step 1 with these additions before `podium serve`. Leave
   `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` unset, so the verify scheduler runs
   at its default interval and verifies once at start.

   ```bash
   export PODIUM_AUDIT_LOG_PATH="$WORK/audit.log"
   export PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor.key"
   export PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=3600
   ```

   **Expect.** `server_alive` reports the server running. The step makes no
   claim about `$WORK/audit.log`, which `FileSink` creates on its first
   append; step 3 creates it if nothing has yet.

2. Run S87 step 2 to provision `globex-$S` as olivia.

   **Expect.** `$GLOBEX` is a UUID.

3. Create unlabeled records attributed to dave. Stop the registry, append one
   record with an empty tenant, and restart it.

   ```bash
   kill "$SRV"; wait "$SRV" 2>/dev/null
   cat > "$WORK/seed.go" <<'EOF'
   package main

   import (
       "context"
       "os"
       "time"

       "github.com/lennylabs/podium/pkg/audit"
   )

   func main() {
       s, err := audit.NewFileSink(os.Args[1])
       if err != nil { panic(err) }
       if err := s.Append(context.Background(), audit.Event{Type: "artifact.loaded", Caller: "dave@acme.com", Target: "legacy", Timestamp: time.Now().UTC()}); err != nil { panic(err) }
   }
   EOF
   go -C "$REAL_HOME/projects/podium" run "$WORK/seed.go" "$PODIUM_AUDIT_LOG_PATH"
   podium serve --no-embeddings --bind 127.0.0.1:8188 >> "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "$URL/healthz"
   jq -c 'select(.target=="legacy") | {caller: .caller.identity, tenant}' "$PODIUM_AUDIT_LOG_PATH"
   ```

   **Expect.** One line, `{"caller":"dave@acme.com","tenant":null}`.

4. As dave, register a user layer in `default` and one in `globex-$S`.

   ```bash
   for org in default "globex-$S"; do
     as dave@acme.com "$org" -s -o /dev/null -w "%{http_code}\n" -X POST -H 'Content-Type: application/json' \
       -d "{\"id\":\"dave-$org-$S\",\"source_type\":\"git\",\"repo\":\"https://git.invalid/dave/notes.git\",\"ref\":\"main\"}" "$URL/v1/layers"
   done
   jq -r 'select(.caller.identity=="dave@acme.com") | .tenant // "-"' "$PODIUM_AUDIT_LOG_PATH" | sort | uniq -c
   ```

   **Expect.** Two `201` codes. The tenant counts list the `default` tenant ID,
   `$GLOBEX`, and `-` (the seeded record). No line reads `podium:unrouted`.

5. As carol, the `default` admin, erase dave.

   ```bash
   as carol@acme.com default -X POST -H 'Content-Type: application/json' \
     -d '{"user_id":"dave@acme.com","salt":"s88-salt"}' "$URL/v1/admin/erase" | tee "$WORK/erase.json"
   ```

   **Expect.** HTTP 200. `layers_purged` lists only `dave-default-<pid>`, and
   the body carries only `erased`, `layers_purged`, and
   `audit_events_redacted`.

6. Inspect the log.

   ```bash
   jq -c 'select(.caller.identity=="dave@acme.com") | {type, tenant}' "$PODIUM_AUDIT_LOG_PATH"
   jq -c 'select(.type=="user.erased") | {tenant, context}' "$PODIUM_AUDIT_LOG_PATH"
   jq -r '.type' "$PODIUM_AUDIT_LOG_PATH" | awk '/user.erased/{e=1} e && /audit.anchored/{print "anchored after erase"; exit}'
   ```

   **Expect.** No remaining dave record carries the `default` tenant ID; the
   remaining ones carry `$GLOBEX` or no tenant. `user.erased` carries the
   `default` tenant ID and a non-empty `superseded_head`. The last command
   prints `anchored after erase`.

7. Stop the registry and run the offline form against its file.

   ```bash
   kill "$SRV"; wait "$SRV" 2>/dev/null
   podium admin erase dave@acme.com --local --audit-path "$PODIUM_AUDIT_LOG_PATH" \
     --operator olivia@acme.com --salt s88-salt
   jq -c 'select(.caller.identity=="dave@acme.com")' "$PODIUM_AUDIT_LOG_PATH" | wc -l
   ```

   **Expect.** The command reports a redacted count of 1 or more, and the
   final count is `0`: the globex and unlabeled records now carry the
   tombstone.

8. Restart the registry and check the chain.

   ```bash
   podium serve --no-embeddings --bind 127.0.0.1:8188 >> "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null "$URL/healthz"
   jq -r 'select(.type=="audit.gap_detected") | .type' "$PODIUM_AUDIT_LOG_PATH" | wc -l
   grep -c "gap" "$WORK/srv.log"
   ```

   **Expect.** Both counts are `0`. The verify scheduler checks the chain at
   start, so a non-zero count means the rewritten chain does not verify.

**Cleanup.** Delete `dave-globex-$S-<pid>` with `DELETE /v1/layers?id=...` as
dave, stop the server, `rm -rf "$WORK"`, and
`(cd "$REAL_HOME/projects/podium" && make services-down)` when finished with the
standard-mode scenarios.

---
