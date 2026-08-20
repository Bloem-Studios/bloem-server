package auth

import (
	"context"
	"testing"
)

func TestSessionRepositoryRevokeAllByUserAndProfilesScopesOneAtomicUpdate(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	userID, selectedProfileID := newProfileCredentialFixture(t, credentials.pool, "scoped-revoke-selected")
	otherUserID, otherUserProfileID := newProfileCredentialFixture(t, credentials.pool, "scoped-revoke-other-user")

	var organizationID string
	var accessGroupID int64
	if err := credentials.pool.QueryRow(ctx, `
		SELECT organization_id::text, access_group_id
		FROM user_profiles WHERE user_id=$1 AND id=$2`, userID, selectedProfileID,
	).Scan(&organizationID, &accessGroupID); err != nil {
		t.Fatalf("load selected profile scope: %v", err)
	}
	outOfScopeProfileID := "profile-scoped-revoke-out"
	if _, err := credentials.pool.Exec(ctx, `
		INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id)
		VALUES ($1,$2,'Out of scope',$3,$4)`, outOfScopeProfileID, userID, organizationID, accessGroupID); err != nil {
		t.Fatalf("create out-of-scope profile: %v", err)
	}

	if _, err := credentials.pool.Exec(ctx, `
		INSERT INTO auth_sessions (id,user_id,device_id,expires_at,profile_id,profile_credential_revision,auth_method) VALUES
		('scoped-native-1',$1,'device-1',now()+interval '1 hour',$2,0,'direct_profile'),
		('scoped-native-2',$1,'device-2',now()+interval '1 hour',$2,0,'direct_profile'),
		('other-profile-native',$1,'device-3',now()+interval '1 hour',$3,0,'direct_profile'),
		('account-native',$1,'',now()+interval '1 hour',NULL,NULL,'account'),
		('other-user-native',$4,'device-4',now()+interval '1 hour',$5,0,'direct_profile')`,
		userID, selectedProfileID, outOfScopeProfileID, otherUserID, otherUserProfileID); err != nil {
		t.Fatalf("seed scoped sessions: %v", err)
	}

	repository := NewSessionRepository(credentials.pool)
	if err := repository.RevokeAllByUserAndProfiles(ctx, userID, []string{selectedProfileID, otherUserProfileID}); err != nil {
		t.Fatalf("RevokeAllByUserAndProfiles: %v", err)
	}

	for sessionID, wantRevoked := range map[string]bool{
		"scoped-native-1":      true,
		"scoped-native-2":      true,
		"other-profile-native": false,
		"account-native":       false,
		"other-user-native":    false,
	} {
		var revoked bool
		if err := credentials.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, sessionID).Scan(&revoked); err != nil {
			t.Fatalf("load session %q: %v", sessionID, err)
		}
		if revoked != wantRevoked {
			t.Errorf("session %q revoked=%v, want %v", sessionID, revoked, wantRevoked)
		}
	}
}
