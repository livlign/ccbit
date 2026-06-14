// Package diag turns a failed build/test's captured output into a short,
// glanceable reason for the status line — the first concrete error it can find,
// or a user-defined signature message. Pure local text parsing: no network and
// no model call (PRD §10 tier 3 is deliberately omitted to keep ccbit a
// transcript-only, no-daemon tool).
package diag

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Diagnose returns a one-line reason for a failure, or "" when nothing
// trustworthy can be extracted (the caller then shows a plain "build failed").
// A user signature (tier 2) wins over the built-in heuristic (tier 1.5).
func Diagnose(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if m := matchSignature(text); m != "" {
		return m
	}
	return reason(text)
}

var (
	// "path/file.ext:line[:col]: message" — Go, gcc/clang, tsc, eslint, etc.
	compilerRe = regexp.MustCompile(`([\w./\\-]+\.\w+:\d+(?::\d+)?:\s+\S.*)$`)
	// A named Go test failure.
	goFailRe = regexp.MustCompile(`^\s*--- FAIL:\s+(\S+)`)
	// A generic "error: <message>" line (most build tools emit one).
	errorLineRe = regexp.MustCompile(`(?i)\berror:\s*(\S.*)$`)
	// The exit-code line, always present on a failed Bash result (§6.3).
	exitCodeRe = regexp.MustCompile(`(?i)\bexit code\s+\d+`)
)

// reason is the built-in heuristic: prefer a concrete error location or named
// test failure, then an explicit error message, then the bare exit code. Each
// tier is high-signal, so a miss returns "" rather than a guess.
func reason(text string) string {
	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		if m := goFailRe.FindStringSubmatch(ln); m != nil {
			return m[1] + " failed"
		}
		if m := compilerRe.FindStringSubmatch(ln); m != nil {
			return clean(m[1])
		}
	}
	for _, ln := range lines {
		if m := errorLineRe.FindStringSubmatch(ln); m != nil {
			if msg := clean(m[1]); msg != "" {
				return msg
			}
		}
	}
	if loc := exitCodeRe.FindString(text); loc != "" {
		return clean(loc)
	}
	return ""
}

// clean collapses runs of whitespace so a wrapped or tab-indented error reads
// as one tidy line.
func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type signature struct {
	re  *regexp.Regexp
	msg string
}

// matchSignature returns the message of the first user signature whose pattern
// matches the failure text, or "".
func matchSignature(text string) string {
	for _, s := range loadSignatures() {
		if s.re.MatchString(text) {
			return s.msg
		}
	}
	return ""
}

// signaturePath is the user's signature file: $XDG_CONFIG_HOME/ccbit/
// error-signatures, else ~/.config/ccbit/error-signatures. Absent is fine —
// tier 2 is opt-in.
func signaturePath() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "ccbit", "error-signatures")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "ccbit", "error-signatures")
	}
	return ""
}

// loadSignatures parses the signature file: one "pattern<TAB>message" per line,
// "#" comments and blanks ignored. The pattern is a case-insensitive regexp;
// unparsable lines are skipped so one typo can't break the whole file.
func loadSignatures() []signature {
	p := signaturePath()
	if p == "" {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []signature
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		pat := strings.TrimSpace(line[:tab])
		msg := strings.TrimSpace(line[tab+1:])
		if pat == "" || msg == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			continue
		}
		out = append(out, signature{re: re, msg: msg})
	}
	return out
}
