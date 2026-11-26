package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// A member's password follows the server's password policy on create and
// reset, and the policy's own error comes back so the route can name the
// rule. A replay of a create that succeeded returns its member even after
// the policy has tightened.
func TestMemberPasswordsFollowThePasswordPolicyDB(t *testing.T) {
	ctx, _, store, service, _ := newMemberService(t)
	policy := auth.PasswordPolicy{MinimumLength: 12, RequireUppercase: true, RequireSpecial: true}
	service.SetPasswordPolicy(func(_ context.Context, password string) error { return policy.Check(password) })
	tenant := createMemberTenant(t, ctx, store, 3)
	name := fmt.Sprintf("policy-member-%d", time.Now().UnixNano())
	input := tenancy.CreateMemberInput{Username: name, Email: name + "@members.test"}

	for password, rule := range map[string]error{
		"Short-1":                 auth.ErrPasswordTooShort,
		strings.Repeat("Aa!", 25): auth.ErrPasswordTooLong,
		"lowercase-only-1":        auth.ErrPasswordNeedsUppercase,
		"Uppercase1234":           auth.ErrPasswordNeedsSpecial,
	} {
		input.Password = password
		if _, _, err := service.Create(ctx, tenant.ID, "refused-"+password, input); !errors.Is(err, rule) {
			t.Fatalf("create with %q = %v, want %v", password, err, rule)
		} else if _, ok := auth.PasswordRuleMessage(err); !ok {
			t.Fatalf("create with %q = %v, not the policy's refusal", password, err)
		}
	}

	input.Password = "Policy-Password-1"
	member, replay, err := service.Create(ctx, tenant.ID, "accepted", input)
	if err != nil || replay {
		t.Fatalf("create with a password the policy accepts = %v, replay %v", err, replay)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), tenant.ID, member.ID) })

	policy.MinimumLength = 40
	again, replay, err := service.Create(ctx, tenant.ID, "accepted", input)
	if err != nil || !replay || again.ID != member.ID {
		t.Fatalf("replay after the policy tightened = %+v, replay %v, %v; want member %d", again, replay, err, member.ID)
	}

	if _, err := service.ResetPassword(ctx, tenant.ID, member.ID, "Policy-Password-2"); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("reset to a password the policy refuses = %v, want ErrPasswordTooShort", err)
	}
	policy.MinimumLength = 12
	if _, err := service.ResetPassword(ctx, tenant.ID, member.ID, "Policy-Password-2"); err != nil {
		t.Fatalf("reset to a password the policy accepts = %v", err)
	}
}

// Without a policy installed, a member's password still has the policy's
// defaults: 8 characters, not bytes, and at most 72 bytes.
func TestMemberPasswordDefaultsWithoutAPolicyDB(t *testing.T) {
	ctx, _, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	name := fmt.Sprintf("default-member-%d", time.Now().UnixNano())
	for _, password := range []string{"", "seven77", "ééééééé", strings.Repeat("x", 73)} {
		input := tenancy.CreateMemberInput{Username: name, Email: name + "@members.test", Password: password}
		if _, _, err := service.Create(ctx, tenant.ID, "default-"+password, input); !errors.Is(err, tenancy.ErrInvalidMemberCommand) {
			t.Fatalf("create with %q = %v, want ErrInvalidMemberCommand", password, err)
		}
	}
}
