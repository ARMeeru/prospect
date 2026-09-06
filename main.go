package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usageLine = "usage: prospect <mine|reverify|deleak|issues|dedup|audit|migrate> ..."

// commands maps subcommand names to their entry points. Usage errors print
// and exit 2 inside the command; runtime errors return and exit 1 in main.
var commands = map[string]func(args []string) error{
	"mine":     cmdMine,
	"reverify": cmdReverify,
	"deleak":   cmdDeleak,
	"issues":   cmdIssues,
	"dedup":    cmdDedup,
	"audit":    cmdAudit,
	"migrate":  cmdMigrate,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usageLine)
		os.Exit(2)
	}
	cmd, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintln(os.Stderr, usageLine)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// parseHoisted parses flags that may appear before or after positional
// arguments (flag.Parse stops at the first non-flag argument, so flags are
// hoisted in front). valueFlags names the flags that consume the following
// argument; boolean flags and -flag=value forms never do. Returns the
// positional arguments.
func parseHoisted(fs *flag.FlagSet, args []string, valueFlags map[string]bool) []string {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "--" {
			name := strings.TrimLeft(a, "-")
			takesValue := !strings.Contains(name, "=") && valueFlags[name]
			if takesValue && i+1 < len(args) {
				fs.Parse([]string{a, args[i+1]})
				i++
			} else {
				fs.Parse([]string{a})
			}
			continue
		}
		pos = append(pos, a)
	}
	fs.Parse(pos)
	return fs.Args()
}

func cmdMine(args []string) error {
	fs := flag.NewFlagSet("mine", flag.ExitOnError)
	since := fs.String("since", "", "only commits after this date (YYYY-MM-DD)")
	limit := fs.Int("limit", 0, "max test-touching commits to examine (0 = all)")
	out := fs.String("out", "mined", "output directory for Harbor tasks")
	emitUnverified := fs.Bool("emit-unverified", false, "also emit tasks that failed verification")
	probeOnly := fs.Bool("probe-only", false, "discovery + compile probe only (funnel survey)")
	var envs kvList
	fs.Var(&envs, "env", "test env var KEY=VALUE (repeatable)")
	pos := parseHoisted(fs, args, map[string]bool{"since": true, "limit": true, "out": true, "env": true})
	if len(pos) < 1 {
		fmt.Fprintln(os.Stderr, "error: repo path required")
		os.Exit(2)
	}
	cfg := mineConfig{
		Repo: pos[0], Since: *since, Limit: *limit, Out: *out,
		Env: map[string]string{}, EmitUnverified: *emitUnverified, ProbeOnly: *probeOnly,
	}
	for _, kv := range envs {
		if i := strings.Index(kv, "="); i > 0 {
			cfg.Env[kv[:i]] = kv[i+1:]
		}
	}
	return mine(cfg)
}

func cmdReverify(args []string) error {
	fs := flag.NewFlagSet("reverify", flag.ExitOnError)
	jobs := fs.Int("jobs", 2, "concurrent builds/trials")
	var publics kvList
	fs.Var(&publics, "public", "root whose instances have public origin (repeatable)")
	pos := parseHoisted(fs, args, map[string]bool{"jobs": true, "public": true})
	if len(pos) < 1 {
		fmt.Fprintln(os.Stderr, "error: at least one tasks root required")
		os.Exit(2)
	}
	pub := map[string]bool{}
	for _, p := range publics {
		pub[filepath.Clean(p)] = true
	}
	return runReverify(pos, *jobs, pub)
}

func cmdDeleak(args []string) error {
	fs := flag.NewFlagSet("deleak", flag.ExitOnError)
	repo := fs.String("repo", "", "source repo path (for symbol extraction)")
	repoName := fs.String("name", "", "repo display name for instructions")
	pos := parseHoisted(fs, args, map[string]bool{"repo": true, "name": true})
	if len(pos) < 1 || *repo == "" {
		fmt.Fprintln(os.Stderr, "usage: prospect deleak <tasks-root> --repo <repo-path> [--name <display-name>]")
		os.Exit(2)
	}
	if *repoName == "" {
		*repoName = filepath.Base(*repo)
	}
	return runDeleak(pos[0], *repo, *repoName)
}

func cmdIssues(args []string) error {
	fs := flag.NewFlagSet("issues", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: prospect issues <tasks-root>")
		os.Exit(2)
	}
	return runIssues(fs.Arg(0))
}

func cmdDedup(args []string) error {
	fs := flag.NewFlagSet("dedup", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: prospect dedup <tasks-root>")
		os.Exit(2)
	}
	return runDedup(fs.Arg(0))
}

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: prospect migrate <tasks-root>")
		os.Exit(2)
	}
	return runMigrate(fs.Arg(0))
}

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	origin := fs.String("origin", "", "origin substring that must never appear in agent transcripts (e.g. repo slug)")
	pos := parseHoisted(fs, args, map[string]bool{"origin": true})
	if len(pos) < 1 || *origin == "" {
		fmt.Fprintln(os.Stderr, "usage: prospect audit <jobsdir> --origin <repo-slug>")
		os.Exit(2)
	}
	return runAudit(pos[0], *origin)
}

type kvList []string

func (k *kvList) String() string { return strings.Join(*k, ",") }
func (k *kvList) Set(v string) error {
	*k = append(*k, v)
	return nil
}
