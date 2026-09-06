package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// metaSchema is the current per-instance metadata schema version.
const metaSchema = 2

// Meta is the per-instance metadata record (meta.json, schema 2) and the
// single source of truth for task.toml's [metadata] section. Every pipeline
// stage loads and saves instances through loadMeta/saveMeta; nothing else
// may patch meta.json or the [metadata] section in place.
type Meta struct {
	Schema            int      `json:"schema"`
	InstanceID        string   `json:"instance_id"`
	Kind              string   `json:"kind"`
	Base              string   `json:"base"`
	Fix               string   `json:"fix"`
	Subject           string   `json:"subject"`
	PR                int      `json:"pr"`
	TestFiles         []string `json:"test_files"`
	TestNames         []string `json:"test_names"`
	Cluster           string   `json:"cluster,omitempty"`
	ContainerVerified bool     `json:"container_verified"`
	OriginVisibility  string   `json:"origin_visibility,omitempty"`
	FailureWitness    string   `json:"failure_witness,omitempty"`
	InstructionClass  string   `json:"instruction_class,omitempty"`
	LeakFlag          bool     `json:"leak_flag"`
	LeakSymbols       []string `json:"leak_symbols,omitempty"`
	LeakVerbs         []string `json:"leak_verbs,omitempty"`
	ChangedSymbols    []string `json:"changed_symbols,omitempty"`
	VerifierLint      []string `json:"verifier_lint,omitempty"`
	StagesRun         []string `json:"stages_run,omitempty"`
}

// loadMeta reads a task dir's meta.json. Schema-1 metadata is refused with
// a pointer to prospect migrate (decision D1: explicit migration, never
// silent upgrades on read).
func loadMeta(dir string) (Meta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Meta{}, err
	}
	var probe struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Meta{}, fmt.Errorf("%s: %w", filepath.Join(dir, "meta.json"), err)
	}
	if probe.Schema != metaSchema {
		return Meta{}, fmt.Errorf("%s has schema-%d metadata; run: prospect migrate <tasks-root>", dir, max(probe.Schema, 1))
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Meta{}, fmt.Errorf("%s: %w", filepath.Join(dir, "meta.json"), err)
	}
	return m, nil
}

// saveMeta writes meta.json and regenerates task.toml's [metadata] section
// from the same record, so the two stores cannot diverge.
func saveMeta(dir string, m Meta) error {
	m.Schema = metaSchema
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), mj, 0o644); err != nil {
		return err
	}
	tomlPath := filepath.Join(dir, "task.toml")
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no task.toml yet (or ever); meta.json alone is fine
		}
		return err
	}
	return os.WriteFile(tomlPath, []byte(applyTaskTOMLMetadata(string(raw), m)), 0o644)
}

// addStage appends name to stages once, preserving order of first run.
func addStage(stages []string, name string) []string {
	for _, s := range stages {
		if s == name {
			return stages
		}
	}
	return append(stages, name)
}

// tomlMetadataSection renders the [metadata] section for m. Line order
// reproduces the historical patch results byte for byte: container_verified
// and origin_visibility appear once re-verification has run,
// verified_host_only marks the pre-container state, instruction_class
// follows, and the canonical join keys close the section.
func tomlMetadataSection(m Meta) string {
	var b strings.Builder
	b.WriteString("[metadata]\n")
	if m.ContainerVerified {
		b.WriteString("container_verified = true\n")
	}
	if m.OriginVisibility != "" {
		fmt.Fprintf(&b, "origin_visibility = %q\n", m.OriginVisibility)
	}
	if !m.ContainerVerified && m.OriginVisibility == "" {
		b.WriteString("verified_host_only = true\n")
	}
	if m.InstructionClass != "" {
		fmt.Fprintf(&b, "instruction_class = %q\n", m.InstructionClass)
	}
	if m.InstanceID != "" {
		fmt.Fprintf(&b, "instance_id = %q\n", m.InstanceID)
	}
	if m.Cluster != "" {
		fmt.Fprintf(&b, "cluster = %q\n", m.Cluster)
	}
	return b.String()
}

// applyTaskTOMLMetadata returns toml with its [metadata] section (always the
// final section of an emitted task.toml) regenerated from m, appending one
// if the file has none.
func applyTaskTOMLMetadata(toml string, m Meta) string {
	if i := strings.Index(toml, "\n[metadata]\n"); i >= 0 {
		return toml[:i+1] + tomlMetadataSection(m)
	}
	if !strings.HasSuffix(toml, "\n") {
		toml += "\n"
	}
	return toml + "\n" + tomlMetadataSection(m)
}
