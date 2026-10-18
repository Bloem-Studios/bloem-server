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
