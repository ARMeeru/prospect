package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: prospect mine <repo> [flags] | prospect reverify <tasks-root>... [--jobs N]")
		os.Exit(2)
	}
	if os.Args[1] == "reverify" {
		fs := flag.NewFlagSet("reverify", flag.ExitOnError)
		jobs := fs.Int("jobs", 2, "concurrent builds/trials")
		var publics kvList
		fs.Var(&publics, "public", "root whose instances have public origin (repeatable)")
		// hoist flags ahead of positional args (flag.Parse stops at the
		// first non-flag argument); every reverify flag takes a value.
		var pos []string
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			if strings.HasPrefix(args[i], "-") && i+1 < len(args) {
				fs.Parse([]string{args[i], args[i+1]})
				i++
			} else {
				pos = append(pos, args[i])
			}
		}
		fs.Parse(pos)
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "error: at least one tasks root required")
			os.Exit(2)
		}
		pub := map[string]bool{}
		for _, p := range publics {
			pub[filepath.Clean(p)] = true
		}
		if err := runReverify(fs.Args(), *jobs, pub); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] == "dedup" {
		fs := flag.NewFlagSet("dedup", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: prospect dedup <tasks-root>")
			os.Exit(2)
		}
		if err := runDedup(fs.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] == "deleak" {
		fs := flag.NewFlagSet("deleak", flag.ExitOnError)
		repo := fs.String("repo", "", "source repo path (for symbol extraction)")
		repoName := fs.String("name", "", "repo display name for instructions")
		var pos []string
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			if strings.HasPrefix(args[i], "-") && i+1 < len(args) {
				fs.Parse([]string{args[i], args[i+1]})
				i++
			} else {
				pos = append(pos, args[i])
			}
		}
		fs.Parse(pos)
		if fs.NArg() < 1 || *repo == "" {
			fmt.Fprintln(os.Stderr, "usage: prospect deleak <tasks-root> --repo <repo-path> [--name <display-name>]")
			os.Exit(2)
		}
		if *repoName == "" {
			*repoName = filepath.Base(*repo)
		}
		if err := runDeleak(fs.Arg(0), *repo, *repoName); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] == "issues" {
		fs := flag.NewFlagSet("issues", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: prospect issues <tasks-root>")
			os.Exit(2)
		}
		if err := runIssues(fs.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] == "audit" {
		fs := flag.NewFlagSet("audit", flag.ExitOnError)
		origin := fs.String("origin", "", "origin substring that must never appear in agent transcripts (e.g. repo slug)")
		var pos []string
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			if strings.HasPrefix(args[i], "-") && i+1 < len(args) {
				fs.Parse([]string{args[i], args[i+1]})
				i++
			} else {
				pos = append(pos, args[i])
			}
		}
		fs.Parse(pos)
		if fs.NArg() < 1 || *origin == "" {
			fmt.Fprintln(os.Stderr, "usage: prospect audit <jobsdir> --origin <repo-slug>")
			os.Exit(2)
		}
		if err := runAudit(fs.Arg(0), *origin); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] != "mine" {
		fmt.Fprintln(os.Stderr, "usage: prospect mine <repo> [flags] | prospect reverify <tasks-root>... [--jobs N]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("mine", flag.ExitOnError)
	since := fs.String("since", "", "only commits after this date (YYYY-MM-DD)")
	limit := fs.Int("limit", 0, "max test-touching commits to examine (0 = all)")
	out := fs.String("out", "mined", "output directory for Harbor tasks")
	emitUnverified := fs.Bool("emit-unverified", false, "also emit tasks that failed verification")
	probeOnly := fs.Bool("probe-only", false, "discovery + compile probe only (funnel survey)")
	var envs kvList
	fs.Var(&envs, "env", "test env var KEY=VALUE (repeatable)")
	// flag.Parse stops at the first non-flag argument, so hoist all flags
	// in front of positional args before parsing.
	var positional []string
	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "--" {
			if !strings.Contains(a, "=") && a != "-emit-unverified" && a != "--emit-unverified" && a != "-probe-only" && a != "--probe-only" && i+1 < len(args) {
				i++
				fs.Parse([]string{a, args[i]})
			} else {
				fs.Parse([]string{a})
			}
			continue
		}
		positional = append(positional, a)
	}
	fs.Parse(positional)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: repo path required")
		os.Exit(2)
	}
	cfg := mineConfig{
		Repo: fs.Arg(0), Since: *since, Limit: *limit, Out: *out,
		Env: map[string]string{}, EmitUnverified: *emitUnverified, ProbeOnly: *probeOnly,
	}
	for _, kv := range envs {
		i := strings.Index(kv, "=")
		if i > 0 {
			cfg.Env[kv[:i]] = kv[i+1:]
		}
	}
	if err := mine(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type kvList []string

func (k *kvList) String() string { return strings.Join(*k, ",") }
func (k *kvList) Set(v string) error {
	*k = append(*k, v)
	return nil
}
