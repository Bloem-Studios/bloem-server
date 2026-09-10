package apiv2

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeEmailVerification struct {
	calls, user        int
	profile, id, email string
	err                error
	current, dispatch  bool
}

func (*fakeEmailVerification) EmailVerificationAvailable() bool              { return true }
func (f *fakeEmailVerification) EmailDispatchAvailable(context.Context) bool { return f.dispatch }
func (f *fakeEmailVerification) QueueEmailVerification(_ context.Context, user int, profile, id, email string) (notifications.EmailVerificationReceipt, error) {
	f.calls++
	f.user = user
	f.profile = profile
	f.id = id
	f.email = email
	return notifications.EmailVerificationReceipt{ID: id, ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Current: f.current}, f.err
}

const emailVerificationBody = `{"verification_id":"00000000-0000-4000-8000-000000000001","email":"address@example.test"}`

func TestEmailVerificationTransport(t *testing.T) {
	f := &fakeEmailVerification{current: true}
	deps := pilotDeps(nil, nil)
	deps.NotificationEmailVerification = f
	h := NewHandler(deps)
	path := Prefix + "/notifications/email-preferences/address"
	r := do(t, h, http.MethodPut, path, emailVerificationBody, profileOwner())
	if r.Code != 200 || f.user != 1 || f.profile != "p-owner" || f.email != "address@example.test" || !strings.Contains(r.Body.String(), `"current":true`) || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s %+v", r.Code, r.Body.String(), f)
	}
	f.current = false
	r = do(t, h, http.MethodPut, path, emailVerificationBody, profileOwner())
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"current":false`) {
		t.Fatal("inactive receipt", r.Code)
	}
	before := f.calls
	r = do(t, h, http.MethodPut, path, emailVerificationBody, bearer(memberToken))
	if r.Code != 422 || f.calls != before {
		t.Fatal("profileless", r.Code)
	}
	for _, body := range []string{`{}`, `{"verification_id":"not-uuid","email":"a"}`, `{"verification_id":"00000000-0000-4000-8000-000000000001","email":""}`} {
		r = do(t, h, http.MethodPut, path, body, profileOwner())
		if r.Code != 422 || f.calls != before {
			t.Fatalf("validation=%d %s", r.Code, r.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{notifications.ErrEmailChildProfile, 403}, {notifications.ErrEmailInvalidAddress, 422}, {notifications.ErrEmailVerificationConflict, 409}, {notifications.ErrEmailAddressInUse, 409}, {notifications.ErrEmailNoLinkBase, 409}, {notifications.ErrEmailVerifyRateLimited, 429}, {notifications.ErrEmailVerificationUnavailable, 503}} {
		f.err = tc.err
		r = do(t, h, http.MethodPut, path, emailVerificationBody, profileOwner())
		if r.Code != tc.status {
			t.Fatalf("%v=%d", tc.err, r.Code)
		}
	}
	r = do(t, h, http.MethodGet, path+"/capabilities", "", profileOwner())
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"queue_available":true`) || !strings.Contains(r.Body.String(), `"dispatch_available":false`) {
		t.Fatal("capability conflated queue/dispatch", r.Code)
	}
	f.dispatch = true
	r = do(t, h, http.MethodGet, path+"/capabilities", "", profileOwner())
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"dispatch_available":true`) {
		t.Fatal("capability hides dispatch", r.Code, r.Body.String())
	}
	deps.NotificationEmailVerification = nil
	h = NewHandler(deps)
	r = do(t, h, http.MethodPut, path, emailVerificationBody, profileOwner())
	if r.Code != 503 {
		t.Fatal("absent", r.Code)
	}
}

func (f *fakeEmailVerification) EmailVerificationAllowed(context.Context, int, string) bool {
	return !errors.Is(f.err, notifications.ErrEmailChildProfile)
}
