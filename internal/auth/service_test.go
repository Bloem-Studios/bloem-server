package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

var errOwnershipActivation = errors.New("ownership activation failed")

type recordingOwnershipBootstrapper struct {
	accountIDs []int
	err        error
	onActivate func()
}

func (b *recordingOwnershipBootstrapper) ActivateInitialOwnership(_ context.Context, accountID int) error {
	if b.onActivate != nil {
		b.onActivate()
	}
	b.accountIDs = append(b.accountIDs, accountID)
	return b.err
}

type setupMembershipProvisioner struct {
	accountIDs  []int
	legacyRoles []string
	err         error
}

func (p *setupMembershipProvisioner) ProvisionDefaultMembership(_ context.Context, accountID int, legacyRole string) error {
	p.accountIDs = append(p.accountIDs, accountID)
	p.legacyRoles = append(p.legacyRoles, legacyRole)
	return p.err
}

type setupUserRepository struct {
	user      *models.User
	deletedID int
}

// CompareAndSwapPassword satisfies serviceUserRepository. Setup-flow tests
// never change a password, so an unexpected call is a test bug, not a path
// worth faking.
func (r *setupUserRepository) CompareAndSwapPassword(_ context.Context, _ int, _, _ string) error {
	return errors.New("setupUserRepository does not support password changes")
}

func (r *setupUserRepository) Create(_ context.Context, input models.CreateUserInput) (*models.User, error) {
	r.user = &models.User{
		ID:                   47,
		AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"),
		Username:             input.Username,
		Email:                input.Email,
		Role:                 input.Role,
		Enabled:              true,
	}
	return r.user, nil
}

func (r *setupUserRepository) Delete(_ context.Context, id int) error {
	r.deletedID = id
	return nil
}

func (r *setupUserRepository) Count(context.Context) (int, error) {
	if r.user == nil {
		return 0, nil
	}
	return 1, nil
}

func (r *setupUserRepository) GetByID(_ context.Context, id int) (*models.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, ErrNotFound
	}
	return r.user, nil
}

type setupSessions struct {
	created []models.AuthSession
}

func (s *setupSessions) Create(_ context.Context, session models.AuthSession) error {
	s.created = append(s.created, session)
	return nil
}

func (s *setupSessions) CreateProfileSessionIfCurrent(
	ctx context.Context,
	session models.AuthSession,
	_ SessionSubject,
) error {
	return s.Create(ctx, session)
}

func (*setupSessions) GetByID(context.Context, string) (*models.AuthSession, error) {
	return nil, ErrSessionNotFound
}

func (*setupSessions) ListByUser(context.Context, int) ([]*models.AuthSession, error) {
	return nil, nil
}

func (*setupSessions) Revoke(context.Context, string) error { return nil }

func (*setupSessions) ExtendExpiresAt(context.Context, string, time.Time) error { return nil }

type setupProvider struct {
	user                  *models.User
	bootstrapperActivated func() bool
	authenticateCalls     int
}

func (p *setupProvider) Authenticate(_ context.Context, _ Credentials) (*models.User, error) {
	p.authenticateCalls++
	if !p.bootstrapperActivated() {
		return nil, errors.New("login attempted before ownership activation")
	}
	return p.user, nil
}

func (*setupProvider) ValidateSession(context.Context, string) (bool, error) { return true, nil }

func newSetupInitialUserService(bootstrapper *recordingOwnershipBootstrapper) (*Service, *setupUserRepository, *setupProvider, *setupSessions) {
	users := &setupUserRepository{}
	provider := &setupProvider{
		bootstrapperActivated: func() bool { return len(bootstrapper.accountIDs) == 1 },
	}
	sessions := &setupSessions{}
	service := &Service{
		provider:  provider,
		jwt:       NewJWTService("test-secret", time.Hour, 24*time.Hour),
		sessions:  sessions,
		users:     users,
		providers: map[string]AuthProvider{"local": provider},
		defaultID: "local",
		accounts:  NewAccountProvisioner(users, nil),
	}
	service.SetOwnershipBootstrapper(bootstrapper)
	return service, users, provider, sessions
}

func TestSetupInitialUserOwnership_ActivatesCreatedAccountBeforeLogin(t *testing.T) {
	memberships := &setupMembershipProvisioner{}
	bootstrapper := &recordingOwnershipBootstrapper{}
	bootstrapper.onActivate = func() {
		if len(memberships.accountIDs) != 1 || memberships.accountIDs[0] != 47 || memberships.legacyRoles[0] != "admin" {
			t.Fatalf("membership provisioning = accounts %v roles %v, want account 47 as admin before ownership", memberships.accountIDs, memberships.legacyRoles)
		}
	}
	service, users, provider, sessions := newSetupInitialUserService(bootstrapper)
	service.SetMembershipProvisioner(memberships)
	provider.user = &models.User{ID: 47, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Username: "admin", Role: "admin", Enabled: true}

	pair, user, err := service.SetupInitialUser(
		context.Background(), "admin", "admin@example.test", "password", false, "", "browser", "127.0.0.1",
	)
	if err != nil {
		t.Fatalf("SetupInitialUser: %v", err)
	}
	if pair == nil || pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("token pair = %#v, want issued tokens", pair)
	}
	claims, err := service.jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validate setup access token: %v", err)
	}
	if claims.AccountIncarnationID != users.user.AccountIncarnationID.String() {
		t.Fatalf("setup token incarnation = %q, want %s", claims.AccountIncarnationID, users.user.AccountIncarnationID)
	}
	if user == nil || user.ID != 47 {
		t.Fatalf("user = %#v, want created account 47", user)
	}
	if len(bootstrapper.accountIDs) != 1 || bootstrapper.accountIDs[0] != 47 {
		t.Fatalf("activated accounts = %v, want [47]", bootstrapper.accountIDs)
	}
	if len(memberships.accountIDs) != 1 || memberships.accountIDs[0] != 47 || len(memberships.legacyRoles) != 1 || memberships.legacyRoles[0] != "admin" {
		t.Fatalf("membership provisioning = accounts %v roles %v, want [47] [admin]", memberships.accountIDs, memberships.legacyRoles)
	}
	if provider.authenticateCalls != 1 {
		t.Fatalf("login calls = %d, want 1 after ownership activation", provider.authenticateCalls)
	}
	if len(sessions.created) != 1 {
		t.Fatalf("created sessions = %d, want 1", len(sessions.created))
	}
}

func TestSetupInitialUserOwnership_CleansUpAndDoesNotIssueTokensWhenActivationFails(t *testing.T) {
	bootstrapper := &recordingOwnershipBootstrapper{err: errOwnershipActivation}
	service, users, provider, sessions := newSetupInitialUserService(bootstrapper)

	pair, user, err := service.SetupInitialUser(
		context.Background(), "admin", "admin@example.test", "password", false, "", "browser", "127.0.0.1",
	)
	if !errors.Is(err, errOwnershipActivation) {
		t.Fatalf("SetupInitialUser error = %v, want ownership activation error", err)
	}
	if pair != nil || user != nil {
		t.Fatalf("setup result = (%#v, %#v), want no tokens or user", pair, user)
	}
	if users.deletedID != 47 {
		t.Fatalf("deleted account = %d, want 47", users.deletedID)
	}
	if provider.authenticateCalls != 0 {
		t.Fatalf("login calls = %d, want 0", provider.authenticateCalls)
	}
	if len(sessions.created) != 0 {
		t.Fatalf("created sessions = %d, want 0", len(sessions.created))
	}
}

func TestSetupInitialUserOwnership_CleansUpAndDoesNotIssueTokensWhenMembershipProvisioningFails(t *testing.T) {
	provisionErr := errors.New("membership provisioning failed")
	bootstrapper := &recordingOwnershipBootstrapper{}
	service, users, provider, sessions := newSetupInitialUserService(bootstrapper)
	provider.user = &models.User{ID: 47, Username: "admin", Role: "admin", Enabled: true}
	service.SetMembershipProvisioner(&setupMembershipProvisioner{err: provisionErr})

	pair, user, err := service.SetupInitialUser(
		context.Background(), "admin", "admin@example.test", "password", false, "", "browser", "127.0.0.1",
	)
	if !errors.Is(err, provisionErr) {
		t.Fatalf("SetupInitialUser error = %v, want membership provisioning error", err)
	}
	if pair != nil || user != nil {
		t.Fatalf("setup result = (%#v, %#v), want no tokens or user", pair, user)
	}
	if users.deletedID != 47 {
		t.Fatalf("deleted account = %d, want 47", users.deletedID)
	}
	if len(bootstrapper.accountIDs) != 0 {
		t.Fatalf("ownership activation calls = %v, want none", bootstrapper.accountIDs)
	}
	if provider.authenticateCalls != 0 {
		t.Fatalf("login calls = %d, want 0", provider.authenticateCalls)
	}
	if len(sessions.created) != 0 {
		t.Fatalf("created sessions = %d, want 0", len(sessions.created))
	}
}
