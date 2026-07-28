package webapp

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBelongsMatchesRegardlessOfSeparatorAndCase(t *testing.T) {
	// A Windows command line is backslashed, doubly-escaped and mixed case; a git
	// toplevel is forward-slashed and lower case. They still have to match.
	cmd := `"node"   "D:\projects\Api2-Zero\node_modules\.bin\\..\vite\bin\vite.js"`
	if !belongs(cmd, "D:/projects/api2-zero") {
		t.Error("a backslashed, doubly-separated command line should match its project root")
	}
	if belongs(cmd, "D:/claude/ccbit") {
		t.Error("another project's root must not match")
	}
}

func TestBelongsMatchesWorkingDirectory(t *testing.T) {
	// A server whose command line names no path (python -m http.server) is still
	// placed by its working directory.
	text := "python -m http.server 8000\x00/home/me/work/site"
	if !belongs(text, "/home/me/work/site") {
		t.Error("the working directory should place a path-free command")
	}
	if belongs(text, "/home/me/work/other") {
		t.Error("a sibling directory must not match")
	}
}

func TestBelongsRejectsEmpty(t *testing.T) {
	if belongs("", "/some/root") || belongs("/some/root/app", "") {
		t.Error("an empty side can never be a match")
	}
}

func TestOwnedWithoutRootIsUnknown(t *testing.T) {
	// No project root means no way to attribute; the caller must not hide on it.
	if got := owned(1234, ""); got != OwnUnknown {
		t.Fatalf("want OwnUnknown without a root, got %v", got)
	}
}

func TestOwnedIdentifiesThisProcess(t *testing.T) {
	// The test binary is the listening process, so its own executable path and
	// working directory are what the lookup should find.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := owned(port, wd); got == OwnForeign {
		t.Fatalf("the current process listening in %s should not read as foreign", wd)
	}
	// A root the listener has nothing to do with must not be claimed. Skip when
	// the platform cannot attribute at all, which is a legitimate answer.
	if _, ok := ownerText(port); !ok {
		t.Skip("no socket-to-process lookup on this platform")
	}
	if got := owned(port, filepath.Join(wd, "definitely-not-a-real-subdir-xyz")); got != OwnForeign {
		t.Fatalf("want OwnForeign for an unrelated root, got %v", got)
	}
}

func TestClassifyHidesForeignPorts(t *testing.T) {
	now := time.Now()
	// The reported bug: a neighbouring project's servers, verified listening and
	// named in this session's transcript, must not appear on this project's line.
	ports := map[string]*Port{
		"3001": {Mentioned: now.Unix(), Probed: now.Unix(), Up: now.Unix(), Own: OwnForeign},
		"5501": {Mentioned: now.Unix(), Probed: now.Unix(), Up: now.Unix(), Own: OwnForeign},
		"5173": {Mentioned: now.Unix(), Probed: now.Unix(), Up: now.Unix(), Own: OwnMine},
	}
	got := classify(ports, now)
	if len(got.Servers) != 1 || got.Servers[0].Port != 5173 {
		t.Fatalf("want only this project's server, got %+v", got.Servers)
	}
}

func TestClassifyShowsUnattributablePorts(t *testing.T) {
	now := time.Now()
	// When the OS cannot say who owns a socket, the port keeps its place: a
	// failed lookup is not evidence against the server.
	ports := map[string]*Port{
		"5173": {Mentioned: now.Unix(), Probed: now.Unix(), Up: now.Unix(), Own: OwnUnknown},
	}
	if got := classify(ports, now); len(got.Servers) != 1 {
		t.Fatalf("an unattributable listening port should still show, got %+v", got.Servers)
	}
}

func TestClassifyHidesForeignPortThatDied(t *testing.T) {
	now := time.Now()
	// A foreign server crashing is still not this project's news.
	ports := map[string]*Port{
		"3001": {Mentioned: now.Unix(), Probed: now.Unix(), Up: now.Add(-time.Minute).Unix(), Own: OwnForeign},
	}
	if got := classify(ports, now); len(got.Servers) != 0 {
		t.Fatalf("want nothing for a foreign server's death, got %+v", got.Servers)
	}
}

func TestDetectHidesForeignServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if _, ok := ownerText(port); !ok {
		t.Skip("no socket-to-process lookup on this platform")
	}

	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(`{"u":"http://localhost:`+strconv.Itoa(port)+`/"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The transcript names it and it is genuinely listening, but the process
	// belongs to another project: the whole point is that this is not enough.
	got := Detect("sess", path, filepath.Join(t.TempDir(), "some-other-project"), time.Now())
	if len(got.Servers) != 0 {
		t.Fatalf("want a foreign server hidden, got %+v", got.Servers)
	}
}
