package main

import (
	"strings"
	"testing"
)

func TestStripDirectives(t *testing.T) {
	cases := []struct {
		note, in, wantContains string
		wantAbsent             string
	}{
		{
			note:        "directive as second sentence in a line (the #171 regression)",
			in:          "Exclude the unit and pagination values from the count query arguments. Add sqlmock coverage for location searches with and without text.",
			wantContains: "Exclude the unit and pagination values",
			wantAbsent:  "sqlmock",
		},
		{
			note:        "directive-only line",
			in:          "Add unit tests for the new path.",
			wantContains: "",
			wantAbsent:  "unit tests",
		},
		{
			note:        "behavior sentence mentioning tests without directive verb survives",
			in:          "Tests currently fail with a nil map. The endpoint should return 429.",
			wantContains: "should return 429",
			wantAbsent:  "",
		},
	}
	for _, c := range cases {
		got := stripDirectives(c.in)
		if c.wantContains != "" && !strings.Contains(got, c.wantContains) {
			t.Errorf("%s: output missing %q (got %q)", c.note, c.wantContains, got)
		}
		if c.wantAbsent != "" && strings.Contains(got, c.wantAbsent) {
			t.Errorf("%s: output should not contain %q (got %q)", c.note, c.wantAbsent, got)
		}
	}
}

func TestMechanismVerbNominates(t *testing.T) {
	if !mechanismVerbRe.MatchString("Exclude the unit and pagination values from the count query arguments.") {
		t.Error("mechanism verb 'exclude' should nominate")
	}
	if mechanismVerbRe.MatchString("Search count returns wrong totals when pagination is applied.") {
		t.Error("behavior statement should not nominate")
	}
}
