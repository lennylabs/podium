# Manual validation scenarios

This document is a set of end-to-end scenarios for validating a Podium build by
hand. Each scenario is a self-contained sequence a person runs in a terminal,
observes, and checks against an explicit list of expected results. The
scenarios cover the deployment modes (solo filesystem, standalone server, and
standard server), embeddings on and off, the local and managed vector backends,
single and multiple layers backed by real Git repositories, the four harness
adapters, and the governance features (per-caller visibility, admin RBAC,
signing, public mode, lifecycle, and migration).

The same scenarios are executed by the agentic workflow in
`tools/workflows` (the `agentic-manual-validation` workflow), which runs one
scenario at a time, validates the observed output, and fixes any product bug it
finds. A person and the workflow follow the identical steps.

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

### Per-scenario isolation

Run each scenario in a fresh shell and start with this block. It redirects all
server and client state into a throwaway directory so the run never touches the
real `~/.podium`, and it puts the fresh build first on `PATH`.

```bash
export PODIUM_BIN="$HOME/projects/podium/bin"
export PATH="$PODIUM_BIN:$PATH"; hash -r
export WORK="$(mktemp -d)"
export PODIUM_SQLITE_PATH="$WORK/podium.db"
export PODIUM_FILESYSTEM_ROOT="$WORK/objects"
export PODIUM_AUDIT_LOG_PATH="$WORK/audit.log"
export PODIUM_CACHE_DIR="$WORK/cache"
export PODIUM_TOKEN_KEYCHAIN_NAME="podium-manual-$$"
unset PODIUM_REGISTRY PODIUM_HARNESS PODIUM_SESSION_TOKEN
which podium    # must print $PODIUM_BIN/podium
```

Confirm `which podium` prints the path under `$PODIUM_BIN`. If it prints a
Homebrew or other path, the `PATH` export did not take; open a new shell and
repeat.

### Conventions

- Command flags come before positional arguments. `podium search --registry
  "$URL" "query"` works; `podium search "query" --registry "$URL"` does not.
- Server scenarios start `podium serve` in the background and bind a loopback
  port. The cleanup step stops the server and removes `$WORK`.
- A registry directory is a tree of artifact directories. `podium artifact
  scaffold --type <type> <path>` writes one artifact at `<path>`; the artifact
  name is the last path element.
- Scenarios that need live infrastructure name it under Prerequisites. When the
  infrastructure or credentials are absent, the scenario is skipped rather than
  forced. Record the skip and the reason.

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

- `layer list` shows `base` and `team` in order (`base` at `Order` 1, `team` at
  `Order` 2).
- `layer reingest team` reports `greet` rejected with code `ingest.collision`
  and a reason naming the layer that already contributed it: `cross-layer
  collision: "greet" already contributed by layer "base"; declare extends: greet
  to overlay it`. The team layer's non-colliding `deploy` is ingested.
- Searching `greet` returns a single `greet` artifact whose description is the
  base layer's (`Base greet`), confirming the base artifact survives and the
  colliding team artifact was rejected rather than silently shadowing it.
- Searching `deploy` returns the team-only `deploy` skill.
- `artifact show greet` prints the base layer's body.

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
~/projects/podium/test.env; set +a`.

**Steps.**

1. Run the isolation block, then load the key.

   ```bash
   set -a; source ~/projects/podium/test.env; set +a
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
  `LastIngestedRef` advances to the new commit), and the post-reingest search
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
   export PODIUM_REGISTRY=http://127.0.0.1:8107
   ```

3. Drive the MCP bridge over stdio with two JSON-RPC requests: initialize, then
   list tools.

   ```bash
   printf '%s\n%s\n' \
     '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"manual","version":"0"}}}' \
     '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
     | PODIUM_REGISTRY="$PODIUM_REGISTRY" podium-mcp 2>"$WORK/mcp.log" | head -40
   ```

4. Optionally, wire the bridge into Claude Code (`claude mcp add podium --
   env PODIUM_REGISTRY=$PODIUM_REGISTRY -- $PODIUM_BIN/podium-mcp`), open the
   harness, and ask it to search the catalog. This part is observed in the
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

3. Generate a runtime key, boot the server in injected-session-token mode, and
   register the key. Seed SCIM so the `engineering` group resolves.

   ```bash
   go run ./tools/minttoken --keys "$WORK/keys" >/dev/null 2>&1   # writes the keypair
   export PODIUM_IDENTITY_PROVIDER=injected-session-token
   export PODIUM_OAUTH_AUDIENCE=https://podium.manual
   export PODIUM_SCIM_TOKENS=scim-secret
   export PODIUM_SCIM_STORE_PATH="$WORK/scim.json"
   podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8108 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8108/healthz
   export PODIUM_REGISTRY=http://127.0.0.1:8108
   podium admin runtime register --registry "$PODIUM_REGISTRY" --issuer manual-runtime --algorithm RS256 --public-key-file "$WORK/keys/runtime-pub.pem"
   ```

   Provision `alice@acme.com` into the `engineering` SCIM group and leave
   `bob@acme.com` out of it. Use the SCIM endpoint with the `scim-secret`
   bearer; the precise SCIM calls are in `docs/` and the
   `seedSCIM` helper in `test/e2e/authserver_harness_test.go`.

4. Mint a token for each caller and search.

   ```bash
   ALICE=$(go run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com --groups engineering)
   BOB=$(go run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
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
2. Boot an injected-session-token server with `alice@acme.com` as a bootstrap
   admin, over a small registry, and register the runtime key (as in S12, with
   `PODIUM_BOOTSTRAP_ADMINS=alice@acme.com` added and `--bind 127.0.0.1:8109`).
3. Exercise the admin surface as alice (admin) and bob (non-admin).

   ```bash
   ALICE=$(go run ./tools/minttoken --keys "$WORK/keys" --sub alice@acme.com --email alice@acme.com)
   BOB=$(go run ./tools/minttoken --keys "$WORK/keys" --sub bob@acme.com --email bob@acme.com)
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
2. Start services and load the environment.

   ```bash
   cd ~/projects/podium && make services-up
   set -a; source ~/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_VECTOR_BACKEND=pgvector
   export PODIUM_EMBEDDING_PROVIDER=openai
   export PODIUM_EMBEDDING_MODEL=text-embedding-3-small
   ```

3. Author a registry that includes a large resource file, then serve in strict
   mode.

   ```bash
   podium artifact scaffold --type skill --description "Generate a quarterly report" "$WORK/reg/report"
   head -c 2000000 /dev/urandom | base64 > "$WORK/reg/report/big-template.txt"
   podium serve --strict --layer-path "$WORK/reg" --bind 127.0.0.1:8110 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8110/healthz
   export PODIUM_REGISTRY=http://127.0.0.1:8110
   podium config show --server | grep -E 'store|object_store|vector'
   podium search --registry "$PODIUM_REGISTRY" "quarterly report"
   podium artifact show --registry "$PODIUM_REGISTRY" report
   curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=report" \
     | python3 -c "import sys,json; d=json.load(sys.stdin); print('large_resources:', json.dumps(d.get('large_resources'), indent=2)); print('inline resources:', list((d.get('resources') or {}).keys()))"
   ```

**Expected.**

- `config show --server` reports the Postgres store, the S3 object store, and
  the pgvector backend.
- The server boots and `healthz` returns 200.
- Semantic search returns the `report` skill.
- The large resource is stored in S3 and served through a presigned URL when
  loaded. The `load_artifact` response lists `big-template.txt` under
  `large_resources` with a presigned `http://localhost:9000/podium/...` URL and
  an empty inline `resources` map, so the control plane does not stream the
  large body inline (§7.2 sets the inline cutoff at 256 KB).

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and `make services-down` when
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

   ```bash
   cd ~/projects/podium && make services-up
   set -a; source ~/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_VECTOR_BACKEND=pinecone
   export PODIUM_EMBEDDING_PROVIDER=openai
   export PODIUM_EMBEDDING_MODEL=text-embedding-3-small
   export PODIUM_PINECONE_NAMESPACE="manual-s15-$$-$(date +%s)"
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
   export PODIUM_REGISTRY=http://127.0.0.1:8111
   podium config show --server | grep -E 'store|object_store|vector|embedding'
   podium search --registry "$PODIUM_REGISTRY" "close the books for the month"
   ```

**Expected.**

- `config show --server` reports the Pinecone backend.
- The server log shows vectors upserted into the managed index during ingest,
  namespaced per run so a shared index is not polluted across runs.
- The paraphrased query returns the `reconcile` skill as the top result.
- Repeating the scenario against Weaviate or Qdrant produces the same ranking.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.

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
   cd ~/projects/podium && make services-up
   set -a; source ~/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres
   export PODIUM_OBJECT_STORE=s3
   export PODIUM_VECTOR_BACKEND=pinecone
   export PODIUM_PINECONE_INDEX="$PODIUM_PINECONE_SELFEMBED_INDEX"
   export PODIUM_PINECONE_NAMESPACE="manual-s16-$$-$(date +%s)"
   unset PODIUM_EMBEDDING_PROVIDER PODIUM_EMBEDDING_MODEL
   ```

2. Author the S07 registry, serve in strict mode on `127.0.0.1:8112`, and query.

   ```bash
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

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and drop the throwaway registry
store created for the run.

---

## S17: Public mode and the sensitivity floor

**Goal.** Validate public mode: anonymous callers read the catalog, and the
configured sensitivity floor blocks artifacts above the threshold.

**Covers.** Standalone deployment, public mode, anonymous access, the
sensitivity floor.

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
   export PODIUM_REGISTRY=http://127.0.0.1:8113
   podium status
   podium search --registry "$PODIUM_REGISTRY" ""
   podium artifact show --registry "$PODIUM_REGISTRY" faq
   podium artifact show --registry "$PODIUM_REGISTRY" incident
   ```

**Expected.**

- `podium status` reports `registry mode: public`.
- The anonymous search and `artifact show faq` succeed.
- `artifact show incident` is refused by the public-mode sensitivity floor,
  because a `high`-sensitivity artifact is not served to an anonymous caller.

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
   export PODIUM_REGISTRY=http://127.0.0.1:8114
   podium layer register --registry "$PODIUM_REGISTRY" --id team --repo "$WORK/repo" --ref main --public
   podium layer reingest --registry "$PODIUM_REGISTRY" team
   ```

3. Publish version 2.0.0 by editing the artifact's `version` and committing,
   then re-ingest. A bare `artifact show` resolves `latest`, which is the most
   recently ingested non-deprecated version, so it reports 2.0.0.

   ```bash
   # bump the version in $WORK/repo/deploy/ARTIFACT.md to 2.0.0, then:
   cd "$WORK/repo" && git commit -aqm "deploy 2.0.0"
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
   cd "$WORK/repo" && git commit -aqm "deploy 3.0.0 deprecated"
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
high-sensitivity artifact loads under a verification policy, and an unsigned one
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
   export PODIUM_REGISTRY=http://127.0.0.1:8115
   grep "ingest signing" "$WORK/srv.log"
   ```

3. Confirm the registry stored a signature at ingest, then load the artifact.
   `podium artifact show` prints the body without verifying; the signature lives
   in the `load_artifact` response and consumer-side verification happens at
   materialization (next step).

   ```bash
   curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=signed-runbook" \
     | python3 -c 'import sys,json; print(json.load(sys.stdin)["signature"])'
   export PODIUM_VERIFY_SIGNATURES=medium-and-above
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
   without `--sign`, and load it through the bridge under the same enforcing
   policy. An unsigned high-sensitivity artifact is refused.

   ```bash
   podium artifact scaffold --type skill --sensitivity high --description "Unsigned runbook" "$WORK/reg-unsigned/unsigned-runbook"
   PODIUM_SQLITE_PATH="$WORK/podium2.db" PODIUM_FILESYSTEM_ROOT="$WORK/objects2" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg-unsigned" \
     --bind 127.0.0.1:8116 > "$WORK/srv-unsigned.log" 2>&1 &
   SRV2=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8116/healthz
   echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"unsigned-runbook"}}}' \
     | PODIUM_REGISTRY=http://127.0.0.1:8116 \
       PODIUM_HARNESS=none \
       PODIUM_MATERIALIZE_ROOT="$WORK/out-unsigned" \
       PODIUM_VERIFY_SIGNATURES=medium-and-above \
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
  response carries a `signature` envelope (`{"key_id":...,"signature":...}`).
- `podium artifact show` prints the signed artifact's body. The CLI read path
  does not verify; it confirms the artifact loads.
- With `PODIUM_VERIFY_SIGNATURES=medium-and-above`, loading the signed
  high-sensitivity artifact through the MCP bridge verifies the signature and
  materializes the artifact under `$WORK/out`.
- An unsigned high-sensitivity artifact loaded under the same policy fails with
  `materialize.signature_invalid` (`signature_missing: sensitivity "high"
  requires a signature`) and writes nothing. A signature that does not validate
  against the configured public key fails the same way
  (`signature_invalid: signature does not verify`).
- `PODIUM_VERIFY_SIGNATURES` accepts `never`, `medium-and-above`, or `always`.
  Any other value exits the bridge with a nonzero status and the message
  `PODIUM_VERIFY_SIGNATURES must be never | medium-and-above | always`.

**Cleanup.** Stop both servers (`kill "$SRV" "$SRV2"`) and `rm -rf "$WORK"`.

---

## S20: Migration from standalone to standard

**Goal.** Validate `admin migrate-to-standard`: state authored in a standalone
SQLite plus filesystem deployment lands in Postgres plus S3 with parity.

**Covers.** Standalone deployment, the migration command, standard deployment,
cross-store parity.

**Prerequisites.** `make services-up` and `test.env` (Postgres, S3). Skip if
absent.

**Steps.**

1. Run the isolation block.
2. Build standalone state: author a registry, serve standalone, register a
   Git layer, and confirm a search returns results (as in S09, on
   `127.0.0.1:8116`). Stop the standalone server.
3. Load the standard-store environment and run the migration. The migration
   command takes its target from `--postgres <dsn>` and `--object-store <url>`
   (the §13.4 short form). The `--object-store` S3 URL carries the endpoint,
   bucket, credentials, region, and TLS toggle from `test.env`. The standalone
   source lives under `$WORK`, so name it with `--source-sqlite` and
   `--source-objects`. The `PODIUM_REGISTRY_STORE`, `PODIUM_OBJECT_STORE`, and
   `PODIUM_VECTOR_BACKEND` exports select the standard backends for the
   `podium serve --strict` run in step 4.

   ```bash
   set -a; source ~/projects/podium/test.env; set +a
   export PODIUM_REGISTRY_STORE=postgres PODIUM_OBJECT_STORE=s3 PODIUM_VECTOR_BACKEND=pgvector
   S3URL="s3://${PODIUM_S3_ACCESS_KEY_ID}:${PODIUM_S3_SECRET_ACCESS_KEY}@localhost:9000/${PODIUM_S3_BUCKET}?region=${PODIUM_S3_REGION}&ssl=false"
   podium admin migrate-to-standard \
     --postgres "$PODIUM_POSTGRES_DSN" \
     --object-store "$S3URL" \
     --source-sqlite "$WORK/podium.db" \
     --source-objects "$WORK/objects"
   ```

4. Serve in strict mode against the standard stores and compare.

   ```bash
   podium serve --strict --bind 127.0.0.1:8117 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 60 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8117/healthz
   export PODIUM_REGISTRY=http://127.0.0.1:8117
   podium layer list --registry "$PODIUM_REGISTRY"
   podium search --registry "$PODIUM_REGISTRY" "deploy"
   ```

**Expected.**

- The migration command reports the layers, artifacts, and objects pumped into
  Postgres and S3.
- The standard server lists the same layers and returns the same artifacts in
  search as the standalone deployment did.

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and `make services-down`.

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

**Steps.**

1. Run the isolation block, start services, load `test.env`, and serve in strict
   mode with Postgres and S3 (as in S14, on `127.0.0.1:8118`). Confirm a search
   works.
2. Stop the Postgres container (`docker stop` the database service), wait for the
   health probe to flip, and observe.

   ```bash
   podium status
   podium search --registry "$PODIUM_REGISTRY" "report"
   podium layer register --registry "$PODIUM_REGISTRY" --id new --repo "$WORK/repo" --ref main --public
   ```

3. Restart Postgres and confirm recovery.

**Expected.**

- After Postgres stops, `podium status` reports `registry mode: read_only`.
- Reads (search and load) continue to serve from the available stores and cache.
- The write (`layer register`) is refused with a read-only error.
- After Postgres restarts, the mode returns to ready and writes succeed again.

**Cleanup.** Stop the server, `rm -rf "$WORK"`, and `make services-down`.
