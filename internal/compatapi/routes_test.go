package compatapi

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func inlineCompatRouter(h *Handler) chi.Router {
	r := chi.NewRouter()
	r.Route(MountPrefix, func(r chi.Router) {
		r.Use(RelativePaths)
		h.RegisterRoutes(r)
	})
	return r
}

func TestInlineRoutesMatchContractAndLegacyDispatch(t *testing.T) {
	f := newFixture(t, nil)
	before := f.handler.Operations()
	inline := inlineCompatRouter(f.handler)
	legacy := chi.NewRouter()
	legacy.Mount(MountPrefix, http.StripPrefix(MountPrefix, f.handler))
	if !reflect.DeepEqual(before, f.handler.Operations()) {
		t.Fatal("registration changed operation metadata")
	}
	seen := map[string]bool{}
	if err := chi.Walk(inline, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[method+" "+strings.TrimPrefix(path, MountPrefix)] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	contract := loadContractOperations(t)
	if len(seen) != len(contract) {
		t.Fatalf("walked %d routes; contract has %d", len(seen), len(contract))
	}
	for key := range contract {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	probes := append(before, Operation{Method: http.MethodGet, Path: "/no/such/route"}, Operation{Method: http.MethodDelete, Path: "/health"}, Operation{Method: http.MethodGet, Path: ""}, Operation{Method: http.MethodGet, Path: "/"})
	for _, op := range probes {
		for _, bearer := range []string{"", bearerFull} {
			request := func(h http.Handler) *httptest.ResponseRecorder {
				req := httptest.NewRequest(op.Method, MountPrefix+op.Path, strings.NewReader(`{}`))
				req.Header.Set(traceHeader, "registration-parity")
				if bearer != "" {
					req.Header.Set("Authorization", "Bearer "+bearer)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				return rec
			}
			old, got := request(legacy), request(inline)
			if old.Code != got.Code || old.Body.String() != got.Body.String() || !reflect.DeepEqual(old.Header(), got.Header()) {
				t.Errorf("%s %s bearer=%t: inline %d %s %v; legacy %d %s %v", op.Method, op.Path, bearer != "", got.Code, got.Body.String(), got.Header(), old.Code, old.Body.String(), old.Header())
			}
		}
	}
}

func TestInlineEnrollmentReplaysLegacyIdempotencyRecord(t *testing.T) {
	f := newFixture(t, nil)
	legacy := chi.NewRouter()
	legacy.Mount(MountPrefix, http.StripPrefix(MountPrefix, f.handler))
	inline := inlineCompatRouter(f.handler)
	body := `{"secret":"enroll-secret","kind":"audiobookshelf","instance_id":"inst-new","version":"0.1.0","api":{"min":1,"max":1},"requested_capabilities":["catalog"]}`
	var first string
	for _, router := range []http.Handler{legacy, inline} {
		req := httptest.NewRequest(http.MethodPost, MountPrefix+"/enroll", strings.NewReader(body))
		req.Header.Set(idempotencyHeader, "registration-replay")
		req.Header.Set(traceHeader, "registration-parity")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("enroll = %d %s", rec.Code, rec.Body.String())
		}
		if first == "" {
			first = rec.Body.String()
		} else if first != rec.Body.String() {
			t.Fatal("replay response changed")
		}
	}
	if f.enroller.calls != 1 {
		t.Fatalf("enrollment ran %d times, want 1", f.enroller.calls)
	}
}

func TestInlineParameterizedMutationReplaysLegacyRecord(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)
	legacy := chi.NewRouter()
	legacy.Mount(MountPrefix, http.StripPrefix(MountPrefix, f.handler))
	inline := inlineCompatRouter(f.handler)
	var first string
	for _, router := range []http.Handler{legacy, inline} {
		req := httptest.NewRequest(http.MethodPut, MountPrefix+"/state/progress/item%20one", strings.NewReader(`{"position_seconds":12}`))
		req.Header.Set("Authorization", "Bearer "+bearerFull)
		req.Header.Set(subjectTokenHeader, token)
		req.Header.Set(idempotencyHeader, "parameterized-registration-replay")
		req.Header.Set(traceHeader, "registration-parity")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("progress = %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"item_id":"item%20one"`) && !strings.Contains(rec.Body.String(), `"item_id":"item one"`) {
			t.Fatalf("missing route parameter in %s", rec.Body.String())
		}
		if first == "" {
			first = rec.Body.String()
		} else if first != rec.Body.String() {
			t.Fatal("parameterized replay response changed")
		}
	}
	if f.state.setCalls != 1 {
		t.Fatalf("progress write ran %d times, want 1", f.state.setCalls)
	}
}
