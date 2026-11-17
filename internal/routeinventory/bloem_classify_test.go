package routeinventory

import (
	"slices"
	"testing"
)

func TestBloemLiveTVDeliveryProfileAuthorityClassification(t *testing.T) {
	class, traits := classifyAuth([]string{
		"bloemLiveTVStreamTokens(s.auth, s.liveTV.JWTSecret)",
		"s.auth.RequireAuth",
		"s.viewer.RequireViewerAccess",
		"requireBloemLiveTVProfile",
	})
	if class != authProfileScoped {
		t.Fatalf("auth class = %q, want %q", class, authProfileScoped)
	}
	for _, trait := range []string{traitAuthenticated, traitViewerAccess, traitProfileReq, traitStreamToken} {
		if !slices.Contains(traits, trait) {
			t.Errorf("missing %q in %v", trait, traits)
		}
	}
	if slices.Contains(traits, "unclassified_middleware") {
		t.Errorf("owned delivery gates are unclassified: %v", traits)
	}
}

func TestBloemPlatformEngagementAuthorityClassification(t *testing.T) {
	class, traits := classifyAuth([]string{"adminMW.Require", "handlers.RequireBloemPlatformContext"})
	if class != authActingAdmin {
		t.Fatalf("auth class = %q, want %q", class, authActingAdmin)
	}
	if !slices.Equal(traits, []string{traitActingAdmin, traitAdminContext}) {
		t.Fatalf("platform context traits = %v", traits)
	}
}

func TestBloemSeasonalViewerAuthorityClassification(t *testing.T) {
	class, traits := classifyAuth([]string{
		"client.auth.RequireAuth", "client.tenant.ResolveNative",
		"client.rateLimit.Handler", "client.viewer.RequireViewerAccess", "apimw.RequireProfile",
	})
	if class != authProfileScoped || !slices.Equal(traits, []string{
		traitAuthenticated, traitProfileReq, traitRateLimited, traitTenantScoped, traitViewerAccess,
	}) {
		t.Fatalf("seasonal viewer authority = %q %v", class, traits)
	}
}

func TestBloemProfileCredentialRateLimitClassification(t *testing.T) {
	class, traits := classifyAuth([]string{"authMW.RequireAuth", "surfaces.ProfileCredentialLimit"})
	if class != authAuthenticated || !slices.Equal(traits, []string{traitAuthenticated, traitRateLimited}) {
		t.Fatalf("profile credential authority = %q %v", class, traits)
	}
}

func TestBloemNativeVideoCapturePreservesRouteAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		gates  []string
		class  string
		traits []string
	}{
		{name: "public", class: authPublic, traits: []string{}},
		{name: "authenticated", gates: []string{"authMW.RequireAuth"}, class: authAuthenticated, traits: []string{traitAuthenticated}},
		{name: "profile", gates: []string{"authMW.RequireAuth", "viewer.RequireViewerAccess", "apimw.RequireProfile"}, class: authProfileScoped, traits: []string{traitAuthenticated, traitProfileReq, traitViewerAccess}},
		{name: "platform_admin", gates: []string{"adminMW.Require", "handlers.RequireBloemPlatformContext"}, class: authActingAdmin, traits: []string{traitActingAdmin, traitAdminContext}},
		{name: "unknown_gate", gates: []string{"futureNativeVideoAuthority"}, class: authPublic, traits: []string{"unclassified_middleware"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stack := append([]string{"captureNativeVideoRequest", "apimw.RequestID", "apimw.NormalizeClientHeaders", "middleware.Recoverer"}, tc.gates...)
			class, traits := classifyAuth(stack)
			if class != tc.class || !slices.Equal(traits, tc.traits) {
				t.Fatalf("captured stack authority = %q %v; want %q %v", class, traits, tc.class, tc.traits)
			}
		})
	}
}
