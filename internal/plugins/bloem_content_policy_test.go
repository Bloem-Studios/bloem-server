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
	for _, policy := range []string{"public", "authenticated", "admin"} {
		svc := &contentTestService{access: policy, file: file}
		resolver := func(*http.Request) ContentAccess {
			return ContentAccess{Authenticated: true, Admin: true, UserID: 7, PlaybackDenied: true}
		}
		h := NewContentHandler(NewHTTPProxy(svc, profileTestInstallationStore{}), resolver, resolver)
		for _, tc := range []struct{ method, path string }{{"POST", "/plugins/1/page"}, {"GET", "/plugin-assets/1/asset.txt"}} {
			r := httptest.NewRequest(tc.method, ContentPrefix+tc.path, strings.NewReader("request"))
			r.Header.Set("Range", "bytes=2-4")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || svc.request != nil || strings.Contains(w.Body.String(), "234") {
				t.Errorf("%s %s: %d %s dispatch=%v", policy, tc.path, w.Code, w.Body, svc.request != nil)
			}
		}
	}
}
