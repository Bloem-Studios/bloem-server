package apiv2

import (
	"reflect"
	"testing"
)

func TestBloemAudiobookAcquisitionDocumentContract(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct{ path, method, status, response string }{
		{"/admin/organization/book-requests/acquisitions", "get", "200", "AudiobookAcquisitionList"},
		{"/admin/organization/book-requests/{id}/acquisition", "get", "200", "AudiobookAcquisition"},
		{"/admin/organization/book-requests/{id}/approve-candidate", "post", "200", "Request"},
		{"/admin/organization/book-requests/{id}/accept-download", "post", "200", "Request"},
		{"/admin/organization/book-requests/{id}/retry", "post", "200", "Request"},
		{"/admin/organization/audiobook-request-integrations", "get", "200", "AudiobookRequestSetup"},
		{"/admin/organization/audiobook-request-integrations", "post", "201", "AudiobookIntegrationSummary"},
		{"/admin/organization/audiobook-request-integrations/{id}/library", "put", "200", "AudiobookIntegrationSummary"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
			if !reflect.DeepEqual(op["security"], []any{map[string]any{"adminContextAuth": []any{}}}) {
				t.Fatalf("security=%v", op["security"])
			}
			response := bloemDocObject(t, op, "responses", tc.status, "content", "application/json", "schema")
			if response["$ref"] != "#/components/schemas/"+tc.response {
				t.Fatalf("response schema=%v", response)
			}
			for _, status := range []string{"400", "401", "403", "404", "409", "500", "503"} {
				err := bloemDocObject(t, op, "responses", status, "content", "application/json", "schema")
				if err["$ref"] != "#/components/schemas/BloemNativeError" {
					t.Fatalf("error schema=%v", err)
				}
			}
		})
	}
	for _, action := range []string{"approve-candidate", "accept-download", "retry"} {
		body := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/book-requests/{id}/"+action, "post", "requestBody", "content", "application/json", "schema")
		schema := bloemDocSchema(t, doc, body)
		if action == "approve-candidate" {
			bloemDocFields(t, schema, "expected_revision", "candidate_ref")
		} else {
			bloemDocFields(t, schema, "expected_revision", "reason")
		}
	}
	binding := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/audiobook-request-integrations/{id}/library", "put")
	for _, v := range binding["parameters"].([]any) {
		p := v.(map[string]any)
		if p["name"] == "id" {
			schema := p["schema"].(map[string]any)
			if schema["type"] != "string" || schema["format"] == "uuid" {
				t.Fatalf("integration ID must be opaque: %v", schema)
			}
		}
	}
	body := bloemDocObject(t, binding, "requestBody", "content", "application/json", "schema")
	bloemDocFields(t, bloemDocSchema(t, doc, body), "library_id", "download_roots")
	summary := bloemDocObject(t, doc, "components", "schemas", "AudiobookIntegrationSummary")
	bloemDocFields(t, summary, "id", "name", "installation_id", "capability_id", "library_id", "library_name", "download_roots", "binding_revision", "ready")
	list := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/book-requests/acquisitions", "get")
	params := map[string]map[string]any{}
	for _, v := range list["parameters"].([]any) {
		p := v.(map[string]any)
		params[p["name"].(string)] = p["schema"].(map[string]any)
	}
	if params["limit"]["minimum"] != float64(1) || params["limit"]["maximum"] != float64(100) || params["limit"]["default"] != float64(100) {
		t.Fatalf("limit=%v", params["limit"])
	}
	if params["state"] == nil || params["cursor"] == nil {
		t.Fatalf("missing list query: %v", params)
	}
}
