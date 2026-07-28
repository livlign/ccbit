package webapp

import (
	"path/filepath"
	"strings"
)

// Ownership is what ccbit could work out about who is listening on a port.
type Ownership int

const (
	// OwnUnknown means the question could not be answered on this machine (no
	// project root, an OS that exposes no socket table, a process owned by
	// another user). The caller keeps whatever behaviour it had; a port is never
	// hidden on the strength of a failed lookup.
	OwnUnknown Ownership = iota
	// OwnMine means the listening process is working inside this project.
	OwnMine
	// OwnForeign means the listening process was identified and belongs to
	// something else: another repo, another session's app, an unrelated service.
	OwnForeign
)

// owned asks the OS which process is listening on port and decides whether it
// belongs to root. This is the only honest way to answer the question: a port
// number in the transcript says nothing about who is serving it, and the two
// are routinely unrelated (a URL pasted into the chat, a config value, a status
// line quoted back, a neighbouring project's app).
//
// The process is matched on its command line and working directory, not its
// parentage. Dev servers are usually orphans by the time anyone asks: the shell
// that launched them has exited, so walking parents finds nothing, while
// `node .../<project>/node_modules/vite/bin/vite.js` still names its project.
func owned(port int, root string) Ownership {
	if root == "" {
		return OwnUnknown
	}
	text, ok := ownerText(port)
	if !ok {
		return OwnUnknown
	}
	if belongs(text, root) {
		return OwnMine
	}
	return OwnForeign
}

// belongs reports whether a process description (command line, working
// directory, executable path) places the process inside root. Comparison is
// case-insensitive with separators normalised, so a Windows command line full
// of backslashes matches a git toplevel full of forward slashes.
func belongs(text, root string) bool {
	root = normalizePath(root)
	if root == "" || text == "" {
		return false
	}
	return strings.Contains(normalizePath(text), root)
}

func normalizePath(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, `\`, "/"))
	// Collapse the doubled separators that shell-quoted command lines are full
	// of ("C:\\Program Files\\nodejs\\\\node.exe") so a path still matches.
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	return strings.TrimSuffix(s, "/")
}

// ProjectRoot is the path a server has to be working inside to count as this
// session's. The git toplevel is preferred (a monorepo's web/ and server/ both
// sit under it); the working directory is the fallback.
func ProjectRoot(repoRoot, cwd string) string {
	for _, d := range []string{repoRoot, cwd} {
		if d != "" {
			return filepath.Clean(d)
		}
	}
	return ""
}
