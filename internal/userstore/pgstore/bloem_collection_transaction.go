package pgstore

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// BloemCollectionTransaction binds the companion's narrow manual-collection
// commands to the transaction that owns their delivery receipt. Native handlers
// still own validation and profile ownership. Other commands are not bridged.
type BloemCollectionTransaction struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
	user int
}

func NewBloemCollectionTransaction(pool *pgxpool.Pool, tx pgx.Tx, user int) *BloemCollectionTransaction {
	return &BloemCollectionTransaction{pool, tx, user}
}
func (p *BloemCollectionTransaction) Close() error { return nil }
func (p *BloemCollectionTransaction) ForUser(_ context.Context, user int) (userstore.UserStore, error) {
	if p == nil || p.tx == nil || user != p.user {
		return nil, errors.New("collection transaction account mismatch")
	}
	return &bloemCollectionStore{UserStore: newStore(p.pool, user), tx: p.tx, user: user}, nil
}

type bloemCollectionStore struct {
	userstore.UserStore
	tx   pgx.Tx
	user int
}

func (s *bloemCollectionStore) CollectionFeatures() userstore.CollectionFeatures {
	return userstore.CollectionFeatures{Description: true}
}
func (s *bloemCollectionStore) GetCollection(ctx context.Context, id string) (*userstore.Collection, error) {
	c, err := scanCollection(s.tx.QueryRow(ctx, `SELECT `+collectionSelectColumns+` FROM user_personal_collections WHERE user_id=$1 AND id=$2 AND native FOR UPDATE`, s.user, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, userstore.ErrCollectionNotFound
	}
	return c, err
}
func (s *bloemCollectionStore) CreateCollection(ctx context.Context, in userstore.CreateCollectionInput) (*userstore.Collection, error) {
	if in.CollectionType != "manual" && in.CollectionType != "" || in.IsShared || in.SourceURL != "" || in.PosterURL != "" || in.SyncSchedule != nil || in.IncludeInServerCollections {
		return nil, errors.New("unsupported companion collection definition")
	}
	id := uuid.NewString()
	_, err := s.tx.Exec(ctx, `INSERT INTO user_personal_collections(id,user_id,profile_id,creator_profile_id,name,description,collection_type,query_definition,sort_config,source_config,sort_order,native)
 VALUES($1,$2,$3,$3,$4,$5,'manual',$6,$7,'{}',COALESCE((SELECT MAX(sort_order)+1 FROM user_personal_collections WHERE user_id=$2 AND creator_profile_id=$3 AND native),0),true)`, id, s.user, in.CreatorProfileID, in.Name, in.Description, nonemptyCollectionJSON(in.QueryDefinition), nonemptyCollectionJSON(in.SortConfig))
	if err != nil {
		return nil, err
	}
	return s.GetCollection(ctx, id)
}
func nonemptyCollectionJSON(value string) string {
	if value == "" {
		return "{}"
	}
	return value
}
func (s *bloemCollectionStore) UpdateCollection(ctx context.Context, in userstore.UpdateCollectionInput) error {
	// Transport admits only name/description patches. The native handler builds
	// this input; unsupported fields never fall through to a separate transaction.
	if in.IsShared != nil || in.QueryDefinition != nil || in.SortConfig != nil || in.SourceURL != nil || in.SourceConfig != nil || in.PosterURL != nil || in.PosterThumbhash != nil || in.GroupID != nil || in.IncludeInServerCollections != nil {
		return errors.New("unsupported companion collection patch")
	}
	_, err := s.tx.Exec(ctx, `UPDATE user_personal_collections SET name=COALESCE($3,name),description=COALESCE($4,description),updated_at=clock_timestamp() WHERE user_id=$1 AND id=$2 AND native`, s.user, in.ID, in.Name, in.Description)
	return err
}
func (s *bloemCollectionStore) DeleteCollection(ctx context.Context, id string) error {
	if _, err := s.tx.Exec(ctx, `DELETE FROM user_personal_collection_items WHERE user_id=$1 AND collection_id=$2`, s.user, id); err != nil {
		return err
	}
	_, err := s.tx.Exec(ctx, `DELETE FROM user_personal_collections WHERE user_id=$1 AND id=$2 AND native`, s.user, id)
	return err
}
func (s *bloemCollectionStore) AddCollectionItem(ctx context.Context, id, item string, position int) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO user_personal_collection_items(user_id,collection_id,media_item_id,position,added_at) VALUES($1,$2,$3,$4,clock_timestamp()) ON CONFLICT(user_id,collection_id,media_item_id,sub_item_id) DO NOTHING`, s.user, id, item, position)
	return err
}
func (s *bloemCollectionStore) RemoveCollectionItem(ctx context.Context, id, item string) error {
	_, err := s.tx.Exec(ctx, `DELETE FROM user_personal_collection_items WHERE user_id=$1 AND collection_id=$2 AND media_item_id=$3 AND sub_item_id=''`, s.user, id, item)
	return err
}
