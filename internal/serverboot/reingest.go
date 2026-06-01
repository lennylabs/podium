package serverboot

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// buildReingestRunner returns the §7.3.1 ingest-pipeline driver the layer
// endpoint invokes for the manual reingest and inbound-webhook triggers. It
// resolves the layer's built-in source provider, threads the shared linter,
// resource uploader, event publisher, audit emitter, and the §4.7.2 freeze
// windows, and applies a break-glass grant when the manual reingest path
// supplies one.
func buildReingestRunner(
	st store.Store,
	srv *server.Server,
	cfg *Config,
	resourcePut ingest.ResourcePutFunc,
	auditSink *audit.FileSink,
	scrubber *audit.PIIScrubber,
	signer ingest.SignerFunc,
) server.ReingestRunner {
	return func(ctx context.Context, lc store.LayerConfig, bg *server.BreakGlass) (*ingest.Result, error) {
		prov, err := sourceProviderFor(lc.SourceType)
		if err != nil {
			return nil, err
		}
		caller := reingestCaller(ctx)
		opts := ingest.SourceIngestOptions{
			Linter:        ingestLinter(cfg.allowPerDomain()),
			PublishEvent:  srv.PublishEvent,
			ResourcePut:   resourcePut,
			CallerID:      caller,
			FreezeWindows: applyBreakGlass(cfg.freezeWindows, bg, caller),
			// §13.10/§4.7.9 ingest signing: nil leaves manifests unsigned.
			Signer: signer,
		}
		if auditSink != nil {
			opts.AuditEmit = ingestAuditEmitter(ctx, auditSink, scrubber, caller)
		}
		return ingest.SourceIngestWithOptions(ctx, st, prov, lc, opts)
	}
}

// sourceProviderFor returns the built-in §4.6 source provider for a layer's
// source type. An unknown type is an invalid config (the runner maps it to
// registry.invalid_argument).
func sourceProviderFor(sourceType string) (source.Provider, error) {
	switch sourceType {
	case "git":
		return source.Git{}, nil
	case "local":
		return source.Local{}, nil
	default:
		return nil, fmt.Errorf("%w: unknown source type %q", source.ErrInvalidConfig, sourceType)
	}
}

// applyBreakGlass returns a copy of the configured freeze windows with the
// break-glass grant attached when the manual reingest path supplied one. The
// ingest pipeline validates the §4.7.2 dual-signoff (two distinct approvers,
// non-empty justification, ≤24h) before honoring the bypass; an invalid grant
// leaves the window in effect. The triggering caller is recorded as one
// approver so a single supplied approver still yields the required two.
func applyBreakGlass(windows []ingest.FreezeWindow, bg *server.BreakGlass, caller string) []ingest.FreezeWindow {
	if len(windows) == 0 || bg == nil {
		return windows
	}
	approvers := bg.Approvers
	if caller != "" {
		approvers = append([]string{caller}, approvers...)
	}
	out := make([]ingest.FreezeWindow, len(windows))
	for i, w := range windows {
		w.BreakGlass = true
		w.Justification = bg.Justification
		w.Approvers = approvers
		w.GrantedAt = time.Now().UTC()
		out[i] = w
	}
	return out
}

// reingestCaller resolves the operator identity from the per-request audit
// metadata so emitted §8.1 events name who triggered the ingest. Empty when
// the request carries no identity (anonymous standalone).
func reingestCaller(ctx context.Context) string {
	if m, ok := server.AuditMetaFromContext(ctx); ok {
		return m.Email
	}
	return ""
}

// ingestAuditEmitter adapts the §8.3 sink to ingest.AuditEmitterFunc so the
// pipeline's §8.1 events (artifact.published, layer.ingested,
// layer.history_rewritten, freeze.break_glass) land in the audit log with the
// request's trace id and caller. A nil scrubber disables query-text scrubbing.
func ingestAuditEmitter(ctx context.Context, sink *audit.FileSink, scrubber *audit.PIIScrubber, caller string) ingest.AuditEmitterFunc {
	traceID := ""
	if m, ok := server.AuditMetaFromContext(ctx); ok {
		traceID = m.TraceID
	}
	return func(eventType, target string, ctxFields map[string]string) {
		ev := audit.Event{
			Type:    audit.EventType(eventType),
			Caller:  caller,
			Target:  target,
			Context: ctxFields,
			TraceID: traceID,
		}
		ev = scrubber.ScrubEvent(ev)
		_ = sink.Append(ctx, ev)
	}
}
