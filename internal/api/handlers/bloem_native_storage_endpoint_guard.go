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
var nativeStorageGuardedConnectionKeys = map[string]bool{
	"endpoint": true, "base_url": true, "allow_loopback_http": true, "allow_insecure_http": true,
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
//   - a case variant of "connection" or of any of those fields, and any two
//     connection fields differing only in case, are invalid, because plugins
//     decode JSON field names case-insensitively.
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
	seen := map[string]bool{}
	for key := range connection {
		folded := strings.ToLower(key)
		if seen[folded] || (nativeStorageGuardedConnectionKeys[folded] && key != folded) {
			return nativeStorageInvalid()
		}
		seen[folded] = true
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
