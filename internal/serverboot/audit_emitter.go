package serverboot

import (
	"context"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// auditEmitterFor adapts the §8 file-backed sink to the core.AuditEmitter
// so every meta-tool call surfaces in the audit log. It carries the §8.1
// trace id and structured caller attributes (email, groups, public-mode
// network, and the public_mode flag) from the per-request audit metadata
// the server's identity middleware attached to the context.
//
// Before each write it applies the two §8.2 redaction surfaces: manifest-
// declared field redaction (RedactFields keyed by the event's RedactKeys)
// and default-on query-text scrubbing (scrubber.ScrubEvent over the search
// query). A nil scrubber disables query-text scrubbing.
func auditEmitterFor(sink *audit.FileSink, scrubber *audit.PIIScrubber) core.AuditEmitter {
	return func(ctx context.Context, e core.AuditEvent) {
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
