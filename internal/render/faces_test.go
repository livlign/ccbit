package render

import (
	"strings"
	"testing"

	"github.com/livlign/ccbit/internal/transcript"
)

func TestEyeFromPool(t *testing.T) {
	for i := uint64(0); i < 200; i++ {
		if !contains(eyes, eye(i)) {
			t.Fatalf("eye(%d)=%q not in pool", i, eye(i))
		}
	}
}

func TestIdleFaceStableAndCarriesEye(t *testing.T) {
	for i := uint64(0); i < 100; i++ {
		f := idleFace(i, false)
		if f != idleFace(i, false) {
			t.Fatalf("idle face not stable for seed %d", i)
		}
		if !strings.Contains(f, eye(i)) {
			t.Errorf("idleFace(%d)=%q should carry its eye %q", i, f, eye(i))
		}
	}
}

func TestWorkingFaceAnimatesAndCarriesEye(t *testing.T) {
	for i := uint64(0); i < 100; i++ {
		a, b := workingFace(i, 0), workingFace(i, 1)
		if a == b {
			t.Errorf("working frames identical for seed %d: %q", i, a)
		}
		if !strings.Contains(a, eye(i)) || !strings.Contains(b, eye(i)) {
			t.Errorf("working face should carry its eye %q: %q / %q", eye(i), a, b)
		}
	}
}

// Eyes and hands must vary independently across turns — sequential seeds should
// reach every eye and every hand. (Hands are counted by index: two working
// gestures intentionally share a first frame and differ only in the second.)
func TestPartsCoverage(t *testing.T) {
	seenEye := map[string]bool{}
	seenIdleHand := map[uint64]bool{}
	seenWorkHand := map[uint64]bool{}
	for i := uint64(0); i < 1000; i++ {
		seenEye[eye(i)] = true
		seenIdleHand[handIndex(i, len(idleHands))] = true
		seenWorkHand[handIndex(i, len(workingHands))] = true
	}
	if len(seenEye) != len(eyes) {
		t.Errorf("eyes covered %d/%d", len(seenEye), len(eyes))
	}
	if len(seenIdleHand) != len(idleHands) {
		t.Errorf("idle hands covered %d/%d", len(seenIdleHand), len(idleHands))
	}
	if len(seenWorkHand) != len(workingHands) {
		t.Errorf("working hands covered %d/%d", len(seenWorkHand), len(workingHands))
	}
}

func TestIdleCupDropsPropWhenNarrow(t *testing.T) {
	for i := uint64(0); i < 1000; i++ {
		if strings.Contains(idleFace(i, false), "旦") {
			if got := idleFace(i, true); strings.Contains(got, "旦") {
				t.Fatalf("cup face should lose its prop when narrow, got %q", got)
			}
			return
		}
	}
	t.Fatal("expected a cup face across seeds")
}

func TestFaceSeedPerTurn(t *testing.T) {
	a := transcript.Turn{PromptID: "turn-a"}
	b := transcript.Turn{PromptID: "turn-b"}
	if faceSeed("s", a) != faceSeed("s", a) {
		t.Fatal("seed must be stable for the same session+turn")
	}
	if faceSeed("s", a) == faceSeed("s", b) {
		t.Error("different turns should produce different seeds")
	}
	if faceSeed("s1", a) == faceSeed("s2", a) {
		t.Error("different sessions should produce different seeds")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
