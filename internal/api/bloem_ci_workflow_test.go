package api

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type bloemWorkflowStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

type bloemWorkflowJob struct {
	If       string `yaml:"if"`
	Services map[string]struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
	Steps []bloemWorkflowStep `yaml:"steps"`
}

func readBloemWorkflow(t *testing.T, path string) map[string]bloemWorkflowJob {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var workflow struct {
		Jobs map[string]bloemWorkflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return workflow.Jobs
}

// The upstream-sync merge job does not run the compatibility-removal gate
// itself: it would inspect the default branch after the merge had landed.
// It relies on CI's Go contract checks job having run the gate on the tested
// head, so that job must run on every pull request, with ripgrep installed
// before the gate.
func TestUpstreamSyncMergeReliesOnCIForTheCompatibilityGate(t *testing.T) {
	verify, ok := readBloemWorkflow(t, "../../.github/workflows/ci.yml")["go-verify"]
	if !ok {
		t.Fatal("CI has no go-verify job")
	}
	if verify.If != "" {
		t.Fatalf("go-verify runs only when %q; the upstream-sync merge needs it on every head", verify.If)
	}
	ripgrep, gate := -1, -1
	for i, step := range verify.Steps {
		if strings.Contains(step.Run, "bloem-install-test-tools.sh") && strings.Contains(step.Run, "ripgrep") && ripgrep < 0 {
			ripgrep = i
		}
		if strings.Contains(step.Run, "verify-no-compat") && gate < 0 {
			gate = i
		}
	}
	if gate < 0 {
		t.Fatal("go-verify does not run verify-no-compat")
	}
	if ripgrep < 0 || ripgrep > gate {
		t.Fatal("go-verify does not install ripgrep before verify-no-compat")
	}

	merge, ok := readBloemWorkflow(t, "../../.github/workflows/upstream-sync-merge.yml")["merge"]
	if !ok {
		t.Fatal("upstream-sync-merge has no merge job")
	}
	for _, step := range merge.Steps {
		if strings.Contains(step.Run, "verify-no-compat") {
			t.Fatalf("upstream-sync-merge step %q runs verify-no-compat against the default branch", step.Name)
		}
	}
}

// CI's PostgreSQL services run the major version the deployment compose file
// runs. PostgreSQL 18 reports some constraint violations with SQLSTATEs that
// 17 does not (23001 for RESTRICT), so a 17 service hides behavior production
// has.
func TestCIPostgresServicesMatchTheDeploymentMajor(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	var compose struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}
	deployed := compose.Services["postgres"].Image
	if !strings.HasPrefix(deployed, "pgvector/pgvector:pg") {
		t.Fatalf("docker-compose.yml postgres image = %q", deployed)
	}
	services := 0
	for name, job := range readBloemWorkflow(t, "../../.github/workflows/ci.yml") {
		postgres, ok := job.Services["postgres"]
		if !ok {
			continue
		}
		services++
		if postgres.Image != deployed {
			t.Errorf("CI job %s runs %s, deployment runs %s", name, postgres.Image, deployed)
		}
	}
	if services == 0 {
		t.Fatal("CI has no PostgreSQL service")
	}
}
