package backup

// Bloem's test-only additions to Silo's backup coverage tests.

func init() {
	// The fixture-compatibility shim (internal/bloemtestdb) adds this
	// table to test databases only: a test's own bookkeeping, never in a
	// production schema, so production lists do not name it.
	serverStateTables = append(serverStateTables, "bloem_fixture_shim_memberships")
}

// bloemFieldDecisions are the fields Bloem adds to Silo's access policy and
// profile types, each with how the restore's authority comparison treats
// it. TestAuthorityComparisonCoversEveryField and
// TestProfileComparisonCoversEveryField skip a field named here.
//
// The ones marked "not compared yet" grant or limit access, but Silo's
// restore cannot compare them yet: a restore set's effective policy and
// profile entries do not carry them, so comparing them would refuse every
// account a selective restore brings to Bloem. Backups are off in Bloem's
// production. docs/architecture/bloem-backup.md lists the follow-up.
var bloemFieldDecisions = map[string]string{
	// accesspolicy.EffectiveUserPolicy
	"PlaybackAllowed": "not compared yet: a restore set's effective policy has no playback switch",
	"PreviewOnly":     "not compared yet: a restore set's effective policy has no preview flag",
	// userstore.Profile
	"OrganizationID":     "not compared yet: a restore set's profile entry has no organization",
	"AccessGroupID":      "not compared yet: a restore set's profile entry has no access group",
	"LoginEmail":         "no authority: the address the profile signs in with, an identity like Name",
	"CredentialRevision": "no authority: profile sign-in tokens are bound to it, as to PINRevision",
}
