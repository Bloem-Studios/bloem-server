// Package refuse declares one root per refusal rule; each is registered on
// its own so the test can pin the exact error.
package refuse

import (
	"encoding/json"
	"io"
	"time"
)

// Box is generic.
type Box[T any] struct {
	V T `json:"v"`
}

// GenericField references a generic instantiation.
type GenericField struct {
	B Box[int] `json:"b"`
}

// StringOption uses the ,string option.
type StringOption struct {
	N int `json:"n,string"`
}

// Untagged has an exported field with no json tag.
type Untagged struct {
	Name string
}

// Unnamed has a tag with options but no name.
type Unnamed struct {
	Name string `json:",omitempty"`
}

// Marshaler has a custom marshaler.
type Marshaler struct {
	V int `json:"v"`
}

// MarshalJSON hides the wire shape.
func (m Marshaler) MarshalJSON() ([]byte, error) { return json.Marshal(m.V) }

// MarshalerField references it without a registry serializer.
type MarshalerField struct {
	M Marshaler `json:"m"`
}

// TextMarshaler has a text marshaler on the pointer receiver.
type TextMarshaler struct {
	V int `json:"v"`
}

// UnmarshalText hides the wire shape.
func (m *TextMarshaler) UnmarshalText(b []byte) error { return nil }

// TextMarshalerField references it.
type TextMarshalerField struct {
	M TextMarshaler `json:"m"`
}

// Base is embedded by the embedding cases.
type Base struct {
	ID int `json:"id"`
}

// EmbeddedPointer embeds a pointer.
type EmbeddedPointer struct {
	*Base
}

// EmbeddedTagged embeds with a json tag.
type EmbeddedTagged struct {
	Base `json:"base"`
}

// Named is a non-struct embedded type.
type Named string

// EmbeddedNonStruct embeds a named string.
type EmbeddedNonStruct struct {
	Named
}

// Left and Right collide on wire name "id" at the same depth.
type Left struct {
	ID int `json:"id"`
}

// Right collides with Left.
type Right struct {
	ID int `json:"id"`
}

// Ambiguous embeds both.
type Ambiguous struct {
	Left
	Right
}

// Anonymous has an anonymous struct field.
type Anonymous struct {
	Inner struct {
		X int `json:"x"`
	} `json:"inner"`
}

// MapKey uses a non-string key.
type MapKey struct {
	M map[int]string `json:"m"`
}

// PointerPointer has a double pointer.
type PointerPointer struct {
	P **int `json:"p"`
}

// Interface has a non-empty interface field.
type Interface struct {
	R io.Reader `json:"r"`
}

// Unsigned uses uint.
type Unsigned struct {
	U uint `json:"u"`
}

// Outside references a struct from outside the module.
type Outside struct {
	L time.Location `json:"l"`
}

// Channel has a channel field.
type Channel struct {
	C chan int `json:"c"`
}

// NumberField uses json.Number, a string in Go but a number on the wire.
type NumberField struct {
	N json.Number `json:"n"`
}

// Plain is a named string without constants: not a valid root.
type Plain string

// Fine is a valid struct, used by registry-level refusals.
type Fine struct {
	V string `json:"v"`
}
