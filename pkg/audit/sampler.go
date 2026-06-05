package audit

import "math/rand"

// Sampler implements the §8.4 optional sampling of high-volume
// low-sensitivity events ("e.g., domain.loaded at 10% sample reduces
// storage cost"). Each event type maps to a keep-probability in the
// range [0,1]: a rate of 0.1 keeps roughly 10% of events of that type.
//
// Sampling is consulted at write time, before an event is appended, so
// a dropped event never enters the §8.6 hash chain and never affects
// the chain head. A type with no configured rate is always kept, so
// sampling only ever reduces volume for the explicitly listed
// high-volume types; every other event is retained in full.
//
// spec: §8.4
type Sampler struct {
	rates map[EventType]float64
	// rng returns a pseudo-random value in [0,1). Overridable for
	// deterministic tests; production uses math/rand.Float64.
	rng func() float64
}

// NewSampler returns a Sampler for the given per-event-type keep rates,
// or nil when no rates are configured. A nil Sampler keeps every event,
// so callers can hold a *Sampler unconditionally and let Keep no-op.
func NewSampler(rates map[EventType]float64) *Sampler {
	if len(rates) == 0 {
		return nil
	}
	copied := make(map[EventType]float64, len(rates))
	for t, r := range rates {
		copied[t] = r
	}
	return &Sampler{rates: copied, rng: rand.Float64}
}

// Keep reports whether an event of type t should be written. A type
// with no configured rate (or a rate >= 1) is always kept; a rate <= 0
// always drops the type; any rate in between keeps the event with that
// probability. A nil Sampler keeps every event.
func (s *Sampler) Keep(t EventType) bool {
	if s == nil {
		return true
	}
	rate, ok := s.rates[t]
	if !ok || rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	return s.rng() < rate
}
