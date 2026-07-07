package render

import (
	"strings"
	"testing"
	"time"

	"github.com/livlign/ccbit/internal/gitx"
	"github.com/livlign/ccbit/internal/input"
	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func TestTaskClause(t *testing.T) {
	cases := []struct {
		name    string
		state   state.State
		tasks   transcript.TaskSummary
		touched bool   // current turn created/updated a task
		want    string // "" means the line must not contain a todo fragment
	}{
		{"working partial", state.Working, transcript.TaskSummary{Total: 7, Done: 3}, false, "3/7 todos"},
		{"working none done", state.Working, transcript.TaskSummary{Total: 7, Done: 0}, false, "0/7 todos"},
		{"working all done this turn", state.Working, transcript.TaskSummary{Total: 7, Done: 7}, true, "7/7 todos"},
		{"working all done earlier turn", state.Working, transcript.TaskSummary{Total: 7, Done: 7}, false, ""},
		{"working current ignored", state.Working, transcript.TaskSummary{Total: 7, Done: 2, Current: "Wiring auth flow"}, false, "2/7 todos"},
		{"working no plan", state.Working, transcript.TaskSummary{}, true, ""},
		{"agents partial", state.Agents, transcript.TaskSummary{Total: 5, Done: 2}, false, "2/5 todos"},
		{"agents all done earlier turn", state.Agents, transcript.TaskSummary{Total: 5, Done: 5}, false, ""},
		{"idle with plan", state.Idle, transcript.TaskSummary{Total: 7, Done: 3}, true, ""},
		{"done with plan", state.DoneNormal, transcript.TaskSummary{Total: 7, Done: 3}, true, ""},
		{"failed with plan", state.Failed, transcript.TaskSummary{Total: 7, Done: 3}, true, ""},
		{"waiting with plan", state.Waiting, transcript.TaskSummary{Total: 7, Done: 3}, true, ""},
		{"stopped with plan", state.Stopped, transcript.TaskSummary{Total: 7, Done: 3}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctx()
			c.Tasks = tc.tasks
			v := state.View{State: tc.state, Turn: transcript.Turn{
				Edited:      []string{"D:/proj/a/x.go"},
				Builds:      []transcript.BuildResult{{Kind: "test", IsError: tc.state == state.Failed}},
				TaskTouched: tc.touched,
			}}
			got := Render(v, c)[0]
			if tc.want == "" {
				if strings.Contains(got, "todos") {
					t.Fatalf("line = %q, want no todo fragment", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("line = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestThinkingClause(t *testing.T) {
	v := state.View{State: state.Working, Thinking: true, HasLastAge: true, LastAge: 2*time.Minute + 31*time.Second}
	got := Render(v, ctx())[0]
	if !strings.Contains(got, "thinking (2m31s)") {
		t.Fatalf("working line = %q, want thinking note", got)
	}
	v.Thinking = false
	if got = Render(v, ctx())[0]; strings.Contains(got, "thinking") {
		t.Fatalf("non-thinking working line = %q, should have no note", got)
	}
}

func TestRunningClause(t *testing.T) {
	v := state.View{State: state.Working, HasInFlight: true,
		InFlight: `dotnet list "D:\proj\x.sln" package --vulnerable --include-transitive`, InFlightFor: 3*time.Minute + 18*time.Second}
	got := Render(v, ctx())[0]
	if !strings.Contains(got, "running: ") || !strings.Contains(got, "(3m18s)") {
		t.Fatalf("working line = %q, want running clause with duration", got)
	}
	if strings.Contains(got, "--include-transitive") {
		t.Fatalf("command should be ellipsized, got %q", got)
	}
	// Quick calls stay silent.
	v.InFlightFor = 5 * time.Second
	if got = Render(v, ctx())[0]; strings.Contains(got, "running:") {
		t.Fatalf("sub-30s tool should be silent, got %q", got)
	}
}

func TestLoopNoteOnWorking(t *testing.T) {
	v := state.View{State: state.Working, Turn: transcript.Turn{
		Edited: []string{"D:/proj/a/render.go"}, HotFile: "D:/proj/a/render.go", HotFileEdits: 5,
	}}
	got := Render(v, ctx())[0]
	if !strings.Contains(got, "(render.go edited 5×)") {
		t.Fatalf("working line = %q, want churn note", got)
	}
}

func TestFailStreakOnFailed(t *testing.T) {
	v := state.View{State: state.Failed, Turn: transcript.Turn{
		Builds:     []transcript.BuildResult{{Kind: "test", IsError: true}},
		FailStreak: 3,
	}}
	got := Render(v, ctx())[0]
	if !strings.Contains(got, "(3× in a row)") {
		t.Fatalf("failed line = %q, want streak note", got)
	}
}

func TestWaitingAge(t *testing.T) {
	v := state.View{State: state.Waiting, HasLastAge: true, LastAge: 6 * time.Minute}
	got := Render(v, ctx())[0]
	if !strings.Contains(got, "waiting on you · 6m") {
		t.Fatalf("waiting line = %q, want age", got)
	}
	// Fresh waits stay terse.
	v.LastAge = 10 * time.Second
	if got = Render(v, ctx())[0]; strings.Contains(got, "·") {
		t.Fatalf("fresh wait should have no age, got %q", got)
	}
}

// clearFeatureEnv turns every opt-in visual feature off for a test, so its
// assertions don't depend on ambient CCBIT_* set in the developer's shell.
func clearFeatureEnv(t *testing.T) {
	for _, k := range []string{"CCBIT_NERD_FONT", "CCBIT_ICONS", "CCBIT_GIT_COLOR", "CCBIT_CTX_GAUGE", "CCBIT_RATE_COLOR"} {
		t.Setenv(k, "")
	}
}

func TestGitSegment(t *testing.T) {
	clearFeatureEnv(t)
	c := ctx()
	c.Git = gitx.Info{Branch: "main", New: 1, Modified: 3, Deleted: 2, Ahead: 2}
	l2 := Render(state.View{State: state.Idle}, c)[1]
	if !strings.Contains(l2, "main +1 ~3 -2 ↑2") {
		t.Fatalf("line2 = %q, want git segment main +1 ~3 -2 ↑2", l2)
	}
	c.Git = gitx.Info{Branch: "main"}
	l2 = Render(state.View{State: state.Idle}, c)[1]
	if !strings.Contains(l2, "main") || strings.ContainsAny(l2, "+~") {
		t.Fatalf("clean repo line2 = %q, want bare branch", l2)
	}
}

func TestGitSegmentNerdFont(t *testing.T) {
	clearFeatureEnv(t)
	t.Setenv("CCBIT_NERD_FONT", "1")
	c := ctx()
	c.Git = gitx.Info{Branch: "main", New: 1, Modified: 3, Deleted: 2, Ahead: 2}
	l2 := Render(state.View{State: state.Idle}, c)[1]
	// plus , pencil , trash  — each icon spaced from its count.
	if want := "main  1  3  2 ↑2"; !strings.Contains(l2, want) {
		t.Fatalf("line2 = %q, want git segment %q", l2, want)
	}
	if strings.ContainsAny(l2, "+~") {
		t.Fatalf("nerd-font line2 = %q, should not carry ASCII marks", l2)
	}
}

func TestDoneShipClauses(t *testing.T) {
	v := state.View{State: state.DoneNormal, Turn: transcript.Turn{
		Edited: []string{"a.go"}, Pushed: true, Deployed: true,
	}}
	got := Render(v, ctx())[0]
	if !strings.Contains(got, "Pushed.") || !strings.Contains(got, "Deploy triggered.") {
		t.Fatalf("done line = %q, want ship clauses", got)
	}
	v.Turn = transcript.Turn{Edited: []string{"a.go"}, Committed: true}
	got = Render(v, ctx())[0]
	if !strings.Contains(got, "Committed, not pushed.") {
		t.Fatalf("done line = %q, want committed-not-pushed nudge", got)
	}
}

func TestCompletionNudgeReadReceipt(t *testing.T) {
	c := ctx()
	doneAt := c.Now.Add(-2 * time.Minute)
	c.Siblings = []sessions.Beat{{SessionID: "x", State: "idle", Project: "web", Title: "Fix login", DoneSince: doneAt.Unix(), UpdatedAt: c.Now.Unix()}}

	// News arrived after the user's last prompt here: nudge shows.
	c.LastPromptAt = doneAt.Add(-10 * time.Minute)
	if got := Render(state.View{State: state.Idle}, c)[0]; !strings.Contains(got, "has new updates") {
		t.Fatalf("unseen completion should nudge, got %q", got)
	}
	// The user prompted this session AFTER the news existed: considered read.
	c.LastPromptAt = doneAt.Add(30 * time.Second)
	if got := Render(state.View{State: state.Idle}, c)[0]; strings.Contains(got, "has new updates") {
		t.Fatalf("acknowledged completion should stop nudging, got %q", got)
	}
	// Alerts are NOT receipt-cleared: a crashed sibling nags until fixed.
	c.Siblings = []sessions.Beat{{SessionID: "x", State: "failed", Project: "web", Title: "Fix login"}}
	if got := Render(state.View{State: state.Idle}, c)[0]; !strings.Contains(got, "crashed") {
		t.Fatalf("alert must persist regardless of receipt, got %q", got)
	}
}

func TestSameTitleSiblingSuppressed(t *testing.T) {
	c := ctx()
	c.SelfTitle = "Fix ccbit hook script file paths"
	c.ProjectLabel = "ccbit"
	// A heartbeat with this session's own title (dupe/resume/test pollution)
	// must never be named — not as a collision, not as anything.
	c.Siblings = []sessions.Beat{{SessionID: "other", State: "working", Project: "ccbit", Title: "Fix ccbit hook script file paths"}}
	if got := Render(state.View{State: state.Working}, c)[0]; strings.Contains(got, "also working") || strings.Contains(got, "Elsewhere") || strings.Contains(got, "session") {
		t.Fatalf("same-title sibling should be suppressed, got %q", got)
	}
	// A genuinely different session on the same repo still warns.
	c.Siblings = []sessions.Beat{{SessionID: "other", State: "working", Project: "ccbit", Title: "Refactor parser"}}
	if got := Render(state.View{State: state.Working}, c)[0]; !strings.Contains(got, "also working this repo") {
		t.Fatalf("distinct same-repo session should warn, got %q", got)
	}
}

func TestSameRepoCollision(t *testing.T) {
	c := ctx()
	c.ProjectLabel = "ccbit"
	c.Siblings = []sessions.Beat{{SessionID: "x", State: "working", Project: "ccbit", Title: "Refactor parser"}}
	got := Render(state.View{State: state.Working}, c)[0]
	if !strings.Contains(got, `"Refactor parser" is also working this repo`) {
		t.Fatalf("line1 = %q, want collision warning", got)
	}
	// Different repo: no warning (and a single benign sibling stays silent).
	c.Siblings = []sessions.Beat{{SessionID: "x", State: "working", Project: "other"}}
	if got = Render(state.View{State: state.Working}, c)[0]; strings.Contains(got, "also working") {
		t.Fatalf("line1 = %q, no collision expected", got)
	}
}

func TestGitSegmentColor(t *testing.T) {
	clearFeatureEnv(t)
	t.Setenv("CCBIT_GIT_COLOR", "1")
	c := ctx()
	c.ColorOn = true
	c.Git = gitx.Info{Branch: "main", New: 1, Modified: 3, Deleted: 2}
	l2 := Render(state.View{State: state.Idle}, c)[1]
	// New green, modified yellow, deleted red — each mark wrapped in its color.
	if !strings.Contains(l2, colorize(" +1", green)) ||
		!strings.Contains(l2, colorize(" ~3", yellow)) ||
		!strings.Contains(l2, colorize(" -2", red)) {
		t.Fatalf("colored git line2 = %q, want green/yellow/red marks", l2)
	}
}

func TestCtxGauge(t *testing.T) {
	clearFeatureEnv(t)
	t.Setenv("CCBIT_CTX_GAUGE", "1")
	c := ctx()
	pct := 38.0
	c.In.CtxPct = &pct
	if got := ctxSegment(c); !strings.Contains(got, "▆▆▁▁▁") || !strings.Contains(got, "38%") {
		t.Fatalf("ctx gauge = %q, want a 2/5 bar and 38%%", got)
	}
}

func TestRateSegmentColor(t *testing.T) {
	clearFeatureEnv(t)
	t.Setenv("CCBIT_RATE_COLOR", "1")
	hot := 94.0
	rl := &input.RateLimit{UsedPercentage: &hot}
	if got := rateSegment("7d", rl, time.Time{}, true); !strings.Contains(got, red) {
		t.Fatalf("hot rate segment = %q, want red", got)
	}
	warm := 78.0
	rl.UsedPercentage = &warm
	if got := rateSegment("5h", rl, time.Time{}, true); !strings.Contains(got, yellow) {
		t.Fatalf("warm rate segment = %q, want yellow", got)
	}
	cool := 12.0
	rl.UsedPercentage = &cool
	if got := rateSegment("5h", rl, time.Time{}, true); strings.Contains(got, "\x1b[") {
		t.Fatalf("healthy rate segment = %q, want no color", got)
	}
}

func TestIdleTipRotates(t *testing.T) {
	clearFeatureEnv(t) // all features off → all tips eligible
	c := ctx()
	// A showing slot (slot%tipShowEvery==0): first tip is the Nerd Font hint.
	c.Now = time.Unix(0, 0)
	if l1 := Render(state.View{State: state.Idle}, c)[0]; !strings.Contains(l1, "tip: set CCBIT_NERD_FONT=1") {
		t.Fatalf("idle line = %q, want Nerd Font tip", l1)
	}
	// A resting slot shows no tip.
	c.Now = time.Unix(tipPeriodSecs, 0) // slot 1, 1%3 != 0
	if l1 := Render(state.View{State: state.Idle}, c)[0]; strings.Contains(l1, "tip:") {
		t.Fatalf("resting-slot idle line = %q, want no tip", l1)
	}
	// A sibling to talk about preempts the tip entirely.
	c.Now = time.Unix(0, 0)
	c.Siblings = []sessions.Beat{{State: "failed", Project: "api"}}
	if l1 := Render(state.View{State: state.Idle}, c)[0]; strings.Contains(l1, "tip:") {
		t.Fatalf("idle-with-sibling line = %q, want no tip", l1)
	}
}
