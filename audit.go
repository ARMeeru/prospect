package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// audit scans a Harbor jobs directory for origin-fetch attempts — the
// closed-book leak check. Any agent transcript referencing the origin
// (repo slug, clone URL, or git apply of a fetched diff) voids its trial.
func runAudit(jobsDir, origin string) error {
	if origin == "" {
		return fmt.Errorf("origin pattern required (--origin)")
	}
	var violations []string
	seen := map[string]bool{}
	err := filepath.WalkDir(jobsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.Contains(path, string(os.PathSeparator)+"agent"+string(os.PathSeparator)) {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".txt" && ext != ".jsonl" && ext != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), origin) {
			rel, _ := filepath.Rel(jobsDir, path)
			trial := rel
			if i := strings.Index(rel, string(os.PathSeparator)); i > 0 {
				trial = rel[:i]
			}
			if !seen[trial] {
				seen[trial] = true
				violations = append(violations, trial)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(violations) == 0 {
		fmt.Println("audit clean: no origin references found")
		return nil
	}
	fmt.Printf("AUDIT VIOLATIONS (%d trials referenced origin %q) — trials voided:\n", len(violations), origin)
	for _, v := range violations {
		fmt.Println("  " + v)
	}
	return fmt.Errorf("%d trial(s) voided by origin-fetch audit", len(violations))
}
