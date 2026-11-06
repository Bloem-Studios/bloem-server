package backup

import "testing"

// TestBloemAccountRestoresFromUserDataBackupsDB runs Silo's tests of
// restoring accounts and profiles with Bloem's user-data backups on: what a
// restore of accounts brings back, matches and maps is the same from a
// user-data backup as from a whole-server one.
func TestBloemAccountRestoresFromUserDataBackupsDB(t *testing.T) {
	for _, c := range []struct {
		name string
		test func(*testing.T)
	}{
		{"UserRestore", TestUserRestoreDB},
		{"UserMatching", TestUserMatchingDB},
		{"UserRestoreSameServer", TestUserRestoreSameServerDB},
		{"OmittedAccountIsReported", TestOmittedAccountIsReportedDB},
		{"EmailLessAccountRestore", TestEmailLessAccountRestoreDB},
		{"NewAccountNeedsItsPrimaryProfile", TestNewAccountNeedsItsPrimaryProfileDB},
		{"ItemMatchingInATransactionWithOnePoolConnection", TestItemMatchingInATransactionWithOnePoolConnectionDB},
		{"MatchedAccountPending", TestMatchedAccountPendingDB},
		{"FreshBackupComparesSame", TestFreshBackupComparesSameDB},
		{"AccountsAndImportOnOneConnection", TestAccountsAndImportOnOneConnectionDB},
		{"CollectionImportKeepsTheProfileQuota", TestCollectionImportKeepsTheProfileQuotaDB},
		{"ImportsReadInBatches", TestImportsReadInBatchesDB},
		{"RestoreItemWritesDoNotGrow", TestRestoreItemWritesDoNotGrowDB},
		{"IdentityTwinsRestoreOneToOne", TestIdentityTwinsRestoreOneToOneDB},
		{"LayoutCollectionsMapAcrossServers", TestLayoutCollectionsMapAcrossServersDB},
		{"RestoreLockOrder", TestRestoreLockOrderDB},
		{"ProfileTransfer", TestProfileTransferDB},
	} {
		t.Run(c.name, func(t *testing.T) {
			bloemUserDataBackups(t)
			c.test(t)
		})
	}
}
