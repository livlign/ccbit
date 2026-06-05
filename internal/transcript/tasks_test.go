package transcript

import (
	"strings"
	"testing"
)

const taskTS = "2026-06-05T10:00:00.000Z"

func taskCreate(useID, subject, activeForm string) string {
	return `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"` + useID + `","name":"TaskCreate","input":{"subject":"` + subject + `","description":"d","activeForm":"` + activeForm + `"}}]}}`
}

func taskCreated(useID, taskID, subject string) string {
	return `{"type":"user","timestamp":"` + taskTS + `","toolUseResult":{"x":1},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + useID + `","is_error":false,"content":"Task #` + taskID + ` created successfully: ` + subject + `"}]}}`
}

func taskUpdate(useID, taskID, status string) string {
	return `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"` + useID + `","name":"TaskUpdate","input":{"taskId":"` + taskID + `","status":"` + status + `"}}]}}`
}

func parseFixture(lines ...string) []Entry {
	return Parse(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

func TestTasksReplay(t *testing.T) {
	entries := parseFixture(
		taskCreate("u1", "Wire auth flow", "Wiring auth flow"),
		taskCreated("u1", "1", "Wire auth flow"),
		taskCreate("u2", "Write tests", "Writing tests"),
		taskCreated("u2", "2", "Write tests"),
		taskCreate("u3", "Update docs", "Updating docs"),
		taskCreated("u3", "3", "Update docs"),
		taskUpdate("u4", "1", "in_progress"),
		taskUpdate("u5", "1", "completed"),
		taskUpdate("u6", "2", "in_progress"),
	)
	s := Tasks(entries)
	if s.Total != 3 || s.Done != 1 {
		t.Fatalf("total/done = %d/%d, want 3/1", s.Total, s.Done)
	}
	if s.Current != "Writing tests" {
		t.Fatalf("current = %q, want activeForm of in-progress task", s.Current)
	}
}

func TestTasksAllDoneAndCancelled(t *testing.T) {
	entries := parseFixture(
		taskCreate("u1", "A", "Doing A"),
		taskCreated("u1", "1", "A"),
		taskCreate("u2", "B", "Doing B"),
		taskCreated("u2", "2", "B"),
		taskUpdate("u3", "1", "completed"),
		taskUpdate("u4", "2", "cancelled"),
	)
	s := Tasks(entries)
	if s.Total != 1 || s.Done != 1 || s.Current != "" {
		t.Fatalf("got %+v, want total=1 done=1 current empty (cancelled dropped)", s)
	}
}

func TestTasksUpdateBeforeCreateInTail(t *testing.T) {
	// The create scrolled out of the tail window: an update alone still counts
	// the task (as a placeholder) without inventing a current label.
	entries := parseFixture(taskUpdate("u1", "7", "in_progress"))
	s := Tasks(entries)
	if s.Total != 1 || s.Done != 0 || s.Current != "" {
		t.Fatalf("got %+v, want placeholder task with no label", s)
	}
}

func TestTasksIgnoresQuotedCreateText(t *testing.T) {
	// A Bash result that merely quotes the create phrase (e.g. grepping
	// transcripts) must not register a phantom task.
	entries := parseFixture(
		`{"type":"assistant","timestamp":"`+taskTS+`","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"grep created *.jsonl"}}]}}`,
		`{"type":"user","timestamp":"`+taskTS+`","toolUseResult":{"stdout":"x"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","is_error":false,"content":"{\"content\":\"Task #2 created successfully: Phantom\"}"}]}}`,
	)
	if s := Tasks(entries); s.Total != 0 {
		t.Fatalf("quoted create text registered a task: %+v", s)
	}
}

func TestTasksNone(t *testing.T) {
	entries := parseFixture(
		`{"type":"user","timestamp":"` + taskTS + `","promptId":"p","message":{"role":"user","content":"hi"}}`,
	)
	if s := Tasks(entries); s.Total != 0 {
		t.Fatalf("expected zero summary, got %+v", s)
	}
}
