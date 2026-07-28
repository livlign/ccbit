package render

import (
	"strings"
	"testing"

	"github.com/livlign/ccbit/internal/webapp"
)

func TestLine2ShowsRunningDevServer(t *testing.T) {
	c := ctx()
	c.Web = webapp.Info{Servers: []webapp.Server{{Port: 5500, Running: true}}}
	got := line2(c, false)
	if !strings.Contains(got, "localhost:5500 (running)") {
		t.Fatalf("ambient line should report the running dev server, got %q", got)
	}
	// It belongs with the project facts, before the model/context readouts.
	if strings.Index(got, "localhost:5500") > strings.Index(got, "Opus") {
		t.Fatalf("dev server should precede the model segment, got %q", got)
	}
}

func TestLine2ShowsMultiplePorts(t *testing.T) {
	c := ctx()
	c.Web = webapp.Info{Servers: []webapp.Server{
		{Port: 5500, Running: true},
		{Port: 3002, Running: false},
	}}
	if got := line2(c, false); !strings.Contains(got, "localhost:5500 (running), 3002 (down)") {
		t.Fatalf("ambient line should report each port's status, got %q", got)
	}
}

func TestLine2SilentWithoutDevServer(t *testing.T) {
	if got := line2(ctx(), false); strings.Contains(got, "localhost") {
		t.Fatalf("no detected server means no segment, got %q", got)
	}
}

func TestWebSegmentWarnsWhenDown(t *testing.T) {
	c := ctx()
	c.ColorOn = true
	c.Web = webapp.Info{Servers: []webapp.Server{{Port: 5500, Running: false}}}
	if got := webSegment(c, false); !strings.Contains(got, yellow) {
		t.Fatalf("a stopped server should be colored as a warning, got %q", got)
	}
	c.Web = webapp.Info{Servers: []webapp.Server{{Port: 5500, Running: true}}}
	if got := webSegment(c, false); strings.Contains(got, yellow) {
		t.Fatalf("a healthy server should stay calm, got %q", got)
	}
}
