package catalog

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

// NativePhaseQuery is the producer's existing query context, never a policy lease.
type NativePhaseQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// NativePhaseProspective describes a genuinely absent destination. Its generated
// key grants nothing: the host authorizes the actual source/parent and libraries.
type NativePhaseProspective struct {
	ContentID            string
	SourceIDs, ParentIDs []string
	LibraryIDs           []int
}
type NativePhaseTargets struct {
	ContentIDs          []string
	FileIDs, LibraryIDs []int
	Prospective         []NativePhaseProspective
}
type NativePhaseAuthorizer func(context.Context, NativePhaseQuery, NativePhaseTargets) error

type nativePhaseKey struct{}
type nativePhaseOrigin struct {
	query     NativePhaseQuery
	authorize NativePhaseAuthorizer
}

// WithNativePhaseAuthorizer installs the real caller's immutable authorization
// closure. Nested delegates cannot replace or erase the originating request.
func WithNativePhaseAuthorizer(ctx context.Context, q NativePhaseQuery, authorize NativePhaseAuthorizer) context.Context {
	if NativePhaseRequest(ctx) {
		return ctx
	}
	return context.WithValue(ctx, nativePhaseKey{}, nativePhaseOrigin{q, authorize})
}
func NativePhaseRequest(ctx context.Context) bool { return ctx.Value(nativePhaseKey{}) != nil }
func CarryNativePhaseOrigin(origin, destination context.Context) context.Context {
	if value := origin.Value(nativePhaseKey{}); value != nil {
		return context.WithValue(destination, nativePhaseKey{}, value)
	}
	return destination
}

// NativePhaseRefusal carries only a fixed public classification, never target IDs
// or host errors. Earlier committed phases are not rolled back by this refusal.
type NativePhaseRefusal struct{ Code string }

func (e *NativePhaseRefusal) Error() string {
	switch e.Code {
	case "unauthenticated":
		return "Authentication required"
	case "forbidden":
		return "Access denied"
	case "not_found":
		return "Not found"
	case "native_local_operation_unsupported":
		return "Native local operation unsupported"
	default:
		return "Native storage admission unavailable"
	}
}
func IsNativePhaseRefusal(err error) bool {
	var refusal *NativePhaseRefusal
	return errors.As(err, &refusal)
}

// RequireNativePhase authorizes the complete selected set before classifying it.
// Background/native enrichment has no request origin and keeps its host behavior.
func RequireNativePhase(ctx context.Context, q NativePhaseQuery, selected NativePhaseTargets) error {
	origin, request := ctx.Value(nativePhaseKey{}).(nativePhaseOrigin)
	if !request {
		return nil
	}
	unavailable := func() error { return &NativePhaseRefusal{Code: "native_storage_unavailable"} }
	if q == nil {
		q = origin.query
	}
	if q == nil || origin.authorize == nil {
		return unavailable()
	}
	targets := NativePhaseTargets{ContentIDs: slices.Clone(selected.ContentIDs), FileIDs: slices.Clone(selected.FileIDs), LibraryIDs: slices.Clone(selected.LibraryIDs)}
	for _, proposed := range selected.Prospective {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1 UNION ALL SELECT 1 FROM episodes WHERE content_id=$1 UNION ALL SELECT 1 FROM seasons WHERE content_id=$1 UNION ALL SELECT 1 FROM media_extras WHERE content_id=$1)`, proposed.ContentID).Scan(&exists); err != nil {
			return unavailable()
		}
		if exists {
			targets.ContentIDs = append(targets.ContentIDs, proposed.ContentID)
			continue
		}
		if len(proposed.SourceIDs)+len(proposed.ParentIDs)+len(proposed.LibraryIDs) == 0 {
			return &NativePhaseRefusal{Code: "forbidden"}
		}
		targets.ContentIDs = append(targets.ContentIDs, proposed.SourceIDs...)
		targets.ContentIDs = append(targets.ContentIDs, proposed.ParentIDs...)
		targets.LibraryIDs = append(targets.LibraryIDs, proposed.LibraryIDs...)
		proposed.SourceIDs, proposed.ParentIDs, proposed.LibraryIDs = slices.Clone(proposed.SourceIDs), slices.Clone(proposed.ParentIDs), slices.Clone(proposed.LibraryIDs)
		targets.Prospective = append(targets.Prospective, proposed)
	}
	// Stored file ownership, including an extra/episode parent and its library, is
	// supplied to the host before any native classification; incoming tuples alone
	// cannot authorize a different stored winner.
	if len(targets.FileIDs) > 0 {
		rows, err := q.Query(ctx, `SELECT COALESCE(content_id,''), COALESCE(episode_id,''), COALESCE(extra_id,''), media_folder_id FROM media_files WHERE id=ANY($1)`, targets.FileIDs)
		if err != nil {
			return unavailable()
		}
		for rows.Next() {
			var item, episode, extra string
			var folder int
			if err := rows.Scan(&item, &episode, &extra, &folder); err != nil {
				rows.Close()
				return unavailable()
			}
			for _, id := range []string{item, episode, extra} {
				if id != "" {
					targets.ContentIDs = append(targets.ContentIDs, id)
				}
			}
			targets.LibraryIDs = append(targets.LibraryIDs, folder)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return unavailable()
		}
	}
	if err := origin.authorize(ctx, q, targets); err != nil {
		return err
	}
	if !NativeStorageSchemaReady(ctx, q) {
		return unavailable()
	}
	native := false
	for _, id := range targets.ContentIDs {
		if id == "" {
			continue
		}
		var class string
		if err := q.QueryRow(ctx, "SELECT public.bloem_native_item_class($1)", id).Scan(&class); err != nil {
			return unavailable()
		}
		if class == "native" {
			native = true
			continue
		}
		if class != "local" {
			return unavailable()
		}
	}
	for _, id := range targets.LibraryIDs {
		var class string
		if err := q.QueryRow(ctx, "SELECT public.bloem_native_folder_class($1)", id).Scan(&class); err != nil {
			return unavailable()
		}
		if class == "native" {
			native = true
			continue
		}
		if class != "local" {
			return unavailable()
		}
	}
	if native {
		return &NativePhaseRefusal{Code: "native_local_operation_unsupported"}
	}
	return nil
}
