package tenancy_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdevice"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Every member change that signs the account out everywhere also withdraws
// the device sign-ins it approved that no device has collected, so such a
// device cannot collect a session afterwards, also once a suspended member is
// resumed. Removing a member whose account is deleted leaves the approval no
// account to collect as.
func TestMemberRevocationsWithdrawApprovedDeviceSignInsDB(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 8)
	for name, revoke := range map[string]func(userID int) error{
		"suspend and resume": func(userID int) error {
			if _, err := service.Suspend(ctx, tenant.ID, userID); err != nil {
				return err
			}
			_, err := service.Resume(ctx, tenant.ID, userID)
			return err
		},
		"password reset": func(userID int) error {
			_, err := service.ResetPassword(ctx, tenant.ID, userID, "member-password-replaced")
			return err
		},
		"identity change": func(userID int) error {
			email := fmt.Sprintf("changed-%d@members.test", userID)
			_, err := service.Update(ctx, tenant.ID, userID, tenancy.UpdateMemberInput{Email: &email})
			return err
		},
		"removal": func(userID int) error { return service.Delete(ctx, tenant.ID, userID) },
	} {
		t.Run(name, func(t *testing.T) {
			member, _, err := service.Create(ctx, tenant.ID, "device-"+name, memberInput(fmt.Sprintf("device-member-%d", time.Now().UnixNano())))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Delete(context.Background(), tenant.ID, member.ID) })
			signIn := bloemtestdevice.Approve(t, pool, member.ID)
			if err := revoke(member.ID); err != nil {
				t.Fatal(err)
			}
			signIn.RequireWithdrawn(t)
		})
	}
}
