package handlers

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// nativeStorageGuardedConnectionKeys are the connection fields that decide
// where a storage plugin process connects, or relax that.
var nativeStorageGuardedConnectionKeys = []string{"endpoint", "base_url", "allow_loopback_http", "allow_insecure_http"}

// nativeStoragePlatformOnlyConnectionKeys are the S3 proxy fields. A proxy
// names any host and port the plugin then connects to, so only a platform
// administrator may set them.
var nativeStoragePlatformOnlyConnectionKeys = []string{"http_proxy", "http_proxy_username", "http_proxy_password"}

const nativeStoragePlatformSettingCode = "platform_setting_required"

// nativeStorageJSONKey returns the canonical key among keys that encoding/json
// would decode name into, or "". Plugins decode field names the way
// encoding/json does, and strings.EqualFold folds exactly as it does: case,
// U+017F (long s) as "s" and U+212A (Kelvin sign) as "k".
func nativeStorageJSONKey(name string, keys []string) string {
	for _, key := range keys {
		if strings.EqualFold(name, key) {
			return key
		}
	}
	return ""
}

// nativeStorageOrganizationConnectionGuard stops an organization administrator
// from pointing a storage plugin process at an internal address. Plugins
// connect to their configured address themselves, so in organization scope:
//
//   - S3 connection.endpoint must pass the S3 browse guard (https origin,
//     well-known S3 port, every resolved address public);
//   - Bookwarehouse connection.base_url must pass the same guard, with a path
//     allowed;
//   - allow_loopback_http (S3) and allow_insecure_http (Bookwarehouse) are
//     refused;
//   - the S3 proxy fields http_proxy, http_proxy_username and
//     http_proxy_password are refused with 403 platform_setting_required
//     naming the field, whatever their value, including null or empty;
//   - a variant of "connection" or of any of those fields that encoding/json
//     still matches (case, long s, Kelvin sign), and any two connection
//     fields it would treat as one, are invalid, because plugins decode JSON
//     field names that way.
//
// This is a check at configuration time: the plugin process resolves the name
// again whenever it starts, so DNS rebinding afterwards is a documented
// residual (see docs/architecture/bloem-native-storage.md). Platform
// administrators keep the plugins' own rules.
func nativeStorageOrganizationConnectionGuard(ctx context.Context, scope auth.AdminScope, config map[string]map[string]any) error {
	if scope == auth.AdminScopePlatform {
		return nil
	}
	for key := range config {
		if key != "connection" && strings.EqualFold(key, "connection") {
			return nativeStorageInvalid()
		}
	}
	connection := config["connection"]
	if connection == nil {
		return nil
	}
	keys := make([]string, 0, len(connection))
	for key := range connection {
		for _, other := range keys {
			if strings.EqualFold(key, other) {
				return nativeStorageInvalid()
			}
		}
		keys = append(keys, key)
		if canonical := nativeStorageJSONKey(key, nativeStorageGuardedConnectionKeys); canonical != "" && canonical != key {
			return nativeStorageInvalid()
		}
	}
	// Refused whatever its spelling or value: with no proxy field the edit
	// keeps the one a platform administrator stored.
	for _, key := range keys {
		if canonical := nativeStorageJSONKey(key, nativeStoragePlatformOnlyConnectionKeys); canonical != "" {
			return &catalog.NativeOnboardingError{Code: nativeStoragePlatformSettingCode, Field: "connection." + canonical}
		}
	}
	rejected := &catalog.NativeOnboardingError{Code: nativestorage.S3EndpointRejectedCode}
	for _, flag := range []string{"allow_loopback_http", "allow_insecure_http"} {
		if value, present := connection[flag]; present && value != nil && value != false {
			return rejected
		}
	}
	for key, check := range map[string]func(context.Context, string, bool) error{
		"endpoint": nativestorage.CheckS3SourceEndpoint,
		"base_url": nativestorage.CheckServiceBaseURL,
	} {
		raw, present := connection[key]
		if !present || raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return nativeStorageInvalid()
		}
		if err := check(ctx, value, true); err != nil {
			return err
		}
	}
	return nil
}
