package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBranchFromHead(t *testing.T) {
	root := t.TempDir()
	gd := filepath.Join(root, ".git")
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Branch(root); got != "feature/x" {
		t.Fatalf("branch = %q, want feature/x", got)
	}

	// Detached HEAD shows a short hash.
	if err := os.WriteFile(filepath.Join(gd, "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Branch(root); got != "0123456" {
		t.Fatalf("detached branch = %q, want 0123456", got)
	}

	if got := Branch(filepath.Join(root, "nope")); got != "" {
		t.Fatalf("missing repo should be empty, got %q", got)
	}
}

func TestBranchWorktreeFile(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "HEAD"), []byte("ref: refs/heads/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+real+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Branch(root); got != "wt" {
		t.Fatalf("worktree branch = %q, want wt", got)
	}
}

func TestParsePorcelain(t *testing.T) {
	out := []byte("## main...origin/main [ahead 2, behind 1]\n M cmd/main.go\n?? new.txt\n")
	dirty, ahead, behind := parsePorcelain(out)
	if dirty != 2 || ahead != 2 || behind != 1 {
		t.Fatalf("got %d/%d/%d, want 2/2/1", dirty, ahead, behind)
	}

	out = []byte("## main...origin/main\n")
	dirty, ahead, behind = parsePorcelain(out)
	if dirty != 0 || ahead != 0 || behind != 0 {
		t.Fatalf("clean synced repo: got %d/%d/%d, want zeros", dirty, ahead, behind)
	}

	out = []byte("## main...origin/main [ahead 3]\n M a\n")
	dirty, ahead, behind = parsePorcelain(out)
	if dirty != 1 || ahead != 3 || behind != 0 {
		t.Fatalf("ahead-only: got %d/%d/%d, want 1/3/0", dirty, ahead, behind)
	}
}
