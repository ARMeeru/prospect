package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runScoreToJSON(t *testing.T, suite string, sweeps []sweepInput) (map[string]any, []sweepSummary, []pairStats) {
	t.Helper()
	prefix := filepath.Join(t.TempDir(), "scorecard")
	if err := runScore(suite, sweeps, filepath.Join("scripts", "prices.json"), prefix); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(prefix + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Suite     string         `json:"suite"`
		Instances int            `json:"instances"`
		Clusters  int            `json:"clusters"`
		Sweeps    []sweepSummary `json:"sweeps"`
		Pairs     []pairStats    `json:"pairs"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	json.Unmarshal(raw, &top)
	if report.Instances != 6 || report.Clusters != 3 {
		t.Fatalf("suite size = %d instances / %d clusters", report.Instances, report.Clusters)
	}
	return top, report.Sweeps, report.Pairs
}

// TestScoreParityWithPython asserts the Go scorecard reproduces the retired
// Python scripts' arithmetic on the committed fixtures. Reference values
// come from running scripts/scorecard.py and scripts/cost-report.py on
// exactly these fixtures before deletion:
//
//	sonnet (model-a): 18 trials, 1 exception, 0 limit hits, pass 4/6 = 67%
//	opus   (model-b): 18 trials, 0 exceptions, 0 limit hits, pass 3/6 = 50%
//	per-instance: both {alpha-pr1, beta-pr3}, sonnet-only {beta-pr4,
//	gamma-pr6}, opus-only {alpha-pr2}, neither {beta-pr5}
//	cost: sonnet $0.0153, opus $0.03825 (printed 0.0382 at 4 dp)
func TestScoreParityWithPython(t *testing.T) {
	suite := filepath.Join("testdata", "score", "suite")
	_, sweeps, pairs := runScoreToJSON(t, suite, []sweepInput{
		{Label: "sonnet", Dir: filepath.Join("testdata", "score", "jobs", "model-a")},
		{Label: "opus", Dir: filepath.Join("testdata", "score", "jobs", "model-b")},
	})
	if len(sweeps) != 2 || len(pairs) != 1 {
		t.Fatalf("sweeps = %d, pairs = %d", len(sweeps), len(pairs))
	}
	a, b := sweeps[0], sweeps[1]

	// raw counting parity with scorecard.py
	if a.Trials != 18 || a.Exceptions != 1 || a.LimitHits != 0 {
		t.Errorf("sonnet sweep stats = %+v", a)
	}
	if b.Trials != 18 || b.Exceptions != 0 || b.LimitHits != 0 {
		t.Errorf("opus sweep stats = %+v", b)
	}
	if a.InstancesPassed != 4 || a.InstancesScored != 6 {
		t.Errorf("sonnet instances = %d/%d, want 4/6", a.InstancesPassed, a.InstancesScored)
	}
	if b.InstancesPassed != 3 || b.InstancesScored != 6 {
		t.Errorf("opus instances = %d/%d, want 3/6", b.InstancesPassed, b.InstancesScored)
	}

	// cost parity with cost-report.py
	if !a.CostKnown || math.Abs(a.CostUSD-0.0153) > 1e-9 {
		t.Errorf("sonnet cost = %v (known %v), want 0.0153", a.CostUSD, a.CostKnown)
	}
	if !b.CostKnown || math.Abs(b.CostUSD-0.03825) > 1e-9 {
		t.Errorf("opus cost = %v (known %v), want 0.03825", b.CostUSD, b.CostKnown)
	}

	// cluster weighting (new): sonnet solves c02+c03, opus solves c01
	if a.ClustersPassed != 2 || a.ClustersScored != 3 {
		t.Errorf("sonnet clusters = %d/%d, want 2/3", a.ClustersPassed, a.ClustersScored)
	}
	if b.ClustersPassed != 1 || b.ClustersScored != 3 {
		t.Errorf("opus clusters = %d/%d, want 1/3", b.ClustersPassed, b.ClustersScored)
	}
	if math.Abs(a.CostPerSolved-0.0153/2) > 1e-9 || math.Abs(b.CostPerSolved-0.03825) > 1e-9 {
		t.Errorf("cost per solved cluster = %v / %v", a.CostPerSolved, b.CostPerSolved)
	}

	// wall-clock: 60s per trial; sonnet solved c02 (9 trials) + c03 (3),
	// median of {540, 180} = 360; opus solved c01 (6 trials) = 360
	if !a.HasWallclock || a.MedianWallSolved != 360 {
		t.Errorf("sonnet wall-clock = %v (has %v), want 360", a.MedianWallSolved, a.HasWallclock)
	}
	if !b.HasWallclock || b.MedianWallSolved != 360 {
		t.Errorf("opus wall-clock = %v (has %v), want 360", b.MedianWallSolved, b.HasWallclock)
	}

	// paired inference: discordant c01 (opus), c02+c03 (sonnet); sign test
	// with n+=2, n-=1 gives p = 1.0 exactly; diff = +33 points
	p := pairs[0]
	if p.PairedClusters != 3 || p.SignTestP != 1.0 {
		t.Errorf("pair = %+v", p)
	}
	if math.Abs(p.DiffPoints-100.0/3) > 1e-9 {
		t.Errorf("diff = %v, want +33.3", p.DiffPoints)
	}
	if strings.Join(p.AOnly, ",") != "c02,c03" || strings.Join(p.BOnly, ",") != "c01" {
		t.Errorf("discordant = %v / %v", p.AOnly, p.BOnly)
	}
	if !strings.Contains(p.Verdict, "no detectable difference at 3 clusters") {
		t.Errorf("verdict = %q", p.Verdict)
	}
	if strings.Contains(p.Verdict, "10") && strings.Contains(p.Verdict, "point") && strings.Contains(p.Verdict, "threshold") {
		t.Errorf("verdict smells like a fixed threshold: %q", p.Verdict)
	}
}

func TestScoreRefusesVoidSweep(t *testing.T) {
	suite := filepath.Join("testdata", "score", "suite")
	err := runScore(suite, []sweepInput{
		{Label: "void", Dir: filepath.Join("testdata", "score", "jobs", "void")},
		{Label: "opus", Dir: filepath.Join("testdata", "score", "jobs", "model-b")},
	}, "", filepath.Join(t.TempDir(), "scorecard"))
	if err == nil || !strings.Contains(err.Error(), "refusing to score") {
		t.Fatalf("want void refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "void") {
		t.Errorf("refusal should name the voided sweep: %v", err)
	}
}

func TestScoreAmbiguousPrefixHardError(t *testing.T) {
	err := runScore(filepath.Join("testdata", "score", "suite-ambiguous"), []sweepInput{
		{Label: "m", Dir: filepath.Join("testdata", "score", "jobs", "ambiguous")},
	}, "", filepath.Join(t.TempDir(), "scorecard"))
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguity hard error, got %v", err)
	}
}

func TestScoreUnmatchedTrialHardError(t *testing.T) {
	err := runScore(filepath.Join("testdata", "score", "suite"), []sweepInput{
		{Label: "m", Dir: filepath.Join("testdata", "score", "jobs", "unmatched")},
	}, "", filepath.Join(t.TempDir(), "scorecard"))
	if err == nil || !strings.Contains(err.Error(), "matches no suite instance") {
		t.Fatalf("want unmatched hard error, got %v", err)
	}
}

// TestScoreTrialMetadataConsistency proves the loud failure when the
// prefix join disagrees with trial-level task metadata.
func TestScoreTrialMetadataConsistency(t *testing.T) {
	jobs := filepath.Join(t.TempDir(), "jobs")
	trial := "alpha-pr1-fix-date-parse-clamps-__t1"
	if err := os.MkdirAll(filepath.Join(jobs, "job-1", trial), 0o755); err != nil {
		t.Fatal(err)
	}
	jobJSON := `{"stats":{"evals":{"k":{"reward_stats":{"reward":{"1.0":["` + trial + `"]}}}}}}`
	if err := os.WriteFile(filepath.Join(jobs, "job-1", "result.json"), []byte(jobJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	trialJSON := `{"task_name":"prospect/beta-pr3-fix-router-panic-on-nil-route","trial_name":"` + trial + `"}`
	if err := os.WriteFile(filepath.Join(jobs, "job-1", trial, "result.json"), []byte(trialJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runScore(filepath.Join("testdata", "score", "suite"), []sweepInput{{Label: "m", Dir: jobs}},
		"", filepath.Join(t.TempDir(), "scorecard"))
	if err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("want consistency hard error, got %v", err)
	}
}

func TestSignTestP(t *testing.T) {
	cases := []struct {
		nPlus, nMinus int
		want          float64
	}{
		{0, 0, 1},
		{2, 1, 1},          // 2*(C(3,0)+C(3,1))/8 = 1.0
		{9, 1, 11.0 / 512}, // 2*(1+10)/1024
		{5, 5, 1},          // symmetric caps at 1
		{8, 0, 2.0 / 256},  // 2*C(8,0)/256
	}
	for _, c := range cases {
		if got := signTestP(c.nPlus, c.nMinus); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("signTestP(%d,%d) = %v, want %v", c.nPlus, c.nMinus, got, c.want)
		}
	}
}

func TestBootstrapDeterministicAndDegenerate(t *testing.T) {
	pairs := [][2]int{{1, 0}, {0, 1}, {1, 0}, {1, 1}, {0, 0}}
	lo1, hi1 := bootstrapCI(pairs, bootstrapResamples, bootstrapSeed)
	lo2, hi2 := bootstrapCI(pairs, bootstrapResamples, bootstrapSeed)
	if lo1 != lo2 || hi1 != hi2 {
		t.Error("bootstrap not deterministic under fixed seed")
	}
	if lo1 > hi1 {
		t.Errorf("CI inverted: [%v, %v]", lo1, hi1)
	}
	same := [][2]int{{1, 1}, {1, 1}, {0, 0}}
	lo, hi := bootstrapCI(same, bootstrapResamples, bootstrapSeed)
	if lo != 0 || hi != 0 {
		t.Errorf("degenerate pairs should give a zero-width CI at 0, got [%v, %v]", lo, hi)
	}
	if requiredClusters(same) != -1 {
		t.Error("zero variance must report the estimate as unavailable")
	}
}
