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

	"github.com/livlign/ccbit/internal/gitx"
	"github.com/livlign/ccbit/internal/input"
	"github.com/livlign/ccbit/internal/memory"
	"github.com/livlign/ccbit/internal/render"
	"github.com/livlign/ccbit/internal/sessions"
	"github.com/livlign/ccbit/internal/state"
	"github.com/livlign/ccbit/internal/transcript"
)

func main() {
	now := time.Now()
	in := input.Parse(os.Stdin)

	root := repoRoot(in.Cwd)
	label := projectLabel(root, in.ProjectDir, in.Cwd)

	// Durable per-project memory: learned thresholds derived from past turns.
	memKey := memory.Key(root, label)
	stats := memory.Load(memKey)
	stall := stallThreshold(stats)

	var turns []transcript.Turn
	var agents transcript.AgentInfo
	var title string
	var tasks transcript.TaskSummary
	if in.TranscriptPath != "" {
		if entries, err := transcript.ReadTail(in.TranscriptPath); err == nil {
			turns = transcript.BuildTurns(entries)
			title = transcript.LatestTitle(entries)
			tasks = transcript.Tasks(entries)
		}
	}
	turnStart, hasStart := currentTurnStart(turns)
	if in.TranscriptPath != "" {
		agents = transcript.ScanSubagents(in.TranscriptPath, now, stall, turnStart, hasStart)
	}

	v := state.Derive(turns, now, stall, agents)

	// Fold the latest completed turn into memory exactly once, using the
	// per-session high-water mark so the per-second renders don't double-count.
	prev := sessions.Snapshot(in.SessionID)
	lastTurnStart := prev.LastTurnStart
	if t := newestClosedTurn(turns); t != nil && t.HasStart && t.Start.Unix() > prev.LastTurnStart {
		dur := time.Duration(0)
		if t.HasLast {
			dur = t.Last.Sub(t.Start)
		}
		stats.Record(dur, t.MaxGap)
		memory.Save(memKey, stats)
		lastTurnStart = t.Start.Unix()
	}

	// Per-turn lines-changed: the stdin cost counters are session-cumulative, so
	// snapshot them at each turn start (carried in the heartbeat) and show the
	// delta — the recap's line numbers then match its per-turn file count.
	baseTurn, baseAdded, baseRemoved := prev.LinesBaseTurn, prev.LinesBaseAdded, prev.LinesBaseRemoved
	turnAdded, turnRemoved := 0, 0
	if hasStart {
		if turnStart.Unix() != baseTurn {
			baseTurn, baseAdded, baseRemoved = turnStart.Unix(), in.LinesAdded, in.LinesRemoved
		}
		turnAdded = max(0, in.LinesAdded-baseAdded)
		turnRemoved = max(0, in.LinesRemoved-baseRemoved)
	}

	// Heartbeat: record this session's state for sibling sessions to read, and
	// get back our own context-window velocity from the rolling ctx samples.
	trend := sessions.Record(sessions.Beat{
		SessionID:        in.SessionID,
		State:            v.State.String(),
		Project:          label,
		Title:            title,
		LastTurnStart:    lastTurnStart,
		LinesBaseTurn:    baseTurn,
		LinesBaseAdded:   baseAdded,
		LinesBaseRemoved: baseRemoved,
	}, in.CtxPct, now)

	cols := envInt("COLUMNS")
	ctx := render.Ctx{
		In:       in,
		RepoRoot: root,
		// RepoRoot is resolved with a disposable cache so a 1s refresh interval
		// does not spawn git every render.
		Cols:        cols,
		Narrow:      cols > 0 && cols < render.NarrowCols,
		Frame:       int((now.Unix() / 2) % 2),
		FrameFast:   int(now.Unix() % 2),
		ColorOn:     os.Getenv("NO_COLOR") == "",
		Now:         now,
		Trend:            trend,
		Siblings:         sessions.Active(in.SessionID, now),
		TypicalTurn:      stats.TypicalTurn(),
		TurnLinesAdded:   turnAdded,
		TurnLinesRemoved: turnRemoved,
		Tasks:            tasks,
		ProjectLabel:     label,
		Git:              gitInfo(root, now),
	}

	for _, line := range render.Render(v, ctx) {
		fmt.Println(line)
	}
}

// gitInfo assembles the ambient git facts: branch from a plain .git/HEAD read
// (every render), dirty/ahead/behind from a TTL-cached git status.
func gitInfo(root string, now time.Time) gitx.Info {
	g := gitx.Info{Branch: gitx.Branch(root)}
	if g.Branch != "" {
		g.Dirty, g.Ahead, g.Behind = gitx.Status(root, now)
	}
	return g
}

// newestClosedTurn returns the most recent finished turn (the current turn is
// skipped while still open), or nil if none has completed.
func newestClosedTurn(turns []transcript.Turn) *transcript.Turn {
	for i := len(turns) - 1; i >= 0; i-- {
		if !turns[i].Open {
			return &turns[i]
		}
	}
	return nil
}

func currentTurnStart(turns []transcript.Turn) (time.Time, bool) {
	if n := len(turns); n > 0 && turns[n-1].HasStart {
		return turns[n-1].Start, true
	}
	return time.Time{}, false
}

// projectLabel is the short name shown for this session in a sibling's digest:
// the git repo basename, else the project dir, else the cwd.
func projectLabel(repoRoot, projectDir, cwd string) string {
	for _, d := range []string{repoRoot, projectDir, cwd} {
		if d != "" {
			return filepath.Base(d)
		}
	}
	return ""
}

// stallThreshold is the Stopped cutoff. An explicit CCBIT_STALL wins; otherwise
// it's the per-project learned value, falling back to the fixed default until
// enough turns have been seen.
func stallThreshold(stats memory.Stats) time.Duration {
	if n := envInt("CCBIT_STALL"); n > 0 {
		return time.Duration(n) * time.Second
	}
	return stats.LearnedStall(state.DefaultStall)
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
