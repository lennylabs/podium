package audit

import "testing"

// Spec: §8.4 — a nil Sampler keeps every event, so callers can hold
// one unconditionally.
func TestSampler_NilKeepsEverything(t *testing.T) {
	t.Parallel()
	var s *Sampler
	if !s.Keep(EventDomainLoaded) {
		t.Errorf("nil sampler dropped an event")
	}
}

// Spec: §8.4 — NewSampler returns nil when no rates are configured.
func TestNewSampler_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	if s := NewSampler(nil); s != nil {
		t.Errorf("NewSampler(nil) = %v, want nil", s)
	}
	if s := NewSampler(map[EventType]float64{}); s != nil {
		t.Errorf("NewSampler(empty) = %v, want nil", s)
	}
}

// Spec: §8.4 — an unconfigured event type is always kept; sampling
// only ever reduces volume for the explicitly listed types.
func TestSampler_UnconfiguredTypeAlwaysKept(t *testing.T) {
	t.Parallel()
	s := NewSampler(map[EventType]float64{EventDomainLoaded: 0})
	if !s.Keep(EventArtifactLoaded) {
		t.Errorf("unconfigured type dropped")
	}
	// And the configured rate-0 type is always dropped.
	if s.Keep(EventDomainLoaded) {
		t.Errorf("rate-0 type kept")
	}
}

// Spec: §8.4 — rate >= 1 keeps everything; rate <= 0 drops everything,
// independent of the rng.
func TestSampler_BoundaryRates(t *testing.T) {
	t.Parallel()
	s := NewSampler(map[EventType]float64{
		EventDomainLoaded:   1,
		EventArtifactLoaded: 0,
	})
	// rng that would drop a fractional rate; boundaries ignore it.
	s.rng = func() float64 { return 0.99 }
	if !s.Keep(EventDomainLoaded) {
		t.Errorf("rate 1.0 dropped an event")
	}
	if s.Keep(EventArtifactLoaded) {
		t.Errorf("rate 0.0 kept an event")
	}
}

// Spec: §8.4 — a fractional rate keeps an event when rng < rate and
// drops it otherwise (the 10% domain.loaded example).
func TestSampler_FractionalRateUsesRNG(t *testing.T) {
	t.Parallel()
	s := NewSampler(map[EventType]float64{EventDomainLoaded: 0.1})
	s.rng = func() float64 { return 0.05 } // below 0.1 -> keep
	if !s.Keep(EventDomainLoaded) {
		t.Errorf("rng 0.05 < rate 0.1 should keep")
	}
	s.rng = func() float64 { return 0.5 } // above 0.1 -> drop
	if s.Keep(EventDomainLoaded) {
		t.Errorf("rng 0.5 > rate 0.1 should drop")
	}
	// Exactly at the rate boundary drops (keep requires strictly less).
	s.rng = func() float64 { return 0.1 }
	if s.Keep(EventDomainLoaded) {
		t.Errorf("rng == rate should drop")
	}
}
