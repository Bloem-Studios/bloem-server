package storageplugin

import (
	"context"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestValidateNativeSurfaces(t *testing.T) {
	router := func(id string) *publicv1.CapabilityDescriptor {
		return &publicv1.CapabilityDescriptor{Type: "request_router.v1", Id: id}
	}
	for name, tc := range map[string]struct {
		manifest *publicv1.PluginManifest
		ok       bool
	}{
		"no surfaces":         {&publicv1.PluginManifest{}, true},
		"request routers":     {&publicv1.PluginManifest{Capabilities: []*publicv1.CapabilityDescriptor{router("ebooks"), router("audiobooks")}}, true},
		"metadata provider":   {&publicv1.PluginManifest{Capabilities: []*publicv1.CapabilityDescriptor{{Type: "metadata_provider.v1", Id: "metadata"}}}, false},
		"router and metadata": {&publicv1.PluginManifest{Capabilities: []*publicv1.CapabilityDescriptor{router("ebooks"), {Type: "scan_source.v1", Id: "scan"}}}, false},
		"router without id":   {&publicv1.PluginManifest{Capabilities: []*publicv1.CapabilityDescriptor{router("")}}, false},
		"router and route":    {&publicv1.PluginManifest{Capabilities: []*publicv1.CapabilityDescriptor{router("ebooks")}, HttpRoutes: []*publicv1.HttpRouteDescriptor{{Path: "/x"}}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateNativeSurfaces(tc.manifest); (err == nil) != tc.ok {
				t.Fatalf("ValidateNativeSurfaces = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestDeclaresRequestRouter(t *testing.T) {
	m := &publicv1.PluginManifest{Capabilities: []*publicv1.CapabilityDescriptor{{Type: "request_router.v1", Id: "ebooks"}, {Type: "metadata_provider.v1", Id: "audiobooks"}}}
	if !DeclaresRequestRouter(m, "ebooks") || DeclaresRequestRouter(m, "audiobooks") || DeclaresRequestRouter(m, "") {
		t.Fatal("request router declaration misread")
	}
}

// The storage session also carries the provider's request router, over the
// same process and connection, so the host can route requests to it.
func TestSessionServesDeclaredRequestRouter(t *testing.T) {
	m := manager(t)
	s, err := m.Ensure(context.Background(), snapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	router, ok := s.RequestRouter("ebooks")
	if !ok {
		t.Fatal("declared request router not offered")
	}
	if _, ok := s.RequestRouter("audiobooks"); ok {
		t.Fatal("undeclared request router offered")
	}
	response, err := router.TestConnection(context.Background(), &publicv1.TestConnectionRequest{CapabilityId: "ebooks", Connection: &publicv1.RouterConnection{Id: "c1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetOk() || response.GetMessage() != "fixture router c1" {
		t.Fatalf("router answered %v", response)
	}
	if _, ok := (&Session{}).RequestRouter("ebooks"); ok {
		t.Fatal("session without a router offered one")
	}
}
