// Package webapp answers the one question a backgrounded `npm run dev` never
// does: is the thing actually up?
//
// Two facts have to be combined, because neither is enough alone. The
// transcript says which localhost ports this session has anything to do with,
// but it is only ever evidence of intent: a port printed an hour ago may be
// dead, and a port quoted in passing was never a server. A loopback dial says
// what is listening right now, but not whether it is yours. So ports are
// harvested loosely from the transcript, then a port is only ever REPORTED
// once ccbit has personally dialed it and found it answering. A port ccbit
// never saw up is never shown, however often the transcript names it.
//
// Both halves are remembered per session (see store), which is what lets a
// server survive scrolling out of the transcript tail, and what makes "(down)"
// mean "this died" rather than "this was mentioned once".
package webapp

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is one detected local port and its live status.
type Server struct {
	Port    int
	Running bool
}

// Info is the ambient view of this session's local web apps, ports ascending.
type Info struct{ Servers []Server }

// Running reports whether any detected port is currently listening.
func (i Info) Running() bool {
	for _, s := range i.Servers {
		if s.Running {
			return true
		}
	}
	return false
}

const (
	// probeCap bounds how many ports are dialed per render. Ports ccbit has seen
	// listening are dialed first, so a busy session's noise can never crowd out
	// the servers that actually run.
	probeCap = 8
	// dialTimeout bounds one probe. Loopback accepts or refuses immediately, so
	// this only matters for a filtered port; it must stay well under the ~1s
	// status-line repaint.
	dialTimeout = 150 * time.Millisecond
	// probeInterval throttles dialing. The line repaints ~1×/s, but every
	// completed connection leaves a TIME_WAIT socket behind for a minute or more,
	// so dialing at render rate would pile up hundreds of them against a healthy
	// server. Between probes the last result is reused, which costs a few seconds
	// of staleness on a transition and nothing else.
	probeInterval = 5 * time.Second
	// ownTTL is how long an ownership verdict is trusted before ccbit asks the
	// OS again. Cheap on Windows and Linux (direct syscalls and /proc), a pair
	// of lsof calls on macOS, so it is worth not asking every probe. A port that
	// goes down and comes back is re-checked immediately regardless: it may be a
	// different program on the same number.
	ownTTL = time.Minute
	// downGrace is how long a port that ccbit SAW listening keeps its place on
	// the line after it stops answering. This is the crash signal: long enough to
	// notice the server died, short enough that a finished session's ports fade.
	downGrace = 5 * time.Minute
	// forgetTTL prunes a remembered port that has neither been mentioned nor been
	// seen up for this long, so the store cannot grow without bound.
	forgetTTL = 12 * time.Hour
	// maxPortsRemembered caps the store regardless of TTL.
	maxPortsRemembered = 32
	// maxDisplayed is how many ports read comfortably in one segment; past it the
	// segment collapses to a count.
	maxDisplayed = 3
	// maxFirstScan bounds the one-time cost of adopting an already-long
	// transcript. Later renders only read the bytes appended since the last one.
	maxFirstScan = 16 << 20
)

// Port is what the store remembers about one port: when the transcript last
// named it, when it was last dialed, when that dial last succeeded, and who the
// OS says is listening. A port is listening NOW exactly when its last dial is
// the one that succeeded, which is what lets the result be reused between
// throttled probes.
type Port struct {
	Mentioned int64     `json:"m,omitempty"` // unix, when ccbit last read it in the transcript
	Probed    int64     `json:"p,omitempty"` // unix, when ccbit last dialed it
	Up        int64     `json:"u,omitempty"` // unix, when a dial last found it listening
	Own       Ownership `json:"o,omitempty"` // whose process is listening (see owned)
	OwnAt     int64     `json:"oa,omitempty"`
}

// live reports the result of the most recent dial.
func (p Port) live() bool { return p.Up > 0 && p.Up == p.Probed }

// shows reports whether this port belongs on the status line at all. A port
// serving another project is somebody else's business, however often this
// session's transcript happens to name it.
func (p Port) shows() bool { return p.Own != OwnForeign }

// store is one session's disposable port memory. Offset is how far into the
// transcript the scanner has read, so each render only pays for the bytes
// appended since the last one.
type store struct {
	Offset int64            `json:"offset"`
	Ports  map[string]*Port `json:"ports"`
}

// hostPortRe matches a loopback host:port anywhere in the transcript text: the
// shape a dev server prints ("Local: http://localhost:5173/"), the shape a curl
// against it takes, and the shape you paste back into the chat.
var hostPortRe = regexp.MustCompile(`(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):(\d{2,5})`)

// portFlagRe catches the server that announces its port only on its own command
// line (`--port 5500`, `PORT=3000 npm start`). Bare `-p` is deliberately absent:
// raw transcript text carries no command context, and every dial is charged
// against probeCap.
var portFlagRe = regexp.MustCompile(`(?:--port[ =]|\bPORT=)(\d{2,5})`)

// Detect updates this session's port memory from the transcript, dials what it
// knows about, and returns what is worth showing. sessionID may be empty (no
// persistence, one bounded scan); transcriptPath may be missing, in which case
// remembered ports are still probed.
func Detect(sessionID, transcriptPath, root string, now time.Time) Info {
	st := load(sessionID)
	scanTranscript(&st, transcriptPath, now)

	due := pick(st.Ports, now)
	wasLive := map[int]bool{}
	for _, port := range due {
		wasLive[port] = st.Ports[strconv.Itoa(port)].live()
	}
	live := probe(due)
	for _, port := range due {
		st.Ports[strconv.Itoa(port)].Probed = now.Unix()
	}
	for _, port := range live {
		p := st.Ports[strconv.Itoa(port)]
		p.Up = now.Unix()
		// Ask who owns it when the verdict is missing, stale, or possibly about a
		// different process: a port that was down and is up again may well be a
		// different program.
		if p.Own == OwnUnknown || !wasLive[port] || now.Sub(time.Unix(p.OwnAt, 0)) >= ownTTL {
			p.Own = owned(port, root)
			p.OwnAt = now.Unix()
		}
	}
	prune(&st, now)
	save(sessionID, st)

	return classify(st.Ports, now)
}

// scanTranscript folds every localhost port mentioned in the bytes appended
// since the last render into the store. It reads raw JSONL text rather than
// parsed entries: a substring match is far cheaper than a full parse, and
// scanning the WHOLE file (incrementally) is what keeps a server visible after
// its startup banner scrolls past the parser's tail window.
//
// Mentions are stamped with the read time, not the entry's own timestamp. The
// stamp only feeds pruning and probe ordering; nothing user-visible depends on
// it, and for a live session the two are a second apart anyway.
func scanTranscript(st *store, path string, now time.Time) {
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	start := st.Offset
	if start > fi.Size() {
		start = 0 // truncated or replaced: start over
	}
	// The stored offset always lands on a line boundary (only complete lines
	// advance it). Clamping a huge first scan does not, so that case, and only
	// that case, drops the partial line it lands in.
	aligned := true
	if fi.Size()-start > maxFirstScan {
		start = fi.Size() - maxFirstScan
		aligned = false
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	if !aligned {
		skipped, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		start += int64(len(skipped))
	}
	read := start
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break // trailing partial line: leave it for the next render
		}
		read += int64(len(line))
		note(st, string(line), now)
	}
	st.Offset = read
}

func note(st *store, text string, now time.Time) {
	record := func(raw string) {
		n, err := strconv.Atoi(raw)
		// Sub-1024 on loopback is almost never a dev server, and a two-digit match
		// is far likelier to be a timestamp than a port.
		if err != nil || n < 1024 || n > 65535 {
			return
		}
		key := strconv.Itoa(n)
		if st.Ports[key] == nil {
			st.Ports[key] = &Port{}
		}
		st.Ports[key].Mentioned = now.Unix()
	}
	for _, m := range hostPortRe.FindAllStringSubmatch(text, -1) {
		record(m[1])
	}
	for _, m := range portFlagRe.FindAllStringSubmatch(text, -1) {
		record(m[1])
	}
}

// rank orders remembered ports by how much they matter: last seen listening
// first, then last mentioned. Used both to choose what to dial and to choose
// what to forget when the store overflows.
func rank(ports map[string]*Port) []int {
	type entry struct {
		port int
		p    Port
	}
	all := make([]entry, 0, len(ports))
	for k, p := range ports {
		if n, err := strconv.Atoi(k); err == nil && p != nil {
			all = append(all, entry{n, *p})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].p.Up != all[j].p.Up {
			return all[i].p.Up > all[j].p.Up
		}
		if all[i].p.Mentioned != all[j].p.Mentioned {
			return all[i].p.Mentioned > all[j].p.Mentioned
		}
		return all[i].port < all[j].port
	})
	out := make([]int, len(all))
	for i, e := range all {
		out[i] = e.port
	}
	return out
}

// pick chooses which remembered ports to dial this render: those whose last
// probe has aged out, best-ranked first and capped at probeCap, so a noisy
// session can never crowd out the servers that actually run.
func pick(ports map[string]*Port, now time.Time) []int {
	var due []int
	for _, n := range rank(ports) {
		if p := ports[strconv.Itoa(n)]; now.Sub(time.Unix(p.Probed, 0)) >= probeInterval {
			due = append(due, n)
		}
	}
	if len(due) > probeCap {
		due = due[:probeCap]
	}
	return due
}

// probe dials the given ports concurrently, so the worst case is one
// dialTimeout for the whole render rather than one per port. It returns the
// subset that answered.
func probe(ports []int) []int {
	if len(ports) == 0 {
		return nil
	}
	answered := make([]bool, len(ports))
	var wg sync.WaitGroup
	for i, port := range ports {
		wg.Add(1)
		go func(i, port int) {
			defer wg.Done()
			answered[i] = listening(port)
		}(i, port)
	}
	wg.Wait()
	var live []int
	for i, ok := range answered {
		if ok {
			live = append(live, ports[i])
		}
	}
	return live
}

// loopbacks are tried in order. A dev server told to bind "localhost" on
// Windows often lands on the IPv6 loopback ALONE (Node resolves localhost to
// ::1 first), so checking only 127.0.0.1 would report a perfectly healthy
// server as down. The v4 dial comes first because it is the common case and
// costs one refused connection when it misses.
var loopbacks = []string{"127.0.0.1", "[::1]"}

func listening(port int) bool {
	for _, host := range loopbacks {
		conn, err := net.DialTimeout("tcp", host+":"+strconv.Itoa(port), dialTimeout)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return true
	}
	return false
}

// classify decides what the line says. A listening port owned by this project
// is always reported. A port that is silent now is reported only if ccbit
// itself saw it listening within downGrace, which is the difference between
// "your server just died" and "some number appeared in the transcript once".
// Ports the OS attributes to another project never appear at all.
func classify(ports map[string]*Port, now time.Time) Info {
	var info Info
	for k, p := range ports {
		n, err := strconv.Atoi(k)
		if err != nil || p == nil || !p.shows() {
			continue
		}
		switch {
		case p.live():
			info.Servers = append(info.Servers, Server{Port: n, Running: true})
		case p.Up > 0 && now.Sub(time.Unix(p.Up, 0)) <= downGrace:
			info.Servers = append(info.Servers, Server{Port: n})
		}
	}
	// Ascending port order: the segment must not reshuffle itself between
	// renders as mention times change.
	sort.Slice(info.Servers, func(i, j int) bool { return info.Servers[i].Port < info.Servers[j].Port })
	return info
}

func prune(st *store, now time.Time) {
	for k, p := range st.Ports {
		if p == nil {
			delete(st.Ports, k)
			continue
		}
		last := p.Up
		if p.Mentioned > last {
			last = p.Mentioned
		}
		if last == 0 || now.Sub(time.Unix(last, 0)) > forgetTTL {
			delete(st.Ports, k)
		}
	}
	if len(st.Ports) <= maxPortsRemembered {
		return
	}
	kept := map[string]*Port{}
	for _, n := range rank(st.Ports)[:maxPortsRemembered] {
		k := strconv.Itoa(n)
		kept[k] = st.Ports[k]
	}
	st.Ports = kept
}

// Format renders the servers for the ambient line. One port reads as a sentence
// ("localhost:5500 (running)"); several collapse behind a shared host, grouped
// by status so the eye lands on what is up ("localhost:3002,5500 (running)",
// "localhost:5500 (running), 3002 (down)"). A narrow terminal, or more ports
// than read comfortably, falls back to a count ("localhost 2/3 running").
func Format(i Info, narrow bool) string {
	if len(i.Servers) == 0 {
		return ""
	}
	var up, down []int
	for _, s := range i.Servers {
		if s.Running {
			up = append(up, s.Port)
		} else {
			down = append(down, s.Port)
		}
	}
	if narrow || len(i.Servers) > maxDisplayed {
		return "localhost " + strconv.Itoa(len(up)) + "/" + strconv.Itoa(len(i.Servers)) + " running"
	}
	switch {
	case len(down) == 0:
		return "localhost:" + joinPorts(up) + " (running)"
	case len(up) == 0:
		return "localhost:" + joinPorts(down) + " (down)"
	default:
		return "localhost:" + joinPorts(up) + " (running), " + joinPorts(down) + " (down)"
	}
}

func joinPorts(ports []int) string {
	s := make([]string, len(ports))
	for i, p := range ports {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ",")
}

// --- store I/O (disposable; every failure degrades to "no memory this render") ---

func dir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "ccbit", "ports")
	}
	return filepath.Join(os.TempDir(), "ccbit", "ports")
}

func storePath(sessionID string) string { return filepath.Join(dir(), sessionID+".json") }

func load(sessionID string) store {
	st := store{Ports: map[string]*Port{}}
	if sessionID == "" {
		return st
	}
	b, err := os.ReadFile(storePath(sessionID))
	if err != nil || json.Unmarshal(b, &st) != nil || st.Ports == nil {
		return store{Ports: map[string]*Port{}}
	}
	return st
}

func save(sessionID string, st store) {
	if sessionID == "" {
		return
	}
	d := dir()
	if os.MkdirAll(d, 0o755) != nil {
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := storePath(sessionID) + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, storePath(sessionID))
}
