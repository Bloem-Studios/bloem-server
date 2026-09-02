// Package coverage carries the coverage allowlist
// (contracts/client/v1/coverage.json) that closes the §8 coverage-drift loop:
// every exported struct with json tags in a registered package must either be
// reached from a registry root or carry a written reason here, and every v2
// model file of the §9.1 migration inventory must have a generated counterpart
// or a written reason.
//
// A reason is a sentence that says why the server does not generate the shape
// ("admin dashboard payload served to the bundled web UI", "client-only view
// state"). Placeholder reasons — "TODO", "not yet" — are rejected at load, so
// the allowlist cannot quietly become a backlog.
package coverage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// SchemaVersion is the only allowlist format version this loader accepts.
const SchemaVersion = 1

// Allowlist is the parsed coverage.json.
type Allowlist struct {
	Schema int         `json:"schema"`
	Files  []FileEntry `json:"files"`
	Types  []TypeEntry `json:"types"`
}

// FileEntry is one entry of the §9.1 migration inventory. File is the v2
// model file the spec names, or — where the spec speaks per domain rather
// than per file ("by package file: catalog, section, personal, …") — that
// domain's model-file group. Exactly one of GeneratedFrom and Reason is set:
// the generated counterpart comes from the named registered packages' roots;
// a reason says why the server does not generate it.
type FileEntry struct {
	File string `json:"file"`
	// GeneratedFrom lists registered packages whose registry roots produce
	// the counterpart.
	GeneratedFrom []string `json:"generated_from,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// TypeEntry is one coverage-drift exception: a graph key
// ("<package path>.<Go name>") of an exported, tagged struct in a registered
// package that is neither a root nor reached from one.
type TypeEntry struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// LoadFile reads and validates the allowlist at path.
func LoadFile(path string, reg *registry.Registry) (*Allowlist, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	allow, err := Load(raw, reg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return allow, nil
}

// Load validates raw against the semantic rules the JSON schema cannot
// express: entries are unique, every file entry picks exactly one of
// generated counterpart and written reason, generated_from names registered
// packages, and no reason is a placeholder.
func Load(raw []byte, reg *registry.Registry) (*Allowlist, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var allow Allowlist
	if err := dec.Decode(&allow); err != nil {
		return nil, fmt.Errorf("decoding coverage allowlist: %w", err)
	}
	if allow.Schema != SchemaVersion {
		return nil, fmt.Errorf("unsupported coverage allowlist schema %d: want %d", allow.Schema, SchemaVersion)
	}
	registered := map[string]bool{}
	for _, p := range reg.Packages {
		registered[p.Path] = true
	}

	files := map[string]bool{}
	for i, f := range allow.Files {
		if f.File == "" {
			return nil, fmt.Errorf("files[%d]: empty file name", i)
		}
		if files[f.File] {
			return nil, fmt.Errorf("file %q listed twice", f.File)
		}
		files[f.File] = true
		switch {
		case len(f.GeneratedFrom) > 0 && f.Reason != "":
			return nil, fmt.Errorf("file %q: generated_from and reason are mutually exclusive", f.File)
		case len(f.GeneratedFrom) == 0 && f.Reason == "":
			return nil, fmt.Errorf("file %q: set generated_from or a written reason", f.File)
		case len(f.GeneratedFrom) > 0:
			for _, pkg := range f.GeneratedFrom {
				if !registered[pkg] {
					return nil, fmt.Errorf("file %q: generated_from names %q, which is not a registered package", f.File, pkg)
				}
			}
		default:
			if err := checkReason("file "+f.File, f.Reason); err != nil {
				return nil, err
			}
		}
	}

	types := map[string]bool{}
	for i, t := range allow.Types {
		if t.Type == "" {
			return nil, fmt.Errorf("types[%d]: empty type key", i)
		}
		if types[t.Type] {
			return nil, fmt.Errorf("type %q listed twice", t.Type)
		}
		types[t.Type] = true
		if !strings.Contains(t.Type, ".") {
			return nil, fmt.Errorf("type %q: want the graph key \"<package path>.<Go name>\"", t.Type)
		}
		if err := checkReason("type "+t.Type, t.Reason); err != nil {
			return nil, err
		}
	}
	return &allow, nil
}

// placeholders are the reason spellings that would turn the allowlist into a
// backlog instead of a record of decisions.
var placeholders = []string{
	"todo", "tbd", "fixme", "wip", "not yet", "for now",
	"eventually", "placeholder", "to be decided", "no reason",
}

func checkReason(entry, reason string) error {
	lower := strings.ToLower(reason)
	for _, p := range placeholders {
		if strings.Contains(lower, p) {
			return fmt.Errorf("%s: reason %q contains %q; state why the shape is not server-generated", entry, reason, p)
		}
	}
	if len(reason) < 20 || !strings.Contains(reason, " ") || !strings.HasSuffix(reason, ".") {
		return fmt.Errorf("%s: reason %q is not a full sentence", entry, reason)
	}
	return nil
}

// Check compares the graph's coverage drift against the allowlist and returns
// an error joining every finding so the author sees the whole picture at
// once: unreached wire shapes missing from the allowlist, and allowlist
// entries that have gone stale because the type is now reached or deleted.
func Check(g *graph.Graph, allow *Allowlist) error {
	unreached := map[string]bool{}
	for _, p := range g.Packages {
		if !p.Registered {
			continue
		}
		for _, name := range p.Unreached {
			unreached[p.Path+"."+name] = true
		}
	}

	var errs []error
	allowed := map[string]bool{}
	for _, t := range allow.Types {
		allowed[t.Type] = true
		if !unreached[t.Type] {
			errs = append(errs, fmt.Errorf("stale allowlist entry %s: the type is reached from a root now (or gone); delete the entry", t.Type))
		}
	}
	var missing []string
	for key := range unreached {
		if !allowed[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		errs = append(errs, fmt.Errorf("unreached wire type %s is neither registered nor allowlisted: register it if clients decode it, or give it a written reason in the coverage allowlist", key))
	}
	return errors.Join(errs...)
}
