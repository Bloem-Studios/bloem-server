package api

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	mw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// The companion transports intent; the ordinary native collection services own
// validation. The owned store adapter keeps effects and receipts in one commit.
func (s *managedCalendarSource) MutateLibrary(ctx context.Context, p managedtracking.Profile, tx pgx.Tx, in *library.LibraryMutation) (*library.LibraryMutationReceipt, error) {
	if in == nil {
		return nil, managedtracking.ErrSnapshot
	}
	if !strings.HasPrefix(in.Operation, "copy.") && !strings.HasPrefix(in.Operation, "book.") && (in.Copy != nil || in.BookState != nil) {
		return nil, managedtracking.ErrSnapshot
	}
	scope, err := s.scope(ctx, p)
	if err != nil {
		return nil, err
	}
	if s.pool == nil || s.stores == nil || tx == nil {
		return nil, managedtracking.ErrAuthority
	}
	ctx = access.SetScope(mw.SetProfileID(mw.SetClaims(ctx, &auth.Claims{UserID: scope.UserID}), p.ProfileID), scope)
	if strings.HasPrefix(in.Operation, "book.") {
		return s.mutateManagedBook(ctx, tx, in, scope)
	}
	if strings.HasPrefix(in.Operation, "copy.") {
		return s.mutateManagedCopy(ctx, tx, in, scope)
	}
	provider := pgstore.NewBloemCollectionTransaction(s.pool, tx, scope.UserID)
	handler := handlers.NewCollectionHandler(provider)
	handler.Executor = &catalog.QueryExecutor{Pool: s.pool}
	out := &library.LibraryMutationReceipt{EventId: in.EventId, Status: "applied"}
	var id string
	if in.Operation == "collection.create" {
		if in.TargetId != "" || in.ExpectedRevision != 0 || len(in.Items) != 0 || strings.TrimSpace(in.Name) == "" {
			return nil, managedtracking.ErrSnapshot
		}
		view, e := handler.CreatePersonalCollection(ctx, handlers.PersonalCollectionCreateCommand{UserID: scope.UserID, ProfileID: p.ProfileID, Request: handlers.PersonalCollectionCreateRequest{Name: in.Name, Description: in.Description, CollectionType: "manual"}})
		if e != nil {
			return nil, managedLibraryMutationError(e)
		}
		id = view.ID
	} else {
		var ok bool
		prefix, target, ok := strings.Cut(in.TargetId, ":")
		if !ok || prefix != "personal" || target == "" {
			return nil, managedtracking.ErrAuthority
		}
		id = target
		var owner, kind string
		err = tx.QueryRow(ctx, `SELECT creator_profile_id,collection_type FROM user_personal_collections WHERE user_id=$1 AND id=$2 AND native FOR UPDATE`, scope.UserID, id).Scan(&owner, &kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, managedtracking.ErrAuthority
		}
		if err != nil {
			return nil, err
		}
		if owner != p.ProfileID || kind != "manual" {
			return nil, managedtracking.ErrAuthority
		}
		var revision uint64
		if err = tx.QueryRow(ctx, `SELECT revision FROM user_collection_revisions WHERE user_id=$1 AND collection_id=$2`, scope.UserID, id).Scan(&revision); err != nil {
			return nil, err
		}
		if in.ExpectedRevision == 0 || revision != in.ExpectedRevision {
			return nil, managedtracking.ErrConflict
		}
		switch in.Operation {
		case "collection.update":
			if len(in.Items) != 0 || strings.TrimSpace(in.Name) == "" {
				return nil, managedtracking.ErrSnapshot
			}
			_, err = handler.UpdatePersonalCollection(ctx, handlers.PersonalCollectionUpdateCommand{UserID: scope.UserID, ProfileID: p.ProfileID, CollectionID: id, Request: handlers.PersonalCollectionUpdateRequest{Name: &in.Name, Description: &in.Description}})
		case "collection.add", "collection.remove":
			if len(in.Items) != 1 || in.Items[0] == nil || in.Items[0].ContentId == "" {
				return nil, managedtracking.ErrSnapshot
			}
			if in.Operation == "collection.add" {
				var position int
				if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(position)+1,0) FROM user_personal_collection_items WHERE user_id=$1 AND collection_id=$2`, scope.UserID, id).Scan(&position); err != nil {
					return nil, err
				}
				err = handler.AddPersonalCollectionItem(ctx, scope.UserID, p.ProfileID, id, in.Items[0].ContentId, position)
			} else {
				err = handler.RemovePersonalCollectionItem(ctx, scope.UserID, p.ProfileID, id, in.Items[0].ContentId)
			}
		case "collection.delete":
			if len(in.Items) != 0 {
				return nil, managedtracking.ErrSnapshot
			}
			err = handler.DeletePersonalCollection(ctx, scope.UserID, p.ProfileID, id)
			out.Status = "removed"
		default:
			return nil, managedtracking.ErrSnapshot
		}
		if err != nil {
			return nil, managedLibraryMutationError(err)
		}
	}
	out.TargetId = "personal:" + id
	if err = tx.QueryRow(ctx, `SELECT revision FROM user_collection_revisions WHERE user_id=$1 AND collection_id=$2`, scope.UserID, id).Scan(&out.Revision); err != nil {
		return nil, err
	}
	return out, nil
}

func managedLibraryMutationError(err error) error {
	var api *handlers.APIError
	if errors.As(err, &api) {
		switch api.Status {
		case 403, 404:
			return managedtracking.ErrAuthority
		case 409:
			return managedtracking.ErrConflict
		case 400, 422:
			return managedtracking.ErrSnapshot
		}
	}
	return err
}
