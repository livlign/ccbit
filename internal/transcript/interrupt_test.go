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
