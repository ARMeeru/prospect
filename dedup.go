package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// dedup clusters task instances that share reference test files — instances
// verifying the same behavior through the same tests measure one thing, so
// effective-N (cluster count) is the statistically honest suite size.
func runDedup(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	type task struct {
		name  string
		files map[string]bool
	}
	var tasks []task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := loadMeta(filepath.Join(root, e.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err // schema-1 metadata: run prospect migrate first
		}
		if len(m.TestFiles) == 0 {
			continue
		}
		fs := map[string]bool{}
		for _, f := range m.TestFiles {
			fs[f] = true
		}
		tasks = append(tasks, task{name: e.Name(), files: fs})
	}

	// union-find over shared test files
	parent := make([]int, len(tasks))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) { parent[find(a)] = find(b) }
	for i := range tasks {
		for j := i + 1; j < len(tasks); j++ {
			shared := false
			for f := range tasks[i].files {
				if tasks[j].files[f] {
					shared = true
					break
				}
			}
			if shared {
				union(i, j)
			}
		}
	}

	clusters := map[int][]string{}
	for i := range tasks {
		r := find(i)
		clusters[r] = append(clusters[r], tasks[i].name)
	}
	keys := make([]int, 0, len(clusters))
	for k := range clusters {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for idx, k := range keys {
		members := clusters[k]
		id := fmt.Sprintf("c%02d", idx+1)
		sort.Strings(members)
		for _, name := range members {
			dir := filepath.Join(root, name)
			m, err := loadMeta(dir)
			if err != nil {
				continue
			}
			m.Cluster = id
			m.StagesRun = addStage(m.StagesRun, "dedup")
			if err := saveMeta(dir, m); err != nil {
				return err
			}
		}
		if len(members) > 1 {
			fmt.Printf("%s (%d): %s\n", id, len(members), strings.Join(members, ", "))
		}
	}
	recordStage(root, "dedup", "ok", "", map[string]int{"instances": len(tasks), "clusters": len(keys)})
	fmt.Printf("\ntasks: %d → clusters (effective-N): %d\n", len(tasks), len(keys))
	return nil
}
