// Package memory is ccbit's durable, cross-session learning: small per-project
// aggregates that let Bit get smarter the more you use it, without any model and
// without storing prompt content. It records only numbers — how long turns
// typically run, how long activity normally pauses mid-turn — and derives
// per-project thresholds and "is this unusual?" judgements from them.
//
// It is updated at turn boundaries (cheap) and read once per render. Like the
// heartbeat, it is disposable: delete the dir and ccbit falls back to its fixed
// defaults.
package memory

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"
)

const (
	// alpha weights the newest sample in the exponential moving averages — high
	// enough to adapt to a changing project, low enough to smooth outliers.
	alpha = 0.25
	// minSamples gates every learned value: below it, Bit says nothing and falls
	// back to fixed defaults. A wrong insight costs more trust than a missing one.
	minSamples = 5
	// stallFloor/stallCeil bound the learned Stopped threshold to a sane range.
	stallFloor = 45 * time.Second
	stallCeil  = 300 * time.Second
	// gapSampleCeil discards implausibly long in-turn gaps (you walked away)
	// before they poison the typical-gap average.
	gapSampleCeil = 10 * time.Minute
)

// Stats is one project's accumulated aggregates. Numbers only — never any text.
type Stats struct {
	Samples int     `json:"samples"`    // completed turns recorded
	GapEWMA float64 `json:"gap_ewma_s"` // typical worst in-turn activity gap, seconds
	DurEWMA float64 `json:"dur_ewma_s"` // typical turn duration, seconds
}

func dir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "ccbit", "memory")
	}
	return filepath.Join(os.TempDir(), "ccbit", "memory")
}

func path(key string) string { return filepath.Join(dir(), key+".json") }

// Key is a stable, readable filename for a project, derived from its root path
// (so two projects sharing a basename don't collide) with the label for legibility.
func Key(root, label string) string {
	if root == "" && label == "" {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(root))
	name := label
	if name == "" {
		name = "project"
	}
	return fmt.Sprintf("%s-%08x", sanitize(name), h.Sum32())
}

func sanitize(s string) string {
	b := []byte(s)
	for i := range b {
		c := b[i]
		ok := c == '.' || c == '_' || c == '-' ||
			(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !ok {
			b[i] = '-'
		}
	}
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}

// Load returns a project's stats, or a zero Stats (nothing learned yet) on any
// error. Never fails hard.
func Load(key string) Stats {
	var s Stats
	if key == "" {
		return s
	}
	if b, err := os.ReadFile(path(key)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Record folds one completed turn into the moving averages.
func (s *Stats) Record(dur, maxGap time.Duration) {
	if maxGap > gapSampleCeil {
		maxGap = gapSampleCeil
	}
	if maxGap < 0 {
		maxGap = 0
	}
	if dur < 0 {
		dur = 0
	}
	g, d := maxGap.Seconds(), dur.Seconds()
	if s.Samples == 0 {
		s.GapEWMA, s.DurEWMA = g, d
	} else {
		s.GapEWMA = alpha*g + (1-alpha)*s.GapEWMA
		s.DurEWMA = alpha*d + (1-alpha)*s.DurEWMA
	}
	s.Samples++
}

// Save writes the stats atomically (temp + rename) so a concurrent reader in
// another session never sees a half-written file. Best-effort.
func Save(key string, s Stats) {
	if key == "" {
		return
	}
	d := dir()
	if os.MkdirAll(d, 0o755) != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	tmp := path(key) + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path(key))
}

// LearnedStall is a per-project Stopped threshold: roughly twice the typical
// worst in-turn gap (so a normal think/build pause never trips it), clamped to a
// sane range. Falls back until enough turns have been seen.
func (s Stats) LearnedStall(fallback time.Duration) time.Duration {
	if s.Samples < minSamples || s.GapEWMA <= 0 {
		return fallback
	}
	d := time.Duration(s.GapEWMA*2) * time.Second
	if d < stallFloor {
		d = stallFloor
	}
	if d > stallCeil {
		d = stallCeil
	}
	return d
}

// TypicalTurn is the learned mean turn duration for the project, or 0 until
// enough turns have been seen. Used to flag a turn running unusually long.
func (s Stats) TypicalTurn() time.Duration {
	if s.Samples < minSamples || s.DurEWMA <= 0 {
		return 0
	}
	return time.Duration(s.DurEWMA) * time.Second
}
