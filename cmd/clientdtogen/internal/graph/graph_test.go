package graph

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

const (
	fixturePath = "cmd/clientdtogen/internal/graph/testdata/fixture"
	otherPath   = fixturePath + "/other"
	refusePath  = "cmd/clientdtogen/internal/graph/testdata/refuse"
)

// repoRoot walks up from the package directory to go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

func fixtureRegistry() *registry.Registry {
	return &registry.Registry{
		Schema: 1,
		Packages: []registry.Package{{
			Path:    fixturePath,
			Dialect: registry.DialectUpstreamCompat,
			Roots: []registry.Root{
				{Type: "Scalars", Direction: registry.DirectionResponse},
				{Type: "Collections", Direction: registry.DirectionResponse},
				{Type: "Embedded", Direction: registry.DirectionRequest},
				{Type: "AliasRoot", Direction: registry.DirectionResponse},
				{Type: "Request", Direction: registry.DirectionRequest},
				{Type: "Gated", Direction: registry.DirectionResponse, Gate: "cap.gated"},
				{Type: "Gated2", Direction: registry.DirectionResponse, Gate: "cap.other"},
				{Type: "StandaloneAlias", Direction: registry.DirectionRequest, Dialect: registry.DialectBloem, Gate: "cap.alias"},
				{Type: "Compat", Direction: registry.DirectionResponse, BloemFields: []string{"promo"}},
				{Type: "BloemOnly", Direction: registry.DirectionBoth, Dialect: registry.DialectBloem, Gate: "cap.bloem"},
				{Type: "Protocol", Direction: registry.DirectionRequest},
				{Type: "UnmarshalOnly", Direction: registry.DirectionResponse},
			},
		}},
		Serializers: map[string]registry.Serializer{
			fixturePath + ".Response.frame_rate":     {"kotlin": "org.example.FrameRateWire", "swift": "FrameRateWire"},
			fixturePath + ".Response.frame_rate_ptr": {"kotlin": "org.example.FrameRateWire"},
		},
	}
}

func buildFixture(t *testing.T) *Graph {
	t.Helper()
	g, err := Build(Config{Dir: repoRoot(t), Registry: fixtureRegistry()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func mustType(t *testing.T, g *Graph, key string) *Type {
	t.Helper()
	typ, ok := g.Type(key)
	if !ok {
		t.Fatalf("type %s not in graph", key)
	}
	return typ
}

func fieldByWire(t *testing.T, typ *Type, wire string) Field {
	t.Helper()
	for _, f := range typ.Fields {
		if f.WireName == wire {
			return f
		}
	}
	t.Fatalf("%s has no field %q; fields: %v", typ.Key(), wire, wireNames(typ))
	return Field{}
}

func wireNames(typ *Type) []string {
	out := make([]string, 0, len(typ.Fields))
	for _, f := range typ.Fields {
		out = append(out, f.WireName)
	}
	return out
}

func ref(kind Kind) TypeRef         { return TypeRef{Kind: kind} }
func nullable(r TypeRef) TypeRef    { r.Nullable = true; return r }
func list(elem TypeRef) TypeRef     { return TypeRef{Kind: KindList, Elem: &elem} }
func mapOf(elem TypeRef) TypeRef    { return TypeRef{Kind: KindMap, Elem: &elem} }
func structRef(key string) TypeRef  { return TypeRef{Kind: KindStruct, Named: key} }
func enumRef(key string) TypeRef    { return TypeRef{Kind: KindEnum, Named: key} }
func fixtureKey(name string) string { return fixturePath + "." + name }
func otherKey(name string) string   { return otherPath + "." + name }
func refuseKey(name string) string  { return refusePath + "." + name }

func TestScalarRows(t *testing.T) {
	g := buildFixture(t)
	typ := mustType(t, g, fixtureKey("Scalars"))
	if typ.Kind != KindStruct {
		t.Fatalf("Scalars kind = %s", typ.Kind)
	}
	want := []struct {
		wire      string
		typ       TypeRef
		omitEmpty bool
	}{
		{"s", ref(KindString), false},
		{"b", ref(KindBool), false},
		{"i", ref(KindInt), false},
		{"i8", ref(KindInt), false},
		{"i16", ref(KindInt), false},
		{"i32", ref(KindInt), false},
		{"u8", ref(KindInt), false},
		{"u16", ref(KindInt), false},
		{"u32", ref(KindInt), false},
		{"i64", ref(KindLong), false},
		{"u64", ref(KindLong), false},
		{"f32", ref(KindDouble), false},
		{"f64", ref(KindDouble), false},
		{"t", ref(KindTime), false},
		{"tp", nullable(ref(KindTime)), true},
		{"u", ref(KindUUID), false},
		{"up", nullable(ref(KindUUID)), false},
		{"raw", nullable(ref(KindRaw)), false},
		{"any", nullable(ref(KindRaw)), false},
		{"any_map", mapOf(nullable(ref(KindRaw))), false},
		{"bytes", ref(KindBytes), false},
		{"label", ref(KindString), false},
		{"count", ref(KindInt), false},
		{"names", list(ref(KindString)), false},
		{"proto", enumRef(fixtureKey("Protocol")), false},
		{"proto_ptr", nullable(enumRef(fixtureKey("Protocol"))), true},
		{"int_ptr", nullable(ref(KindInt)), true},
		{"omit", ref(KindInt), true},
		{"t_omit", ref(KindTime), false}, // struct: encoding/json never omits it
		{"u_omit", ref(KindUUID), false}, // array: never omitted
		{"raw_omit", nullable(ref(KindRaw)), true},
		{"bytes_omit", ref(KindBytes), true},
		{"proto_omit", enumRef(fixtureKey("Protocol")), true},
	}
	if len(typ.Fields) != len(want) {
		t.Fatalf("Scalars has %d fields, want %d: %v", len(typ.Fields), len(want), wireNames(typ))
	}
	for i, w := range want {
		f := typ.Fields[i]
		if f.WireName != w.wire {
			t.Errorf("field %d: wire %q, want %q (declaration order)", i, f.WireName, w.wire)
			continue
		}
		if !reflect.DeepEqual(f.Type, w.typ) {
			t.Errorf("%s: type %s, want %s", w.wire, f.Type, w.typ)
		}
		if f.OmitEmpty != w.omitEmpty {
			t.Errorf("%s: omitempty %v, want %v", w.wire, f.OmitEmpty, w.omitEmpty)
		}
		if f.GoName == "" || strings.ContainsAny(f.GoName, "_") {
			t.Errorf("%s: GoName %q is not the Go identifier", w.wire, f.GoName)
		}
	}
	if f := fieldByWire(t, typ, "proto_ptr"); f.GoName != "ProtoPtr" {
		t.Errorf("GoName = %q, want ProtoPtr", f.GoName)
	}
	if _, ok := g.Type(fixtureKey("Label")); ok {
		t.Error("named string without constants must not become a graph type")
	}
	if _, ok := g.Type(fixtureKey("Count")); ok {
		t.Error("named int must not become a graph type")
	}
}

func TestEnumConstants(t *testing.T) {
	g := buildFixture(t)
	typ := mustType(t, g, fixtureKey("Protocol"))
	if typ.Kind != KindEnum || !typ.Root {
		t.Fatalf("Protocol kind=%s root=%v", typ.Kind, typ.Root)
	}
	want := []Constant{
		{GoName: "ProtocolHLS", Value: "hls"},
		{GoName: "ProtocolProgressive", Value: "progressive"},
		{GoName: "ProtocolLate", Value: "late"},
	}
	if !reflect.DeepEqual(typ.Constants, want) {
		t.Errorf("constants = %v, want %v (source order, exported typed constants only)", typ.Constants, want)
	}
	if typ.Direction != registry.DirectionBoth {
		t.Errorf("Protocol direction = %s, want both (request root + Scalars response field)", typ.Direction)
	}
}

func TestCollectionRows(t *testing.T) {
	g := buildFixture(t)
	typ := mustType(t, g, fixtureKey("Collections"))
	child := structRef(fixtureKey("Child"))
	want := map[string]TypeRef{
		"strings":       list(ref(KindString)),
		"structs":       list(child),
		"optional_list": nullable(list(child)),
		"headers":       mapOf(ref(KindString)),
		"struct_map":    mapOf(child),
		"enum_map":      mapOf(ref(KindInt)),
		"fixed":         list(ref(KindInt)),
		"nested":        list(list(ref(KindString))),
		"ptr_list":      list(nullable(child)),
		"fixed_omit":    list(ref(KindInt)),
		"struct_omit":   child,
		"map_omit":      mapOf(ref(KindInt)),
	}
	for wire, w := range want {
		if got := fieldByWire(t, typ, wire).Type; !reflect.DeepEqual(got, w) {
			t.Errorf("%s: %s, want %s", wire, got, w)
		}
	}
	for wire, want := range map[string]bool{
		"structs": true, "optional_list": true, "map_omit": true,
		"fixed_omit": false, "struct_omit": false,
	} {
		if got := fieldByWire(t, typ, wire).OmitEmpty; got != want {
			t.Errorf("%s: OmitEmpty %v, want %v (omitempty only applies to values encoding/json can omit)", wire, got, want)
		}
	}
	if next := fieldByWire(t, mustType(t, g, fixtureKey("Child")), "next").Type; !reflect.DeepEqual(next, nullable(child)) {
		t.Errorf("self-referential Child.next = %s", next)
	}
}

func TestEmbeddedPromotionAndOmittedFields(t *testing.T) {
	g := buildFixture(t)
	typ := mustType(t, g, fixtureKey("Embedded"))
	got := wireNames(typ)
	want := []string{"id", "deep", "kind", "extra", "-", "mixed", "low", "zero", "direct", "ptr"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if f := fieldByWire(t, typ, "id"); f.PromotedFrom != fixtureKey("Base") || f.GoName != "ID" {
		t.Errorf("id promoted_from=%q go=%q", f.PromotedFrom, f.GoName)
	}
	if f := fieldByWire(t, typ, "kind"); f.PromotedFrom != "" {
		t.Errorf("outer kind must shadow Base.Kind, got promoted from %q", f.PromotedFrom)
	}
	if f := fieldByWire(t, typ, "mixed"); f.PromotedFrom != otherKey("Mixin") {
		t.Errorf("mixed promoted_from=%q", f.PromotedFrom)
	}
	if f := fieldByWire(t, typ, "low"); f.PromotedFrom != fixtureKey("lower") {
		t.Errorf("low promoted_from=%q", f.PromotedFrom)
	}
	if f := fieldByWire(t, typ, "deep"); !reflect.DeepEqual(f.Type, structRef(fixtureKey("Inner"))) {
		t.Errorf("deep = %s", f.Type)
	}
	if !fieldByWire(t, typ, "zero").OmitEmpty {
		t.Error("omitzero must set OmitEmpty")
	}
	if f := fieldByWire(t, typ, "direct"); !reflect.DeepEqual(f.Type, structRef(fixtureKey("Child"))) {
		t.Errorf("direct = %s", f.Type)
	}
	if f := fieldByWire(t, typ, "ptr"); !reflect.DeepEqual(f.Type, nullable(structRef(fixtureKey("Child")))) {
		t.Errorf("ptr = %s", f.Type)
	}
	// Embedded types are not generated unless referenced on their own.
	for _, absent := range []string{fixtureKey("Base"), fixtureKey("lower"), otherKey("Mixin")} {
		if _, ok := g.Type(absent); ok {
			t.Errorf("embedded-only type %s must not be in the graph", absent)
		}
	}
	if _, ok := g.Type(fixtureKey("Inner")); !ok {
		t.Error("Inner is referenced through the promoted field and must be in the graph")
	}
}

func TestAliasAndSerializers(t *testing.T) {
	g := buildFixture(t)
	if _, ok := g.Type(fixtureKey("AliasRoot")); ok {
		t.Error("alias root must resolve to its target, not register under the alias name")
	}
	resp := mustType(t, g, fixtureKey("Response"))
	if !resp.Root {
		t.Error("Response (alias target of AliasRoot) must be a root")
	}
	if f := fieldByWire(t, resp, "target"); !reflect.DeepEqual(f.Type, structRef(otherKey("Target"))) {
		t.Errorf("target = %s, want the alias target in package other", f.Type)
	}
	if _, ok := g.Type(fixtureKey("Alias")); ok {
		t.Error("alias must not be a graph type")
	}
	f := fieldByWire(t, resp, "frame_rate")
	wantSer := map[string]string{"kotlin": "org.example.FrameRateWire", "swift": "FrameRateWire"}
	if f.Type.Kind != KindCustom || f.Type.Nullable || !reflect.DeepEqual(f.Serializers, wantSer) {
		t.Errorf("frame_rate = %s serializers=%v", f.Type, f.Serializers)
	}
	fp := fieldByWire(t, resp, "frame_rate_ptr")
	if fp.Type.Kind != KindCustom || !fp.Type.Nullable || !reflect.DeepEqual(fp.Serializers, map[string]string{"kotlin": "org.example.FrameRateWire"}) {
		t.Errorf("frame_rate_ptr = %s serializers=%v, want Custom? kotlin only", fp.Type, fp.Serializers)
	}
	if fieldByWire(t, resp, "target").Serializers != nil {
		t.Error("a plain field must carry no serializers")
	}
	if _, ok := g.Type(otherKey("FrameRate")); ok {
		t.Error("a serializer field's Go type must not be walked")
	}

	var other *Package
	for _, p := range g.Packages {
		if p.Path == otherPath {
			other = p
		}
	}
	if other == nil || other.Registered {
		t.Fatalf("package other must appear as reached (unregistered): %+v", other)
	}
	if len(other.Types) != 2 || other.Types[0].Name != "Standalone" || other.Types[1].Name != "Target" {
		t.Errorf("other package types = %v", typeNames(other))
	}
	if other.ImportPath != g.ModulePath+"/"+otherPath {
		t.Errorf("import path %q", other.ImportPath)
	}
}

func TestDirectionPropagation(t *testing.T) {
	g := buildFixture(t)
	cases := map[string]registry.Direction{
		"Scalars":   registry.DirectionResponse,
		"Child":     registry.DirectionBoth, // Collections (response) and Embedded (request)
		"Embedded":  registry.DirectionRequest,
		"Inner":     registry.DirectionRequest,
		"Shared":    registry.DirectionBoth,
		"Request":   registry.DirectionRequest,
		"GatedOnly": registry.DirectionResponse,
		"PromoCard": registry.DirectionBoth, // Compat (response) and BloemOnly (both)
	}
	for name, want := range cases {
		typ := mustType(t, g, fixtureKey(name))
		if typ.Direction != want {
			t.Errorf("%s direction = %s, want %s", name, typ.Direction, want)
		}
		if want.Response() != typ.Direction.Response() {
			t.Errorf("%s Response() mismatch", name)
		}
	}
}

func TestGateAndDialectPropagation(t *testing.T) {
	g := buildFixture(t)
	if got := mustType(t, g, fixtureKey("Gated")).Gates; !reflect.DeepEqual(got, []string{"cap.gated"}) {
		t.Errorf("Gated gates = %v", got)
	}
	if got := mustType(t, g, fixtureKey("GatedOnly")).Gates; !containsString(got, "cap.gated") {
		t.Errorf("GatedOnly gates = %v, want inherited cap.gated (full set pinned in TestMultipleGates)", got)
	}
	if got := mustType(t, g, fixtureKey("GatedChild")).Gates; len(got) != 0 {
		t.Errorf("GatedChild gates = %v, want none: it is also reached from the ungated Response root", got)
	}
	if got := mustType(t, g, fixtureKey("PromoCard")).Gates; len(got) != 0 {
		t.Errorf("PromoCard gates = %v, want none: reached ungated through Compat", got)
	}
	bloem := mustType(t, g, fixtureKey("BloemOnly"))
	if bloem.Dialect != registry.DialectBloem || !reflect.DeepEqual(bloem.Gates, []string{"cap.bloem"}) {
		t.Errorf("BloemOnly dialect=%s gates=%v", bloem.Dialect, bloem.Gates)
	}

	compat := mustType(t, g, fixtureKey("Compat"))
	if compat.Dialect != registry.DialectUpstreamCompat || !reflect.DeepEqual(compat.BloemFields, []string{"promo"}) {
		t.Errorf("Compat dialect=%s bloem_fields=%v", compat.Dialect, compat.BloemFields)
	}
	if f := fieldByWire(t, compat, "plain"); f.Dialect != registry.DialectUpstreamCompat {
		t.Errorf("plain dialect = %s", f.Dialect)
	}
	if f := fieldByWire(t, compat, "promo"); f.Dialect != registry.DialectBloem {
		t.Errorf("promo dialect = %s, want bloem", f.Dialect)
	}
	// PromoCard is reached through Compat.promo (bloem-marked), Gated.promo
	// (upstream-compat, unmarked) and BloemOnly (bloem): any upstream path wins.
	if got := mustType(t, g, fixtureKey("PromoCard")).Dialect; got != registry.DialectUpstreamCompat {
		t.Errorf("PromoCard dialect = %s, want upstream-compat via Gated.promo", got)
	}
}

func TestBloemFieldOnlyPathIsBloem(t *testing.T) {
	reg := &registry.Registry{
		Schema: 1,
		Packages: []registry.Package{{
			Path:    fixturePath,
			Dialect: registry.DialectUpstreamCompat,
			Roots: []registry.Root{
				{Type: "Compat", Direction: registry.DirectionResponse, BloemFields: []string{"promo"}},
			},
		}},
	}
	g, err := Build(Config{Dir: repoRoot(t), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustType(t, g, fixtureKey("PromoCard")).Dialect; got != registry.DialectBloem {
		t.Errorf("PromoCard dialect = %s, want bloem when only reached through a bloem field", got)
	}
}

func TestUnreachedAndPackageOrder(t *testing.T) {
	g := buildFixture(t)
	if len(g.Packages) != 2 || g.Packages[0].Path != fixturePath || g.Packages[1].Path != otherPath {
		t.Fatalf("package order = %v", packagePaths(g))
	}
	if got := g.Packages[0].Unreached; !reflect.DeepEqual(got, []string{"Orphan"}) {
		t.Errorf("unreached = %v, want [Orphan] (Helper has no tags, Base/Inner are reached or embedded)", got)
	}
	types := g.Packages[0].Types
	for i := 1; i < len(types); i++ {
		if types[i-1].Name >= types[i].Name {
			t.Fatalf("types not sorted: %s before %s", types[i-1].Name, types[i].Name)
		}
	}
	if len(g.Types()) != len(types)+len(g.Packages[1].Types) {
		t.Error("Types() must list every package's types")
	}
}

func typeNames(p *Package) []string {
	out := make([]string, 0, len(p.Types))
	for _, t := range p.Types {
		out = append(out, t.Name)
	}
	return out
}

// TestCrossPackageAliasRoot: a root registered under an alias lands in the
// alias target's (reached) package and carries the registering root's
// metadata.
func TestCrossPackageAliasRoot(t *testing.T) {
	g := buildFixture(t)
	if _, ok := g.Type(fixtureKey("StandaloneAlias")); ok {
		t.Error("alias name must not be a graph type")
	}
	typ := mustType(t, g, otherKey("Standalone"))
	if typ.Package.Path != otherPath || typ.Package.Registered {
		t.Errorf("Standalone lives in %q registered=%v", typ.Package.Path, typ.Package.Registered)
	}
	if !typ.Root || typ.Direction != registry.DirectionRequest || typ.Dialect != registry.DialectBloem ||
		!reflect.DeepEqual(typ.Gates, []string{"cap.alias"}) {
		t.Errorf("Standalone root=%v direction=%s dialect=%s gates=%v", typ.Root, typ.Direction, typ.Dialect, typ.Gates)
	}
}

// TestMultipleGates pins the ruling: a type reached under two distinct gates
// carries the sorted set, and any one advertised gate suffices to decode it.
func TestMultipleGates(t *testing.T) {
	g := buildFixture(t)
	if got := mustType(t, g, fixtureKey("GatedOnly")).Gates; !reflect.DeepEqual(got, []string{"cap.gated", "cap.other"}) {
		t.Errorf("GatedOnly gates = %v, want [cap.gated cap.other]", got)
	}
	if got := mustType(t, g, fixtureKey("Gated2")).Gates; !reflect.DeepEqual(got, []string{"cap.other"}) {
		t.Errorf("Gated2 gates = %v", got)
	}
}

func packagePaths(g *Graph) []string {
	out := make([]string, 0, len(g.Packages))
	for _, p := range g.Packages {
		out = append(out, p.Path)
	}
	return out
}

func TestDumpIsDeterministic(t *testing.T) {
	root := repoRoot(t)
	var dumps [2]string
	for i := range dumps {
		g, err := Build(Config{Dir: root, Registry: fixtureRegistry()})
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := g.Dump(&buf); err != nil {
			t.Fatal(err)
		}
		dumps[i] = buf.String()
	}
	if dumps[0] != dumps[1] {
		t.Fatal("two builds dumped different bytes")
	}
	for _, want := range []string{
		"package " + fixturePath + " registered dialect=upstream-compat\n",
		"package " + otherPath + " reached\n",
		"  struct Compat direction=response dialect=upstream-compat root bloem_fields=promo\n",
		"    promo Promo Struct " + fixturePath + ".PromoCard? omitempty dialect=bloem\n",
		"  enum Protocol direction=both dialect=upstream-compat root\n    ProtocolHLS = \"hls\"\n",
		"    frame_rate Rate Custom serializers=kotlin:org.example.FrameRateWire,swift:FrameRateWire\n",
		"    frame_rate_ptr RatePtr Custom? omitempty serializers=kotlin:org.example.FrameRateWire\n",
		"  struct GatedOnly direction=response dialect=upstream-compat gate=cap.gated,cap.other\n",
		"  struct UnmarshalOnly direction=response dialect=upstream-compat root\n",
		"package " + otherPath + " reached\n  struct Standalone direction=request dialect=bloem gate=cap.alias root\n",
		"    id ID Long from=" + fixturePath + ".Base\n",
		"  unreached Orphan\n",
	} {
		if !strings.Contains(dumps[0], want) {
			t.Errorf("dump lacks %q\n%s", want, dumps[0])
		}
	}
}

func TestRefusals(t *testing.T) {
	root := repoRoot(t)
	cases := []struct {
		name      string
		root      string
		dir       registry.Direction
		wantType  string
		wantField string
		wantMsg   string
	}{
		{"generic", "GenericField", registry.DirectionResponse, refuseKey("GenericField"), "B", "generic type"},
		{"string option", "StringOption", registry.DirectionResponse, refuseKey("StringOption"), "N", `",string"`},
		{"untagged exported field", "Untagged", registry.DirectionResponse, refuseKey("Untagged"), "Name", "no json tag"},
		{"tag without name", "Unnamed", registry.DirectionResponse, refuseKey("Unnamed"), "Name", "no name"},
		{"custom marshaler", "MarshalerField", registry.DirectionResponse, refuseKey("MarshalerField"), "M", "MarshalJSON"},
		{"custom text unmarshaler", "TextMarshaler", registry.DirectionRequest, refuseKey("TextMarshaler"), "", "UnmarshalText"},
		{"embedded pointer", "EmbeddedPointer", registry.DirectionResponse, refuseKey("EmbeddedPointer"), "Base", "embedded pointer"},
		{"embedded with tag", "EmbeddedTagged", registry.DirectionResponse, refuseKey("EmbeddedTagged"), "Base", "json tag"},
		{"embedded non-struct", "EmbeddedNonStruct", registry.DirectionResponse, refuseKey("EmbeddedNonStruct"), "Named", "non-struct"},
		{"ambiguous promotion", "Ambiguous", registry.DirectionResponse, refuseKey("Ambiguous"), "ID", `wire name "id"`},
		{"anonymous struct", "Anonymous", registry.DirectionResponse, refuseKey("Anonymous"), "Inner", "anonymous struct"},
		{"map key", "MapKey", registry.DirectionResponse, refuseKey("MapKey"), "M", "map key"},
		{"pointer to pointer", "PointerPointer", registry.DirectionResponse, refuseKey("PointerPointer"), "P", "pointer to pointer"},
		{"interface", "Interface", registry.DirectionResponse, refuseKey("Interface"), "R", "interface"},
		{"uint", "Unsigned", registry.DirectionResponse, refuseKey("Unsigned"), "U", "unsupported basic type uint"},
		{"outside module", "Outside", registry.DirectionResponse, refuseKey("Outside"), "L", "outside module"},
		{"channel", "Channel", 0, refuseKey("Channel"), "C", "unsupported Go type"},
		{"json.Number", "NumberField", 0, refuseKey("NumberField"), "N", "json.Number"},
		{"unknown root", "Missing", registry.DirectionResponse, refuseKey("Missing"), "", "root not found"},
		{"root not a wire type", "Plain", registry.DirectionResponse, refuseKey("Plain"), "", "root must be a struct"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := tc.dir
			if dir == 0 {
				dir = registry.DirectionResponse
			}
			reg := &registry.Registry{Schema: 1, Packages: []registry.Package{{
				Path:    refusePath,
				Dialect: registry.DialectUpstreamCompat,
				Roots:   []registry.Root{{Type: tc.root, Direction: dir}},
			}}}
			g, err := Build(Config{Dir: root, Registry: reg})
			if err == nil {
				t.Fatalf("Build succeeded; graph: %v", packagePaths(g))
			}
			var refusal *RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("error is not a RefusalError: %v", err)
			}
			if refusal.Type != tc.wantType || refusal.Field != tc.wantField {
				t.Errorf("refusal names %s field %q, want %s field %q", refusal.Type, refusal.Field, tc.wantType, tc.wantField)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q lacks %q", err, tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.root) {
				t.Errorf("error %q does not name the Go type %s", err, tc.root)
			}
			if tc.wantField != "" {
				if refusal.Pos.Filename != refusePath+"/refuse.go" || refusal.Pos.Line == 0 {
					t.Errorf("refusal position = %s, want %s/refuse.go:<line>", refusal.Pos, refusePath)
				}
				if !strings.HasPrefix(err.Error(), refusePath+"/refuse.go:") {
					t.Errorf("error %q does not start with the repository-relative position", err)
				}
			}
		})
	}
}

func TestRefusalsAreCollected(t *testing.T) {
	reg := &registry.Registry{Schema: 1, Packages: []registry.Package{{
		Path:    refusePath,
		Dialect: registry.DialectUpstreamCompat,
		Roots: []registry.Root{
			{Type: "Untagged", Direction: registry.DirectionResponse},
			{Type: "StringOption", Direction: registry.DirectionResponse},
		},
	}}}
	_, err := Build(Config{Dir: repoRoot(t), Registry: reg})
	if err == nil {
		t.Fatal("Build succeeded")
	}
	for _, want := range []string{"Untagged field Name", "StringOption field N"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("joined error lacks %q: %v", want, err)
		}
	}
}

func TestRegistryLevelRefusals(t *testing.T) {
	root := repoRoot(t)
	t.Run("bloem_fields names a missing field", func(t *testing.T) {
		reg := &registry.Registry{Schema: 1, Packages: []registry.Package{{
			Path:    refusePath,
			Dialect: registry.DialectUpstreamCompat,
			Roots:   []registry.Root{{Type: "Fine", Direction: registry.DirectionResponse, BloemFields: []string{"nope"}}},
		}}}
		_, err := Build(Config{Dir: root, Registry: reg})
		if err == nil || !strings.Contains(err.Error(), `bloem_fields names "nope"`) || !strings.Contains(err.Error(), "Fine") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unused serializer", func(t *testing.T) {
		reg := &registry.Registry{Schema: 1, Packages: []registry.Package{{
			Path:    refusePath,
			Dialect: registry.DialectUpstreamCompat,
			Roots:   []registry.Root{{Type: "Fine", Direction: registry.DirectionResponse}},
		}}, Serializers: map[string]registry.Serializer{refusePath + ".Fine.missing": {"kotlin": "x.Y"}}}
		_, err := Build(Config{Dir: root, Registry: reg})
		if err == nil || !strings.Contains(err.Error(), "matches no reached field") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown package", func(t *testing.T) {
		reg := &registry.Registry{Schema: 1, Packages: []registry.Package{{
			Path:    "cmd/clientdtogen/internal/graph/testdata/nowhere",
			Dialect: registry.DialectUpstreamCompat,
			Roots:   []registry.Root{{Type: "X", Direction: registry.DirectionResponse}},
		}}}
		_, err := Build(Config{Dir: root, Registry: reg})
		if err == nil {
			t.Fatal("Build succeeded for a package that does not exist")
		}
	})
	t.Run("nil registry", func(t *testing.T) {
		if _, err := Build(Config{Dir: root}); err == nil {
			t.Fatal("Build succeeded with a nil registry")
		}
	})
}

// TestRealTreeStarterRegistry proves the committed registry loads against
// internal/playback and that the dump is stable across two builds.
func TestRealTreeStarterRegistry(t *testing.T) {
	root := repoRoot(t)
	reg, err := registry.Load(filepath.Join(root, "contracts", "client", "v1", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dumps [2]string
	var g *Graph
	for i := range dumps {
		g, err = Build(Config{Dir: root, Registry: reg})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		var buf bytes.Buffer
		if err := g.Dump(&buf); err != nil {
			t.Fatal(err)
		}
		dumps[i] = buf.String()
	}
	if dumps[0] != dumps[1] {
		t.Fatal("real-tree dump differs between builds")
	}
	if g.ModulePath != "github.com/Silo-Server/silo-server" {
		t.Errorf("module = %q", g.ModulePath)
	}
	for _, p := range reg.Packages {
		for _, r := range p.Roots {
			typ := mustType(t, g, p.Path+"."+r.Type)
			if !typ.Root || typ.Direction&r.Direction == 0 {
				t.Errorf("%s: root=%v direction=%s", typ.Key(), typ.Root, typ.Direction)
			}
		}
	}
	timeline := mustType(t, g, "internal/playback.TimelineV3")
	if !timeline.Direction.Response() || timeline.Direction.Request() {
		t.Errorf("TimelineV3 direction = %s, want response", timeline.Direction)
	}
	if f := fieldByWire(t, timeline, "seek_window_start_seconds"); !reflect.DeepEqual(f.Type, nullable(ref(KindDouble))) || !f.OmitEmpty {
		t.Errorf("seek_window_start_seconds = %s omitempty=%v", f.Type, f.OmitEmpty)
	}
	msgType := mustType(t, g, "internal/playback.RealtimeMessageType")
	if msgType.Kind != KindEnum || msgType.Direction != registry.DirectionBoth || !reflect.DeepEqual(msgType.Gates, []string{"realtime"}) {
		t.Errorf("RealtimeMessageType kind=%s direction=%s gates=%v", msgType.Kind, msgType.Direction, msgType.Gates)
	}
	if f := fieldByWire(t, mustType(t, g, "internal/playback.EventEnvelope"), "payload"); !reflect.DeepEqual(f.Type, nullable(ref(KindRaw))) {
		t.Errorf("EventEnvelope.payload = %s", f.Type)
	}
	if got := mustType(t, g, "internal/playback.TransformationV3").Gates; len(got) != 0 {
		t.Errorf("TransformationV3 gates = %v, want none (reached from ungated CapabilityResponseV3)", got)
	}
}
