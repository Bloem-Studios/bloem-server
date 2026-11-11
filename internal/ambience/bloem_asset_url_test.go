package ambience

import "testing"

// Uploads return paths on the native API. Paths from before the move were
// rewritten by a migration, so the retired prefixes no longer validate.
func TestAssetURLAcceptsNativePathsOnly(t *testing.T) {
	if got := AssetURL("0123456789abcdef.png"); got != "/api/bloem/v2/ambience/assets/0123456789abcdef.png" {
		t.Fatalf("AssetURL = %q", got)
	}
	for _, ok := range []string{
		"/api/bloem/v2/ambience/assets/0123456789abcdef.png",
		"https://cdn.example.test/banner.png",
	} {
		if !IsAssetURL(ok) {
			t.Errorf("IsAssetURL(%q) = false", ok)
		}
	}
	for _, bad := range []string{
		"/api/bloem/v2/ambience/assets/../secret.png",
		"/api/v2/ambience/assets/0123456789abcdef.png",
		"/api/v1/ambience/assets/0123456789abcdef.png",       // v1-guard: allow (retired prefix is refused)
		"/api/bloem/v1/ambience/assets/0123456789abcdef.png", // v1-guard: allow (retired prefix is refused)
		"/api/bloem/v2/ambience/assets/",
		"http://cdn.example.test/banner.png",
	} {
		if IsAssetURL(bad) {
			t.Errorf("IsAssetURL(%q) = true", bad)
		}
	}
}
