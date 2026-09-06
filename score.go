package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// score implements SCORE_PREREGISTRATION.md (frozen 2026-09-06) for any
// number of labeled Harbor job directories:
//   - an instance passes when at least 2 of its trials score reward 1.0
//   - a cluster passes when a majority of its scored member instances pass
//   - effective-N (cluster count) is the headline denominator
//   - rate-limited trials are excluded from denominators; two or more limit
//     hits void a sweep and the scorecard refuses to score
//   - inference is a sign test on paired clusters plus a bootstrap CI
//     (10,000 resamples) on the cluster pass-rate difference; verdicts
//     report detectability at the achieved N, never a fixed threshold

const (
	bootstrapResamples = 10000
	bootstrapSeed      = 1      // fixed seed: identical inputs give identical scorecards
	alphaZ             = 1.9600 // z at two-sided alpha 0.05
	powerZ             = 0.8416 // z at power 0.80
	targetGap          = 0.05   // the 5-point gap the pre-registration names
)

var limitMarkers = []string{"session limit", "429"}

type sweepInput struct{ Label, Dir string }

type scoreInstance struct {
	ID      string
	Cluster string
}

// loadSuiteInstances reads instance metadata (schema 2) under a tasks root.
func loadSuiteInstances(root string) ([]scoreInstance, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []scoreInstance
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := loadMeta(filepath.Join(root, e.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if m.InstanceID == "" {
			return nil, fmt.Errorf("%s: metadata has no instance_id", e.Name())
		}
		if m.Cluster == "" {
			return nil, fmt.Errorf("%s: no cluster assigned; run prospect dedup (or build) first", e.Name())
		}
		out = append(out, scoreInstance{ID: m.InstanceID, Cluster: m.Cluster})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no instances with metadata under %s", root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// joiner resolves Harbor trial IDs (task name truncated near 32 chars, then
// "__<suffix>") to suite instances by prefix against canonical instance
// IDs. Ambiguity and unmatched trials are hard errors, never guesses.
type joiner struct {
	ids   []string
	cache map[string]string
}

func newJoiner(instances []scoreInstance) *joiner {
	j := &joiner{cache: map[string]string{}}
	for _, in := range instances {
		j.ids = append(j.ids, in.ID)
	}
	return j
}

func (j *joiner) match(trialID string) (string, error) {
	prefix := trialID
	if i := strings.Index(trialID, "__"); i >= 0 {
		prefix = trialID[:i]
	}
	if id, ok := j.cache[prefix]; ok {
		return id, nil
	}
	var hits []string
	for _, id := range j.ids {
		if strings.HasPrefix(id, prefix) {
			hits = append(hits, id)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("trial %q matches no suite instance", trialID)
	case 1:
		j.cache[prefix] = hits[0]
		return hits[0], nil
	default:
		return "", fmt.Errorf("trial %q is ambiguous: prefix %q matches instances %s (shared truncation prefix)",
			trialID, prefix, strings.Join(hits, ", "))
	}
}

// tokenTotals accumulates transcript token usage for one model.
type tokenTotals struct {
	In, Out, CacheRead, CacheWrite int64
}

type sweepData struct {
	Label      string
	Dir        string
	Trials     map[string][]float64 // instance ID -> trial rewards
	TrialCount int
	Exceptions int
	LimitHits  int
	Voided     bool

	Durations map[string]float64 // instance ID -> summed trial seconds

	Tokens map[string]*tokenTotals // model -> totals

	InstPass    map[string]bool
	ClusterPass map[string]bool // cluster -> pass, only clusters with data
}

// loadSweep parses one Harbor jobs directory: job-level result.json for
// reward stats, trial-level result.json for timing and the trial-ID
// consistency check, exception files for the void audit, and agent
// transcripts for token usage.
func loadSweep(in sweepInput, j *joiner) (*sweepData, error) {
	s := &sweepData{
		Label:       in.Label,
		Dir:         in.Dir,
		Trials:      map[string][]float64{},
		Durations:   map[string]float64{},
		Tokens:      map[string]*tokenTotals{},
		InstPass:    map[string]bool{},
		ClusterPass: map[string]bool{},
	}
	jobResults, err := filepath.Glob(filepath.Join(in.Dir, "*", "result.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(jobResults)
	for _, rj := range jobResults {
		raw, err := os.ReadFile(rj)
		if err != nil {
			return nil, err
		}
		var r struct {
			Stats struct {
				Evals map[string]struct {
					RewardStats struct {
						Reward map[string][]string `json:"reward"`
					} `json:"reward_stats"`
				} `json:"evals"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", rj, err)
		}
		for _, ev := range r.Stats.Evals {
			for scoreStr, trialIDs := range ev.RewardStats.Reward {
				reward, err := strconv.ParseFloat(scoreStr, 64)
				if err != nil {
					return nil, fmt.Errorf("%s: bad reward key %q", rj, scoreStr)
				}
				for _, tid := range trialIDs {
					id, err := j.match(tid)
					if err != nil {
						return nil, fmt.Errorf("%s (%s): %w", in.Label, in.Dir, err)
					}
					s.Trials[id] = append(s.Trials[id], reward)
					s.TrialCount++
				}
			}
		}
	}

	// exception audit
	exceptions, err := filepath.Glob(filepath.Join(in.Dir, "*", "*", "exception.txt"))
	if err != nil {
		return nil, err
	}
	for _, ex := range exceptions {
		s.Exceptions++
		content, err := os.ReadFile(ex)
		if err != nil {
			continue
		}
		for _, marker := range limitMarkers {
			if strings.Contains(string(content), marker) {
				s.LimitHits++
				break
			}
		}
	}
	s.Voided = s.LimitHits >= 2

	// trial-level results: wall-clock plus the loud consistency check
	// between prefix-joined identity and the task metadata identity
	trialResults, err := filepath.Glob(filepath.Join(in.Dir, "*", "*", "result.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(trialResults)
	for _, tr := range trialResults {
		raw, err := os.ReadFile(tr)
		if err != nil {
			return nil, err
		}
		var r struct {
			TaskName   string `json:"task_name"`
			TrialName  string `json:"trial_name"`
			StartedAt  string `json:"started_at"`
			FinishedAt string `json:"finished_at"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			continue // not a trial result
		}
		if r.TrialName == "" {
			continue
		}
		id, err := j.match(r.TrialName)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", in.Label, in.Dir, err)
		}
		if r.TaskName != "" {
			want := r.TaskName
			if i := strings.LastIndex(want, "/"); i >= 0 {
				want = want[i+1:]
			}
			if want != id {
				return nil, fmt.Errorf("%s: trial %q prefix-joins to %q but task metadata says %q; trial-ID join is inconsistent",
					in.Label, r.TrialName, id, want)
			}
		}
		if start, err1 := time.Parse(time.RFC3339, r.StartedAt); err1 == nil {
			if finish, err2 := time.Parse(time.RFC3339, r.FinishedAt); err2 == nil && finish.After(start) {
				s.Durations[id] += finish.Sub(start).Seconds()
			}
		}
	}

	// transcript token usage (ported from cost-report.py, with model-name
	// detection generalized to message.model when no system line exists)
	err = filepath.WalkDir(in.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".txt" || filepath.Base(filepath.Dir(path)) != "agent" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		model := ""
		for _, line := range strings.Split(string(raw), "\n") {
			var entry struct {
				Type    string `json:"type"`
				Model   string `json:"model"`
				Message struct {
					Model string `json:"model"`
					Usage struct {
						InputTokens        int64 `json:"input_tokens"`
						OutputTokens       int64 `json:"output_tokens"`
						CacheReadInput     int64 `json:"cache_read_input_tokens"`
						CacheCreationInput int64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			if entry.Type == "system" && entry.Model != "" {
				model = strings.SplitN(entry.Model, "[", 2)[0]
			}
			if model == "" && entry.Message.Model != "" {
				model = strings.SplitN(entry.Message.Model, "[", 2)[0]
			}
			u := entry.Message.Usage
			if u.InputTokens+u.OutputTokens+u.CacheReadInput+u.CacheCreationInput == 0 {
				continue
			}
			key := model
			if key == "" {
				key = "unknown"
			}
			t := s.Tokens[key]
			if t == nil {
				t = &tokenTotals{}
				s.Tokens[key] = t
			}
			t.In += u.InputTokens
			t.Out += u.OutputTokens
			t.CacheRead += u.CacheReadInput
			t.CacheWrite += u.CacheCreationInput
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// signTestP is the exact two-sided sign test over discordant pairs.
func signTestP(nPlus, nMinus int) float64 {
	n := nPlus + nMinus
	if n == 0 {
		return 1
	}
	k := nPlus
	if nMinus < k {
		k = nMinus
	}
	var cum float64
	for i := 0; i <= k; i++ {
		cum += binomCoeff(n, i)
	}
	p := 2 * cum / math.Pow(2, float64(n))
	if p > 1 {
		return 1
	}
	return p
}

func binomCoeff(n, k int) float64 {
	r := 1.0
	for i := 1; i <= k; i++ {
		r = r * float64(n-k+i) / float64(i)
	}
	return r
}

// bootstrapCI resamples paired cluster outcomes and returns the 2.5th and
// 97.5th percentiles of the pass-rate difference (a minus b).
func bootstrapCI(pairs [][2]int, resamples int, seed int64) (lo, hi float64) {
	rng := rand.New(rand.NewSource(seed))
	n := len(pairs)
	diffs := make([]float64, resamples)
	for r := range diffs {
		var sa, sb int
		for i := 0; i < n; i++ {
			p := pairs[rng.Intn(n)]
			sa += p[0]
			sb += p[1]
		}
		diffs[r] = float64(sa-sb) / float64(n)
	}
	sort.Float64s(diffs)
	return diffs[int(0.025*float64(resamples))], diffs[int(0.975*float64(resamples))-1]
}

// requiredClusters estimates the paired-comparison size that resolves
// targetGap at the observed per-cluster variance (normal approximation,
// two-sided alpha 0.05, power 0.80). Returns -1 when variance is zero.
func requiredClusters(pairs [][2]int) int {
	n := len(pairs)
	if n < 2 {
		return -1
	}
	var sum float64
	for _, p := range pairs {
		sum += float64(p[0] - p[1])
	}
	mean := sum / float64(n)
	var ss float64
	for _, p := range pairs {
		d := float64(p[0]-p[1]) - mean
		ss += d * d
	}
	variance := ss / float64(n-1)
	if variance == 0 {
		return -1
	}
	return int(math.Ceil(variance * math.Pow(alphaZ+powerZ, 2) / (targetGap * targetGap)))
}

type pairStats struct {
	A              string   `json:"a"`
	B              string   `json:"b"`
	PairedClusters int      `json:"paired_clusters"`
	DiffPoints     float64  `json:"diff_points"`
	SignTestP      float64  `json:"sign_test_p"`
	CILo           float64  `json:"ci_lo_points"`
	CIHi           float64  `json:"ci_hi_points"`
	AOnly          []string `json:"a_only_clusters"`
	BOnly          []string `json:"b_only_clusters"`
	Verdict        string   `json:"verdict"`
}

type sweepSummary struct {
	Label            string  `json:"label"`
	JobsDir          string  `json:"jobs_dir"`
	Trials           int     `json:"trials"`
	Exceptions       int     `json:"exceptions"`
	LimitHits        int     `json:"limit_hits"`
	InstancesScored  int     `json:"instances_scored"`
	InstancesPassed  int     `json:"instances_passed"`
	ClustersScored   int     `json:"clusters_scored"`
	ClustersPassed   int     `json:"clusters_passed"`
	CostUSD          float64 `json:"cost_usd"`
	CostKnown        bool    `json:"cost_known"`
	CostPerSolved    float64 `json:"cost_per_solved_cluster_usd"`
	MedianWallSolved float64 `json:"median_wallclock_per_solved_cluster_sec"`
	HasWallclock     bool    `json:"has_wallclock"`
}

// runScore drives the whole scorecard: suite join, void audit, cluster
// scoring, paired inference, cost, and the md/json artifacts.
func runScore(suiteRoot string, sweepArgs []sweepInput, pricesPath, outPrefix string) error {
	instances, err := loadSuiteInstances(suiteRoot)
	if err != nil {
		return err
	}
	clusters := map[string][]string{}
	for _, in := range instances {
		clusters[in.Cluster] = append(clusters[in.Cluster], in.ID)
	}
	clusterIDs := sortedKeysOf(clusters)
	j := newJoiner(instances)

	var prices map[string]map[string]float64
	if pricesPath != "" {
		raw, err := os.ReadFile(pricesPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &prices); err != nil {
			return fmt.Errorf("%s: %w", pricesPath, err)
		}
	}

	var sweeps []*sweepData
	for _, sa := range sweepArgs {
		s, err := loadSweep(sa, j)
		if err != nil {
			return err
		}
		sweeps = append(sweeps, s)
	}

	// void audit precedes all scoring: a voided sweep is never scored
	var voided []string
	for _, s := range sweeps {
		fmt.Printf("%s: %s\n  trials: %d | exceptions: %d | limit hits: %d%s\n",
			s.Label, s.Dir, s.TrialCount, s.Exceptions, s.LimitHits, voidSuffix(s.Voided))
		if s.Voided {
			voided = append(voided, s.Label)
		}
	}
	if len(voided) > 0 {
		return fmt.Errorf("refusing to score: sweep(s) voided by rate limits (%s); re-run after reset", strings.Join(voided, ", "))
	}

	// per-sweep pass computation
	var summaries []sweepSummary
	for _, s := range sweeps {
		for id, rewards := range s.Trials {
			var sum float64
			for _, r := range rewards {
				sum += r
			}
			s.InstPass[id] = sum >= 2
		}
		for _, c := range clusterIDs {
			withData, passed := 0, 0
			for _, id := range clusters[c] {
				if _, ok := s.Trials[id]; ok {
					withData++
					if s.InstPass[id] {
						passed++
					}
				}
			}
			if withData > 0 {
				s.ClusterPass[c] = passed*2 > withData
			}
		}
		summaries = append(summaries, summarize(s, clusters, prices))
	}

	// pairwise inference over clusters scored in both sweeps
	var pairs []pairStats
	for i := 0; i < len(sweeps); i++ {
		for k := i + 1; k < len(sweeps); k++ {
			pairs = append(pairs, comparePair(sweeps[i], sweeps[k], clusterIDs))
		}
	}

	md := renderScorecard(suiteRoot, len(instances), clusterIDs, clusters, sweeps, summaries, pairs)
	fmt.Print("\n" + md)
	if err := os.WriteFile(outPrefix+".md", []byte(md), 0o644); err != nil {
		return err
	}
	report := map[string]any{
		"suite":        suiteRoot,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"instances":    len(instances),
		"clusters":     len(clusterIDs),
		"sweeps":       summaries,
		"pairs":        pairs,
	}
	rj, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPrefix+".json", rj, 0o644); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s.md and %s.json\n", outPrefix, outPrefix)
	return nil
}

func voidSuffix(voided bool) string {
	if voided {
		return "  => SWEEP VOID"
	}
	return ""
}

func summarize(s *sweepData, clusters map[string][]string, prices map[string]map[string]float64) sweepSummary {
	sum := sweepSummary{
		Label: s.Label, JobsDir: s.Dir,
		Trials: s.TrialCount, Exceptions: s.Exceptions, LimitHits: s.LimitHits,
	}
	for _, pass := range s.InstPass {
		sum.InstancesScored++
		if pass {
			sum.InstancesPassed++
		}
	}
	for _, pass := range s.ClusterPass {
		sum.ClustersScored++
		if pass {
			sum.ClustersPassed++
		}
	}

	// cost: only quotable when every observed model has a price
	sum.CostKnown = len(s.Tokens) > 0 && prices != nil
	for model, t := range s.Tokens {
		p, ok := prices[model]
		if !ok {
			sum.CostKnown = false
			break
		}
		sum.CostUSD += (float64(t.In)*p["in"] + float64(t.Out)*p["out"] +
			float64(t.CacheRead)*p["cache_read"] + float64(t.CacheWrite)*p["cache_write"]) / 1e6
	}
	if !sum.CostKnown {
		sum.CostUSD = 0
	}
	if sum.CostKnown && sum.ClustersPassed > 0 {
		sum.CostPerSolved = sum.CostUSD / float64(sum.ClustersPassed)
	}

	// median wall-clock across solved clusters (sum of member trial time)
	var solvedDurations []float64
	for c, pass := range s.ClusterPass {
		if !pass {
			continue
		}
		var total float64
		for _, id := range clusters[c] {
			total += s.Durations[id]
		}
		if total > 0 {
			solvedDurations = append(solvedDurations, total)
		}
	}
	if len(solvedDurations) > 0 {
		sort.Float64s(solvedDurations)
		n := len(solvedDurations)
		if n%2 == 1 {
			sum.MedianWallSolved = solvedDurations[n/2]
		} else {
			sum.MedianWallSolved = (solvedDurations[n/2-1] + solvedDurations[n/2]) / 2
		}
		sum.HasWallclock = true
	}
	return sum
}

func comparePair(a, b *sweepData, clusterIDs []string) pairStats {
	ps := pairStats{A: a.Label, B: b.Label}
	var paired [][2]int
	for _, c := range clusterIDs {
		ap, aok := a.ClusterPass[c]
		bp, bok := b.ClusterPass[c]
		if !aok || !bok {
			continue
		}
		pair := [2]int{boolInt(ap), boolInt(bp)}
		paired = append(paired, pair)
		if ap && !bp {
			ps.AOnly = append(ps.AOnly, c)
		}
		if bp && !ap {
			ps.BOnly = append(ps.BOnly, c)
		}
	}
	ps.PairedClusters = len(paired)
	if len(paired) == 0 {
		ps.Verdict = "no clusters scored in both sweeps"
		ps.SignTestP = 1
		return ps
	}
	var sa, sb int
	for _, p := range paired {
		sa += p[0]
		sb += p[1]
	}
	n := len(paired)
	ps.DiffPoints = 100 * float64(sa-sb) / float64(n)
	ps.SignTestP = signTestP(len(ps.AOnly), len(ps.BOnly))
	lo, hi := bootstrapCI(paired, bootstrapResamples, bootstrapSeed)
	ps.CILo, ps.CIHi = 100*lo, 100*hi

	ci := fmt.Sprintf("CI [%+.0f, %+.0f] points", ps.CILo, ps.CIHi)
	if ps.SignTestP < 0.05 {
		ps.Verdict = fmt.Sprintf("difference of %+.0f points at %d clusters (p = %.3f, %s)",
			ps.DiffPoints, n, ps.SignTestP, ci)
		return ps
	}
	switch req := requiredClusters(paired); {
	case req < 0:
		ps.Verdict = fmt.Sprintf("no detectable difference at %d clusters (p = %.3f, %s); additional-cluster estimate unavailable (zero observed variance)",
			n, ps.SignTestP, ci)
	case req > n:
		ps.Verdict = fmt.Sprintf("no detectable difference at %d clusters (p = %.3f, %s); ≈%d more clusters would resolve a 5-point gap at the observed variance",
			n, ps.SignTestP, ci, req-n)
	default:
		ps.Verdict = fmt.Sprintf("no detectable difference at %d clusters (p = %.3f, %s); the observed variance already suffices for a 5-point gap",
			n, ps.SignTestP, ci)
	}
	return ps
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sortedKeysOf(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func renderScorecard(root string, nInstances int, clusterIDs []string, clusters map[string][]string, sweeps []*sweepData, summaries []sweepSummary, pairs []pairStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# prospect scorecard\n\nSuite: %s (%d instances, %d clusters; effective-N = %d)\n\n", root, nInstances, len(clusterIDs), len(clusterIDs))

	b.WriteString("| sweep | trials | exceptions | limit hits | instances | clusters (effective-N) | cost (USD) | cost/solved cluster | median wall-clock/solved cluster |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range summaries {
		cost, perSolved, wall := "n/a", "n/a", "n/a"
		if s.CostKnown {
			cost = fmt.Sprintf("%.4f", s.CostUSD)
			if s.ClustersPassed > 0 {
				perSolved = fmt.Sprintf("%.4f", s.CostPerSolved)
			}
		}
		if s.HasWallclock {
			wall = fmt.Sprintf("%.0fs", s.MedianWallSolved)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d/%d | %d/%d | %s | %s | %s |\n",
			s.Label, s.Trials, s.Exceptions, s.LimitHits,
			s.InstancesPassed, s.InstancesScored,
			s.ClustersPassed, s.ClustersScored,
			cost, perSolved, wall)
	}

	b.WriteString("\n## Clusters\n\n| cluster | instances |")
	for _, s := range sweeps {
		fmt.Fprintf(&b, " %s |", s.Label)
	}
	b.WriteString("\n|---|---|")
	for range sweeps {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, c := range clusterIDs {
		fmt.Fprintf(&b, "| %s | %d |", c, len(clusters[c]))
		for _, s := range sweeps {
			fmt.Fprintf(&b, " %s |", passMark(s.ClusterPass, c))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Instances\n\n| instance |")
	for _, s := range sweeps {
		fmt.Fprintf(&b, " %s |", s.Label)
	}
	b.WriteString("\n|---|")
	for range sweeps {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, c := range clusterIDs {
		for _, id := range clusters[c] {
			fmt.Fprintf(&b, "| %s |", id)
			for _, s := range sweeps {
				fmt.Fprintf(&b, " %s |", passMark(s.InstPass, id))
			}
			b.WriteString("\n")
		}
	}

	if len(pairs) > 0 {
		b.WriteString("\n## Comparisons\n\n")
		for _, p := range pairs {
			fmt.Fprintf(&b, "- **%s vs %s** (%d paired clusters): %s\n", p.A, p.B, p.PairedClusters, p.Verdict)
			if len(p.AOnly) > 0 {
				fmt.Fprintf(&b, "  - %s only: %s\n", p.A, strings.Join(p.AOnly, ", "))
			}
			if len(p.BOnly) > 0 {
				fmt.Fprintf(&b, "  - %s only: %s\n", p.B, strings.Join(p.BOnly, ", "))
			}
		}
	}
	return b.String()
}

func passMark(m map[string]bool, key string) string {
	pass, ok := m[key]
	if !ok {
		return "-"
	}
	if pass {
		return "✓"
	}
	return "✗"
}
