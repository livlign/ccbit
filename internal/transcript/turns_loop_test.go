package transcript

import (
	"fmt"
	"strings"
	"testing"
)

const loopTS = "2026-06-05T10:00:00.000Z"

func upEntry() string {
	return `{"type":"user","timestamp":"` + loopTS + `","promptId":"p","message":{"role":"user","content":"go"}}`
}

func editEntry(useID, path string) string {
	return `{"type":"assistant","timestamp":"` + loopTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"` + useID + `","name":"Edit","input":{"file_path":"` + path + `"}}]}}`
}

func bashEntry(useID, cmd string) string {
	return `{"type":"assistant","timestamp":"` + loopTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"` + useID + `","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
}

func passEntry(useID string) string {
	return `{"type":"user","timestamp":"` + loopTS + `","toolUseResult":{"stdout":"ok"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + useID + `","is_error":false,"content":"ok"}]}}`
}

func failEntry(useID string) string {
	return `{"type":"user","timestamp":"` + loopTS + `","toolUseResult":"Error: Exit code 1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + useID + `","is_error":true,"content":"Exit code 1"}]}}`
}

func lastTurn(t *testing.T, lines ...string) Turn {
	t.Helper()
	turns := BuildTurns(Parse(strings.NewReader(strings.Join(lines, "\n") + "\n")))
	if len(turns) == 0 {
		t.Fatal("no turns parsed")
	}
	return turns[len(turns)-1]
}

func TestHotFileChurn(t *testing.T) {
	lines := []string{upEntry()}
	for i := 0; i < 5; i++ {
		lines = append(lines, editEntry(fmt.Sprintf("t%d", i), "D:/proj/a/render.go"))
	}
	lines = append(lines, editEntry("tx", "D:/proj/a/other.go"))
	turn := lastTurn(t, lines...)
	if turn.HotFile != "D:/proj/a/render.go" || turn.HotFileEdits != 5 {
		t.Fatalf("hot file = %q ×%d, want render.go ×5", turn.HotFile, turn.HotFileEdits)
	}
	if len(turn.Edited) != 2 {
		t.Fatalf("distinct edited = %d, want 2 (dedupe keeps working)", len(turn.Edited))
	}
}

func TestFailStreakAndReset(t *testing.T) {
	turn := lastTurn(t,
		upEntry(),
		bashEntry("t1", "go test ./..."), failEntry("t1"),
		bashEntry("t2", "go test ./..."), failEntry("t2"),
		bashEntry("t3", "go test ./..."), failEntry("t3"),
	)
	if turn.FailStreak != 3 {
		t.Fatalf("fail streak = %d, want 3", turn.FailStreak)
	}
	// A pass resets the streak: not a loop anymore.
	turn = lastTurn(t,
		upEntry(),
		bashEntry("t1", "go test ./..."), failEntry("t1"),
		bashEntry("t2", "go test ./..."), failEntry("t2"),
		bashEntry("t3", "go test ./..."), passEntry("t3"),
	)
	if turn.FailStreak != 0 {
		t.Fatalf("fail streak after pass = %d, want 0", turn.FailStreak)
	}
}

func TestShipDetection(t *testing.T) {
	turn := lastTurn(t,
		upEntry(),
		bashEntry("t1", "git add -A && git commit -m x"), passEntry("t1"),
		bashEntry("t2", "git push origin main"), passEntry("t2"),
		bashEntry("t3", "curl -X POST https://jenkins.dev.example/job/lhproduct/build"), passEntry("t3"),
	)
	if !turn.Committed || !turn.Pushed || !turn.Deployed {
		t.Fatalf("committed/pushed/deployed = %v/%v/%v, want all true", turn.Committed, turn.Pushed, turn.Deployed)
	}
	// A FAILED push must not count as shipped.
	turn = lastTurn(t,
		upEntry(),
		bashEntry("t1", "git push origin main"), failEntry("t1"),
	)
	if turn.Pushed {
		t.Fatal("failed push should not set Pushed")
	}
	// Reading a deploy doc is not a deploy.
	turn = lastTurn(t,
		upEntry(),
		bashEntry("t1", "cat DEPLOY.md && grep deploy notes.txt"), passEntry("t1"),
	)
	if turn.Deployed {
		t.Fatal("mentioning deploy in a read-only command should not set Deployed")
	}
}
