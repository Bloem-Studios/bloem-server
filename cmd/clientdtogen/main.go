// Command clientdtogen emits the client DTO set from the Go wire types the
// registry (contracts/client/v1/registry.json) names.
//
// This version builds and dumps the language-neutral type graph; language
// emitters attach to the same graph in later chunks
// (docs/specs/client-dto-generator.md §10).
//
// Usage:
//
//	clientdtogen -dump                       # dump the graph for the default registry
//	clientdtogen -digest-file contracts/client/v1/digest.txt
//	clientdtogen -registry path/to/registry.json -root path/to/repo -dump
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/digestfile"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

const defaultRegistry = "contracts/client/v1/registry.json"

const defaultDigestFile = "contracts/client/v1/digest.txt"

func main() {
	root := flag.String("root", "", "repository root (default: the module root above the working directory)")
	registryPath := flag.String("registry", "", "registry file (default: "+defaultRegistry+" under -root)")
	dump := flag.Bool("dump", false, "print the type graph")
	digestFile := flag.String("digest-file", "", "pin the type-graph digest and the v1-scope removals-table digest to this file, e.g. "+defaultDigestFile)
	flag.Parse()

	if *root == "" {
		found, err := findModuleRoot()
		if err != nil {
			fail("%v", err)
		}
		*root = found
	}
	if *registryPath == "" {
		*registryPath = filepath.Join(*root, defaultRegistry)
	}

	reg, err := registry.Load(*registryPath)
	if err != nil {
		fail("%v", err)
	}
	g, err := graph.Build(graph.Config{Dir: *root, Registry: reg})
	if err != nil {
		fail("building type graph:\n%v", err)
	}

	switch {
	case *digestFile != "":
		pinDigest(*root, *digestFile, g)
	case *dump:
		if err := g.Dump(os.Stdout); err != nil {
			fail("%v", err)
		}
	default:
		fail("nothing to do: no emitter is wired yet; pass -dump to inspect the graph or -digest-file to pin the contract digest")
	}
}

// pinDigest writes digest.txt: the graph digest next to the current digest of
// the pre-lock removals table (§7.2).
func pinDigest(root, path string, g *graph.Graph) {
	scope, err := os.ReadFile(filepath.Join(root, digestfile.ScopeFile))
	if err != nil {
		fail("reading %s: %v", digestfile.ScopeFile, err)
	}
	table, err := digestfile.TableDigest(scope)
	if err != nil {
		fail("%v", err)
	}
	pins := digestfile.Pins{Contract: g.Digest(), RemovalsTable: table}
	if err := digestfile.Write(path, pins); err != nil {
		fail("writing %s: %v", path, err)
	}
	fmt.Printf("pinned %s\n  contract:       %s\n  removals-table: %s\n", path, pins.Contract, pins.RemovalsTable)
}

// findModuleRoot walks up from the working directory to the nearest go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s; pass -root", dir)
		}
		dir = parent
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "clientdtogen: "+format+"\n", args...)
	os.Exit(1)
}
