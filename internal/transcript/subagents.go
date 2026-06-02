package transcript

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AgentInfo summarizes subagent activity for the current turn, derived from the
// per-session subagents directory that current Claude Code writes alongside the
// transcript: <transcript-dir>/<session-id>/subagents/agent-<id>.jsonl.
//
// This is the reliable running-agent signal: the main transcript records nothing
// while a subagent runs (the Task spawn itself trails ~one inference), so a long
// agent run would otherwise look like a stall.
type AgentInfo struct {
	Running   int
	Done      int
	Latest    time.Time // freshest running-agent activity (file mtime)
	HasLatest bool
	Found     bool // the subagents dir existed; counts are authoritative when true
}

// agentStaleFactor times the stall threshold to bound how long a silent subagent
// file is still considered "running" before it's treated as dead/abandoned.
const agentStaleFactor = 3

// ScanSubagents inspects the session's subagents directory. turnStart scopes the
// "done" count to the current turn (and prunes historical agent files cheaply).
func ScanSubagents(transcriptPath string, now time.Time, stall time.Duration, turnStart time.Time, hasTurnStart bool) AgentInfo {
	dir := subagentsDir(transcriptPath)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return AgentInfo{}
	}
	info := AgentInfo{Found: true}
	runWindow := time.Duration(agentStaleFactor) * stall
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		mt := fi.ModTime()
		// Files untouched since before this turn are prior-turn agents: not
		// running now, and not "done this turn". Skip without reading them.
		if hasTurnStart && mt.Before(turnStart) {
			continue
		}
		if subagentEnded(filepath.Join(dir, name)) {
			info.Done++
			continue
		}
		if now.Sub(mt) < runWindow {
			info.Running++
			if mt.After(info.Latest) {
				info.Latest, info.HasLatest = mt, true
			}
		}
	}
	return info
}

func subagentsDir(transcriptPath string) string {
	base := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	return filepath.Join(filepath.Dir(transcriptPath), base, "subagents")
}

// subagentEnded reports whether a subagent's last entry is an end_turn assistant
// message (its final reply), i.e. the agent has finished.
func subagentEnded(path string) bool {
	line := lastLine(path)
	if line == nil {
		return false
	}
	var re rawEntry
	if json.Unmarshal(line, &re) != nil || re.Type != "assistant" {
		return false
	}
	var m rawMessage
	if json.Unmarshal(re.Message, &m) != nil {
		return false
	}
	return m.StopReason != nil && *m.StopReason == "end_turn"
}

// lastLine returns the last non-empty line of a file, reading only its tail.
func lastLine(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	const window = 16 << 10
	if start := fi.Size() - window; start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) > 0 {
			return lines[i]
		}
	}
	return nil
}
