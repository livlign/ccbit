// Package input parses the JSON object Claude Code writes to the status-line
// command's stdin on each render. Missing fields degrade to safe zero values;
// parsing never fails hard.
package input

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

type RateLimit struct {
	UsedPercentage *float64
	ResetsAt       time.Time
	HasReset       bool
}

type Stdin struct {
	SessionID      string
	TranscriptPath string
	Cwd            string
	ModelName      string
	CurrentDir     string
	ProjectDir     string
	Version        string

	CtxPct       *float64
	LinesAdded   int // session-cumulative, from cost.total_lines_added
	LinesRemoved int // session-cumulative, from cost.total_lines_removed
	FiveHour     *RateLimit
	SevenDay     *RateLimit
}

type rawStdin struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Model          struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	Version string `json:"version"`
	Cost    *struct {
		TotalLinesAdded   int `json:"total_lines_added"`
		TotalLinesRemoved int `json:"total_lines_removed"`
	} `json:"cost"`
	ContextWindow *struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour *rawRate `json:"five_hour"`
		SevenDay *rawRate `json:"seven_day"`
	} `json:"rate_limits"`
}

type rawRate struct {
	UsedPercentage *float64        `json:"used_percentage"`
	ResetsAt       json.RawMessage `json:"resets_at"`
}

// Parse reads and decodes the stdin JSON. It returns a best-effort Stdin even on
// malformed input (empty struct), so the renderer can still show a neutral line.
func Parse(r io.Reader) Stdin {
	data, _ := io.ReadAll(r)
	var raw rawStdin
	_ = json.Unmarshal(data, &raw)

	s := Stdin{
		SessionID:      raw.SessionID,
		TranscriptPath: raw.TranscriptPath,
		Cwd:            raw.Cwd,
		ModelName:      raw.Model.DisplayName,
		CurrentDir:     raw.Workspace.CurrentDir,
		ProjectDir:     raw.Workspace.ProjectDir,
		Version:        raw.Version,
	}
	if raw.ContextWindow != nil {
		s.CtxPct = raw.ContextWindow.UsedPercentage
	}
	if raw.Cost != nil {
		s.LinesAdded = raw.Cost.TotalLinesAdded
		s.LinesRemoved = raw.Cost.TotalLinesRemoved
	}
	if raw.RateLimits != nil {
		s.FiveHour = convertRate(raw.RateLimits.FiveHour)
		s.SevenDay = convertRate(raw.RateLimits.SevenDay)
	}
	return s
}

func convertRate(r *rawRate) *RateLimit {
	if r == nil {
		return nil
	}
	out := &RateLimit{UsedPercentage: r.UsedPercentage}
	if t, ok := parseResetsAt(r.ResetsAt); ok {
		out.ResetsAt, out.HasReset = t, true
	}
	return out
}

// parseResetsAt accepts a unix timestamp (seconds or milliseconds) or an RFC3339
// string. The exact shape varies by plan ([VERIFY] in the PRD), so both are
// handled; anything unrecognized is treated as absent.
func parseResetsAt(raw json.RawMessage) (time.Time, bool) {
	c := bytes.TrimSpace(raw)
	if len(c) == 0 || string(c) == "null" || string(c) == "0" {
		return time.Time{}, false
	}
	if c[0] == '"' {
		var str string
		if json.Unmarshal(c, &str) == nil {
			if t, err := time.Parse(time.RFC3339, str); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	var n float64
	if json.Unmarshal(c, &n) != nil || n <= 0 {
		return time.Time{}, false
	}
	if n > 1e12 { // milliseconds
		return time.UnixMilli(int64(n)), true
	}
	return time.Unix(int64(n), 0), true
}
