package handlers

// The Bloem-native OpenAPI document (internal/apiv2/bloem_native_*.go) restates
// shapes that are unexported here, because the document is generated from types
// in that package while this package owns the bytes on the wire.
//
// Two declarations of one shape drift the moment a field is added to only one,
// and the failure is silent: the document simply stops describing what the
// server sends, and a generated client never learns to parse the new field.
//
// These functions exist so a test in that package can compare the two field
// sets without exporting the response types themselves, which would invite
// callers to depend on shapes that are this package's private business.

// BloemServerIdentityWireShape returns a zero value of the identity probe's
// response, for contract tests that compare its JSON field set against the
// documented one.
func BloemServerIdentityWireShape() any { return serverIdentityResponse{} }

// BloemOrganizationWireShape returns a zero value of one organization entry,
// for the same purpose.
func BloemOrganizationWireShape() any { return bloemOrganization{} }

// BloemSyncProgressItemWireShape returns a zero value of one submitted playback
// position, for the same contract comparison.
func BloemSyncProgressItemWireShape() any { return syncProgressItem{} }

// BloemSyncProgressResultWireShape returns a zero value of one per-item result.
func BloemSyncProgressResultWireShape() any { return syncProgressResultItem{} }

// BloemPersonDetailWireShape returns a zero value of the person detail
// response, for the same contract comparison.
func BloemPersonDetailWireShape() any { return personDetailResponse{} }

// BloemPersonFilmographyWireShape returns a zero value of one filmography entry.
func BloemPersonFilmographyWireShape() any { return personFilmographyEntry{} }

// BloemNotificationPageWireShape returns a zero value of one native inbox page.
func BloemNotificationPageWireShape() any { return bloemNotificationPage{} }

// BloemNotificationSyncPageWireShape returns a zero value of one forward-sync
// page.
func BloemNotificationSyncPageWireShape() any { return bloemNotificationSyncPage{} }

// BloemNotificationPageInfoWireShape returns a zero value of the page block
// both of them carry.
func BloemNotificationPageInfoWireShape() any { return bloemNotificationPageInfo{} }

// BloemItemCollectionsWireShape returns a zero value of the item-collections
// document, for the same contract comparison.
func BloemItemCollectionsWireShape() any { return itemCollectionsResponse{} }

// BloemItemCollectionWireShape returns a zero value of one collection entry.
func BloemItemCollectionWireShape() any { return itemCollectionEntry{} }
