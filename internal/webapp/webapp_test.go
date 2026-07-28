package webapp

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newStore() store { return store{Ports: map[string]*Port{}} }

func mentioned(st store, port int) bool { return st.Ports[strconv.Itoa(port)] != nil }

// listener opens a real loopback listener and returns its port plus a closer.
func listener(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}

// deadPort returns a port nothing is listening on.
func deadPort(t *testing.T) int {
	t.Helper()
	p, closer := listener(t)
	closer()
	return p
}

func TestNoteHarvestsServerURLs(t *testing.T) {
	st := newStore()
	now := time.Now()
	note(&st, `  VITE v5.0.0 ready\n  ➜  Local:   http://localhost:5173/`, now)
	note(&st, `API listening on http://127.0.0.1:3001 (D1 mode)`, now)
	note(&st, `npx live-server --port 5500`, now)
	note(&st, `PORT=4000 npm start`, now)

	for _, p := range []int{5173, 3001, 5500, 4000} {
		if !mentioned(st, p) {
			t.Errorf("port %d should have been harvested", p)
		}
	}
}

func TestNoteSkipsPrivilegedAndBogusPorts(t *testing.T) {
	st := newStore()
	note(&st, "http://localhost:80/ http://localhost:443/ localhost:99", time.Now())
	if len(st.Ports) != 0 {
		t.Fatalf("want nothing harvested, got %v", st.Ports)
	}
}

func TestClassifyReportsListeningPort(t *testing.T) {
	now := time.Now()
	// Mentioned long ago and never yet dialed successfully: still reported, because
	// the dial just found it answering.
	ports := map[string]*Port{"5500": {
		Mentioned: now.Add(-6 * time.Hour).Unix(),
		Probed:    now.Unix(),
		Up:        now.Unix(),
	}}
	got := classify(ports, now)
	if len(got.Servers) != 1 || !got.Servers[0].Running || got.Servers[0].Port != 5500 {
		t.Fatalf("want 5500 running, got %+v", got.Servers)
	}
	if !got.Running() {
		t.Fatal("Running() should be true when a port is listening")
	}
}

func TestClassifyHidesPortNeverSeenUp(t *testing.T) {
	now := time.Now()
	// The whole false-positive class: a port the transcript keeps naming (a
	// pasted URL, a config, ccbit's own status line quoted back) that ccbit has
	// never once found listening. It must never reach the line.
	ports := map[string]*Port{
		"3099": {Mentioned: now.Unix(), Probed: now.Unix()},
		"5500": {Mentioned: now.Add(-time.Minute).Unix(), Probed: now.Unix()},
	}
	if got := classify(ports, now); len(got.Servers) != 0 {
		t.Fatalf("want nothing shown for never-seen-up ports, got %+v", got.Servers)
	}
}

func TestClassifyReportsRecentlyDiedServer(t *testing.T) {
	now := time.Now()
	ports := map[string]*Port{"5500": {
		Mentioned: now.Unix(),
		Probed:    now.Unix(),
		Up:        now.Add(-time.Minute).Unix(),
	}}
	got := classify(ports, now)
	if len(got.Servers) != 1 || got.Servers[0].Running {
		t.Fatalf("want 5500 reported as down, got %+v", got.Servers)
	}
	if got.Running() {
		t.Fatal("Running() should be false when nothing is listening")
	}
}

func TestClassifyDropsLongDeadServer(t *testing.T) {
	now := time.Now()
	ports := map[string]*Port{"5500": {
		Mentioned: now.Unix(),
		Probed:    now.Unix(),
		Up:        now.Add(-downGrace - time.Minute).Unix(),
	}}
	if got := classify(ports, now); len(got.Servers) != 0 {
		t.Fatalf("a server down past the grace window is clutter, got %+v", got.Servers)
	}
}

func TestClassifyOrdersByPortForStability(t *testing.T) {
	now := time.Now()
	ports := map[string]*Port{
		"5501": {Mentioned: now.Add(-time.Hour).Unix(), Probed: now.Unix(), Up: now.Unix()},
		"3001": {Mentioned: now.Unix(), Probed: now.Unix(), Up: now.Unix()},
	}
	got := classify(ports, now)
	if len(got.Servers) != 2 || got.Servers[0].Port != 3001 || got.Servers[1].Port != 5501 {
		t.Fatalf("want ascending ports regardless of mention order, got %+v", got.Servers)
	}
}

func TestPickPrefersPortsSeenUp(t *testing.T) {
	now := time.Now().Unix()
	ports := map[string]*Port{}
	// More noise than probeCap, all mentioned more recently than the real server.
	for i := 0; i < probeCap+4; i++ {
		ports[strconv.Itoa(9000+i)] = &Port{Mentioned: now}
	}
	ports["5501"] = &Port{Mentioned: now - 7200, Up: now - 3600}

	got := pick(ports, time.Now())
	if len(got) != probeCap {
		t.Fatalf("want %d probes, got %d", probeCap, len(got))
	}
	if got[0] != 5501 {
		t.Fatalf("a known-up server must be probed first, got %v", got)
	}
}

func TestPruneForgetsStaleAndCaps(t *testing.T) {
	now := time.Now()
	st := newStore()
	st.Ports["5500"] = &Port{Mentioned: now.Add(-forgetTTL - time.Hour).Unix()}
	st.Ports["3000"] = &Port{Mentioned: now.Unix()}
	prune(&st, now)
	if mentioned(st, 5500) {
		t.Error("a port untouched past forgetTTL should be forgotten")
	}
	if !mentioned(st, 3000) {
		t.Error("a freshly mentioned port should be kept")
	}

	for i := 0; i < maxPortsRemembered*2; i++ {
		st.Ports[strconv.Itoa(20000+i)] = &Port{Mentioned: now.Unix()}
	}
	prune(&st, now)
	if len(st.Ports) > maxPortsRemembered {
		t.Fatalf("store should be capped at %d, got %d", maxPortsRemembered, len(st.Ports))
	}
}

func TestScanTranscriptIsIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	write := func(s string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	now := time.Now()
	st := newStore()
	write(`{"text":"Local: http://localhost:5501/"}` + "\n")
	scanTranscript(&st, path, now)
	if !mentioned(st, 5501) {
		t.Fatal("first pass should harvest 5501")
	}
	first := st.Offset

	write(`{"text":"API: http://127.0.0.1:3001/"}` + "\n")
	scanTranscript(&st, path, now)
	if !mentioned(st, 3001) {
		t.Fatal("second pass should harvest the appended port")
	}
	if st.Offset <= first {
		t.Fatalf("offset should advance past the appended bytes, %d -> %d", first, st.Offset)
	}
	// The port from the first pass is remembered, not re-derived: this is what
	// keeps a server visible after its banner scrolls out of any tail window.
	if !mentioned(st, 5501) {
		t.Fatal("5501 should still be remembered after the incremental pass")
	}
}

func TestScanTranscriptLeavesPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(`{"text":"http://localhost:5501/"`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st := newStore()
	scanTranscript(&st, path, now)
	if st.Offset != 0 {
		t.Fatalf("a half-written line must not advance the offset, got %d", st.Offset)
	}
	// Once the line is completed, the port is picked up.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("}\n")
	f.Close()
	scanTranscript(&st, path, now)
	if !mentioned(st, 5501) {
		t.Fatal("the completed line should be scanned on the next pass")
	}
}

func TestScanTranscriptHandlesTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	now := time.Now()
	st := store{Ports: map[string]*Port{}, Offset: 1 << 20} // offset past a rewritten file
	if err := os.WriteFile(path, []byte(`{"text":"http://localhost:5501/"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanTranscript(&st, path, now)
	if !mentioned(st, 5501) {
		t.Fatal("a shrunken transcript should be rescanned from the start")
	}
}

func TestDetectEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	port, closer := listener(t)
	defer closer()
	dead := deadPort(t)

	path := filepath.Join(t.TempDir(), "t.jsonl")
	line := `{"text":"Local: http://localhost:` + strconv.Itoa(port) +
		`/ API: http://127.0.0.1:` + strconv.Itoa(dead) + `/"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	got := Detect("sess-1", path, "", now)
	if len(got.Servers) != 1 || got.Servers[0].Port != port || !got.Servers[0].Running {
		t.Fatalf("want only the listening port reported, got %+v", got.Servers)
	}

	// The memory persists: a second render with the transcript gone still knows
	// the port, and still reports it because it is still listening.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	again := Detect("sess-1", path, "", now)
	if len(again.Servers) != 1 || !again.Servers[0].Running {
		t.Fatalf("remembered port should survive the transcript, got %+v", again.Servers)
	}

	// When it stops answering, it reports as down within the grace window.
	closer()
	down := Detect("sess-1", path, "", now.Add(time.Minute))
	if len(down.Servers) != 1 || down.Servers[0].Running {
		t.Fatalf("want the stopped server reported as down, got %+v", down.Servers)
	}

	// And a session that never saw it up says nothing about it.
	other := Detect("sess-2", path, "", now.Add(time.Minute))
	if len(other.Servers) != 0 {
		t.Fatalf("a different session should not inherit ports, got %+v", other.Servers)
	}
}

func TestDetectWithoutSessionIDDoesNotPersist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	port, closer := listener(t)
	defer closer()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(`{"u":"http://localhost:`+strconv.Itoa(port)+`/"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Detect("", path, "", time.Now()); len(got.Servers) != 1 {
		t.Fatalf("detection should still work without a session id, got %+v", got.Servers)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "ccbit", "ports")); !os.IsNotExist(err) {
		t.Fatal("no session id means nothing should be written to disk")
	}
}

func TestFormat(t *testing.T) {
	up := func(p int) Server { return Server{Port: p, Running: true} }
	down := func(p int) Server { return Server{Port: p} }

	cases := []struct {
		name    string
		servers []Server
		narrow  bool
		want    string
	}{
		{"none", nil, false, ""},
		{"single running", []Server{up(5500)}, false, "localhost:5500 (running)"},
		{"single down", []Server{down(5500)}, false, "localhost:5500 (down)"},
		{"both running", []Server{up(3002), up(5500)}, false, "localhost:3002,5500 (running)"},
		{"both down", []Server{down(3002), down(5500)}, false, "localhost:3002,5500 (down)"},
		{"mixed", []Server{down(3002), up(5500)}, false, "localhost:5500 (running), 3002 (down)"},
		{"narrow collapses", []Server{up(5500), down(3002)}, true, "localhost 1/2 running"},
		{"many collapse", []Server{up(5500), up(3002), down(4000), down(4001)}, false, "localhost 2/4 running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(Info{Servers: tc.servers}, tc.narrow); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFormatNeverWrapsAcrossSeparator(t *testing.T) {
	// The ambient line joins segments with " · "; the segment must not contain one.
	got := Format(Info{Servers: []Server{{Port: 5500, Running: true}, {Port: 3002}}}, false)
	if strings.Contains(got, " · ") {
		t.Fatalf("segment must not embed the ambient separator, got %q", got)
	}
}

func TestProbeIsThrottledBetweenRenders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	port, closer := listener(t)
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(`{"u":"http://localhost:`+strconv.Itoa(port)+`/"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if got := Detect("sess", path, "", now); !got.Running() {
		t.Fatalf("first render should dial and find it up, got %+v", got.Servers)
	}
	// The server dies, but the next render lands inside the probe interval: the
	// status line reuses the last result rather than dialing every second.
	closer()
	if got := Detect("sess", path, "", now.Add(probeInterval/2)); !got.Running() {
		t.Fatalf("a render inside the probe interval should reuse the last result, got %+v", got.Servers)
	}
	// Once the interval has passed, the truth catches up.
	got := Detect("sess", path, "", now.Add(probeInterval+time.Second))
	if got.Running() {
		t.Fatalf("a render past the probe interval should re-dial and see it down, got %+v", got.Servers)
	}
	if len(got.Servers) != 1 {
		t.Fatalf("the dead server should still report as down within the grace window, got %+v", got.Servers)
	}
}

func TestListeningFindsIPv6OnlyServer(t *testing.T) {
	// A dev server told to bind "localhost" often ends up on ::1 alone (Node on
	// Windows resolves it that way); dialing only 127.0.0.1 would call it down.
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !listening(port) {
		t.Fatal("an IPv6-only loopback server should be detected as listening")
	}
}
