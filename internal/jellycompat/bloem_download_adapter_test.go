package jellycompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func TestProfileDownloadServerRouteUsesProfilePolicy(t *testing.T) {
	for _, tc := range []struct {
		profile string
		status  int
	}{
		{"strict", http.StatusForbidden}, {"sibling", http.StatusOK}, {"", http.StatusOK},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(tc.profile+"/"+method, func(t *testing.T) {
				svc, files, tenants := newBloemCompatDownloadFixture(t)
				cfg, err := config.LoadFromDB(map[string]string{})
				if err != nil {
					t.Fatal(err)
				}
				store := NewSessionStore(time.Hour, nil)
				if err := store.Put(Session{Token: compatSocketToken, StreamAppUserID: 1, ProfileID: tc.profile}); err != nil {
					t.Fatal(err)
				}
				codec := NewResourceIDCodec()
				server := NewServerWithDependencies(Dependencies{
					Config: cfg, SessionStore: store, IDCodec: codec,
					ContentService: &stubContentService{detail: &upstreamItemDetail{ContentID: "movie-1", Type: "movie", Versions: []catalog.FileVersion{{FileID: 42}}}},
					Downloads:      NewProfileDownloadServer(svc, tenants.resolver()),
				})
				rr := httptest.NewRecorder()
				server.Handler().ServeHTTP(rr, httptest.NewRequest(method, "/Items/"+codec.EncodeStringID(EncodedIDItem, "movie-1")+"/Download?api_key="+compatSocketToken, nil))
				if rr.Code != tc.status {
					t.Fatalf("status=%d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
				}
				if tc.status == http.StatusForbidden {
					if files.calls != 0 || rr.Header().Get("Content-Disposition") != "" {
						t.Fatal("denied profile reached the file")
					}
				} else if method == http.MethodGet && rr.Body.String() != "original media" {
					t.Fatalf("download bytes = %q", rr.Body.String())
				}
			})
		}
	}
}

func TestProfileDownloadServerRejectsInvalidSubjectsBeforeFileIO(t *testing.T) {
	for _, tc := range []struct {
		name, profile  string
		missingSession bool
		user           int
		mutate         func(*bloemDownloadTenants)
	}{
		{name: "missing session", user: 1, missingSession: true},
		{name: "account mismatch", profile: "sibling", user: 2},
		{name: "nonpositive account", profile: "sibling"},
		{name: "foreign profile", profile: "foreign", user: 1},
		{name: "unknown profile", profile: "unknown", user: 1},
		{name: "tenant unavailable", profile: "sibling", user: 1, mutate: func(s *bloemDownloadTenants) { s.err = tenancy.ErrTenantUnavailable }},
		{name: "organization suspended", profile: "sibling", user: 1, mutate: func(s *bloemDownloadTenants) { s.organization.Status = tenancy.OrganizationSuspended }},
		{name: "membership suspended", profile: "sibling", user: 1, mutate: func(s *bloemDownloadTenants) { s.membership.Status = tenancy.MembershipSuspended }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, files, tenants := newBloemCompatDownloadFixture(t)
			if tc.mutate != nil {
				tc.mutate(tenants)
			}
			ctx := t.Context()
			if !tc.missingSession {
				ctx = context.WithValue(ctx, compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: tc.profile})
			}
			err := NewProfileDownloadServer(svc, tenants.resolver()).ServeDirect(ctx, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), tc.user, 42, "", catalog.AccessFilter{})
			if !errors.Is(err, downloads.ErrDownloadNotAllowed) {
				t.Fatalf("error=%v, want ErrDownloadNotAllowed", err)
			}
			if files.calls != 0 {
				t.Fatal("invalid subject reached file IO")
			}
			if (tc.missingSession || tc.user != 1) && tenants.calls != 0 {
				t.Fatal("invalid session reached tenant lookup")
			}
		})
	}
}

func TestProfileDownloadServerPreservesServeContextAndSharedPolicy(t *testing.T) {
	svc, files, tenants := newBloemCompatDownloadFixture(t)
	adapter := NewProfileDownloadServer(svc, tenants.resolver())
	ctx := context.WithValue(t.Context(), compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "sibling"})
	// A stale caller tenant must be replaced by the subject's hosted organization.
	ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: 2, OrganizationID: uuid.New()})
	observed := 0
	ctx = downloads.WithServeAuthorized(ctx, func(target downloads.FileTarget) {
		observed++
		if target.MediaFileID != 42 {
			t.Errorf("observed file=%d", target.MediaFileID)
		}
	})
	ctx = downloads.WithServeEntityTag(ctx, func(*os.File, os.FileInfo) string { return `"preserved"` })
	for _, tc := range []struct {
		rangeTag, wantBody string
		status             int
	}{
		{`"preserved"`, "media", http.StatusPartialContent}, {`"stale"`, "original media", http.StatusOK},
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Range", "bytes=9-")
		req.Header.Set("If-Range", tc.rangeTag)
		if err := adapter.ServeDirect(ctx, rr, req, 1, 42, "", catalog.AccessFilter{}); err != nil {
			t.Fatal(err)
		}
		if rr.Code != tc.status || rr.Body.String() != tc.wantBody || rr.Header().Get("ETag") != `"preserved"` {
			t.Fatalf("response=%d %q ETag=%q", rr.Code, rr.Body.String(), rr.Header().Get("ETag"))
		}
		if files.tenant.OrganizationID != tenants.organization.ID || files.tenant.AccountID != 1 {
			t.Fatalf("file tenant=%+v", files.tenant)
		}
	}
	if observed != 2 {
		t.Fatalf("authorized callbacks=%d, want 2", observed)
	}
	// Revocation on the native service must immediately affect its compat adapter.
	svc.SetGroupPolicyProvider(bloemCompatDownloadGroups{organization: tenants.organization.ID, denyAll: true})
	err := adapter.ServeDirect(ctx, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), 1, 42, "", catalog.AccessFilter{})
	if !errors.Is(err, downloads.ErrDownloadNotAllowed) || files.calls != 2 || observed != 2 {
		t.Fatalf("revoked download error=%v files=%d callbacks=%d", err, files.calls, observed)
	}
}

func TestProfileDownloadServerNilDependencies(t *testing.T) {
	svc, _, tenants := newBloemCompatDownloadFixture(t)
	if NewProfileDownloadServer(nil, tenants.resolver()) != nil || NewProfileDownloadServer(svc, nil) != nil {
		t.Fatal("missing dependencies must preserve a nil DownloadServer")
	}
}

func TestProfileDownloadServerCustomCeilingPreservesCatalogDenials(t *testing.T) {
	svc, files, tenants := newBloemCompatDownloadFixture(t)
	file := files.testDownloadFiles[42]
	file.Resolution = "2160p"
	engine, err := policy.NewEngineWithCustom(t.Context(), map[string]policy.ActiveSource{
		policy.DomainAction: {DocumentID: 1, VersionID: 1, Source: "package silo_custom.action\nimport rego.v1\noverride(_, _) := {\"allowed\": true, \"quality_ceiling\": \"1080p\"}\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if skipped := engine.SkippedSources(); len(skipped) != 0 {
		t.Fatalf("override skipped: %+v", skipped)
	}
	svc.SetActionDecider(policy.NewPDP(engine))
	adapter := NewProfileDownloadServer(svc, tenants.resolver())
	ctx := context.WithValue(t.Context(), compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "sibling"})
	for _, tc := range []struct {
		name, resolution string
		filter           catalog.AccessFilter
		want             error
	}{
		{"visible original above action ceiling", "2160p", catalog.AccessFilter{}, downloads.ErrDownloadNotAllowed},
		{"original above profile access ceiling", "2160p", catalog.AccessFilter{MaxPlaybackQuality: "1080p"}, catalog.ErrItemNotFound},
		{"compliant original", "1080p", catalog.AccessFilter{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file.Resolution = tc.resolution
			rr := httptest.NewRecorder()
			err := adapter.ServeDirect(ctx, rr, httptest.NewRequest(http.MethodGet, "/", nil), 1, 42, "", tc.filter)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if tc.want != nil && (rr.Body.Len() != 0 || rr.Header().Get("Content-Disposition") != "") {
				t.Fatal("refusal leaked file bytes or attachment")
			}
			if tc.want == nil && rr.Body.String() != "original media" {
				t.Fatalf("download bytes=%q", rr.Body.String())
			}
		})
	}
	if files.calls != 3 {
		t.Fatalf("file checks=%d, want 3 after policy admission", files.calls)
	}
}

func newBloemCompatDownloadFixture(t *testing.T) (*downloads.Service, *bloemDownloadFilesSpy, *bloemDownloadTenants) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "original.mkv")
	if err := os.WriteFile(path, []byte("original media"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := 1
	organization := uuid.MustParse("10000000-0000-0000-0000-000000000010")
	tenants := &bloemDownloadTenants{
		organization: tenancy.Organization{ID: organization, Status: tenancy.OrganizationActive, OwnerAccountID: &owner, PolicyRevision: 1},
		membership:   tenancy.Membership{ID: uuid.New(), OrganizationID: organization, AccountID: 1, Status: tenancy.MembershipActive, SecurityRevision: 1},
	}
	files := &bloemDownloadFilesSpy{testDownloadFiles: testDownloadFiles{42: {ID: 42, ContentID: "movie-1", FilePath: path, Resolution: "1080p"}}}
	cfg := config.DownloadConfig{Enabled: true}
	svc := downloads.NewService(nil, nil, nil, files, nil, nil, testDownloadUsers{user: &models.User{ID: 1, Role: models.RoleUser}}, testDownloadAccess{}, nil, &cfg)
	svc.SetGroupPolicyProvider(bloemCompatDownloadGroups{organization: organization})
	return svc, files, tenants
}

type bloemDownloadFilesSpy struct {
	testDownloadFiles
	calls  int
	tenant tenancy.Context
}

func (s *bloemDownloadFilesSpy) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	s.calls++
	s.tenant, _ = tenancy.FromContext(ctx)
	return s.testDownloadFiles.GetByID(ctx, id)
}

type bloemCompatDownloadGroups struct {
	organization uuid.UUID
	denyAll      bool
}

func (g bloemCompatDownloadGroups) GetPolicyForUser(context.Context, int) (*access.GroupPolicy, error) {
	return nil, errors.New("profile subject required")
}
func (g bloemCompatDownloadGroups) ResolvePolicy(_ context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
	if subject.OrganizationID != g.organization || subject.AccountID != 1 {
		return nil, access.ErrGroupNotFound
	}
	return &access.GroupPolicy{DownloadAllowed: !g.denyAll && subject.ProfileID != "strict"}, nil
}

type bloemDownloadTenants struct {
	organization tenancy.Organization
	membership   tenancy.Membership
	err          error
	calls        int
}

func (s *bloemDownloadTenants) resolver() *tenancy.SubjectResolver {
	return tenancy.NewSubjectResolver(tenancy.NewResolver(s), s)
}
func (s *bloemDownloadTenants) ProfileOrganization(_ context.Context, user int, profile string) (uuid.UUID, error) {
	s.calls++
	if s.err != nil {
		return uuid.Nil, s.err
	}
	if user != 1 || (profile != "strict" && profile != "sibling") {
		return uuid.Nil, tenancy.ErrTenantNotFoundOrHidden
	}
	return s.organization.ID, nil
}
func (s *bloemDownloadTenants) AccountOrganization(context.Context, int) (uuid.UUID, error) {
	s.calls++
	return s.organization.ID, s.err
}
func (s *bloemDownloadTenants) DefaultOrganization(context.Context) (tenancy.Organization, error) {
	return tenancy.Organization{}, errors.New("hosted subject must not fall back to default organization")
}
func (s *bloemDownloadTenants) GetOrganization(_ context.Context, id uuid.UUID) (tenancy.Organization, error) {
	if id != s.organization.ID {
		return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
	}
	return s.organization, s.err
}
func (s *bloemDownloadTenants) GetMembership(_ context.Context, user int, organization uuid.UUID) (tenancy.Membership, error) {
	if user != 1 || organization != s.organization.ID {
		return tenancy.Membership{}, tenancy.ErrMembershipNotFound
	}
	return s.membership, s.err
}
