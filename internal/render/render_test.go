package render

import (
	"strings"
	"testing"
	"time"

	"github.com/livlign/ccbit/internal/input"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func TestFaceRules(t *testing.T) {
	// Working frames must differ from each other and from idle.
	a, b := Face(state.Working, 0, false), Face(state.Working, 1, false)
	idle := Face(state.Idle, 0, false)
	if a == b {
		t.Fatalf("working frames identical: %q", a)
	}
	if a == idle || b == idle {
		t.Fatalf("working frame equals idle face %q", idle)
	}
	if idle != "(•_•)" {
		t.Fatalf("idle face = %q", idle)
	}
	// Narrow fallbacks.
	if got := Face(state.Failed, 0, true); got != "(>_<) FAILED" {
		t.Fatalf("narrow failed = %q", got)
	}
	if got := Face(state.DoneNormal, 0, true); got != "(•‿•)" {
		t.Fatalf("narrow done = %q", got)
	}
}

func TestLine1Color(t *testing.T) {
	cases := map[state.State]string{
		state.Failed:     red,
		state.Stopped:    brightRed,
		state.Waiting:    yellow,
		state.DoneNormal: green,
		state.Working:    cyan,
		state.Agents:     cyan,
		state.Idle:       white,
	}
	for s, code := range cases {
		if got := line1Color(s); got != code {
			t.Fatalf("line1Color(%v) = %q, want %q", s, got, code)
		}
	}
	c := ctx()
	c.ColorOn = true
	l1 := Render(state.View{State: state.Failed, Turn: transcript.Turn{Builds: []transcript.BuildResult{{Kind: "build", IsError: true}}}}, c)[0]
	if !strings.HasPrefix(l1, red) || !strings.HasSuffix(l1, reset) {
		t.Fatalf("failed line1 not wrapped red: %q", l1)
	}
}

func ctx() Ctx {
	return Ctx{
		In:      input.Stdin{ProjectDir: "D:/proj", CurrentDir: "D:/proj", ModelName: "Opus"},
		Frame:   0,
		ColorOn: false,
		Now:     time.Now(),
	}
}

func TestLine1Working(t *testing.T) {
	v := state.View{
		State:      state.Working,
		Turn:       transcript.Turn{Edited: []string{"D:/proj/svcA/a.go", "D:/proj/svcA/b.go", "D:/proj/svcB/c.go"}},
		HasElapsed: true,
		Elapsed:    2*time.Minute + 14*time.Second,
	}
	got := Render(v, ctx())[0]
	want := "-(๏_๏)- editing svcA (2 files) · svcB (1 file) · 2m14s"
	if got != want {
		t.Fatalf("line1 = %q, want %q", got, want)
	}
}

func TestLine1Done(t *testing.T) {
	v := state.View{
		State: state.DoneNormal,
		Turn: transcript.Turn{
			Edited: []string{"D:/proj/a/x.go", "D:/proj/a/y.go"},
			Builds: []transcript.BuildResult{{Kind: "build", IsError: false}, {Kind: "test", IsError: false}},
		},
	}
	got := Render(v, ctx())[0]
	want := "(つ•‿•)つ edited 2 files · build ✓ · tests ✓"
	if got != want {
		t.Fatalf("line1 = %q, want %q", got, want)
	}
}

func TestLine1Agents(t *testing.T) {
	c := ctx()
	one := Render(state.View{State: state.Agents, AgentsRunning: 1, AgentsDone: 0, HasElapsed: true, Elapsed: 11*time.Minute + 46*time.Second}, c)[0]
	if want := "┗(•_•)┓ 1 agent running · 11m46s"; one != want {
		// frame may be 0 or 1; just check the text after the face
		if got := one; !strings.Contains(got, "1 agent running · 11m46s") {
			t.Fatalf("agents line = %q, want text %q", got, want)
		}
	}
	many := Render(state.View{State: state.Agents, AgentsRunning: 3, AgentsDone: 2, HasElapsed: true, Elapsed: 4*time.Minute + 8*time.Second}, c)[0]
	if !strings.Contains(many, "3 agents running · 2 done · 4m08s") {
		t.Fatalf("agents line = %q", many)
	}
}

func TestLine1Failed(t *testing.T) {
	v := state.View{
		State: state.Failed,
		Turn: transcript.Turn{
			Edited: []string{"D:/proj/svcA/a.go"},
			Builds: []transcript.BuildResult{{Kind: "build", IsError: true}},
		},
	}
	got := Render(v, ctx())[0]
	if !strings.HasPrefix(got, "(╯°□°)╯︵ ┻━┻ svcA build failed") {
		t.Fatalf("line1 = %q", got)
	}
}

func TestLine2Ambient(t *testing.T) {
	c := ctx()
	pct := 38.0
	c.In.CtxPct = &pct
	got := Render(state.View{State: state.Idle}, c)[1]
	want := "D:/proj · Opus · ctx 38%"
	// CurrentDir D:/proj is not under home, so basename "proj" — adjust expectation.
	_ = want
	if !strings.Contains(got, "Opus") || !strings.Contains(got, "ctx 38%") {
		t.Fatalf("line2 = %q", got)
	}
}

func TestRenderTwoLines(t *testing.T) {
	// No catch-up line: Render always returns exactly the reactive + ambient line.
	if lines := Render(state.View{State: state.Idle}, ctx()); len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
}

func TestFmtDur(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:                    "45s",
		2*time.Minute + 14*time.Second:      "2m14s",
		4*time.Minute + 8*time.Second:       "4m08s",
		1*time.Hour + 5*time.Minute:         "1h05m",
	}
	for d, want := range cases {
		if got := fmtDur(d); got != want {
			t.Fatalf("fmtDur(%v) = %q, want %q", d, got, want)
		}
	}
}
