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
// colored as a whole by state; line 2 is the ambient context line.
func Render(v state.View, c Ctx) []string {
	l1 := Face(v.State, c.Frame, c.Narrow) + " " + line1(v, c)
	if c.ColorOn {
		l1 = colorize(l1, line1Color(v.State))
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
		segs := projectSplit(v.Turn.Edited, c.RepoRoot, c.In.ProjectDir)
		if len(segs) == 0 {
			return "working" + elapsedSuffix(v)
		}
		return "editing " + strings.Join(segs, " · ") + elapsedSuffix(v)

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
		parts := []string{fmt.Sprintf("edited %s", countFiles(len(v.Turn.Edited)))}
		if bp := buildPart(v.Turn.Builds); bp != "" {
			parts = append(parts, bp)
		}
		if tp := testPart(v.Turn.Builds); tp != "" {
			parts = append(parts, tp)
		}
		out := strings.Join(parts, " · ")
		if v.State == state.DoneRedeemed {
			out += " (recovered)"
		}
		return out

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

func buildPart(builds []transcript.BuildResult) string {
	ran, ok := false, true
	for _, b := range builds {
		if b.Kind == "build" {
			ran = true
			if b.IsError {
				ok = false
			} else {
				ok = true
			}
		}
	}
	if !ran {
		return ""
	}
	if ok {
		return "build ✓"
	}
	return "build ✗"
}

func testPart(builds []transcript.BuildResult) string {
	ran, ok := false, true
	for _, b := range builds {
		if b.Kind == "test" {
			ran = true
			if b.IsError {
				ok = false
			} else {
				ok = true
			}
		}
	}
	if !ran {
		return ""
	}
	if ok {
		return "tests ✓"
	}
	return "tests ✗"
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
	body := fmt.Sprintf("ctx %d%%", pct)
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

func rateSegment(label string, rl *input.RateLimit, now time.Time) string {
	if rl == nil || rl.UsedPercentage == nil {
		return ""
	}
	seg := fmt.Sprintf("%s %d%%", label, int(*rl.UsedPercentage+0.5))
	if rl.HasReset {
		if d := rl.ResetsAt.Sub(now); d > 0 {
			seg += fmt.Sprintf(" (resets %s)", fmtCountdown(d))
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
