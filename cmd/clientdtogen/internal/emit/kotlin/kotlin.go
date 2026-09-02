// Package kotlin emits the client DTO set as kotlinx.serialization classes
// (docs/specs/client-dto-generator.md §4, §5). The output shape is a
// provenance requirement (§2): every declaration line carries explicit
// public, every property has @SerialName on its own line, defaults and
// nullability follow §4.4 by rule, and no generated file contains a fun.
package kotlin

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
const Language = "kotlin"

// BasePackage is the Kotlin package every generated file lives under; each
// Go package adds its last path element (§5.1).
const BasePackage = "org.bloemserver.bloem.contract"

// ContractFile is the file holding the GeneratedContract object (§5.3).
const ContractFile = "GeneratedContract.kt"

// Keywords are the Kotlin hard keywords; a property name that lands on one
// gets a Value suffix (§4.3 step 3). Soft and modifier keywords (type, value,
// data, …) are legal identifiers and stay as they are.
var Keywords = map[string]bool{
	"as": true, "break": true, "class": true, "continue": true, "do": true, "else": true,
	"false": true, "for": true, "fun": true, "if": true, "in": true, "interface": true,
	"is": true, "null": true, "object": true, "package": true, "return": true, "super": true,
	"this": true, "throw": true, "true": true, "try": true, "typealias": true, "typeof": true,
	"val": true, "var": true, "when": true, "while": true,
}

// PropertyName applies the §4.3 rule for Kotlin.
func PropertyName(goName string) string { return emit.CamelCase(goName, Keywords) }

// Emitter renders a graph as Kotlin. The zero value is ready to use.
type Emitter struct{}

// Language implements emit.Emitter.
func (Emitter) Language() string { return Language }

// Emit implements emit.Emitter.
func (Emitter) Emit(g *graph.Graph, opts emit.Options) (emit.Files, error) {
	e := &emitter{g: g, opts: opts, classes: map[string]classRef{}}
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
		pk := e.packages[p.Path]
		files = append(files, emit.File{Path: pk.dir + "/" + pk.file, Content: content})
	}
	contract, err := e.contractFile()
	if err != nil {
		return nil, err
	}
	files = append(files, emit.File{Path: ContractFile, Content: contract})
	files.Sort()
	return files, nil
}

type kotlinPackage struct {
	name string // Kotlin package, e.g. org.bloemserver.bloem.contract.playback
	dir  string // last Go path element
	file string // Playback.kt
}

type classRef struct {
	pkg    string // Kotlin package
	simple string // Go type name verbatim
}

func (c classRef) fqcn() string { return c.pkg + "." + c.simple }

type emitter struct {
	g        *graph.Graph
	opts     emit.Options
	packages map[string]*kotlinPackage // by Go package path
	classes  map[string]classRef       // by graph key
}

// plan assigns Kotlin packages and class names and refuses collisions and
// unexpressible fields before any file is rendered.
func (e *emitter) plan() error {
	e.packages = map[string]*kotlinPackage{}
	byDir := map[string]string{}
	for _, p := range e.g.Packages {
		if len(p.Types) == 0 {
			continue
		}
		dir := path.Base(p.Path)
		if prev, dup := byDir[dir]; dup {
			return fmt.Errorf("kotlin: packages %s and %s both map to Kotlin package %s.%s", prev, p.Path, BasePackage, dir)
		}
		byDir[dir] = p.Path
		pk := &kotlinPackage{
			name: BasePackage + "." + dir,
			dir:  dir,
			file: strings.ToUpper(dir[:1]) + dir[1:] + ".kt",
		}
		e.packages[p.Path] = pk
		for _, t := range p.Types {
			e.classes[t.Key()] = classRef{pkg: pk.name, simple: t.Name}
		}
	}
	for _, t := range e.g.Types() {
		if t.Kind != graph.KindStruct {
			continue
		}
		seen := map[string]string{}
		for _, f := range t.Fields {
			if f.Type.Kind == graph.KindCustom {
				if _, ok := f.Serializers[Language]; !ok {
					return fmt.Errorf("kotlin: %s.%s has a registry serializer for other languages but none for kotlin", t.Key(), f.GoName)
				}
			}
			name := PropertyName(f.GoName)
			if prev, dup := seen[name]; dup {
				return fmt.Errorf("kotlin: %s: fields %s and %s both name property %s", t.Key(), prev, f.GoName, name)
			}
			seen[name] = f.GoName
		}
	}
	return nil
}

// fileBody accumulates a file's declarations and the imports they need.
type fileBody struct {
	pkg     *kotlinPackage
	imports map[string]bool
	b       strings.Builder
}

func (fb *fileBody) importClass(fq string) {
	fb.imports[fq] = true
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
// Decoding requires the shared Json configuration: ignoreUnknownKeys, coerceInputValues,
// explicitNulls = false, encodeDefaults (see network/BloemHttpClient.kt).
`, source, rev, e.opts.EffectiveGeneratorVersion(), e.opts.RegistryPath)
}

func (e *emitter) packageFile(p *graph.Package) ([]byte, error) {
	fb := &fileBody{pkg: e.packages[p.Path], imports: map[string]bool{
		"kotlinx.serialization.SerialName":   true,
		"kotlinx.serialization.Serializable": true,
	}}
	for _, t := range p.Types {
		fb.b.WriteByte('\n')
		var err error
		switch t.Kind {
		case graph.KindStruct:
			err = e.renderStruct(fb, t)
		case graph.KindEnum:
			err = e.renderEnum(fb, t)
		default:
			err = fmt.Errorf("kotlin: %s has kind %s", t.Key(), t.Kind)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := checkSimpleNames(fb, p); err != nil {
		return nil, err
	}

	var out strings.Builder
	out.WriteString(e.header(p.Path))
	out.WriteString("\npackage " + fb.pkg.name + "\n\n")
	imports := make([]string, 0, len(fb.imports))
	for imp := range fb.imports {
		imports = append(imports, imp)
	}
	sort.Strings(imports)
	for _, imp := range imports {
		out.WriteString("import " + imp + "\n")
	}
	out.WriteString(fb.b.String())
	return []byte(out.String()), nil
}

// checkSimpleNames refuses a file whose imports would shadow one another or a
// class declared in the file.
func checkSimpleNames(fb *fileBody, p *graph.Package) error {
	owners := map[string]string{}
	for _, t := range p.Types {
		owners[t.Name] = fb.pkg.name + "." + t.Name
	}
	imports := make([]string, 0, len(fb.imports))
	for imp := range fb.imports {
		imports = append(imports, imp)
	}
	sort.Strings(imports)
	for _, imp := range imports {
		simple := simpleName(imp)
		if prev, dup := owners[simple]; dup && prev != imp {
			return fmt.Errorf("kotlin: %s: %s and %s share the simple name %s", fb.pkg.file, prev, imp, simple)
		}
		owners[simple] = imp
	}
	return nil
}

func (e *emitter) kdoc(t *graph.Type) string {
	var b strings.Builder
	fmt.Fprintf(&b, "/** Wire type `%s`. Direction: %s. Dialect: %s.", t.Key(), t.Direction, t.Dialect)
	if len(t.Gates) > 0 {
		fmt.Fprintf(&b, " Gate: %s.", joinGates(t.Gates))
	}
	if t.Root {
		b.WriteString(" Registered root.")
	}
	b.WriteString(" */\n")
	return b.String()
}

// joinGates renders the gates of which any one suffices (graph.Type.Gates).
func joinGates(gates []string) string { return strings.Join(gates, "|") }

func (e *emitter) renderStruct(fb *fileBody, t *graph.Type) error {
	fb.b.WriteString(e.kdoc(t))
	fb.b.WriteString("@Serializable\n")
	if len(t.Fields) == 0 {
		// A data class needs a constructor property; an empty wire object is a
		// plain class, still constructible as T().
		fmt.Fprintf(&fb.b, "public class %s\n", t.Name)
		return nil
	}
	fmt.Fprintf(&fb.b, "public data class %s(\n", t.Name)
	for _, f := range t.Fields {
		if f.Dialect != t.Dialect {
			fmt.Fprintf(&fb.b, "    /** Dialect: %s. */\n", f.Dialect)
		}
		fmt.Fprintf(&fb.b, "    @SerialName(%s)\n", strconv.Quote(f.WireName))
		if f.Type.Kind == graph.KindCustom {
			ser := f.Serializers[Language]
			fb.importClass(ser)
			fmt.Fprintf(&fb.b, "    @Serializable(with = %s.Serializer::class)\n", simpleName(ser))
		}
		typ, def, err := e.property(fb, t, f)
		if err != nil {
			return err
		}
		fmt.Fprintf(&fb.b, "    public val %s: %s", PropertyName(f.GoName), typ)
		if def != "" {
			fmt.Fprintf(&fb.b, " = %s", def)
		}
		fb.b.WriteString(",\n")
	}
	fb.b.WriteString(")\n")
	return nil
}

// property derives the Kotlin type and default of one field per §4.4. An
// empty default means the constructor parameter is required.
func (e *emitter) property(fb *fileBody, owner *graph.Type, f graph.Field) (typ, def string, err error) {
	ref := f.Type
	typ, err = e.typeName(fb, ref, f)
	if err != nil {
		return "", "", err
	}
	if ref.Nullable {
		return typ, "null", nil
	}
	response := owner.Direction.Response()
	optional := response || f.OmitEmpty
	switch ref.Kind {
	case graph.KindCustom:
		// The client owns the shape, so the generator cannot state a zero
		// value; the only default it can express is absence.
		if optional {
			return typ + "?", "null", nil
		}
		return typ, "", nil
	case graph.KindStruct:
		if response {
			return typ, typ + "()", nil
		}
		if f.OmitEmpty {
			// A request-only class need not have an all-defaults
			// constructor; omitted is modeled as null (explicitNulls = false
			// drops the key).
			return typ + "?", "null", nil
		}
		return typ, "", nil
	case graph.KindEnum:
		if optional {
			return typ, typ + `("")`, nil
		}
		return typ, "", nil
	case graph.KindList:
		if optional {
			return typ, "emptyList()", nil
		}
		return typ, "", nil
	case graph.KindMap:
		if optional {
			return typ, "emptyMap()", nil
		}
		return typ, "", nil
	case graph.KindRaw:
		return typ, "null", nil
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
	case graph.KindInt:
		return "0"
	case graph.KindLong:
		return "0L"
	case graph.KindDouble:
		return "0.0"
	}
	return ""
}

// typeName renders ref, including its own "?" when nullable, registering
// imports as it goes. A Raw element inside a list or map renders as a
// non-null JsonElement: JsonElement decodes a JSON null as JsonNull itself,
// and §4.1 maps map[string]any to Map<String, JsonElement>.
func (e *emitter) typeName(fb *fileBody, ref graph.TypeRef, f graph.Field) (string, error) {
	return e.typeNameAt(fb, ref, f, true)
}

func (e *emitter) typeNameAt(fb *fileBody, ref graph.TypeRef, f graph.Field, top bool) (string, error) {
	var name string
	switch ref.Kind {
	case graph.KindString, graph.KindTime, graph.KindUUID, graph.KindBytes:
		name = "String"
	case graph.KindBool:
		name = "Boolean"
	case graph.KindInt:
		name = "Int"
	case graph.KindLong:
		name = "Long"
	case graph.KindDouble:
		name = "Double"
	case graph.KindRaw:
		fb.importClass("kotlinx.serialization.json.JsonElement")
		name = "JsonElement"
		if !top {
			return name, nil
		}
	case graph.KindList, graph.KindMap:
		if ref.Elem == nil {
			return "", fmt.Errorf("kotlin: %s has no element type", ref)
		}
		elem, err := e.typeNameAt(fb, *ref.Elem, f, false)
		if err != nil {
			return "", err
		}
		if ref.Kind == graph.KindList {
			name = "List<" + elem + ">"
		} else {
			name = "Map<String, " + elem + ">"
		}
	case graph.KindStruct, graph.KindEnum:
		c, ok := e.classes[ref.Named]
		if !ok {
			return "", fmt.Errorf("kotlin: %s references %s, which is not in the graph", f.GoName, ref.Named)
		}
		if c.pkg != fb.pkg.name {
			fb.importClass(c.fqcn())
		}
		name = c.simple
	case graph.KindCustom:
		name = simpleName(f.Serializers[Language])
	default:
		return "", fmt.Errorf("kotlin: cannot render %s", ref)
	}
	if ref.Nullable {
		name += "?"
	}
	return name, nil
}

func simpleName(fq string) string { return fq[strings.LastIndex(fq, ".")+1:] }

func (e *emitter) renderEnum(fb *fileBody, t *graph.Type) error {
	names, err := emit.ConstantNames(t)
	if err != nil {
		return fmt.Errorf("kotlin: %w", err)
	}
	for i, n := range names {
		if n == "KNOWN" {
			return fmt.Errorf("kotlin: enum %s: constant %s maps to KNOWN, which names the vocabulary table", t.Key(), t.Constants[i].GoName)
		}
	}
	fb.importClass("kotlin.jvm.JvmInline")
	fb.b.WriteString(e.kdoc(t))
	fb.b.WriteString("@Serializable\n@JvmInline\n")
	fmt.Fprintf(&fb.b, "public value class %s(public val wire: String) {\n", t.Name)
	fb.b.WriteString("    public companion object {\n")
	for i, c := range t.Constants {
		fmt.Fprintf(&fb.b, "        @SerialName(%s)\n", strconv.Quote(c.Value))
		fmt.Fprintf(&fb.b, "        public val %s: %s = %s(%s)\n", names[i], t.Name, t.Name, strconv.Quote(c.Value))
	}
	fmt.Fprintf(&fb.b, "        public val KNOWN: List<%s> = listOf(%s)\n", t.Name, strings.Join(names, ", "))
	fb.b.WriteString("    }\n}\n")
	return nil
}

func (e *emitter) contractFile() ([]byte, error) {
	var dump strings.Builder
	if err := e.g.Dump(&dump); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(dump.String()))

	type row struct{ class, value string }
	var direction, dialect, gate []row
	for _, t := range e.g.Types() {
		fq := e.classes[t.Key()].fqcn()
		direction = append(direction, row{fq, t.Direction.String()})
		dialect = append(dialect, row{fq, string(t.Dialect)})
		if len(t.Gates) > 0 {
			gate = append(gate, row{fq, joinGates(t.Gates)})
		}
	}
	table := func(b *strings.Builder, name string, rows []row) {
		fmt.Fprintf(b, "    public val %s: Map<String, String> = mapOf(", name)
		if len(rows) == 0 {
			b.WriteString(")\n")
			return
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].class < rows[j].class })
		b.WriteString("\n")
		for _, r := range rows {
			fmt.Fprintf(b, "        %s to %s,\n", strconv.Quote(r.class), strconv.Quote(r.value))
		}
		b.WriteString("    )\n")
	}

	rev := e.opts.ServerRevision
	if rev == "" {
		rev = "unknown"
	}
	var b strings.Builder
	b.WriteString(e.header(e.opts.RegistryPath))
	b.WriteString("\npackage " + BasePackage + "\n\n")
	b.WriteString("/** Provenance of the generated contract and per-type registry metadata, as tables. */\n")
	b.WriteString("public object GeneratedContract {\n")
	fmt.Fprintf(&b, "    public const val SERVER_REVISION: String = %s\n", strconv.Quote(rev))
	fmt.Fprintf(&b, "    public const val GENERATOR_VERSION: Int = %d\n", e.opts.EffectiveGeneratorVersion())
	b.WriteString("    /** Digest of the normalised type graph; changes only when a wire shape changes. */\n")
	fmt.Fprintf(&b, "    public const val CONTRACT_DIGEST: String = %s\n", strconv.Quote("sha256:"+hex.EncodeToString(sum[:])))
	b.WriteString("    /** Fully qualified class name → direction (request, response or both). */\n")
	table(&b, "DIRECTION", direction)
	fmt.Fprintf(&b, "    /** Fully qualified class name → dialect (%s or %s), from the registry. */\n", registry.DialectUpstreamCompat, registry.DialectBloem)
	table(&b, "DIALECT", dialect)
	b.WriteString("    /** Fully qualified class name → capability gates, for gated types only; any one of the \"|\"-separated gates suffices. */\n")
	table(&b, "GATE", gate)
	b.WriteString("}\n")
	return []byte(b.String()), nil
}
