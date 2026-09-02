package graph

import (
	"fmt"
	"io"
	"strings"
)

// Dump writes a deterministic textual rendering of the graph: packages in
// Graph.Packages order, types by name, fields in declaration order, constants
// in source order. Two builds of the same tree dump identical bytes.
func (g *Graph) Dump(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n", g.ModulePath)
	for _, p := range g.Packages {
		fmt.Fprintf(&b, "package %s", p.Path)
		if p.Registered {
			fmt.Fprintf(&b, " registered dialect=%s", p.Dialect)
			if p.Gate != "" {
				fmt.Fprintf(&b, " gate=%s", p.Gate)
			}
		} else {
			b.WriteString(" reached")
		}
		b.WriteByte('\n')
		for _, t := range p.Types {
			dumpType(&b, t)
		}
		if len(p.Unreached) > 0 {
			fmt.Fprintf(&b, "  unreached %s\n", strings.Join(p.Unreached, " "))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func dumpType(b *strings.Builder, t *Type) {
	fmt.Fprintf(b, "  %s %s direction=%s dialect=%s", strings.ToLower(t.Kind.String()), t.Name, t.Direction, t.Dialect)
	if len(t.Gates) > 0 {
		fmt.Fprintf(b, " gate=%s", strings.Join(t.Gates, ","))
	}
	if t.Root {
		b.WriteString(" root")
	}
	if len(t.BloemFields) > 0 {
		fmt.Fprintf(b, " bloem_fields=%s", strings.Join(t.BloemFields, ","))
	}
	b.WriteByte('\n')
	for _, f := range t.Fields {
		fmt.Fprintf(b, "    %s %s %s", f.WireName, f.GoName, f.Type)
		if f.OmitEmpty {
			b.WriteString(" omitempty")
		}
		if f.Dialect != t.Dialect {
			fmt.Fprintf(b, " dialect=%s", f.Dialect)
		}
		if f.Serializer != "" {
			fmt.Fprintf(b, " serializer=%s", f.Serializer)
		}
		if f.PromotedFrom != "" {
			fmt.Fprintf(b, " from=%s", f.PromotedFrom)
		}
		b.WriteByte('\n')
	}
	for _, c := range t.Constants {
		fmt.Fprintf(b, "    %s = %q\n", c.GoName, c.Value)
	}
}
