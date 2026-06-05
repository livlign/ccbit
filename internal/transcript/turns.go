package transcript

import (
	"regexp"
	"time"
)

// Turn is a single user-prompt-to-completion exchange, derived by segmenting
// entries at user-prompt boundaries (promptId is not shared across an entire
// turn, so linear order is authoritative — see the probe in the PRD appendix).
type Turn struct {
	PromptID string
	Start    time.Time
	HasStart bool
	Last     time.Time
	HasLast  bool

	// Open is true while the assistant is still working this turn (the last
	// meaningful entry is not an end_turn assistant message). Only meaningful
	// for the most recent turn; earlier turns are always closed.
	Open bool

	Edited      []string // distinct edited file paths, in first-seen order
	Builds      []BuildResult
	Spawns      int
	Completions int
	Pending     string // "AskUserQuestion" / "ExitPlanMode" while awaiting the user, else ""
	Model       string

	// MaxGap is the longest pause between consecutive timed entries within this
	// turn — the basis for learning a per-project stall threshold.
	MaxGap time.Duration

	// Loop signals: a file reworked over and over, or build/test failing
	// repeatedly with no pass in between — the "going in circles, intervene"
	// cues a glance can act on.
	HotFile      string // most-edited file this turn
	HotFileEdits int
	FailStreak   int // consecutive failing build/test results at the turn's tail

	// Ship signals, from successful Bash results: this turn committed, pushed,
	// or triggered a deploy/CI run (Jenkins, gh workflow, kubectl, ...).
	Committed bool
	Pushed    bool
	Deployed  bool

	editedSet map[string]int // path -> edit count this turn
	lastTimed time.Time
	hasTimed  bool
}

// BuildResult is one build/test command outcome in a turn. Kind is "build" or
// "test"; IsError distinguishes pass (false) from fail (true) via the measured
// tool_result.is_error flag (there is no numeric exit-code field).
type BuildResult struct {
	Kind    string
	IsError bool
	Command string
	Text    string
}

var (
	testRe  = regexp.MustCompile(`(?i)(\b(go|dotnet|cargo)\s+test\b|npm\s+(run\s+)?test|yarn\s+test|pnpm\s+(run\s+)?test|\bpytest\b|\bjest\b|\bvitest\b|mvn\s+test|gradle\w*\s+\w*test)`)
	buildRe = regexp.MustCompile(`(?i)(\b(go|dotnet|cargo)\s+build\b|npm\s+run\s+build|yarn\s+build|pnpm\s+(run\s+)?build|\bmake\b|\bmsbuild\b|\btsc\b|gradle\w*\s+\w*build|mvn\s+(package|compile|install))`)

	commitRe = regexp.MustCompile(`\bgit\b[^\n|;&]*\bcommit\b`)
	pushRe   = regexp.MustCompile(`\bgit\b[^\n|;&]*\bpush\b`)
	// deployRe recognizes common deploy/CI triggers without any configuration:
	// Jenkins build URLs, GitHub workflow dispatch, k8s/container/IaC rollouts,
	// and the usual "deploy" package scripts. Patterns are tool-anchored so a
	// command merely mentioning "deploy" doesn't false-positive.
	deployRe = regexp.MustCompile(`(?i)((curl|wget)[^\n]*jenkins[^\n]*/build|/job/[^\s"']+/build\b|\bgh\s+workflow\s+run\b|\bkubectl\s+(apply|rollout)\b|\bdocker\s+push\b|\bterraform\s+apply\b|\bhelm\s+(install|upgrade)\b|\b(sam|serverless|vercel|netlify|fly|railway)\s+deploy\b|\bnpm\s+run\s+deploy\b|\byarn\s+deploy\b)`)
)

func classifyCommand(cmd string) string {
	switch {
	case testRe.MatchString(cmd):
		return "test"
	case buildRe.MatchString(cmd):
		return "build"
	default:
		return ""
	}
}

func isEditTool(name string) bool {
	switch name {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

// BuildTurns segments parsed entries into turns and populates per-turn state.
func BuildTurns(entries []Entry) []Turn {
	var turns []Turn
	var cur *Turn

	open := func(e Entry) {
		turns = append(turns, Turn{editedSet: map[string]int{}, PromptID: e.PromptID})
		cur = &turns[len(turns)-1]
		if e.HasTime {
			cur.Start, cur.HasStart = e.Time, true
		}
	}

	for _, e := range entries {
		if e.Kind == KindTitle {
			continue // sidecar metadata, not part of any turn
		}
		if e.Kind == KindUserPrompt || cur == nil {
			open(e)
		}
		if e.HasTime {
			if cur.hasTimed {
				if gap := e.Time.Sub(cur.lastTimed); gap > cur.MaxGap {
					cur.MaxGap = gap
				}
			}
			cur.lastTimed, cur.hasTimed = e.Time, true
			cur.Last, cur.HasLast = e.Time, true
		}

		switch e.Kind {
		case KindAssistant:
			if e.Model != "" {
				cur.Model = e.Model
			}
			for _, tu := range e.ToolUses {
				switch {
				case isEditTool(tu.Name):
					if tu.FilePath != "" {
						if cur.editedSet[tu.FilePath] == 0 {
							cur.Edited = append(cur.Edited, tu.FilePath)
						}
						cur.editedSet[tu.FilePath]++
						if n := cur.editedSet[tu.FilePath]; n > cur.HotFileEdits {
							cur.HotFile, cur.HotFileEdits = tu.FilePath, n
						}
					}
				case tu.Name == "Task":
					cur.Spawns++
				case tu.Name == "AskUserQuestion" || tu.Name == "ExitPlanMode":
					cur.Pending = tu.Name
				}
			}
		case KindToolResult:
			if e.IsBash {
				if kind := classifyCommand(e.ResultCommand); kind != "" {
					cur.Builds = append(cur.Builds, BuildResult{
						Kind: kind, IsError: e.IsError, Command: e.ResultCommand, Text: e.ResultText,
					})
					if e.IsError {
						cur.FailStreak++
					} else {
						cur.FailStreak = 0
					}
				}
				if !e.IsError {
					switch {
					case pushRe.MatchString(e.ResultCommand):
						cur.Pushed = true
					case commitRe.MatchString(e.ResultCommand):
						cur.Committed = true
					}
					if deployRe.MatchString(e.ResultCommand) {
						cur.Deployed = true
					}
				}
			}
			if e.ResultToolName == "Task" || containsAgentID(e.ResultText) {
				cur.Completions++
			}
			// A result for the pending interactive tool means the user answered.
			if e.ResultToolName == "AskUserQuestion" || e.ResultToolName == "ExitPlanMode" {
				cur.Pending = ""
			}
		}
	}

	if len(turns) > 0 {
		turns[len(turns)-1].Open = lastTurnOpen(entries)
	}
	return turns
}

// lastTurnOpen decides whether the most recent turn is still in progress by
// inspecting the last meaningful entry: an assistant end_turn closes the turn;
// a pending tool_use, a tool result, or a fresh user prompt keep it open.
func lastTurnOpen(entries []Entry) bool {
	for i := len(entries) - 1; i >= 0; i-- {
		switch entries[i].Kind {
		case KindAssistant:
			return entries[i].StopReason != "end_turn"
		case KindToolResult, KindUserPrompt:
			return true
		}
	}
	return false
}

func containsAgentID(s string) bool {
	return len(s) > 0 && indexOf(s, "agentId:") >= 0
}

// indexOf avoids importing strings here for a single substring search.
func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
