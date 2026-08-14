package compatapp

import (
	"context"
	"errors"
	"testing"
)

// The revision is the optimistic-concurrency token the Compatibility
// Applications admin surface hands back with every decision. It is a schema
// invariant, not a service convention: a writer that never passes through the
// service must not be able to change an application's governed state without
// moving the revision, nor move the revision without changing that state.

func applicationRevision(t *testing.T, service *Service, applicationID string) int64 {
	t.Helper()
	var revision int64
	if err := service.store.pool.QueryRow(context.Background(),
		`SELECT revision FROM compat_applications WHERE id = $1`, applicationID).Scan(&revision); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	return revision
}

func TestApplicationRevisionBumpsOnGovernedDirectWrite(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "direct-governed")
	pool := service.store.pool

	start := applicationRevision(t, service, credential.ApplicationID)
	if start < 1 {
		t.Fatalf("initial revision = %d, want a positive revision", start)
	}

	// Each of these bypasses the service entirely. The bump has to come from
	// the database or the guard is decorative.
	governed := []struct {
		name string
		sql  string
	}{
		{"enablement", `UPDATE compat_applications SET enabled = false WHERE id = $1`},
		{"revocation", `UPDATE compat_applications SET revoked_at = now() WHERE id = $1`},
		{"capability grant", `UPDATE compat_applications SET granted_capabilities = ARRAY['catalog'] WHERE id = $1`},
		{"api range", `UPDATE compat_applications SET api_range_max = 2 WHERE id = $1`},
		{"credential rotation", `UPDATE compat_applications SET credential_rotated_at = now() WHERE id = $1`},
	}
	previous := start
	for _, write := range governed {
		if _, err := pool.Exec(ctx, write.sql, credential.ApplicationID); err != nil {
			t.Fatalf("%s: direct write: %v", write.name, err)
		}
		current := applicationRevision(t, service, credential.ApplicationID)
		if current != previous+1 {
			t.Fatalf("%s: revision = %d, want %d", write.name, current, previous+1)
		}
		previous = current
	}
}

func TestApplicationRevisionIgnoresLivenessAndNoOpWrites(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "direct-liveness")
	pool := service.store.pool
	start := applicationRevision(t, service, credential.ApplicationID)

	// Companion liveness is not an administrative decision. Folding it into
	// the revision would expire an administrator's expected revision on every
	// heartbeat, so the guard would only ever produce spurious conflicts.
	inert := []struct {
		name string
		sql  string
	}{
		{"health report", `UPDATE compat_applications SET health_status = 'healthy' WHERE id = $1`},
		{"last contact", `UPDATE compat_applications SET last_contact_at = now() WHERE id = $1`},
		{"no-op enablement", `UPDATE compat_applications SET enabled = enabled WHERE id = $1`},
		{"no-op capabilities", `UPDATE compat_applications SET granted_capabilities = granted_capabilities WHERE id = $1`},
	}
	for _, write := range inert {
		if _, err := pool.Exec(ctx, write.sql, credential.ApplicationID); err != nil {
			// A no-op update must succeed; failing closed here would break
			// heartbeat and re-save paths for no benefit.
			t.Fatalf("%s: direct write: %v", write.name, err)
		}
		if current := applicationRevision(t, service, credential.ApplicationID); current != start {
			t.Fatalf("%s: revision = %d, want it to stay %d", write.name, current, start)
		}
	}

	if err := service.Heartbeat(ctx, credential.ApplicationID, HealthHealthy); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if current := applicationRevision(t, service, credential.ApplicationID); current != start {
		t.Fatalf("revision after heartbeat = %d, want it to stay %d", current, start)
	}
}

func TestApplicationRevisionCannotBeSetByAWriter(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "direct-revision")
	pool := service.store.pool
	start := applicationRevision(t, service, credential.ApplicationID)

	// Assigning the revision on its own changes nothing: it is derived.
	if _, err := pool.Exec(ctx,
		`UPDATE compat_applications SET revision = 9999 WHERE id = $1`, credential.ApplicationID); err != nil {
		t.Fatalf("direct revision write: %v", err)
	}
	if current := applicationRevision(t, service, credential.ApplicationID); current != start {
		t.Fatalf("revision = %d, want it to stay %d", current, start)
	}

	// Nor can a writer freeze the revision while changing governed state.
	if _, err := pool.Exec(ctx,
		`UPDATE compat_applications SET enabled = false, revision = revision WHERE id = $1`,
		credential.ApplicationID); err != nil {
		t.Fatalf("direct frozen-revision write: %v", err)
	}
	if current := applicationRevision(t, service, credential.ApplicationID); current != start+1 {
		t.Fatalf("revision = %d, want %d", current, start+1)
	}
}

func TestApplicationInstanceIDIsGloballyUniqueInTheDatabase(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "shared-instance")

	// The admin surface addresses an application by instance_id alone, so the
	// address has to be unambiguous across kinds too.
	enrollment := mustCreateEnrollment(t, service, KindAudiobookshelf)
	if _, err := service.Enroll(ctx, enrollment.Secret, validEnrollmentRequest("shared-instance")); !errors.Is(err, ErrInstanceAlreadyEnrolled) {
		t.Fatalf("cross-kind instance reuse error = %v, want ErrInstanceAlreadyEnrolled", err)
	}
}
