package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// manifestSchema is the current suite.json schema version.
const manifestSchema = 1

// stageRecord is one pipeline stage's entry in suite.json.
type stageRecord struct {
	Name      string         `json:"name"`
	Timestamp string         `json:"timestamp"`
	Status    string         `json:"status"` // ok | failed | skipped
	Note      string         `json:"note,omitempty"`
	Counts    map[string]int `json:"counts,omitempty"`
}

// suiteManifest is suite.json at a tasks root: repo identity, mine config,
// per-stage records, and totals. score and later phases read it.
type suiteManifest struct {
	Schema   int               `json:"schema"`
	RepoName string            `json:"repo_name"`
	RepoURL  string            `json:"repo_url,omitempty"`
	RepoPath string            `json:"repo_path,omitempty"`
	Since    string            `json:"since,omitempty"`
	Limit    int               `json:"limit,omitempty"`
	Public   bool              `json:"public"`
	Env      map[string]string `json:"env,omitempty"`
	Stages   []stageRecord     `json:"stages"`
	Totals   map[string]int    `json:"totals,omitempty"`
}

// loadManifest reads suite.json under root; (nil, nil) when absent.
func loadManifest(root string) (*suiteManifest, error) {
	raw, err := os.ReadFile(filepath.Join(root, "suite.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m suiteManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *suiteManifest) save(root string) error {
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "suite.json"), mj, 0o644)
}

// stage returns the record for name, or nil.
func (m *suiteManifest) stage(name string) *stageRecord {
	for i := range m.Stages {
		if m.Stages[i].Name == name {
			return &m.Stages[i]
		}
	}
	return nil
}

// setStage upserts a stage record (matched by name) with a fresh timestamp.
func (m *suiteManifest) setStage(name, status, note string, counts map[string]int) {
	rec := stageRecord{
		Name:      name,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Status:    status,
		Note:      note,
		Counts:    counts,
	}
	if cur := m.stage(name); cur != nil {
		*cur = rec
		return
	}
	m.Stages = append(m.Stages, rec)
}

// recordStage upserts a stage record in an existing suite.json. Roots
// without a manifest (pre-manifest suites) are left untouched: stage
// commands remain usable there, they just are not tracked.
func recordStage(root, name, status, note string, counts map[string]int) {
	m, err := loadManifest(root)
	if err != nil || m == nil {
		return
	}
	m.setStage(name, status, note, counts)
	m.save(root)
}
