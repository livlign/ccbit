package diag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReasonHeuristics(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"empty", "", ""},
		{"whitespace", "   \n\t", ""},
		{"go compiler", "Exit code 1\n# pkg\ninternal/render/render.go:42:3: undefined: foo",
			"internal/render/render.go:42:3: undefined: foo"},
		{"go test fail", "Exit code 1\n--- FAIL: TestThing (0.00s)\n    x_test.go:9: boom\nFAIL",
			"TestThing failed"},
		{"named test beats compiler order", "--- FAIL: TestA\nfoo.go:1:1: nope", "TestA failed"},
		{"rust error line", "Exit code 101\nerror: could not compile `app` due to 2 errors",
			"could not compile `app` due to 2 errors"},
		{"collapses whitespace", "error:   too    many\t spaces", "too many spaces"},
		{"exit code fallback", "Error: Exit code 2\n(no recognizable detail)", "Exit code 2"},
		{"nothing recognizable", "something went sideways", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Diagnose(c.text); got != c.want {
				t.Errorf("Diagnose(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

func TestSignatureOverridesHeuristic(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ccbit")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	// pattern<TAB>message, plus a comment and a malformed line to skip.
	body := "# my signatures\n" +
		"401|codeartifact\trenew AWS SSO for codeartifact\n" +
		"no-tab-here-is-ignored\n"
	if err := os.WriteFile(filepath.Join(cfg, "error-signatures"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)

	// A signature match wins even though the text also has a compiler line.
	got := Diagnose("Exit code 1\nfoo.go:1:1: bad\n401 Unauthorized from codeartifact")
	if got != "renew AWS SSO for codeartifact" {
		t.Fatalf("signature should win, got %q", got)
	}
	// No signature match falls back to the heuristic.
	if got := Diagnose("Exit code 1\nfoo.go:1:1: bad"); got != "foo.go:1:1: bad" {
		t.Fatalf("non-matching text should fall back to heuristic, got %q", got)
	}
}

func TestNoSignatureFileIsFine(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // exists, but no ccbit/error-signatures
	if got := Diagnose("error: boom"); got != "boom" {
		t.Fatalf("missing signature file should not break diagnosis, got %q", got)
	}
}
