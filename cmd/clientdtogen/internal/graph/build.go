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
			obj, ok := scope.Lookup(root.Type).(*types.TypeName)
			if !ok {
				b.refuse(key, "", "root not found in package "+rp.Path, token.NoPos)
				continue
			}
			ref := b.resolveRef(obj.Type(), key, "", obj.Pos())
			t, ok := b.types[ref.Named]
			if !ok || (ref.Kind != KindStruct && ref.Kind != KindEnum) {
				if ref.Kind != KindInvalid {
					b.refuse(key, "", fmt.Sprintf("root must be a struct or a string type with constants, not %s", ref), obj.Pos())
				}
				continue
			}
			t.Root = true
			t.BloemFields = append([]string(nil), root.BloemFields...)
			for _, wire := range root.BloemFields {
				if !hasWire(t.Fields, wire) {
					b.refuse(key, "", fmt.Sprintf("bloem_fields names %q, which is not a wire field", wire), obj.Pos())
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
		b.refuse(ownerKey, field, "anonymous struct: name the type so it can be generated", pos)
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
	if t.TypeParams().Len() > 0 || t.TypeArgs().Len() > 0 {
		b.refuse(ownerKey, field, fmt.Sprintf("generic type %s", types.TypeString(t, nil)), pos)
		return TypeRef{}
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
		if _, ok := b.types[key]; !ok {
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
	t := &Type{
		Name:    obj.Name(),
		Package: b.packageFor(obj.Pkg()),
		Kind:    KindStruct,
	}
	b.types[key] = t // registered before the walk so cycles terminate
	pkgPath, _ := b.repoPath(obj.Pkg())
	raw := b.collectFields(key, pkgPath, obj.Name(), st, 0, "")
	t.Fields = resolvePromotion(b, key, raw)
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
	if named.TypeParams().Len() > 0 || named.TypeArgs().Len() > 0 {
		b.refuse(ownerKey, f.Name(), fmt.Sprintf("embedded generic type %s", types.TypeString(named, nil)), f.Pos())
		return nil
	}
	embPath, inModule := b.repoPath(named.Obj().Pkg())
	if !inModule {
		b.refuse(ownerKey, f.Name(), fmt.Sprintf("embedded type %s is outside module %s", types.TypeString(named, nil), b.module), f.Pos())
		return nil
	}
	embKey := typeKey(embPath, named.Obj().Name())
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
