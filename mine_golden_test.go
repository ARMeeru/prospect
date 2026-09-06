package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// dirManifest returns one "sha256  relpath" line per file under root,
// sorted by path. Byte-identity of the manifest implies byte-identity of
// every file mine wrote.
func dirManifest(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("%x  %s", sha256.Sum256(data), filepath.ToSlash(rel)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// goldenCompare asserts got matches the committed golden file. Run with
// PROSPECT_UPDATE_GOLDEN=1 to rewrite goldens after an intended change.
func goldenCompare(t *testing.T, goldenPath string, got []byte) {
	t.Helper()
	if os.Getenv("PROSPECT_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden %s (run with PROSPECT_UPDATE_GOLDEN=1 to create): %v", goldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s: output differs from golden\n--- got ---\n%s\n--- want ---\n%s", goldenPath, clip(got), clip(want))
	}
}

func clip(b []byte) string {
	const max = 4000
	if len(b) > max {
		return string(b[:max]) + "\n…(clipped)"
	}
	return string(b)
}

// TestMineFixtureGolden runs the full mine pipeline (host-side, no Docker)
// on the deterministic fixture repo and asserts every emitted byte against
// committed goldens. This is the refactor byte-identity harness: the golden
// set was generated before the Phase-1 refactor and any intended change to
// it must arrive in the same commit as the code that causes it.
func TestMineFixtureGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build/test subprocesses")
	}
	repo := buildFixtureRepo(t)
	out := filepath.Join(t.TempDir(), "suite")
	if err := mine(mineConfig{Repo: repo, Out: out, Env: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	goldenCompare(t, filepath.Join("testdata", "golden", "mine", "manifest.txt"), []byte(dirManifest(t, out)))

	// Full-content goldens for the files a human reviews when the manifest
	// moves: the run reports and one emitted task of each kind.
	fullContent := []string{
		"results.json",
		"summary.md",
		"fix-pr0-fix-clamp-negative-counts-to-zero/task.toml",
		"fix-pr0-fix-clamp-negative-counts-to-zero/meta.json",
		"fix-pr0-fix-clamp-negative-counts-to-zero/instruction.md",
		"fix-pr0-fix-clamp-negative-counts-to-zero/tests/test.sh",
		"tdd-pr0-test-repeat-repeats-input-n-times-tdd-9b/task.toml",
		"tdd-pr0-test-repeat-repeats-input-n-times-tdd-9b/meta.json",
	}
	for _, rel := range fullContent {
		data, err := os.ReadFile(filepath.Join(out, rel))
		if err != nil {
			t.Errorf("expected mine output %s: %v", rel, err)
			continue
		}
		goldenCompare(t, filepath.Join("testdata", "golden", "mine", filepath.FromSlash(rel)), data)
	}
}
