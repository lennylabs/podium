package serverboot

import (
	"context"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// auditEmitterFor adapts the §8 file-backed sink to the
// core.AuditEmitter shape so every meta-tool call surfaces in the
// audit log.
func auditEmitterFor(sink *audit.FileSink) core.AuditEmitter {
	return func(ctx context.Context, e core.AuditEvent) {
		_ = sink.Append(ctx, audit.Event{
			Type:    audit.EventType(e.Type),
			Caller:  e.Caller,
			Target:  e.Target,
			Context: e.Context,
		})
	}
}
