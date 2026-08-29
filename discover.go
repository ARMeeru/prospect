package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type kind string

const (
	// fixPair: a fix commit that changed tests; base = parent, fix = the commit.
	fixPair kind = "fix"
	// tdd: a test-only commit written against a not-yet-existing fix;
	// base = the commit itself (tests fail), fix = next fix touching its packages.
	tdd kind = "tdd"
)

type candidate struct {
	Kind         kind
	BaseSHA      string   // where the agent starts; reference tests must FAIL here
	FixSHA       string   // commit whose state must PASS
	Subject      string
	PRNumber     int
	TestFiles    []string
	Pkgs         []string
	TestNames    []string
	VerifierLint []string // test files flagged for message-text assertions
	// status fields
	compiles   bool
	beforePass bool // behavioral result on base (want false)
	afterPass  bool // behavioral result on fix (want true)
	skipReason string
}

// messageAssertionRe flags reference tests that assert on error/message TEXT
// (the confirmed over-specific class: a semantically-correct fix worded
// differently gets rejected). Nomination only — recorded, not rejected.
var messageAssertionRe = regexp.MustCompile(`(?i)(ErrorContains\(|EqualError\(|NotContains\([^)]{0,160}\.Error\(\)|Contains\([^)]{0,160}\.Error\(\))`)

// lintMessageAssertions returns the test files containing message-text assertions.
func lintMessageAssertions(repo string, c candidate) []string {
	sha := c.FixSHA
	if c.Kind == tdd {
		sha = c.BaseSHA
	}
	var flagged []string
	for _, f := range c.TestFiles {
		out, err := git(repo, "show", sha+":"+f)
		if err != nil {
			continue
		}
		if messageAssertionRe.MatchString(out) {
			flagged = append(flagged, filepath.Base(f))
		}
	}
	return flagged
}

var prRe = regexp.MustCompile(`#(\d+)`)
var subjectTest = regexp.MustCompile(`^test(\(|:| )`)

func uniqueDirs(files []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range files {
		d := dirOf(f)
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// discover classifies test-touching commits into mineable candidates.
func discover(repo, since string, limit int) ([]candidate, error) {
	commits, err := testTouchingCommits(repo, since, limit)
	if err != nil {
		return nil, err
	}
	var cands []candidate
	for _, c := range commits {
		files, err := changedFiles(repo, c.sha)
		if err != nil {
			continue
		}
		var testFiles []string
		var srcFiles []string
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				testFiles = append(testFiles, f)
			} else {
				srcFiles = append(srcFiles, f)
			}
		}
		if len(testFiles) == 0 {
			continue
		}
		names := addedTestFuncs(repo, c.sha, testFiles)
		if len(names) == 0 {
			continue
		}
		// Invariant: single-parent, non-merge subject. --no-merges missed at
		// least one "Merge commit from fork" in the wild.
		if parents, err := git(repo, "show", "-s", "--format=%P", c.sha); err != nil || len(strings.Fields(parents)) != 1 || strings.HasPrefix(c.subject, "Merge") {
			cd := candidate{Subject: c.subject, TestFiles: testFiles, Pkgs: uniqueDirs(testFiles), TestNames: names}
			cd.skipReason = "merge/invalid topology"
			cands = append(cands, cd)
			continue
		}
		cd := candidate{
			Subject:   c.subject,
			TestFiles: testFiles,
			Pkgs:      uniqueDirs(testFiles),
			TestNames: names,
		}
		if m := prRe.FindStringSubmatch(c.subject); m != nil {
			cd.PRNumber = atoi(m[1])
		}
		switch {
		case subjectTest.MatchString(c.subject) && len(srcFiles) == 0:
			fix, err := findNextFix(repo, c.sha, cd.Pkgs)
			if err != nil || fix == "" {
				cd.skipReason = "no paired fix found"
				cands = append(cands, cd)
				continue
			}
			cd.Kind = tdd
			cd.BaseSHA = c.sha
			cd.FixSHA = fix
			cd.Subject = c.subject + " [tdd→" + short(fix) + "]"
		case len(srcFiles) > 0:
			// Any commit touching both tests and source is a fix-pair
			// candidate regardless of subject style; the compile probe and
			// behavioral checks are the real filters. (SPIRE writes "Fix …",
			// "datastore: …" — prefixes are not a reliable signal.)
			parent, err := parentOf(repo, c.sha)
			if err != nil {
				cd.skipReason = "no parent"
				cands = append(cands, cd)
				continue
			}
			cd.Kind = fixPair
			cd.BaseSHA = parent
			cd.FixSHA = c.sha
		default:
			cd.skipReason = "test-only commit without test-prefix"
			cands = append(cands, cd)
			continue
		}
		cands = append(cands, cd)
	}
	return cands, nil
}

// findNextFix returns the first commit after base (chronological) that changes
// non-test files under any of pkgs. Empty string if none within the branch.
func findNextFix(repo, base string, pkgs []string) (string, error) {
	out, err := git(repo, "log", base+"..HEAD", "--no-merges", "--reverse", "--format=%H", "--")
	if err != nil {
		return "", err
	}
	for _, sha := range strings.Fields(out) {
		files, err := changedFiles(repo, sha)
		if err != nil {
			continue
		}
		pkgSet := map[string]bool{}
		for _, p := range pkgs {
			pkgSet[p] = true
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			if pkgSet[dirOf(f)] {
				return sha, nil
			}
		}
	}
	return "", nil
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func (c candidate) id() string {
	slug := regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(strings.ToLower(c.Subject), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return fmt.Sprintf("%s-pr%d-%s", c.Kind, c.PRNumber, slug)
}
