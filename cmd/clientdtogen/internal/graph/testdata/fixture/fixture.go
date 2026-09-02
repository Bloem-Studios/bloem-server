// Package fixture declares one struct per mapping row of
// docs/specs/client-dto-generator.md §4.1/§4.4 so the graph tests can assert
// each row; it is loaded through go/packages, never imported.
package fixture

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph/testdata/fixture/other"
)

// Protocol is a named string type with a const block: an enum.
type Protocol string

// Protocol vocabulary. The unexported constant and the untyped one are not
// part of it.
const (
	ProtocolHLS         Protocol = "hls"
	ProtocolProgressive Protocol = "progressive"
	protocolHidden      Protocol = "hidden"
	Untyped                      = "untyped"
)

var _ = protocolHidden

// ProtocolLate is declared after ProtocolProgressive in source order but
// sorts first by name; the graph keeps source order.
const ProtocolLate Protocol = "late"

// Label is a named string type without constants: a plain string.
type Label string

// Count is a named int: its underlying kind.
type Count int

// Names is a named slice: its underlying shape.
type Names []string

// Scalars covers every scalar row.
type Scalars struct {
	S        string          `json:"s"`
	B        bool            `json:"b"`
	I        int             `json:"i"`
	I8       int8            `json:"i8"`
	I16      int16           `json:"i16"`
	I32      int32           `json:"i32"`
	U8       uint8           `json:"u8"`
	U16      uint16          `json:"u16"`
	U32      uint32          `json:"u32"`
	I64      int64           `json:"i64"`
	U64      uint64          `json:"u64"`
	F32      float32         `json:"f32"`
	F64      float64         `json:"f64"`
	T        time.Time       `json:"t"`
	TP       *time.Time      `json:"tp,omitempty"`
	U        uuid.UUID       `json:"u"`
	UP       *uuid.UUID      `json:"up"`
	Raw      json.RawMessage `json:"raw"`
	Any      any             `json:"any"`
	AnyMap   map[string]any  `json:"any_map"`
	Bytes    []byte          `json:"bytes"`
	Label    Label           `json:"label"`
	Count    Count           `json:"count"`
	Names    Names           `json:"names"`
	Proto    Protocol        `json:"proto"`
	ProtoPtr *Protocol       `json:"proto_ptr,omitempty"`
	IntPtr   *int            `json:"int_ptr,omitempty"`
	Omit     int             `json:"omit,omitempty"`
	TOmit    time.Time       `json:"t_omit,omitempty"`
	UOmit    uuid.UUID       `json:"u_omit,omitempty"`
	ROmit    json.RawMessage `json:"raw_omit,omitempty"`
	BOmit    []byte          `json:"bytes_omit,omitempty"`
	POmit    Protocol        `json:"proto_omit,omitempty"`
}

// Child is referenced from several places and from itself (cycle).
type Child struct {
	Name string `json:"name"`
	Next *Child `json:"next,omitempty"`
}

// Collections covers the list and map rows.
type Collections struct {
	Strings      []string          `json:"strings"`
	Structs      []Child           `json:"structs,omitempty"`
	OptionalList *[]Child          `json:"optional_list,omitempty"`
	Headers      map[string]string `json:"headers"`
	StructMap    map[string]Child  `json:"struct_map"`
	EnumMap      map[Label]int     `json:"enum_map"`
	Fixed        [3]int            `json:"fixed"`
	Nested       [][]string        `json:"nested"`
	PtrList      []*Child          `json:"ptr_list"`
	FixedOmit    [2]int            `json:"fixed_omit,omitempty"`
	StructOmit   Child             `json:"struct_omit,omitempty"`
	MapOmit      map[string]int    `json:"map_omit,omitempty"`
}

// Base is embedded into Embedded.
type Base struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Deep Inner  `json:"deep"`
}

// Inner is only reachable through the embedded Base.
type Inner struct {
	Depth int `json:"depth"`
}

// lower is an unexported embedded struct whose exported fields still promote.
type lower struct {
	Low string `json:"low"`
}

// Embedded covers promotion, shadowing, omitted fields and struct-valued
// fields.
type Embedded struct {
	Base
	Kind    string `json:"kind"` // shadows Base.Kind: shallower wins
	Extra   string `json:"extra"`
	Skipped string `json:"-"`
	Dash    string `json:"-,"`
	hidden  string
	other.Mixin
	lower
	Zero   Child  `json:"zero,omitzero"`
	Direct Child  `json:"direct"`
	Ptr    *Child `json:"ptr"`
}

var _ = Embedded{}.hidden

// Alias resolves to other.Target: one class in other's package file.
type Alias = other.Target

// AliasRoot is registered under its alias name.
type AliasRoot = Response

// Response is a response root using the alias and a serializer field.
type Response struct {
	Target  Alias            `json:"target"`
	Rate    other.FrameRate  `json:"frame_rate"`
	RatePtr *other.FrameRate `json:"frame_rate_ptr,omitempty"`
	Shared  Shared           `json:"shared"`
	Gated   GatedChild       `json:"gated_child"`
}

// Request is a request root that also reaches Shared.
type Request struct {
	Shared Shared   `json:"shared"`
	Proto  Protocol `json:"proto"`
}

// Shared is reached from both directions.
type Shared struct {
	V string `json:"v"`
}

// Gated is a gated root.
type Gated struct {
	Child GatedChild `json:"child"`
	Only  GatedOnly  `json:"only"`
	Promo *PromoCard `json:"promo,omitempty"`
}

// Gated2 is a second gated root reaching GatedOnly under another gate.
type Gated2 struct {
	Only GatedOnly `json:"only"`
}

// StandaloneAlias is a cross-package alias registered as a root.
type StandaloneAlias = other.Standalone

// GatedChild is also reached from the ungated Response root.
type GatedChild struct {
	V string `json:"v"`
}

// GatedOnly is reached only through gated roots.
type GatedOnly struct {
	V string `json:"v"`
}

// Compat is an upstream-compat root with a bloem_fields marking.
type Compat struct {
	Plain string     `json:"plain"`
	Promo *PromoCard `json:"promo,omitempty"`
}

// PromoCard is reached only through bloem-marked fields and bloem roots.
type PromoCard struct {
	Title string `json:"title"`
}

// BloemOnly is a bloem-dialect root.
type BloemOnly struct {
	Card PromoCard `json:"card"`
}

// Orphan is a tagged exported struct nobody registers or reaches.
type Orphan struct {
	X int `json:"x"`
}

// Helper has no json tags and must not count as unreached wire shape.
type Helper struct {
	X int
}
