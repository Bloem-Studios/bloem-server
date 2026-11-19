package storageplugin

import (
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
