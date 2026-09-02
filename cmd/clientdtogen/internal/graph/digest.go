package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Digest returns "sha256:<hex>" over the normalised type graph: every reached
// type, sorted by key, with its kind, direction, dialect, gates, fields (in
// declaration order, each with wire name, mapped kind, nullability, omitempty,
// dialect, serializer set and promotion origin) and enum constants (in source
// order). Registry bookkeeping that does not change a wire shape — adding a
// package with no new reachable types, or a coverage-allowlist entry — leaves
// it unchanged; any field, type, direction or vocabulary change alters it
// (docs/specs/client-dto-generator.md §7.2).
func (g *Graph) Digest() string {
	keys := make([]string, 0, len(g.types))
	for key := range g.types {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		t := g.types[key]
		fmt.Fprintf(&b, "type %s kind=%s direction=%s dialect=%s root=%t", key, t.Kind, t.Direction, t.Dialect, t.Root)
		if len(t.Gates) > 0 {
			fmt.Fprintf(&b, " gates=%s", strings.Join(t.Gates, ","))
		}
		if len(t.BloemFields) > 0 {
			// The registry lists bloem_fields in author order; the digest is
			// canonical, so the set is what counts.
			sorted := append([]string(nil), t.BloemFields...)
			sort.Strings(sorted)
			fmt.Fprintf(&b, " bloem_fields=%s", strings.Join(sorted, ","))
		}
		b.WriteByte('\n')
		for _, f := range t.Fields {
			fmt.Fprintf(&b, "  field %s %s %s omitempty=%t dialect=%s promoted_from=%s",
				f.GoName, f.WireName, f.Type, f.OmitEmpty, f.Dialect, f.PromotedFrom)
			if len(f.Serializers) > 0 {
				langs := make([]string, 0, len(f.Serializers))
				for lang := range f.Serializers {
					langs = append(langs, lang)
				}
				sort.Strings(langs)
				pairs := make([]string, 0, len(langs))
				for _, lang := range langs {
					pairs = append(pairs, lang+"="+f.Serializers[lang])
				}
				fmt.Fprintf(&b, " serializers=%s", strings.Join(pairs, ","))
			}
			b.WriteByte('\n')
		}
		for _, c := range t.Constants {
			fmt.Fprintf(&b, "  const %s = %q\n", c.GoName, c.Value)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}
