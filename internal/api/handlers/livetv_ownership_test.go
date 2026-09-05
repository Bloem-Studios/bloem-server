package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestLiveTVAdminOwnershipOverride(t *testing.T) {
	for _, tt := range []struct {
		name, role, profile string
		primary, found      bool
		err                 error
		want                bool
	}{
		{name: "ordinary", role: "user"},
		{name: "admin before profile", role: "admin", want: true},
		{name: "primary admin", role: "admin", profile: "primary", primary: true, found: true, want: true},
		{name: "child admin", role: "admin", profile: "child", found: true},
		{name: "unknown profile", role: "admin", profile: "unknown"},
		{name: "lookup error", role: "admin", profile: "primary", primary: true, found: true, err: errors.New("offline")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &LiveTVHandler{PrimaryProfileChecker: func(context.Context, int, string) (bool, bool, error) { return tt.primary, tt.found, tt.err }}
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Profile-Id", tt.profile)
			r = r.WithContext(apimw.SetClaims(r.Context(), &auth.Claims{UserID: 7, Role: tt.role}))
			if got := h.canManageOtherViewers(r); got != tt.want {
				t.Fatalf("override=%v want %v", got, tt.want)
			}
		})
	}
}

func TestLiveTVAdminOwnershipUsesTokenBoundProfile(t *testing.T) {
	h := &LiveTVHandler{PrimaryProfileChecker: func(_ context.Context, _ int, profile string) (bool, bool, error) {
		return profile == "primary", true, nil
	}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Profile-Id", "primary")
	r = r.WithContext(apimw.SetClaims(r.Context(), &auth.Claims{UserID: 7, Role: "admin", ProfileID: "child", AuthMethod: auth.AuthMethodDirectProfile}))
	if h.canManageOtherViewers(r) {
		t.Fatal("child token used primary profile header for ownership override")
	}
}
