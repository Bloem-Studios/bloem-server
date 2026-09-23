package handlers

// Silo declarations Bloem no longer calls are kept verbatim in their
// Silo-owned files, so those files stay byte-close to upstream and upstream
// edits to them keep merging cleanly. These blank references keep the unused
// linter quiet about them. If upstream deletes one, delete its line here.
var (
	_ = (*AdminHandler).presignPosterURL
	_ = readImportDataFromRemoteURL
	_ = containsStringFoldV3
)

var (
	_ adminUserProfileRow
	_ notificationApplePushDisplayResponse
	_ libraryCollectionGroupsListResponse
	_ createLibraryCollectionGroupRequest
	_ updateLibraryCollectionGroupRequest
	_ reorderLibraryCollectionGroupsRequest
)
