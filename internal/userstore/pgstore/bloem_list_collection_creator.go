package pgstore

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// BloemListCollectionCreator lets the native create service retain its name,
// query, sort and sharing validation while the creation receipt owns the commit.
// Only the newly created collection receives this source binding marker.
type BloemListCollectionCreator struct {
	*BloemCollectionTransaction
	binding string
}

func NewBloemListCollectionCreator(pool *pgxpool.Pool, tx pgx.Tx, user int, binding string) *BloemListCollectionCreator {
	return &BloemListCollectionCreator{NewBloemCollectionTransaction(pool, tx, user), binding}
}
func (p *BloemListCollectionCreator) ForUser(ctx context.Context, user int) (userstore.UserStore, error) {
	if _, err := uuid.Parse(p.binding); err != nil {
		return nil, errors.New("invalid list binding")
	}
	store, err := p.BloemCollectionTransaction.ForUser(ctx, user)
	if err != nil {
		return nil, err
	}
	base, ok := store.(*bloemCollectionStore)
	if !ok {
		return nil, errors.New("invalid collection transaction store")
	}
	return &bloemListCreateStore{bloemCollectionStore: base, binding: p.binding}, nil
}

type bloemListCreateStore struct {
	*bloemCollectionStore
	binding string
}

func (s *bloemListCreateStore) CreateCollection(ctx context.Context, in userstore.CreateCollectionInput) (*userstore.Collection, error) {
	if in.CollectionType != "synced" || in.SourceConfig != "" || in.IncludeInServerCollections {
		return nil, errors.New("unsupported list collection definition")
	}
	shared := in.IsShared
	in.IsShared = false
	in.CollectionType = "manual"
	c, err := s.bloemCollectionStore.CreateCollection(ctx, in)
	if err != nil {
		return nil, err
	}
	if _, err = s.tx.Exec(ctx, `UPDATE user_personal_collections SET collection_type='synced',is_shared=$3,source_config=jsonb_build_object('bloem_list_binding',$4::text) WHERE user_id=$1 AND id=$2 AND native`, s.user, c.ID, shared, s.binding); err != nil {
		return nil, err
	}
	return s.GetCollection(ctx, c.ID)
}
