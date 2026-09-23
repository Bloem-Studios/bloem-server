package access

// Bloem tenant-group resolver coverage moved out of Silo's resolver_test.go.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func TestResolverLoadsProfileBeforeResolvingTenantGroup(t *testing.T) {
	organizationID := uuid.New()
	events := []string{}
	store := orderedResolverStore{
		stubStore: stubStore{profile: &userstore.Profile{
			ID:             "prof-1",
			OrganizationID: organizationID.String(),
		}},
		events: &events,
	}
	groups := &stubGroupProvider{
		group:  &GroupPolicy{PlaybackAllowed: true, TranscodeAllowed: true, DownloadAllowed: true, DownloadTranscodeAllowed: true, RequestsAllowed: true},
		events: &events,
	}
	resolver := NewResolver(
		stubUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}},
		stubStoreProvider{store: store},
		nil,
		groups,
	)
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: organizationID,
		AccountID:      1,
	})

	if _, err := resolver.Resolve(ctx, ResolveInput{UserID: 1, ProfileID: "prof-1"}); err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"profile", "group"}) {
		t.Fatalf("resolution order = %#v, want profile then group", events)
	}
	wantSubject := GroupSubject{OrganizationID: organizationID, AccountID: 1, ProfileID: "prof-1"}
	if groups.subject != wantSubject {
		t.Fatalf("ResolvePolicy subject = %#v, want %#v", groups.subject, wantSubject)
	}
}

func TestResolverBloemWithoutProfileFailsClosedAtGroupResolution(t *testing.T) {
	organizationID := uuid.New()
	groups := &stubGroupProvider{err: ErrGroupNotFound}
	resolver := NewResolver(
		stubUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}},
		stubStoreProvider{store: stubStore{}},
		nil,
		groups,
	)
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: organizationID,
		AccountID:      1,
	})

	if _, err := resolver.Resolve(ctx, ResolveInput{UserID: 1}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrGroupNotFound", err)
	}
	wantSubject := GroupSubject{OrganizationID: organizationID, AccountID: 1}
	if groups.subject != wantSubject {
		t.Fatalf("ResolvePolicy subject = %#v, want %#v", groups.subject, wantSubject)
	}
}

type orderedResolverStore struct {
	stubStore
	events *[]string
}

func (s orderedResolverStore) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	*s.events = append(*s.events, "profile")
	return s.stubStore.GetProfile(ctx, id)
}
