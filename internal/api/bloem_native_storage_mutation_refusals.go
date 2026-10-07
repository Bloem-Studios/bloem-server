package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/scantrigger"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeMutationGuard struct {
	capabilities *nativeStorageCapabilities

	pool      *pgxpool.Pool
	access    *apimw.NativeMutationAccess
	resources *resourcetenancy.Store
	folders   *catalog.FolderRepository
	curation  bool
	library   *handlers.LibraryHandler
	admin     *handlers.AdminHandler
}

type nativeMutationRequestKey struct{}
type nativeMutationV2Key struct{}

// Called once where the router's REAL auth/tenant/viewer/policy instances are
// in scope. Packing changes no routing tree or original middleware order.
func nativeMutationGates(deps Dependencies, am *apimw.AuthMiddleware, vm *apimw.ViewerAccessMiddleware,
	tm *apimw.TenantMiddleware, users *auth.UserRepository, primary apimw.PrimaryProfileChecker,
	pdp apimw.PermissionDecider, groups access.GroupPolicyProvider, library *handlers.LibraryHandler,
	admin *handlers.AdminHandler, acting, curation func(http.Handler) http.Handler) (*nativeMutationGuard, func(http.Handler) http.Handler, func(http.Handler) http.Handler) {
	g := &nativeMutationGuard{pool: deps.DB, folders: deps.FolderRepo, curation: curation != nil, library: library, admin: admin}
	if deps.DB != nil {
		g.resources = resourcetenancy.NewStore(deps.DB)
	}
	var pm *apimw.PolicyPermissionMiddleware
	var lm *apimw.PermissionMiddleware
	if deps.DB != nil && users != nil {
		libraries := apimw.NewPGMetadataTargetLibraryResolver(deps.DB)
		if pdp != nil {
			pm = apimw.NewPolicyPermissionMiddleware(users, libraries, primary, pdp, groups)
		} else {
			lm = apimw.NewPermissionMiddleware(users, libraries, primary, groups)
		}
	}
	g.access = apimw.NewNativeMutationAccess(am, vm, tm, users, primary, pm, lm)
	if acting != nil && curation != nil && deps.NativeStorageManagement != nil {
		g.capabilities, _ = deps.NativeStorageManagement.Capabilities.(*nativeStorageCapabilities)
	}
	pack := func(gate func(http.Handler) http.Handler) func(http.Handler) http.Handler {
		if gate == nil {
			return nil
		}
		return func(next http.Handler) http.Handler { return gate(g.v1(next.ServeHTTP)) }
	}
	return g, pack(acting), pack(curation)
}

// Only the phase authorizer is carried. No service/PDP/global writer is
// replaced; trusted native host enrichment without this origin stays legal.
func (g *nativeMutationGuard) admission(ctx context.Context, gate string, targets catalog.NativePhaseTargets, code string) (context.Context, error) {
	r, _ := ctx.Value(nativeMutationRequestKey{}).(*http.Request)
	if r == nil {
		return ctx, g.transport(ctx, nativeUnavailable())
	}
	origin, err := apimw.CaptureNativeMutationOrigin(r.WithContext(ctx))
	if err != nil {
		return ctx, g.transport(ctx, err)
	}
	callback := func(phaseCtx context.Context, q catalog.NativePhaseQuery, t catalog.NativePhaseTargets) error {
		resolved, err := g.expand(phaseCtx, q, t)
		if err != nil {
			return err
		}
		return g.authorize(phaseCtx, origin, gate, q, resolved)
	}
	selected, err := g.expand(ctx, g.pool, targets)
	if err == nil {
		err = g.authorize(ctx, origin, gate, g.pool, selected)
	}
	if err == nil {
		err = g.classify(ctx, g.pool, selected, code)
	}
	if err != nil {
		return ctx, g.transport(ctx, err)
	}
	return catalog.WithNativePhaseAuthorizer(ctx, g.pool, callback), nil
}

func nativeUnavailable() error {
	return &catalog.NativePhaseRefusal{Code: "native_storage_unavailable"}
}
func nativeMissing() error { return &catalog.NativePhaseRefusal{Code: "not_found"} }
func nativeTargetError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, catalog.ErrItemNotFound) || errors.Is(err, catalog.ErrSeasonNotFound) || errors.Is(err, catalog.ErrEpisodeNotFound) || errors.Is(err, catalog.ErrExtraNotFound) || errors.Is(err, catalog.ErrFolderNotFound) || errors.Is(err, resourcetenancy.ErrResourceHidden) || errors.Is(err, pgx.ErrNoRows) {
		return nativeMissing()
	}
	return nativeUnavailable()
}

// expand reads actual associations without classifying. It never invents an
// existing item for a prospective key. Producer RequireNativePhase normalizes
// occupancy; this callback still verifies each supplied absent basis.
func (g *nativeMutationGuard) expand(ctx context.Context, q catalog.NativePhaseQuery, t catalog.NativePhaseTargets) (catalog.NativePhaseTargets, error) {
	if g == nil || g.pool == nil || q == nil {
		return t, nativeUnavailable()
	}
	out := catalog.NativePhaseTargets{ContentIDs: slices.Clone(t.ContentIDs), FileIDs: slices.Clone(t.FileIDs), LibraryIDs: slices.Clone(t.LibraryIDs)}
	for _, p := range t.Prospective {
		if p.ContentID == "" || len(p.SourceIDs)+len(p.ParentIDs)+len(p.LibraryIDs) == 0 {
			return out, nativeUnavailable()
		}
		var occupied bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1 UNION ALL SELECT 1 FROM seasons WHERE content_id=$1 UNION ALL SELECT 1 FROM episodes WHERE content_id=$1 UNION ALL SELECT 1 FROM media_extras WHERE content_id=$1)`, p.ContentID).Scan(&occupied); err != nil {
			return out, nativeUnavailable()
		}
		if occupied {
			out.ContentIDs = append(out.ContentIDs, p.ContentID)
		} else {
			out.ContentIDs = append(out.ContentIDs, p.SourceIDs...)
			out.ContentIDs = append(out.ContentIDs, p.ParentIDs...)
			out.LibraryIDs = append(out.LibraryIDs, p.LibraryIDs...)
		}
	}
	for _, id := range slices.Clone(out.FileIDs) {
		var content, episode, extra string
		var library int
		if err := q.QueryRow(ctx, `SELECT COALESCE(content_id,''),COALESCE(episode_id,''),COALESCE(extra_id,''),media_folder_id FROM media_files WHERE id=$1`, id).Scan(&content, &episode, &extra, &library); err != nil {
			return out, nativeTargetError(err)
		}
		out.LibraryIDs = append(out.LibraryIDs, library)
		for _, key := range []string{content, episode, extra} {
			if key != "" {
				out.ContentIDs = append(out.ContentIDs, key)
			}
		}
	}
	for _, id := range uniqueNativeStrings(out.ContentIDs) {
		var root string
		err := q.QueryRow(ctx, `SELECT content_id FROM media_items WHERE content_id=$1 UNION ALL SELECT series_id FROM seasons WHERE content_id=$1 UNION ALL SELECT series_id FROM episodes WHERE content_id=$1 UNION ALL SELECT parent_id FROM media_extras WHERE content_id=$1 LIMIT 1`, id).Scan(&root)
		if err != nil {
			return out, nativeTargetError(err)
		}
		out.ContentIDs = append(out.ContentIDs, root)
		rows, err := q.Query(ctx, `SELECT media_folder_id FROM media_item_libraries WHERE content_id=$1 UNION SELECT media_folder_id FROM media_files WHERE content_id=$1 OR episode_id=$2 OR extra_id=$2`, root, id)
		if err != nil {
			return out, nativeUnavailable()
		}
		for rows.Next() {
			var library int
			if err = rows.Scan(&library); err != nil {
				rows.Close()
				return out, nativeUnavailable()
			}
			out.LibraryIDs = append(out.LibraryIDs, library)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, nativeUnavailable()
		}
	}
	out.ContentIDs = uniqueNativeStrings(out.ContentIDs)
	out.FileIDs = uniqueNativeInts(out.FileIDs)
	out.LibraryIDs = uniqueNativeInts(out.LibraryIDs)
	return out, nil
}

func uniqueNativeStrings(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
func uniqueNativeInts(ids []int) []int {
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (g *nativeMutationGuard) authorize(ctx context.Context, origin *apimw.NativeMutationOrigin, gate string, q catalog.NativePhaseQuery, t catalog.NativePhaseTargets) error {
	current, err := g.access.Current(ctx, origin, gate, t.LibraryIDs)
	if err != nil {
		return err
	}
	filter := handlers.AccessFilterFromContext(current, origin.DeviceID())
	items := catalog.NativePhaseItemAccess{Query: q}
	files := &handlers.MediaFileAuthorizer{FileResolver: scanner.NativePhaseFileLookup{Query: q}, ItemAccess: items,
		EpisodeLookup: catalog.NativePhaseEpisodeLookup{Query: q}, ExtraLookup: catalog.NativePhaseExtraLookup{Query: q}}
	// Authorize the entire set before ANY classifier call, irrespective of its
	// ordering. Parent-series access uses the host's current item predicate.
	for _, id := range t.ContentIDs {
		var root string
		if err = q.QueryRow(ctx, `SELECT content_id FROM media_items WHERE content_id=$1 UNION ALL SELECT series_id FROM seasons WHERE content_id=$1 UNION ALL SELECT series_id FROM episodes WHERE content_id=$1 UNION ALL SELECT parent_id FROM media_extras WHERE content_id=$1 LIMIT 1`, id).Scan(&root); err != nil {
			return nativeTargetError(err)
		}
		if err = items.EnsureAccessible(current, root, filter); err != nil {
			return nativeTargetError(err)
		}
	}
	for _, id := range t.FileIDs {
		if _, err = files.AuthorizeContext(current, id, filter); err != nil {
			return nativeTargetError(err)
		}
	}
	tenant, ok := tenancy.FromContext(current)
	if !ok {
		return nativeMissing()
	}
	for _, id := range t.LibraryIDs {
		if id <= 0 || !catalog.FileAllowedByLibraryScope(&models.MediaFile{MediaFolderID: id}, filter.AllowedLibraryIDs, filter.DisabledLibraryIDs) {
			return nativeMissing()
		}
		var exists bool
		if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_folders WHERE id=$1)`, id).Scan(&exists); err != nil {
			return nativeUnavailable()
		}
		if !exists {
			return nativeMissing()
		}
		if _, err = g.resources.RequireAccess(current, tenant, resourcetenancy.RootRef{Kind: resourcetenancy.RootMediaFolder, ID: int64(id)}); err != nil {
			return nativeTargetError(err)
		}
	}
	return nil
}

func (g *nativeMutationGuard) classify(ctx context.Context, q catalog.NativePhaseQuery, t catalog.NativePhaseTargets, code string) error {
	if q == nil || !catalog.NativeStorageSchemaReady(ctx, q) {
		return nativeUnavailable()
	}
	native := false
	// Finish the full class inventory so an inconsistent sibling yields fixed
	// unavailable rather than whichever native entry happened to be first.
	check := func(sql string, id any) error {
		var class string
		if err := q.QueryRow(ctx, sql, id).Scan(&class); err != nil {
			return nativeUnavailable()
		}
		switch class {
		case "local":
		case "native":
			native = true
		default:
			return nativeUnavailable()
		}
		return nil
	}
	for _, id := range t.ContentIDs {
		if err := check(`SELECT bloem_native_item_class($1)`, id); err != nil {
			return err
		}
	}
	for _, id := range t.LibraryIDs {
		if err := check(`SELECT bloem_native_folder_class($1)`, id); err != nil {
			return err
		}
	}
	if native {
		return &handlers.APIError{Status: 409, Code: code, Message: nativeRefusalMessage(code)}
	}
	return nil
}

func nativeRefusalMessage(code string) string {
	switch code {
	case "native_library_delete_unsupported":
		return "Native library deletion is unsupported"
	case "native_repair_unsupported":
		return "Native library repair is unsupported"
	case "native_local_operation_unsupported":
		return "Native local operation is unsupported"
	case "unauthenticated":
		return "Authentication required"
	case "forbidden":
		return "Access forbidden"
	case "not_found":
		return "Resource not found"
	}
	return "Native storage unavailable"
}
func nativeAPIError(err error) *handlers.APIError {
	var api *handlers.APIError
	if errors.As(err, &api) {
		return api
	}
	var refusal *catalog.NativePhaseRefusal
	if !errors.As(err, &refusal) {
		return &handlers.APIError{Status: 503, Code: "native_storage_unavailable", Message: "Native storage unavailable"}
	}
	status := 503
	code := refusal.Code
	switch code {
	case "unauthenticated":
		status = 401
		code = "unauthorized"
	case "forbidden":
		status = 403
	case "not_found":
		status = 404
	case "native_local_operation_unsupported":
		status = 409
	}
	return (&handlers.APIError{Status: status, Code: code, Message: nativeRefusalMessage(refusal.Code)}).WithCause(err)
}
func (g *nativeMutationGuard) transport(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	api := nativeAPIError(err)
	if v2, _ := ctx.Value(nativeMutationV2Key{}).(bool); v2 {
		switch api.Code {
		case "native_library_delete_unsupported", "native_repair_unsupported", "native_local_operation_unsupported", "native_storage_unavailable":
			return apiv2.NewProblem(apiv2.ProblemType{ID: api.Code, Status: api.Status, Title: http.StatusText(api.Status)}, api.Message)
		}
	}
	// Ordinary errors retain their owning translator's status/code mapping.
	return api
}

// Only operations that actually select library catalog/files expand them.
func (g *nativeMutationGuard) libraryTargets(ctx context.Context, id int, contents bool) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{LibraryIDs: []int{id}}
	if !contents {
		return t, nil
	}
	if g.pool == nil {
		return t, nativeUnavailable()
	}
	rows, err := g.pool.Query(ctx, `SELECT content_id FROM media_item_libraries WHERE media_folder_id=$1`, id)
	if err != nil {
		return t, nativeUnavailable()
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return t, nativeUnavailable()
		}
		t.ContentIDs = append(t.ContentIDs, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return t, nativeUnavailable()
	}
	rows, err = g.pool.Query(ctx, `SELECT id FROM media_files WHERE media_folder_id=$1`, id)
	if err != nil {
		return t, nativeUnavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var file int
		if err = rows.Scan(&file); err != nil {
			return t, nativeUnavailable()
		}
		t.FileIDs = append(t.FileIDs, file)
	}
	return t, nativeTargetError(rows.Err())
}

func (g *nativeMutationGuard) itemTargets(ctx context.Context, id string, children bool) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{ContentIDs: []string{id}}
	if !children {
		return t, nil
	}
	if g.pool == nil {
		return t, nativeUnavailable()
	}
	rows, err := g.pool.Query(ctx, `SELECT content_id FROM seasons WHERE series_id=$1 UNION SELECT content_id FROM episodes WHERE series_id=$1`, id)
	if err != nil {
		return t, nativeUnavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return t, nativeUnavailable()
		}
		t.ContentIDs = append(t.ContentIDs, key)
	}
	return t, nativeTargetError(rows.Err())
}

func (g *nativeMutationGuard) rootTargets(ctx context.Context, id int, path string) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{LibraryIDs: []int{id}}
	if g.pool == nil || g.library == nil || g.library.ObservedLocationRepo == nil {
		return t, nativeUnavailable()
	}
	// The owning service selects the primary observed group. Reuse its actual
	// repository rather than reinterpret the root as a path-prefix selector.
	location, err := g.library.ObservedLocationRepo.Get(ctx, id, filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return t, nativeUnavailable()
	}
	if location == nil || location.PrimaryContentGroupKey == "" {
		return t, nil
	} // Original service renders root/ambiguity validation.
	rows, err := g.pool.Query(ctx, `SELECT id FROM media_files WHERE media_folder_id=$1 AND group_key_version=$2 AND content_group_key=$3`, id, location.PrimaryGroupKeyVersion, location.PrimaryContentGroupKey)
	if err != nil {
		return t, nativeUnavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var file int
		if err = rows.Scan(&file); err != nil {
			return t, nativeUnavailable()
		}
		t.FileIDs = append(t.FileIDs, file)
	}
	return t, nativeTargetError(rows.Err())
}

func (g *nativeMutationGuard) scanTargets(ctx context.Context, id *int, path string) (catalog.NativePhaseTargets, error) {
	if g.folders == nil {
		return catalog.NativePhaseTargets{}, nativeUnavailable()
	}
	var folder *models.MediaFolder
	var err error
	if id != nil {
		folder, err = g.folders.GetByID(ctx, *id)
	} else {
		var folders []*models.MediaFolder
		folders, err = g.folders.List(ctx)
		if err == nil {
			folder, _, err = scantrigger.MatchFolderForPath(filepath.Clean(path), folders)
		}
	}
	if err != nil {
		var req *scantrigger.RequestError
		if errors.As(err, &req) {
			return catalog.NativePhaseTargets{}, &handlers.APIError{Status: req.Status, Code: req.Code, Message: req.Message}
		}
		return catalog.NativePhaseTargets{}, nativeTargetError(err)
	}
	if folder == nil {
		return catalog.NativePhaseTargets{}, nativeMissing()
	}
	if strings.TrimSpace(path) == "" {
		return g.libraryTargets(ctx, folder.ID, true)
	}
	t := catalog.NativePhaseTargets{LibraryIDs: []int{folder.ID}}
	// Read known path-selected rows before any filesystem/provider/queue work.
	// Exact widening/reselection remains in the producer's owning boundary.
	rows, err := g.pool.Query(ctx, `SELECT id FROM media_files WHERE media_folder_id=$1 AND (file_path=$2 OR starts_with(file_path,$3))`, folder.ID, filepath.Clean(path), filepath.Clean(path)+string(filepath.Separator))
	if err != nil {
		return t, nativeUnavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var file int
		if err = rows.Scan(&file); err != nil {
			return t, nativeUnavailable()
		}
		t.FileIDs = append(t.FileIDs, file)
	}
	return t, nativeTargetError(rows.Err())
}

func (g *nativeMutationGuard) cancelTargets(ctx context.Context, id string, jobID int64) (catalog.NativePhaseTargets, error) {
	t := catalog.NativePhaseTargets{ContentIDs: []string{id}}
	if g.pool == nil {
		return t, nativeUnavailable()
	}
	var target string
	var children bool
	err := g.pool.QueryRow(ctx, `SELECT content_id,include_children FROM metadata_translation_jobs WHERE id=$1`, jobID).Scan(&target, &children)
	if err != nil || target != id {
		if err == nil || errors.Is(err, pgx.ErrNoRows) {
			return t, nativeMissing()
		}
		return t, nativeUnavailable()
	}
	return g.itemTargets(ctx, id, children)
}

// Route matching follows chi's full raw-path/method grammar. This runs after
// the original gate, including at a library mount where child params do not
// yet exist. No route context or URL parameter is fabricated.
type nativeFiniteRoute struct{ method, path, operation, gate string }

var nativeFiniteRoutes = []nativeFiniteRoute{
	{"PUT", "/libraries/roots/override", "root-set", "admin"},
	{"DELETE", "/libraries/roots/override", "root-delete", "admin"},
	{"POST", "/libraries/stale-ids/{id}/rematch", "rematch", "admin"},
	{"PUT", "/libraries/reorder", "reorder", "admin"},
	{"PUT", "/libraries/{id}", "update-library", "admin"},
	{"DELETE", "/libraries/{id}", "delete-library", "admin"},
	{"POST", "/libraries/{id}/check-mount", "mount", "admin"},
	{"POST", "/libraries/{id}/confirm-empty-root-cleanup", "allowance", "admin"},
	{"POST", "/libraries/{id}/metadata-match-queue/retry", "queue-retry", "admin"},
	{"POST", "/libraries/{id}/metadata-match-queue/cancel", "queue-cancel", "admin"},
	{"POST", "/libraries/{id}/refresh-metadata", "refresh-library", "admin"},
	{"PUT", "/libraries/{id}/providers", "providers", "admin"},
	{"PUT", "/libraries/{id}/poster", "poster", "admin"},
	{"DELETE", "/libraries/{id}/poster", "poster-delete", "admin"},
	{"POST", "/scan", "scan", "admin"},
	{"POST", "/scan/cancel", "scan-cancel", "admin"},
	{"POST", "/admin/items/{id}/refresh-metadata", "refresh-item", "curation"},
	{"PATCH", "/admin/items/{id}/metadata", "metadata", "curation"},
	{"POST", "/admin/items/{id}/match/search", "match-search", "curation"},
	{"POST", "/admin/items/{id}/match/apply", "match-apply", "curation"},
	{"POST", "/admin/items/{id}/split", "split", "curation"},
	{"POST", "/admin/items/{id}/merge", "merge", "curation"},
	{"POST", "/admin/items/{id}/metadata-translation", "translate", "curation"},
	{"POST", "/admin/items/{id}/metadata-translation/jobs/{job}/cancel", "translate-cancel", "curation"},
	{"POST", "/admin/items/{id}/images/apply", "image", "admin"},
	{"POST", "/items/{id}/translate-description", "on-view", "viewer"},
	{"POST", "/items/{id}/trailers/refresh", "trailers", "viewer"},
}

func nativeRouteMatch(r *http.Request) (nativeFiniteRoute, map[string]string, bool) {
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return nativeFiniteRoute{}, nil, false
	}
	path = strings.TrimPrefix(path, "/api/v1")
	parts := strings.Split(path, "/")
	for _, route := range nativeFiniteRoutes {
		if route.method != r.Method {
			continue
		}
		pattern := strings.Split(route.path, "/")
		if len(parts) != len(pattern) {
			continue
		}
		params := map[string]string{}
		matched := true
		for i, part := range pattern {
			if strings.HasPrefix(part, "{") {
				if parts[i] == "" {
					matched = false
					break
				}
				params[strings.Trim(part, "{}")] = parts[i]
			} else if part != parts[i] {
				matched = false
				break
			}
		}
		if matched {
			return route, params, true
		}
	}
	return nativeFiniteRoute{}, nil, false
}

// A first-value decoder replays every consumed byte, decoder read-ahead and
// untouched suffix. It preserves duplicate keys, unknown fields, null and
// trailing second values. V1 has no existing hard byte cap; none is invented.
func nativeDecodeReplay(r *http.Request, value any) error {
	if r.Body == nil {
		return io.EOF
	}
	original := r.Body
	var consumed bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(original, &consumed))
	err := decoder.Decode(value)
	r.Body = &nativeReplayBody{Reader: io.MultiReader(bytes.NewReader(consumed.Bytes()), original), closer: original}
	return err
}

type nativeReplayBody struct {
	io.Reader
	closer io.Closer
}

func (b *nativeReplayBody) Close() error { return b.closer.Close() }

func (g *nativeMutationGuard) v1(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		route, params, ok := nativeRouteMatch(r)
		if !ok {
			next(w, r)
			return
		}
		// Source handler availability/validation still wins when it cannot effect
		// a mutation. The same original handler renders its own error once.
		if !g.v1Available(route.operation) {
			next(w, r)
			return
		}
		targets, err := g.v1Targets(r, route.operation, params)
		if errors.Is(err, errNativeOriginalValidation) {
			next(w, r)
			return
		}
		if err == nil {
			gate := route.gate
			if gate == "curation" && !g.curation {
				gate = "admin"
			}
			ctx := context.WithValue(r.Context(), nativeMutationRequestKey{}, apimw.NativeMutationRequestSnapshot(r))
			var admitted context.Context
			admitted, err = g.admission(ctx, gate, targets, nativeOperationCode(route.operation))
			if err == nil {
				next(w, r.WithContext(admitted))
				return
			}
		}
		api := nativeAPIError(err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(api.Status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": api.Code, "message": api.Message})
	}
}

var errNativeOriginalValidation = errors.New("original validation must render")

func nativeOperationCode(operation string) string {
	if operation == "delete-library" {
		return "native_library_delete_unsupported"
	}
	switch operation {
	case "rematch", "match-search", "match-apply", "split", "merge":
		return "native_repair_unsupported"
	}
	return "native_local_operation_unsupported"
}

func (g *nativeMutationGuard) v1Available(operation string) bool {
	switch operation {
	case "poster", "poster-delete":
		return g.library != nil && g.library.ArtworkStore != nil
	case "metadata":
		return g.admin != nil && g.admin.DetailSvc != nil
	case "delete-library":
		return g.library != nil && g.library.JobRepo != nil
	case "refresh-library":
		return g.library != nil && g.library.JobRepo != nil
	case "providers":
		return g.library != nil && g.library.ChainRepo != nil
	case "root-set", "root-delete":
		return g.library != nil && g.library.GroupOverrideRepo != nil && g.library.ObservedLocationRepo != nil
	case "refresh-item":
		return g.admin != nil && g.admin.JobRepo != nil && g.admin.ItemRefreshResolver != nil
	}
	return true
}

func (g *nativeMutationGuard) v1Targets(r *http.Request, operation string, p map[string]string) (catalog.NativePhaseTargets, error) {
	ctx := r.Context()
	t := catalog.NativePhaseTargets{}
	decode := func(value any, optional bool) bool {
		err := nativeDecodeReplay(r, value)
		return err == nil || (optional && errors.Is(err, io.EOF))
	}
	invalid := func() (catalog.NativePhaseTargets, error) { return t, errNativeOriginalValidation }
	if strings.Contains(operation, "library") || slices.Contains([]string{"mount", "allowance", "queue-retry", "queue-cancel", "providers", "poster", "poster-delete"}, operation) {
		id, err := strconv.Atoi(p["id"])
		if err != nil {
			return invalid()
		}
		// Preserve the original parse-before-body ordering for these handlers.
		if operation == "update-library" {
			var req handlers.LibraryUpdateRequest
			if !decode(&req, false) {
				return invalid()
			}
		}
		if operation == "refresh-library" {
			var req struct {
				Mode string `json:"mode"`
			}
			if !decode(&req, true) || (req.Mode != "" && req.Mode != "quick" && req.Mode != "full") {
				return invalid()
			}
		}
		contents := slices.Contains([]string{"delete-library", "refresh-library", "queue-retry", "queue-cancel"}, operation)
		return g.libraryTargets(ctx, id, contents)
	}
	switch operation {
	case "root-set":
		var req handlers.RootOverrideUpsertRequest
		if !decode(&req, false) || req.LibraryID <= 0 {
			return invalid()
		}
		return g.rootTargets(ctx, req.LibraryID, req.RootPath)
	case "root-delete":
		var req handlers.RootOverrideDeleteRequest
		if !decode(&req, false) || req.LibraryID <= 0 {
			return invalid()
		}
		return g.rootTargets(ctx, req.LibraryID, req.RootPath)
	case "reorder":
		var req struct {
			Entries []catalog.FolderReorderEntry `json:"entries"`
		}
		if !decode(&req, false) {
			return invalid()
		}
		for _, entry := range req.Entries {
			t.LibraryIDs = append(t.LibraryIDs, entry.ID)
		}
		return t, nil
	case "scan":
		var req struct {
			LibraryID *int   `json:"library_id"`
			Path      string `json:"path"`
		}
		if !decode(&req, false) || (req.LibraryID == nil && strings.TrimSpace(req.Path) == "") {
			return invalid()
		}
		return g.scanTargets(ctx, req.LibraryID, req.Path)
	case "scan-cancel":
		var req struct {
			LibraryID int `json:"library_id"`
		}
		if !decode(&req, false) || req.LibraryID <= 0 {
			return invalid()
		}
		return g.libraryTargets(ctx, req.LibraryID, true)
	case "merge":
		var req struct {
			Into string `json:"into"`
		}
		if !decode(&req, false) || strings.TrimSpace(req.Into) == "" {
			return invalid()
		}
		return catalog.NativePhaseTargets{ContentIDs: []string{p["id"], strings.TrimSpace(req.Into)}}, nil
	case "split":
		var req handlers.AdminSplitRequest
		if !decode(&req, false) || len(req.FileIDs) == 0 {
			return invalid()
		}
		t.ContentIDs = []string{p["id"]}
		if destination := strings.TrimSpace(req.Target.ContentID); destination != "" {
			t.ContentIDs = append(t.ContentIDs, destination)
		}
		t.FileIDs = req.FileIDs
		return t, nil
	case "match-search":
		var req handlers.AdminMatchSearchRequest
		if !decode(&req, false) {
			return invalid()
		}
		t.ContentIDs = []string{p["id"]}
		if req.LibraryID != nil {
			t.LibraryIDs = []int{*req.LibraryID}
		}
		return t, nil
	case "match-apply":
		var req handlers.AdminMatchApplyRequest
		if !decode(&req, false) {
			return invalid()
		}
		t.ContentIDs = []string{p["id"]}
		if req.LibraryID != nil {
			t.LibraryIDs = []int{*req.LibraryID}
		}
		return t, nil
	case "translate":
		var req handlers.TranslateMetadataRequest
		if !decode(&req, false) || req.TargetLanguage == "" {
			return invalid()
		}
		return g.itemTargets(ctx, p["id"], req.IncludeChildren == nil || *req.IncludeChildren)
	case "translate-cancel":
		job, err := strconv.ParseInt(p["job"], 10, 64)
		if err != nil {
			return invalid()
		}
		return g.cancelTargets(ctx, p["id"], job)
	case "refresh-item":
		var req struct {
			Mode string `json:"mode"`
		}
		if !decode(&req, true) || (req.Mode != "" && req.Mode != "quick" && req.Mode != "complete") {
			return invalid()
		}
	case "metadata":
		var req handlers.UpdateItemMetadataRequest
		if !decode(&req, false) {
			return invalid()
		}
	case "image":
		var req handlers.AdminItemImageRequest
		if !decode(&req, false) {
			return invalid()
		}
	case "on-view":
		var req struct {
			TargetLanguage string `json:"target_language"`
		}
		if !decode(&req, false) || req.TargetLanguage == "" {
			return invalid()
		}
	}
	t.ContentIDs = []string{p["id"]}
	return t, nil
}

// Snapshot only immutable origin inputs. Native problems travel through the
// actual v2 translators; the original response writer is always preserved.
func (g *nativeMutationGuard) captureV2(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !nativeV2Finite(r) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), nativeMutationRequestKey{}, apimw.NativeMutationRequestSnapshot(r))
		ctx = context.WithValue(ctx, nativeMutationV2Key{}, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func nativeV2Finite(r *http.Request) bool {
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	if !strings.HasPrefix(path, "/api/v2/") {
		return false
	}
	path = strings.TrimPrefix(path, "/api/v2")
	method := r.Method
	if strings.HasPrefix(path, "/catalog/items/") {
		path = strings.TrimPrefix(path, "/catalog")
	}
	parts := strings.Split(path, "/")
	for _, route := range nativeFiniteRoutes {
		want := route.method
		if route.operation == "update-library" {
			want = "PATCH"
		}
		if route.operation == "reorder" {
			want = "POST"
		}
		if method != want {
			continue
		}
		pattern := strings.Split(route.path, "/")
		if len(parts) != len(pattern) {
			continue
		}
		matched := true
		for i, part := range pattern {
			if strings.HasPrefix(part, "{") {
				if parts[i] == "" {
					matched = false
					break
				}
			} else if part != parts[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
