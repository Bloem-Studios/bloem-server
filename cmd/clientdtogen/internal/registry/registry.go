// Package registry loads and validates the client DTO registry
// (contracts/client/v1/registry.json).
//
// The registry is the generator's only input besides the Go tree: it names the
// root wire types per Go package and carries the metadata (direction, dialect,
// capability gate, Bloem-only fields, client-side serializers) that must not
// live in struct tags because the owning Go files are upstream-identical. The
// JSON shape is enforced by the embedded registry.schema.json; this package adds
// the semantic rules a schema cannot express (no duplicate packages or roots,
// serializer keys that name a registered package).
package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	clientv1 "github.com/Silo-Server/silo-server/contracts/client/v1"
)

// SchemaVersion is the only registry format version this loader accepts.
const SchemaVersion = 1

const schemaPath = "registry.schema.json"

// Dialect says whether a wire shape exists on upstream Silo as well.
type Dialect string

// Dialect values, as spelled in the registry.
const (
	DialectUpstreamCompat Dialect = "upstream-compat"
	DialectBloem          Dialect = "bloem"
)

// Direction is the way a type crosses the wire. It is a bit set so a type
// reached from both a request root and a response root carries both.
type Direction uint8

// Direction bits.
const (
	DirectionRequest  Direction = 1 << iota // client → server
	DirectionResponse                       // server → client
	DirectionBoth     = DirectionRequest | DirectionResponse
)

// ParseDirection maps the registry spelling to a Direction.
func ParseDirection(s string) (Direction, error) {
	switch s {
	case "request":
		return DirectionRequest, nil
	case "response":
		return DirectionResponse, nil
	case "both":
		return DirectionBoth, nil
	}
	return 0, fmt.Errorf("unknown direction %q: want request, response or both", s)
}

// String returns the registry spelling of d.
func (d Direction) String() string {
	switch d {
	case DirectionRequest:
		return "request"
	case DirectionResponse:
		return "response"
	case DirectionBoth:
		return "both"
	}
	return "none"
}

// Request reports whether the request bit is set.
func (d Direction) Request() bool { return d&DirectionRequest != 0 }

// Response reports whether the response bit is set.
func (d Direction) Response() bool { return d&DirectionResponse != 0 }

// UnmarshalJSON accepts the registry spelling.
func (d *Direction) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := ParseDirection(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalJSON writes the registry spelling.
func (d Direction) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// Registry is the parsed registry.json.
type Registry struct {
	Schema   int       `json:"schema"`
	Packages []Package `json:"packages"`
	// Serializers maps "<package path>.<Go type>.<wire name>" to a client-side
	// serializer class. See the schema description.
	Serializers map[string]string `json:"serializers,omitempty"`
}

// Package is one registered Go package. Gate and Dialect are the defaults for
// its roots.
type Package struct {
	Path    string  `json:"path"`
	Dialect Dialect `json:"dialect"`
	Gate    string  `json:"gate,omitempty"`
	Roots   []Root  `json:"roots"`
}

// Root is one registered root type. Dialect and Gate, when set, override the
// package defaults; use EffectiveDialect / EffectiveGate.
type Root struct {
	Type        string    `json:"type"`
	Direction   Direction `json:"direction"`
	Dialect     Dialect   `json:"dialect,omitempty"`
	Gate        string    `json:"gate,omitempty"`
	BloemFields []string  `json:"bloem_fields,omitempty"`
}

// EffectiveDialect returns the root's dialect after package inheritance.
func (p Package) EffectiveDialect(r Root) Dialect {
	if r.Dialect != "" {
		return r.Dialect
	}
	return p.Dialect
}

// EffectiveGate returns the root's gate after package inheritance ("" =
// ungated).
func (p Package) EffectiveGate(r Root) string {
	if r.Gate != "" {
		return r.Gate
	}
	return p.Gate
}

// SerializerKey builds the Serializers map key for a field.
func SerializerKey(pkgPath, typeName, wireName string) string {
	return pkgPath + "." + typeName + "." + wireName
}

// Load reads and validates the registry at path.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading registry: %w", err)
	}
	reg, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return reg, nil
}

// Parse validates raw against the embedded schema and the semantic rules and
// returns the decoded registry.
func Parse(raw []byte) (*Registry, error) {
	if err := validateAgainstSchema(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var reg Registry
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("decoding registry: %w", err)
	}
	if err := reg.validate(); err != nil {
		return nil, err
	}
	return &reg, nil
}

func validateAgainstSchema(raw []byte) error {
	schemaBytes, err := fs.ReadFile(clientv1.FS, schemaPath)
	if err != nil {
		return fmt.Errorf("reading embedded registry schema: %w", err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return fmt.Errorf("parsing registry schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaPath, schemaDoc); err != nil {
		return fmt.Errorf("registering registry schema: %w", err)
	}
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		return fmt.Errorf("compiling registry schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parsing registry: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("registry does not satisfy %s: %w", schemaPath, err)
	}
	return nil
}

// validate applies the rules the schema cannot express.
func (r *Registry) validate() error {
	if r.Schema != SchemaVersion {
		return fmt.Errorf("unsupported registry schema %d: want %d", r.Schema, SchemaVersion)
	}
	seenPkg := map[string]bool{}
	for _, p := range r.Packages {
		if seenPkg[p.Path] {
			return fmt.Errorf("package %q registered twice", p.Path)
		}
		seenPkg[p.Path] = true
		seenRoot := map[string]bool{}
		for _, root := range p.Roots {
			if seenRoot[root.Type] {
				return fmt.Errorf("package %q: root %q registered twice", p.Path, root.Type)
			}
			seenRoot[root.Type] = true
			if p.EffectiveDialect(root) == DialectBloem && len(root.BloemFields) > 0 {
				return fmt.Errorf("package %q: root %q is bloem dialect; bloem_fields only applies to upstream-compat roots",
					p.Path, root.Type)
			}
		}
	}
	keys := make([]string, 0, len(r.Serializers))
	for k := range r.Serializers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := serializerPackage(k, seenPkg); !ok {
			return fmt.Errorf("serializer %q does not name a registered package", k)
		}
	}
	return nil
}

// serializerPackage finds the registered package a serializer key belongs to.
// Package paths contain slashes and never dots in their last element in this
// repository, so the longest registered path that prefixes the key wins.
func serializerPackage(key string, packages map[string]bool) (string, bool) {
	best := ""
	for p := range packages {
		if strings.HasPrefix(key, p+".") && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return "", false
	}
	rest := strings.TrimPrefix(key, best+".")
	if strings.Count(rest, ".") != 1 {
		return "", false
	}
	return best, true
}
