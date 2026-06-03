package serverboot

import (
	"context"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// auditEmitterFor adapts the §8.3 registry sink (a file sink, or an
// EndpointSink when redirected to a SIEM) to the core.AuditEmitter so
// every meta-tool call surfaces in the audit log. It carries the §8.1
// trace id and structured caller attributes (email, groups, public-mode
// network, and the public_mode flag) from the per-request audit metadata
// the server's identity middleware attached to the context.
//
// Before each write it applies the two §8.2 redaction surfaces: manifest-
// declared field redaction (RedactFields keyed by the event's RedactKeys)
// and default-on query-text scrubbing (scrubber.ScrubEvent over the search
// query). A nil scrubber disables query-text scrubbing.
//
// The §8.4 sampler is consulted first: when an event type carries a
// configured keep-rate (e.g. domain.loaded at 10%), the emitter drops the
// event before it enters the hash chain. A nil sampler keeps every event.
func auditEmitterFor(sink audit.Sink, scrubber *audit.PIIScrubber, sampler *audit.Sampler) core.AuditEmitter {
	return func(ctx context.Context, e core.AuditEvent) {
		if !sampler.Keep(audit.EventType(e.Type)) {
			return
		}
		fields := e.Context
		if len(e.RedactKeys) > 0 {
			fields = audit.RedactFields(fields, e.RedactKeys)
		}
		ev := audit.Event{
			Type:           audit.EventType(e.Type),
			Caller:         e.Caller,
			Target:         e.Target,
			Context:        fields,
			ResolvedLayers: e.ResolvedLayers,
			ResultSize:     e.ResultSize,
		}
		ev = scrubber.ScrubEvent(ev)
		if m, ok := server.AuditMetaFromContext(ctx); ok {
			ev.TraceID = m.TraceID
			ev.PublicMode = m.PublicMode
			if m.PublicMode {
				ev.CallerNetwork = &audit.CallerNetwork{SourceIP: m.SourceIP, ForwardedUser: m.ForwardedUser}
			} else {
				ev.CallerEmail = m.Email
				ev.CallerGroups = m.Groups
			}
		}
		_ = sink.Append(ctx, ev)
	}
}

// auditVolumeEmitter wraps next so every emitted audit event counts against the
// tenant's §4.7.8 daily audit-volume budget. It records first, then delegates
// to next (which may be nil when no sink is configured, in which case the event
// is counted and dropped). The recorded count is what the §7.3.1 reingest path
// consults to refuse new writes with quota.audit_volume_exceeded.
func auditVolumeEmitter(meter *server.AuditVolumeMeter, tenant string, next core.AuditEmitter) core.AuditEmitter {
	return func(ctx context.Context, e core.AuditEvent) {
		meter.Record(tenant)
		if next != nil {
			next(ctx, e)
		}
	}
}
