package main

import (
	"flag"
	"reflect"
	"testing"
)

// mineFlagSet replicates cmdMine's flag shape for parser tests.
func mineFlagSet() (*flag.FlagSet, map[string]bool) {
	fs := flag.NewFlagSet("mine", flag.ExitOnError)
	fs.String("since", "", "")
	fs.Int("limit", 0, "")
	fs.String("out", "mined", "")
	fs.Bool("emit-unverified", false, "")
	fs.Bool("probe-only", false, "")
	var envs kvList
	fs.Var(&envs, "env", "")
	return fs, map[string]bool{"since": true, "limit": true, "out": true, "env": true}
}

// The documented invocations put flags after the positional repo/root
// arguments; parseHoisted must accept every one of these orders.
func TestParseHoistedMineOrders(t *testing.T) {
	cases := []struct {
		note string
		args []string
		pos  []string
		want map[string]string
	}{
		{
			note: "README order: repo first, value flags after",
			args: []string{"/path/to/repo", "--out", "./mined", "--since", "2025-12-01", "--limit", "90"},
			pos:  []string{"/path/to/repo"},
			want: map[string]string{"out": "./mined", "since": "2025-12-01", "limit": "90"},
		},
		{
			note: "bool flag after positional",
			args: []string{"repo", "--probe-only"},
			pos:  []string{"repo"},
			want: map[string]string{"probe-only": "true"},
		},
		{
			note: "bool flag between positional and value flag",
			args: []string{"repo", "--emit-unverified", "--out", "x"},
			pos:  []string{"repo"},
			want: map[string]string{"emit-unverified": "true", "out": "x"},
		},
		{
			note: "equals form after positional",
			args: []string{"repo", "--limit=5"},
			pos:  []string{"repo"},
			want: map[string]string{"limit": "5"},
		},
		{
			note: "repeatable env flag after positional",
			args: []string{"repo", "--env", "A=1", "--env", "B=2"},
			pos:  []string{"repo"},
			want: map[string]string{"env": "A=1,B=2"},
		},
		{
			note: "flags before positional still parse",
			args: []string{"--out", "x", "repo"},
			pos:  []string{"repo"},
			want: map[string]string{"out": "x"},
		},
		{
			note: "double dash is a terminator, not a flag",
			args: []string{"--", "repo"},
			pos:  []string{"repo"},
			want: map[string]string{},
		},
	}
	for _, c := range cases {
		fs, valueFlags := mineFlagSet()
		pos := parseHoisted(fs, c.args, valueFlags)
		if !reflect.DeepEqual(pos, c.pos) {
			t.Errorf("%s: positionals = %v, want %v", c.note, pos, c.pos)
		}
		for name, want := range c.want {
			if got := fs.Lookup(name).Value.String(); got != want {
				t.Errorf("%s: flag %s = %q, want %q", c.note, name, got, want)
			}
		}
	}
}

func TestParseHoistedReverifyOrders(t *testing.T) {
	fs := flag.NewFlagSet("reverify", flag.ExitOnError)
	jobs := fs.Int("jobs", 2, "")
	var publics kvList
	fs.Var(&publics, "public", "")
	pos := parseHoisted(fs, []string{"rootA", "rootB", "--jobs", "3", "--public", "rootA"}, map[string]bool{"jobs": true, "public": true})
	if !reflect.DeepEqual(pos, []string{"rootA", "rootB"}) {
		t.Errorf("positionals = %v", pos)
	}
	if *jobs != 3 || len(publics) != 1 || publics[0] != "rootA" {
		t.Errorf("jobs = %d, publics = %v", *jobs, publics)
	}
}

func TestParseHoistedDeleakOrders(t *testing.T) {
	fs := flag.NewFlagSet("deleak", flag.ExitOnError)
	repo := fs.String("repo", "", "")
	name := fs.String("name", "", "")
	pos := parseHoisted(fs, []string{"./mined", "--repo", "/path/to/repo", "--name", "repo"}, map[string]bool{"repo": true, "name": true})
	if !reflect.DeepEqual(pos, []string{"./mined"}) || *repo != "/path/to/repo" || *name != "repo" {
		t.Errorf("pos = %v, repo = %q, name = %q", pos, *repo, *name)
	}
}

func TestParseHoistedAuditOrders(t *testing.T) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	origin := fs.String("origin", "", "")
	pos := parseHoisted(fs, []string{"/jobs/dir", "--origin", "example/slug"}, map[string]bool{"origin": true})
	if !reflect.DeepEqual(pos, []string{"/jobs/dir"}) || *origin != "example/slug" {
		t.Errorf("pos = %v, origin = %q", pos, *origin)
	}
}
