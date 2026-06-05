package transcript

import "regexp"

// TaskSummary is the session's task-tool plan replayed from the transcript tail:
// what the assistant said it would do, and where it is. Zero-config insight —
// the TaskCreate/TaskUpdate tool calls are already in the transcript.
type TaskSummary struct {
	Total   int
	Done    int
	Current string // activeForm (else subject) of the task being worked now, "" if none
}

// taskCreatedRe matches the TaskCreate tool_result text, which is the
// authoritative id assignment: "Task #2 created successfully: <subject>".
// Anchored: a Bash result merely quoting this phrase (a grep of transcripts,
// say) must not register a phantom task.
var taskCreatedRe = regexp.MustCompile(`^\s*Task #(\d+) created successfully: (.+)`)

type taskRec struct {
	subject    string
	activeForm string
	status     string
}

// Tasks replays TaskCreate results (id + subject) and TaskUpdate inputs (status
// moves) into a summary. Tasks created before the tail window appear as
// placeholders when an update references them, so counts stay consistent.
// The latest tool_use can trail its result by one inference; both sources are
// read so the summary converges within a render or two.
func Tasks(entries []Entry) TaskSummary {
	tasks := map[string]*taskRec{}
	var order []string
	activeByUse := map[string]string{} // TaskCreate tool_use id -> activeForm
	currentID := ""

	get := func(id string) *taskRec {
		if t, ok := tasks[id]; ok {
			return t
		}
		t := &taskRec{status: "pending"}
		tasks[id] = t
		order = append(order, id)
		return t
	}

	for _, e := range entries {
		switch e.Kind {
		case KindAssistant:
			for _, tu := range e.ToolUses {
				switch tu.Name {
				case "TaskCreate":
					if tu.ActiveForm != "" {
						activeByUse[tu.ID] = tu.ActiveForm
					}
				case "TaskUpdate":
					if tu.TaskID == "" || tu.Status == "" {
						continue
					}
					t := get(tu.TaskID)
					t.status = tu.Status
					if tu.Status == "in_progress" {
						currentID = tu.TaskID
					}
				}
			}
		case KindToolResult:
			// Only TaskCreate results assign ids. An unknown tool name ("") is
			// tolerated for the trailing edge where the tool_use hasn't flushed
			// yet; a result correlated to any OTHER tool (e.g. a Bash grep whose
			// output quotes the phrase) is rejected.
			if e.ResultToolName != "" && e.ResultToolName != "TaskCreate" {
				continue
			}
			m := taskCreatedRe.FindStringSubmatch(e.ResultText)
			if m == nil {
				continue
			}
			t := get(m[1])
			t.subject = m[2]
			if af, ok := activeByUse[e.ToolUseID]; ok {
				t.activeForm = af
			}
		}
	}

	var s TaskSummary
	for _, id := range order {
		t := tasks[id]
		switch t.status {
		case "cancelled", "deleted":
			continue // off the plan; don't count
		case "completed":
			s.Done++
		}
		s.Total++
	}
	if t, ok := tasks[currentID]; ok && t.status == "in_progress" {
		s.Current = t.activeForm
		if s.Current == "" {
			s.Current = t.subject
		}
	}
	if s.Current == "" {
		// Fall back to the first in-progress task in creation order (e.g. the
		// in_progress TaskUpdate scrolled out of the tail window).
		for _, id := range order {
			if t := tasks[id]; t.status == "in_progress" && (t.activeForm != "" || t.subject != "") {
				s.Current = t.activeForm
				if s.Current == "" {
					s.Current = t.subject
				}
				break
			}
		}
	}
	return s
}
