package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// compileCheck attempts to build the test binary of each package.
// Returns true only if every package compiles.
func compileCheck(dir string, pkgs []string) (bool, string) {
	for _, p := range pkgs {
		cmd := exec.Command("go", "test", "-c", "-o", os.DevNull, "./"+p+"/")
		cmd.Dir = dir
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return false, fmt.Sprintf("%s: %s", p, firstLines(out.String(), 3))
		}
	}
	return true, ""
}

// runGoTest runs the named tests in dir. Returns (allPassed, combinedOutput).
func runGoTest(dir string, pkgs []string, testNames []string, extraEnv map[string]string) (bool, string) {
	args := []string{"test", "-count=1", "-timeout=300s", "-run", "^(" + strings.Join(testNames, "|") + ")$"}
	for _, p := range pkgs {
		args = append(args, "./"+p+"/")
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = envWith(extraEnv)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return err == nil, out.String()
}

func envWith(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(strings.TrimSpace(s), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}
