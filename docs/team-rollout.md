# Small-team rollout

This is a practical guide for getting Podium running for a small team — roughly 3 to 10 people, one or two harnesses (Claude Code and/or Cursor, typically), one shared catalog. If you're rolling out to a larger org, this guide will be too informal; the [operator's guide](operator-guide.md) is more useful starting around 20 people or wherever governance, on-call, and multi-tenant concerns enter the picture.

The shape: one platform-engineer-equivalent (you, probably) does the setup over a couple of focused afternoons; the rest of the team starts authoring within a week.

## What you're deciding upfront

Before installing anything, settle three questions:

1. **Who runs the registry?** Pick one of:
   - **A shared standalone instance on a small VM** — a single `podium serve --standalone` on an EC2 / Hetzner / DO instance, bound to your VPN or to the office network. Cheapest. Good for teams that already share a VPN.
   - **A standard deployment with a managed Postgres + S3** — heavier ops, but matches the long-term shape and avoids a migration later. Pick this if the team already runs services in Kubernetes / has a managed-Postgres habit.

   For a small team, standalone-on-a-VM is almost always the right call. Standard deployment becomes worth it once you need OIDC group membership (for visibility), multi-tenancy, or production-grade availability.

2. **Where does authoring live?** Either a single Git repo that everyone has push access to, or a `local`-source layer rooted at a directory the registry can read. Git is the better default — it inherits your team's review workflow. Use `local` only if you're doing fast iteration on the same machine the registry runs on.

3. **What harnesses do people use?** If everyone is on Claude Code, set `PODIUM_HARNESS=claude-code` once and forget it. If the team is split (some Cursor, some Claude Code), keep `PODIUM_HARNESS` in each developer's MCP config rather than a registry-wide setting.

## Day 1 — get the registry up

Do one of:

**Standalone on a VM** (recommended for small teams):

```bash
# On the VM
podium serve --standalone \
  --layer-path /var/podium/artifacts \
  --bind 0.0.0.0:8080
```

Front it with TLS via Caddy / Cloudflare Tunnel / an ALB — never expose plain HTTP to the network. Verify clients can reach it:

```bash
podium init --remote https://podium.your-team.example
podium domain show
```

**Standard deployment**: follow the [operator's guide](operator-guide.md). Plan for a half-day of setup (Postgres, S3, OIDC client registration, Helm values).

## Day 1 — pick a layer plan

For a small team, two layers is plenty:

- **`team-shared`** — one Git repo, organization-visible (or open to the VPN if standalone). Holds skills, agents, contexts, and prompts the team agrees on.
- **`<person>-personal`** — each developer registers their own user-defined layer for in-progress work. Default visibility is `users: [<self>]`; capped at 3 per person.

The workspace local overlay (`<workspace>/.podium/overlay/`) sits on top of those automatically and doesn't need any registry-side setup.

Don't pre-build a multi-team hierarchy "just in case." Add layers as the team actually outgrows the shared layer.

## Day 1 — register the shared layer

```bash
# Standalone with a Git source
podium layer register \
  --id team-shared \
  --repo git@github.com:your-team/podium-artifacts.git \
  --ref main

# Or local-source on the VM
podium layer register \
  --id team-shared \
  --local /var/podium/artifacts/team-shared
```

The first form prints a webhook URL + HMAC secret. Add it to the GitHub repo so merges trigger reingest automatically.

## Day 2 — onboard one person end-to-end

Before sending anything to the team, walk one person through it yourself. Catch the stupid stuff early.

For that person:

1. Install `podium` and `podium-mcp`.
2. `podium init --remote https://podium.your-team.example`.
3. `podium login` — completes the OAuth device-code flow (or skips if you're on standalone with no auth).
4. Add Podium to their Claude Code MCP config (the snippet in §6.11 of the spec).
5. Restart Claude Code. Confirm the meta-tools appear.
6. Author a small test skill in the `team-shared` repo, push, merge. Confirm it appears in `podium search`.

If anything is awkward, fix it before scaling up. Common issues at this step are MCP config typos and webhook secrets being copied wrong.

## Week 1 — onboard the rest

Send the team a short message:

```
Podium is running at https://podium.your-team.example.

To get started:

1. Install podium and podium-mcp (see <link>).
2. Run: podium init --remote https://podium.your-team.example
3. Run: podium login (one-time browser flow)
4. Add Podium to your harness MCP config (see <link to your team's snippet>).
5. Restart your harness.

Author shared skills by opening a PR against
github.com/your-team/podium-artifacts. Author personal skills by registering
your own layer — see <link to a 5-minute video or doc>.

Questions: <Slack channel or email>.
```

Keep the loop tight. The first week is for de-risking, not for hitting an artifact-count target.

## Week 2+ — let it accrete

Don't define standards for skill descriptions until you have ~20 skills and can see what actually gets searched-for. Don't add freeze windows until you have something to freeze. Don't introduce `sensitivity: medium` until at least one skill has content that matters.

Watch for:

- **Skills that nobody loads.** If `podium search` returns it but `podium audit` shows zero `load_artifact` calls in 30 days, the description is probably wrong (or the skill isn't useful). Fix the description first.
- **Drift between people's harnesses.** If one person's Claude Code shows a skill and another's doesn't, check `PODIUM_HARNESS` and the OAuth identity (`podium login` and confirm the `sub` matches expectations).
- **The `team-shared` repo growing past ~50 artifacts.** Time to consider per-domain `DOMAIN.md` files for curation, or splitting into multiple layers (e.g., `team-shared-engineering` and `team-shared-design`).

## When to graduate from standalone

You probably want to migrate to a standard deployment when any of these holds:

- More than ~10 active authors.
- You need OIDC group-based visibility (some skills visible to one team, not another).
- You need real availability (the VM going down stops everyone).
- You need to host the registry for multiple tenants (separate engineering / sales / external-collaborator catalogs).

Migrate via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (§13.4). Layer config, audit history, and grants are preserved.

## What this guide deliberately doesn't cover

- **Multi-tenancy.** Don't enable it until you have two or more meaningfully separate audiences. The single-tenant standalone model handles small teams.
- **Signing.** The default for standalone (`PODIUM_VERIFY_SIGNATURES=never`) is fine until you have content where authorship integrity matters (e.g., production runbooks). When you do, set it to `medium-and-above` and start signing artifacts above that sensitivity.
- **SBOM / CVE tracking.** Out of scope for standalone (§13.10). Worth turning on once you're shipping skills that pull in third-party Python or shell scripts.
- **Freeze windows.** A small team doesn't need them. Bigger orgs use them around release cuts.

When any of these starts hurting, read the relevant spec section and turn it on.
