package compatapp

import (
	"context"
	"strings"
	"testing"
)

// The schema is a trust boundary of its own: a direct SQL write that bypasses
// the service must not be able to weaken an application's recorded identity,
// rewrite its audit trail, or store malformed trust state.

func TestCompatApplicationIdentityIsImmutableInTheDatabase(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "immutable")
	pool := service.store.pool

	if _, err := pool.Exec(ctx, `
		UPDATE compat_applications SET kind = 'audiobookshelf' WHERE id = $1`,
		credential.ApplicationID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("direct kind rewrite error = %v, want immutability violation", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE compat_applications SET instance_id = 'other-instance' WHERE id = $1`,
		credential.ApplicationID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("direct instance rewrite error = %v, want immutability violation", err)
	}
	// Mutable operational state stays writable.
	if _, err := pool.Exec(ctx, `
		UPDATE compat_applications SET health_status = 'degraded' WHERE id = $1`,
		credential.ApplicationID); err != nil {
		t.Fatalf("update mutable application state: %v", err)
	}
}

func TestCompatAuditRowsAreImmutableInTheDatabase(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "audited")
	pool := service.store.pool

	if _, err := pool.Exec(ctx, `
		UPDATE compat_application_audit SET event = 'enabled' WHERE application_id = $1`,
		credential.ApplicationID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("audit rewrite error = %v, want immutability violation", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM compat_application_audit WHERE application_id = $1`,
		credential.ApplicationID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("audit delete error = %v, want immutability violation", err)
	}
}

func TestCompatSchemaRejectsMalformedDirectWrites(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "constrained")
	pool := service.store.pool

	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{
			"unknown kind",
			`INSERT INTO compat_applications (kind, instance_id, version, api_range_min, api_range_max, granted_capabilities)
			 VALUES ('plex', 'x', '1', 1, 1, ARRAY['catalog.read'])`,
			nil,
		},
		{
			"blank instance",
			`INSERT INTO compat_applications (kind, instance_id, version, api_range_min, api_range_max, granted_capabilities)
			 VALUES ('jellyfin', '   ', '1', 1, 1, ARRAY['catalog.read'])`,
			nil,
		},
		{
			"inverted API range",
			`INSERT INTO compat_applications (kind, instance_id, version, api_range_min, api_range_max, granted_capabilities)
			 VALUES ('jellyfin', 'ranged', '1', 3, 2, ARRAY['catalog.read'])`,
			nil,
		},
		{
			"unknown health status",
			`UPDATE compat_applications SET health_status = 'thriving' WHERE id = $1`,
			[]any{credential.ApplicationID},
		},
		{
			"non-digest credential secret",
			`INSERT INTO compat_application_credentials (application_id, secret_digest, expires_at)
			 VALUES ($1, 'raw-secret-not-a-digest', now())`,
			[]any{credential.ApplicationID},
		},
		{
			"non-digest enrollment secret",
			`INSERT INTO compat_application_enrollments (kind, secret_digest, granted_capabilities, expires_at)
			 VALUES ('jellyfin', 'raw-secret-not-a-digest', ARRAY['catalog.read'], now())`,
			nil,
		},
	}
	for _, testCase := range cases {
		if _, err := pool.Exec(ctx, testCase.sql, testCase.args...); err == nil {
			t.Fatalf("%s: direct write succeeded, want constraint violation", testCase.name)
		}
	}
}

func TestStoreReadsApplicationAndOrderedAudit(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "readable")

	application, err := service.store.Application(ctx, credential.ApplicationID)
	if err != nil {
		t.Fatalf("Application: %v", err)
	}
	if application.ID != credential.ApplicationID || application.Health != HealthUnknown || application.CreatedAt.IsZero() {
		t.Fatalf("application = %#v, want freshly enrolled record", application)
	}

	if _, err := service.store.Application(ctx, unknownApplicationID); err == nil {
		t.Fatal("Application(unknown) succeeded, want an error")
	}

	if err := service.SetEnabled(ctx, credential.ApplicationID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := service.Revoke(ctx, credential.ApplicationID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	events, err := service.store.AuditEvents(ctx, credential.ApplicationID)
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	var names []string
	for _, event := range events {
		if event.CreatedAt.IsZero() || event.ID == 0 {
			t.Fatalf("audit event = %#v, want identified timestamped row", event)
		}
		names = append(names, event.Event)
	}
	want := []string{"enrolled", "disabled", "revoked"}
	if len(names) != len(want) {
		t.Fatalf("audit events = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("audit events = %v, want %v in order", names, want)
		}
	}
}
