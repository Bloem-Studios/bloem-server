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

func mustFixtureSuite(data []byte) Suite {
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		panic("invalid embedded compatibility fixture: " + err.Error())
	}
	return suite
}
