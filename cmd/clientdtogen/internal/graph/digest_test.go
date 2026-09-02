package graph

import (
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// setRoot finds the named root and mutates it through f.
func setRoot(t *testing.T, reg *registry.Registry, name string, f func(*registry.Root)) {
	t.Helper()
	for i := range reg.Packages {
		for j := range reg.Packages[i].Roots {
			if reg.Packages[i].Roots[j].Type == name {
				f(&reg.Packages[i].Roots[j])
				return
			}
		}
	}
	t.Fatalf("registry has no root %s", name)
}

// reversedRegistry returns the fixture registry with the package and root
// order inverted: the digest must be canonical over the graph, not over the
// registry's authoring order.
func reversedRegistry() *registry.Registry {
	base := fixtureRegistry()
	pkgs := make([]registry.Package, 0, len(base.Packages))
	for i := len(base.Packages) - 1; i >= 0; i-- {
		p := base.Packages[i]
		roots := make([]registry.Root, 0, len(p.Roots))
		for j := len(p.Roots) - 1; j >= 0; j-- {
			roots = append(roots, p.Roots[j])
		}
		p.Roots = roots
		pkgs = append(pkgs, p)
	}
	base.Packages = pkgs
	return base
}

func buildDigest(t *testing.T, dir string, reg *registry.Registry) string {
	t.Helper()
	g, err := Build(Config{Dir: dir, Registry: reg})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g.Digest()
}

func TestDigestIsDeterministicAndCanonical(t *testing.T) {
	root := repoRoot(t)
	base := buildDigest(t, root, fixtureRegistry())
	if len(base) != len("sha256:")+64 || base[:7] != "sha256:" {
		t.Fatalf("digest %q is not \"sha256:\" plus 64 hex digits", base)
	}
	if again := buildDigest(t, root, fixtureRegistry()); again != base {
		t.Errorf("two builds of the same registry differ: %s vs %s", base, again)
	}
	if reversed := buildDigest(t, root, reversedRegistry()); reversed != base {
		t.Errorf("reordering packages and roots changed the digest: %s vs %s", base, reversed)
	}
}

// TestDigestIgnoresBloemFieldOrder: bloem_fields is a set; the registry may
// spell it in any order without changing the contract digest.
func TestDigestIgnoresBloemFieldOrder(t *testing.T) {
	root := repoRoot(t)
	forward := fixtureRegistry()
	setRoot(t, forward, "Compat", func(r *registry.Root) { r.BloemFields = []string{"promo", "plain"} })
	want := buildDigest(t, root, forward)

	backward := fixtureRegistry()
	setRoot(t, backward, "Compat", func(r *registry.Root) { r.BloemFields = []string{"plain", "promo"} })
	if got := buildDigest(t, root, backward); got != want {
		t.Errorf("bloem_fields spelling order changed the digest: %s vs %s", want, got)
	}
}

// TestDigestSensitivity pins that the digest moves when the contract moves:
// a field remapped to a client-side serializer changes its wire kind, a new
// gate changes what may decode a type, and a new direction changes who may
// encode it.
func TestDigestSensitivity(t *testing.T) {
	root := repoRoot(t)
	base := buildDigest(t, root, fixtureRegistry())

	t.Run("serializer remaps a field to Custom", func(t *testing.T) {
		reg := fixtureRegistry()
		reg.Serializers[fixturePath+".Response.shared"] = registry.Serializer{"kotlin": "org.example.SharedWire", "swift": "SharedWire"}
		if got := buildDigest(t, root, reg); got == base {
			t.Error("adding a serializer (field becomes KindCustom) must change the digest")
		}
	})
	t.Run("new gate on a root", func(t *testing.T) {
		reg := fixtureRegistry()
		setRoot(t, reg, "Scalars", func(r *registry.Root) { r.Gate = "cap.new" })
		if got := buildDigest(t, root, reg); got == base {
			t.Error("gating a root must change the digest")
		}
	})
	t.Run("new direction on a root", func(t *testing.T) {
		reg := fixtureRegistry()
		setRoot(t, reg, "Scalars", func(r *registry.Root) { r.Direction = registry.DirectionBoth })
		if got := buildDigest(t, root, reg); got == base {
			t.Error("widening a root's direction must change the digest")
		}
	})
	t.Run("new bloem_fields entry", func(t *testing.T) {
		reg := fixtureRegistry()
		setRoot(t, reg, "Compat", func(r *registry.Root) { r.BloemFields = append(r.BloemFields, "plain") })
		if got := buildDigest(t, root, reg); got == base {
			t.Error("marking a field bloem must change the digest")
		}
	})
}

// TestRealTreeDigestIsDeterministic: the committed registry produces the same
// digest on every build — the precondition for pinning it in
// contracts/client/v1/digest.txt.
func TestRealTreeDigestIsDeterministic(t *testing.T) {
	root := repoRoot(t)
	reg, err := registry.Load(filepath.Join(root, "contracts", "client", "v1", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	first := buildDigest(t, root, reg)
	second := buildDigest(t, root, reg)
	if first != second {
		t.Fatalf("real-tree digest differs between builds: %s vs %s", first, second)
	}
}
