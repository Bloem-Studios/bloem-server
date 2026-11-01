package apiv2

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

func bloemStorefrontJSONFields(t reflect.Type) []string {
	var names []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// The documented update body must name exactly the fields the handler decodes.
func TestBloemPlatformAccountUpdateBodyMatchesTheHandler(t *testing.T) {
	doc := bloemStorefrontJSONFields(reflect.TypeFor[BloemPlatformAccountUpdateBody]())
	handler := bloemStorefrontJSONFields(reflect.TypeFor[handlers.BloemPlatformAccountUpdateRequest]())
	if !slices.Equal(doc, handler) {
		t.Fatalf("documented fields %v, handler fields %v", doc, handler)
	}
}
