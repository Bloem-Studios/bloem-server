package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// nativeStorageOrganizationConnectionGuard stops an organization administrator
// from pointing a storage plugin process at an internal address. The S3 plugin
// connects to connection.endpoint itself, so, in organization scope, that
// endpoint must pass the same guard as S3 browsing (https, well-known S3 port,
// no loopback/private/link-local/metadata address) and the plain-HTTP loopback
// fixture switch is refused. Platform administrators keep the plugin's own
// rules. Bookwarehouse base_url is not covered here.
func nativeStorageOrganizationConnectionGuard(ctx context.Context, scope auth.AdminScope, config map[string]map[string]any) error {
	if scope == auth.AdminScopePlatform {
		return nil
	}
	connection := config["connection"]
	if connection == nil {
		return nil
	}
	if allow, present := connection["allow_loopback_http"]; present && allow != nil && allow != false {
		return &catalog.NativeOnboardingError{Code: nativestorage.S3EndpointRejectedCode}
	}
	raw, present := connection["endpoint"]
	if !present || raw == nil {
		return nil
	}
	endpoint, ok := raw.(string)
	if !ok {
		return nativeStorageInvalid()
	}
	return nativestorage.CheckS3SourceEndpoint(ctx, endpoint, true)
}
