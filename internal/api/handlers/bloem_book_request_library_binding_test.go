package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Leaving out library_id is a mistake, not a request to clear the binding;
// clearing takes an explicit library_id of 0.
func TestBloemBookRequestLibraryBindingRequiresLibraryID(t *testing.T) {
	ebook := &ebookSetupStub{}
	ebooks := ebookSetupHandler(ebook)
	audiobook := &audiobookSetupStub{}
	audiobooks := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, nil)
	audiobooks.SetAudiobookRequestSetup(audiobook)
	for _, tc := range []struct {
		name, body string
		handle     http.HandlerFunc
		calls      *int
	}{
		{"ebook without library_id", `{}`, ebooks.HandleEbookIntegrationLibrary, &ebook.calls},
		{"ebook with null library_id", `{"library_id":null}`, ebooks.HandleEbookIntegrationLibrary, &ebook.calls},
		{"audiobook without library_id", `{"download_roots":["/books/new"]}`, audiobooks.HandleAudiobookIntegrationLibrary, &audiobook.calls},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handle(w, audiobookSetupRequest(http.MethodPut, tc.body, "platform_admin"))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"invalid_request"`) || *tc.calls != 0 {
				t.Fatalf("response=%d %s calls=%d", w.Code, w.Body.String(), *tc.calls)
			}
		})
	}
	for _, tc := range []struct {
		name, body string
		handle     http.HandlerFunc
		calls      *int
	}{
		{"ebook clear", `{"library_id":0}`, ebooks.HandleEbookIntegrationLibrary, &ebook.calls},
		{"audiobook clear", `{"library_id":0}`, audiobooks.HandleAudiobookIntegrationLibrary, &audiobook.calls},
	} {
		w := httptest.NewRecorder()
		tc.handle(w, audiobookSetupRequest(http.MethodPut, tc.body, "platform_admin"))
		if w.Code != http.StatusOK || *tc.calls != 1 {
			t.Fatalf("%s: response=%d %s calls=%d", tc.name, w.Code, w.Body.String(), *tc.calls)
		}
	}
}
