package compatcontract

import (
	_ "embed"
	"encoding/json"
)

//go:embed testdata/jellyfin/baseline.json
var jellyfinBaselineJSON []byte

//go:embed testdata/audiobookshelf/baseline.json
var audiobookshelfBaselineJSON []byte

//go:embed testdata/jellyfin/adult-authorized.json
var jellyfinAdultAuthorizedJSON []byte

//go:embed testdata/jellyfin/adult-ordinary.json
var jellyfinAdultOrdinaryJSON []byte

//go:embed testdata/audiobookshelf/adult-authorized.json
var audiobookshelfAdultAuthorizedJSON []byte

//go:embed testdata/audiobookshelf/adult-ordinary.json
var audiobookshelfAdultOrdinaryJSON []byte

//go:embed testdata/identity/jellyfin.json
var jellyfinIdentityJSON []byte

//go:embed testdata/identity/audiobookshelf.json
var audiobookshelfIdentityJSON []byte

//go:embed testdata/identity/direct-profile.json
var directProfileIdentityJSON []byte

// JellyfinBaseline returns the fixture suite for the embedded Jellyfin
// listener. The fixtures use only invented IDs.
func JellyfinBaseline() Suite { return mustFixtureSuite(jellyfinBaselineJSON) }

// AudiobookshelfBaseline returns the fixture suite for the embedded
// Audiobookshelf handler. The fixtures use only invented IDs.
func AudiobookshelfBaseline() Suite { return mustFixtureSuite(audiobookshelfBaselineJSON) }

func JellyfinAuthorizedAdultPolicy() Suite { return mustFixtureSuite(jellyfinAdultAuthorizedJSON) }
func JellyfinOrdinaryAdultPolicy() Suite   { return mustFixtureSuite(jellyfinAdultOrdinaryJSON) }
func AudiobookshelfAuthorizedAdultPolicy() Suite {
	return mustFixtureSuite(audiobookshelfAdultAuthorizedJSON)
}
func AudiobookshelfOrdinaryAdultPolicy() Suite {
	return mustFixtureSuite(audiobookshelfAdultOrdinaryJSON)
}

// JellyfinIdentity returns the frozen identity contract for the embedded
// Jellyfin protocol surface, expressed against its real login conventions:
// account login selecting the remembered/default profile, explicit
// username#profile selection, password#pin for PIN-protected profiles, the
// single-tile user list, sibling and cross-household non-disclosure, logout
// and account-session revocation, adult-free events, and missing-ID timing.
// Placeholders bind the suite to the system under test; the same document
// runs against the real router and the reference listener. Timing cases
// compare the missing-adult and random-missing medians over at least 100
// interleaved samples with a documented tolerance of a 3x ratio and a 2ms
// absolute noise floor, and every sample must answer the same status.
func JellyfinIdentity() Suite { return mustFixtureSuite(jellyfinIdentityJSON) }

// AudiobookshelfIdentity returns the frozen identity contract for the
// embedded Audiobookshelf protocol surface. Audiobookshelf has no reliable
// general-purpose profile picker, so account login resolves the primary
// profile and username#profile selects explicitly; tokens are revoked by
// logout and validated per request. The timing tolerance matches
// JellyfinIdentity.
func AudiobookshelfIdentity() Suite { return mustFixtureSuite(audiobookshelfIdentityJSON) }

// DirectProfileIdentity returns the frozen contract for direct profile login
// on the native surface: the login binds exactly one profile and the issued
// credential is captured and then spent proving its bounds — the profile
// directory, household management, sibling mutation, credential minting, and
// PIN verification all refuse it, while the bound profile's own record stays
// readable and writable. The revocation cases probe tokens whose policy
// revision, security revision, or profile credential moved on after issue;
// consumers rotate real state between phases via Suite.Pick.
func DirectProfileIdentity() Suite { return mustFixtureSuite(directProfileIdentityJSON) }

// UnknownJellyfinDeviceProfileList returns the identity case pinning that an
// unknown device receives an empty public profile directory.
func UnknownJellyfinDeviceProfileList() Case {
	return mustSuiteCase(JellyfinIdentity(), "unknown device receives no profile directory")
}

func mustSuiteCase(suite Suite, name string) Case {
	for _, c := range suite.Cases {
		if c.Name == name {
			return c
		}
	}
	panic("missing compatibility fixture case: " + name)
}

func mustFixtureSuite(data []byte) Suite {
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		panic("invalid embedded compatibility fixture: " + err.Error())
	}
	return suite
}
