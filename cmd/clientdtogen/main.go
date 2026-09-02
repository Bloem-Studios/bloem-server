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
//	clientdtogen -registry path/to/registry.json -root path/to/repo -dump
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

const defaultRegistry = "contracts/client/v1/registry.json"

func main() {
	root := flag.String("root", "", "repository root (default: the module root above the working directory)")
	registryPath := flag.String("registry", "", "registry file (default: "+defaultRegistry+" under -root)")
	dump := flag.Bool("dump", false, "print the type graph")
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

	if !*dump {
		fail("nothing to do: no emitter is wired yet; pass -dump to inspect the graph")
	}
	if err := g.Dump(os.Stdout); err != nil {
		fail("%v", err)
	}
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
