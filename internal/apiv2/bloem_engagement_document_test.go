package apiv2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validate JSON emitted by the real domain structs, including nil pointers.
// The reflection shape check cannot distinguish a pointer from its value.
func TestBloemEngagementDocumentResponseNullability(t *testing.T) {
	doc := bloemWebDocument(t)
	start := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	promotion := promotions.Promotion{
		ID: "holiday-campaign", Surfaces: []string{promotions.SurfaceHome},
		Headline: "Holiday highlights", ImageURL: "https://example.com/holiday.jpg",
		StartsAt: start, EndsAt: end, Targeting: promotions.Targeting{Audience: "all"},
		Dismissible: true, CreatedBy: 1, CreatedAt: start, UpdatedAt: start,
	}
	pack := ambience.Pack{
		ID: "winter-pack", EffectID: "snow", Intensity: 1,
		Window:   ambience.Window{StartsAt: start, EndsAt: end, Timezone: "UTC"},
		Surfaces: []string{ambience.SurfaceAll}, CreatedBy: 1, CreatedAt: start, UpdatedAt: start,
	}
	organization := uuid.MustParse("d75a7e76-f180-40d4-9b27-f483f656f565")
	for _, scope := range []string{"deployment-wide", "organization-owned"} {
		t.Run(scope, func(t *testing.T) {
			campaign, season := promotion, pack
			if scope == "organization-owned" {
				campaign.OrganizationID, season.OrganizationID = &organization, &organization
				campaign.CTA = &promotions.CTA{Label: "Explore", URL: "https://example.com/holiday"}
			}
			for _, tc := range []struct {
				path, method, status string
				body                 any
			}{
				{"/admin/platform/promotions/", "post", "201", campaign},
				{"/admin/platform/promotions/{id}", "put", "200", campaign},
				{"/admin/platform/promotions/", "get", "200", map[string]any{"promotions": []promotions.Promotion{campaign}, "surfaces": promotions.Surfaces}},
				{"/admin/platform/ambience/", "post", "201", season},
				{"/admin/platform/ambience/{id}", "put", "200", season},
				{"/admin/platform/ambience/", "get", "200", map[string]any{"packs": []ambience.Pack{season}, "storage_available": true, "yearly_scheduling": true}},
				{"/admin/platform/ambience/{id}/assets", "post", "201", map[string]any{"url": "https://example.com/banner.png", "slot": "banner", "pack": season}},
			} {
				t.Run(tc.method+tc.path, func(t *testing.T) {
					schema := bloemEngagementSchema(t, doc, tc.path, tc.method, tc.status)
					if err := schema.Validate(bloemEngagementJSON(t, tc.body)); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}

	for _, tc := range []struct {
		path     string
		body     any
		required []string
		nonnull  []string
	}{
		{"/admin/platform/promotions/", promotion, []string{"organization_id", "cta", "headline", "dismissible"}, []string{"headline", "dismissible", "placement", "targeting"}},
		{"/admin/platform/ambience/", pack, []string{"organization_id", "intensity", "window"}, []string{"intensity", "window", "assets"}},
	} {
		t.Run(tc.path+" constraints", func(t *testing.T) {
			schema := bloemEngagementSchema(t, doc, tc.path, "post", "201")
			for _, field := range tc.required {
				body := bloemEngagementJSON(t, tc.body)
				delete(body, field)
				if err := schema.Validate(body); err == nil {
					t.Errorf("missing required %s must be rejected", field)
				}
			}
			for _, field := range tc.nonnull {
				body := bloemEngagementJSON(t, tc.body)
				body[field] = nil
				if err := schema.Validate(body); err == nil {
					t.Errorf("null %s must be rejected", field)
				}
			}
		})
	}
	response := bloemEngagementSchema(t, doc, "/admin/platform/promotions/", "post", "201")
	for _, invalid := range []any{false, "Explore", map[string]any{"label": "Explore"}, map[string]any{"label": "Explore", "url": nil}} {
		body := bloemEngagementJSON(t, promotion)
		body["cta"] = invalid
		if err := response.Validate(body); err == nil {
			t.Errorf("invalid response CTA %T must be rejected", invalid)
		}
	}
}

func TestBloemEngagementDocumentRequestNullability(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct {
		name, path, body string
		checkDomain      func(*testing.T, []byte)
		nonnull          []string
	}{
		{
			"promotion", "/admin/platform/promotions",
			`{"surfaces":["home"],"headline":"Holiday highlights","image_url":"https://example.com/holiday.jpg","starts_at":"2026-12-01T00:00:00Z","ends_at":"2027-01-01T00:00:00Z"}`,
			func(t *testing.T, raw []byte) {
				t.Helper()
				var in promotions.Input
				if err := json.Unmarshal(raw, &in); err != nil {
					t.Fatal(err)
				}
				normalized, err := promotions.Normalize(in)
				if err != nil {
					t.Fatal(err)
				}
				if in.Dismissible == nil && !normalized.Dismissible {
					t.Fatal("omitted/null dismissible must default to true")
				}
			},
			[]string{"surfaces", "headline", "image_url", "starts_at", "ends_at"},
		},
		{
			"ambience", "/admin/platform/ambience",
			`{"effect_id":"snow","window":{"starts_at":"2026-12-01T00:00:00Z","ends_at":"2027-01-01T00:00:00Z"}}`,
			func(t *testing.T, raw []byte) {
				t.Helper()
				var in ambience.Input
				if err := json.Unmarshal(raw, &in); err != nil {
					t.Fatal(err)
				}
				normalized, err := ambience.Normalize(in)
				if err != nil {
					t.Fatal(err)
				}
				if in.Intensity == nil && normalized.Intensity != 1 {
					t.Fatal("omitted/null intensity must default to 1")
				}
			},
			[]string{"effect_id", "window"},
		},
	} {
		for _, variant := range []string{"omitted", "null", "populated"} {
			t.Run(tc.name+"/"+variant, func(t *testing.T) {
				var body map[string]any
				if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
					t.Fatal(err)
				}
				if variant != "omitted" {
					body["organization_id"] = nil
					if tc.name == "promotion" {
						body["dismissible"], body["cta"], body["image_width"], body["image_height"] = nil, nil, nil, nil
					} else {
						body["intensity"] = nil
					}
				}
				if variant == "populated" {
					body["organization_id"] = "d75a7e76-f180-40d4-9b27-f483f656f565"
					if tc.name == "promotion" {
						body["dismissible"] = false
						body["cta"] = map[string]any{"label": "Explore", "url": "https://example.com/holiday"}
						body["image_width"], body["image_height"] = 1920, 1080
					} else {
						body["intensity"] = 0.5
					}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				tc.checkDomain(t, raw)
				for _, method := range []string{"post", "put"} {
					path := tc.path + "/"
					if method == "put" {
						path += "{id}"
					}
					schema := bloemEngagementSchema(t, doc, path, method, "")
					if err := schema.Validate(body); err != nil {
						t.Errorf("%s: %v", method, err)
					}
					for _, field := range tc.nonnull {
						invalid := bloemEngagementJSON(t, body)
						invalid[field] = nil
						if err := schema.Validate(invalid); err == nil {
							t.Errorf("%s null %s must be rejected", method, field)
						}
					}
					if tc.name == "promotion" {
						for _, cta := range []any{true, map[string]any{"url": "https://example.com/holiday"}, map[string]any{"label": nil, "url": "https://example.com/holiday"}} {
							invalid := bloemEngagementJSON(t, body)
							invalid["cta"] = cta
							if err := schema.Validate(invalid); err == nil {
								t.Errorf("%s invalid request CTA %T must be rejected", method, cta)
							}
						}
					}
				}
			})
		}
	}
}

func bloemEngagementJSON(t *testing.T, body any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func bloemEngagementSchema(t *testing.T, doc map[string]any, path, method, status string) *jsonschema.Schema {
	t.Helper()
	op := bloemDocObject(t, doc, "paths", BloemPrefix+path, method)
	var schema map[string]any
	if status == "" {
		schema = bloemDocObject(t, op, "requestBody", "content", "application/json", "schema")
	} else {
		schema = bloemDocObject(t, op, "responses", status, "content", "application/json", "schema")
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://schema.example.invalid/bloem-engagement.json"
	if err := compiler.AddResource(resource, doc); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(resource + schema["$ref"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}
