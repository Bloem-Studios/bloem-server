// Package swift emits the client DTO set as Swift `Codable` value types
// (docs/specs/client-dto-generator.md §4, §5, §10 C7). It is the second
// emitter behind the language-neutral graph; every semantic decision mirrors
// internal/emit/kotlin unless Swift forces otherwise, and each forced
// divergence is commented where it is taken.
//
// The output shape is a provenance requirement (§2): every declaration line
// carries explicit public, every property has its wire name in a CodingKeys
// case of its own, defaults and nullability follow §4.4 by rule, and the only
// func in a generated file is the mechanical `encode(to:)`.
package swift

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/emit"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// Language is the -lang value that selects this emitter.
const Language = "swift"

// ContractFile is the file holding the GeneratedContract enum (§5.3).
const ContractFile = "GeneratedContract.swift"

// RawType names the client-owned JSON tree type the generated code refers to
// for json.RawMessage and any (§4.1). Kotlin has kotlinx.serialization's
// JsonElement; Swift's standard library has no JSON value type, so the client
// owns one, exactly as it owns the registry serializers (§9.2). It must be
// `Codable`, `Hashable` and `Sendable`.
const RawType = "BloemJSONValue"

// IndirectType names the client-owned box that breaks a self-referential
// value type. Swift structs are stored inline, so `struct Child { let next:
// Child? }` is rejected by the compiler ("value type cannot have a stored
// property that recursively contains it"); Kotlin has no such rule. Fields on
// a containment cycle are therefore stored through this box and exposed by a
// computed property of the honest type. It must be a generic value type with
// `init(_ value: Value)`, a `value` property, and conditional `Hashable` /
// `Sendable` conformances.
const IndirectType = "Indirect"

// Keywords are the Swift words that cannot appear as a bare declaration name;
// a property name that lands on one gets a Value suffix (§4.3 step 3), the
// same repair Kotlin makes. Contextual keywords (any, some, open, lazy,
// final, get, set, …) are legal identifiers and stay as they are. Swift's
// backtick escape is deliberately not used: the identifier a client types
// should be greppable and should read the same on both clients.
var Keywords = map[string]bool{
	"as": true, "associatedtype": true, "await": true, "break": true, "case": true,
	"catch": true, "class": true, "continue": true, "default": true, "defer": true,
	"deinit": true, "do": true, "else": true, "enum": true, "extension": true,
	"fallthrough": true, "false": true, "fileprivate": true, "for": true, "func": true,
	"guard": true, "if": true, "import": true, "in": true, "init": true, "inout": true,
	"internal": true, "is": true, "let": true, "nil": true, "operator": true,
	"precedencegroup": true, "private": true, "protocol": true, "public": true,
	"repeat": true, "rethrows": true, "return": true, "self": true, "static": true,
	"struct": true, "subscript": true, "super": true, "switch": true, "throw": true,
	"throws": true, "true": true, "try": true, "typealias": true, "var": true,
	"where": true, "while": true,
}

// PropertyName applies the §4.3 rule for Swift.
func PropertyName(goName string) string { return emit.CamelCase(goName, Keywords) }

// ReservedTypeNames are the names Swift refuses for a type nested in another
// type: Protocol collides with the `X.Protocol` metatype expression, and Self
// and Any are keywords. Renaming would break the rule that every generated
// type name is a Go type name, so the emitter escapes them with backticks
// instead — the escape the compiler itself suggests. Verified against
// swiftc 6.3: Type nests without complaint and is deliberately absent.
var ReservedTypeNames = map[string]bool{"Protocol": true, "Self": true, "Any": true}

// TypeName renders a Go type or namespace name as a Swift identifier,
// backtick-escaped when Swift reserves it.
func TypeName(name string) string {
	if ReservedTypeNames[name] {
		return "`" + name + "`"
	}
	return name
}

// Emitter renders a graph as Swift. The zero value is ready to use.
type Emitter struct{}

// Language implements emit.Emitter.
func (Emitter) Language() string { return Language }

// Emit implements emit.Emitter.
func (Emitter) Emit(g *graph.Graph, opts emit.Options) (emit.Files, error) {
	e := &emitter{g: g, opts: opts, types: map[string]typeRef{}, boxed: map[string]bool{}}
	if err := e.plan(); err != nil {
		return nil, err
	}
	var files emit.Files
	for _, p := range g.Packages {
		if len(p.Types) == 0 {
			continue
		}
		content, err := e.packageFile(p)
		if err != nil {
			return nil, err
		}
		ns := e.namespaces[p.Path]
		files = append(files, emit.File{Path: ns.dir + "/" + ns.file, Content: content})
	}
	contract, err := e.contractFile()
	if err != nil {
		return nil, err
	}
	files = append(files, emit.File{Path: ContractFile, Content: contract})
	files.Sort()
	return files, nil
}

// namespace is the caseless enum one Go package's types are nested in. Swift
// has no per-directory namespace, so where Kotlin writes `package
// org.bloemserver.bloem.contract.playback` this emitter writes `public enum
// Playback { … }` — without it the 319 types of the real registry would share
// one flat name space, and two of them are already called Status.
type namespace struct {
	name string // Playback
	dir  string // playback (last Go path element), mirroring the Kotlin layout
	file string // Playback.swift
}

type typeRef struct {
	ns     string // namespace enum name
	simple string // Go type name verbatim
}

// qualified is the table key and the reader-facing name: never escaped.
func (t typeRef) qualified() string { return t.ns + "." + t.simple }

// ref is the name generated code refers to the type by, from a file whose
// namespace is ns.
func (t typeRef) ref(ns string) string {
	if t.ns == ns {
		return TypeName(t.simple)
	}
	return TypeName(t.ns) + "." + TypeName(t.simple)
}

type emitter struct {
	g          *graph.Graph
	opts       emit.Options
	namespaces map[string]*namespace // by Go package path
	types      map[string]typeRef    // by graph key
	// boxed holds "<graph key>#<Go field name>" for the struct fields stored
	// through IndirectType because they sit on a containment cycle.
	boxed map[string]bool
}

// plan assigns namespaces and type names and refuses collisions and
// unexpressible fields before any file is rendered.
func (e *emitter) plan() error {
	e.namespaces = map[string]*namespace{}
	byDir := map[string]string{}
	for _, p := range e.g.Packages {
		if len(p.Types) == 0 {
			continue
		}
		dir := path.Base(p.Path)
		if prev, dup := byDir[dir]; dup {
			return fmt.Errorf("swift: packages %s and %s both map to namespace %s", prev, p.Path, title(dir))
		}
		byDir[dir] = p.Path
		ns := &namespace{name: title(dir), dir: dir, file: title(dir) + ".swift"}
		e.namespaces[p.Path] = ns
		for _, t := range p.Types {
			e.types[t.Key()] = typeRef{ns: ns.name, simple: t.Name}
		}
	}
	// A type whose name equals a namespace shadows that namespace inside its
	// own file, so a cross-namespace reference would silently resolve to the
	// nested type. Refuse rather than emit code that compiles into the wrong
	// type.
	nsNames := map[string]string{}
	for _, ns := range e.namespaces {
		nsNames[ns.name] = ns.dir
	}
	for _, t := range e.g.Types() {
		if dir, clash := nsNames[t.Name]; clash {
			return fmt.Errorf("swift: type %s has the same name as the namespace of Go package %s", t.Key(), dir)
		}
	}
	for _, t := range e.g.Types() {
		if t.Kind != graph.KindStruct {
			continue
		}
		seen := map[string]string{}
		for _, f := range t.Fields {
			if f.Type.Kind == graph.KindCustom {
				if _, err := serializerType(t.Key(), f); err != nil {
					return err
				}
			}
			name := PropertyName(f.GoName)
			if prev, dup := seen[name]; dup {
				return fmt.Errorf("swift: %s: fields %s and %s both name property %s", t.Key(), prev, f.GoName, name)
			}
			seen[name] = f.GoName
		}
	}
	return e.planBoxing()
}

// title upper-cases the first letter, the same transform the Kotlin emitter
// applies to build a file name.
func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// serializerType resolves the Swift type of a Custom field. The registry's
// swift target wins; absent one, the emitter takes the last dot-separated
// component of the kotlin target, which is the type name the two clients
// hand-write for the same wire value (the fixture registry records exactly
// that pairing: org.example.FrameRateWire / FrameRateWire). It refuses when
// the entry names no target it can read, so a serializer added for a third
// language alone still fails loudly and names the field.
func serializerType(ownerKey string, f graph.Field) (string, error) {
	if s := f.Serializers[Language]; s != "" {
		return s, nil
	}
	if k := f.Serializers["kotlin"]; k != "" {
		return k[strings.LastIndex(k, ".")+1:], nil
	}
	return "", fmt.Errorf("swift: %s.%s has a registry serializer but no swift target and no kotlin target to take the type name from", ownerKey, f.GoName)
}

// planBoxing finds the struct fields that must be stored indirectly. Only a
// struct-typed field stores its type inline; lists, maps, Custom types and
// the raw JSON type are references or client-owned, so they already break a
// cycle. A field is boxed when its type can reach the owning type again
// through inline edges, and boxing is only possible where Go used a pointer —
// a cycle of non-pointer fields would be an infinitely large Go value, so a
// cycle that survives boxing is refused rather than mangled.
func (e *emitter) planBoxing() error {
	type edge struct {
		owner    string
		field    string
		to       string
		optional bool
	}
	var edges []edge
	for _, t := range e.g.Types() {
		if t.Kind != graph.KindStruct {
			continue
		}
		for _, f := range t.Fields {
			if f.Type.Kind != graph.KindStruct {
				continue
			}
			edges = append(edges, edge{owner: t.Key(), field: f.GoName, to: f.Type.Named, optional: f.Type.Nullable})
		}
	}
	reaches := func(use func(edge) bool) map[string]map[string]bool {
		adj := map[string][]string{}
		for _, ed := range edges {
			if use(ed) {
				adj[ed.owner] = append(adj[ed.owner], ed.to)
			}
		}
		out := map[string]map[string]bool{}
		for from := range adj {
			seen := map[string]bool{}
			stack := append([]string(nil), adj[from]...)
			for len(stack) > 0 {
				cur := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if seen[cur] {
					continue
				}
				seen[cur] = true
				stack = append(stack, adj[cur]...)
			}
			out[from] = seen
		}
		return out
	}
	all := reaches(func(edge) bool { return true })
	for _, ed := range edges {
		if ed.optional && all[ed.to][ed.owner] {
			e.boxed[ed.owner+"#"+ed.field] = true
		}
	}
	direct := reaches(func(ed edge) bool { return !e.boxed[ed.owner+"#"+ed.field] })
	var stuck []string
	for from, seen := range direct {
		if seen[from] {
			stuck = append(stuck, from)
		}
	}
	if len(stuck) > 0 {
		sort.Strings(stuck)
		return fmt.Errorf("swift: %s contain a cycle of non-pointer struct fields, which no Swift value type can store", strings.Join(stuck, ", "))
	}
	return nil
}

func (e *emitter) header(source string) string {
	rev := e.opts.ServerRevision
	if rev == "" {
		rev = "unknown"
	}
	return fmt.Sprintf(`// Code generated by cmd/clientdtogen from bloem-server %s. DO NOT EDIT.
// Server revision: %s (generator v%d, registry %s)
// Regenerate in the server repo with: make client-dtos
//
// Decoding is exact: every initializer reads an absent or null key as the documented
// default, so a server that omits a key still decodes, and encoding omits a nil value
// rather than writing null. Requires the client-owned %s (a Codable, Hashable,
// Sendable JSON value) and %s (a box for self-referential fields).
`, source, rev, e.opts.EffectiveGeneratorVersion(), e.opts.RegistryPath, RawType, IndirectType)
}

func (e *emitter) packageFile(p *graph.Package) ([]byte, error) {
	ns := e.namespaces[p.Path]
	var b strings.Builder
	b.WriteString(e.header(p.Path))
	fmt.Fprintf(&b, "\n/// Wire types of the Go package `%s`.\npublic enum %s {\n", p.Path, TypeName(ns.name))
	for i, t := range p.Types {
		if i > 0 {
			b.WriteByte('\n')
		}
		var err error
		switch t.Kind {
		case graph.KindStruct:
			err = e.renderStruct(&b, ns, t)
		case graph.KindEnum:
			err = e.renderEnum(&b, t)
		default:
			err = fmt.Errorf("swift: %s has kind %s", t.Key(), t.Kind)
		}
		if err != nil {
			return nil, err
		}
	}
	b.WriteString("}\n")
	return []byte(b.String()), nil
}

// doc renders the type's provenance line, the Swift spelling of the Kotlin
// KDoc.
func (e *emitter) doc(t *graph.Type) string {
	var b strings.Builder
	fmt.Fprintf(&b, "    /// Wire type `%s`. Direction: %s. Dialect: %s.", t.Key(), t.Direction, t.Dialect)
	if len(t.Gates) > 0 {
		fmt.Fprintf(&b, " Gate: %s.", joinGates(t.Gates))
	}
	if t.Root {
		b.WriteString(" Registered root.")
	}
	b.WriteByte('\n')
	return b.String()
}

// joinGates renders the gates of which any one suffices (graph.Type.Gates).
func joinGates(gates []string) string { return strings.Join(gates, "|") }

// property is one rendered field: everything the four member blocks need.
type property struct {
	name    string // Swift property name
	typ     string // Swift type, "?" included
	base    string // typ without the trailing "?"
	def     string // default expression, "" when the field is required
	key     string // wire name
	boxed   bool
	dialect string // set when the field's dialect differs from its type's
}

func (p property) optional() bool { return strings.HasSuffix(p.typ, "?") }

// storage is the stored property's name: the box for a self-referential
// field, the property itself otherwise.
func (p property) storage() string {
	if p.boxed {
		return "_" + p.name
	}
	return p.name
}

func (e *emitter) properties(ns *namespace, t *graph.Type) ([]property, error) {
	out := make([]property, 0, len(t.Fields))
	for _, f := range t.Fields {
		typ, def, err := e.property(ns, t, f)
		if err != nil {
			return nil, err
		}
		p := property{
			name:  PropertyName(f.GoName),
			typ:   typ,
			base:  strings.TrimSuffix(typ, "?"),
			def:   def,
			key:   f.WireName,
			boxed: e.boxed[t.Key()+"#"+f.GoName],
		}
		if f.Dialect != t.Dialect {
			p.dialect = string(f.Dialect)
		}
		out = append(out, p)
	}
	return out, nil
}

func (e *emitter) renderStruct(b *strings.Builder, ns *namespace, t *graph.Type) error {
	props, err := e.properties(ns, t)
	if err != nil {
		return err
	}
	b.WriteString(e.doc(t))
	fmt.Fprintf(b, "    public struct %s: Codable, Hashable, Sendable {\n", TypeName(t.Name))
	if len(props) == 0 {
		// An empty wire object still needs a public initializer to be
		// constructible outside the module; Codable synthesizes itself.
		b.WriteString("        public init() {}\n    }\n")
		return nil
	}

	for _, p := range props {
		if p.dialect != "" {
			fmt.Fprintf(b, "        /// Dialect: %s.\n", p.dialect)
		}
		if p.boxed {
			// Swift stores a struct inline, so a field that can reach its own
			// type must sit behind a reference. Kotlin needs no equivalent.
			b.WriteString("        /// Self-referential on the wire; stored through a box.\n")
			fmt.Fprintf(b, "        public var %s: %s { %s.value }\n", p.name, p.typ, p.storage())
			fmt.Fprintf(b, "        private let %s: %s<%s>\n", p.storage(), IndirectType, p.typ)
			continue
		}
		fmt.Fprintf(b, "        public let %s: %s\n", p.name, p.typ)
	}

	b.WriteString("\n        public enum CodingKeys: String, CodingKey {\n")
	for _, p := range props {
		fmt.Fprintf(b, "            case %s = %s\n", p.name, strconv.Quote(p.key))
	}
	b.WriteString("        }\n")

	b.WriteString("\n        public init(\n")
	for i, p := range props {
		fmt.Fprintf(b, "            %s: %s", p.name, p.typ)
		if p.def != "" {
			fmt.Fprintf(b, " = %s", p.def)
		}
		if i < len(props)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("        ) {\n")
	for _, p := range props {
		if p.boxed {
			fmt.Fprintf(b, "            self.%s = %s(%s)\n", p.storage(), IndirectType, p.name)
			continue
		}
		fmt.Fprintf(b, "            self.%s = %s\n", p.name, p.name)
	}
	b.WriteString("        }\n")

	b.WriteString("\n        public init(from decoder: any Decoder) throws {\n")
	b.WriteString("            let container = try decoder.container(keyedBy: CodingKeys.self)\n")
	for _, p := range props {
		fmt.Fprintf(b, "            self.%s = %s\n", p.storage(), e.decodeExpr(p))
	}
	b.WriteString("        }\n")

	b.WriteString("\n        public func encode(to encoder: any Encoder) throws {\n")
	b.WriteString("            var container = encoder.container(keyedBy: CodingKeys.self)\n")
	for _, p := range props {
		verb := "encode"
		if p.optional() {
			// Kotlin's explicitNulls = false drops a null key; encodeIfPresent
			// is the same rule, so an absent value stays absent on the wire.
			verb = "encodeIfPresent"
		}
		// self-qualified: a wire field may be called "container", and the
		// local encoding container would shadow it.
		fmt.Fprintf(b, "            try container.%s(self.%s, forKey: .%s)\n", verb, p.name, p.name)
	}
	b.WriteString("        }\n    }\n")
	return nil
}

// decodeExpr renders one field's decode. decodeIfPresent returns nil for both
// an absent key and an explicit null, which is exactly what Kotlin's
// ignoreUnknownKeys + coerceInputValues + defaults give the Android client.
func (e *emitter) decodeExpr(p property) string {
	read := fmt.Sprintf("try container.decodeIfPresent(%s.self, forKey: .%s)", p.base, p.name)
	switch {
	case p.boxed:
		return fmt.Sprintf("%s(%s)", IndirectType, read)
	case p.optional():
		return read
	case p.def != "":
		return read + " ?? " + p.def
	default:
		return fmt.Sprintf("try container.decode(%s.self, forKey: .%s)", p.base, p.name)
	}
}

// property derives the Swift type and default of one field per §4.4, row for
// row with the Kotlin emitter. An empty default means the initializer
// parameter is required.
func (e *emitter) property(ns *namespace, owner *graph.Type, f graph.Field) (typ, def string, err error) {
	ref := f.Type
	typ, err = e.typeName(ns, owner.Key(), ref, f)
	if err != nil {
		return "", "", err
	}
	if ref.Nullable {
		return typ, "nil", nil
	}
	response := owner.Direction.Response()
	optional := response || f.OmitEmpty
	switch ref.Kind {
	case graph.KindCustom:
		// The client owns the shape, so the generator cannot state a zero
		// value; the only default it can express is absence.
		if optional {
			return typ + "?", "nil", nil
		}
		return typ, "", nil
	case graph.KindStruct:
		if response {
			return typ, typ + "()", nil
		}
		if f.OmitEmpty {
			return typ + "?", "nil", nil
		}
		return typ, "", nil
	case graph.KindEnum:
		if optional {
			return typ, typ + `(rawValue: "")`, nil
		}
		return typ, "", nil
	case graph.KindList:
		if optional {
			return typ, "[]", nil
		}
		return typ, "", nil
	case graph.KindMap:
		if optional {
			return typ, "[:]", nil
		}
		return typ, "", nil
	case graph.KindRaw:
		return typ, "nil", nil
	}
	if optional {
		return typ, zeroValue(ref.Kind), nil
	}
	return typ, "", nil
}

func zeroValue(k graph.Kind) string {
	switch k {
	case graph.KindString, graph.KindTime, graph.KindUUID, graph.KindBytes:
		return `""`
	case graph.KindBool:
		return "false"
	case graph.KindInt, graph.KindLong:
		return "0"
	case graph.KindDouble:
		return "0.0"
	}
	return ""
}

// typeName renders ref, including its own "?" when nullable. A Raw element
// inside a list or map renders non-optional, the same rule the Kotlin emitter
// applies to JsonElement: the JSON value type represents null itself.
func (e *emitter) typeName(ns *namespace, ownerKey string, ref graph.TypeRef, f graph.Field) (string, error) {
	return e.typeNameAt(ns, ownerKey, ref, f, true)
}

func (e *emitter) typeNameAt(ns *namespace, ownerKey string, ref graph.TypeRef, f graph.Field, top bool) (string, error) {
	var name string
	switch ref.Kind {
	case graph.KindString, graph.KindTime, graph.KindUUID, graph.KindBytes:
		// time.Time stays a String for the reason §4.1 gives on the Kotlin
		// side: the server hand-formats some date fields, so a typed instant
		// would be wrong for them. uuid.UUID likewise: Foundation's UUID
		// refuses a string the server may legitimately send.
		name = "String"
	case graph.KindBool:
		name = "Bool"
	case graph.KindInt:
		// Swift's Int is 64-bit on every Apple platform, so it covers the
		// uint32 row Kotlin's 32-bit Int cannot; the wire shape is unchanged.
		name = "Int"
	case graph.KindLong:
		name = "Int64"
	case graph.KindDouble:
		name = "Double"
	case graph.KindRaw:
		name = RawType
		if !top {
			return name, nil
		}
	case graph.KindList, graph.KindMap:
		if ref.Elem == nil {
			return "", fmt.Errorf("swift: %s has no element type", ref)
		}
		elem, err := e.typeNameAt(ns, ownerKey, *ref.Elem, f, false)
		if err != nil {
			return "", err
		}
		if ref.Kind == graph.KindList {
			name = "[" + elem + "]"
		} else {
			name = "[String: " + elem + "]"
		}
	case graph.KindStruct, graph.KindEnum:
		c, ok := e.types[ref.Named]
		if !ok {
			return "", fmt.Errorf("swift: %s references %s, which is not in the graph", f.GoName, ref.Named)
		}
		if c.ns != ns.name && ReservedTypeNames[c.simple] {
			// `Namespace.Protocol` is metatype syntax, and backticks do not
			// rescue the qualified form: the type is unreachable from any
			// other namespace, and so is unusable by client code outside its
			// own. Renaming the Go type is the fix; guessing a Swift name for
			// it is not.
			return "", fmt.Errorf("swift: %s.%s refers to %s across namespaces, and Swift cannot name a nested type %q from outside it; rename the Go type", ownerKey, f.GoName, ref.Named, c.simple)
		}
		name = c.ref(ns.name)
	case graph.KindCustom:
		ser, err := serializerType(ownerKey, f)
		if err != nil {
			return "", err
		}
		name = ser
	default:
		return "", fmt.Errorf("swift: cannot render %s", ref)
	}
	if ref.Nullable {
		name += "?"
	}
	return name, nil
}

// renderEnum emits the §4.2 open vocabulary: a RawRepresentable String
// wrapper, so an unknown server value decodes instead of failing. Kotlin uses
// a @JvmInline value class; Swift's equivalent is a single-property struct,
// and the standard library's RawRepresentable conformance supplies Codable,
// which decodes and encodes it as a bare string.
func (e *emitter) renderEnum(b *strings.Builder, t *graph.Type) error {
	names, err := emit.ConstantNames(t)
	if err != nil {
		return fmt.Errorf("swift: %w", err)
	}
	for i, n := range names {
		if n == "KNOWN" {
			return fmt.Errorf("swift: enum %s: constant %s maps to KNOWN, which names the vocabulary table", t.Key(), t.Constants[i].GoName)
		}
	}
	b.WriteString(e.doc(t))
	name := TypeName(t.Name)
	fmt.Fprintf(b, "    public struct %s: RawRepresentable, Codable, Hashable, Sendable {\n", name)
	b.WriteString("        public let wire: String\n")
	b.WriteString("        public var rawValue: String { wire }\n")
	b.WriteString("        public init(rawValue: String) { self.wire = rawValue }\n")
	if len(t.Constants) > 0 {
		b.WriteByte('\n')
	}
	for i, c := range t.Constants {
		// The constant identifiers are SCREAMING_CASE in both emitters so the
		// two clients name one vocabulary identically; Swift's own convention
		// would be lowerCamelCase.
		fmt.Fprintf(b, "        public static let %s: %s = %s(rawValue: %s)\n", names[i], name, name, strconv.Quote(c.Value))
	}
	fmt.Fprintf(b, "        public static let KNOWN: [%s] = [%s]\n", name, strings.Join(names, ", "))
	b.WriteString("    }\n")
	return nil
}

func (e *emitter) contractFile() ([]byte, error) {
	var dump strings.Builder
	if err := e.g.Dump(&dump); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(dump.String()))

	type row struct{ typeName, value string }
	var direction, dialect, gate []row
	for _, t := range e.g.Types() {
		q := e.types[t.Key()].qualified()
		direction = append(direction, row{q, t.Direction.String()})
		dialect = append(dialect, row{q, string(t.Dialect)})
		if len(t.Gates) > 0 {
			gate = append(gate, row{q, joinGates(t.Gates)})
		}
	}
	table := func(b *strings.Builder, name string, rows []row) {
		fmt.Fprintf(b, "    public static let %s: [String: String] = [", name)
		if len(rows) == 0 {
			b.WriteString(":]\n")
			return
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].typeName < rows[j].typeName })
		b.WriteByte('\n')
		for _, r := range rows {
			fmt.Fprintf(b, "        %s: %s,\n", strconv.Quote(r.typeName), strconv.Quote(r.value))
		}
		b.WriteString("    ]\n")
	}

	rev := e.opts.ServerRevision
	if rev == "" {
		rev = "unknown"
	}
	var b strings.Builder
	b.WriteString(e.header(e.opts.RegistryPath))
	b.WriteString("\n/// Provenance of the generated contract and per-type registry metadata, as tables.\n")
	b.WriteString("public enum GeneratedContract {\n")
	fmt.Fprintf(&b, "    public static let SERVER_REVISION: String = %s\n", strconv.Quote(rev))
	fmt.Fprintf(&b, "    public static let GENERATOR_VERSION: Int = %d\n", e.opts.EffectiveGeneratorVersion())
	b.WriteString("    /// Digest of the normalised type graph; changes only when a wire shape changes.\n")
	fmt.Fprintf(&b, "    public static let CONTRACT_DIGEST: String = %s\n", strconv.Quote("sha256:"+hex.EncodeToString(sum[:])))
	b.WriteString("    /// Qualified type name → direction (request, response or both).\n")
	table(&b, "DIRECTION", direction)
	fmt.Fprintf(&b, "    /// Qualified type name → dialect (%s or %s), from the registry.\n", registry.DialectUpstreamCompat, registry.DialectBloem)
	table(&b, "DIALECT", dialect)
	b.WriteString("    /// Qualified type name → capability gates, for gated types only; any one of the \"|\"-separated gates suffices.\n")
	table(&b, "GATE", gate)
	b.WriteString("}\n")
	return []byte(b.String()), nil
}
