package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ciEnv is the service+env contract parsed from the repo's GitHub Actions
// test workflow — the same trick the hand-checks used.
type ciEnv struct {
	Services []service
	Env      map[string]string // job-level env the tests expect
	Source   string            // workflow file it came from
}

type service struct {
	Name          string
	Image         string
	Env           map[string]string
	ContainerPort int
}

type wfJob struct {
	Services map[string]struct {
		Image string            `yaml:"image"`
		Env   map[string]string `yaml:"env"`
		Ports []string          `yaml:"ports"`
	} `yaml:"services"`
	Env map[string]string `yaml:"env"`
}

type wfFile struct {
	Jobs map[string]wfJob `yaml:"jobs"`
}

// parseCI finds a workflow with service containers and extracts the contract.
func parseCI(repo string) (*ciEnv, error) {
	entries, err := filepath.Glob(filepath.Join(repo, ".github", "workflows", "*.y*ml"))
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("no workflows found")
	}
	// Prefer files named *test*, fall back to scanning all.
	sort.Slice(entries, func(i, j int) bool { return strings.Contains(entries[i], "test") })
	for _, f := range entries {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var wf wfFile
		if err := yaml.Unmarshal(data, &wf); err != nil {
			continue
		}
		for _, job := range wf.Jobs {
			if len(job.Services) == 0 {
				continue
			}
			env := &ciEnv{Env: map[string]string{}, Source: filepath.Base(f)}
			for k, v := range job.Env {
				if strings.Contains(v, "${{") {
					continue // Actions expressions, not real values
				}
				env.Env[k] = v
			}
			for name, s := range job.Services {
				svc := service{Name: name, Image: s.Image, Env: s.Env}
				for _, p := range s.Ports {
					if cp := afterColon(p); cp > 0 {
						svc.ContainerPort = cp
						break
					}
				}
				env.Services = append(env.Services, svc)
			}
			sort.Slice(env.Services, func(i, j int) bool { return env.Services[i].Name < env.Services[j].Name })
			return env, nil
		}
	}
	return nil, fmt.Errorf("no workflow with service containers")
}

func afterColon(p string) int {
	if i := strings.LastIndex(p, ":"); i >= 0 {
		n, _ := strconv.Atoi(strings.TrimSpace(p[i+1:]))
		return n
	}
	n, _ := strconv.Atoi(strings.TrimSpace(p))
	return n
}

// startedService tracks runtime state for cleanup and env rewriting.
type startedEnv struct {
	Final    map[string]string
	launched []int // host ports we bound, for readiness waits
	cleanups []func()
}

// startServices launches each service via docker run on random host ports and
// rewrites env values that referenced the container port.
func (c *ciEnv) startServices() (*startedEnv, error) {
	st := &startedEnv{Final: map[string]string{}}
	for k, v := range c.Env {
		st.Final[k] = v
	}
	if err := dockerOK(); err != nil {
		return nil, err
	}
	for _, svc := range c.Services {
		if svc.ContainerPort == 0 {
			continue
		}
		host := freePort()
		name := fmt.Sprintf("prospect-%s-%d", svc.Name, time.Now().UnixNano()%1e6)
		args := []string{"run", "-d", "--name", name, "-p", fmt.Sprintf("%d:%d", host, svc.ContainerPort)}
		keys := make([]string, 0, len(svc.Env))
		for k := range svc.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "-e", k+"="+svc.Env[k])
		}
		args = append(args, svc.Image)
		if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
			st.cleanup()
			return nil, fmt.Errorf("docker run %s: %v: %s", svc.Image, err, firstLines(string(out), 3))
		}
		container := svc.ContainerPort
		st.launched = append(st.launched, host)
		st.cleanups = append(st.cleanups, func() { exec.Command("docker", "rm", "-f", name).Run() })
		// Rewrite job-level env values pointing at the container port.
		for k, v := range st.Final {
			if v == strconv.Itoa(container) {
				st.Final[k] = strconv.Itoa(host)
			}
		}
	}
	// Wait for TCP readiness on each mapped port (best effort, bounded).
	deadline := time.Now().Add(60 * time.Second)
	for _, host := range st.launched {
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(host)), time.Second)
			if err == nil {
				conn.Close()
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	time.Sleep(2 * time.Second) // settle
	return st, nil
}

func (s *startedEnv) cleanup() {
	for _, f := range s.cleanups {
		f()
	}
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func dockerOK() error {
	return exec.Command("docker", "info").Run()
}
