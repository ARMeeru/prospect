package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// runMigrate lifts schema-1 meta.json files under root to schema 2 and
// regenerates each task.toml [metadata] section through the single writer,
// so migrated instances carry the canonical join key. Idempotent: schema-2
// instances are left untouched. The schema-1 "verified" field is dropped
// (every emitted instance was host-verified by construction;
// container_verified is the meaningful flag after re-verification).
func runMigrate(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	migrated, current := 0, 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			continue
		}
		var probe struct {
			Schema int `json:"schema"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(dir, "meta.json"), err)
		}
		if probe.Schema == metaSchema {
			current++
			continue
		}
		var old map[string]any
		if err := json.Unmarshal(raw, &old); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(dir, "meta.json"), err)
		}
		m := Meta{
			InstanceID:        e.Name(),
			Kind:              jsonStr(old, "kind"),
			Base:              jsonStr(old, "base"),
			Fix:               jsonStr(old, "fix"),
			Subject:           jsonStr(old, "subject"),
			PR:                jsonInt(old, "pr"),
			TestFiles:         jsonStrs(old, "test_files"),
			TestNames:         jsonStrs(old, "test_names"),
			Cluster:           jsonStr(old, "cluster"),
			ContainerVerified: jsonBool(old, "container_verified"),
			OriginVisibility:  jsonStr(old, "origin_visibility"),
			FailureWitness:    jsonStr(old, "failure_witness"),
			InstructionClass:  jsonStr(old, "instruction_class"),
			LeakFlag:          jsonBool(old, "leak_flag"),
			LeakSymbols:       jsonStrs(old, "leak_symbols"),
			LeakVerbs:         jsonStrs(old, "leak_verbs"),
			ChangedSymbols:    jsonStrs(old, "changed_symbols"),
			VerifierLint:      jsonStrs(old, "verifier_lint"),
		}
		if err := saveMeta(dir, m); err != nil {
			return err
		}
		migrated++
	}
	fmt.Printf("migrated %d instance(s) to schema %d (%d already current)\n", migrated, metaSchema, current)
	return nil
}

func jsonStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func jsonBool(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func jsonInt(m map[string]any, k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

func jsonStrs(m map[string]any, k string) []string {
	xs, _ := m[k].([]any)
	var out []string
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
