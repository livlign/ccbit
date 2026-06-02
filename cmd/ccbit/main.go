// Command ccbit is a session-awareness status line for Claude Code. It reads the
// stdin JSON Claude Code provides, parses the session transcript, derives the
// current state, and prints two-to-three lines led by the stateful face "Bit".
//
// It is invoked as the statusLine command, reads to EOF, prints, and exits. No
// hooks, no state files; the transcript is the source of truth.
package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/livlign/ccbit/internal/input"
	"github.com/livlign/ccbit/internal/render"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func main() {
	now := time.Now()
	in := input.Parse(os.Stdin)

	stall := stallThreshold()

	var turns []transcript.Turn
	var agents transcript.AgentInfo
	if in.TranscriptPath != "" {
		if entries, err := transcript.ReadTail(in.TranscriptPath); err == nil {
			turns = transcript.BuildTurns(entries)
		}
		turnStart, hasStart := currentTurnStart(turns)
		agents = transcript.ScanSubagents(in.TranscriptPath, now, stall, turnStart, hasStart)
	}

	v := state.Derive(turns, now, stall, agents)

	cols := envInt("COLUMNS")
	ctx := render.Ctx{
		In:       in,
		RepoRoot: repoRoot(in.Cwd),
		// RepoRoot is resolved with a disposable cache so a 1s refresh interval
		// does not spawn git every render.
		Cols:     cols,
		Narrow:   cols > 0 && cols < render.NarrowCols,
		Frame:    int((now.Unix() / 2) % 2),
		ColorOn:  os.Getenv("NO_COLOR") == "",
		Now:      now,
	}

	for _, line := range render.Render(v, ctx) {
		fmt.Println(line)
	}
}

func currentTurnStart(turns []transcript.Turn) (time.Time, bool) {
	if n := len(turns); n > 0 && turns[n-1].HasStart {
		return turns[n-1].Start, true
	}
	return time.Time{}, false
}

// stallThreshold is the Stopped cutoff, overridable via CCBIT_STALL (seconds).
func stallThreshold() time.Duration {
	if n := envInt("CCBIT_STALL"); n > 0 {
		return time.Duration(n) * time.Second
	}
	return state.DefaultStall
}

func envInt(key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return 0
	}
	return n
}

// repoRootTTL bounds how long a cached repo root is trusted before re-resolving.
const repoRootTTL = 6 * time.Hour

// repoRoot resolves the git top-level for dir. It is cached on disk (keyed by
// dir) so a 1s status-line refresh does not spawn git on every render; the cache
// is disposable renderer state, never authoritative. Empty on failure (render
// falls back to the stdin project_dir).
func repoRoot(dir string) string {
	if dir == "" {
		return ""
	}
	cache := repoRootCachePath(dir)
	if fi, err := os.Stat(cache); err == nil && time.Since(fi.ModTime()) < repoRootTTL {
		if b, err := os.ReadFile(cache); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	root := gitToplevel(dir)
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
		_ = os.WriteFile(cache, []byte(root), 0o644)
	}
	return root
}

func repoRootCachePath(dir string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(dir))
	return filepath.Join(os.TempDir(), "ccbit", fmt.Sprintf("root-%08x", h.Sum32()))
}

func gitToplevel(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
