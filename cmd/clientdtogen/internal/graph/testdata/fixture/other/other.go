// Package other is reached from the fixture package only transitively: it
// proves cross-package alias resolution, embedded promotion and serializer
// escape hatches.
package other

import "encoding/json"

// Mixin is embedded into fixture.Embedded from another package.
type Mixin struct {
	Mixed string `json:"mixed"`
}

// Target is the alias target of fixture.Alias.
type Target struct {
	Value string `json:"value"`
}

// Standalone is only reached through a cross-package alias root in fixture.
type Standalone struct {
	Only string `json:"only"`
}

// FrameRate has a polymorphic wire shape and a custom marshaler; only a
// registry serializer lets a field of this type into the graph.
type FrameRate struct {
	Num, Den int
}

// MarshalJSON writes either a number or a "num/den" string.
func (f FrameRate) MarshalJSON() ([]byte, error) {
	if f.Den == 1 {
		return json.Marshal(f.Num)
	}
	return json.Marshal(f)
}
