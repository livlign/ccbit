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

	editedSet map[string]bool
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
	testRe = regexp.MustCompile(`(?i)(\b(go|dotnet|cargo)\s+test\b|npm\s+(run\s+)?test|yarn\s+test|pnpm\s+(run\s+)?test|\bpytest\b|\bjest\b|\bvitest\b|mvn\s+test|gradle\w*\s+\w*test)`)
	buildRe = regexp.MustCompile(`(?i)(\b(go|dotnet|cargo)\s+build\b|npm\s+run\s+build|yarn\s+build|pnpm\s+(run\s+)?build|\bmake\b|\bmsbuild\b|\btsc\b|gradle\w*\s+\w*build|mvn\s+(package|compile|install))`)
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
		turns = append(turns, Turn{editedSet: map[string]bool{}, PromptID: e.PromptID})
		cur = &turns[len(turns)-1]
		if e.HasTime {
			cur.Start, cur.HasStart = e.Time, true
		}
	}

	for _, e := range entries {
		if e.Kind == KindUserPrompt || cur == nil {
			open(e)
		}
		if e.HasTime {
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
					if tu.FilePath != "" && !cur.editedSet[tu.FilePath] {
						cur.editedSet[tu.FilePath] = true
						cur.Edited = append(cur.Edited, tu.FilePath)
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
