package compatapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// The administration surface reads applications by instance identity and
// decides against a revision it was shown. These tests pin both halves: the
// read carries no secret material, and every decision is refused unless it
// was taken against the application's current revision.

func TestListApplicationsReturnsTheAdministrationView(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	jellyfin := mustEnroll(t, service, "list-jellyfin")

	absEnrollment := mustCreateEnrollment(t, service, KindAudiobookshelf)
	if _, err := service.Enroll(ctx, absEnrollment.Secret, validEnrollmentRequest("list-audiobookshelf")); err != nil {
		t.Fatalf("Enroll audiobookshelf: %v", err)
	}

	applications, err := service.ListApplications(ctx)
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	if len(applications) != 2 {
		t.Fatalf("listed %d applications, want 2", len(applications))
	}
	// Ordered by kind then instance so the surface is stable between reads.
	if applications[0].Kind != KindAudiobookshelf || applications[1].Kind != KindJellyfin {
		t.Fatalf("kinds = %q, %q, want audiobookshelf then jellyfin", applications[0].Kind, applications[1].Kind)
	}

	var listed Application
	for _, application := range applications {
		if application.InstanceID == "list-jellyfin" {
			listed = application
		}
	}
	switch {
	case listed.ID != jellyfin.ApplicationID:
		t.Fatalf("application id = %q, want %q", listed.ID, jellyfin.ApplicationID)
	case listed.Version != "1.2.3" || listed.ImageDigest != "sha256:0123456789abcdef":
		t.Fatalf("application = %#v, want the enrolled version and image digest", listed)
	case listed.APIRangeMin != 1 || listed.APIRangeMax != 1:
		t.Fatalf("api range = [%d,%d], want [1,1]", listed.APIRangeMin, listed.APIRangeMax)
	case len(listed.Capabilities) != 2:
		t.Fatalf("capabilities = %v, want the two granted capabilities", listed.Capabilities)
	case !listed.Enabled || listed.RevokedAt != nil || listed.Health != HealthUnknown:
		t.Fatalf("application = %#v, want an enabled, unrevoked, unknown-health record", listed)
	case listed.Revision < 1:
		t.Fatalf("revision = %d, want a positive revision", listed.Revision)
	case listed.CreatedAt.IsZero() || listed.UpdatedAt.IsZero():
		t.Fatalf("application = %#v, want both timestamps populated", listed)
	}

	// The administration view is a trust report, not a credential store. No
	// listed field may carry secret material.
	credentialSecret := jellyfin.Secret
	for _, application := range applications {
		for _, field := range []string{application.ID, application.InstanceID, application.Version, application.ImageDigest, application.TLSFingerprint} {
			if credentialSecret != "" && strings.Contains(field, credentialSecret) {
				t.Fatalf("administration view leaked credential material in %q", field)
			}
			if strings.HasPrefix(field, serviceCredentialPrefix) || strings.HasPrefix(field, enrollmentSecretPrefix) {
				t.Fatalf("administration view leaked a secret-shaped value: %q", field)
			}
		}
	}
}

func TestListApplicationsIsEmptyBeforeAnyEnrollment(t *testing.T) {
	service := newCompatAppService(t)
	applications, err := service.ListApplications(context.Background())
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	if len(applications) != 0 {
		t.Fatalf("listed %d applications, want none", len(applications))
	}
}

func TestSetEnabledByInstanceRefusesAStaleRevision(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "guarded-enable")

	current := mustFindApplication(t, service, "guarded-enable")
	disabled, err := service.SetEnabledByInstance(ctx, "guarded-enable", false, current.Revision)
	if err != nil {
		t.Fatalf("SetEnabledByInstance: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("application still enabled after a disable")
	}
	if disabled.Revision != current.Revision+1 {
		t.Fatalf("revision = %d, want %d", disabled.Revision, current.Revision+1)
	}

	// The same decision replayed from the page the administrator was looking
	// at must be refused, and must say what to retry against.
	_, err = service.SetEnabledByInstance(ctx, "guarded-enable", true, current.Revision)
	var mismatch *RevisionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("stale enable error = %v, want a RevisionMismatchError", err)
	}
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale enable error = %v, want it to match ErrRevisionMismatch", err)
	}
	if mismatch.Current != disabled.Revision || mismatch.Expected != current.Revision {
		t.Fatalf("mismatch = %#v, want current %d expected %d", mismatch, disabled.Revision, current.Revision)
	}
	if again := mustFindApplication(t, service, "guarded-enable"); again.Enabled || again.Revision != disabled.Revision {
		t.Fatalf("refused decision still changed the application: %#v", again)
	}
}

func TestSetEnabledByInstanceAcceptsANoOpTransition(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "noop-enable")
	current := mustFindApplication(t, service, "noop-enable")

	// Enabling an already-enabled application decided nothing, so it must
	// neither fail nor move the revision out from under another
	// administrator.
	application, err := service.SetEnabledByInstance(ctx, "noop-enable", true, current.Revision)
	if err != nil {
		t.Fatalf("SetEnabledByInstance no-op: %v", err)
	}
	if application.Revision != current.Revision {
		t.Fatalf("revision = %d, want it to stay %d", application.Revision, current.Revision)
	}
	if names := auditEventNames(t, service, current.ID); countAuditEvents(names, "enabled") != 0 {
		t.Fatalf("audit events = %v, want no enablement record for a no-op", names)
	}
}

func TestConcurrentSetEnabledByInstanceAdmitsExactlyOne(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "raced-enable")
	current := mustFindApplication(t, service, "raced-enable")

	// Two administrators, one page, one revision: the row lock serializes
	// them and the loser is told the revision moved.
	var wait sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i := range results {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			<-start
			_, err := service.SetEnabledByInstance(ctx, "raced-enable", false, current.Revision)
			results[slot] = err
		}(i)
	}
	close(start)
	wait.Wait()

	successes, mismatches := 0, 0
	for _, err := range results {
		var mismatch *RevisionMismatchError
		switch {
		case err == nil:
			successes++
		case errors.As(err, &mismatch):
			mismatches++
			if mismatch.Current != current.Revision+1 {
				t.Fatalf("mismatch current revision = %d, want %d", mismatch.Current, current.Revision+1)
			}
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Fatalf("results = %v, want exactly one success and one revision mismatch", results)
	}
	final := mustFindApplication(t, service, "raced-enable")
	if final.Revision != current.Revision+1 {
		t.Fatalf("final revision = %d, want exactly one bump to %d", final.Revision, current.Revision+1)
	}
	if names := auditEventNames(t, service, current.ID); countAuditEvents(names, "disabled") != 1 {
		t.Fatalf("audit events = %v, want exactly one disablement", names)
	}
}

func TestRotateByInstanceGuardsTheRevisionAndKillsTheOldCredential(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	original := mustEnroll(t, service, "guarded-rotate")
	current := mustFindApplication(t, service, "guarded-rotate")

	if _, _, err := service.RotateByInstance(ctx, "guarded-rotate", current.Revision+5); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale rotate error = %v, want a revision mismatch", err)
	}

	credential, application, err := service.RotateByInstance(ctx, "guarded-rotate", current.Revision)
	if err != nil {
		t.Fatalf("RotateByInstance: %v", err)
	}
	if credential.Secret == original.Secret || credential.Secret == "" {
		t.Fatal("rotation returned no fresh credential")
	}
	// Rotation is an administrative decision, so it has to move the revision:
	// otherwise two administrators rotate from the same page and the first
	// one's brand-new secret is dead before it is ever used.
	if application.Revision != current.Revision+1 {
		t.Fatalf("revision = %d, want %d", application.Revision, current.Revision+1)
	}
	if _, err := service.Authenticate(ctx, original.Secret, nil); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("old credential error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, nil); err != nil {
		t.Fatalf("fresh credential rejected: %v", err)
	}
}

func TestSelfRenewalDoesNotMoveTheAdministrativeRevision(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "self-renewal")
	current := mustFindApplication(t, service, "self-renewal")

	// Companion self-renewal runs on the credential window, not on an
	// administrator's say-so. If it moved the revision, every open admin page
	// would go stale on its own every fifteen minutes.
	if _, err := service.Rotate(ctx, credential.ApplicationID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if after := mustFindApplication(t, service, "self-renewal"); after.Revision != current.Revision {
		t.Fatalf("revision = %d, want it to stay %d", after.Revision, current.Revision)
	}
}

func TestRevokeByInstanceGuardsTheRevisionAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "guarded-revoke")
	current := mustFindApplication(t, service, "guarded-revoke")

	if _, err := service.RevokeByInstance(ctx, "guarded-revoke", current.Revision+1); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale revoke error = %v, want a revision mismatch", err)
	}

	revoked, err := service.RevokeByInstance(ctx, "guarded-revoke", current.Revision)
	if err != nil {
		t.Fatalf("RevokeByInstance: %v", err)
	}
	if revoked.RevokedAt == nil || revoked.Revision != current.Revision+1 {
		t.Fatalf("application = %#v, want a revoked record one revision on", revoked)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, nil); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("revoked credential error = %v, want ErrApplicationRevoked", err)
	}

	// Revocation is terminal, so replaying it against the current revision
	// settles rather than fails.
	again, err := service.RevokeByInstance(ctx, "guarded-revoke", revoked.Revision)
	if err != nil {
		t.Fatalf("second RevokeByInstance: %v", err)
	}
	if again.Revision != revoked.Revision {
		t.Fatalf("revision = %d, want it to stay %d", again.Revision, revoked.Revision)
	}
}

func TestGuardedDecisionsRefuseARevokedApplication(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "revoked-decisions")
	current := mustFindApplication(t, service, "revoked-decisions")
	revoked, err := service.RevokeByInstance(ctx, "revoked-decisions", current.Revision)
	if err != nil {
		t.Fatalf("RevokeByInstance: %v", err)
	}

	var revokedErr *ApplicationRevokedError
	_, err = service.SetEnabledByInstance(ctx, "revoked-decisions", true, revoked.Revision)
	if !errors.As(err, &revokedErr) || !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("enable-after-revoke error = %v, want an ApplicationRevokedError", err)
	}
	if revokedErr.Current != revoked.Revision {
		t.Fatalf("revoked error current revision = %d, want %d", revokedErr.Current, revoked.Revision)
	}
	if _, _, err := service.RotateByInstance(ctx, "revoked-decisions", revoked.Revision); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("rotate-after-revoke error = %v, want ErrApplicationRevoked", err)
	}
}

func TestGuardedDecisionsRejectAnUnknownInstance(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "known-instance")

	if _, err := service.SetEnabledByInstance(ctx, "no-such-instance", false, 1); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("unknown enable error = %v, want ErrApplicationNotFound", err)
	}
	if _, _, err := service.RotateByInstance(ctx, "no-such-instance", 1); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("unknown rotate error = %v, want ErrApplicationNotFound", err)
	}
	if _, err := service.RevokeByInstance(ctx, "", 1); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("empty-instance revoke error = %v, want ErrApplicationNotFound", err)
	}
}

func mustFindApplication(t *testing.T, service *Service, instanceID string) Application {
	t.Helper()
	applications, err := service.ListApplications(context.Background())
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	for _, application := range applications {
		if application.InstanceID == instanceID {
			return application
		}
	}
	t.Fatalf("no application with instance %q", instanceID)
	return Application{}
}
