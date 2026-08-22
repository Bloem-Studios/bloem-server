package entitlements_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

func TestApplyPolicyPatchSetOperationsAreCanonical(t *testing.T) {
	base := entitlements.Policy{
		LibraryIDs:               []int{1, 3},
		PlaybackAllowed:          true,
		MaxStreams:               3,
		MaxProfiles:              5,
		TranscodeAllowed:         true,
		MaxTranscodes:            1,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		MaxPlaybackQuality:       "1080p",
		AllowedPermissions:       []string{"marker_edit"},
		RequestsAllowed:          true,
	}
	tests := []struct {
		name        string
		patch       entitlements.PolicyPatch
		wantLibrary []int
		wantPerms   []string
	}{
		{name: "add libraries", patch: entitlements.PolicyPatch{Libraries: &entitlements.SetOperation[int]{Mode: "add", Values: []int{2, 1, 2}}}, wantLibrary: []int{1, 2, 3}, wantPerms: []string{"marker_edit"}},
		{name: "remove libraries", patch: entitlements.PolicyPatch{Libraries: &entitlements.SetOperation[int]{Mode: "remove", Values: []int{3, 3}}}, wantLibrary: []int{1}, wantPerms: []string{"marker_edit"}},
		{name: "replace libraries", patch: entitlements.PolicyPatch{Libraries: &entitlements.SetOperation[int]{Mode: "replace", Values: []int{7, 5, 7}}}, wantLibrary: []int{5, 7}, wantPerms: []string{"marker_edit"}},
		{name: "all libraries", patch: entitlements.PolicyPatch{Libraries: &entitlements.SetOperation[int]{Mode: "all"}}, wantLibrary: nil, wantPerms: []string{"marker_edit"}},
		{name: "no libraries", patch: entitlements.PolicyPatch{Libraries: &entitlements.SetOperation[int]{Mode: "none"}}, wantLibrary: []int{}, wantPerms: []string{"marker_edit"}},
		{name: "add permissions", patch: entitlements.PolicyPatch{Permissions: &entitlements.SetOperation[string]{Mode: "add", Values: []string{"metadata_curation", "marker_edit"}}}, wantLibrary: []int{1, 3}, wantPerms: []string{"marker_edit", "metadata_curation"}},
		{name: "remove permissions", patch: entitlements.PolicyPatch{Permissions: &entitlements.SetOperation[string]{Mode: "remove", Values: []string{"marker_edit"}}}, wantLibrary: []int{1, 3}, wantPerms: []string{}},
		{name: "replace permissions", patch: entitlements.PolicyPatch{Permissions: &entitlements.SetOperation[string]{Mode: "replace", Values: []string{"metadata_curation", "metadata_curation"}}}, wantLibrary: []int{1, 3}, wantPerms: []string{"metadata_curation"}},
		{name: "unrestricted permissions", patch: entitlements.PolicyPatch{Permissions: &entitlements.SetOperation[string]{Mode: "unrestricted"}}, wantLibrary: []int{1, 3}, wantPerms: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := entitlements.ApplyPolicyPatch(base, test.patch)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.LibraryIDs, test.wantLibrary) || !reflect.DeepEqual(got.AllowedPermissions, test.wantPerms) {
				t.Fatalf("sets = libraries %#v permissions %#v, want %#v/%#v", got.LibraryIDs, got.AllowedPermissions, test.wantLibrary, test.wantPerms)
			}
		})
	}
}

func TestApplyPolicyPatchRejectsUnknownModesAndInvalidDependencies(t *testing.T) {
	base := entitlements.Policy{PlaybackAllowed: true, MaxStreams: 3, MaxProfiles: 5, TranscodeAllowed: true, MaxTranscodes: 1, DownloadAllowed: true, DownloadTranscodeAllowed: true}
	disabled := false
	negative := -1
	tests := []entitlements.PolicyPatch{
		{Libraries: &entitlements.SetOperation[int]{Mode: "toggle", Values: []int{1}}},
		{Permissions: &entitlements.SetOperation[string]{Mode: "toggle", Values: []string{"marker_edit"}}},
		{Permissions: &entitlements.SetOperation[string]{Mode: "replace", Values: []string{"unknown_permission"}}},
		{MaxStreams: &negative},
		{DownloadAllowed: &disabled},
		{PlaybackAllowed: &disabled},
	}
	for _, patch := range tests {
		if _, err := entitlements.ApplyPolicyPatch(base, patch); !errors.Is(err, entitlements.ErrInvalidPolicy) {
			t.Fatalf("patch %+v error = %v, want ErrInvalidPolicy", patch, err)
		}
	}
}

func TestPolicyPatchDigestCanonicalizesEquivalentSetValues(t *testing.T) {
	first := entitlements.PolicyPatch{
		Libraries:   &entitlements.SetOperation[int]{Mode: "replace", Values: []int{7, 5, 7}},
		Permissions: &entitlements.SetOperation[string]{Mode: "replace", Values: []string{"metadata_curation", "marker_edit", "metadata_curation"}},
	}
	second := entitlements.PolicyPatch{
		Libraries:   &entitlements.SetOperation[int]{Mode: "replace", Values: []int{5, 7}},
		Permissions: &entitlements.SetOperation[string]{Mode: "replace", Values: []string{"marker_edit", "metadata_curation"}},
	}
	firstDigest, err := entitlements.PolicyPatchDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := entitlements.PolicyPatchDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == "" || firstDigest != secondDigest {
		t.Fatalf("equivalent digests = %q/%q", firstDigest, secondDigest)
	}
}

func TestApplyPolicyPatchPreservesDynamicAllLibrarySemantics(t *testing.T) {
	base := entitlements.Policy{
		LibraryIDs: nil, PlaybackAllowed: true, MaxStreams: 3, MaxProfiles: 5,
		TranscodeAllowed: true, MaxTranscodes: 1, DownloadAllowed: true,
		DownloadTranscodeAllowed: true, MaxPlaybackQuality: "1080p", RequestsAllowed: true,
	}
	added, err := entitlements.ApplyPolicyPatch(base, entitlements.PolicyPatch{
		Libraries: &entitlements.SetOperation[int]{Mode: entitlements.PolicySetAdd, Values: []int{7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.LibraryIDs != nil {
		t.Fatalf("add to dynamic all libraries = %#v, want nil (all)", added.LibraryIDs)
	}
	if _, err := entitlements.ApplyPolicyPatch(base, entitlements.PolicyPatch{
		Libraries: &entitlements.SetOperation[int]{Mode: entitlements.PolicySetRemove, Values: []int{7}},
	}); !errors.Is(err, entitlements.ErrInvalidPolicy) {
		t.Fatalf("remove from unmaterialized all libraries error = %v, want ErrInvalidPolicy", err)
	}
}
