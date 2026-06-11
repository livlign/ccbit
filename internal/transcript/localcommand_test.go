package transcript

import "testing"

// A local slash command (/mcp, /config, ...) writes a user entry whose string
// content is the command echo and its stdout. The model never runs, so these
// must be skipped as chrome — not parsed as prompts, and not left holding the
// turn open (which would read as "working" while nothing is running).
func TestLocalCommandIsNotAPrompt(t *testing.T) {
	cases := []string{
		`<command-name>/mcp</command-name>\n<command-message>mcp</command-message>\n<command-args></command-args>`,
		`<local-command-stdout>Authentication successful. Connected to mixpanel.</local-command-stdout>`,
		`<local-command-stderr>boom</local-command-stderr>`,
	}
	for _, content := range cases {
		line := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"` + content + `"}}`
		entries := parseFixture(line)
		if len(entries) != 0 {
			t.Fatalf("content %q: got %d entries, want 0 (chrome skipped)", content, len(entries))
		}
	}
}

func TestLocalCommandDoesNotHoldTurnOpen(t *testing.T) {
	prompt := `{"type":"user","timestamp":"` + taskTS + `","promptId":"p1","message":{"role":"user","content":"do the thing"}}`
	done := `{"type":"assistant","timestamp":"` + taskTS + `","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"all done"}]}}`
	mcpEcho := `{"type":"user","timestamp":"` + taskTS + `","message":{"role":"user","content":"<command-name>/mcp</command-name>"}}`
	mcpOut := `{"type":"user","timestamp":"` + taskTS + `","message":{"role":"user","content":"<local-command-stdout>Connected to mixpanel.</local-command-stdout>"}}`

	turns := BuildTurns(parseFixture(prompt, done, mcpEcho, mcpOut))
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1 (the local command must not start a turn)", len(turns))
	}
	if turns[0].Open {
		t.Fatalf("turn left open after a completed turn followed by a local command; would read as working")
	}
}
