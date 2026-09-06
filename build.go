package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// buildStages is the fixed pipeline order.
var buildStages = []string{"mine", "reverify", "deleak", "issues", "dedup"}

type buildConfig struct {
	Repo   string
	Out    string
	Since  string
	Limit  int
	Public bool
	Skip   map[string]bool
	Env    map[string]string
	Jobs   int
}

// runBuild executes mine → reverify → deleak → issues → dedup, recording
// every stage in suite.json. Stages after mine refuse to run when the
// manifest does not show mine ok (from this run or an earlier one).
// Unavailable tooling records a skip instead of failing the build: reverify
// needs Docker, issues needs an authenticated gh.
func runBuild(cfg buildConfig) error {
	repo, err := filepath.Abs(cfg.Repo)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(cfg.Out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	mf, err := loadManifest(out)
	if err != nil {
		return err
	}
	if mf == nil {
		mf = &suiteManifest{Schema: manifestSchema, RepoName: filepath.Base(repo)}
	}
	mf.RepoPath = repo
	mf.Public = cfg.Public
	if err := mf.save(out); err != nil {
		return err
	}

	skipStage := func(name, note string) {
		recordStage(out, name, "skipped", note, nil)
		fmt.Printf("-- %s skipped: %s\n", name, note)
	}
	mineOK := func() bool {
		m, _ := loadManifest(out)
		if m == nil {
			return false
		}
		st := m.stage("mine")
		return st != nil && st.Status == "ok"
	}

	for _, name := range buildStages {
		if cfg.Skip[name] {
			skipStage(name, "skipped by --skip")
			continue
		}
		if name != "mine" && !mineOK() {
			skipStage(name, "prerequisite mine not satisfied")
			continue
		}
		fmt.Printf("== %s\n", name)
		var err error
		switch name {
		case "mine":
			err = mine(mineConfig{Repo: repo, Since: cfg.Since, Limit: cfg.Limit, Out: out, Env: cfg.Env})
		case "reverify":
			if dockerOK() != nil {
				skipStage(name, "docker unavailable")
				continue
			}
			pub := map[string]bool{}
			if cfg.Public {
				pub[filepath.Clean(out)] = true
			}
			err = runReverify([]string{out}, cfg.Jobs, pub)
		case "deleak":
			err = runDeleak(out, repo, repoDisplayName(out))
		case "issues":
			if ghAuthOK() != nil {
				skipStage(name, "gh not authenticated")
				continue
			}
			err = runIssues(out)
		case "dedup":
			err = runDedup(out)
		}
		if err != nil {
			recordStage(out, name, "failed", err.Error(), nil)
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	if mf, _ := loadManifest(out); mf != nil {
		if mf.Totals == nil {
			mf.Totals = map[string]int{}
		}
		if st := mf.stage("dedup"); st != nil && st.Status == "ok" {
			mf.Totals["instances"] = st.Counts["instances"]
			mf.Totals["clusters"] = st.Counts["clusters"]
		}
		if err := mf.save(out); err != nil {
			return err
		}
	}
	fmt.Printf("build complete → %s\n", out)
	return nil
}

// ghAuthOK reports whether the gh CLI is present and authenticated.
func ghAuthOK() error {
	return exec.Command("gh", "auth", "status").Run()
}
