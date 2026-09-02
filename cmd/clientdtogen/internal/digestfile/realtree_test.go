package digestfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

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

// TestDigestPinMatchesTree is the §7.2 test: the pinned contract digest must
// match the type graph of the committed registry, and the pinned
// removals-table digest must match the table as it stands. A contract change
// passes only after the wire-shape change has been recorded in the pre-lock
// removals table (when it removes or retypes a shape) and the pins have been
// refreshed with `make client-digest` — the error below names the missing
// step in each case.
func TestDigestPinMatchesTree(t *testing.T) {
	root := repoRoot(t)

	reg, err := registry.Load(filepath.Join(root, "contracts", "client", "v1", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(graph.Config{Dir: root, Registry: reg})
	if err != nil {
		t.Fatalf("building type graph: %v", err)
	}
	current := g.Digest()

	scope, err := os.ReadFile(filepath.Join(root, ScopeFile))
	if err != nil {
		t.Fatal(err)
	}
	table, err := TableDigest(scope)
	if err != nil {
		t.Fatal(err)
	}

	pins, err := Read(filepath.Join(root, "contracts", "client", "v1", "digest.txt"))
	if err != nil {
		t.Fatalf("contracts/client/v1/digest.txt is unreadable: %v", err)
	}

	if pins.Contract == current {
		if pins.RemovalsTable != table {
			t.Fatalf(`%s changed after digest.txt was pinned:
  pinned         %s
  table now      %s
If the edit records a removal already reflected in the tree, re-pin with
make client-digest; otherwise expect the contract digest to move with it.`,
				ScopeFile, pins.RemovalsTable, table)
		}
		return
	}

	if pins.RemovalsTable == table {
		t.Fatalf(`the type-graph digest changed but %s did not:
  pinned    %s
  tree now  %s
A wire-shape change is a pre-lock contract change: if it removes or retypes a
wire shape, record it in the %q table of %s (docs/specs/client-dto-generator.md
§7.2), then re-pin both digests with make client-digest.`,
			ScopeFile, pins.Contract, current, "Breaking removals taken before lock", ScopeFile)
	}

	t.Fatalf(`the type-graph digest changed and %s was edited, but
contracts/client/v1/digest.txt still pins the old pair:
  contract  pinned %s, tree now %s
  table     pinned %s, table now %s
Re-pin with make client-digest once the removal row and the wire change are in
the same commit.`, ScopeFile, pins.Contract, current, pins.RemovalsTable, table)
}
