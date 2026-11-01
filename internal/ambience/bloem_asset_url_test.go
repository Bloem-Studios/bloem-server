package ambience

import "testing"

// Uploads now return paths on the native API; paths saved before the move
// still validate, because the legacy route keeps serving them.
func TestAssetURLAcceptsNativeAndLegacyPaths(t *testing.T) {
	if got := AssetURL("0123456789abcdef.png"); got != "/api/bloem/v1/ambience/assets/0123456789abcdef.png" {
		t.Fatalf("AssetURL = %q", got)
	}
	for _, ok := range []string{
		"/api/bloem/v1/ambience/assets/0123456789abcdef.png",
		"/api/v1/ambience/assets/0123456789abcdef.png",
		"https://cdn.example.test/banner.png",
	} {
		if !IsAssetURL(ok) {
			t.Errorf("IsAssetURL(%q) = false", ok)
		}
	}
	for _, bad := range []string{
		"/api/bloem/v1/ambience/assets/../secret.png",
		"/api/v2/ambience/assets/0123456789abcdef.png",
		"/api/bloem/v1/ambience/assets/",
		"http://cdn.example.test/banner.png",
	} {
		if IsAssetURL(bad) {
			t.Errorf("IsAssetURL(%q) = true", bad)
		}
	}
}
