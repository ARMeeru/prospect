package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// git runs a git command in dir and returns stdout (trimmed).
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

type commit struct {
	sha, subject string
}

// testTouchingCommits lists commits (newest first) that touched *_test.go.
func testTouchingCommits(repo, since string, limit int) ([]commit, error) {
	args := []string{"log", "--no-merges", "--format=%H%x1f%s"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	args = append(args, "--", "*_test.go")
	out, err := git(repo, args...)
	if err != nil {
		return nil, err
	}
	var cs []commit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		cs = append(cs, commit{sha: parts[0], subject: parts[1]})
		if limit > 0 && len(cs) >= limit {
			break
		}
	}
	return cs, nil
}

// changedFiles returns all files changed by a commit.
func changedFiles(repo, sha string) ([]string, error) {
	out, err := git(repo, "show", "--name-only", "--format=", sha)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

func commitBody(repo, sha string) string {
	out, _ := git(repo, "log", "-1", "--format=%b", sha)
	return strings.TrimSpace(out)
}

func parentOf(repo, sha string) (string, error) {
	return git(repo, "rev-parse", sha+"^")
}

// worktreeAt creates a detached worktree at sha. Returns dir and cleanup.
func worktreeAt(repo, sha string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "prospect-wt-")
	if err != nil {
		return "", nil, err
	}
	if _, err := git(repo, "worktree", "add", "--detach", "-q", dir, sha); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	cleanup := func() {
		git(repo, "worktree", "remove", "--force", dir)
		os.RemoveAll(dir)
	}
	return dir, cleanup, nil
}

// checkoutFiles materializes files from fromSha into dir (overlay).
func checkoutFiles(dir, fromSha string, files []string) error {
	args := append([]string{"checkout", "-q", fromSha, "--"}, files...)
	_, err := git(dir, args...)
	return err
}

// addedTestFuncs extracts Test/Fuzz function names added by a commit's test diff.
func addedTestFuncs(repo, sha string, testFiles []string) []string {
	seen := map[string]bool{}
	var names []string
	for _, f := range testFiles {
		out, err := git(repo, "show", sha, "--", f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "+func ") {
				rest := strings.TrimPrefix(line, "+func ")
				for _, prefix := range []string{"Test", "Fuzz", "Benchmark"} {
					if i := strings.Index(rest, prefix); i >= 0 {
						tail := rest[i+len(prefix):]
						j := strings.IndexAny(tail, "( \t")
						name := prefix
						if j >= 0 {
							name = prefix + tail[:j]
						}
						if !seen[name] {
							seen[name] = true
							names = append(names, name)
						}
					}
				}
			}
		}
	}
	return names
}
