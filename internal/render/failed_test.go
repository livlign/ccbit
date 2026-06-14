package render

import (
	"strings"
	"testing"

	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func TestFailedLineSurfacesDiagnosis(t *testing.T) {
	c := ctx()
	v := state.View{State: state.Failed, Turn: transcript.Turn{
		Builds: []transcript.BuildResult{{Kind: "build", IsError: true,
			Text: "Exit code 1\nsvc/render.go:42:3: undefined: foo"}},
	}}
	got := Render(v, c)[0]
	if !strings.Contains(got, "build failed") {
		t.Fatalf("missing base failed text: %q", got)
	}
	if !strings.Contains(got, "svc/render.go:42:3: undefined: foo") {
		t.Errorf("failed line should surface the concrete reason: %q", got)
	}
}

func TestFailedLineNoDiagnosisStaysPlain(t *testing.T) {
	c := ctx()
	v := state.View{State: state.Failed, Turn: transcript.Turn{
		Builds: []transcript.BuildResult{{Kind: "build", IsError: true, Text: "something opaque"}},
	}}
	got := Render(v, c)[0]
	if strings.Contains(got, " · ") {
		t.Errorf("no extractable reason should leave the line plain, got %q", got)
	}
}

func TestFailedLineDiagnosisIsBounded(t *testing.T) {
	c := ctx()
	long := "error: " + strings.Repeat("verylongtoken ", 20)
	v := state.View{State: state.Failed, Turn: transcript.Turn{
		Builds: []transcript.BuildResult{{Kind: "build", IsError: true, Text: long}},
	}}
	got := Render(v, c)[0]
	// The reason clause is ellipsized to keep line 1 from blowing out.
	if !strings.Contains(got, "…") {
		t.Errorf("an overlong reason should be ellipsized, got %q", got)
	}
}
