// Package sessions persists a tiny per-session heartbeat so ccbit can do two
// things a stateless, single-transcript renderer cannot:
//
//   - cross-session awareness: each render writes this session's state to a
//     shared dir and reads its siblings', so line 2 can flag "another window is
//     waiting / failed" — the one thing a status line can't learn from its own
//     transcript.
//   - context velocity: the heartbeat carries a short rolling window of ctx%
//     samples, so the renderer can show whether context is climbing (↑) or
//     dropping after a compaction (↓) rather than just a static number.
//
// The heartbeat is disposable renderer state, never authoritative: every file
// op fails silently, and the whole dir can be deleted at any time.
package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// activeWindow bounds how recent a sibling heartbeat must be to count as a
	// live session. A closed session stops writing and drops off within this gap.
	activeWindow = 3 * time.Minute
	// hardTTL prunes long-dead heartbeats so the dir never grows without bound.
	hardTTL = 24 * time.Hour
	// sampleInterval is the minimum gap between stored ctx samples (the line may
	// render ~1×/s; we don't need that density to see a trend).
	sampleInterval = 5 * time.Second
	// sampleWindow is how far back velocity looks.
	sampleWindow = 90 * time.Second
	// minSpan is the minimum sample span before a trend is trustworthy.
	minSpan = 15 * time.Second
	// trendRate is the |%/min| slope above which ctx is reported rising/falling.
	trendRate = 2.5
	// doneWindow is how long after a session stops working Bit keeps nudging
	// "come take a look" at a sibling. It self-clears earlier when you switch to
	// that session and prompt it (working again resets the completion stamp).
	doneWindow = 10 * time.Minute
	// liveWindow bounds how stale a heartbeat can be while its completion still
	// earns a nudge. Beats land ~1×/s while a session runs, so a closed terminal
	// goes silent within this window instead of riding out the full activeWindow
	// — there is nothing left to "take a look" at. Alerts are exempt: a crashed
	// session needs you precisely because it stopped beating.
	liveWindow = 45 * time.Second
)

// State classes group the eight derived states by what they mean for a sibling
// digest: busy (still working), resting (a turn finished), or alert (needs you).
const (
	classResting = iota // idle / done / redeemed, and unknown
	classBusy           // working / agents
	classAlert          // waiting / failed / stopped
)

func stateClass(s string) int {
	switch s {
	case "working", "agents":
		return classBusy
	case "waiting", "failed", "stopped":
		return classAlert
	default: // idle, done, redeemed, unknown
		return classResting
	}
}

// Trend is the direction of context-window growth over the recent window.
type Trend int

const (
	TrendNone Trend = iota // not enough data
	TrendFlat
	TrendUp
	TrendDown
)

type sample struct {
	At  int64   `json:"at"` // unix seconds
	Pct float64 `json:"pct"`
}

// Beat is one session's heartbeat: enough for a sibling to render a digest, plus
// the ctx sample history this session uses for its own velocity readout.
type Beat struct {
	SessionID string   `json:"session_id"`
	State     string   `json:"state"`   // state.State.String()
	Project   string   `json:"project"` // short label (repo/dir basename)
	Title     string   `json:"title"`   // session's ai-title, how Bit names it to siblings
	UpdatedAt int64    `json:"updated_at"`
	CtxPct    float64  `json:"ctx_pct"` // -1 when unknown
	Samples   []sample `json:"samples,omitempty"`
	// DoneSince is when this session last stopped working (working/agents ->
	// resting). It's the "a turn just finished" signal a sibling uses to nudge
	// "come take a look". 0 while busy or before any work has completed.
	DoneSince int64 `json:"done_since,omitempty"`
	// LastTurnStart is the start (unix) of the most recent turn already folded
	// into the durable memory store — a high-water mark so a turn is counted once
	// across the per-second renders, not on every repaint.
	LastTurnStart int64 `json:"last_turn_start,omitempty"`
	// LinesBase* snapshot the session-cumulative lines-changed counters (stdin
	// cost.total_lines_*) as of the start of the turn LinesBaseTurn (unix), so
	// the renderer can show this turn's delta instead of the whole-session total.
	LinesBaseTurn    int64 `json:"lines_base_turn,omitempty"`
	LinesBaseAdded   int   `json:"lines_base_added,omitempty"`
	LinesBaseRemoved int   `json:"lines_base_removed,omitempty"`
}

// Snapshot returns this session's last-written heartbeat, or a zero Beat if none
// exists. Used to read forward-carried fields (the memory high-water mark)
// before computing the next beat.
func Snapshot(id string) Beat {
	b, _ := loadPath(path(id))
	return b
}

func dir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "ccbit", "sessions")
	}
	return filepath.Join(os.TempDir(), "ccbit", "sessions")
}

func path(id string) string { return filepath.Join(dir(), id+".json") }

func loadPath(p string) (Beat, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return Beat{}, false
	}
	var beat Beat
	if json.Unmarshal(b, &beat) != nil {
		return Beat{}, false
	}
	return beat, true
}

// Record writes this session's heartbeat for the current render and returns the
// context-velocity trend. ctxPct is nil when context usage is unknown (early in
// a session or just after /compact). It rolls the prior render's ctx samples
// forward, trims them to the velocity window, and persists the result. All I/O
// is best-effort; a write failure costs only the trend on this render.
func Record(b Beat, ctxPct *float64, now time.Time) Trend {
	if b.SessionID == "" {
		return TrendNone
	}
	prev, _ := loadPath(path(b.SessionID))

	b.UpdatedAt = now.Unix()
	b.CtxPct = -1
	if ctxPct != nil {
		b.CtxPct = *ctxPct
	}
	b.Samples = rollSamples(prev.Samples, ctxPct, now)
	b.DoneSince = completionStamp(prev, b.State, now)
	if b.Title == "" {
		b.Title = prev.Title // the ai-title scrolls out of the tail; keep it once seen
	}

	write(b)
	return trendOf(b.Samples, now)
}

// completionStamp tracks the working->resting transition. It sets "now" the
// moment a busy session goes to rest (a turn just finished), carries that stamp
// forward while it stays at rest, and clears to 0 when it's busy again (you
// prompted it) or in an alert state (which speaks for itself).
func completionStamp(prev Beat, newState string, now time.Time) int64 {
	switch stateClass(newState) {
	case classResting:
		if stateClass(prev.State) == classBusy {
			return now.Unix()
		}
		return prev.DoneSince // carry forward (0 if it never worked)
	default: // busy or alert
		return 0
	}
}

// JustCompleted reports whether this session finished a turn recently enough to
// still be worth a "come take a look" nudge — and is still alive to look at.
func JustCompleted(b Beat, now time.Time) bool {
	if now.Sub(time.Unix(b.UpdatedAt, 0)) > liveWindow {
		return false
	}
	return b.DoneSince > 0 && now.Sub(time.Unix(b.DoneSince, 0)) <= doneWindow
}

// Notable reports whether a sibling is worth naming on line 1 — either it needs
// attention (alert) or it just finished a turn. Everything else folds into a
// plain live-session count.
func Notable(b Beat, now time.Time) bool {
	return Actionable(b.State) || JustCompleted(b, now)
}

func rollSamples(prev []sample, ctxPct *float64, now time.Time) []sample {
	s := prev
	if ctxPct != nil {
		nowSec := now.Unix()
		if len(s) == 0 || nowSec-s[len(s)-1].At >= int64(sampleInterval.Seconds()) {
			s = append(s, sample{At: nowSec, Pct: *ctxPct})
		}
	}
	cutoff := now.Add(-sampleWindow).Unix()
	drop := 0
	for drop < len(s) && s[drop].At < cutoff {
		drop++
	}
	// Keep at least the last two samples so a trend survives a quiet stretch.
	if drop > 0 && len(s)-drop < 2 {
		if len(s) >= 2 {
			drop = len(s) - 2
		} else {
			drop = 0
		}
	}
	return append([]sample(nil), s[drop:]...)
}

func trendOf(s []sample, now time.Time) Trend {
	if len(s) < 2 {
		return TrendNone
	}
	first, last := s[0], s[len(s)-1]
	span := last.At - first.At
	if span < int64(minSpan.Seconds()) {
		return TrendNone
	}
	slope := (last.Pct - first.Pct) / (float64(span) / 60.0) // %/min
	switch {
	case slope >= trendRate:
		return TrendUp
	case slope <= -trendRate:
		return TrendDown
	default:
		return TrendFlat
	}
}

func write(b Beat) {
	d := dir()
	if os.MkdirAll(d, 0o755) != nil {
		return
	}
	data, err := json.Marshal(b)
	if err != nil {
		return
	}
	// Write-then-rename so a concurrent sibling never reads a half-written file.
	tmp := path(b.SessionID) + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path(b.SessionID))
}

// Active returns the live sibling heartbeats (excluding selfID), sorted with the
// actionable states first (failed, then stopped, then waiting), then by project.
// It also prunes heartbeats older than hardTTL as a cheap housekeeping pass.
func Active(selfID string, now time.Time) []Beat {
	ents, err := os.ReadDir(dir())
	if err != nil {
		return nil
	}
	var out []Beat
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		p := filepath.Join(dir(), name)
		b, ok := loadPath(p)
		if !ok {
			continue
		}
		age := now.Sub(time.Unix(b.UpdatedAt, 0))
		if age > hardTTL {
			_ = os.Remove(p)
			continue
		}
		if b.SessionID == selfID || age > activeWindow {
			continue
		}
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i], now), rank(out[j], now)
		if ri != rj {
			return ri < rj
		}
		return out[i].Project < out[j].Project
	})
	return out
}

// rank orders the sibling digest: alerts first (by severity), then sessions
// that just finished a turn, then everything else.
func rank(b Beat, now time.Time) int {
	if r := severity(b.State); r < 9 {
		return r
	}
	if JustCompleted(b, now) {
		return 3
	}
	return 9
}

// severity ranks states for the sibling digest: lower sorts first. Actionable
// states (something needs you or broke) lead; everything else is benign.
func severity(s string) int {
	switch s {
	case "failed":
		return 0
	case "stopped":
		return 1
	case "waiting":
		return 2
	default:
		return 9
	}
}

// Actionable reports whether a sibling state is worth surfacing by name rather
// than folding into a plain active count.
func Actionable(s string) bool { return severity(s) < 9 }
