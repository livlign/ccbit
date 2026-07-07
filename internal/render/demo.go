package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/livlign/ccbit/internal/gitx"
	"github.com/livlign/ccbit/internal/input"
	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

// Demo renders a representative line for every state, plus one full two-line
// sample with an ambient line and sibling sessions. It lets a user preview Bit
// without waiting for a real session to hit each state, and is the harness the
// README's hero visual is built from. Pure synthetic data: no transcript,
// stdin, or heartbeat is read.
func Demo(colorOn bool) []string {
	now := time.Unix(1_700_000_000, 0)
	root := "/work/ccbit"
	f := func(p string) string { return root + "/" + p }
	base := Ctx{ColorOn: colorOn, Now: now, RepoRoot: root, ProjectLabel: "ccbit"}

	withElapsed := func(v state.View, d time.Duration) state.View {
		v.Elapsed, v.HasElapsed = d, true
		return v
	}
	withAge := func(v state.View, d time.Duration) state.View {
		v.LastAge, v.HasLastAge = d, true
		return v
	}

	type scene struct {
		label string
		v     state.View
		c     Ctx
	}
	scenes := []scene{
		{"working", withElapsed(state.View{State: state.Working,
			Turn: transcript.Turn{Open: true, Edited: []string{f("render.go"), f("state.go"), f("main.go")}}},
			134*time.Second), withTasks(base, 4, 1)},
		{"agents", withElapsed(state.View{State: state.Agents, AgentsRunning: 2, AgentsDone: 1,
			Turn: transcript.Turn{Open: true}}, 48*time.Second), base},
		{"waiting", withAge(state.View{State: state.Waiting,
			Turn: transcript.Turn{Pending: "AskUserQuestion"}}, 90*time.Second), base},
		{"failed", state.View{State: state.Failed,
			Turn: transcript.Turn{Edited: []string{f("render.go")},
				Builds: []transcript.BuildResult{{Kind: "build", IsError: true,
					Text: "Exit code 1\ninternal/render/render.go:42:3: undefined: foo"}}}}, base},
		{"done", state.View{State: state.DoneNormal,
			Turn: transcript.Turn{Edited: []string{f("a.go"), f("b.go"), f("c.go"), f("d.go")},
				Builds: []transcript.BuildResult{{Kind: "build"}, {Kind: "test"}}, Pushed: true}},
			withLines(base, 885, 99)},
		{"redeemed", state.View{State: state.DoneRedeemed,
			Turn: transcript.Turn{Edited: []string{f("render.go"), f("state.go")},
				Builds: []transcript.BuildResult{{Kind: "build", IsError: true}, {Kind: "build"}}}},
			withLines(base, 12, 3)},
		{"stopped", withAge(state.View{State: state.Stopped,
			Turn: transcript.Turn{Edited: []string{f("render.go")}, HasLast: true}}, 120*time.Second), base},
		{"idle", state.View{State: state.Idle}, base},
	}

	out := []string{"Bit — state preview", ""}
	for _, s := range scenes {
		line1 := Render(s.v, s.c)[0] // [0] is the reactive line; [1] is ambient
		out = append(out, fmt.Sprintf("  %-9s  %s", s.label, line1))
	}

	// Working and Idle faces are assembled per turn from shared parts; print the
	// parts so the rotation (and how each glyph renders in this terminal) is
	// visible at a glance.
	sample := eyes[0]
	var ih []string
	for _, h := range idleHands {
		ih = append(ih, fmt.Sprintf(h, sample))
	}
	var wh []string
	for _, h := range workingHands {
		wh = append(wh, fmt.Sprintf(h.a, sample)+"⇄"+fmt.Sprintf(h.b, sample))
	}
	out = append(out,
		"",
		"Faces assemble per turn from shared parts:",
		"  eyes:          "+strings.Join(eyes, "  "),
		"  idle hands:    "+strings.Join(ih, "   "),
		"  working hands: "+strings.Join(wh, "   "),
	)

	// One full render so the ambient line and the cross-session digest show too.
	out = append(out, "", "Full status line (ambient line + other sessions):", "")
	ctxPct := 38.0
	full := withLines(base, 885, 99)
	full.In = input.Stdin{CurrentDir: root, ModelName: "Opus", CtxPct: &ctxPct}
	full.Git = gitx.Info{Branch: "main", New: 1, Modified: 2, Deleted: 1, Ahead: 1}
	full.Trend = sessions.TrendUp
	full.Siblings = []sessions.Beat{
		{State: "failed", Project: "api", Title: "Fix login bug", UpdatedAt: now.Unix()},
		{State: "waiting", Project: "web", UpdatedAt: now.Unix()},
	}
	done := state.View{State: state.DoneNormal,
		Turn: transcript.Turn{Edited: []string{f("a.go"), f("b.go"), f("c.go"), f("d.go")},
			Builds: []transcript.BuildResult{{Kind: "build"}, {Kind: "test"}}, Pushed: true}}
	for _, line := range Render(done, full) {
		out = append(out, "  "+line)
	}
	return out
}

func withTasks(c Ctx, total, done int) Ctx {
	c.Tasks = transcript.TaskSummary{Total: total, Done: done}
	return c
}

func withLines(c Ctx, added, removed int) Ctx {
	c.TurnLinesAdded, c.TurnLinesRemoved = added, removed
	return c
}
