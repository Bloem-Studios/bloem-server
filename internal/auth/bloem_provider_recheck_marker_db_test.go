package auth

import (
	"context"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// bloemRecheckMarkerSkip names the gap TestProviderRecheckMarkerWaitFailsOpenDB
// pins. Remove the skip together with the fix.
const bloemRecheckMarkerSkip = "known gap in Silo-owned code, an upstream contribution candidate: " +
	"ProviderRecheck.markOutcomeUnknown bounds the call-outcome marker UPDATE with the plugin-call timeout. " +
	"When a row lock on the identity outlasts it, pgx closes the advisory-locked connection, " +
	"and the refresh fails with \"conn closed\" instead of failing open. " +
	"The marker needs its own bound, ended by the server (lock_timeout) rather than the context"

// TestProviderRecheckMarkerWaitFailsOpenDB: a refresh whose call-outcome
// marker waits on a locked identity row past the plugin-call timeout must
// still end like any other unanswered check, keeping the session (fail_open,
// the default). It must not fail the refresh with a closed connection.
//
// TestProviderRecheckUnavailableDB used to hit this under CI load with its
// 100 ms timeout; this test holds the lock on purpose so the gap stays
// visible after that test's timeout was raised.
func TestProviderRecheckMarkerWaitFailsOpenDB(t *testing.T) {
	t.Skip(bloemRecheckMarkerSkip)
	env := newRecheckEnv(t, "markerwait", "rt-1")
	env.recheck.timeout = 100 * time.Millisecond
	env.checker.respond = answer(pluginv1.CheckAccountStatus_CHECK_ACCOUNT_STATUS_ACTIVE, "")
	sessionID, refresh := env.session(t, true)
	env.makeDue(t)

	holder := env.lockRow(t, `SELECT id FROM plugin_auth_identities WHERE id = $1 FOR UPDATE`, env.identityID)
	release := time.AfterFunc(4*env.recheck.timeout, func() { _ = holder.Rollback(context.Background()) })
	defer release.Stop()

	if _, err := env.svc.Refresh(t.Context(), refresh); err != nil {
		t.Fatalf("refresh with a slow call-outcome marker = %v, want the session kept", err)
	}
	if env.sessionRow(t, sessionID).RevokedAt != nil {
		t.Fatal("a slow call-outcome marker revoked the session")
	}
	switch got := env.identityState(t).LastCheckStatus; got {
	case CheckStatusActive, CheckStatusUnavailable:
	default:
		t.Fatalf("status = %q, want the provider's answer or unavailable", got)
	}
}
