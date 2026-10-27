package apiv2

import (
	"reflect"
	"testing"
)

func TestBloemEbookRequestDocumentRoutesAndOpaqueIntegrationIdentity(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct{ path, method string }{
		{"/ebook-requests/capability", "get"}, {"/ebook-requests/mine", "get"}, {"/ebook-requests", "post"}, {"/ebook-requests/{id}", "get"}, {"/ebook-requests/{id}/cancel", "post"},
		{"/admin/organization/ebook-requests", "get"}, {"/admin/organization/ebook-requests/{id}/approve", "post"}, {"/admin/organization/ebook-requests/{id}/decline", "post"}, {"/admin/organization/ebook-requests/{id}/retry", "post"},
		{"/admin/organization/ebook-request-integrations", "get"}, {"/admin/organization/ebook-request-integrations", "post"}, {"/admin/organization/ebook-request-integrations/{id}/library", "put"}, {"/admin/organization/ebook-request-integrations/{id}/test", "post"}, {"/admin/organization/ebook-request-policy", "put"},
	} {
		bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
	}
	op := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/ebook-request-integrations/{id}/library", "put")
	for _, v := range op["parameters"].([]any) {
		p := v.(map[string]any)
		if p["name"] == "id" {
			schema := p["schema"].(map[string]any)
			if schema["type"] != "string" || schema["format"] == "uuid" {
				t.Fatalf("integration IDs must be opaque strings: %+v", schema)
			}
		}
	}
	test := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/ebook-request-integrations/{id}/test", "post", "responses", "200", "content", "application/json", "schema")
	if test["$ref"] != "#/components/schemas/RequestIntegrationTestResult" {
		t.Fatalf("connection test response=%v", test)
	}
	create := bloemDocObject(t, doc, "paths", BloemPrefix+"/ebook-requests", "post", "responses")
	if create["200"] == nil || create["201"] == nil {
		t.Fatal("durable idempotent replay response missing")
	}
	quota := bloemDocObject(t, create, "429", "content", "application/json", "schema")
	if len(quota["anyOf"].([]any)) != 2 {
		t.Fatalf("quota and rate-limit refusals share 429: %v", quota)
	}
}

// Queue review needs an organization context; setup also accepts a platform
// context, which resolves the deployment's default organization.
func TestBloemEbookRequestDocumentAdministrativeSecurity(t *testing.T) {
	doc := bloemWebDocument(t)
	organization := []any{map[string]any{"bloemOrganizationContext": []any{}}}
	either := []any{map[string]any{"bloemOrganizationContext": []any{}}, map[string]any{"bloemPlatformContext": []any{}}}
	for _, tc := range []struct {
		path, method string
		security     []any
	}{
		{"/admin/organization/ebook-requests", "get", organization},
		{"/admin/organization/ebook-requests/{id}/approve", "post", organization},
		{"/admin/organization/ebook-requests/{id}/decline", "post", organization},
		{"/admin/organization/ebook-requests/{id}/retry", "post", organization},
		{"/admin/organization/ebook-request-integrations", "get", either},
		{"/admin/organization/ebook-request-integrations", "post", either},
		{"/admin/organization/ebook-request-integrations/{id}/library", "put", either},
		{"/admin/organization/ebook-request-integrations/{id}/test", "post", either},
		{"/admin/organization/ebook-request-policy", "put", either},
	} {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
		if !reflect.DeepEqual(op["security"], tc.security) {
			t.Errorf("%s %s security=%v", tc.method, tc.path, op["security"])
		}
		if _, ok := bloemDocObject(t, op, "responses")["429"]; ok {
			t.Errorf("%s %s has no rate limit or quota", tc.method, tc.path)
		}
	}
	schemes := bloemDocObject(t, doc, "components", "securitySchemes")
	for _, op := range []string{"bloemOrganizationContext", "bloemPlatformContext"} {
		bloemDocObject(t, schemes, op)
	}
}
