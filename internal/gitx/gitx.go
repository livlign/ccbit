// Package gitx reads lightweight git facts for the ambient line: branch, dirty
// file count, and ahead/behind — Bit involving itself with where the work is
// going, not just what changed.
//
// Cost discipline matters more than freshness here: the branch comes from a
// plain file read of .git/HEAD (no subprocess, every render), while the
// dirty/ahead/behind snapshot shells out to git at most once per cache TTL.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Info is the rendered subset of git state.
type Info struct {
	Branch string // branch name, or short detached hash, "" if unknown
	Dirty  int    // modified/untracked paths in the worktree
	Ahead  int    // commits ahead of upstream
	Behind int    // commits behind upstream
}

// statusTTL bounds how often the git status subprocess may run per repo.
const statusTTL = 45 * time.Second

// Branch resolves the current branch by reading .git/HEAD directly — a file
// read, safe to do on every 1s render. Worktrees (a .git *file* containing
// "gitdir: <path>") are followed one level. Detached HEAD shows a short hash.
func Branch(root string) string {
	if root == "" {
		return ""
	}
	gitDir := filepath.Join(root, ".git")
	if fi, err := os.Stat(gitDir); err != nil {
		return ""
	} else if !fi.IsDir() {
		// Worktree: .git is a file pointing at the real git dir.
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(b))
		const p = "gitdir:"
		if !strings.HasPrefix(line, p) {
			return ""
		}
		gitDir = strings.TrimSpace(line[len(p):])
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(head))
	if name, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
		return name
	}
	if len(ref) >= 7 { // detached
		return ref[:7]
	}
	return ""
}

// Status returns dirty/ahead/behind, refreshed via `git status` at most once per
// statusTTL (cached on disk per repo, like the repo-root cache: disposable,
// never authoritative). Returns zeros when git is slow, absent, or upstream-less.
func Status(root string, now time.Time) (dirty, ahead, behind int) {
	if root == "" {
		return 0, 0, 0
	}
	cache := cachePath(root)
	if fi, err := os.Stat(cache); err == nil && now.Sub(fi.ModTime()) < statusTTL {
		if b, err := os.ReadFile(cache); err == nil {
			return parseCache(b)
		}
	}
	dirty, ahead, behind = liveStatus(root)
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
		_ = os.WriteFile(cache, fmt.Appendf(nil, "%d %d %d", dirty, ahead, behind), 0o644)
	}
	return dirty, ahead, behind
}

func liveStatus(root string) (dirty, ahead, behind int) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain=v1", "-b").Output()
	if err != nil {
		return 0, 0, 0
	}
	return parsePorcelain(out)
}

var aheadBehindRe = regexp.MustCompile(`\[(?:ahead (\d+))?(?:, )?(?:behind (\d+))?\]`)

// parsePorcelain reads `git status --porcelain=v1 -b` output: the "## branch"
// header carries [ahead N, behind M]; every following line is a dirty path.
func parsePorcelain(out []byte) (dirty, ahead, behind int) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("## ")) {
			if m := aheadBehindRe.FindSubmatch(line); m != nil {
				ahead, _ = strconv.Atoi(string(m[1]))
				behind, _ = strconv.Atoi(string(m[2]))
			}
			continue
		}
		dirty++
	}
	return dirty, ahead, behind
}

func parseCache(b []byte) (dirty, ahead, behind int) {
	_, _ = fmt.Sscanf(string(b), "%d %d %d", &dirty, &ahead, &behind)
	return dirty, ahead, behind
}

func cachePath(root string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(root))
	return filepath.Join(os.TempDir(), "ccbit", fmt.Sprintf("git-%08x", h.Sum32()))
}
