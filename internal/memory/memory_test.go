package memory

import (
	"testing"
	"time"
)

func TestLearnedStallGatedBySamples(t *testing.T) {
	var s Stats
	// Below the sample gate, Bit stays on the fallback — never asserts on thin data.
	for i := 0; i < minSamples-1; i++ {
		s.Record(30*time.Second, 20*time.Second)
		if got := s.LearnedStall(90 * time.Second); got != 90*time.Second {
			t.Fatalf("with %d samples LearnedStall = %v, want fallback 90s", s.Samples, got)
		}
	}
	// One more crosses the gate; threshold ~= 2x typical gap (20s -> 40s), but
	// clamped up to the 45s floor.
	s.Record(30*time.Second, 20*time.Second)
	if got := s.LearnedStall(90 * time.Second); got != stallFloor {
		t.Fatalf("learned stall = %v, want floor %v", got, stallFloor)
	}
}

func TestLearnedStallClamp(t *testing.T) {
	var s Stats
	// Long, steady in-turn gaps push the learned stall up — capped at the ceiling.
	for i := 0; i < 20; i++ {
		s.Record(5*time.Minute, 5*time.Minute)
	}
	if got := s.LearnedStall(90 * time.Second); got != stallCeil {
		t.Fatalf("learned stall = %v, want ceil %v", got, stallCeil)
	}
}

func TestGapSampleCeilCaps(t *testing.T) {
	var s Stats
	// A single "walked away" gap must not dominate the typical-gap average.
	s.Record(time.Minute, time.Hour)
	if s.GapEWMA > gapSampleCeil.Seconds()+1 {
		t.Fatalf("GapEWMA = %.0f, should be capped near %.0f", s.GapEWMA, gapSampleCeil.Seconds())
	}
}

func TestTypicalTurnGate(t *testing.T) {
	var s Stats
	s.Record(2*time.Minute, 10*time.Second)
	if got := s.TypicalTurn(); got != 0 {
		t.Fatalf("TypicalTurn with 1 sample = %v, want 0 (gated)", got)
	}
	for i := 0; i < minSamples; i++ {
		s.Record(2*time.Minute, 10*time.Second)
	}
	if got := s.TypicalTurn(); got < 90*time.Second || got > 150*time.Second {
		t.Fatalf("TypicalTurn = %v, want ~2m", got)
	}
}

func TestKeyStableAndDistinct(t *testing.T) {
	a := Key("/home/u/proj-one", "proj-one")
	b := Key("/home/u/proj-one", "proj-one")
	c := Key("/home/u/other/proj-one", "proj-one") // same basename, different path
	if a != b {
		t.Fatalf("Key not stable: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("Key collided across distinct paths: %q", a)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := Key("/x/y", "y")
	var s Stats
	for i := 0; i < 6; i++ {
		s.Record(time.Minute, 25*time.Second)
	}
	Save(key, s)
	got := Load(key)
	if got.Samples != s.Samples || got.DurEWMA != s.DurEWMA || got.GapEWMA != s.GapEWMA {
		t.Fatalf("round-trip mismatch: saved %+v, loaded %+v", s, got)
	}
}
