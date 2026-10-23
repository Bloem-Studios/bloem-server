package api

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"fmt"
	"path/filepath"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
)

type browserPluginFixture struct{ dir string }

func (browserPluginFixture) RouteDescriptors(context.Context, int) ([]*pluginv1.HttpRouteDescriptor, error) {
	return []*pluginv1.HttpRouteDescriptor{
		{Method: "GET", Path: "/", Access: "authenticated", StaticAsset: true},
		{Method: "GET", Path: "/app.js", Access: "authenticated", StaticAsset: true},
		{Method: "GET", Path: "/admin", Access: "admin", StaticAsset: true},
	}, nil
}

func (f browserPluginFixture) ResolveAssetPath(_ context.Context, _ int, path string) (string, error) {
	if path == "" || path == "admin" {
		path = "page.html"
	}
	return filepath.Join(f.dir, path), nil
}

func (browserPluginFixture) HTTPRoutesClient(context.Context, int, string) (*pluginhost.HTTPRoutesClient, error) {
	return nil, fmt.Errorf("fixture serves only static content")
}
