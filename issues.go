package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func execGH(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %v: %s", strings.Join(args, " "), err, firstLines(out.String(), 2))
	}
	return []byte(out.String()), nil
}

// reinstruct replaces commit-body instructions with the linked issue's
// symptom text where one exists (issues describe symptoms; commits describe
// cures), and tags instruction_class accordingly:
//   bug-report   — instruction from a linked issue
//   change-spec  — instruction remains the fix commit's own narration

var fixesRe = regexp.MustCompile(`(?im)^\s*(?:fixes|fix|closes|close|resolves|resolve)\b[^#\n]*#(\d+)`)

var ghSlugRe = regexp.MustCompile(`github\.com[/:]([^/]+/[^/]+?)(?:\.git)?/`)

type issueInfo struct{ title, body string }

func fetchIssue(slug string, num int) (issueInfo, error) {
	out, err := ghJSON("repos/" + slug + "/issues/" + fmt.Sprint(num))
	if err != nil {
		return issueInfo{}, err
	}
	var r struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return issueInfo{}, err
	}
	return issueInfo{title: r.Title, body: r.Body}, nil
}

func ghJSON(path string) ([]byte, error) {
	return execGH("api", path)
}

// fetchPRBody returns a PR's body text.
func fetchPRBody(slug string, num int) (string, error) {
	out, err := ghJSON("repos/" + slug + "/pulls/" + fmt.Sprint(num))
	if err != nil {
		return "", err
	}
	var r struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	return r.Body, nil
}

func trimBody(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func runIssues(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	classCounts := map[string]int{}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !e.IsDir() {
			continue
		}
		tomlRaw, err := os.ReadFile(filepath.Join(dir, "task.toml"))
		if err != nil {
			continue
		}
		meta, err := loadMeta(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err // schema-1 metadata: run prospect migrate first
		}
		src := regexp.MustCompile(`source = "([^"]+)"`).FindStringSubmatch(string(tomlRaw))
		setClass := func(cls string) error {
			classCounts[cls]++
			meta.InstructionClass = cls
			meta.StagesRun = addStage(meta.StagesRun, "issues")
			return saveMeta(dir, meta)
		}
		if src == nil || meta.PR == 0 {
			classCounts["change-spec"]++
			fmt.Printf("change-spec | %s (no source/pr)\n", e.Name())
			continue
		}
		sm := ghSlugRe.FindStringSubmatch(src[1] + "/")
		if sm == nil {
			classCounts["change-spec"]++
			fmt.Printf("change-spec | %s (non-github source)\n", e.Name())
			continue
		}
		slug := sm[1]

		cls := "change-spec"
		newInstr := ""
		if body, err := fetchPRBody(slug, meta.PR); err == nil {
			if m := fixesRe.FindStringSubmatch(body); m != nil {
				num := atoi(m[1])
				if iss, err := fetchIssue(slug, num); err == nil && len(strings.TrimSpace(iss.body)) > 40 {
					newInstr = fmt.Sprintf("# %s\n\nRepository: `%s` (checked out at a fixed commit in `/app`)\n\n%s\n\nYour work will be validated by hidden verification tests. Do not add tests.\n",
						iss.title, repoDisplayName(root), trimBody(iss.body, 1500))
					cls = "bug-report"
					fmt.Printf("bug-report   | %s (issue #%d)\n", e.Name(), num)
				}
			}
		}
		if cls == "change-spec" {
			fmt.Printf("change-spec | %s\n", e.Name())
		}

		if newInstr != "" {
			os.WriteFile(filepath.Join(dir, "instruction.md"), []byte(newInstr), 0o644)
		}
		if err := setClass(cls); err != nil {
			return err
		}
	}
	recordStage(root, "issues", "ok", "", classCounts)
	return nil
}

// repoDisplayName recovers the repo display name from the suite manifest,
// falling back to the root dir name. (The old sibling-meta keyword scan
// never fired: meta.json has no keywords field.)
func repoDisplayName(root string) string {
	if m, err := loadManifest(root); err == nil && m != nil && m.RepoName != "" {
		return m.RepoName
	}
	return filepath.Base(root)
}
