// Package serverboot is the shared bootstrap for the registry HTTP
// server. The podium-server binary and the `podium serve`
// subcommand both call Run, so a single deployment ships only the
// `podium` binary and still has the standalone server available
// in-process.
//
// Configuration comes from PODIUM_* environment variables (§13.12)
// with `~/.podium/registry.yaml` filling in any unset values.
// Default behavior matches §13.10's zero-flag standalone
// deployment: SQLite metadata, filesystem object store, no auth,
// bound on 127.0.0.1:8080.
package serverboot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/notification"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/scim"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/tracing"
	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/webhook"
	"github.com/lennylabs/podium/web"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// envFirst returns the value of the first non-empty env var.
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// buildSCIMHandler returns a SCIM handler when at least one
// bearer token is configured via PODIUM_SCIM_TOKENS (comma-
// separated). Returns nil otherwise; the registry then runs
// without an IdP push interface and the visibility evaluator
// matches groups via JWT claims only.
func buildSCIMHandler(store scim.Store) *scim.Handler {
	raw := os.Getenv("PODIUM_SCIM_TOKENS")
	if raw == "" {
		return nil
	}
	tokens := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tokens[t] = true
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	return &scim.Handler{Store: store, Tokens: tokens}
}

// openNotifier returns the §9 notification provider per
// PODIUM_NOTIFICATION_PROVIDER. Returns nil when unset or when the
// provider name resolves to "noop".
func openNotifier() notification.Provider {
	switch os.Getenv("PODIUM_NOTIFICATION_PROVIDER") {
	case "", "noop":
		return nil
	case "log":
		return notification.LogProvider{}
	case "webhook":
		url := os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_URL")
		if url == "" {
			log.Printf("warning: PODIUM_NOTIFICATION_WEBHOOK_URL is required for webhook notifier")
			return nil
		}
		return notification.Webhook{
			URL:    url,
			Secret: os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_SECRET"),
		}
	case "email", "smtp":
		// §9.1 NotificationProvider email delivery over SMTP.
		smtp, ok := smtpNotifierFromEnv()
		if !ok {
			log.Printf("warning: PODIUM_NOTIFICATION_SMTP_HOST and PODIUM_NOTIFICATION_SMTP_FROM are required for the email notifier")
			return nil
		}
		return smtp
	case "multi":
		// §9.1 default "Email + webhook": "multi" combines the log
		// provider with the webhook and email providers when each is
		// configured. Useful for "alert + record" deployments.
		out := []notification.Provider{notification.LogProvider{}}
		if url := os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_URL"); url != "" {
			out = append(out, notification.Webhook{
				URL:    url,
				Secret: os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_SECRET"),
			})
		}
		if smtp, ok := smtpNotifierFromEnv(); ok {
			out = append(out, smtp)
		}
		return notification.MultiProvider{Providers: out}
	}
	log.Printf("warning: unknown PODIUM_NOTIFICATION_PROVIDER=%q",
		os.Getenv("PODIUM_NOTIFICATION_PROVIDER"))
	return nil
}

// smtpNotifierFromEnv builds the §9.1 email NotificationProvider from
// the PODIUM_NOTIFICATION_SMTP_* environment. Reports false when the
// required host or sender address is absent so the caller can warn and
// fall back. Per-notification Recipients override the configured
// PODIUM_NOTIFICATION_SMTP_TO list.
func smtpNotifierFromEnv() (notification.SMTP, bool) {
	host := os.Getenv("PODIUM_NOTIFICATION_SMTP_HOST")
	from := os.Getenv("PODIUM_NOTIFICATION_SMTP_FROM")
	if host == "" || from == "" {
		return notification.SMTP{}, false
	}
	return notification.SMTP{
		Host:     host,
		Port:     envInt("PODIUM_NOTIFICATION_SMTP_PORT", 0),
		From:     from,
		To:       splitCSVTrim(os.Getenv("PODIUM_NOTIFICATION_SMTP_TO")),
		Username: os.Getenv("PODIUM_NOTIFICATION_SMTP_USERNAME"),
		Password: os.Getenv("PODIUM_NOTIFICATION_SMTP_PASSWORD"),
	}, true
}

// splitCSVTrim splits a comma-separated list, trimming whitespace and
// dropping empty entries. Returns nil for an empty input.
func splitCSVTrim(raw string) []string {
	if raw == "" {
		return nil
	}
	out := []string{}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// adaptNotifier turns a notification.Provider into the
// core.NotificationFunc shape, swallowing errors (the registry
// keeps running on outage; the audit log records what happened).
func adaptNotifier(p notification.Provider) core.NotificationFunc {
	return func(ctx context.Context, severity, title, body string, tags map[string]string) {
		err := p.Notify(ctx, notification.Notification{
			Severity: notification.Severity(severity),
			Title:    title,
			Body:     body,
			Tags:     tags,
			Time:     time.Now().UTC(),
		})
		if err != nil {
			log.Printf("notification (%s): %v", p.ID(), err)
		}
	}
}

// envInt returns the integer value of an env var, or def when the
// var is unset or invalid.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func envInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// isManagedVectorBackend reports whether the configured vector backend is an
// external managed service (Pinecone, Weaviate, Qdrant, or any backend not
// collocated with the metadata store) rather than an in-process or
// store-collocated one (memory, sqlite-vec, pgvector). External backends route
// §4.7.2 embedding through the transactional outbox so ingest never blocks on
// the remote service.
func isManagedVectorBackend(id string) bool {
	switch id {
	case "", "none", "memory", "sqlite-vec", "pgvector":
		return false
	default:
		return true
	}
}

// parseAuditSampleRates parses the §8.4 sampling spec
// "TYPE=RATE,TYPE=RATE" (e.g. "domain.loaded=0.1,artifact.loaded=0.5")
// into a per-event-type keep-rate map. Malformed entries and rates
// outside [0,1] are skipped so a typo disables sampling for that type
// rather than dropping every event. Returns nil when nothing parses.
func parseAuditSampleRates(raw string) map[audit.EventType]float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[audit.EventType]float64{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.LastIndex(part, "=")
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(part[:eq])
		rate, err := strconv.ParseFloat(strings.TrimSpace(part[eq+1:]), 64)
		if err != nil || rate < 0 || rate > 1 {
			continue
		}
		out[audit.EventType(name)] = rate
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// bootstrapLayerPath ingests the filesystem registry at layerPath
// (when non-empty), persists a store.LayerConfig per resolved layer,
// and returns the in-memory []layer.Layer the core registry uses for
// visibility filtering. Returns an empty slice when layerPath is
// empty so the caller can pass the result straight into core.New.
//
// Ingest runs against context.Background: a bootstrap failure
// returns an error and aborts startup before any HTTP listener is
// bound, so there is no in-flight request context to thread through.
//
// Layer ordering follows filesystem.Open's resolution
// (alphabetical, or layer_order: when .registry-config sets it),
// with Order/Precedence assigned startOrder+1..startOrder+N
// (lowest-precedence first per §4.6). startOrder lets a caller append
// these layers after a declarative `layers:` list.
//
// vis is the visibility stamped on every resolved layer. It is computed
// by the caller from the deployment mode (§4.6 / §13.10): public for a
// no-identity-provider standalone, otherwise the configured default
// (F-4.6.9). The bootstrap path supplies no per-layer visibility input.
// ingestLinter builds the ingest linter shared by the bootstrap paths:
// §4.4 prose-URL validation (offline per PODIUM_INGEST_OFFLINE) plus the
// §4.5.5 discovery-override warning when the tenant disabled per-domain
// overrides (allowPerDomain false).
func ingestLinter(allowPerDomain bool) *lint.Linter {
	lr := lint.NewIngestLinter(isTrue(os.Getenv("PODIUM_INGEST_OFFLINE")))
	if !allowPerDomain {
		disabled := false
		lr.AllowPerDomainOverrides = &disabled
	}
	return lr
}

// parseBootstrapAdmins splits PODIUM_BOOTSTRAP_ADMINS into user IDs. The
// value accepts commas, whitespace, or both as separators (so a YAML list
// rendered as "alice@acme.com, bob@acme.com" and a shell-friendly
// "alice@acme.com bob@acme.com" both work). Empty fields are dropped.
//
// spec: §13.1.1 — the evaluation-stack bootstrap creates the first admin
// user, configurable via env vars.
func parseBootstrapAdmins(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// seedBootstrapAdmins grants the tenant admin role to each user in admins.
// It returns the number of grants applied. The operation is idempotent: the
// store's GrantAdmin upserts, so re-seeding an existing admin on a later boot
// is a no-op rather than an error. A single failed grant aborts and returns
// the error with the count applied so far.
//
// spec: §13.1.1 — the bootstrap step "creates the first tenant and admin
// user (configurable via env vars)". The default tenant is created by Run's
// caller before this runs.
func seedBootstrapAdmins(ctx context.Context, st store.Store, tenantID string, admins []string) (int, error) {
	seeded := 0
	for _, userID := range admins {
		if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: userID, OrgID: tenantID}); err != nil {
			return seeded, fmt.Errorf("grant admin %q in %q: %w", userID, tenantID, err)
		}
		seeded++
	}
	return seeded, nil
}

func bootstrapLayerPath(st store.Store, tenantID, layerPath string, vis layer.Visibility, startOrder int, allowPerDomain bool, resourcePut ingest.ResourcePutFunc, rejectAtOrAbove manifest.Sensitivity, signer ingest.SignerFunc, useVectorOutbox bool, enforceSandbox bool, hostSandboxes []string) ([]layer.Layer, error) {
	if layerPath == "" {
		return []layer.Layer{}, nil
	}
	fsReg, err := filesystem.Open(layerPath)
	if err != nil {
		return nil, fmt.Errorf("open layer path %s: %w", layerPath, err)
	}
	ctx := context.Background()
	layers := make([]layer.Layer, 0, len(fsReg.Layers))
	for i, l := range fsReg.Layers {
		order := startOrder + i + 1
		res, err := ingest.Ingest(ctx, st, ingest.Request{
			TenantID: tenantID,
			LayerID:  l.ID,
			Files:    os.DirFS(l.Path),
			// §13.10/§13.2.2 public-mode sensitivity ceiling: reject medium and
			// high artifacts at ingest with ingest.public_mode_rejects_sensitive.
			// Empty (non-public deployments) imposes no floor.
			RejectAtOrAbove: rejectAtOrAbove,
			// §13.10 sandbox-profile ingest gate (off unless enforced).
			EnforceSandboxProfile:      enforceSandbox,
			EnforceableSandboxProfiles: hostSandboxes,
			// §4.4: validate prose URL references with an HTTP HEAD by
			// default; PODIUM_INGEST_OFFLINE=true skips the network probe.
			// §4.5.5: warn on DOMAIN.md discovery: blocks when per-domain
			// overrides are disabled tenant-wide.
			Linter: ingestLinter(allowPerDomain),
			// §7.2 data plane: upload bundled resources to the configured
			// object store at ingest so load_artifact can serve them.
			ResourcePut: resourcePut,
			// §13.10/§4.7.9 ingest signing: nil leaves manifests unsigned.
			Signer: signer,
			// §4.7.2: an external vector backend commits a vector_pending row
			// in the same transaction; the drain worker embeds it later.
			UseVectorOutbox: useVectorOutbox,
		})
		if err != nil {
			return nil, fmt.Errorf("ingest layer %s from %s: %w", l.ID, l.Path, err)
		}
		// Persist a LayerConfig so /v1/layers, /v1/layers/reingest, and
		// the standalone web UI (§13.10) see the bootstrap layers. The
		// SourceType is "local" with LocalPath set to the resolved
		// directory so a future reingest can re-snapshot the same path.
		now := time.Now().UTC()
		cfg := store.LayerConfig{
			TenantID:     tenantID,
			ID:           l.ID,
			SourceType:   "local",
			LocalPath:    l.Path,
			Order:        order,
			Public:       vis.Public,
			Organization: vis.Organization,
			Groups:       vis.Groups,
			Users:        vis.Users,
			CreatedAt:    now,
			// §7.3.1: the bootstrap ingest just completed, so stamp
			// last_ingested_at for staleness monitoring (F-7.3.6).
			LastIngestedAt: &now,
		}
		if err := st.PutLayerConfig(ctx, cfg); err != nil {
			return nil, fmt.Errorf("persist layer config %s: %w", l.ID, err)
		}
		layers = append(layers, layer.Layer{
			ID:         l.ID,
			Precedence: order,
			Visibility: vis,
		})
		log.Printf("ingested layer %s from %s (accepted=%d, idempotent=%d, rejected=%d, advisories=%d)",
			l.ID, l.Path, res.Accepted, res.Idempotent, len(res.Rejected), len(res.Advisories))
	}
	return layers, nil
}

// publicSensitivityFloor returns the §13.10/§13.2.2 ingest sensitivity floor
// for the deployment. Public mode rejects medium and high artifacts at ingest;
// every other deployment imposes no floor (empty).
func publicSensitivityFloor(c *Config) manifest.Sensitivity {
	if c.publicMode {
		return manifest.SensitivityMedium
	}
	return ""
}

// defaultBootstrapVisibility returns the visibility stamped on a layer that
// carries no explicit visibility input (a §13.10 PODIUM_LAYER_PATH bootstrap
// layer, or a declarative layer whose `visibility:` block is empty).
//
// spec: §4.6 / §13.10 / §13.12 (F-4.6.9, F-13.10.15). Public mode bypasses the
// visibility model entirely, so its bootstrap layers are public. Otherwise the
// bootstrap layer carries the resolved PODIUM_DEFAULT_LAYER_VISIBILITY: §13.12
// documents that env var as the fallback "applied when a layer is registered
// without an explicit setting", and a bootstrap layer is exactly that. The
// fallback default is itself deployment-aware (LoadConfig resolves an unset
// value to "public" for a no-identity standalone per §13.10 and "private" once
// an identity provider gates access), so a zero-config standalone still yields
// public bootstrap layers, while an operator who sets
// PODIUM_DEFAULT_LAYER_VISIBILITY=private|organization for a multi-user
// standalone gets the restricted visibility they asked for instead of an
// unconditional public layer.
func defaultBootstrapVisibility(cfg *Config) layer.Visibility {
	if cfg.publicMode {
		return layer.Visibility{Public: true}
	}
	if cfg.defaultLayerVisibility == "" {
		// A directly-constructed Config (e.g. a unit test) may skip
		// LoadConfig's resolution; mirror it so a no-identity standalone
		// still defaults to public and an identity-gated deployment to
		// private.
		if cfg.identityProvider == "" {
			return layer.Visibility{Public: true}
		}
		return layer.Visibility{}
	}
	return visibilityFromDefault(cfg.defaultLayerVisibility)
}

// visibilityFromDefault maps a PODIUM_DEFAULT_LAYER_VISIBILITY value to a
// layer.Visibility. "private"/unset/unknown leaves no visibility filters set,
// so only an explicit grant (added later via the layer-management API) can
// surface the layer. spec: §4.6.
func visibilityFromDefault(v string) layer.Visibility {
	switch v {
	case "public":
		return layer.Visibility{Public: true}
	case "organization":
		return layer.Visibility{Organization: true}
	default:
		return layer.Visibility{}
	}
}

// visibilityIsEmpty reports whether a visibility block declares no filters.
func visibilityIsEmpty(v layer.Visibility) bool {
	return !v.Public && !v.Organization && len(v.Groups) == 0 && len(v.Users) == 0
}

// bootstrapDeclaredLayers seeds a store.LayerConfig per entry in the §4.6
// declarative `layers:` list and returns the in-memory []layer.Layer the core
// registry uses for visibility filtering. Local sources are ingested at
// startup so their artifacts are immediately searchable. Git sources are
// seeded as config rows only: a git clone is unbounded network I/O that must
// not block startup, so the §7.3.1 reingest/webhook path pulls them on demand
// (spec §13.10: "Additional local and git layers can be registered ... after
// startup"). Orders are assigned 1..N in list order (lowest precedence first,
// "in the order they appear in the registry config", §4.6).
//
// Ingest runs against context.Background: a bootstrap failure aborts startup
// before any HTTP listener binds.
func bootstrapDeclaredLayers(st store.Store, tenantID string, cfg *Config, resourcePut ingest.ResourcePutFunc, signer ingest.SignerFunc, useVectorOutbox bool) ([]layer.Layer, error) {
	if len(cfg.declaredLayers) == 0 {
		return []layer.Layer{}, nil
	}
	ctx := context.Background()
	layers := make([]layer.Layer, 0, len(cfg.declaredLayers))
	for i, entry := range cfg.declaredLayers {
		order := i + 1
		lc, vis, err := layerConfigFromEntry(tenantID, entry, order, cfg)
		if err != nil {
			return nil, err
		}
		if err := st.PutLayerConfig(ctx, lc); err != nil {
			return nil, fmt.Errorf("persist layer config %s: %w", lc.ID, err)
		}
		switch lc.SourceType {
		case "local":
			res, err := ingest.Ingest(ctx, st, ingest.Request{
				TenantID: tenantID,
				LayerID:  lc.ID,
				Files:    os.DirFS(lc.LocalPath),
				// §13.10/§13.2.2 public-mode sensitivity ceiling.
				RejectAtOrAbove: publicSensitivityFloor(cfg),
				// §13.10 sandbox-profile ingest gate (off unless enforced).
				EnforceSandboxProfile:      cfg.enforceSandboxProfile,
				EnforceableSandboxProfiles: cfg.hostSandboxes,
				Linter:                     ingestLinter(cfg.allowPerDomain()),
				// §7.2 data plane: persist bundled resources at ingest.
				ResourcePut: resourcePut,
				// §13.10/§4.7.9 ingest signing: nil leaves manifests unsigned.
				Signer: signer,
				// §4.7.2 external-backend outbox enqueue at ingest.
				UseVectorOutbox: useVectorOutbox,
			})
			if err != nil {
				return nil, fmt.Errorf("ingest declared layer %s from %s: %w", lc.ID, lc.LocalPath, err)
			}
			log.Printf("ingested declared layer %s from %s (accepted=%d, idempotent=%d, rejected=%d, advisories=%d)",
				lc.ID, lc.LocalPath, res.Accepted, res.Idempotent, len(res.Rejected), len(res.Advisories))
		case "git":
			log.Printf("seeded declared git layer %s (repo=%s ref=%s); awaiting reingest/webhook to ingest",
				lc.ID, lc.Repo, lc.Ref)
		}
		layers = append(layers, layer.Layer{
			ID:         lc.ID,
			Precedence: order,
			Visibility: vis,
		})
	}
	return layers, nil
}

// layerConfigFromEntry validates one §4.6 declared layer entry and builds the
// store.LayerConfig plus the resolved visibility. An empty `visibility:` block
// falls back to the deployment default (§4.6 / F-4.6.9). Exactly one source
// (git or local) must be set.
// validForcePushPolicyYAML reports whether a registry.yaml
// source.git.force_push_policy value is one of the §7.3.1 accepted forms.
func validForcePushPolicyYAML(p string) bool {
	switch p {
	case "", "tolerant", "strict":
		return true
	default:
		return false
	}
}

func layerConfigFromEntry(tenantID string, entry yamlLayerEntry, order int, cfg *Config) (store.LayerConfig, layer.Visibility, error) {
	if entry.ID == "" {
		return store.LayerConfig{}, layer.Visibility{}, fmt.Errorf("declared layer at position %d is missing id", order)
	}
	hasGit := entry.Source.Git != nil
	hasLocal := entry.Source.Local != nil
	if hasGit == hasLocal {
		return store.LayerConfig{}, layer.Visibility{}, fmt.Errorf("declared layer %s must set exactly one source (git or local)", entry.ID)
	}
	vis := layer.Visibility{
		Public:       entry.Visibility.Public,
		Organization: entry.Visibility.Organization,
		Groups:       entry.Visibility.Groups,
		Users:        entry.Visibility.Users,
	}
	if visibilityIsEmpty(vis) {
		vis = defaultBootstrapVisibility(cfg)
	}
	lc := store.LayerConfig{
		TenantID:     tenantID,
		ID:           entry.ID,
		Order:        order,
		Public:       vis.Public,
		Organization: vis.Organization,
		Groups:       vis.Groups,
		Users:        vis.Users,
		CreatedAt:    time.Now().UTC(),
	}
	switch {
	case hasGit:
		if entry.Source.Git.Repo == "" {
			return store.LayerConfig{}, layer.Visibility{}, fmt.Errorf("declared git layer %s is missing source.git.repo", entry.ID)
		}
		if !validForcePushPolicyYAML(entry.Source.Git.ForcePushPolicy) {
			return store.LayerConfig{}, layer.Visibility{}, fmt.Errorf(
				"declared git layer %s has invalid force_push_policy %q (want tolerant or strict)",
				entry.ID, entry.Source.Git.ForcePushPolicy)
		}
		lc.SourceType = "git"
		lc.Repo = entry.Source.Git.Repo
		lc.Ref = entry.Source.Git.Ref
		lc.Root = entry.Source.Git.Root
		lc.ForcePushPolicy = entry.Source.Git.ForcePushPolicy
	case hasLocal:
		if entry.Source.Local.Path == "" {
			return store.LayerConfig{}, layer.Visibility{}, fmt.Errorf("declared local layer %s is missing source.local.path", entry.ID)
		}
		lc.SourceType = "local"
		lc.LocalPath = entry.Source.Local.Path
	}
	return lc, vis, nil
}

// Run loads configuration, opens the configured backends, mounts
// every endpoint, and blocks on the HTTP listener. Returns the
// http.Server's error (always non-nil — at minimum
// http.ErrServerClosed when the listener exits cleanly).
func Run() error {
	// §13.10 (F-13.10.2): an explicitly named --config / PODIUM_CONFIG_FILE that
	// does not exist is a hard error — the operator named a config, so a missing
	// one is not a cue to invent standalone defaults.
	if cf := os.Getenv("PODIUM_CONFIG_FILE"); cf != "" {
		if _, err := os.Stat(cf); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("config file %q does not exist", cf)
			}
			return fmt.Errorf("config file %q: %w", cf, err)
		}
	}

	cfg := LoadConfig()
	if err := cfg.validate(); err != nil {
		return err
	}

	// §13.10 / §14.3 / §14.10: apply the zero-flag standalone policy — refuse to
	// start under --strict when no config is present, otherwise emit the
	// first-run notice and write the client and server default files so
	// consumers resolve the registry from ~/.podium/sync.yaml without an extra
	// env var. Existing files are preserved.
	if err := standaloneStartup(cfg); err != nil {
		return err
	}

	st, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// Standalone bootstrap: ensure the bootstrapped org exists so initial
	// requests don't fail on missing-tenant lookups (§13.10 auto-bootstrap).
	// spec: §4.7.1 — the org ID is a UUID while "default" stays the
	// human-readable name; the deterministic UUID keeps the ID stable across
	// restarts so persisted tenant-scoped rows are not orphaned. Everything
	// below threads this tenantID as the org. CreateTenant is idempotent, so
	// a store fault is tolerated (the ID is still returned).
	tenantID, _ := bootstrapDefaultTenant(context.Background(), st, cfg.exposeScopePreview)

	// §13.1.1 evaluation-stack bootstrap: seed the configured admin users
	// for the default tenant so the documented `docker compose up` →
	// `podium init` → `podium login` workflow reaches a state where an
	// admin exists. PODIUM_BOOTSTRAP_ADMINS carries the user IDs; the
	// chicken-and-egg of "the first admin can only be granted by an existing
	// admin" is broken here at boot rather than over the admin API.
	if n, err := seedBootstrapAdmins(context.Background(), st, tenantID, cfg.bootstrapAdmins); err != nil {
		log.Printf("warning: bootstrap admin seeding failed: %v", err)
	} else if n > 0 {
		log.Printf("bootstrap: seeded %d admin grant(s) for tenant %q", n, tenantID)
	}

	// §7.2 data plane: open the object store before any ingest so bundled
	// resources upload to it keyed by content hash as artifacts ingest.
	// A nil store (disabled, or open failure) leaves resources inline on
	// the manifest record.
	objStore := openObjectStoreOrNil(cfg)
	var resourcePut ingest.ResourcePutFunc
	if objStore != nil {
		resourcePut = objStore.Put
	}

	// §13.10 / §4.7.9 ingest signing: when --sign registry-key (PODIUM_SIGN)
	// is set, every accepted manifest's content hash is signed with the
	// registry-managed key. Disabled by default; the bootstrap and reingest
	// paths leave the signature envelope empty.
	ingestSigner, err := registrySignerFor(cfg.signMode)
	if err != nil {
		return fmt.Errorf("registry signing key: %w", err)
	}
	if ingestSigner != nil {
		log.Printf("ingest signing: registry-managed key (§4.7.9)")
	}

	// §4.7.2: route ingest embedding through the transactional outbox when the
	// configured vector backend is an external managed service and the store
	// supports the outbox. Computed from config (the backend is opened later),
	// so boot ingest and the §7.3.1 reingest runner enqueue vector_pending
	// rows the drain worker reconciles asynchronously.
	useVectorOutbox := isManagedVectorBackend(cfg.vectorBackend)
	if useVectorOutbox {
		if _, ok := st.(store.VectorOutbox); !ok {
			useVectorOutbox = false
		}
	}

	// §4.6 declarative layers: the registry.yaml `layers:` list seeds an
	// admin-defined layer per entry (lowest precedence first, in config
	// order). Local sources are ingested at boot; git sources are seeded as
	// config rows for the §7.3.1 reingest/webhook path.
	declared, err := bootstrapDeclaredLayers(st, tenantID, cfg, resourcePut, ingestSigner, useVectorOutbox)
	if err != nil {
		return err
	}
	// PODIUM_LAYER_PATH: when set, ingest a filesystem registry at
	// startup. Mirrors server.NewFromFilesystem for the pieces needed
	// for search and load_artifact, and additionally persists a
	// store.LayerConfig per layer so the §7.3.1 layer-management
	// endpoints (GET /v1/layers, POST /v1/layers/reingest,
	// DELETE /v1/layers) see the bootstrap layers. These layers carry no
	// per-layer visibility input, so they take the deployment default
	// (§4.6 / §13.10 / F-4.6.9). They append after the declared layers.
	pathLayers, err := bootstrapLayerPath(st, tenantID, cfg.layerPath, defaultBootstrapVisibility(cfg), len(declared), cfg.allowPerDomain(), resourcePut, publicSensitivityFloor(cfg), ingestSigner, useVectorOutbox, cfg.enforceSandboxProfile, cfg.hostSandboxes)
	if err != nil {
		return err
	}
	bootLayers := append(declared, pathLayers...)
	registry := core.New(st, tenantID, bootLayers)
	// §13.12 / §4.5.5: apply the tenant registry.yaml discovery defaults
	// and the allow_per_domain_overrides gate to load_domain rendering.
	registry = registry.WithDiscoveryDefaults(cfg.discoveryDefaults(), cfg.allowPerDomain())
	// §3.3 / §12 learn-from-usage: record meta-tool accesses so
	// search_artifacts and load_domain rerank by access frequency. The signal
	// is in-process advisory ordering; a restart relearns it from live traffic.
	registry = registry.WithUsageSignals(core.NewMemoryUsageSignals())

	// §13.8 Prometheus instrumentation: build the metric set once and thread it
	// through the request, cache, visibility, and ingest seams. Nil when
	// disabled (PODIUM_METRICS=false), in which case every recording site
	// no-ops and /metrics is not mounted.
	var mreg *metrics.Registry
	if metricsEnabled() {
		mreg = metrics.New()
		registry = registry.WithCacheObserver(mreg.ObserveCache)
	}
	// vecProvider / embedProvider are captured beyond the open block so the
	// §4.7.2 outbox drain worker (started later) can embed and write pending
	// rows to the external backend.
	var vecProvider vector.Provider
	var embedProvider embedding.Provider
	if v, e, err := openVectorAndEmbedder(cfg); err != nil {
		log.Printf("warning: vector search disabled: %v", err)
	} else if v != nil {
		vecProvider, embedProvider = v, e
		registry = registry.WithVectorSearch(v, e)
		if e != nil {
			log.Printf("hybrid search: vector=%s embedder=%s dim=%d", v.ID(), e.ID(), e.Dimensions())
		} else {
			// §13.12 (F-13.12.6): the backend embeds text server-side, so no
			// separate embedding provider is wired.
			log.Printf("hybrid search: vector=%s self-embedding=%s", v.ID(), cfg.vectorInferenceModel)
		}
	}

	// §9 NotificationProvider: chosen via PODIUM_NOTIFICATION_PROVIDER
	// (one of "noop", "log", "webhook", or "multi"). Wraps the
	// notifier in core.NotificationFunc so the registry can fire
	// operational notifications without depending on this package.
	if np := openNotifier(); np != nil {
		registry = registry.WithNotifier(adaptNotifier(np))
		log.Printf("notification provider: %s", np.ID())
	}

	// §6.3.1 SCIM 2.0: when at least one bearer token is configured,
	// the SCIM IdP receiver is mounted at /scim/v2/. When
	// PODIUM_SCIM_STORE_PATH is set, IdP-pushed users + groups
	// persist as a JSON file at that path so they survive server
	// restarts. The same store feeds the §4.6 visibility evaluator's
	// `groups:` expander so layer filters resolve against
	// IdP-pushed group membership.
	var scimStore scim.Store = scim.NewMemory()
	if path := os.Getenv("PODIUM_SCIM_STORE_PATH"); path != "" {
		fs, err := scim.LoadFileStore(path)
		if err != nil {
			log.Printf("warning: SCIM persistence disabled: %v", err)
		} else {
			scimStore = fs
			log.Printf("SCIM directory persisted at %s", path)
		}
	}
	scimHandler := buildSCIMHandler(scimStore)
	if scimHandler != nil {
		registry = registry.WithGroupResolver(func(g string) []string {
			members, err := scimStore.MembersOf(context.Background(), g)
			if err != nil {
				return nil
			}
			return members
		})
	}

	mode := server.NewModeTracker()
	// §7.3.2 outbound webhook worker: when PODIUM_WEBHOOK_STORE_PATH
	// is set, receivers persist as a JSON file at that path; the
	// store reloads them on startup. Without the env var, receivers
	// stay in memory (subscriptions vanish on restart).
	var webhookStore webhook.Store = webhook.NewMemoryStore()
	if path := os.Getenv("PODIUM_WEBHOOK_STORE_PATH"); path != "" {
		fs, err := webhook.LoadFileStore(path)
		if err != nil {
			log.Printf("warning: webhook persistence disabled: %v", err)
		} else {
			webhookStore = fs
			log.Printf("webhook receivers persisted at %s", path)
		}
	}
	webhookWorker := &webhook.Worker{Store: webhookStore}

	bootOpts := bootstrapOptions(cfg, objStore)
	bootOpts = append(bootOpts, server.WithWebhooks(webhookWorker), server.WithMode(mode))

	// §13.9 /readyz reachability probes, run at request time and
	// bounded by the handler's deadline. The metadata-store probe
	// pings the read path (GetTenant); the object-store probe (when an
	// object store is configured) confirms the backend answers. A
	// failing probe makes /readyz report not_ready (503) so a load
	// balancer pulls the registry out of rotation, distinct from the
	// §13.2.1 read_only replication-fallback state (200, in rotation).
	readyChecks := []server.ReadinessCheck{storeReadinessCheck(st, tenantID)}
	if objStore != nil {
		readyChecks = append(readyChecks, objectStoreReadinessCheck(objStore))
	}
	bootOpts = append(bootOpts, server.WithReadinessChecks(readyChecks...))
	// §13.2.1 replication lag: the Postgres store reports the replica's
	// replay lag; every other backend (and a primary with no replica)
	// reports 0. Threaded into the /readyz body and the
	// X-Podium-Read-Only-Lag-Seconds header.
	if lag := storeLagReporter(st); lag != nil {
		bootOpts = append(bootOpts, server.WithLagReporter(lag))
	}
	if scimHandler != nil {
		bootOpts = append(bootOpts, server.WithSCIM(scimHandler))
		log.Printf("SCIM 2.0 receiver mounted at /scim/v2/")
	}
	// §4.7.8 rate limits per tenant. Zero values disable per
	// dimension; the limiter still mounts so multi-tenant
	// deployments can enable a single dimension at a time.
	quotaLimits := server.QuotaLimits{
		SearchQPS:       cfg.searchQPSLimit,
		MaterializeRate: cfg.materializeRateLimit,
	}
	if quotaLimits.SearchQPS > 0 || quotaLimits.MaterializeRate > 0 {
		log.Printf("rate limits: search_qps=%d materialize_rate=%d",
			quotaLimits.SearchQPS, quotaLimits.MaterializeRate)
	}
	bootOpts = append(bootOpts, server.WithQuotaLimiter(server.NewQuotaLimiter(quotaLimits)))

	// §7.1 latency SLO surface and §13.8 request metrics share the single
	// per-request LatencyObserver seam: the access log emits a structured line
	// keyed by operation name, and the metrics recorder feeds
	// podium_request_total / podium_request_duration_seconds. The access log is
	// on by default (PODIUM_ACCESS_LOG=false silences it); the metrics recorder
	// is present when PODIUM_METRICS is not disabled. With both off, no observer
	// is installed and the server adds zero per-request overhead.
	var latencyObs server.LatencyObserver
	if accessLogEnabled() {
		latencyObs = accessLogObserver()
		log.Printf("access log: enabled (per-request latency keyed by operation; §7.1 SLO surface)")
	}
	if mreg != nil {
		latencyObs = combineObservers(latencyObs, metricsLatencyObserver(mreg))
	}
	if latencyObs != nil {
		bootOpts = append(bootOpts, server.WithLatencyObserver(latencyObs))
	}

	// §6.3.2 runtime trust keys: when PODIUM_RUNTIME_KEYS_PATH is set,
	// registrations persist as a JSON file at that path; the registry
	// reloads them on startup. Without the env var, the registry stays in
	// memory (registrations vanish on restart). The same store is consulted
	// by the request-time JWT verifier and by the admin register/list
	// endpoint, so a key registered over HTTP is immediately trusted.
	var runtimeKeys identity.RuntimeKeyVerifierStore = identity.NewRuntimeKeyRegistry()
	if path := os.Getenv("PODIUM_RUNTIME_KEYS_PATH"); path != "" {
		persisted, err := identity.LoadFilePersistedRuntimeKeyRegistry(path)
		if err != nil {
			log.Printf("warning: runtime key persistence disabled: %v", err)
		} else {
			runtimeKeys = persisted
			log.Printf("runtime trust keys persisted at %s", path)
		}
	}

	// §6.3.2 injected-session-token: install the per-request verifier so the
	// registry verifies the signed JWT on every meta-tool call, mapping the
	// verified claims to the caller Identity and rejecting an unregistered
	// or unsigned token with auth.untrusted_runtime. Without this the server
	// would treat every caller as anonymous-public, which defeats the trust
	// model the section specifies. Only the injected-session-token provider
	// installs it; oauth-device-code and standalone stay on the anonymous
	// resolver.
	// §9.1/§9.2 IdentityProvider selection: consult the process-global
	// identity.Default registry so a custom provider imported into a source
	// build (via identity.Default.Register, mirroring typeprovider) is
	// selected by its PODIUM_IDENTITY_PROVIDER id. The built-in
	// oauth-device-code and injected-session-token providers are seeded into
	// the registry; injected-session-token additionally installs the
	// request-time JWT verifier so the registry verifies every meta-tool
	// call. Server-side identity modes that are not MCP-server providers (the
	// empty standalone default, "oidc", public mode) are absent from the
	// registry and stay on the anonymous resolver.
	verifierInstalled := false
	providerSelected := false
	// layerVerify is the request-time verifier the §7.3.1 layer endpoint uses
	// to attribute user-defined layers and gate admin operations. It is the
	// same verifier installed on the meta-tool server; nil leaves the layer
	// endpoint on the anonymous resolver.
	var layerVerify func(*http.Request) (layer.Identity, error)
	if prov, err := selectIdentityProvider(cfg); err != nil {
		return fmt.Errorf("identity provider %q: %w", cfg.identityProvider, err)
	} else if prov != nil {
		providerSelected = true
		log.Printf("identity provider: %s (registered via identity.Default)", prov.ID())
		if cfg.identityProvider == "injected-session-token" {
			// §6.3.2: the verifier validates aud against this registry's
			// endpoint on every call; refuse to start without it rather than
			// accept tokens whose audience goes unchecked.
			if err := injectedTokenAudienceGuard(cfg.identityProvider, cfg.oauthAudience); err != nil {
				return err
			}
			layerVerify = injectedTokenVerifier(runtimeKeys, cfg.oauthAudience, cfg.idpGroupMapping)
			bootOpts = append(bootOpts, server.WithIdentityVerifier(layerVerify))
			verifierInstalled = true
			log.Printf("identity provider: injected-session-token (verifying runtime-signed JWTs)")
		}
	}

	if err := identityVisibilityGuard(cfg.identityProvider, providerSelected, cfg.publicMode, verifierInstalled); err != nil {
		return err
	}

	srv := server.New(registry, bootOpts...)

	// §7.3.1 layer-management endpoint: mounted alongside the meta-
	// tools so admin operators can register/list/unregister layers
	// over HTTP. The endpoint shares the ModeTracker with the
	// read-only probe so config writes refuse during outage.
	// §4.7.2 — mutating admin-defined layers (register, update,
	// unregister, reorder, visibility edits) requires an authenticated
	// tenant admin. A standalone deployment configures no identity
	// provider, so it has no authenticated callers and the local operator
	// is the de facto admin (§13.10/§13.11), mirroring the §4.6 visibility
	// bypass. With an identity provider configured, AdminAuthorize gates
	// the operation; the anonymous caller is denied, which closes the
	// unauthenticated layer-management surface the standard deployment
	// would otherwise expose.
	// §4.6/§7.3.1: the layer endpoint resolves the caller itself (it is
	// mounted outside the meta-tool identity middleware) so it can attribute
	// a user-defined layer to its authenticated registrant and gate
	// admin-defined operations on the real identity rather than a hardcoded
	// anonymous caller.
	layerIdentity := layerIdentityResolver(layerVerify)
	layers := server.NewLayerEndpoint(st, tenantID, mode).
		WithDefaultVisibility(cfg.defaultLayerVisibility).
		WithMaxUserLayers(cfg.maxUserLayers).
		WithPublicBaseURL(cfg.publicURL).
		WithIdentityResolver(layerIdentity).
		WithAdminAuth(func(r *http.Request) error {
			// §13.10/§13.11: a standalone deployment configures no identity
			// provider, so the local operator is the de facto admin; public
			// mode authenticates no one. Otherwise gate on the resolved
			// attested identity via the §4.7.2 admin grant table.
			if cfg.publicMode || cfg.identityProvider == "" {
				return nil
			}
			return registry.AdminAuthorize(r.Context(), layerIdentity(r))
		})

	runtimeEndpoint := server.NewRuntimeKeyEndpoint(runtimeKeys, mode)

	mux := http.NewServeMux()
	mux.Handle("/v1/layers", layers.Handler())
	mux.Handle("/v1/layers/", layers.Handler())
	// §7.3.1 inbound Git-provider webhook trigger. Mounted at the
	// per-layer path `podium layer register` advertises.
	mux.Handle("/v1/ingest/webhook/", layers.WebhookHandler())
	// §8.5 GDPR right-to-erasure: purges the user's owned layers and redacts
	// the registry audit stream. Backed by the same store + audit sink as the
	// layer endpoint.
	mux.Handle("/v1/admin/erase", layers.EraseHandler())
	mux.Handle("/v1/admin/runtime", runtimeEndpoint.Handler())
	if cfg.webUI {
		mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(web.Assets()))))
		log.Printf("web UI mounted at /ui/")
	}
	if mreg != nil {
		// §13.8 Prometheus scrape endpoint. operationName("/metrics") is "", so
		// the latency observer does not record scrapes against any meta-tool.
		mux.Handle("/metrics", mreg.Handler())
		log.Printf("metrics: Prometheus endpoint mounted at /metrics (§13.8)")
	}
	mux.Handle("/", srv.Handler())

	// §8.3 audit sink: file-backed, hash-chained, shared by the
	// anchor scheduler, the retention scheduler, the read-only
	// probe transition events, and the §8.1 meta-tool emission
	// hook on the registry. Nil when the path can't be resolved
	// (probes still log; downstream features that need the sink
	// gracefully no-op).
	// auditSink is the §8.3 registry sink every event is emitted through
	// (a file sink, or an EndpointSink when PODIUM_AUDIT_LOG_PATH names a
	// SIEM endpoint, F-8.3.1). auditFile is the same sink in its file form,
	// non-nil only for the file case; the §8.6 anchor/verify, §8.4 retention,
	// and §8.5 erasure paths rewrite the on-disk chain and run only against
	// it.
	auditSink, auditFile := openAuditSink(cfg)
	// §8.2 default-on query-text scrubbing: build the scrubber from the
	// resolved PIIRedactionConfig (env PODIUM_PII_REDACTION + registry.yaml
	// pii_redaction). A nil scrubber means an operator disabled it. Resolved
	// here unconditionally so the reingest runner's audit emitter shares it.
	scrubber, err := cfg.piiRedaction.BuildScrubber()
	if err != nil {
		return fmt.Errorf("pii redaction config: %w", err)
	}
	// §8.4 optional sampling for high-volume low-sensitivity events
	// (e.g. domain.loaded at 10%). Built from PODIUM_AUDIT_SAMPLE_RATES;
	// nil when unset, in which case every event is kept.
	auditSampler := audit.NewSampler(cfg.auditSampleRates)
	// §8.1/§8.3/F-8.4.7: when a sink is configured this FileSink is the
	// registry's own sink for catalogue events (the metadata store persists no
	// audit stream), so the §8.4 retention scheduler below, which enforces
	// against this same sink, ages out registry-owned catalogue events.
	var baseEmitter core.AuditEmitter
	if auditSink != nil {
		baseEmitter = auditEmitterFor(auditSink, scrubber, auditSampler)
	}
	// §4.7.8 audit-volume quota: count every emitted audit event against the
	// tenant's daily budget so the §7.3.1 reingest path can refuse new writes
	// once it is spent. The recorder is added only when enforcement is on.
	auditMeter := server.NewAuditVolumeMeter(cfg.auditVolumePerDay)
	emitter := baseEmitter
	if cfg.auditVolumePerDay > 0 {
		emitter = auditVolumeEmitter(auditMeter, tenantID, emitter)
	}
	// §13.8: the visibility-denial metric is driven from the registry audit
	// stream, so wrap the emitter (which may be nil) when metrics are on. This
	// installs an emitter even with no sink, so the counter still moves.
	switch {
	case mreg != nil:
		registry = registry.WithAudit(metricsAuditEmitter(mreg, emitter))
	case emitter != nil:
		registry = registry.WithAudit(emitter)
	}
	if auditSink != nil {
		// spec §8.1: the same §8.3 sink records the HTTP-boundary events —
		// admin.granted (grants handler) and layer.config_changed /
		// layer.user_registered (layer endpoint) — so every audit stream
		// shares one §8.6 hash chain.
		server.WithAudit(auditSink)(srv)
		layers.WithAudit(auditSink)
		// §8.5 erasure rewrites the on-disk chain in place, so the layer
		// endpoint also needs the file form of the sink; nil when redirected
		// to an endpoint (F-8.3.1).
		layers.WithEraseSink(auditFile)
	}

	// §7.3.1: wire the ingest-pipeline driver so the manual reingest and
	// inbound-webhook triggers run the pipeline (resolve source provider,
	// lint, hash, store, publish events) instead of only recording intent.
	// It carries the §4.7.2 freeze windows so an active window blocks ingest
	// unless the manual reingest passes break-glass.
	layers.WithReingestRunner(buildReingestRunner(st, srv, cfg, resourcePut, auditSink, scrubber, ingestSigner, mreg, auditMeter, tenantID, useVectorOutbox))

	// §4.7.2: when ingest routes embedding through the transactional outbox
	// (external vector backend), start the drain worker that embeds and writes
	// pending rows to the backend, publishes the outbox-depth gauge, and emits
	// vector.outbox_lagging when the backend falls behind.
	if useVectorOutbox {
		startVectorOutboxWorker(cfg, st, vecProvider, embedProvider, mreg, auditSink, tenantID)
	}

	// §8.6 transparency anchoring: when the operator enables
	// PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS, a goroutine periodically
	// anchors new entries via the registry-managed signing key.
	// Operators monitor audit.anchored / audit.anchor_failed events.
	var reAnchor func()
	if cfg.auditAnchorInterval > 0 {
		if signer := startAnchorScheduler(cfg, auditFile); signer != nil {
			// §8.6/F-8.4.8: after a retention pass drops events the chain
			// head moves, invalidating the last anchor. Re-anchor the new
			// head immediately so verifiers do not wait for the next tick.
			reAnchor = func() {
				if _, err := audit.Anchor(context.Background(), auditFile, signer); err != nil {
					log.Printf("audit re-anchor after retention failed: %v", err)
				}
			}
		}
	}

	// §8.6 audit-integrity verification: a goroutine re-verifies the hash
	// chain on a cadence and records an audit.gap_detected event (plus an
	// operator alert) on any break, so an out-of-band edit that breaks the
	// chain is detected and surfaced to SIEM at runtime. Enabled by default
	// (PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS defaults to one hour); set the
	// interval to 0 to disable.
	if cfg.auditVerifyInterval > 0 {
		startVerifyScheduler(cfg, auditFile)
	}

	// §8.4 audit-event retention: a goroutine truncates the audit log on
	// a cadence using the configured retention policies (the §8.4 1-year
	// default for event metadata, plus the §8.4 query-text window).
	// Enabled by default (PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS defaults
	// to one day); set the interval to 0 to disable.
	if cfg.auditRetentionInterval > 0 {
		startRetentionScheduler(cfg, auditFile, reAnchor)
	}

	// §8.4 store retention: when PODIUM_STORE_RETENTION_INTERVAL_SECONDS
	// > 0, a goroutine purges deprecated artifact versions past the
	// 90-day window and hard-deletes soft-deleted layers past the 30-day
	// recovery window.
	if cfg.storeRetentionInterval > 0 {
		startStoreRetentionScheduler(cfg, st)
	}

	// §13.2.1 read-only probe: ping the metadata store on a tick
	// and flip the shared mode tracker after Failures consecutive
	// errors. Disabled when failures threshold is 0. Mode
	// transitions write registry.read_only_entered /
	// registry.read_only_exited events to the audit sink.
	if cfg.readOnlyProbeFailures > 0 {
		auditEnter := readOnlyEnterCallback(auditSink, tenantID, "store_probe_failed")
		auditExit := readOnlyExitCallback(auditSink, tenantID)
		probe := &server.ReadOnlyProbe{
			Store:    st,
			Tracker:  mode,
			TenantID: tenantID,
			Interval: time.Duration(cfg.readOnlyProbeInterval) * time.Second,
			Failures: cfg.readOnlyProbeFailures,
			OnEnter: func() {
				log.Printf("registry entered read_only mode after %d probe failures", cfg.readOnlyProbeFailures)
				auditEnter()
			},
			OnExit: func() {
				log.Printf("registry exited read_only mode")
				auditExit()
			},
		}
		go func() {
			if err := probe.Run(context.Background()); err != nil && err != context.Canceled {
				log.Printf("read-only probe stopped: %v", err)
			}
		}()
	}

	// §13.8 OpenTelemetry: install the W3C TraceContext propagator and, when
	// PODIUM_TRACING (or an OTLP endpoint) is configured, the trace exporter.
	// Off by default; the propagator is installed regardless so inbound
	// traceparent extraction works. Wrap the handler with otelhttp so each
	// request opens a server span named by the operation, with the incoming
	// W3C context as its parent. The shutdown flushes the exporter on exit.
	shutdownTracing, terr := tracing.Init(context.Background(), "podium-registry")
	if terr != nil {
		return fmt.Errorf("tracing: %w", terr)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()
	handler := otelhttp.NewHandler(mux, "podium-registry",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if op := server.OperationName(r.URL.Path); op != "" {
				return op
			}
			return r.Method + " " + r.URL.Path
		}),
	)

	httpServer := &http.Server{
		Addr:              cfg.bind,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// spec: §13.2.2 / §13.10 — when public mode is engaged, emit the
	// documented warning banner to stderr at startup so operators see that
	// authentication is skipped and that a non-loopback bind requires
	// --allow-public-bind.
	emitStartupBanner(os.Stderr, cfg.publicMode)
	log.Printf("podium-server listening on %s (mode=%s)", cfg.bind, cfg.modeBanner())
	return httpServer.ListenAndServe()
}

// emitStartupBanner writes the §13.2.2 / §13.10 public-mode warning to w
// when public mode is engaged. The text is copied verbatim from §13.10
// (including the ⚠ glyph and indentation) so the startup banner matches
// the documented contract. When public mode is off it writes nothing.
func emitStartupBanner(w io.Writer, publicMode bool) {
	if !publicMode {
		return
	}
	fmt.Fprintln(w, "⚠  PUBLIC MODE: all artifacts visible to all callers without authentication.")
	fmt.Fprintln(w, "   Bound to 127.0.0.1 by default; pass --allow-public-bind to bind a non-loopback address.")
}

type Config struct {
	bind       string
	publicMode bool
	// allowPublicBind is the §13.10 escape hatch (--allow-public-bind /
	// PODIUM_ALLOW_PUBLIC_BIND). Public mode refuses a non-loopback bind
	// unless this is set.
	allowPublicBind bool
	// webUI mounts the §13.10 single-page web UI at /ui/ (--web-ui /
	// PODIUM_WEB_UI). webUIAllowPublicBind (--web-ui-allow-public-bind /
	// PODIUM_WEB_UI_ALLOW_PUBLIC_BIND) is the escape hatch that, together with
	// a configured identity provider, lets the UI bind a non-loopback address.
	webUI                bool
	webUIAllowPublicBind bool
	// signMode is the §13.10 ingest-signing selection (--sign /
	// PODIUM_SIGN). Standalone signing is disabled by default; the only
	// accepted value is "registry-key", which signs every accepted manifest
	// with a registry-managed Ed25519 key (§4.7.9).
	signMode         string
	identityProvider string
	// oauthAudience is the §6.3.2 `aud` claim the injected-session-token
	// verifier requires (the registry endpoint). Empty disables audience
	// checking. Sourced from PODIUM_OAUTH_AUDIENCE.
	oauthAudience string
	// oauthAuthorizationEndpoint is the §6.3 / §13.12 identity-provider
	// authorization endpoint (PODIUM_OAUTH_AUTHORIZATION_ENDPOINT or the
	// registry.yaml identity_provider.authorization_endpoint). Surfaced via
	// `config show`; the device-code clients consume the same value.
	oauthAuthorizationEndpoint string
	// idpGroupMapping is the §6.3.1 IdpGroupMapping adapter: a registry-
	// side table rewriting OIDC group-claim values to layer group names
	// for IdPs without SCIM. Nil or empty passes groups through unchanged.
	// Sourced from PODIUM_IDP_GROUP_MAPPING.
	idpGroupMapping *identity.IdpGroupMapping
	storeType       string
	sqlitePath      string
	postgresDSN     string
	objectStore     string
	filesystemRoot  string
	publicURL       string
	presignTTL      time.Duration
	s3Endpoint      string
	s3Region        string
	s3Bucket        string
	s3AccessKey     string
	s3SecretKey     string
	// s3ForcePathStyle maps to §13.12 PODIUM_S3_FORCE_PATH_STYLE. TLS is
	// derived from the PODIUM_S3_ENDPOINT URL scheme (§13.12 documents the
	// endpoint as a URL), so there is no separate use-SSL knob.
	s3ForcePathStyle bool
	// Vector + embedding (§4.7).
	vectorBackend     string
	embeddingProvider string
	// embeddingProviderExplicit records whether the operator explicitly chose
	// an EmbeddingProvider (PODIUM_EMBEDDING_PROVIDER set, or registry.yaml
	// embedding.type) as opposed to inheriting the per-mode default. §9.1 / §4.7
	// (F-9.1.1): a self-embedding backend honors an explicit provider as an
	// override of its hosted model, but a defaulted provider is not an override.
	embeddingProviderExplicit bool
	embeddingModel            string
	openaiAPIKey              string
	// openaiBaseURL / openaiOrg mirror §13.12 PODIUM_OPENAI_BASE_URL /
	// PODIUM_OPENAI_ORG (F-13.12.1). Sourced from env or registry.yaml's
	// embedding_provider.base_url / org and passed to the openai embedder.
	openaiBaseURL string
	openaiOrg     string
	voyageAPIKey  string
	cohereAPIKey  string
	ollamaURL     string
	pgvectorDSN   string
	pineconeKey   string
	pineconeHost  string
	pineconeIndex string
	pineconeNS    string
	// pineconeControlPlane overrides the Pinecone control-plane base URL used
	// to auto-resolve PODIUM_PINECONE_HOST from the index name (§13.12,
	// F-13.12.3). Empty uses the real control plane (https://api.pinecone.io);
	// it exists for tests and private Pinecone deployments. Sourced from
	// PODIUM_PINECONE_CONTROL_PLANE.
	pineconeControlPlane string
	weaviateURL          string
	weaviateKey          string
	weaviateColl         string
	qdrantURL            string
	qdrantKey            string
	qdrantColl           string
	// vectorInferenceModel captures the §13.12 self-embedding model name
	// (PODIUM_PINECONE_INFERENCE_MODEL / PODIUM_WEAVIATE_VECTORIZER /
	// PODIUM_QDRANT_INFERENCE_MODEL, or vector_backend.inference_model). The
	// self-embedding wiring itself is tracked separately (F-13.12.6); the
	// value is parsed and surfaced so a config-file deployment is not silently
	// dropped.
	vectorInferenceModel string
	// §4.6 default visibility for newly-registered layers when no
	// explicit visibility is supplied. One of "public" |
	// "organization" | "private". Defaults to "private" so
	// admin-defined layers don't leak by accident.
	defaultLayerVisibility string
	// §7.3.1 cap on user-defined layers per identity. Zero applies the
	// server.DefaultMaxUserLayers default (3); a negative value disables
	// the cap.
	maxUserLayers int
	// §13.10 sandbox-profile ingest gate. enforceSandboxProfile is set by
	// PODIUM_ENFORCE_SANDBOX_PROFILE=true; when true the registry refuses to
	// ingest an artifact whose sandbox_profile cannot be honored by the local
	// host. hostSandboxes is the locally-enforceable set (PODIUM_HOST_SANDBOXES,
	// default unrestricted). Off by default so standalone treats
	// sandbox_profile as informational.
	enforceSandboxProfile bool
	hostSandboxes         []string
	// §13.2.1 read-only mode probe.
	readOnlyProbeFailures int
	readOnlyProbeInterval int
	// §8.6 transparency anchoring.
	auditLogPath        string
	auditSigningKeyPath string
	auditAnchorInterval int
	// §8.6 audit-integrity verification. auditVerifyInterval > 0 enables a
	// goroutine that re-verifies the hash chain on a cadence and records an
	// audit.gap_detected event on any break ("Detection of gaps is
	// automated and alerted").
	auditVerifyInterval int
	// §8.4 audit-event retention enforcement.
	auditRetentionInterval   int
	auditRetentionMaxAgeDays int
	// §8.4 optional per-event-type sampling keep-rates (e.g.
	// domain.loaded -> 0.1). Empty disables sampling (every event kept).
	auditSampleRates map[audit.EventType]float64
	// §8.4 store retention sweeps. storeRetentionInterval > 0 enables a
	// goroutine that purges deprecated artifact versions and expired
	// soft-deleted layers on a cadence; it defaults to one day so the
	// windows are enforced out of the box (set the interval to 0 to
	// disable). The day windows default to the §8.4 table (90 days for
	// deprecated versions, 30 days for the owner-unregistered-layer recovery
	// window).
	storeRetentionInterval  int
	deprecatedRetentionDays int
	layerRecoveryDays       int
	// §8.2 query-text PII scrub config. Default-on (Enabled nil); sourced
	// from PODIUM_PII_REDACTION and registry.yaml's pii_redaction block.
	piiRedaction audit.PIIRedactionConfig
	// §4.7.8 rate limits.
	searchQPSLimit       int
	materializeRateLimit int
	// §4.7.8 audit-volume quota: the per-tenant maximum number of audit
	// events per UTC day. When exceeded, new auditable writes (reingest /
	// inbound webhook) are refused with quota.audit_volume_exceeded. Zero
	// disables enforcement.
	auditVolumePerDay int64
	// §4.7.2 vector-outbox drain worker tuning (external backends only).
	vectorOutboxInterval int // poll interval seconds (default 5)
	vectorOutboxBatch    int // rows drained per pass (default 50)
	vectorOutboxLagDepth int // depth that trips vector.outbox_lagging (default 100)
	vectorOutboxLagAge   int // oldest-row age seconds that trips it (default 300)
	// §13.10 standalone bootstrap layer path. When non-empty,
	// Run() opens the filesystem registry at the path, ingests
	// every resolved layer, and persists a store.LayerConfig per
	// layer so the §7.3.1 layer endpoints see them.
	layerPath string
	// §13.1.1 evaluation-stack bootstrap admins. Comma- or
	// space-separated user IDs seeded as tenant admins at boot, so the
	// docker-compose evaluation stack arrives at a state where an admin
	// exists (the "creates the first tenant and admin user, configurable
	// via env vars" responsibility of the bootstrap step). Sourced from
	// PODIUM_BOOTSTRAP_ADMINS. Idempotent: re-seeding an existing grant is
	// a no-op.
	bootstrapAdmins []string
	// §4.6 declarative admin-defined layer list from registry.yaml's
	// `layers:` block. Each entry is seeded as a store.LayerConfig at
	// startup; local sources are also ingested. Config-file-only.
	declaredLayers []yamlLayerEntry
	// §4.7.2 org-level freeze windows from registry.yaml's `freeze_windows:`
	// block. Enforced on the §7.3.1 manual reingest and inbound-webhook
	// ingest paths; an active window rejects with ingest.frozen unless the
	// manual reingest passes break-glass. Config-file-only.
	freezeWindows []ingest.FreezeWindow
	// §13.12 / §4.5.5 tenant-scope discovery rendering defaults from
	// registry.yaml's `discovery:` block. Applied to the registry as
	// core.DiscoveryDefaults; config-file-only.
	discovery yamlDiscovery
	// §4.5.5 allow_per_domain_overrides gate. nil leaves the default
	// (true); a tenant sets it false to disable per-domain DOMAIN.md
	// discovery overrides registry-wide and have lint warn on them.
	allowPerDomainOverrides *bool
	// §3.5 expose_scope_preview tenant gate. nil leaves the default
	// (true); a tenant sets it false so GET /v1/scope/preview answers
	// 403 config.scope_preview_disabled. Sourced from PODIUM_EXPOSE_SCOPE_PREVIEW
	// or registry.yaml's tenant.expose_scope_preview.
	exposeScopePreview *bool
}

// discoveryDefaults converts the parsed registry.yaml discovery block
// into the core tenant-default knobs.
func (c *Config) discoveryDefaults() core.DiscoveryDefaults {
	return core.DiscoveryDefaults{
		MaxDepth:              c.discovery.MaxDepth,
		NotableCount:          c.discovery.NotableCount,
		FoldBelowArtifacts:    c.discovery.FoldBelowArtifacts,
		TargetResponseTokens:  c.discovery.TargetResponseTokens,
		FoldPassthroughChains: c.discovery.FoldPassthroughChains,
	}
}

// allowPerDomain resolves the §4.5.5 allow_per_domain_overrides gate,
// defaulting to true when the tenant did not set it.
func (c *Config) allowPerDomain() bool {
	return c.allowPerDomainOverrides == nil || *c.allowPerDomainOverrides
}

// Setting names one resolved field together with the env var (or
// fallback) it came from. `podium config show` consumes this.
type Setting struct {
	Name   string
	Value  string
	Source string
}

// Settings returns a deterministic view of the resolved
// configuration with the source of each value (env var, yaml
// file, or hardcoded default). Secrets are redacted.
func (c *Config) Settings() []Setting {
	const yamlSrc = "registry.yaml"
	const defaultSrc = "default"
	envOrSrc := func(env, src string) string {
		if os.Getenv(env) != "" {
			return env
		}
		return src
	}
	// envFirstOrSrc reports the first set env var among keys (for a value that
	// has more than one env source), else the fallback source.
	envFirstOrSrc := func(src string, keys ...string) string {
		for _, k := range keys {
			if os.Getenv(k) != "" {
				return k
			}
		}
		return src
	}
	redact := func(s string) string {
		if s == "" {
			return ""
		}
		return "<redacted>"
	}
	return []Setting{
		{"bind", c.bind, envOrSrc("PODIUM_BIND", defaultSrc)},
		{"public_mode", boolStr(c.publicMode), envOrSrc("PODIUM_PUBLIC_MODE", defaultSrc)},
		{"allow_public_bind", boolStr(c.allowPublicBind), envOrSrc("PODIUM_ALLOW_PUBLIC_BIND", defaultSrc)},
		{"identity_provider", c.identityProvider, envOrSrc("PODIUM_IDENTITY_PROVIDER", yamlSrc)},
		{"oauth_audience", c.oauthAudience, envOrSrc("PODIUM_OAUTH_AUDIENCE", defaultSrc)},
		{"identity_provider.authorization_endpoint", c.oauthAuthorizationEndpoint, envOrSrc("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", yamlSrc)},
		{"idp_group_mapping", idpGroupMappingStr(c.idpGroupMapping), envOrSrc("PODIUM_IDP_GROUP_MAPPING", defaultSrc)},
		{"store.type", c.storeType, envOrSrc("PODIUM_REGISTRY_STORE", defaultSrc)},
		{"store.sqlite_path", c.sqlitePath, envOrSrc("PODIUM_SQLITE_PATH", defaultSrc)},
		{"store.postgres_dsn", redact(c.postgresDSN), envOrSrc("PODIUM_POSTGRES_DSN", yamlSrc)},
		{"object_store.type", c.objectStore, envOrSrc("PODIUM_OBJECT_STORE", defaultSrc)},
		{"object_store.filesystem_root", c.filesystemRoot, envOrSrc("PODIUM_FILESYSTEM_ROOT", defaultSrc)},
		{"object_store.s3_endpoint", c.s3Endpoint, envOrSrc("PODIUM_S3_ENDPOINT", yamlSrc)},
		{"object_store.s3_bucket", c.s3Bucket, envOrSrc("PODIUM_S3_BUCKET", yamlSrc)},
		{"object_store.s3_region", c.s3Region, envOrSrc("PODIUM_S3_REGION", defaultSrc)},
		{"object_store.s3_force_path_style", boolStr(c.s3ForcePathStyle), envOrSrc("PODIUM_S3_FORCE_PATH_STYLE", defaultSrc)},
		{"vector_backend", c.vectorBackend, envOrSrc("PODIUM_VECTOR_BACKEND", yamlSrc)},
		{"vector_backend.index", c.pineconeIndex, envOrSrc("PODIUM_PINECONE_INDEX", yamlSrc)},
		{"vector_backend.namespace", c.pineconeNS, envOrSrc("PODIUM_PINECONE_NAMESPACE", yamlSrc)},
		{"vector_backend.inference_model", c.vectorInferenceModel, envFirstOrSrc(yamlSrc, "PODIUM_PINECONE_INFERENCE_MODEL", "PODIUM_WEAVIATE_VECTORIZER", "PODIUM_QDRANT_INFERENCE_MODEL")},
		{"embedding_provider", c.embeddingProvider, envOrSrc("PODIUM_EMBEDDING_PROVIDER", yamlSrc)},
		{"embedding_model", c.embeddingModel, envOrSrc("PODIUM_EMBEDDING_MODEL", yamlSrc)},
		{"layers.default_visibility", c.defaultLayerVisibility, envOrSrc("PODIUM_DEFAULT_LAYER_VISIBILITY", defaultSrc)},
		{"layers.max_user_layers", intStr(c.maxUserLayers), envOrSrc("PODIUM_MAX_USER_LAYERS", defaultSrc)},
		{"sandbox_profile.enforced", boolStr(c.enforceSandboxProfile), envOrSrc("PODIUM_ENFORCE_SANDBOX_PROFILE", defaultSrc)},
		{"sandbox_profile.host_sandboxes", strings.Join(c.hostSandboxes, ","), envOrSrc("PODIUM_HOST_SANDBOXES", defaultSrc)},
		{"layers.path", c.layerPath, envOrSrc("PODIUM_LAYER_PATH", yamlSrc)},
		{"tenant.expose_scope_preview", boolStr(c.exposeScopePreview == nil || *c.exposeScopePreview), envOrSrc("PODIUM_EXPOSE_SCOPE_PREVIEW", yamlSrc)},
		{"read_only.probe_failures", intStr(c.readOnlyProbeFailures), envOrSrc("PODIUM_READONLY_PROBE_FAILURES", defaultSrc)},
		{"read_only.probe_interval_seconds", intStr(c.readOnlyProbeInterval), envOrSrc("PODIUM_READONLY_PROBE_INTERVAL", defaultSrc)},
		{"openai_api_key", redact(c.openaiAPIKey), envOrSrc("OPENAI_API_KEY", "")},
		{"voyage_api_key", redact(c.voyageAPIKey), envOrSrc("VOYAGE_API_KEY", "")},
		{"cohere_api_key", redact(c.cohereAPIKey), envOrSrc("COHERE_API_KEY", "")},
		{"ollama_url", c.ollamaURL, envOrSrc("PODIUM_OLLAMA_URL", defaultSrc)},
		{"pii_redaction.enabled", boolStr(c.piiRedaction.Active()), envOrSrc("PODIUM_PII_REDACTION", yamlSrc)},
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func intStr(n int) string {
	return strconv.Itoa(n)
}

// idpGroupMappingStr renders the §6.3.1 group-mapping table for `config
// show`: the entry count, or "" when no mapping is configured. The raw
// claim→group pairs are not printed so the output stays compact.
func idpGroupMappingStr(m *identity.IdpGroupMapping) string {
	if m == nil || m.Empty() {
		return ""
	}
	return intStr(m.Len()) + " mappings"
}

func LoadConfig() *Config {
	c := &Config{
		bind:                       envDefault("PODIUM_BIND", "127.0.0.1:8080"),
		publicMode:                 isTrue(os.Getenv("PODIUM_PUBLIC_MODE")),
		allowPublicBind:            isTrue(os.Getenv("PODIUM_ALLOW_PUBLIC_BIND")),
		webUI:                      isTrue(os.Getenv("PODIUM_WEB_UI")),
		webUIAllowPublicBind:       isTrue(os.Getenv("PODIUM_WEB_UI_ALLOW_PUBLIC_BIND")),
		signMode:                   os.Getenv("PODIUM_SIGN"),
		identityProvider:           os.Getenv("PODIUM_IDENTITY_PROVIDER"),
		oauthAudience:              os.Getenv("PODIUM_OAUTH_AUDIENCE"),
		oauthAuthorizationEndpoint: os.Getenv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT"),
		storeType:                  envDefault("PODIUM_REGISTRY_STORE", "sqlite"),
		sqlitePath:                 os.Getenv("PODIUM_SQLITE_PATH"),
		postgresDSN:                os.Getenv("PODIUM_POSTGRES_DSN"),
		objectStore:                envDefault("PODIUM_OBJECT_STORE", "filesystem"),
		filesystemRoot:             os.Getenv("PODIUM_FILESYSTEM_ROOT"),
		publicURL:                  os.Getenv("PODIUM_PUBLIC_URL"),
		s3Endpoint:                 os.Getenv("PODIUM_S3_ENDPOINT"),
		// §13.12 marks PODIUM_S3_REGION required for s3; no implicit default
		// so a missing region is named by validate() (F-13.12.9) rather than
		// silently replaced by us-east-1.
		s3Region:         os.Getenv("PODIUM_S3_REGION"),
		s3Bucket:         os.Getenv("PODIUM_S3_BUCKET"),
		s3AccessKey:      os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		s3SecretKey:      os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		s3ForcePathStyle: isTrue(os.Getenv("PODIUM_S3_FORCE_PATH_STYLE")),
		// §4.7 vector + embedding.
		vectorBackend:     os.Getenv("PODIUM_VECTOR_BACKEND"),
		embeddingProvider: os.Getenv("PODIUM_EMBEDDING_PROVIDER"),
		embeddingModel:    os.Getenv("PODIUM_EMBEDDING_MODEL"),
		openaiAPIKey:      os.Getenv("OPENAI_API_KEY"),
		openaiBaseURL:     os.Getenv("PODIUM_OPENAI_BASE_URL"),
		openaiOrg:         os.Getenv("PODIUM_OPENAI_ORG"),
		voyageAPIKey:      os.Getenv("VOYAGE_API_KEY"),
		cohereAPIKey:      os.Getenv("COHERE_API_KEY"),
		ollamaURL:         envDefault("PODIUM_OLLAMA_URL", "http://localhost:11434"),
		pgvectorDSN:       envFirst("PODIUM_PGVECTOR_DSN", "PODIUM_POSTGRES_DSN"),
		pineconeKey:       os.Getenv("PODIUM_PINECONE_API_KEY"),
		pineconeHost:      os.Getenv("PODIUM_PINECONE_HOST"),
		pineconeIndex:     os.Getenv("PODIUM_PINECONE_INDEX"),
		// §13.12 (F-13.12.3) Pinecone control-plane override (test/private use).
		pineconeControlPlane: os.Getenv("PODIUM_PINECONE_CONTROL_PLANE"),
		// Read raw here; the §13.12 "default" namespace fallback (F-13.12.11)
		// is applied after applyYAML so env > registry.yaml > default holds.
		pineconeNS:  os.Getenv("PODIUM_PINECONE_NAMESPACE"),
		weaviateURL: os.Getenv("PODIUM_WEAVIATE_URL"),
		weaviateKey: os.Getenv("PODIUM_WEAVIATE_API_KEY"),
		// §13.12 marks the collection required for weaviate-cloud/qdrant-cloud;
		// no implicit default so validate() names a missing one (F-13.12.12).
		weaviateColl: os.Getenv("PODIUM_WEAVIATE_COLLECTION"),
		qdrantURL:    os.Getenv("PODIUM_QDRANT_URL"),
		qdrantKey:    os.Getenv("PODIUM_QDRANT_API_KEY"),
		qdrantColl:   os.Getenv("PODIUM_QDRANT_COLLECTION"),
		// §13.12 self-embedding model (parsed/surfaced; wiring is F-13.12.6).
		vectorInferenceModel: envFirst("PODIUM_PINECONE_INFERENCE_MODEL", "PODIUM_WEAVIATE_VECTORIZER", "PODIUM_QDRANT_INFERENCE_MODEL"),
		// §4.6 + §13.2.1. The default visibility is resolved after applyYAML
		// (a standalone deployment defaults to public; see below, F-13.12.15).
		defaultLayerVisibility: os.Getenv("PODIUM_DEFAULT_LAYER_VISIBILITY"),
		// §7.3.1 user-defined-layer cap (0 = default of 3).
		maxUserLayers: envInt("PODIUM_MAX_USER_LAYERS", 0),
		// §13.10 sandbox-profile ingest gate. Off unless flipped to
		// standard-mode behavior; hostSandboxes defaults to unrestricted.
		enforceSandboxProfile: isTrue(os.Getenv("PODIUM_ENFORCE_SANDBOX_PROFILE")),
		hostSandboxes:         splitCSVTrim(envDefault("PODIUM_HOST_SANDBOXES", "unrestricted")),
		// §13.2.1 read-only probe. Sentinel -1 means "unset by env" so the
		// registry.yaml overlay and the spec defaults below can distinguish an
		// absent value from an explicit 0 (which disables the probe). The
		// failure threshold defaults to 3 and the interval to 5 s so the
		// documented automatic fallback runs out of the box.
		readOnlyProbeFailures: envInt("PODIUM_READONLY_PROBE_FAILURES", -1),
		readOnlyProbeInterval: envInt("PODIUM_READONLY_PROBE_INTERVAL", -1),
		// §8.6 audit anchoring.
		auditLogPath:        os.Getenv("PODIUM_AUDIT_LOG_PATH"),
		auditSigningKeyPath: os.Getenv("PODIUM_AUDIT_SIGNING_KEY_PATH"),
		auditAnchorInterval: envInt("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", 0),
		// §8.6 audit-integrity verification. Defaults to one hour so gap
		// detection is automated out of the box; set
		// PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS=0 to disable.
		auditVerifyInterval: envInt("PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS", 3600),
		// §8.4 audit-event retention enforcement. The interval defaults to
		// one day so the §8.4 1-year metadata default applies out of the
		// box; set PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS=0 to disable.
		auditRetentionInterval:   envInt("PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS", 86400),
		auditRetentionMaxAgeDays: envInt("PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS", 365),
		// §8.4 optional sampling, e.g. PODIUM_AUDIT_SAMPLE_RATES="domain.loaded=0.1".
		auditSampleRates: parseAuditSampleRates(os.Getenv("PODIUM_AUDIT_SAMPLE_RATES")),
		// §8.4 store retention sweeps (deprecated-version + layer-recovery
		// purge). The interval defaults to one day so the §8.4 "90 days after
		// the deprecation flag is set" and "30 days" (owner-unregistered layer)
		// windows are enforced out of the box, matching the audit-retention
		// scheduler; set PODIUM_STORE_RETENTION_INTERVAL_SECONDS=0 to disable.
		storeRetentionInterval:  envInt("PODIUM_STORE_RETENTION_INTERVAL_SECONDS", 86400),
		deprecatedRetentionDays: envInt("PODIUM_DEPRECATED_RETENTION_DAYS", 90),
		layerRecoveryDays:       envInt("PODIUM_LAYER_RECOVERY_DAYS", 30),
		// §8.2 query-text scrub: default-on, disabled with PODIUM_PII_REDACTION=false.
		piiRedaction: audit.PIIRedactionConfig{Enabled: envBoolPtr("PODIUM_PII_REDACTION")},
		// §4.7.8 rate limits.
		searchQPSLimit:       envInt("PODIUM_QUOTA_SEARCH_QPS", 0),
		materializeRateLimit: envInt("PODIUM_QUOTA_MATERIALIZE_RATE", 0),
		auditVolumePerDay:    envInt64("PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY", 0),
		vectorOutboxInterval: envInt("PODIUM_VECTOR_OUTBOX_INTERVAL_SECONDS", 5),
		vectorOutboxBatch:    envInt("PODIUM_VECTOR_OUTBOX_BATCH", 50),
		vectorOutboxLagDepth: envInt("PODIUM_VECTOR_OUTBOX_LAG_DEPTH", 100),
		vectorOutboxLagAge:   envInt("PODIUM_VECTOR_OUTBOX_LAG_AGE_SECONDS", 300),
		// §13.10 standalone bootstrap layer path.
		layerPath: os.Getenv("PODIUM_LAYER_PATH"),
		// §13.1.1 evaluation-stack bootstrap admins.
		bootstrapAdmins: parseBootstrapAdmins(os.Getenv("PODIUM_BOOTSTRAP_ADMINS")),
		// §3.5 scope-preview tenant gate (nil = default true).
		exposeScopePreview: envBoolPtr("PODIUM_EXPOSE_SCOPE_PREVIEW"),
	}
	// §13.10 ~/.podium/registry.yaml: load and overlay onto env-
	// derived defaults. Env values keep precedence per applyYAML.
	if y, err := readYAMLConfig(); err != nil {
		log.Printf("warning: ignored registry.yaml: %v", err)
	} else {
		applyYAML(c, y)
	}
	// §13.12 (F-13.12.11): the Pinecone namespace prefix defaults to "default"
	// when neither the env var nor registry.yaml set it.
	if c.pineconeNS == "" {
		c.pineconeNS = "default"
	}
	// §13.2.1 (F-13.2.3): apply the spec defaults once env and registry.yaml
	// have had their say. A negative failure threshold means neither set it,
	// so the documented automatic fallback engages (probe every 5 s, flip
	// after three consecutive failures). An explicit 0 from env or yaml keeps
	// the probe disabled.
	if c.readOnlyProbeFailures < 0 {
		c.readOnlyProbeFailures = 3
	}
	if c.readOnlyProbeInterval <= 0 {
		c.readOnlyProbeInterval = 5
	}
	// §9.1 / §13.10 (F-9.1.5): realize the per-deployment-mode defaults for the
	// RegistrySearchProvider and EmbeddingProvider rows. A zero-config standard
	// deployment defaults to pgvector + openai; a standalone deployment defaults
	// to sqlite-vec + ollama, the same SQLite file holding manifests and
	// vectors. Explicit env / registry.yaml values keep precedence. The operator
	// opts out to BM25-only with PODIUM_NO_EMBEDDINGS=true (the spec's
	// --no-embeddings fallback) or by setting either variable to "none".
	if isTrue(os.Getenv("PODIUM_NO_EMBEDDINGS")) {
		c.vectorBackend, c.embeddingProvider = "none", "none"
	} else {
		standard := c.storeType == "postgres"
		// Apply the per-mode default only when the operator made no explicit
		// choice. An explicitly empty env var (§13.12: "Setting it to the
		// empty string disables embedding generation; search degrades to
		// BM25-only") and a registry.yaml value both count as explicit and
		// keep precedence over the default.
		if _, set := os.LookupEnv("PODIUM_VECTOR_BACKEND"); !set && c.vectorBackend == "" {
			if standard {
				c.vectorBackend = "pgvector"
			} else {
				c.vectorBackend = "sqlite-vec"
			}
		}
		_, embedSet := os.LookupEnv("PODIUM_EMBEDDING_PROVIDER")
		// §9.1 / §4.7 (F-9.1.1): an explicit choice is the env var being set
		// (even to the empty string, which §13.12 reads as "disable embedding
		// generation") or a registry.yaml value (already merged into
		// c.embeddingProvider). Capture it before the per-mode default is
		// applied so a self-embedding backend can distinguish an operator
		// override from an inherited default.
		c.embeddingProviderExplicit = embedSet || c.embeddingProvider != ""
		if !embedSet && c.embeddingProvider == "" {
			if standard {
				c.embeddingProvider = "openai"
			} else {
				c.embeddingProvider = "ollama"
			}
		}
	}
	// §13.10 / §13.12 (F-13.12.15): when no explicit default visibility was
	// supplied (env or registry.yaml), a standalone deployment (no identity
	// provider) defaults endpoint-registered layers to `public`, matching the
	// §13.10 standalone default; once an identity provider gates access, the
	// default is `private` so admin-defined layers do not leak by accident.
	if c.defaultLayerVisibility == "" {
		if c.identityProvider == "" {
			c.defaultLayerVisibility = "public"
		} else {
			c.defaultLayerVisibility = "private"
		}
	}
	// §6.3.1 IdpGroupMapping: parse the registry-side group-mapping table
	// from PODIUM_IDP_GROUP_MAPPING ("oktaGroupOID=finance,..."). A
	// malformed spec is logged and ignored rather than crashing startup;
	// groups then pass through unmapped.
	if spec := os.Getenv("PODIUM_IDP_GROUP_MAPPING"); spec != "" {
		if m, err := identity.ParseIdpGroupMapping(spec); err != nil {
			log.Printf("warning: ignored PODIUM_IDP_GROUP_MAPPING: %v", err)
		} else {
			c.idpGroupMapping = m
		}
	}
	if c.sqlitePath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.sqlitePath = filepath.Join(home, ".podium", "standalone", "podium.db")
		}
	}
	if c.filesystemRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.filesystemRoot = filepath.Join(home, ".podium", "standalone", "objects")
		}
	}
	if v := os.Getenv("PODIUM_PRESIGN_TTL_SECONDS"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			c.presignTTL = time.Duration(secs) * time.Second
		}
	}
	if c.presignTTL <= 0 {
		c.presignTTL = objectstore.DefaultPresignTTL
	}
	if c.publicURL == "" {
		c.publicURL = "http://" + c.bind
	}
	return c
}

func (c *Config) validate() error {
	startup := server.StartupConfig{
		PublicMode:           c.publicMode,
		IdentityProvider:     c.identityProvider,
		Bind:                 c.bind,
		AllowPublicBind:      c.allowPublicBind,
		WebUI:                c.webUI,
		WebUIAllowPublicBind: c.webUIAllowPublicBind,
	}
	if err := startup.Validate(); err != nil {
		return err
	}
	// §13.10 signing: the only accepted --sign / PODIUM_SIGN value is
	// "registry-key"; reject anything else at startup so a typo is named
	// rather than silently leaving signing disabled.
	if c.signMode != "" && c.signMode != "registry-key" {
		return fmt.Errorf("config.invalid_sign_mode: PODIUM_SIGN must be registry-key, got %q", c.signMode)
	}
	if c.storeType == "postgres" && c.postgresDSN == "" {
		return fmt.Errorf("PODIUM_POSTGRES_DSN is required when PODIUM_REGISTRY_STORE=postgres")
	}
	// §13.12: "The registry refuses to start when a backend is selected but
	// its required values are missing, naming the missing keys in the error."
	if missing := c.missingBackendValues(); len(missing) > 0 {
		return fmt.Errorf("missing required configuration for the selected backend(s): %s",
			strings.Join(missing, ", "))
	}
	return nil
}

// missingBackendValues returns the env-var names that a selected backend
// requires but that resolved empty. §13.12 makes a configured-but-incomplete
// backend a hard startup error (F-13.12.10); the warn-and-disable path in Run
// is reserved for the explicit none/unset selection, for an unknown backend
// name, and for a fully-configured backend that is merely unreachable at
// runtime (search then degrades to BM25 per §13.12). An embedding provider
// set to the empty string is an intentional disable, not a selection, so it
// is not checked here.
func (c *Config) missingBackendValues() []string {
	var missing []string
	req := func(present bool, key string) {
		if !present {
			missing = append(missing, key)
		}
	}
	switch c.objectStore {
	case "s3":
		req(c.s3Bucket != "", "PODIUM_S3_BUCKET")
		// §13.12 marks the region required for s3 (F-13.12.9).
		req(c.s3Region != "", "PODIUM_S3_REGION")
	}
	switch c.vectorBackend {
	case "pinecone":
		req(c.pineconeKey != "", "PODIUM_PINECONE_API_KEY")
		// The host is auto-resolved from the index name (§13.12), so either
		// the host or the index locates the backend.
		req(c.pineconeHost != "" || c.pineconeIndex != "", "PODIUM_PINECONE_INDEX")
	case "weaviate-cloud":
		req(c.weaviateURL != "", "PODIUM_WEAVIATE_URL")
		req(c.weaviateKey != "", "PODIUM_WEAVIATE_API_KEY")
		// §13.12 marks the collection required for weaviate-cloud (F-13.12.12).
		req(c.weaviateColl != "", "PODIUM_WEAVIATE_COLLECTION")
	case "qdrant-cloud":
		req(c.qdrantURL != "", "PODIUM_QDRANT_URL")
		req(c.qdrantKey != "", "PODIUM_QDRANT_API_KEY")
		// §13.12 marks the collection required for qdrant-cloud (F-13.12.12).
		req(c.qdrantColl != "", "PODIUM_QDRANT_COLLECTION")
	case "pgvector":
		req(c.pgvectorDSN != "", "PODIUM_PGVECTOR_DSN")
	}
	// §9.1 / §13.12 (F-13.12.6, F-9.1.3): an embedder is built, and so its
	// per-provider API key is required, unless the selected backend genuinely
	// self-embeds. A storage-only backend (pgvector, sqlite-vec) cannot
	// self-embed even with a stray *_INFERENCE_MODEL set, so gate on
	// vectorSelfEmbeds() rather than on the inference-model string alone.
	// §9.1 / §4.7 (F-9.1.1): even on a self-embedding backend, an explicit
	// EmbeddingProvider override is built and needs its key.
	if !c.vectorSelfEmbeds() || c.embeddingProviderExplicit {
		switch c.embeddingProvider {
		case "openai":
			req(c.openaiAPIKey != "", "OPENAI_API_KEY")
		case "voyage":
			req(c.voyageAPIKey != "", "VOYAGE_API_KEY")
		case "cohere":
			req(c.cohereAPIKey != "", "COHERE_API_KEY")
		}
	}
	return missing
}

func (c *Config) modeBanner() string {
	if c.publicMode {
		return "public"
	}
	if c.identityProvider != "" {
		return c.identityProvider
	}
	return "standalone"
}

func openStore(c *Config) (store.Store, error) {
	switch c.storeType {
	case "sqlite":
		dir := filepath.Dir(c.sqlitePath)
		_ = os.MkdirAll(dir, 0o755)
		return store.OpenSQLite(c.sqlitePath)
	case "memory":
		// §13.12 lists only postgres | sqlite; `memory` is an undocumented
		// test affordance. Warn so an operator who selects it in a real
		// process knows it persists nothing (F-13.12.14).
		log.Printf("warning: PODIUM_REGISTRY_STORE=memory is a non-durable test backend; it persists nothing across restarts")
		return store.NewMemory(), nil
	case "postgres":
		return store.OpenPostgres(c.postgresDSN)
	}
	return nil, fmt.Errorf("unknown PODIUM_REGISTRY_STORE: %s", c.storeType)
}

// openObjectStoreOrNil opens the configured §7.2 object store, logging
// and returning nil when it is disabled or fails to open. The same
// instance backs the ingest-time resource upload, the load_artifact
// data plane, and the §13.9 readiness probe, so it is opened once in Run
// and threaded everywhere.
func openObjectStoreOrNil(c *Config) objectstore.Provider {
	objStore, err := openObjectStore(c)
	if err != nil {
		log.Printf("warning: object store disabled: %v", err)
		return nil
	}
	return objStore
}

// bootstrapOptions builds the base server options for the already-opened
// object store (nil when disabled). The store is opened in Run so the
// ingest-time resource upload and the §13.9 readiness probe share the
// same instance.
func bootstrapOptions(c *Config, objStore objectstore.Provider) []server.Option {
	out := []server.Option{}
	if c.publicMode {
		out = append(out, server.WithPublicMode())
	}
	if objStore != nil {
		out = append(out, server.WithObjectStore(objStore, c.publicURL, c.presignTTL))
	}
	return out
}

// vectorSelfEmbeds reports whether the selected vector backend embeds text
// server-side (§13.12 F-13.12.6): a cloud backend with an inference-model /
// vectorizer configured. The local backends (pgvector, sqlite-vec) cannot
// self-embed, so a stray inference model with one of those is ignored and
// the normal embedding-provider path applies.
func (c *Config) vectorSelfEmbeds() bool {
	if c.vectorInferenceModel == "" {
		return false
	}
	switch c.vectorBackend {
	case "pinecone", "weaviate-cloud", "qdrant-cloud":
		return true
	}
	return false
}

// embeddingSettings collects the resolved §9.1 EmbeddingProvider settings
// a registered custom provider may read. The map is wire-serializable per
// §9.3 so a future out-of-process provider receives the same inputs.
func (c *Config) embeddingSettings() map[string]string {
	return map[string]string{
		"model":      c.embeddingModel,
		"openai_key": c.openaiAPIKey,
		"voyage_key": c.voyageAPIKey,
		"cohere_key": c.cohereAPIKey,
		"ollama_url": c.ollamaURL,
	}
}

// vectorSettings collects the resolved §9.1 RegistrySearchProvider
// settings a registered custom backend may read. The map is wire-
// serializable per §9.3.
func (c *Config) vectorSettings() map[string]string {
	return map[string]string{
		"pgvector_dsn":    c.pgvectorDSN,
		"sqlite_path":     c.sqlitePath,
		"pinecone_key":    c.pineconeKey,
		"pinecone_host":   c.pineconeHost,
		"pinecone_index":  c.pineconeIndex,
		"namespace":       c.pineconeNS,
		"weaviate_url":    c.weaviateURL,
		"weaviate_key":    c.weaviateKey,
		"collection":      c.weaviateColl,
		"qdrant_url":      c.qdrantURL,
		"qdrant_key":      c.qdrantKey,
		"inference_model": c.vectorInferenceModel,
	}
}

// openVectorAndEmbedder returns the configured §4.7 hybrid-search
// pieces. Returns (nil, nil, nil) when vector search is disabled
// (operator left PODIUM_VECTOR_BACKEND unset / set to "none").
//
// §13.12 (F-13.12.6): when the selected backend self-embeds, the embedding
// provider is optional; the backend is opened with no local dimension and a
// nil embedder is returned so the registry sends raw text on Put/Query.
//
// §9.1 / §4.7 (F-9.1.1): an explicitly-configured EmbeddingProvider overrides
// the backend's hosted model even when the backend self-embeds. The embedder
// is built and the backend is opened at the embedder's dimension, so the
// core's override path (queryVector / upsertVector prefer a present embedder)
// embeds locally and writes vectors instead of sending raw text. A defaulted
// provider, or an explicit "none"/empty, is not an override and leaves the
// backend self-embedding.
func openVectorAndEmbedder(c *Config) (vector.Provider, embedding.Provider, error) {
	if c.vectorSelfEmbeds() {
		if c.embeddingProviderExplicit {
			emb, err := openEmbedder(c)
			if err != nil {
				return nil, nil, err
			}
			if emb != nil {
				v, err := openVectorBackend(c, emb.Dimensions())
				if err != nil {
					return nil, nil, err
				}
				return v, emb, nil
			}
		}
		v, err := openVectorBackend(c, 0)
		if err != nil {
			return nil, nil, err
		}
		return v, nil, nil
	}
	emb, err := openEmbedder(c)
	if err != nil {
		return nil, nil, err
	}
	if emb == nil {
		return nil, nil, nil
	}
	v, err := openVectorBackend(c, emb.Dimensions())
	if err != nil {
		return nil, nil, err
	}
	return v, emb, nil
}

// embeddingModelFor resolves the §13.12 model name for a built-in provider.
// The per-provider env var (e.g. PODIUM_OPENAI_MODEL) wins; otherwise it falls
// back to c.embeddingModel, which already carries the generic
// PODIUM_EMBEDDING_MODEL env var or the registry.yaml embedding_provider.model
// key. An empty result leaves the provider's hardcoded default model in force.
// spec: §13.12 (F-13.12.2 — the config-file model: key takes effect for the
// built-in providers, not just the per-provider env vars).
func (c *Config) embeddingModelFor(perProviderEnv string) string {
	if v := os.Getenv(perProviderEnv); v != "" {
		return v
	}
	return c.embeddingModel
}

// openEmbedder honors §13 per-provider model env vars
// (PODIUM_OPENAI_MODEL, PODIUM_VOYAGE_MODEL, PODIUM_COHERE_MODEL,
// PODIUM_OLLAMA_MODEL). The generic PODIUM_EMBEDDING_MODEL and the
// registry.yaml embedding_provider.model key (both captured in
// c.embeddingModel) act as a cross-provider fallback when the per-provider
// variable isn't set (F-13.12.2).
func openEmbedder(c *Config) (embedding.Provider, error) {
	// §9.1/§9.2: consult the process-global embedding.Default registry first
	// so a custom EmbeddingProvider imported into a source build (via
	// embedding.Default.Register) is selectable by PODIUM_EMBEDDING_PROVIDER
	// without editing this switch. The built-in providers fall through.
	if p, ok, err := embedding.Default.New(c.embeddingProvider, c.embeddingSettings()); err != nil {
		return nil, err
	} else if ok {
		return p, nil
	}
	switch c.embeddingProvider {
	case "", "none":
		return nil, nil
	case "openai":
		if c.openaiAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for openai embedder")
		}
		return embedding.OpenAI{
			APIKey:  c.openaiAPIKey,
			Model_:  c.embeddingModelFor("PODIUM_OPENAI_MODEL"),
			BaseURL: c.openaiBaseURL,
			Org:     c.openaiOrg,
		}, nil
	case "voyage":
		if c.voyageAPIKey == "" {
			return nil, fmt.Errorf("VOYAGE_API_KEY required for voyage embedder")
		}
		return embedding.Voyage{
			APIKey: c.voyageAPIKey,
			Model_: c.embeddingModelFor("PODIUM_VOYAGE_MODEL"),
		}, nil
	case "cohere":
		if c.cohereAPIKey == "" {
			return nil, fmt.Errorf("COHERE_API_KEY required for cohere embedder")
		}
		return embedding.Cohere{
			APIKey: c.cohereAPIKey,
			Model_: c.embeddingModelFor("PODIUM_COHERE_MODEL"),
		}, nil
	case "ollama":
		return embedding.Ollama{
			BaseURL: c.ollamaURL,
			Model_:  c.embeddingModelFor("PODIUM_OLLAMA_MODEL"),
		}, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_EMBEDDING_PROVIDER: %s", c.embeddingProvider)
}

func openVectorBackend(c *Config, dim int) (vector.Provider, error) {
	// §9.1/§9.2: consult the process-global vector.Default registry first so
	// a custom RegistrySearchProvider imported into a source build (via
	// vector.Default.Register) is selectable by PODIUM_VECTOR_BACKEND without
	// editing this switch. The built-in backends fall through.
	if p, ok, err := vector.Default.New(c.vectorBackend, c.vectorSettings(), dim); err != nil {
		return nil, err
	} else if ok {
		return p, nil
	}
	if c.vectorBackend == "memory" {
		// §13.12 lists pgvector | sqlite-vec | pinecone | weaviate-cloud |
		// qdrant-cloud; `memory` is an undocumented test affordance. Warn so
		// an operator who selects it knows it persists nothing (F-13.12.14).
		log.Printf("warning: PODIUM_VECTOR_BACKEND=memory is a non-durable test backend; it persists nothing across restarts")
	}
	// §13.12 (F-13.12.6) self-embedding leaves InferenceModel empty for
	// storage-only mode. The built-in backends are constructed by the shared
	// vector.OpenBuiltin factory so the overlay path (§6.4.1) selects the
	// same set the same way.
	return vector.OpenBuiltin(c.vectorBackend, c.backendConfig(), dim)
}

// backendConfig projects the resolved vector configuration into the shared
// vector.BackendConfig consumed by vector.OpenBuiltin.
func (c *Config) backendConfig() vector.BackendConfig {
	return vector.BackendConfig{
		PgVectorDSN:          c.pgvectorDSN,
		SQLitePath:           c.sqlitePath,
		PineconeKey:          c.pineconeKey,
		PineconeHost:         c.pineconeHost,
		PineconeIndex:        c.pineconeIndex,
		PineconeNS:           c.pineconeNS,
		PineconeControlPlane: c.pineconeControlPlane,
		WeaviateURL:          c.weaviateURL,
		WeaviateKey:          c.weaviateKey,
		WeaviateColl:         c.weaviateColl,
		QdrantURL:            c.qdrantURL,
		QdrantKey:            c.qdrantKey,
		QdrantColl:           c.qdrantColl,
		InferenceModel:       c.vectorInferenceModel,
	}
}

// openObjectStore returns the configured §13.10 object-storage
// backend, or (nil, nil) when the standalone deployment runs without
// one (resources stay inline regardless of size).
func openObjectStore(c *Config) (objectstore.Provider, error) {
	switch c.objectStore {
	case "", "filesystem":
		_ = os.MkdirAll(c.filesystemRoot, 0o755)
		return objectstore.Open(c.filesystemRoot)
	case "s3":
		if c.s3Bucket == "" {
			return nil, fmt.Errorf("PODIUM_S3_BUCKET is required when PODIUM_OBJECT_STORE=s3")
		}
		// §13.12: the endpoint is a URL; its scheme selects TLS (https on,
		// http off), and an unset endpoint defaults to AWS S3 over TLS.
		host, useSSL := objectstore.ParseS3Endpoint(c.s3Endpoint)
		return objectstore.NewS3(objectstore.S3Config{
			Endpoint:        host,
			Bucket:          c.s3Bucket,
			Region:          c.s3Region,
			AccessKeyID:     c.s3AccessKey,
			SecretAccessKey: c.s3SecretKey,
			UseSSL:          useSSL,
			ForcePathStyle:  c.s3ForcePathStyle,
		})
	case "none":
		return nil, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_OBJECT_STORE: %s", c.objectStore)
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// envBoolPtr reads a tri-state boolean env var: unset yields nil (leave
// the default), a truthy value yields &true, anything else yields &false.
func envBoolPtr(key string) *bool {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	b := isTrue(v)
	return &b
}
