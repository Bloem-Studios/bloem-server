package apiv2

import (
	"reflect"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/danielgtaylor/huma/v2"
)

// Adjust only the native document's engagement schemas. Chi handlers serialize
// these domain types directly, including nil ownership and CTA pointers. Huma
// loses UUID pointer nullability and cannot use nullable tags on object refs.
// Keep the domain schemas and their required fields; do not change shared CTA
// or UUID schemas, or any runtime registration/validation behavior.
func bloemEngagementDocumentNullability(schemas huma.Registry) {
	promotion := schemas.Schema(reflect.TypeFor[promotions.Promotion](), false, "")
	pack := schemas.Schema(reflect.TypeFor[ambience.Pack](), false, "")
	request := schemas.Schema(reflect.TypeFor[BloemPromotionBody](), false, "")
	promotion.Properties["organization_id"].Nullable = true
	pack.Properties["organization_id"].Nullable = true
	for _, schema := range []*huma.Schema{promotion, request} {
		cta := *schema.Properties["cta"]
		description := cta.Description
		cta.Description = ""
		schema.Properties["cta"] = &huma.Schema{
			Description: description,
			AnyOf:       []*huma.Schema{&cta, {Type: "null"}},
		}
	}
}
