package entitlements_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func requireImmutableAuditEvent(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "entitlement audit events are immutable") {
		t.Fatalf("err = %v, want the audit immutability refusal", err)
	}
}

// An account an entitlement template was applied to can be deleted: its audit
// events stay, with the target cleared. Audit events remain otherwise
// immutable.
func TestEntitlementAuditEventsReleaseDeletedTargetAccount(t *testing.T) {
	ctx, pool, _ := entitlementTestStore(t)
	name := fmt.Sprintf("audit-target-%d", time.Now().UnixNano())
	var accountID int
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role)
		VALUES ($1::text, $1::text || '@example.invalid', 'invented', 'user') RETURNING id`, name).Scan(&accountID))
	var eventID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO entitlement_audit_events (action, target_account_id, template_key, template_revision, request_id)
		VALUES ('entitlement_template.account_applied', $1, 'standard', 2, $2) RETURNING id`, accountID, name).Scan(&eventID))

	_, err := pool.Exec(ctx, `UPDATE entitlement_audit_events SET template_revision = 3 WHERE id = $1`, eventID)
	requireImmutableAuditEvent(t, err)
	_, err = pool.Exec(ctx, `UPDATE entitlement_audit_events SET target_account_id = NULL, request_id = 'rewritten' WHERE id = $1`, eventID)
	requireImmutableAuditEvent(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	require.NoError(t, err)
	var target *int
	var action, requestID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT target_account_id, action, request_id FROM entitlement_audit_events WHERE id = $1`, eventID).Scan(&target, &action, &requestID))
	require.Nil(t, target)
	require.Equal(t, "entitlement_template.account_applied", action)
	require.Equal(t, name, requestID)

	_, err = pool.Exec(ctx, `DELETE FROM entitlement_audit_events WHERE id = $1`, eventID)
	requireImmutableAuditEvent(t, err)
}
