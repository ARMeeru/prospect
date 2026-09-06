package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildFixtureRepo constructs a deterministic git repository covering every
// discovery class the miner distinguishes: a no-test commit (ignored), a
// feature commit with tests (verified fix-pair), a clean fix-pair, a TDD
// pair (test-only commit, then the fix), an interface-coupled fix (compile
// probe must discard), and a single-parent commit with a Merge subject
// (topology discard). All content is fictional (example.com/fixture).
// Author, committer, and dates are pinned, so SHAs and mine output are
// byte-stable across runs.
func buildFixtureRepo(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixturerepo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		// Isolate from user/system git config: no signing, no hooks, no
		// templates. Identity comes from the env vars below.
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		cmd.Env = append(cmd.Env, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	commit := func(subject, body string) {
		t.Helper()
		n++
		date := fmt.Sprintf("2026-01-%02dT10:00:00 +0000", n)
		env := []string{
			"GIT_AUTHOR_NAME=Fixture Author",
			"GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=Fixture Author",
			"GIT_COMMITTER_EMAIL=fixture@example.com",
			"GIT_AUTHOR_DATE=" + date,
			"GIT_COMMITTER_DATE=" + date,
		}
		msg := subject
		if body != "" {
			msg += "\n\n" + body
		}
		run(env, "add", "-A")
		run(env, "commit", "-q", "-m", msg)
	}

	run(nil, "init", "-q", "-b", "main")

	// C1: no-test commit (ignored by discovery).
	write("go.mod", "module example.com/fixture\n\ngo 1.24\n")
	write("greet.go", `// Package fixture is synthetic test data for the prospect test suite.
package fixture

// Greet returns a greeting.
func Greet(name string) string {
	_ = name
	return "hi"
}
`)
	write("count.go", `package fixture

// Count returns the number of items.
func Count(n int) int {
	return n
}
`)
	write("repeat.go", `package fixture

// Repeat repeats s n times.
func Repeat(s string, n int) string {
	_ = n
	return s
}
`)
	commit("chore: init module", "")

	// C2: feature commit with tests (fix-pair, verified: test fails on C1).
	write("greet.go", `// Package fixture is synthetic test data for the prospect test suite.
package fixture

// Greet returns a greeting addressed to name.
func Greet(name string) string {
	return "hi " + name
}
`)
	write("greet_test.go", `package fixture

import "testing"

func TestGreetName(t *testing.T) {
	if got := Greet("ada"); got != "hi ada" {
		t.Fatalf("Greet = %q, want %q", got, "hi ada")
	}
}
`)
	commit("feat: greet includes the name", "")

	// C3: clean fix-pair (verified: test fails on C2).
	write("count.go", `package fixture

// Count returns the number of items, never negative.
func Count(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
`)
	write("count_test.go", `package fixture

import "testing"

func TestCountClamp(t *testing.T) {
	if got := Count(-5); got != 0 {
		t.Fatalf("Count(-5) = %d, want 0", got)
	}
	if got := Count(3); got != 3 {
		t.Fatalf("Count(3) = %d, want 3", got)
	}
}
`)
	commit("fix: clamp negative counts to zero", "Counts below zero clamp to zero instead of passing through.")

	// C4: TDD pair, test half (test-only commit; fails until C5 lands).
	write("repeat_test.go", `package fixture

import "testing"

func TestRepeat(t *testing.T) {
	if got := Repeat("ab", 2); got != "abab" {
		t.Fatalf("Repeat = %q, want %q", got, "abab")
	}
}
`)
	commit("test: repeat repeats input n times", "")

	// C5: TDD pair, fix half (no test files touched).
	write("repeat.go", `package fixture

import "strings"

// Repeat repeats s n times.
func Repeat(s string, n int) string {
	return strings.Repeat(s, n)
}
`)
	commit("fix repeat to honor the count", "")

	// C6: interface-coupled fix (new symbol + test; overlay cannot compile
	// against C5, so the compile probe must discard it).
	write("shout.go", `package fixture

import "strings"

// Shout upcases s and adds emphasis.
func Shout(s string) string {
	return strings.ToUpper(s) + "!"
}
`)
	write("shout_test.go", `package fixture

import "testing"

func TestShout(t *testing.T) {
	if got := Shout("hey"); got != "HEY!" {
		t.Fatalf("Shout = %q, want %q", got, "HEY!")
	}
}
`)
	commit("fix: add shout helper", "")

	// C7: single-parent commit with a Merge subject (topology discard; adds
	// a test func so it reaches the topology check at all).
	write("greet.go", `// Package fixture is synthetic test data for the prospect test suite.
package fixture

// Greet returns a greeting addressed to name.
// It always uses the informal register.
func Greet(name string) string {
	return "hi " + name
}
`)
	write("greet_test.go", `package fixture

import "testing"

func TestGreetName(t *testing.T) {
	if got := Greet("ada"); got != "hi ada" {
		t.Fatalf("Greet = %q, want %q", got, "hi ada")
	}
}

func TestGreetAgain(t *testing.T) {
	if got := Greet("bo"); got != "hi bo" {
		t.Fatalf("Greet = %q, want %q", got, "hi bo")
	}
}
`)
	commit("Merge fictional fork", "")

	return root
}
