// Package deploytest checks the deployment and CI configuration mechanically (Stage 18, docs/stage-18/security-testing.md
// §3): the compose stack, the Dockerfiles and the GitHub Actions workflow. A hardening control that is only written in a
// document drifts; these tests fail the build when one is removed.
package deploytest

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type service struct {
	Image       string         `yaml:"image"`
	Build       any            `yaml:"build"`
	User        string         `yaml:"user"`
	Privileged  bool           `yaml:"privileged"`
	NetworkMode string         `yaml:"network_mode"`
	Pid         string         `yaml:"pid"`
	ReadOnly    bool           `yaml:"read_only"`
	SecurityOpt []string       `yaml:"security_opt"`
	CapDrop     []string       `yaml:"cap_drop"`
	CapAdd      []string       `yaml:"cap_add"`
	PidsLimit   int            `yaml:"pids_limit"`
	MemLimit    string         `yaml:"mem_limit"`
	Ports       []string       `yaml:"ports"`
	Volumes     []string       `yaml:"volumes"`
	Networks    any            `yaml:"networks"`
	Environment map[string]any `yaml:"environment"`
}

func composeServices(t *testing.T) map[string]service {
	t.Helper()
	var doc struct {
		Services map[string]service `yaml:"services"`
	}
	if err := yaml.Unmarshal(readFile(t, "compose.yaml"), &doc); err != nil {
		t.Fatalf("compose.yaml: %v", err)
	}
	if len(doc.Services) < 10 {
		t.Fatalf("compose.yaml: only %d services parsed", len(doc.Services))
	}
	return doc.Services
}

// thirdPartyCaps is the only capability set an image may add back, and only to switch to its unprivileged user.
var thirdPartyCaps = map[string][]string{
	"fundzim-postgres": {"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"},
	"fundzim-clamav":   {"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"},
	"fundzim-secrets":  {"SETGID", "SETUID"},
}

func networkNames(n any) []string {
	switch v := n.(type) {
	case []any:
		var out []string
		for _, x := range v {
			out = append(out, x.(string))
		}
		return out
	case map[string]any:
		var out []string
		for k := range v {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	return nil
}

func TestComposeContainersAreHardened(t *testing.T) {
	for name, s := range composeServices(t) {
		if !slices.Contains(s.SecurityOpt, "no-new-privileges:true") {
			t.Errorf("%s: missing security_opt no-new-privileges:true", name)
		}
		if !slices.Equal(s.CapDrop, []string{"ALL"}) {
			t.Errorf("%s: cap_drop must be [ALL], got %v", name, s.CapDrop)
		}
		for _, c := range s.CapAdd {
			if !slices.Contains(thirdPartyCaps[name], c) {
				t.Errorf("%s: capability %s is not on its allow-list", name, c)
			}
		}
		if s.Privileged || s.NetworkMode == "host" || s.Pid == "host" {
			t.Errorf("%s: privileged, host network or host PID namespace", name)
		}
		if s.PidsLimit <= 0 || s.MemLimit == "" {
			t.Errorf("%s: needs pids_limit and mem_limit", name)
		}
		for _, p := range s.Ports {
			if !strings.HasPrefix(p, "127.0.0.1:") {
				t.Errorf("%s: port %q is not bound to 127.0.0.1", name, p)
			}
		}
		for _, v := range s.Volumes {
			if strings.Contains(v, "docker.sock") {
				t.Errorf("%s: mounts the Docker socket", name)
			}
		}
		if s.Build != nil || strings.HasPrefix(s.Image, "fundzim/") {
			// FundZim's own images: read-only root filesystem, never root
			if !s.ReadOnly {
				t.Errorf("%s: FundZim containers run with read_only: true", name)
			}
			// F-R1-07: one tag per compose project, so a disposable build never replaces the development project's image
			if !strings.HasSuffix(s.Image, ":${COMPOSE_PROJECT_NAME}") {
				t.Errorf("%s: FundZim image %q must be tagged with ${COMPOSE_PROJECT_NAME}", name, s.Image)
			}
		} else if !regexp.MustCompile(`@sha256:[0-9a-f]{64}$`).MatchString(s.Image) {
			t.Errorf("%s: third-party image %q is not pinned by digest", name, s.Image)
		}
	}
}

func TestComposeNetworkSegmentation(t *testing.T) {
	svc := composeServices(t)
	web := svc["fundzim-web"]
	if got := networkNames(web.Networks); !slices.Equal(got, []string{"edge"}) {
		t.Fatalf("fundzim-web must be on the edge network only (no data-service access), got %v", got)
	}
	for _, name := range []string{"fundzim-postgres", "fundzim-redis", "fundzim-storage", "fundzim-secrets", "fundzim-clamav", "fundzim-worker"} {
		if slices.Contains(networkNames(svc[name].Networks), "edge") {
			t.Errorf("%s must not be on the edge network", name)
		}
	}
	nets := web.Networks.(map[string]any)
	ip := nets["edge"].(map[string]any)["ipv4_address"].(string)
	if got := svc["fundzim-api"].Environment["TRUSTED_PROXY_CIDRS"]; got != ip+"/32" {
		t.Fatalf("the API must trust exactly the web proxy (%s/32), got %v", ip, got)
	}
	// the Valkey password never appears on a command line
	if r := svc["fundzim-redis"]; r.User == "" || r.User == "0" || r.User == "root" {
		t.Errorf("fundzim-redis must run as the valkey user, got %q", r.User)
	}
	raw := string(readFile(t, "compose.yaml"))
	if regexp.MustCompile(`"--requirepass"`).MatchString(raw) {
		t.Error("the Valkey password is passed on the command line")
	}
}

var fromRE = regexp.MustCompile(`(?m)^FROM\s+(\S+)`)
var argRE = regexp.MustCompile(`(?m)^ARG\s+([A-Z_]+)(?:=(\S+))?`)

func TestDockerfilesPinnedAndUnprivileged(t *testing.T) {
	for _, f := range []string{"deploy/docker/api.Dockerfile", "deploy/docker/web.Dockerfile"} {
		src := string(readFile(t, f))
		args := map[string]string{}
		for _, m := range argRE.FindAllStringSubmatch(src, -1) {
			args[m[1]] = m[2]
			if regexp.MustCompile(`SECRET|TOKEN|PASSWORD|KEY`).MatchString(m[1]) {
				t.Errorf("%s: build argument %s looks like a secret (build args end up in image metadata)", f, m[1])
			}
		}
		froms := fromRE.FindAllStringSubmatch(src, -1)
		if len(froms) == 0 {
			t.Fatalf("%s: no FROM", f)
		}
		for _, m := range froms {
			img := m[1]
			if strings.HasPrefix(img, "${") {
				img = args[strings.Trim(img, "${}")]
			}
			if !regexp.MustCompile(`@sha256:[0-9a-f]{64}$`).MatchString(img) {
				t.Errorf("%s: base image %q is not pinned by digest", f, img)
			}
		}
		users := regexp.MustCompile(`(?m)^USER\s+(\S+)`).FindAllStringSubmatch(src, -1)
		if len(users) == 0 {
			t.Errorf("%s: the runtime stage must set a USER", f)
		} else if u := users[len(users)-1][1]; u == "root" || u == "0" || strings.HasPrefix(u, "0:") {
			t.Errorf("%s: runs as %s", f, u)
		}
	}
}

func TestWorkflowLeastPrivilege(t *testing.T) {
	raw := readFile(t, ".github/workflows/ci.yml")
	var wf struct {
		On          map[string]any            `yaml:"on"`
		Permissions map[string]string         `yaml:"permissions"`
		Jobs        map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Fatalf("top-level permissions must be exactly {contents: read}, got %v", wf.Permissions)
	}
	if _, ok := wf.On["pull_request_target"]; ok {
		t.Fatal("pull_request_target runs untrusted code with secrets: not allowed")
	}
	if _, ok := wf.On["workflow_run"]; ok {
		t.Fatal("workflow_run is not used by FundZim CI")
	}
	src := string(raw)
	if regexp.MustCompile(`\$\{\{\s*secrets\.`).MatchString(src) {
		t.Fatal("CI must not use repository secrets (no deployments, no production credentials)")
	}
	for name, job := range wf.Jobs {
		if p, ok := job["permissions"]; ok {
			if m, ok := p.(map[string]any); ok {
				for scope, level := range m {
					if level == "write" && !(name == "provenance" && (scope == "id-token" || scope == "attestations")) {
						t.Errorf("job %s requests %s: write", name, scope)
					}
				}
			}
		}
		if _, ok := job["environment"]; ok {
			t.Errorf("job %s targets a deployment environment: CI never deploys", name)
		}
	}
	for _, m := range regexp.MustCompile(`(?m)uses:\s*(\S+)`).FindAllStringSubmatch(src, -1) {
		if !regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(m[1]) {
			t.Errorf("action %s is not pinned to a commit SHA", m[1])
		}
	}
	// every downloaded binary is verified against a pinned checksum before use
	for _, m := range regexp.MustCompile(`curl [^\n]*-o\s+(\S+)`).FindAllStringSubmatch(src, -1) {
		if !strings.Contains(src, m[1]+"\" | sha256sum -c") && !strings.Contains(src, m[1]+"' | sha256sum -c") &&
			!regexp.MustCompile(regexp.QuoteMeta(m[1])+`\s*"\s*\|\s*sha256sum -c`).MatchString(src) {
			t.Errorf("download %s is not verified with sha256sum -c", m[1])
		}
	}
}

// requiredChecks are the status checks the proposed protection of main requires (docs/remediation/phase-2/branch-protection.md).
// Branch protection matches checks by job name: renaming a job silently drops it from the gate, and a second workflow could
// report a check of the same name. Both must fail here first.
var requiredChecks = []string{
	"Go (format, vet, unit tests, vulnerabilities)",
	"Web (lint, types, unit tests, build, audit)",
	"Web end-to-end (Playwright)",
	"OpenAPI and SQL design drafts",
	"Integration (real PostgreSQL, Valkey, Garage, ClamAV via docker compose)",
	"Real-stack E2E (compose + Playwright)",
	"Container images (build, SBOM, vulnerability scan, provenance)",
	"Secret scanning",
}

func TestWorkflowRequiredChecks(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	if !slices.Equal(files, []string{"ci.yml"}) {
		t.Fatalf("exactly one workflow (ci.yml) may report status checks, found %v: another workflow could satisfy a required check by name", files)
	}
	var wf struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(readFile(t, ".github/workflows/ci.yml"), &wf); err != nil {
		t.Fatal(err)
	}
	var names []string
	for id, j := range wf.Jobs {
		if j.Name == "" {
			t.Errorf("job %s has no name: required checks match on the job name", id)
		}
		names = append(names, j.Name)
	}
	slices.Sort(names)
	want := slices.Clone(requiredChecks)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("CI job names %q differ from the required checks %q: update branch protection and this list together", names, want)
	}
}
