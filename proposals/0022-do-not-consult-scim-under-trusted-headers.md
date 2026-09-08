# Proposal 0022: Do not consult SCIM under `trusted-headers`

- Issue: (to be filed)
- Status: Approved (2026-09-07). Verified after 9 adversarial review rounds (9 findings fixed); the open questions are resolved in favour of the staged positions.
- Date: 2026-09-07

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off. Every anchor is read against `fix/scim-not-consulted-under-trusted-headers` at `822e6a9`.

## Summary

**What changes.**

- The boot path builds the §6.3.1 SCIM group expander only under an identity provider whose group membership the registry resolves from a credential it verifies. Today it builds the expander whenever `PODIUM_SCIM_TOKENS` names at least one bearer token (`internal/serverboot/serverboot.go:985-995`), with no condition on the configured provider. §6.3.3 states that under `trusted-headers` "Groups come from `X-Podium-User-Groups` directly; SCIM and the `IdpGroupMapping` adapter are not consulted, because there is no token to read and the gateway is the source of truth", and the registry consults SCIM there anyway.
- One variable feeds both wiring sites, so one condition closes both. `resolveGroup` is declared at `internal/serverboot/serverboot.go:985`, handed to the composed catalog at `:994`, and handed to the §7.3.1 layer endpoint at `:1269`. Under the condition it stays nil for every provider outside the allowlist, and a nil resolver is the claim-only path both consumers already contract for (`pkg/layer/composer.go:43-49`, `:81`). `pkg/layer` is untouched.
- §6.3.1 gains a paragraph naming the providers its SCIM sentence applies under and binding the rule to every §4.6 evaluation the registry performs, including the §7.3.1 layer read. §6.3.1 introduces the directory with no provider qualification, and the carve-out lives two subsections away in §6.3.3.
- The registry logs at startup whether the pushed directory decides a layer read and under which identity provider. The line's text is fixed rather than left open, because the end-to-end arms assert both values it can report and S61 quotes it. `visibilityReason` (`pkg/registry/core/admin.go:82-95`) separates the directory-derived grant from the claim-derived one, so `GET /v1/admin/show-effective` names which arm admitted a caller. Neither signal exists today.
- `docs/deployment/gateway-delegated-identity.md:85` documents the defect as supported behavior and is replaced. `docs/reference/http-api.md:776` states the expansion with no provider qualification and gains one. `test/manual-validation.md` gains S61, and `CHANGELOG.md` records the withdrawn grant.

**Fixed decisions.**

- **The spec is correct and the code is the defect.** No spec text is amended to permit SCIM under `trusted-headers`. The staged spec paragraph states the scope of a rule §6.3.1 and §6.3.3 already fix between them, and it authorizes no behavior the specification does not already require.
- **The gate is one condition at the single construction site, and the evaluator is unchanged.** `layer.VisibleWith` takes the resolver as a parameter and contracts a nil resolver as the claim-only path, so passing nil is already a supported input on both consumers. The correction is to stop building the resolver rather than to teach the evaluator about identity providers, and `pkg/layer` holds no notion of an identity provider and gains none here.
- **The call at `internal/serverboot/serverboot.go:1269` is left as it stands.** It passes the same variable, which the condition leaves nil. A second provider check there would duplicate the gate and could drift from it.
- **The allowlist is `oidc-jwt` and `injected-session-token`, and its default arm denies.** Every other value, including `trusted-headers`, an unset provider, public mode, a free-form label the registry resolves to no provider, and any provider registered later through the §9.1 seam, resolves group membership from the caller's own identity. The expander can only grant, so a provider the boot path cannot classify gets the narrower evaluation.
- **The SCIM receiver keeps its mount under every provider.** `buildSCIMHandler` is keyed on `PODIUM_SCIM_TOKENS` alone (`internal/serverboot/serverboot.go:68-84`) and the receiver is mounted at `:1064-1066`. A `trusted-headers` deployment that pushes a directory for its own reasons keeps the endpoint, the persistence, and the CRUD surface. What it loses is the directory's effect on a `groups:` filter.
- **The detectability additions are proportionate to the defect.** A startup line reports the configuration and an existing admin diagnostic separates the arms. Neither adds a per-request record, a §8.1 field, a §6.10 code, or a matrix cell.
- Podium is pre-1.0. No flag, environment variable, or `registry.yaml` key restores the SCIM expansion under `trusted-headers`, and no dual code path is added.

**Watch out for.**

- **`injected-session-token` is staged as permitted and is not settled.** §6.3.1's opening sentence resolves group membership registry-side through SCIM as a general rule and names `groups` as the claim the adapter reads under this provider, and §6.3.2 states nothing about groups. The cell is unspecified at the provider level, the staged position preserves shipped behavior, and OQ-1 puts the decision to the reviewer.
- **Minimality leaves a structural cost.** Nothing in `pkg/layer`, `pkg/registry/core`, or `pkg/registry/server` prevents a later caller from building its own resolver and passing it in. `WithGroupResolver` stays available on the registry (`pkg/registry/core/core.go:338`) and on the layer endpoint (`pkg/registry/server/layers.go:218`) with no provider argument, so a third wiring site added in a future change reintroduces the defect and no compile error reports it. The guards are the predicate's doc comment, which names the rule, and the end-to-end test, which fails on the observable outcome whatever the wiring site.
- **The two consumers reach the evaluator by different routes and both must be read.** The composed catalog reaches it through `visibleManifests`; the layer read reaches it through `readableBy`, whose admin arm returns the tenant's whole list before the evaluator runs (`pkg/registry/server/layers.go:256-258`). A test that observes the withdrawal through `GET /v1/layers` as a tenant admin observes nothing.
- **No shipped test combines `trusted-headers` with SCIM, so the suite passes with and without the fix.** `gwTrustedHeadersServer` sets no SCIM token (`test/e2e/auth_gateway_test.go:49-83`), and `test/e2e/auth_scim_visibility_test.go` runs under `injected-session-token` (`:59`). A green suite is evidence for neither direction, which is what makes the new end-to-end arms the whole verification.
- **The positive arm has a shipped pin that must stay green.** `TestAuthSCIMVisibility_MembershipDrivesVisibility` and `TestAuthSCIMVisibility_UserDeletionRevokesVisibility` (`test/e2e/auth_scim_visibility_test.go:131`, `:207`) assert that a verified `injected-session-token` caller whose token carries no group claim sees a groups-restricted layer through SCIM, on the layer read and on the data plane. Dropping `injected-session-token` from the allowlist fails both, which is the mechanical check on the second entry.
- **The `trusted-headers` verifier already carries a comment claiming the behavior it does not have.** `internal/serverboot/identity_verify.go:283-284` and `pkg/identity/trusted_headers.go:54-55` both state that SCIM and the `IdpGroupMapping` adapter are not consulted. The adapter half is true, because neither site calls `groups.Map`. The SCIM half is false, because the resolver is wired downstream of the verifier and read by every evaluator call. A reader auditing the verifier alone finds a correct-looking comment above incorrect behavior.
- **CODE-3's signature change breaks a shipped test at compile time.** `visibilityReason` is package-private and is called by `pkg/registry/core/coverage_gaps_test.go:247` in its three-argument form. Landing CODE-3 without the matching test edit stops the whole `pkg/registry/core` test package from building, so S3 carries both and is marked indivisible.
- **The anonymous arms of the layer read return no layers at all.** `readableBy` answers the empty list for an unauthenticated caller before the evaluator runs (`pkg/registry/server/layers.go:259-261`), so a public layer is absent there as well. An anonymous expectation belongs on the data plane, and TEST-3 is written that way.
- **Parts of `test/e2e` skip silently on macOS**, so a local pass is not evidence. Check the output for `SKIP` before treating the new arms as run.
- **The wiring line is not unit-reachable.** It sits inside the boot function, so the default `go test` profile scores it as uncovered even when an end-to-end test drives it. Measure it with `GOCOVERDIR` per `.claude/rules/test-coverage.md`.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §6.3.1 gains the paragraph naming the providers under which the SCIM expansion applies and binding the rule to every §4.6 evaluation, including the §7.3.1 layer read. Committed alone and verified before any code.
      Levels: —. Depends on: —
- [ ] **S2 · code** — CODE-1, CODE-2, TEST-1. The `scimResolvesGroups` predicate, the gated construction site, the amended comments on the SCIM block, the startup line, and the predicate's unit table. **Indivisible**: the predicate has no other caller, and the table and the predicate falsify each other.
      Levels: unit. Depends on: S1
- [ ] **S3 · code** — CODE-3, TEST-2. `visibilityReason` separates the directory-derived grant, and the shipped table in `pkg/registry/core/coverage_gaps_test.go` takes the new parameter and gains the directory case. **Indivisible**: the signature change breaks the shipped call at `pkg/registry/core/coverage_gaps_test.go:247`, so the package does not build until both land.
      Levels: unit. Depends on: S2
- [ ] **S4 · test** — TEST-3, TEST-4. The end-to-end arms through the built binary: a `trusted-headers` registry with SCIM mounted refusing the directory-derived grant on both consumers, the negative control on the header-derived grant, the `oidc-jwt` arm confirming the expansion survives where §6.3.3 requires it, and the boot-log assertions that pin CODE-2's line to its withheld value on the first registry and its permitted value on the second.
      Levels: e2e. Depends on: S2
- [ ] **S5 · docs** — DOC-1 through DOC-4. `docs/deployment/gateway-delegated-identity.md`, `docs/reference/http-api.md`, `test/manual-validation.md` with its Scenario index row, and `CHANGELOG.md`.
      Levels: —. Depends on: S3, S4

**Ordering constraints.** S1 precedes the code, per `.claude/rules/spec-driven-development.md`, because CODE-1's doc comment cites the paragraph it lands. S3 depends on S2 rather than running beside it: TEST-2 drives `visibilityReason` and `ShowEffective` directly and needs nothing from S2 to compile, and the diagnostic's `trusted-headers` outcome, the unexpanded view that matches the live evaluator, holds only once CODE-1 leaves the resolver nil under that provider. Landing S3 first would ship a diagnostic that names an arm the fix has not yet withdrawn. S4 depends on S2 alone, because its assertions are HTTP statuses, a layer list, and the boot line CODE-2 adds in S2, rather than the diagnostic reason S3 adds. S4 follows S2 rather than preceding it, because the `trusted-headers` arm fails against the shipped boot path and would land a red test. S5 follows the code and the tests so the pages describe what the tested build does. Neither `docs/deployment/gateway-delegated-identity.md` nor `docs/reference/http-api.md` appears in `tools/doccov/manifest.yaml`, and the staged edits add no runnable fenced block to either, so no `doccov-check` obligation is created.

## Current state and the gap

### The expander is built on the SCIM token alone

`internal/serverboot/serverboot.go:981-995` builds the handler from `PODIUM_SCIM_TOKENS` and then builds the expander:

```go
	scimHandler := buildSCIMHandler(scimStore)
	// The expander is held here so the §7.3.1 layer read filters against the
	// same IdP-pushed membership the composed catalog does. A nil value is the
	// JWT-only path both consumers already contract for.
	var resolveGroup layer.GroupResolver
	if scimHandler != nil {
		resolveGroup = func(g string) []string {
			members, err := scimStore.MembersOf(context.Background(), g)
			if err != nil {
				return nil
			}
			return members
		}
		registry = registry.WithGroupResolver(resolveGroup)
	}
```

The only condition is `scimHandler != nil`, which `buildSCIMHandler` decides on `PODIUM_SCIM_TOKENS` alone (`:68-84`). The configured identity provider is in scope at that line: `cfg` is bound at `:762` and `cfg.identityProvider` is read from `:558` onward, including by the startup guards at `:1142` and `:1146`. It is not consulted here. The comment above the declaration names the intended condition ("the JWT-only path") and the code keys on a provisioning setting instead.

The same variable reaches the layer endpoint at `:1269`, in the builder chain that also installs the identity resolver and the admin callback.

### The expander can only grant

`layer.VisibleWith` evaluates the §4.6 union (`pkg/layer/composer.go:64-99`). Its `groups:` arm is:

```go
	for _, g := range v.Groups {
		for _, ug := range id.Groups {
			if g == ug {
				return true
			}
		}
		if resolveGroup != nil {
			for _, member := range resolveGroup(g) {
				if member == id.Sub || member == id.Email {
					return true
				}
			}
		}
	}
```

The caller's own groups are tried first, and the resolver runs when they do not match. Every path through the resolver returns `true` or falls through, so the expander adds visibility and removes none. `Memory.MembersOf` returns each member's `userName` (`pkg/scim/scim.go:295-311`), compared against `id.Sub` and `id.Email`.

Under `trusted-headers` the caller's groups are exactly what the gateway put in `X-Podium-User-Groups` (`pkg/identity/trusted_headers.go:71-75`, wired at `internal/serverboot/identity_verify.go:285-303`), so the gateway's decision to withhold a group is reversed whenever the SCIM directory lists that caller's subject or email as a member of it. That is a fail-open on a §4.6 decision, and `.claude/rules/code-best-practices.md` requires a visibility filter that cannot evaluate to deny rather than admit.

### Exploitability

The broadened access needs one deployment condition beyond the defect: `PODIUM_SCIM_TOKENS` set on a registry running `PODIUM_IDENTITY_PROVIDER=trusted-headers`. Neither setting constrains the other at startup or later. The startup guards branch on the provider at `internal/serverboot/serverboot.go:1146`, `:1169`, and `:1211`, and none of them reads the SCIM setting.

An operator reaches the configuration by following the shipped documentation. `docs/deployment/gateway-delegated-identity.md:85` describes the expansion under `trusted-headers` as behavior and offers unsetting `PODIUM_SCIM_TOKENS` as the way to keep the gateway authoritative, so a page written against the code rather than the spec has turned the defect into documented behavior. An operator also reaches it by migration: a deployment that ran `oidc-jwt` with SCIM provisioning and later moved behind a gateway keeps `PODIUM_SCIM_TOKENS` and the pushed directory, because the IdP's provisioning application is still writing to it.

Reachability is the ordinary request path. Every caller the gateway admits reaches the broadened view on a plain read, with no special route, header, or privilege involved.

### The grant it confers

Under `trusted-headers` the registry performs no verification of the header contents (§6.3.3). The values compared against SCIM membership, `id.Sub` and `id.Email`, are the `X-Podium-User-Sub` and `X-Podium-User-Email` headers the gateway sets, trimmed (`pkg/identity/trusted_headers.go:61-75`). These properties follow.

**The gateway is authoritative over identifiers it may not constrain.** A gateway that authenticates a caller and then sets `X-Podium-User-Email` from a user-editable profile attribute, or from an unnormalized directory field, hands the registry a value the caller influences. The registry compares it to a SCIM `userName` for equality after trimming, with no normalization, no case folding, and no domain check. Under the specified behavior that value reaches the `users:` arm, which the operator declares layer by layer. Under the defect it also reaches every `groups:` filter in the tenant through whatever the IdP has pushed, so the set of values that produce a grant is no longer the set the operator wrote into layer configs.

**The SCIM directory is not the set of registry callers.** An IdP provisions SCIM from a directory group, and that group holds every member of the team, whether or not they use the registry and whether or not the gateway would admit them. Under `oidc-jwt` that costs nothing, because a member reaches the registry only by presenting a token the registry verifies and whose subject it reads. Under `trusted-headers` the same directory becomes a list of identifier values that grant, held by a system the registry does not authenticate against, and matched against a header.

**The two namespaces are compared as one.** A single SCIM `userName` is tested against both `id.Sub` and `id.Email`. A directory that provisions `userName` as an email address and a gateway that sets `X-Podium-User-Sub` to an email address produce a surface on which a subject value matches an email entry. §6.3.3 requires the subject claim to identify one principal for the life of the deployment, and the registry has no equivalent guarantee over an email address, which is reassignable at most directories.

### Blast radius

§4.6 governs the composed view, so a layer that becomes visible contributes its artifacts on every read surface. The resolver reaches each of them through one field per consumer, and both fields are set from the single `resolveGroup` variable:

| Consumer | Wiring site | Reads that ride it |
|:--|:--|:--|
| Composed catalog | `internal/serverboot/serverboot.go:994`, into `core.Registry.resolveGroup` (`pkg/registry/core/core.go:73`) | `search_artifacts` and `load_artifact` (`pkg/registry/core/core.go:2006`, with the audit composition at `:365`), `search_domains` and `load_domain` (`pkg/registry/core/domain_load.go:161`), the §3.5 scope preview and reverse-dependency read (`pkg/registry/core/dependents.go:142`), and the §4.7.2 `show-effective` diagnostic (`pkg/registry/core/admin.go:69`) |
| §7.3.1 layer read | `internal/serverboot/serverboot.go:1269`, into `LayerEndpoint.resolveGroup` (`pkg/registry/server/layers.go:65`) | `readableBy` (`pkg/registry/server/layers.go:266`), for every caller the admin callback refuses, which also decides the `reorder` response |

What the caller then reads is the layer's whole contribution: the artifact bodies, the `DOMAIN.md` files, the manifests, and the search index entries. §4.6 states that read-side enforcement happens at the registry on every call and that Git provider permissions are not consulted, so there is no second gate behind visibility. The bound on the radius is the set of layers whose `groups:` filter names a group the pushed directory holds; a layer declaring only `users:`, only `organization: true`, or only `public: true` is unaffected, because the resolver is reached from the `groups:` loop alone.

Ingest and every layer write are unaffected, because neither reads visibility. The §4.7.2 admin arm of the layer read is unaffected, because it answers the whole tenant list before the evaluator runs (`pkg/registry/server/layers.go:256-258`).

### Detectability

Nothing distinguishes a grant made through the SCIM arm from one made on the caller's asserted groups.

- The §8.1 audit event carries the caller's identity, email, and groups (`pkg/audit/audit.go:131-136`) and the ordered layer composition (`:142-145`). It carries no field naming why a layer entered the composition.
- The server log records `SCIM directory persisted at <path>` when `PODIUM_SCIM_STORE_PATH` is set (`internal/serverboot/serverboot.go:978`) and records nothing when the resolver is wired. A registry running the exposed configuration logs the same lines as one that is not.
- The admin diagnostic returns one reason string for both group arms: `visibilityReason` answers `"user matches layer.users or layer.groups"` for every non-public, non-organization grant (`pkg/registry/core/admin.go:90-91`).
- §8.1's `visibility.denied` records a refusal. A grant records no event of its own.

An operator can reconstruct exposure after the fact from the fields the audit event does carry: an event whose resolved layer list names a layer whose `groups:` filter intersects none of the recorded caller groups, and whose `users:` list names neither the caller's subject nor email, was admitted through the SCIM arm. The reconstruction needs the layer configs as they stood at the time and holds only while the events are retained (§8.4 sets one year for event metadata). It is a forensic exercise rather than a query, which is why CODE-2 and CODE-3 add the two distinguishing signals.

### What each provider requires

| `PODIUM_IDENTITY_PROVIDER` | SCIM resolves `groups:` | Basis, and whether the code agrees |
|:--|:--|:--|
| `oidc-jwt` | Yes | §6.3.3: on success the registry "resolves groups through SCIM or the `IdpGroupMapping` adapter (§6.3.1) applied to the token's group claim". Code agrees |
| `injected-session-token` | Staged as yes | §6.3.1's opening sentence resolves group membership registry-side through SCIM as the general rule and names `groups` as the claim the adapter reads under this provider. §6.3.2 is silent at the provider level, so the cell is unspecified. Code consults it. SPEC-1 states the rule and OQ-1 records the alternative |
| `trusted-headers` | No | §6.3.3. **Code diverges.** The expander is built on `scimHandler != nil` alone and reaches the evaluator's group arm |
| Unset, and public mode | Not consulted | The registry resolves every caller as anonymous-public and `VisibleWith` short-circuits on `id.IsPublic` before any field is read (`pkg/layer/composer.go:65-66`), which §4.6's public-mode and no-identity bypasses state. Public mode is already mutually exclusive with an identity provider at startup (`config.public_mode_with_idp`, §6.3.3) |
| A label the registry resolves to no provider | Not consulted | The same bypass. `selectIdentityProvider` returns nil for an unregistered label (`internal/serverboot/identity_verify.go:185-188`) and `identityVisibilityGuard` exempts it from the startup refusal (`:117-122`), so the deployment runs with every caller anonymous-public |
| `oauth-device-code` on a registry | Unreachable | It is a client-side acquisition provider with no request-time verifier in the registry, so a registry naming it fails startup with `config.identity_provider_unverified` (`internal/serverboot/identity_verify.go:117-122`) |

The last three rows are cases where the expander's presence has no observable effect today, because the evaluator never reaches the `groups:` arm for an anonymous-public caller. The allowlist's default arm settles them, so the outcome does not depend on that short-circuit staying where it is.

## Spec amendment: where the SCIM expansion applies

**SPEC-1.** Anchor: `spec/06-mcp-server.md`, §6.3.1. One paragraph is appended at the end of the section, after the paragraph beginning "**Per-request tenant selection.**" and before the `### 6.3.2` heading. It is appended at the end of its level and inserted between no existing siblings.

> **Where the directory expansion applies.** The registry expands a layer's `groups:` filter against the SCIM directory under the identity providers whose caller identity comes from a credential the registry verifies, which are `oidc-jwt` (§6.3.3) and `injected-session-token` (§6.3.2). It does not expand it under `trusted-headers`, where the gateway is the source of group membership and §6.3.3 states that SCIM and the `IdpGroupMapping` adapter are not consulted. The rule binds every §4.6 visibility evaluation the registry performs for a caller, which is the composed view the meta-tools serve and the §7.3.1 layer read, so under `trusted-headers` a layer's `groups:` filter is satisfied by a group `X-Podium-User-Groups` carries and by no other membership record. A registry that mounts the SCIM receiver under `trusted-headers` still accepts the identity provider's pushes and still serves the directory, and no read of a layer's visibility consults it. A deployment whose provider this specification does not name expands no `groups:` filter against the directory until that provider's own rule states one, because a directory that can only widen a caller's view is a grant the operator did not make.

§6.3.1 is the section that owns group resolution. Its opening sentence states that "Group membership is resolved registry-side via SCIM 2.0 push from the IdP" with no provider qualification, and its `IdpGroupMapping` paragraph already names the claim each provider reads, so the provider-by-source rule belongs beside them. §6.3.3 is not amended: its `trusted-headers` sentence is correct as written and forbids the consultation already, and §6.3.3 describes one provider at a time and has no place to state the positive and the unnamed-provider halves. §4.6 is not amended either: its visibility table fixes what a `groups:` filter means and its bypass paragraphs name the conditions under which the evaluator is short-circuited, and neither decides which membership record expands a `groups:` entry. §7.3.1's layer read visibility rule needs no edit of its own, because it already returns "the layers that caller can see under §4.6" and delegates the evaluation; naming it in §6.3.1 is what closes the reading under which the rule governed identity resolution and left the evaluator's group expander unaddressed.

This adds no §6.10 error code and no matrix cell. §6.3.1 keeps its existing test citations, so `speccov-drift` gains no unsatisfied obligation, and TEST-1, TEST-3, and TEST-4 cite the section as well.

## Proposed solution

### CODE-1: the provider condition at the point the expander is built

A predicate lands beside `buildSCIMHandler` (after `internal/serverboot/serverboot.go:84`):

```go
// scimResolvesGroups reports whether a layer's §4.6 `groups:` filter expands
// against the §6.3.1 SCIM directory under the configured identity provider.
//
// The list is an allowlist over the providers whose caller identity comes from
// a credential the registry verifies: oidc-jwt resolves groups through SCIM or
// the IdpGroupMapping adapter applied to the token's group claim, and the
// §6.3.2 injected-session-token verifier reads the token's own subject and
// groups. It is not an allowlist over deployments that mount the receiver:
// trusted-headers mounts it and does not consult it, because there is no token
// to read and the gateway is the source of group membership (§6.3.3).
//
// The expander can only grant. layer.VisibleWith consults it exactly when the
// caller's own groups do not match, so consulting SCIM under trusted-headers
// would reverse a decision the gateway made and hand a caller a layer the
// gateway kept from it.
//
// The default arm denies, so an unset provider, a free-form label the registry
// resolves to no provider, and any provider added later resolve group
// membership from the caller's own identity until they are named here. A
// provider that gains directory-resolved group membership is added in the same
// change that gives it one.
//
// Spec: §6.3.1, §6.3.2, §6.3.3
func scimResolvesGroups(identityProvider string) bool {
	switch identityProvider {
	case "oidc-jwt", "injected-session-token":
		return true
	default:
		return false
	}
}
```

The block at `internal/serverboot/serverboot.go:964-995` takes the condition, and both comments take the scope:

```go
	// §6.3.1 SCIM 2.0: when at least one bearer token is configured,
	// the SCIM IdP receiver is mounted at /scim/v2/. When
	// PODIUM_SCIM_STORE_PATH is set, IdP-pushed users + groups
	// persist as a JSON file at that path so they survive server
	// restarts. Under an identity provider that resolves the caller
	// from a verified credential, the same store feeds the §4.6
	// visibility evaluator's `groups:` expander so layer filters
	// resolve against IdP-pushed group membership.
```

```go
	scimHandler := buildSCIMHandler(scimStore)
	// The expander is held here so the §7.3.1 layer read filters against the
	// same IdP-pushed membership the composed catalog does. A nil value is the
	// claim-only path both consumers already contract for, and it is what every
	// provider outside scimResolvesGroups gets: one condition here settles both
	// consumers, because both read this variable. Under trusted-headers the
	// gateway's `X-Podium-User-Groups` is the whole of the caller's group
	// membership (§6.3.3), so a directory entry naming the caller's unverified
	// sub or email header value grants nothing.
	var resolveGroup layer.GroupResolver
	if scimHandler != nil && scimResolvesGroups(cfg.identityProvider) {
		resolveGroup = func(g string) []string {
			members, err := scimStore.MembersOf(context.Background(), g)
			if err != nil {
				return nil
			}
			return members
		}
		registry = registry.WithGroupResolver(resolveGroup)
	}
```

`internal/serverboot/serverboot.go:1269` is unchanged. `WithGroupResolver(nil)` leaves the endpoint's `resolveGroup` field nil, which `readableBy` passes to `layer.VisibleWith`, which is the claim-only path the type's doc comment contracts. The SCIM mount at `:1064-1066` is unchanged.

`pkg/layer`, `pkg/registry/core`, and `pkg/registry/server` keep `layer.GroupResolver`, `Registry.WithGroupResolver`, and `LayerEndpoint.WithGroupResolver` with their signatures and their doc comments, each of which describes a resolver the caller supplies and none of which claims the resolver is installed unconditionally.

Public mode needs no condition of its own. §6.3.3 makes it mutually exclusive with the registry-process providers at startup, a public-mode registry configures no provider, so `cfg.identityProvider` is empty and the predicate returns false. §4.6's public-mode bypass short-circuits the evaluator ahead of the `groups:` loop in any case.

### CODE-2: the startup line

Add after the block above:

```go
	if scimHandler != nil {
		// An operator reading the log can tell whether the pushed directory
		// decides a layer read on this registry. Nothing else in the log or in
		// the §8.1 audit stream separates a directory-derived grant from one
		// made on the caller's asserted groups.
		log.Printf("SCIM group expansion in layer visibility: %t (identity provider %q)",
			resolveGroup != nil, cfg.identityProvider)
	}
```

The line names a configuration outcome and an identifier and carries no directory contents, which is what `.claude/rules/code-best-practices.md` requires of a log line. It is one line at startup on a registry that already mounts the receiver.

The line's text is fixed as staged rather than left to the implementor, because three places read it: TEST-3 asserts the withheld form, TEST-4 asserts the permitted form, and S61 step 3 quotes the whole line in its Expect block. The assertions match on the literal prefix `SCIM group expansion in layer visibility: ` followed by the boolean, so an implementor may reword the parenthetical that names the provider only by amending TEST-3, TEST-4, and S61 in the same change.

### CODE-3: the diagnostic separates the directory-derived grant

`ShowEffective` is the surface §4.7.2 gives an operator for the question "why does this caller see this layer", and §12's mitigation row states that it surfaces the effective view for any identity. Its reason string collapses both group arms and the `users:` arm into one answer, so the operator cannot tell which membership record admitted the caller. Separating the directory arm is what makes the diagnostic answer the question this fix makes worth asking.

The call at `pkg/registry/core/admin.go:69-74` computes the discriminator and passes it:

```go
		visible := layer.VisibleWith(l, target, r.resolveGroup)
		// A caller the expander admits and the claim-only evaluation refuses was
		// admitted through the §6.3.1 directory rather than on the groups the
		// caller's credential carries. The re-evaluation runs on an admin
		// diagnostic over the tenant's layer list and is not on a read path.
		viaDirectory := visible && r.resolveGroup != nil && !layer.VisibleWith(l, target, nil)
		out = append(out, EffectiveLayer{
			LayerID: l.ID,
			Visible: visible,
			Reason:  visibilityReason(l, target, visible, viaDirectory),
		})
```

`visibilityReason` (`:79-95`) gains the parameter and one arm, and stays a pure function so its table can be driven without a registry:

```go
// visibilityReason returns a stable one-liner explaining why l is
// visible to id (or not). Operators grep for these in support
// conversations.
//
// The group arms are reported separately: a caller admitted on membership the
// registry resolved from the §6.3.1 directory was admitted on a different
// record from one admitted on the groups the caller's credential carries, and
// the two read as one answer to an operator who cannot tell them apart.
//
// Spec: §4.6, §4.7.2, §6.3.1
func visibilityReason(l layer.Layer, id layer.Identity, visible, viaDirectory bool) string {
	switch {
	case l.Visibility.Public:
		return "layer.public=true"
	case id.IsPublic && !visible:
		return "caller is anonymous; layer requires authentication"
	case l.Visibility.Organization && id.IsAuthenticated && visible:
		return "layer.organization=true and identity is authenticated"
	case viaDirectory:
		return "user matches layer.groups through the SCIM directory"
	case visible:
		return "user matches layer.users or layer.groups"
	default:
		return "user is not in layer.users or layer.groups"
	}
}
```

The existing strings are unchanged and the new arm is an addition, so an operator matching on a shipped string keeps matching. A public layer never reaches the new arm, because the claim-only evaluation admits it too and `viaDirectory` is false there.

`visibilityReason` is package-private and has a second, shipped caller: the table test at `pkg/registry/core/coverage_gaps_test.go:247` calls the three-argument form. The parameter is added there in the same change, or the `pkg/registry/core` test package stops compiling and every test in it goes red rather than one assertion failing. TEST-2 carries that edit.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| `trusted-headers`, SCIM mounted, the directory lists the caller in `engineering`, the gateway sends no `X-Podium-User-Groups` | The caller does not reach the `groups: [engineering]` layer: `load_artifact` reports `404` and `GET /v1/layers` omits the layer. Admitted today | SPEC-1; CODE-1; TEST-3 |
| `trusted-headers`, SCIM mounted, the gateway sends `X-Podium-User-Groups: engineering` | The caller reaches the layer, whatever the directory records | SPEC-1; TEST-3 |
| `trusted-headers`, SCIM mounted, the caller's subject is absent from the directory and the email matches an entry | Not visible. Both routes to the grant are withdrawn, because the expander matched on `Email` as well as `Sub` | CODE-1; TEST-3 |
| `trusted-headers`, SCIM mounted, the layer declares `users: [<the caller's email>]` | Visible. The `users:` arm reads §4.6's table and consults no directory | Non-goals |
| `trusted-headers`, SCIM mounted, a tenant admin reads `GET /v1/layers` | The whole tenant list, unchanged, because `readableBy`'s admin arm answers before the evaluator | TEST-3 |
| `trusted-headers`, SCIM mounted, any SCIM CRUD request | Unchanged. The receiver stays mounted and the directory stays writable, readable, and persisted | CODE-1 |
| `trusted-headers`, SCIM mounted, at startup | One line reporting the expansion withheld and naming the configured provider | CODE-2; TEST-3 |
| `trusted-headers`, SCIM mounted, `GET /v1/admin/show-effective` | The unexpanded view, matching the live evaluator. Reports the expanded one today | CODE-3 |
| `oidc-jwt` or `injected-session-token` with SCIM mounted | Unchanged: directory membership grants `groups:` visibility on both consumers, and the diagnostic names the arm | SPEC-1; the shipped `test/e2e/auth_scim_visibility_test.go`; TEST-4 |
| SCIM not mounted, any provider | Unchanged. The resolver was already nil | CODE-1 |
| A registry with no identity provider, or one in public mode | Unchanged. The expander is not built, and the evaluator short-circuits on `id.IsPublic` before the `groups:` arm, so no caller's outcome moves | §4.6's public-mode and no-identity bypasses |
| A registry naming a label it resolves to no provider | Unchanged, on the same terms. The deployment fronts the registry with external authentication and the evaluator is bypassed for every caller | §4.6's no-identity bypass |
| A registry naming `oauth-device-code` | Unreachable: startup fails with `config.identity_provider_unverified` before any expander is built | `internal/serverboot/identity_verify.go:117-122` |
| A provider registered later through the §9.1 seam, with no allowlist entry | Group membership comes from the caller's own identity, so the deployment withholds a grant until the entry and its spec sentence land. Accepted: the failure is a denial | CODE-1 |
| `MembersOf` fails at request time under a permitted provider | Denies, unchanged. The closure swallows the error and returns no members | CODE-1 |
| A third wiring site added later that builds its own resolver | The defect returns, and nothing in the type system reports it. Accepted cost of the minimal fix; TEST-3 is the check | Watch out for; Non-goals |
| A `trusted-headers` deployment relying on the directory for group visibility today | Loses it. Callers lose visibility they currently have, and the operator provisions the membership at the gateway | Operator impact; DOC-1, DOC-4 |

**Accepted.** The startup line reports the configuration rather than each grant. An operator learns whether the directory can decide a read on this registry and does not learn from the log which reads it decided. Per-request attribution would be a new §8.1 field on the read events, which is a spec change and a wider surface than this defect warrants.

**Accepted.** The expansion is resolved at startup, so changing `PODIUM_IDENTITY_PROVIDER` takes effect at the next start. Every other identity setting behaves the same way.

**Accepted.** The resolver closure keeps its own `context.Background()`, which the context rule discourages. Threading the request context requires changing `layer.GroupResolver`'s signature and every call site, and it is unrelated to this defect.

### Operator impact

A deployment running `trusted-headers` with `PODIUM_SCIM_TOKENS` set today grants `groups:` visibility from the `X-Podium-User-Groups` header and from the pushed directory. After this change the header is the only source. A caller whose access rests on a directory membership the gateway does not also send loses it at the registry's next start, on the data plane and on the layer read together. Every other grant is unchanged, including the `users:` arm, the `organization:` arm, and any group the gateway asserts, and no other identity provider is affected.

The loss is silent from the caller's side, because §4.6 withholds rather than refuses: a `load_artifact` reports `404` and the layer is absent from `GET /v1/layers` rather than reported and denied. The §8.1 `visibility.denied` entry is the request-time record.

The operator restores the access by provisioning the membership at the gateway, so the caller's request carries the group in `X-Podium-User-Groups`. That is the source of truth this provider is defined around, and every gateway that can inject `X-Podium-User-Sub` can inject `X-Podium-User-Groups`. A small, stable set of affected callers can instead be named on the layer's `users:` list, which is evaluated against the subject and email headers and consults no directory. An operator who wants the registry to resolve membership from the directory runs `oidc-jwt`, where the registry verifies the token itself and §6.3.1 and §6.3.3 authorize the directory path. Both providers apply on a standalone and on a standard backend (§6.3.3), so the switch is a configuration change.

Before upgrading, an operator enumerates what will change from state the registry still holds: list the layers carrying a `groups:` filter from `registry.yaml` and from `GET /v1/layers` read as an admin, read `GET /scim/v2/Groups` for each named group, which lists its members as opaque user ids and carries no `userName` (`pkg/scim/handler.go:230-247`, `:284-299`), resolve each member id through `GET /scim/v2/Users/{id}` to obtain the `userName` (`pkg/scim/handler.go:169-176`, `:125-137`), which is the value the evaluator matches (`pkg/scim/scim.go:295-311`), and compare those `userName` values against the subject and email values the gateway sets for callers it does not also place in that group. Every match in that comparison is a caller who reached the layer through the withdrawn arm. After upgrading, the startup line reports the state on every start, so a registry that still mounts the receiver under this provider says so.

`PODIUM_SCIM_TOKENS` can stay set where the receiver serves another purpose, such as maintaining the directory across a planned move to `oidc-jwt`. Unset it where it was mounted only to feed visibility.

## Testing

**TEST-1 · unit, `internal/serverboot`.** A new `scim_group_gate_test.go` tables every value the boot path can hold, carrying `// Spec: §6.3.1, §6.3.2, §6.3.3`:

```go
// Spec: §6.3.1, §6.3.2, §6.3.3 — the SCIM `groups:` expansion applies under
// the providers that read a credential the registry verifies, and never under
// trusted-headers, where the gateway is the source of group membership.
func TestSCIMResolvesGroups_ProviderAllowlist(t *testing.T) {
	cases := map[string]bool{
		"oidc-jwt":               true,
		"injected-session-token": true,
		"trusted-headers":        false,
		"":                       false,
		"oidc":                   false,
		"oauth-device-code":      false,
		"not-a-provider":         false,
	}
	for provider, want := range cases {
		if got := scimResolvesGroups(provider); got != want {
			t.Errorf("scimResolvesGroups(%q) = %v, want %v", provider, got, want)
		}
	}
}
```

The `oidc` case is the free-form label `selectIdentityProvider` resolves to nothing. The table is the only falsifiable statement of the unset and free-form-label arms, because those deployments resolve every caller as anonymous-public and the evaluator short-circuits before the `groups:` arm, so they have no observable end-to-end difference.

**TEST-2 · unit, `pkg/registry/core/coverage_gaps_test.go`.** `TestVisibilityReason_Arms` (`:196-251`) is the shipped table over the reason arms, and it is amended rather than duplicated in a new file. Its case struct gains a `viaDirectory` field, its call at `:247` becomes `visibilityReason(c.l, c.id, c.visible, c.viaDirectory)`, and its shipped cases pass `false` and keep their current `want` strings, which is what makes the additive claim above falsifiable. The table gains the directory case: the same layer and the same target identity as the "visible via users or groups" case, differing only in `viaDirectory`, expecting `"user matches layer.groups through the SCIM directory"`. A second test drives `ShowEffective` against a registry carrying an expander that names the target and a layer declaring `groups:`, and asserts the directory reason; the same registry with no expander reports the layer invisible with the refusal reason. `TestShowEffective_ReasonsPerLayer` (`:254-278`) is unchanged: its registry carries no expander, so `viaDirectory` is false for every layer, and its `team` layer is admitted on `users:` in any case. `// Spec: §4.6, §4.7.2, §6.3.1`.

**TEST-3 · end-to-end, `test/e2e/auth_gateway_test.go`. This is the test that fails against the pre-fix code.** `gwTrustedHeadersServer` (`test/e2e/auth_gateway_test.go:49`) already declares a public layer and an `eng-layer` with `visibility: { groups: [engineering] }` and takes a proxy secret; it gains an optional SCIM token so the same fixture can mount the receiver. The test pushes a SCIM user whose `userName` is `alice@acme.com` and a SCIM group `engineering` holding her, reusing `oidcSCIMDo`, `oidcSCIMUserBody`, and `oidcSCIMGroupBody` (`test/e2e/auth_oidc_test.go:213`, `:239`, `:249`). Every request carries the matching `X-Podium-Proxy-Secret`. The arms:

- `X-Podium-User-Sub: alice@acme.com` and no groups header. `load_artifact` on the engineering layer answers `404` and `GET /v1/layers` lists the public layer alone. Pre-fix both admit her, because the directory arm matches her subject against the pushed `userName`. This arm is the regression test.
- `X-Podium-User-Sub: opaque-123` with `X-Podium-User-Email: alice@acme.com` and no groups header. The same two outcomes, which pins the email half of the comparison.
- **The negative control.** The same caller with `X-Podium-User-Groups: engineering` answers `200` on the load and lists the engineering layer, before and after the fix. This proves the correction withdrew the directory arm and left the header arm intact.
- An anonymous request reaches the public layer and not the engineering layer on the data plane: `load_artifact` answers `200` on the public artifact and `404` on the engineering one.
- A request carrying the identity headers and no proxy secret is anonymous on the same terms, so the correction did not shift the secret gate.
- **The startup line.** The test reads the registry's boot log through `srv.log()` (`test/e2e/helpers_test.go:282`) and asserts it contains `SCIM group expansion in layer visibility: false`, which is CODE-2's withheld form on a registry that mounts the receiver under `trusted-headers`. This is the automated assertion on the line's withheld value, and TEST-4 pins the permitted one. The level matches the behavior: the line is written by the spawned binary at boot, and `test/e2e/auth_oidc_jwt_test.go:222-223` pins the accepted-issuer line the same way.

The two anonymous arms are asserted on the data plane alone. On the §7.3.1 layer read an unauthenticated caller receives no layers at all, including the public one, because `readableBy` returns the empty list before the evaluator runs for any caller that is not authenticated (`pkg/registry/server/layers.go:259-261`), which is what §7.3.1 states when it says such a caller "resolves no verified subject and the read returns it no layers". Where those arms read `GET /v1/layers`, they assert an empty list.

The test carries `// Spec: §4.6, §6.3.1, §6.3.3, §7.3.1` and reads both consumers on the two authenticated arms and on the negative control, because `readableBy` and `visibleManifests` reach the evaluator by different routes and the layer read has an admin arm that answers ahead of it. The caller holds no §4.7.2 admin grant, which the fixture asserts by reading the list as that caller and finding the public layer alone.

**TEST-4 · end-to-end.** An `oidc-jwt` arm asserting the expansion survives where §6.3.3 requires it: a verified caller whose token carries no matching group claim, present in the SCIM group, reads the layer on the data plane and on the layer read. The same arm asserts CODE-2's permitted form, reading `srv.log()` and requiring it to contain `SCIM group expansion in layer visibility: true`, so the two tests together pin both values the boolean can take and a line that reports one value under every provider fails one of them. `test/e2e/auth_scim_visibility_test.go` pins the `injected-session-token` half and is not modified; TEST-4 pins the other allowlisted provider, which nothing covers end to end on both consumers today.

**Non-regression.** `TestAuthSCIMVisibility_MembershipDrivesVisibility` and `TestAuthSCIMVisibility_UserDeletionRevokesVisibility` (`test/e2e/auth_scim_visibility_test.go:131`, `:207`) run unchanged and must stay green: they are the positive pin for `injected-session-token`, on the layer read and on the data plane, including the revocation direction. `test/e2e/auth_group_search_filter_test.go:38` drives a SCIM-resolved member and a claim-carrying member through one search on the same harness and must stay green. `test/e2e/auth_gateway_test.go`'s existing `trusted-headers` arms and `internal/serverboot/identity_gateway_integration_test.go` set no SCIM token, so they are unaffected. In `pkg/registry/core/coverage_gaps_test.go`, `TestShowEffective_ReasonsPerLayer` runs unchanged, and `TestVisibilityReason_Arms` is the one shipped test the change invalidates at compile time; TEST-2 amends it and its existing expectations stay as they are.

**Mutation checks.** Drop the `scimResolvesGroups` conjunct from the construction site and confirm TEST-1 still passes while TEST-3's first arm fails; that separation is why TEST-3 exists beside the table. Add `trusted-headers` to the allowlist and confirm TEST-1's `trusted-headers` case and TEST-3's first arm both fail. Remove `injected-session-token` and confirm both shipped SCIM visibility tests fail. Make the default arm return true and confirm TEST-1's unset, free-form-label, and unrecognized cases fail. **Gate only the `registry.WithGroupResolver` call at `internal/serverboot/serverboot.go:994` while leaving the variable populated, and confirm TEST-3's layer-read assertion on the first arm fails while its data-plane assertion passes**; that mutation is what a fix applied at a single call site produces, and it is why TEST-3 reads both consumers. Invert `viaDirectory` and confirm TEST-2's two group arms swap. Flip the boolean CODE-2 logs, by passing `resolveGroup == nil` in place of `resolveGroup != nil`, and confirm TEST-3's startup-line assertion and TEST-4's both fail; deleting the line fails them as well, which is what makes the line a tested signal rather than a hand-checked one.

**Coverage.** The predicate, the reason arm, and its discriminator are unit-covered. The construction site and the startup line run only inside the spawned binary, so the default profile scores them as uncovered even though TEST-3 and TEST-4 assert the line's content through the boot log; confirm the execution with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...` and `go tool covdata textfmt` per `.claude/rules/test-coverage.md`. Measure the in-process half with `go test -coverpkg=./... -coverprofile=cover.out ./internal/serverboot/ ./pkg/registry/core/`, and read `codecov/patch` on the pull request rather than the local figure. Check the end-to-end output for `SKIP` before treating a local run as evidence.

## Manual validation

`test/manual-validation.md` gains **S61**, appended after S60, on the standalone `trusted-headers` stack S32 builds (`test/manual-validation.md:2079`). It needs no identity provider, container, or certificate: the registry runs standalone on a loopback bind, and `trusted-headers` authenticates nothing itself. The Scenario index table gains a row after S60's (`test/manual-validation.md:163`), carrying S32's column values:

```
| S61 | SCIM membership does not grant under trusted-headers | standalone | none | none | none |
```

The scenario is a hand-run rather than a suite duplicate because what it reads is the operator's view of the two sources disagreeing: the directory reports alice in `engineering` while the same registry answers `404` on the engineering layer for that caller, and the startup line names the configuration in one place. **DOC-3** carries the whole edit.

> ## S61: SCIM membership does not grant under `trusted-headers`
>
> **Goal.** Validate that a registry running the `trusted-headers` identity
> provider with the SCIM receiver mounted resolves a layer's `groups:` filter
> from `X-Podium-User-Groups` alone, that a directory entry naming the caller's
> `X-Podium-User-Sub` or `X-Podium-User-Email` value grants nothing on the data
> plane or on the layer read, and that the receiver keeps serving its directory.
>
> **Covers.** The `trusted-headers` identity provider (§6.3.3), the §6.3.1 SCIM
> receiver and directory, per-layer visibility (§4.6), the §7.3.1 layer read,
> and the startup line naming whether the directory decides a layer read.
>
> **Prerequisites.** None beyond the build. No identity provider, container, or
> certificate is needed.
>
> **Steps.**
>
> 1. Run the isolation block.
> 2. Write a registry config with a public layer and a group-restricted layer.
>
>    ```bash
>    mkdir -p "$WORK/pub/handbook" "$WORK/eng/deploy"
>    podium artifact scaffold --type context --description "Company handbook" --force "$WORK/pub/handbook"
>    podium artifact scaffold --type skill --description "Engineering deploy" --force "$WORK/eng/deploy"
>    cat > "$WORK/registry.yaml" <<YAML
>    registry:
>      layers:
>        - id: public-handbook
>          source: { local: { path: $WORK/pub } }
>          visibility: { public: true }
>        - id: eng-internal
>          source: { local: { path: $WORK/eng } }
>          visibility: { groups: [engineering] }
>    YAML
>    ```
>
> 3. Boot the server in `trusted-headers` mode with a proxy secret and the SCIM
>    receiver mounted. The bind is loopback, so no `--allow-public-bind` is
>    needed.
>
>    ```bash
>    export PODIUM_IDENTITY_PROVIDER=trusted-headers
>    export PODIUM_TRUSTED_PROXY_SECRET=gateway-secret
>    export PODIUM_SCIM_TOKENS=scim-bearer
>    podium serve --standalone --no-embeddings --config "$WORK/registry.yaml" --bind 127.0.0.1:8134 > "$WORK/srv.log" 2>&1 &
>    SRV=$!
>    curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8134/healthz
>    export URL=http://127.0.0.1:8134
>    grep -E "identity provider|SCIM" "$WORK/srv.log"
>    ```
>
>    **Expect.** `identity provider: trusted-headers (proxy secret required on
>    every request)`, `SCIM 2.0 receiver mounted at /scim/v2/`, and `SCIM group
>    expansion in layer visibility: false (identity provider
>    "trusted-headers")`. The third line is the one this scenario exists for: it
>    names the configuration in which a pushed directory grants nothing.
>
> 4. Push a SCIM user and place her in the `engineering` group.
>
>    ```bash
>    SCIM="Authorization: Bearer scim-bearer"
>    CT="Content-Type: application/scim+json"
>    SCIM_UID=$(curl -s -H "$SCIM" -H "$CT" -X POST "$URL/scim/v2/Users" \
>      -d '{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"alice@acme.com","active":true}' \
>      | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
>    echo "scim user id: $SCIM_UID"
>    curl -s -o /dev/null -w "group create: %{http_code}\n" -H "$SCIM" -H "$CT" -X POST "$URL/scim/v2/Groups" \
>      -d "{\"schemas\":[\"urn:ietf:params:scim:schemas:core:2.0:Group\"],\"displayName\":\"engineering\",\"members\":[{\"value\":\"$SCIM_UID\"}]}"
>    curl -s -H "$SCIM" "$URL/scim/v2/Users/$SCIM_UID" | grep -c "\"id\":\"$SCIM_UID\""
>    curl -s -H "$SCIM" "$URL/scim/v2/Groups"
>    ```
>
>    **Expect.** `scim user id:` prints a non-empty identifier, `group create:
>    201`, the fetch-by-id prints `1`, and the group listing shows
>    `engineering` holding a member whose `value` is that identifier. The
>    fetch-by-id is the assertion that the extraction captured a real user:
>    the receiver answers `404` with a body carrying no identifier when the id
>    names no user (`pkg/scim/handler.go:169-176`), and an empty capture
>    requests the collection route, whose body carries the stored ids and never
>    the string `"id":""`, so both failures print `0`. The group read is a
>    display rather than an assertion, because the receiver stores a member
>    value verbatim and validates nothing about it
>    (`pkg/scim/handler.go:249-255`, `pkg/scim/scim.go:223-241`), so it echoes
>    back whatever the create body interpolated. A lost identifier still
>    creates the group, `MembersOf` then resolves no member
>    (`pkg/scim/scim.go:295-311`), and step 5 would pass for a reason unrelated
>    to this scenario. The capture variable avoids the name `UID`,
>    which both shells reserve as a read-only parameter holding the process
>    user id. The receiver accepts and stores the push on this registry
>    exactly as it does on an `oidc-jwt` one; what differs is what reads it. A
>    `404` from the create requests means `PODIUM_SCIM_TOKENS` was not set
>    before the registry started.
>
> 5. Issue loads as the gateway would. alice is in the SCIM `engineering` group
>    throughout.
>
>    ```bash
>    code() { curl -s -o /dev/null -w "%{http_code}\n" "$@"; }
>    SEC="X-Podium-Proxy-Secret: gateway-secret"
>    echo "scim-only sub:   $(code -H "X-Podium-User-Sub: alice@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
>    echo "scim-only email: $(code -H "X-Podium-User-Sub: opaque-123" -H "X-Podium-User-Email: alice@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
>    echo "asserted group:  $(code -H "X-Podium-User-Sub: alice@acme.com" -H "X-Podium-User-Groups: engineering" -H "$SEC" "$URL/v1/load_artifact?id=deploy")"
>    echo "handbook:        $(code -H "X-Podium-User-Sub: alice@acme.com" -H "$SEC" "$URL/v1/load_artifact?id=handbook")"
>    echo "anon deploy:     $(code "$URL/v1/load_artifact?id=deploy")"
>    ```
>
>    **Expect.**
>
>    - `scim-only sub` and `scim-only email` return `404`. The directory names
>      alice in `engineering`, neither request carries the group header, and the
>      layer is invisible on both routes to the grant.
>    - `asserted group` returns `200`. This is the negative control: the same
>      caller and the same layer, admitted on the group the gateway asserted. A
>      run in which this line returns `404` means the fix withdrew the specified
>      grant as well as the unspecified one.
>    - `handbook` returns `200`, since the public layer needs no group.
>    - `anon deploy` returns `404`.
>
> 6. Read the layer list as the same caller without the groups header.
>
>    ```bash
>    curl -s -H "X-Podium-User-Sub: alice@acme.com" -H "$SEC" "$URL/v1/layers" \
>      | python3 -c 'import json,sys; print(sorted(l["id"] for l in json.load(sys.stdin)["layers"]))'
>    ```
>
>    **Expect.** `['public-handbook']`. The body is parsed rather than grepped
>    for `"id":"`, because `writeJSON` indents the layer list
>    (`pkg/registry/server/server.go:1438-1443`) and a compact pattern matches
>    nothing whether or not the fix is applied. `eng-internal` is absent rather than
>    refused, so the read discloses no identifier, source location, or
>    visibility declaration for it. The layer read and the data plane agree,
>    which is what one gated construction site produces.
>
> **Cleanup.** Stop the server and `rm -rf "$WORK"`.

**IMPLEMENTOR'S CHOICE:** the SCIM request bodies and the identifier extraction, subject to the constraints that follow. The scenario creates one user whose `userName` equals the subject the later requests send and one group named `engineering` holding that user. The extracted value is the created user's `id` from the create response, is non-empty, and is not the process user id, so the capture variable is a name neither bash nor zsh reserves. Step 4 fetches the user by that extracted id and asserts the receiver returns it, so a lost identifier fails step 4 rather than passing step 5 for the wrong reason. The group member value is not the assertion, because the receiver stores it verbatim and echoes it back for any value the create body carried. The bodies above are written from the receiver's shipped contract as `test/e2e/auth_oidc_test.go:239` and `:249` build it; confirm the response envelope when the scenario is landed and adjust the extraction if it differs.

## Documentation changes

**DOC-1.** `docs/deployment/gateway-delegated-identity.md:85` states the defect as behavior, in the sentences "When the registry also mounts the SCIM receiver, a layer's `groups:` filter still expands against the pushed directory: the registry grants visibility when the named group holds a member whose SCIM `userName` equals the caller's `X-Podium-User-Sub` or `X-Podium-User-Email` value. Leave `PODIUM_SCIM_TOKENS` unset under `trusted-headers` when the gateway is meant to be the only source of group membership." Both are replaced with a statement that the pushed directory resolves no layer visibility under this provider, that the receiver stays mounted and keeps accepting and serving pushes when `PODIUM_SCIM_TOKENS` is set, that the registry names the withheld expansion in its startup log, and that a deployment wanting directory membership to grant visibility provisions it at the gateway or runs `oidc-jwt`. The paragraph's first two sentences, on `X-Podium-User-Groups` and `IdpGroupMapping`, stand as written. The page's `oidc-jwt` paragraph at `:57` sits under the `## oidc-jwt` heading, is correctly scoped, and is unchanged.

**DOC-2.** `docs/reference/http-api.md:776` states "The visibility evaluator resolves `groups:` filters against the membership this endpoint records" with no provider qualification, which reads as the rule on every deployment. It gains the qualification: the evaluator resolves `groups:` filters against the recorded membership under the identity providers that verify the caller's credential, and under `trusted-headers` a `groups:` filter matches the `X-Podium-User-Groups` value alone while the receiver keeps recording what the identity provider pushes. The route-mounting sentence is unchanged.

**DOC-3** is the manual-validation edit above, with its Scenario index row.

**DOC-4.** `CHANGELOG.md` gains an entry under `## [Unreleased]` recording the withdrawn grant as backward-incompatible under a MINOR bump: under `trusted-headers`, a layer's `groups:` filter is satisfied by `X-Podium-User-Groups` alone, a caller whose access rested on a SCIM-pushed membership loses it, and the operator provisions that membership at the gateway or moves to `oidc-jwt`. The entry names the startup line and the new diagnostic reason, and states that the SCIM receiver keeps its mount on every provider.

The OIDC cookbook pages that describe the expansion (`docs/deployment/oidc/index.md:59`, `docs/deployment/oidc/okta.md:107`, `docs/deployment/oidc/entra-id.md:146`) each sit inside a guide configuring `oidc-jwt`, where the statement stays true, and are not touched. `docs/deployment/access-control.md:126` and `docs/deployment/integrations.md:92` describe SCIM as a group-membership source without naming a provider and make no claim about `trusted-headers`; both stay true and are left as they are.

## Resolved in adversarial review

### Pass 1 (2026-09-07, automated)

- **CODE-3's parameter broke a shipped test no edit list named.** `visibilityReason` has a second caller, the table at `pkg/registry/core/coverage_gaps_test.go:247`, which calls the three-argument form, so the staged signature stopped the `pkg/registry/core` test package from compiling. S3 now names the file and is marked indivisible, TEST-2 stages the amendment to `TestVisibilityReason_Arms` (the `viaDirectory` field, the amended call, the shipped cases passing `false`, and the directory case), CODE-3 states why the edit is required, and Non-regression records that `TestShowEffective_ReasonsPerLayer` runs unchanged. A Summary bullet names the compile-time trap.
- **TEST-3's anonymous arms asserted a public layer on a consumer that returns none.** `readableBy` returns the empty list for an unauthenticated caller before the evaluator runs (`pkg/registry/server/layers.go:259-261`), which §7.3.1 states. The anonymous arm and the no-proxy-secret arm are now asserted on the data plane, the "both consumers" reading is scoped to the two authenticated arms and the negative control, and a Summary bullet records the asymmetry.
- **S61 step 4 assigned to `UID`, which both shells reserve.** The capture is renamed to `SCIM_UID` in the assignment, the echo, and the group body, so the group is created with the user's real identifier. Step 4 also fetches the user by the captured id, so a lost identifier fails step 4 instead of making step 5 pass for an unrelated reason, and the scenario's IMPLEMENTOR'S CHOICE carries those constraints.
- **The first fix's group-read check matched every possible capture.** The receiver stores a group member value verbatim and validates nothing about it (`pkg/scim/handler.go:249-255`, `pkg/scim/scim.go:223-241`), and `groupToSCIM` re-emits it unchanged (`pkg/scim/handler.go:236-247`), so `grep -c "\"value\":\"$SCIM_UID\""` matched the string the shell had interpolated, including the empty one a failed extraction produces. Step 4 now fetches `/scim/v2/Users/$SCIM_UID` and counts `"id":"$SCIM_UID"` in the response, which the store can refuse: an unknown id answers `404` with a body carrying no identifier (`pkg/scim/handler.go:169-176`), and an empty capture falls through to the collection route, whose body never carries `"id":""`. The group listing stays in the step as a display, and the Expect and the IMPLEMENTOR'S CHOICE say which of the two is the assertion.
- **S61 step 6 grepped a compact pattern against an indented body.** `writeJSON` indents the layer list (`pkg/registry/server/server.go:1438-1443`), so `"id":"` matched nothing before or after the fix. The step parses the body with python3, as `test/manual-validation.md:6236` does, and expects `['public-handbook']`.

### Pass 2 (2026-09-07, automated)

- **S61 step 4's Expect anchored the Users-by-id 404 on the Groups handler.** The step fetches `GET /scim/v2/Users/$SCIM_UID`, which `routeUsers` dispatches to `getUser` (`pkg/scim/handler.go:73-89`), and `getUser` is the arm that answers `404` with a body carrying no identifier when the store refuses the id (`pkg/scim/handler.go:169-176`). The cited `pkg/scim/handler.go:275-282` is `getGroup`, the handler for `GET /scim/v2/Groups/{id}`. The Expect block is staged text that lands verbatim in `test/manual-validation.md`, so the wrong anchor would have shipped into the repository. The citation now names `getUser` in the Expect block and in the matching Pass 1 entry, and the argument around it is unchanged.

### Pass 3 (2026-09-07, automated)

- **The pre-upgrade enumeration compared group member ids against gateway header values.** `GET /scim/v2/Groups` emits each member as a `value`/`type` pair holding the registry's internal user id and carries no `userName` (`pkg/scim/handler.go:230-247`, `:284-299`), while the evaluator matches the `userName` that lives on the user record (`pkg/scim/scim.go:295-311`). Followed as written, the procedure compared internal ids against the subject and email values the gateway sets, matched nothing, and reported that no caller loses access. It is the only procedure the proposal gives an operator for finding who loses visibility at the next restart. The step now states that the group listing carries opaque member ids and resolves each id through `GET /scim/v2/Users/{id}`, whose response carries the `userName` (`pkg/scim/handler.go:169-176`, `:125-137`), before the comparison. This aligns the paragraph with the S61 analysis, which already routes its assertion through the Users endpoint for the same reason. DOC-1 and DOC-4 stage no copy of the procedure, so no other section changed.

### Pass 4 (2026-09-07, automated)

- **CODE-2's startup line was pinned by no automated test.** The proposal named the line one of its detectability additions and staged an edge-case row for it, and every listed test read HTTP statuses, layer lists, or the diagnostic reason, so an implementor who inverted the logged boolean or dropped the line broke nothing in the suite and only the hand-run S61 step 3 would have caught it. The behavior is written by the spawned binary at boot, which is the level `test/e2e/auth_oidc_jwt_test.go:222-223` pins the accepted-issuer line at, and TEST-3 already boots a `trusted-headers` registry with the receiver mounted while `serverProc` exposes `srv.log()` (`test/e2e/helpers_test.go:282`). TEST-3 gains an arm asserting the withheld form and TEST-4 an assertion on the permitted form, both matching the literal prefix and the boolean, so the two values the line can report are each pinned. Mutation checks gain the flipped boolean and the deleted line. CODE-2's IMPLEMENTOR'S CHOICE over the wording is withdrawn, because S61 step 3 already quoted the line verbatim and the assertions read it as well, and a blank is not allowed for text a test asserts. The edge-case row, the S4 checklist entry, the ordering paragraph's justification for S4's dependency, the Coverage paragraph, and the Summary now state the same rule.
- **Correction to the entry above: TEST-3's startup-line arm called itself the only assertion on the line.** The same pass added a second one in TEST-4, and CODE-2, the Mutation checks, the Coverage paragraph, and this pass entry all describe two assertions, so an implementor reading TEST-3 alone would have read the TEST-4 assertion as redundant or unstaged and dropped the permitted-value half. The clause now scopes TEST-3 to the line's withheld value and names TEST-4 as the pin on the permitted one. No other statement changed, because the rest already said this.

## Open questions

**OQ-1: `injected-session-token` in the allowlist.** The specification does not state at the provider level whether the directory applies. §6.3.1's opening sentence resolves group membership registry-side through SCIM as the general rule and names `groups` as the claim the adapter reads under this provider, §6.3.2 states nothing about groups, and §6.3.3's per-provider sentences cover the registry-process providers alone. SPEC-1's staged position is that it applies, on two grounds: the injected token carries a subject the directory is keyed by, so the reason §6.3.3 gives for the `trusted-headers` carve-out ("there is no token to read") does not hold here; and the registry verifies the token's signature on every call (§6.3.2), so the identity is one the registry established rather than one a second system asserted. The alternative reading is that a runtime acting on a user's behalf is the authority on that user's groups the way a gateway is, which would forbid the expansion and change shipped behavior on that provider. Choosing the alternative fails both shipped SCIM visibility end-to-end tests, which is the mechanical consequence to weigh. **Resolved 2026-09-07 by the reviewer: `injected-session-token` stays in the allowlist, and SPEC-1 stands as staged.** The deciding property is that the registry verifies the token's signature on every call, which is the line §6.3.3 itself reaches for when it explains the `trusted-headers` carve-out, and which places this provider with `oidc-jwt` rather than with the gateway. The §6.3.1 mention of the `IdpGroupMapping` adapter under this provider was weighed and does not decide the cell, because the adapter transforms values the token already carried while the directory is an independent source. SPEC-1 states the cell either way, which is the change that closes it.

**OQ-2: whether `PODIUM_SCIM_TOKENS` under `trusted-headers` should refuse startup.** The staged position mounts the receiver and logs that the directory decides no read, because the identity provider writes it, a later provider change reads it, and refusing would stop a running deployment on an input that is now inert. The alternative refuses at startup, on the reasoning that a mounted receiver whose data no decision reads is a misconfiguration the operator should be told about once rather than a line they may not read. The log line is the weaker signal of the two. A refusal would need a §6.10 config code and its `matrix-audit` entry, which SPEC-1 does not stage.

**Resolved 2026-09-07 by the reviewer: the staged position stands.** The registry mounts the receiver and logs. Turning a setting that is inert after this fix into a boot failure would stop a running deployment on an input that is no longer dangerous, and the refusal would carry a new error code this proposal does not stage.

**OQ-3: whether the diagnostic's second re-evaluation is worth its cost.** CODE-3 evaluates each layer twice on `GET /v1/admin/show-effective`, once with the expander and once without. The endpoint is admin-gated, runs over the tenant's layer list, and is not on a read path, so the cost is bounded. The alternative is to leave the reason string collapsed and rely on the startup line alone, which tells an operator whether the directory can decide a read on this registry and not which layers it decided.

**Resolved 2026-09-07 by the reviewer: the staged position stands.** CODE-3 is kept. The second evaluation passes a nil resolver, so it performs no directory lookup and repeats an in-memory comparison on an admin-gated diagnostic. It is the only live answer to which layers the directory decides for a given caller, which is the question this change creates for an operator and which no §8.1 field records.

## Non-goals

- **Amending the spec to permit SCIM under `trusted-headers`.** The direction is settled: the spec is correct and the code is the defect.
- **A type-level chokepoint in `pkg/identity`.** The strongest alternative mechanism is a `GroupExpansion` value type whose only non-empty constructor reads the provider label, taken by the evaluator in place of the bare `layer.GroupResolver` func. Its argument is correct-by-construction enforcement: the field is unexported, the zero value expands nothing, and a wiring site added in a future change cannot produce a permissive value without going through the constructor, so the wiring-discipline failure that produced this defect becomes unwritable rather than merely fixed. It is rejected as the mechanism here because it removes `layer.GroupResolver`, `layer.VisibleWith`, and `layer.EffectiveLayersWith`, changes six non-test call sites and two setters, moves the evaluator's own test files, and adds an import edge from `pkg/layer` into `pkg/identity`. That is a large refactor riding on a security fix, and there is one construction site today. The structural cost of declining it is stated in "Watch out for" and in the edge-case table rather than paid here.
- **Group provenance on the caller identity.** The other alternative records on `layer.Identity` how the caller's groups were established, and has the evaluator consult the resolver only for a token-derived identity. Its argument is that the deciding fact is a property of the identity rather than of the registry's configuration: a gateway-asserted identity is complete as delivered, a token-derived one is incomplete because §6.3.1 places the authoritative membership in a registry-side directory, and the zero value declines the expansion so every unconverted construction site fails closed at the point of decision. It is rejected because it widens a struct three packages read, requires each of the shipped evaluator fixtures to state its provenance before it stops reporting the change as a regression, and still does not remove provider knowledge from the wiring: `handleAdminShowEffective` synthesizes its target identity from query parameters and holds no credential, so the configured provenance would have to be supplied to the server at boot for that one endpoint. The result is weaker than "the rule travels with the data, everywhere", at the cost of a field on a value type that is not otherwise part of this change.
- **Teaching `layer.VisibleWith` about identity providers.** The evaluator takes a resolver and contracts nil as the claim-only path. Putting a provider condition inside it would spread identity configuration into a package that holds none.
- **A resolver that can deny.** The expander is additive by construction, and this proposal does not change what it returns. Making the directory able to withdraw a membership the caller's own claims carry is a separate §4.6 question with no spec basis today.
- **Changing `layer.VisibleWith`'s arm order or its `member == id.Sub || member == id.Email` comparison.** The evaluator is correct for the providers that reach it with a resolver.
- **Changing the `users:` arm's subject-or-email match under `trusted-headers`.** §4.6's table and §6.3.3's operational-assumption paragraph specify it together, and the identifier property it carries is one §6.3.3 assigns to the gateway.
- **Changing what `PODIUM_SCIM_TOKENS` mounts.** The receiver, its routes, its authentication, its persistence, and its CRUD surface are unchanged on every provider.
- **The `IdpGroupMapping` adapter.** It rewrites the values a token's group claim carries (`internal/serverboot/serverboot.go:1666-1670`, applied at `internal/serverboot/identity_verify.go:264-267` and `:32-35`), so it is unreachable under `trusted-headers` for want of a token. No change is needed to satisfy the other half of §6.3.3's sentence.
- **Replacing `context.Background()` in the resolver closure with the request context**, which requires changing `layer.GroupResolver`'s signature and every call site.
- **Adding a per-request audit field, a §6.10 error code, or a metric naming the arm that admitted a caller.** §8.1's event set is unchanged. The two signals this proposal adds report configuration and evaluation rather than adding a record to the read path.
- **A flag, environment variable, or `registry.yaml` key restoring the SCIM expansion under `trusted-headers`.**
- **Four divergences found while drafting this proposal and tracked separately.** A malformed `PODIUM_IDP_GROUP_MAPPING` is logged and dropped rather than refused (`internal/serverboot/serverboot.go:2198-2204`), so the raw claim values are matched against `groups:` filters unrewritten. §13.12's registry-process identity table carries no row for `PODIUM_IDP_GROUP_MAPPING`, `PODIUM_SCIM_TOKENS`, or `PODIUM_SCIM_STORE_PATH`. `docs/deployment/oidc/index.md:60` states that the group claim key is fixed at `groups` and that a single delimited string resolves to no groups, both of which the code and §6.3.1 contradict. `pkg/scim/scim.go:13` names `PODIUM_SCIM_TOKEN` where the receiver reads `PODIUM_SCIM_TOKENS`. None of them is staged here.
