package access

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// bloemRequestProfile loads the request's profile before Resolve reads the
// profile's access group. A profile that no longer exists answers
// ErrProfileNotFound, which clients take as a sign to go back to the profile
// picker, rather than the ErrGroupNotFound the tenant group lookup returns
// for it. Live TV access is decided from the profile in the same place.
//
// The store it returns answers Silo's own GetProfile of the same id, later in
// Resolve, from this read, so the profile is read from the database once.
func bloemRequestProfile(ctx context.Context, store userstore.UserStore, profileID string) (userstore.UserStore, *userstore.Profile, error) {
	if profileID == "" {
		return store, nil, nil
	}
	profile, err := store.GetProfile(ctx, profileID)
	if err != nil {
		return store, nil, fmt.Errorf("loading profile %s: %w", profileID, err)
	}
	if profile == nil {
		return store, nil, ErrProfileNotFound
	}
	return loadedProfileStore{UserStore: store, profile: *profile}, profile, nil
}

// loadedProfileStore is a user store that already holds one profile.
type loadedProfileStore struct {
	userstore.UserStore
	profile userstore.Profile
}

// GetProfile returns a copy of the loaded profile for its id, and reads any
// other profile from the store.
func (s loadedProfileStore) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	if id != s.profile.ID {
		return s.UserStore.GetProfile(ctx, id)
	}
	profile := s.profile
	return &profile, nil
}
