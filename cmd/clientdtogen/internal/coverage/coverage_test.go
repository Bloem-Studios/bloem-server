package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

func testRegistry() *registry.Registry {
	return &registry.Registry{Schema: 1, Packages: []registry.Package{
		{Path: "internal/playback", Dialect: registry.DialectUpstreamCompat, Roots: []registry.Root{
			{Type: "PlanV3", Direction: registry.DirectionResponse},
		}},
	}}
}

func validDoc() string {
	return `{
  "schema": 1,
  "files": [
    {"file": "model/playback/PlaybackProtocolV3.kt", "generated_from": ["internal/playback"]},
    {"file": "some v2 helper file", "reason": "Client-only view state rendered from generated shapes."}
  ],
  "types": [
    {"type": "internal/playback.RecipeCard", "reason": "Durable transcode recipe carried in stream-token claims, never serialised to a client."}
  ]
}`
}

func TestLoadValid(t *testing.T) {
	allow, err := Load([]byte(validDoc()), testRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if allow.Schema != 1 || len(allow.Files) != 2 || len(allow.Types) != 1 {
		t.Fatalf("parsed %+v", allow)
	}
}

func TestLoadRejects(t *testing.T) {
	good := validDoc()
	swap := func(from, to string) string {
		if !strings.Contains(good, from) {
			t.Fatalf("test document lacks %q", from)
		}
		return strings.Replace(good, from, to, 1)
	}
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"schema", swap(`"schema": 1`, `"schema": 2`), "schema 2"},
		{"unknown key", swap(`"files": [`, `"extra": [], "files": [`), "unknown field"},
		{"duplicate file", swap(`"some v2 helper file"`, `"model/playback/PlaybackProtocolV3.kt"`), "listed twice"},
		{"duplicate type", swap(`{"type": "internal/playback.RecipeCard", "reason": "Durable transcode recipe carried in stream-token claims, never serialised to a client."}`,
			`{"type": "internal/playback.RecipeCard", "reason": "Durable transcode recipe carried in stream-token claims, never serialised to a client."}, {"type": "internal/playback.RecipeCard", "reason": "Durable transcode recipe carried in stream-token claims, never serialised to a client."}`),
			"listed twice"},
		{"both generated_from and reason", swap(
			`{"file": "some v2 helper file", "reason": "Client-only view state rendered from generated shapes."}`,
			`{"file": "some v2 helper file", "generated_from": ["internal/playback"], "reason": "Client-only view state rendered from generated shapes."}`),
			"mutually exclusive"},
		{"neither generated_from nor reason", swap(
			`{"file": "some v2 helper file", "reason": "Client-only view state rendered from generated shapes."}`,
			`{"file": "some v2 helper file"}`),
			"set generated_from or a written reason"},
		{"unregistered generated_from", swap(`"generated_from": ["internal/playback"]},`, `"generated_from": ["internal/nope"]},`), "not a registered package"},
		{"todo reason", swap(`"Client-only view state rendered from generated shapes."`, `"TODO"`), `contains "todo"`},
		{"not-yet reason", swap(`"Client-only view state rendered from generated shapes."`, `"Not yet registered, that comes later."`), `contains "not yet"`},
		{"fragment reason", swap(`"Client-only view state rendered from generated shapes."`, `"client-only view state"`), "not a full sentence"},
		{"type key without package", swap(`"internal/playback.RecipeCard"`, `"RecipeCard"`), "graph key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(tc.doc), testRegistry())
			if err == nil {
				t.Fatal("Load accepted the document")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

// repoRoot walks up from the package directory to go.mod.
func repoRoot(t *testing.T) string {
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

// buildRealTreeGraph builds the graph of the committed registry, the same way
// the generator does.
func buildRealTreeGraph(t *testing.T) (*graph.Graph, *registry.Registry, string) {
	t.Helper()
	root := repoRoot(t)
	reg, err := registry.Load(filepath.Join(root, "contracts", "client", "v1", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(graph.Config{Dir: root, Registry: reg})
	if err != nil {
		t.Fatalf("building type graph: %v", err)
	}
	return g, reg, root
}

// TestAllowlistMatchesTree is the §8 coverage-drift gate: the committed
// coverage.json must exactly explain the drift of the committed registry —
// every unreached wire type allowlisted with a reason, and no stale entries.
func TestAllowlistMatchesTree(t *testing.T) {
	g, reg, root := buildRealTreeGraph(t)
	allow, err := LoadFile(filepath.Join(root, "contracts", "client", "v1", "coverage.json"), reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(g, allow); err != nil {
		t.Fatalf("coverage drift:\n%v", err)
	}
}

// TestAllowlistDetectsDrift: deleting an allowlist entry must fail the check
// (the type is still unreached), and inventing one must fail it too (stale),
// so the list can neither silently grow nor keep entries it no longer needs.
func TestAllowlistDetectsDrift(t *testing.T) {
	g, reg, root := buildRealTreeGraph(t)
	allow, err := LoadFile(filepath.Join(root, "contracts", "client", "v1", "coverage.json"), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(allow.Types) == 0 {
		t.Fatal("committed allowlist has no type entries to test with")
	}
	dropped := allow.Types[0]
	allow.Types = allow.Types[1:]
	err = Check(g, allow)
	if err == nil || !strings.Contains(err.Error(), dropped.Type) || !strings.Contains(err.Error(), "allowlisted") {
		t.Fatalf("dropping %s from the allowlist must fail the check, got %v", dropped.Type, err)
	}

	allow.Types = append(allow.Types, TypeEntry{Type: "internal/playback.NotAWireType", Reason: "Invented entry used by the test to pin stale-entry detection."})
	err = Check(g, allow)
	if err == nil || !strings.Contains(err.Error(), "stale allowlist entry") || !strings.Contains(err.Error(), "NotAWireType") {
		t.Fatalf("an invented entry must fail the check as stale, got %v", err)
	}
}

// TestSpecFilesArePinned: the v2 model files §9.1 names explicitly must be
// in the allowlist with the posture the spec gives them, so the inventory
// cannot be quietly reduced.
func TestSpecFilesArePinned(t *testing.T) {
	_, reg, root := buildRealTreeGraph(t)
	allow, err := LoadFile(filepath.Join(root, "contracts", "client", "v1", "coverage.json"), reg)
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]FileEntry{}
	for _, f := range allow.Files {
		byFile[f.File] = f
	}
	cases := []struct {
		file          string
		generatedFrom string
		wantReason    bool
	}{
		{"model/playback/PlaybackProtocolV3.kt", "internal/playback", false},
		{"model/playback/PlaybackModels.kt", "internal/models", false},
		{"model/catalog/FrameRateSerializer.kt", "", true},
		{"CatalogModels.kt presentation helpers", "", true},
		{"settings resolution files", "", true},
		{"PlaybackV3Validation sealed hierarchy", "", true},
	}
	for _, tc := range cases {
		f, ok := byFile[tc.file]
		if !ok {
			t.Errorf("allowlist lacks the §9.1 entry %q", tc.file)
			continue
		}
		if tc.wantReason && f.Reason == "" {
			t.Errorf("%q must carry a written reason (it stays hand-written per §9.2)", tc.file)
		}
		if tc.generatedFrom != "" {
			found := false
			for _, pkg := range f.GeneratedFrom {
				if pkg == tc.generatedFrom {
					found = true
				}
			}
			if !found {
				t.Errorf("%q must be generated from %s, got %v", tc.file, tc.generatedFrom, f.GeneratedFrom)
			}
		}
	}
}
