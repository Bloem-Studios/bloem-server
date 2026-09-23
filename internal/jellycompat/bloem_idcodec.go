package jellycompat

import "github.com/google/uuid"

// Bloem Live TV encoded ID types, kept out of upstream's const block.
const (
	EncodedIDLiveTVChannel     EncodedIDType = 11
	EncodedIDLiveTVProgram     EncodedIDType = 12
	EncodedIDLiveTVTimer       EncodedIDType = 13
	EncodedIDLiveTVSeriesTimer EncodedIDType = 14
)

// Live TV string ID namespaces join upstream's table at package init, before
// any codec use.
func init() {
	stringIDNamespaces[EncodedIDLiveTVChannel] = uuid.MustParse("1c8e4a2f-6d91-5b3a-9e70-2f4c8a1b5d63")
	stringIDNamespaces[EncodedIDLiveTVProgram] = uuid.MustParse("2d9f5b30-7e02-5c4b-af81-3a5d9b2c6e74")
	stringIDNamespaces[EncodedIDLiveTVTimer] = uuid.MustParse("3e0a6c41-8f13-5d5c-b092-4b6e0c3d7f85")
	stringIDNamespaces[EncodedIDLiveTVSeriesTimer] = uuid.MustParse("4f1b7d52-9024-5e6d-c1a3-5c7f1d4e8096")
}
