package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildFixtureSuite runs the pipeline end to end on the fixture repo
// without Docker or gh (reverify and issues skipped by flag, as on any
// machine without those tools) and asserts the manifest and per-instance
// metadata that build promises.
func TestBuildFixtureSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build/test subprocesses")
	}
	repo := buildFixtureRepo(t)
	out := filepath.Join(t.TempDir(), "suite")
	err := runBuild(buildConfig{
		Repo: repo, Out: out, Jobs: 1,
		Skip: map[string]bool{"reverify": true, "issues": true},
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}

	mf, err := loadManifest(out)
	if err != nil || mf == nil {
		t.Fatalf("suite.json: %v", err)
	}
	wantStatus := map[string]string{
		"mine": "ok", "reverify": "skipped", "deleak": "ok", "issues": "skipped", "dedup": "ok",
	}
	for name, want := range wantStatus {
		st := mf.stage(name)
		if st == nil || st.Status != want {
			t.Errorf("stage %s = %+v, want status %q", name, st, want)
		}
	}
	if mf.Totals["instances"] != 3 || mf.Totals["clusters"] != 3 {
		t.Errorf("totals = %v, want 3 instances / 3 clusters", mf.Totals)
	}
	if mf.RepoName != "fixturerepo" || mf.RepoPath == "" {
		t.Errorf("repo identity = %q / %q", mf.RepoName, mf.RepoPath)
	}

	// per-instance metadata after mine → deleak → dedup
	inst := "fix-pr0-fix-clamp-negative-counts-to-zero"
	m, err := loadMeta(filepath.Join(out, inst))
	if err != nil {
		t.Fatal(err)
	}
	if m.InstanceID != inst || m.Cluster == "" || m.InstructionClass != "unclassified" {
		t.Errorf("meta = %+v", m)
	}
	if want := []string{"mine", "deleak", "dedup"}; strings.Join(m.StagesRun, ",") != strings.Join(want, ",") {
		t.Errorf("stages_run = %v, want %v", m.StagesRun, want)
	}
	// deleak nomination fires on the fixture: "clamp" is a mechanism verb
	// (stored raw, one entry per match, as deleak always has)
	if !m.LeakFlag || len(m.LeakVerbs) == 0 || m.LeakVerbs[0] != "clamp" {
		t.Errorf("leak nomination = flag %v, verbs %v", m.LeakFlag, m.LeakVerbs)
	}
	toml, err := os.ReadFile(filepath.Join(out, inst, "task.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"instance_id = \"" + inst + "\"",
		"cluster = \"" + m.Cluster + "\"",
		"instruction_class = \"unclassified\"",
		"verified_host_only = true",
	} {
		if !strings.Contains(string(toml), want) {
			t.Errorf("task.toml missing %q", want)
		}
	}
	// deleak's instruction rewrite keeps the body and appends the no-tests rule
	instr, err := os.ReadFile(filepath.Join(out, inst, "instruction.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Counts below zero clamp to zero", "Do not add tests."} {
		if !strings.Contains(string(instr), want) {
			t.Errorf("instruction.md missing %q", want)
		}
	}
}

// TestBuildRefusesWithoutMine proves the prerequisite gate: skipping mine on
// a fresh directory records every later stage as skipped, not run.
func TestBuildRefusesWithoutMine(t *testing.T) {
	out := filepath.Join(t.TempDir(), "suite")
	err := runBuild(buildConfig{
		Repo: t.TempDir(), Out: out, Jobs: 1,
		Skip: map[string]bool{"mine": true},
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	mf, err := loadManifest(out)
	if err != nil || mf == nil {
		t.Fatalf("suite.json: %v", err)
	}
	for _, name := range []string{"reverify", "deleak", "issues", "dedup"} {
		st := mf.stage(name)
		if st == nil || st.Status != "skipped" || !strings.Contains(st.Note, "prerequisite") {
			t.Errorf("stage %s = %+v, want prerequisite skip", name, st)
		}
	}
	if st := mf.stage("mine"); st == nil || st.Status != "skipped" {
		t.Errorf("mine stage = %+v, want skipped", st)
	}
}
