// Package emit defines the language-neutral emitter contract the client DTO
// generator drives: an Emitter turns the type graph into a set of files for
// one target language (docs/specs/client-dto-generator.md §5, §11 Q8).
//
// Everything a target shares — the property-naming rule (§4.3), the enum
// constant rule (§4.2), the provenance header facts (§5.2) — lives here so a
// second emitter (Swift, chunk C7) reuses it instead of re-deriving it. The
// graph decides nothing per language; each emitter maps graph.Kind rows to its
// own types and derives nullability/defaults from the owning type's direction
// (§4.4).
package emit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
)

// GeneratorVersion is the generator's output-shape version. It changes only
// when the emitted shape changes for an unchanged graph, so a client can tell
// a regeneration from a contract change (§5.2, §5.3).
const GeneratorVersion = 1

// Options carries the provenance facts every emitter stamps into its output.
type Options struct {
	// ServerRevision is the full git SHA of the server tree the graph was
	// built from. It is the one non-deterministic input (§5.2).
	ServerRevision string
	// GeneratorVersion is emitted into the header and GeneratedContract;
	// zero means GeneratorVersion.
	GeneratorVersion int
	// RegistryPath is the repository-relative registry file named in the
	// header (contracts/client/v1/registry.json).
	RegistryPath string
	// OutputRoot is the directory the files are written under. Emitters
	// produce paths relative to it; Files.Write joins them.
	OutputRoot string
}

// EffectiveGeneratorVersion returns the version emitters stamp: the option
// when set, else GeneratorVersion.
func (o Options) EffectiveGeneratorVersion() int {
	if o.GeneratorVersion == 0 {
		return GeneratorVersion
	}
	return o.GeneratorVersion
}

// File is one emitted file. Path is slash-separated and relative to
// Options.OutputRoot.
type File struct {
	Path    string
	Content []byte
}

// Files is an emitter's output, sorted by Path.
type Files []File

// Sort orders the files by path so two emits compare positionally.
func (f Files) Sort() { sort.Slice(f, func(i, j int) bool { return f[i].Path < f[j].Path }) }

// Equal reports whether two outputs are byte-identical, path by path.
func (f Files) Equal(other Files) bool {
	if len(f) != len(other) {
		return false
	}
	for i := range f {
		if f[i].Path != other[i].Path || !bytes.Equal(f[i].Content, other[i].Content) {
			return false
		}
	}
	return true
}

// Write writes every file under root, creating directories as needed. It
// refuses a path that escapes root.
func (f Files) Write(root string) error {
	if root == "" {
		return fmt.Errorf("emit: no output root")
	}
	for _, file := range f {
		rel := filepath.FromSlash(file.Path)
		if file.Path == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("emit: refusing to write %q outside %s", file.Path, root)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, file.Content, 0o644); err != nil { //nolint:gosec // generated source, not a secret
			return err
		}
	}
	return nil
}

// Emitter renders the graph for one target language. Implementations must be
// deterministic: two calls over the same graph and options return equal
// Files. They fail rather than guess when the graph carries something the
// language cannot express (a Custom field without a serializer for the
// language, an identifier collision).
type Emitter interface {
	// Language is the -lang value that selects the emitter.
	Language() string
	Emit(g *graph.Graph, opts Options) (Files, error)
}
