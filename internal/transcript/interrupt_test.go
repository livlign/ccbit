package transcript

import "testing"

func TestInterruptClosesTurn(t *testing.T) {
	prompt := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"do the thing"}}`
	use := `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"git commit"}}]}}`
	rejected := `{"type":"user","timestamp":"` + taskTS + `","toolUseResult":"rejected","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}`
	marker := `{"type":"user","timestamp":"` + taskTS + `","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`

	// Rejection result alone keeps the turn open (more may follow)...
	turns := BuildTurns(parseFixture(prompt, use, rejected))
	if len(turns) != 1 || !turns[0].Open {
		t.Fatalf("turns = %+v, want one open turn before the marker", turns)
	}
	// ...the interrupt marker closes it: Bit must not read "working" forever.
	turns = BuildTurns(parseFixture(prompt, use, rejected, marker))
	if len(turns) != 1 || turns[0].Open {
		t.Fatalf("turns = %+v, want one closed turn after the interrupt", turns)
	}
}

func TestInterruptThenStragglerResultStaysClosed(t *testing.T) {
	// Interrupting a running tool: the interrupt marker lands first, then the
	// tool's result still flushes in. The trailing result must not re-open the
	// turn — Bit must not read "working" after an Esc.
	prompt := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"run it"}}`
	use := `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"sleep 60"}}]}}`
	marker := `{"type":"user","timestamp":"` + taskTS + `","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	straggler := `{"type":"user","timestamp":"` + taskTS + `","toolUseResult":{"x":1},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","is_error":false,"content":"done"}]}}`

	turns := BuildTurns(parseFixture(prompt, use, marker, straggler))
	if len(turns) != 1 || turns[0].Open {
		t.Fatalf("turns = %+v, want one closed turn after interrupt + straggler result", turns)
	}
}

func TestInterruptThenStragglerAssistantStaysClosed(t *testing.T) {
	// Interrupting an in-flight request: the marker lands, then late assistant
	// messages from the killed request still flush in (stop_reason "tool_use"),
	// followed by their tool results — all with no new user prompt. None of these
	// stragglers may re-open the turn; Bit must not read "working" after an Esc.
	prompt := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"go"}}`
	use := `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"sleep 60"}}]}}`
	marker := `{"type":"user","timestamp":"` + taskTS + `","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	lateThink := `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"more"}]}}`
	lateUse := `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"echo hi"}}]}}`
	lateResult := `{"type":"user","timestamp":"` + taskTS + `","toolUseResult":{"x":1},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","is_error":false,"content":"hi"}]}}`

	turns := BuildTurns(parseFixture(prompt, use, marker, lateThink, lateUse, lateResult))
	if len(turns) != 1 || turns[0].Open {
		t.Fatalf("turns = %+v, want one closed turn after interrupt + straggler assistant", turns)
	}
}

func TestInterruptEscVariant(t *testing.T) {
	prompt := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"hi"}}`
	marker := `{"type":"user","timestamp":"` + taskTS + `","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	turns := BuildTurns(parseFixture(prompt, marker))
	if len(turns) != 1 || turns[0].Open {
		t.Fatalf("turns = %+v, want closed turn after plain Esc interrupt", turns)
	}
}

func TestInterruptTextTypedByUserIsAPrompt(t *testing.T) {
	// The marker shape is array content; the same words typed by a user arrive
	// as string content and must stay a normal prompt.
	typed := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"[Request interrupted by user]"}}`
	entries := parseFixture(typed)
	if len(entries) != 1 || entries[0].Kind != KindUserPrompt {
		t.Fatalf("entries = %+v, want a single user prompt", entries)
	}
}
