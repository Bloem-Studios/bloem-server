// Package graph builds the language-neutral wire-type graph the client DTO
// emitters consume.
//
// Build loads the registered Go packages with go/packages, resolves every
// registered root, walks the type graph transitively, and records for each
// reached named type its fields (encoding/json semantics: tag names,
// omitempty/omitzero, embedded promotion, "-" and unexported fields dropped),
// its string-constant vocabulary, and the registry metadata that propagates
// along the walk (direction, dialect, capability gates). Anything encoding/json
// would handle in a way the contract cannot state is refused with an error
// naming the Go type and field rather than guessed (docs/specs/client-dto-generator.md §3.2).
//
// The graph carries no target-language decisions: Kind names the §4.1 mapping
// row, and nullability/defaults are for the emitter to derive from Nullable,
// OmitEmpty and the owning type's Direction (§4.4).
package graph

import (
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// Kind is the §4.1 mapping row a type reference falls in.
type Kind uint8

// Kinds. Scalar kinds carry no Elem or Named; List and Map carry Elem (map
// keys are always strings); Struct and Enum carry Named.
const (
	KindInvalid Kind = iota
	KindString       // string, named string types without constants
	KindBool         // bool
	KindInt          // int, int8, int16, int32, uint8, uint16, uint32
	KindLong         // int64, uint64
	KindDouble       // float32, float64
	KindTime         // time.Time (RFC 3339 string on the wire)
	KindUUID         // github.com/google/uuid.UUID (string on the wire)
	KindBytes        // []byte (base64 string on the wire)
	KindRaw          // json.RawMessage, any: an arbitrary JSON value, always nullable
	KindList         // []T, [N]T
	KindMap          // map[string]T
	KindStruct       // a named struct type in the graph
	KindEnum         // a named string type with exported constants in the graph
	KindCustom       // a field with a registry serializer; the client owns the shape
)

var kindNames = [...]string{
	KindInvalid: "invalid",
	KindString:  "String",
	KindBool:    "Bool",
	KindInt:     "Int",
	KindLong:    "Long",
	KindDouble:  "Double",
	KindTime:    "Time",
	KindUUID:    "UUID",
	KindBytes:   "Bytes",
	KindRaw:     "Raw",
	KindList:    "List",
	KindMap:     "Map",
	KindStruct:  "Struct",
	KindEnum:    "Enum",
	KindCustom:  "Custom",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", k)
}

// IsScalar reports whether k is a single wire value with no element type and
// no named type behind it.
func (k Kind) IsScalar() bool {
	switch k {
	case KindString, KindBool, KindInt, KindLong, KindDouble, KindTime, KindUUID, KindBytes:
		return true
	}
	return false
}

// TypeRef is a resolved reference to a type as used by a field or as an
// element of a list or map.
type TypeRef struct {
	Kind Kind
	// Nullable is set when the Go type is a pointer at this level, and always
	// for KindRaw (Go writes null for a nil RawMessage and a nil interface).
	Nullable bool
	// Elem is the element type for KindList and the value type for KindMap.
	Elem *TypeRef
	// Named is the graph key ("<repo path>.<Go name>") for KindStruct and
	// KindEnum; look it up with Graph.Type.
	Named string
}

// String renders the reference in the dump notation, e.g. "List<Struct
// internal/playback.PlanV3>?".
func (r TypeRef) String() string {
	var b strings.Builder
	b.WriteString(r.Kind.String())
	switch r.Kind {
	case KindList, KindMap:
		b.WriteByte('<')
		if r.Elem != nil {
			b.WriteString(r.Elem.String())
		}
		b.WriteByte('>')
	case KindStruct, KindEnum:
		b.WriteByte(' ')
		b.WriteString(r.Named)
	}
	if r.Nullable {
		b.WriteByte('?')
	}
	return b.String()
}

// Field is one wire field of a struct type, after embedded promotion.
type Field struct {
	// GoName is the Go field identifier; the emitter's property-naming rule
	// (§4.3) starts from it, never from WireName.
	GoName string
	// WireName is the json tag name, byte for byte.
	WireName string
	Type     TypeRef
	// OmitEmpty is set for ",omitempty" and ",omitzero": the key may be absent
	// on the wire.
	OmitEmpty bool
	// Dialect is bloem when the registry lists the field in bloem_fields of an
	// upstream-compat root; otherwise it is the owning type's dialect.
	Dialect registry.Dialect
	// Serializer is the client-side serializer class from the registry, set
	// only when Type.Kind == KindCustom.
	Serializer string
	// PromotedFrom is the graph key of the embedded struct this field was
	// inlined from, or "" for a field declared directly on the type.
	PromotedFrom string
}

// Constant is one exported typed constant of an enum type.
type Constant struct {
	// GoName is the Go constant identifier, e.g. StreamProtocolHLSV3.
	GoName string
	// Value is the wire string.
	Value string
}

// Type is a named Go type in the graph: a struct or a string enumeration.
type Type struct {
	// Name is the Go type name, verbatim (may be unexported).
	Name string
	// Package owns the type.
	Package *Package
	// Kind is KindStruct or KindEnum.
	Kind Kind
	// Fields, for structs, in Go declaration order with embedded structs
	// inlined at the embed point.
	Fields []Field
	// Constants, for enums, in Go source order.
	Constants []Constant
	// Direction is the union of the directions of every root that reaches the
	// type. Response() true means "response-reachable" in §4.4.
	Direction registry.Direction
	// Dialect is upstream-compat when any path from a root reaches the type
	// without crossing a bloem root or a bloem_fields field; otherwise bloem.
	Dialect registry.Dialect
	// Gates lists the capability gates guarding the type, sorted. Empty when
	// any reaching root is ungated (or reaches it through an ungated path).
	Gates []string
	// Root is set when the registry lists the type directly.
	Root bool
	// BloemFields, for roots, is the registry's bloem_fields list.
	BloemFields []string
}

// Key returns the graph key "<repo path>.<Go name>".
func (t *Type) Key() string { return typeKey(t.Package.Path, t.Name) }

func typeKey(pkgPath, name string) string { return pkgPath + "." + name }

// Package groups the graph's types by Go package.
type Package struct {
	// Path is the repository-relative Go package path (internal/playback).
	Path string
	// ImportPath is the full Go import path.
	ImportPath string
	// Registered is true for packages listed in the registry; a package that is
	// only reached transitively (an alias target, an imported struct) is false.
	Registered bool
	// Dialect and Gate are the registry package defaults (registered only).
	Dialect registry.Dialect
	Gate    string
	// Types sorted by Go name.
	Types []*Type
	// Unreached lists exported struct types in a registered package that carry
	// json tags but are neither roots nor reached from one — the coverage-drift
	// signal of §8. Sorted.
	Unreached []string
}

// Graph is the result of Build.
type Graph struct {
	// ModulePath is the Go module the registered packages live in.
	ModulePath string
	// Packages lists registered packages in registry order followed by reached
	// unregistered packages sorted by path.
	Packages []*Package
	types    map[string]*Type
}

// Type looks a type up by its graph key.
func (g *Graph) Type(key string) (*Type, bool) {
	t, ok := g.types[key]
	return t, ok
}

// Resolve follows a KindStruct/KindEnum reference to its type.
func (g *Graph) Resolve(ref TypeRef) (*Type, bool) {
	if ref.Named == "" {
		return nil, false
	}
	return g.Type(ref.Named)
}

// Types returns every type in Packages order, then by Go name.
func (g *Graph) Types() []*Type {
	var out []*Type
	for _, p := range g.Packages {
		out = append(out, p.Types...)
	}
	return out
}
