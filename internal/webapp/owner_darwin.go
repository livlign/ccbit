//go:build darwin

package webapp

// macOS socket-to-process lookup. There is no /proc and no public socket-table
// API worth binding, so this shells out to lsof, which every macOS install
// ships. Calls are bounded by a hard timeout and the result is cached by the
// caller (see ownTTL), so the cost lands at most once a minute per port.

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// lookupTimeout bounds each helper. lsof can stall on a wedged mount; a status
// line must return regardless.
const lookupTimeout = 700 * time.Millisecond

// ownerText returns the command line and working directory of whatever is
// listening on port, and whether the lookup itself worked.
func ownerText(port int) (string, bool) {
	pid, ok := listenerPID(port)
	if !ok {
		return "", false
	}
	cmd := run("ps", "-ww", "-o", "command=", "-p", pid)
	cwd := lsofCwd(pid)
	if strings.TrimSpace(cmd+cwd) == "" {
		return "", false
	}
	return cmd + "\x00" + cwd, true
}

func listenerPID(port int) (string, bool) {
	out := run("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t")
	for _, line := range strings.Split(out, "\n") {
		if pid := strings.TrimSpace(line); pid != "" {
			return pid, true
		}
	}
	return "", false
}

// lsofCwd reads the process's working directory in field mode, where the answer
// is the line beginning with "n".
func lsofCwd(pid string) string {
	out := run("lsof", "-a", "-p", pid, "-d", "cwd", "-Fn")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimSpace(line[1:])
		}
	}
	return ""
}

func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
