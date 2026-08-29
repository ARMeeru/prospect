package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// deleak post-processes emitted task dirs per the Phase-1 review:
//   - strip test-authoring directives from instructions (they collide with
//     overlaid reference tests — the sqlmock redeclaration failure)
//   - extract symbols changed by the fix; if the instruction mentions one,
//     flag it for hand classification (the flag NOMINATES, it does not
//     classify — mechanism language vs behavior language is a human call
//     at n≈30)
//   - write instruction_class into meta.json and task.toml [metadata]

// sentence-level so a directive following a normal sentence still strips
var directiveRe = regexp.MustCompile(`(?i)[^.!?]*\b(add|write|include|cover|extend|improve)\b[^.!?]*\b(test|tests|coverage|sqlmock|mock|suite)\b[^.!?]*[.!?]\s*`)

// mechanism verbs: imperative change-language aimed at internals. Weak
// signal on purpose — nomination only, humans classify.
var mechanismVerbRe = regexp.MustCompile(`(?i)\b(exclude|bind|suppress|strip|redact|gate|derive|serialize|inline|reorder|persist|clamp|anchor|whitelist|denylist|harden|wire|re-?encode|deduplicate|throttle)\b`)

// stripDirectives removes test-authoring sentences from a commit body.
func stripDirectives(body string) string {
	return strings.TrimSpace(directiveRe.ReplaceAllString(body, ""))
}

// changedSymbols extracts Go function/method names appearing in the fix
// commit's non-test diff hunk headers.
func changedSymbols(repo, fixSha string, srcFiles []string) []string {
	seen := map[string]bool{}
	hunkRe := regexp.MustCompile(`^@@ .* @@ ?(.*)$`)
	symRe := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\(`)
	for _, f := range srcFiles {
		out, err := git(repo, "show", fixSha, "--", f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "@@ ") {
				continue
			}
			if m := hunkRe.FindStringSubmatch(line); m != nil {
				for _, s := range symRe.FindAllStringSubmatch(m[1], -1) {
					name := strings.TrimSuffix(s[1], "(")
					if len(name) > 3 && !seen[name] {
						seen[name] = true
					}
				}
			}
		}
	}
	var syms []string
	for s := range seen {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	return syms
}

func runDeleak(root, repo, repoName string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	fmt.Printf("%-60s %-8s %s\n", "task", "flagged", "symbols-in-instruction")
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !e.IsDir() {
			continue
		}
		metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Base      string   `json:"base_sha"`
			Fix       string   `json:"fix_sha"`
			Subject   string   `json:"subject"`
			TestFiles []string `json:"test_files"`
		}
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			continue
		}

		// all non-test files the fix changed
		files, _ := changedFiles(repo, meta.Fix)
		var src []string
		for _, f := range files {
			if !strings.HasSuffix(f, "_test.go") {
				src = append(src, f)
			}
		}
		symbols := changedSymbols(repo, meta.Fix, src)

		// rewrite instruction.md without directives
		instrPath := filepath.Join(dir, "instruction.md")
		instr, _ := os.ReadFile(instrPath)
		instrText := string(instr)
		if i := strings.Index(instrText, "\n\n"); i >= 0 {
			head := instrText[:i+1] // "# subject\n"
			rest := instrText[i+1:]
			// body sits between the Repository line and the boilerplate line
			lines := strings.Split(rest, "\n")
			var bodyLines []string
			for _, l := range lines {
				if strings.HasPrefix(l, "Repository:") || strings.HasPrefix(l, "Implement the change") ||
					strings.HasPrefix(l, "Your work will be validated") {
					continue
				}
				bodyLines = append(bodyLines, l)
			}
			body := stripDirectives(strings.TrimSpace(strings.Join(bodyLines, "\n")))
			newInstr := fmt.Sprintf("%s\nRepository: `%s` (checked out at a fixed commit in `/app`)\n\n%s\n\nYour work will be validated by hidden verification tests. Do not add tests.\n",
				head, repoName, body)
			if err := os.WriteFile(instrPath, []byte(newInstr), 0o644); err != nil {
				return err
			}
			instrText = newInstr
		}

		// nomination flag: symbol mention OR mechanism verb
		lower := strings.ToLower(instrText)
		var hits []string
		for _, s := range symbols {
			if strings.Contains(lower, strings.ToLower(s)) {
				hits = append(hits, s)
			}
		}
		verbs := mechanismVerbRe.FindAllString(instrText, -1)
		verbHit := len(verbs) > 0

		// update meta.json
		var mm map[string]any
		json.Unmarshal(metaRaw, &mm)
		mm["instruction_class"] = "unclassified"
		mm["leak_flag"] = len(hits) > 0 || verbHit
		mm["leak_symbols"] = hits
		mm["leak_verbs"] = verbs
		mm["changed_symbols"] = symbols
		mj, _ := json.MarshalIndent(mm, "", "  ")
		os.WriteFile(filepath.Join(dir, "meta.json"), mj, 0o644)

		// task.toml [metadata]
		tomlPath := filepath.Join(dir, "task.toml")
		toml, _ := os.ReadFile(tomlPath)
		if !strings.Contains(string(toml), "instruction_class") {
			t := strings.Replace(string(toml), "verified_host_only = true", "verified_host_only = true\ninstruction_class = \"unclassified\"", 1)
			os.WriteFile(tomlPath, []byte(t), 0o644)
		}

		flag := "-"
		reason := ""
		if len(hits) > 0 {
			flag = "FLAG"
			reason = "symbols: " + strings.Join(hits, ",")
		}
		if verbHit {
			flag = "FLAG"
			if reason != "" {
				reason += "; "
			}
			reason += "verbs: " + strings.Join(lowercaseUnique(verbs), ",")
		}
		fmt.Printf("%-60s %-8s %s\n", e.Name(), flag, reason)
	}
	fmt.Println("\nclassify each FLAG row by hand: change-spec (names the change) vs bug-report (names the behavior); set instruction_class in meta.json + task.toml accordingly.")
	return nil
}

func lowercaseUnique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		l := strings.ToLower(x)
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}
