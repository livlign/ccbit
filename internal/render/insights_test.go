package render

import (
	"strings"
	"testing"
	"time"

	"github.com/livlign/ccbit/internal/gitx"
	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func TestTaskClauseOnWorking(t *testing.T) {
	c := ctx()
	c.Tasks = transcript.TaskSummary{Total: 7, Done: 2, Current: "Wiring auth flow"}
	v := state.View{State: state.Working, Turn: transcript.Turn{Edited: []string{"D:/proj/a/x.go"}}}
	got := Render(v, c)[0]
	if !strings.Contains(got, "task 3/7: Wiring auth flow") {
		t.Fatalf("working line = %q, want task clause", got)
	}

	// No in-progress label: show plain progress while work remains.
	c.Tasks = transcript.TaskSummary{Total: 4, Done: 1}
	got = Render(v, c)[0]
	if !strings.Contains(got, "tasks 1/4 done") {
		t.Fatalf("working line = %q, want tasks 1/4 done", got)
	}

	// All done or no plan: silent.
	c.Tasks = transcript.TaskSummary{Total: 4, Done: 4}
	if got = Render(v, c)[0]; strings.Contains(got, "task") {
		t.Fatalf("completed plan should be silent, got %q", got)
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

func TestGitSegment(t *testing.T) {
	c := ctx()
	c.Git = gitx.Info{Branch: "main", Dirty: 3, Ahead: 2}
	l2 := Render(state.View{State: state.Idle}, c)[1]
	if !strings.Contains(l2, "main* ↑2") {
		t.Fatalf("line2 = %q, want git segment main* ↑2", l2)
	}
	c.Git = gitx.Info{Branch: "main"}
	l2 = Render(state.View{State: state.Idle}, c)[1]
	if !strings.Contains(l2, "main") || strings.Contains(l2, "main*") {
		t.Fatalf("clean repo line2 = %q, want bare branch", l2)
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
	c.Siblings = []sessions.Beat{{SessionID: "x", State: "idle", Project: "web", Title: "Fix login", DoneSince: doneAt.Unix()}}

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
