package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type orgDomainBaseKey struct{}

// onOrganizationDomain marks r as arriving on a registered organization
// domain, as internal/orgdomains does for the real listener.
func onOrganizationDomain(r *http.Request, base string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), orgDomainBaseKey{}, base))
}

func domainBaseFromContext(ctx context.Context) string {
	base, _ := ctx.Value(orgDomainBaseKey{}).(string)
	return base
}

// A web sign-in that starts on an organization's own domain stays there: the
// provider is sent back to that domain's callback, and the browser finishes
// on that domain's completion page, where its cookies are.
func TestOAuthWebFlowStaysOnTheOrganizationDomain(t *testing.T) {
	rig := newOAuthRig(t)
	rig.h.SetRequestBaseURL(domainBaseFromContext)

	w := httptest.NewRecorder()
	start := onOrganizationDomain(httptest.NewRequest(http.MethodPost, "https://main.domain.com/api/v2/auth/oauth/42/init?next=/me", nil), "https://main.domain.com")
	rig.serveWebStart(w, start, 42)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://idp.example/authorize" {
		t.Fatalf("start on the organization domain = %d %q, want the provider (no bounce)", w.Code, w.Header().Get("Location"))
	}
	if got := rig.client.gotInit.GetRedirectUri(); got != "https://main.domain.com/api/v2/auth/oauth/42/callback" {
		t.Fatalf("redirect_uri = %q, want the organization domain's callback", got)
	}
	var binder *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, oauthBinderCookiePrefix) {
			binder = c
		}
	}
	if binder == nil {
		t.Fatal("no binding cookie")
	}

	callback := onOrganizationDomain(httptest.NewRequest(http.MethodGet, "https://main.domain.com/api/v2/auth/oauth/42/callback?"+rig.stateQuery(), nil), "https://main.domain.com")
	callback.AddCookie(&http.Cookie{Name: binder.Name, Value: binder.Value})
	wCb := httptest.NewRecorder()
	rig.h.ServeCallback(wCb, callback, oauthV2Prefix, 42)
	if loc := wCb.Header().Get("Location"); wCb.Code != http.StatusFound || !strings.HasPrefix(loc, "https://main.domain.com/login/oauth-complete?") {
		t.Fatalf("callback = %d %q, want the organization domain's completion page", wCb.Code, loc)
	}
	if completionCookieValue(t, wCb) == "" {
		t.Fatal("no completion cookie, so the browser could not redeem the code")
	}
}

// Off a registered domain the hook answers nothing and the flow keeps the
// public URL: a start elsewhere still bounces there.
func TestOAuthOffTheOrganizationDomainKeepsThePublicURL(t *testing.T) {
	rig := newOAuthRig(t)
	rig.h.SetRequestBaseURL(domainBaseFromContext)
	w, cookie := rig.start(t, "http://192.0.2.20:8080/api/v2/auth/oauth/42/init?next=/me")
	if w.Code != http.StatusTemporaryRedirect || cookie != nil || !strings.HasPrefix(w.Header().Get("Location"), "https://silo.test/") {
		t.Fatalf("unregistered host = %d %q, want the bounce to the public URL", w.Code, w.Header().Get("Location"))
	}
	if _, cookie := rig.start(t, "https://silo.test/api/v2/auth/oauth/42/init"); cookie == nil || rig.client.gotInit.GetRedirectUri() != "https://silo.test/api/v2/auth/oauth/42/callback" {
		t.Fatalf("public origin: redirect_uri = %q", rig.client.gotInit.GetRedirectUri())
	}
}

// A provider sign-out on an organization's own domain sends the browser back
// to that domain's login page; elsewhere it keeps the public URL's.
func TestOAuthProviderLogoutReturnsToTheOrganizationDomain(t *testing.T) {
	rig := newOAuthRig(t)
	rig.h.SetRequestBaseURL(domainBaseFromContext)
	onDomain := context.WithValue(context.Background(), orgDomainBaseKey{}, "https://main.domain.com")
	if got, err := rig.h.ProviderLogoutURL(onDomain, 7); err != nil || got != "https://idp.example/logout?user="+url.QueryEscape("https://main.domain.com/login") {
		t.Fatalf("on the organization domain: %q, %v", got, err)
	}
	if got, err := rig.h.ProviderLogoutURL(context.Background(), 7); err != nil || got != "https://idp.example/logout?user="+url.QueryEscape("https://silo.test/login") {
		t.Fatalf("off a registered domain: %q, %v", got, err)
	}
	rig.h.SetHostBaseURL("")
	if got, _ := rig.h.ProviderLogoutURL(onDomain, 7); got != "" {
		t.Fatalf("without a public URL: %q, want no provider sign-out", got)
	}
}
