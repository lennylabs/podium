package serverboot

import (
	"context"

	"github.com/lennylabs/podium/pkg/audit"
)

// readOnlyEnterCallback returns a function that appends a
// registry.read_only_entered event to sink. Used by the §13.2.1
// read-only probe's OnEnter hook.
func readOnlyEnterCallback(sink *audit.FileSink, tenantID, reason string) func() {
	return func() {
		if sink == nil {
			return
		}
		_ = sink.Append(context.Background(), audit.Event{
			Type:    audit.EventReadOnlyEntered,
			Caller:  "system",
			Target:  tenantID,
			Context: map[string]string{"reason": reason},
		})
	}
}

// readOnlyExitCallback returns a function that appends a
// registry.read_only_exited event to sink.
func readOnlyExitCallback(sink *audit.FileSink, tenantID string) func() {
	return func() {
		if sink == nil {
			return
		}
		_ = sink.Append(context.Background(), audit.Event{
			Type:   audit.EventReadOnlyExited,
			Caller: "system",
			Target: tenantID,
		})
	}
}
