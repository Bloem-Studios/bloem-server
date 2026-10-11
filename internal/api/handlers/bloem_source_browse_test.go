package handlers

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func TestSourceBrowseRequestBoundsAndFields(t *testing.T) {
	r := httptest.NewRequest("GET", "/browse?directory_ref=opaque&cursor=host&limit=100", nil)
	request, err := nativeSourceBrowseRequest(r)
	if err != nil || request.Limit != 100 || request.DirectoryRef != "opaque" || request.Cursor != "host" {
		t.Fatalf("valid browse request: %+v %v", request, err)
	}
	for _, query := range []string{"limit=101", "limit=0", "limit=-1", "limit=1&limit=2", "source=root", "cursor=", "directory_ref="} {
		r := httptest.NewRequest("GET", "/browse?"+query, nil)
		if _, err := nativeSourceBrowseRequest(r); err == nil {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
	r = httptest.NewRequest("GET", "/browse", strings.NewReader("{}"))
	if _, err := nativeSourceBrowseRequest(r); err == nil {
		t.Fatal("GET body accepted")
	}
}
func TestSourceBrowseErrorsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"invalid_selection_reference", 409}, {"duplicate_selection", 400}, {"selection_limit_exceeded", 422}, {"browse_limit_exceeded", 422}, {"invalid_provider_page", 502}} {
		err := NativeStorageAPIError(&catalog.NativeOnboardingError{Code: tc.code})
		if err.Status != tc.status || err.Code != tc.code {
			t.Fatalf("error %s: %+v", tc.code, err)
		}
	}
	r := httptest.NewRequest("GET", "/browse?directory_ref="+strings.Repeat("x", 6145), nil)
	if _, err := nativeSourceBrowseRequest(r); err == nil {
		t.Fatal("oversized request line accepted")
	}
	err := NativeStorageAPIError(errors.New("secret provider response"))
	if strings.Contains(err.Message, "secret") {
		t.Fatal("private provider error exposed")
	}
}
