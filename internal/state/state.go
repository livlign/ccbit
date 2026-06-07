// Package state derives ccbit's session state from parsed turns, following the
// fixed priority order in the PRD (first match wins).
package state

import (
	"time"

	"github.com/livlign/ccbit/internal/transcript"
)

type State int

const (
	Idle State = iota
	Working
	Agents
	DoneNormal
	DoneRedeemed
	Waiting
	Failed
	Stopped
)

func (s State) String() string {
	switch s {
	case Working:
		return "working"
	case Agents:
		return "agents"
	case DoneNormal:
		return "done"
	case DoneRedeemed:
		return "redeemed"
	case Waiting:
		return "waiting"
	case Failed:
		return "failed"
	case Stopped:
		return "stopped"
	default:
		return "idle"
	}
}

// toolGrace is how long an in-flight tool call (tool_use flushed, no result
// yet) may keep a quiet turn in Working before it's considered Stopped. Long
// commands legitimately run to their ~10m timeout while the transcript stays
// silent; only beyond that is the session presumed hung.
const toolGrace = 15 * time.Minute

// thinkGrace bounds how long a quiet turn with NOTHING pending stays Working.
// The transcript is silent while the model composes — extended thinking runs
// minutes without a single entry — and a permission prompt always leaves a
// pending tool_use behind, so bare silence is almost always thinking, not a
// stall. Past this ceiling it's presumed hung.
const thinkGrace = 15 * time.Minute

// DefaultStall is how long an open turn may go without a new entry before it is
// considered Stopped. Set above the typical gap of a long pure-text generation
// (which writes no transcript entries while composing) to avoid false stalls;
// override per-environment with CCBIT_STALL (seconds).
const DefaultStall = 90 * time.Second

// View is the rendered-state snapshot for the current turn.
type View struct {
	State         State
	Turn          transcript.Turn
	AgentsRunning int
	AgentsDone    int
	Elapsed       time.Duration
	HasElapsed    bool
	LastAge       time.Duration
	HasLastAge    bool

	// InFlight is the tool call currently executing (display text + how long),
	// when one is — the "it's running a long command, not hung" readout.
	InFlight    string
	InFlightFor time.Duration
	HasInFlight bool

	// Thinking is a quiet Working turn with nothing pending: the model has the
	// floor and is composing. The render notes it so the silence reads as
	// deliberate ("thinking (2m)") rather than decaying into a false Stopped.
	Thinking bool
}

// Derive computes the View from the full ordered list of turns and subagent
// activity. agents.Found means the subagents dir was read (authoritative counts);
// otherwise it falls back to the main transcript's spawn/completion counts.
func Derive(turns []transcript.Turn, now time.Time, stall time.Duration, agents transcript.AgentInfo) View {
	v := View{State: Idle}
	if len(turns) == 0 {
		return v
	}
	cur := turns[len(turns)-1]
	v.Turn = cur

	running, done := agents.Running, agents.Done
	if !agents.Found {
		if running = cur.Spawns - cur.Completions; running < 0 {
			running = 0
		}
		done = cur.Completions
	}
	v.AgentsRunning = running
	v.AgentsDone = done

	if cur.HasStart {
		v.Elapsed, v.HasElapsed = now.Sub(cur.Start), true
	}
	// A running subagent writes to its own sidechain, not the main transcript, so
	// fold its freshness into "last activity" — otherwise the turn looks stalled.
	lastAct, hasLast := cur.Last, cur.HasLast
	if agents.HasLatest && agents.Latest.After(lastAct) {
		lastAct, hasLast = agents.Latest, true
	}
	stalled := false
	if hasLast {
		v.LastAge, v.HasLastAge = now.Sub(lastAct), true
		stalled = v.LastAge > stall
	}

	// An in-flight tool call keeps a quiet turn alive: the transcript is silent
	// while a long command runs, but the flushed tool_use proves work is
	// happening. The grace window expires for tools that outlive any plausible
	// timeout (genuinely hung).
	toolActive := false
	if cur.Open && cur.InFlight != "" {
		since := cur.Last
		if cur.HasInFlight {
			since = cur.InFlightSince
		}
		v.InFlight = cur.InFlight
		v.InFlightFor = now.Sub(since)
		v.HasInFlight = true
		toolActive = v.InFlightFor < toolGrace
	}

	latestErr, earlierErr, hasBuild := analyzeBuilds(cur.Builds)

	quiet := cur.Open && stalled && cur.Pending == "" && running == 0 && !toolActive
	switch {
	// Quiet with something dispatched and unanswered (instant tool with no
	// result: a permission prompt or a hang) stops at the stall threshold;
	// quiet with nothing pending is the model thinking and gets thinkGrace.
	case quiet && (cur.PendingTools > 0 || v.LastAge > thinkGrace):
		v.State = Stopped
	case hasBuild && latestErr:
		v.State = Failed
	case cur.Pending != "":
		v.State = Waiting
	case running > 0:
		v.State = Agents
	case cur.Open:
		v.State = Working
		v.Thinking = quiet
	case hasBuild && !latestErr && earlierErr:
		v.State = DoneRedeemed
	case hasBuild && !latestErr:
		v.State = DoneNormal
	case len(cur.Edited) > 0 || cur.Committed || cur.Pushed || cur.Deployed:
		// Plenty of turns never build or test (docs, configs, commit-and-push):
		// finishing with edits or a ship action is still a done turn worth a
		// recap, not a blank idle.
		v.State = DoneNormal
	default:
		v.State = Idle
	}

	return v
}

// analyzeBuilds returns whether the most recent build/test errored, whether any
// earlier one errored (for the redeemed state), and whether there was any.
func analyzeBuilds(builds []transcript.BuildResult) (latestErr, earlierErr, has bool) {
	if len(builds) == 0 {
		return false, false, false
	}
	for i, b := range builds {
		if i < len(builds)-1 && b.IsError {
			earlierErr = true
		}
	}
	return builds[len(builds)-1].IsError, earlierErr, true
}
