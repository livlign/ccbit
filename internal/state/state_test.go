package state

import (
	"strings"
	"testing"
	"time"

	"github.com/livlign/ccbit/internal/transcript"
)

const base = "2026-06-02T10:00:00.000Z"

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// JSONL fixture builders mirroring the measured transcript schema.
func up(ts, c string) string {
	return `{"type":"user","timestamp":"` + ts + `","promptId":"p","message":{"role":"user","content":"` + c + `"}}`
}
func asstTool(ts string, blocks ...string) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"tool_use","content":[` + strings.Join(blocks, ",") + `]}}`
}
func asstEnd(ts string) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`
}
func tuBash(id, cmd string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + cmd + `"}}`
}
func tuWrite(id, p string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"Write","input":{"file_path":"` + p + `"}}`
}
func tuTask(id string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"Task","input":{"subagent_type":"g","prompt":"x"}}`
}
func tuAsk(id string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"AskUserQuestion","input":{}}`
}
func resPass(ts, id string) string {
	return `{"type":"user","timestamp":"` + ts + `","promptId":"p","toolUseResult":{"stdout":"ok","stderr":""},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":false,"content":"ok"}]}}`
}
func resFail(ts, id string) string {
	return `{"type":"user","timestamp":"` + ts + `","toolUseResult":"Error: Exit code 1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":true,"content":"Exit code 1"}]}}`
}
func resTask(ts, id string) string {
	return `{"type":"user","timestamp":"` + ts + `","promptId":"p","toolUseResult":{"x":1},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":[{"type":"text","text":"DONE"},{"type":"text","text":"agentId: abc"}]}]}}`
}

func j(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func derive(fixture string, now time.Time) View {
	entries := transcript.Parse(strings.NewReader(fixture))
	// No subagents dir in fixtures: AgentInfo{} (Found=false) falls back to the
	// main-transcript Task spawn/completion counts.
	return Derive(transcript.BuildTurns(entries), now, DefaultStall, transcript.AgentInfo{})
}

func TestStatePriority(t *testing.T) {
	soon := mustTime(base).Add(10 * time.Second)
	stalled := mustTime(base).Add(120 * time.Second)

	cases := []struct {
		name    string
		fixture string
		now     time.Time
		want    State
	}{
		{"idle", j(up(base, "x"), asstEnd(base)), soon, Idle},
		{"working", j(up(base, "x"), asstTool(base, tuWrite("t1", "D:/proj/a/x.go"))), soon, Working},
		{"failed", j(up(base, "x"), asstTool(base, tuBash("t1", "dotnet build")), resFail(base, "t1"), asstEnd(base)), soon, Failed},
		{"done", j(up(base, "x"), asstTool(base, tuWrite("t0", "D:/proj/a/x.go")), asstTool(base, tuBash("t1", "dotnet build")), resPass(base, "t1"), asstEnd(base)), soon, DoneNormal},
		{"redeemed", j(up(base, "x"), asstTool(base, tuBash("t1", "dotnet build")), resFail(base, "t1"), asstTool(base, tuBash("t2", "dotnet build")), resPass(base, "t2"), asstEnd(base)), soon, DoneRedeemed},
		{"waiting", j(up(base, "x"), asstTool(base, tuAsk("t1"))), soon, Waiting},
		{"agents", j(up(base, "x"), asstTool(base, tuTask("t1")), asstTool(base, tuTask("t2")), asstTool(base, tuTask("t3")), resTask(base, "t1")), soon, Agents},
		{"stopped", j(up(base, "x"), asstTool(base, tuWrite("t1", "D:/proj/a/Order.go"))), stalled, Stopped},
		// Regression: a running agent (open turn, no entries for >stall) is Agents, not Stopped.
		{"running-agent-not-stopped", j(up(base, "x"), asstTool(base, tuTask("t1"))), stalled, Agents},
		// Failed outranks an in-progress (open) next action.
		{"failed-outranks-working", j(up(base, "x"), asstTool(base, tuBash("t1", "dotnet build")), resFail(base, "t1"), asstTool(base, tuWrite("t2", "D:/proj/a/x.go"))), soon, Failed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := derive(c.fixture, c.now).State
			if got != c.want {
				t.Fatalf("state = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAgentCount(t *testing.T) {
	v := derive(j(up(base, "x"), asstTool(base, tuTask("t1")), asstTool(base, tuTask("t2")), asstTool(base, tuTask("t3")), resTask(base, "t1")), mustTime(base).Add(5*time.Second))
	if v.AgentsRunning != 2 || v.AgentsDone != 1 {
		t.Fatalf("running=%d done=%d, want 2/1", v.AgentsRunning, v.AgentsDone)
	}
}
