package identitycontract

import (
	"embed"
	"encoding/json"
)

var _ embed.FS

//go:embed testdata/identity/direct-profile.json
var directProfileIdentityJSON []byte

func DirectProfileIdentity() Suite { return mustFixtureSuite(directProfileIdentityJSON) }

func mustFixtureSuite(data []byte) Suite {
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		panic(err)
	}
	return suite
}
