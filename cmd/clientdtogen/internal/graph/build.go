package graph

import (
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/registry"
)

// Config selects what Build loads.
type Config struct {
	// Dir is the repository root: the directory holding go.mod. Registry
	// package paths are resolved relative to it.
	Dir string
	// Registry is the validated registry.
	Registry *registry.Registry
}

// RefusalError is a type-level fact the generator will not guess about
// (§3.2). Type is the graph key of the offending type; Field is the Go field
// name when the refusal is field-specific; Pos is the declaration site
// (repository-relative file) when go/types knows it.
type RefusalError struct {
	Type   string
	Field  string
	Reason string
	Pos    token.Position
}

func (e *RefusalError) Error() string {
	var b strings.Builder
	if e.Pos.IsValid() {
		fmt.Fprintf(&b, "%s: ", e.Pos)
	}
	fmt.Fprintf(&b, "refused %s", e.Type)
	if e.Field != "" {
		fmt.Fprintf(&b, " field %s", e.Field)
	}
	fmt.Fprintf(&b, ": %s", e.Reason)
	return b.String()
}

// Well-known types outside the module that map to a scalar kind rather than
// being walked. Keyed by "<import path>.<name>".
var knownNamed = map[string]TypeRef{
	"time.Time":                   {Kind: KindTime},
	"github.com/google/uuid.UUID": {Kind: KindUUID},
	"encoding/json.RawMessage":    {Kind: KindRaw, Nullable: true},

	// apiv2's instants carry custom marshallers, so without an entry here the
	// graph refuses them as "response shape invisible". The shape is not
	// invisible, it is declared: both types implement huma.Schema as a
	// date-time string, non-zero, UTC with millisecond precision, and the
	// nullable one as that or null. Mapping them to the same kind as time.Time
	// keeps one client representation for every instant on the wire.
	// apiv2.JSONValue is []byte that marshals as the raw JSON it holds, or
	// null when empty — the same role encoding/json.RawMessage plays above, and
	// it takes the same client representation.
	"github.com/Silo-Server/silo-server/internal/apiv2.JSONValue": {Kind: KindRaw, Nullable: true},
	// The same shape under four more names: each is a json.RawMessage carrying
	// JSON whose form the server does not know -- a plugin's form default, a
	// plugin config value, a policy document or decision sample, a navigation
	// shortcut item. Their custom MarshalJSON only passes the bytes through,
	// so the wire shape is raw JSON rather than something hidden.
	"github.com/Silo-Server/silo-server/internal/apiv2.ScanSourceDefaultValue":  {Kind: KindRaw, Nullable: true},
	"github.com/Silo-Server/silo-server/internal/apiv2.PluginConfigSchemaValue": {Kind: KindRaw, Nullable: true},
	"github.com/Silo-Server/silo-server/internal/apiv2.PolicyJSON":              {Kind: KindRaw, Nullable: true},
	"github.com/Silo-Server/silo-server/internal/apiv2.NavigationShortcutItem":  {Kind: KindRaw, Nullable: true},

	"github.com/Silo-Server/silo-server/internal/apiv2.Instant":         {Kind: KindTime},
	"github.com/Silo-Server/silo-server/internal/apiv2.NullableInstant": {Kind: KindTime, Nullable: true},
}

// knownGenericOrigins maps a generic type, by the qualified name of its origin,
// to the wire shape every instantiation of it has. It is consulted before the
// instantiation is emitted as a type of its own.
var knownGenericOrigins = map[string]TypeRef{
	// Patch[T] is the presence-aware PATCH transport: a field is absent
	// (unchanged), null (cleared) or a value. Three states, and the struct
	// carrying them has no json tags at all -- its wire shape comes entirely
	// from its MarshalJSON.
	//
	// Raw is what expresses all three in both target languages without a
	// hand-written transport type in each: an absent field is the client's own
	// null, which both emitters omit rather than write, while an explicit JSON
	// null is a raw value that is written. A nullable T could not do this --
	// the emitters drop a null rather than send one, so "clear this field"
	// would be unsendable, and a PATCH body that cannot clear a field is worse
	// than one that is loosely typed.
	//
	// The cost is real: the client no longer sees T, so it can construct a
	// value of the wrong type and learn about it from a 422 rather than from
	// its compiler. The server validates the field against the schema either
	// way, and the OpenAPI document still states T. If that trade stops being
	// worth it, the replacement is a generated three-state wrapper plus a
	// hand-written Patch<T> in each client, not a nullable T.
	"github.com/Silo-Server/silo-server/internal/apiv2.Patch": {Kind: KindRaw, Nullable: true},
}

// marshalerMethods are the method names encoding/json dispatches on, split by
// the side of the wire they change: marshal-side methods hide the response
// shape, unmarshal-side methods hide the request shape.
var (
	marshalMethods   = []string{"MarshalJSON", "MarshalText"}
	unmarshalMethods = []string{"UnmarshalJSON", "UnmarshalText"}
)

// Build loads the registered packages and returns the type graph, or an error
// joining every refusal found so a registry author sees them all at once.
func Build(cfg Config) (*Graph, error) {
	if cfg.Registry == nil {
		return nil, errors.New("graph: nil registry")
	}
	b := &builder{
		cfg:             cfg,
		fset:            token.NewFileSet(),
		types:           map[string]*Type{},
		packages:        map[string]*Package{},
		serializersUsed: map[string]bool{},
		embedded:        map[string]bool{},
		unmarshalOnly:   map[string]string{},
	}
	if err := b.load(); err != nil {
		return nil, err
	}
	b.resolveRoots()
	b.checkSerializers()
	if len(b.refusals) > 0 {
		return nil, errors.Join(b.refusals...)
	}
	b.propagate()
	b.checkUnmarshalOnly()
	if len(b.refusals) > 0 {
		return nil, errors.Join(b.refusals...)
	}
	b.recordUnreached()
	return b.finish(), nil
}

type builder struct {
	cfg    Config
	fset   *token.FileSet
	module string

	loaded          map[string]*packages.Package // by repo path
	types           map[string]*Type
	packages        map[string]*Package // by repo path
	roots           []rootRef
	refusals        []error
	serializersUsed map[string]bool
	embedded        map[string]bool // graph keys of structs inlined somewhere
	// unmarshalOnly maps the graph key of every reached type carrying a
	// custom unmarshaler (and no custom marshaler) to the method name. Such
	// types are response-only: their request shape is invisible.
	unmarshalOnly map[string]string
}

type rootRef struct {
	typ  *Type
	pkg  registry.Package
	root registry.Root
}

func (b *builder) refuse(typeKey, field, reason string, pos token.Pos) {
	position := b.fset.Position(pos)
	if position.IsValid() {
		if rel, err := filepath.Rel(b.cfg.Dir, position.Filename); err == nil {
			position.Filename = filepath.ToSlash(rel)
		}
	}
	b.refusals = append(b.refusals, &RefusalError{Type: typeKey, Field: field, Reason: reason, Pos: position})
}

func (b *builder) load() error {
	patterns := make([]string, 0, len(b.cfg.Registry.Packages))
	for _, p := range b.cfg.Registry.Packages {
		patterns = append(patterns, "./"+p.Path)
	}
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedTypes |
			packages.NeedImports | packages.NeedModule | packages.NeedDeps,
		Dir:  b.cfg.Dir,
		Fset: b.fset,
	}, patterns...)
	if err != nil {
		return fmt.Errorf("loading packages: %w", err)
	}
	var loadErrs []error
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, fmt.Errorf("%s: %s", p.PkgPath, e.Msg))
		}
	}
	if len(loadErrs) > 0 {
		return errors.Join(loadErrs...)
	}
	if len(pkgs) == 0 {
		return errors.New("no packages loaded")
	}
	for _, p := range pkgs {
		if p.Module == nil {
			return fmt.Errorf("package %s is not in a Go module", p.PkgPath)
		}
		if b.module == "" {
			b.module = p.Module.Path
		} else if b.module != p.Module.Path {
			return fmt.Errorf("packages span modules %s and %s", b.module, p.Module.Path)
		}
	}
	b.loaded = map[string]*packages.Package{}
	for _, p := range pkgs {
		b.loaded[strings.TrimPrefix(p.PkgPath, b.module+"/")] = p
	}
	for _, rp := range b.cfg.Registry.Packages {
		p, ok := b.loaded[rp.Path]
		if !ok {
			return fmt.Errorf("registered package %s was not loaded (module %s)", rp.Path, b.module)
		}
		b.packages[rp.Path] = &Package{
			Path:       rp.Path,
			ImportPath: p.PkgPath,
			Registered: true,
			Dialect:    rp.Dialect,
			Gate:       rp.Gate,
		}
	}
	return nil
}

// repoPath returns the repository-relative path of a package and whether it
// belongs to the module being generated.
func (b *builder) repoPath(pkg *types.Package) (string, bool) {
	if pkg == nil {
		return "", false
	}
	if pkg.Path() == b.module {
		return ".", true
	}
	rest, ok := strings.CutPrefix(pkg.Path(), b.module+"/")
	return rest, ok
}

func (b *builder) packageFor(pkg *types.Package) *Package {
	path, _ := b.repoPath(pkg)
	if p, ok := b.packages[path]; ok {
		return p
	}
	p := &Package{Path: path, ImportPath: pkg.Path()}
	b.packages[path] = p
	return p
}

func (b *builder) resolveRoots() {
	for _, rp := range b.cfg.Registry.Packages {
		scope := b.loaded[rp.Path].Types.Scope()
		for _, root := range rp.Roots {
			key := typeKey(rp.Path, root.Type)
			rooted, pos, err := lookupRootType(scope, root.Type)
			if err != nil {
				b.refuse(key, "", err.Error()+" in package "+rp.Path, token.NoPos)
				continue
			}
			ref := b.resolveRef(rooted, key, "", pos)
			t, ok := b.types[ref.Named]
			if !ok || (ref.Kind != KindStruct && ref.Kind != KindEnum) {
				if ref.Kind != KindInvalid {
					b.refuse(key, "", fmt.Sprintf("root must be a struct or a string type with constants, not %s", ref), pos)
				}
				continue
			}
			t.Root = true
			t.BloemFields = append([]string(nil), root.BloemFields...)
			for _, wire := range root.BloemFields {
				if !hasWire(t.Fields, wire) {
					b.refuse(key, "", fmt.Sprintf("bloem_fields names %q, which is not a wire field", wire), pos)
				}
			}
			b.roots = append(b.roots, rootRef{typ: t, pkg: rp, root: root})
		}
	}
}

func hasWire(fields []Field, wire string) bool {
	for _, f := range fields {
		if f.WireName == wire {
			return true
		}
	}
	return false
}

func (b *builder) checkSerializers() {
	keys := make([]string, 0, len(b.cfg.Registry.Serializers))
	for k := range b.cfg.Registry.Serializers {
		if !b.serializersUsed[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.refusals = append(b.refusals, fmt.Errorf("serializer %q matches no reached field", k))
	}
}

// checkUnmarshalOnly refuses reached types that carry a custom unmarshaler
// and ended up request-reachable: their request wire shape is invisible, so
// generating them would let a client encode bytes the server cannot read.
// Runs after propagation, when directions are final.
func (b *builder) checkUnmarshalOnly() {
	keys := make([]string, 0, len(b.unmarshalOnly))
	for key := range b.unmarshalOnly {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t := b.types[key]
		if t == nil {
			continue
		}
		if t.Direction.Request() {
			b.refuse(key, "", fmt.Sprintf("%s has a custom %s; its request shape is invisible, so it may only be registered response-reachable", key, b.unmarshalOnly[key]), token.NoPos)
		}
	}
}

// resolveRef maps a Go type to a TypeRef, registering named struct and enum
// types as a side effect. ownerKey/field/pos name the location for refusals.
func (b *builder) resolveRef(t types.Type, ownerKey, field string, pos token.Pos) TypeRef {
	t = types.Unalias(t)
	switch tt := t.(type) {
	case *types.Pointer:
		inner := b.resolveRef(tt.Elem(), ownerKey, field, pos)
		if _, isPtr := types.Unalias(tt.Elem()).(*types.Pointer); isPtr {
			b.refuse(ownerKey, field, "pointer to pointer has no wire meaning", pos)
			return TypeRef{}
		}
		inner.Nullable = true
		return inner
	case *types.Named:
		return b.resolveNamed(tt, ownerKey, field, pos)
	case *types.Basic:
		return b.resolveBasic(tt, ownerKey, field, pos)
	case *types.Slice:
		if isByte(tt.Elem()) {
			return TypeRef{Kind: KindBytes}
		}
		elem := b.resolveRef(tt.Elem(), ownerKey, field, pos)
		return TypeRef{Kind: KindList, Elem: &elem}
	case *types.Array:
		elem := b.resolveRef(tt.Elem(), ownerKey, field, pos)
		return TypeRef{Kind: KindList, Elem: &elem}
	case *types.Map:
		if basic, ok := types.Unalias(tt.Key()).Underlying().(*types.Basic); !ok || basic.Kind() != types.String {
			b.refuse(ownerKey, field, fmt.Sprintf("map key %s is not a string", tt.Key()), pos)
			return TypeRef{}
		}
		elem := b.resolveRef(tt.Elem(), ownerKey, field, pos)
		return TypeRef{Kind: KindMap, Elem: &elem}
	case *types.Interface:
		if tt.Empty() {
			return TypeRef{Kind: KindRaw, Nullable: true}
		}
		b.refuse(ownerKey, field, "non-empty interface type has no wire shape", pos)
	case *types.Struct:
		return b.resolveAnonymousStruct(tt, ownerKey, field, pos)
	default:
		b.refuse(ownerKey, field, fmt.Sprintf("unsupported Go type %s", t), pos)
	}
	return TypeRef{}
}

func isByte(t types.Type) bool {
	basic, ok := types.Unalias(t).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Uint8
}

func (b *builder) resolveBasic(t *types.Basic, ownerKey, field string, pos token.Pos) TypeRef {
	switch t.Kind() {
	case types.Bool:
		return TypeRef{Kind: KindBool}
	case types.String:
		return TypeRef{Kind: KindString}
	case types.Int, types.Int8, types.Int16, types.Int32, types.Uint8, types.Uint16, types.Uint32:
		return TypeRef{Kind: KindInt}
	case types.Int64, types.Uint64:
		return TypeRef{Kind: KindLong}
	case types.Float32, types.Float64:
		return TypeRef{Kind: KindDouble}
	}
	b.refuse(ownerKey, field, fmt.Sprintf("unsupported basic type %s", t), pos)
	return TypeRef{}
}

func (b *builder) resolveNamed(t *types.Named, ownerKey, field string, pos token.Pos) TypeRef {
	obj := t.Obj()
	// An uninstantiated generic has no single wire shape and stays refused.
	// An instantiated one does have a shape, and is emitted under a name built
	// from its type arguments — see instantiatedName.
	if t.TypeArgs().Len() == 0 && t.TypeParams().Len() > 0 {
		b.refuse(ownerKey, field, fmt.Sprintf("uninstantiated generic type %s", types.TypeString(t, nil)), pos)
		return TypeRef{}
	}
	if t.TypeArgs().Len() > 0 {
		if ref, ok := knownGenericOrigins[originQualified(t)]; ok {
			return ref
		}
		return b.resolveInstantiated(t, ownerKey, field, pos)
	}
	qualified := obj.Name()
	if obj.Pkg() != nil {
		qualified = obj.Pkg().Path() + "." + obj.Name()
	}
	if ref, ok := knownNamed[qualified]; ok {
		return ref
	}
	if qualified == "encoding/json.Number" {
		b.refuse(ownerKey, field, "json.Number is a string in Go but a bare number on the wire; state the numeric type instead", pos)
		return TypeRef{}
	}
	if m := hasCustomMarshal(t); m != "" {
		b.refuse(ownerKey, field, fmt.Sprintf("%s has a custom %s; its response shape is invisible — add a registry serializer for the field or project it into a named type", qualified, m), pos)
		return TypeRef{}
	}
	if u := hasCustomUnmarshal(t); u != "" {
		// A custom unmarshaler changes only how the server reads the value;
		// the bytes it writes are still the plain struct. The type may join
		// the graph, but only as response-reachable — checked after
		// propagation, when directions are known.
		key := ""
		if path, inModule := b.repoPath(obj.Pkg()); inModule {
			key = typeKey(path, obj.Name())
		}
		b.unmarshalOnly[key] = u
	}
	switch under := t.Underlying().(type) {
	case *types.Struct:
		path, inModule := b.repoPath(obj.Pkg())
		if !inModule {
			b.refuse(ownerKey, field, fmt.Sprintf("struct type %s is outside module %s", qualified, b.module), pos)
			return TypeRef{}
		}
		key := typeKey(path, obj.Name())
		existing, seen := b.types[key]
		// The mirror of the checks in resolveInstantiated and
		// resolveAnonymousStruct: whichever is reached first, a declared type
		// and a synthesized one that want the same name are refused rather
		// than merged. Which wins the race depends on walk order, so the
		// refusal cannot live on one side alone.
		if seen && existing.synthesizedFrom != "" {
			b.refuse(ownerKey, field, fmt.Sprintf("%s collides with %s, which already claims that name", qualified, existing.synthesizedFrom), pos)
			return TypeRef{}
		}
		if !seen {
			b.buildStruct(key, obj, under)
		}
		return TypeRef{Kind: KindStruct, Named: key}
	case *types.Basic:
		if under.Kind() == types.String {
			consts := b.constantsOf(t)
			if len(consts) > 0 {
				path, inModule := b.repoPath(obj.Pkg())
				if !inModule {
					b.refuse(ownerKey, field, fmt.Sprintf("string type %s with constants is outside module %s", qualified, b.module), pos)
					return TypeRef{}
				}
				key := typeKey(path, obj.Name())
				if _, ok := b.types[key]; !ok {
					b.types[key] = &Type{
						Name:      obj.Name(),
						Package:   b.packageFor(obj.Pkg()),
						Kind:      KindEnum,
						Constants: consts,
					}
				}
				return TypeRef{Kind: KindEnum, Named: key}
			}
		}
		return b.resolveBasic(under, ownerKey, field, pos)
	default:
		return b.resolveRef(under, ownerKey, field, pos)
	}
}

// hasCustomMarshal reports the first marshal-side method (which hides the
// response shape) the type or its pointer carries, or "".
func hasCustomMarshal(t types.Type) string {
	return firstMethod(t, marshalMethods)
}

// hasCustomUnmarshal reports the first unmarshal-side method (which hides the
// request shape) the type or its pointer carries, or "".
func hasCustomUnmarshal(t types.Type) string {
	return firstMethod(t, unmarshalMethods)
}

func firstMethod(t types.Type, names []string) string {
	for _, set := range []*types.MethodSet{types.NewMethodSet(t), types.NewMethodSet(types.NewPointer(t))} {
		for _, name := range names {
			if set.Lookup(nil, name) != nil {
				return name
			}
		}
	}
	return ""
}

// constantsOf lists the exported constants declared with exactly this type,
// in source order.
func (b *builder) constantsOf(t *types.Named) []Constant {
	pkg := t.Obj().Pkg()
	if pkg == nil {
		return nil
	}
	type posConst struct {
		c   Constant
		pos token.Position
	}
	var found []posConst
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !c.Exported() || !types.Identical(c.Type(), t) {
			continue
		}
		found = append(found, posConst{
			c:   Constant{GoName: c.Name(), Value: constant.StringVal(c.Val())},
			pos: b.fset.Position(c.Pos()),
		})
	}
	sort.SliceStable(found, func(i, j int) bool {
		pi, pj := found[i].pos, found[j].pos
		if pi.Filename != pj.Filename {
			return pi.Filename < pj.Filename
		}
		if pi.Offset != pj.Offset {
			return pi.Offset < pj.Offset
		}
		return found[i].c.GoName < found[j].c.GoName
	})
	out := make([]Constant, len(found))
	for i, f := range found {
		out[i] = f.c
	}
	return out
}

// rawField is a collected struct field before encoding/json's promotion
// conflict rule is applied.
type rawField struct {
	Field
	depth int
	pos   token.Pos
}

func (b *builder) buildStruct(key string, obj *types.TypeName, st *types.Struct) {
	b.buildStructNamed(key, obj, st, obj.Name())
}

// buildStructNamed is buildStruct with the emitted name stated separately, so
// an instantiated generic can be emitted under a name of its own while its
// fields still come from the substituted struct go/types hands back.
func (b *builder) buildStructNamed(key string, obj *types.TypeName, st *types.Struct, name string) {
	pkgPath, _ := b.repoPath(obj.Pkg())
	b.buildStructIn(key, b.packageFor(obj.Pkg()), pkgPath, st, name)
}

// buildStructIn is buildStructNamed for a struct that has no TypeName at all,
// so the owning package is stated rather than read off a declaration.
func (b *builder) buildStructIn(key string, pkg *Package, pkgPath string, st *types.Struct, name string) {
	t := &Type{Name: name, Package: pkg, Kind: KindStruct}
	b.types[key] = t // registered before the walk so cycles terminate
	raw := b.collectFields(key, pkgPath, name, st, 0, "")
	t.Fields = resolvePromotion(b, key, raw)
}

// instantiatedName is the name an instantiated generic is emitted under: its
// type arguments, then the generic's own name. Collection[UserLibrary] becomes
// UserLibraryCollection, which is what the hand-written envelopes in apiv2
// already call themselves (ProfileCollection, HistoryCollection), so generated
// and declared types read alike.
//
// Deterministic by construction: the same instantiation always produces the
// same name, and two different instantiations cannot produce one name, because
// the argument names are what differ.
func instantiatedName(t *types.Named) string {
	args := t.TypeArgs()
	if args.Len() == 0 {
		return t.Obj().Name()
	}
	var prefix strings.Builder
	for i := 0; i < args.Len(); i++ {
		arg := types.Unalias(args.At(i))
		if named, ok := arg.(*types.Named); ok {
			prefix.WriteString(instantiatedName(named))
			continue
		}
		prefix.WriteString(exportedBasicName(types.TypeString(arg, nil)))
	}
	return prefix.String() + t.Obj().Name()
}

// exportedBasicName turns a non-named type argument into a name fragment:
// Collection[string] reads as StringCollection.
func exportedBasicName(goType string) string {
	cleaned := strings.NewReplacer("[]", "List", "*", "", ".", "").Replace(goType)
	if cleaned == "" {
		return "Value"
	}
	return strings.ToUpper(cleaned[:1]) + cleaned[1:]
}

// collectFields walks a struct's fields depth-first, inlining embedded structs
// at the embed point the way encoding/json promotes them.
func (b *builder) collectFields(ownerKey, pkgPath, typeName string, st *types.Struct, depth int, promotedFrom string) []rawField {
	var out []rawField
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag, hasTag := reflect.StructTag(st.Tag(i)).Lookup("json")
		if f.Embedded() {
			out = append(out, b.collectEmbedded(ownerKey, pkgPath, typeName, f, hasTag, depth)...)
			continue
		}
		if !f.Exported() {
			continue
		}
		if !hasTag {
			b.refuse(ownerKey, f.Name(), "exported field has no json tag; the contract must state the wire name", f.Pos())
			continue
		}
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			b.refuse(ownerKey, f.Name(), "json tag has no name; the contract must state the wire name", f.Pos())
			continue
		}
		field := Field{GoName: f.Name(), WireName: name, PromotedFrom: promotedFrom}
		for _, opt := range strings.Split(opts, ",") {
			switch opt {
			case "omitempty":
				// encoding/json never omits a struct or array value on omitempty.
				field.OmitEmpty = canBeEmpty(f.Type())
			case "omitzero":
				field.OmitEmpty = true
			case "string":
				b.refuse(ownerKey, f.Name(), "json \",string\" option is not representable", f.Pos())
			}
		}
		serializerKey := registry.SerializerKey(pkgPath, typeName, name)
		if s, ok := b.cfg.Registry.Serializers[serializerKey]; ok {
			b.serializersUsed[serializerKey] = true
			field.Serializers = map[string]string{}
			for lang, name := range s {
				field.Serializers[lang] = name
			}
			_, isPtr := types.Unalias(f.Type()).(*types.Pointer)
			field.Type = TypeRef{Kind: KindCustom, Nullable: isPtr}
		} else {
			field.Type = b.resolveRef(f.Type(), ownerKey, f.Name(), f.Pos())
		}
		out = append(out, rawField{Field: field, depth: depth, pos: f.Pos()})
	}
	return out
}

// canBeEmpty reports whether encoding/json's omitempty can ever omit a value
// of this type: false for structs and arrays, which have no empty value.
func canBeEmpty(t types.Type) bool {
	switch types.Unalias(t).Underlying().(type) {
	case *types.Struct, *types.Array:
		return false
	}
	return true
}

func (b *builder) collectEmbedded(ownerKey, pkgPath, typeName string, f *types.Var, hasTag bool, depth int) []rawField {
	if hasTag {
		b.refuse(ownerKey, f.Name(), "embedded field with a json tag is not supported", f.Pos())
		return nil
	}
	ft := types.Unalias(f.Type())
	if _, isPtr := ft.(*types.Pointer); isPtr {
		b.refuse(ownerKey, f.Name(), "embedded pointer is not supported", f.Pos())
		return nil
	}
	named, ok := ft.(*types.Named)
	if !ok {
		b.refuse(ownerKey, f.Name(), "embedded field must be a named struct type", f.Pos())
		return nil
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		b.refuse(ownerKey, f.Name(), fmt.Sprintf("embedded non-struct type %s is not supported", types.TypeString(named, nil)), f.Pos())
		return nil
	}
	// An *instantiated* generic is safe to flatten: go/types hands back an
	// underlying struct whose field types are already substituted, so
	// Collection[Profile] contributes `items []Profile` and `page *PageInfo`
	// with their tags intact — nothing is guessed. Only an uninstantiated
	// generic is refused, which is the case the spec's rationale is about: a
	// helper like optionalField[T] has no single wire shape to emit.
	//
	// The distinction has to be drawn on TypeArgs, not TypeParams: an
	// instantiated named type still reports its origin's parameters, so
	// TypeParams() is non-empty for Collection[Profile] as well.
	if named.TypeArgs().Len() == 0 && named.TypeParams().Len() > 0 {
		b.refuse(ownerKey, f.Name(), fmt.Sprintf("embedded uninstantiated generic type %s", types.TypeString(named, nil)), f.Pos())
		return nil
	}
	embPath, inModule := b.repoPath(named.Obj().Pkg())
	if !inModule {
		b.refuse(ownerKey, f.Name(), fmt.Sprintf("embedded type %s is outside module %s", types.TypeString(named, nil), b.module), f.Pos())
		return nil
	}
	embKey := typeKey(embPath, embeddedTypeName(named))
	b.embedded[embKey] = true
	// Fields keep the outer type's serializer namespace: the registry author
	// writes the key against the type whose wire object carries the field.
	return b.collectFields(ownerKey, pkgPath, typeName, st, depth+1, embKey)
}

// resolvePromotion applies encoding/json's rule for duplicate wire names: the
// shallowest declaration wins; a tie at the same depth is ambiguous and, where
// encoding/json would silently drop both, the graph refuses.
func resolvePromotion(b *builder, ownerKey string, raw []rawField) []Field {
	minDepth := map[string]int{}
	for _, f := range raw {
		if d, ok := minDepth[f.WireName]; !ok || f.depth < d {
			minDepth[f.WireName] = f.depth
		}
	}
	winners := map[string]int{}
	for _, f := range raw {
		if f.depth == minDepth[f.WireName] {
			winners[f.WireName]++
		}
	}
	var out []Field
	reported := map[string]bool{}
	for _, f := range raw {
		if f.depth != minDepth[f.WireName] {
			continue
		}
		if winners[f.WireName] > 1 {
			if !reported[f.WireName] {
				b.refuse(ownerKey, f.GoName, fmt.Sprintf("wire name %q is declared more than once at the same depth", f.WireName), f.pos)
				reported[f.WireName] = true
			}
			continue
		}
		out = append(out, f.Field)
	}
	return out
}

// recordUnreached fills Package.Unreached for registered packages.
func (b *builder) recordUnreached() {
	for path, p := range b.loaded {
		pkg := b.packages[path]
		if pkg == nil || !pkg.Registered {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || !obj.Exported() || obj.IsAlias() {
				continue
			}
			st, ok := obj.Type().Underlying().(*types.Struct)
			if !ok || !hasJSONTag(st) {
				continue
			}
			key := typeKey(path, name)
			if _, reached := b.types[key]; !reached && !b.embedded[key] {
				pkg.Unreached = append(pkg.Unreached, name)
			}
		}
		sort.Strings(pkg.Unreached)
	}
}

func hasJSONTag(st *types.Struct) bool {
	for i := 0; i < st.NumFields(); i++ {
		if _, ok := reflect.StructTag(st.Tag(i)).Lookup("json"); ok {
			return true
		}
	}
	return false
}

func (b *builder) finish() *Graph {
	g := &Graph{ModulePath: b.module, types: b.types}
	for _, t := range b.types {
		t.Package.Types = append(t.Package.Types, t)
	}
	for _, p := range b.packages {
		sort.Slice(p.Types, func(i, j int) bool { return p.Types[i].Name < p.Types[j].Name })
	}
	for _, rp := range b.cfg.Registry.Packages {
		g.Packages = append(g.Packages, b.packages[rp.Path])
	}
	var reached []*Package
	for _, p := range b.packages {
		if !p.Registered && len(p.Types) > 0 {
			reached = append(reached, p)
		}
	}
	sort.Slice(reached, func(i, j int) bool { return reached[i].Path < reached[j].Path })
	g.Packages = append(g.Packages, reached...)
	return g
}

// embeddedTypeName names an embedded type for the serializer namespace,
// distinguishing instantiations of one generic: Collection[Profile] and
// Collection[Notification] flatten different fields and must not share a key.
func embeddedTypeName(named *types.Named) string {
	name := named.Obj().Name()
	args := named.TypeArgs()
	if args.Len() == 0 {
		return name
	}
	parts := make([]string, 0, args.Len())
	for i := 0; i < args.Len(); i++ {
		if arg, ok := types.Unalias(args.At(i)).(*types.Named); ok {
			parts = append(parts, arg.Obj().Name())
			continue
		}
		parts = append(parts, types.TypeString(args.At(i), nil))
	}
	return name + "[" + strings.Join(parts, ",") + "]"
}

// resolveInstantiated emits an instantiated generic as its own named struct.
//
// The underlying struct go/types returns is already substituted, so the fields
// are concrete and nothing is guessed. The only new decision is the name, and
// the only new hazard is that name colliding with a type that already exists —
// which is refused rather than silently merged, because two different wire
// shapes under one name is precisely the failure this naming exists to avoid.
func (b *builder) resolveInstantiated(t *types.Named, ownerKey, field string, pos token.Pos) TypeRef {
	obj := t.Obj()
	under, ok := t.Underlying().(*types.Struct)
	if !ok {
		b.refuse(ownerKey, field, fmt.Sprintf("instantiated generic %s is not a struct", types.TypeString(t, nil)), pos)
		return TypeRef{}
	}
	path, inModule := b.repoPath(obj.Pkg())
	if !inModule {
		b.refuse(ownerKey, field, fmt.Sprintf("instantiated generic %s is outside module %s", types.TypeString(t, nil), b.module), pos)
		return TypeRef{}
	}
	name := instantiatedName(t)
	key := typeKey(path, name)
	if existing, seen := b.types[key]; seen {
		if existing.synthesizedFrom == "" {
			b.refuse(ownerKey, field, fmt.Sprintf("instantiated generic %s would be emitted as %s, which a declared type in that package already claims", types.TypeString(t, nil), name), pos)
			return TypeRef{}
		}
		if existing.synthesizedFrom != types.TypeString(t, nil) {
			b.refuse(ownerKey, field, fmt.Sprintf("instantiated generic %s would be emitted as %s, which %s already claims", types.TypeString(t, nil), name, existing.synthesizedFrom), pos)
			return TypeRef{}
		}
		return TypeRef{Kind: KindStruct, Named: key}
	}
	b.buildStructNamed(key, obj, under, name)
	b.types[key].synthesizedFrom = types.TypeString(t, nil)
	return TypeRef{Kind: KindStruct, Named: key}
}

// lookupRootType resolves a registry root's type name in a package scope.
//
// Most roots are a plain identifier. A root may also name an instantiation --
// "Collection[UserLibrary]" -- because v2 returns its list pages as one generic
// envelope rather than a declared type per endpoint, and requiring a declared
// wrapper for each would mean ~50 server-side types that exist only to give the
// registry something to point at. The instantiation is emitted under the name
// built by instantiatedName, so "Collection[UserLibrary]" generates
// UserLibraryCollection.
//
// Type arguments must live in the same package. A cross-package argument is
// refused rather than guessed at, because resolving one means deciding which
// package a bare name belongs to, and a wrong guess silently generates the
// wrong shape.
func lookupRootType(scope *types.Scope, name string) (types.Type, token.Pos, error) {
	open := strings.IndexByte(name, '[')
	if open < 0 {
		obj, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			return nil, token.NoPos, errors.New("root not found")
		}
		return obj.Type(), obj.Pos(), nil
	}
	if !strings.HasSuffix(name, "]") {
		return nil, token.NoPos, fmt.Errorf("root %q opens a type-argument list it never closes", name)
	}
	generic, ok := scope.Lookup(name[:open]).(*types.TypeName)
	if !ok {
		return nil, token.NoPos, fmt.Errorf("root %q names a generic type that is not declared", name)
	}
	origin, ok := generic.Type().(*types.Named)
	if !ok || origin.TypeParams().Len() == 0 {
		return nil, token.NoPos, fmt.Errorf("root %q states type arguments, but %s is not generic", name, name[:open])
	}
	var args []types.Type
	for _, argName := range strings.Split(name[open+1:len(name)-1], ",") {
		argName = strings.TrimSpace(argName)
		if strings.ContainsAny(argName, ".[]") {
			return nil, token.NoPos, fmt.Errorf("root %q takes its type argument %q from another package; declare a named envelope for it instead", name, argName)
		}
		arg, ok := scope.Lookup(argName).(*types.TypeName)
		if !ok {
			return nil, token.NoPos, fmt.Errorf("root %q names the type argument %q, which is not declared", name, argName)
		}
		args = append(args, arg.Type())
	}
	if len(args) != origin.TypeParams().Len() {
		return nil, token.NoPos, fmt.Errorf("root %q states %d type arguments; %s takes %d", name, len(args), name[:open], origin.TypeParams().Len())
	}
	inst, err := types.Instantiate(nil, origin, args, true)
	if err != nil {
		return nil, token.NoPos, fmt.Errorf("root %q does not instantiate: %w", name, err)
	}
	return inst, generic.Pos(), nil
}

// EmittedRootName is the name a registry root's type is emitted under. It is
// the root's own name for a plain root, and the instantiated name for one that
// states type arguments, so a caller holding only the registry can find the
// type the root produced.
func EmittedRootName(root string) string {
	open := strings.IndexByte(root, '[')
	if open < 0 || !strings.HasSuffix(root, "]") {
		return root
	}
	var name strings.Builder
	for _, arg := range strings.Split(root[open+1:len(root)-1], ",") {
		name.WriteString(strings.TrimSpace(arg))
	}
	name.WriteString(root[:open])
	return name.String()
}

// resolveAnonymousStruct emits an anonymous struct field as its own named type,
// under the owner's name followed by the field's: DownloadManifest.ArtworkURLs
// becomes DownloadManifestArtworkURLs.
//
// The alternative was refusing it and asking that the struct be given a name in
// Go. That is the better shape, but not every such struct is ours to rename --
// several live in files this fork tracks byte-for-byte against upstream -- and
// refusing meant the whole reaching root generated nothing. An anonymous struct
// has exactly one wire shape and exactly one place it is used, so a name built
// from that place is unambiguous.
//
// The collision rule is the one instantiated generics follow, for the same
// reason: a synthesized name that a declared type or another synthesis already
// holds is refused, never merged.
func (b *builder) resolveAnonymousStruct(st *types.Struct, ownerKey, field string, pos token.Pos) TypeRef {
	owner, ok := b.types[ownerKey]
	if !ok || owner.Package == nil || field == "" {
		b.refuse(ownerKey, field, "anonymous struct in a position with no name to build one from; name the type so it can be generated", pos)
		return TypeRef{}
	}
	name := owner.Name + field
	key := typeKey(owner.Package.Path, name)
	from := ownerKey + "." + field
	if existing, seen := b.types[key]; seen {
		if existing.synthesizedFrom == from {
			return TypeRef{Kind: KindStruct, Named: key}
		}
		what := "a declared type in that package"
		if existing.synthesizedFrom != "" {
			what = existing.synthesizedFrom
		}
		b.refuse(ownerKey, field, fmt.Sprintf("the anonymous struct at %s would be emitted as %s, which %s already claims", from, name, what), pos)
		return TypeRef{}
	}
	b.buildStructIn(key, owner.Package, owner.Package.Path, st, name)
	b.types[key].synthesizedFrom = from
	return TypeRef{Kind: KindStruct, Named: key}
}

// originQualified names the generic a type was instantiated from, without its
// type arguments: Collection[UserLibrary] reports ".../internal/apiv2.Collection".
func originQualified(t *types.Named) string {
	obj := t.Origin().Obj()
	if obj.Pkg() == nil {
		return obj.Name()
	}
	return obj.Pkg().Path() + "." + obj.Name()
}
