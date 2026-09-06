package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// reverify loads each emitted task's real artifact (build environment image,
// run tests/test.sh on base → expect 0, apply solution → expect 1) inside
// containers. This is the verifier-of-the-verifier: what Phase 1 verified was
// the recipe, not the artifact.
//
// Policy (per Phase-1 external review):
//   - risk-weighted trials: 5 for flake-prone (concurrency) tasks, 3 floor
//   - any base-run that passes ⇒ discard (poisonous: bug not reliably observable)
//   - any fix-run that fails ⇒ discard
//   - trials stop early once the outcome is decided
//   - both-directions-fail with connection errors ⇒ bucket "needs-compose",
//     not discard: the instance is recoverable, its services are missing

var flakeRe = regexp.MustCompile(`(?i)(concurrent|waitgroup|race|mutex|goroutine|refresh|replay|serial|retry|flaky|timeout)`)

var envErrRe = regexp.MustCompile(`(?i)(dial tcp|connection refused|no such host|connect: |i/o timeout)`)

type rvResult struct {
	Dir         string `json:"dir"`
	BuildOK     bool   `json:"build_ok"`
	Trials      int    `json:"trials_run"`
	BasePassAny bool   `json:"base_pass_any"`
	FixFailAny  bool   `json:"fix_fail_any"`
	Verdict     string `json:"verdict"` // container-verified | needs-compose | discard | build-failure
	Witness     string `json:"failure_witness,omitempty"`
	Note        string `json:"note,omitempty"`
}

func runReverify(roots []string, jobs int, publicRoots map[string]bool) error {
	var dirs []string
	for _, root := range roots {
		// Docker volume mounts require absolute paths; resolve early so a
		// relative CLI argument cannot poison every trial.
		abs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		root = abs
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				if _, err := os.Stat(filepath.Join(root, e.Name(), "task.toml")); err == nil {
					dirs = append(dirs, filepath.Join(root, e.Name()))
				}
			}
		}
	}
	fmt.Printf("re-verifying %d artifacts with %d workers\n", len(dirs), jobs)

	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]rvResult, 0, len(dirs))
	for _, dir := range dirs {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			sem <- struct{}{}
			r := reverifyOne(dir)
			<-sem
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
			fmt.Printf("[%s] %s %s\n", r.Verdict, filepath.Base(dir), r.Note)
		}(dir)
	}
	wg.Wait()

	for _, root := range roots {
		writeRVReport(root, results, publicRoots[root])
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Verdict]++
	}
	fmt.Printf("\nDone: %v\n", counts)
	return nil
}

func reverifyOne(dir string) rvResult {
	name := filepath.Base(dir)
	res := rvResult{Dir: dir}

	metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	var meta struct {
		Subject    string   `json:"subject"`
		TestNames  []string `json:"test_names"`
		TestFiles  []string `json:"test_files"`
		Kind       string   `json:"kind"`
	}
	if err == nil {
		json.Unmarshal(metaRaw, &meta)
	}
	trials := 3
	if flakeRe.MatchString(strings.ToLower(meta.Subject + " " + strings.Join(meta.TestNames, " "))) {
		trials = 5
		res.Note = "elevated-trials; "
	}

	tag := sanitizeTag(name)
	buildCtx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "docker", "build", "--rm", "-q", "-t", tag, filepath.Join(dir, "environment"))
	if out, err := build.CombinedOutput(); err != nil {
		res.Verdict = "build-failure"
		res.Note += "docker build: " + firstLines(string(out), 3)
		return res
	}
	res.BuildOK = true

	for t := 1; t <= trials; t++ {
		baseReward, fixReward, baseLog, fixLog, err := rvTrial(tag, dir)
		if err != nil {
			res.Verdict = "discard"
			res.Note += fmt.Sprintf("trial %d: %v; ", t, err)
			return res
		}
		res.Trials = t
		basePass := baseReward == "1"
		fixPass := fixReward == "1"
		if basePass {
			res.BasePassAny = true
			res.Verdict = "discard"
			res.Note += "base passed (bug not reliably observable)"
			return res
		}
		if t == 1 && res.Witness == "" {
			res.Witness = failureWitness(baseLog, meta.TestNames)
			if res.Witness == "" {
				// reward 0 without an assertion-level failure: build error,
				// module fetch, panic — not the target bug misbehaving.
				if envErrRe.MatchString(baseLog) {
					res.Verdict = "needs-compose"
					res.Note += "base fails with service-connection error (no assertion witness)"
				} else {
					res.Verdict = "discard"
					res.Note += "base fails without failure witness (build/env, not the bug)"
				}
				return res
			}
		}
		if !fixPass {
			// retry within the trial budget to separate flake from hard fail;
			// persistent fix-fail (or env-error signature) discards.
			res.FixFailAny = true
			if envErrRe.MatchString(fixLog) {
				res.Verdict = "needs-compose"
				res.Note += "fix fails with service-connection error"
				return res
			}
			res.Note += fmt.Sprintf("fix-fail trial %d; ", t)
			continue
		}
		res.Verdict = "container-verified"
		break
	}
	if res.FixFailAny && res.Verdict != "container-verified" {
		res.Verdict = "discard"
		res.Note += "fix does not pass within trial budget"
	}
	return res
}

// failureWitness asserts the base-direction failure is assertion-level
// ("--- FAIL: <expected test>") rather than a build/env failure. Returns the
// matching witness line, or "" if none. Note: Go appends a duration suffix
// ("--- FAIL: TestX (0.01s)"), so compare on the test name only.
func failureWitness(baseLog string, testNames []string) string {
	for _, line := range strings.Split(baseLog, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "--- FAIL: ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "--- FAIL: "))
		name := rest
		if i := strings.IndexAny(rest, " ("); i >= 0 {
			name = rest[:i]
		}
		for _, n := range testNames {
			if name == n || strings.HasPrefix(name, n+"/") {
				return line
			}
		}
	}
	return ""
}

// sanitizeTag builds a docker-safe repository tag from a task name.
func sanitizeTag(name string) string {
	trimmed := strings.Trim(regexp.MustCompile(`[^a-zA-Z0-9-]`).ReplaceAllString(strings.ToLower(name), "-"), "-")
	if trimmed == "" {
		return "prospect-rv"
	}
	return "prospect-rv-" + trimmed
}

// rvTrial runs one trial: fresh container, base test → solve → fix test.
func rvTrial(tag, dir string) (baseReward, fixReward, baseLog, fixLog string, err error) {
	logsDir, err := os.MkdirTemp("", "prospect-rv-logs-")
	if err != nil {
		return "", "", "", "", err
	}
	defer os.RemoveAll(logsDir)

	script := `/tests/test.sh >/tmp/base.log 2>&1
BR=$(cat /logs/verifier/reward.txt 2>/dev/null)
/solution/solve.sh >/dev/null 2>&1
rm -f /logs/verifier/reward.txt
/tests/test.sh >/tmp/fix.log 2>&1
FR=$(cat /logs/verifier/reward.txt 2>/dev/null)
echo "RVBASE=$BR RVFIX=$FR"
echo "--BASELOG--"
cat /tmp/base.log
echo "--FIXLOG--"
tail -40 /tmp/fix.log
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", filepath.Join(dir, "tests")+":/tests",
		"-v", filepath.Join(dir, "solution")+":/solution",
		"-v", logsDir+":/logs",
		tag, "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", "", "", "", fmt.Errorf("container timeout")
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("docker run: %v: %s", err, firstLines(string(out), 3))
	}
	text := string(out)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "RVBASE=") {
			fmt.Sscanf(line, "RVBASE=%s RVFIX=%s", &baseReward, &fixReward)
		}
	}
	if i := strings.Index(text, "--BASELOG--"); i >= 0 {
		baseLog = text[i+len("--BASELOG--"):]
		if j := strings.Index(baseLog, "--FIXLOG--"); j >= 0 {
			baseLog = baseLog[:j]
		}
	}
	if j := strings.Index(text, "--FIXLOG--"); j >= 0 {
		fixLog = text[j:]
	}
	if baseReward == "" || fixReward == "" {
		return baseReward, fixReward, baseLog, fixLog, fmt.Errorf("reward file missing")
	}
	return baseReward, fixReward, baseLog, fixLog, nil
}

func writeRVReport(root string, results []rvResult, public bool) {
	root = filepath.Clean(root)
	var mine []rvResult
	for _, r := range results {
		if strings.HasPrefix(filepath.Clean(r.Dir), root+string(os.PathSeparator)) || filepath.Clean(r.Dir) == root {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return
	}
	rj, _ := json.MarshalIndent(mine, "", "  ")
	os.WriteFile(filepath.Join(root, "reverify.json"), rj, 0o644)
	var b strings.Builder
	fmt.Fprintf(&b, "# re-verification — %s\n\n", root)
	b.WriteString("| verdict | task | trials | witness | note |\n|---|---|---|---|---|\n")
	for _, r := range mine {
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s |\n", r.Verdict, filepath.Base(r.Dir), r.Trials, oneLine(r.Witness), oneLine(r.Note))
	}
	os.WriteFile(filepath.Join(root, "reverify-report.md"), []byte(b.String()), 0o644)

	// backfill per-artifact metadata: container verification status,
	// origin visibility, failure witness; retire the stale host-only flag.
	vis := "private"
	if public {
		vis = "public"
	}
	for _, r := range mine {
		if !r.BuildOK {
			continue
		}
		verified := r.Verdict == "container-verified"
		if metaRaw, err := os.ReadFile(filepath.Join(r.Dir, "meta.json")); err == nil {
			var mm map[string]any
			json.Unmarshal(metaRaw, &mm)
			if mm == nil {
				mm = map[string]any{}
			}
			mm["container_verified"] = verified
			mm["origin_visibility"] = vis
			if r.Witness != "" {
				mm["failure_witness"] = r.Witness
			}
			delete(mm, "verified_host_only")
			if mj, err := json.MarshalIndent(mm, "", "  "); err == nil {
				os.WriteFile(filepath.Join(r.Dir, "meta.json"), mj, 0o644)
			}
		}
		if tomlRaw, err := os.ReadFile(filepath.Join(r.Dir, "task.toml")); err == nil {
			t := string(tomlRaw)
			t = strings.Replace(t, "verified_host_only = true\n", "", 1)
			if !strings.Contains(t, "origin_visibility") {
				t = strings.Replace(t, "[metadata]", "[metadata]\norigin_visibility = \""+vis+"\"", 1)
			}
			if verified && !strings.Contains(t, "container_verified") {
				t = strings.Replace(t, "[metadata]", "[metadata]\ncontainer_verified = true", 1)
			}
			os.WriteFile(filepath.Join(r.Dir, "task.toml"), []byte(t), 0o644)
		}
	}
}
