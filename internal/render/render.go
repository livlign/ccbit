// Package render turns a derived state View into the one-to-three lines printed
// to the status line: a reactive face-led line, an ambient context line, and an
// optional catch-up recap.
package render

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/livlign/ccbit/internal/diag"
	"github.com/livlign/ccbit/internal/gitx"
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
	In        input.Stdin
	RepoRoot  string // git toplevel, or "" if unknown
	Cols      int    // terminal width from COLUMNS, 0 if unset
	Narrow    bool
	Frame     int // 0 or 1, wall-clock selected (~2s swap) — Agents shimmy
	FrameFast int // 0 or 1, wall-clock selected (~1s swap) — Working face
	ColorOn   bool
	Now       time.Time

	Trend    sessions.Trend  // context-window velocity for the ctx% segment
	Siblings []sessions.Beat // other live sessions, actionable-first

	// TypicalTurn is the project's learned mean turn duration (0 if not yet
	// learned), used to flag a turn running unusually long.
	TypicalTurn time.Duration

	// TurnLinesAdded/Removed are this turn's lines-changed delta. The stdin cost
	// counters are session-cumulative, so main re-bases them at each turn start
	// (via the heartbeat) — keeping the recap's line numbers in the same scope as
	// its per-turn file count.
	TurnLinesAdded   int
	TurnLinesRemoved int

	// Tasks is the session's task-tool plan (TaskCreate/TaskUpdate replayed from
	// the transcript): what Bit is working through and where it is.
	Tasks transcript.TaskSummary

	// ProjectLabel is this session's short project name (repo basename), used to
	// spot a sibling session working the same repo (edit-collision risk).
	ProjectLabel string

	// SelfTitle is this session's own ai-title. A sibling heartbeat carrying the
	// same title is the same work — a resumed/forked session, or a duplicate
	// heartbeat — never a distinct window worth naming, so it's filtered out
	// (naming the session you're in is always redundant).
	SelfTitle string

	// LastPromptAt is when the user last submitted a prompt in THIS session —
	// the interaction read-receipt: any sibling news older than this has been on
	// the status line while the user was demonstrably at this window, so it's
	// considered delivered and stops nagging. Zero when unknown (show all news).
	LastPromptAt time.Time

	// Git is the repo's branch/dirty/ahead/behind snapshot for the ambient line.
	Git gitx.Info
}

// The Working and Idle faces are assembled per turn from shared parts: a pool
// of eye cores, and a per-state pool of "hands" (wrappers, %s = the eyes). A
// turn-derived seed (faceSeed) picks one eye and one hand, so the face is steady
// through a turn's repaints and varies between turns. Eye index uses the seed's
// low end, hand index the next radix up, so the two vary independently.

// eyes is the shared eye pool. All glyphs are single-width.
var eyes = []string{
	"•_•", "°.°", "-_-", "◔_◔", "ʘ_ʘ", "◕_◕", "^_^", ">_<", "*_*", "o_o",
}

// idleHands are the resting face's static wrappers; %s is where the eyes slot.
var idleHands = []string{
	"(%s)",   // bare
	"(%s)旦",  // sipping a cup, on a break
	"d(%s)b", // thumbs up
}

// workingHand is one animated gesture: the two frame templates the face
// alternates between (~1s) while a turn runs. The frames are distinct poses, so
// the motion reads as action rather than a mirror flip.
type workingHand struct{ a, b string }

var workingHands = []workingHand{
	{`\(%s)/`, `/(%s)\`}, // raise-the-roof
	{`>(%s)<`, `<(%s)>`}, // elbows pumping
	{`ᕙ(%s)ᕗ`, `\(%s)/`}, // march-arm -> arms up
	{`ᕦ(%s)ᕤ`, `\(%s)/`}, // flex -> arms up
	{`ง(%s)ง`, `-(%s)-`}, // fists -> arms out
	{`-(%s)-`, `৲(%s)৲`}, // arm swing (the original Working face)
	{`-(%s)-`, `\(%s)/`}, // wind-up
	{`\(%s)-`, `-(%s)/`}, // alternating arms
	{`/(%s)/`, `\(%s)\`}, // swaying / rowing
	{`ง(%s)ง`, `ว(%s)ว`}, // fists shaking
}

// eye picks this turn's shared eye core.
func eye(seed uint64) string { return eyes[seed%uint64(len(eyes))] }

// handIndex picks a hand for a pool of size n, from a different slice of the
// seed than eye() uses so the two rotate independently.
func handIndex(seed uint64, n int) uint64 { return (seed / uint64(len(eyes))) % uint64(n) }

// idleFace assembles the resting face: a per-turn eye in a per-turn static hand.
// The cup is the one width-risky glyph, so a narrow terminal drops the prop.
func idleFace(seed uint64, narrow bool) string {
	hand := idleHands[handIndex(seed, len(idleHands))]
	if narrow && strings.Contains(hand, "旦") {
		hand = "(%s)"
	}
	return fmt.Sprintf(hand, eye(seed))
}

// workingFace assembles the working face: a per-turn eye in a per-turn gesture,
// alternating between the gesture's two frames as frame flips.
func workingFace(seed uint64, frame int) string {
	h := workingHands[handIndex(seed, len(workingHands))]
	tmpl := h.a
	if frame == 1 {
		tmpl = h.b
	}
	return fmt.Sprintf(tmpl, eye(seed))
}

// faceSeed derives a stable per-turn seed for face rotation: identical across a
// turn's ~1×/s repaints (so the face never flickers) yet different from turn to
// turn and session to session.
func faceSeed(sessionID string, t transcript.Turn) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%s|%d", sessionID, t.PromptID, t.Start.Unix())
	return h.Sum64()
}

// Face returns the kaomoji for a state. Working and Agents animate via Frame;
// Idle rotates per turn via seed; the rest are static. Narrow swaps risky glyphs
// for safe fallbacks.
func Face(s state.State, frame int, narrow bool, seed uint64) string {
	switch s {
	case state.Working:
		return workingFace(seed, frame)
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
	case state.Idle:
		return idleFace(seed, narrow)
	default: // unknown -> neutral resting face
		return fmt.Sprintf("(%s)", eyes[0])
	}
}

// Render builds all output lines for the view. Line 1 (the reactive line) is
// Bit narrating this session — its state, what it changed, and a word about any
// sibling sessions — colored as a whole by state; line 2 is ambient context.
func Render(v state.View, c Ctx) []string {
	frame := c.Frame
	if v.State == state.Working {
		frame = c.FrameFast // Working swaps every ~1s; Agents stays at ~2s
	}
	seed := faceSeed(c.In.SessionID, v.Turn)
	l1 := Face(v.State, frame, c.Narrow, seed) + " " + line1(v, c)
	if c.ColorOn {
		l1 = colorize(l1, line1Color(v.State))
	}
	// The sibling clause is appended outside the whole-line color so its own
	// per-severity colors survive (an embedded reset would otherwise truncate
	// the state color for the rest of the line).
	if clause := siblingClause(c); clause != "" {
		l1 += " · " + clause
	} else if v.State == state.Idle {
		// A genuinely quiet moment (idle, nothing to say about siblings) is where
		// a discovery hint fits without ever competing with real news.
		if tip := idleTip(c); tip != "" {
			l1 += " · " + tip
		}
	}
	return []string{l1, line2(c)}
}

// tipPeriodSecs is how long one rotation slot lasts; tipShowEvery is how many
// slots pass between tips (so most of the time idle reads plain).
const (
	tipPeriodSecs = 20
	tipShowEvery  = 3
)

// idleTip advertises one still-off visual feature during a quiet idle moment,
// rotating slowly through whichever remain off and staying blank most of the
// time. Returns "" once every feature is enabled. Keyed off c.Now so it's
// stable within a slot (no flicker between 1s repaints) and testable.
func idleTip(c Ctx) string {
	tips := offFeatureTips()
	if len(tips) == 0 {
		return ""
	}
	slot := c.Now.Unix() / tipPeriodSecs
	if slot%tipShowEvery != 0 {
		return "" // a resting slot: idle reads plain
	}
	i := int((slot / tipShowEvery) % int64(len(tips)))
	tip := "tip: " + tips[i]
	if c.ColorOn {
		tip = colorize(tip, dim) // a faint aside, never louder than the state
	}
	return tip
}

// offFeatureTips lists a one-line enable hint for each visual feature that is
// currently off, in a stable order. Polished, imperative, and self-describing:
// the env var name is the action.
func offFeatureTips() []string {
	var tips []string
	if !nerdFont() {
		tips = append(tips, "set CCBIT_NERD_FONT=1 for Nerd Font icons")
	}
	if !gitColorOn() {
		tips = append(tips, "set CCBIT_GIT_COLOR=1 to color git changes")
	}
	if !useIcons() {
		tips = append(tips, "set CCBIT_ICONS=1 for per-segment icons")
	}
	if !ctxGaugeOn() {
		tips = append(tips, "set CCBIT_CTX_GAUGE=1 for a context gauge")
	}
	if !rateColorOn() {
		tips = append(tips, "set CCBIT_RATE_COLOR=1 to color rate-limit meters")
	}
	return tips
}

// line1Color maps a state to its whole-line color. Alert states stand out (red
// for failed, bright red for stopped, yellow for needs-you); active is cyan,
// resting uses the theme default foreground; done is green.
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
		return defaultFg
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
		if t := taskClause(c.Tasks, v.Turn.TaskTouched); t != "" {
			base += " · " + t
		}
		// A command that's been executing a while is THE thing to say — it
		// explains the quiet ("running: dotnet test … (3m)"), where the old
		// readout decayed into a false "stopped · last: thinking".
		if r := runningClause(v); r != "" {
			base += " · " + r
		}
		if v.Thinking && v.HasLastAge {
			base += " · thinking (" + fmtDur(v.LastAge) + ")"
		}
		return base + elapsedSuffix(v) + longerThanUsual(v, c) + loopNote(v.Turn)

	case state.Agents:
		s := pluralCount(v.AgentsRunning, "agent") + " running"
		if v.AgentsDone > 0 {
			s += fmt.Sprintf(" · %d done", v.AgentsDone)
		}
		if t := taskClause(c.Tasks, v.Turn.TaskTouched); t != "" {
			s += " · " + t
		}
		return s + elapsedSuffix(v)

	case state.Waiting:
		// How long it's been waiting is the user-behavior signal: a question
		// sitting unanswered for long is a forgotten session.
		if v.HasLastAge && v.LastAge >= time.Minute {
			return "waiting on you · " + fmtAge(v.LastAge)
		}
		return "waiting on you"

	case state.Failed:
		kind, _ := failedKind(v.Turn.Builds)
		proj := failedProject(v.Turn, c)
		s := kind + " failed"
		if proj != "" {
			s = fmt.Sprintf("%s %s failed", proj, kind)
		}
		// A repeat offender is the "stop and look" cue: Bit is going in circles.
		if v.Turn.FailStreak >= 3 {
			s += fmt.Sprintf(" (%d× in a row)", v.Turn.FailStreak)
		}
		// What actually broke, when the failure text gives up a concrete reason
		// (a compiler location, a named test, a signature) — turning the alarm
		// into a signpost.
		if d := diag.Diagnose(lastFailedText(v.Turn.Builds)); d != "" {
			s += " · " + ellipsize(d, 48)
		}
		return s

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

// modelSegment renders the model with its reasoning effort in parens, e.g.
// "Opus 4.8 (high)". Effort is omitted when unknown.
func modelSegment(model, effort string) string {
	if model == "" {
		return ""
	}
	if effort != "" {
		return model + " (" + effort + ")"
	}
	return model
}

func line2(c Ctx) string {
	var parts []string
	if d := dirLabel(c.In.CurrentDir); d != "" {
		parts = append(parts, withIcon(iconDir, d))
	}
	if g := gitSegment(c.Git, c.ColorOn); g != "" {
		parts = append(parts, g)
	}
	if seg := modelSegment(c.In.ModelName, c.In.Effort); seg != "" {
		parts = append(parts, withIcon(iconModel, seg))
	}
	parts = append(parts, ctxSegment(c))
	if seg := rateSegment("5h", c.In.FiveHour, c.Now, c.ColorOn); seg != "" {
		parts = append(parts, seg)
	}
	if seg := rateSegment("7d", c.In.SevenDay, c.Now, c.ColorOn); seg != "" {
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
	// Drop any sibling that shares this session's title: it's the same work
	// (resume/fork/dupe heartbeat), and naming the window you're in is redundant.
	if c.SelfTitle != "" {
		kept := make([]sessions.Beat, 0, len(others))
		for _, b := range others {
			if b.Title == c.SelfTitle {
				continue
			}
			kept = append(kept, b)
		}
		others = kept
	}
	if len(others) == 0 {
		return ""
	}
	const maxNamed = 2
	var named []string
	extra := 0
	allDone := true // every named sibling is a fresh completion (pure good news)
	for _, b := range others {
		// Edit-collision risk outranks everything else worth saying about a
		// sibling: another session actively working this same repo can race
		// this one's changes.
		if collision(b, c.ProjectLabel) {
			phrase := siblingName(b) + " is also working this repo"
			if c.ColorOn {
				phrase = colorize(phrase, yellow)
			}
			named, allDone = append(named, phrase), false
			continue
		}
		fresh := freshCompletion(b, c)
		if !sessions.Actionable(b.State) && !fresh {
			continue
		}
		if len(named) < maxNamed {
			named = append(named, siblingPhrase(b, fresh, c.ColorOn))
			if !fresh {
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

// collision reports a sibling actively working the same project as this session
// — concurrent edits to one repo from two sessions can race each other.
func collision(b sessions.Beat, project string) bool {
	if project == "" || b.Project != project {
		return false
	}
	return b.State == "working" || b.State == "agents"
}

// freshCompletion is the completion nudge gated by the interaction
// read-receipt: it shows only while recent AND newer than the user's last
// prompt in this session. Typing here acknowledges everything already shown,
// so read news stops lingering on the line.
func freshCompletion(b sessions.Beat, c Ctx) bool {
	if !sessions.JustCompleted(b, c.Now) {
		return false
	}
	return c.LastPromptAt.IsZero() || b.DoneSince > c.LastPromptAt.Unix()
}

// siblingPhrase is Bit naming one session and what it's doing — "Read and review
// project" has new updates", "api crashed", "web needs you" — colored so the
// meaning reads even mid-sentence (green for a finished turn, severity color for
// an alert).
func siblingPhrase(b sessions.Beat, fresh, colorOn bool) string {
	word, col := siblingWord(b.State), line1Color(siblingState(b.State))
	if fresh {
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
	// Ship clauses: where the work went. "Committed, not pushed" doubles as a
	// nudge that the remote hasn't seen it yet.
	switch {
	case v.Turn.Pushed:
		sentences = append(sentences, "Pushed")
	case v.Turn.Committed:
		sentences = append(sentences, "Committed, not pushed")
	}
	if v.Turn.Deployed {
		sentences = append(sentences, "Deploy triggered")
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

// linesDelta is this turn's diff size as bare "+added/-removed" (per-turn, not
// the session total — see Ctx.TurnLinesAdded); doneSentence supplies the
// "line changes:" lead-in.
func linesDelta(c Ctx) string {
	a, r := c.TurnLinesAdded, c.TurnLinesRemoved
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

// taskClause is Bit's read of the session plan: how far through the todo
// list. An unfinished plan stays relevant across turns (it will be resumed);
// a completed one is only news during the turn that completed it — on later
// turns it's stale and stays silent.
func taskClause(t transcript.TaskSummary, touched bool) string {
	if t.Total == 0 || (t.Done == t.Total && !touched) {
		return ""
	}
	return fmt.Sprintf("%d/%d todos", t.Done, t.Total)
}

// runningClause names a tool call that has been executing for a while —
// long-running commands are silent in the transcript, so this is what stands
// between "running: dotnet test (3m)" and a false "stopped". Quick calls stay
// silent (every render would otherwise narrate routine sub-30s commands).
func runningClause(v state.View) string {
	if !v.HasInFlight || v.InFlightFor < 30*time.Second {
		return ""
	}
	return fmt.Sprintf("running: %s (%s)", ellipsize(v.InFlight, 36), fmtDur(v.InFlightFor))
}

// loopNote flags file churn: the same file reworked over and over this turn
// usually means Bit is thrashing, not progressing.
func loopNote(t transcript.Turn) string {
	if t.HotFileEdits >= 4 {
		return fmt.Sprintf(" (%s edited %d×)", filepath.Base(filepath.ToSlash(t.HotFile)), t.HotFileEdits)
	}
	return ""
}

func ellipsize(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

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

// lastFailedText is the captured output of the most recent failing build/test —
// the raw material diag.Diagnose mines for a concise reason.
func lastFailedText(builds []transcript.BuildResult) string {
	for i := len(builds) - 1; i >= 0; i-- {
		if builds[i].IsError {
			return builds[i].Text
		}
	}
	return ""
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

// gitSegment is where the work is going: "main +1 ~3 -2 ↑2" = on main, 1 new
// path, 3 modified, 2 deleted, 2 commits not pushed. Each mark appears only
// when nonzero, so a clean, synced branch is just its name. When CCBIT_NERD_FONT
// is set the change marks use Nerd Font glyphs (plus/pencil/trash); CCBIT_ICONS
// adds a leading branch glyph; CCBIT_GIT_COLOR tints the marks green/yellow/red.
func gitSegment(g gitx.Info, colorOn bool) string {
	if g.Branch == "" {
		return ""
	}
	nf := nerdFont()
	color := colorOn && gitColorOn()
	s := withIcon(iconBranch, g.Branch)
	s += changeMark(nf, color, g.New, "+", green, "")       // nf-fa-plus
	s += changeMark(nf, color, g.Modified, "~", yellow, "") // nf-fa-pencil
	s += changeMark(nf, color, g.Deleted, "-", red, "")     // nf-fa-trash
	if g.Ahead > 0 {
		s += fmt.Sprintf(" ↑%d", g.Ahead)
	}
	if g.Behind > 0 {
		s += fmt.Sprintf(" ↓%d", g.Behind)
	}
	return s
}

// changeMark renders one worktree-change count. Nerd Font glyphs get a space
// before the number (they're icons); the ASCII signs hug it (+3, ~3, -3). When
// color is on the mark is tinted with its category color.
func changeMark(nf, color bool, n int, ascii, colorCode, glyph string) string {
	if n <= 0 {
		return ""
	}
	var mark string
	if nf {
		mark = fmt.Sprintf(" %s %d", glyph, n)
	} else {
		mark = fmt.Sprintf(" %s%d", ascii, n)
	}
	if color {
		mark = colorize(mark, colorCode)
	}
	return mark
}

// Optional visual features, each opt-in via its own env var (default off). They
// stay off by default because none can be safely auto-detected: Nerd Font glyphs
// show as tofu without a patched font, and color/gauges are a matter of taste.
// A quiet idle line advertises whichever are still off (see idleTip).
const (
	envNerdFont  = "CCBIT_NERD_FONT"  // Nerd Font glyphs for git change marks
	envIcons     = "CCBIT_ICONS"      // a leading Nerd Font icon per ambient segment
	envGitColor  = "CCBIT_GIT_COLOR"  // color git change marks (green/yellow/red)
	envCtxGauge  = "CCBIT_CTX_GAUGE"  // a mini fill bar beside the ctx percentage
	envRateColor = "CCBIT_RATE_COLOR" // escalate 5h/7d rate limits to yellow/red
)

// envOn reports whether an opt-in env var is set to a truthy value.
func envOn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func nerdFont() bool    { return envOn(envNerdFont) }
func useIcons() bool    { return envOn(envIcons) }
func gitColorOn() bool  { return envOn(envGitColor) }
func ctxGaugeOn() bool  { return envOn(envCtxGauge) }
func rateColorOn() bool { return envOn(envRateColor) }

// Nerd Font glyphs used as leading segment icons (nf-fa family). Each renders at
// single width in a patched font, tofu without one — hence the CCBIT_ICONS gate.
const (
	iconDir    = "" // nf-fa-folder
	iconBranch = "" // nf-fa-code_branch
	iconModel  = "" // nf-fa-microchip
	iconCtx    = "" // nf-fa-tachometer
	iconRate   = "" // nf-fa-clock_o
)

// withIcon prefixes a segment with a Nerd Font icon when CCBIT_ICONS is on.
func withIcon(glyph, s string) string {
	if useIcons() {
		return glyph + " " + s
	}
	return s
}

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
	label := "ctx"
	if useIcons() {
		label = iconCtx
	}
	if c.In.CtxPct == nil {
		return label + " --"
	}
	pct := int(*c.In.CtxPct + 0.5)
	body := label + " "
	if ctxGaugeOn() {
		body += gaugeBar(pct) + " "
	}
	body += fmt.Sprintf("%d%%", pct) + trendArrow(c.Trend)
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

// gaugeCells is the width of the ctx fill bar.
const gaugeCells = 5

// gaugeBar renders a small fill bar for a 0-100 percentage: filled cells (▆)
// then empty ones (▁), so context pressure reads at a glance. Both glyphs are
// widely-supported block elements — no Nerd Font needed.
func gaugeBar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct*gaugeCells + 50) / 100
	return strings.Repeat("▆", filled) + strings.Repeat("▁", gaugeCells-filled)
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

func rateSegment(label string, rl *input.RateLimit, now time.Time, colorOn bool) string {
	if rl == nil || rl.UsedPercentage == nil {
		return ""
	}
	pct := int(*rl.UsedPercentage + 0.5)
	seg := fmt.Sprintf("%s %d%%", label, pct)
	if rl.HasReset {
		if d := rl.ResetsAt.Sub(now); d > 0 {
			seg += fmt.Sprintf(" (%s)", fmtCountdown(d))
		}
	}
	seg = withIcon(iconRate, seg)
	// CCBIT_RATE_COLOR escalates a limit nearing its cap, mirroring ctx%.
	if colorOn && rateColorOn() {
		switch {
		case pct >= 90:
			seg = colorize(seg, red)
		case pct >= 70:
			seg = colorize(seg, yellow)
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
	defaultFg = "\x1b[39m" // theme's default foreground: legible on light and dark
	cyan      = "\x1b[36m"
	green     = "\x1b[32m"
	yellow    = "\x1b[33m"
	red       = "\x1b[31m"
	brightRed = "\x1b[91m" // more legible than dim red on dark terminals
	dim       = "\x1b[2m"  // faint: a stale (likely-gone) session in the roster
)

func colorize(s, code string) string { return code + s + reset }
