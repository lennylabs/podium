package core

// EventAudienceWaiters reports how many callers of a.Visible are waiting on
// the shared layer read. TEST-1 case 14 uses it to confirm a caller is in the
// single-flight wait before it cancels the evaluator.
func EventAudienceWaiters(a *EventAudience) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.waiters
}
