package transcript

import (
	"regexp"
	"strings"
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

	// TaskTouched is true when this turn created or updated a plan task — the
	// freshness signal that keeps a fully-completed plan from lingering on
	// later turns' lines.
	TaskTouched bool

	// InFlight is a tool call still executing: its tool_use is flushed (current
	// CC writes it at execution start) but no result has arrived. Long commands
	// run minutes while the transcript stays silent — without this, they read
	// as a stall. Only meaningful on the most recent turn.
	InFlight      string // display text (command first line, else tool name)
	InFlightSince time.Time
	HasInFlight   bool

	// PendingTools counts unresolved tool_uses of any kind. Zero on a quiet
	// open turn means the model has the floor (composing/thinking); nonzero
	// means something dispatched never came back — a hang or an unanswered
	// permission prompt. Only meaningful on the most recent turn.
	PendingTools int

	editedSet map[string]int // path -> edit count this turn
	pending   []pendingUse   // tool_uses awaiting results, in file order
	lastTimed time.Time
	hasTimed  bool
}

type pendingUse struct {
	use     ToolUse
	at      time.Time
	hasTime bool
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
	commitRe = regexp.MustCompile(`\bgit\b[^\n|;&]*\bcommit\b`)
	pushRe   = regexp.MustCompile(`\bgit\b[^\n|;&]*\bpush\b`)
	// deployRe recognizes common deploy/CI triggers without any configuration:
	// Jenkins build URLs, GitHub workflow dispatch, k8s/container/IaC rollouts,
	// and the usual "deploy" package scripts. Patterns are tool-anchored so a
	// command merely mentioning "deploy" doesn't false-positive.
	deployRe = regexp.MustCompile(`(?i)((curl|wget)[^\n]*jenkins[^\n]*/build|/job/[^\s"']+/build\b|\bgh\s+workflow\s+run\b|\bkubectl\s+(apply|rollout)\b|\bdocker\s+push\b|\bterraform\s+apply\b|\bhelm\s+(install|upgrade)\b|\b(sam|serverless|vercel|netlify|fly|flyctl|railway)\s+deploy\b|\bnpm\s+run\s+deploy\b|\byarn\s+deploy\b)`)
)

// Build/test gate detection. A command is a gate only if a known tool appears
// with an allowed build/test subcommand (a binary alone is never enough), or a
// known standalone runner appears as a token. Anything else classifies as ""
// (no gate) — fail-safe by construction.
var subcommandGates = map[string]map[string]string{
	"go":         {"build": "build", "test": "test", "vet": "build"},
	"dotnet":     {"build": "build", "test": "test"},
	"cargo":      {"build": "build", "test": "test", "check": "build", "clippy": "build", "nextest": "test"},
	"npm":        {"test": "test", "ci": "build"},
	"pnpm":       {"build": "build", "test": "test", "lint": "build", "typecheck": "build"},
	"yarn":       {"build": "build", "test": "test", "lint": "build", "typecheck": "build"},
	"bun":        {"test": "test"},
	"mvn":        {"test": "test", "verify": "test", "package": "build", "install": "build", "compile": "build"},
	"mvnw":       {"test": "test", "verify": "test", "package": "build", "install": "build", "compile": "build"},
	"gradle":     {"build": "build", "test": "test", "check": "test"},
	"gradlew":    {"build": "build", "test": "test", "check": "test"},
	"bazel":      {"build": "build", "test": "test"},
	"make":       {"build": "build", "test": "test", "check": "test", "lint": "build", "ci": "test", "verify": "test"},
	"just":       {"build": "build", "test": "test", "check": "test", "lint": "build", "ci": "test", "verify": "test"},
	"swift":      {"build": "build", "test": "test"},
	"deno":       {"test": "test", "lint": "build", "check": "build"},
	"mix":        {"test": "test", "compile": "build"},
	"rake":       {"test": "test", "build": "build"},
	"sbt":        {"test": "test", "compile": "build"},
	"xcodebuild": {"build": "build", "test": "test"},
	"playwright": {"test": "test"},
	"biome":      {"check": "build", "lint": "build", "ci": "build"},
	"nx":         {"build": "build", "test": "test", "lint": "build", "typecheck": "build"},
	"turbo":      {"build": "build", "test": "test", "lint": "build", "typecheck": "build"},
	"cmake":      {"--build": "build"},
}

var runnerGates = map[string]string{
	"pytest": "test", "tox": "test", "nox": "test", "jest": "test",
	"vitest": "test", "mocha": "test", "rspec": "test", "phpunit": "test",
	"ctest": "test", "gotestsum": "test",
	"eslint": "build", "ruff": "build", "mypy": "build", "flake8": "build",
	"golangci-lint": "build", "rubocop": "build", "tsc": "build",
	"msbuild": "build", "pyright": "build", "pylint": "build",
	"staticcheck": "build", "ninja": "build",
}

// runScriptTools dispatch package scripts via "run"; for them the script name
// is the effective subcommand (npm run build), so "run" itself doesn't exclude.
var runScriptTools = map[string]bool{
	"npm": true, "pnpm": true, "yarn": true, "bun": true, "nx": true, "turbo": true,
}

var runScriptGates = map[string]string{
	"build": "build", "test": "test", "lint": "build", "typecheck": "build", "check": "build",
}

var gateExcluded = map[string]bool{
	"run": true, "serve": true, "start": true, "dev": true, "watch": true,
}

func classifyCommand(cmd string) string {
	toks := strings.Fields(strings.ToLower(strings.ReplaceAll(cmd, ";", " ")))
	verdict := ""
	for i, t := range toks {
		if t == "--watch" {
			return ""
		}
		if strings.ContainsRune(t, '=') {
			continue
		}
		name := binaryName(t)
		if kind, ok := runnerGates[name]; ok {
			verdict = strongerGate(verdict, kind)
			continue
		}
		subs, ok := subcommandGates[name]
		if !ok {
			continue
		}
		sub, j := nextGateWord(toks, i+1)
		if sub == "run" && runScriptTools[name] {
			sub, _ = nextGateWord(toks, j+1)
			if sub == "" {
				continue
			}
			if gateExcluded[sub] {
				return ""
			}
			verdict = strongerGate(verdict, runScriptGates[sub])
			continue
		}
		if sub == "" {
			continue
		}
		if gateExcluded[sub] {
			return ""
		}
		verdict = strongerGate(verdict, subs[sub])
	}
	return verdict
}

func binaryName(t string) string {
	if i := strings.LastIndexAny(t, `/\`); i >= 0 {
		t = t[i+1:]
	}
	for _, ext := range []string{".exe", ".bat", ".cmd"} {
		t = strings.TrimSuffix(t, ext)
	}
	return t
}

// nextGateWord finds the effective subcommand after a tool token: skips flags
// and VAR=val args, stops at shell separators. "--build" passes through as a
// word so cmake --build can match.
func nextGateWord(toks []string, i int) (string, int) {
	for ; i < len(toks); i++ {
		t := toks[i]
		switch t {
		case "&&", "||", "|", "&":
			return "", i
		}
		if strings.ContainsRune(t, '=') {
			continue
		}
		if t != "--build" && (strings.HasPrefix(t, "-") || strings.HasPrefix(t, "+")) {
			continue
		}
		return t, i
	}
	return "", len(toks)
}

// strongerGate keeps the test verdict over build, preserving the old
// test-regex-first precedence for compound commands (go build && go test).
func strongerGate(cur, kind string) string {
	if cur == "test" || kind == "test" {
		return "test"
	}
	if cur == "build" || kind == "build" {
		return "build"
	}
	return ""
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
				case tu.Name == "TaskCreate" || tu.Name == "TaskUpdate":
					cur.TaskTouched = true
				}
				// Track unresolved tool calls. Task/interactive tools are excluded:
				// agents have their own detection (subagents dir) and a pending
				// question is the Waiting state, not an in-flight tool.
				if tu.ID != "" && tu.Name != "Task" && tu.Name != "AskUserQuestion" && tu.Name != "ExitPlanMode" {
					cur.pending = append(cur.pending, pendingUse{use: tu, at: e.Time, hasTime: e.HasTime})
				}
			}
		case KindToolResult:
			if e.ToolUseID != "" {
				for i := range cur.pending {
					if cur.pending[i].use.ID == e.ToolUseID {
						cur.pending = append(cur.pending[:i], cur.pending[i+1:]...)
						break
					}
				}
			}
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
		last := &turns[len(turns)-1]
		last.Open = lastTurnOpen(entries)
		last.PendingTools = len(last.pending)
		// Surface the most recent still-executing SHELL call on the open turn.
		// Only command-carrying tools get this: they legitimately run for
		// minutes, whereas an instant tool (Write/Read) pending that long is a
		// hang or an unanswered permission prompt — both deserve Stopped.
		if last.Open {
			for i := len(last.pending) - 1; i >= 0; i-- {
				p := last.pending[i]
				if p.use.Command == "" {
					continue
				}
				last.InFlight = firstLine(p.use.Command)
				if p.hasTime {
					last.InFlightSince, last.HasInFlight = p.at, true
				}
				break
			}
		}
	}
	return turns
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// lastTurnOpen decides whether the most recent turn is still in progress by
// scanning back for the nearest decisive entry: an assistant end_turn closes the
// turn, a fresh user prompt keeps it open, an interrupt closes it.
//
// The subtlety is the straggler problem. Interrupting an in-flight request
// writes the marker, then the killed request keeps flushing entries in for a
// second or two afterward — late assistant messages (stop_reason "tool_use")
// AND their tool results — all before any new user prompt. Neither is decisive
// on its own: stopping at one would re-open a turn the user already interrupted,
// so Bit would read "working" forever after an Esc. So an assistant message or
// tool result only counts as live work when NO interrupt precedes it back to the
// nearest user prompt; an interrupt seen first wins and closes the turn.
func lastTurnOpen(entries []Entry) bool {
	sawWork := false // a late assistant/tool result, pending an interrupt check
	for i := len(entries) - 1; i >= 0; i-- {
		switch entries[i].Kind {
		case KindAssistant:
			if entries[i].StopReason == "end_turn" {
				return false
			}
			sawWork = true
		case KindToolResult:
			sawWork = true
		case KindUserPrompt:
			return true
		case KindInterrupt:
			return false
		}
	}
	// No prompt/interrupt in view (the dispatching turn boundary scrolled out of
	// the tail): open iff there was unfinished work.
	return sawWork
}

func containsAgentID(s string) bool {
	return strings.Contains(s, "agentId:")
}
