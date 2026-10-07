package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

func TestNativeFiniteRequestInventory(t *testing.T) {
	cases := []struct{ method, path, operation string }{
		{"PUT", "/libraries/roots/override", "root-set"}, {"DELETE", "/libraries/roots/override", "root-delete"},
		{"POST", "/libraries/stale-ids/a%2Fb/rematch", "rematch"}, {"PUT", "/libraries/reorder", "reorder"},
		{"PUT", "/libraries/7", "update-library"}, {"DELETE", "/libraries/7", "delete-library"},
		{"POST", "/libraries/7/check-mount", "mount"}, {"POST", "/libraries/7/confirm-empty-root-cleanup", "allowance"},
		{"POST", "/libraries/7/metadata-match-queue/retry", "queue-retry"}, {"POST", "/libraries/7/metadata-match-queue/cancel", "queue-cancel"},
		{"POST", "/libraries/7/refresh-metadata", "refresh-library"}, {"PUT", "/libraries/7/providers", "providers"},
		{"PUT", "/libraries/7/poster", "poster"}, {"DELETE", "/libraries/7/poster", "poster-delete"},
		{"POST", "/scan", "scan"}, {"POST", "/scan/cancel", "scan-cancel"},
		{"POST", "/admin/items/a/refresh-metadata", "refresh-item"}, {"PATCH", "/admin/items/a/metadata", "metadata"},
		{"POST", "/admin/items/a/match/search", "match-search"}, {"POST", "/admin/items/a/match/apply", "match-apply"},
		{"POST", "/admin/items/a/split", "split"}, {"POST", "/admin/items/a/merge", "merge"},
		{"POST", "/admin/items/a/metadata-translation", "translate"}, {"POST", "/admin/items/a/metadata-translation/jobs/4/cancel", "translate-cancel"},
		{"POST", "/admin/items/a/images/apply", "image"}, {"POST", "/items/a/translate-description", "on-view"},
		{"POST", "/items/a/trailers/refresh", "trailers"},
	}
	if len(cases) != 27 || len(nativeFiniteRoutes) != 27 {
		t.Fatal("finite inventory changed")
	}
	for _, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v1"+tc.path, nil)
			route, _, ok := nativeRouteMatch(r)
			if !ok || route.operation != tc.operation {
				t.Fatalf("%s %s: %+v %v", tc.method, tc.path, route, ok)
			}
		})
	}
	for _, path := range []string{"/api/v1/libraries/7/", "/api/v1/libraries//poster", "/api/v1/%6cibraries/7", "/api/v1/libraries/7/poster/more", "/else/api/v1/libraries/7", "/api/v1/libraries", "/api/v1/libraries/roots/unknown"} {
		r := httptest.NewRequest("PUT", path, nil)
		if _, _, ok := nativeRouteMatch(r); ok {
			t.Fatalf("invented route %s", path)
		}
	}
	r := httptest.NewRequest("DELETE", "/api/v1/libraries/reorder", nil)
	route, p, ok := nativeRouteMatch(r)
	if !ok || route.operation != "delete-library" || p["id"] != "reorder" {
		t.Fatal("method-dependent chi parameter fallback lost")
	}
	r = httptest.NewRequest("PUT", "/api/v1/libraries/1", nil)
	r.URL.RawPath = "/api/v1/libraries/%31"
	_, p, ok = nativeRouteMatch(r)
	if !ok || p["id"] != "%31" {
		t.Fatal("raw path was normalized")
	}
}

func TestNativeV1BodyReplay(t *testing.T) {
	for _, body := range []string{`{"entries":[{"id":1}],"entries":[{"id":2}]} {"second":true}`, `null`, "", `{"entries":`, "{\"unknown\":\"" + strings.Repeat("x", 2<<20) + "\",\"entries\":[{\"id\":3}]} trailing"} {
		r := httptest.NewRequest("PUT", "/api/v1/libraries/reorder", strings.NewReader(body))
		var req struct {
			Entries []catalog.FolderReorderEntry `json:"entries"`
		}
		_ = nativeDecodeReplay(r, &req)
		replayed, err := io.ReadAll(r.Body)
		if err != nil || string(replayed) != body {
			t.Fatalf("body replay changed: %v", err)
		}
	}
}

// Unit boundary only: these doubles test sequencing/delegation. They do not
// grant host authority and provide no authenticated DB acceptance evidence.
type nativeMergeUnitBase struct {
	apiv2.AdminCatalogSplitService
	calls      int
	ctx        context.Context
	from, into string
	result     string
	err        error
}

func (b *nativeMergeUnitBase) MergeAdminItem(ctx context.Context, from, into string) (string, error) {
	b.calls++
	b.ctx = ctx
	b.from = from
	b.into = into
	return b.result, b.err
}
func TestNativeV2MergeDenialAndDelegationUnit(t *testing.T) {
	ctx := context.WithValue(t.Context(), struct{}{}, "original")
	for _, deny := range []bool{true, false} {
		base := &nativeMergeUnitBase{result: "returned", err: errors.New("original service error")}
		g := &nativeMutationGuard{}
		s := &nativeMutationServices{AdminCatalogSplitService: base, guard: g, admit: func(got context.Context, gate string, targets catalog.NativePhaseTargets, code string) (context.Context, error) {
			if got != ctx || gate != "admin" || code != "native_repair_unsupported" || !reflect.DeepEqual(targets.ContentIDs, []string{"from", "into"}) {
				t.Fatal("incomplete merge admission")
			}
			if deny {
				return got, &handlers.APIError{Status: 409, Code: code, Message: "Native library repair is unsupported"}
			}
			return got, nil
		}}
		result, err := s.MergeAdminItem(ctx, "from", " into ")
		if deny {
			var api *handlers.APIError
			if !errors.As(err, &api) || api.Status != 409 || base.calls != 0 || result != "" {
				t.Fatalf("denial delegated: %v", err)
			}
		} else if base.calls != 1 || base.ctx != ctx || base.from != "from" || base.into != " into " || result != base.result || err != base.err {
			t.Fatal("local delegation changed original args/result")
		}
	}
}

func TestNativeFinalV2WiringUnit(t *testing.T) {
	base := &nativeMergeUnitBase{}
	deps := apiv2.Dependencies{AdminCatalogSplit: base}
	g := &nativeMutationGuard{}
	g.wrapV2(&deps)
	wrapped, ok := deps.AdminCatalogSplit.(*nativeMutationServices)
	if !ok || wrapped.AdminCatalogSplitService != base {
		t.Fatal("final actual instance was not wrapped")
	}
	if deps.LibraryAdmin != nil || deps.ScanControls != nil || deps.MetadataAI != nil || deps.CatalogTrailers != nil {
		t.Fatal("nil dependency became published")
	}
}

func TestNativeV2SnapshotPreservesOriginalWriterUnit(t *testing.T) {
	g := &nativeMutationGuard{}
	for _, method := range []string{"POST", "GET"} {
		rec := httptest.NewRecorder()
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if w != rec {
				t.Fatal("original writer replaced")
			}
			if method == "POST" {
				if r.Context().Value(nativeMutationRequestKey{}) == nil {
					t.Fatal("missing immutable snapshot")
				}
				var problem *apiv2.Problem
				if !errors.As(g.transport(r.Context(), &catalog.NativePhaseRefusal{Code: "native_local_operation_unsupported"}), &problem) || !strings.HasSuffix(problem.Type, "/native_local_operation_unsupported") {
					t.Fatal("native problem lost")
				}
				ordinary := &handlers.APIError{Status: 400, Code: "bad_request", Message: "Invalid request"}
				if g.transport(r.Context(), ordinary) != ordinary {
					t.Fatal("ordinary error changed")
				}
			}
			w.WriteHeader(204)
		})
		g.captureV2(next).ServeHTTP(rec, httptest.NewRequest(method, "/api/v2/admin/items/a/merge", nil))
	}
}

func TestNativeOriginalGateDenialPrecedesAddressingUnit(t *testing.T) {
	g := &nativeMutationGuard{}
	body := &nativeNeverRead{t: t}
	originalGate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	}
	handler := originalGate(g.v1(func(http.ResponseWriter, *http.Request) { t.Fatal("denied gate delegated") }))
	r := httptest.NewRequest("PUT", "/api/v1/libraries/reorder", nil)
	r.Body = body
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
}

type nativeNeverRead struct{ t *testing.T }

func (b *nativeNeverRead) Read([]byte) (int, error) {
	b.t.Fatal("body read before real gate")
	return 0, io.EOF
}
func (b *nativeNeverRead) Close() error { return nil }

// All 27 concrete override bodies are called with an explicit refusal double.
// Nil embedded bases deliberately panic if any override accidentally delegates.
// Targets that need PG addressing fail unavailable before the admission double;
// this is only a missing-composition/denial gate, not native DB acceptance.
func TestNativeV2AllFiniteOverridesDenyUnit(t *testing.T) {
	g := &nativeMutationGuard{}
	s := &nativeMutationServices{guard: g, admit: func(ctx context.Context, _ string, _ catalog.NativePhaseTargets, code string) (context.Context, error) {
		return ctx, &handlers.APIError{Status: 409, Code: code, Message: nativeRefusalMessage(code)}
	}}
	id := 1
	ctx := t.Context()
	cases := []struct {
		name string
		call func() error
	}{
		{"UpdateLibrary", func() error { _, err := s.UpdateLibrary(ctx, id, 1, handlers.LibraryUpdateRequest{}); return err }},
		{"DeleteLibrary", func() error { _, err := s.DeleteLibrary(ctx, id, 1); return err }},
		{"CheckLibraryMount", func() error { _, err := s.CheckLibraryMount(ctx, id); return err }},
		{"ConfirmEmptyRootCleanup", func() error { return s.ConfirmEmptyRootCleanup(ctx, id) }},
		{"ReorderLibraries", func() error { return s.ReorderLibraries(ctx, []catalog.FolderReorderEntry{{ID: id}}) }},
		{"SetRootOverride", func() error {
			return s.SetRootOverride(ctx, 1, handlers.RootOverrideUpsertRequest{LibraryID: id, RootPath: "/root"})
		}},
		{"DeleteRootOverride", func() error {
			return s.DeleteRootOverride(ctx, handlers.RootOverrideDeleteRequest{LibraryID: id, RootPath: "/root"})
		}},
		{"RematchStaleID", func() error { return s.RematchStaleID(ctx, "item") }},
		{"RetryMetadataMatchQueue", func() error { _, err := s.RetryMetadataMatchQueue(ctx, id); return err }},
		{"CancelMetadataMatchQueue", func() error { _, err := s.CancelMetadataMatchQueue(ctx, id); return err }},
		{"RefreshLibraryMetadata", func() error {
			_, err := s.RefreshLibraryMetadata(ctx, id, 1, adminjob.LibraryRefreshModeQuick)
			return err
		}},
		{"UploadLibraryPoster", func() error { _, err := s.UploadLibraryPoster(ctx, id, "image/png", []byte("poster")); return err }},
		{"DeleteLibraryPoster", func() error { return s.DeleteLibraryPoster(ctx, id) }},
		{"SetLibraryProviders", func() error { return s.SetLibraryProviders(ctx, id, nil) }},
		{"StartLibraryScan", func() error { _, err := s.StartLibraryScan(ctx, &id, ""); return err }},
		{"CancelLibraryScans", func() error { _, err := s.CancelLibraryScans(ctx, id); return err }},
		{"CreateItemMetadataRefresh", func() error {
			_, err := s.CreateItemMetadataRefresh(ctx, "item", adminjob.ItemRefreshModeQuick, 1)
			return err
		}},
		{"UpdateCatalogItemMetadata", func() error {
			_, err := s.UpdateCatalogItemMetadata(ctx, "item", handlers.UpdateItemMetadataRequest{})
			return err
		}},
		{"SearchAdminItemMatches", func() error {
			_, err := s.SearchAdminItemMatches(ctx, "item", handlers.AdminMatchSearchRequest{})
			return err
		}},
		{"ApplyAdminItemMatch", func() error {
			_, err := s.ApplyAdminItemMatch(ctx, "item", handlers.AdminMatchApplyRequest{})
			return err
		}},
		{"SplitAdminItem", func() error {
			_, err := s.SplitAdminItem(ctx, "item", handlers.AdminSplitRequest{FileIDs: []int{1}})
			return err
		}},
		{"MergeAdminItem", func() error { _, err := s.MergeAdminItem(ctx, "item", "into"); return err }},
		{"ApplyAdminItemImage", func() error {
			_, err := s.ApplyAdminItemImage(ctx, "item", handlers.AdminItemImageRequest{})
			return err
		}},
		{"TranslateAdminMetadata", func() error {
			_, err := s.TranslateAdminMetadata(ctx, "item", handlers.TranslateMetadataRequest{TargetLanguage: "nl"}, 1)
			return err
		}},
		{"CancelAdminMetadataTranslation", func() error { return s.CancelAdminMetadataTranslation(ctx, "item", 1) }},
		{"TranslateOnView", func() error { _, err := s.TranslateOnView(ctx, catalog.AccessFilter{}, "item", "nl", nil); return err }},
		{"RequestTrailersRefresh", func() error {
			_, err := s.RequestTrailersRefresh(ctx, 1, "item", func() (catalog.AccessFilter, error) {
				t.Fatal("refused request called access callback")
				return catalog.AccessFilter{}, nil
			})
			return err
		}},
	}
	if len(cases) != 27 {
		t.Fatal("method inventory changed")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var api *handlers.APIError
			if err := tc.call(); !errors.As(err, &api) || (api.Status != 409 && api.Status != 503) {
				t.Fatalf("refusal lost: %v", err)
			}
		})
	}
}
