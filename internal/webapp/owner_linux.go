//go:build linux

package webapp

// Linux socket-to-process lookup, straight from /proc: no subprocess, no lsof
// dependency. The kernel exposes listening sockets with their inode, and each
// process exposes its open sockets as fd symlinks, so the join is a couple of
// directory reads.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ownerText returns the command line, working directory and executable of
// whatever is listening on port, and whether the lookup itself worked.
func ownerText(port int) (string, bool) {
	inodes := listenInodes(port)
	if len(inodes) == 0 {
		return "", false
	}
	pid, ok := pidForInodes(inodes)
	if !ok {
		// The socket exists but belongs to a process we cannot read (another
		// user, a container). Report "cannot tell" rather than "not yours".
		return "", false
	}
	base := filepath.Join("/proc", pid)
	cmd, _ := os.ReadFile(filepath.Join(base, "cmdline"))
	cwd, _ := os.Readlink(filepath.Join(base, "cwd"))
	exe, _ := os.Readlink(filepath.Join(base, "exe"))
	// cmdline is NUL-separated; the separator only has to not join two paths
	// into one accidental match.
	text := strings.ReplaceAll(string(cmd), "\x00", " ") + "\x00" + cwd + "\x00" + exe
	if strings.TrimSpace(strings.ReplaceAll(text, "\x00", "")) == "" {
		return "", false
	}
	return text, true
}

// listenInodes collects the socket inodes listening on port, over IPv4 and IPv6.
func listenInodes(port int) map[string]bool {
	inodes := map[string]bool{}
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" { // 0A = TCP_LISTEN
				continue
			}
			local := f[1]
			i := strings.LastIndex(local, ":")
			if i < 0 {
				continue
			}
			p, err := strconv.ParseUint(local[i+1:], 16, 32)
			if err != nil || int(p) != port {
				continue
			}
			inodes[f[9]] = true
		}
	}
	return inodes
}

// pidForInodes finds the process holding one of these socket inodes. Processes
// whose fds cannot be read (other users) are skipped silently.
func pidForInodes(inodes map[string]bool) (string, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", pid, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join("/proc", pid, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				return pid, true
			}
		}
	}
	return "", false
}
