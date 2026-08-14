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

// JellyfinIdentity returns the frozen identity and policy contract for the
// Jellyfin protocol surface: direct profile login bound to exactly one
// profile with least privilege, legacy account login selecting the
// remembered/default profile, trusted-device tiles, unprotected and
// PIN-protected switching, cross-organization denial, a named case per
// revocation revision, and adult absence from bodies, counts, artwork,
// events, and playback. Timing cases compare the missing-adult and
// random-missing distributions over at least 100 samples with a documented
// tolerance of a 3x mean ratio plus the runner's fixed 20ms noise allowance.
func JellyfinIdentity() Suite { return mustFixtureSuite(jellyfinIdentityJSON) }

// AudiobookshelfIdentity returns the frozen identity and policy contract for
// the Audiobookshelf protocol surface. Audiobookshelf has no reliable
// general-purpose profile picker, so legacy account login resolves the
// remembered/default profile and there is no PIN switching; every other
// subject rule matches JellyfinIdentity, including the timing tolerance
// documented there.
func AudiobookshelfIdentity() Suite { return mustFixtureSuite(audiobookshelfIdentityJSON) }

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
