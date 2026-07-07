package render

import (
	"strings"
	"testing"
	"time"

	"github.com/livlign/ccbit/internal/sessions"
)

func TestRosterEmpty(t *testing.T) {
	got := Roster(nil, time.Unix(1000, 0), false)
	if len(got) != 1 || !strings.Contains(got[0], "No live") {
		t.Fatalf("empty roster = %q, want a single 'No live...' line", got)
	}
}

func TestRosterRows(t *testing.T) {
	now := time.Unix(10_000, 0)
	beats := []sessions.Beat{
		{State: "failed", Project: "api", Title: "Fix login bug", UpdatedAt: now.Add(-3 * time.Second).Unix()},
		{State: "waiting", Project: "web", Title: "Redesign nav", UpdatedAt: now.Add(-5 * time.Second).Unix()},
		{State: "idle", Project: "docs", UpdatedAt: now.Add(-30 * time.Second).Unix()},
	}
	got := Roster(beats, now, false)
	if len(got) != 4 { // header + 3 rows
		t.Fatalf("got %d lines, want 4:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.HasPrefix(got[0], "STATE") {
		t.Fatalf("first line should be the header, got %q", got[0])
	}
	// State words use the line-1 vocabulary; ages and titles are present.
	if !strings.Contains(got[1], "failed") || !strings.Contains(got[1], "Fix login bug") {
		t.Errorf("failed row = %q", got[1])
	}
	if !strings.Contains(got[2], "needs you") {
		t.Errorf("waiting row should read 'needs you', got %q", got[2])
	}
	// A titleless session shows a dash, not an empty cell.
	if !strings.Contains(got[3], "—") {
		t.Errorf("titleless row should show a dash, got %q", got[3])
	}
}

// A heartbeat that has stopped advancing (terminal closed) must not pose as its
// frozen live state — the roster relabels it "stale" and dims it, until it ages
// out of Active entirely. Guards the closed-tab bug.
func TestRosterStale(t *testing.T) {
	now := time.Unix(10_000, 0)
	beats := []sessions.Beat{
		{State: "idle", Project: "docs", Title: "Old session", UpdatedAt: now.Add(-2 * time.Minute).Unix()},
	}
	plain := Roster(beats, now, false)[1]
	if !strings.Contains(plain, "stale") || strings.Contains(plain, "idle") {
		t.Errorf("stale row should read 'stale', not 'idle', got %q", plain)
	}
	if !strings.Contains(plain, "2m") {
		t.Errorf("stale row should still show its (frozen) age, got %q", plain)
	}
	colored := Roster(beats, now, true)[1]
	if !strings.Contains(colored, dim) {
		t.Errorf("stale row should be dimmed, got %q", colored)
	}
}

// AGE measures how long the session has been like this, not the ~1s gap since
// the last render: a live session that stalled hours ago still beats every
// second (UpdatedAt ≈ now), so AGE must come from LastActiveAt. Guards the
// "stalled for hours, AGE 0s forever" bug.
func TestRosterAgeFromLastActive(t *testing.T) {
	now := time.Unix(100_000, 0)
	beats := []sessions.Beat{
		{
			State:        "stopped",
			Project:      "api2-zero",
			Title:        "Set up cd alias",
			UpdatedAt:    now.Unix(), // still beating: terminal open
			LastActiveAt: now.Add(-3 * time.Hour).Unix(),
		},
	}
	row := Roster(beats, now, false)[1]
	if !strings.Contains(row, "3h") {
		t.Errorf("stalled row should show time since last activity (3h), got %q", row)
	}
	if strings.Contains(row, "0s") {
		t.Errorf("AGE must not collapse to 0s for a live-but-stalled session, got %q", row)
	}
}

func TestRosterColorsStateCellOnly(t *testing.T) {
	now := time.Unix(10_000, 0)
	beats := []sessions.Beat{{State: "failed", Project: "api", Title: "x", UpdatedAt: now.Unix()}}
	row := Roster(beats, now, true)[1]
	if !strings.Contains(row, red) {
		t.Fatalf("colored roster row should color the state cell, got %q", row)
	}
	// Exactly one color span: the rest of the row is plain.
	if n := strings.Count(row, reset); n != 1 {
		t.Fatalf("want exactly one reset (state cell only), got %d in %q", n, row)
	}
}

func TestRosterStatusWords(t *testing.T) {
	cases := map[string]string{
		"failed": "failed", "stopped": "stalled", "waiting": "needs you",
		"working": "working", "agents": "agents", "done": "done",
		"redeemed": "done", "idle": "idle", "unknown-thing": "idle",
	}
	for in, want := range cases {
		if got := rosterStatus(in); got != want {
			t.Errorf("rosterStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
