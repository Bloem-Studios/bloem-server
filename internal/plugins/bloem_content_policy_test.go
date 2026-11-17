package plugins

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentBrowseOnlyBlocksPublicAndPrivateBytes(t *testing.T) {
	file := filepath.Join(t.TempDir(), "asset.txt")
	if err := os.WriteFile(file, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	denials := []BloemContentDenials{{PlaybackDenied: true}}
	for _, policy := range []string{"public", "authenticated", "admin"} {
		for _, denial := range denials {
			testContentDenied(t, file, policy, denial)
		}
	}
}

func testContentDenied(t *testing.T, file, policy string, denial BloemContentDenials) {
	t.Helper()
	svc := &contentTestService{access: policy, file: file}
	resolver := func(*http.Request) ContentAccess {
		return ContentAccess{Authenticated: true, Admin: true, UserID: 7, BloemContentDenials: denial}
	}
	h := NewContentHandler(NewHTTPProxy(svc, profileTestInstallationStore{}), resolver, resolver)
	for _, tc := range []struct{ method, path string }{{"POST", "/plugins/1/page"}, {"GET", "/plugin-assets/1/asset.txt"}} {
		r := httptest.NewRequest(tc.method, ContentPrefix+tc.path, strings.NewReader("request"))
		r.Header.Set("Range", "bytes=2-4")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || svc.request != nil || strings.Contains(w.Body.String(), "234") {
			t.Errorf("%s %+v %s: %d %s dispatch=%v", policy, denial, tc.path, w.Code, w.Body, svc.request != nil)
		}
	}
}
