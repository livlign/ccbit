package sessions

import (
	"testing"
	"time"
)

// isolate points the home dir at a temp dir so Record/Active touch only test
// state. USERPROFILE is set too because os.UserHomeDir() uses it (not HOME) on
// Windows — without it the test reads the real ~/.claude/ccbit/sessions dir.
func isolate(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
}

func TestRecordTrend(t *testing.T) {
	isolate(t)
	base := time.Unix(1_700_000_000, 0)

	// Climbing context across a > minSpan window reads as TrendUp.
	id := "s1"
	for i, pct := range []float64{10, 20, 30, 40} {
		at := base.Add(time.Duration(i) * 10 * time.Second)
		got := Record(Beat{SessionID: id, State: "working"}, &pct, at)
		_ = got // trend only meaningful once enough span has accrued
	}
	last := 50.0
	if got := Record(Beat{SessionID: id, State: "working"}, &last, base.Add(40*time.Second)); got != TrendUp {
		t.Fatalf("rising ctx trend = %v, want TrendUp", got)
	}
}

func TestRecordFlatAndDown(t *testing.T) {
	isolate(t)
	base := time.Unix(1_700_000_000, 0)

	// Flat: same pct over the window.
	for i := 0; i < 5; i++ {
		pct := 50.0
		Record(Beat{SessionID: "flat", State: "idle"}, &pct, base.Add(time.Duration(i)*10*time.Second))
	}
	pct := 50.0
	if got := Record(Beat{SessionID: "flat", State: "idle"}, &pct, base.Add(50*time.Second)); got != TrendFlat {
		t.Fatalf("flat ctx trend = %v, want TrendFlat", got)
	}

	// Falling (e.g. after a compaction) reads as TrendDown.
	for i, p := range []float64{80, 60, 40} {
		pp := p
		Record(Beat{SessionID: "drop", State: "idle"}, &pp, base.Add(time.Duration(i)*10*time.Second))
	}
	low := 20.0
	if got := Record(Beat{SessionID: "drop", State: "idle"}, &low, base.Add(30*time.Second)); got != TrendDown {
		t.Fatalf("falling ctx trend = %v, want TrendDown", got)
	}
}

func TestRecordTrendNoneEarly(t *testing.T) {
	isolate(t)
	now := time.Unix(1_700_000_000, 0)
	pct := 42.0
	if got := Record(Beat{SessionID: "new", State: "working"}, &pct, now); got != TrendNone {
		t.Fatalf("single-sample trend = %v, want TrendNone", got)
	}
	// nil ctx (unknown) never trends.
	if got := Record(Beat{SessionID: "new2", State: "working"}, nil, now); got != TrendNone {
		t.Fatalf("nil-ctx trend = %v, want TrendNone", got)
	}
}

func TestActiveFiltersAndSorts(t *testing.T) {
	isolate(t)
	now := time.Unix(1_700_000_000, 0)
	p := 30.0

	Record(Beat{SessionID: "self", State: "working", Project: "me"}, &p, now)
	Record(Beat{SessionID: "wait", State: "waiting", Project: "web"}, &p, now)
	Record(Beat{SessionID: "fail", State: "failed", Project: "api"}, &p, now)
	Record(Beat{SessionID: "busy", State: "working", Project: "lib"}, &p, now)
	// A stale sibling (older than activeWindow) must be excluded.
	Record(Beat{SessionID: "old", State: "failed", Project: "ghost"}, &p, now.Add(-10*time.Minute))

	got := Active("self", now)
	if len(got) != 3 {
		t.Fatalf("active count = %d (%v), want 3 (self + stale excluded)", len(got), got)
	}
	// Actionable first: failed, then waiting, then the benign working session.
	if got[0].State != "failed" || got[1].State != "waiting" || got[2].State != "working" {
		t.Fatalf("active order = [%s %s %s], want [failed waiting working]", got[0].State, got[1].State, got[2].State)
	}
}

func TestCompletionStampLifecycle(t *testing.T) {
	isolate(t)
	base := time.Unix(1_700_000_000, 0)
	p := 30.0
	id := "s"

	// Working: no completion stamp.
	Record(Beat{SessionID: id, State: "working"}, &p, base)
	// Transition working -> idle stamps completion at this moment.
	finished := base.Add(20 * time.Second)
	Record(Beat{SessionID: id, State: "idle"}, &p, finished)
	b, _ := loadPath(path(id))
	if b.DoneSince != finished.Unix() {
		t.Fatalf("DoneSince = %d, want %d (working->idle stamp)", b.DoneSince, finished.Unix())
	}
	if !JustCompleted(b, finished.Add(30*time.Second)) {
		t.Fatal("should read as just-completed while still beating")
	}
	// That beat is from `finished`; a minute later with no fresh beat the
	// session is gone, and a dead session's completion earns no nudge.
	if JustCompleted(b, finished.Add(time.Minute)) {
		t.Fatal("a session that stopped beating must not nudge")
	}

	// Staying at rest carries the stamp forward (does not refresh it).
	Record(Beat{SessionID: id, State: "idle"}, &p, finished.Add(time.Minute))
	b, _ = loadPath(path(id))
	if b.DoneSince != finished.Unix() {
		t.Fatalf("DoneSince drifted to %d while resting; should hold %d", b.DoneSince, finished.Unix())
	}

	// Working again (you prompted it) clears the stamp.
	Record(Beat{SessionID: id, State: "working"}, &p, finished.Add(2*time.Minute))
	b, _ = loadPath(path(id))
	if b.DoneSince != 0 {
		t.Fatalf("DoneSince = %d after resuming work, want 0", b.DoneSince)
	}
}

func TestActiveRanksCompletionAfterAlerts(t *testing.T) {
	isolate(t)
	now := time.Unix(1_700_000_000, 0)
	p := 30.0
	// Seed a "just finished" sibling: working then idle.
	Record(Beat{SessionID: "fin", State: "working", Project: "web"}, &p, now.Add(-20*time.Second))
	Record(Beat{SessionID: "fin", State: "idle", Project: "web"}, &p, now)
	Record(Beat{SessionID: "fail", State: "failed", Project: "api"}, &p, now)
	Record(Beat{SessionID: "busy", State: "working", Project: "lib"}, &p, now)

	got := Active("self", now)
	if len(got) != 3 {
		t.Fatalf("active count = %d, want 3", len(got))
	}
	// Alert first, then the completion, then the still-busy session.
	if got[0].State != "failed" || !JustCompleted(got[1], now) || got[2].State != "working" {
		t.Fatalf("order = [%s, just=%v, %s], want [failed, completion, working]",
			got[0].State, JustCompleted(got[1], now), got[2].State)
	}
}

func TestActionable(t *testing.T) {
	for _, s := range []string{"failed", "waiting", "stopped"} {
		if !Actionable(s) {
			t.Fatalf("Actionable(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"working", "idle", "done", "agents"} {
		if Actionable(s) {
			t.Fatalf("Actionable(%q) = true, want false", s)
		}
	}
}

func TestRosterVisible(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh := now.Add(-5 * time.Minute).Unix()
	stale := now.Add(-11 * time.Minute).Unix()

	// done/redeemed/stopped drop off once their last activity is past restRosterTTL.
	for _, s := range []string{"done", "redeemed", "stopped"} {
		if !RosterVisible(Beat{State: s, UpdatedAt: now.Unix(), LastActiveAt: fresh}, now) {
			t.Errorf("%q with fresh activity should be visible", s)
		}
		// Still beating (UpdatedAt = now) but last active long ago: must be hidden.
		if RosterVisible(Beat{State: s, UpdatedAt: now.Unix(), LastActiveAt: stale}, now) {
			t.Errorf("%q stale-but-beating should be hidden", s)
		}
	}

	// Live work and unanswered alerts are never hidden, however old.
	for _, s := range []string{"working", "agents", "waiting", "failed", "idle"} {
		if !RosterVisible(Beat{State: s, UpdatedAt: now.Unix(), LastActiveAt: stale}, now) {
			t.Errorf("%q should always be visible", s)
		}
	}
}
