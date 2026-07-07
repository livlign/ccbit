package render

import (
	"strings"
	"testing"
	"time"

	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func TestSiblingClauseAlerts(t *testing.T) {
	c := ctx()
	c.Siblings = []sessions.Beat{
		{State: "failed", Project: "api"},
		{State: "waiting", Project: "web"},
		{State: "working", Project: "lib"},
	}
	got := siblingClause(c)
	want := "Elsewhere: api crashed, web needs you"
	if got != want {
		t.Fatalf("siblingClause = %q, want %q", got, want)
	}
}

func TestSiblingClauseUsesTitle(t *testing.T) {
	c := ctx()
	c.Siblings = []sessions.Beat{{State: "failed", Project: "api", Title: "Fix login bug"}}
	got := siblingClause(c)
	want := `The session "Fix login bug" crashed`
	if got != want {
		t.Fatalf("titled siblingClause = %q, want %q", got, want)
	}
}

func TestSiblingClauseBenignOnly(t *testing.T) {
	c := ctx()
	// Two or more benign sessions are worth a light note.
	c.Siblings = []sessions.Beat{
		{State: "working", Project: "a"},
		{State: "idle", Project: "b"},
	}
	if got := siblingClause(c); got != "2 other sessions running" {
		t.Fatalf("benign siblingClause = %q, want %q", got, "2 other sessions running")
	}
	// A lone benign session is not mentioned at all.
	c.Siblings = []sessions.Beat{{State: "working", Project: "a"}}
	if got := siblingClause(c); got != "" {
		t.Fatalf("single benign should be silent, got %q", got)
	}
}

func TestSiblingClauseCapsNamed(t *testing.T) {
	c := ctx()
	c.Siblings = []sessions.Beat{
		{State: "failed", Project: "a"},
		{State: "failed", Project: "b"},
		{State: "failed", Project: "c"},
	}
	got := siblingClause(c)
	if !strings.Contains(got, "and 1 more") {
		t.Fatalf("expected truncation marker in %q", got)
	}
	if strings.Contains(got, "c crashed") {
		t.Fatalf("third actionable should be folded, got %q", got)
	}
}

func TestSiblingClauseEmpty(t *testing.T) {
	if got := siblingClause(ctx()); got != "" {
		t.Fatalf("no siblings should yield empty clause, got %q", got)
	}
}

// Siblings ride line 1, appended after this session's state message.
func TestLine1ShowsSiblings(t *testing.T) {
	c := ctx()
	c.Siblings = []sessions.Beat{{State: "failed", Project: "api"}}
	v := state.View{State: state.Idle}
	l1 := Render(v, c)[0]
	// The idle face rotates per turn, so assert on the face this turn's seed
	// selects plus the sibling clause that should ride line 1 after it.
	face := idleFace(faceSeed(c.In.SessionID, v.Turn), c.Narrow)
	want := face + " idle · The session api crashed"
	if l1 != want {
		t.Fatalf("line1 with sibling = %q, want %q", l1, want)
	}
}

func TestLinesDelta(t *testing.T) {
	c := ctx()
	c.TurnLinesAdded, c.TurnLinesRemoved = 182, 47
	if got := linesDelta(c); got != "+182/-47" {
		t.Fatalf("linesDelta = %q, want %q", got, "+182/-47")
	}
	c.TurnLinesAdded, c.TurnLinesRemoved = 0, 0
	if got := linesDelta(c); got != "" {
		t.Fatalf("zero diff should be empty, got %q", got)
	}
}

func TestDoneSentence(t *testing.T) {
	c := ctx()
	c.TurnLinesAdded, c.TurnLinesRemoved = 885, 99
	v := state.View{
		State: state.DoneNormal,
		Turn: transcript.Turn{
			Edited: []string{"a.go", "b.go", "c.go", "d.go"},
			Builds: []transcript.BuildResult{{Kind: "build"}, {Kind: "test"}},
		},
	}
	got := Render(v, c)[0]
	want := "(つ•‿•)つ 4 files edited, line changes: +885/-99. Build succeeded. Tests succeeded."
	if got != want {
		t.Fatalf("done line1 = %q, want %q", got, want)
	}

	// Working never mentions lines (a climbing mid-turn diff is noise).
	work := Render(state.View{State: state.Working, Turn: transcript.Turn{Edited: []string{"x/a.go"}}}, c)[0]
	if strings.Contains(work, "line") {
		t.Fatalf("working line1 = %q, should not show lines", work)
	}
}

func TestSiblingClauseCompletion(t *testing.T) {
	c := ctx()
	now := c.Now
	// A sibling that just finished a turn gets a full sentence and the nudge.
	c.Siblings = []sessions.Beat{
		{State: "idle", Project: "web", Title: "Read and review project", DoneSince: now.Add(-30 * time.Second).Unix(), UpdatedAt: now.Unix()},
	}
	want := `The session "Read and review project" has new updates — take a look`
	if got := siblingClause(c); got != want {
		t.Fatalf("completion clause = %q, want %q", got, want)
	}

	// An alert alongside a completion: alert leads, no nudge.
	c.Siblings = []sessions.Beat{
		{State: "failed", Project: "api"},
		{State: "done", Project: "web", DoneSince: now.Add(-30 * time.Second).Unix(), UpdatedAt: now.Unix()},
	}
	if got := siblingClause(c); got != "Elsewhere: api crashed, web has new updates" {
		t.Fatalf("mixed clause = %q", got)
	}

	// A stale completion (older than the window) is benign; a lone one is silent.
	c.Siblings = []sessions.Beat{
		{State: "idle", Project: "web", DoneSince: now.Add(-30 * time.Minute).Unix(), UpdatedAt: now.Unix()},
	}
	if got := siblingClause(c); got != "" {
		t.Fatalf("stale lone completion should be silent, got %q", got)
	}

	// A closed session (heartbeat stopped) must not nudge, however fresh the
	// completion — there is no window left to take a look at.
	c.Siblings = []sessions.Beat{
		{State: "idle", Project: "web", DoneSince: now.Add(-30 * time.Second).Unix(), UpdatedAt: now.Add(-2 * time.Minute).Unix()},
	}
	if got := siblingClause(c); got != "" {
		t.Fatalf("dead session should not nudge, got %q", got)
	}

	// Alerts are exempt from liveness: a crashed session needs you precisely
	// because it stopped beating.
	c.Siblings = []sessions.Beat{
		{State: "failed", Project: "api", UpdatedAt: now.Add(-2 * time.Minute).Unix()},
	}
	if got := siblingClause(c); got != "api crashed" && !strings.Contains(got, "crashed") {
		t.Fatalf("stale alert should still show, got %q", got)
	}
}

func TestSiblingClauseStalledAgesOut(t *testing.T) {
	c := ctx()
	now := c.Now
	// A freshly stalled session is still worth naming.
	c.Siblings = []sessions.Beat{
		{State: "stopped", Project: "api", Title: "Set up cd alias for current folder",
			LastActiveAt: now.Add(-2 * time.Minute).Unix(), UpdatedAt: now.Unix()},
	}
	want := `The session "Set up cd alias for current folder" stalled`
	if got := siblingClause(c); got != want {
		t.Fatalf("fresh stalled clause = %q, want %q", got, want)
	}

	// The same session left open all morning keeps beating (UpdatedAt fresh) but
	// hasn't made progress in hours — it must drop off line 2, not nag forever.
	c.Siblings[0].LastActiveAt = now.Add(-3 * time.Hour).Unix()
	if got := siblingClause(c); got != "" {
		t.Fatalf("long-stalled session should age out, got %q", got)
	}

	// An alert (failed/waiting) is exempt: it genuinely needs you, however old.
	c.Siblings = []sessions.Beat{
		{State: "failed", Project: "api", LastActiveAt: now.Add(-3 * time.Hour).Unix(), UpdatedAt: now.Unix()},
	}
	if got := siblingClause(c); !strings.Contains(got, "crashed") {
		t.Fatalf("old alert should still show, got %q", got)
	}
}

func TestLongerThanUsual(t *testing.T) {
	c := ctx()
	c.TypicalTurn = time.Minute
	// A turn well past 2x the norm gets the subtle note.
	long := Render(state.View{State: state.Working, HasElapsed: true, Elapsed: 3 * time.Minute}, c)[0]
	if !strings.Contains(long, "(longer than usual)") {
		t.Fatalf("long turn line1 = %q, want the note", long)
	}
	// A normal-length turn stays quiet.
	short := Render(state.View{State: state.Working, HasElapsed: true, Elapsed: 30 * time.Second}, c)[0]
	if strings.Contains(short, "longer than usual") {
		t.Fatalf("normal turn line1 = %q, should be quiet", short)
	}
	// No learned baseline yet -> never fires.
	c.TypicalTurn = 0
	cold := Render(state.View{State: state.Working, HasElapsed: true, Elapsed: 9 * time.Minute}, c)[0]
	if strings.Contains(cold, "longer than usual") {
		t.Fatalf("without a baseline line1 = %q, should be quiet", cold)
	}
}

func TestRecoveryGreenAgain(t *testing.T) {
	c := ctx()
	v := state.View{
		State: state.DoneRedeemed,
		Turn: transcript.Turn{
			Edited: []string{"a.go"},
			Builds: []transcript.BuildResult{{Kind: "build", IsError: true}, {Kind: "build", IsError: false}},
		},
	}
	got := Render(v, c)[0]
	if !strings.Contains(got, "Build green again.") {
		t.Fatalf("recovery line1 = %q, want 'Build green again.'", got)
	}
	if strings.Contains(got, "Recovered") {
		t.Fatalf("recovery line1 = %q, should use subtle wording, not 'Recovered'", got)
	}
}

func TestTrendArrow(t *testing.T) {
	cases := map[sessions.Trend]string{
		sessions.TrendUp:   " ↑",
		sessions.TrendDown: " ↓",
		sessions.TrendFlat: "",
		sessions.TrendNone: "",
	}
	for tr, want := range cases {
		if got := trendArrow(tr); got != want {
			t.Fatalf("trendArrow(%v) = %q, want %q", tr, got, want)
		}
	}
}

func TestCtxSegmentTrend(t *testing.T) {
	c := ctx()
	pct := 38.0
	c.In.CtxPct = &pct
	c.Trend = sessions.TrendUp
	if got := ctxSegment(c); got != "ctx 38% ↑" {
		t.Fatalf("ctxSegment with trend = %q, want %q", got, "ctx 38% ↑")
	}
}

// guard: line1Color reuse for sibling states stays in sync.
func TestSiblingStateMapping(t *testing.T) {
	if siblingState("failed") != state.Failed || siblingState("waiting") != state.Waiting || siblingState("stopped") != state.Stopped {
		t.Fatal("siblingState mapping drifted from state constants")
	}
}
