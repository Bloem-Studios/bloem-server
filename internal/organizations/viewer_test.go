package organizations_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/organizations"
)

type boundaryUsers struct{ user *models.User }

func (u boundaryUsers) GetByID(context.Context, int) (*models.User, error) { return u.user, nil }

type boundarySource func(context.Context, int64) (organizations.ViewerBoundary, error)

func (f boundarySource) ResolveViewerBoundary(ctx context.Context, id int64) (organizations.ViewerBoundary, error) {
	return f(ctx, id)
}

type innerViewer func(context.Context, access.ResolveInput) (access.Scope, error)

func (f innerViewer) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	return f(ctx, in)
}

func TestViewerBoundaryOnlyNarrowsSiloPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		ceiling, allowed, want []int
	}{
		{"unrestricted", []int{3, 1, 3}, nil, []int{1, 3}},
		{"restricted", []int{1, 3}, []int{3, 9}, []int{3}},
		{"denied", []int{1, 3}, []int{}, []int{}},
		{"no grants", []int{}, nil, []int{}},
		{"missing ceiling", nil, nil, []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := boundarySource(func(_ context.Context, id int64) (organizations.ViewerBoundary, error) {
				if id != 7 {
					t.Fatalf("organization taken from outside account: %d", id)
				}
				return organizations.ViewerBoundary{OrganizationID: 7, AccessRevision: 2, AllowedLibraryIDs: tc.ceiling}, nil
			})
			inner := innerViewer(func(context.Context, access.ResolveInput) (access.Scope, error) {
				return access.Scope{UserID: 4, ProfileID: "profile", AllowedLibraryIDs: tc.allowed, MaxContentRating: "PG", ProfileVerified: true}, nil
			})
			resolver := organizations.NewViewerResolver(boundaryUsers{&models.User{ID: 4, OrganizationID: 7}}, source, inner)
			scope, err := resolver.Resolve(t.Context(), access.ResolveInput{UserID: 4, ProfileID: "profile"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(scope.AllowedLibraryIDs, tc.want) || !scope.LibrariesRestricted {
				t.Fatalf("got %+v, want libraries %v", scope, tc.want)
			}
			if scope.MaxContentRating != "PG" || !scope.ProfileVerified || scope.ProfileID != "profile" {
				t.Fatal("organization boundary changed existing profile policy")
			}
		})
	}
}

func TestViewerBoundaryFailsClosed(t *testing.T) {
	called := false
	source := boundarySource(func(context.Context, int64) (organizations.ViewerBoundary, error) {
		return organizations.ViewerBoundary{}, errors.New("organization unavailable")
	})
	inner := innerViewer(func(context.Context, access.ResolveInput) (access.Scope, error) {
		called = true
		return access.Scope{}, nil
	})
	resolver := organizations.NewViewerResolver(boundaryUsers{&models.User{ID: 4, OrganizationID: 7}}, source, inner)
	if _, err := resolver.Resolve(t.Context(), access.ResolveInput{UserID: 4}); err == nil || called {
		t.Fatal("organization failure did not stop scope resolution")
	}
}
