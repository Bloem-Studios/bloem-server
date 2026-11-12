//go:build integration

package plugins

import (
	"encoding/json"
	"testing"
)

// An S3 edit through the management path keeps the stored request limits and
// proxy it does not resend, and stores the result in one revision. It runs
// against the real store, beside the credential rotation tests.
func TestReplaceConfigurationCarriesS3StoredSettingsDB(t *testing.T) {
	f := newRotationFixture(t, nativeS3PluginID, true)
	before := f.state(t)
	seeded := map[string]any{}
	for k, v := range before.conn {
		seeded[k] = v
	}
	seeded["max_read_mbps"], seeded["max_listing_requests"] = 90.0, 3.0
	seeded["http_proxy"], seeded["http_proxy_username"], seeded["http_proxy_password"] = "http://proxy.synthetic.example:3128", "synthetic-user", "synthetic-proxy-password"
	revision, err := f.r.ReplaceConfigurationAuthorized(t.Context(), f.key, f.owner, before.revision, map[string]map[string]any{"connection": seeded}, rotationAllow)
	if err != nil {
		t.Fatalf("seeding the limits and proxy: code=%s", rotationCode(err))
	}

	// The edit form's submission: no limits or proxy, one limit cleared.
	edit := map[string]any{"source_id": seeded["source_id"], "bucket": "synthetic-other", "region": seeded["region"], "endpoint": seeded["endpoint"],
		"access_key": "synthetic-new-access", "secret_key": "synthetic-new-secret", "max_listing_requests": nil}
	if _, err = f.r.ReplaceConfigurationAuthorized(t.Context(), f.key, f.owner, revision, map[string]map[string]any{"connection": edit}, rotationAllow); err != nil {
		t.Fatalf("edit refused: code=%s", rotationCode(err))
	}
	after := f.state(t)
	if after.revision != revision+1 {
		t.Fatalf("revision %d->%d", revision, after.revision)
	}
	// The store decodes numbers as json.Number.
	if after.conn["bucket"] != "synthetic-other" || after.conn["max_read_mbps"] != json.Number("90") || after.conn["http_proxy"] != seeded["http_proxy"] || after.conn["http_proxy_username"] != seeded["http_proxy_username"] {
		t.Fatalf("edit did not keep the stored settings: bucket=%v mbps=%#v proxy kept=%v user kept=%v", after.conn["bucket"], after.conn["max_read_mbps"],
			after.conn["http_proxy"] == seeded["http_proxy"], after.conn["http_proxy_username"] == seeded["http_proxy_username"])
	}
	if rotationFP(after.conn["http_proxy_password"]) != rotationFP(seeded["http_proxy_password"]) {
		t.Fatal("the proxy password was not kept for the same proxy and user")
	}
	if _, kept := after.conn["max_listing_requests"]; kept {
		t.Fatal("a cleared limit survived the edit")
	}
	if _, kept := after.conn["session_token"]; kept {
		t.Fatal("an edit carried the stored session token")
	}
}
