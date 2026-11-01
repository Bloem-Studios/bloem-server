package orgdomains

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestSignInCheck(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	defaultOrg := defaultOrganization(t, pool)
	org2Member := seedAccount(t, pool, "org2-member")
	org2 := seedOrganization(t, pool, "main", org2Member)
	defaultMember := seedAccount(t, pool, "default-member")
	joinOrganization(t, pool, defaultOrg, defaultMember)
	if _, err := store.AddDomain(ctx, org2, "main.domain.com", SourcePlatform, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddDomain(ctx, defaultOrg, "lib.example.org", SourcePlatform, false); err != nil {
		t.Fatal(err)
	}
	d := NewDirectory(store, time.Hour)
	check := d.SignInCheck(store, func(context.Context) string { return "https://server.example.net" })

	cases := []struct {
		name    string
		host    uuid.UUID // the request's domain organization; Nil for an unregistered host
		account int
		refuse  string // the URL named, or "" to allow
	}{
		{"own organization's domain", org2, org2Member, ""},
		{"default organization's domain", defaultOrg, org2Member, "https://main.domain.com"},
		{"another organization's domain", org2, defaultMember, "https://lib.example.org"},
		{"unregistered host", uuid.Nil, org2Member, ""},
	}
	for _, tc := range cases {
		reqCtx := ctx
		if tc.host != uuid.Nil {
			reqCtx = WithRequestOrganization(ctx, tc.host)
		}
		err := check(reqCtx, tc.account)
		var domainErr *auth.OrganizationDomainError
		switch {
		case tc.refuse == "" && err != nil:
			t.Errorf("%s: %v, want allowed", tc.name, err)
		case tc.refuse != "" && (!errors.As(err, &domainErr) || domainErr.SignInURL != tc.refuse):
			t.Errorf("%s: %v, want refused with %s", tc.name, err, tc.refuse)
		}
	}

	// Without its own primary domain the default organization's accounts are
	// sent to the server's public URL.
	if err := store.RemoveDomain(ctx, defaultOrg, "lib.example.org"); err != nil {
		t.Fatal(err)
	}
	d.Invalidate()
	var domainErr *auth.OrganizationDomainError
	if err := check(WithRequestOrganization(ctx, org2), defaultMember); !errors.As(err, &domainErr) || domainErr.SignInURL != "https://server.example.net" {
		t.Fatalf("no primary domain: %v, want the server public URL", err)
	}
}

// An organization without a domain signs in at the server's public URL. When
// that URL's host is registered to the default organization, its accounts are
// still let in there, never sent back to the address that refused them.
func TestSignInCheckLetsAnOrganizationWithoutADomainUseTheServerAddress(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	defaultOrg := defaultOrganization(t, pool)
	homeless := seedAccount(t, pool, "homeless-member")
	seedOrganization(t, pool, "homeless", homeless)
	org2Member := seedAccount(t, pool, "org2-member")
	org2 := seedOrganization(t, pool, "main", org2Member)
	if _, err := store.AddDomain(ctx, defaultOrg, "lib.example.org", SourcePlatform, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddDomain(ctx, org2, "main.domain.com", SourcePlatform, false); err != nil {
		t.Fatal(err)
	}
	d := NewDirectory(store, time.Hour)
	check := d.SignInCheck(store, func(context.Context) string { return "https://Lib.Example.org/" })
	on := func(org uuid.UUID, host string) context.Context {
		return WithRequestDomain(WithRequestOrganization(ctx, org), host)
	}

	if err := check(on(defaultOrg, "lib.example.org"), homeless); err != nil {
		t.Fatalf("server address: %v, want allowed", err)
	}
	var domainErr *auth.OrganizationDomainError
	if err := check(on(org2, "main.domain.com"), homeless); !errors.As(err, &domainErr) || domainErr.SignInURL != "https://Lib.Example.org/" {
		t.Fatalf("another organization's domain: %v, want refused with the server address", err)
	}
}
