package graph

import (
	"sort"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// walkCtx is the registry metadata flowing from a root along field edges.
type walkCtx struct {
	dir     registry.Direction
	dialect registry.Dialect
	gate    string // "" = ungated
}

// typeState accumulates what every path into a type contributed.
type typeState struct {
	dir          registry.Direction
	upstreamSeen bool
	ungatedSeen  bool
	gates        map[string]bool
}

type visitKey struct {
	typ string
	ctx walkCtx
}

// propagate walks from every root and records direction, dialect and gates on
// each reached type (§3.2, §4.4, §7.3):
//
//   - direction is the union over all reaching roots;
//   - dialect is upstream-compat if any path is upstream-compat, else bloem,
//     where crossing a bloem_fields field turns the path bloem;
//   - gates are the set of gates over all paths, cleared entirely when any
//     path is ungated.
func (b *builder) propagate() {
	states := map[string]*typeState{}
	visited := map[visitKey]bool{}

	var walk func(t *Type, ctx walkCtx)
	walk = func(t *Type, ctx walkCtx) {
		vk := visitKey{typ: t.Key(), ctx: ctx}
		if visited[vk] {
			return
		}
		visited[vk] = true

		st := states[t.Key()]
		if st == nil {
			st = &typeState{gates: map[string]bool{}}
			states[t.Key()] = st
		}
		st.dir |= ctx.dir
		if ctx.dialect == registry.DialectUpstreamCompat {
			st.upstreamSeen = true
		}
		if ctx.gate == "" {
			st.ungatedSeen = true
		} else {
			st.gates[ctx.gate] = true
		}

		for _, f := range t.Fields {
			fctx := ctx
			if containsString(t.BloemFields, f.WireName) {
				fctx.dialect = registry.DialectBloem
			}
			for _, key := range namedRefs(f.Type) {
				walk(b.types[key], fctx)
			}
		}
	}

	for _, r := range b.roots {
		walk(r.typ, walkCtx{
			dir:     r.root.Direction,
			dialect: r.pkg.EffectiveDialect(r.root),
			gate:    r.pkg.EffectiveGate(r.root),
		})
	}

	for key, t := range b.types {
		st := states[key]
		if st == nil {
			continue // cannot happen: every type is reached from a root
		}
		t.Direction = st.dir
		t.Dialect = registry.DialectBloem
		if st.upstreamSeen {
			t.Dialect = registry.DialectUpstreamCompat
		}
		if !st.ungatedSeen {
			for g := range st.gates {
				t.Gates = append(t.Gates, g)
			}
			sort.Strings(t.Gates)
		}
		for i := range t.Fields {
			t.Fields[i].Dialect = t.Dialect
			if containsString(t.BloemFields, t.Fields[i].WireName) {
				t.Fields[i].Dialect = registry.DialectBloem
			}
		}
	}
}

// namedRefs lists the graph keys a type reference points at, through lists
// and maps.
func namedRefs(r TypeRef) []string {
	switch r.Kind {
	case KindStruct, KindEnum:
		return []string{r.Named}
	case KindList, KindMap:
		if r.Elem != nil {
			return namedRefs(*r.Elem)
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
