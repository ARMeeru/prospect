package main

import (
	"strings"
	"testing"
)

// The witness matcher once discarded 25 valid instances because Go appends a
// duration suffix to --- FAIL lines. These cases are the fixture for that bug.
func TestFailureWitness(t *testing.T) {
	names := []string{"TestCalculateDistance_UnitsAgree", "TestConvertDistance_RoundTripsThroughMiles"}
	cases := []struct {
		note, log, want string
	}{
		{
			note: "top-level fail with duration suffix",
			log:  "--- FAIL: TestCalculateDistance_UnitsAgree (0.01s)\nFAIL",
			want: "--- FAIL: TestCalculateDistance_UnitsAgree (0.01s)",
		},
		{
			note: "subtest fail of an expected test",
			log:  "--- FAIL: TestCalculateDistance_UnitsAgree/sub (0.00s)\nFAIL",
			want: "--- FAIL: TestCalculateDistance_UnitsAgree/sub (0.00s)",
		},
		{
			note: "rapid-style multi-line failure",
			log:  "--- FAIL: TestConvertDistance_RoundTripsThroughMiles (0.00s)\n    x.go:10: disagreement\nFAIL",
			want: "--- FAIL: TestConvertDistance_RoundTripsThroughMiles (0.00s)",
		},
		{
			note: "other test failed — not a witness for ours",
			log:  "--- FAIL: TestSomethingElse (0.01s)\nFAIL",
			want: "",
		},
		{
			note: "build failure has no assertion witness",
			log:  "# github.com/x/y [build failed]\nFAIL",
			want: "",
		},
		{
			note: "empty log",
			log:  "",
			want: "",
		},
	}
	for _, c := range cases {
		if got := failureWitness(c.log, names); got != c.want {
			t.Errorf("%s: got %q, want %q", c.note, got, c.want)
		}
	}
}

func TestSanitizeTag(t *testing.T) {
	cases := map[string]string{
		"fix-pr171-fix-location-bind":         "prospect-rv-fix-pr171-fix-location-bind",
		"trailing-dash-":                      "prospect-rv-trailing-dash",
		"UPPER_and.dots":                      "prospect-rv-upper-and-dots",
		"---":                                 "prospect-rv", // degenerate but valid
		"unicode-✓-name":                      "prospect-rv-unicode---name", // docker-legal: hyphens allowed mid-tag
		"fix-pr136-security-bounds-check-in-": "prospect-rv-fix-pr136-security-bounds-check-in",
	}
	for in, want := range cases {
		if got := sanitizeTag(in); got != want {
			t.Errorf("sanitizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

// Docker tag charset is [a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}; the failure we
// guard against is a leading/trailing hyphen or an empty tag.
func TestSanitizeTagNeverInvalid(t *testing.T) {
	for _, name := range []string{"a-", "-a", "a--b", "", "A B C", "---"} {
		tag := sanitizeTag(name)
		if tag == "" || strings.HasPrefix(tag, "-") || strings.HasSuffix(tag, "-") {
			t.Errorf("sanitizeTag(%q) produced invalid docker tag %q", name, tag)
		}
	}
}
