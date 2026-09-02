// Package graphtest builds the synthetic fixture graph
// (internal/graph/testdata/fixture) for emitter tests. The graph package's own
// tests carry the same registry inline because an in-package test cannot
// import this package without a cycle; keep the two in step.
package graphtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// FixturePath is the repository-relative path of the fixture package.
const FixturePath = "cmd/clientdtogen/internal/graph/testdata/fixture"

// OtherPath is the package the fixture reaches transitively.
const OtherPath = FixturePath + "/other"

// RepoRoot walks up from the test's working directory to go.mod.
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// Registry returns the fixture registry: one root per §4.1/§4.4 row.
func Registry() *registry.Registry {
	return &registry.Registry{
		Schema: 1,
		Packages: []registry.Package{{
			Path:    FixturePath,
			Dialect: registry.DialectUpstreamCompat,
			Roots: []registry.Root{
				{Type: "Scalars", Direction: registry.DirectionResponse},
				{Type: "Collections", Direction: registry.DirectionResponse},
				{Type: "Embedded", Direction: registry.DirectionRequest},
				{Type: "AliasRoot", Direction: registry.DirectionResponse},
				{Type: "Request", Direction: registry.DirectionRequest},
				{Type: "Gated", Direction: registry.DirectionResponse, Gate: "cap.gated"},
				{Type: "Gated2", Direction: registry.DirectionResponse, Gate: "cap.other"},
				{Type: "StandaloneAlias", Direction: registry.DirectionRequest, Dialect: registry.DialectBloem, Gate: "cap.alias"},
				{Type: "Compat", Direction: registry.DirectionResponse, BloemFields: []string{"promo"}},
				{Type: "BloemOnly", Direction: registry.DirectionBoth, Dialect: registry.DialectBloem, Gate: "cap.bloem"},
				{Type: "Protocol", Direction: registry.DirectionRequest},
			},
		}},
		Serializers: map[string]registry.Serializer{
			FixturePath + ".Response.frame_rate":     {"kotlin": "org.example.FrameRateWire", "swift": "FrameRateWire"},
			FixturePath + ".Response.frame_rate_ptr": {"kotlin": "org.example.FrameRateWire"},
		},
	}
}

// Build builds the fixture graph from reg (Registry() when nil).
func Build(t *testing.T, reg *registry.Registry) *graph.Graph {
	t.Helper()
	if reg == nil {
		reg = Registry()
	}
	g, err := graph.Build(graph.Config{Dir: RepoRoot(t), Registry: reg})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}
