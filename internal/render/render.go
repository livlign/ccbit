// Package render turns a derived state View into the one-to-three lines printed
// to the status line: a reactive face-led line, an ambient context line, and an
// optional catch-up recap.
package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/livlign/ccbit/internal/input"
	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

// NarrowCols is the COLUMNS threshold below which risky wide glyphs fall back to
// single-width-safe forms.
const NarrowCols = 60

// Ctx carries per-render environment the line builders need.
type Ctx struct {
	In       input.Stdin
	RepoRoot string // git toplevel, or "" if unknown
	Cols     int    // terminal width from COLUMNS, 0 if unset
	Narrow   bool
	Frame    int // 0 or 1, wall-clock selected
	ColorOn  bool
	Now      time.Time

	Trend    sessions.Trend  // context-window velocity for the ctx% segment
	Siblings []sessions.Beat // other live sessions, actionable-first

	// TypicalTurn is the project's learned mean turn duration (0 if not yet
	// learned), used to flag a turn running unusually long.
	TypicalTurn time.Duration
}

// Face returns the kaomoji for a state. Working and Agents animate via Frame;
// every other face is static. Narrow swaps risky glyphs for safe fallbacks.
func Face(s state.State, frame int, narrow bool) string {
	switch s {
	case state.Working:
		// Arms flat -> raised + mouth opening: pure-ASCII body language that reads
		// as active work and renders on every font (the v1 triangles ◣◢ are
		// Geometric-Shapes glyphs that tofu/mangle on Windows).
		if frame == 0 {
			return "-(๏_๏)-"
		}
		return "৲(๏_๏)৲"
	case state.Agents:
		if narrow {
			if frame == 0 {
				return "(•_•)>"
			}
			return "<(•_•)"
		}
		if frame == 0 {
			return "┏(•_•)┛"
		}
		return "┗(•_•)┓"
	case state.DoneNormal:
		if narrow {
			return "(•‿•)"
		}
		return "(つ•‿•)つ"
	case state.DoneRedeemed:
		return `(→_←")`
	case state.Waiting:
		return "(◕_◕)?"
	case state.Failed:
		if narrow {
			return "(>_<) FAILED"
		}
		return "(╯°□°)╯︵ ┻━┻"
	case state.Stopped:
		return "(¬°-°)¬"
	default: // Idle
		return "(•_•)"
	}
}

// Render builds all output lines for the view. Line 1 (the reactive line) is
// Bit narrating this session — its state, what it changed, and a word about any
// sibling sessions — colored as a whole by state; line 2 is ambient context.
func Render(v state.View, c Ctx) []string {
	l1 := Face(v.State, c.Frame, c.Narrow) + " " + line1(v, c)
	if c.ColorOn {
		l1 = colorize(l1, line1Color(v.State))
	}
	// The sibling clause is appended outside the whole-line color so its own
	// per-severity colors survive (an embedded reset would otherwise truncate
	// the state color for the rest of the line).
	if clause := siblingClause(c); clause != "" {
		l1 += " · " + clause
	}
	return []string{l1, line2(c)}
}

// line1Color maps a state to its whole-line color. Alert states stand out (red
// for failed, bright red for stopped, yellow for needs-you); active and resting
// states are white; done is green.
func line1Color(s state.State) string {
	switch s {
	case state.Failed:
		return red
	case state.Stopped:
		return brightRed
	case state.Waiting:
		return yellow
	case state.DoneNormal, state.DoneRedeemed:
		return green
	case state.Working, state.Agents:
		return cyan
	default: // Idle
		return white
	}
}

func line1(v state.View, c Ctx) string {
	switch v.State {
	case state.Working:
		// No lines count here: a live, climbing diff mid-turn is noise — the
		// file list and elapsed clock already convey "work in progress." The
		// cumulative +/- lands on the Done line, where it summarizes the result.
		segs := projectSplit(v.Turn.Edited, c.RepoRoot, c.In.ProjectDir)
		base := "working"
		if len(segs) > 0 {
			base = "editing " + strings.Join(segs, " · ")
		}
		return base + elapsedSuffix(v) + longerThanUsual(v, c)

	case state.Agents:
		s := pluralCount(v.AgentsRunning, "agent") + " running"
		if v.AgentsDone > 0 {
			s += fmt.Sprintf(" · %d done", v.AgentsDone)
		}
		return s + elapsedSuffix(v)

	case state.Waiting:
		return "waiting on you"

	case state.Failed:
		kind, _ := failedKind(v.Turn.Builds)
		proj := failedProject(v.Turn, c)
		if proj != "" {
			return fmt.Sprintf("%s %s failed", proj, kind)
		}
		return kind + " failed"

	case state.DoneNormal, state.DoneRedeemed:
		return doneSentence(v, c)

	case state.Stopped:
		act := lastActivity(v.Turn, c.RepoRoot, c.In.ProjectDir)
		if v.HasLastAge {
			return fmt.Sprintf("stopped · last: %s · %s ago", act, fmtAge(v.LastAge))
		}
		return "stopped · last: " + act

	default:
		return "idle"
	}
}

func line2(c Ctx) string {
	var parts []string
	if d := dirLabel(c.In.CurrentDir); d != "" {
		parts = append(parts, d)
	}
	if c.In.ModelName != "" {
		parts = append(parts, c.In.ModelName)
	}
	parts = append(parts, ctxSegment(c))
	if seg := rateSegment("5h", c.In.FiveHour, c.Now); seg != "" {
		parts = append(parts, seg)
	}
	if seg := rateSegment("7d", c.In.SevenDay, c.Now); seg != "" {
		parts = append(parts, seg)
	}
	return strings.Join(parts, " · ")
}

// siblingClause is Bit speaking up about the other live sessions, by name. One
// that needs attention or just finished gets a sentence ("The session "Read and
// review project" has new updates — take a look"); when several are merely busy
// it's a light note ("3 other sessions running"); a single idle one isn't worth
// mentioning. Empty when no other session is live.
func siblingClause(c Ctx) string {
	others := c.Siblings
	if len(others) == 0 {
		return ""
	}
	const maxNamed = 2
	var named []string
	extra := 0
	allDone := true // every named sibling is a fresh completion (pure good news)
	for _, b := range others {
		if !sessions.Notable(b, c.Now) {
			continue
		}
		if len(named) < maxNamed {
			named = append(named, siblingPhrase(b, c.Now, c.ColorOn))
			if !sessions.JustCompleted(b, c.Now) {
				allDone = false
			}
		} else {
			extra++
		}
	}

	if len(named) == 0 {
		// Only benign sessions elsewhere. A lone one is noise; several are a
		// useful "you've got others open" reminder.
		if len(others) < 2 {
			return ""
		}
		return fmt.Sprintf("%d other sessions running", len(others))
	}

	body := strings.Join(named, ", ")
	if extra > 0 {
		body += fmt.Sprintf(", and %d more", extra)
	}
	s := "Elsewhere: " + body
	if len(named) == 1 {
		s = "The session " + body
	}
	// When the only news is "a turn finished," add Bit's nudge.
	if allDone {
		s += " — take a look"
	}
	return s
}

// siblingPhrase is Bit naming one session and what it's doing — "Read and review
// project" has new updates", "api crashed", "web needs you" — colored so the
// meaning reads even mid-sentence (green for a finished turn, severity color for
// an alert).
func siblingPhrase(b sessions.Beat, now time.Time, colorOn bool) string {
	word, col := siblingWord(b.State), line1Color(siblingState(b.State))
	if sessions.JustCompleted(b, now) {
		word, col = "has new updates", green
	}
	phrase := siblingName(b) + " " + word
	if colorOn {
		return colorize(phrase, col)
	}
	return phrase
}

// siblingName identifies a session: its ai-title (quoted, since titles are
// multi-word) if known, else the project dir, else a generic stand-in.
func siblingName(b sessions.Beat) string {
	if b.Title != "" {
		return `"` + b.Title + `"`
	}
	if b.Project != "" {
		return b.Project
	}
	return "another session"
}

func siblingWord(s string) string {
	switch s {
	case "failed":
		return "crashed"
	case "stopped":
		return "stalled"
	case "waiting":
		return "needs you"
	default:
		return s
	}
}

// siblingState maps a heartbeat's state string back to a state.State purely to
// reuse line1Color for the digest (red/bright-red/yellow). Only the actionable
// states are reachable here.
func siblingState(s string) state.State {
	switch s {
	case "failed":
		return state.Failed
	case "stopped":
		return state.Stopped
	case "waiting":
		return state.Waiting
	default:
		return state.Idle
	}
}

// doneSentence is Bit recapping a finished turn in plain sentences rather than a
// glyph-and-bullet list: "4 files edited, line changes: +885/-99. Build
// succeeded. Tests succeeded." Each clause appears only when it happened.
func doneSentence(v state.View, c Ctx) string {
	var sentences []string

	work := ""
	if n := len(v.Turn.Edited); n > 0 {
		work = countFiles(n) + " edited"
	}
	if d := linesDelta(c); d != "" {
		if work != "" {
			work += ", line changes: " + d
		} else {
			work = "Line changes: " + d
		}
	}
	if work != "" {
		sentences = append(sentences, work)
	}
	// On a recovery (red -> green this turn), the first passing result reads
	// "green again" — a subtle nod to the save, instead of a flat "succeeded".
	redeemed := v.State == state.DoneRedeemed
	greenUsed := false
	if ran, ok := outcome(v.Turn.Builds, "build"); ran {
		w := "Build " + outcomeWord(ok)
		if redeemed && ok && !greenUsed {
			w, greenUsed = "Build green again", true
		}
		sentences = append(sentences, w)
	}
	if ran, ok := outcome(v.Turn.Builds, "test"); ran {
		w := "Tests " + outcomeWord(ok)
		if redeemed && ok && !greenUsed {
			w, greenUsed = "Tests green again", true
		}
		sentences = append(sentences, w)
	}
	if len(sentences) == 0 {
		return "done"
	}
	return strings.Join(sentences, ". ") + "."
}

// longerThanUsual is Bit's subtle note that this turn has run well past the
// project's learned norm. Silent until enough history exists and the turn is
// clearly over the line (2x typical), so it never cries wolf on normal variance.
func longerThanUsual(v state.View, c Ctx) string {
	if c.TypicalTurn <= 0 || !v.HasElapsed {
		return ""
	}
	if v.Elapsed > 2*c.TypicalTurn {
		return " (longer than usual)"
	}
	return ""
}

// linesDelta is the session's cumulative diff size as bare "+added/-removed";
// doneSentence supplies the "line changes:" lead-in.
func linesDelta(c Ctx) string {
	a, r := c.In.LinesAdded, c.In.LinesRemoved
	if a == 0 && r == 0 {
		return ""
	}
	return fmt.Sprintf("+%d/-%d", a, r)
}

// outcome reports whether a build/test of the given kind ran this turn and, if
// so, whether the latest one passed.
func outcome(builds []transcript.BuildResult, kind string) (ran, ok bool) {
	ok = true
	for _, b := range builds {
		if b.Kind == kind {
			ran = true
			ok = !b.IsError
		}
	}
	return ran, ok
}

func outcomeWord(ok bool) string {
	if ok {
		return "succeeded"
	}
	return "failed"
}

func pluralCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// --- line-1 helpers ---

func elapsedSuffix(v state.View) string {
	if v.HasElapsed && v.Elapsed > 0 {
		return " · " + fmtDur(v.Elapsed)
	}
	return ""
}

// projectSplit returns "<project> (<n> files)" segments in first-seen order.
// The count is bound to its project with a unit so it never reads as a bare,
// context-free number floating between separators.
func projectSplit(files []string, root, projectDir string) []string {
	counts := map[string]int{}
	var order []string
	for _, f := range files {
		p := projectOf(f, root, projectDir)
		if _, seen := counts[p]; !seen {
			order = append(order, p)
		}
		counts[p]++
	}
	var segs []string
	for _, p := range order {
		segs = append(segs, fmt.Sprintf("%s (%s)", p, countFiles(counts[p])))
	}
	return segs
}

// projectOf maps a file path to a project: the first path segment relative to
// the repo root (or project dir), or the repo basename if the file is at root.
func projectOf(file, root, projectDir string) string {
	base := root
	if base == "" {
		base = projectDir
	}
	if base == "" {
		return parentName(file)
	}
	rel, err := filepath.Rel(base, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return parentName(file)
	}
	rel = filepath.ToSlash(rel)
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	// File sits at the repo root.
	return filepath.Base(base)
}

func parentName(file string) string {
	d := filepath.Dir(filepath.ToSlash(file))
	if d == "." || d == "" || d == "/" {
		return filepath.Base(file)
	}
	return filepath.Base(d)
}

func countFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func failedKind(builds []transcript.BuildResult) (string, bool) {
	for i := len(builds) - 1; i >= 0; i-- {
		if builds[i].IsError {
			if builds[i].Kind == "test" {
				return "tests", true
			}
			return "build", true
		}
	}
	return "build", false
}

func failedProject(t transcript.Turn, c Ctx) string {
	if len(t.Edited) > 0 {
		return projectOf(t.Edited[len(t.Edited)-1], c.RepoRoot, c.In.ProjectDir)
	}
	if c.RepoRoot != "" {
		return filepath.Base(c.RepoRoot)
	}
	return ""
}

func lastActivity(t transcript.Turn, root, projectDir string) string {
	if len(t.Edited) > 0 {
		return "editing " + filepath.Base(filepath.ToSlash(t.Edited[len(t.Edited)-1]))
	}
	return "thinking"
}

// --- line-2 helpers ---

func dirLabel(dir string) string {
	if dir == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return filepath.Base(dir)
}

func ctxSegment(c Ctx) string {
	if c.In.CtxPct == nil {
		return "ctx --"
	}
	pct := int(*c.In.CtxPct + 0.5)
	body := fmt.Sprintf("ctx %d%%", pct) + trendArrow(c.Trend)
	if !c.ColorOn {
		return body
	}
	// Only color when it warrants attention; low usage stays grey like the rest
	// of the ambient line (a green "all fine" badge is just noise).
	switch {
	case pct >= 90:
		return colorize(body, red)
	case pct >= 70:
		return colorize(body, yellow)
	default:
		return body
	}
}

// trendArrow renders context-window velocity: rising (↑) during active work,
// falling (↓) after a compaction. Flat and unknown show nothing — an arrow that
// only appears when context is actually moving is the signal worth the glyph.
func trendArrow(t sessions.Trend) string {
	switch t {
	case sessions.TrendUp:
		return " ↑"
	case sessions.TrendDown:
		return " ↓"
	default:
		return ""
	}
}

func rateSegment(label string, rl *input.RateLimit, now time.Time) string {
	if rl == nil || rl.UsedPercentage == nil {
		return ""
	}
	seg := fmt.Sprintf("%s %d%%", label, int(*rl.UsedPercentage+0.5))
	if rl.HasReset {
		if d := rl.ResetsAt.Sub(now); d > 0 {
			seg += fmt.Sprintf(" (%s)", fmtCountdown(d))
		}
	}
	return seg
}

// --- duration formatting ---

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	}
}

func fmtAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func fmtCountdown(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// --- ANSI ---

const (
	reset     = "\x1b[0m"
	white     = "\x1b[37m"
	cyan      = "\x1b[36m"
	green     = "\x1b[32m"
	yellow    = "\x1b[33m"
	red       = "\x1b[31m"
	brightRed = "\x1b[91m" // more legible than dim red on dark terminals
)

func colorize(s, code string) string { return code + s + reset }
