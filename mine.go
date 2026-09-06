package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type mineConfig struct {
	Repo          string
	Since         string
	Limit         int
	Out           string
	Env           map[string]string
	EmitUnverified bool
	ProbeOnly     bool // discovery + compile probe only; no behavioral runs, no emission
}

type resultRow struct {
	Kind      string `json:"kind"`
	Base      string `json:"base_sha"`
	Fix       string `json:"fix_sha"`
	Subject   string `json:"subject"`
	PR        int    `json:"pr"`
	Compiles  bool   `json:"compiles"`
	BeforeOK  bool   `json:"before_passes"`
	AfterOK   bool   `json:"after_passes"`
	Verified  bool   `json:"verified"`
	Emitted   bool   `json:"emitted"`
	Skip      string `json:"skip,omitempty"`
}

func mine(cfg mineConfig) error {
	repo, err := filepath.Abs(cfg.Repo)
	if err != nil {
		return err
	}
	if _, err := git(repo, "rev-parse", "HEAD"); err != nil {
		return fmt.Errorf("not a git repo: %s", cfg.Repo)
	}
	repoName := filepath.Base(repo)
	repoURL := strings.TrimSpace(must(git(repo, "config", "--get", "remote.origin.url")))
	if strings.HasPrefix(repoURL, "git@") {
		repoURL = "https://" + strings.ReplaceAll(strings.TrimPrefix(repoURL, "git@"), ":", "/")
	}
	repoURL = strings.TrimSuffix(repoURL, ".git")

	cands, err := discover(repo, cfg.Since, cfg.Limit)
	if err != nil {
		return err
	}
	mineable := 0
	for i := range cands {
		if cands[i].skipReason == "" {
			mineable++
		}
	}
	fmt.Printf("discovered %d candidates (%d mineable)\n", len(cands), mineable)

	ci, ciErr := parseCI(repo)
	if ciErr != nil {
		fmt.Printf("ci parse: %v (continuing without services)\n", ciErr)
	}
	var started *startedEnv
	if ci != nil && len(ci.Services) > 0 {
		started, err = ci.startServices()
		if err != nil {
			fmt.Printf("services: %v (continuing without)\n", err)
			started = nil
		} else {
			fmt.Printf("services up: %s\n", ci.Source)
		}
	}
	if started != nil {
		defer started.cleanup()
	}

	finalEnv := map[string]string{}
	if ci != nil {
		for k, v := range ci.Env {
			finalEnv[k] = v
		}
	}
	for k, v := range cfg.Env {
		finalEnv[k] = v
	}
	if started != nil {
		for k, v := range started.Final {
			finalEnv[k] = v
		}
	}

	os.MkdirAll(cfg.Out, 0o755)
	mf, err := loadManifest(cfg.Out)
	if err != nil {
		return err
	}
	if mf == nil {
		mf = &suiteManifest{}
	}
	mf.Schema = manifestSchema
	mf.RepoName = repoName
	mf.RepoURL = repoURL
	mf.RepoPath = repo
	mf.Since = cfg.Since
	mf.Limit = cfg.Limit
	if len(cfg.Env) > 0 {
		mf.Env = cfg.Env
	}
	var rows []resultRow
	emitted := 0
	verified := 0
	compiled := 0

	for i := range cands {
		cand := &cands[i]
		if cand.skipReason != "" {
			rows = append(rows, resultRow{Kind: string(cand.Kind), Base: cand.BaseSHA, Fix: cand.FixSHA, Subject: cand.Subject, PR: cand.PRNumber, Skip: cand.skipReason})
			continue
		}
		fmt.Printf("[%d/%d] %s %s\n", i+1, len(cands), cand.Kind, cand.Subject)

		wtBase, cleanBase, err := worktreeAt(repo, cand.BaseSHA)
		if err != nil {
			cand.skipReason = "worktree: " + err.Error()
			rows = append(rows, resultRow{Subject: cand.Subject, Skip: cand.skipReason})
			continue
		}
		wtFix, cleanFix, err := worktreeAt(repo, cand.FixSHA)
		if err != nil {
			cleanBase()
			cand.skipReason = "worktree: " + err.Error()
			rows = append(rows, resultRow{Subject: cand.Subject, Skip: cand.skipReason})
			continue
		}

		row := resultRow{Kind: string(cand.Kind), Base: cand.BaseSHA, Fix: cand.FixSHA, Subject: cand.Subject, PR: cand.PRNumber}
		func() {
			defer cleanBase()
			defer cleanFix()
			if cand.Kind == fixPair {
				if err := checkoutFiles(wtBase, cand.FixSHA, cand.TestFiles); err != nil {
					row.Skip = "overlay: " + err.Error()
					return
				}
			}
			if ok, why := compileCheck(wtBase, cand.Pkgs); !ok {
				row.Skip = "compile-coupled: " + why
				return
			}
			row.Compiles = true
			compiled++
			if cfg.ProbeOnly {
				row.Skip = "probe-only"
				rows = append(rows, row)
				fmt.Printf("  probe-compile ✓\n")
				return
			}
			// verifier lint: reference tests asserting on error/message TEXT
			// reject semantically-correct fixes that word things differently
			// (the confirmed over-specific class). Nominate, don't reject.
			if flagged := lintMessageAssertions(repo, *cand); len(flagged) > 0 {
				cand.VerifierLint = flagged
				fmt.Printf("  lint: message-assertion tests in %s\n", strings.Join(flagged, ", "))
			}
			row.BeforeOK, _ = runGoTest(wtBase, cand.Pkgs, cand.TestNames, finalEnv)
			row.AfterOK, _ = runGoTest(wtFix, cand.Pkgs, cand.TestNames, finalEnv)
			row.Verified = !row.BeforeOK && row.AfterOK
			if row.Verified {
				verified++
				path, err := emitTask(cfg.Out, repo, repoName, repoURL, *cand, finalEnv)
				if err != nil {
					fmt.Printf("  emit failed: %v\n", err)
					row.Skip = "emit: " + err.Error()
					return
				}
				row.Emitted = true
				emitted++
				fmt.Printf("  ✓ verified → %s\n", filepath.Base(path))
			} else {
				why := "base passes (not fail-before)"
				if row.BeforeOK == false && row.AfterOK == false {
					why = "fix commit does not pass (env or flake)"
				}
				row.Skip = why
				fmt.Printf("  ✗ %s\n", why)
			}
		}()
		rows = append(rows, row)
	}

	if err := writeReports(cfg, repoName, rows, len(cands), mineable, compiled, verified, emitted); err != nil {
		return err
	}
	mf.setStage("mine", "ok", "", map[string]int{
		"candidates": len(cands), "mineable": mineable, "compiled": compiled,
		"verified": verified, "emitted": emitted,
	})
	if mf.Totals == nil {
		mf.Totals = map[string]int{}
	}
	mf.Totals["emitted"] = emitted
	if err := mf.save(cfg.Out); err != nil {
		return err
	}
	fmt.Printf("\nDone: %d candidates, %d compiled, %d verified, %d emitted → %s\n", len(cands), compiled, verified, emitted, cfg.Out)
	return nil
}

func writeReports(cfg mineConfig, repo string, rows []resultRow, total, mineable, compiled, verified, emitted int) error {
	rj, _ := json.MarshalIndent(rows, "", "  ")
	os.WriteFile(filepath.Join(cfg.Out, "results.json"), rj, 0o644)
	var b strings.Builder
	fmt.Fprintf(&b, "# prospect — %s\n\n", repo)
	fmt.Fprintf(&b, "| total | mineable | compiled | verified | emitted |\n|---|---|---|---|---|\n| %d | %d | %d | %d | %d |\n\n", total, mineable, compiled, verified, emitted)
	b.WriteString("| kind | pr | verified | note | subject |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		note := r.Skip
		if r.Verified {
			note = "✓"
		}
		fmt.Fprintf(&b, "| %s | %d | %t | %s | %s |\n", r.Kind, r.PR, r.Verified, oneLine(note), oneLine(r.Subject))
	}
	return os.WriteFile(filepath.Join(cfg.Out, "summary.md"), []byte(b.String()), 0o644)
}

func must(s string, err error) string {
	if err != nil {
		return ""
	}
	return s
}
