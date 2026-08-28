package compatgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicMountContextNormalizesOnlyKnownMounts(t *testing.T) {
	tests := []struct {
		name  string
		mount string
		want  string
	}{
		{name: "audiobookshelf", mount: "/audiobookshelf/", want: "/audiobookshelf"},
		{name: "empty", mount: "", want: ""},
		{name: "root", mount: "/", want: ""},
		{name: "relative", mount: "audiobookshelf", want: ""},
		{name: "dot segment", mount: "/audiobookshelf/../admin", want: ""},
		{name: "unknown mount", mount: "/admin", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := withPublicMount(context.Background(), test.mount)
			if got := publicMountFromContext(ctx); got != test.want {
				t.Fatalf("publicMountFromContext() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPublicMountHandlerAcceptsHeaderOnlyAfterIdentityVerification(t *testing.T) {
	tests := []struct {
		name          string
		verified      bool
		want          string
		wantInProcess bool
	}{
		{name: "verified gateway", verified: true, want: "/audiobookshelf"},
		{name: "unverified caller", verified: false, want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got string
			var gotInProcess bool
			handler := PublicMountHandler(
				func(*http.Request) bool { return test.verified },
				func(w http.ResponseWriter, r *http.Request, mount string, inProcess bool) {
					got = mount
					gotInProcess = inProcess
					if value := r.Header.Get(publicMountHeader); value != "" {
						t.Fatalf("fixed mount header reached application handler as %q", value)
					}
					w.WriteHeader(http.StatusNoContent)
				},
			)
			req := httptest.NewRequest(http.MethodGet, "/api/items/book-1", nil)
			req.Header.Set(publicMountHeader, "/audiobookshelf")
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if got != test.want {
				t.Fatalf("trusted public mount = %q, want %q", got, test.want)
			}
			if gotInProcess != test.wantInProcess {
				t.Fatalf("in-process dispatch = %t, want %t", gotInProcess, test.wantInProcess)
			}
		})
	}
}

func TestPublicMountHandlerPrefersPrivateContextOverHeader(t *testing.T) {
	var got string
	var gotInProcess bool
	handler := PublicMountHandler(
		func(*http.Request) bool { return true },
		func(_ http.ResponseWriter, _ *http.Request, mount string, inProcess bool) {
			got = mount
			gotInProcess = inProcess
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/items/book-1", nil)
	req = req.WithContext(withPublicMount(req.Context(), "/audiobookshelf"))
	req.Header.Set(publicMountHeader, "/forged")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if got != "/audiobookshelf" {
		t.Fatalf("trusted public mount = %q, want private /audiobookshelf", got)
	}
	if !gotInProcess {
		t.Fatal("private mount context did not retain in-process dispatch identity")
	}
}
