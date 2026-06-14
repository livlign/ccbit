package render

import (
	"strings"
	"testing"
)

func TestDemoCoversEveryState(t *testing.T) {
	out := strings.Join(Demo(false), "\n")
	for _, label := range []string{"working", "agents", "waiting", "failed", "done", "redeemed", "stopped", "idle"} {
		if !strings.Contains(out, label) {
			t.Errorf("demo output missing state %q", label)
		}
	}
	// The full sample exercises the ambient line and the sibling digest.
	for _, want := range []string{"ctx 38%", "main", "Elsewhere", "needs you"} {
		if !strings.Contains(out, want) {
			t.Errorf("demo full sample missing %q", want)
		}
	}
}

func TestDemoColorIsOptional(t *testing.T) {
	if strings.Contains(strings.Join(Demo(false), "\n"), "\x1b[") {
		t.Error("NO_COLOR demo should contain no ANSI escapes")
	}
	if !strings.Contains(strings.Join(Demo(true), "\n"), "\x1b[") {
		t.Error("colored demo should contain ANSI escapes")
	}
}
