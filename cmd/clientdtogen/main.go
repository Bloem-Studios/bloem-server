// Command clientdtogen emits the client DTO set from the Go wire types the
// registry (contracts/client/v1/registry.json) names.
//
// It builds the language-neutral type graph and hands it to the emitter
// selected by -lang (docs/specs/client-dto-generator.md §5, §6.1).
//
// Usage:
//
//	clientdtogen -dump                                   # dump the graph for the default registry
//	clientdtogen -lang kotlin -out contracts/client/v1/kotlin -server-revision $(git rev-parse HEAD)
//	clientdtogen -registry path/to/registry.json -root path/to/repo -dump
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/emit"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/emit/kotlin"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

const defaultRegistry = "contracts/client/v1/registry.json"

// emitters lists the languages -lang accepts. Swift attaches here (§11 Q8).
var emitters = []emit.Emitter{kotlin.Emitter{}}

func lookupEmitter(lang string) (emit.Emitter, bool) {
	for _, e := range emitters {
		if e.Language() == lang {
			return e, true
		}
	}
	return nil, false
}

func languages() []string {
	out := make([]string, 0, len(emitters))
	for _, e := range emitters {
		out = append(out, e.Language())
	}
	return out
}

func main() {
	root := flag.String("root", "", "repository root (default: the module root above the working directory)")
	registryPath := flag.String("registry", "", "registry file (default: "+defaultRegistry+" under -root)")
	dump := flag.Bool("dump", false, "print the type graph")
	lang := flag.String("lang", "", "target language to emit ("+strings.Join(languages(), ", ")+")")
	out := flag.String("out", "", "output directory for -lang (required with -lang)")
	serverRevision := flag.String("server-revision", "", "server git SHA stamped into the generated header")
	flag.Parse()

	var emitter emit.Emitter
	if *lang != "" {
		found, ok := lookupEmitter(*lang)
		if !ok {
			fail("unknown -lang %q: want one of %s", *lang, strings.Join(languages(), ", "))
		}
		if *out == "" {
			fail("-lang %s needs -out", *lang)
		}
		emitter = found
	}

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

	if *dump {
		if err := g.Dump(os.Stdout); err != nil {
			fail("%v", err)
		}
	}
	if emitter == nil {
		if !*dump {
			fail("nothing to do: pass -lang <language> -out <dir>, or -dump to inspect the graph")
		}
		return
	}
	registryRel, err := filepath.Rel(*root, *registryPath)
	if err != nil || strings.HasPrefix(registryRel, "..") {
		registryRel = *registryPath
	}
	files, err := emitter.Emit(g, emit.Options{
		ServerRevision: *serverRevision,
		RegistryPath:   filepath.ToSlash(registryRel),
		OutputRoot:     *out,
	})
	if err != nil {
		fail("%v", err)
	}
	if err := files.Write(*out); err != nil {
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
