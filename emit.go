package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// emitTask writes a Harbor-format task directory for a verified candidate.
func emitTask(outRoot, repo, repoName, repoURL string, cand candidate, finalEnv map[string]string) (string, error) {
	dir := filepath.Join(outRoot, cand.id())
	for _, d := range []string{"environment", "tests", "solution"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return "", err
		}
	}

	// instruction.md — what the agent sees. No mention of hidden tests or fix.
	body := commitBody(repo, cand.FixSHA)
	if len(body) > 1200 {
		body = body[:1200] + "…"
	}
	instr := fmt.Sprintf("# %s\n\nRepository: `%s` (checked out at a fixed commit in `/app`)\n\n%s\n\nImplement the change described above, following the repository's existing patterns. Your work will be validated by hidden verification tests.\n",
		cand.Subject, repoName, strings.TrimSpace(body))
	if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte(instr), 0o644); err != nil {
		return "", err
	}

	// task.toml — contract validated against harbor's TaskConfig pydantic
	// model: schema_version is a string, task.name is org/name, authors are
	// tables, source is a root-level string (must precede any [section]).
	var toml strings.Builder
	toml.WriteString("schema_version = \"1\"\n")
	if repoURL != "" && cand.PRNumber > 0 {
		fmt.Fprintf(&toml, "source = %q\n", fmt.Sprintf("%s/pull/%d", strings.TrimSuffix(repoURL, ".git"), cand.PRNumber))
	}
	toml.WriteString("\n[task]\n")
	fmt.Fprintf(&toml, "name = %q\n", "prospect/"+cand.id())
	toml.WriteString("version = \"1.0.0\"\n")
	fmt.Fprintf(&toml, "description = %q\n", oneLine(cand.Subject))
	toml.WriteString("authors = [{ name = \"prospect\" }]\n")
	fmt.Fprintf(&toml, "keywords = [%s]\n", quotedList([]string{"mined", "go", repoName}))
	fmt.Fprintf(&toml, "\n[verifier]\ntimeout_sec = 900\n")
	toml.WriteString("\n[agent]\ntimeout_sec = 1800\n")
	if len(finalEnv) > 0 {
		toml.WriteString("\n[verifier.env]\n")
		for _, k := range sortedKeys(finalEnv) {
			fmt.Fprintf(&toml, "%s = %q\n", k, finalEnv[k])
		}
	}
	toml.WriteString("\n[metadata]\nverified_host_only = true\n")
	if err := os.WriteFile(filepath.Join(dir, "task.toml"), []byte(toml.String()), 0o644); err != nil {
		return "", err
	}

	// environment: Dockerfile + base-commit snapshot
	var df strings.Builder
	df.WriteString("FROM golang:1.26-alpine\nRUN apk add --no-cache git bash build-base\nWORKDIR /app\nCOPY repo/ ./\nRUN go mod download || true\n")
	for _, k := range sortedKeys(finalEnv) {
		fmt.Fprintf(&df, "ENV %s=%s\n", k, finalEnv[k])
	}
	if err := os.WriteFile(filepath.Join(dir, "environment", "Dockerfile"), []byte(df.String()), 0o644); err != nil {
		return "", err
	}
	if err := gitArchive(repo, cand.BaseSHA, filepath.Join(dir, "environment", "repo")); err != nil {
		return "", fmt.Errorf("snapshot base tree: %w", err)
	}

	// tests: hidden reference tests + runner
	testOverlay := filepath.Join(dir, "tests", "overlay")
	if cand.Kind == fixPair {
		for _, f := range cand.TestFiles {
			if err := writeFileFromCommit(repo, cand.FixSHA, f, filepath.Join(testOverlay, f)); err != nil {
				return "", err
			}
		}
	}
	testSh := fmt.Sprintf(`#!/bin/bash
set -u
mkdir -p /logs/verifier
cd /app
if [ -d /tests/overlay ]; then cp -r /tests/overlay/. /app/; fi
if go test %s -run '^(%s)$' -count=1 -timeout=480s >/tmp/verify.log 2>&1; then
  echo 1 > /logs/verifier/reward.txt
else
  echo 0 > /logs/verifier/reward.txt
  tail -50 /tmp/verify.log
fi
cat /logs/verifier/reward.txt
`, shellPkgPaths(cand.Pkgs), strings.Join(cand.TestNames, "|"))
	if err := os.WriteFile(filepath.Join(dir, "tests", "test.sh"), []byte(testSh), 0o755); err != nil {
		return "", err
	}

	// solution: full post-fix state of every file the fix commit changed
	solOverlay := filepath.Join(dir, "solution", "overlay")
	fixFiles, err := changedFiles(repo, cand.FixSHA)
	if err != nil {
		return "", err
	}
	for _, f := range fixFiles {
		if err := writeFileFromCommit(repo, cand.FixSHA, f, filepath.Join(solOverlay, f)); err != nil {
			continue // non-text or vanished file: skip
		}
	}
	solveSh := `#!/bin/bash
set -u
cp -r /solution/overlay/. /app/ 2>/dev/null || true
cd /app && go build ./... || true
`
	if err := os.WriteFile(filepath.Join(dir, "solution", "solve.sh"), []byte(solveSh), 0o755); err != nil {
		return "", err
	}

	// meta.json (miner bookkeeping, not part of Harbor format)
	meta, _ := json.MarshalIndent(map[string]any{
		"kind": cand.Kind, "base": cand.BaseSHA, "fix": cand.FixSHA,
		"subject": cand.Subject, "pr": cand.PRNumber,
		"test_files": cand.TestFiles, "test_names": cand.TestNames,
		"verifier_lint": cand.VerifierLint,
		"verified": true,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// gitArchive extracts the repo tree at sha into dest (no .git).
func gitArchive(repo, sha, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "prospect-archive-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.Close()
	if _, err := git(repo, "archive", "--format=tar", "-o", tmp.Name(), sha); err != nil {
		return err
	}
	return exec.Command("tar", "-x", "-f", tmp.Name(), "-C", dest).Run()
}

// writeFileFromCommit materializes <sha>:<path> to <dest>.
func writeFileFromCommit(repo, sha, path, dest string) error {
	out, err := git(repo, "show", sha+":"+path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, []byte(out+"\n"), 0o644)
}

func shellPkgPaths(pkgs []string) string {
	var b strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&b, "./%s/ ", p)
	}
	return b.String()
}

func quotedList(xs []string) string {
	for i, x := range xs {
		xs[i] = fmt.Sprintf("%q", x)
	}
	return strings.Join(xs, ", ")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
