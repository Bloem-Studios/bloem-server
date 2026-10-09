package handlers

import (
	"github.com/Silo-Server/silo-server/internal/auth"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeCatalogCommandsRequireAdminContext(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopePlatform)
	for _, handler := range []func(*httptest.ResponseRecorder){
		func(w *httptest.ResponseRecorder) {
			h.HandleCatalogInstall(w, httptest.NewRequest("POST", "/catalog/installations", strings.NewReader(`{}`)))
		},
		func(w *httptest.ResponseRecorder) {
			h.HandleCatalogUpgrade(w, httptest.NewRequest("POST", "/catalog/installations/1/upgrade", strings.NewReader(`{}`)))
		},
	} {
		w := httptest.NewRecorder()
		handler(w)
		if w.Code != 401 {
			t.Fatalf("anonymous catalog command status %d", w.Code)
		}
	}
}
